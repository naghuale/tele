package telegram

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// AuthProvider supplies user input as authorization progresses.
//
// Returning any error from a provider aborts RunAuth. tui.ErrAuthCanceled
// from the TUI prompt is a typical error here.
//
// A provider must not return an empty phone number, code, or password
// with a nil error: RunAuth rejects that with ErrInvalidAuthParameters.
type AuthProvider interface {
	ProvidePhoneNumber(ctx context.Context) (string, error)
	ProvideCode(ctx context.Context) (string, error)
	ProvidePassword(ctx context.Context) (string, error)
}

// AuthSender is the narrow runtime dependency used by the coordinator.
//
// It is satisfied by *Runtime. It exists as an interface so tests can
// drive the coordinator without a native TDLib handle.
type AuthSender interface {
	Send(clientID int, request RawMessage) error
}

// AuthResult describes the outcome of RunAuth.
type AuthResult struct {
	State   AuthState
	Elapsed time.Duration
}

var (
	// ErrNilRuntime is returned when a nil runtime/sender is passed.
	ErrNilRuntime = errors.New("auth: nil runtime")
	// ErrNilProvider is returned when a nil provider is passed.
	ErrNilProvider = errors.New("auth: nil provider")
	// ErrNilClient is returned when a nil client is passed.
	ErrNilClient = errors.New("auth: nil client")
	// ErrUpdateChannelGone is returned when the update channel closes
	// before authorization completes.
	ErrUpdateChannelGone = errors.New("auth: update channel closed")
	// ErrAuthorizationClosed is returned when TDLib reaches a terminal
	// state (Closed, Closing, LoggingOut) before Ready.
	ErrAuthorizationClosed = errors.New("auth: authorization closed before ready")
)

// RunAuth creates a new logical TDLib client and drives it through the
// authorization state machine until Ready.
//
// The returned AuthResult describes the terminal state. A non-nil error
// means authorization did not complete.
//
// Input validation (nil runtime, nil provider, already-cancelled
// context) happens before rt.NewClient(), so a rejected call does not
// create a TDLib client ID.
func RunAuth(
	ctx context.Context,
	rt *Runtime,
	params TdlibParameters,
	provider AuthProvider,
) (AuthResult, error) {
	if rt == nil {
		return AuthResult{}, ErrNilRuntime
	}
	if provider == nil {
		return AuthResult{}, ErrNilProvider
	}
	if err := ctx.Err(); err != nil {
		return AuthResult{}, err
	}

	// Safe logging must be installed before any credential-bearing
	// request, including the setTdlibParameters built by the
	// coordinator. Startup fails closed when the verbosity cannot be
	// lowered.
	if err := rt.ConfigureSafeLogging(); err != nil {
		return AuthResult{}, err
	}

	client, err := rt.NewClient()
	if err != nil {
		return AuthResult{}, fmt.Errorf("auth: create client: %w", err)
	}

	return RunAuthWithClient(ctx, rt, client, params, provider)
}

// RunAuthWithClient drives an existing logical client through the
// authorization state machine until Ready.
//
// The function is the single owner of client.Updates() and
// client.Errors() for the duration of the call. Callers must not read
// from either channel concurrently.
//
// When client.Errors() closes, the error branch is disabled and the
// coordinator continues reading client.Updates(). This prevents a
// spurious ErrErrorChannelGone from racing against a buffered Ready
// update during shutdown.
//
// Error semantics:
//
//   - Native runtime errors from client.Errors() abort with the
//     underlying error wrapped.
//   - Parse errors from ParseAuthUpdate (malformed JSON) abort with the
//     underlying error.
//   - ErrUnsupportedAuthState from ParseAuthUpdate (unknown TDLib
//     authorization_state type) aborts with that sentinel wrapped.
//   - ErrUnsupportedAuthState from AuthSession.Step (known but not
//     implemented state such as email or registration) aborts with that
//     sentinel wrapped.
//   - Terminal states (Closed, Closing, LoggingOut) before Ready abort
//     with ErrAuthorizationClosed, preserving the terminal state in the
//     AuthResult.
//   - Non-authorization updates (state == AuthStateUnknown, err == nil)
//     are skipped.
//   - Empty provider input is rejected with ErrInvalidAuthParameters.
//   - Context cancellation aborts with ctx.Err().
//   - A closed update channel aborts with ErrUpdateChannelGone.
func RunAuthWithClient(
	ctx context.Context,
	sender AuthSender,
	client *Client,
	params TdlibParameters,
	provider AuthProvider,
) (AuthResult, error) {
	return RunAuthWithClientDiagnostics(
		ctx,
		sender,
		client,
		params,
		provider,
		DiscardAuthDiagnostics(),
	)
}

// RunAuthWithClientDiagnostics is RunAuthWithClient with metadata-only
// authorization diagnostics.
//
// The diagnostics receive closed labels only: an observed state, the kind
// of request the session produced and the local outcome of handing it to
// the native bridge. No payload, credential or provider answer is ever
// passed in, so enabling them cannot widen what the process writes.
func RunAuthWithClientDiagnostics(
	ctx context.Context,
	sender AuthSender,
	client *Client,
	params TdlibParameters,
	provider AuthProvider,
	diagnostics AuthDiagnostics,
) (AuthResult, error) {
	if sender == nil {
		return AuthResult{}, ErrNilRuntime
	}
	if client == nil {
		return AuthResult{}, ErrNilClient
	}
	if provider == nil {
		return AuthResult{}, ErrNilProvider
	}

	if diagnostics == nil {
		diagnostics = DiscardAuthDiagnostics()
	}

	session := NewAuthSession(params)
	start := time.Now()
	updates := client.Updates()
	runtimeErrors := client.Errors()

	// pending correlates an answer with the request that caused it.
	//
	// A request carries a generated identifier in its @extra field, and
	// TDLib copies it into the answer. Without that, an error arriving
	// after a phone request could only be guessed onto it, and a guess
	// is exactly what this trace exists to avoid.
	var diagnosticSequence AuthDiagnosticRequestID
	pending := make(map[QueryID]AuthDiagnosticRequest)

	for {
		select {
		case <-ctx.Done():
			return AuthResult{
				State:   session.State(),
				Elapsed: time.Since(start),
			}, ctx.Err()

		case runtimeErr, ok := <-runtimeErrors:
			if !ok {
				// Runtime error channel closed; disable this branch
				// and continue consuming updates. Receive from a nil
				// channel blocks forever, so this case no longer
				// participates in the select.
				runtimeErrors = nil
				continue
			}
			if runtimeErr == nil {
				continue
			}
			return AuthResult{
				State:   session.State(),
				Elapsed: time.Since(start),
			}, fmt.Errorf("auth: Telegram runtime: %w", runtimeErr)

		case update, ok := <-updates:
			if !ok {
				return AuthResult{
					State:   session.State(),
					Elapsed: time.Since(start),
				}, ErrUpdateChannelGone
			}

			// An answer that carries one of our identifiers belongs to a
			// request this run sent, so it is reported against that
			// request. Anything else is reported as unattributed, and is
			// never guessed onto the most recent request.
			if queryID, matched, idErr := responseQueryID(
				update.Raw,
			); idErr == nil && matched {
				request, known := pending[queryID]
				id := diagnosticIDFor(queryID)

				// Classification belongs to a request. An answer with
				// no owner gets no name, because there is nothing the
				// name would be about.
				errorName := AuthErrorNameUnset

				if !known {
					// The identifier is in our namespace but no
					// request is pending under it, so the answer has
					// no owner. Its own number is not echoed as an
					// owner: this run never sent it.
					request = AuthDiagnosticRequestOther
					id = 0
				} else {
					errorName = authErrorNameOf(update.Raw)
				}
				delete(pending, queryID)

				_, errorCode := classifyAuthDiagnosticEnvelope(
					update.Raw,
				)

				diagnostics.Result(
					id,
					request,
					answerResult(update.Raw, errorCode),
					errorCode,
					errorName,
				)
			} else if envelope, errorCode := classifyAuthDiagnosticEnvelope(
				update.Raw,
			); authDiagnosticEnvelopeReportable(envelope) {
				diagnostics.Envelope(envelope, errorCode)
			}

			state, err := ParseAuthUpdate(update.Raw)
			if err != nil {
				return AuthResult{
					State:   session.State(),
					Elapsed: time.Since(start),
				}, fmt.Errorf("auth: parse update: %w", err)
			}

			// Only a state the product acts on is worth a line; the wire
			// carries many objects that are not authorization states.
			if label, ok := authDiagnosticStateKnown(state); ok {
				diagnostics.State(label)
			}

			if state == AuthStateUnknown {
				// Non-authorization update.
				continue
			}

			// Terminal states must not be silently skipped: reaching
			// one before Ready means authorization cannot continue.
			switch state {
			case AuthStateClosed, AuthStateClosing, AuthStateLoggingOut:
				if _, stepErr := session.Step(state, AuthInput{}); stepErr != nil {
					return AuthResult{
						State:   state,
						Elapsed: time.Since(start),
					}, stepErr
				}
				return AuthResult{
					State:   state,
					Elapsed: time.Since(start),
				}, fmt.Errorf("%w: %s", ErrAuthorizationClosed, state)
			}

			var input AuthInput
			switch state {
			case AuthStateWaitPhoneNumber:
				phone, err := provider.ProvidePhoneNumber(ctx)
				if err != nil {
					return AuthResult{
						State:   state,
						Elapsed: time.Since(start),
					}, err
				}
				if phone == "" {
					return AuthResult{
						State:   state,
						Elapsed: time.Since(start),
					}, fmt.Errorf("%w: empty phone number", ErrInvalidAuthParameters)
				}
				input.PhoneNumber = phone

			case AuthStateWaitCode:
				code, err := provider.ProvideCode(ctx)
				if err != nil {
					return AuthResult{
						State:   state,
						Elapsed: time.Since(start),
					}, err
				}
				if code == "" {
					return AuthResult{
						State:   state,
						Elapsed: time.Since(start),
					}, fmt.Errorf("%w: empty authentication code", ErrInvalidAuthParameters)
				}
				input.Code = code

			case AuthStateWaitPassword:
				password, err := provider.ProvidePassword(ctx)
				if err != nil {
					return AuthResult{
						State:   state,
						Elapsed: time.Since(start),
					}, err
				}
				if password == "" {
					return AuthResult{
						State:   state,
						Elapsed: time.Since(start),
					}, fmt.Errorf("%w: empty password", ErrInvalidAuthParameters)
				}
				input.Password = password
			}

			request, err := session.Step(state, input)
			if err != nil {
				return AuthResult{
					State:   state,
					Elapsed: time.Since(start),
				}, err
			}
			if request == nil {
				diagnostics.Result(
					0,
					diagnosticRequestForState(state),
					noRequestResult(state),
					0,
					AuthErrorNameUnset,
				)
			} else {
				requestType := authDiagnosticRequest(request)

				// The request is tagged so its answer can be matched.
				// A tagging failure is not fatal: the trace degrades to an
				// unattributed answer instead of losing the handshake.
				diagnosticSequence++
				id := diagnosticSequence

				tagged := request
				queryID := QueryID(fmt.Sprintf(
					"%s%d:%d",
					queryNamespace,
					client.ID(),
					id,
				))

				withID, tagErr := withQueryID(request, queryID)
				if tagErr == nil {
					tagged = withID
					pending[queryID] = requestType
				} else {
					id = 0
				}

				diagnostics.Request(id, requestType)

				if err := sender.Send(client.ID(), tagged); err != nil {
					delete(pending, queryID)

					diagnostics.Result(
						id,
						requestType,
						AuthDiagnosticResultError,
						0,
						AuthErrorNameUnset,
					)

					return AuthResult{
						State:   state,
						Elapsed: time.Since(start),
					}, fmt.Errorf("auth: send request: %w", err)
				}

				diagnostics.Result(
					id,
					requestType,
					AuthDiagnosticResultSubmitted,
					0,
					AuthErrorNameUnset,
				)
			}
			if session.Done() {
				return AuthResult{
					State:   AuthStateReady,
					Elapsed: time.Since(start),
				}, nil
			}
		}
	}
}

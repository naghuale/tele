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
	if sender == nil {
		return AuthResult{}, ErrNilRuntime
	}
	if client == nil {
		return AuthResult{}, ErrNilClient
	}
	if provider == nil {
		return AuthResult{}, ErrNilProvider
	}

	session := NewAuthSession(params)
	start := time.Now()
	updates := client.Updates()
	runtimeErrors := client.Errors()

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

			state, err := ParseAuthUpdate(update.Raw)
			if err != nil {
				return AuthResult{
					State:   session.State(),
					Elapsed: time.Since(start),
				}, fmt.Errorf("auth: parse update: %w", err)
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
			if request != nil {
				if err := sender.Send(client.ID(), request); err != nil {
					return AuthResult{
						State:   state,
						Elapsed: time.Since(start),
					}, fmt.Errorf("auth: send request: %w", err)
				}
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

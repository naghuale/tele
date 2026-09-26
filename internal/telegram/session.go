package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"
)

// RuntimeCloser is the narrow runtime dependency used by
// AuthorizedSession.Close.
type RuntimeCloser interface {
	Close(ctx context.Context) error
}

var (
	ErrSessionNilClient     = errors.New("session: nil client")
	ErrSessionCloseTimeout  = errors.New("session: close timeout")
	ErrSessionUpdatesClosed = errors.New(
		"session: update channel closed before authorizationStateClosed",
	)
	ErrSessionNilSender = errors.New("session: nil sender")
)

// AuthorizedSession owns one logical TDLib client from successful
// authorization until close.
type AuthorizedSession struct {
	sender AuthSender
	closer RuntimeCloser
	client *Client

	shutdownTimeout time.Duration

	updates chan Update
	errors  chan error

	ctx    context.Context
	cancel context.CancelFunc

	closed   chan struct{}
	pumpDone chan struct{}

	stateMu   sync.RWMutex
	authState AuthState
	lastErr   error

	queryMu        sync.Mutex
	pendingQueries map[QueryID]*pendingQuery
	queryErr       error

	closeOnce  sync.Once
	closeDone  chan struct{}
	closeErrMu sync.RWMutex
	closeErr   error
}

// Authorize runs the authorization state machine and returns the
// session once TDLib reaches authorizationStateReady.
//
// Authorize sends getAuthorizationState immediately after NewClient.
//
// In the verified TDLib 1.8.67 integration flow, the logical client
// produced no initial authorization object before its first request.
// getAuthorizationState activates that flow and returns the current
// authorization state. It is an offline method permitted before
// initialization.
func Authorize(
	ctx context.Context,
	rt *Runtime,
	params TdlibParameters,
	provider AuthProvider,
) (*AuthorizedSession, error) {
	if rt == nil {
		return nil, ErrNilRuntime
	}
	if provider == nil {
		return nil, ErrNilProvider
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Safe logging must be installed before any credential-bearing
	// request. TDLib defaults to verbosity level 5 and dumps every
	// incoming request, including the api_hash inside
	// setTdlibParameters. Startup fails closed when the verbosity
	// cannot be lowered.
	if err := rt.ConfigureSafeLogging(); err != nil {
		return nil, err
	}

	client, err := rt.NewClient()
	if err != nil {
		return nil, fmt.Errorf("session: create client: %w", err)
	}

	if err := activateAuthorizationClient(rt, client); err != nil {
		return nil, closeFailedAuthorization(
			rt,
			client,
			fmt.Errorf("activate authorization client: %w", err),
		)
	}

	// The diagnostics write to stderr and stay completely silent unless
	// TELECLI_AUTH_TRACE is set, so a normal session is unchanged. They
	// are attached here because this is the only production path that
	// runs the authorization handshake.
	result, err := RunAuthWithClientDiagnostics(
		ctx,
		rt,
		client,
		params,
		provider,
		NewEnvironmentAuthDiagnostics(os.Stderr),
	)
	if err != nil {
		return nil, closeFailedAuthorization(rt, client, err)
	}
	if result.State != AuthStateReady {
		return nil, closeFailedAuthorization(
			rt,
			client,
			fmt.Errorf("session: unexpected terminal state %s", result.State),
		)
	}

	return newAuthorizedSession(
		rt,
		rt,
		client,
		rt.ShutdownTimeout(),
	), nil
}

type getAuthorizationStateRequest struct {
	Type string `json:"@type"`
}

// activateAuthorizationClient sends the first request for a newly
// created logical TDLib client.
//
// The verified TDLib 1.8.67 runtime produced no initial authorization
// object before this request. getAuthorizationState activates the
// client's update flow and returns the current authorization state as
// a direct response object.
func activateAuthorizationClient(
	sender AuthSender,
	client *Client,
) error {
	if sender == nil {
		return ErrSessionNilSender
	}
	if client == nil {
		return ErrSessionNilClient
	}

	request, err := json.Marshal(getAuthorizationStateRequest{
		Type: "getAuthorizationState",
	})
	if err != nil {
		return fmt.Errorf("build getAuthorizationState: %w", err)
	}

	if err := sender.Send(client.ID(), request); err != nil {
		return fmt.Errorf("send getAuthorizationState: %w", err)
	}
	return nil
}

// closeFailedAuthorization closes a logical client created before
// authorization succeeded.
func closeFailedAuthorization(
	rt *Runtime,
	client *Client,
	authErr error,
) error {
	session := newAuthorizedSession(
		rt,
		rt,
		client,
		rt.ShutdownTimeout(),
	)

	closeErr := session.Close(context.Background())

	return errors.Join(
		fmt.Errorf("session: authorize: %w", authErr),
		closeErr,
	)
}

// ShutdownTimeout returns the configured shutdown timeout, or a
// sensible default when the runtime is zero-valued.
func (r *Runtime) ShutdownTimeout() time.Duration {
	if r == nil || r.cfg.ShutdownTimeout <= 0 {
		return 5 * time.Second
	}
	return r.cfg.ShutdownTimeout
}

// newAuthorizedSession constructs a session and starts its pump.
func newAuthorizedSession(
	sender AuthSender,
	closer RuntimeCloser,
	client *Client,
	shutdownTimeout time.Duration,
) *AuthorizedSession {
	if shutdownTimeout <= 0 {
		shutdownTimeout = 5 * time.Second
	}

	ctx, cancel := context.WithCancel(context.Background())

	s := &AuthorizedSession{
		sender:          sender,
		closer:          closer,
		client:          client,
		shutdownTimeout: shutdownTimeout,
		updates:         make(chan Update, 256),
		errors:          make(chan error, 16),
		ctx:             ctx,
		cancel:          cancel,
		closed:          make(chan struct{}),
		pumpDone:        make(chan struct{}),
		pendingQueries:  make(map[QueryID]*pendingQuery),
		closeDone:       make(chan struct{}),
	}

	go s.run()
	return s
}

// ClientID returns the logical TDLib client ID.
func (s *AuthorizedSession) ClientID() int {
	if s == nil || s.client == nil {
		return 0
	}
	return s.client.ID()
}

// Updates returns the best-effort application-facing update channel.
func (s *AuthorizedSession) Updates() <-chan Update {
	if s == nil {
		return nil
	}
	return s.updates
}

// Errors returns the best-effort application-facing runtime error
// channel.
func (s *AuthorizedSession) Errors() <-chan error {
	if s == nil {
		return nil
	}
	return s.errors
}

// Close sends the TDLib close request, waits for
// authorizationStateClosed, stops the Runtime, and unloads the native
// library.
func (s *AuthorizedSession) Close(ctx context.Context) error {
	if s == nil || s.client == nil {
		return ErrSessionNilClient
	}

	s.closeOnce.Do(func() {
		go s.closeSequence()
	})

	select {
	case <-s.closeDone:
		s.closeErrMu.RLock()
		defer s.closeErrMu.RUnlock()
		return s.closeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

// recordError stores and forwards a runtime error, non-blockingly.
func (s *AuthorizedSession) recordError(err error) {
	if err == nil {
		return
	}
	s.stateMu.Lock()
	s.lastErr = err
	s.stateMu.Unlock()

	select {
	case s.errors <- err:
	default:
	}
}

// run is the session pump. It is the single consumer of
// client.Updates() and client.Errors() between Authorize and Close.
func (s *AuthorizedSession) run() {
	// Defer order is LIFO:
	//   1. close(errors)
	//   2. close(updates)
	//   3. failPendingQueries
	//   4. close(pumpDone)
	defer close(s.pumpDone)
	defer s.failPendingQueries(ErrQuerySessionClosed)
	defer close(s.updates)
	defer close(s.errors)

	clientUpdates := s.client.Updates()
	clientErrors := s.client.Errors()
	closedSignaled := false

	for {
		select {
		case <-s.ctx.Done():
			return

		case err, ok := <-clientErrors:
			if !ok {
				clientErrors = nil
				continue
			}
			s.recordError(err)

		case u, ok := <-clientUpdates:
			if !ok {
				return
			}

			routed, routeErr := s.routeQueryResponse(u.Raw)
			if routeErr != nil {
				s.recordError(fmt.Errorf(
					"session: route query response: %w",
					routeErr,
				))
				continue
			}
			if routed {
				continue
			}

			state, err := ParseAuthUpdate(u.Raw)
			if err != nil {
				s.recordError(fmt.Errorf(
					"session: parse update: %w",
					err,
				))
				continue
			}
			if state == AuthStateUnknown {
				select {
				case s.updates <- u:
				default:
				}
				continue
			}

			s.stateMu.Lock()
			s.authState = state
			s.stateMu.Unlock()

			if state == AuthStateClosed && !closedSignaled {
				closedSignaled = true
				close(s.closed)
			}
		}
	}
}

func (s *AuthorizedSession) closeSequence() {
	defer close(s.closeDone)

	err := s.doClose()

	s.closeErrMu.Lock()
	s.closeErr = err
	s.closeErrMu.Unlock()
}

func (s *AuthorizedSession) doClose() error {
	if s.sender == nil {
		s.cancel()
		<-s.pumpDone

		return errors.Join(
			ErrSessionNilSender,
			s.stopRuntime(),
		)
	}

	closeReq, buildErr := buildCloseRequest()
	if buildErr != nil {
		s.cancel()
		<-s.pumpDone
		return errors.Join(
			fmt.Errorf("session: build close request: %w", buildErr),
			s.stopRuntime(),
		)
	}

	sendErr := s.sender.Send(s.client.ID(), closeReq)

	var waitErr error
	if sendErr == nil {
		waitErr = s.waitClosed()
	}

	// Stop the pump, then stop the runtime.
	s.cancel()
	<-s.pumpDone

	return errors.Join(
		wrapIf("session: send close", sendErr),
		wrapIf("session: wait for closed", waitErr),
		s.stopRuntime(),
	)
}

// waitClosed waits for authorizationStateClosed or a terminal pump
// condition, using the session's internal shutdown timeout.
func (s *AuthorizedSession) waitClosed() error {
	timer := time.NewTimer(s.shutdownTimeout)
	defer timer.Stop()

	select {
	case <-s.closed:
		return nil

	case <-s.pumpDone:
		select {
		case <-s.closed:
			return nil
		default:
		}
		return s.terminalError(ErrSessionUpdatesClosed)

	case <-timer.C:
		return s.terminalError(ErrSessionCloseTimeout)
	}
}

// terminalError joins a terminal sentinel with the last runtime error
// observed by the pump, if any.
func (s *AuthorizedSession) terminalError(sentinel error) error {
	s.stateMu.RLock()
	lastErr := s.lastErr
	s.stateMu.RUnlock()
	return errors.Join(sentinel, wrapIf("session: runtime receive", lastErr))
}

// stopRuntime closes the runtime on an independent context.
func (s *AuthorizedSession) stopRuntime() error {
	if s.closer == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.shutdownTimeout)
	defer cancel()

	if err := s.closer.Close(ctx); err != nil {
		return fmt.Errorf("session: runtime close: %w", err)
	}
	return nil
}

type closeRequest struct {
	Type string `json:"@type"`
}

func buildCloseRequest() ([]byte, error) {
	return json.Marshal(closeRequest{Type: "close"})
}

func wrapIf(prefix string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", prefix, err)
}

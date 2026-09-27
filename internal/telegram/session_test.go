package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"telecli/internal/telemetry/recorder"
)

type fakeCloser struct {
	mu           sync.Mutex
	calls        int
	err          error
	lastCtxAlive bool
	lastCtxErr   error
}

func (f *fakeCloser) Close(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls++
	f.lastCtxAlive = ctx.Err() == nil
	f.lastCtxErr = ctx.Err()

	return f.err
}

func (f *fakeCloser) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.calls
}

func (f *fakeCloser) ctxAlive() bool {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.lastCtxAlive
}

// newSessionWithFakes constructs an AuthorizedSession backed by fake
// transport components and starts its pump.
//
// The cleanup guarantees the pump exits even if the test does not call
// Close.
func newSessionWithFakes(
	t *testing.T,
) (*AuthorizedSession, *fakeSender, *fakeCloser, *Client) {
	t.Helper()

	sender := &fakeSender{}
	closer := &fakeCloser{}
	client := newTestClient(t)

	session := newAuthorizedSession(
		sender,
		closer,
		client,
		100*time.Millisecond,
		nil,
	)

	t.Cleanup(func() {
		session.cancel()

		select {
		case <-session.pumpDone:
		case <-time.After(time.Second):
			t.Error(
				"session pump did not exit during cleanup",
			)
		}
	})

	return session, sender, closer, client
}

func waitForCloseDone(
	t *testing.T,
	session *AuthorizedSession,
) {
	t.Helper()

	select {
	case <-session.closeDone:
	case <-time.After(2 * time.Second):
		t.Fatal(
			"close sequence did not complete",
		)
	}
}

// ---- Test-native used for failed-authorization cleanup ----

type sessionReceiveResult struct {
	raw RawMessage
	err error
}

type sessionNative struct {
	nextClientID atomic.Int64

	receive chan sessionReceiveResult

	mu       sync.Mutex
	requests []RawMessage

	closeCalls atomic.Int64
}

func newSessionNative() *sessionNative {
	native := &sessionNative{
		receive: make(
			chan sessionReceiveResult,
			32,
		),
	}
	native.nextClientID.Store(0)

	return native
}

func (n *sessionNative) CreateClientID() (int, error) {
	return int(n.nextClientID.Add(1)), nil
}

func (n *sessionNative) Send(
	clientID int,
	request []byte,
) error {
	n.mu.Lock()
	n.requests = append(
		n.requests,
		append(RawMessage(nil), request...),
	)
	n.mu.Unlock()

	var envelope struct {
		Type string `json:"@type"`
	}

	if err := json.Unmarshal(
		request,
		&envelope,
	); err != nil {
		return err
	}

	switch envelope.Type {
	case "getAuthorizationState":
		// A freshly created logical client is dormant until it
		// receives its first request. In the verified TDLib 1.8.67
		// flow, getAuthorizationState activates the client and
		// returns the current authorization state as a direct
		// response object.
		raw, err := json.Marshal(
			map[string]any{
				"@client_id": clientID,
				"@type":      "authorizationStateWaitPhoneNumber",
			},
		)
		if err != nil {
			return err
		}

		n.receive <- sessionReceiveResult{
			raw: raw,
		}

	case "close":
		raw, err := json.Marshal(
			map[string]any{
				"@client_id": clientID,
				"@type":      "updateAuthorizationState",
				"authorization_state": map[string]any{
					"@type": "authorizationStateClosed",
				},
			},
		)
		if err != nil {
			return err
		}

		n.receive <- sessionReceiveResult{
			raw: raw,
		}
	}

	return nil
}

func (n *sessionNative) Receive(
	timeout time.Duration,
) ([]byte, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case result := <-n.receive:
		return result.raw, result.err
	case <-timer.C:
		return nil, nil
	}
}

func (n *sessionNative) Execute(
	[]byte,
) ([]byte, error) {
	// TDLib answers every synchronous request with a JSON object.
	// Production startup issues setLogVerbosityLevel through this path
	// and fails closed on an empty response, so the fake must behave
	// like the real native layer.
	return []byte(`{"@type":"ok"}`), nil
}

func (n *sessionNative) Close() error {
	n.closeCalls.Add(1)
	return nil
}

func (n *sessionNative) sentTypes(
	t *testing.T,
) []string {
	t.Helper()

	n.mu.Lock()
	defer n.mu.Unlock()

	types := make(
		[]string,
		0,
		len(n.requests),
	)

	for _, raw := range n.requests {
		var envelope struct {
			Type string `json:"@type"`
		}

		if err := json.Unmarshal(
			raw,
			&envelope,
		); err != nil {
			t.Fatalf(
				"decode request: %v",
				err,
			)
		}

		types = append(
			types,
			envelope.Type,
		)
	}

	return types
}

// ---- Authorize input validation ----

func TestAuthorizeRejectsNilRuntime(t *testing.T) {
	_, err := Authorize(
		context.Background(),
		nil,
		validAuthParams(),
		&fakeProvider{},
	)

	if !errors.Is(err, ErrNilRuntime) {
		t.Fatalf(
			"error = %v, want ErrNilRuntime",
			err,
		)
	}
}

func TestAuthorizeRejectsNilProvider(t *testing.T) {
	ctx, cancel := context.WithCancel(
		context.Background(),
	)
	cancel()

	runtime := &Runtime{}

	_, err := Authorize(
		ctx,
		runtime,
		validAuthParams(),
		nil,
	)

	if !errors.Is(err, ErrNilProvider) {
		t.Fatalf(
			"error = %v, want ErrNilProvider",
			err,
		)
	}
}

func TestAuthorizeRejectsCanceledContextBeforeClientCreation(
	t *testing.T,
) {
	ctx, cancel := context.WithCancel(
		context.Background(),
	)
	cancel()

	runtime := &Runtime{}

	_, err := Authorize(
		ctx,
		runtime,
		validAuthParams(),
		&fakeProvider{},
	)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf(
			"error = %v, want context.Canceled",
			err,
		)
	}
}

// ---- activateAuthorizationClient ----

func TestActivateAuthorizationClientSendsRequest(t *testing.T) {
	sender := &fakeSender{}
	client := newTestClient(t)

	if err := activateAuthorizationClient(
		sender,
		client,
	); err != nil {
		t.Fatalf(
			"activateAuthorizationClient() error = %v",
			err,
		)
	}

	sender.mu.Lock()
	requests := append(
		[]RawMessage(nil),
		sender.requests...,
	)
	sender.mu.Unlock()

	if len(requests) != 1 {
		t.Fatalf(
			"request count = %d, want 1",
			len(requests),
		)
	}

	var envelope struct {
		Type string `json:"@type"`
	}

	if err := json.Unmarshal(
		requests[0],
		&envelope,
	); err != nil {
		t.Fatalf(
			"decode request: %v",
			err,
		)
	}

	if envelope.Type != "getAuthorizationState" {
		t.Fatalf(
			"@type = %q, want getAuthorizationState",
			envelope.Type,
		)
	}
}

func TestActivateAuthorizationClientRejectsNilSender(
	t *testing.T,
) {
	client := newTestClient(t)

	err := activateAuthorizationClient(nil, client)

	if !errors.Is(err, ErrSessionNilSender) {
		t.Fatalf(
			"error = %v, want ErrSessionNilSender",
			err,
		)
	}
}

func TestActivateAuthorizationClientRejectsNilClient(
	t *testing.T,
) {
	sender := &fakeSender{}

	err := activateAuthorizationClient(sender, nil)

	if !errors.Is(err, ErrSessionNilClient) {
		t.Fatalf(
			"error = %v, want ErrSessionNilClient",
			err,
		)
	}

	if sender.count() != 0 {
		t.Fatalf(
			"sender calls = %d, want 0",
			sender.count(),
		)
	}
}

func TestActivateAuthorizationClientPropagatesSendError(
	t *testing.T,
) {
	sendErr := errors.New("send failed")
	sender := &fakeSender{err: sendErr}
	client := newTestClient(t)

	err := activateAuthorizationClient(sender, client)

	if !errors.Is(err, sendErr) {
		t.Fatalf(
			"error = %v, want %v",
			err,
			sendErr,
		)
	}
}

// ---- Authorize failure cleanup ----

// Failed authorization must close the logical TDLib client and stop the
// runtime. The original provider error must remain discoverable through
// errors.Is.
//
// The sessionNative now activates the client on getAuthorizationState
// and emits authorizationStateClosed on close. The test no longer
// pre-seeds a phone-number update: the activation request itself
// produces authorizationStateWaitPhoneNumber.
func TestAuthorizeFailureClosesCreatedClient(t *testing.T) {
	native := newSessionNative()

	cfg := DefaultConfig()
	cfg.ReceiveTimeout = time.Millisecond
	cfg.ShutdownTimeout = 500 * time.Millisecond

	runtime, err := NewRuntime(
		cfg,
		native,
		recorder.NewNoop(),
	)
	if err != nil {
		t.Fatalf(
			"NewRuntime() error = %v",
			err,
		)
	}

	if err := runtime.Start(
		context.Background(),
	); err != nil {
		t.Fatalf(
			"Runtime.Start() error = %v",
			err,
		)
	}

	providerErr := errors.New("provider failed")

	_, err = Authorize(
		context.Background(),
		runtime,
		validAuthParams(),
		&fakeProvider{
			err: providerErr,
		},
	)

	if !errors.Is(err, providerErr) {
		t.Fatalf(
			"error = %v, want provider error",
			err,
		)
	}

	requestTypes := native.sentTypes(t)

	wantRequestTypes := []string{
		"getAuthorizationState",
		"close",
	}

	if len(requestTypes) != len(wantRequestTypes) {
		t.Fatalf(
			"request types = %v, want %v",
			requestTypes,
			wantRequestTypes,
		)
	}

	for index := range wantRequestTypes {
		if requestTypes[index] != wantRequestTypes[index] {
			t.Fatalf(
				"request types = %v, want %v",
				requestTypes,
				wantRequestTypes,
			)
		}
	}

	if runtime.State() != LifecycleClosed {
		t.Fatalf(
			"runtime state = %s, want %s",
			runtime.State(),
			LifecycleClosed,
		)
	}

	if native.closeCalls.Load() != 1 {
		t.Fatalf(
			"native Close calls = %d, want 1",
			native.closeCalls.Load(),
		)
	}
}

// ---- Close success paths ----

func TestSessionCloseSendsCloseRequestAndStopsRuntime(
	t *testing.T,
) {
	session, sender, closer, client :=
		newSessionWithFakes(t)

	feedAuth(
		t,
		client,
		"authorizationStateClosed",
	)

	if err := session.Close(
		context.Background(),
	); err != nil {
		t.Fatalf(
			"Close() error = %v",
			err,
		)
	}

	sender.mu.Lock()
	requests := append(
		[]RawMessage(nil),
		sender.requests...,
	)
	sender.mu.Unlock()

	if len(requests) != 1 {
		t.Fatalf(
			"sent %d requests, want 1",
			len(requests),
		)
	}

	var decoded map[string]any
	if err := json.Unmarshal(
		requests[0],
		&decoded,
	); err != nil {
		t.Fatalf(
			"decode request: %v",
			err,
		)
	}

	if decoded["@type"] != "close" {
		t.Fatalf(
			"@type = %v, want close",
			decoded["@type"],
		)
	}

	if closer.count() != 1 {
		t.Fatalf(
			"closer calls = %d, want 1",
			closer.count(),
		)
	}

	if !closer.ctxAlive() {
		t.Fatal(
			"stopRuntime received an expired context",
		)
	}
}

func TestSessionCloseAcceptsClosingThenClosed(
	t *testing.T,
) {
	session, _, closer, client :=
		newSessionWithFakes(t)

	feedAuth(
		t,
		client,
		"authorizationStateClosing",
		"authorizationStateClosed",
	)

	if err := session.Close(
		context.Background(),
	); err != nil {
		t.Fatalf(
			"Close() error = %v",
			err,
		)
	}

	if closer.count() != 1 {
		t.Fatalf(
			"closer calls = %d, want 1",
			closer.count(),
		)
	}
}

func TestSessionCloseSkipsNonAuthUpdates(t *testing.T) {
	session, _, closer, client :=
		newSessionWithFakes(t)

	client.updates <- Update{
		ClientID: client.id,
		Raw: []byte(
			`{"@type":"updateNewChat"}`,
		),
	}

	feedAuth(
		t,
		client,
		"authorizationStateClosed",
	)

	if err := session.Close(
		context.Background(),
	); err != nil {
		t.Fatalf(
			"Close() error = %v",
			err,
		)
	}

	if closer.count() != 1 {
		t.Fatalf(
			"closer calls = %d, want 1",
			closer.count(),
		)
	}
}

// ---- Close rejection paths ----

func TestSessionCloseRejectsUpdateChannelClosedBeforeClosed(
	t *testing.T,
) {
	session, _, closer, client :=
		newSessionWithFakes(t)

	close(client.updates)

	err := session.Close(context.Background())

	if !errors.Is(err, ErrSessionUpdatesClosed) {
		t.Fatalf(
			"error = %v, want ErrSessionUpdatesClosed",
			err,
		)
	}

	if closer.count() != 1 {
		t.Fatalf(
			"closer calls = %d, want 1",
			closer.count(),
		)
	}

	if !closer.ctxAlive() {
		t.Fatal(
			"stopRuntime received an expired context",
		)
	}
}

func TestSessionCloseInternalTimeout(t *testing.T) {
	session, _, closer, _ :=
		newSessionWithFakes(t)

	err := session.Close(context.Background())

	if !errors.Is(err, ErrSessionCloseTimeout) {
		t.Fatalf(
			"error = %v, want ErrSessionCloseTimeout",
			err,
		)
	}

	if closer.count() != 1 {
		t.Fatalf(
			"closer calls = %d, want 1",
			closer.count(),
		)
	}

	if !closer.ctxAlive() {
		t.Fatal(
			"stopRuntime received an expired context",
		)
	}
}

func TestSessionCloseAttachesLastRuntimeErrorToTimeout(
	t *testing.T,
) {
	session, _, _, client :=
		newSessionWithFakes(t)

	runtimeErr := errors.New("receive failed")
	client.errors <- runtimeErr

	err := session.Close(context.Background())

	if !errors.Is(err, ErrSessionCloseTimeout) {
		t.Fatalf(
			"error = %v, want ErrSessionCloseTimeout",
			err,
		)
	}

	if !errors.Is(err, runtimeErr) {
		t.Fatalf(
			"error = %v, want runtime error attached",
			err,
		)
	}
}

// ---- Caller timeout does not poison internal shutdown ----

func TestSessionCloseCallerContextTimesOutButRuntimeStops(
	t *testing.T,
) {
	session, _, closer, _ :=
		newSessionWithFakes(t)

	ctx, cancel := context.WithTimeout(
		context.Background(),
		20*time.Millisecond,
	)
	defer cancel()

	err := session.Close(ctx)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf(
			"error = %v, want context.DeadlineExceeded",
			err,
		)
	}

	waitForCloseDone(t, session)

	if closer.count() != 1 {
		t.Fatalf(
			"closer calls = %d, want 1",
			closer.count(),
		)
	}

	if !closer.ctxAlive() {
		t.Fatal(
			"stopRuntime received an expired context",
		)
	}
}

func TestSessionCloseSecondCallSeesFinalResultAfterFirstCancel(
	t *testing.T,
) {
	session, _, closer, _ :=
		newSessionWithFakes(t)

	shortCtx, cancelShort := context.WithTimeout(
		context.Background(),
		10*time.Millisecond,
	)
	defer cancelShort()

	if err := session.Close(shortCtx); !errors.Is(
		err,
		context.DeadlineExceeded,
	) {
		t.Fatalf(
			"first Close() error = %v, want DeadlineExceeded",
			err,
		)
	}

	err := session.Close(context.Background())

	if !errors.Is(err, ErrSessionCloseTimeout) {
		t.Fatalf(
			"second Close() error = %v, want ErrSessionCloseTimeout",
			err,
		)
	}

	if closer.count() != 1 {
		t.Fatalf(
			"closer calls = %d, want 1",
			closer.count(),
		)
	}
}

// ---- Idempotency and concurrency ----

func TestSessionCloseIdempotent(t *testing.T) {
	session, sender, closer, client :=
		newSessionWithFakes(t)

	feedAuth(
		t,
		client,
		"authorizationStateClosed",
	)

	for call := 1; call <= 3; call++ {
		if err := session.Close(
			context.Background(),
		); err != nil {
			t.Fatalf(
				"Close call %d: %v",
				call,
				err,
			)
		}
	}

	if closer.count() != 1 {
		t.Fatalf(
			"closer calls = %d, want 1",
			closer.count(),
		)
	}

	if sender.count() != 1 {
		t.Fatalf(
			"sender calls = %d, want 1",
			sender.count(),
		)
	}
}

func TestSessionCloseConcurrentCalls(t *testing.T) {
	session, sender, closer, client :=
		newSessionWithFakes(t)

	feedAuth(
		t,
		client,
		"authorizationStateClosed",
	)

	var waitGroup sync.WaitGroup

	for index := 0; index < 8; index++ {
		waitGroup.Add(1)

		go func() {
			defer waitGroup.Done()
			_ = session.Close(context.Background())
		}()
	}

	waitGroup.Wait()

	if closer.count() != 1 {
		t.Fatalf(
			"closer calls = %d, want 1",
			closer.count(),
		)
	}

	if sender.count() != 1 {
		t.Fatalf(
			"sender calls = %d, want 1",
			sender.count(),
		)
	}
}

// ---- Close error propagation ----

func TestSessionClosePropagatesCloserError(t *testing.T) {
	closerErr := errors.New("close failed")
	session, _, closer, client :=
		newSessionWithFakes(t)

	closer.err = closerErr

	feedAuth(
		t,
		client,
		"authorizationStateClosed",
	)

	err := session.Close(context.Background())

	if !errors.Is(err, closerErr) {
		t.Fatalf(
			"error = %v, want closer error",
			err,
		)
	}
}

func TestSessionClosePropagatesSendError(t *testing.T) {
	sendErr := errors.New("send failed")
	session, sender, closer, _ :=
		newSessionWithFakes(t)

	sender.err = sendErr

	err := session.Close(context.Background())

	if !errors.Is(err, sendErr) {
		t.Fatalf(
			"error = %v, want send error",
			err,
		)
	}

	if closer.count() != 1 {
		t.Fatalf(
			"closer calls = %d, want 1",
			closer.count(),
		)
	}
}

func TestSessionCloseRejectsNilSenderAndStopsRuntime(
	t *testing.T,
) {
	closer := &fakeCloser{}
	client := newTestClient(t)

	session := newAuthorizedSession(
		nil,
		closer,
		client,
		100*time.Millisecond,
		nil,
	)

	t.Cleanup(func() {
		session.cancel()

		select {
		case <-session.pumpDone:
		case <-time.After(time.Second):
			t.Error(
				"session pump did not exit",
			)
		}
	})

	err := session.Close(context.Background())

	if !errors.Is(err, ErrSessionNilSender) {
		t.Fatalf(
			"error = %v, want ErrSessionNilSender",
			err,
		)
	}

	if closer.count() != 1 {
		t.Fatalf(
			"closer calls = %d, want 1",
			closer.count(),
		)
	}
}

func TestSessionCloseHandlesClosedErrorChannel(
	t *testing.T,
) {
	session, _, closer, client :=
		newSessionWithFakes(t)

	close(client.errors)

	feedAuth(
		t,
		client,
		"authorizationStateClosed",
	)

	if err := session.Close(
		context.Background(),
	); err != nil {
		t.Fatalf(
			"Close() error = %v",
			err,
		)
	}

	if closer.count() != 1 {
		t.Fatalf(
			"closer calls = %d, want 1",
			closer.count(),
		)
	}
}

// ---- Pump behavior ----

func TestSessionPumpAppliesNonAuthUpdatesToLiveState(t *testing.T) {
	session, _, _, client :=
		newSessionWithFakes(t)

	client.updates <- Update{
		ClientID: client.id,
		Raw:      newChatRaw(7, "Alice", 100, ""),
	}

	chat := waitForLiveChat(t, session.LiveState(), 7)
	if chat.Title != "Alice" {
		t.Fatalf(
			"Title = %q, want Alice",
			chat.Title,
		)
	}
	if chat.Order != 100 {
		t.Fatalf(
			"Order = %d, want 100",
			chat.Order,
		)
	}
}

func TestSessionPumpForwardsRuntimeErrors(t *testing.T) {
	session, _, _, client :=
		newSessionWithFakes(t)

	runtimeErr := errors.New("receive failed")
	client.errors <- runtimeErr

	select {
	case receivedErr := <-session.Errors():
		if !errors.Is(
			receivedErr,
			runtimeErr,
		) {
			t.Fatalf(
				"forwarded error = %v, want %v",
				receivedErr,
				runtimeErr,
			)
		}

	case <-time.After(time.Second):
		t.Fatal(
			"runtime error was not forwarded by session pump",
		)
	}
}

func TestSessionPumpReportsMalformedUpdate(t *testing.T) {
	session, _, _, client :=
		newSessionWithFakes(t)

	client.updates <- Update{
		ClientID: client.id,
		Raw:      []byte("not json"),
	}

	select {
	case receivedErr := <-session.Errors():
		if receivedErr == nil {
			t.Fatal(
				"expected parse error",
			)
		}

	case <-time.After(time.Second):
		t.Fatal(
			"parse error was not reported",
		)
	}
}

// ---- Nil handling and accessors ----

func TestSessionCloseNil(t *testing.T) {
	var session *AuthorizedSession

	if err := session.Close(
		context.Background(),
	); !errors.Is(err, ErrSessionNilClient) {
		t.Fatalf(
			"error = %v, want ErrSessionNilClient",
			err,
		)
	}
}

func TestSessionCloseNilClient(t *testing.T) {
	session := &AuthorizedSession{}

	if err := session.Close(
		context.Background(),
	); !errors.Is(err, ErrSessionNilClient) {
		t.Fatalf(
			"error = %v, want ErrSessionNilClient",
			err,
		)
	}
}

func TestSessionClientID(t *testing.T) {
	session, _, _, client :=
		newSessionWithFakes(t)

	if session.ClientID() != client.ID() {
		t.Fatalf(
			"ClientID = %d, want %d",
			session.ClientID(),
			client.ID(),
		)
	}
}

func TestSessionClientIDOnNil(t *testing.T) {
	var session *AuthorizedSession

	if got := session.ClientID(); got != 0 {
		t.Fatalf(
			"ClientID = %d, want 0",
			got,
		)
	}
}

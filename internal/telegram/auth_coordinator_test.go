package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeSender struct {
	mu       sync.Mutex
	requests []RawMessage
	err      error
}

func (f *fakeSender) Send(_ int, request RawMessage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.requests = append(f.requests, append(RawMessage(nil), request...))
	return nil
}

func (f *fakeSender) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

func (f *fakeSender) types(t *testing.T) []string {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.requests))
	for _, raw := range f.requests {
		var env struct {
			Type string `json:"@type"`
		}
		if err := json.Unmarshal(raw, &env); err != nil {
			t.Fatalf("decode sent request: %v", err)
		}
		out = append(out, env.Type)
	}
	return out
}

type fakeProvider struct {
	phone    string
	code     string
	password string
	err      error
}

func (p *fakeProvider) ProvidePhoneNumber(context.Context) (string, error) {
	if p.err != nil {
		return "", p.err
	}
	return p.phone, nil
}

func (p *fakeProvider) ProvideCode(context.Context) (string, error) {
	if p.err != nil {
		return "", p.err
	}
	return p.code, nil
}

func (p *fakeProvider) ProvidePassword(context.Context) (string, error) {
	if p.err != nil {
		return "", p.err
	}
	return p.password, nil
}

func validAuthParams() TdlibParameters {
	return TdlibParameters{
		APIID:              1,
		APIHash:            "hash",
		DatabaseDirectory:  "/db",
		FilesDirectory:     "/files",
		SystemLanguageCode: "en",
		DeviceModel:        "test",
		ApplicationVersion: "dev",
	}
}

func newTestClient(t *testing.T) *Client {
	t.Helper()
	return &Client{
		id:      1,
		updates: make(chan Update, 32),
		errors:  make(chan error, 4),
	}
}

func feedAuth(t *testing.T, c *Client, tdlibTypes ...string) {
	t.Helper()
	for _, tdlibType := range tdlibTypes {
		raw := []byte(
			`{"@type":"updateAuthorizationState",` +
				`"authorization_state":{` +
				`"@type":"` + tdlibType + `"}}`,
		)
		c.updates <- Update{ClientID: c.id, Raw: raw}
	}
}

func TestRunAuthWithClientHappyPath(t *testing.T) {
	sender := &fakeSender{}
	client := newTestClient(t)
	provider := &fakeProvider{
		phone:    "+15551234",
		code:     "12345",
		password: "secret",
	}

	feedAuth(t, client,
		"authorizationStateWaitTdlibParameters",
		"authorizationStateWaitPhoneNumber",
		"authorizationStateWaitCode",
		"authorizationStateWaitPassword",
		"authorizationStateReady",
	)

	result, err := RunAuthWithClient(
		context.Background(), sender, client, validAuthParams(), provider,
	)
	if err != nil {
		t.Fatalf("RunAuthWithClient: %v", err)
	}
	if result.State != AuthStateReady {
		t.Fatalf("result.State = %q, want %q", result.State, AuthStateReady)
	}

	gotTypes := sender.types(t)
	wantTypes := []string{
		"setTdlibParameters",
		"setAuthenticationPhoneNumber",
		"checkAuthenticationCode",
		"checkAuthenticationPassword",
	}
	if len(gotTypes) != len(wantTypes) {
		t.Fatalf("sent %d requests (%v), want %d", len(gotTypes), gotTypes, len(wantTypes))
	}
	for i := range wantTypes {
		if gotTypes[i] != wantTypes[i] {
			t.Fatalf("request[%d] = %q, want %q", i, gotTypes[i], wantTypes[i])
		}
	}
}

func TestRunAuthWithClientPhoneOnlyReady(t *testing.T) {
	sender := &fakeSender{}
	client := newTestClient(t)
	provider := &fakeProvider{phone: "+15551234"}

	feedAuth(t, client,
		"authorizationStateWaitTdlibParameters",
		"authorizationStateWaitPhoneNumber",
		"authorizationStateReady",
	)

	result, err := RunAuthWithClient(
		context.Background(), sender, client, validAuthParams(), provider,
	)
	if err != nil {
		t.Fatalf("RunAuthWithClient: %v", err)
	}
	if result.State != AuthStateReady {
		t.Fatalf("result.State = %q, want Ready", result.State)
	}
	if sender.count() != 2 {
		t.Fatalf("sent %d requests, want 2", sender.count())
	}
}

func TestRunAuthWithClientSkipsNonAuthUpdate(t *testing.T) {
	sender := &fakeSender{}
	client := newTestClient(t)
	provider := &fakeProvider{}

	client.updates <- Update{
		ClientID: client.id,
		Raw:      []byte(`{"@type":"updateNewChat"}`),
	}
	feedAuth(t, client,
		"authorizationStateWaitTdlibParameters",
		"authorizationStateReady",
	)

	result, err := RunAuthWithClient(
		context.Background(), sender, client, validAuthParams(), provider,
	)
	if err != nil {
		t.Fatalf("RunAuthWithClient: %v", err)
	}
	if result.State != AuthStateReady {
		t.Fatalf("result.State = %q, want Ready", result.State)
	}
}

func TestRunAuthWithClientRejectsNilSender(t *testing.T) {
	client := newTestClient(t)
	_, err := RunAuthWithClient(
		context.Background(), nil, client, validAuthParams(), &fakeProvider{},
	)
	if !errors.Is(err, ErrNilRuntime) {
		t.Fatalf("err = %v, want ErrNilRuntime", err)
	}
}

func TestRunAuthWithClientRejectsNilClient(t *testing.T) {
	_, err := RunAuthWithClient(
		context.Background(), &fakeSender{}, nil, validAuthParams(), &fakeProvider{},
	)
	if !errors.Is(err, ErrNilClient) {
		t.Fatalf("err = %v, want ErrNilClient", err)
	}
}

func TestRunAuthWithClientRejectsNilProvider(t *testing.T) {
	client := newTestClient(t)
	_, err := RunAuthWithClient(
		context.Background(), &fakeSender{}, client, validAuthParams(), nil,
	)
	if !errors.Is(err, ErrNilProvider) {
		t.Fatalf("err = %v, want ErrNilProvider", err)
	}
}

// RunAuth must reject a nil provider before creating a TDLib client ID.
// rt is nil here so the check order is observable through the returned
// sentinel: ErrNilRuntime first, then ErrNilProvider.
func TestRunAuthRejectsNilRuntimeBeforeAnythingElse(t *testing.T) {
	_, err := RunAuth(
		context.Background(), nil, validAuthParams(), nil,
	)
	if !errors.Is(err, ErrNilRuntime) {
		t.Fatalf("err = %v, want ErrNilRuntime", err)
	}
}

func TestRunAuthRejectsNilProviderBeforeContextCheck(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Runtime must be non-nil to reach the provider check, but the
	// runtime.NewClient path would require a live native handle. Use a
	// dummy *Runtime with a nil native handle: the nil-provider check
	// returns before NewClient is called.
	rt := &Runtime{}

	_, err := RunAuth(ctx, rt, validAuthParams(), nil)
	if !errors.Is(err, ErrNilProvider) {
		t.Fatalf("err = %v, want ErrNilProvider", err)
	}
}

func TestRunAuthRejectsCanceledContextBeforeClientCreation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Non-nil provider and non-nil runtime. The canceled context must
	// be detected before rt.NewClient() is called; the fake Runtime has
	// no native handle, so if NewClient were called we would see
	// ErrNotStarted instead of context.Canceled.
	rt := &Runtime{}

	_, err := RunAuth(ctx, rt, validAuthParams(), &fakeProvider{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestRunAuthWithClientContextCanceled(t *testing.T) {
	sender := &fakeSender{}
	client := newTestClient(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := RunAuthWithClient(
		ctx, sender, client, validAuthParams(), &fakeProvider{},
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestRunAuthWithClientClosedUpdateChannel(t *testing.T) {
	sender := &fakeSender{}
	client := newTestClient(t)
	close(client.updates)

	_, err := RunAuthWithClient(
		context.Background(), sender, client, validAuthParams(), &fakeProvider{},
	)
	if !errors.Is(err, ErrUpdateChannelGone) {
		t.Fatalf("err = %v, want ErrUpdateChannelGone", err)
	}
}

// Regression: a closed error channel must not preempt a buffered Ready.
// The coordinator disables the error branch and continues reading
// updates.
func TestRunAuthWithClientClosedErrorChannelStillProcessesReady(t *testing.T) {
	sender := &fakeSender{}
	client := newTestClient(t)

	close(client.errors)

	feedAuth(t, client,
		"authorizationStateWaitTdlibParameters",
		"authorizationStateReady",
	)

	result, err := RunAuthWithClient(
		context.Background(), sender, client, validAuthParams(), &fakeProvider{},
	)
	if err != nil {
		t.Fatalf("RunAuthWithClient() error = %v", err)
	}
	if result.State != AuthStateReady {
		t.Fatalf("state = %s, want %s", result.State, AuthStateReady)
	}
}

func TestRunAuthWithClientReturnsRuntimeError(t *testing.T) {
	runtimeErr := errors.New("receive failed")
	sender := &fakeSender{}
	client := newTestClient(t)

	client.errors <- runtimeErr

	_, err := RunAuthWithClient(
		context.Background(), sender, client, validAuthParams(), &fakeProvider{},
	)
	if !errors.Is(err, runtimeErr) {
		t.Fatalf("err = %v, want runtime error", err)
	}
}

func TestRunAuthWithClientParseError(t *testing.T) {
	sender := &fakeSender{}
	client := newTestClient(t)

	client.updates <- Update{ClientID: client.id, Raw: []byte(`not json`)}

	_, err := RunAuthWithClient(
		context.Background(), sender, client, validAuthParams(), &fakeProvider{},
	)
	if err == nil {
		t.Fatal("expected error for malformed update")
	}
	if errors.Is(err, ErrUnsupportedAuthState) {
		t.Fatalf("malformed JSON must not be ErrUnsupportedAuthState: %v", err)
	}
}

func TestRunAuthWithClientUnsupportedParseState(t *testing.T) {
	sender := &fakeSender{}
	client := newTestClient(t)

	client.updates <- Update{
		ClientID: client.id,
		Raw: []byte(
			`{"@type":"updateAuthorizationState",` +
				`"authorization_state":{` +
				`"@type":"authorizationStateFuture"}}`,
		),
	}

	_, err := RunAuthWithClient(
		context.Background(), sender, client, validAuthParams(), &fakeProvider{},
	)
	if !errors.Is(err, ErrUnsupportedAuthState) {
		t.Fatalf("err = %v, want ErrUnsupportedAuthState", err)
	}
}

func TestRunAuthWithClientUnsupportedStepState(t *testing.T) {
	sender := &fakeSender{}
	client := newTestClient(t)

	feedAuth(t, client,
		"authorizationStateWaitTdlibParameters",
		"authorizationStateWaitEmailAddress",
	)

	_, err := RunAuthWithClient(
		context.Background(), sender, client, validAuthParams(), &fakeProvider{},
	)
	if !errors.Is(err, ErrUnsupportedAuthState) {
		t.Fatalf("err = %v, want ErrUnsupportedAuthState", err)
	}
}

func TestRunAuthWithClientTerminalStatesBeforeReady(t *testing.T) {
	tests := []struct {
		name      string
		tdlibType string
		wantState AuthState
	}{
		{"closed", "authorizationStateClosed", AuthStateClosed},
		{"closing", "authorizationStateClosing", AuthStateClosing},
		{"logging_out", "authorizationStateLoggingOut", AuthStateLoggingOut},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sender := &fakeSender{}
			client := newTestClient(t)

			feedAuth(t, client, test.tdlibType)

			result, err := RunAuthWithClient(
				context.Background(), sender, client, validAuthParams(), &fakeProvider{},
			)
			if !errors.Is(err, ErrAuthorizationClosed) {
				t.Fatalf("err = %v, want ErrAuthorizationClosed", err)
			}
			if result.State != test.wantState {
				t.Fatalf("result.State = %q, want %q", result.State, test.wantState)
			}
			if sender.count() != 0 {
				t.Fatalf("sent %d requests, want 0", sender.count())
			}
		})
	}
}

func TestRunAuthWithClientRejectsEmptyProviderInput(t *testing.T) {
	tests := []struct {
		name      string
		tdlibType string
	}{
		{"phone", "authorizationStateWaitPhoneNumber"},
		{"code", "authorizationStateWaitCode"},
		{"password", "authorizationStateWaitPassword"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sender := &fakeSender{}
			client := newTestClient(t)

			feedAuth(t, client, test.tdlibType)

			_, err := RunAuthWithClient(
				context.Background(), sender, client, validAuthParams(), &fakeProvider{},
			)
			if !errors.Is(err, ErrInvalidAuthParameters) {
				t.Fatalf("err = %v, want ErrInvalidAuthParameters", err)
			}
			if sender.count() != 0 {
				t.Fatalf("sent %d requests, want 0", sender.count())
			}
		})
	}
}

func TestRunAuthWithClientSendError(t *testing.T) {
	sendErr := errors.New("send failed")
	sender := &fakeSender{err: sendErr}
	client := newTestClient(t)

	feedAuth(t, client,
		"authorizationStateWaitTdlibParameters",
		"authorizationStateReady",
	)

	_, err := RunAuthWithClient(
		context.Background(), sender, client, validAuthParams(), &fakeProvider{},
	)
	if !errors.Is(err, sendErr) {
		t.Fatalf("err = %v, want send error", err)
	}
}

func TestRunAuthWithClientProviderError(t *testing.T) {
	providerErr := errors.New("provider failed")
	sender := &fakeSender{}
	client := newTestClient(t)

	feedAuth(t, client,
		"authorizationStateWaitTdlibParameters",
		"authorizationStateWaitPhoneNumber",
	)

	_, err := RunAuthWithClient(
		context.Background(), sender, client, validAuthParams(),
		&fakeProvider{err: providerErr},
	)
	if !errors.Is(err, providerErr) {
		t.Fatalf("err = %v, want provider error", err)
	}
}

func TestRunAuthWithClientUsesContextDuringWait(t *testing.T) {
	sender := &fakeSender{}
	client := newTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := RunAuthWithClient(
		ctx, sender, client, validAuthParams(), &fakeProvider{},
	)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
}

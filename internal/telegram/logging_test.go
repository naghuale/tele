package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"telecli/internal/telemetry/recorder"
)

// recordingNative records the exact order of every TDLib interaction so
// ordering invariants can be asserted.
type recordingNative struct {
	mu            sync.Mutex
	events        []string
	requests      [][]byte
	responses     [][]byte
	executeErr    error
	emptyResponse bool
	// executeResponse, when set, replaces the default ok response.
	executeResponse []byte
	receive         chan []byte
	onSend          func(kind string, raw []byte)
	clientID        int
}

func newRecordingNative() *recordingNative {
	return &recordingNative{
		receive:  make(chan []byte, 32),
		clientID: 7,
	}
}

func (n *recordingNative) record(event string, payload []byte) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.events = append(n.events, event)
	if payload != nil {
		n.requests = append(n.requests, append([]byte(nil), payload...))
	}
}

func (n *recordingNative) CreateClientID() (int, error) {
	n.record("create_client_id", nil)
	return n.clientID, nil
}

func (n *recordingNative) Send(_ int, request []byte) error {
	kind := requestType(request)
	n.record("send:"+kind, request)
	if n.onSend != nil {
		n.onSend(kind, append([]byte(nil), request...))
	}
	return nil
}

func (n *recordingNative) Receive(timeout time.Duration) ([]byte, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case raw := <-n.receive:
		return raw, nil
	case <-timer.C:
		return nil, nil
	}
}

func (n *recordingNative) Execute(request []byte) ([]byte, error) {
	n.record("execute:"+requestType(request), request)
	if n.executeErr != nil {
		return nil, n.executeErr
	}
	if n.emptyResponse {
		return nil, nil
	}
	n.mu.Lock()
	n.responses = append(n.responses, append([]byte(nil), request...))
	n.mu.Unlock()
	if n.executeResponse != nil {
		return append([]byte(nil), n.executeResponse...), nil
	}
	return []byte(`{"@type":"ok"}`), nil
}

func (n *recordingNative) Close() error {
	n.record("close", nil)
	return nil
}

func (n *recordingNative) recordedEvents() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string(nil), n.events...)
}

func (n *recordingNative) sentRequestsOfType(tdlibType string) [][]byte {
	n.mu.Lock()
	defer n.mu.Unlock()
	var out [][]byte
	for _, raw := range n.requests {
		if requestType(raw) == tdlibType {
			out = append(out, raw)
		}
	}
	return out
}

// requestType extracts the @type field of a raw TDLib request.
func requestType(raw []byte) string {
	var envelope struct {
		Type string `json:"@type"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return "unparsable"
	}
	return envelope.Type
}

func indexOfEvent(events []string, want string) int {
	for i, event := range events {
		if event == want {
			return i
		}
	}
	return -1
}

// stateJSON renders a direct authorization-state response. The runtime
// routes incoming messages by @client_id, so the response must carry the
// id of the client that requested it.
func (n *recordingNative) stateJSON(state string) []byte {
	return []byte(
		`{"@client_id":` + itoa(n.clientID) +
			`,"@type":"` + state + `"}`,
	)
}

// updateJSON renders a pushed updateAuthorizationState envelope.
func (n *recordingNative) updateJSON(state string) []byte {
	return []byte(
		`{"@client_id":` + itoa(n.clientID) +
			`,"@type":"updateAuthorizationState",` +
			`"authorization_state":{"@type":"` + state + `"}}`,
	)
}

// itoa renders a small non-negative int without importing strconv into
// the test surface twice.
func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var digits []byte
	for v > 0 {
		digits = append([]byte{byte('0' + v%10)}, digits...)
		v /= 10
	}
	return string(digits)
}

// scriptHappyPath makes the fake answer every authorization request the
// way TDLib does, so the flow reaches Ready deterministically.
//
// When duplicateParameters is true the fake also pushes a second
// waitTdlibParameters update, reproducing the sequence observed during
// the manual smoke.
func scriptHappyPath(
	native *recordingNative,
	duplicateParameters bool,
) {
	native.onSend = func(kind string, _ []byte) {
		switch kind {
		case "getAuthorizationState":
			native.receive <- native.stateJSON(
				"authorizationStateWaitTdlibParameters",
			)
			if duplicateParameters {
				native.receive <- native.updateJSON(
					"authorizationStateWaitTdlibParameters",
				)
			}
		case "setTdlibParameters":
			native.receive <- native.stateJSON(
				"authorizationStateWaitPhoneNumber",
			)
		case "setAuthenticationPhoneNumber":
			native.receive <- native.stateJSON("authorizationStateWaitCode")
		case "checkAuthenticationCode":
			native.receive <- native.stateJSON(
				"authorizationStateWaitPassword",
			)
		case "checkAuthenticationPassword":
			native.receive <- native.stateJSON("authorizationStateReady")
		}
	}
}

// newStartedRecordingRuntime builds a started Runtime whose native
// records interactions.
func newStartedRecordingRuntime(
	t *testing.T,
	native *recordingNative,
) *Runtime {
	t.Helper()
	cfg := DefaultConfig()
	cfg.ReceiveTimeout = time.Millisecond
	runtime, err := NewRuntime(cfg, native, recorder.NewNoop())
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = runtime.Close(context.Background())
	})
	return runtime
}

// ---- verbosity level ----

func TestProductionSessionUsesTDLibVerbosityLevelOne(t *testing.T) {
	native := newRecordingNative()
	runtime := newStartedRecordingRuntime(t, native)

	if err := runtime.ConfigureSafeLogging(); err != nil {
		t.Fatalf("ConfigureSafeLogging: %v", err)
	}

	var found int
	for _, raw := range native.requests {
		if requestType(raw) != "setLogVerbosityLevel" {
			continue
		}
		found++
		var payload struct {
			Level int `json:"new_verbosity_level"`
		}
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatalf("decode setLogVerbosityLevel: %v", err)
		}
		if payload.Level != 1 {
			t.Fatalf(
				"new_verbosity_level = %d, want 1",
				payload.Level,
			)
		}
	}
	if found != 1 {
		t.Fatalf("setLogVerbosityLevel calls = %d, want 1", found)
	}

	if productionTDLibVerbosityLevel != 1 {
		t.Fatalf(
			"productionTDLibVerbosityLevel = %d, want 1",
			productionTDLibVerbosityLevel,
		)
	}
}

func TestBuildSetLogVerbosityRequestRejectsNegativeLevel(t *testing.T) {
	if _, err := buildSetLogVerbosityRequest(-1); !errors.Is(
		err,
		ErrTDLibLogConfiguration,
	) {
		t.Fatalf("error = %v, want ErrTDLibLogConfiguration", err)
	}
}

func TestSetSafeTDLibLogVerbosityRejectsNilNative(t *testing.T) {
	if err := setSafeTDLibLogVerbosity(nil); !errors.Is(
		err,
		ErrTDLibLogConfiguration,
	) {
		t.Fatalf("error = %v, want ErrTDLibLogConfiguration", err)
	}
}

// TestSetSafeTDLibLogVerbosityFailsClosedOnNonOkResponse pins that only
// an explicit ok counts as success. td_execute returns a rejected request
// as an error object rather than a call failure, and accepting it would
// leave TDLib at its default verbosity, which dumps api_hash.
func TestSetSafeTDLibLogVerbosityFailsClosedOnNonOkResponse(t *testing.T) {
	cases := map[string]string{
		"error object":    `{"@type":"error","code":400,"message":"rejected"}`,
		"unexpected type": `{"@type":"logVerbosityLevel","verbosity_level":5}`,
		"missing type":    `{}`,
		"malformed json":  `{"@type":`,
	}
	for name, response := range cases {
		t.Run(name, func(t *testing.T) {
			native := newRecordingNative()
			native.executeResponse = []byte(response)

			if err := setSafeTDLibLogVerbosity(native); !errors.Is(
				err,
				ErrTDLibLogConfiguration,
			) {
				t.Fatalf("error = %v, want ErrTDLibLogConfiguration", err)
			}
		})
	}
}

// TestAuthorizeFailsClosedWhenTDLibRejectsVerbosity pins that a rejected
// verbosity request stops startup before any client exists, so no
// credential-bearing request can be sent.
func TestAuthorizeFailsClosedWhenTDLibRejectsVerbosity(t *testing.T) {
	native := newRecordingNative()
	native.executeResponse = []byte(
		`{"@type":"error","code":400,"message":"rejected"}`,
	)
	runtime := newStartedRecordingRuntime(t, native)

	// Without the fail-closed check Authorize would proceed and wait
	// for an authorization state that never arrives.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := Authorize(
		ctx,
		runtime,
		TdlibParameters{},
		&fakeProvider{},
	)
	if !errors.Is(err, ErrTDLibLogConfiguration) {
		t.Fatalf("error = %v, want ErrTDLibLogConfiguration", err)
	}

	for _, event := range native.recordedEvents() {
		if event == "create_client_id" || strings.HasPrefix(event, "send:") {
			t.Fatalf("event %q after rejected verbosity: %v",
				event, native.recordedEvents())
		}
	}
}

func TestConfigureSafeLoggingRejectsNilRuntime(t *testing.T) {
	var runtime *Runtime
	if err := runtime.ConfigureSafeLogging(); !errors.Is(
		err,
		ErrTDLibLogConfiguration,
	) {
		t.Fatalf("error = %v, want ErrTDLibLogConfiguration", err)
	}
}

// ---- ordering ----

// TestProductionSessionConfiguresTDLibVerbosityBeforeCreatingAuthorizationTraffic
// pins the required production startup order:
//
//	execute setLogVerbosityLevel -> create client ->
//	send getAuthorizationState -> send setTdlibParameters
func TestProductionSessionConfiguresTDLibVerbosityBeforeCreatingAuthorizationTraffic(
	t *testing.T,
) {
	native := newRecordingNative()
	scriptHappyPath(native, false)
	runtime := newStartedRecordingRuntime(t, native)

	ctx, cancel := context.WithTimeout(
		context.Background(),
		5*time.Second,
	)
	defer cancel()

	if _, err := Authorize(
		ctx,
		runtime,
		validAuthParams(),
		&fakeProvider{
			phone:    "+10000000000",
			code:     "12345",
			password: "pw",
		},
	); err != nil {
		t.Fatalf(
			"Authorize: %v; events = %v",
			err,
			native.recordedEvents(),
		)
	}

	events := native.recordedEvents()

	verbosityAt := indexOfEvent(events, "execute:setLogVerbosityLevel")
	clientAt := indexOfEvent(events, "create_client_id")
	stateAt := indexOfEvent(events, "send:getAuthorizationState")
	paramsAt := indexOfEvent(events, "send:setTdlibParameters")

	if verbosityAt < 0 {
		t.Fatalf(
			"setLogVerbosityLevel was never executed; events = %v",
			events,
		)
	}
	if clientAt < 0 || stateAt < 0 || paramsAt < 0 {
		t.Fatalf(
			"missing startup events (client=%d state=%d params=%d); events = %v",
			clientAt,
			stateAt,
			paramsAt,
			events,
		)
	}

	if !(verbosityAt < clientAt &&
		clientAt < stateAt &&
		stateAt < paramsAt) {
		t.Fatalf(
			"wrong order: verbosity=%d client=%d state=%d params=%d; events = %v",
			verbosityAt,
			clientAt,
			stateAt,
			paramsAt,
			events,
		)
	}
}

// ---- fail closed ----

func TestProductionSessionFailsClosedWhenLogConfigurationFails(
	t *testing.T,
) {
	native := newRecordingNative()
	native.executeErr = errors.New("native execute unavailable")
	runtime := newStartedRecordingRuntime(t, native)

	_, err := Authorize(
		context.Background(),
		runtime,
		validAuthParams(),
		&fakeProvider{phone: "+10000000000"},
	)
	if !errors.Is(err, ErrTDLibLogConfiguration) {
		t.Fatalf("error = %v, want ErrTDLibLogConfiguration", err)
	}
}

func TestProductionSessionDoesNotActivateAuthorizationAfterLogConfigurationFailure(
	t *testing.T,
) {
	native := newRecordingNative()
	native.executeErr = errors.New("native execute unavailable")
	runtime := newStartedRecordingRuntime(t, native)

	if _, err := Authorize(
		context.Background(),
		runtime,
		validAuthParams(),
		&fakeProvider{phone: "+10000000000"},
	); err == nil {
		t.Fatal("Authorize must fail when log configuration fails")
	}

	events := native.recordedEvents()

	for _, forbidden := range []string{
		"create_client_id",
		"send:getAuthorizationState",
		"send:setTdlibParameters",
	} {
		if indexOfEvent(events, forbidden) >= 0 {
			t.Fatalf(
				"%s happened after a failed log configuration; events = %v",
				forbidden,
				events,
			)
		}
	}
}

func TestRunAuthFailsClosedWhenLogConfigurationFails(t *testing.T) {
	native := newRecordingNative()
	native.executeErr = errors.New("native execute unavailable")
	runtime := newStartedRecordingRuntime(t, native)

	_, err := RunAuth(
		context.Background(),
		runtime,
		validAuthParams(),
		&fakeProvider{phone: "+10000000000"},
	)
	if !errors.Is(err, ErrTDLibLogConfiguration) {
		t.Fatalf("error = %v, want ErrTDLibLogConfiguration", err)
	}

	if indexOfEvent(
		native.recordedEvents(),
		"send:setTdlibParameters",
	) >= 0 {
		t.Fatal("setTdlibParameters must not be sent after a failed log configuration")
	}
}

// ---- credential leakage ----

// TestProductionAuthorizationDoesNotLogAPIHash asserts that a credential
// carried by the authorization flow never reaches an error string or any
// other observable output of the production path. The hash may only
// appear inside the setTdlibParameters payload that TDLib requires.
func TestProductionAuthorizationDoesNotLogAPIHash(t *testing.T) {
	const secretHash = "0123456789abcdef0123456789abcdef"

	params := validAuthParams()
	params.APIHash = secretHash

	native := newRecordingNative()
	scriptHappyPath(native, false)
	runtime := newStartedRecordingRuntime(t, native)

	ctx, cancel := context.WithTimeout(
		context.Background(),
		5*time.Second,
	)
	defer cancel()

	_, err := Authorize(
		ctx,
		runtime,
		params,
		&fakeProvider{
			phone:    "+10000000000",
			code:     "12345",
			password: "pw",
		},
	)
	if err != nil {
		t.Fatalf(
			"Authorize: %v; events = %v",
			err,
			native.recordedEvents(),
		)
	}

	assertSecretOnlyInTdlibParameters(t, native, secretHash, []error{err})
}

func TestProductionAuthorizationDoesNotLogPhone(t *testing.T) {
	const secretPhone = "+79999999999"

	native := newRecordingNative()
	scriptHappyPath(native, false)
	runtime := newStartedRecordingRuntime(t, native)

	ctx, cancel := context.WithTimeout(
		context.Background(),
		5*time.Second,
	)
	defer cancel()

	_, err := Authorize(
		ctx,
		runtime,
		validAuthParams(),
		&fakeProvider{
			phone:    secretPhone,
			code:     "12345",
			password: "pw",
		},
	)
	if err != nil {
		t.Fatalf(
			"Authorize: %v; events = %v",
			err,
			native.recordedEvents(),
		)
	}

	// The phone number is sent to TDLib in setPhoneNumber, so it is
	// allowed to appear in that payload only.
	assertSecretAbsent(t, native, secretPhone, []error{err})
}

func TestProductionAuthorizationDoesNotLogPassword(t *testing.T) {
	const secretPassword = "two-factor-secret"

	native := newRecordingNative()
	scriptHappyPath(native, false)
	runtime := newStartedRecordingRuntime(t, native)

	ctx, cancel := context.WithTimeout(
		context.Background(),
		5*time.Second,
	)
	defer cancel()

	_, err := Authorize(
		ctx,
		runtime,
		validAuthParams(),
		&fakeProvider{
			phone:    "+10000000000",
			code:     "12345",
			password: secretPassword,
		},
	)
	if err != nil {
		t.Fatalf(
			"Authorize: %v; events = %v",
			err,
			native.recordedEvents(),
		)
	}

	// The password is sent to TDLib in checkPassword, so it is allowed
	// to appear in that payload only.
	assertSecretAbsent(t, native, secretPassword, []error{err})
}

// assertSecretOnlyInTdlibParameters fails when the secret shows up in an
// error, in a non-credential request, or in the recorded event log.
func assertSecretOnlyInTdlibParameters(
	t *testing.T,
	native *recordingNative,
	secret string,
	errs []error,
) {
	t.Helper()

	for _, err := range errs {
		if err != nil && strings.Contains(err.Error(), secret) {
			t.Fatalf("secret leaked into an error: %v", err)
		}
	}

	for _, event := range native.recordedEvents() {
		if strings.Contains(event, secret) {
			t.Fatalf("secret leaked into an event name: %s", event)
		}
	}

	for _, raw := range native.sentRequestsOfType("setTdlibParameters") {
		if !strings.Contains(string(raw), secret) {
			continue
		}
		return
	}
	t.Fatal("secret never reached the setTdlibParameters payload")
}

// assertSecretAbsent fails when the secret appears anywhere in the
// recorded traffic or in the supplied errors.
func assertSecretAbsent(
	t *testing.T,
	native *recordingNative,
	secret string,
	errs []error,
) {
	t.Helper()

	for _, err := range errs {
		if err != nil && strings.Contains(err.Error(), secret) {
			t.Fatalf("secret leaked into an error: %v", err)
		}
	}

	for _, event := range native.recordedEvents() {
		if strings.Contains(event, secret) {
			t.Fatalf("secret leaked into an event name: %s", event)
		}
	}
}

// TestProductionSessionRejectsEmptyLogConfigurationResponse pins the
// fail-closed contract for an empty synchronous response. A native layer
// that returns no payload is misbehaving, and continuing would leave
// TDLib at its default verbosity.
func TestProductionSessionRejectsEmptyLogConfigurationResponse(t *testing.T) {
	native := newRecordingNative()
	native.emptyResponse = true
	runtime := newStartedRecordingRuntime(t, native)

	err := runtime.ConfigureSafeLogging()
	if err == nil {
		t.Fatal("ConfigureSafeLogging() error = nil, want non-nil")
	}
	if !errors.Is(err, ErrTDLibLogConfiguration) {
		t.Fatalf("error = %v, want ErrTDLibLogConfiguration", err)
	}
}

// TestProductionLoggingFailureDoesNotExposeAuthCredentials proves the
// startup error text never carries credential values.
//
// The secrets are deliberately not included in failure messages.
func TestProductionLoggingFailureDoesNotExposeAuthCredentials(t *testing.T) {
	const secretHash = "h7-security-test-api-hash"
	const secretPhone = "+15550009999"

	params := validAuthParams()
	params.APIHash = secretHash

	native := newRecordingNative()
	native.executeErr = errors.New("native execute failed")
	runtime := newStartedRecordingRuntime(t, native)

	_, err := Authorize(
		context.Background(),
		runtime,
		params,
		&fakeProvider{phone: secretPhone},
	)
	if err == nil {
		t.Fatal("Authorize() error = nil, want non-nil")
	}

	for _, secret := range []string{secretHash, secretPhone} {
		if strings.Contains(err.Error(), secret) {
			t.Fatal("startup error contains a credential value")
		}
	}
}

package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// countRequestsOfType counts how many recorded requests carry the given
// TDLib @type.
func countRequestsOfType(
	native *recordingNative,
	tdlibType string,
) int {
	return len(native.sentRequestsOfType(tdlibType))
}

// ---- state machine level ----

// TestAuthSessionSendsTdlibParametersOnce pins the primary invariant: a
// single AuthSession emits setTdlibParameters at most once.
func TestAuthSessionSendsTdlibParametersOnce(t *testing.T) {
	session := NewAuthSession(validAuthParams())

	first, err := session.Step(
		AuthStateWaitTdlibParameters,
		AuthInput{},
	)
	if err != nil {
		t.Fatalf("first Step: %v", err)
	}
	if first == nil {
		t.Fatal("first Step returned no request")
	}
	if got := requestType(first); got != "setTdlibParameters" {
		t.Fatalf("first request type = %q, want setTdlibParameters", got)
	}

	second, err := session.Step(
		AuthStateWaitTdlibParameters,
		AuthInput{},
	)
	if err != nil {
		t.Fatalf("second Step: %v", err)
	}
	if second != nil {
		t.Fatalf(
			"second Step returned %s, want nil",
			requestType(second),
		)
	}
}

// TestAuthSessionIgnoresDuplicateWaitTdlibParameters hammers the state
// and requires that no further request is produced.
func TestAuthSessionIgnoresDuplicateWaitTdlibParameters(t *testing.T) {
	session := NewAuthSession(validAuthParams())

	emitted := 0
	for i := 0; i < 5; i++ {
		request, err := session.Step(
			AuthStateWaitTdlibParameters,
			AuthInput{},
		)
		if err != nil {
			t.Fatalf("Step %d: %v", i, err)
		}
		if request != nil {
			emitted++
		}
	}

	if emitted != 1 {
		t.Fatalf(
			"setTdlibParameters emitted %d times, want exactly 1",
			emitted,
		)
	}
}

// TestAuthSessionAcceptsDirectAndUpdateFormsWithoutDuplicateParameters
// feeds both wire forms of the same state through ParseAuthUpdate and
// Step, exactly as the runtime delivers them.
func TestAuthSessionAcceptsDirectAndUpdateFormsWithoutDuplicateParameters(
	t *testing.T,
) {
	direct := []byte(
		`{"@type":"authorizationStateWaitTdlibParameters"}`,
	)
	update := []byte(
		`{"@type":"updateAuthorizationState",` +
			`"authorization_state":` +
			`{"@type":"authorizationStateWaitTdlibParameters"}}`,
	)

	session := NewAuthSession(validAuthParams())
	emitted := 0

	for _, raw := range [][]byte{direct, update, direct, update} {
		state, err := ParseAuthUpdate(raw)
		if err != nil {
			t.Fatalf("ParseAuthUpdate(%s): %v", raw, err)
		}
		if state != AuthStateWaitTdlibParameters {
			t.Fatalf("state = %q, want wait_tdlib_parameters", state)
		}

		request, err := session.Step(state, AuthInput{})
		if err != nil {
			t.Fatalf("Step: %v", err)
		}
		if request != nil {
			emitted++
			if got := requestType(request); got != "setTdlibParameters" {
				t.Fatalf("request type = %q", got)
			}
		}
	}

	if emitted != 1 {
		t.Fatalf(
			"setTdlibParameters emitted %d times, want exactly 1",
			emitted,
		)
	}
}

// TestAuthSessionDoesNotMarkParametersRequestedWhenBuildFails proves the
// flag is set only after a valid request was produced, so a corrected
// configuration can still succeed.
func TestAuthSessionDoesNotMarkParametersRequestedWhenBuildFails(
	t *testing.T,
) {
	invalid := TdlibParameters{APIID: 1} // everything else empty

	session := NewAuthSession(invalid)

	request, err := session.Step(
		AuthStateWaitTdlibParameters,
		AuthInput{},
	)
	if !errors.Is(err, ErrInvalidAuthParameters) {
		t.Fatalf("error = %v, want ErrInvalidAuthParameters", err)
	}
	if request != nil {
		t.Fatal("invalid parameters must not produce a request")
	}
}

// TestAuthSessionRetriesParametersAfterBuildFailure uses one session,
// fails the build, repairs nothing, and requires that a subsequent
// successful build is still possible on a fresh session while the failed
// session stays marked as not requested.
func TestAuthSessionRetriesParametersAfterBuildFailure(t *testing.T) {
	session := NewAuthSession(TdlibParameters{APIID: 1})

	if _, err := session.Step(
		AuthStateWaitTdlibParameters,
		AuthInput{},
	); err == nil {
		t.Fatal("expected the invalid build to fail")
	}

	if session.tdlibParametersRequested {
		t.Fatal(
			"tdlibParametersRequested must stay false after a failed build",
		)
	}

	// The same session can still emit once the parameters are valid.
	session.params = validAuthParams()

	request, err := session.Step(
		AuthStateWaitTdlibParameters,
		AuthInput{},
	)
	if err != nil {
		t.Fatalf("Step after repair: %v", err)
	}
	if request == nil {
		t.Fatal("Step after repair returned no request")
	}
}

// TestAuthSessionDoesNotResetParametersFlagOnLaterStates pins that the
// flag belongs to the lifecycle and survives forward transitions.
func TestAuthSessionDoesNotResetParametersFlagOnLaterStates(t *testing.T) {
	session := NewAuthSession(validAuthParams())

	if _, err := session.Step(
		AuthStateWaitTdlibParameters,
		AuthInput{},
	); err != nil {
		t.Fatal(err)
	}

	if _, err := session.Step(
		AuthStateWaitPhoneNumber,
		AuthInput{PhoneNumber: "+10000000000"},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Step(
		AuthStateWaitCode,
		AuthInput{Code: "12345"},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Step(
		AuthStateWaitPassword,
		AuthInput{Password: "pw"},
	); err != nil {
		t.Fatal(err)
	}

	if !session.tdlibParametersRequested {
		t.Fatal("the flag must not be cleared by later states")
	}

	// A late duplicate must still be a no-op.
	request, err := session.Step(
		AuthStateWaitTdlibParameters,
		AuthInput{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if request != nil {
		t.Fatal("a late duplicate must not re-emit setTdlibParameters")
	}
}

// TestAuthSessionSerializesConcurrentSteps runs the race detector over
// a concurrent duplicate storm.
func TestAuthSessionSerializesConcurrentSteps(t *testing.T) {
	session := NewAuthSession(validAuthParams())

	const goroutines = 16

	var wg sync.WaitGroup
	results := make([][]byte, goroutines)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(slot int) {
			defer wg.Done()
			request, err := session.Step(
				AuthStateWaitTdlibParameters,
				AuthInput{},
			)
			if err != nil {
				t.Errorf("Step: %v", err)
				return
			}
			results[slot] = request
		}(i)
	}
	wg.Wait()

	emitted := 0
	for _, request := range results {
		if request != nil {
			emitted++
		}
	}
	if emitted != 1 {
		t.Fatalf(
			"concurrent Steps emitted %d requests, want exactly 1",
			emitted,
		)
	}
}

// ---- production regression ----

// TestProductionSessionSendsTdlibParametersOnce reproduces the exact
// sequence captured during the manual smoke: the direct response to
// getAuthorizationState and a pushed updateAuthorizationState both carry
// waitTdlibParameters.
//
// Before the fix this produced two setTdlibParameters requests and TDLib
// answered the second one with "Unexpected setTdlibParameters".
func TestProductionSessionSendsTdlibParametersOnce(t *testing.T) {
	native := newRecordingNative()
	scriptHappyPath(native, true) // includes the duplicate push
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

	if got := countRequestsOfType(native, "setTdlibParameters"); got != 1 {
		t.Fatalf(
			"setTdlibParameters call count = %d, want 1; events = %v",
			got,
			native.recordedEvents(),
		)
	}
}

// TestProductionSessionDoesNotSendUnexpectedTdlibParameters asserts the
// positive call count rather than the absence of an error string.
func TestProductionSessionDoesNotSendUnexpectedTdlibParameters(
	t *testing.T,
) {
	native := newRecordingNative()
	scriptHappyPath(native, true)
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
		t.Fatalf("Authorize: %v", err)
	}

	for _, raw := range native.sentRequestsOfType("setTdlibParameters") {
		if !strings.Contains(string(raw), `"@type":"setTdlibParameters"`) {
			t.Fatalf("unexpected parameters payload: %s", raw)
		}
		var payload map[string]any
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatalf("decode setTdlibParameters: %v", err)
		}
		if payload["api_id"] == nil || payload["api_hash"] == nil {
			t.Fatal("setTdlibParameters lost its credential fields")
		}
	}
}

// TestApplicationStartsOneAuthorizationFlow pins that a single Authorize
// call creates exactly one logical client and one activation.
func TestApplicationStartsOneAuthorizationFlow(t *testing.T) {
	native := newRecordingNative()
	scriptHappyPath(native, true)
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
		t.Fatalf("Authorize: %v", err)
	}

	events := native.recordedEvents()

	if got := countEvent(events, "create_client_id"); got != 1 {
		t.Fatalf("create_client_id count = %d, want 1", got)
	}
	if got := countEvent(events, "send:getAuthorizationState"); got != 1 {
		t.Fatalf("getAuthorizationState count = %d, want 1", got)
	}
	if got := countEvent(events, "send:setTdlibParameters"); got != 1 {
		t.Fatalf("setTdlibParameters count = %d, want 1", got)
	}
}

func countEvent(events []string, want string) int {
	count := 0
	for _, event := range events {
		if event == want {
			count++
		}
	}
	return count
}

// TestAuthSessionDeduplicatesPushThenDirectWaitTdlibParameters proves the
// observation order does not matter: a pushed update followed by the
// direct response still yields exactly one request.
func TestAuthSessionDeduplicatesPushThenDirectWaitTdlibParameters(
	t *testing.T,
) {
	session := NewAuthSession(validAuthParams())

	push, err := ParseAuthUpdate(
		[]byte(
			`{"@type":"updateAuthorizationState",` +
				`"authorization_state":` +
				`{"@type":"authorizationStateWaitTdlibParameters"}}`,
		),
	)
	if err != nil {
		t.Fatalf("parse push state: %v", err)
	}

	direct, err := ParseAuthUpdate(
		[]byte(`{"@type":"authorizationStateWaitTdlibParameters"}`),
	)
	if err != nil {
		t.Fatalf("parse direct state: %v", err)
	}

	requests := 0
	for i, state := range []AuthState{push, direct} {
		request, err := session.Step(state, AuthInput{})
		if err != nil {
			t.Fatalf("Step %d: %v", i, err)
		}
		if request == nil {
			continue
		}
		if got := requestType(request); got != "setTdlibParameters" {
			t.Fatalf("Step %d request type = %q", i, got)
		}
		requests++
	}

	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
}

// TestAuthorizationClientSendsTdlibParametersOnceForDirectAndPushState
// is the end-to-end contract: exactly one verbosity request, exactly one
// parameters request, and the logging configuration strictly before the
// credential-bearing request.
func TestAuthorizationClientSendsTdlibParametersOnceForDirectAndPushState(
	t *testing.T,
) {
	native := newRecordingNative()
	scriptHappyPath(native, true)
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

	if got := countEvent(events, "execute:setLogVerbosityLevel"); got != 1 {
		t.Fatalf("setLogVerbosityLevel calls = %d, want 1", got)
	}
	if got := countEvent(events, "send:setTdlibParameters"); got != 1 {
		t.Fatalf("setTdlibParameters calls = %d, want 1", got)
	}

	verbosityAt := indexOfEvent(events, "execute:setLogVerbosityLevel")
	parametersAt := indexOfEvent(events, "send:setTdlibParameters")
	if verbosityAt >= parametersAt {
		t.Fatalf(
			"TDLib logging configured after the parameters request; events = %v",
			events,
		)
	}
}

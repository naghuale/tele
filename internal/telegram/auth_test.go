package telegram

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestAuthStateValid(t *testing.T) {
	valid := []AuthState{
		AuthStateClosed, AuthStateClosing,
		AuthStateWaitTdlibParameters,
		AuthStateWaitPhoneNumber, AuthStateWaitCode, AuthStateWaitPassword,
		AuthStateWaitEmailAddress, AuthStateWaitEmailCode,
		AuthStateWaitOtherDevice, AuthStateWaitRegistration,
		AuthStateWaitPremiumPurchase,
		AuthStateReady, AuthStateLoggingOut, AuthStateUnknown,
	}
	for _, s := range valid {
		if !s.Valid() {
			t.Fatalf("%q should be valid", s)
		}
		if s.String() != string(s) {
			t.Fatalf("String mismatch for %q", s)
		}
	}
	if AuthState("bogus").Valid() {
		t.Fatal("unknown AuthState must be invalid")
	}
}

func TestParseAuthUpdateAllKnownStates(t *testing.T) {
	cases := []struct {
		tdlib string
		want  AuthState
	}{
		{"authorizationStateClosed", AuthStateClosed},
		{"authorizationStateClosing", AuthStateClosing},
		{"authorizationStateWaitTdlibParameters", AuthStateWaitTdlibParameters},
		{"authorizationStateWaitPhoneNumber", AuthStateWaitPhoneNumber},
		{"authorizationStateWaitCode", AuthStateWaitCode},
		{"authorizationStateWaitPassword", AuthStateWaitPassword},
		{"authorizationStateWaitEmailAddress", AuthStateWaitEmailAddress},
		{"authorizationStateWaitEmailCode", AuthStateWaitEmailCode},
		{"authorizationStateWaitOtherDeviceConfirmation", AuthStateWaitOtherDevice},
		{"authorizationStateWaitRegistration", AuthStateWaitRegistration},
		{"authorizationStateWaitPremiumPurchase", AuthStateWaitPremiumPurchase},
		{"authorizationStateReady", AuthStateReady},
		{"authorizationStateLoggingOut", AuthStateLoggingOut},
	}
	for _, c := range cases {
		raw := []byte(`{"@type":"updateAuthorizationState","authorization_state":{"@type":"` + c.tdlib + `"}}`)
		got, err := ParseAuthUpdate(raw)
		if err != nil {
			t.Fatalf("ParseAuthUpdate(%s): %v", c.tdlib, err)
		}
		if got != c.want {
			t.Fatalf("ParseAuthUpdate(%s) = %q, want %q", c.tdlib, got, c.want)
		}
	}
}

func TestParseAuthUpdateNonAuth(t *testing.T) {
	got, err := ParseAuthUpdate([]byte(`{"@type":"updateNewChat"}`))
	if err != nil {
		t.Fatalf("ParseAuthUpdate: %v", err)
	}
	if got != AuthStateUnknown {
		t.Fatalf("got = %q, want AuthStateUnknown", got)
	}
}

func TestParseAuthUpdateUnknownState(t *testing.T) {
	raw := []byte(
		`{"@type":"updateAuthorizationState",` +
			`"authorization_state":{` +
			`"@type":"authorizationStateFuture"}}`,
	)

	got, err := ParseAuthUpdate(raw)
	if got != AuthStateUnknown {
		t.Fatalf("state = %q, want AuthStateUnknown", got)
	}
	if !errors.Is(err, ErrUnsupportedAuthState) {
		t.Fatalf("error = %v, want ErrUnsupportedAuthState", err)
	}
}

func TestParseAuthUpdateRejectsMalformed(t *testing.T) {
	if _, err := ParseAuthUpdate([]byte(`not json`)); err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestParseAuthUpdateRejectsMissingEmbeddedState(t *testing.T) {
	state, err := ParseAuthUpdate(
		[]byte(`{"@type":"updateAuthorizationState"}`),
	)
	if state != AuthStateUnknown {
		t.Fatalf("state = %s, want AuthStateUnknown", state)
	}
	if err == nil {
		t.Fatal("expected error for missing authorization_state")
	}
}

// ---- direct authorization-state response ----

func TestParseAuthUpdateDirectAuthorizationState(t *testing.T) {
	raw := []byte(`{"@type":"authorizationStateWaitTdlibParameters"}`)

	state, err := ParseAuthUpdate(raw)
	if err != nil {
		t.Fatalf("ParseAuthUpdate() error = %v", err)
	}
	if state != AuthStateWaitTdlibParameters {
		t.Fatalf("state = %s, want %s", state, AuthStateWaitTdlibParameters)
	}
}

func TestParseAuthUpdateDirectReady(t *testing.T) {
	raw := []byte(`{"@type":"authorizationStateReady"}`)

	state, err := ParseAuthUpdate(raw)
	if err != nil {
		t.Fatalf("ParseAuthUpdate() error = %v", err)
	}
	if state != AuthStateReady {
		t.Fatalf("state = %s, want %s", state, AuthStateReady)
	}
}

func TestParseAuthUpdateDirectUnknownState(t *testing.T) {
	raw := []byte(`{"@type":"authorizationStateFuture"}`)

	state, err := ParseAuthUpdate(raw)
	if state != AuthStateUnknown {
		t.Fatalf("state = %s, want AuthStateUnknown", state)
	}
	if !errors.Is(err, ErrUnsupportedAuthState) {
		t.Fatalf("error = %v, want ErrUnsupportedAuthState", err)
	}
}

func TestParseAuthUpdateNonAuthStillReturnsUnknownNil(t *testing.T) {
	raw := []byte(`{"@type":"updateNewChat"}`)

	state, err := ParseAuthUpdate(raw)
	if err != nil {
		t.Fatalf("ParseAuthUpdate() error = %v", err)
	}
	if state != AuthStateUnknown {
		t.Fatalf("state = %s, want AuthStateUnknown", state)
	}
}

// ---- AuthSession ----

func TestAuthSessionStepWaitTdlibParameters(t *testing.T) {
	params := TdlibParameters{
		APIID:              123,
		APIHash:            "hash",
		DatabaseDirectory:  "/db",
		FilesDirectory:     "/f",
		SystemLanguageCode: "en",
		DeviceModel:        "m",
		SystemVersion:      "v",
		ApplicationVersion: "a",
	}
	s := NewAuthSession(params)

	req, err := s.Step(AuthStateWaitTdlibParameters, AuthInput{})
	if err != nil {
		t.Fatalf("Step: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(req, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded["@type"] != "setTdlibParameters" {
		t.Fatalf("@type = %v", decoded["@type"])
	}
	if decoded["use_file_database"] != true {
		t.Fatalf("use_file_database = %v", decoded["use_file_database"])
	}
	if decoded["use_message_database"] != true {
		t.Fatalf("use_message_database = %v", decoded["use_message_database"])
	}
}

func TestAuthSessionStepPhone(t *testing.T) {
	params := TdlibParameters{
		APIID: 1, APIHash: "h",
		DatabaseDirectory: "/d", FilesDirectory: "/f",
		SystemLanguageCode: "en", DeviceModel: "m", ApplicationVersion: "a",
	}
	s := NewAuthSession(params)

	req, err := s.Step(AuthStateWaitPhoneNumber, AuthInput{})
	if err != nil {
		t.Fatalf("Step: %v", err)
	}
	if req != nil {
		t.Fatal("no input -> no request")
	}
	if !s.NeedsInput() {
		t.Fatal("wait_phone_number should need input")
	}

	req, err = s.Step(AuthStateWaitPhoneNumber, AuthInput{PhoneNumber: "+15551234"})
	if err != nil {
		t.Fatalf("Step: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(req, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded["@type"] != "setAuthenticationPhoneNumber" {
		t.Fatalf("@type = %v", decoded["@type"])
	}
	if decoded["phone_number"] != "+15551234" {
		t.Fatalf("phone_number = %v", decoded["phone_number"])
	}
}

func TestAuthSessionStepCode(t *testing.T) {
	s := NewAuthSession(TdlibParameters{})
	req, err := s.Step(AuthStateWaitCode, AuthInput{Code: "12345"})
	if err != nil {
		t.Fatalf("Step: %v", err)
	}
	var decoded map[string]any
	_ = json.Unmarshal(req, &decoded)
	if decoded["@type"] != "checkAuthenticationCode" || decoded["code"] != "12345" {
		t.Fatalf("decoded = %v", decoded)
	}
}

func TestAuthSessionStepPassword(t *testing.T) {
	s := NewAuthSession(TdlibParameters{})
	req, err := s.Step(AuthStateWaitPassword, AuthInput{Password: "secret"})
	if err != nil {
		t.Fatalf("Step: %v", err)
	}
	var decoded map[string]any
	_ = json.Unmarshal(req, &decoded)
	if decoded["@type"] != "checkAuthenticationPassword" {
		t.Fatalf("@type = %v", decoded["@type"])
	}
}

func TestAuthSessionUnsupportedStates(t *testing.T) {
	states := []AuthState{
		AuthStateWaitEmailAddress,
		AuthStateWaitEmailCode,
		AuthStateWaitOtherDevice,
		AuthStateWaitRegistration,
		AuthStateWaitPremiumPurchase,
	}
	for _, st := range states {
		s := NewAuthSession(TdlibParameters{})
		_, err := s.Step(st, AuthInput{})
		if err == nil {
			t.Fatalf("state %s: expected error", st)
		}
		if !errors.Is(err, ErrUnsupportedAuthState) {
			t.Fatalf("state %s: err = %v, want ErrUnsupportedAuthState", st, err)
		}
	}
}

func TestAuthSessionReady(t *testing.T) {
	s := NewAuthSession(TdlibParameters{})
	req, err := s.Step(AuthStateReady, AuthInput{})
	if err != nil {
		t.Fatalf("Step: %v", err)
	}
	if req != nil {
		t.Fatal("Ready must not produce a request")
	}
	if !s.Done() {
		t.Fatal("session should be Done after Ready")
	}
	if s.NeedsInput() {
		t.Fatal("Ready must not need input")
	}
}

func TestAuthSessionClosedAndClosingNoRequests(t *testing.T) {
	s := NewAuthSession(TdlibParameters{})
	for _, st := range []AuthState{
		AuthStateClosed, AuthStateClosing, AuthStateLoggingOut,
	} {
		req, err := s.Step(st, AuthInput{})
		if err != nil {
			t.Fatalf("Step(%s): %v", st, err)
		}
		if req != nil {
			t.Fatalf("Step(%s): expected no request", st)
		}
	}
}

func TestBuildSetTdlibParametersValidation(t *testing.T) {
	if _, err := buildSetTdlibParameters(TdlibParameters{}); !errors.Is(err, ErrInvalidAuthParameters) {
		t.Fatalf("empty: err = %v", err)
	}
	if _, err := buildSetTdlibParameters(TdlibParameters{APIID: 1}); !errors.Is(err, ErrInvalidAuthParameters) {
		t.Fatalf("missing hash: err = %v", err)
	}
	partial := TdlibParameters{
		APIID: 1, APIHash: "h",
		DatabaseDirectory: "/d", FilesDirectory: "/f",
	}
	if _, err := buildSetTdlibParameters(partial); !errors.Is(err, ErrInvalidAuthParameters) {
		t.Fatalf("missing language/device/app: err = %v", err)
	}
	full := TdlibParameters{
		APIID: 1, APIHash: "h",
		DatabaseDirectory: "/d", FilesDirectory: "/f",
		SystemLanguageCode: "en", DeviceModel: "m", ApplicationVersion: "a",
	}
	if _, err := buildSetTdlibParameters(full); err != nil {
		t.Fatalf("full: err = %v", err)
	}
}

func TestSetTdlibParametersEncodesEncryptionKey(t *testing.T) {
	params := TdlibParameters{
		APIID:                 1,
		APIHash:               "hash",
		DatabaseDirectory:     "/database",
		FilesDirectory:        "/files",
		DatabaseEncryptionKey: []byte{1, 2, 3},
		SystemLanguageCode:    "en",
		DeviceModel:           "test",
		ApplicationVersion:    "dev",
	}

	request, err := buildSetTdlibParameters(params)
	if err != nil {
		t.Fatalf("buildSetTdlibParameters() error = %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(request, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if got := decoded["database_encryption_key"]; got != "AQID" {
		t.Fatalf("database_encryption_key = %v, want AQID", got)
	}
}

func TestSetAuthenticationPhoneNumberHasNullSettings(t *testing.T) {
	request, err := buildSetPhoneNumber("+15551234")
	if err != nil {
		t.Fatalf("buildSetPhoneNumber() error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(request, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	settings, exists := decoded["settings"]
	if !exists {
		t.Fatal("settings field is missing")
	}
	if settings != nil {
		t.Fatalf("settings = %v, want null", settings)
	}
}

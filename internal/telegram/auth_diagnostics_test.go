package telegram

import (
	"bytes"
	"strings"
	"testing"
)

// The diagnostics exist to make a stuck handshake observable, so the one
// thing they must never do is widen what the process writes. Every test
// here asserts on absence: that a credential placed in a real request
// cannot reach the trace, and that an untrusted label is replaced rather
// than formatted.

func TestAuthDiagnosticsReportsStateWithoutPayload(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer

	diagnostics := NewWriterAuthDiagnostics(&output)

	diagnostics.State(AuthDiagnosticStateWaitPhoneNumber)
	diagnostics.State(AuthDiagnosticStateWaitCode)

	got := output.String()

	for _, want := range []string{
		"telecli auth trace: state=wait_phone_number\n",
		"telecli auth trace: state=wait_code\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("output = %q, want substring %q", got, want)
		}
	}
}

func TestAuthDiagnosticsReportsRequestTypeWithoutPayload(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer

	diagnostics := NewWriterAuthDiagnostics(&output)

	diagnostics.Request(1, AuthDiagnosticRequestSetPhoneNumber)
	diagnostics.Result(
		1,
		AuthDiagnosticRequestSetPhoneNumber,
		AuthDiagnosticResultSubmitted,
		0,
	)

	got := output.String()

	for _, want := range []string{
		"telecli auth trace: request_id=1 " +
			"request=set_authentication_phone_number\n",
		"telecli auth trace: request_id=1 " +
			"request=set_authentication_phone_number result=submitted\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("output = %q, want substring %q", got, want)
		}
	}
}

// TestAuthDiagnosticsSanitizesUnknownLabels is the guard against a future
// caller smuggling a value through the label type.
func TestAuthDiagnosticsSanitizesUnknownLabels(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer

	diagnostics := NewWriterAuthDiagnostics(&output)

	diagnostics.State(
		AuthDiagnosticState("unsafe-state\napi_hash=secret"),
	)
	diagnostics.Request(
		1,
		AuthDiagnosticRequest("unsafe-request\nphone=secret"),
	)
	diagnostics.Result(
		1,
		AuthDiagnosticRequest("unsafe-request"),
		AuthDiagnosticResult("unsafe-result\npassword=secret"),
		0,
	)

	got := output.String()

	if strings.Contains(got, "secret") {
		t.Fatalf("diagnostics exposed an untrusted value: %q", got)
	}

	if strings.Contains(got, "unsafe") {
		t.Fatalf("diagnostics kept an untrusted label: %q", got)
	}

	for _, want := range []string{
		"state=other",
		"request=other",
		"request=other result=error",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("output = %q, want substring %q", got, want)
		}
	}
}

// TestAuthDiagnosticsDoesNotExposeAPIHash places a real API hash in a real
// setTdlibParameters request and proves only the type survives.
func TestAuthDiagnosticsDoesNotExposeAPIHash(t *testing.T) {
	t.Parallel()

	const apiHash = "h7-sensitive-api-hash-value"

	var output bytes.Buffer

	NewWriterAuthDiagnostics(&output).Request(
		1,
		authDiagnosticRequest(RawMessage(
			`{"@type":"setTdlibParameters","api_hash":"`+apiHash+`"}`,
		)),
	)

	got := output.String()

	if strings.Contains(got, apiHash) {
		t.Fatal("diagnostics exposed the API hash")
	}

	if strings.Contains(got, "api_hash") {
		t.Fatal("diagnostics exposed the raw request field name")
	}
}

func TestAuthDiagnosticsDoesNotExposePhone(t *testing.T) {
	t.Parallel()

	const phone = "+15550009999"

	var output bytes.Buffer

	NewWriterAuthDiagnostics(&output).Request(
		1,
		authDiagnosticRequest(RawMessage(
			`{"@type":"setAuthenticationPhoneNumber","phone_number":"`+
				phone+`"}`,
		)),
	)

	got := output.String()

	if strings.Contains(got, phone) {
		t.Fatal("diagnostics exposed the phone number")
	}

	// The label itself is the request type and legitimately contains the
	// word, so the field is checked in its raw form: name, separator and
	// value together.
	if strings.Contains(got, `"phone_number":"`) {
		t.Fatalf("diagnostics exposed the raw request field: %q", got)
	}

	if !strings.Contains(got, "request=set_authentication_phone_number") {
		t.Fatalf("diagnostics dropped the request label: %q", got)
	}
}

func TestAuthDiagnosticsDoesNotExposeCode(t *testing.T) {
	t.Parallel()

	const code = "123456"

	var output bytes.Buffer

	NewWriterAuthDiagnostics(&output).Request(
		1,
		authDiagnosticRequest(RawMessage(
			`{"@type":"checkAuthenticationCode","code":"`+code+`"}`,
		)),
	)

	got := output.String()

	if strings.Contains(got, code) {
		t.Fatal("diagnostics exposed the authentication code")
	}
}

func TestAuthDiagnosticsDoesNotExposePassword(t *testing.T) {
	t.Parallel()

	const password = "sensitive-2fa-password"

	var output bytes.Buffer

	NewWriterAuthDiagnostics(&output).Request(
		1,
		authDiagnosticRequest(RawMessage(
			`{"@type":"checkAuthenticationPassword","password":"`+
				password+`"}`,
		)),
	)

	got := output.String()

	if strings.Contains(got, password) {
		t.Fatal("diagnostics exposed the two-factor password")
	}
}

// TestAuthDiagnosticsDoesNotExposeRawJSON covers a nested payload, which a
// naive projection of the whole document would leak.
func TestAuthDiagnosticsDoesNotExposeRawJSON(t *testing.T) {
	t.Parallel()

	request := RawMessage(
		`{"@type":"checkAuthenticationCode","code":"123456",` +
			`"@extra":{"note":"raw-json-marker"}}`,
	)

	var output bytes.Buffer

	NewWriterAuthDiagnostics(&output).Request(
		1,
		authDiagnosticRequest(request),
	)

	got := output.String()

	if strings.Contains(got, "raw-json-marker") {
		t.Fatal("diagnostics exposed a nested payload value")
	}

	if strings.Contains(got, string(request)) {
		t.Fatal("diagnostics included the raw request")
	}
}

// TestAuthDiagnosticRequestIgnoresExtra checks that a user-controlled
// "@extra" cannot influence the label.
func TestAuthDiagnosticRequestIgnoresExtra(t *testing.T) {
	t.Parallel()

	got := authDiagnosticRequest(RawMessage(
		`{"@type":"setAuthenticationPhoneNumber",` +
			`"phone_number":"+15550009999",` +
			`"@extra":{"api_hash":"h7-sensitive"}}`,
	))

	if got != AuthDiagnosticRequestSetPhoneNumber {
		t.Fatalf(
			"authDiagnosticRequest() = %q, want %q",
			got,
			AuthDiagnosticRequestSetPhoneNumber,
		)
	}
}

func TestAuthDiagnosticRequestRejectsMalformedJSON(t *testing.T) {
	t.Parallel()

	if got := authDiagnosticRequest(RawMessage(`{"@type":`)); got !=
		AuthDiagnosticRequestOther {
		t.Fatalf("authDiagnosticRequest() = %q, want other", got)
	}
}

func TestAuthDiagnosticStateCollapsesUnhandledStates(t *testing.T) {
	t.Parallel()

	for _, state := range []AuthState{
		AuthStateWaitEmailAddress,
		AuthStateWaitEmailCode,
		AuthStateWaitRegistration,
		AuthStateWaitPremiumPurchase,
		AuthStateUnknown,
	} {
		label, ok := authDiagnosticStateKnown(state)
		if ok {
			t.Fatalf(
				"authDiagnosticStateKnown(%q) = %q, want not reportable",
				state,
				label,
			)
		}
		if label != AuthDiagnosticStateOther {
			t.Fatalf("unhandled state %q produced %q", state, label)
		}
	}

	if label, ok := authDiagnosticStateKnown(AuthStateWaitCode); !ok ||
		label != AuthDiagnosticStateWaitCode {
		t.Fatalf("authDiagnosticStateKnown(wait_code) = %q, %v", label, ok)
	}
}

func TestEnvironmentAuthDiagnosticsDisabledByDefault(t *testing.T) {
	t.Setenv(authTraceEnvironment, "")

	var output bytes.Buffer

	NewEnvironmentAuthDiagnostics(&output).State(
		AuthDiagnosticStateWaitCode,
	)

	if output.Len() != 0 {
		t.Fatalf(
			"disabled diagnostics wrote %q, want nothing",
			output.String(),
		)
	}
}

func TestEnvironmentAuthDiagnosticsEnabledExplicitly(t *testing.T) {
	t.Setenv(authTraceEnvironment, "1")

	var output bytes.Buffer

	NewEnvironmentAuthDiagnostics(&output).State(
		AuthDiagnosticStateWaitCode,
	)

	want := "telecli auth trace: state=wait_code\n"
	if got := output.String(); got != want {
		t.Fatalf("enabled diagnostics wrote %q, want %q", got, want)
	}
}

// TestAuthDiagnosticsWriterIsConcurrencySafe matters because the trace is
// read from a file while the handshake runs.
func TestAuthDiagnosticsWriterIsConcurrencySafe(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer

	diagnostics := NewWriterAuthDiagnostics(&output)

	const rounds = 50

	done := make(chan struct{})

	for worker := 0; worker < 4; worker++ {
		go func() {
			defer func() { done <- struct{}{} }()

			for i := 0; i < rounds; i++ {
				diagnostics.State(AuthDiagnosticStateWaitCode)
				diagnostics.Request(1, AuthDiagnosticRequestCheckCode)
			}
		}()
	}

	for worker := 0; worker < 4; worker++ {
		<-done
	}

	if got := strings.Count(output.String(), "\n"); got != 4*rounds*2 {
		t.Fatalf("wrote %d lines, want %d", got, 4*rounds*2)
	}
}

// TestAuthDiagnosticEnvelopeClassifiesErrorWithoutMessage pins the
// property the next smoke run depends on: a refusal is visible through
// its numeric code while the message, which carries the rejected value,
// never reaches the trace.
func TestAuthDiagnosticEnvelopeClassifiesErrorWithoutMessage(t *testing.T) {
	t.Parallel()

	const secretMessage = "PHONE_NUMBER_INVALID +15550009999"

	message := RawMessage(
		`{"@type":"error","code":400,"message":"` + secretMessage + `"}`,
	)

	envelope, code := classifyAuthDiagnosticEnvelope(message)

	if envelope != AuthDiagnosticEnvelopeError {
		t.Fatalf("envelope = %q, want error", envelope)
	}

	if code != 400 {
		t.Fatalf("code = %d, want 400", code)
	}

	var output bytes.Buffer

	NewWriterAuthDiagnostics(&output).Envelope(envelope, code)

	got := output.String()

	if want := "telecli auth trace: envelope=error code=400\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}

	if strings.Contains(got, secretMessage) {
		t.Fatal("the error message leaked into the trace")
	}
}

func TestAuthDiagnosticEnvelopeClassifiesOK(t *testing.T) {
	t.Parallel()

	envelope, code := classifyAuthDiagnosticEnvelope(
		RawMessage(`{"@type":"ok"}`),
	)

	if envelope != AuthDiagnosticEnvelopeOK {
		t.Fatalf("envelope = %q, want ok", envelope)
	}

	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}

	var output bytes.Buffer

	NewWriterAuthDiagnostics(&output).Envelope(envelope, code)

	if want := "telecli auth trace: envelope=ok\n"; output.String() != want {
		t.Fatalf("output = %q, want %q", output.String(), want)
	}
}

// TestAuthDiagnosticEnvelopeDoesNotExposeNestedSecrets proves a nested
// authorization_state object cannot leak even when the classifier only
// looks at the top-level type.
func TestAuthDiagnosticEnvelopeDoesNotExposeNestedSecrets(t *testing.T) {
	t.Parallel()

	const (
		apiHash  = "h7-envelope-secret"
		phone    = "+15550009999"
		code     = "123456"
		password = "secret-password"
	)

	message := RawMessage(
		`{"@type":"updateAuthorizationState","authorization_state":{` +
			`"@type":"authorizationStateWaitCode",` +
			`"api_hash":"` + apiHash + `",` +
			`"phone_number":"` + phone + `",` +
			`"code":"` + code + `",` +
			`"password":"` + password + `"}}`,
	)

	envelope, errorCode := classifyAuthDiagnosticEnvelope(message)

	if envelope != AuthDiagnosticEnvelopeAuthorizationUpdate {
		t.Fatalf("envelope = %q, want update_authorization_state", envelope)
	}

	var output bytes.Buffer

	NewWriterAuthDiagnostics(&output).Envelope(envelope, errorCode)

	got := output.String()

	for _, secret := range []string{apiHash, phone, code, password} {
		if strings.Contains(got, secret) {
			t.Fatalf("diagnostics exposed %q:\n%s", secret, got)
		}
	}
}

// TestUnknownAuthStatesAreNotEmitted keeps the noise out: a state the
// product does not act on must not produce a line at all.
func TestUnknownAuthStatesAreNotEmitted(t *testing.T) {
	t.Parallel()

	for _, state := range []AuthState{
		AuthStateUnknown,
		AuthStateWaitEmailAddress,
		AuthStateWaitEmailCode,
		AuthStateWaitRegistration,
		AuthStateWaitPremiumPurchase,
		AuthStateWaitOtherDevice,
		AuthStateLoggingOut,
	} {
		if label, ok := authDiagnosticStateKnown(state); ok {
			t.Fatalf("%q was emitted as %q", state, label)
		}
	}
}

// TestUnrelatedEnvelopesAreNotEmitted is the noise test for the wire: the
// update stream carries many objects that say nothing about authorization.
func TestUnrelatedEnvelopesAreNotEmitted(t *testing.T) {
	t.Parallel()

	for _, message := range []string{
		`{"@type":"updateOption"}`,
		`{"@type":"updateNewChat"}`,
		`{"@type":"updateUser"}`,
		`{"@type":"updateChatPosition"}`,
		`not json at all`,
	} {
		envelope, code := classifyAuthDiagnosticEnvelope(
			RawMessage(message),
		)

		if envelope != AuthDiagnosticEnvelopeOther {
			t.Fatalf("%s classified as %q", message, envelope)
		}

		if code != 0 {
			t.Fatalf("%s produced code %d", message, code)
		}

		if authDiagnosticEnvelopeReportable(envelope) {
			t.Fatalf("%s was treated as reportable", message)
		}
	}
}

// TestAuthDiagnosticErrorCodeSanitizer rejects a code that could not have
// come from TDLib, so a malformed message cannot print a strange value.
func TestAuthDiagnosticErrorCodeSanitizer(t *testing.T) {
	t.Parallel()

	for _, bad := range []int{-1, 10000, 1 << 30} {
		if got := sanitizeAuthDiagnosticErrorCode(bad); got != 0 {
			t.Fatalf("sanitizeAuthDiagnosticErrorCode(%d) = %d, want 0", bad, got)
		}
	}

	for _, good := range []int{0, 400, 420, 429} {
		if got := sanitizeAuthDiagnosticErrorCode(good); got != good {
			t.Fatalf("sanitizeAuthDiagnosticErrorCode(%d) = %d", good, got)
		}
	}
}

// TestAuthDiagnosticEnvelopeSanitizesUnknownLabels keeps a future caller
// from smuggling a value through the envelope type.
func TestAuthDiagnosticEnvelopeSanitizesUnknownLabels(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer

	NewWriterAuthDiagnostics(&output).Envelope(
		AuthDiagnosticEnvelope("update_option\nphone=+15550009999"),
		0,
	)

	got := output.String()

	if strings.Contains(got, "15550009999") {
		t.Fatalf("an untrusted envelope label was formatted: %q", got)
	}

	if want := "telecli auth trace: envelope=other\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

// TestAuthDiagnosticEnvelopeIgnoresExtraOnAnOK proves the privacy property
// holds for the message that matters most: an "ok" is reported, and the
// user-controlled @extra riding alongside it is not.
func TestAuthDiagnosticEnvelopeIgnoresExtraOnAnOK(t *testing.T) {
	t.Parallel()

	const phone = "+15550009999"

	envelope, code := classifyAuthDiagnosticEnvelope(
		RawMessage(`{"@type":"ok","@extra":{"phone":"` + phone + `"}}`),
	)

	if envelope != AuthDiagnosticEnvelopeOK {
		t.Fatalf("envelope = %q, want ok", envelope)
	}

	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}

	var output bytes.Buffer

	NewWriterAuthDiagnostics(&output).Envelope(envelope, code)

	got := output.String()

	if strings.Contains(got, phone) {
		t.Fatalf("the @extra value leaked: %q", got)
	}

	if want := "telecli auth trace: envelope=ok\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

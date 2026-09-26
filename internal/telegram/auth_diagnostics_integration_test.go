package telegram

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// The unit tests prove the diagnostic sink is safe in isolation. This
// test proves it end to end: a full authorization handshake runs through
// the real coordinator with a credential planted in every field that
// carries one, and the trace must still contain nothing but labels.
//
// It reuses the existing fake sender, client and provider rather than
// standing up a second coordinator harness.

const (
	traceAPIHash   = "h7-trace-api-hash-marker"
	tracePhone     = "+15550009999"
	traceCode      = "654321"
	tracePassword  = "trace-2fa-password-marker"
	traceDataRoot  = "/tmp/telecli-trace-marker"
	traceKeyMarker = "trace-database-key-marker"
)

// traceAuthParams plants a recognisable API hash, data root and database
// key, so a leak of any of them is detectable.
func traceAuthParams() TdlibParameters {
	return TdlibParameters{
		APIID:                 123456,
		APIHash:               traceAPIHash,
		DatabaseDirectory:     traceDataRoot + "/database",
		FilesDirectory:        traceDataRoot + "/files",
		DatabaseEncryptionKey: []byte(traceKeyMarker),
		SystemLanguageCode:    "en",
		DeviceModel:           "telecli",
		SystemVersion:         "macOS",
		ApplicationVersion:    "0.0.0-trace",
	}
}

func TestAuthCoordinatorDiagnosticsDoNotExposeCredentials(t *testing.T) {
	t.Parallel()

	var trace bytes.Buffer

	sender := &fakeSender{}
	client := newTestClient(t)
	provider := &fakeProvider{
		phone:    tracePhone,
		code:     traceCode,
		password: tracePassword,
	}

	feedAuth(t, client,
		"authorizationStateWaitTdlibParameters",
		"authorizationStateWaitPhoneNumber",
		"authorizationStateWaitCode",
		"authorizationStateWaitPassword",
		"authorizationStateReady",
	)

	// The same credentials the coordinator will really use, so the trace
	// is produced from a run that carried every secret.
	params := traceAuthParams()

	result, err := RunAuthWithClientDiagnostics(
		context.Background(),
		sender,
		client,
		params,
		provider,
		NewWriterAuthDiagnostics(&trace),
	)
	if err != nil {
		t.Fatalf("RunAuthWithClientDiagnostics: %v", err)
	}

	if result.State != AuthStateReady {
		t.Fatalf("result.State = %q, want ready", result.State)
	}

	got := trace.String()

	for _, secret := range []string{
		traceAPIHash,
		tracePhone,
		traceCode,
		tracePassword,
		traceDataRoot,
		traceKeyMarker,
	} {
		if strings.Contains(got, secret) {
			t.Fatalf("trace exposed %q:\n%s", secret, got)
		}
	}

	// The handshake must still be legible, otherwise the trace is
	// useless for the purpose it was added for.
	for _, want := range []string{
		"state=wait_tdlib_parameters",
		"request=set_tdlib_parameters",
		"state=wait_phone_number",
		"request=set_authentication_phone_number",
		"state=wait_code",
		"request=check_authentication_code",
		"state=wait_password",
		"request=check_authentication_password",
		"state=ready",
		"result=submitted",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("trace = %q, want substring %q", got, want)
		}
	}
}

// TestAuthCoordinatorDiagnosticsReportThePhoneBoundary is the specific
// sequence the packaged smoke needs to be able to read: the state before
// the phone request, the request itself, its local outcome, and the state
// that follows.
func TestAuthCoordinatorDiagnosticsReportThePhoneBoundary(t *testing.T) {
	t.Parallel()

	var trace bytes.Buffer

	sender := &fakeSender{}
	client := newTestClient(t)
	provider := &fakeProvider{
		phone:    tracePhone,
		code:     traceCode,
		password: tracePassword,
	}

	feedAuth(t, client,
		"authorizationStateWaitPhoneNumber",
		"authorizationStateWaitCode",
		"authorizationStateReady",
	)

	if _, err := RunAuthWithClientDiagnostics(
		context.Background(),
		sender,
		client,
		traceAuthParams(),
		provider,
		NewWriterAuthDiagnostics(&trace),
	); err != nil {
		t.Fatalf("RunAuthWithClientDiagnostics: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(trace.String()), "\n")

	want := []string{
		"telecli auth trace: envelope=update_authorization_state",
		"telecli auth trace: state=wait_phone_number",
		"telecli auth trace: request_id=1 " +
			"request=set_authentication_phone_number",
		"telecli auth trace: request_id=1 request=" +
			"set_authentication_phone_number result=submitted",
		"telecli auth trace: envelope=update_authorization_state",
		"telecli auth trace: state=wait_code",
	}

	if len(lines) < len(want) {
		t.Fatalf("trace = %q, want at least %d lines", trace.String(), len(want))
	}

	for i, line := range want {
		if lines[i] != line {
			t.Fatalf("line[%d] = %q, want %q", i, lines[i], line)
		}
	}
}

// TestAuthCoordinatorDiagnosticsReportAFailingSend proves the outcome label
// distinguishes a rejected request, which is the other half of the
// boundary the smoke has to read.
// errTraceSend stands in for a native bridge that refuses the request.
var errTraceSend = errors.New("trace send failure")

func TestAuthCoordinatorDiagnosticsReportAFailingSend(t *testing.T) {
	t.Parallel()

	var trace bytes.Buffer

	sender := &fakeSender{err: errTraceSend}
	client := newTestClient(t)
	provider := &fakeProvider{phone: tracePhone}

	feedAuth(t, client, "authorizationStateWaitPhoneNumber")

	if _, err := RunAuthWithClientDiagnostics(
		context.Background(),
		sender,
		client,
		traceAuthParams(),
		provider,
		NewWriterAuthDiagnostics(&trace),
	); err == nil {
		t.Fatal("a failing send must abort the handshake")
	}

	got := trace.String()

	if !strings.Contains(
		got,
		"request=set_authentication_phone_number result=error",
	) {
		t.Fatalf("trace = %q, want an error outcome for the phone request", got)
	}

	if strings.Contains(got, tracePhone) {
		t.Fatal("a failing send leaked the phone number into the trace")
	}
}

// TestAuthCoordinatorDiagnosticsIgnoreUnrelatedUpdates is the noise test at
// the level that matters. The update stream carries a large number of
// objects that say nothing about authorization; if the coordinator emitted
// a line for each, the two or three lines that matter would be unreadable.
func TestAuthCoordinatorDiagnosticsIgnoreUnrelatedUpdates(t *testing.T) {
	t.Parallel()

	var trace bytes.Buffer

	sender := &fakeSender{}
	client := newTestClient(t)
	provider := &fakeProvider{phone: tracePhone}

	// Unrelated traffic first, then the handshake that must be visible.
	for i := 0; i < 20; i++ {
		client.updates <- Update{
			ClientID: client.id,
			Raw:      []byte(`{"@type":"updateOption"}`),
		}
	}
	client.updates <- Update{
		ClientID: client.id,
		Raw:      []byte(`{"@type":"updateConnectionState"}`),
	}
	client.updates <- Update{
		ClientID: client.id,
		Raw: []byte(
			`{"@type":"updateNewChat","chat":{"id":1,"title":"x"}}`,
		),
	}
	client.updates <- Update{ClientID: client.id, Raw: []byte(`{"@type":"ok"}`)}

	feedAuth(t, client,
		"authorizationStateWaitPhoneNumber",
		"authorizationStateReady",
	)

	if _, err := RunAuthWithClientDiagnostics(
		context.Background(),
		sender,
		client,
		traceAuthParams(),
		provider,
		NewWriterAuthDiagnostics(&trace),
	); err != nil {
		t.Fatalf("RunAuthWithClientDiagnostics: %v", err)
	}

	got := trace.String()

	// Unrelated objects must contribute nothing at all.
	for _, unwanted := range []string{
		"envelope=other",
		"state=other",
		"updateOption",
		"updateNewChat",
	} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("trace contains %q:\n%s", unwanted, got)
		}
	}

	// The connection update and the direct answer are both reportable.
	for _, want := range []string{
		"envelope=update_connection_state",
		"envelope=ok",
		"state=wait_phone_number",
		"state=ready",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("trace = %q, want substring %q", got, want)
		}
	}

	// Twenty unrelated objects must not have produced twenty lines.
	lines := strings.Count(strings.TrimSpace(got), "\n") + 1
	if lines > 12 {
		t.Fatalf("trace has %d lines, unrelated updates leaked in:\n%s", lines, got)
	}
}

// answerSender answers every request with a direct reply that echoes the
// request's own correlation identifier, exactly as TDLib does.
type answerSender struct {
	mu      sync.Mutex
	client  *Client
	reply   func(id QueryID) RawMessage
	failing error
}

func (s *answerSender) Send(
	clientID int,
	request RawMessage,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.failing != nil {
		return s.failing
	}

	id, ok, err := responseQueryID(request)
	if err != nil || !ok || s.reply == nil {
		return nil
	}

	s.client.updates <- Update{
		ClientID: clientID,
		Raw:      s.reply(id),
	}

	return nil
}

// TestAuthDiagnosticsCorrelatesOKWithPhoneRequest proves an accepted
// request is matched to the answer that accepted it.
func TestAuthDiagnosticsCorrelatesOKWithPhoneRequest(t *testing.T) {
	t.Parallel()

	var trace bytes.Buffer

	client := newTestClient(t)
	sender := &answerSender{
		client: client,
		reply: func(id QueryID) RawMessage {
			return RawMessage(`{"@type":"ok","@extra":"` + string(id) + `"}`)
		},
	}

	provider := &fakeProvider{phone: tracePhone, code: traceCode}

	// The ready state is not fed: the run ends on the deadline, so the
	// answers the sender produced are read before the loop stops.
	feedAuth(t, client,
		"authorizationStateWaitPhoneNumber",
		"authorizationStateWaitCode",
	)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	_, _ = RunAuthWithClientDiagnostics(
		ctx,
		sender,
		client,
		traceAuthParams(),
		provider,
		NewWriterAuthDiagnostics(&trace),
	)

	got := trace.String()

	// request_id=1 is the phone request: the first state produced no
	// request, the second produced the phone request.
	if !strings.Contains(
		got,
		"request_id=1 request=set_authentication_phone_number "+
			"result=submitted",
	) {
		t.Fatalf("the phone request was not reported:\\n%s", got)
	}

	if !strings.Contains(
		got,
		"request_id=1 request=set_authentication_phone_number "+
			"result=answered",
	) {
		t.Fatalf("the answer was not matched to the phone request:\\n%s", got)
	}

	if strings.Contains(got, tracePhone) {
		t.Fatal("correlation leaked the phone number")
	}
}

// TestAuthDiagnosticsCorrelatesErrorWithPhoneRequest is the case the whole
// investigation needs: a refusal that is provably the phone request.
func TestAuthDiagnosticsCorrelatesErrorWithPhoneRequest(t *testing.T) {
	t.Parallel()

	var trace bytes.Buffer

	client := newTestClient(t)
	sender := &answerSender{
		client: client,
		reply: func(id QueryID) RawMessage {
			return RawMessage(
				`{"@type":"error","code":400,` +
					`"message":"PHONE_NUMBER_INVALID ` +
					tracePhone + `","@extra":"` +
					string(id) + `"}`,
			)
		},
	}

	provider := &fakeProvider{phone: tracePhone}
	feedAuth(t, client, "authorizationStateWaitPhoneNumber")

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	_, _ = RunAuthWithClientDiagnostics(
		ctx,
		sender,
		client,
		traceAuthParams(),
		provider,
		NewWriterAuthDiagnostics(&trace),
	)

	got := trace.String()

	if !strings.Contains(
		got,
		"request_id=1 request=set_authentication_phone_number "+
			"result=error code=400",
	) {
		t.Fatalf("the refusal was not attributed to the phone request:\\n%s", got)
	}

	// The message carried the rejected value and must not appear.
	if strings.Contains(got, tracePhone) {
		t.Fatal("the error message leaked the rejected phone number")
	}
	if strings.Contains(got, "PHONE_NUMBER_INVALID") {
		t.Fatal("the TDLib error message leaked into the trace")
	}
}

// TestAuthDiagnosticsDoesNotAttributeUnrelatedErrorToPhoneRequest is the
// critical regression. A refusal that arrives without our identifier must
// not be guessed onto the phone request, because that guess is exactly
// what this trace exists to eliminate.
func TestAuthDiagnosticsDoesNotAttributeUnrelatedErrorToPhoneRequest(
	t *testing.T,
) {
	t.Parallel()

	var trace bytes.Buffer

	client := newTestClient(t)

	// The sender accepts the phone request but answers nothing, so an
	// unrelated refusal arrives later with no correlation identifier.
	sender := &answerSender{client: client}

	feedAuth(t, client, "authorizationStateWaitPhoneNumber")

	// The unrelated refusal and the connection noise carry no identifier.
	feedRaw(t, client,
		`{"@type":"error","code":400,"message":"refused"}`,
		`{"@type":"updateConnectionState"}`,
	)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	_, _ = RunAuthWithClientDiagnostics(
		ctx,
		sender,
		client,
		traceAuthParams(),
		&fakeProvider{phone: tracePhone},
		NewWriterAuthDiagnostics(&trace),
	)

	got := trace.String()

	// The refusal is reported, but as an envelope without an owner.
	if !strings.Contains(got, "envelope=error code=400") {
		t.Fatalf("the unrelated refusal was not reported at all:\\n%s", got)
	}

	if strings.Contains(
		got,
		"set_authentication_phone_number result=error",
	) {
		t.Fatalf("an unrelated refusal was attributed to the phone "+
			"request:\\n%s", got)
	}

	// The phone request itself is only ever reported as submitted.
	if !strings.Contains(
		got,
		"request_id=1 request=set_authentication_phone_number "+
			"result=submitted",
	) {
		t.Fatalf("the phone request was not reported as submitted:\\n%s", got)
	}
}

// TestAuthDiagnosticsRejectsUnknownRequestID proves a foreign identifier
// is not mistaken for one of ours.
func TestAuthDiagnosticsRejectsUnknownRequestID(t *testing.T) {
	t.Parallel()

	for _, foreign := range []QueryID{
		"someone-else:1:1",
		"telecli:1:abc",
		"telecli:1",
		"",
	} {
		if id := diagnosticIDFor(foreign); id != 0 {
			t.Fatalf(
				"diagnosticIDFor(%q) = %d, want 0",
				foreign,
				id,
			)
		}
	}

	if id := diagnosticIDFor("telecli:1:7"); id != 7 {
		t.Fatalf("diagnosticIDFor(own) = %d, want 7", id)
	}
}

// TestAuthDiagnosticsClearsPendingRequestAfterResult proves a settled
// request is not matched a second time.
func TestAuthDiagnosticsClearsPendingRequestAfterResult(t *testing.T) {
	t.Parallel()

	var trace bytes.Buffer

	client := newTestClient(t)
	sender := &answerSender{
		client: client,
		reply: func(id QueryID) RawMessage {
			return RawMessage(`{"@type":"ok","@extra":"` + string(id) + `"}`)
		},
	}

	feedAuth(t, client, "authorizationStateWaitPhoneNumber")

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	_, _ = RunAuthWithClientDiagnostics(
		ctx,
		sender,
		client,
		traceAuthParams(),
		&fakeProvider{phone: tracePhone},
		NewWriterAuthDiagnostics(&trace),
	)

	got := trace.String()

	answered := strings.Count(got, "result=answered")
	if answered != 1 {
		t.Fatalf("the answer was recorded %d times, want 1:\n%s", answered, got)
	}
}

// feedRaw pushes verbatim messages into the update stream, for cases where
// the message is not an authorization state update.
func feedRaw(t *testing.T, c *Client, messages ...string) {
	t.Helper()

	for _, message := range messages {
		c.updates <- Update{ClientID: c.id, Raw: RawMessage(message)}
	}
}

// TestAuthDiagnosticsDoesNotAttributeUnmatchedIDToPhoneRequest covers the
// dangerous shape: the message carries an identifier in telecli's own
// namespace, so it looks like ours, but nothing is pending under it. It
// must not be attached to the phone request, because attaching it is
// exactly the guess this trace exists to remove.
func TestAuthDiagnosticsDoesNotAttributeUnmatchedIDToPhoneRequest(
	t *testing.T,
) {
	t.Parallel()

	var trace bytes.Buffer

	client := newTestClient(t)

	// The sender accepts the phone request and answers nothing, so the
	// only message carrying an identifier arrives afterwards.
	sender := &answerSender{client: client}

	feedAuth(t, client, "authorizationStateWaitPhoneNumber")
	feedRaw(t, client,
		`{"@type":"error","code":400,"message":"refused",`+
			`"@extra":"telecli:1:999"}`,
	)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	_, _ = RunAuthWithClientDiagnostics(
		ctx,
		sender,
		client,
		traceAuthParams(),
		&fakeProvider{phone: tracePhone},
		NewWriterAuthDiagnostics(&trace),
	)

	got := trace.String()

	if strings.Contains(
		got,
		"set_authentication_phone_number result=error",
	) {
		t.Fatalf("an unmatched identifier was attributed to the phone "+
			"request:\n%s", got)
	}

	// It is still reported, and still attributed to nothing.
	if !strings.Contains(got, "request_id=0 request=other result=error") {
		t.Fatalf("the unmatched refusal was not reported as ownerless:\n%s", got)
	}

	if !strings.Contains(got, "code=400") {
		t.Fatalf("the refusal code was lost:\n%s", got)
	}
}

// TestAuthCoordinatorNamesACorrelatedRefusal walks the exact case the
// investigation needs: a refusal tied to the phone request is named, and
// the name comes from the response rather than from the code alone.
func TestAuthCoordinatorNamesACorrelatedRefusal(t *testing.T) {
	t.Parallel()

	var trace bytes.Buffer

	client := newTestClient(t)
	sender := &answerSender{
		client: client,
		reply: func(id QueryID) RawMessage {
			return RawMessage(
				`{"@type":"error","code":400,` +
					`"message":"PHONE_NUMBER_INVALID",` +
					`"@extra":"` + string(id) + `"}`,
			)
		},
	}

	feedAuth(t, client, "authorizationStateWaitPhoneNumber")

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	_, _ = RunAuthWithClientDiagnostics(
		ctx,
		sender,
		client,
		traceAuthParams(),
		&fakeProvider{phone: tracePhone},
		NewWriterAuthDiagnostics(&trace),
	)

	got := trace.String()

	want := "request_id=1 request=set_authentication_phone_number " +
		"result=error code=400 name=phone_number_invalid"

	if !strings.Contains(got, want) {
		t.Fatalf("trace = %q, want substring %q", got, want)
	}
}

// TestAuthCoordinatorLeavesAnUnlistedRefusalUnnamed is the counterpart: a
// code the allowlist does not list, and a listed code carrying some other
// message, both stay unknown.
func TestAuthCoordinatorLeavesAnUnlistedRefusalUnnamed(t *testing.T) {
	t.Parallel()

	for name, reply := range map[string]func(QueryID) RawMessage{
		"other message under 400": func(id QueryID) RawMessage {
			return RawMessage(
				`{"@type":"error","code":400,` +
					`"message":"Wrong phone number specified",` +
					`"@extra":"` + string(id) + `"}`,
			)
		},
		"listed message under another code": func(id QueryID) RawMessage {
			return RawMessage(
				`{"@type":"error","code":420,` +
					`"message":"PHONE_NUMBER_INVALID",` +
					`"@extra":"` + string(id) + `"}`,
			)
		},
	} {
		var trace bytes.Buffer

		client := newTestClient(t)
		sender := &answerSender{client: client, reply: reply}

		feedAuth(t, client, "authorizationStateWaitPhoneNumber")

		ctx, cancel := context.WithTimeout(
			context.Background(),
			300*time.Millisecond,
		)

		_, _ = RunAuthWithClientDiagnostics(
			ctx,
			sender,
			client,
			traceAuthParams(),
			&fakeProvider{phone: tracePhone},
			NewWriterAuthDiagnostics(&trace),
		)

		cancel()

		got := trace.String()

		if !strings.Contains(got, "result=error") {
			t.Fatalf("%s: the refusal was not reported:\n%s", name, got)
		}

		if strings.Contains(got, "name=phone_number_invalid") {
			t.Fatalf("%s: an unlisted refusal was named:\n%s", name, got)
		}

		if !strings.Contains(got, "name=unknown") {
			t.Fatalf("%s: the refusal was not marked unknown:\n%s", name, got)
		}
	}
}

// TestAuthCoordinatorNamesNothingWithoutAnOwner proves the classification
// does not appear on an answer that could not be tied to a request.
func TestAuthCoordinatorNamesNothingWithoutAnOwner(t *testing.T) {
	t.Parallel()

	var trace bytes.Buffer

	client := newTestClient(t)
	sender := &answerSender{client: client}

	feedAuth(t, client, "authorizationStateWaitPhoneNumber")
	feedRaw(t, client,
		`{"@type":"error","code":400,`+
			`"message":"PHONE_NUMBER_INVALID",`+
			`"@extra":"telecli:1:999"}`,
	)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	_, _ = RunAuthWithClientDiagnostics(
		ctx,
		sender,
		client,
		traceAuthParams(),
		&fakeProvider{phone: tracePhone},
		NewWriterAuthDiagnostics(&trace),
	)

	got := trace.String()

	if !strings.Contains(got, "request_id=0 request=other result=error") {
		t.Fatalf("the ownerless refusal was not reported:\n%s", got)
	}

	if strings.Contains(got, "name=") {
		t.Fatalf("an ownerless refusal was given a name:\n%s", got)
	}
}

// TestAuthCoordinatorNeverEmitsTheResponseMessage sweeps the values a real
// refusal message is likely to contain.
func TestAuthCoordinatorNeverEmitsTheResponseMessage(t *testing.T) {
	t.Parallel()

	const refusal = "PHONE_NUMBER_INVALID " + tracePhone

	var trace bytes.Buffer

	client := newTestClient(t)
	sender := &answerSender{
		client: client,
		reply: func(id QueryID) RawMessage {
			return RawMessage(
				`{"@type":"error","code":400,"message":"` + refusal +
					`","@extra":"` + string(id) + `"}`,
			)
		},
	}

	feedAuth(t, client, "authorizationStateWaitPhoneNumber")

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	_, _ = RunAuthWithClientDiagnostics(
		ctx,
		sender,
		client,
		traceAuthParams(),
		&fakeProvider{phone: tracePhone},
		NewWriterAuthDiagnostics(&trace),
	)

	got := trace.String()

	// The message does not match the allowlist, so it must not be named.
	if strings.Contains(got, "name=phone_number_invalid") {
		t.Fatalf("a message that does not match was named:\n%s", got)
	}

	for _, secret := range []string{tracePhone, traceAPIHash, refusal} {
		if strings.Contains(got, secret) {
			t.Fatalf("the trace exposed %q:\n%s", secret, got)
		}
	}
}

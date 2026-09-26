package telegram

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

// Authorization diagnostics report the shape of the authorization
// handshake and nothing else.
//
// A packaged runtime that cannot complete authorization is hard to reason
// about, because the native layer answers in its own log and the
// application loop is silent. These diagnostics make the sequence
// observable without ever handling a credential: they accept closed
// enumerations only, they never receive a payload, and an unrecognised
// value collapses to a neutral label instead of being formatted.
//
// The whole facility is off unless TELECLI_AUTH_TRACE names a truthy
// value, so a normal run is byte-for-byte unchanged.

// authTraceEnvironment enables the diagnostics when set to a truthy value.
const authTraceEnvironment = "TELECLI_AUTH_TRACE"

// AuthDiagnosticState is a closed set of authorization state labels.
type AuthDiagnosticState string

const (
	AuthDiagnosticStateOther AuthDiagnosticState = "other"

	AuthDiagnosticStateWaitTDLibParameters AuthDiagnosticState = "wait_tdlib_parameters"

	AuthDiagnosticStateWaitPhoneNumber AuthDiagnosticState = "wait_phone_number"

	AuthDiagnosticStateWaitCode AuthDiagnosticState = "wait_code"

	AuthDiagnosticStateWaitPassword AuthDiagnosticState = "wait_password"

	AuthDiagnosticStateReady AuthDiagnosticState = "ready"

	AuthDiagnosticStateClosed AuthDiagnosticState = "closed"
)

// AuthDiagnosticRequest is a closed set of request labels.
type AuthDiagnosticRequest string

const (
	AuthDiagnosticRequestOther AuthDiagnosticRequest = "other"

	AuthDiagnosticRequestGetAuthorizationState AuthDiagnosticRequest = "get_authorization_state"

	AuthDiagnosticRequestSetTDLibParameters AuthDiagnosticRequest = "set_tdlib_parameters"

	AuthDiagnosticRequestSetPhoneNumber AuthDiagnosticRequest = "set_authentication_phone_number"

	AuthDiagnosticRequestCheckCode AuthDiagnosticRequest = "check_authentication_code"

	AuthDiagnosticRequestCheckPassword AuthDiagnosticRequest = "check_authentication_password"

	AuthDiagnosticRequestLogOut AuthDiagnosticRequest = "log_out"

	AuthDiagnosticRequestClose AuthDiagnosticRequest = "close"
)

// AuthDiagnosticRequestID is a small internal sequence number.
//
// It identifies a request within one authorization run so a later answer
// can be attributed to the request that caused it. It is a counter this
// package generates; it is not a Telegram value and it carries nothing
// from a payload.
type AuthDiagnosticRequestID uint64

// AuthDiagnosticResult is a closed set of outcome labels.
//
// "submitted" deliberately does not claim that Telegram accepted the
// request: a successful Send only proves the local native bridge took it.
type AuthDiagnosticResult string

const (
	AuthDiagnosticResultSubmitted AuthDiagnosticResult = "submitted"

	AuthDiagnosticResultError AuthDiagnosticResult = "error"

	AuthDiagnosticResultIgnoredDuplicate AuthDiagnosticResult = "ignored_duplicate"

	AuthDiagnosticResultNoRequest AuthDiagnosticResult = "no_request"

	// AuthDiagnosticResultUnattributed marks an answer that could not be
	// tied to any request this run sent. Reporting it is the point: an
	// unexplained error must not be guessed onto the last request.
	AuthDiagnosticResultUnattributed AuthDiagnosticResult = "unattributed"

	// AuthDiagnosticResultAnswered marks a direct answer matched to a
	// pending request through its correlation identifier.
	AuthDiagnosticResultAnswered AuthDiagnosticResult = "answered"
)

// AuthDiagnosticEnvelope is a closed set of incoming message labels.
type AuthDiagnosticEnvelope string

const (
	AuthDiagnosticEnvelopeOther AuthDiagnosticEnvelope = "other"

	AuthDiagnosticEnvelopeOK AuthDiagnosticEnvelope = "ok"

	AuthDiagnosticEnvelopeError AuthDiagnosticEnvelope = "error"

	AuthDiagnosticEnvelopeAuthorizationState AuthDiagnosticEnvelope = "authorization_state"

	AuthDiagnosticEnvelopeAuthorizationUpdate AuthDiagnosticEnvelope = "update_authorization_state"

	AuthDiagnosticEnvelopeConnectionUpdate AuthDiagnosticEnvelope = "update_connection_state"
)

// AuthDiagnostics receives metadata-only authorization events.
type AuthDiagnostics interface {
	// State reports a state the loop observed.
	State(state AuthDiagnosticState)
	// Request reports the kind of request the session produced and the
	// identifier a later answer will be matched against.
	Request(id AuthDiagnosticRequestID, request AuthDiagnosticRequest)
	// Result reports the outcome of a request, with a numeric error code
	// when the answer was a refusal. The error message is never
	// available here.
	Result(
		id AuthDiagnosticRequestID,
		request AuthDiagnosticRequest,
		result AuthDiagnosticResult,
		errorCode int,
	)
	// Envelope reports an incoming message that carried no correlation
	// identifier, so it cannot be attributed to a request.
	Envelope(envelope AuthDiagnosticEnvelope, errorCode int)
}

// discardAuthDiagnostics drops every event.
type discardAuthDiagnostics struct{}

func (discardAuthDiagnostics) State(AuthDiagnosticState) {}

func (discardAuthDiagnostics) Request(
	AuthDiagnosticRequestID,
	AuthDiagnosticRequest,
) {
}

func (discardAuthDiagnostics) Envelope(AuthDiagnosticEnvelope, int) {}

func (discardAuthDiagnostics) Result(
	AuthDiagnosticRequestID,
	AuthDiagnosticRequest,
	AuthDiagnosticResult,
	int,
) {
}

// DiscardAuthDiagnostics returns a silent sink.
func DiscardAuthDiagnostics() AuthDiagnostics {
	return discardAuthDiagnostics{}
}

// writerAuthDiagnostics writes sanitised labels to a writer.
type writerAuthDiagnostics struct {
	mu     sync.Mutex
	writer io.Writer
}

// NewWriterAuthDiagnostics returns diagnostics that write to writer.
//
// A nil writer yields a silent sink, so a caller never has to nil-check.
func NewWriterAuthDiagnostics(writer io.Writer) AuthDiagnostics {
	if writer == nil {
		return DiscardAuthDiagnostics()
	}

	return &writerAuthDiagnostics{writer: writer}
}

// NewEnvironmentAuthDiagnostics returns diagnostics only when
// TELECLI_AUTH_TRACE is truthy, and a silent sink otherwise.
func NewEnvironmentAuthDiagnostics(writer io.Writer) AuthDiagnostics {
	if !authTraceEnabled(os.Getenv(authTraceEnvironment)) {
		return DiscardAuthDiagnostics()
	}

	return NewWriterAuthDiagnostics(writer)
}

// authTraceEnabled reports whether a trace was asked for.
func authTraceEnabled(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func (d *writerAuthDiagnostics) State(state AuthDiagnosticState) {
	d.write("state", string(sanitizeAuthDiagnosticState(state)))
}

func (d *writerAuthDiagnostics) Request(
	id AuthDiagnosticRequestID,
	request AuthDiagnosticRequest,
) {
	d.write(
		"request_id",
		fmt.Sprintf(
			"%d request=%s",
			sanitizeAuthDiagnosticRequestID(id),
			sanitizeAuthDiagnosticRequest(request),
		),
	)
}

// Result writes an outcome line. A refusal additionally reports its
// numeric code, because the distinction between "this request was
// refused" and "something else failed" is the whole point of the trace.
func (d *writerAuthDiagnostics) Result(
	id AuthDiagnosticRequestID,
	request AuthDiagnosticRequest,
	result AuthDiagnosticResult,
	errorCode int,
) {
	d.mu.Lock()
	defer d.mu.Unlock()

	line := fmt.Sprintf(
		"telecli auth trace: request_id=%d request=%s result=%s",
		sanitizeAuthDiagnosticRequestID(id),
		sanitizeAuthDiagnosticRequest(request),
		sanitizeAuthDiagnosticResult(result),
	)

	if sanitizeAuthDiagnosticResult(result) == AuthDiagnosticResultError {
		line += fmt.Sprintf(" code=%d", sanitizeAuthDiagnosticErrorCode(errorCode))
	}

	_, _ = fmt.Fprintln(d.writer, line)
}

// Envelope writes an incoming message label. An error additionally
// reports its numeric code, because the distinction between "Telegram
// refused" and "no answer arrived" is the whole point of the trace.
func (d *writerAuthDiagnostics) Envelope(
	envelope AuthDiagnosticEnvelope,
	errorCode int,
) {
	envelope = sanitizeAuthDiagnosticEnvelope(envelope)

	d.mu.Lock()
	defer d.mu.Unlock()

	if envelope == AuthDiagnosticEnvelopeError {
		_, _ = fmt.Fprintf(
			d.writer,
			"telecli auth trace: envelope=error code=%d\n",
			sanitizeAuthDiagnosticErrorCode(errorCode),
		)

		return
	}

	_, _ = fmt.Fprintf(
		d.writer,
		"telecli auth trace: envelope=%s\n",
		envelope,
	)
}

func (d *writerAuthDiagnostics) write(field string, value string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, _ = fmt.Fprintf(d.writer, "telecli auth trace: %s=%s\n", field, value)
}

// sanitizeAuthDiagnosticState collapses anything outside the closed set.
func sanitizeAuthDiagnosticState(
	state AuthDiagnosticState,
) AuthDiagnosticState {
	switch state {
	case AuthDiagnosticStateWaitTDLibParameters,
		AuthDiagnosticStateWaitPhoneNumber,
		AuthDiagnosticStateWaitCode,
		AuthDiagnosticStateWaitPassword,
		AuthDiagnosticStateReady,
		AuthDiagnosticStateClosed:
		return state
	default:
		return AuthDiagnosticStateOther
	}
}

// sanitizeAuthDiagnosticRequest collapses anything outside the closed set.
func sanitizeAuthDiagnosticRequest(
	request AuthDiagnosticRequest,
) AuthDiagnosticRequest {
	switch request {
	case AuthDiagnosticRequestGetAuthorizationState,
		AuthDiagnosticRequestSetTDLibParameters,
		AuthDiagnosticRequestSetPhoneNumber,
		AuthDiagnosticRequestCheckCode,
		AuthDiagnosticRequestCheckPassword,
		AuthDiagnosticRequestLogOut,
		AuthDiagnosticRequestClose:
		return request
	default:
		return AuthDiagnosticRequestOther
	}
}

// sanitizeAuthDiagnosticEnvelope collapses anything outside the closed set.
func sanitizeAuthDiagnosticEnvelope(
	envelope AuthDiagnosticEnvelope,
) AuthDiagnosticEnvelope {
	switch envelope {
	case AuthDiagnosticEnvelopeOK,
		AuthDiagnosticEnvelopeError,
		AuthDiagnosticEnvelopeAuthorizationState,
		AuthDiagnosticEnvelopeAuthorizationUpdate,
		AuthDiagnosticEnvelopeConnectionUpdate:
		return envelope
	default:
		return AuthDiagnosticEnvelopeOther
	}
}

// sanitizeAuthDiagnosticErrorCode keeps a plausible TDLib code and drops
// anything else, so a malformed message cannot print a strange value.
func sanitizeAuthDiagnosticErrorCode(code int) int {
	if code < 0 || code > 9999 {
		return 0
	}

	return code
}

// sanitizeAuthDiagnosticResult collapses anything outside the closed set.
func sanitizeAuthDiagnosticResult(
	result AuthDiagnosticResult,
) AuthDiagnosticResult {
	switch result {
	case AuthDiagnosticResultSubmitted,
		AuthDiagnosticResultIgnoredDuplicate,
		AuthDiagnosticResultNoRequest,
		AuthDiagnosticResultUnattributed,
		AuthDiagnosticResultAnswered:
		return result
	default:
		return AuthDiagnosticResultError
	}
}

// sanitizeAuthDiagnosticRequestID keeps a plausible counter and collapses
// anything else to zero, which reads as "no request".
func sanitizeAuthDiagnosticRequestID(
	id AuthDiagnosticRequestID,
) uint64 {
	if id > 1<<20 {
		return 0
	}

	return uint64(id)
}

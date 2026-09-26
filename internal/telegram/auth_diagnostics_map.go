package telegram

import (
	"encoding/json"
	"strconv"
	"strings"
)

// The mapping from internal values to diagnostic labels is the only place
// that reads an authorization state or a request, and it extracts nothing
// but an enumerated name.

// authDiagnosticStateKnown maps a state onto its closed label and reports
// whether the label says anything.
//
// An update that is not an authorization state, and a state the product
// does not handle, both answer false. They are not reported: the wire
// carries a large number of unrelated objects, and a line per object
// buries the handful that matter.
func authDiagnosticStateKnown(
	state AuthState,
) (AuthDiagnosticState, bool) {
	switch state {
	case AuthStateWaitTdlibParameters:
		return AuthDiagnosticStateWaitTDLibParameters, true
	case AuthStateWaitPhoneNumber:
		return AuthDiagnosticStateWaitPhoneNumber, true
	case AuthStateWaitCode:
		return AuthDiagnosticStateWaitCode, true
	case AuthStateWaitPassword:
		return AuthDiagnosticStateWaitPassword, true
	case AuthStateReady:
		return AuthDiagnosticStateReady, true
	case AuthStateClosed, AuthStateClosing:
		return AuthDiagnosticStateClosed, true
	default:
		return AuthDiagnosticStateOther, false
	}
}

// authDiagnosticEnvelopeHeader is the only part of an incoming message
// the diagnostics are allowed to see.
//
// The decode target has two fields. A nested authorization_state or
// connection state is deliberately absent: the authorization state is
// already reported from the parsed update, so decoding it twice would add
// surface without adding information.
type authDiagnosticEnvelopeHeader struct {
	Type string `json:"@type"`
	Code int    `json:"code"`
}

// classifyAuthDiagnosticEnvelope names an incoming message and, for an
// error, its numeric code.
//
// The error message is never read. Telegram puts the rejected phone
// number and similar values into it, so a code is the most that can be
// reported safely.
func classifyAuthDiagnosticEnvelope(
	message RawMessage,
) (AuthDiagnosticEnvelope, int) {
	var header authDiagnosticEnvelopeHeader

	if err := json.Unmarshal(message, &header); err != nil {
		return AuthDiagnosticEnvelopeOther, 0
	}

	switch header.Type {
	case "ok":
		return AuthDiagnosticEnvelopeOK, 0
	case "error":
		return AuthDiagnosticEnvelopeError, header.Code
	case "updateAuthorizationState":
		return AuthDiagnosticEnvelopeAuthorizationUpdate, 0
	case "updateConnectionState":
		return AuthDiagnosticEnvelopeConnectionUpdate, 0
	case "authorizationStateWaitTdlibParameters",
		"authorizationStateWaitPhoneNumber",
		"authorizationStateWaitCode",
		"authorizationStateWaitPassword",
		"authorizationStateReady",
		"authorizationStateClosed":
		return AuthDiagnosticEnvelopeAuthorizationState, 0
	default:
		return AuthDiagnosticEnvelopeOther, 0
	}
}

// authDiagnosticEnvelopeReportable reports whether an envelope says
// enough to be worth a line.
func authDiagnosticEnvelopeReportable(
	envelope AuthDiagnosticEnvelope,
) bool {
	switch envelope {
	case AuthDiagnosticEnvelopeOK,
		AuthDiagnosticEnvelopeError,
		AuthDiagnosticEnvelopeAuthorizationState,
		AuthDiagnosticEnvelopeAuthorizationUpdate,
		AuthDiagnosticEnvelopeConnectionUpdate:
		return true
	default:
		return false
	}
}

// authDiagnosticRequest reads only the "@type" of a request.
//
// The payload is parsed into a struct with a single field, so no other
// value can reach the caller even by accident.
func authDiagnosticRequest(request RawMessage) AuthDiagnosticRequest {
	var envelope struct {
		Type string `json:"@type"`
	}

	if err := json.Unmarshal(request, &envelope); err != nil {
		return AuthDiagnosticRequestOther
	}

	switch envelope.Type {
	case "getAuthorizationState":
		return AuthDiagnosticRequestGetAuthorizationState
	case "setTdlibParameters":
		return AuthDiagnosticRequestSetTDLibParameters
	case "setAuthenticationPhoneNumber":
		return AuthDiagnosticRequestSetPhoneNumber
	case "checkAuthenticationCode":
		return AuthDiagnosticRequestCheckCode
	case "checkAuthenticationPassword":
		return AuthDiagnosticRequestCheckPassword
	case "logOut":
		return AuthDiagnosticRequestLogOut
	case "close":
		return AuthDiagnosticRequestClose
	default:
		return AuthDiagnosticRequestOther
	}
}

// diagnosticRequestForState names the request a state is expected to
// produce, so a state that produced nothing is still reportable.
func diagnosticRequestForState(state AuthState) AuthDiagnosticRequest {
	switch state {
	case AuthStateWaitTdlibParameters:
		return AuthDiagnosticRequestSetTDLibParameters
	case AuthStateWaitPhoneNumber:
		return AuthDiagnosticRequestSetPhoneNumber
	case AuthStateWaitCode:
		return AuthDiagnosticRequestCheckCode
	case AuthStateWaitPassword:
		return AuthDiagnosticRequestCheckPassword
	default:
		return AuthDiagnosticRequestOther
	}
}

// noRequestResult distinguishes an expected repeat from an unexplained gap.
func noRequestResult(state AuthState) AuthDiagnosticResult {
	if state == AuthStateWaitTdlibParameters {
		// The session is already configured, so producing nothing here
		// is the normal outcome and not a gap.
		return AuthDiagnosticResultIgnoredDuplicate
	}

	return AuthDiagnosticResultNoRequest
}

// answerResult names the outcome of a direct answer.
//
// Only a refusal is an error. Anything else that carries our identifier
// is a success envelope, because the request it answers was accepted.
func answerResult(
	message RawMessage,
	errorCode int,
) AuthDiagnosticResult {
	if envelope, _ := classifyAuthDiagnosticEnvelope(message); envelope ==
		AuthDiagnosticEnvelopeError || errorCode != 0 {
		return AuthDiagnosticResultError
	}

	return AuthDiagnosticResultAnswered
}

// diagnosticIDFor recovers the sequence number from a query identifier.
//
// isTelecliQueryID only checks the namespace prefix, so the full shape is
// verified here: telecli, a client number, a sequence number. Anything else
// yields zero, which the writer renders as an unattributed answer rather
// than an invented number.
func diagnosticIDFor(queryID QueryID) AuthDiagnosticRequestID {
	parts := strings.Split(string(queryID), ":")
	if len(parts) != 3 || parts[0]+":" != queryNamespace {
		return 0
	}

	for _, part := range parts[1:] {
		if part == "" {
			return 0
		}

		for _, digit := range part {
			if digit < '0' || digit > '9' {
				return 0
			}
		}
	}

	value, err := strconv.ParseUint(parts[2], 10, 64)
	if err != nil {
		return 0
	}

	return AuthDiagnosticRequestID(value)
}

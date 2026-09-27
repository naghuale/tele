package telegram

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
)

// ErrAuthRequestRejected reports that TDLib refused an authorization
// request, for example an invalid phone number or code.
var ErrAuthRequestRejected = errors.New("auth: Telegram rejected the request")

// AuthRequestError describes a refused authorization request.
//
// It never carries the submitted value or TDLib's free-text message:
// Reason is set only when the message is a Telegram error identifier
// such as PHONE_NUMBER_INVALID.
type AuthRequestError struct {
	Request AuthDiagnosticRequest
	Code    int
	Reason  string
}

func (e *AuthRequestError) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("%s: %s: %s (code %d)",
			ErrAuthRequestRejected, e.Request, e.Reason, e.Code)
	}
	return fmt.Sprintf("%s: %s (code %d)",
		ErrAuthRequestRejected, e.Request, e.Code)
}

func (e *AuthRequestError) Unwrap() error { return ErrAuthRequestRejected }

// telegramErrorIdentifier matches Telegram's error identifiers, which
// never contain user input.
var telegramErrorIdentifier = regexp.MustCompile(`^[A-Z][A-Z0-9_]{2,63}$`)

// authRequestRejection returns the refusal carried by an answer, or nil
// when the answer is not an error object.
func authRequestRejection(
	request AuthDiagnosticRequest,
	answer RawMessage,
) *AuthRequestError {
	var header struct {
		Type    string `json:"@type"`
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(answer, &header); err != nil || header.Type != "error" {
		return nil
	}

	rejection := &AuthRequestError{Request: request, Code: header.Code}
	if telegramErrorIdentifier.MatchString(header.Message) {
		rejection.Reason = header.Message
	}
	return rejection
}

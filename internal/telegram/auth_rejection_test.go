package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// answeringSender answers every request of one TDLib type with a
// response built from the request's @extra, the way TDLib does.
type answeringSender struct {
	fakeSender
	client     *Client
	answerType string
	answer     func(extra string) string
}

func (s *answeringSender) Send(clientID int, request RawMessage) error {
	if err := s.fakeSender.Send(clientID, request); err != nil {
		return err
	}
	var envelope struct {
		Type  string `json:"@type"`
		Extra string `json:"@extra"`
	}
	if err := json.Unmarshal(request, &envelope); err != nil {
		return err
	}
	if envelope.Type == s.answerType {
		s.client.updates <- Update{
			ClientID: clientID,
			Raw:      RawMessage(s.answer(envelope.Extra)),
		}
	}
	return nil
}

func runRejectedPhone(t *testing.T, message string) error {
	t.Helper()
	client := newTestClient(t)
	sender := &answeringSender{
		client:     client,
		answerType: "setAuthenticationPhoneNumber",
		answer: func(extra string) string {
			return `{"@type":"error","code":400,"message":"` + message +
				`","@extra":"` + extra + `"}`
		},
	}
	feedAuth(t, client,
		"authorizationStateWaitTdlibParameters",
		"authorizationStateWaitPhoneNumber",
	)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := RunAuthWithClient(
		ctx, sender, client, validAuthParams(),
		&fakeProvider{phone: "+15551234"},
	)
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("authorization hung after TDLib rejected the phone number")
	}
	return err
}

// TestRunAuthWithClientFailsOnRejectedPhone pins that a TDLib refusal of
// an authorization request ends the run with a readable reason instead
// of waiting forever for a state change that never comes.
func TestRunAuthWithClientFailsOnRejectedPhone(t *testing.T) {
	err := runRejectedPhone(t, "PHONE_NUMBER_INVALID")

	if !errors.Is(err, ErrAuthRequestRejected) {
		t.Fatalf("error = %v, want ErrAuthRequestRejected", err)
	}
	var rejected *AuthRequestError
	if !errors.As(err, &rejected) {
		t.Fatalf("error = %T, want *AuthRequestError", err)
	}
	if rejected.Request != AuthDiagnosticRequestSetPhoneNumber ||
		rejected.Code != 400 ||
		rejected.Reason != "PHONE_NUMBER_INVALID" {
		t.Fatalf("rejection = %+v", rejected)
	}
	if !strings.Contains(err.Error(), "PHONE_NUMBER_INVALID") {
		t.Fatalf("error text %q does not name the reason", err)
	}
}

// TestRunAuthWithClientRejectionNeverEchoesFreeText pins that only a
// Telegram error identifier is reported. Free text may carry the phone
// number or another submitted value.
func TestRunAuthWithClientRejectionNeverEchoesFreeText(t *testing.T) {
	err := runRejectedPhone(t, "number +15551234 is not valid")

	var rejected *AuthRequestError
	if !errors.As(err, &rejected) {
		t.Fatalf("error = %v, want *AuthRequestError", err)
	}
	if rejected.Reason != "" {
		t.Fatalf("reason = %q, want empty for free text", rejected.Reason)
	}
	if strings.Contains(err.Error(), "15551234") {
		t.Fatalf("error text leaked the phone number: %q", err)
	}
	if !strings.Contains(err.Error(), "400") {
		t.Fatalf("error text %q does not carry the code", err)
	}
}

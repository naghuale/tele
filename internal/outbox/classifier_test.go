package outbox

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestClassifyNilErrorAccepted(t *testing.T) {
	if got := Classify(nil, true); got != OutcomeAccepted {
		t.Fatalf("got %s, want accepted", got)
	}
	if got := Classify(nil, false); got != OutcomeAccepted {
		t.Fatalf("got %s, want accepted", got)
	}
}

func TestClassifyBeforeSendRetryable(t *testing.T) {
	err := errors.New("claim failed")
	if got := Classify(err, false); got != OutcomeRetryable {
		t.Fatalf("got %s, want retryable", got)
	}
}

func TestClassifyContextCanceledAfterSendUncertain(t *testing.T) {
	for _, err := range []error{
		context.Canceled,
		context.DeadlineExceeded,
	} {
		if got := Classify(err, true); got != OutcomeUncertain {
			t.Fatalf("err=%v: got %s, want uncertain", err, got)
		}
	}
}

func TestClassifySendErrorFlags(t *testing.T) {
	if got := Classify(&SendError{Permanent: true}, true); got != OutcomePermanent {
		t.Fatalf("permanent: got %s", got)
	}
	if got := Classify(&SendError{Retryable: true}, true); got != OutcomeRetryable {
		t.Fatalf("retryable: got %s", got)
	}
}

func TestClassifySendErrorCodes(t *testing.T) {
	cases := []struct {
		code int
		want Outcome
	}{
		{400, OutcomePermanent},
		{401, OutcomePermanent},
		{403, OutcomePermanent},
		{404, OutcomePermanent},
		{420, OutcomeRetryable},
		{429, OutcomeRetryable},
		{500, OutcomeRetryable},
		{502, OutcomeRetryable},
		{503, OutcomeRetryable},
		{504, OutcomeRetryable},
		{999, OutcomeUncertain},
		{0, OutcomeUncertain},
	}
	for _, c := range cases {
		err := &SendError{Code: c.code}
		got := Classify(err, true)
		if got != c.want {
			t.Fatalf("code=%d: got %s, want %s", c.code, got, c.want)
		}
	}
}

func TestClassifyUnknownAfterSendUncertain(t *testing.T) {
	err := errors.New("network reset")
	if got := Classify(err, true); got != OutcomeUncertain {
		t.Fatalf("got %s, want uncertain", got)
	}
}

func TestSendErrorDetails(t *testing.T) {
	sendErr := &SendError{Code: 400, Message: "x"}
	got, ok := SendErrorDetails(sendErr)
	if !ok || got != sendErr {
		t.Fatalf("ok=%t got=%+v", ok, got)
	}

	wrapped := fmt.Errorf("wrapped: %w", sendErr)
	got, ok = SendErrorDetails(wrapped)
	if !ok || got != sendErr {
		t.Fatalf("wrapped: ok=%t got=%+v", ok, got)
	}

	if _, ok := SendErrorDetails(errors.New("plain")); ok {
		t.Fatal("plain error should not match")
	}
}

// ---- SafeReason ----

func TestSafeReasonNil(t *testing.T) {
	if got := SafeReason(nil); got == "" {
		t.Fatal("nil reason must be non-empty")
	}
}

func TestSafeReasonSendError(t *testing.T) {
	cases := []struct {
		err  *SendError
		want string
	}{
		{&SendError{Code: 400}, "send error code=400"},
		{&SendError{Permanent: true}, "permanent send failure"},
		{&SendError{Retryable: true}, "retryable send failure"},
		{&SendError{}, "structured send failure"},
	}
	for _, c := range cases {
		if got := SafeReason(c.err); got != c.want {
			t.Fatalf("err=%+v: got %q, want %q", c.err, got, c.want)
		}
	}
}

func TestSafeReasonContext(t *testing.T) {
	if got := SafeReason(context.Canceled); got != "send context canceled" {
		t.Fatalf("canceled: got %q", got)
	}
	if got := SafeReason(context.DeadlineExceeded); got != "send deadline exceeded" {
		t.Fatalf("deadline: got %q", got)
	}
}

func TestSafeReasonDoesNotExposeErrorText(t *testing.T) {
	secret := "private message body"

	err := fmt.Errorf("send failed for %q", secret)

	got := SafeReason(err)

	if strings.Contains(got, secret) {
		t.Fatalf("safe reason leaked message text: %q", got)
	}
}

func TestSafeReasonSendErrorIgnoresMessageField(t *testing.T) {
	secret := "private message body"
	err := &SendError{Code: 400, Message: secret}

	got := SafeReason(err)
	if strings.Contains(got, secret) {
		t.Fatalf("safe reason leaked SendError.Message: %q", got)
	}
}

package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"telecli/internal/outbox"
	"telecli/internal/telegram"
)

// fakeTelegramSender is a scripted TelegramSender.
type fakeTelegramSender struct {
	result telegram.Message
	err    error
	calls  int
	got    struct {
		chatID telegram.ChatID
		text   string
	}
}

func (f *fakeTelegramSender) SendTextMessage(
	_ context.Context,
	chatID telegram.ChatID,
	text string,
) (telegram.Message, error) {
	f.calls++
	f.got.chatID = chatID
	f.got.text = text
	return f.result, f.err
}

// ---- Success ----

func TestTelegramOutboxSenderMapsMessage(t *testing.T) {
	fake := &fakeTelegramSender{
		result: telegram.Message{ID: 9001, ChatID: 42},
	}
	adapter := NewTelegramOutboxSender(fake)

	sent, err := adapter.SendMessage(context.Background(), 42, "hello")
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if sent.ID != 9001 {
		t.Fatalf("ID = %d, want 9001", sent.ID)
	}
	if sent.ChatID != 42 {
		t.Fatalf("ChatID = %d, want 42", sent.ChatID)
	}
	if fake.calls != 1 {
		t.Fatalf("calls = %d, want 1", fake.calls)
	}
	if fake.got.chatID != 42 {
		t.Fatalf("forwarded chat id = %d, want 42", fake.got.chatID)
	}
	if fake.got.text != "hello" {
		t.Fatalf("forwarded text = %q, want hello", fake.got.text)
	}
}

func TestTelegramOutboxSenderPreservesWhitespace(t *testing.T) {
	fake := &fakeTelegramSender{
		result: telegram.Message{ID: 1, ChatID: 42},
	}
	adapter := NewTelegramOutboxSender(fake)

	if _, err := adapter.SendMessage(
		context.Background(), 42, "  hello  ",
	); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if fake.got.text != "  hello  " {
		t.Fatalf("text = %q, want %q", fake.got.text, "  hello  ")
	}
}

func TestTelegramOutboxSenderAllowsNegativeChatID(t *testing.T) {
	fake := &fakeTelegramSender{
		result: telegram.Message{ID: 1, ChatID: -1001},
	}
	adapter := NewTelegramOutboxSender(fake)

	sent, err := adapter.SendMessage(context.Background(), -1001, "hi")
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if sent.ChatID != -1001 {
		t.Fatalf("ChatID = %d, want -1001", sent.ChatID)
	}
	if fake.got.chatID != -1001 {
		t.Fatalf("forwarded chat id = %d, want -1001", fake.got.chatID)
	}
}

// ---- Error translation ----

func TestTelegramOutboxSenderTranslatesTDLibError(t *testing.T) {
	codes := []int{400, 401, 403, 404, 420, 429, 500, 502, 503, 504, 999}

	for _, code := range codes {
		fake := &fakeTelegramSender{
			err: &telegram.TDLibError{
				Code:    code,
				Message: "tdlib-provided text",
			},
		}
		adapter := NewTelegramOutboxSender(fake)

		_, err := adapter.SendMessage(context.Background(), 42, "hi")
		if err == nil {
			t.Fatalf("code=%d: expected error", code)
		}

		sendErr, ok := outbox.SendErrorDetails(err)
		if !ok {
			t.Fatalf("code=%d: err is %T, want *outbox.SendError",
				code, err)
		}
		if sendErr.Code != code {
			t.Fatalf("code=%d: got %d", code, sendErr.Code)
		}
		if sendErr.Message != "" {
			t.Fatalf("code=%d: SendError.Message = %q, want empty",
				code, sendErr.Message)
		}
		if sendErr.Permanent || sendErr.Retryable {
			t.Fatalf("code=%d: flags should be unset: %+v", code, sendErr)
		}
	}
}

func TestTelegramOutboxSenderDoesNotLeakTDLibErrorMessage(t *testing.T) {
	secret := "private content inside tdlib message"
	fake := &fakeTelegramSender{
		err: &telegram.TDLibError{Code: 400, Message: secret},
	}
	adapter := NewTelegramOutboxSender(fake)

	_, err := adapter.SendMessage(context.Background(), 42, "hi")
	sendErr, ok := outbox.SendErrorDetails(err)
	if !ok {
		t.Fatalf("err = %T, want *outbox.SendError", err)
	}
	if sendErr.Message != "" {
		t.Fatalf("SendError.Message = %q, want empty", sendErr.Message)
	}
	if err.Error() != "outbox send error: code=400" {
		t.Fatalf("Error() = %q", err.Error())
	}
}

func TestTelegramOutboxSenderPassesThroughWrappedTDLibError(t *testing.T) {
	inner := &telegram.TDLibError{Code: 500, Message: "x"}
	wrapped := fmt.Errorf("send failed: %w", inner)

	fake := &fakeTelegramSender{err: wrapped}
	adapter := NewTelegramOutboxSender(fake)

	_, err := adapter.SendMessage(context.Background(), 42, "hi")
	sendErr, ok := outbox.SendErrorDetails(err)
	if !ok {
		t.Fatalf("wrapped TDLibError not detected: %T", err)
	}
	if sendErr.Code != 500 {
		t.Fatalf("Code = %d, want 500", sendErr.Code)
	}
}

func TestTelegramOutboxSenderPreservesContextErrors(t *testing.T) {
	for _, e := range []error{
		context.Canceled,
		context.DeadlineExceeded,
	} {
		fake := &fakeTelegramSender{err: e}
		adapter := NewTelegramOutboxSender(fake)

		_, err := adapter.SendMessage(context.Background(), 42, "hi")
		if !errors.Is(err, e) {
			t.Fatalf("err = %v, want %v", err, e)
		}
		if _, ok := outbox.SendErrorDetails(err); ok {
			t.Fatalf("err = %v must not be converted to *SendError", err)
		}
	}
}

func TestTelegramOutboxSenderPreservesGenericError(t *testing.T) {
	genErr := errors.New("network reset")
	fake := &fakeTelegramSender{err: genErr}
	adapter := NewTelegramOutboxSender(fake)

	_, err := adapter.SendMessage(context.Background(), 42, "hi")
	if !errors.Is(err, genErr) {
		t.Fatalf("err = %v, want %v", err, genErr)
	}
	if _, ok := outbox.SendErrorDetails(err); ok {
		t.Fatal("generic error must not be converted to *SendError")
	}
}

// ---- Typed-nil TDLib error ----

// A typed-nil *telegram.TDLibError must not be dereferenced and must
// not be converted to *outbox.SendError.
func TestTranslateTelegramSendErrorHandlesTypedNil(t *testing.T) {
	var tdErr *telegram.TDLibError
	var err error = tdErr

	got := translateTelegramSendError(err)

	if _, ok := outbox.SendErrorDetails(got); ok {
		t.Fatal("typed-nil TDLibError must not become SendError")
	}
}

func TestTelegramOutboxSenderHandlesTypedNilTDLibError(t *testing.T) {
	var tdErr *telegram.TDLibError

	fake := &fakeTelegramSender{err: tdErr}
	adapter := NewTelegramOutboxSender(fake)

	_, err := adapter.SendMessage(context.Background(), 42, "hi")

	// The adapter must not panic and must not produce SendError.
	if _, ok := outbox.SendErrorDetails(err); ok {
		t.Fatalf("typed-nil error converted to SendError: %v", err)
	}
}

// ---- Nil guards ----

func TestTelegramOutboxSenderNilSender(t *testing.T) {
	adapter := NewTelegramOutboxSender(nil)

	_, err := adapter.SendMessage(context.Background(), 42, "hi")
	if err == nil {
		t.Fatal("expected error for nil sender")
	}
}

func TestTelegramOutboxSenderNilReceiver(t *testing.T) {
	var adapter *TelegramOutboxSender

	_, err := adapter.SendMessage(context.Background(), 42, "hi")
	if err == nil {
		t.Fatal("expected error for nil receiver")
	}
}

// ---- Classification integration with outbox.Classify ----

func TestTelegramOutboxSenderClassifiesAsPermanent(t *testing.T) {
	fake := &fakeTelegramSender{err: &telegram.TDLibError{Code: 400}}
	adapter := NewTelegramOutboxSender(fake)

	_, err := adapter.SendMessage(context.Background(), 42, "hi")
	if got := outbox.Classify(err, true); got != outbox.OutcomePermanent {
		t.Fatalf("Outcome = %s, want permanent", got)
	}
}

func TestTelegramOutboxSenderClassifiesAsRetryable(t *testing.T) {
	for _, code := range []int{420, 429, 500, 502, 503, 504} {
		fake := &fakeTelegramSender{err: &telegram.TDLibError{Code: code}}
		adapter := NewTelegramOutboxSender(fake)

		_, err := adapter.SendMessage(context.Background(), 42, "hi")
		if got := outbox.Classify(err, true); got != outbox.OutcomeRetryable {
			t.Fatalf("code=%d: Outcome = %s, want retryable", code, got)
		}
	}
}

func TestTelegramOutboxSenderClassifiesUnknownCodeAsUncertain(t *testing.T) {
	fake := &fakeTelegramSender{err: &telegram.TDLibError{Code: 999}}
	adapter := NewTelegramOutboxSender(fake)

	_, err := adapter.SendMessage(context.Background(), 42, "hi")
	if got := outbox.Classify(err, true); got != outbox.OutcomeUncertain {
		t.Fatalf("Outcome = %s, want uncertain", got)
	}
}

func TestTelegramOutboxSenderContextErrorIsUncertain(t *testing.T) {
	fake := &fakeTelegramSender{err: context.DeadlineExceeded}
	adapter := NewTelegramOutboxSender(fake)

	_, err := adapter.SendMessage(context.Background(), 42, "hi")
	if got := outbox.Classify(err, true); got != outbox.OutcomeUncertain {
		t.Fatalf("Outcome = %s, want uncertain", got)
	}
}

func TestTelegramOutboxSenderGenericErrorIsUncertain(t *testing.T) {
	fake := &fakeTelegramSender{err: errors.New("transport reset")}
	adapter := NewTelegramOutboxSender(fake)

	_, err := adapter.SendMessage(context.Background(), 42, "hi")
	if got := outbox.Classify(err, true); got != outbox.OutcomeUncertain {
		t.Fatalf("Outcome = %s, want uncertain", got)
	}
}

// ---- Privacy: SafeReason over the translated error ----

func TestTelegramOutboxSenderSafeReasonOmitsTDLibMessage(t *testing.T) {
	secret := "private content inside tdlib message"
	fake := &fakeTelegramSender{
		err: &telegram.TDLibError{Code: 400, Message: secret},
	}
	adapter := NewTelegramOutboxSender(fake)

	_, err := adapter.SendMessage(context.Background(), 42, "hi")
	reason := outbox.SafeReason(err)

	if reason == "" {
		t.Fatal("SafeReason returned empty string")
	}
	if reason != "send error code=400" {
		t.Fatalf("SafeReason = %q, want send error code=400", reason)
	}
	for _, token := range []string{secret, "private content"} {
		if strings.Contains(reason, token) {
			t.Fatalf("SafeReason leaked token %q: %q", token, reason)
		}
	}
}

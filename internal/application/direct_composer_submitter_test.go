package application

import (
	"context"
	"errors"
	"strings"
	"testing"

	"telecli/internal/telegram"
)

type directComposerTestSender struct {
	calls  int
	chatID telegram.ChatID
	text   string
	result telegram.Message
	err    error
}

func (s *directComposerTestSender) SendTextMessage(
	_ context.Context,
	chatID telegram.ChatID,
	text string,
) (telegram.Message, error) {
	s.calls++
	s.chatID = chatID
	s.text = text
	return s.result, s.err
}

func TestNewDirectComposerSubmitterRejectsNilSender(t *testing.T) {
	t.Parallel()

	got, err := NewDirectComposerSubmitter(nil)
	if err == nil {
		t.Fatal("NewDirectComposerSubmitter() error = nil, want non-nil")
	}
	if got != nil {
		t.Fatalf("NewDirectComposerSubmitter() = %#v, want nil", got)
	}
}

func TestDirectComposerSubmitterReturnsSent(t *testing.T) {
	t.Parallel()

	sender := &directComposerTestSender{
		result: telegram.Message{ID: 9001},
	}
	submitter, err := NewDirectComposerSubmitter(sender)
	if err != nil {
		t.Fatalf("NewDirectComposerSubmitter() error = %v", err)
	}

	got, err := submitter.SubmitMessage(
		context.Background(),
		"account-1",
		42,
		"hello",
	)
	if err != nil {
		t.Fatalf("SubmitMessage() error = %v", err)
	}
	want := MessageSubmission{ID: "9001", State: MessageDeliverySent}
	if got != want {
		t.Fatalf("SubmitMessage() = %#v, want %#v", got, want)
	}
	if sender.calls != 1 {
		t.Fatalf("sender calls = %d, want 1", sender.calls)
	}
	if sender.chatID != 42 || sender.text != "hello" {
		t.Fatalf("sender args = (%v, %q), want (42, hello)", sender.chatID, sender.text)
	}
}

func TestDirectComposerSubmitterPropagatesSenderError(t *testing.T) {
	t.Parallel()

	senderErr := errors.New("send failed")
	sender := &directComposerTestSender{err: senderErr}
	submitter, err := NewDirectComposerSubmitter(sender)
	if err != nil {
		t.Fatalf("NewDirectComposerSubmitter() error = %v", err)
	}

	_, err = submitter.SubmitMessage(
		context.Background(),
		"account-1",
		42,
		"secret message payload",
	)
	if !errors.Is(err, senderErr) {
		t.Fatalf("SubmitMessage() error = %v, want wrapped sender error", err)
	}
	if strings.Contains(err.Error(), "secret message payload") {
		t.Fatal("SubmitMessage() error contains message text")
	}
}

func TestDirectComposerSubmitterPropagatesCanceledContext(t *testing.T) {
	t.Parallel()

	sender := &directComposerTestSender{}
	submitter, err := NewDirectComposerSubmitter(sender)
	if err != nil {
		t.Fatalf("NewDirectComposerSubmitter() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = submitter.SubmitMessage(ctx, "account-1", 42, "hello")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("SubmitMessage() error = %v, want context.Canceled", err)
	}
	if sender.calls != 0 {
		t.Fatalf("sender calls = %d, want 0", sender.calls)
	}
}

func TestDirectComposerSubmitterNilReceiver(t *testing.T) {
	t.Parallel()

	var submitter *DirectComposerSubmitter
	_, err := submitter.SubmitMessage(
		context.Background(),
		"account-1",
		42,
		"hello",
	)
	if !errors.Is(err, ErrMessageDeliveryUnavailable) {
		t.Fatalf("SubmitMessage() error = %v, want ErrMessageDeliveryUnavailable", err)
	}
}

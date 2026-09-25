package application

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"telecli/internal/outbox"
)

type stubDurableMessageSubmitter struct {
	queueMessage func(
		ctx context.Context,
		chatID int64,
		text string,
	) (outbox.Entry, error)
}

func (s *stubDurableMessageSubmitter) QueueMessage(
	ctx context.Context,
	chatID int64,
	text string,
) (outbox.Entry, error) {
	if s.queueMessage == nil {
		return outbox.Entry{}, errors.New("unexpected QueueMessage call")
	}
	return s.queueMessage(ctx, chatID, text)
}

func (s *stubDurableMessageSubmitter) CancelMessage(
	context.Context,
	outbox.ID,
	uint64,
) (outbox.Entry, error) {
	return outbox.Entry{}, errors.New("unexpected CancelMessage call")
}

func TestNewDurableComposerSubmitterRejectsNilSubmitter(t *testing.T) {
	t.Parallel()

	got, err := NewDurableComposerSubmitter(nil, nil)
	if err == nil {
		t.Fatal("NewDurableComposerSubmitter() error = nil, want non-nil")
	}
	if got != nil {
		t.Fatalf("NewDurableComposerSubmitter() = %#v, want nil", got)
	}
}

func TestDurableComposerSubmitterQueuesMessage(t *testing.T) {
	t.Parallel()

	const (
		accountKey = "account-1"
		chatID     = int64(42)
		text       = "message payload"
		entryID    = "entry-1"
	)

	var available atomic.Bool
	available.Store(true)

	delegate := &stubDurableMessageSubmitter{
		queueMessage: func(
			ctx context.Context,
			gotChatID int64,
			gotText string,
		) (outbox.Entry, error) {
			if err := ctx.Err(); err != nil {
				t.Fatalf("QueueMessage() context error = %v", err)
			}
			if gotChatID != chatID {
				t.Fatalf("QueueMessage() chatID = %d, want %d", gotChatID, chatID)
			}
			if gotText != text {
				t.Fatal("QueueMessage() did not receive text verbatim")
			}
			return outbox.Entry{
				ID:         outbox.ID(entryID),
				AccountKey: accountKey,
			}, nil
		},
	}

	submitter, err := NewDurableComposerSubmitter(delegate, &available)
	if err != nil {
		t.Fatalf("NewDurableComposerSubmitter() error = %v", err)
	}

	got, err := submitter.SubmitMessage(
		context.Background(),
		accountKey,
		chatID,
		text,
	)
	if err != nil {
		t.Fatalf("SubmitMessage() error = %v", err)
	}

	want := MessageSubmission{
		ID:    entryID,
		State: MessageDeliveryQueued,
	}
	if got != want {
		t.Fatalf("SubmitMessage() = %#v, want %#v", got, want)
	}
}

func TestDurableComposerSubmitterRejectsUnavailableRuntime(t *testing.T) {
	t.Parallel()

	var called atomic.Bool
	var available atomic.Bool
	available.Store(false)

	delegate := &stubDurableMessageSubmitter{
		queueMessage: func(context.Context, int64, string) (outbox.Entry, error) {
			called.Store(true)
			return outbox.Entry{}, nil
		},
	}

	submitter, err := NewDurableComposerSubmitter(delegate, &available)
	if err != nil {
		t.Fatalf("NewDurableComposerSubmitter() error = %v", err)
	}

	_, err = submitter.SubmitMessage(
		context.Background(),
		"account-1",
		42,
		"message payload",
	)
	if !errors.Is(err, ErrMessageDeliveryUnavailable) {
		t.Fatalf("SubmitMessage() error = %v, want ErrMessageDeliveryUnavailable", err)
	}
	if called.Load() {
		t.Fatal("QueueMessage() was called for unavailable runtime")
	}
}

func TestDurableComposerSubmitterRejectsNilReceiver(t *testing.T) {
	t.Parallel()

	var submitter *DurableComposerSubmitter
	_, err := submitter.SubmitMessage(
		context.Background(),
		"account-1",
		42,
		"message payload",
	)
	if !errors.Is(err, ErrMessageDeliveryUnavailable) {
		t.Fatalf("SubmitMessage() error = %v, want ErrMessageDeliveryUnavailable", err)
	}
}

func TestDurableComposerSubmitterPropagatesCanceledContext(t *testing.T) {
	t.Parallel()

	var called atomic.Bool
	delegate := &stubDurableMessageSubmitter{
		queueMessage: func(context.Context, int64, string) (outbox.Entry, error) {
			called.Store(true)
			return outbox.Entry{}, nil
		},
	}

	submitter, err := NewDurableComposerSubmitter(delegate, nil)
	if err != nil {
		t.Fatalf("NewDurableComposerSubmitter() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = submitter.SubmitMessage(
		ctx,
		"account-1",
		42,
		"message payload",
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("SubmitMessage() error = %v, want context.Canceled", err)
	}
	if called.Load() {
		t.Fatal("QueueMessage() was called with canceled context")
	}
}

func TestDurableComposerSubmitterPropagatesQueueErrorWithoutText(t *testing.T) {
	t.Parallel()

	const text = "secret message payload"
	queueErr := errors.New("store unavailable")
	delegate := &stubDurableMessageSubmitter{
		queueMessage: func(context.Context, int64, string) (outbox.Entry, error) {
			return outbox.Entry{}, queueErr
		},
	}

	submitter, err := NewDurableComposerSubmitter(delegate, nil)
	if err != nil {
		t.Fatalf("NewDurableComposerSubmitter() error = %v", err)
	}

	_, err = submitter.SubmitMessage(
		context.Background(),
		"account-1",
		42,
		text,
	)
	if !errors.Is(err, queueErr) {
		t.Fatalf("SubmitMessage() error = %v, want wrapped queue error", err)
	}
	if strings.Contains(err.Error(), text) {
		t.Fatal("SubmitMessage() error contains message text")
	}
}

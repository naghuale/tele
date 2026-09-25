package application

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"telecli/internal/outbox"
)

func TestProjectMessageState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		state outbox.State
		want  MessageDeliveryState
	}{
		{
			name:  "queued",
			state: outbox.StateQueued,
			want:  MessageDeliveryQueued,
		},
		{
			name:  "dispatching",
			state: outbox.StateDispatching,
			want:  MessageDeliverySending,
		},
		{
			name:  "accepted",
			state: outbox.StateAccepted,
			want:  MessageDeliverySent,
		},
		{
			name:  "failed retryable",
			state: outbox.StateFailedRetryable,
			want:  MessageDeliveryRetrying,
		},
		{
			name:  "failed permanent",
			state: outbox.StateFailedPermanent,
			want:  MessageDeliveryFailed,
		},
		{
			name:  "uncertain",
			state: outbox.StateUncertain,
			want:  MessageDeliveryUncertain,
		},
		{
			name:  "canceled",
			state: outbox.StateCanceled,
			want:  MessageDeliveryCanceled,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := projectMessageState(test.state)
			if err != nil {
				t.Fatalf("projectMessageState() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("projectMessageState() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestProjectMessageStateRejectsUnknownState(t *testing.T) {
	t.Parallel()

	got, err := projectMessageState(outbox.State("future"))
	if err == nil {
		t.Fatal("projectMessageState() error = nil, want non-nil")
	}
	if got != "" {
		t.Fatalf("projectMessageState() = %q, want empty", got)
	}
}

func TestProjectMessageStatus(t *testing.T) {
	t.Parallel()

	now := time.Unix(1700000000, 123).UTC()
	next := now.Add(time.Minute)
	entry := outbox.Entry{
		ID:                outbox.ID("entry-1"),
		AccountKey:        "account-1",
		ChatID:            42,
		Text:              "secret message payload",
		State:             outbox.StateDispatching,
		AttemptCount:      2,
		NextAttempt:       next,
		UpdatedAt:         now,
		LastErrorMessage:  "provider failure secret message payload",
		LastErrorCode:     500,
		TelegramMessageID: 99,
		Version:           3,
	}

	got, err := projectMessageStatus(entry)
	if err != nil {
		t.Fatalf("projectMessageStatus() error = %v", err)
	}

	want := MessageStatus{
		EntryID:       "entry-1",
		AccountKey:    "account-1",
		ChatID:        42,
		State:         MessageDeliverySending,
		Attempt:       2,
		NextAttemptAt: next,
		UpdatedAt:     now,
	}
	if got != want {
		t.Fatalf("projectMessageStatus() = %#v, want %#v", got, want)
	}
}

func TestProjectMessageStatusRejectsUnknownState(t *testing.T) {
	t.Parallel()

	entry := outbox.Entry{
		ID:         "entry-1",
		AccountKey: "account-1",
		ChatID:     42,
		State:      outbox.State("future"),
	}

	got, err := projectMessageStatus(entry)
	if err == nil {
		t.Fatal("projectMessageStatus() error = nil, want non-nil")
	}
	if got != (MessageStatus{}) {
		t.Fatalf("projectMessageStatus() = %#v, want zero status", got)
	}
}

func TestProjectMessageStatusDoesNotExposeText(t *testing.T) {
	t.Parallel()

	const text = "secret message payload"
	entry := outbox.Entry{
		ID:               "entry-1",
		AccountKey:       "account-1",
		ChatID:           42,
		Text:             text,
		State:            outbox.StateQueued,
		LastErrorMessage: "provider failure " + text,
	}

	status, err := projectMessageStatus(entry)
	if err != nil {
		t.Fatalf("projectMessageStatus() error = %v", err)
	}
	if strings.Contains(fmt.Sprintf("%+v", status), text) {
		t.Fatal("projectMessageStatus() exposed message text")
	}
}

func TestProjectMessageStatusErrorDoesNotExposeText(t *testing.T) {
	t.Parallel()

	const text = "secret message payload"
	entry := outbox.Entry{
		ID:               "entry-1",
		AccountKey:       "account-1",
		ChatID:           42,
		Text:             text,
		State:            outbox.State("unknown"),
		LastErrorMessage: "provider failure " + text,
	}

	_, err := projectMessageStatus(entry)
	if err == nil {
		t.Fatal("projectMessageStatus() error = nil, want non-nil")
	}
	if strings.Contains(err.Error(), text) {
		t.Fatal("projectMessageStatus() error contains message text")
	}
}

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

func TestProjectEntryStatus(t *testing.T) {
	t.Parallel()

	now := time.Unix(1700000000, 789).UTC()
	next := now.Add(90 * time.Second)

	tests := []struct {
		name   string
		status outbox.EntryStatus
		want   MessageStatus
	}{
		{
			name: "queued",
			status: outbox.EntryStatus{
				ID:         "entry-1",
				AccountKey: "account-1",
				ChatID:     42,
				State:      outbox.StateQueued,
				Attempt:    0,
				UpdatedAt:  now,
			},
			want: MessageStatus{
				EntryID:    "entry-1",
				AccountKey: "account-1",
				ChatID:     42,
				State:      MessageDeliveryQueued,
				UpdatedAt:  now,
			},
		},
		{
			name: "retrying keeps next attempt",
			status: outbox.EntryStatus{
				ID:            "entry-2",
				AccountKey:    "account-1",
				ChatID:        42,
				State:         outbox.StateFailedRetryable,
				Attempt:       4,
				NextAttemptAt: next,
				UpdatedAt:     now,
			},
			want: MessageStatus{
				EntryID:       "entry-2",
				AccountKey:    "account-1",
				ChatID:        42,
				State:         MessageDeliveryRetrying,
				Attempt:       4,
				NextAttemptAt: next,
				UpdatedAt:     now,
			},
		},
		{
			name: "uncertain ignores version",
			status: outbox.EntryStatus{
				ID:         "entry-3",
				AccountKey: "account-1",
				ChatID:     42,
				State:      outbox.StateUncertain,
				Attempt:    2,
				UpdatedAt:  now,
				Version:    17,
			},
			want: MessageStatus{
				EntryID:    "entry-3",
				AccountKey: "account-1",
				ChatID:     42,
				State:      MessageDeliveryUncertain,
				Attempt:    2,
				UpdatedAt:  now,
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := projectEntryStatus(test.status)
			if err != nil {
				t.Fatalf("projectEntryStatus() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("projectEntryStatus() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestProjectEntryStatusRejectsUnknownState(t *testing.T) {
	t.Parallel()

	now := time.Unix(1700000000, 0).UTC()
	status := outbox.EntryStatus{
		ID:         "entry-1",
		AccountKey: "account-1",
		ChatID:     42,
		State:      outbox.State("future"),
		UpdatedAt:  now,
	}

	got, err := projectEntryStatus(status)
	if err == nil {
		t.Fatal("projectEntryStatus() error = nil, want non-nil")
	}
	if got != (MessageStatus{}) {
		t.Fatalf("projectEntryStatus() = %#v, want zero status", got)
	}
	if !strings.Contains(err.Error(), "entry-1") {
		t.Fatalf("projectEntryStatus() error = %v, want entry id", err)
	}
}

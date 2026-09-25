package application

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"telecli/internal/outbox"
)

type h6bFakeEntryStatusReader struct {
	entries []outbox.EntryStatus
	err     error

	mu        sync.Mutex
	calls     int
	lastQuery outbox.ListEntryStatusesQuery
}

func (r *h6bFakeEntryStatusReader) ListEntryStatuses(
	_ context.Context,
	query outbox.ListEntryStatusesQuery,
) ([]outbox.EntryStatus, error) {
	r.mu.Lock()
	r.calls++
	r.lastQuery = query
	r.mu.Unlock()

	if r.err != nil {
		return nil, r.err
	}
	return r.entries, nil
}

func (r *h6bFakeEntryStatusReader) observed() (int, outbox.ListEntryStatusesQuery) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls, r.lastQuery
}

func TestNewOutboxMessageStatusSourceRejectsNilReader(t *testing.T) {
	t.Parallel()

	source, err := NewOutboxMessageStatusSource(nil, 0)
	if err == nil {
		t.Fatal("NewOutboxMessageStatusSource() error = nil, want non-nil")
	}
	if source != nil {
		t.Fatalf("NewOutboxMessageStatusSource() = %#v, want nil", source)
	}
}

func TestNewOutboxMessageStatusSourceRejectsNegativeLimit(t *testing.T) {
	t.Parallel()

	source, err := NewOutboxMessageStatusSource(
		&h6bFakeEntryStatusReader{},
		-1,
	)
	if err == nil {
		t.Fatal("NewOutboxMessageStatusSource() error = nil, want non-nil")
	}
	if source != nil {
		t.Fatalf("NewOutboxMessageStatusSource() = %#v, want nil", source)
	}
}

func TestNewOutboxMessageStatusSourceAcceptsDefaultLimit(t *testing.T) {
	t.Parallel()

	source, err := NewOutboxMessageStatusSource(
		&h6bFakeEntryStatusReader{},
		0,
	)
	if err != nil {
		t.Fatalf("NewOutboxMessageStatusSource() error = %v", err)
	}
	if source == nil {
		t.Fatal("NewOutboxMessageStatusSource() = nil, want source")
	}
	if source.limit != 0 {
		t.Fatalf("source limit = %d, want 0", source.limit)
	}
}

func TestOutboxMessageStatusSourceRejectsNilReceiver(t *testing.T) {
	t.Parallel()

	var source *OutboxMessageStatusSource
	statuses, err := source.ListMessageStatuses(
		context.Background(),
		"account-1",
		42,
	)
	if err == nil {
		t.Fatal("ListMessageStatuses() error = nil, want non-nil")
	}
	if statuses != nil {
		t.Fatalf("ListMessageStatuses() = %#v, want nil", statuses)
	}
}

func TestOutboxMessageStatusSourceRejectsNilContext(t *testing.T) {
	t.Parallel()

	source, err := NewOutboxMessageStatusSource(
		&h6bFakeEntryStatusReader{},
		0,
	)
	if err != nil {
		t.Fatalf("NewOutboxMessageStatusSource() error = %v", err)
	}

	statuses, err := source.ListMessageStatuses(nil, "account-1", 42)
	if err == nil {
		t.Fatal("ListMessageStatuses() error = nil, want non-nil")
	}
	if statuses != nil {
		t.Fatalf("ListMessageStatuses() = %#v, want nil", statuses)
	}
}

func TestOutboxMessageStatusSourceRejectsCanceledContext(t *testing.T) {
	t.Parallel()

	reader := &h6bFakeEntryStatusReader{}
	source, err := NewOutboxMessageStatusSource(reader, 0)
	if err != nil {
		t.Fatalf("NewOutboxMessageStatusSource() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	statuses, err := source.ListMessageStatuses(ctx, "account-1", 42)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ListMessageStatuses() error = %v, want context.Canceled", err)
	}
	if statuses != nil {
		t.Fatalf("ListMessageStatuses() = %#v, want nil", statuses)
	}
	if calls, _ := reader.observed(); calls != 0 {
		t.Fatalf("reader calls = %d, want 0", calls)
	}
}

func TestOutboxMessageStatusSourceForwardsQuery(t *testing.T) {
	t.Parallel()

	reader := &h6bFakeEntryStatusReader{}
	source, err := NewOutboxMessageStatusSource(reader, 7)
	if err != nil {
		t.Fatalf("NewOutboxMessageStatusSource() error = %v", err)
	}

	if _, err := source.ListMessageStatuses(
		context.Background(),
		"account-1",
		42,
	); err != nil {
		t.Fatalf("ListMessageStatuses() error = %v", err)
	}

	calls, query := reader.observed()
	if calls != 1 {
		t.Fatalf("reader calls = %d, want 1", calls)
	}
	want := outbox.ListEntryStatusesQuery{
		AccountKey: "account-1",
		ChatID:     42,
		Limit:      7,
	}
	if query != want {
		t.Fatalf("reader query = %#v, want %#v", query, want)
	}
}

func TestOutboxMessageStatusSourceKeepsDefaultLimitForStorePolicy(t *testing.T) {
	t.Parallel()

	reader := &h6bFakeEntryStatusReader{}
	source, err := NewOutboxMessageStatusSource(reader, 0)
	if err != nil {
		t.Fatalf("NewOutboxMessageStatusSource() error = %v", err)
	}

	if _, err := source.ListMessageStatuses(
		context.Background(),
		"account-2",
		7,
	); err != nil {
		t.Fatalf("ListMessageStatuses() error = %v", err)
	}

	_, query := reader.observed()
	if query.Limit != 0 {
		t.Fatalf("reader query limit = %d, want 0", query.Limit)
	}
}

func TestOutboxMessageStatusSourceProjectsMetadata(t *testing.T) {
	t.Parallel()

	now := time.Unix(1700000000, 456).UTC()
	next := now.Add(2 * time.Minute)
	reader := &h6bFakeEntryStatusReader{
		entries: []outbox.EntryStatus{
			{
				ID:         "entry-1",
				AccountKey: "account-1",
				ChatID:     42,
				State:      outbox.StateDispatching,
				Attempt:    1,
				UpdatedAt:  now,
				Version:    9,
			},
			{
				ID:            "entry-2",
				AccountKey:    "account-1",
				ChatID:        42,
				State:         outbox.StateFailedRetryable,
				Attempt:       3,
				NextAttemptAt: next,
				UpdatedAt:     now,
			},
		},
	}
	source, err := NewOutboxMessageStatusSource(reader, 0)
	if err != nil {
		t.Fatalf("NewOutboxMessageStatusSource() error = %v", err)
	}

	statuses, err := source.ListMessageStatuses(
		context.Background(),
		"account-1",
		42,
	)
	if err != nil {
		t.Fatalf("ListMessageStatuses() error = %v", err)
	}

	want := []MessageStatus{
		{
			EntryID:    "entry-1",
			AccountKey: "account-1",
			ChatID:     42,
			State:      MessageDeliverySending,
			Attempt:    1,
			UpdatedAt:  now,
		},
		{
			EntryID:       "entry-2",
			AccountKey:    "account-1",
			ChatID:        42,
			State:         MessageDeliveryRetrying,
			Attempt:       3,
			NextAttemptAt: next,
			UpdatedAt:     now,
		},
	}
	if !reflect.DeepEqual(statuses, want) {
		t.Fatalf("ListMessageStatuses() = %#v, want %#v", statuses, want)
	}
}

func TestOutboxMessageStatusSourceReturnsNonNilEmptySlice(t *testing.T) {
	t.Parallel()

	reader := &h6bFakeEntryStatusReader{}
	source, err := NewOutboxMessageStatusSource(reader, 0)
	if err != nil {
		t.Fatalf("NewOutboxMessageStatusSource() error = %v", err)
	}

	statuses, err := source.ListMessageStatuses(
		context.Background(),
		"account-1",
		42,
	)
	if err != nil {
		t.Fatalf("ListMessageStatuses() error = %v", err)
	}
	if statuses == nil {
		t.Fatal("ListMessageStatuses() = nil, want non-nil empty slice")
	}
	if len(statuses) != 0 {
		t.Fatalf("ListMessageStatuses() = %#v, want empty", statuses)
	}
}

func TestOutboxMessageStatusSourceWrapsReaderError(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("read failed")
	reader := &h6bFakeEntryStatusReader{err: sentinel}
	source, err := NewOutboxMessageStatusSource(reader, 0)
	if err != nil {
		t.Fatalf("NewOutboxMessageStatusSource() error = %v", err)
	}

	statuses, err := source.ListMessageStatuses(
		context.Background(),
		"account-1",
		42,
	)
	if !errors.Is(err, sentinel) {
		t.Fatalf("ListMessageStatuses() error = %v, want wrapped %v", err, sentinel)
	}
	if statuses != nil {
		t.Fatalf("ListMessageStatuses() = %#v, want nil", statuses)
	}
}

func TestOutboxMessageStatusSourceRejectsUnknownState(t *testing.T) {
	t.Parallel()

	now := time.Unix(1700000000, 0).UTC()
	reader := &h6bFakeEntryStatusReader{
		entries: []outbox.EntryStatus{
			{
				ID:         "entry-1",
				AccountKey: "account-1",
				ChatID:     42,
				State:      outbox.StateQueued,
				UpdatedAt:  now,
			},
			{
				ID:         "entry-2",
				AccountKey: "account-1",
				ChatID:     42,
				State:      outbox.State("future"),
				UpdatedAt:  now,
			},
		},
	}
	source, err := NewOutboxMessageStatusSource(reader, 0)
	if err != nil {
		t.Fatalf("NewOutboxMessageStatusSource() error = %v", err)
	}

	statuses, err := source.ListMessageStatuses(
		context.Background(),
		"account-1",
		42,
	)
	if err == nil {
		t.Fatal("ListMessageStatuses() error = nil, want non-nil")
	}
	if statuses != nil {
		t.Fatalf("ListMessageStatuses() = %#v, want nil", statuses)
	}
	if !strings.Contains(err.Error(), "entry-2") {
		t.Fatalf("ListMessageStatuses() error = %v, want entry id", err)
	}
}

func TestOutboxMessageStatusSourceDoesNotExposePayloadFields(t *testing.T) {
	t.Parallel()

	now := time.Unix(1700000000, 0).UTC()
	reader := &h6bFakeEntryStatusReader{
		entries: []outbox.EntryStatus{
			{
				ID:         "entry-1",
				AccountKey: "account-1",
				ChatID:     42,
				State:      outbox.StateAccepted,
				Attempt:    1,
				UpdatedAt:  now,
			},
		},
	}
	source, err := NewOutboxMessageStatusSource(reader, 0)
	if err != nil {
		t.Fatalf("NewOutboxMessageStatusSource() error = %v", err)
	}

	statuses, err := source.ListMessageStatuses(
		context.Background(),
		"account-1",
		42,
	)
	if err != nil {
		t.Fatalf("ListMessageStatuses() error = %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("ListMessageStatuses() len = %d, want 1", len(statuses))
	}

	printed := fmt.Sprintf("%+v", statuses[0])
	for _, forbidden := range []string{
		"Text",
		"LastError",
		"Ciphertext",
		"Decrypt",
		"Payload",
	} {
		if strings.Contains(printed, forbidden) {
			t.Fatalf("status %q exposes %q", printed, forbidden)
		}
	}
}

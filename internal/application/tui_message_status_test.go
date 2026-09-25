package application

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"telecli/internal/tui"
)

type h6c2aStubStatusSource struct {
	listMessageStatuses func(
		context.Context,
		string,
		int64,
	) ([]MessageStatus, error)
}

func (s *h6c2aStubStatusSource) ListMessageStatuses(
	ctx context.Context,
	accountKey string,
	chatID int64,
) ([]MessageStatus, error) {
	if s.listMessageStatuses == nil {
		return nil, errors.New("unexpected ListMessageStatuses call")
	}
	return s.listMessageStatuses(ctx, accountKey, chatID)
}

var _ MessageStatusSource = (*h6c2aStubStatusSource)(nil)

func TestNewTUIMessageStatusSourceAdapterReturnsNilForNilSource(t *testing.T) {
	t.Parallel()

	got := newTUIMessageStatusSourceAdapter(nil)
	if got != nil {
		t.Fatalf("newTUIMessageStatusSourceAdapter() = %#v, want nil", got)
	}
}

func TestTUIMessageStatusSourceAdapterProjectsAllStates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		from MessageDeliveryState
		want tui.MessageDeliveryState
	}{
		{
			name: "queued",
			from: MessageDeliveryQueued,
			want: tui.MessageDeliveryQueued,
		},
		{
			name: "sending",
			from: MessageDeliverySending,
			want: tui.MessageDeliverySending,
		},
		{
			name: "retrying",
			from: MessageDeliveryRetrying,
			want: tui.MessageDeliveryRetrying,
		},
		{
			name: "failed",
			from: MessageDeliveryFailed,
			want: tui.MessageDeliveryFailed,
		},
		{
			name: "uncertain",
			from: MessageDeliveryUncertain,
			want: tui.MessageDeliveryUncertain,
		},
		{
			name: "sent",
			from: MessageDeliverySent,
			want: tui.MessageDeliverySent,
		},
		{
			name: "canceled",
			from: MessageDeliveryCanceled,
			want: tui.MessageDeliveryCanceled,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := projectTUIMessageDeliveryState(test.from)
			if err != nil {
				t.Fatalf("projectTUIMessageDeliveryState() error = %v", err)
			}
			if got != test.want {
				t.Fatalf(
					"projectTUIMessageDeliveryState() = %q, want %q",
					got,
					test.want,
				)
			}
		})
	}
}

func TestProjectTUIMessageDeliveryStateRejectsUnknownState(t *testing.T) {
	t.Parallel()

	got, err := projectTUIMessageDeliveryState(
		MessageDeliveryState("unknown"),
	)
	if err == nil {
		t.Fatal("projectTUIMessageDeliveryState() error = nil, want non-nil")
	}
	if got != "" {
		t.Fatalf("projectTUIMessageDeliveryState() = %q, want empty", got)
	}
}

func TestTUIMessageStatusSourceAdapterProjectsAllMetadataAndPreservesOrder(t *testing.T) {
	t.Parallel()

	now := time.Unix(1700000000, 111).UTC()
	input := []MessageStatus{
		{
			EntryID:       "entry-newer",
			AccountKey:    "account-1",
			ChatID:        42,
			State:         MessageDeliveryQueued,
			Attempt:       1,
			NextAttemptAt: now.Add(time.Minute),
			UpdatedAt:     now,
		},
		{
			EntryID:    "entry-older",
			AccountKey: "account-1",
			ChatID:     42,
			State:      MessageDeliveryRetrying,
			Attempt:    2,
			UpdatedAt:  now.Add(-time.Minute),
		},
	}

	source := &h6c2aStubStatusSource{
		listMessageStatuses: func(
			context.Context,
			string,
			int64,
		) ([]MessageStatus, error) {
			return input, nil
		},
	}

	adapter := newTUIMessageStatusSourceAdapter(source)
	got, err := adapter.ListMessageStatuses(
		context.Background(),
		"account-1",
		42,
	)
	if err != nil {
		t.Fatalf("ListMessageStatuses() error = %v", err)
	}

	want := []tui.MessageStatus{
		{
			EntryID:       "entry-newer",
			AccountKey:    "account-1",
			ChatID:        42,
			State:         tui.MessageDeliveryQueued,
			Attempt:       1,
			NextAttemptAt: now.Add(time.Minute),
			UpdatedAt:     now,
		},
		{
			EntryID:    "entry-older",
			AccountKey: "account-1",
			ChatID:     42,
			State:      tui.MessageDeliveryRetrying,
			Attempt:    2,
			UpdatedAt:  now.Add(-time.Minute),
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListMessageStatuses() = %#v, want %#v", got, want)
	}
}

func TestTUIMessageStatusSourceAdapterReturnsNonNilEmptySlice(t *testing.T) {
	t.Parallel()

	source := &h6c2aStubStatusSource{
		listMessageStatuses: func(
			context.Context,
			string,
			int64,
		) ([]MessageStatus, error) {
			return nil, nil
		},
	}

	adapter := newTUIMessageStatusSourceAdapter(source)
	got, err := adapter.ListMessageStatuses(
		context.Background(),
		"account-1",
		42,
	)
	if err != nil {
		t.Fatalf("ListMessageStatuses() error = %v", err)
	}
	if got == nil {
		t.Fatal("ListMessageStatuses() = nil, want non-nil empty slice")
	}
	if len(got) != 0 {
		t.Fatalf("ListMessageStatuses() = %#v, want empty", got)
	}
}

func TestTUIMessageStatusSourceAdapterForwardsQuery(t *testing.T) {
	t.Parallel()

	const (
		accountKey = "account-1"
		chatID     = int64(42)
	)

	var gotAccountKey string
	var gotChatID int64
	source := &h6c2aStubStatusSource{
		listMessageStatuses: func(
			_ context.Context,
			key string,
			id int64,
		) ([]MessageStatus, error) {
			gotAccountKey = key
			gotChatID = id
			return nil, nil
		},
	}

	adapter := newTUIMessageStatusSourceAdapter(source)
	if _, err := adapter.ListMessageStatuses(
		context.Background(),
		accountKey,
		chatID,
	); err != nil {
		t.Fatalf("ListMessageStatuses() error = %v", err)
	}
	if gotAccountKey != accountKey {
		t.Fatalf("accountKey = %q, want %q", gotAccountKey, accountKey)
	}
	if gotChatID != chatID {
		t.Fatalf("chatID = %d, want %d", gotChatID, chatID)
	}
}

func TestTUIMessageStatusSourceAdapterPreservesSourceError(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("status source unavailable")
	source := &h6c2aStubStatusSource{
		listMessageStatuses: func(
			context.Context,
			string,
			int64,
		) ([]MessageStatus, error) {
			return nil, sentinel
		},
	}

	adapter := newTUIMessageStatusSourceAdapter(source)
	got, err := adapter.ListMessageStatuses(
		context.Background(),
		"account-1",
		42,
	)
	if !errors.Is(err, sentinel) {
		t.Fatalf("ListMessageStatuses() error = %v, want wrapped %v", err, sentinel)
	}
	if got != nil {
		t.Fatalf("ListMessageStatuses() = %#v, want nil", got)
	}
}

func TestTUIMessageStatusSourceAdapterPropagatesCanceledContext(t *testing.T) {
	t.Parallel()

	var called atomic.Bool
	source := &h6c2aStubStatusSource{
		listMessageStatuses: func(
			context.Context,
			string,
			int64,
		) ([]MessageStatus, error) {
			called.Store(true)
			return nil, nil
		},
	}

	adapter := newTUIMessageStatusSourceAdapter(source)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	got, err := adapter.ListMessageStatuses(ctx, "account-1", 42)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ListMessageStatuses() error = %v, want context.Canceled", err)
	}
	if got != nil {
		t.Fatalf("ListMessageStatuses() = %#v, want nil", got)
	}
	if called.Load() {
		t.Fatal("application status source was called with canceled context")
	}
}

func TestTUIMessageStatusSourceAdapterReturnsNoPartialSnapshot(t *testing.T) {
	t.Parallel()

	source := &h6c2aStubStatusSource{
		listMessageStatuses: func(
			context.Context,
			string,
			int64,
		) ([]MessageStatus, error) {
			return []MessageStatus{
				{
					EntryID: "entry-valid",
					State:   MessageDeliveryQueued,
				},
				{
					EntryID: "entry-invalid",
					State:   MessageDeliveryState("unknown"),
				},
			}, nil
		},
	}

	adapter := newTUIMessageStatusSourceAdapter(source)
	got, err := adapter.ListMessageStatuses(
		context.Background(),
		"account-1",
		42,
	)
	if err == nil {
		t.Fatal("ListMessageStatuses() error = nil, want non-nil")
	}
	if got != nil {
		t.Fatalf("ListMessageStatuses() = %#v, want nil", got)
	}
	if !strings.Contains(err.Error(), "entry-invalid") {
		t.Fatalf("ListMessageStatuses() error = %v, want entry id", err)
	}
}

func TestTUIMessageStatusSourceAdapterErrorDoesNotContainSensitiveMetadata(t *testing.T) {
	t.Parallel()

	const sensitive = "sensitive-account-key"

	source := &h6c2aStubStatusSource{
		listMessageStatuses: func(
			context.Context,
			string,
			int64,
		) ([]MessageStatus, error) {
			return []MessageStatus{
				{
					EntryID:    "entry-invalid",
					AccountKey: sensitive,
					ChatID:     42,
					State:      MessageDeliveryState("unknown"),
				},
			}, nil
		},
	}

	adapter := newTUIMessageStatusSourceAdapter(source)
	_, err := adapter.ListMessageStatuses(context.Background(), sensitive, 42)
	if err == nil {
		t.Fatal("ListMessageStatuses() error = nil, want non-nil")
	}
	if strings.Contains(err.Error(), sensitive) {
		t.Fatal("ListMessageStatuses() error contains sensitive metadata")
	}
}

func TestTUIMessageStatusSourceAdapterNilSource(t *testing.T) {
	t.Parallel()

	var adapter *tuiMessageStatusSourceAdapter

	got, err := adapter.ListMessageStatuses(
		context.Background(),
		"account-1",
		42,
	)
	if !errors.Is(err, ErrMessageStatusUnavailable) {
		t.Fatalf(
			"ListMessageStatuses() error = %v, want ErrMessageStatusUnavailable",
			err,
		)
	}
	if got != nil {
		t.Fatalf("ListMessageStatuses() = %#v, want nil", got)
	}
}

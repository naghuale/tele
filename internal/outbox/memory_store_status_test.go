package outbox

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestMemoryStoreListEntryStatusesRejectsInvalidStoredMetadata(t *testing.T) {
	t.Parallel()

	store := NewMemoryStore()
	store.entries["bad"] = Entry{
		ID:         "bad",
		AccountKey: "account-1",
		ChatID:     42,
		State:      State("future"),
		UpdatedAt:  time.Unix(1700000000, 0).UTC(),
	}

	_, err := store.ListEntryStatuses(context.Background(), ListEntryStatusesQuery{
		AccountKey: "account-1",
		ChatID:     42,
	})
	if !errors.Is(err, ErrInvalidEntry) {
		t.Fatalf("error = %v, want ErrInvalidEntry", err)
	}
	if strings.Contains(err.Error(), "payload") {
		t.Fatal("status error contains payload")
	}
}

func TestMemoryStoreListEntryStatusesPropagatesCanceledContext(t *testing.T) {
	t.Parallel()

	store := NewMemoryStore()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.ListEntryStatuses(ctx, ListEntryStatusesQuery{
		AccountKey: "account-1",
		ChatID:     42,
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

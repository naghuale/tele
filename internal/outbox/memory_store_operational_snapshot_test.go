package outbox

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMemoryStoreOperationalSnapshotEmpty(t *testing.T) {
	t.Parallel()

	store := NewMemoryStore()
	got, err := store.ReadOperationalSnapshot(context.Background())
	if err != nil {
		t.Fatalf("ReadOperationalSnapshot() error = %v", err)
	}
	if got != (OperationalSnapshot{}) {
		t.Fatalf("snapshot = %#v, want zero", got)
	}
}

func TestMemoryStoreOperationalSnapshotCountsStates(t *testing.T) {
	t.Parallel()

	store := NewMemoryStore()
	base := time.Unix(1700000000, 0).UTC()
	for index, state := range []State{
		StateQueued,
		StateQueued,
		StateDispatching,
		StateAccepted,
		StateFailedRetryable,
		StateFailedPermanent,
		StateUncertain,
		StateCanceled,
	} {
		seedStatusEntry(
			t,
			store,
			"m-"+string(rune('a'+index)),
			"account-1",
			42,
			base.Add(time.Duration(index)*time.Second),
			state,
		)
	}

	got, err := store.ReadOperationalSnapshot(context.Background())
	if err != nil {
		t.Fatalf("ReadOperationalSnapshot() error = %v", err)
	}
	want := OperationalSnapshot{
		Queued:          2,
		Dispatching:     1,
		Accepted:        1,
		FailedRetryable: 1,
		FailedPermanent: 1,
		Uncertain:       1,
		Canceled:        1,
	}
	if got != want {
		t.Fatalf("snapshot = %#v, want %#v", got, want)
	}
}

func TestMemoryStoreOperationalSnapshotReflectsTransitions(t *testing.T) {
	t.Parallel()

	store := NewMemoryStore()
	at := time.Unix(1700000000, 0).UTC()
	seedStatusEntry(t, store, "m-a", "account-1", 42, at, StateQueued)

	entry, err := store.Get(context.Background(), "m-a")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	claimed, err := store.Claim(
		context.Background(),
		entry.ID,
		entry.Version,
		"owner",
		at.Add(time.Hour),
		at,
	)
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if _, err := store.MarkRetryable(
		context.Background(),
		entry.ID,
		claimed.Version,
		at.Add(time.Minute),
		500,
		"retry",
		at.Add(time.Second),
	); err != nil {
		t.Fatalf("MarkRetryable() error = %v", err)
	}

	got, err := store.ReadOperationalSnapshot(context.Background())
	if err != nil {
		t.Fatalf("ReadOperationalSnapshot() error = %v", err)
	}
	want := OperationalSnapshot{FailedRetryable: 1}
	if got != want {
		t.Fatalf("snapshot = %#v, want %#v", got, want)
	}
}

func TestMemoryStoreOperationalSnapshotCanceledContext(t *testing.T) {
	t.Parallel()

	store := NewMemoryStore()
	seedStatusEntry(
		t,
		store,
		"m-a",
		"account-1",
		42,
		time.Unix(1700000000, 0).UTC(),
		StateQueued,
	)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	got, err := store.ReadOperationalSnapshot(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if got != (OperationalSnapshot{}) {
		t.Fatalf("snapshot = %#v, want zero", got)
	}
}

func TestMemoryStoreOperationalSnapshotNilReceiver(t *testing.T) {
	t.Parallel()

	var store *MemoryStore
	got, err := store.ReadOperationalSnapshot(context.Background())
	if err == nil {
		t.Fatal("ReadOperationalSnapshot() error = nil, want non-nil")
	}
	if got != (OperationalSnapshot{}) {
		t.Fatalf("snapshot = %#v, want zero", got)
	}
}

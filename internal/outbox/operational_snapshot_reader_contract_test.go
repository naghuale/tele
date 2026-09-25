package outbox

import (
	"context"
	"errors"
	"testing"
	"time"
)

type operationalSnapshotReaderFactory func(
	*testing.T,
) (Store, OperationalSnapshotReader)

// runOperationalSnapshotReaderContract is the shared contract for
// payload-free operational snapshots.
//
// It intentionally avoids readiness semantics: "ready" is defined by
// ListReady, and the operational snapshot must not invent a second
// definition.
func runOperationalSnapshotReaderContract(
	t *testing.T,
	factory operationalSnapshotReaderFactory,
) {
	t.Helper()

	t.Run("returns zero snapshot for empty store", func(t *testing.T) {
		_, reader := factory(t)
		got, err := reader.ReadOperationalSnapshot(context.Background())
		if err != nil {
			t.Fatalf("ReadOperationalSnapshot() error = %v", err)
		}
		if got != (OperationalSnapshot{}) {
			t.Fatalf("snapshot = %#v, want zero", got)
		}
	})

	t.Run("counts every persisted state", func(t *testing.T) {
		store, reader := factory(t)
		base := time.Unix(1700000000, 0).UTC()
		states := []State{
			StateQueued,
			StateDispatching,
			StateAccepted,
			StateFailedRetryable,
			StateFailedPermanent,
			StateUncertain,
			StateCanceled,
		}
		for index, state := range states {
			seedStatusEntry(
				t,
				store,
				"op-"+string(rune('a'+index)),
				"account-1",
				42,
				base.Add(time.Duration(index)*time.Second),
				state,
			)
		}

		got, err := reader.ReadOperationalSnapshot(context.Background())
		if err != nil {
			t.Fatalf("ReadOperationalSnapshot() error = %v", err)
		}
		want := OperationalSnapshot{
			Queued:          1,
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
	})

	t.Run("counts repeated state and aggregates every account", func(t *testing.T) {
		store, reader := factory(t)
		base := time.Unix(1700000000, 0).UTC()
		seedStatusEntry(t, store, "op-a", "account-1", 42, base, StateQueued)
		seedStatusEntry(t, store, "op-b", "account-1", 43, base, StateQueued)
		seedStatusEntry(t, store, "op-c", "account-2", 42, base, StateQueued)
		seedStatusEntry(t, store, "op-d", "account-2", 42, base, StateAccepted)

		got, err := reader.ReadOperationalSnapshot(context.Background())
		if err != nil {
			t.Fatalf("ReadOperationalSnapshot() error = %v", err)
		}
		if got.Queued != 3 {
			t.Fatalf("Queued = %d, want 3", got.Queued)
		}
		if got.Accepted != 1 {
			t.Fatalf("Accepted = %d, want 1", got.Accepted)
		}
	})

	t.Run("reflects state transitions", func(t *testing.T) {
		store, reader := factory(t)
		at := time.Unix(1700000000, 0).UTC()
		seedStatusEntry(t, store, "op-a", "account-1", 42, at, StateQueued)

		got, err := reader.ReadOperationalSnapshot(context.Background())
		if err != nil {
			t.Fatalf("ReadOperationalSnapshot() error = %v", err)
		}
		if got.Queued != 1 || got.Dispatching != 0 {
			t.Fatalf("snapshot = %#v, want one queued entry", got)
		}

		entry, err := store.Get(context.Background(), "op-a")
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if _, err := store.Claim(
			context.Background(),
			entry.ID,
			entry.Version,
			"owner",
			at.Add(time.Hour),
			at,
		); err != nil {
			t.Fatalf("Claim() error = %v", err)
		}

		got, err = reader.ReadOperationalSnapshot(context.Background())
		if err != nil {
			t.Fatalf("ReadOperationalSnapshot() error = %v", err)
		}
		if got.Queued != 0 || got.Dispatching != 1 {
			t.Fatalf("snapshot = %#v, want one dispatching entry", got)
		}
	})

	t.Run("returns independent observations", func(t *testing.T) {
		store, reader := factory(t)
		seedStatusEntry(
			t,
			store,
			"op-a",
			"account-1",
			42,
			time.Unix(1700000000, 0).UTC(),
			StateQueued,
		)

		first, err := reader.ReadOperationalSnapshot(context.Background())
		if err != nil {
			t.Fatalf("ReadOperationalSnapshot() error = %v", err)
		}
		first.Queued = 100

		second, err := reader.ReadOperationalSnapshot(context.Background())
		if err != nil {
			t.Fatalf("ReadOperationalSnapshot() error = %v", err)
		}
		if second.Queued != 1 {
			t.Fatalf("Queued = %d, want 1", second.Queued)
		}
	})

	t.Run("propagates canceled context", func(t *testing.T) {
		_, reader := factory(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		got, err := reader.ReadOperationalSnapshot(ctx)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
		if got != (OperationalSnapshot{}) {
			t.Fatalf("snapshot = %#v, want zero", got)
		}
	})
}

func TestOperationalSnapshotReaderContractMemoryStore(t *testing.T) {
	t.Parallel()

	runOperationalSnapshotReaderContract(
		t,
		func(t *testing.T) (Store, OperationalSnapshotReader) {
			store := NewMemoryStore()
			return store, store
		},
	)
}

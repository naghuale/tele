package outbox

import (
	"context"
	"reflect"
	"testing"
	"time"
)

type entryStatusReaderFactory func(*testing.T) (Store, EntryStatusReader)

func runEntryStatusReaderContract(
	t *testing.T,
	factory entryStatusReaderFactory,
) {
	t.Helper()

	t.Run("rejects invalid query", func(t *testing.T) {
		_, reader := factory(t)
		for _, query := range []ListEntryStatusesQuery{
			{AccountKey: "", ChatID: 42},
			{AccountKey: " \t", ChatID: 42},
			{AccountKey: "account-1", ChatID: 0},
			{AccountKey: "account-1", ChatID: 42, Limit: -1},
			{AccountKey: "account-1", ChatID: 42, Limit: MaxEntryStatusLimit + 1},
		} {
			if _, err := reader.ListEntryStatuses(context.Background(), query); err == nil {
				t.Fatalf("ListEntryStatuses(%+v) error = nil, want non-nil", query)
			}
		}
	})

	t.Run("filters account and chat", func(t *testing.T) {
		store, reader := factory(t)
		seedStatusEntry(t, store, "a-1", "account-1", 42, time.Unix(1700000000, 0).UTC(), StateQueued)
		seedStatusEntry(t, store, "a-2", "account-1", 43, time.Unix(1700000001, 0).UTC(), StateQueued)
		seedStatusEntry(t, store, "b-1", "account-2", 42, time.Unix(1700000002, 0).UTC(), StateQueued)

		got, err := reader.ListEntryStatuses(context.Background(), ListEntryStatusesQuery{
			AccountKey: "account-1",
			ChatID:     42,
		})
		if err != nil {
			t.Fatalf("ListEntryStatuses() error = %v", err)
		}
		if len(got) != 1 || got[0].ID != "a-1" {
			t.Fatalf("statuses = %#v, want a-1", got)
		}
	})

	t.Run("orders by updated descending and id ascending", func(t *testing.T) {
		store, reader := factory(t)
		base := time.Unix(1700000000, 0).UTC()
		seedStatusEntry(t, store, "b", "account-1", 42, base, StateQueued)
		seedStatusEntry(t, store, "a", "account-1", 42, base, StateQueued)
		seedStatusEntry(t, store, "c", "account-1", 42, base.Add(time.Second), StateQueued)

		got, err := reader.ListEntryStatuses(context.Background(), ListEntryStatusesQuery{
			AccountKey: "account-1",
			ChatID:     42,
		})
		if err != nil {
			t.Fatalf("ListEntryStatuses() error = %v", err)
		}
		want := []string{"c", "a", "b"}
		if len(got) != len(want) {
			t.Fatalf("len = %d, want %d", len(got), len(want))
		}
		for i, id := range want {
			if got[i].ID != id {
				t.Fatalf("status[%d].ID = %q, want %q", i, got[i].ID, id)
			}
		}
	})

	t.Run("applies default and explicit limit", func(t *testing.T) {
		store, reader := factory(t)
		for i := 0; i < 3; i++ {
			seedStatusEntry(t, store, statusEntryID(i), "account-1", 42, time.Unix(1700000000+int64(i), 0).UTC(), StateQueued)
		}

		all, err := reader.ListEntryStatuses(context.Background(), ListEntryStatusesQuery{
			AccountKey: "account-1",
			ChatID:     42,
		})
		if err != nil || len(all) != 3 {
			t.Fatalf("default statuses = %d, err = %v", len(all), err)
		}
		limited, err := reader.ListEntryStatuses(context.Background(), ListEntryStatusesQuery{
			AccountKey: "account-1",
			ChatID:     42,
			Limit:      2,
		})
		if err != nil || len(limited) != 2 {
			t.Fatalf("limited statuses = %d, err = %v", len(limited), err)
		}
	})

	t.Run("returns all seven states", func(t *testing.T) {
		store, reader := factory(t)
		states := []State{
			StateQueued,
			StateDispatching,
			StateAccepted,
			StateFailedRetryable,
			StateFailedPermanent,
			StateUncertain,
			StateCanceled,
		}
		for i, state := range states {
			seedStatusEntry(t, store, statusEntryID(i), "account-1", 42, time.Unix(1700000000+int64(i), 0).UTC(), state)
		}
		got, err := reader.ListEntryStatuses(context.Background(), ListEntryStatusesQuery{
			AccountKey: "account-1",
			ChatID:     42,
		})
		if err != nil {
			t.Fatalf("ListEntryStatuses() error = %v", err)
		}
		seen := make(map[State]bool, len(got))
		for _, status := range got {
			seen[status.State] = true
		}
		for _, state := range states {
			if !seen[state] {
				t.Fatalf("state %q missing from %#v", state, got)
			}
		}
	})

	t.Run("returns empty result", func(t *testing.T) {
		_, reader := factory(t)
		got, err := reader.ListEntryStatuses(context.Background(), ListEntryStatusesQuery{
			AccountKey: "account-1",
			ChatID:     42,
		})
		if err != nil {
			t.Fatalf("ListEntryStatuses() error = %v", err)
		}
		if got == nil || len(got) != 0 {
			t.Fatalf("statuses = %#v, want non-nil empty", got)
		}
	})

	t.Run("propagates canceled context", func(t *testing.T) {
		_, reader := factory(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := reader.ListEntryStatuses(ctx, ListEntryStatusesQuery{
			AccountKey: "account-1",
			ChatID:     42,
		}); err == nil {
			t.Fatal("ListEntryStatuses() error = nil, want non-nil")
		}
	})

	t.Run("does not expose payload fields", func(t *testing.T) {
		typeOfStatus := reflect.TypeOf(EntryStatus{})
		for _, name := range []string{"Text", "EncryptedText", "Ciphertext", "Nonce", "ErrorMessage"} {
			if _, exists := typeOfStatus.FieldByName(name); exists {
				t.Fatalf("EntryStatus contains forbidden field %q", name)
			}
		}
	})

	t.Run("returns defensive slice", func(t *testing.T) {
		store, reader := factory(t)
		seedStatusEntry(t, store, "a", "account-1", 42, time.Unix(1700000000, 0).UTC(), StateQueued)
		got, err := reader.ListEntryStatuses(context.Background(), ListEntryStatusesQuery{
			AccountKey: "account-1",
			ChatID:     42,
		})
		if err != nil || len(got) != 1 {
			t.Fatalf("ListEntryStatuses() = %#v, err = %v", got, err)
		}
		got[0].State = StateCanceled
		again, err := reader.ListEntryStatuses(context.Background(), ListEntryStatusesQuery{
			AccountKey: "account-1",
			ChatID:     42,
		})
		if err != nil || again[0].State != StateQueued {
			t.Fatalf("mutated store result = %#v, err = %v", again, err)
		}
	})
}

func seedStatusEntry(
	t *testing.T,
	store Store,
	id string,
	accountKey string,
	chatID int64,
	at time.Time,
	state State,
) {
	t.Helper()
	ctx := context.Background()
	entry := queuedEntryAt(ID(id), chatID, "payload-"+id, at)
	entry.AccountKey = accountKey
	if err := store.Enqueue(ctx, entry); err != nil {
		t.Fatalf("Enqueue(%s): %v", id, err)
	}

	switch state {
	case StateQueued:
		return
	case StateDispatching:
		if _, err := store.Claim(ctx, entry.ID, entry.Version, "owner", at.Add(time.Hour), at); err != nil {
			t.Fatalf("Claim(%s): %v", id, err)
		}
	case StateAccepted:
		claimed, err := store.Claim(ctx, entry.ID, entry.Version, "owner", at.Add(time.Hour), at)
		if err != nil {
			t.Fatalf("Claim(%s): %v", id, err)
		}
		if _, err := store.MarkAccepted(ctx, entry.ID, claimed.Version, 1, at.Add(time.Second)); err != nil {
			t.Fatalf("MarkAccepted(%s): %v", id, err)
		}
	case StateFailedRetryable:
		claimed, err := store.Claim(ctx, entry.ID, entry.Version, "owner", at.Add(time.Hour), at)
		if err != nil {
			t.Fatalf("Claim(%s): %v", id, err)
		}
		if _, err := store.MarkRetryable(ctx, entry.ID, claimed.Version, at.Add(time.Minute), 500, "retry", at.Add(time.Second)); err != nil {
			t.Fatalf("MarkRetryable(%s): %v", id, err)
		}
	case StateFailedPermanent:
		claimed, err := store.Claim(ctx, entry.ID, entry.Version, "owner", at.Add(time.Hour), at)
		if err != nil {
			t.Fatalf("Claim(%s): %v", id, err)
		}
		if _, err := store.MarkPermanentFailure(ctx, entry.ID, claimed.Version, 400, "failed", at.Add(time.Second)); err != nil {
			t.Fatalf("MarkPermanentFailure(%s): %v", id, err)
		}
	case StateUncertain:
		claimed, err := store.Claim(ctx, entry.ID, entry.Version, "owner", at.Add(time.Hour), at)
		if err != nil {
			t.Fatalf("Claim(%s): %v", id, err)
		}
		if _, err := store.MarkUncertain(ctx, entry.ID, claimed.Version, "uncertain", at.Add(time.Second)); err != nil {
			t.Fatalf("MarkUncertain(%s): %v", id, err)
		}
	case StateCanceled:
		if _, err := store.Cancel(ctx, entry.ID, entry.Version, at); err != nil {
			t.Fatalf("Cancel(%s): %v", id, err)
		}
	default:
		t.Fatalf("unsupported seed state %q", state)
	}
}

func statusEntryID(i int) string {
	return "entry-" + string(rune('a'+i))
}

func TestEntryStatusReaderContractMemoryStore(t *testing.T) {
	t.Parallel()

	runEntryStatusReaderContract(t, func(t *testing.T) (Store, EntryStatusReader) {
		store := NewMemoryStore()
		return store, store
	})
}

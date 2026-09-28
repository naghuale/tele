package outbox

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestDispatcherRunPurgesOldDeliveredEntries pins that Run removes
// delivered entries past the retention period, so encrypted message text
// does not accumulate forever.
//
// "Delivered" is StateSent, not StateAccepted: TDLib accepting a message
// only means it is still on its way, and a record whose outcome nobody
// knows is a record the user may still come back to. The entry left in
// accepted here is the same fact seen from the other side, and it is
// never purged.
func TestDispatcherRunPurgesOldDeliveredEntries(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	t0 := time.Unix(1700000000, 0).UTC()

	if err := store.Enqueue(ctx, queuedEntryAt("op-old", 42, "x", t0)); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.Claim(ctx, "op-old", 0, "d1", t0.Add(time.Hour), t0)
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := store.MarkAccepted(ctx, claimed.ID, claimed.Version, 1, t0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.MarkSent(ctx, accepted.ID, accepted.Version, 1001, t0); err != nil {
		t.Fatal(err)
	}

	// A second entry Telegram never answered about, in its own chat so
	// ordering cannot hold it back.
	if err := store.Enqueue(ctx, queuedEntryAt("op-unanswered", 43, "y", t0)); err != nil {
		t.Fatal(err)
	}
	unanswered, err := store.Claim(
		ctx, "op-unanswered", 0, "d1", t0.Add(time.Hour), t0,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.MarkAccepted(
		ctx, unanswered.ID, unanswered.Version, 2, t0,
	); err != nil {
		t.Fatal(err)
	}

	clock := newFakeClock(t0.Add(30 * 24 * time.Hour))
	cfg := DefaultDispatcherConfig("test-dispatcher")
	cfg.PollInterval = time.Hour
	cfg.Logger = quietLogger()
	disp := NewDispatcher(store, &fakeSender{}, clock, cfg)

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- disp.Run(runCtx) }()

	deadline := time.After(2 * time.Second)
	for {
		if _, err := store.Get(ctx, "op-old"); errors.Is(err, ErrNotFound) {
			break
		}
		select {
		case <-deadline:
			t.Fatal("old delivered entry was not purged")
		case <-time.After(time.Millisecond):
		}
	}

	cancel()
	if err := <-runErr; err != nil {
		t.Fatalf("Run error = %v", err)
	}

	if _, err := store.Get(ctx, "op-unanswered"); err != nil {
		t.Fatalf("an entry Telegram never answered about was purged: %v", err)
	}
}

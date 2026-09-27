package outbox

import (
	"context"
	"testing"
	"time"
)

// TestDispatcherNotifyWakesIdleRun pins that an enqueue notification
// starts a scan at once instead of waiting for the poll interval.
func TestDispatcherNotifyWakesIdleRun(t *testing.T) {
	store := NewMemoryStore()
	sender := &fakeSender{
		results: []fakeSendResult{{message: SentMessage{ID: 9001, ChatID: 42}}},
	}
	// The fake clock is never advanced, so the poll timer never fires:
	// only Notify can start the second scan.
	clock := newFakeClock(time.Unix(1700000100, 0).UTC())
	cfg := DefaultDispatcherConfig("test-dispatcher")
	cfg.PollInterval = time.Hour
	cfg.Logger = quietLogger()
	disp := NewDispatcher(store, sender, clock, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- disp.Run(ctx) }()

	waitForIdle := time.After(2 * time.Second)
	for {
		clock.mu.Lock()
		idle := len(clock.waiters) > 0
		clock.mu.Unlock()
		if idle {
			break
		}
		select {
		case <-waitForIdle:
			t.Fatal("dispatcher never went idle")
		case <-time.After(time.Millisecond):
		}
	}

	if err := store.Enqueue(context.Background(), queuedEntry("op-1", 42, "hello")); err != nil {
		t.Fatal(err)
	}
	disp.Notify()

	deadline := time.After(2 * time.Second)
	for sender.callCount() == 0 {
		select {
		case <-deadline:
			t.Fatal("Notify did not wake the idle dispatcher")
		case <-time.After(time.Millisecond):
		}
	}

	cancel()
	if err := <-runErr; err != nil {
		t.Fatalf("Run error = %v", err)
	}
}

// TestDispatcherNotifyNeverBlocks pins that notifications coalesce and
// never block the submitter, even with no Run consuming them.
func TestDispatcherNotifyNeverBlocks(t *testing.T) {
	disp := NewDispatcher(NewMemoryStore(), &fakeSender{}, nil, DispatcherConfig{})
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			disp.Notify()
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Notify blocked")
	}
}

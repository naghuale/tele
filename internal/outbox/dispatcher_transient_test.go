package outbox

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var errStoreBusy = errors.New("database is locked")

// flakyListStore fails ListReady a fixed number of times, the way SQLite
// does under a short lock, and then delegates.
type flakyListStore struct {
	Store
	failures atomic.Int64
	calls    atomic.Int64
}

func (s *flakyListStore) ListReady(
	ctx context.Context,
	now time.Time,
	limit int,
) ([]Entry, error) {
	s.calls.Add(1)
	if s.failures.Add(-1) >= 0 {
		return nil, errStoreBusy
	}
	return s.Store.ListReady(ctx, now, limit)
}

func newTransientTestDispatcher(store Store, sender Sender, logs *bytes.Buffer) *Dispatcher {
	cfg := DefaultDispatcherConfig("test-dispatcher")
	cfg.PollInterval = time.Millisecond
	cfg.MaxConsecutiveScanErrors = 3
	cfg.Logger = quietLogger()
	if logs != nil {
		cfg.Logger = slog.New(slog.NewTextHandler(logs, nil))
	}
	return NewDispatcher(store, sender, SystemClock{}, cfg)
}

// TestDispatcherRunSurvivesTransientScanErrors pins that a short store
// failure before any send is retried instead of stopping delivery.
func TestDispatcherRunSurvivesTransientScanErrors(t *testing.T) {
	base := NewMemoryStore()
	if err := base.Enqueue(context.Background(), queuedEntry("op-1", 42, "hello")); err != nil {
		t.Fatal(err)
	}
	store := &flakyListStore{Store: base}
	store.failures.Store(2)
	sender := &fakeSender{
		results: []fakeSendResult{{message: SentMessage{ID: 9001, ChatID: 42}}},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- newTransientTestDispatcher(store, sender, nil).Run(ctx) }()

	for {
		entry, err := base.Get(context.Background(), "op-1")
		if err != nil {
			t.Fatal(err)
		}
		if entry.State == StateAccepted {
			break
		}
		select {
		case err := <-runErr:
			t.Fatalf("Run stopped before delivery: %v", err)
		case <-ctx.Done():
			t.Fatal("entry was not delivered after transient scan errors")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	if err := <-runErr; err != nil {
		t.Fatalf("Run error = %v, want nil after cancellation", err)
	}
}

// TestDispatcherRunStopsAfterConsecutiveScanErrors pins that a store that
// keeps failing still ends Run with the underlying error.
func TestDispatcherRunStopsAfterConsecutiveScanErrors(t *testing.T) {
	store := &flakyListStore{Store: NewMemoryStore()}
	store.failures.Store(1 << 30)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := newTransientTestDispatcher(store, &fakeSender{}, nil).Run(ctx)

	if !errors.Is(err, errStoreBusy) {
		t.Fatalf("Run error = %v, want the store error", err)
	}
	if got := store.calls.Load(); got != 3 {
		t.Fatalf("ListReady calls = %d, want 3", got)
	}
}

// conflictingFinalizeStore loses every accepted finalization to a
// concurrent update.
type conflictingFinalizeStore struct {
	Store
}

func (s *conflictingFinalizeStore) MarkAccepted(
	context.Context,
	ID,
	uint64,
	int64,
	time.Time,
) (Entry, error) {
	return Entry{}, ErrVersionConflict
}

// TestDispatcherLogsLostFinalization pins that a known outcome dropped
// because of a version conflict leaves a trace instead of vanishing.
func TestDispatcherLogsLostFinalization(t *testing.T) {
	base := NewMemoryStore()
	if err := base.Enqueue(context.Background(), queuedEntry("op-1", 42, "secret body")); err != nil {
		t.Fatal(err)
	}
	sender := &fakeSender{
		results: []fakeSendResult{{message: SentMessage{ID: 9001, ChatID: 42}}},
	}
	var logs bytes.Buffer
	disp := newTransientTestDispatcher(&conflictingFinalizeStore{Store: base}, sender, &logs)

	if _, err := disp.ScanOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	out := logs.String()
	if !strings.Contains(out, "outbox finalize lost") ||
		!strings.Contains(out, "entry_id=op-1") ||
		!strings.Contains(out, "outcome=accepted") {
		t.Fatalf("lost finalization was not logged: %q", out)
	}
	if strings.Contains(out, "secret body") {
		t.Fatalf("logs leaked message text: %q", out)
	}
}

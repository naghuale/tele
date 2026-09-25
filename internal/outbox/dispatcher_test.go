package outbox

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSender is a scripted Sender.
type fakeSender struct {
	mu       sync.Mutex
	results  []fakeSendResult
	calls    int
	observed []fakeSendCall
}

type fakeSendCall struct {
	chatID int64
	text   string
}

type fakeSendResult struct {
	message SentMessage
	err     error
}

func (f *fakeSender) SendMessage(
	ctx context.Context,
	chatID int64,
	text string,
) (SentMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	index := f.calls
	f.calls++
	f.observed = append(f.observed, fakeSendCall{chatID: chatID, text: text})

	if index < len(f.results) {
		return f.results[index].message, f.results[index].err
	}
	return SentMessage{}, errors.New("no scripted result")
}

func (f *fakeSender) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// cancelingSender cancels its parent context during SendMessage to
// exercise detached finalization.
type cancelingSender struct {
	cancel context.CancelFunc
}

func (s *cancelingSender) SendMessage(
	_ context.Context,
	_ int64,
	_ string,
) (SentMessage, error) {
	s.cancel()
	return SentMessage{}, context.Canceled
}

// fakeClock advances manually.
type fakeClock struct {
	mu      sync.Mutex
	now     time.Time
	waiters []chan time.Time
}

func newFakeClock(t time.Time) *fakeClock {
	return &fakeClock{now: t}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	c.mu.Lock()
	c.waiters = append(c.waiters, ch)
	c.mu.Unlock()
	return ch
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	waiters := c.waiters
	c.waiters = nil
	c.mu.Unlock()

	for _, ch := range waiters {
		ch <- c.now
	}
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newDispatcherForTest(
	t *testing.T,
	store Store,
	sender Sender,
	clock Clock,
) *Dispatcher {
	t.Helper()
	cfg := DefaultDispatcherConfig("test-dispatcher")
	cfg.PollInterval = time.Millisecond
	cfg.Logger = quietLogger()
	return NewDispatcher(store, sender, clock, cfg)
}

// ---- Happy path ----

func TestDispatcherAccepted(t *testing.T) {
	store := NewMemoryStore()
	entry := queuedEntry("op-1", 42, "hello")
	if err := store.Enqueue(context.Background(), entry); err != nil {
		t.Fatal(err)
	}

	sender := &fakeSender{
		results: []fakeSendResult{{message: SentMessage{ID: 9001, ChatID: 42}}},
	}
	clock := newFakeClock(time.Unix(1700000100, 0).UTC())
	disp := newDispatcherForTest(t, store, sender, clock)

	processed, err := disp.ScanOnce(context.Background())
	if err != nil {
		t.Fatalf("ScanOnce: %v", err)
	}
	if !processed {
		t.Fatal("expected one processed entry")
	}

	got, err := store.Get(context.Background(), entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateAccepted {
		t.Fatalf("state = %s, want accepted", got.State)
	}
	if got.TelegramMessageID != 9001 {
		t.Fatalf("message id = %d", got.TelegramMessageID)
	}
	if sender.callCount() != 1 {
		t.Fatalf("sender calls = %d, want 1", sender.callCount())
	}
}

// ---- Retryable ----

func TestDispatcherRetryableError(t *testing.T) {
	store := NewMemoryStore()
	entry := queuedEntry("op-1", 42, "hello")
	if err := store.Enqueue(context.Background(), entry); err != nil {
		t.Fatal(err)
	}

	sender := &fakeSender{
		results: []fakeSendResult{
			{err: &SendError{Code: 500, Message: "server error"}},
		},
	}
	clock := newFakeClock(time.Unix(1700000100, 0).UTC())

	cfg := DefaultDispatcherConfig("test-dispatcher")
	cfg.Logger = quietLogger()
	cfg.Backoff = Backoff{Delays: []time.Duration{time.Minute}}
	disp := NewDispatcher(store, sender, clock, cfg)

	if _, err := disp.ScanOnce(context.Background()); err != nil {
		t.Fatalf("ScanOnce: %v", err)
	}

	got, err := store.Get(context.Background(), entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateFailedRetryable {
		t.Fatalf("state = %s, want failed_retryable", got.State)
	}
	if got.NextAttempt.IsZero() {
		t.Fatal("NextAttempt not set")
	}
	if got.LastErrorCode != 500 {
		t.Fatalf("code = %d", got.LastErrorCode)
	}
}

// ---- Permanent ----

func TestDispatcherPermanentError(t *testing.T) {
	store := NewMemoryStore()
	entry := queuedEntry("op-1", 42, "hello")
	if err := store.Enqueue(context.Background(), entry); err != nil {
		t.Fatal(err)
	}

	sender := &fakeSender{
		results: []fakeSendResult{
			{err: &SendError{Code: 400, Message: "chat not found"}},
		},
	}
	clock := newFakeClock(time.Unix(1700000100, 0).UTC())
	disp := newDispatcherForTest(t, store, sender, clock)

	if _, err := disp.ScanOnce(context.Background()); err != nil {
		t.Fatalf("ScanOnce: %v", err)
	}

	got, err := store.Get(context.Background(), entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateFailedPermanent {
		t.Fatalf("state = %s, want failed_permanent", got.State)
	}
	if got.LastErrorCode != 400 {
		t.Fatalf("code = %d", got.LastErrorCode)
	}
}

// ---- Uncertain ----

func TestDispatcherUnknownErrorIsUncertain(t *testing.T) {
	store := NewMemoryStore()
	entry := queuedEntry("op-1", 42, "hello")
	if err := store.Enqueue(context.Background(), entry); err != nil {
		t.Fatal(err)
	}

	sender := &fakeSender{
		results: []fakeSendResult{
			{err: errors.New("network error after send")},
		},
	}
	clock := newFakeClock(time.Unix(1700000100, 0).UTC())
	disp := newDispatcherForTest(t, store, sender, clock)

	if _, err := disp.ScanOnce(context.Background()); err != nil {
		t.Fatalf("ScanOnce: %v", err)
	}

	got, err := store.Get(context.Background(), entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateUncertain {
		t.Fatalf("state = %s, want uncertain", got.State)
	}

	ready, err := store.ListReady(
		context.Background(),
		clock.Now().Add(time.Hour),
		10,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(ready) != 0 {
		t.Fatalf("uncertain entry leaked into ready set: %+v", ready)
	}
}

func TestDispatcherZeroMessageIDIsUncertain(t *testing.T) {
	store := NewMemoryStore()
	entry := queuedEntry("op-1", 42, "hello")
	if err := store.Enqueue(context.Background(), entry); err != nil {
		t.Fatal(err)
	}

	sender := &fakeSender{
		results: []fakeSendResult{{message: SentMessage{ID: 0, ChatID: 42}}},
	}
	clock := newFakeClock(time.Unix(1700000100, 0).UTC())
	disp := newDispatcherForTest(t, store, sender, clock)

	if _, err := disp.ScanOnce(context.Background()); err != nil {
		t.Fatalf("ScanOnce: %v", err)
	}

	got, err := store.Get(context.Background(), entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateUncertain {
		t.Fatalf("state = %s, want uncertain", got.State)
	}
}

func TestDispatcherMismatchedChatIDIsUncertain(t *testing.T) {
	store := NewMemoryStore()
	entry := queuedEntry("op-1", 42, "hello")
	if err := store.Enqueue(context.Background(), entry); err != nil {
		t.Fatal(err)
	}

	sender := &fakeSender{
		results: []fakeSendResult{
			{message: SentMessage{ID: 9001, ChatID: 99}},
		},
	}
	clock := newFakeClock(time.Unix(1700000100, 0).UTC())
	disp := newDispatcherForTest(t, store, sender, clock)

	if _, err := disp.ScanOnce(context.Background()); err != nil {
		t.Fatalf("ScanOnce: %v", err)
	}

	got, err := store.Get(context.Background(), entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateUncertain {
		t.Fatalf("state = %s, want uncertain", got.State)
	}
	if got.TelegramMessageID != 0 {
		t.Fatalf("TelegramMessageID = %d, want 0", got.TelegramMessageID)
	}
}

// ---- Detached finalization ----

// When the caller context is canceled during SendMessage, the outcome
// must still be persisted. Otherwise recovery would convert the entry
// to uncertain despite a known result.
func TestDispatcherPersistsUncertainWhenParentContextCanceled(t *testing.T) {
	store := NewMemoryStore()
	entry := queuedEntry("op-1", 42, "hello")
	if err := store.Enqueue(context.Background(), entry); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	sender := &cancelingSender{cancel: cancel}

	clock := newFakeClock(time.Unix(1700000100, 0).UTC())

	cfg := DefaultDispatcherConfig("test-dispatcher")
	cfg.Logger = quietLogger()
	cfg.FinalizeTimeout = time.Second

	dispatcher := NewDispatcher(store, sender, clock, cfg)

	processed, err := dispatcher.ScanOnce(ctx)
	if err != nil {
		t.Fatalf("ScanOnce: %v", err)
	}
	if !processed {
		t.Fatal("expected entry to be processed")
	}

	got, err := store.Get(context.Background(), entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateUncertain {
		t.Fatalf("state = %s, want uncertain", got.State)
	}
	if got.LastErrorMessage != "send context canceled" {
		t.Fatalf("LastErrorMessage = %q", got.LastErrorMessage)
	}
}

// ---- Retry loop ----

func TestDispatcherRetryThenAccept(t *testing.T) {
	store := NewMemoryStore()
	entry := queuedEntry("op-1", 42, "hello")
	if err := store.Enqueue(context.Background(), entry); err != nil {
		t.Fatal(err)
	}

	sender := &fakeSender{
		results: []fakeSendResult{
			{err: &SendError{Code: 500}},
			{message: SentMessage{ID: 9001, ChatID: 42}},
		},
	}
	clock := newFakeClock(time.Unix(1700000100, 0).UTC())

	cfg := DefaultDispatcherConfig("test-dispatcher")
	cfg.Logger = quietLogger()
	cfg.Backoff = Backoff{Delays: []time.Duration{time.Minute}}
	disp := NewDispatcher(store, sender, clock, cfg)

	if _, err := disp.ScanOnce(context.Background()); err != nil {
		t.Fatalf("first scan: %v", err)
	}

	got, err := store.Get(context.Background(), entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateFailedRetryable {
		t.Fatalf("state = %s, want failed_retryable", got.State)
	}

	if _, err := disp.ScanOnce(context.Background()); err != nil {
		t.Fatalf("second scan: %v", err)
	}
	got, err = store.Get(context.Background(), entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateFailedRetryable {
		t.Fatalf("premature retry: state = %s", got.State)
	}
	if sender.callCount() != 1 {
		t.Fatalf("sender calls = %d, want 1", sender.callCount())
	}

	clock.Advance(2 * time.Minute)

	if _, err := disp.ScanOnce(context.Background()); err != nil {
		t.Fatalf("third scan: %v", err)
	}
	got, err = store.Get(context.Background(), entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateAccepted {
		t.Fatalf("state = %s, want accepted", got.State)
	}
	if sender.callCount() != 2 {
		t.Fatalf("sender calls = %d, want 2", sender.callCount())
	}
}

// ---- MaxAttempts ----

func TestDispatcherMaxAttemptsForcesPermanent(t *testing.T) {
	store := NewMemoryStore()
	entry := queuedEntry("op-1", 42, "hello")
	if err := store.Enqueue(context.Background(), entry); err != nil {
		t.Fatal(err)
	}

	sender := &fakeSender{
		results: []fakeSendResult{
			{err: &SendError{Code: 500}},
			{err: &SendError{Code: 500}},
		},
	}
	clock := newFakeClock(time.Unix(1700000100, 0).UTC())

	cfg := DefaultDispatcherConfig("test-dispatcher")
	cfg.Logger = quietLogger()
	cfg.Backoff = Backoff{Delays: []time.Duration{
		time.Second, time.Second, time.Second, time.Second,
	}}
	cfg.MaxAttempts = 2
	disp := NewDispatcher(store, sender, clock, cfg)

	if _, err := disp.ScanOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Second)

	if _, err := disp.ScanOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Second)

	beforeCalls := sender.callCount()
	if _, err := disp.ScanOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(context.Background(), entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateFailedPermanent {
		t.Fatalf("state = %s, want failed_permanent", got.State)
	}
	if sender.callCount() != beforeCalls {
		t.Fatalf("sender was called for a capped attempt: %d -> %d",
			beforeCalls, sender.callCount())
	}
}

// ---- Recovery ----

func TestDispatcherRecoveryConvertsExpiredLeaseToUncertain(t *testing.T) {
	store := NewMemoryStore()
	entry := queuedEntry("op-1", 42, "hello")
	if err := store.Enqueue(context.Background(), entry); err != nil {
		t.Fatal(err)
	}

	claimed, err := store.Claim(
		context.Background(),
		entry.ID,
		entry.Version,
		"previous-instance",
		time.Unix(1700000100, 0).UTC().Add(time.Minute),
		time.Unix(1700000100, 0).UTC(),
	)
	if err != nil {
		t.Fatal(err)
	}

	clock := newFakeClock(time.Unix(1700000200, 0).UTC())
	sender := &fakeSender{}
	disp := newDispatcherForTest(t, store, sender, clock)

	recovered, err := disp.RecoverInterrupted(context.Background())
	if err != nil {
		t.Fatalf("RecoverInterrupted: %v", err)
	}
	if recovered != 1 {
		t.Fatalf("recovered = %d, want 1", recovered)
	}

	got, err := store.Get(context.Background(), claimed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateUncertain {
		t.Fatalf("state = %s, want uncertain", got.State)
	}
	if sender.callCount() != 0 {
		t.Fatalf("sender called during recovery: %d", sender.callCount())
	}
}

// ---- Idempotency ----

func TestDispatcherTwoInstancesCannotClaimSameEntry(t *testing.T) {
	store := NewMemoryStore()
	entry := queuedEntry("op-1", 42, "hello")
	if err := store.Enqueue(context.Background(), entry); err != nil {
		t.Fatal(err)
	}

	sender := &fakeSender{
		results: []fakeSendResult{
			{message: SentMessage{ID: 9001, ChatID: 42}},
			{message: SentMessage{ID: 9002, ChatID: 42}},
		},
	}
	clock := newFakeClock(time.Unix(1700000100, 0).UTC())

	cfgA := DefaultDispatcherConfig("dispatcher-a")
	cfgA.Logger = quietLogger()
	cfgB := DefaultDispatcherConfig("dispatcher-b")
	cfgB.Logger = quietLogger()

	dispA := NewDispatcher(store, sender, clock, cfgA)
	dispB := NewDispatcher(store, sender, clock, cfgB)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _, _ = dispA.ScanOnce(context.Background()) }()
	go func() { defer wg.Done(); _, _ = dispB.ScanOnce(context.Background()) }()
	wg.Wait()

	got, err := store.Get(context.Background(), entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateAccepted {
		t.Fatalf("state = %s, want accepted", got.State)
	}
	if sender.callCount() != 1 {
		t.Fatalf("sender calls = %d, want 1", sender.callCount())
	}
}

// ---- Context cancellation ----

func TestDispatcherStopsOnContextCancel(t *testing.T) {
	store := NewMemoryStore()
	sender := &fakeSender{}
	clock := newFakeClock(time.Unix(1700000100, 0).UTC())
	disp := newDispatcherForTest(t, store, sender, clock)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := disp.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// ---- Stop ----

func TestDispatcherStopStopsScans(t *testing.T) {
	store := NewMemoryStore()
	entry := queuedEntry("op-1", 42, "hello")
	if err := store.Enqueue(context.Background(), entry); err != nil {
		t.Fatal(err)
	}

	sender := &fakeSender{
		results: []fakeSendResult{{message: SentMessage{ID: 9001, ChatID: 42}}},
	}
	clock := newFakeClock(time.Unix(1700000100, 0).UTC())
	disp := newDispatcherForTest(t, store, sender, clock)

	disp.Stop()
	processed, err := disp.ScanOnce(context.Background())
	if err != nil {
		t.Fatalf("ScanOnce: %v", err)
	}
	if processed {
		t.Fatal("stopped dispatcher processed an entry")
	}
	if sender.callCount() != 0 {
		t.Fatalf("sender called after Stop: %d", sender.callCount())
	}
}

// ---- Privacy ----

func TestDispatcherDoesNotPersistUnknownErrorText(t *testing.T) {
	store := NewMemoryStore()
	entry := queuedEntry("op-1", 42, "secret body")
	if err := store.Enqueue(context.Background(), entry); err != nil {
		t.Fatal(err)
	}

	sender := &fakeSender{
		results: []fakeSendResult{
			{err: errors.New("transport failed: secret body")},
		},
	}
	clock := newFakeClock(time.Unix(1700000100, 0).UTC())
	disp := newDispatcherForTest(t, store, sender, clock)

	if _, err := disp.ScanOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	got, err := store.Get(context.Background(), entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateUncertain {
		t.Fatalf("state = %s, want uncertain", got.State)
	}
	if strings.Contains(got.LastErrorMessage, "secret body") {
		t.Fatalf("persisted error leaked text: %q", got.LastErrorMessage)
	}
}

func TestDispatcherDoesNotPersistSendErrorMessageField(t *testing.T) {
	store := NewMemoryStore()
	entry := queuedEntry("op-1", 42, "secret body")
	if err := store.Enqueue(context.Background(), entry); err != nil {
		t.Fatal(err)
	}

	sender := &fakeSender{
		results: []fakeSendResult{
			{err: &SendError{Code: 500, Message: "secret body"}},
		},
	}
	clock := newFakeClock(time.Unix(1700000100, 0).UTC())

	cfg := DefaultDispatcherConfig("test-dispatcher")
	cfg.Logger = quietLogger()
	cfg.Backoff = Backoff{Delays: []time.Duration{time.Minute}}
	disp := NewDispatcher(store, sender, clock, cfg)

	if _, err := disp.ScanOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	got, err := store.Get(context.Background(), entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got.LastErrorMessage, "secret body") {
		t.Fatalf("SendError.Message leaked into LastErrorMessage: %q",
			got.LastErrorMessage)
	}
}

func TestDispatcherLoggerDoesNotContainMessageText(t *testing.T) {
	store := NewMemoryStore()
	entry := queuedEntry("op-1", 42, "secret body")
	if err := store.Enqueue(context.Background(), entry); err != nil {
		t.Fatal(err)
	}

	sender := &fakeSender{
		results: []fakeSendResult{
			{err: errors.New("transport failed: secret body")},
		},
	}
	clock := newFakeClock(time.Unix(1700000100, 0).UTC())

	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))

	cfg := DefaultDispatcherConfig("test-dispatcher")
	cfg.Logger = logger
	disp := NewDispatcher(store, sender, clock, cfg)

	if _, err := disp.ScanOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	if strings.Contains(logs.String(), "secret body") {
		t.Fatalf("logs leaked message text: %q", logs.String())
	}
}

// ---- Nil guards ----

func TestDispatcherRejectsNilStore(t *testing.T) {
	cfg := DefaultDispatcherConfig("d")
	cfg.Logger = quietLogger()
	disp := NewDispatcher(nil, &fakeSender{}, SystemClock{}, cfg)
	if err := disp.Run(context.Background()); err == nil {
		t.Fatal("expected error for nil store")
	}
}

func TestDispatcherRejectsNilSender(t *testing.T) {
	store := NewMemoryStore()
	cfg := DefaultDispatcherConfig("d")
	cfg.Logger = quietLogger()
	disp := NewDispatcher(store, nil, SystemClock{}, cfg)
	if err := disp.Run(context.Background()); err == nil {
		t.Fatal("expected error for nil sender")
	}
}

func TestDispatcherScanOnceRejectsNilStore(t *testing.T) {
	cfg := DefaultDispatcherConfig("d")
	cfg.Logger = quietLogger()
	dispatcher := NewDispatcher(nil, &fakeSender{}, SystemClock{}, cfg)

	if _, err := dispatcher.ScanOnce(context.Background()); err == nil {
		t.Fatal("expected nil store error")
	}
}

func TestDispatcherScanOnceRejectsNilSender(t *testing.T) {
	cfg := DefaultDispatcherConfig("d")
	cfg.Logger = quietLogger()
	dispatcher := NewDispatcher(NewMemoryStore(), nil, SystemClock{}, cfg)

	if _, err := dispatcher.ScanOnce(context.Background()); err == nil {
		t.Fatal("expected nil sender error")
	}
}

func TestDispatcherRecoverRejectsNilStore(t *testing.T) {
	cfg := DefaultDispatcherConfig("d")
	cfg.Logger = quietLogger()
	dispatcher := NewDispatcher(nil, &fakeSender{}, SystemClock{}, cfg)

	if _, err := dispatcher.RecoverInterrupted(context.Background()); err == nil {
		t.Fatal("expected nil store error")
	}
}

// blockingFinalizeStore wraps a Store and blocks MarkAccepted until
// the finalization context is done. It is used to prove that an
// internal finalization deadline is returned by Run as an error and
// not silently converted into a graceful shutdown.
type blockingFinalizeStore struct {
	Store
}

func (s *blockingFinalizeStore) MarkAccepted(
	ctx context.Context,
	_ ID,
	_ uint64,
	_ int64,
	_ time.Time,
) (Entry, error) {
	<-ctx.Done()
	return Entry{}, ctx.Err()
}

func TestDispatcherRunReturnsFinalizeTimeout(t *testing.T) {
	base := NewMemoryStore()
	entry := queuedEntry("op-1", 42, "hello")
	if err := base.Enqueue(context.Background(), entry); err != nil {
		t.Fatal(err)
	}

	store := &blockingFinalizeStore{Store: base}

	sender := &fakeSender{
		results: []fakeSendResult{
			{message: SentMessage{ID: 9001, ChatID: 42}},
		},
	}

	cfg := DefaultDispatcherConfig("test-dispatcher")
	cfg.Logger = quietLogger()
	cfg.FinalizeTimeout = time.Millisecond

	dispatcher := NewDispatcher(store, sender, SystemClock{}, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err := dispatcher.Run(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want finalization deadline", err)
	}
}

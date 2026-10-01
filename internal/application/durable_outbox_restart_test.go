package application

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"telecli/internal/config"
	"telecli/internal/outbox"
	"telecli/internal/telegram"
)

// h7dSecretText is the payload used to prove that restart failures never
// surface message text.
const h7dSecretText = "h7d-secret-restart-message"

// h7dKeyProvider is a persistent in-memory key provider.
//
// It survives runtime restarts on purpose: the same database identity must
// load the same key instead of creating a replacement.
type h7dKeyProvider struct {
	mu          sync.Mutex
	keys        map[string][]byte
	loadCalls   int
	createCalls int
}

func newH7dKeyProvider() *h7dKeyProvider {
	return &h7dKeyProvider{keys: make(map[string][]byte)}
}

func (p *h7dKeyProvider) LoadKey(
	_ context.Context,
	databaseID string,
) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.loadCalls++

	key, ok := p.keys[databaseID]
	if !ok {
		return nil, outbox.ErrOutboxKeyUnavailable
	}
	return append([]byte(nil), key...), nil
}

func (p *h7dKeyProvider) CreateKey(
	_ context.Context,
	databaseID string,
) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.createCalls++

	if _, exists := p.keys[databaseID]; exists {
		return nil, outbox.ErrOutboxKeyExists
	}
	key := make([]byte, 32)
	for index := range key {
		key[index] = byte(index + 1)
	}
	p.keys[databaseID] = append([]byte(nil), key...)
	return append([]byte(nil), key...), nil
}

func (p *h7dKeyProvider) deleteKey(databaseID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.keys, databaseID)
}

func (p *h7dKeyProvider) counts() (int, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.loadCalls, p.createCalls
}

var _ outbox.KeyProvider = (*h7dKeyProvider)(nil)

// h7dClock gates the dispatcher: the dispatcher only scans when the test
// fires a timer.
type h7dClock struct {
	mu      sync.Mutex
	now     time.Time
	waiters []chan time.Time
	notify  chan struct{}
}

func newH7dClock(now time.Time) *h7dClock {
	return &h7dClock{now: now, notify: make(chan struct{}, 1)}
}

func (c *h7dClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *h7dClock) After(time.Duration) <-chan time.Time {
	tick := make(chan time.Time, 1)
	c.mu.Lock()
	c.waiters = append(c.waiters, tick)
	c.mu.Unlock()

	select {
	case c.notify <- struct{}{}:
	default:
	}
	return tick
}

func (c *h7dClock) setNow(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = now
}

// fire releases the next pending dispatcher timer.
func (c *h7dClock) fire(t *testing.T) {
	t.Helper()
	select {
	case <-c.notify:
	case <-time.After(2 * time.Second):
		t.Fatal("dispatcher did not wait for a poll timer")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.waiters) == 0 {
		t.Fatal("dispatcher requested a timer without a waiter")
	}
	tick := c.waiters[0]
	c.waiters = c.waiters[1:]
	tick <- c.now
}

var _ outbox.Clock = (*h7dClock)(nil)

// h7dSession is a controllable Telegram session.
type h7dSession struct {
	mu          sync.Mutex
	texts       []string
	calls       atomic.Int64
	send        func(ctx context.Context, chatID telegram.ChatID, text string) (telegram.Message, error)
	closed      atomic.Bool
	sendCall    chan struct{}
	openedChats atomic.Int32
	closedChats atomic.Int32
	viewedChats atomic.Int32
}

func newH7dSession(
	send func(
		context.Context,
		telegram.ChatID,
		string,
	) (telegram.Message, error),
) *h7dSession {
	return &h7dSession{send: send, sendCall: make(chan struct{}, 16)}
}

func (s *h7dSession) SendTextMessage(
	ctx context.Context,
	chatID telegram.ChatID,
	text string,
) (telegram.Message, error) {
	s.calls.Add(1)
	s.mu.Lock()
	s.texts = append(s.texts, text)
	s.mu.Unlock()

	select {
	case s.sendCall <- struct{}{}:
	default:
	}

	if s.send != nil {
		return s.send(ctx, chatID, text)
	}
	return telegram.Message{
		ID:        telegram.MessageID(1),
		ChatID:    chatID,
		Outgoing:  true,
		Text:      text,
		Timestamp: time.Unix(1, 0).UTC(),
	}, nil
}

func (s *h7dSession) GetChats(
	context.Context,
	int,
) (telegram.ChatListSnapshot, error) {
	return telegram.ChatListSnapshot{}, nil
}

func (s *h7dSession) GetChatHistory(
	context.Context,
	telegram.ChatID,
	telegram.MessageID,
	int,
) (telegram.HistoryPage, error) {
	return telegram.HistoryPage{}, nil
}

func (s *h7dSession) OpenChat(context.Context, telegram.ChatID) error {
	s.openedChats.Add(1)
	return nil
}

func (s *h7dSession) CloseChat(context.Context, telegram.ChatID) error {
	s.closedChats.Add(1)
	return nil
}

func (s *h7dSession) ViewMessages(
	context.Context,
	telegram.ChatID,
	[]telegram.MessageID,
) error {
	s.viewedChats.Add(1)
	return nil
}

func (s *h7dSession) LiveState() *telegram.LiveState {
	return nil
}

func (s *h7dSession) GetChatAccess(
	context.Context,
	telegram.ChatID,
) (telegram.ChatAccess, error) {
	return telegram.ChatAccess{CanSend: true}, nil
}

func (s *h7dSession) Close(context.Context) error {
	s.closed.Store(true)
	return nil
}

func (s *h7dSession) sentTexts() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.texts...)
}

var _ deliverySession = (*h7dSession)(nil)

// h7dFixture keeps every restart dependency stable across opens.
type h7dFixture struct {
	t          *testing.T
	dataDir    string
	databaseID string
	instanceID string
	accountKey string
	provider   *h7dKeyProvider
	clock      *h7dClock
}

func newH7dFixture(t *testing.T) *h7dFixture {
	t.Helper()

	dataDir := filepath.Join(t.TempDir(), "outbox")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatalf("create data dir: %v", err)
	}
	if err := os.Chmod(dataDir, 0o700); err != nil {
		t.Fatalf("chmod data dir: %v", err)
	}

	return &h7dFixture{
		t:          t,
		dataDir:    dataDir,
		databaseID: "h7d-restart-database",
		instanceID: "h7d-restart-instance",
		accountKey: "account-1",
		provider:   newH7dKeyProvider(),
		clock:      newH7dClock(time.Unix(1700000000, 0).UTC()),
	}
}

func (f *h7dFixture) config() DurableOutboxRuntimeConfig {
	dispatcher := outbox.DefaultDispatcherConfig(f.instanceID)
	dispatcher.PollInterval = time.Hour
	dispatcher.Backoff = outbox.Backoff{
		Delays: []time.Duration{30 * time.Second},
	}
	dispatcher.Logger = nil
	return DurableOutboxRuntimeConfig{
		Outbox: outbox.Config{
			DataDir:    f.dataDir,
			DatabaseID: f.databaseID,
			InstanceID: f.instanceID,
			Dispatcher: dispatcher,
		},
	}
}

func (f *h7dFixture) open(
	ctx context.Context,
	session deliverySession,
) (*DurableOutboxRuntime, error) {
	f.t.Helper()
	return OpenDurableOutboxRuntime(
		ctx,
		f.config(),
		DurableOutboxRuntimeDeps{
			KeyProvider: f.provider,
			Session:     session,
			Clock:       f.clock,
			IDGenerator: durableRuntimeTestIDGenerator("h7d"),
			// The explicit no-op notifier keeps the dispatcher gated
			// on the test clock; production wakes it on every enqueue.
			Notifier:   noopNotifier{},
			AccountKey: f.accountKey,
		},
	)
}

func (f *h7dFixture) mustOpen(
	ctx context.Context,
	session deliverySession,
) *DurableOutboxRuntime {
	f.t.Helper()
	runtime, err := f.open(ctx, session)
	if err != nil {
		f.t.Fatalf("open durable runtime: %v", err)
	}
	return runtime
}

// entryState reads one entry through the payload-free status source.
func (f *h7dFixture) entryState(
	ctx context.Context,
	runtime *DurableOutboxRuntime,
	entryID string,
) (MessageDeliveryState, MessageStatus, bool) {
	f.t.Helper()
	statuses, err := runtime.StatusSource().ListMessageStatuses(
		ctx,
		f.accountKey,
		42,
	)
	if err != nil {
		f.t.Fatalf("ListMessageStatuses() error = %v", err)
	}
	for _, status := range statuses {
		if status.EntryID != entryID {
			continue
		}
		switch status.State {
		case MessageDeliveryQueued:
			return MessageDeliveryQueued, status, true
		case MessageDeliverySending:
			return MessageDeliverySending, status, true
		case MessageDeliveryRetrying:
			return MessageDeliveryRetrying, status, true
		case MessageDeliveryFailed:
			return MessageDeliveryFailed, status, true
		case MessageDeliveryUncertain:
			return MessageDeliveryUncertain, status, true
		case MessageDeliverySent:
			return MessageDeliverySent, status, true
		case MessageDeliveryCanceled:
			return MessageDeliveryCanceled, status, true
		}
	}
	return "", MessageStatus{}, false
}

// waitForEntryState fires dispatcher timers until the entry reaches want.
//
// The timer is the only synchronization point: the dispatcher never scans on
// its own, and the short sleep is a progress guard, not a scheduling
// mechanism.
func (f *h7dFixture) waitForEntryState(
	ctx context.Context,
	runtime *DurableOutboxRuntime,
	entryID string,
	want MessageDeliveryState,
) MessageStatus {
	f.t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for {
		state, status, ok := f.entryState(ctx, runtime, entryID)
		if ok && state == want {
			return status
		}
		if time.Now().After(deadline) {
			f.t.Fatalf(
				"entry state = %q, want %q (found=%v)",
				state,
				want,
				ok,
			)
		}
		f.clock.fire(f.t)
		time.Sleep(2 * time.Millisecond)
	}
}

// TestDurableRuntimeWakesDispatcherOnSubmit pins the production wiring:
// with no notifier supplied, a submission wakes the dispatcher at once
// instead of waiting for the poll interval. The test clock is never
// fired, so only the wake-up can deliver the message.
func TestDurableRuntimeWakesDispatcherOnSubmit(t *testing.T) {
	t.Parallel()

	fixture := newH7dFixture(t)
	session := newH7dSession(nil)
	runtime, err := OpenDurableOutboxRuntime(
		context.Background(),
		fixture.config(),
		DurableOutboxRuntimeDeps{
			KeyProvider: fixture.provider,
			Session:     session,
			Clock:       fixture.clock,
			IDGenerator: durableRuntimeTestIDGenerator("wake"),
			AccountKey:  fixture.accountKey,
		},
	)
	if err != nil {
		t.Fatalf("open durable runtime: %v", err)
	}
	t.Cleanup(func() {
		if err := runtime.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	if _, err := runtime.Submitter().SubmitMessage(
		context.Background(),
		fixture.accountKey,
		42,
		"hello",
	); err != nil {
		t.Fatalf("SubmitMessage() error = %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for session.calls.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("submission did not wake the dispatcher")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestDurableLifecycleDispatchesQueuedEntryAfterRestart(t *testing.T) {
	t.Parallel()

	fixture := newH7dFixture(t)
	first := fixture.mustOpen(context.Background(), newH7dSession(nil))

	submission, err := first.Submitter().SubmitMessage(
		context.Background(),
		fixture.accountKey,
		42,
		h7dSecretText,
	)
	if err != nil {
		t.Fatalf("SubmitMessage() error = %v", err)
	}
	if submission.State != MessageDeliveryQueued {
		t.Fatalf("submission state = %q, want queued", submission.State)
	}

	// The dispatcher never scanned: the entry is still queued when the
	// runtime stops.
	fixture.waitForEntryState(
		context.Background(),
		first,
		submission.ID,
		MessageDeliveryQueued,
	)
	if err := first.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	loadCalls, createCalls := fixture.provider.counts()

	secondSession := newH7dSession(nil)
	second := fixture.mustOpen(context.Background(), secondSession)
	t.Cleanup(func() {
		if err := second.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	fixture.clock.fire(t)
	// Sending, not sent: this session reports no live store, so nothing
	// ever delivers TDLib's confirmation and the entry stays in the
	// state TDLib's own answer put it in. The chain that moves it on
	// from there is proved by the send-result reconciler's own test.
	fixture.waitForEntryState(
		context.Background(),
		second,
		submission.ID,
		MessageDeliverySending,
	)

	if got := secondSession.calls.Load(); got != 1 {
		t.Fatalf("dispatcher sends = %d, want 1", got)
	}

	loadAfter, createAfter := fixture.provider.counts()
	if createAfter != createCalls {
		t.Fatalf(
			"CreateKey calls = %d, want %d: reopen must not replace the key",
			createAfter,
			createCalls,
		)
	}
	if loadAfter <= loadCalls {
		t.Fatalf("LoadKey calls = %d, want more than %d", loadAfter, loadCalls)
	}
}

func TestDurableLifecyclePreservesRetryScheduleAcrossRestart(t *testing.T) {
	t.Parallel()

	fixture := newH7dFixture(t)
	retryable := &outbox.SendError{Code: 429, Retryable: true}
	firstSession := newH7dSession(func(
		context.Context,
		telegram.ChatID,
		string,
	) (telegram.Message, error) {
		return telegram.Message{}, retryable
	})
	first := fixture.mustOpen(context.Background(), firstSession)

	submission, err := first.Submitter().SubmitMessage(
		context.Background(),
		fixture.accountKey,
		42,
		h7dSecretText,
	)
	if err != nil {
		t.Fatalf("SubmitMessage() error = %v", err)
	}

	fixture.clock.fire(t)
	persisted := fixture.waitForEntryState(
		context.Background(),
		first,
		submission.ID,
		MessageDeliveryRetrying,
	)
	if persisted.Attempt != 1 {
		t.Fatalf("attempt = %d, want 1", persisted.Attempt)
	}
	if persisted.NextAttemptAt.IsZero() {
		t.Fatal("persisted NextAttemptAt is zero")
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	// Reopen while the entry is still waiting for its next attempt.
	secondSession := newH7dSession(nil)
	second := fixture.mustOpen(context.Background(), secondSession)
	t.Cleanup(func() {
		if err := second.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	fixture.clock.fire(t)
	_, reopened, ok := fixture.entryState(
		context.Background(),
		second,
		submission.ID,
	)
	if !ok {
		t.Fatal("entry disappeared after restart")
	}
	if !reopened.NextAttemptAt.Equal(persisted.NextAttemptAt) {
		t.Fatalf(
			"NextAttemptAt = %v, want %v",
			reopened.NextAttemptAt,
			persisted.NextAttemptAt,
		)
	}
	if reopened.Attempt != persisted.Attempt {
		t.Fatalf(
			"attempt = %d, want %d",
			reopened.Attempt,
			persisted.Attempt,
		)
	}
	if secondSession.calls.Load() != 0 {
		t.Fatalf(
			"sender calls before eligibility = %d, want 0",
			secondSession.calls.Load(),
		)
	}

	// Move past the scheduled attempt and let the dispatcher run again.
	fixture.clock.setNow(persisted.NextAttemptAt.Add(time.Second))
	fixture.clock.fire(t)
	// Sending, not sent: the confirmation that would move it on never
	// arrives in this session, which reports no live store.
	fixture.waitForEntryState(
		context.Background(),
		second,
		submission.ID,
		MessageDeliverySending,
	)

	if got := secondSession.calls.Load(); got != 1 {
		t.Fatalf("sender calls after eligibility = %d, want 1", got)
	}
	if texts := secondSession.sentTexts(); len(texts) != 1 ||
		texts[0] != h7dSecretText {
		t.Fatal("reopened payload does not match the original")
	}
}

func TestDurableLifecycleRecoversExpiredLeaseAsUncertain(t *testing.T) {
	t.Parallel()

	fixture := newH7dFixture(t)

	// Prepare a persisted dispatching entry, then abandon it: closing the
	// store without finalization is the crash this scenario models.
	opened, err := outbox.Open(
		context.Background(),
		fixture.config().Outbox,
		outbox.Deps{
			KeyProvider: fixture.provider,
			Sender:      NewTelegramOutboxSender(newH7dSession(nil)),
			Clock:       fixture.clock,
		},
	)
	if err != nil {
		t.Fatalf("outbox.Open() error = %v", err)
	}
	now := fixture.clock.Now()
	entry := outbox.Entry{
		ID:         outbox.ID("h7d-expired-lease"),
		AccountKey: fixture.accountKey,
		ChatID:     42,
		Text:       h7dSecretText,
		State:      outbox.StateQueued,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := opened.Store.Enqueue(context.Background(), entry); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	if _, err := opened.Store.Claim(
		context.Background(),
		entry.ID,
		0,
		"crashed-instance",
		now.Add(30*time.Second),
		now,
	); err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if err := opened.Close(); err != nil {
		t.Fatalf("outbox close: %v", err)
	}

	// The lease expires while the process is down.
	fixture.clock.setNow(now.Add(2 * time.Minute))

	session := newH7dSession(nil)
	runtime := fixture.mustOpen(context.Background(), session)
	t.Cleanup(func() {
		if err := runtime.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	status := fixture.waitForEntryState(
		context.Background(),
		runtime,
		string(entry.ID),
		MessageDeliveryUncertain,
	)
	if status.Attempt == 0 {
		t.Fatal("attempt was silently reset by recovery")
	}

	// Recovery is not a delivery trigger.
	fixture.clock.fire(t)
	if got := session.calls.Load(); got != 0 {
		t.Fatalf("sender calls = %d, want 0 for a recovered entry", got)
	}
}

func TestDurableLifecycleDoesNotRetryUncertainAfterRestart(t *testing.T) {
	t.Parallel()

	fixture := newH7dFixture(t)

	opened, err := outbox.Open(
		context.Background(),
		fixture.config().Outbox,
		outbox.Deps{
			KeyProvider: fixture.provider,
			Sender:      NewTelegramOutboxSender(newH7dSession(nil)),
			Clock:       fixture.clock,
		},
	)
	if err != nil {
		t.Fatalf("outbox.Open() error = %v", err)
	}
	now := fixture.clock.Now()
	entry := outbox.Entry{
		ID:         outbox.ID("h7d-uncertain"),
		AccountKey: fixture.accountKey,
		ChatID:     42,
		Text:       h7dSecretText,
		State:      outbox.StateQueued,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := opened.Store.Enqueue(context.Background(), entry); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	claimed, err := opened.Store.Claim(
		context.Background(),
		entry.ID,
		0,
		"crashed-instance",
		now.Add(30*time.Second),
		now,
	)
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if _, err := opened.Store.MarkUncertain(
		context.Background(),
		entry.ID,
		claimed.Version,
		"uncertain",
		now,
	); err != nil {
		t.Fatalf("MarkUncertain() error = %v", err)
	}
	if err := opened.Close(); err != nil {
		t.Fatalf("outbox close: %v", err)
	}

	first := fixture.mustOpen(context.Background(), newH7dSession(nil))
	fixture.waitForEntryState(
		context.Background(),
		first,
		string(entry.ID),
		MessageDeliveryUncertain,
	)
	if err := first.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	// A second restart must not resurrect the entry.
	secondSession := newH7dSession(nil)
	second := fixture.mustOpen(context.Background(), secondSession)
	t.Cleanup(func() {
		if err := second.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	for i := 0; i < 3; i++ {
		fixture.clock.fire(t)
	}
	fixture.waitForEntryState(
		context.Background(),
		second,
		string(entry.ID),
		MessageDeliveryUncertain,
	)

	if got := secondSession.calls.Load(); got != 0 {
		t.Fatalf("sender calls = %d, want 0 for an uncertain entry", got)
	}
}

func TestDurableLifecycleFailsClosedWithoutExistingKey(t *testing.T) {
	t.Parallel()

	fixture := newH7dFixture(t)
	first := fixture.mustOpen(context.Background(), newH7dSession(nil))
	if err := first.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	fixture.provider.deleteKey(fixture.databaseID)
	_, createCallsBefore := fixture.provider.counts()

	session := newH7dSession(nil)
	reopened, err := fixture.open(context.Background(), session)
	if reopened != nil {
		t.Fatalf("runtime = %#v, want nil", reopened)
	}
	if !errors.Is(err, outbox.ErrOutboxInconsistentInit) {
		t.Fatalf("error = %v, want ErrOutboxInconsistentInit", err)
	}
	if !errors.Is(err, outbox.ErrOutboxKeyUnavailable) {
		t.Fatalf("error = %v, want ErrOutboxKeyUnavailable", err)
	}
	if _, createCallsAfter := fixture.provider.counts(); createCallsAfter !=
		createCallsBefore {
		t.Fatalf(
			"CreateKey calls = %d, want unchanged %d: no replacement key",
			createCallsAfter,
			createCallsBefore,
		)
	}
	if got := session.calls.Load(); got != 0 {
		t.Fatalf("sender calls = %d, want 0", got)
	}
}

func TestDurableLifecycleFailsClosedForEmptyDatabaseWithoutKey(t *testing.T) {
	t.Parallel()

	fixture := newH7dFixture(t)
	database := filepath.Join(fixture.dataDir, "outbox.db")
	if err := os.WriteFile(database, nil, 0o600); err != nil {
		t.Fatalf("create empty database: %v", err)
	}

	_, createCallsBefore := fixture.provider.counts()

	session := newH7dSession(nil)
	runtime, err := fixture.open(context.Background(), session)
	if runtime != nil {
		t.Fatalf("runtime = %#v, want nil", runtime)
	}
	if !errors.Is(err, outbox.ErrOutboxInconsistentInit) {
		t.Fatalf("error = %v, want ErrOutboxInconsistentInit", err)
	}
	if _, createCallsAfter := fixture.provider.counts(); createCallsAfter !=
		createCallsBefore {
		t.Fatalf(
			"CreateKey calls = %d, want unchanged %d",
			createCallsAfter,
			createCallsBefore,
		)
	}
	if got := session.calls.Load(); got != 0 {
		t.Fatalf("sender calls = %d, want 0", got)
	}
}

func TestDurableLifecycleDoesNotFallbackToDirectAfterRestartFailure(t *testing.T) {
	t.Parallel()

	fixture := newH7dFixture(t)
	first := fixture.mustOpen(context.Background(), newH7dSession(nil))
	if err := first.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	fixture.provider.deleteKey(fixture.databaseID)

	directSubmitter := &h4RecordingComposerSubmitter{}
	runtime, err := openMessageDeliveryRuntime(
		context.Background(),
		MessageDeliveryRuntimeConfig{
			Mode:    config.MessageSendModeDurable,
			Durable: fixture.config(),
		},
		MessageDeliveryRuntimeDeps{
			DirectSubmitter: directSubmitter,
			Durable: DurableOutboxRuntimeDeps{
				KeyProvider: fixture.provider,
				Session:     newH7dSession(nil),
				Clock:       fixture.clock,
				IDGenerator: durableRuntimeTestIDGenerator("h7d-fallback"),
				AccountKey:  fixture.accountKey,
			},
		},
		messageDeliveryRuntimeFactory{
			openDurable: func(
				ctx context.Context,
				cfg DurableOutboxRuntimeConfig,
				deps DurableOutboxRuntimeDeps,
			) (MessageDeliveryRuntime, error) {
				return OpenDurableOutboxRuntime(ctx, cfg, deps)
			},
		},
	)
	if err == nil {
		t.Fatal("openMessageDeliveryRuntime() error = nil, want non-nil")
	}
	if !errors.Is(err, outbox.ErrOutboxInconsistentInit) {
		t.Fatalf("error = %v, want ErrOutboxInconsistentInit", err)
	}
	if runtime != nil {
		t.Fatalf("runtime = %#v, want nil", runtime)
	}
	if directSubmitter.calls.Load() != 0 {
		t.Fatalf(
			"direct submitter calls = %d, want 0",
			directSubmitter.calls.Load(),
		)
	}
}

func TestDurableLifecycleClosesStoreBeforeSessionAfterRestart(t *testing.T) {
	t.Parallel()

	fixture := newH7dFixture(t)
	cfg := h5bConfig(t)
	cfg.MessageDelivery.Mode = config.MessageSendModeDurable
	cfg.MessageDelivery.DataDir = fixture.dataDir
	cfg.MessageDelivery.DatabaseID = fixture.databaseID
	cfg.MessageDelivery.InstanceID = fixture.instanceID

	session := newH7dSession(nil)
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)

	result, err := prepareDeliveryAuthResult(
		ctx,
		cfg,
		session,
		cancel,
		func(
			openCtx context.Context,
			runtimeCfg MessageDeliveryRuntimeConfig,
			runtimeDeps MessageDeliveryRuntimeDeps,
		) (MessageDeliveryRuntime, error) {
			durableDeps := runtimeDeps.Durable
			durableDeps.KeyProvider = fixture.provider
			durableDeps.Clock = fixture.clock
			durableDeps.AccountKey = fixture.accountKey
			return OpenDurableOutboxRuntime(
				openCtx,
				runtimeCfg.Durable,
				durableDeps,
			)
		},
		deliveryHealthSampling{},
		0,
	)
	if err != nil {
		t.Fatalf("prepareDeliveryAuthResult() error = %v", err)
	}

	if err := result.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if !session.closed.Load() {
		t.Fatal("session was not closed")
	}

	// The durable store was released by the same cleanup, so a fresh open on
	// the same data directory succeeds.
	reopened := fixture.mustOpen(context.Background(), newH7dSession(nil))
	if err := reopened.Close(); err != nil {
		t.Fatalf("reopen Close() error = %v", err)
	}
}

// h7dHoldingSession returns a session whose sends do not complete until
// release is closed or the caller's context is cancelled.
//
// Both runtimes in the restart test need it. An earlier version held only
// the reopened runtime's send, which left the first runtime free to
// deliver the message before Close: the first runtime's dispatcher then
// raced Close, and the entry was sometimes already accepted and sometimes
// still queued, which is what made the assertion flaky.
func h7dHoldingSession(release <-chan struct{}) *h7dSession {
	return newH7dSession(func(
		ctx context.Context,
		_ telegram.ChatID,
		_ string,
	) (telegram.Message, error) {
		select {
		case <-release:
		case <-ctx.Done():
			// Close cancels the runtime context; a send that ignores it
			// would hang Close instead of unwinding.
			return telegram.Message{}, ctx.Err()
		}
		return telegram.Message{
			ID:     telegram.MessageID(1),
			ChatID: telegram.ChatID(42),
		}, nil
	})
}

// h7dAssertOneUndelivered checks the aggregate the test actually depends
// on: exactly one entry survived the restart, and it was never delivered.
//
// No single counter is asserted, because no counter is scheduling
// independent. After the restart the entry is in one of several states
// depending on what the dispatchers managed to do before they were
// stopped:
//
//   - Queued, when nothing picked it up;
//   - Dispatching, while the reopened runtime's dispatcher works on it;
//   - Uncertain, when the *first* runtime's dispatcher started the send
//     and Close cancelled it mid-flight. An interrupted send is recorded
//     as uncertain, not as queued, because whether Telegram received it
//     is unknown.
//
// All three mean the same thing for this test: the record exists and was
// not delivered. Counting every non-terminal state therefore yields 1
// whichever path the scheduler took, and Accepted must be 0.
//
// The no-leak assertions elsewhere in the test are unaffected: they look
// at the payload, not at the state.
func h7dAssertOneUndelivered(t *testing.T, health MessageDeliveryHealth) {
	t.Helper()

	undelivered := health.Queued +
		health.Dispatching +
		health.FailedRetryable +
		health.Uncertain
	if undelivered != 1 {
		t.Fatalf(
			"health queued=%d dispatching=%d failed_retryable=%d uncertain=%d, "+
				"want their sum to be 1 (accepted=%d)",
			health.Queued,
			health.Dispatching,
			health.FailedRetryable,
			health.Uncertain,
			health.Accepted,
		)
	}
	if health.Accepted != 0 {
		t.Fatalf(
			"health accepted = %d, want 0: the message must not be delivered",
			health.Accepted,
		)
	}
	if health.Canceled != 0 || health.FailedPermanent != 0 {
		t.Fatalf(
			"health canceled=%d failed_permanent=%d, want 0",
			health.Canceled,
			health.FailedPermanent,
		)
	}
}

func TestDurableLifecycleRestartErrorsDoNotContainMessageText(t *testing.T) {
	t.Parallel()

	h7dRunRestartLeakCheck(t, 0)
}

// The same check with a pause before the first Close, so the first
// runtime's dispatcher has time to start the send and have it cancelled.
// That is the path a slow CI runner takes, and it is the one that used to
// fail. It is a separate test rather than a comment, because a claim that
// a race is gone is worth little unless the slow path is exercised.
func TestDurableLifecycleRestartErrorsDoNotContainMessageTextWithSlowFirstRuntime(
	t *testing.T,
) {
	t.Parallel()

	h7dRunRestartLeakCheck(t, 300*time.Millisecond)
}

// h7dRunRestartLeakCheck runs the restart leak check, giving the first
// runtime pauseBeforeClose to dispatch before it is closed.
func h7dRunRestartLeakCheck(t *testing.T, pauseBeforeClose time.Duration) {
	t.Helper()

	fixture := newH7dFixture(t)

	// The first runtime must not deliver the message: if it does, nothing
	// is left to survive the restart and there is nothing to assert.
	firstRelease := make(chan struct{})
	first := fixture.mustOpen(
		context.Background(), h7dHoldingSession(firstRelease),
	)
	if _, err := first.Submitter().SubmitMessage(
		context.Background(),
		fixture.accountKey,
		42,
		h7dSecretText,
	); err != nil {
		t.Fatalf("SubmitMessage() error = %v", err)
	}
	if pauseBeforeClose > 0 {
		// Give the first dispatcher a chance to take the entry, which is
		// what a slow machine does on its own.
		time.Sleep(pauseBeforeClose)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	close(firstRelease)

	// The reopened runtime also holds its send, so the record is present
	// but never finished while the assertions run.
	release := make(chan struct{})
	second := fixture.mustOpen(
		context.Background(), h7dHoldingSession(release),
	)
	t.Cleanup(func() {
		if err := second.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	// Cleanups run last-in-first-out, so this releases the send before the
	// Close above and the dispatcher can finish.
	t.Cleanup(func() { close(release) })

	statuses, err := second.StatusSource().ListMessageStatuses(
		context.Background(),
		fixture.accountKey,
		42,
	)
	if err != nil {
		t.Fatalf("ListMessageStatuses() error = %v", err)
	}
	if len(statuses) == 0 {
		t.Fatal("no persisted status after reopen")
	}
	printed := fmt.Sprintf("%#v", statuses)
	if strings.Contains(printed, h7dSecretText) {
		t.Fatal("status metadata contains message text")
	}

	health, err := second.HealthSource().ReadMessageDeliveryHealth(
		context.Background(),
	)
	if err != nil {
		t.Fatalf("ReadMessageDeliveryHealth() error = %v", err)
	}
	if strings.Contains(
		strings.ToLower(fmt.Sprintf("%#v", health)),
		h7dSecretText,
	) {
		t.Fatal("health snapshot contains message text")
	}
	h7dAssertOneUndelivered(t, health)
	if err := second.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	// The fail-closed restart failure must not leak the payload either.
	fixture.provider.deleteKey(fixture.databaseID)
	failed, reopenErr := fixture.open(context.Background(), newH7dSession(nil))
	if failed != nil {
		t.Fatalf("runtime = %#v, want nil", failed)
	}
	if reopenErr == nil {
		t.Fatal("reopen error = nil, want non-nil")
	}
	if strings.Contains(reopenErr.Error(), h7dSecretText) {
		t.Fatal("restart error contains message text")
	}
}

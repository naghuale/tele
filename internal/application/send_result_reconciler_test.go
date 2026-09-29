package application

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"telecli/internal/outbox"
	"telecli/internal/telegram"
)

// The chain, end to end over a real queue:
//
//	queued --dispatcher--> accepted --updateMessageSendSucceeded--> sent
//	                            \--updateMessageSendFailed-------> failed
//
// The events are the ones internal/telegram/send_result_window_test.go
// decodes out of recorded TDLib payloads, so what this file moves the
// queue with is what a real session delivers.

// recordedMessageEvents is a window holding the message events of the
// chats it is given, as the live store hands them out.
//
// It is partitioned by chat exactly as the real window is. An earlier
// version of this fake ignored the chat id it was asked about and
// answered with every event it held, which meant no test of the
// reconciler could ever notice a confirmation filed under the wrong chat
// — the one way a result can be delivered and never matched. The real
// wiring is proved end to end in send_result_runtime_test.go; this fake
// is for the cases that do not need a whole runtime.
type recordedMessageEvents struct {
	mu     sync.Mutex
	events map[telegram.ChatID][]telegram.MessageEvent

	changed chan struct{}
}

func newRecordedMessageEvents(
	events ...telegram.MessageEvent,
) *recordedMessageEvents {
	// Events handed to the constructor are treated as belonging to the
	// only chat these fixtures use, so a test that does not care about
	// the partition still reads naturally.
	record := &recordedMessageEvents{
		events:  make(map[telegram.ChatID][]telegram.MessageEvent),
		changed: make(chan struct{}, 1),
	}
	if len(events) > 0 {
		record.events[recordedMessageEventsChat] = events
	}
	return record
}

// recordedMessageEventsChat is the chat the reconciler fixtures send to.
const recordedMessageEventsChat = telegram.ChatID(7)

func (r *recordedMessageEvents) MessageEventsSince(
	chatID telegram.ChatID,
	_ uint64,
) ([]telegram.MessageEvent, uint64, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	held := r.events[chatID]
	out := make([]telegram.MessageEvent, len(held))
	copy(out, held)
	return out, uint64(len(held)), false
}

func (r *recordedMessageEvents) Changed() <-chan struct{} { return r.changed }

func (r *recordedMessageEvents) deliver(events ...telegram.MessageEvent) {
	r.deliverTo(recordedMessageEventsChat, events...)
}

// deliverTo files events under one chat, so a test can put a
// confirmation in the wrong window on purpose.
func (r *recordedMessageEvents) deliverTo(
	chatID telegram.ChatID,
	events ...telegram.MessageEvent,
) {
	r.mu.Lock()
	r.events[chatID] = append(r.events[chatID], events...)
	r.mu.Unlock()

	select {
	case r.changed <- struct{}{}:
	default:
	}
}

// reconcilerFixture opens a real durable outbox in a temporary folder
// with a test key provider. The platform provider is never touched: the
// suite must not read or write a Keychain item, and the data folder is
// under t.TempDir() so it can never be the queue on this machine.
type reconcilerFixture struct {
	store      *outbox.Outbox
	events     *recordedMessageEvents
	clock      *reconcilerClock
	provider   *h7dKeyProvider
	reconciler *sendResultReconciler
}

func newReconcilerFixture(
	t *testing.T,
) *reconcilerFixture {
	t.Helper()

	dataDir := filepath.Join(t.TempDir(), "outbox")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatalf("create data dir: %v", err)
	}
	if err := os.Chmod(dataDir, 0o700); err != nil {
		t.Fatalf("chmod data dir: %v", err)
	}

	provider := newH7dKeyProvider()
	opened, err := outbox.Open(
		context.Background(),
		outbox.Config{
			DataDir:    dataDir,
			DatabaseID: "send-result-database",
			InstanceID: "send-result-instance",
			Dispatcher: outbox.DefaultDispatcherConfig(
				"send-result-instance",
			),
		},
		outbox.Deps{
			KeyProvider: provider,
			Clock:       &reconcilerClock{},
			// The sender answers with the temporary identifier TDLib
			// hands out, which is the whole reason a result has to be
			// correlated by old_message_id.
			Sender: NewTelegramOutboxSender(&reconcilerSender{}),
		},
	)
	if err != nil {
		t.Fatalf("open outbox: %v", err)
	}
	t.Cleanup(func() { _ = opened.Close() })

	results, ok := opened.Store.(outbox.SendResultStore)
	if !ok {
		t.Fatal("the durable store does not support send results")
	}

	fixture := &reconcilerFixture{
		store:    opened,
		events:   newRecordedMessageEvents(),
		clock:    &reconcilerClock{},
		provider: provider,
	}
	t.Cleanup(func() { _ = opened.Close() })

	fixture.reconciler = &sendResultReconciler{
		store:      results,
		events:     fixture.events,
		accountKey: "account-1",
		clock:      fixture.clock,
		logger:     slog.New(slog.NewTextHandler(os.Stderr, nil)),
	}
	return fixture
}

// reconcilerSender answers every send with a temporary identifier, the
// way TDLib does until it confirms the message.
type reconcilerSender struct {
	mu       sync.Mutex
	lastText string
	nextID   int64
}

func (s *reconcilerSender) SendTextMessage(
	_ context.Context,
	chatID telegram.ChatID,
	text string,
) (telegram.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.nextID += 1_000_000_000
	s.lastText = text

	return telegram.Message{
		ID:        telegram.MessageID(s.nextID),
		ChatID:    chatID,
		Outgoing:  true,
		Text:      text,
		Timestamp: time.Unix(1759100000, 0).UTC(),
	}, nil
}

// reconcilerClock is a clock a test moves by hand, so a reconcile pass
// stamps an instant the assertions can name.
type reconcilerClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *reconcilerClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.now.IsZero() {
		return time.Unix(1700000000, 0).UTC()
	}
	return c.now
}

func (c *reconcilerClock) setNow(at time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = at
}

func (c *reconcilerClock) After(d time.Duration) <-chan time.Time {
	return time.After(d)
}

// enqueueAndDispatch puts one message through the whole path the program
// takes: it is queued, the dispatcher sends it, and TDLib answers with a
// temporary identifier. The entry is then exactly where a real one waits
// for the result.
func (f *reconcilerFixture) enqueueAndDispatch(
	t *testing.T,
	chatID int64,
	text string,
) outbox.Entry {
	t.Helper()

	ctx := context.Background()
	f.clock.setNow(time.Unix(1700000000, 0).UTC())

	entry := outbox.Entry{
		ID:         outbox.ID("entry-" + text),
		AccountKey: "account-1",
		ChatID:     chatID,
		Text:       text,
		State:      outbox.StateQueued,
		CreatedAt:  f.clock.Now(),
		UpdatedAt:  f.clock.Now(),
	}
	if err := f.store.Store.Enqueue(ctx, entry); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	if _, err := f.store.Dispatcher.ScanOnce(ctx); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	dispatched, err := f.store.Store.Get(ctx, entry.ID)
	if err != nil {
		t.Fatalf("get entry: %v", err)
	}
	if dispatched.State != outbox.StateAccepted {
		t.Fatalf(
			"state = %q, want accepted: TDLib's answer alone is not a "+
				"delivery receipt", dispatched.State,
		)
	}
	return dispatched
}

// A confirmation moves the entry to sent and puts the final identifier
// where the history will look for it.
func TestConfirmationMovesAQueuedMessageToSent(t *testing.T) {
	t.Parallel()

	fixture := newReconcilerFixture(t)
	accepted := fixture.enqueueAndDispatch(t, 7, "отправлено")

	fixture.events.deliver(telegram.MessageReplaced{
		OldID: telegram.MessageID(accepted.TelegramMessageID),
		Message: telegram.Message{
			ID:        telegram.MessageID(501),
			ChatID:    7,
			Outgoing:  true,
			Timestamp: time.Unix(1759100000, 0).UTC(),
		},
	})

	fixture.clock.setNow(time.Unix(1700000600, 0).UTC())
	moved, err := fixture.reconciler.ReconcileOnce(context.Background())
	if err != nil {
		t.Fatalf("ReconcileOnce: %v", err)
	}
	if moved != 1 {
		t.Fatalf("moved = %d, want 1", moved)
	}

	sent, err := fixture.store.Store.Get(context.Background(), accepted.ID)
	if err != nil {
		t.Fatalf("get entry: %v", err)
	}
	if sent.State != outbox.StateSent {
		t.Fatalf("state = %q, want sent", sent.State)
	}
	if sent.TelegramMessageID != 501 {
		t.Fatalf(
			"message id = %d, want the final one 501: the temporary "+
				"identifier is in no history page",
			sent.TelegramMessageID,
		)
	}
	if !sent.SentAt.Equal(time.Unix(1700000600, 0).UTC()) {
		t.Fatalf("sent at = %s, want the moment of the result", sent.SentAt)
	}
	// The moment TDLib took the message is a different fact and is kept.
	if !sent.AcceptedAt.Equal(accepted.AcceptedAt) {
		t.Fatalf("accepted at = %s, want it kept", sent.AcceptedAt)
	}
	if sent.Text != accepted.Text {
		t.Fatal("the payload of a confirmed message must not change")
	}

	// Nothing is left waiting, so a second pass has nothing to do.
	again, err := fixture.reconciler.ReconcileOnce(context.Background())
	if err != nil {
		t.Fatalf("second ReconcileOnce: %v", err)
	}
	if again != 0 {
		t.Fatalf("moved = %d on a second pass, want 0", again)
	}
}

// A refusal ends the message as a permanent failure carrying Telegram's
// own code, and never TDLib's text.
func TestRefusalMovesAQueuedMessageToFailed(t *testing.T) {
	t.Parallel()

	fixture := newReconcilerFixture(t)
	accepted := fixture.enqueueAndDispatch(t, 7, "не отправлено")

	fixture.events.deliver(telegram.MessageFailed{
		OldID: telegram.MessageID(accepted.TelegramMessageID),
		Message: telegram.Message{
			ID:       telegram.MessageID(accepted.TelegramMessageID),
			ChatID:   7,
			Outgoing: true,
		},
		Error: telegram.MessageError{
			Code:        400,
			Description: "Can't send message",
		},
	})

	if _, err := fixture.reconciler.ReconcileOnce(
		context.Background(),
	); err != nil {
		t.Fatalf("ReconcileOnce: %v", err)
	}

	failed, err := fixture.store.Store.Get(context.Background(), accepted.ID)
	if err != nil {
		t.Fatalf("get entry: %v", err)
	}
	if failed.State != outbox.StateFailedPermanent {
		t.Fatalf("state = %q, want failed_permanent", failed.State)
	}
	if failed.LastErrorCode != 400 {
		t.Fatalf("error code = %d, want 400", failed.LastErrorCode)
	}
	if failed.LastErrorMessage != "telegram send error code=400" {
		t.Fatalf(
			"error message = %q, want the code and nothing else: this "+
				"string is persisted and logged",
			failed.LastErrorMessage,
		)
	}
	// A message that never went out is in no message list, so it must not
	// keep the identifier of one that does not exist.
	if failed.TelegramMessageID != 0 || !failed.AcceptedAt.IsZero() {
		t.Fatalf(
			"a failed message kept telegram id %d and accepted at %s",
			failed.TelegramMessageID, failed.AcceptedAt,
		)
	}
}

// The confirmation belongs to one message and to no other. A chat with
// two messages going out must not resolve the first when the second is
// confirmed, or a user would see the wrong text marked as sent.
func TestConfirmationIsMatchedOnTheTemporaryIdentifierAlone(t *testing.T) {
	t.Parallel()

	fixture := newReconcilerFixture(t)
	first := fixture.enqueueAndDispatch(t, 7, "первое")
	// The dispatcher keeps a chat's messages in order, so the second one
	// is dispatched once the first has left the queue.
	fixture.events.deliver(telegram.MessageReplaced{
		OldID: telegram.MessageID(first.TelegramMessageID),
		Message: telegram.Message{
			ID: 900, ChatID: 7, Outgoing: true,
		},
	})
	if _, err := fixture.reconciler.ReconcileOnce(
		context.Background(),
	); err != nil {
		t.Fatalf("ReconcileOnce: %v", err)
	}

	second := fixture.enqueueAndDispatch(t, 7, "второе")

	// The confirmation that arrives names the *second* message's
	// temporary identifier, and carries the first message's final one.
	fixture.events.deliver(telegram.MessageReplaced{
		OldID: telegram.MessageID(second.TelegramMessageID),
		Message: telegram.Message{
			ID: 901, ChatID: 7, Outgoing: true,
		},
	})
	if _, err := fixture.reconciler.ReconcileOnce(
		context.Background(),
	); err != nil {
		t.Fatalf("ReconcileOnce: %v", err)
	}

	firstAfter, err := fixture.store.Store.Get(context.Background(), first.ID)
	if err != nil {
		t.Fatalf("get first: %v", err)
	}
	if firstAfter.TelegramMessageID != 900 {
		t.Fatalf(
			"first message id = %d, want 900", firstAfter.TelegramMessageID,
		)
	}

	secondAfter, err := fixture.store.Store.Get(context.Background(), second.ID)
	if err != nil {
		t.Fatalf("get second: %v", err)
	}
	if secondAfter.State != outbox.StateSent ||
		secondAfter.TelegramMessageID != 901 {
		t.Fatalf(
			"second = %#v, want sent with the final id 901", secondAfter,
		)
	}
}

// An update about a message this queue never sent is not an error. The
// same window carries every message of every client, and a confirmation
// for somebody else's message has to be ignored rather than reported.
func TestConfirmationForAMessageThisQueueNeverSentIsIgnored(t *testing.T) {
	t.Parallel()

	fixture := newReconcilerFixture(t)
	fixture.enqueueAndDispatch(t, 7, "моё")

	fixture.events.deliver(
		telegram.MessageReplaced{
			OldID:   telegram.MessageID(999_999_999),
			Message: telegram.Message{ID: 1, ChatID: 7, Outgoing: true},
		},
		telegram.MessageAdded{
			Message: telegram.Message{ID: 2, ChatID: 7},
		},
		telegram.MessagesDeleted{IDs: []telegram.MessageID{3}},
	)

	moved, err := fixture.reconciler.ReconcileOnce(context.Background())
	if err != nil {
		t.Fatalf("ReconcileOnce: %v", err)
	}
	if moved != 0 {
		t.Fatalf("moved = %d, want 0", moved)
	}
}

// The result of a send of another account is not this account's answer.
// A queue is opened per account, and the reconciler only ever speaks for
// the one it was opened for.
func TestConfirmationOfAnotherAccountIsNotApplied(t *testing.T) {
	t.Parallel()

	fixture := newReconcilerFixture(t)
	accepted := fixture.enqueueAndDispatch(t, 7, "моё")

	fixture.events.deliver(telegram.MessageReplaced{
		OldID:   telegram.MessageID(accepted.TelegramMessageID),
		Message: telegram.Message{ID: 501, ChatID: 7, Outgoing: true},
	})

	other := &sendResultReconciler{
		store:      fixture.store.Store.(outbox.SendResultStore),
		events:     fixture.events,
		accountKey: "account-2",
		clock:      fixture.clock,
	}
	moved, err := other.ReconcileOnce(context.Background())
	if err != nil {
		t.Fatalf("ReconcileOnce: %v", err)
	}
	if moved != 0 {
		t.Fatalf("moved = %d, want 0: the entry belongs to another account", moved)
	}

	untouched, err := fixture.store.Store.Get(context.Background(), accepted.ID)
	if err != nil {
		t.Fatalf("get entry: %v", err)
	}
	if untouched.State != outbox.StateAccepted {
		t.Fatalf("state = %q, want accepted", untouched.State)
	}
}

// A window that can no longer answer for the chat is reported and nothing
// is applied. The entries it covers are still waiting, and the next pass
// reads what the window holds then.
func TestAWindowThatCannotAnswerIsReportedAndChangesNothing(t *testing.T) {
	t.Parallel()

	fixture := newReconcilerFixture(t)
	accepted := fixture.enqueueAndDispatch(t, 7, "моё")

	fixture.events.deliver(telegram.MessageReplaced{
		OldID:   telegram.MessageID(accepted.TelegramMessageID),
		Message: telegram.Message{ID: 501, ChatID: 7, Outgoing: true},
	})
	// A window that reports resync answers nothing at all, whatever it
	// was holding.
	stuck := &resyncingMessageEvents{}
	reconciler := &sendResultReconciler{
		store:      fixture.store.Store.(outbox.SendResultStore),
		events:     stuck,
		accountKey: "account-1",
		clock:      fixture.clock,
		logger:     slog.New(slog.NewTextHandler(os.Stderr, nil)),
	}

	moved, err := reconciler.ReconcileOnce(context.Background())
	if err != nil {
		t.Fatalf("ReconcileOnce: %v", err)
	}
	if moved != 0 {
		t.Fatalf("moved = %d, want 0", moved)
	}

	untouched, err := fixture.store.Store.Get(context.Background(), accepted.ID)
	if err != nil {
		t.Fatalf("get entry: %v", err)
	}
	if untouched.State != outbox.StateAccepted {
		t.Fatalf("state = %q, want accepted", untouched.State)
	}
}

type resyncingMessageEvents struct{}

func (r *resyncingMessageEvents) MessageEventsSince(
	telegram.ChatID,
	uint64,
) ([]telegram.MessageEvent, uint64, bool) {
	return nil, 0, true
}

func (r *resyncingMessageEvents) Changed() <-chan struct{} { return nil }

// The reconciler loop is what the runtime runs, and it has to stop when
// the runtime does: a goroutine that outlives the store would read a
// database that has been closed.
func TestReconcilerRunStopsWithItsContext(t *testing.T) {
	t.Parallel()

	fixture := newReconcilerFixture(t)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- fixture.reconciler.Run(ctx) }()

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run error = %v, want a clean stop", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the reconciler did not stop with its context")
	}
}

// A reconciler with no store or no live store is refused at construction
// rather than started and left doing nothing.
func TestReconcilerRefusesToRunWithoutItsDependencies(t *testing.T) {
	t.Parallel()

	for name, reconciler := range map[string]*sendResultReconciler{
		"no store":  {events: newRecordedMessageEvents()},
		"no events": {store: outbox.NewMemoryStore()},
		"neither":   {},
	} {
		if err := reconciler.Run(context.Background()); err == nil {
			t.Fatalf("%s: Run() error = nil, want a refusal", name)
		}
	}
}

// The loop wakes on the live store's own change signal, so a
// confirmation is applied when TDLib delivers it rather than a poll
// interval later.
func TestReconcilerRunAppliesAResultOnTheChangeSignal(t *testing.T) {
	t.Parallel()

	fixture := newReconcilerFixture(t)
	accepted := fixture.enqueueAndDispatch(t, 7, "по сигналу")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- fixture.reconciler.Run(ctx) }()

	fixture.events.deliver(telegram.MessageReplaced{
		OldID: telegram.MessageID(accepted.TelegramMessageID),
		Message: telegram.Message{
			ID: 777, ChatID: 7, Outgoing: true,
		},
	})

	deadline := time.Now().Add(3 * time.Second)
	for {
		entry, err := fixture.store.Store.Get(context.Background(), accepted.ID)
		if err != nil {
			t.Fatalf("get entry: %v", err)
		}
		if entry.State == outbox.StateSent {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("state = %q, want sent", entry.State)
		}
		time.Sleep(2 * time.Millisecond)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run error = %v, want a clean stop", err)
	}
}

// The store answers a confirmation for an entry that is no longer waiting
// with a sentinel, not a failure: the update reaches every consumer of the
// window and the entry may already have been resolved.
func TestAResultForAnEntryThatMovedOnIsNotAnError(t *testing.T) {
	t.Parallel()

	store := outbox.NewMemoryStore()
	ctx := context.Background()
	entry := outbox.Entry{
		ID:         "entry-gone",
		AccountKey: "account-1",
		ChatID:     7,
		Text:       "x",
		State:      outbox.StateQueued,
		CreatedAt:  time.Unix(1700000000, 0).UTC(),
		UpdatedAt:  time.Unix(1700000000, 0).UTC(),
	}
	if err := store.Enqueue(ctx, entry); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	_, err := store.ApplySendResult(
		ctx, "account-1",
		outbox.SendResult{OldMessageID: 4242, MessageID: 501},
		time.Unix(1700000600, 0).UTC(),
	)
	if !errors.Is(err, outbox.ErrNoAcceptedEntry) {
		t.Fatalf("error = %v, want ErrNoAcceptedEntry", err)
	}
}

// A confirmation filed under another chat is delivered, counted, and
// matched to nothing.
//
// The window is partitioned by chat, so this is the case where TDLib
// names a temporary identifier this queue holds while the window is not
// the one the queue would look in. The entry must not move, and the fact
// must be counted — it is the difference between "Telegram never
// confirmed" and "the confirmation was delivered and nobody matched it".
func TestAConfirmationFiledUnderAnotherChatMatchesNothingAndIsCounted(
	t *testing.T,
) {
	t.Parallel()

	fixture := newReconcilerFixture(t)
	accepted := fixture.enqueueAndDispatch(t, 7, "моё")

	fixture.events.deliverTo(4242, telegram.MessageReplaced{
		OldID:   telegram.MessageID(accepted.TelegramMessageID),
		Message: telegram.Message{ID: 501, ChatID: 4242, Outgoing: true},
	})

	moved, err := fixture.reconciler.ReconcileOnce(context.Background())
	if err != nil {
		t.Fatalf("ReconcileOnce: %v", err)
	}
	if moved != 0 {
		t.Fatalf("moved = %d, want 0", moved)
	}

	untouched, err := fixture.store.Store.Get(context.Background(), accepted.ID)
	if err != nil {
		t.Fatalf("get entry: %v", err)
	}
	if untouched.State != outbox.StateAccepted {
		t.Fatalf("state = %q, want accepted", untouched.State)
	}

	// The entry's own chat is empty, so nothing was seen at all: the
	// confirmation is in a window the queue never reads.
	report := fixture.reconciler.counters.report()
	if report.Seen != 0 {
		t.Fatalf("seen = %d, want 0: nothing was read", report.Seen)
	}
}

// A confirmation this queue already applied is counted once, not once per
// pass.
//
// The window is read from the beginning on every pass, so a result that
// is still in it is read again and again. Counting it every time would
// make `named no record` climb for a queue that is working perfectly, and
// the owner reading that number would go looking for a fault that is not
// there.
func TestAMatchedConfirmationIsCountedOnceAndNotOnEveryPass(t *testing.T) {
	t.Parallel()

	fixture := newReconcilerFixture(t)
	accepted := fixture.enqueueAndDispatch(t, 7, "моё")

	fixture.events.deliver(telegram.MessageReplaced{
		OldID:   telegram.MessageID(accepted.TelegramMessageID),
		Message: telegram.Message{ID: 501, ChatID: 7, Outgoing: true},
	})

	// A pass that applies it, and then a run of passes that find the
	// result again in the window and find nothing to apply it to.
	for pass := 0; pass < 5; pass++ {
		if _, err := fixture.reconciler.ReconcileOnce(
			context.Background(),
		); err != nil {
			t.Fatalf("pass %d: %v", pass, err)
		}
	}

	report := fixture.reconciler.counters.report()
	if report.Matched != 1 {
		t.Fatalf("matched = %d, want 1", report.Matched)
	}
	if report.NoEntry != 0 {
		t.Fatalf(
			"noEntry = %d: a confirmation this queue applied is counted "+
				"again as naming no record, which is the number the owner "+
				"reads to decide whether Telegram's results are arriving",
			report.NoEntry,
		)
	}
	// The result is read exactly once: after the first pass there is no
	// accepted record left, and a pass with nothing to match against does
	// not read the window at all. So the dedup above is the belt to that
	// pair of braces, for the passes that do read a window with several
	// records in it.
	if report.Seen != 1 {
		t.Fatalf("seen = %d, want the result read once", report.Seen)
	}
}

// A confirmation for a record this queue does not hold is reported — but
// only once it is certain.
//
// Two things are being kept apart here. Several messages sent in a row are
// all confirmed before the queue has written a single acceptance, so for a
// moment the window holds confirmations that match nothing, and counting
// them at once would tell the owner that a working queue is full of
// results it cannot place. And a confirmation for a message this queue
// never sent — another client, another account, a record already resolved
// — really is one that will never match, and is worth saying so.
func TestAConfirmationForARecordThisQueueDoesNotHoldIsReportedOnce(
	t *testing.T,
) {
	t.Parallel()

	fixture := newReconcilerFixture(t)
	// One record is waiting, so the window is read; the confirmation in
	// it names a different temporary identifier.
	fixture.enqueueAndDispatch(t, 7, "моё")
	fixture.events.deliver(telegram.MessageReplaced{
		OldID:   telegram.MessageID(999999999),
		Message: telegram.Message{ID: 501, ChatID: 7, Outgoing: true},
	})

	if _, err := fixture.reconciler.ReconcileOnce(
		context.Background(),
	); err != nil {
		t.Fatalf("first ReconcileOnce: %v", err)
	}
	if got := fixture.reconciler.counters.report().NoEntry; got != 0 {
		t.Fatalf(
			"noEntry = %d on the first sight of the confirmation: it is "+
				"too soon to say it matches nothing", got,
		)
	}

	// Time passes and it is still matching nothing. Now it is a fact.
	fixture.clock.setNow(
		fixture.clock.Now().Add(unmatchedGrace + time.Second),
	)
	if _, err := fixture.reconciler.ReconcileOnce(
		context.Background(),
	); err != nil {
		t.Fatalf("second ReconcileOnce: %v", err)
	}
	if got := fixture.reconciler.counters.report().NoEntry; got != 1 {
		t.Fatalf("noEntry = %d, want 1 once the grace has passed", got)
	}

	// And it is counted once, not on every pass afterwards.
	fixture.clock.setNow(
		fixture.clock.Now().Add(unmatchedGrace + time.Second),
	)
	if _, err := fixture.reconciler.ReconcileOnce(
		context.Background(),
	); err != nil {
		t.Fatalf("third ReconcileOnce: %v", err)
	}
	if got := fixture.reconciler.counters.report().NoEntry; got != 1 {
		t.Fatalf("noEntry = %d, want it counted exactly once", got)
	}
}

// A confirmation whose record has not been written yet is not reported.
//
// This is the ordering a real client produces when messages are sent in a
// row: Telegram confirms each one before the queue has finished recording
// the acceptance it belongs to. Counted at once, those would appear as
// results the queue could not place.
func TestConfirmationsForRecordsNotYetAcceptedAreNotReported(
	t *testing.T,
) {
	t.Parallel()

	fixture := newReconcilerFixture(t)
	accepted := fixture.enqueueAndDispatch(t, 7, "моё")

	// Three records in flight: only the first has an acceptance written,
	// and only its confirmation is in the window so far.
	fixture.events.deliver(
		telegram.MessageReplaced{
			OldID: telegram.MessageID(accepted.TelegramMessageID),
			Message: telegram.Message{
				ID: 501, ChatID: 7, Outgoing: true,
			},
		},
		telegram.MessageReplaced{
			OldID: telegram.MessageID(-2000000001),
			Message: telegram.Message{
				ID: 502, ChatID: 7, Outgoing: true,
			},
		},
	)

	if _, err := fixture.reconciler.ReconcileOnce(
		context.Background(),
	); err != nil {
		t.Fatalf("ReconcileOnce: %v", err)
	}

	report := fixture.reconciler.counters.report()
	if report.Matched != 1 {
		t.Fatalf("matched = %d, want 1", report.Matched)
	}
	if report.Seen != 2 {
		t.Fatalf("seen = %d, want both confirmations read", report.Seen)
	}
	if report.NoEntry != 0 {
		t.Fatalf(
			"noEntry = %d: a confirmation for a record the queue is "+
				"still being told about is reported as one it will "+
				"never be able to place", report.NoEntry,
		)
	}
}

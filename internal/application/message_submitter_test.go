package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"telecli/internal/outbox"
)

// recordingNotifier counts Notify calls.
type recordingNotifier struct {
	calls int
}

func (n *recordingNotifier) Notify() { n.calls++ }

// fixedIDGen returns ids from a slice; fails with the configured
// error or when the slice is exhausted.
type fixedIDGen struct {
	ids []outbox.ID
	err error
	i   int
}

func (g *fixedIDGen) next() (outbox.ID, error) {
	if g.err != nil {
		return "", g.err
	}
	if g.i >= len(g.ids) {
		return "", errors.New("id generator exhausted")
	}
	id := g.ids[g.i]
	g.i++
	return id, nil
}

// fakeSubmitterClock returns a fixed time and never fires.
type fakeSubmitterClock struct {
	now time.Time
}

func (c *fakeSubmitterClock) Now() time.Time { return c.now }
func (c *fakeSubmitterClock) After(time.Duration) <-chan time.Time {
	return make(chan time.Time)
}

// newSubmitter wires a submitter with the given store and scripted IDs.
func newSubmitter(
	t *testing.T,
	store outbox.Store,
	ids ...outbox.ID,
) (*OutboxMessageSubmitter, *recordingNotifier) {
	t.Helper()

	gen := &fixedIDGen{ids: ids}
	notifier := &recordingNotifier{}

	sub, err := NewOutboxMessageSubmitter(
		store,
		nil, // SystemClock
		gen.next,
		notifier,
		"primary",
	)
	if err != nil {
		t.Fatalf("NewOutboxMessageSubmitter: %v", err)
	}
	return sub, notifier
}

// ---- Constructor ----

func TestNewOutboxMessageSubmitterRejectsNilStore(t *testing.T) {
	_, err := NewOutboxMessageSubmitter(
		nil,
		nil,
		func() (outbox.ID, error) { return "op-1", nil },
		nil,
		"primary",
	)
	if !errors.Is(err, ErrSubmitterNilStore) {
		t.Fatalf("err = %v, want ErrSubmitterNilStore", err)
	}
}

func TestNewOutboxMessageSubmitterRejectsNilIDGen(t *testing.T) {
	_, err := NewOutboxMessageSubmitter(
		outbox.NewMemoryStore(),
		nil,
		nil,
		nil,
		"primary",
	)
	if !errors.Is(err, ErrSubmitterNilIDGen) {
		t.Fatalf("err = %v, want ErrSubmitterNilIDGen", err)
	}
}

func TestNewOutboxMessageSubmitterRejectsEmptyAccountKey(t *testing.T) {
	for _, key := range []string{"", " ", "\t", "\n", "  \t\n "} {
		_, err := NewOutboxMessageSubmitter(
			outbox.NewMemoryStore(),
			nil,
			func() (outbox.ID, error) { return "op-1", nil },
			nil,
			key,
		)
		if !errors.Is(err, ErrSubmitterEmptyAccount) {
			t.Fatalf("key=%q: err = %v, want ErrSubmitterEmptyAccount",
				key, err)
		}
	}
}

func TestNewOutboxMessageSubmitterDefaultsClockAndNotifier(t *testing.T) {
	sub, err := NewOutboxMessageSubmitter(
		outbox.NewMemoryStore(),
		nil,
		func() (outbox.ID, error) { return "op-1", nil },
		nil,
		"primary",
	)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if sub.clock == nil {
		t.Fatal("clock not defaulted")
	}
	if sub.notifier == nil {
		t.Fatal("notifier not defaulted")
	}
}

// ---- QueueMessage success ----

func TestQueueMessageCreatesQueuedEntry(t *testing.T) {
	store := outbox.NewMemoryStore()
	sub, _ := newSubmitter(t, store, "op-1")

	entry, err := sub.QueueMessage(context.Background(), 42, "hello")
	if err != nil {
		t.Fatalf("QueueMessage: %v", err)
	}

	if entry.ID != "op-1" {
		t.Fatalf("ID = %q, want op-1", entry.ID)
	}
	if entry.AccountKey != "primary" {
		t.Fatalf("AccountKey = %q", entry.AccountKey)
	}
	if entry.ChatID != 42 {
		t.Fatalf("ChatID = %d", entry.ChatID)
	}
	if entry.State != outbox.StateQueued {
		t.Fatalf("State = %s", entry.State)
	}
	if entry.Text != "hello" {
		t.Fatalf("Text = %q", entry.Text)
	}

	stored, err := store.Get(context.Background(), entry.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.Text != "hello" {
		t.Fatalf("stored text = %q", stored.Text)
	}
	if stored.State != outbox.StateQueued {
		t.Fatalf("stored state = %s", stored.State)
	}
}

func TestQueueMessageAllowsNegativeChatID(t *testing.T) {
	store := outbox.NewMemoryStore()
	sub, _ := newSubmitter(t, store, "op-1")

	entry, err := sub.QueueMessage(context.Background(), -1001, "hi")
	if err != nil {
		t.Fatalf("QueueMessage: %v", err)
	}
	if entry.ChatID != -1001 {
		t.Fatalf("ChatID = %d, want -1001", entry.ChatID)
	}
}

func TestQueueMessagePreservesTextVerbatim(t *testing.T) {
	store := outbox.NewMemoryStore()
	sub, _ := newSubmitter(t, store, "op-1")

	text := "  hello  world  "
	entry, err := sub.QueueMessage(context.Background(), 42, text)
	if err != nil {
		t.Fatalf("QueueMessage: %v", err)
	}
	if entry.Text != text {
		t.Fatalf("entry.Text = %q, want %q", entry.Text, text)
	}

	stored, err := store.Get(context.Background(), entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Text != text {
		t.Fatalf("stored.Text = %q, want %q", stored.Text, text)
	}
}

func TestQueueMessageReturnsGeneratedID(t *testing.T) {
	store := outbox.NewMemoryStore()
	sub, _ := newSubmitter(t, store, "custom-id")

	entry, err := sub.QueueMessage(context.Background(), 42, "hello")
	if err != nil {
		t.Fatal(err)
	}
	if entry.ID != "custom-id" {
		t.Fatalf("ID = %q, want custom-id", entry.ID)
	}
}

func TestQueueMessageSetsIdenticalTimestamps(t *testing.T) {
	store := outbox.NewMemoryStore()
	now := time.Unix(1700000000, 0).UTC()
	clock := &fakeSubmitterClock{now: now}

	sub, err := NewOutboxMessageSubmitter(
		store,
		clock,
		func() (outbox.ID, error) { return "op-1", nil },
		nil,
		"primary",
	)
	if err != nil {
		t.Fatal(err)
	}

	entry, err := sub.QueueMessage(context.Background(), 42, "hello")
	if err != nil {
		t.Fatal(err)
	}
	if !entry.CreatedAt.Equal(now) {
		t.Fatalf("CreatedAt = %v, want %v", entry.CreatedAt, now)
	}
	if !entry.UpdatedAt.Equal(now) {
		t.Fatalf("UpdatedAt = %v, want %v", entry.UpdatedAt, now)
	}
}

// ---- QueueMessage validation ----

func TestQueueMessageRejectsZeroChatID(t *testing.T) {
	store := outbox.NewMemoryStore()
	sub, notifier := newSubmitter(t, store, "op-1")

	_, err := sub.QueueMessage(context.Background(), 0, "hello")
	if !errors.Is(err, ErrSubmitterInvalidChatID) {
		t.Fatalf("err = %v, want ErrSubmitterInvalidChatID", err)
	}
	if notifier.calls != 0 {
		t.Fatalf("notifier calls = %d, want 0", notifier.calls)
	}

	all, _ := store.ListAll(context.Background())
	if len(all) != 0 {
		t.Fatalf("store has %d entries, want 0", len(all))
	}
}

func TestQueueMessageRejectsBlankText(t *testing.T) {
	for _, text := range []string{"", " ", "\t", "\n", "  \t\n "} {
		store := outbox.NewMemoryStore()
		sub, notifier := newSubmitter(t, store, "op-1")

		_, err := sub.QueueMessage(context.Background(), 42, text)
		if !errors.Is(err, ErrSubmitterEmptyText) {
			t.Fatalf("text=%q: err = %v, want ErrSubmitterEmptyText",
				text, err)
		}
		if notifier.calls != 0 {
			t.Fatalf("text=%q: notifier called", text)
		}
	}
}

// ---- QueueMessage error propagation ----

func TestQueueMessagePropagatesIDGeneratorError(t *testing.T) {
	genErr := errors.New("id gen failed")
	store := outbox.NewMemoryStore()
	notifier := &recordingNotifier{}

	sub, err := NewOutboxMessageSubmitter(
		store,
		nil,
		func() (outbox.ID, error) { return "", genErr },
		notifier,
		"primary",
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = sub.QueueMessage(context.Background(), 42, "hello")
	if !errors.Is(err, genErr) {
		t.Fatalf("err = %v, want %v", err, genErr)
	}
	if notifier.calls != 0 {
		t.Fatalf("notifier calls = %d, want 0", notifier.calls)
	}

	all, _ := store.ListAll(context.Background())
	if len(all) != 0 {
		t.Fatalf("store has %d entries, want 0", len(all))
	}
}

func TestQueueMessagePropagatesStoreEnqueueError(t *testing.T) {
	store := outbox.NewMemoryStore()
	// Duplicate IDs so the second enqueue fails.
	sub, notifier := newSubmitter(t, store, "op-1", "op-1")

	if _, err := sub.QueueMessage(context.Background(), 42, "first"); err != nil {
		t.Fatalf("first: %v", err)
	}
	if notifier.calls != 1 {
		t.Fatalf("notifier calls = %d, want 1", notifier.calls)
	}

	_, err := sub.QueueMessage(context.Background(), 42, "second")
	if !errors.Is(err, outbox.ErrDuplicateID) {
		t.Fatalf("err = %v, want outbox.ErrDuplicateID", err)
	}
	if notifier.calls != 1 {
		t.Fatalf("notifier calls = %d, want 1 after failed enqueue",
			notifier.calls)
	}
}

func TestQueueMessageContextCancellationPreventsEnqueue(t *testing.T) {
	store := outbox.NewMemoryStore()
	sub, notifier := newSubmitter(t, store, "op-1")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := sub.QueueMessage(ctx, 42, "hello")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if notifier.calls != 0 {
		t.Fatalf("notifier calls = %d, want 0", notifier.calls)
	}

	all, _ := store.ListAll(context.Background())
	if len(all) != 0 {
		t.Fatalf("store has %d entries, want 0", len(all))
	}
}

// ---- QueueMessage notifier ----

func TestQueueMessageNotifiesAfterSuccessfulEnqueue(
	t *testing.T,
) {
	store := outbox.NewMemoryStore()

	// The test performs two successful enqueues, so the
	// deterministic generator must provide two distinct IDs.
	// Reusing the same ID would fail the second enqueue with
	// outbox.ErrDuplicateID and would not exercise the notifier
	// path.
	sub, notifier := newSubmitter(
		t,
		store,
		"op-1",
		"op-2",
	)

	first, err := sub.QueueMessage(
		context.Background(),
		42,
		"hello",
	)
	if err != nil {
		t.Fatalf(
			"first QueueMessage: %v",
			err,
		)
	}
	if first.ID != "op-1" {
		t.Fatalf(
			"first ID = %q, want op-1",
			first.ID,
		)
	}
	if notifier.calls != 1 {
		t.Fatalf(
			"notifier calls after first enqueue = %d, want 1",
			notifier.calls,
		)
	}

	second, err := sub.QueueMessage(
		context.Background(),
		42,
		"world",
	)
	if err != nil {
		t.Fatalf(
			"second QueueMessage: %v",
			err,
		)
	}
	if second.ID != "op-2" {
		t.Fatalf(
			"second ID = %q, want op-2",
			second.ID,
		)
	}
	if notifier.calls != 2 {
		t.Fatalf(
			"notifier calls after second enqueue = %d, want 2",
			notifier.calls,
		)
	}
}

// ---- CancelMessage ----

func TestCancelMessageUsesExpectedVersion(t *testing.T) {
	store := outbox.NewMemoryStore()
	sub, _ := newSubmitter(t, store, "op-1")

	entry, err := sub.QueueMessage(context.Background(), 42, "hello")
	if err != nil {
		t.Fatal(err)
	}

	// Stale version is rejected.
	if _, err := sub.CancelMessage(
		context.Background(),
		entry.ID,
		entry.Version+99,
	); !errors.Is(err, outbox.ErrVersionConflict) {
		t.Fatalf("err = %v, want outbox.ErrVersionConflict", err)
	}

	// Correct version cancels the entry.
	canceled, err := sub.CancelMessage(
		context.Background(),
		entry.ID,
		entry.Version,
	)
	if err != nil {
		t.Fatalf("CancelMessage: %v", err)
	}
	if canceled.State != outbox.StateCanceled {
		t.Fatalf("State = %s, want canceled", canceled.State)
	}
	if canceled.Version != entry.Version+1 {
		t.Fatalf("Version = %d, want %d",
			canceled.Version, entry.Version+1)
	}
}

func TestCancelMessageUnknownID(t *testing.T) {
	store := outbox.NewMemoryStore()
	sub, _ := newSubmitter(t, store, "op-1")

	_, err := sub.CancelMessage(context.Background(), "missing", 0)
	if !errors.Is(err, outbox.ErrNotFound) {
		t.Fatalf("err = %v, want outbox.ErrNotFound", err)
	}
}

func TestCancelMessageContextCancellation(t *testing.T) {
	store := outbox.NewMemoryStore()
	sub, _ := newSubmitter(t, store, "op-1")

	entry, err := sub.QueueMessage(context.Background(), 42, "hello")
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = sub.CancelMessage(ctx, entry.ID, entry.Version)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}

	stored, _ := store.Get(context.Background(), entry.ID)
	if stored.State != outbox.StateQueued {
		t.Fatalf("state = %s, want queued", stored.State)
	}
}

// ---- Interface assertion ----

func TestOutboxMessageSubmitterImplementsInterface(t *testing.T) {
	var _ MessageSubmitter = (*OutboxMessageSubmitter)(nil)
}

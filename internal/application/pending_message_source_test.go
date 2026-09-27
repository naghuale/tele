package application

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"telecli/internal/outbox"
	"telecli/internal/tui"
)

// The pending-message source: the one reader of the text of an outgoing
// message.
//
// Everything else in the delivery path is payload-free, and that is what
// these tests hold in place. A status, an error and a printed value must
// not carry what somebody wrote; the timeline must, and only the timeline.

type pendingSourceStore struct {
	entries []outbox.Entry
	err     error
}

func (s *pendingSourceStore) ListAll(context.Context) ([]outbox.Entry, error) {
	if s.err != nil {
		return nil, s.err
	}

	return s.entries, nil
}

var _ entryLister = (*pendingSourceStore)(nil)

func pendingEntry(
	id string,
	chatID int64,
	state outbox.State,
	text string,
) outbox.Entry {
	return outbox.Entry{
		ID:         outbox.ID(id),
		AccountKey: "account-1",
		ChatID:     chatID,
		Text:       text,
		State:      state,
		CreatedAt:  time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC),
	}
}

// The timeline gets the messages of its own chat, with the text the queue
// holds, in every state the queue can still be in.
func TestPendingSourceListsTheQueueOfOneChat(t *testing.T) {
	t.Parallel()

	created := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	store := &pendingSourceStore{entries: []outbox.Entry{
		pendingEntry("queued", 42, outbox.StateQueued, "первое"),
		pendingEntry("sending", 42, outbox.StateDispatching, "второе"),
		pendingEntry("retrying", 42, outbox.StateFailedRetryable, "третье"),
		pendingEntry("failed", 42, outbox.StateFailedPermanent, "четвёртое"),
		pendingEntry("uncertain", 42, outbox.StateUncertain, "пятое"),
		pendingEntry("canceled", 42, outbox.StateCanceled, "шестое"),
		pendingEntry("other-chat", 43, outbox.StateQueued, "чужое"),
		pendingEntry("other-account", 42, outbox.StateQueued, "чужое"),
	}}
	// The last entry belongs to another account: an entry of another
	// account must never be drawn in a conversation of this one, and the
	// account key is what tells them apart.
	store.entries[len(store.entries)-1].AccountKey = "account-2"

	source, err := NewOutboxPendingMessageSource(store, 0)
	if err != nil {
		t.Fatalf("NewOutboxPendingMessageSource: %v", err)
	}

	messages, err := source.ListPendingMessages(
		context.Background(),
		"account-1",
		42,
	)
	if err != nil {
		t.Fatalf("ListPendingMessages: %v", err)
	}

	if len(messages) != 6 {
		t.Fatalf("messages = %d, want 6: another chat and another account are not ours", len(messages))
	}
	for _, message := range messages {
		if message.Text == "" {
			t.Fatalf("message %q has no text", message.EntryID)
		}
		if !message.CreatedAt.Equal(created) {
			t.Fatalf("message %q lost its creation time", message.EntryID)
		}
	}
	if messages[0].State != MessageDeliveryQueued {
		t.Fatalf("state = %q, want queued", messages[0].State)
	}
	if messages[1].State != MessageDeliverySending {
		t.Fatalf("state = %q, want sending", messages[1].State)
	}
}

// An accepted entry is not pending: Telegram has the message, and the
// entry holds a temporary identifier that the history will not match.
func TestPendingSourceDoesNotListAcceptedEntries(t *testing.T) {
	t.Parallel()

	store := &pendingSourceStore{entries: []outbox.Entry{
		pendingEntry("queued", 42, outbox.StateQueued, "в очереди"),
		pendingEntry("accepted", 42, outbox.StateAccepted, "доставлено"),
	}}
	source, err := NewOutboxPendingMessageSource(store, 0)
	if err != nil {
		t.Fatalf("NewOutboxPendingMessageSource: %v", err)
	}

	messages, err := source.ListPendingMessages(context.Background(), "account-1", 42)
	if err != nil {
		t.Fatalf("ListPendingMessages: %v", err)
	}

	if len(messages) != 1 || messages[0].EntryID != "queued" {
		t.Fatalf("messages = %+v, want only the queued one", messages)
	}
}

// A state the projection does not know is a loud failure, not a message
// drawn as a state this build invented.
func TestPendingSourceRejectsAnUnknownState(t *testing.T) {
	t.Parallel()

	store := &pendingSourceStore{entries: []outbox.Entry{
		pendingEntry("weird", 42, outbox.State("teleported"), "текст"),
	}}
	source, err := NewOutboxPendingMessageSource(store, 0)
	if err != nil {
		t.Fatalf("NewOutboxPendingMessageSource: %v", err)
	}

	if _, err := source.ListPendingMessages(
		context.Background(),
		"account-1",
		42,
	); err == nil {
		t.Fatal("an unknown queue state was accepted")
	}
}

// A store is required: a source that reads nothing must say so rather than
// report an empty queue, which would look like "nothing is pending" on a
// screen that cannot know.
func TestPendingSourceRequiresAStore(t *testing.T) {
	t.Parallel()

	if _, err := NewOutboxPendingMessageSource(nil, 0); err == nil {
		t.Fatal("a source without a store was built")
	}
}

func TestPendingSourcePropagatesAStoreFailure(t *testing.T) {
	t.Parallel()

	boom := errors.New("store is closed")
	store := &pendingSourceStore{err: boom}
	source, err := NewOutboxPendingMessageSource(store, 0)
	if err != nil {
		t.Fatalf("NewOutboxPendingMessageSource: %v", err)
	}

	if _, err := source.ListPendingMessages(
		context.Background(),
		"account-1",
		42,
	); !errors.Is(err, boom) {
		t.Fatalf("error = %v, want the store failure", err)
	}
}

// A canceled context is not read through.
func TestPendingSourceHonorsACanceledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	source, err := NewOutboxPendingMessageSource(&pendingSourceStore{}, 0)
	if err != nil {
		t.Fatalf("NewOutboxPendingMessageSource: %v", err)
	}

	if _, err := source.ListPendingMessages(ctx, "account-1", 42); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

// The text does not leave this side of the boundary in a printed value.
func TestPendingMessagePrintsWithoutItsText(t *testing.T) {
	t.Parallel()

	const text = "секретный текст сообщения"
	message := PendingMessage{
		EntryID: "entry-1",
		ChatID:  42,
		Text:    text,
		State:   MessageDeliveryQueued,
	}

	for _, format := range []string{"%v", "%+v", "%#v", "%s"} {
		printed := fmt.Sprintf(format, message)
		if contains(printed, text) {
			t.Fatalf("%s printed the text: %q", format, printed)
		}
		if !contains(printed, "entry-1") {
			t.Fatalf("%s lost the entry: %q", format, printed)
		}
	}
}

// The status list is still payload-free after this change, and the two
// sources are not the same object: one has the text and one does not.
func TestTheStatusSourceStillHasNoText(t *testing.T) {
	t.Parallel()

	store := &pendingSourceStore{entries: []outbox.Entry{
		pendingEntry("entry-1", 42, outbox.StateQueued, "секретный текст"),
	}}
	statusReader := &statusOnlyStore{store: store}
	statuses, err := NewOutboxMessageStatusSource(statusReader, 0)
	if err != nil {
		t.Fatalf("NewOutboxMessageStatusSource: %v", err)
	}

	read, err := statuses.ListMessageStatuses(context.Background(), "account-1", 42)
	if err != nil {
		t.Fatalf("ListMessageStatuses: %v", err)
	}
	if len(read) != 1 {
		t.Fatalf("statuses = %d, want 1", len(read))
	}
	if contains(fmt.Sprintf("%+v", read[0]), "секретный") {
		t.Fatal("a status carries message text")
	}

	pending, err := NewOutboxPendingMessageSource(store, 0)
	if err != nil {
		t.Fatalf("NewOutboxPendingMessageSource: %v", err)
	}
	messages, err := pending.ListPendingMessages(context.Background(), "account-1", 42)
	if err != nil {
		t.Fatalf("ListPendingMessages: %v", err)
	}
	if len(messages) != 1 || messages[0].Text != "секретный текст" {
		t.Fatalf("the pending source lost the text: %+v", messages)
	}
}

// The adapter hands the TUI the text and nothing else it invented, and a
// nil application source stays nil so that direct delivery mode does not
// wait for messages that can never be pending.
func TestTheTUIAdapterMapsStatesAndKeepsText(t *testing.T) {
	t.Parallel()

	source := &stubPendingSource{messages: []PendingMessage{{
		EntryID:   "entry-1",
		ChatID:    42,
		Text:      "текст",
		State:     MessageDeliveryRetrying,
		Attempt:   3,
		CreatedAt: time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC),
	}}}

	adapter := newTUIPendingMessageSourceAdapter(source)
	messages, err := adapter.ListPendingMessages(context.Background(), "account-1", 42)
	if err != nil {
		t.Fatalf("ListPendingMessages: %v", err)
	}

	if len(messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(messages))
	}
	if messages[0].Text != "текст" {
		t.Fatalf("text = %q, want the text of the entry", messages[0].Text)
	}
	if messages[0].State != tui.MessageDeliveryRetrying {
		t.Fatalf("state = %q, want retrying", messages[0].State)
	}
	if messages[0].Attempt != 3 {
		t.Fatalf("attempt = %d, want 3", messages[0].Attempt)
	}

	if newTUIPendingMessageSourceAdapter(nil) != nil {
		t.Fatal("a nil application source must stay nil")
	}
}

// An application state the TUI has no name for is a loud failure.
func TestTheTUIAdapterRejectsAnUnknownState(t *testing.T) {
	t.Parallel()

	source := &stubPendingSource{messages: []PendingMessage{{
		EntryID: "entry-1",
		ChatID:  42,
		Text:    "текст",
		State:   MessageDeliveryState("teleported"),
	}}}

	_, err := newTUIPendingMessageSourceAdapter(source).
		ListPendingMessages(context.Background(), "account-1", 42)
	if err == nil {
		t.Fatal("an unknown application state reached the TUI")
	}
}

// stubPendingSource answers with what a test put in it.
type stubPendingSource struct {
	messages []PendingMessage
	err      error
}

func (s *stubPendingSource) ListPendingMessages(
	context.Context,
	string,
	int64,
) ([]PendingMessage, error) {
	if s.err != nil {
		return nil, s.err
	}

	return s.messages, nil
}

// statusOnlyStore exposes only the payload-free half of a store, so a test
// can prove that the status reader cannot reach the text.
type statusOnlyStore struct {
	store *pendingSourceStore
}

func (s *statusOnlyStore) ListEntryStatuses(
	_ context.Context,
	query outbox.ListEntryStatusesQuery,
) ([]outbox.EntryStatus, error) {
	entries, err := s.store.ListAll(context.Background())
	if err != nil {
		return nil, err
	}

	statuses := make([]outbox.EntryStatus, 0, len(entries))
	for _, entry := range entries {
		if entry.AccountKey != query.AccountKey || entry.ChatID != query.ChatID {
			continue
		}
		statuses = append(statuses, outbox.EntryStatus{
			ID:         string(entry.ID),
			AccountKey: entry.AccountKey,
			ChatID:     entry.ChatID,
			State:      entry.State,
			Attempt:    entry.AttemptCount,
		})
	}

	return statuses, nil
}

func contains(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) &&
		indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	for index := 0; index+len(needle) <= len(haystack); index++ {
		if haystack[index:index+len(needle)] == needle {
			return index
		}
	}

	return -1
}

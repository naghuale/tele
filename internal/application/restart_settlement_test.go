package application

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"telecli/internal/outbox"
	"telecli/internal/telegram"
)

// Records an earlier process left accepted, settled by a later one.
//
// The claim under test is the one the interface makes to a user: a
// message that is not on its way out any more says so. Two records, two
// outcomes, and neither of them is accepted afterwards.

func newSettlementPath(t *testing.T) *settlementPath {
	t.Helper()

	dataDir := filepath.Join(t.TempDir(), "outbox")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatalf("create data dir: %v", err)
	}
	if err := os.Chmod(dataDir, 0o700); err != nil {
		t.Fatalf("chmod data dir: %v", err)
	}

	opened, err := outbox.Open(
		context.Background(),
		outbox.Config{
			DataDir:    dataDir,
			DatabaseID: "settlement",
			InstanceID: "settlement",
			Dispatcher: outbox.DefaultDispatcherConfig("settlement"),
		},
		outbox.Deps{
			KeyProvider: newH7dKeyProvider(),
			Sender:      NewTelegramOutboxSender(&heldSender{}),
			Clock:       outbox.SystemClock{},
		},
	)
	if err != nil {
		t.Fatalf("open outbox: %v", err)
	}
	t.Cleanup(func() { _ = opened.Close() })

	return &settlementPath{t: t, store: opened, data: dataDir}
}

type settlementPath struct {
	t          *testing.T
	store      *outbox.Outbox
	data       string
	duplicates int
}

// leaveAccepted records a message as a previous process would have left
// it: TDLib took it, the queue wrote the acceptance, and nothing ever
// confirmed it.
func (p *settlementPath) leaveAccepted(
	text string,
	at time.Time,
) outbox.Entry {
	p.t.Helper()

	id := text
	if id == "да" {
		// Two records of the same words are two records, so the ids
		// have to differ: the text is not an identity.
		p.duplicates++
		id = fmt.Sprintf("да-%d", p.duplicates)
	}
	return p.leaveAcceptedAs(id, text, at)
}

func (p *settlementPath) leaveAcceptedAs(
	id, text string,
	at time.Time,
) outbox.Entry {
	return p.leaveAcceptedIn(id, text, settlementChat, at)
}

// leaveAcceptedIn is leaveAcceptedAs for a named chat, because the shapes
// this file is about are the ones a real account produces: a chat with
// oneself and a private chat are not the same chat id.
func (p *settlementPath) leaveAcceptedIn(
	id, text string,
	chatID int64,
	at time.Time,
) outbox.Entry {
	p.t.Helper()

	entry := outbox.Entry{
		ID:         outbox.ID(id),
		AccountKey: settlementAccountKey,
		ChatID:     chatID,
		Text:       text,
		State:      outbox.StateQueued,
		CreatedAt:  at,
		UpdatedAt:  at,
	}
	if err := p.store.Store.Enqueue(context.Background(), entry); err != nil {
		p.t.Fatalf("enqueue %q: %v", text, err)
	}

	claimed, err := p.store.Store.Claim(
		context.Background(), entry.ID, 0, "earlier-process",
		at.Add(30*time.Second), at,
	)
	if err != nil {
		p.t.Fatalf("claim %q: %v", text, err)
	}
	accepted, err := p.store.Store.MarkAccepted(
		context.Background(), claimed.ID, claimed.Version,
		-1000000000, at,
	)
	if err != nil {
		p.t.Fatalf("mark accepted %q: %v", text, err)
	}
	return accepted
}

const (
	settlementAccountKey = "settlement-account"
	settlementChat       = int64(31)
)

// scriptHistory is a chat holding the messages it is given.
type scriptHistory struct {
	pages   map[int64]telegram.HistoryPage
	failFor map[int64]bool
	asked   []int64
}

func (h *scriptHistory) GetChatHistory(
	_ context.Context,
	chatID telegram.ChatID,
	_ telegram.MessageID,
	_ int,
) (telegram.HistoryPage, error) {
	h.asked = append(h.asked, int64(chatID))
	if h.failFor[int64(chatID)] {
		return telegram.HistoryPage{}, os.ErrDeadlineExceeded
	}
	page, ok := h.pages[int64(chatID)]
	if !ok {
		return telegram.HistoryPage{}, nil
	}
	return page, nil
}

func (p *settlementPath) settler(history TelegramHistoryReader) *restartSettler {
	store, ok := p.store.Store.(outbox.UnsettledAcceptedStore)
	if !ok {
		p.t.Fatal("the store cannot list unsettled accepted records")
	}
	return &restartSettler{
		store:      store,
		entries:    p.store.Store,
		history:    history,
		accountKey: settlementAccountKey,
		clock:      outbox.SystemClock{},
	}
}

func (p *settlementPath) state(id string) outbox.State {
	p.t.Helper()

	entry, err := p.store.Store.Get(context.Background(), outbox.ID(id))
	if err != nil {
		p.t.Fatalf("get %s: %v", id, err)
	}
	return entry.State
}

// A record whose message is in the chat becomes sent, with the
// identifier the history will come back with.
func TestAnEarlierRecordWhoseMessageIsInTheChatBecomesSent(t *testing.T) {
	t.Parallel()

	path := newSettlementPath(t)
	at := time.Now().Add(-3 * time.Hour).Truncate(time.Second)

	accepted := path.leaveAccepted("сообщение ушло", at)

	history := &scriptHistory{pages: map[int64]telegram.HistoryPage{
		settlementChat: {
			Messages: []telegram.Message{{
				ID:        4242,
				ChatID:    telegram.ChatID(settlementChat),
				Outgoing:  true,
				Timestamp: at.Add(2 * time.Second),
				Text:      "сообщение ушло",
			}},
		},
	}}

	counts, err := path.settler(history).Settle(context.Background())
	if err != nil {
		t.Fatalf("settle: %v", err)
	}

	if counts.considered != 1 || counts.sent != 1 || counts.uncertain != 0 {
		t.Fatalf("counts = %+v, want one record sent", counts)
	}
	if got := path.state("сообщение ушло"); got != outbox.StateSent {
		t.Fatalf("state = %q, want sent", got)
	}

	entry, err := path.store.Store.Get(
		context.Background(), accepted.ID,
	)
	if err != nil {
		t.Fatalf("get entry: %v", err)
	}
	if entry.TelegramMessageID != 4242 {
		t.Fatalf(
			"message id = %d, want the final id 4242",
			entry.TelegramMessageID,
		)
	}
	if entry.SentAt.IsZero() {
		t.Fatal("a sent record with no sent time")
	}
}

// A record whose message is nowhere to be found becomes uncertain.
//
// This is the case the owner hit: the process was gone when Telegram
// confirmed, so the record has no future event, and leaving it accepted
// draws "on its way out" for as long as the program runs.
func TestAnEarlierRecordWhoseMessageCannotBeFoundBecomesUncertain(
	t *testing.T,
) {
	t.Parallel()

	path := newSettlementPath(t)
	at := time.Now().Add(-3 * time.Hour).Truncate(time.Second)

	path.leaveAccepted("сообщение ушло", at)

	// The chat holds other things, and nothing with this text.
	history := &scriptHistory{pages: map[int64]telegram.HistoryPage{
		settlementChat: {
			Messages: []telegram.Message{{
				ID:        1,
				ChatID:    telegram.ChatID(settlementChat),
				Outgoing:  true,
				Timestamp: at.Add(-time.Hour),
				Text:      "что-то другое",
			}},
		},
	}}

	counts, err := path.settler(history).Settle(context.Background())
	if err != nil {
		t.Fatalf("settle: %v", err)
	}

	if counts.considered != 1 || counts.uncertain != 1 || counts.sent != 0 {
		t.Fatalf("counts = %+v, want one record uncertain", counts)
	}
	if got := path.state("сообщение ушло"); got != outbox.StateUncertain {
		t.Fatalf("state = %q, want uncertain", got)
	}
}

// The same words twice in a row are two messages and two records, and
// each record must end up with its own message.
func TestTwoRecordsOfTheSameWordsKeepTheirOwnMessages(t *testing.T) {
	t.Parallel()

	path := newSettlementPath(t)
	base := time.Now().Add(-2 * time.Hour).Truncate(time.Second)

	first := path.leaveAcceptedAs("первое", "да", base)
	second := path.leaveAcceptedAs("второе", "да", base.Add(3*time.Second))

	history := &scriptHistory{pages: map[int64]telegram.HistoryPage{
		settlementChat: {
			Messages: []telegram.Message{
				{
					ID:        90,
					ChatID:    telegram.ChatID(settlementChat),
					Outgoing:  true,
					Timestamp: base.Add(3 * time.Second),
					Text:      "да",
				},
				{
					ID:        80,
					ChatID:    telegram.ChatID(settlementChat),
					Outgoing:  true,
					Timestamp: base,
					Text:      "да",
				},
			},
		},
	}}

	counts, err := path.settler(history).Settle(context.Background())
	if err != nil {
		t.Fatalf("settle: %v", err)
	}
	if counts.sent != 2 {
		t.Fatalf("counts = %+v, want both records sent", counts)
	}

	got := map[string]int64{}
	for _, id := range []outbox.ID{first.ID, second.ID} {
		entry, err := path.store.Store.Get(context.Background(), id)
		if err != nil {
			t.Fatalf("get %s: %v", id, err)
		}
		if entry.State != outbox.StateSent {
			t.Fatalf("entry %s is %q, want sent", id, entry.State)
		}
		got[string(id)] = entry.TelegramMessageID
	}
	// Each record took the message nearest its own acceptance, so the
	// older one has the older id.
	if got["первое"] != 80 {
		t.Fatalf(
			"the older record took message %d, want 80", got["первое"],
		)
	}
	if got["второе"] != 90 {
		t.Fatalf(
			"the newer record took message %d, want 90", got["второе"],
		)
	}
}

// A chat that cannot be read leaves its records alone.
//
// A history that could not be fetched is not evidence that a message is
// missing, and calling a message uncertain because Telegram was slow to
// answer would be the exact lie this change exists to remove.
func TestAChatThatCannotBeReadLeavesItsRecordsAccepted(t *testing.T) {
	t.Parallel()

	path := newSettlementPath(t)
	at := time.Now().Add(-time.Hour).Truncate(time.Second)

	path.leaveAccepted("сообщение ушло", at)

	history := &scriptHistory{
		pages:   map[int64]telegram.HistoryPage{},
		failFor: map[int64]bool{settlementChat: true},
	}

	counts, err := path.settler(history).Settle(context.Background())
	if err != nil {
		t.Fatalf("settle: %v", err)
	}
	if counts.unreadable != 1 || counts.uncertain != 0 {
		t.Fatalf("counts = %+v, want one unreadable record", counts)
	}
	if got := path.state("сообщение ушло"); got != outbox.StateAccepted {
		t.Fatalf("state = %q, want accepted: the chat could not be read", got)
	}
}

// A message with the right words but from another time is not this
// record's message.
func TestAMessageOutsideTheTimeWindowDoesNotMatch(t *testing.T) {
	t.Parallel()

	path := newSettlementPath(t)
	at := time.Now().Add(-time.Hour).Truncate(time.Second)

	path.leaveAccepted("сообщение ушло", at)

	history := &scriptHistory{pages: map[int64]telegram.HistoryPage{
		settlementChat: {
			Messages: []telegram.Message{{
				ID:        777,
				ChatID:    telegram.ChatID(settlementChat),
				Outgoing:  true,
				Timestamp: at.Add(2 * time.Hour),
				Text:      "сообщение ушло",
			}},
		},
	}}

	counts, err := path.settler(history).Settle(context.Background())
	if err != nil {
		t.Fatalf("settle: %v", err)
	}
	if counts.sent != 0 || counts.uncertain != 1 {
		t.Fatalf("counts = %+v, want the record uncertain", counts)
	}
}

// A message the user received is not this queue's message.
func TestAnIncomingMessageNeverMatches(t *testing.T) {
	t.Parallel()

	path := newSettlementPath(t)
	at := time.Now().Add(-time.Hour).Truncate(time.Second)

	path.leaveAccepted("сообщение ушло", at)

	history := &scriptHistory{pages: map[int64]telegram.HistoryPage{
		settlementChat: {
			Messages: []telegram.Message{{
				ID:        555,
				ChatID:    telegram.ChatID(settlementChat),
				Outgoing:  false,
				Timestamp: at,
				Text:      "сообщение ушло",
			}},
		},
	}}

	counts, err := path.settler(history).Settle(context.Background())
	if err != nil {
		t.Fatalf("settle: %v", err)
	}
	if counts.sent != 0 || counts.uncertain != 1 {
		t.Fatalf("counts = %+v, want the record uncertain", counts)
	}
}

// A queue with nothing left over settles nothing and reads no chat.
func TestSettlementOfACleanQueueReadsNothing(t *testing.T) {
	t.Parallel()

	path := newSettlementPath(t)
	history := &scriptHistory{pages: map[int64]telegram.HistoryPage{}}

	counts, err := path.settler(history).Settle(context.Background())
	if err != nil {
		t.Fatalf("settle: %v", err)
	}
	if counts.considered != 0 {
		t.Fatalf("considered = %d, want 0", counts.considered)
	}
	if len(history.asked) != 0 {
		t.Fatalf("read %d chats of a queue with nothing to settle", len(history.asked))
	}
}

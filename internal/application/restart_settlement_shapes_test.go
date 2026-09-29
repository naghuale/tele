package application

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"telecli/internal/outbox"
	"telecli/internal/telegram"
)

// The settlement, against what the store and TDLib really hold.
//
// The synthetic version of this test passed and the owner's account matched
// none of thirteen. So everything here is a real shape: the record is
// written through the real store, so its payload is genuinely encrypted
// and its acceptance time is a real Unix nanosecond count, and the page is
// a recorded getChatHistory answer read by the real decoder — the same one
// every page the interface draws goes through.
//
// recordedSavedMessagesPage is a getChatHistory answer for Saved Messages,
// in the shape TDLib writes: messages newest first, is_outgoing set on
// the ones this account sent, the date in whole Unix seconds, and the
// text under messageText/formattedText.
const recordedSavedMessagesPage = `{
  "@type": "messages",
  "total_count": 4,
  "messages": [
    {
      "@type": "message",
      "id": 100000000114,
      "sender_id": {"@type": "messageSenderUser", "user_id": 1000100},
      "chat_id": 1000100,
      "is_outgoing": true,
      "date": 1000000114,
      "content": {
        "@type": "messageText",
        "text": {"@type": "formattedText", "text": "вечерняя сводка", "entities": []}
      }
    },
    {
      "@type": "message",
      "id": 100000000107,
      "sender_id": {"@type": "messageSenderUser", "user_id": 1000100},
      "chat_id": 1000100,
      "is_outgoing": true,
      "date": 1000000107,
      "content": {
        "@type": "messageText",
        "text": {"@type": "formattedText", "text": "проверяю сборку", "entities": []}
      }
    },
    {
      "@type": "message",
      "id": 100000000100,
      "sender_id": {"@type": "messageSenderUser", "user_id": 1000100},
      "chat_id": 1000100,
      "is_outgoing": true,
      "date": 1000000100,
      "content": {
        "@type": "messageText",
        "text": {"@type": "formattedText", "text": "утренняя задача", "entities": []}
      }
    }
  ]
}`

// recordedPage serves a recorded answer through the real decoder.
type recordedPage struct {
	chatID telegram.ChatID
	raw    string
	calls  int
}

func (r *recordedPage) GetChatHistory(
	_ context.Context,
	chatID telegram.ChatID,
	_ telegram.MessageID,
	_ int,
) (telegram.HistoryPage, error) {
	r.calls++
	if chatID != r.chatID {
		return telegram.HistoryPage{}, nil
	}

	return telegram.DecodeHistoryPage(telegram.RawMessage(r.raw), chatID)
}

// A record the store really wrote is found in a page TDLib really sent.
func TestARecordFromARealStoreIsFoundInARecordedPage(t *testing.T) {
	const (
		chatID   = int64(1000100)
		finalID  = int64(100000000107)
		wantText = "проверяю сборку"
	)

	path := newSettlementPath(t)

	// The acceptance time is the moment TDLib took the message, which is
	// the moment the message is dated: 1000000107 in whole seconds. The
	// store keeps nanoseconds, and the queue stamped it from a real clock
	// a moment after the second began.
	accepted := time.Unix(1000000107, 0).UTC()
	path.leaveAcceptedIn("record-1", wantText, chatID, accepted)

	history := &recordedPage{
		chatID: telegram.ChatID(chatID),
		raw:    recordedSavedMessagesPage,
	}

	counts, err := path.settler(history).Settle(context.Background())
	if err != nil {
		t.Fatalf("settle: %v", err)
	}

	if counts.sent != 1 {
		t.Fatalf(
			"counts = %+v, want the record sent: the message is in the "+
				"page, the text is the text, and the owner reports "+
				"seeing it as sent in Telegram", counts,
		)
	}
	if counts.uncertain != 0 {
		t.Fatalf("counts = %+v, want nothing uncertain", counts)
	}

	entry, err := path.store.Store.Get(context.Background(), "record-1")
	if err != nil {
		t.Fatalf("get entry: %v", err)
	}
	if entry.State != outbox.StateSent {
		t.Fatalf("state = %q, want sent", entry.State)
	}
	if entry.TelegramMessageID != finalID {
		t.Fatalf(
			"message id = %d, want %d: it is the one the page carries "+
				"for this text, and not the newest message of the chat",
			entry.TelegramMessageID, finalID,
		)
	}

}

// The same record, a day older, is still found: the window is about the
// acceptance and the page reaches back.
func TestARecordOlderThanTheNewestMessagesIsStillFound(t *testing.T) {
	const (
		chatID   = int64(1000100)
		finalID  = int64(100000000100)
		wantText = "утренняя задача"
	)

	path := newSettlementPath(t)
	path.leaveAcceptedIn(
		"record-old", wantText, chatID,
		time.Unix(1000000100, 0).UTC(),
	)

	history := &recordedPage{
		chatID: telegram.ChatID(chatID),
		raw:    recordedSavedMessagesPage,
	}

	counts, err := path.settler(history).Settle(context.Background())
	if err != nil {
		t.Fatalf("settle: %v", err)
	}
	if counts.sent != 1 || counts.uncertain != 0 {
		t.Fatalf("counts = %+v, want the record sent", counts)
	}

	entry, err := path.store.Store.Get(context.Background(), "record-old")
	if err != nil {
		t.Fatalf("get entry: %v", err)
	}
	if entry.TelegramMessageID != finalID {
		t.Fatalf("message id = %d, want %d", entry.TelegramMessageID, finalID)
	}
}

// An entry the store holds must be readable with its text, or there is
// nothing to match against the chat.
func TestTheStoreGivesTheSettlementTheTextItStored(t *testing.T) {
	const (
		chatID   = int64(1000100)
		wantText = "проверяю сборку"
	)

	path := newSettlementPath(t)
	path.leaveAcceptedIn(
		"record-1", wantText, chatID, time.Unix(1000000107, 0).UTC(),
	)

	unsettled, err := path.store.Store.(outbox.UnsettledAcceptedStore).
		ListUnsettledAccepted(context.Background(), settlementAccountKey, 0)
	if err != nil {
		t.Fatalf("ListUnsettledAccepted: %v", err)
	}
	if len(unsettled) != 1 {
		t.Fatalf("unsettled = %#v, want one", unsettled)
	}
	if unsettled[0].Text != wantText {
		t.Fatalf(
			"text = %q, want %q: the payload is encrypted in the store "+
				"and this is the only place it is read back",
			unsettled[0].Text, wantText,
		)
	}
	if !unsettled[0].AcceptedAt.Equal(time.Unix(1000000107, 0).UTC()) {
		t.Fatalf(
			"accepted at %s, want the Unix second the message is dated",
			unsettled[0].AcceptedAt,
		)
	}
}

// A record this build already marked uncertain is looked at again, and
// becomes sent when the fixed lookup finds it.
//
// The settlement is allowed to be wrong once. It marked thirteen records
// uncertain on the owner's account because the lookup was wrong, and
// "uncertain" asks the user to go and look in Telegram by hand — which is
// exactly the work the program is supposed to be doing. A record the
// program itself decided it could not resolve is the one record it is
// obliged to try again.
func TestARecordThisBuildMarkedUncertainIsCheckedAgain(t *testing.T) {
	const (
		chatID   = int64(1000100)
		finalID  = int64(100000000107)
		wantText = "проверяю сборку"
	)

	path := newSettlementPath(t)
	at := time.Unix(1000000107, 0).UTC()
	path.leaveAcceptedIn("record-1", wantText, chatID, at)

	// The first run, with a lookup that cannot see the page.
	counts, err := path.settler(&emptyHistory{}).Settle(context.Background())
	if err != nil {
		t.Fatalf("first settle: %v", err)
	}
	if counts.uncertain != 1 {
		t.Fatalf("counts = %+v, want one uncertain", counts)
	}
	marked, err := path.store.Store.Get(context.Background(), "record-1")
	if err != nil {
		t.Fatalf("get entry: %v", err)
	}
	if marked.State != outbox.StateUncertain {
		t.Fatalf("state = %q, want uncertain", marked.State)
	}

	// The second run, with the lookup fixed.
	history := &recordedPage{
		chatID: telegram.ChatID(chatID),
		raw:    recordedSavedMessagesPage,
	}
	counts, err = path.settler(history).Settle(context.Background())
	if err != nil {
		t.Fatalf("second settle: %v", err)
	}
	if counts.sent != 1 {
		t.Fatalf("counts = %+v, want the record sent on the second run", counts)
	}

	entry, err := path.store.Store.Get(context.Background(), "record-1")
	if err != nil {
		t.Fatalf("get entry: %v", err)
	}
	if entry.State != outbox.StateSent {
		t.Fatalf("state = %q, want sent", entry.State)
	}
	if entry.TelegramMessageID != finalID {
		t.Fatalf("message id = %d, want %d", entry.TelegramMessageID, finalID)
	}
}

// emptyHistory is a chat that answers with nothing, which is what a lookup
// that cannot see the page looks like.
type emptyHistory struct{}

func (emptyHistory) GetChatHistory(
	context.Context, telegram.ChatID, telegram.MessageID, int,
) (telegram.HistoryPage, error) {
	return telegram.HistoryPage{}, nil
}

// A record older than the first page is found by reading further back.
//
// This is the owner's thirteen. Every one of those messages was in the
// chat and showing as sent; none was in the newest hundred messages the
// settlement read, and a lookup that gives up at the end of one page
// reports "not found" for all of them and marks them uncertain, which
// asks the user to go and look in Telegram by hand.
func TestARecordBeyondTheFirstPageIsFoundByReadingFurtherBack(t *testing.T) {
	const (
		chatID   = int64(1000100)
		accepted = int64(1789400000)
		wantID   = int64(201)
	)

	path := newSettlementPath(t)

	// One record, and it is the oldest message of the chat.
	path.leaveAcceptedIn(
		"record-deep", "давняя запись", chatID,
		time.Unix(accepted, 0).UTC(),
	)

	// The first page is a hundred newer messages that say something else.
	// The second is the next hundred back, and the message this queue sent
	// is the oldest of them — dated at the moment TDLib took it.
	history := &pagedHistory{
		chatID: telegram.ChatID(chatID),
		pages: map[int64]string{
			// The first page runs from id 101 down to 2, so its
			// boundary — the message the next page starts from — is 2.
			0: historyPage(1, 100, "свежее", 1789600000),
			2: historyPage(101, 100, "давняя запись", accepted),
		},
		perPage: 100,
	}

	counts, err := path.settler(history).Settle(context.Background())
	if err != nil {
		t.Fatalf("settle: %v", err)
	}
	if counts.sent != 1 {
		t.Fatalf(
			"counts = %+v, calls = %d, want the record sent: it is on "+
				"the second page", counts, history.calls,
		)
	}

	entry, err := path.store.Store.Get(context.Background(), "record-deep")
	if err != nil {
		t.Fatalf("get entry: %v", err)
	}
	if entry.TelegramMessageID != wantID {
		t.Fatalf("message id = %d, want %d", entry.TelegramMessageID, wantID)
	}
	if history.calls != 2 {
		t.Fatalf(
			"the chat was read %d times, want 2: the first page does not "+
				"hold the record and the second does", history.calls,
		)
	}
}

// A chat that runs out before every record is found is still settled, and
// the page budget is what stops the reading.
func TestTheSettlementStopsAtItsPageBudget(t *testing.T) {
	const chatID = int64(1000100)

	path := newSettlementPath(t)
	path.leaveAcceptedIn(
		"record-never", "не найдётся", chatID,
		time.Unix(1789400000, 0).UTC(),
	)

	// A chat that always answers with a full page of nothing that
	// matches, forever.
	history := &pagedHistory{
		chatID:   telegram.ChatID(chatID),
		pages:    map[int64]string{},
		infinite: true,
		perPage:  100,
	}

	counts, err := path.settler(history).Settle(context.Background())
	if err != nil {
		t.Fatalf("settle: %v", err)
	}
	if counts.uncertain != 1 || counts.unreadable != 0 {
		t.Fatalf("counts = %+v, want one uncertain", counts)
	}
	if history.calls != settlementPageBudget {
		t.Fatalf(
			"the chat was read %d times, want the budget of %d: a busy "+
				"chat is not read to its beginning on every start",
			history.calls, settlementPageBudget,
		)
	}
}

// One request when the first page holds the record, which is the common
// case: a queue closed a moment ago.
func TestOneRequestWhenTheFirstPageHoldsTheRecord(t *testing.T) {
	const chatID = int64(1000100)

	path := newSettlementPath(t)
	path.leaveAcceptedIn(
		"record-1", "проверяю сборку", chatID,
		time.Unix(1000000107, 0).UTC(),
	)

	history := &pagedHistory{
		chatID:  telegram.ChatID(chatID),
		pages:   map[int64]string{0: recordedSavedMessagesPage},
		perPage: 100,
	}

	counts, err := path.settler(history).Settle(context.Background())
	if err != nil {
		t.Fatalf("settle: %v", err)
	}
	if counts.sent != 1 {
		t.Fatalf("counts = %+v, want the record sent", counts)
	}
	if history.calls != 1 {
		t.Fatalf(
			"the chat was read %d times, want 1: the record was in the "+
				"first page", history.calls,
		)
	}
}

// pagedHistory serves recorded pages by the boundary asked for.
type pagedHistory struct {
	chatID  telegram.ChatID
	pages   map[int64]string
	calls   int
	perPage int

	// infinite answers every boundary with a full page that holds nothing
	// the settlement wants, which is what a chat of small talk is.
	infinite bool

	// dateBase is the Unix second the oldest message of a synthesised page
	// is dated at.
	dateBase int64
}

func (h *pagedHistory) GetChatHistory(
	_ context.Context,
	chatID telegram.ChatID,
	from telegram.MessageID,
	_ int,
) (telegram.HistoryPage, error) {
	h.calls++
	if chatID != h.chatID {
		return telegram.HistoryPage{}, nil
	}

	raw, ok := h.pages[int64(from)]
	if !ok {
		if !h.infinite {
			return telegram.HistoryPage{}, nil
		}
		// A full page of messages that match nothing, continuing back.
		raw = historyPage(
			int64(from)+1, int64(h.perPage), "просто болтовня",
			int64(h.dateBase),
		)
	}

	return telegram.DecodeHistoryPage(telegram.RawMessage(raw), chatID)
}

// historyPage builds a recorded getChatHistory answer: count messages
// newest first, all with the same text, the oldest dated at dateBase and
// each newer one a second later — which is the order a real page is in and
// the order a page is paged back through.
func historyPage(
	firstID, count int64,
	text string,
	dateBase int64,
) string {
	messages := make([]string, 0, count)
	for i := int64(0); i < count; i++ {
		messages = append(messages, fmt.Sprintf(`{
          "@type": "message",
          "id": %d,
          "sender_id": {"@type": "messageSenderUser", "user_id": 1000100},
          "chat_id": 1000100,
          "is_outgoing": true,
          "date": %d,
          "content": {
            "@type": "messageText",
            "text": {"@type": "formattedText", "text": %q, "entities": []}
          }
        }`, firstID+count-i, dateBase+i, text))
	}

	// The oldest message of the page is the last one, and it is the
	// boundary the next page is asked from.
	return fmt.Sprintf(
		`{"@type":"messages","total_count":%d,"messages":[%s]}`,
		count, strings.Join(messages, ","),
	)
}

// The time window is on the instant, not on the calendar.
//
// The owner is ten hours from UTC and asked whether the two moments are
// compared in the same zone, which is a fair question and worth an answer
// that is a test rather than a claim: the store keeps Unix nanoseconds and
// TDLib dates a message in whole Unix seconds, and both are read as UTC,
// so the difference between them is a duration and no zone is involved.
//
// The record here is accepted at a moment whose *local* date and *UTC*
// date are different days, and the message is dated the same second.
func TestTheWindowIsOnTheInstantAndNotOnTheCalendar(t *testing.T) {
	const chatID = int64(1000100)

	// The moment the page dates the message, as the owner would have seen
	// it on their own clock: 04:20 on the 17th in UTC+10, where it is
	// still the 16th in UTC. A program that compared calendar days would
	// be looking for a message from a different day than the one Telegram
	// dated.
	zone := time.FixedZone("UTC+10", 10*60*60)
	instant := time.Unix(1000000107, 0)
	accepted := instant.In(zone)
	if accepted.Day() == instant.UTC().Day() {
		t.Fatalf(
			"the moment is the same calendar day in both zones (%s), so "+
				"this proves nothing", accepted,
		)
	}

	path := newSettlementPath(t)
	path.leaveAcceptedIn(
		"record-tz", "проверяю сборку", chatID, accepted,
	)

	// The page dates the message at the same instant, in UTC.
	history := &recordedPage{
		chatID: telegram.ChatID(chatID),
		raw:    recordedSavedMessagesPage,
	}

	counts, err := path.settler(history).Settle(context.Background())
	if err != nil {
		t.Fatalf("settle: %v", err)
	}
	if counts.sent != 1 {
		t.Fatalf(
			"counts = %+v, want the record sent: the message is dated "+
				"the same instant, and two moments are compared as a "+
				"duration rather than as calendar days", counts,
		)
	}
}

// A message the user received is never this queue's message, and that is
// asked from the recorded page rather than from a value built by hand.
func TestAnIncomingMessageInARecordedPageIsNotMatched(t *testing.T) {
	const chatID = int64(1000100)

	path := newSettlementPath(t)
	path.leaveAcceptedIn(
		"record-1", "проверяю сборку", chatID,
		time.Unix(1000000107, 0).UTC(),
	)

	// One message of the same words, from the other side.
	history := &recordedPage{
		chatID: telegram.ChatID(chatID),
		raw: `{
          "@type": "messages",
          "total_count": 1,
          "messages": [
            {
              "@type": "message",
              "id": 100000000107,
              "sender_id": {"@type": "messageSenderUser", "user_id": 99},
              "chat_id": 1000100,
              "is_outgoing": false,
              "date": 1000000107,
              "content": {
                "@type": "messageText",
                "text": {
                  "@type": "formattedText",
                  "text": "проверяю сборку",
                  "entities": []
                }
              }
            }
          ]
        }`,
	}

	counts, err := path.settler(history).Settle(context.Background())
	if err != nil {
		t.Fatalf("settle: %v", err)
	}
	if counts.sent != 0 || counts.uncertain != 1 {
		t.Fatalf(
			"counts = %+v, want the record uncertain: a message the "+
				"account received is not one this queue sent", counts,
		)
	}
}

package tui

import (
	"strings"
	"testing"
	"time"

	"telecli/internal/tui/theme"
)

// Yesterday's uncertain rows belong to yesterday.
//
// The owner's screen had them at the bottom: the queue's own records from
// the evening before were drawn under today's sent messages and took the
// foot of the chat, and the conversation was pushed up and out of it. The
// rows were not wrong about anything — an uncertain message is drawn
// wherever it is in the conversation — they were in the wrong place, and
// the place was decided by where the row came from rather than by when the
// message was written.
//
// TUI_SPEC does not put them at the end. It says pending messages are part
// of the feed — the same column, the same scroll, the same heights — and
// that "the queue does not displace history". What it assumed, in the
// comment that introduced this arrangement, was that a message that has
// not gone out is newer than every message of the history. That is true of
// a message this session wrote. It is false of a record an earlier run left
// behind, which is exactly what "Delivery uncertain" rows are: they are the
// messages a queue could not confirm, and the queue did not write them
// yesterday morning.
func TestARowOlderThanTheFeedIsDrawnAtItsOwnTime(t *testing.T) {
	t.Parallel()

	// Two days of conversation, the way the owner's account had it: the
	// newest page full of today, with yesterday's evening underneath.
	// Fixed moments, so the order the feed is in is a fact and not
	// something the machine's clock decided. Yesterday's evening is where
	// the owner's two uncertain rows were written.
	yesterday := time.Date(2026, time.September, 28, 0, 0, 0, 0, time.UTC)
	at := func(hour, minute int) time.Time {
		return yesterday.Add(
			time.Duration(hour)*time.Hour + time.Duration(minute)*time.Minute,
		)
	}

	// Newest first, the way TDLib answers: the model reverses the page
	// where it arrives.
	page := HistoryPage{Messages: []Message{
		{
			ID: 14, Outgoing: true, Text: "сегодня: второе",
			At: at(24+9, 2),
		},
		{
			ID: 13, Outgoing: true, Text: "сегодня: первое",
			At: at(24+9, 1),
		},
		{
			ID: 12, Author: "Anna Example", Text: "вчера: 21:35",
			At: at(21, 35),
		},
		{
			ID: 11, Author: "Anna Example", Text: "вчера: 21:22",
			At: at(21, 22),
		},
		{
			ID: 10, Author: "Anna Example", Text: "вчера: 09:00",
			At: at(9, 0),
		},
	}}

	// The screen is tall enough for the whole conversation, because the
	// assertion below is about where the rows are drawn and a row that is
	// off the top of the window is not drawn anywhere: with the two rows of
	// day boundaries in the feed, the oldest row of the conversation is
	// above a window of twenty-four rows, and a test about the order of the
	// feed would then be a test about the height of a screen.
	base_model := conversationWithPending(
		t, theme.ProfileNoColor, 100, 32, &pendingSource{},
	)

	// The clock and the zone of the fixture: the moments above are of two
	// days in UTC, the day of a message is a row of the feed, and a model
	// that read the zone of the machine would draw a different set of days
	// — and a different number of rows — in every other zone.
	model := withClock(base_model, at(24+12, 0), time.UTC)

	model, _ = updateModel(t, model, historyLoadedMsg{
		chatID:    7,
		operation: base_model.historyOperation,
		page:      page,
	})

	// The two records the owner's queue was holding from the evening
	// before, and the one this session just wrote. The states are what the
	// owner saw: "Delivery uncertain" for the two old ones.
	model.pending = []PendingMessage{
		{
			EntryID:   "old-1",
			ChatID:    7,
			Text:      "вчера: отправлено под вопросом",
			State:     MessageDeliveryUncertain,
			CreatedAt: at(21, 24),
		},
		{
			EntryID:   "old-2",
			ChatID:    7,
			Text:      "вчера: тоже под вопросом",
			State:     MessageDeliveryUncertain,
			CreatedAt: at(21, 30),
		},
		{
			EntryID:   "fresh",
			ChatID:    7,
			Text:      "только что",
			State:     MessageDeliveryQueued,
			CreatedAt: at(24+9, 3),
		},
	}
	model = model.normalizeTimeline().scrollToNewest()

	var order []string
	for _, entry := range model.feedEntries() {
		if entry.isPending() {
			order = append(order, entry.pending.EntryID)
			continue
		}
		order = append(order, "msg:"+entry.message.Text)
	}

	want := []string{
		"msg:вчера: 09:00",
		"msg:вчера: 21:22",
		"old-1", // 21:24 yesterday, between the two messages of the chat
		"old-2", // 21:30 yesterday, ditto
		"msg:вчера: 21:35",
		"msg:сегодня: первое",
		"msg:сегодня: второе",
		"fresh", // written now, so the newest thing in the chat
	}
	if strings.Join(order, " | ") != strings.Join(want, " | ") {
		t.Fatalf(
			"the feed is not in the order the conversation was written in.\n"+
				"got:  %s\nwant: %s\n\nThe two rows of yesterday evening "+
				"are at the foot of the feed on the owner's screen, and "+
				"the conversation is pushed up above them.",
			strings.Join(order, " | "), strings.Join(want, " | "),
		)
	}

	// And the screen: the foot of the chat is today's newest message, not
	// yesterday's uncertainty.
	rows := strings.Join(feedOf(t, model), "\n")
	newest := strings.LastIndex(rows, "сегодня: второе")
	oldest := strings.Index(rows, "вчера: отправлено под вопросом")
	if newest < 0 {
		t.Fatalf("the newest message is not on the screen:\n%s", rows)
	}
	if oldest < 0 || oldest > newest {
		t.Fatalf(
			"yesterday's uncertain row is drawn after today's newest "+
				"message, so it holds the foot of the chat:\n%s", rows,
		)
	}
}

package tui

import (
	"strings"
	"testing"
	"time"

	"telecli/internal/tui/theme"
)

// A message that is still going out is part of the conversation, and a
// queue full of them does not push the conversation off the screen.
//
// The rows of the pending messages used to be subtracted from the
// history's own budget and drawn as a block underneath it. A handful of
// them was enough to leave the user looking at the state of messages they
// had just written and none of the conversation they were writing into.

// pendingAt is a fixed moment, so the rows a pending message draws do
// not depend on the machine's clock.
var pendingAt = time.Date(2026, time.September, 29, 12, 7, 0, 0, time.UTC)

func conversationOfHeight(t *testing.T, height int) Model {
	t.Helper()

	page := HistoryPage{Messages: []Message{
		{ID: 1, Author: "Anna Example", Text: "первое", Time: "11:00"},
		{ID: 2, Author: "Anna Example", Text: "второе", Time: "11:01"},
		{ID: 3, Author: "Anna Example", Text: "третье", Time: "11:02"},
		{ID: 4, Author: "Anna Example", Text: "четвёртое", Time: "11:03"},
		{ID: 5, Author: "Anna Example", Text: "пятое", Time: "11:04"},
	}}

	base := conversationWithPending(
		t, theme.ProfileNoColor, 100, height, &pendingSource{},
	)
	m, _ := updateModel(t, base, historyLoadedMsg{
		chatID:    7,
		operation: base.historyOperation,
		page:      page,
	})

	return m.scrollToNewest()
}

// The messages still going out must not make the history unreachable.
//
// The rows of the pending messages used to be taken out of the history's
// own budget and drawn as a block underneath it, so a queue with anything
// in it left the history window with no rows at all: the conversation
// above the block was not merely pushed down, it could not be scrolled to
// at all. The feed is one list now, so walking the cursor up from the
// newest message walks into the history and the window is placed by the
// heights of both.
func TestTheHistoryIsReachableThroughThePendingMessages(t *testing.T) {
	const height = 24

	m := conversationOfHeight(t, height)

	m.pending = nil
	m.pendingSnapshot = nil
	for i := 0; i < 10; i++ {
		m.pending = append(m.pending, PendingMessage{
			EntryID:   string(rune('a' + i)),
			ChatID:    7,
			Text:      "очередь",
			State:     MessageDeliveryQueued,
			CreatedAt: pendingAt,
		})
	}
	m = m.normalizeTimeline().scrollToNewest()

	// Walk up out of the pending messages and into the history.
	seen := 0
	for step := 0; step < len(m.pending)+len(m.selected().Messages); step++ {
		m = m.moveTimelineCursor(-1)
		if _, isPending := m.selectedPending(); isPending {
			seen++
			continue
		}
		if _, isHistory := m.selectedHistory(); isHistory {
			rows := strings.Join(feedOf(t, m), "\n")
			if !strings.Contains(rows, "первое") &&
				!strings.Contains(rows, "четвёртое") &&
				!strings.Contains(rows, "пятое") {
				t.Fatalf(
					"the cursor is on a history message but the feed "+
						"shows none of the conversation:\n%s", rows,
				)
			}
			return
		}
	}
	t.Fatalf("walked %d pending messages and never reached the history", seen)
}

// A few messages in the queue leave the conversation on the screen, which
// is the case the owner saw emptied.
func TestAFewQueuedMessagesLeaveTheConversationOnScreen(t *testing.T) {
	m := conversationOfHeight(t, 24)

	m.pending = []PendingMessage{
		{
			EntryID:   "a",
			ChatID:    7,
			Text:      "первое в очереди",
			State:     MessageDeliveryQueued,
			CreatedAt: pendingAt,
		},
		{
			EntryID:   "b",
			ChatID:    7,
			Text:      "второе в очереди",
			State:     MessageDeliveryQueued,
			CreatedAt: pendingAt,
		},
	}
	m = m.normalizeTimeline().scrollToNewest()

	rows := strings.Join(feedOf(t, m), "\n")
	for _, word := range []string{"первое", "второе", "третье", "четвёртое", "пятое"} {
		if !strings.Contains(rows, word) {
			t.Fatalf("two queued messages pushed %q off the screen:\n%s", word, rows)
		}
	}
	for _, word := range []string{"первое в очереди", "второе в очереди"} {
		if !strings.Contains(rows, word) {
			t.Fatalf("%q is not on the screen:\n%s", word, rows)
		}
	}
}

// The feed is one column: the messages that are going out are drawn where
// they belong, under the history, and not in a block of their own.
func TestPendingMessagesAreDrawnInsideTheFeed(t *testing.T) {
	m := conversationOfHeight(t, 24)

	m.pending = []PendingMessage{{
		EntryID:   "a",
		ChatID:    7,
		Text:      "ещё в пути",
		State:     MessageDeliverySending,
		CreatedAt: pendingAt,
	}}
	m = m.normalizeTimeline().scrollToNewest()

	rows := strings.Split(strings.Join(feedOf(t, m), "\n"), "\n")
	queuedAt, historyAt := -1, -1
	for index, row := range rows {
		if queuedAt < 0 && strings.Contains(row, "ещё в пути") {
			queuedAt = index
		}
		if historyAt < 0 && strings.Contains(row, "пятое") {
			historyAt = index
		}
	}

	if queuedAt < 0 || historyAt < 0 {
		t.Fatalf(
			"queued at %d and history at %d:\n%s", queuedAt, historyAt,
			strings.Join(feedOf(t, m), "\n"),
		)
	}
	if queuedAt <= historyAt {
		t.Fatalf(
			"the message still going out is at row %d, above the history "+
				"message at %d: it is drawn in a block of its own rather "+
				"than in the feed:\n%s",
			queuedAt, historyAt, strings.Join(feedOf(t, m), "\n"),
		)
	}
}

// The window is measured by the heights the view draws the pending
// messages with, so the two cannot disagree about where the feed ends.
func TestTheFeedWindowIsMeasuredWithThePendingRows(t *testing.T) {
	m := conversationOfHeight(t, 24)

	layout := LayoutFor(m.width, m.height)
	width := layout.ChatContentWidth()
	styles := m.styles()

	m.pending = []PendingMessage{{
		EntryID:   "a",
		ChatID:    7,
		Text:      "строка один\nстрока два",
		State:     MessageDeliveryQueued,
		CreatedAt: pendingAt,
	}}
	m = m.normalizeTimeline().scrollToNewest()

	entries := m.feedEntries()
	if len(entries) == 0 {
		t.Fatal("the feed has no entries")
	}
	last := entries[len(entries)-1]
	if !last.isPending() {
		t.Fatal("the last entry of the feed is not the pending message")
	}

	// The height the window measured with is the height the view drew
	// with: the same call, so they cannot drift.
	measured := m.entryRows(last, layout, width, styles)
	drawn := len(m.entryLines(last, layout, width, styles))
	if measured != drawn {
		t.Fatalf("measured %d rows, drew %d", measured, drawn)
	}
	if measured <= 0 {
		t.Fatalf("the pending entry takes %d rows", measured)
	}
}

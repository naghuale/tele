package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// The timeline is chronological: the oldest message is the first entry of
// Chat.Messages and the newest is the last one, and the screen shows the
// end of it. TDLib answers a history request newest-first, so the order is
// the TUI's to decide and not the source's — §8.3 and the divergence 1 of
// the specification.

// chronologicalPage is one page as the source answers it: newest first.
func chronologicalPage() HistoryPage {
	return HistoryPage{
		Messages: []Message{
			{ID: 100, Text: "newest", At: mockMoment("10:02")},
			{ID: 99, Text: "oldest loaded", At: mockMoment("10:01")},
		},
		NextFrom: 99,
		HasMore:  true,
	}
}

// olderChronologicalPage overlaps the first one on message 99 and adds two
// older messages, again newest first.
func olderChronologicalPage() HistoryPage {
	return HistoryPage{
		Messages: []Message{
			{ID: 99, Text: "oldest loaded", At: mockMoment("10:01")},
			{ID: 98, Text: "older", At: mockMoment("10:00")},
			{ID: 97, Text: "oldest", At: mockMoment("09:59")},
		},
		NextFrom: 97,
		HasMore:  true,
	}
}

func equalIDs(got []int64, want ...int64) bool {
	if len(got) != len(want) {
		return false
	}
	for index, id := range got {
		if id != want[index] {
			return false
		}
	}

	return true
}

// A page arrives newest-first and is stored oldest-first. A conversation
// that shows its messages the wrong way round is not a conversation, and
// the cost of getting it right here is one reversal per page.
func TestHistoryPageIsStoredOldestFirst(t *testing.T) {
	source := &recordingChatSource{pages: []HistoryPage{chronologicalPage()}}
	m := openConversationWithHistory(t, source, 7, chronologicalPage())

	if got := messageIDs(m.selected().Messages); !equalIDs(got, 99, 100) {
		t.Fatalf("messages = %v, want [99 100] oldest first", got)
	}
}

// Opening a chat shows the end of the conversation: the newest message is
// the last one, and it is the one the cursor is on.
func TestOpeningAChatSelectsTheNewestMessage(t *testing.T) {
	source := &recordingChatSource{pages: []HistoryPage{chronologicalPage()}}
	m := openConversationWithHistory(t, source, 7, chronologicalPage())

	if want := len(m.selected().Messages) - 1; m.selectedMsg != want {
		t.Fatalf("selectedMsg = %d, want %d (the newest message)", m.selectedMsg, want)
	}
}

// `j` and `↓` go down the conversation, which is towards the newer
// messages, and stop at the newest one.
func TestDownMovesTowardsTheNewerMessage(t *testing.T) {
	source := &recordingChatSource{pages: []HistoryPage{chronologicalPage()}}
	m := openConversationWithHistory(t, source, 7, chronologicalPage())
	m.focus = FocusHistory
	m.selectedMsg = 0

	m, _ = updateModel(t, m, press(tea.KeyDown))
	if m.selectedMsg != 1 {
		t.Fatalf("selectedMsg = %d, want 1", m.selectedMsg)
	}

	m, _ = updateModel(t, m, pressRunes("j"))
	if m.selectedMsg != 1 {
		t.Fatalf("selectedMsg = %d, want 1 at the newest message", m.selectedMsg)
	}
}

// `k` and `↑` go up the conversation, towards the older messages.
func TestUpMovesTowardsTheOlderMessage(t *testing.T) {
	source := &recordingChatSource{pages: []HistoryPage{chronologicalPage()}}
	m := openConversationWithHistory(t, source, 7, chronologicalPage())
	m.focus = FocusHistory
	m.selectedMsg = 1

	m, _ = updateModel(t, m, press(tea.KeyUp))
	if m.selectedMsg != 0 {
		t.Fatalf("selectedMsg = %d, want 0", m.selectedMsg)
	}

	m, _ = updateModel(t, m, pressRunes("k"))
	if m.selectedMsg != 0 {
		t.Fatalf("selectedMsg = %d, want 0 at the oldest loaded message", m.selectedMsg)
	}
}

// A page is a page of the screen, so PageDown and PageUp move the cursor by
// as many messages as fit rather than by one.
func TestPageKeysMoveByAWholeScreen(t *testing.T) {
	source := &recordingChatSource{
		pages: []HistoryPage{manyMessagePage(40)},
	}
	m := openConversationWithHistory(t, source, 7, manyMessagePage(40))
	m.focus = FocusHistory
	m.selectedMsg = 0
	page := m.timelinePageSize()

	m, _ = updateModel(t, m, press(tea.KeyPgDown))
	if m.selectedMsg != page {
		t.Fatalf("selectedMsg = %d, want %d after PageDown", m.selectedMsg, page)
	}

	m, _ = updateModel(t, m, press(tea.KeyPgUp))
	if m.selectedMsg != 0 {
		t.Fatalf("selectedMsg = %d, want 0 after PageUp", m.selectedMsg)
	}
}

// `G` goes to the newest message, which is the end of the conversation.
func TestLastGoesToTheNewestMessage(t *testing.T) {
	source := &recordingChatSource{
		pages: []HistoryPage{manyMessagePage(40)},
	}
	m := openConversationWithHistory(t, source, 7, manyMessagePage(40))
	m.focus = FocusHistory
	m.selectedMsg = 0

	m, _ = updateModel(t, m, pressRunes("G"))
	if want := len(m.selected().Messages) - 1; m.selectedMsg != want {
		t.Fatalf("selectedMsg = %d, want %d", m.selectedMsg, want)
	}

	m, _ = updateModel(t, m, press(tea.KeyEnd))
	if want := len(m.selected().Messages) - 1; m.selectedMsg != want {
		t.Fatalf("selectedMsg = %d, want %d after End", m.selectedMsg, want)
	}
}

// `g` belongs to the chat list. In a conversation it would mean the
// oldest message, and §8.3 says it means nothing there, so it must not move
// the cursor a user did not ask to move.
func TestFirstDoesNothingInTheConversation(t *testing.T) {
	source := &recordingChatSource{
		pages: []HistoryPage{manyMessagePage(40)},
	}
	m := openConversationWithHistory(t, source, 7, manyMessagePage(40))
	m.focus = FocusHistory
	m.selectedMsg = 10

	m, _ = updateModel(t, m, pressRunes("g"))
	if m.selectedMsg != 10 {
		t.Fatalf("selectedMsg = %d, want 10 unchanged", m.selectedMsg)
	}

	m, _ = updateModel(t, m, press(tea.KeyHome))
	if m.selectedMsg != 10 {
		t.Fatalf("selectedMsg = %d, want 10 unchanged after Home", m.selectedMsg)
	}
}

// `Enter` and `i` move the keys into the composer, which is the one place
// a message is written.
func TestEnterAndIMoveTheFocusToTheComposer(t *testing.T) {
	for name, key := range map[string]tea.KeyMsg{
		"Enter": press(tea.KeyEnter),
		"i":     pressRunes("i"),
	} {
		t.Run(name, func(t *testing.T) {
			source := &recordingChatSource{pages: []HistoryPage{chronologicalPage()}}
			m := openConversationWithHistory(t, source, 7, chronologicalPage())
			m.focus = FocusHistory

			m, _ = updateModel(t, m, key)
			if m.focus != FocusComposer {
				t.Fatalf("focus = %v, want FocusComposer", m.focus)
			}
		})
	}
}

// A page of older messages goes on top, and the message that was on screen
// stays on screen. A user reading upwards who is thrown back down by a
// page they asked for has lost their place, and the page is exactly what
// they asked for.
func TestOlderPageDoesNotMoveTheView(t *testing.T) {
	source := &recordingChatSource{
		pages: []HistoryPage{chronologicalPage(), olderChronologicalPage()},
	}
	m := openConversationWithHistory(t, source, 7, chronologicalPage())
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 12})
	m.focus = FocusHistory
	m, _ = updateModel(t, m, press(tea.KeyUp))

	top := m.timelineTop
	selected := m.selectedMsg
	if top != 0 {
		t.Fatalf("timelineTop = %d, want 0 with two messages on screen", top)
	}

	m, cmd := updateModel(t, m, press(tea.KeyUp))
	m, _ = updateModel(t, m, runCmd(t, cmd))

	if got := messageIDs(m.selected().Messages); !equalIDs(got, 97, 98, 99, 100) {
		t.Fatalf("messages = %v, want [97 98 99 100]", got)
	}
	if m.timelineTop != top+2 {
		t.Fatalf(
			"timelineTop = %d, want %d: the same message has to stay on top",
			m.timelineTop,
			top+2,
		)
	}
	if m.selectedMsg != selected+2 {
		t.Fatalf(
			"selectedMsg = %d, want %d: the same message has to stay selected",
			m.selectedMsg,
			selected+2,
		)
	}
}

// A message that was sent is the newest one, so it goes to the end of the
// conversation, and a user who is at the end stays at the end.
func TestSentMessageGoesToTheEndAndFollowsTheCursor(t *testing.T) {
	source := &recordingChatSource{pages: []HistoryPage{chronologicalPage()}}
	m := openConversationWithHistory(t, source, 7, chronologicalPage())
	m.focus = FocusHistory

	m, _ = updateModel(t, m, messageSentMsg{
		chatID:    7,
		operation: m.sendOperation,
		message:   Message{ID: 101, Outgoing: true, Text: "sent", At: mockMoment("10:03")},
	})

	if got := messageIDs(m.selected().Messages); !equalIDs(got, 99, 100, 101) {
		t.Fatalf("messages = %v, want [99 100 101]", got)
	}
	if m.selectedMsg != 2 {
		t.Fatalf("selectedMsg = %d, want 2 (the sent message)", m.selectedMsg)
	}
}

// A user who has scrolled up to read something is not yanked to the bottom
// by a message that arrives; the message goes to the end and the cursor
// stays where it was.
func TestSentMessageDoesNotMoveACursorThatIsReadingHistory(t *testing.T) {
	source := &recordingChatSource{
		pages: []HistoryPage{manyMessagePage(40)},
	}
	m := openConversationWithHistory(t, source, 7, manyMessagePage(40))
	m.focus = FocusHistory
	m.selectedMsg = 3

	m, _ = updateModel(t, m, messageSentMsg{
		chatID:    7,
		operation: m.sendOperation,
		message:   Message{ID: 1000, Outgoing: true, Text: "sent", At: mockMoment("10:03")},
	})

	if m.selectedMsg != 3 {
		t.Fatalf("selectedMsg = %d, want 3 unchanged", m.selectedMsg)
	}
	if m.selected().Messages[len(m.selected().Messages)-1].ID != 1000 {
		t.Fatal("the sent message is not the newest one")
	}
}

// The scroll position belongs to the conversation: a resize keeps the
// message the user was reading at the top of the window (§10.5).
func TestResizeKeepsTheScrollPosition(t *testing.T) {
	source := &recordingChatSource{
		pages: []HistoryPage{manyMessagePage(40)},
	}
	m := openConversationWithHistory(t, source, 7, manyMessagePage(40))
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 12})
	m.focus = FocusHistory

	for range 4 {
		m, _ = updateModel(t, m, press(tea.KeyUp))
	}
	top := m.timelineTop
	if top == 0 {
		t.Fatal("the view did not scroll up at all")
	}

	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 60, Height: 12})

	if m.timelineTop != top {
		t.Fatalf("timelineTop = %d, want %d after a resize", m.timelineTop, top)
	}
	if m.selectedMsg != 35 {
		t.Fatalf("selectedMsg = %d, want 35 after a resize", m.selectedMsg)
	}
}

// The cursor never leaves the loaded history, whatever it is asked to do.
func TestSelectionStaysInsideTheLoadedHistory(t *testing.T) {
	source := &recordingChatSource{
		pages: []HistoryPage{manyMessagePage(10)},
	}
	m := openConversationWithHistory(t, source, 7, manyMessagePage(10))
	m.focus = FocusHistory
	last := len(m.selected().Messages) - 1

	for range 15 {
		m, _ = updateModel(t, m, press(tea.KeyUp))
	}
	if m.selectedMsg != 0 {
		t.Fatalf("selectedMsg = %d, want 0 at the oldest message", m.selectedMsg)
	}

	for range 20 {
		m, _ = updateModel(t, m, press(tea.KeyDown))
	}
	if m.selectedMsg != last {
		t.Fatalf("selectedMsg = %d, want %d at the newest message", m.selectedMsg, last)
	}
	if m.timelineTop < 0 || m.timelineTop > last {
		t.Fatalf("timelineTop = %d, want it inside the history", m.timelineTop)
	}
}

// manyMessagePage is a page of count messages, newest first, with times
// that make every row distinguishable.
func manyMessagePage(count int) HistoryPage {
	page := HistoryPage{HasMore: true}
	for index := count; index >= 1; index-- {
		page.Messages = append(page.Messages, Message{
			ID:   int64(index),
			Text: "message",
		})
	}
	page.NextFrom = 1

	return page
}

package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"telecli/internal/tui/theme"
)

// The tests in this file are about what the timeline looks like: the order
// of the messages on the screen, the shape of a message, and the markers
// that survive a terminal with no colour.

// lineWith returns the first line of a screen that contains a substring.
func lineWith(view, want string) (string, int, bool) {
	for index, line := range viewLines(view) {
		if strings.Contains(line, want) {
			return line, index, true
		}
	}

	return "", -1, false
}

// lastFilledLineBefore returns the index of the last row with something on
// it before a given row, which is the last row a region ends on.
func lastFilledLineBefore(view string, before int) int {
	for index := before - 1; index >= 0; index-- {
		if strings.TrimSpace(viewLines(view)[index]) != "" {
			return index
		}
	}

	return -1
}

// A conversation opens at its end: the oldest message is on the first row
// of the timeline and the newest is the last thing above the composer. This
// is the difference between a messenger and a log.
// The screen is a narrow one so that the conversation is the only thing on
// it: with two panes the chat list has rows of its own, and "the last row
// above the composer" would be a row of a list.
func TestOpeningAChatShowsTheNewestMessageLast(t *testing.T) {
	m := openedProgramModel(t, theme.ProfileNoColor, 60, 20)
	view := plain(m.View())

	_, oldestAt, ok := lineWith(view, "Hello")
	if !ok {
		t.Fatalf("the oldest message is not on the screen:\n%s", view)
	}
	newest, newestAt, ok := lineWith(view, "How are you?")
	if !ok {
		t.Fatalf("the newest message is not on the screen:\n%s", view)
	}
	if newestAt <= oldestAt {
		t.Fatalf("the newest message is above the oldest one:\n%s", view)
	}

	composerAt, ok := lineIndexWith(view, composerPlaceholder)
	if !ok {
		t.Fatalf("there is no composer:\n%s", view)
	}
	if last := lastFilledLineBefore(view, composerAt); last != newestAt {
		t.Fatalf(
			"the last row above the composer is %q, want the newest message %q:\n%s",
			strings.TrimSpace(viewLines(view)[last]),
			strings.TrimSpace(newest),
			view,
		)
	}
}

// lineIndexWith returns the index of the first line that contains a
// substring.
func lineIndexWith(view, want string) (int, bool) {
	_, index, ok := lineWith(view, want)

	return index, ok
}

// The sender's line says who sent the message and when, on one line: "who
// · when" is the whole of what a reader needs before the words start.
func TestTheAuthorLineSaysTheSenderAndTheTime(t *testing.T) {
	m := openedProgramModel(t, theme.ProfileNoColor, 60, 20)
	m.focus = FocusHistory

	view := plain(m.View())
	first := m.selected().Messages[0]
	line, _, ok := lineWith(view, first.Time)
	if !ok {
		t.Fatalf("no author line on the screen:\n%s", view)
	}

	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, messageAuthor(first)) {
		t.Fatalf("the name does not start the line: %q", trimmed)
	}
	if !strings.HasSuffix(trimmed, first.Time) {
		t.Fatalf("the time does not end the line: %q", trimmed)
	}
}

// The header does not move. It is the name of the chat, and a user who has
// scrolled a hundred messages up must not have to scroll back to remember
// where they are.
func TestConversationHeaderIsTheFirstRowAtAnyScroll(t *testing.T) {
	m := openedProgramModel(t, theme.ProfileNoColor, 100, 20)
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 20})
	m.focus = FocusHistory

	for range 3 {
		m, _ = updateModel(t, m, press(tea.KeyUp))

		view := plain(m.View())
		if !strings.Contains(viewLines(view)[0], m.selected().Title) {
			t.Fatalf("the first row is not the header after scrolling:\n%s", view)
		}
	}
}

// A text longer than the pane wraps. It is not cut: a message cut in half
// is a message a user cannot read and cannot answer, and the pane is the
// one thing the user made small.
func TestLongMessageWrapsToTheWidthOfTheRegion(t *testing.T) {
	m := openedProgramModel(t, theme.ProfileNoColor, 60, 20)
	m.focus = FocusHistory
	m.chats[m.selectedChat].Messages = []Message{
		{
			ID:   1,
			Text: "Очень длинный текст 🌍 с кириллицей, эмодзи и латиницей mixed in, который обязан перенестись по ширине области разговора",
			Time: "10:00",
		},
	}
	m.selectedMsg = 0
	m.timelineTop = 0

	view := m.View()
	for index, line := range viewLines(view) {
		if got := m.widths.StringWidth(line); got > m.width {
			t.Fatalf("row %d is %d columns, want at most %d: %q", index, got, m.width, line)
		}
	}

	plainView := plain(view)
	for _, want := range []string{"перенестись", "ширине", "области", "разговора", "🌍"} {
		if !strings.Contains(plainView, want) {
			t.Fatalf("the wrapped text lost %q:\n%s", want, plainView)
		}
	}
}

// The progress and the failure of an older page belong at the top of the
// timeline, where the page they are about would have gone. At the bottom
// they would be read as the end of the conversation.
func TestOlderPageProgressIsAtTheTopOfTheTimeline(t *testing.T) {
	source := &recordingChatSource{
		pages: []HistoryPage{chronologicalPage(), olderChronologicalPage()},
	}
	m := openConversationWithHistory(t, source, 7, chronologicalPage())
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 20})
	m.focus = FocusHistory
	m = selectOldestMessage(t, m)

	updated, cmd := updateModel(t, m, press(tea.KeyUp))
	if cmd == nil {
		t.Fatal("↑ on the oldest message must start a request")
	}
	m = updated

	view := plain(m.View())
	loadingAt, ok := lineIndexWith(view, "Loading older messages...")
	if !ok {
		t.Fatalf("the request is not on the screen:\n%s", view)
	}
	oldestAt, ok := lineIndexWith(view, "oldest loaded")
	if !ok {
		t.Fatalf("the oldest loaded message is not on the screen:\n%s", view)
	}
	if loadingAt > oldestAt {
		t.Fatalf("the request is below the messages it is about:\n%s", view)
	}
}

// A page of older messages must not move the message that was on screen.
// The reader asked for more history, and the one thing a reader notices is
// the text they were reading jumping away.
func TestOlderPageKeepsTheFirstVisibleMessageOnScreen(t *testing.T) {
	source := &recordingChatSource{
		pages: []HistoryPage{chronologicalPage(), olderChronologicalPage()},
	}
	m := openConversationWithHistory(t, source, 7, chronologicalPage())
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 12})
	m.focus = FocusHistory
	m = selectOldestMessage(t, m)

	before := firstTimelineMessage(t, m)

	m, cmd := updateModel(t, m, press(tea.KeyUp))
	m, _ = updateModel(t, m, runCmd(t, cmd))

	if got := firstTimelineMessage(t, m); got != before {
		t.Fatalf("the first visible message is %q, want %q", got, before)
	}
	if got := messageIDs(m.selected().Messages); !equalIDs(got, 97, 98, 99, 100) {
		t.Fatalf("messages = %v, want [97 98 99 100]", got)
	}
}

// firstTimelineMessage returns the text of the first message on the screen.
//
// It looks for the words of the message rather than for the name above
// them: a screen too short for a name and a text on separate rows has only
// the text, and a screen with them has both.
func firstTimelineMessage(t *testing.T, m Model) string {
	t.Helper()

	view := plain(m.View())
	for _, line := range viewLines(view) {
		if strings.Contains(line, m.selected().Messages[0].Text) {
			return strings.TrimSpace(line)
		}
	}

	t.Fatalf("no message on the screen:\n%s", view)

	return ""
}

// The selected message is marked with a glyph, and it is a glyph in a
// terminal that prints no colour and no attributes. §5.2 asks for one
// marker per selected message and for no line in front of it.
func TestNoColorMarksTheSelectedMessageWithAGlyph(t *testing.T) {
	m := focusedOn(
		openedProgramModel(t, theme.ProfileNoColor, 60, 20),
		FocusHistory,
	)
	m, _ = updateModel(t, m, press(tea.KeyUp))

	view := plain(m.View())
	marked, at, ok := lineWith(view, theme.SelectionMark)
	if !ok {
		t.Fatalf("the selected message is not marked:\n%s", view)
	}

	// One marker, on the message the cursor is on, and nowhere else. The
	// marker is on the first row of a message: its name for a message from
	// the other side, and its text for one of this user, whose block is on
	// the right and has no name above it.
	selected := m.selected().Messages[m.selectedMsg]
	below := viewLines(view)[at+1]
	if selected.Outgoing {
		if !strings.Contains(marked, selected.Text) {
			t.Fatalf(
				"the marker is not on the message it belongs to, want %q: %q",
				selected.Text,
				marked,
			)
		}
	} else {
		if !strings.Contains(marked, messageAuthor(selected)) {
			t.Fatalf("the marker is not on a message: %q", marked)
		}
		if !strings.Contains(below, selected.Text) {
			t.Fatalf(
				"the marker is on the wrong message, want %q: %q",
				selected.Text,
				below,
			)
		}
	}
	// One marker in the timeline. The composer draws a prompt of the same
	// shape, and it is a different thing: one is the message the keys act
	// on and the other is the field they are typed into.
	composer, ok := lineIndexWith(view, composerPlaceholder)
	if !ok {
		t.Fatalf("there is no composer:\n%s", view)
	}
	timeline := strings.Join(viewLines(view)[:composer], "\n")
	if got := strings.Count(timeline, theme.SelectionMark); got != 1 {
		t.Fatalf("%d markers in the timeline, want 1:\n%s", got, view)
	}
}

// The selected chat is a row with a marker of its own in a column of its
// own, and the focus of the list is a rule under its heading. The two are
// different things, and the row carries neither a focus glyph nor a
// `▌`: a list that marked both as `▌` read as a double line, which the
// review of #30 named as the
// one thing left to fix.
func TestSelectedChatRowIsMarkedWithItsOwnGlyph(t *testing.T) {
	m := focusedOn(
		programModel(t, theme.ProfileNoColor, 120, 20),
		FocusChatList,
	)

	view := plain(m.View())
	row, _, ok := lineWith(view, theme.SelectionMark+m.selected().Title)
	if !ok {
		t.Fatalf("the selected chat is not marked with %q:\n%s", theme.SelectionMark, view)
	}
	if !strings.HasPrefix(strings.TrimLeft(row, " "), theme.SelectionMark) {
		t.Fatalf("the marker is not in a column of its own: %q", row)
	}
	for _, glyph := range focusBarGlyphs {
		if strings.ContainsRune(row, glyph) {
			t.Fatalf("the selected row carries the focus glyph %q: %q", glyph, row)
		}
	}
}

// A message longer than the whole timeline is cut at the rows the timeline
// has, and the composer is still on the screen. §3.4 requires the composer
// to be visible whenever a conversation is open, and a long message is not
// a reason to lose it.
func TestAMessageTallerThanTheTimelineKeepsTheComposer(t *testing.T) {
	m := openedProgramModel(t, theme.ProfileNoColor, 60, 12)
	m.focus = FocusHistory
	m.chats[m.selectedChat].Messages = []Message{{
		ID:   1,
		Text: strings.Repeat("слово ", 200),
		Time: "10:00",
	}}
	m.selectedMsg = 0
	m.timelineTop = 0

	view := plain(m.View())
	if _, ok := lineIndexWith(view, composerPlaceholder); !ok {
		t.Fatalf("a long message pushed the composer off the screen:\n%s", view)
	}
	for index, line := range viewLines(view) {
		if got := m.widths.StringWidth(line); got > m.width {
			t.Fatalf("row %d is %d columns, want at most %d", index, got, m.width)
		}
	}
}

// The sender's name and the text of a message start in the same column,
// as §4.4 draws them. A body one column to the left of its own name reads
// as a second message with the name of the first.
func TestTheTextOfAMessageStartsUnderItsSenderName(t *testing.T) {
	t.Run("history", func(t *testing.T) {
		model := openedProgramModel(t, theme.ProfileNoColor, 60, 24)
		layout := LayoutFor(model.width, model.height)
		width := layout.ChatContentWidth()

		entries := timelineEntries(model.selected().Messages)
		rows := model.entryLines(entries[0], layout, width, model.styles())

		// The first row of a block is the blank line between messages, so
		// the name is on the second and the text under it. The name is one
		// column further right than the row starts, because the column in
		// front of it is the marker of the message under the cursor.
		head := indentOf(rows[1]) + selectionMarkerWidth
		for index, row := range rows[2:] {
			if got := indentOf(row); got != head {
				t.Fatalf(
					"row %d starts at column %d, the sender's name at %d: %q",
					index+2,
					got,
					head,
					plain(row),
				)
			}
		}
	})
}

// indentOf returns how many spaces a rendered row starts with.
func indentOf(rendered string) int {
	row := plain(rendered)

	return len(row) - len(strings.TrimLeft(row, " "))
}

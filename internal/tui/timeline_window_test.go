package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// This file is where the window of the feed is placed, and the difference
// between measuring it and guessing at it.
//
// A chat opened on a real account showed a few messages in the corner of an
// empty feed, and pressing ↑ brought the rest in — every time the chat was
// opened. The frames were the right size and the right height; the window
// was in the wrong place. It was placed by dividing the rows of the feed by
// a guess at how tall a message is, and the guess stopped being true in
// #50: an incoming message is its author, its text and the blank row under
// it, an outgoing one its text, its state and the blank row, and an album
// of three photographs is one entry. Two rows a message places a window a
// third of a screen too late, and the rows it left above it were empty.
//
// So the window is measured by the heights the view draws with, from the
// newest entry down, and the tests here are that measurement: a feed full
// to its top row, the newest message on its last row, and a page of older
// messages that does not move the messages a reader is looking at.

// windowWidth and windowHeight are the size these tests draw at: a window
// tall enough that forty messages are more than a screenful of them, and
// wide enough that the messages are not all wrapped.
const (
	windowWidth  = 120
	windowHeight = 40
)

// mixedPage is a page of count messages of every kind the feed draws: a
// message from the other side, one of this user's, a text of two lines, a
// run of three photographs drawn as one entry, and a file. A page of
// identical one-line messages would be measured correctly by a guess of two
// rows a message, and that is the guess this file exists to catch.
func mixedPage(count int) HistoryPage {
	page := HistoryPage{HasMore: true}

	// TDLib answers newest first, and the model reverses it (§8.3).
	for index := count; index >= 1; index-- {
		message := Message{
			ID:     int64(index),
			Time:   fmt.Sprintf("12:%02d", index%60),
			Text:   fmt.Sprintf("message %d", index),
			Author: "Anna Example", AuthorID: 5,
		}

		switch index % 7 {
		case 0:
			message.Outgoing = true
			message.Author, message.AuthorID = "", 0
		case 1:
			message.Text = "a message of two lines\nand the second of them"
		case 2, 3, 4:
			// Three in a row carry one album identifier, so the feed
			// draws them as one entry (§4.4).
			message.Text = ""
			message.Media = "photo"
			message.AlbumID = 100
		case 5:
			message.Text = ""
			message.Media = "photo"
		}

		page.Messages = append(page.Messages, message)
	}

	return page
}

// messageOf is what a message is on the screen as, so that a test can look
// for it: its text, or the file it carries, or the name of whoever sent it.
func messageOf(_ Model, message Message) string {
	if message.Text != "" {
		return message.Text
	}
	if label := mediaLabel(message, 1); label != "" {
		return label
	}

	return messageAuthor(message)
}

// joinedPlain is the rows of the feed as one piece of text.
func joinedPlain(rows []string) string {
	return strings.Join(rows, "\n")
}

// feedOf returns the rows the messages of the conversation are drawn on,
// exactly as the view draws them: the history and then the pending messages
// below it.
func feedOf(t *testing.T, m Model) []string {
	t.Helper()

	layout := LayoutFor(m.width, m.height)
	width := layout.ChatContentWidth()

	return m.timelineLines(layout, width, m.feedRows())
}

// rowsOfTheFeed returns the rows of the whole area of the messages, the
// empty ones included, which is what a user is looking at.
func rowsOfTheFeed(t *testing.T, m Model) []string {
	t.Helper()

	layout := LayoutFor(m.width, m.height)
	width := layout.ChatContentWidth()

	rows := m.conversationRegionHeight(layout, width) - conversationHeaderRows
	rows -= len(m.statusBlockLines(layout, width)) + m.olderPageLineCount()
	rows = maxInt(rows, 0)

	return anchorTimelineToBottom(m.timelineBody(layout, width, rows), rows)
}

// mixedConversation opens a chat with count messages of mixed kinds at the
// size the tests of the window are drawn at.
func mixedConversation(t *testing.T, count int) Model {
	t.Helper()

	page := mixedPage(count)
	source := &recordingChatSource{pages: []HistoryPage{page}}
	m := openConversationWithHistory(t, source, 7, page)
	m, _ = updateModel(t, m, tea.WindowSizeMsg{
		Width: windowWidth, Height: windowHeight,
	})

	return m
}

// The window is filled from the bottom by the heights the entries really
// take, so the feed is full from its top row: no empty row above the first
// message of the window, and the newest message on the last row.
//
// The one row that may be empty above the first message is the gap row of
// that message: it separates it from the message above it, and the message
// above the topmost one of the window is not on the screen, so the view
// does not draw the gap for it (view_conversation.go). A feed that is
// short by more than that row is a window placed by a guess, and a guess
// leaves the rows above it empty.
func TestAChatOpensWithTheFeedFullToItsTopRow(t *testing.T) {
	m := mixedConversation(t, 40)

	messages := m.selected().Messages
	if len(messages) < 40 {
		t.Fatalf("messages = %d, want the 40 of the page", len(messages))
	}

	rows := feedOf(t, m)
	if gap := m.feedRows() - len(rows); gap > 1 {
		t.Fatalf(
			"the feed drew %d rows of the %d it has: the window is placed "+
				"by a guess and a guess leaves the rows above it empty",
			len(rows), m.feedRows(),
		)
	}

	// The anchor is a message of the history, and the entry it names is
	// the first one in the window: the feed starts at a message rather
	// than in the middle of one. It may be cut at the top — the oldest
	// visible message of a feed is the one a reader scrolls up for — but it
	// is never left out.
	if m.timelineTop < 0 || m.timelineTop >= len(messages) {
		t.Fatalf("timelineTop = %d, want a message of the history", m.timelineTop)
	}
	entries := timelineEntries(messages)
	window := entryIndexOfMessage(entries, m.timelineTop)
	if entries[window].first != m.timelineTop {
		t.Fatalf(
			"the window starts inside the entry of message %d, want it to "+
				"start at the entry of %d",
			entries[window].first, m.timelineTop,
		)
	}
	if !strings.Contains(joinedPlain(rows), messageOf(m, messages[m.timelineTop])) {
		t.Fatalf(
			"the message the anchor names is not on the screen:\n%s",
			strings.Join(viewLines(m.View()), "\n"),
		)
	}

	// The newest message is on the last row, which is where a user looks
	// after opening a chat.
	newest := messages[len(messages)-1]
	if last := plain(rows[len(rows)-1]); !strings.Contains(last, newest.Text) {
		t.Fatalf(
			"the last row of the feed is not the newest message (%q):\n%s",
			newest.Text,
			strings.Join(viewLines(strings.Join(rows, "\n")), "\n"),
		)
	}
}

// The same thing said about the screen rather than about the model: the
// area the messages are drawn in is full, with no rows the window left
// empty at the top of it. The blank row between two messages is the design
// (§3.3) and is not what this is about — a window that starts too late
// leaves rows that hold nothing *above the first message*, and a user who
// scrolls up to see what is there is looking at nothing.
func TestTheAreaOfTheMessagesIsFullToItsFirstRow(t *testing.T) {
	m := mixedConversation(t, 40)

	layout := LayoutFor(m.width, m.height)
	width := layout.ChatContentWidth()
	rows := maxInt(
		m.conversationRegionHeight(layout, width)-conversationHeaderRows-
			len(m.statusBlockLines(layout, width))-m.olderPageLineCount(),
		0,
	)

	body := m.timelineBody(layout, width, rows)
	if gap := rows - len(body); gap > 1 {
		t.Fatalf(
			"the messages take %d rows of the %d of the area: %d rows above "+
				"the first message hold nothing\n%s",
			len(body), rows, gap,
			strings.Join(viewLines(m.View()), "\n"),
		)
	}
}

// A conversation shorter than the feed is the other case, and the empty rows
// belong above the messages: the newest one sits on the row directly above
// the composer, which is where a user looks after sending something
// (TestSnapshotShortFeedAboveComposer draws one). The window is placed at
// the oldest message and stays there.
func TestAConversationShorterThanTheFeedSitsAboveTheComposer(t *testing.T) {
	page := HistoryPage{Messages: []Message{
		{ID: 3, Text: "the newest of three", Time: "12:03",
			Author: "Anna Example", AuthorID: 5},
		{ID: 2, Text: "the middle of three", Time: "12:02",
			Author: "Anna Example", AuthorID: 5},
		{ID: 1, Text: "the oldest of three", Time: "12:01",
			Author: "Anna Example", AuthorID: 5},
	}}
	source := &recordingChatSource{pages: []HistoryPage{page}}
	m := openConversationWithHistory(t, source, 7, page)
	m, _ = updateModel(t, m, tea.WindowSizeMsg{
		Width: windowWidth, Height: windowHeight,
	})
	if m.timelineTop != 0 {
		t.Fatalf("timelineTop = %d, want 0 for a conversation of three", m.timelineTop)
	}
	if got := len(m.selected().Messages); got != 3 {
		t.Fatalf("messages = %d, want 3", got)
	}

	rows := rowsOfTheFeed(t, m)
	messages := m.selected().Messages

	// The newest message is at the bottom of the area, and the last row of
	// its block is the half block of air under its words (§4.4).
	if !rowsEndWith(rows, messages[2].Text) {
		t.Fatalf(
			"the last two rows of the area are %q and %q, want the newest message",
			plain(rows[len(rows)-2]),
			plain(rows[len(rows)-1]),
		)
	}

	// The first message of the conversation is the first row that holds
	// something, and every row above it is empty. That row is the air
	// above the block now, the way the last one is the air below it.
	first := -1
	for index, row := range rows {
		if strings.TrimSpace(plain(row)) != "" {
			first = index

			break
		}
	}
	if first < 1 {
		t.Fatalf(
			"the messages start on row %d, want the empty rows above them\n%s",
			first+1, strings.Join(viewLines(m.View()), "\n"),
		)
	}
	// The oldest message is the first thing in the feed, and it takes up to
	// three rows to say so: the air that opens its block, the name of
	// whoever sent it, and the text under the name. Whether the air is a
	// row of half blocks or a row of spaces depends on the profile the
	// test is drawn with, so the name and the text are looked for in the
	// three rows the message can start in rather than on one of them.
	if !rowHolds(rows, first, first+3, messages[0].Text) {
		t.Fatalf(
			"the first three rows of the conversation hold %q, %q and %q, want the oldest message",
			plain(rows[first]),
			plain(rows[first+1]),
			plain(rows[first+2]),
		)
	}
}

// The reported case: a chat whose history arrives in pages, the first of
// them short, as TDLib answers on a database it has just opened. The window
// is placed again from the bottom after every page, so the feed the user is
// left looking at is a screenful of the conversation and the newest message
// is on its last row — not two messages in the corner of an empty feed.
func TestAChatWhoseHistoryArrivesInPagesEndsWithTheFeedFull(t *testing.T) {
	page := mixedPage(30)
	// TDLib answers the first request with what it has — one message here —
	// and the page above it comes after, the way the fill of §18.1 asks for
	// it. mixedPage is newest first, so the first page is its head.
	first := HistoryPage{Messages: page.Messages[:1], HasMore: true}
	older := HistoryPage{Messages: page.Messages[1:], HasMore: false}

	source := &recordingChatSource{pages: []HistoryPage{first, older}}
	m := NewModelWithSource(source)
	m, _ = updateModel(t, m, tea.WindowSizeMsg{
		Width: windowWidth, Height: windowHeight,
	})
	m, _ = updateModel(t, m, chatsLoadedMsg{chats: []Chat{{ID: 7, Title: "A"}}})

	m, cmd := updateModel(t, m, press(tea.KeyEnter))
	m = runLoads(t, m, cmd)

	if got := len(m.selected().Messages); got != 30 {
		t.Fatalf("messages = %d, want the 30 of the two pages", got)
	}

	rows := feedOf(t, m)
	if gap := m.feedRows() - len(rows); gap > 1 {
		t.Fatalf(
			"the feed drew %d rows of the %d it has:\n%s",
			len(rows), m.feedRows(),
			strings.Join(viewLines(m.View()), "\n"),
		)
	}
	newest := m.selected().Messages[len(m.selected().Messages)-1]
	if !rowsEndWith(rows, messageOf(m, newest)) {
		t.Fatalf(
			"the last two rows are %q and %q, want the newest message (%q)",
			plain(rows[len(rows)-2]),
			plain(rows[len(rows)-1]),
			messageOf(m, newest),
		)
	}
}

// rowsEndWith reports whether the last two rows of a feed hold a message.
//
// A block of §4.4 ends with the half block of air under its words, so the
// newest message of a full feed is in the two rows that end it rather than
// in the last one. It is the same statement as before the air was drawn —
// the newest message is at the bottom of the feed — said about the two rows
// a block with one line of text in it now takes.
func rowsEndWith(rows []string, message string) bool {
	return rowHolds(rows, len(rows)-2, len(rows), message)
}

// rowHolds reports whether one of the rows between two of them holds a
// message.
func rowHolds(rows []string, from, to int, message string) bool {
	for _, row := range rows[maxInt(from, 0):minInt(to, len(rows))] {
		if strings.Contains(plain(row), message) {
			return true
		}
	}

	return false
}

// A page of older messages must not move the messages a reader is looking
// at. The window is only placed again when the conversation is being
// followed; a reader who walked the cursor up keeps every row.
func TestAnOlderPageDoesNotMoveTheWindowOfAReader(t *testing.T) {
	page := mixedPage(40)
	source := &recordingChatSource{pages: []HistoryPage{page}}
	m := openConversationWithHistory(t, source, 7, page)
	m, _ = updateModel(t, m, tea.WindowSizeMsg{
		Width: windowWidth, Height: windowHeight,
	})
	m.focus = FocusHistory
	// A few messages up from the newest, so the window is somewhere in the
	// middle of the conversation and not at either end of it.
	for range 3 {
		m, _ = updateModel(t, m, press(tea.KeyUp))
	}
	if m.timelineFollowsNewest() {
		t.Fatal("the conversation is still being followed, want a reader in it")
	}

	// And then to the oldest message, which is the gesture that asks for the
	// page above it.
	m = selectOldestMessage(t, m)

	before := feedOf(t, m)
	beforeTop := m.timelineTop

	older := olderPageOf(page)
	source.pages = append(source.pages, older)
	_, cmd := updateModel(t, m, press(tea.KeyUp))
	if cmd == nil {
		t.Fatal("↑ on the oldest loaded message did not ask for a page")
	}
	m = runLoads(t, m, cmd)

	if got := len(m.selected().Messages); got != len(page.Messages)+len(older.Messages) {
		t.Fatalf("messages = %d, want the older page on top", got)
	}
	if m.timelineTop != beforeTop+len(older.Messages) {
		t.Fatalf(
			"timelineTop = %d, want %d: the same message has to stay on top",
			m.timelineTop, beforeTop+len(older.Messages),
		)
	}

	after := feedOf(t, m)
	if len(after) != len(before) {
		t.Fatalf("the feed drew %d rows, want the %d it drew", len(after), len(before))
	}
	for index := range before {
		if plain(before[index]) != plain(after[index]) {
			t.Fatalf(
				"row %d moved when the older page arrived\n  before: %q\n  after:  %q",
				index+1, plain(before[index]), plain(after[index]),
			)
		}
	}
}

// A resize changes the rows the feed has, and a window that was following
// the conversation is filled again: a taller screen must not leave empty
// rows under the newest message, and a shorter one must not cut it off. A
// window a reader has scrolled away from keeps its message at the top
// (§10.5), which TestResizeKeepsTheScrollPosition says in the model.
func TestAResizeFillsTheWindowOfAConversationThatIsBeingFollowed(t *testing.T) {
	m := mixedConversation(t, 40)

	for _, size := range []tea.WindowSizeMsg{
		{Width: windowWidth, Height: windowHeight + 8},
		{Width: windowWidth, Height: windowHeight - 8},
		{Width: windowWidth, Height: windowHeight},
	} {
		m, _ = updateModel(t, m, size)
		rows := feedOf(t, m)
		// The gap row of the topmost message is the one row the feed may
		// be short by: it separates it from a message that is not on the
		// screen, so the view does not draw it.
		if gap := m.feedRows() - len(rows); gap > 1 {
			t.Fatalf(
				"a %dx%d feed drew %d rows of the %d it has",
				size.Width, size.Height, len(rows), m.feedRows(),
			)
		}

		newest := m.selected().Messages[len(m.selected().Messages)-1]
		if last := plain(rows[len(rows)-1]); !strings.Contains(last, newest.Text) {
			t.Fatalf(
				"a %dx%d feed does not end on the newest message: %q",
				size.Width, size.Height, last,
			)
		}
	}
}

// olderPageOf is the page above a page: the messages of count down to one,
// with identifiers below every message of the page it goes on top of.
func olderPageOf(page HistoryPage) HistoryPage {
	older := HistoryPage{HasMore: true}
	lowest := int64(0)
	for _, message := range page.Messages {
		if lowest == 0 || message.ID < lowest {
			lowest = message.ID
		}
	}

	for offset := int64(1); offset <= 10; offset++ {
		older.Messages = append(older.Messages, Message{
			ID:       lowest - offset,
			Time:     "11:50",
			Text:     fmt.Sprintf("older message %d", lowest-offset),
			Author:   "Anna Example",
			AuthorID: 5,
		})
	}

	return older
}

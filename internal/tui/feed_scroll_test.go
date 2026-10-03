package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"telecli/internal/tui/theme"
)

// This file is the track of the feed and the square that stands on it: the
// two things that say where in a conversation the reader is.
//
// The owner's words (03.10, a channel, on a real account): «чтобы перемотка
// была визуально хороша, нужно на всю длину бесцветным полупрозрачным
// фоном, и справа по всей длине на конце фона — красный квадрат, чтобы видеть
// лучше». Before this the feed said nothing: a reader of a long channel could
// not tell how much was above, how much was below, or whether the end of the
// conversation was on the screen at all.
//
// So the claims here are all about one number and about one column. The
// square is at the top of the track when the beginning of the conversation
// is open and at the bottom of it when the end is, and it is somewhere
// between the two where the reader is in between — which is only true of a
// square placed by the window the rows of the feed are drawn from, and not
// by a count of the messages above it. The track takes one column and takes
// it from the margin: no message is one column narrower, no block moves, and
// the row of the message under the cursor is the row it was. And on a
// terminal that has no colour for a surface the track is gone while the
// square is not, because the square is the one thing on the screen that says
// where the reader is.

// feedScrollOf returns what the view drew for the feed and the track along
// it: the rows of the area, the feed drawn into them, and the square's place
// on the track.
//
// It is conversationRegion's own arithmetic, in one place a test can ask it
// of, and the tests that are about the screen rather than about the geometry
// go through View() and read what a user would read.
func feedScrollOf(t *testing.T, m Model) (scrollTrack, []string) {
	t.Helper()

	layout := LayoutFor(m.width, m.height)
	width := layout.ChatContentWidth()

	rows := rowsOfTheFeed(t, m)
	track := m.timelineFeed(layout, width, len(rows)).window.trackOn(len(rows))

	return track, m.withScrollTrack(rows, track, width)
}

// markerRow returns the row of the track the square stands on, and whether
// there is a square on the track at all.
func markerRow(track scrollTrack) (int, bool) {
	if track.rows < 1 {
		return 0, false
	}

	return track.marker, true
}

// The end of the conversation is open — a chat that was just opened, a
// message that was just sent, `G` — and the square is on the last row of the
// track. Not near it and not one row above it: the whole claim of the track
// is that the last row of it answers «did I reach the end».
//
// The rows of the feed are not a whole number of messages, and the window is
// placed by the heights of the entries (timeline.go, anchoredAt), so the
// square standing one cut above the bottom was a thing that happened before
// the rows above it were counted right.
func TestTheSquareIsAtTheBottomOfTheTrackAtTheNewestMessage(t *testing.T) {
	m := mixedConversation(t, 40)
	m = selectOldestMessage(t, m)
	m, _ = updateModel(t, m, pressRunes("G"))

	track, rows := feedScrollOf(t, m)

	marker, ok := markerRow(track)
	if !ok {
		t.Fatalf("there is no track of %d rows on the screen", len(rows))
	}
	if marker != track.rows-1 {
		t.Errorf(
			"the square is on row %d of %d at the newest message, want the last "+
				"row of the track",
			marker, track.rows,
		)
	}

	if !strings.Contains(plain(rows[marker]), scrollMarkerGlyph) {
		t.Errorf("the row the square should be on has no square on it:\n%q", plain(rows[marker]))
	}
}

// The beginning of the loaded conversation is on the screen and the square is
// at the top of the track, for the same reason the other end is at the
// bottom: the two ends are the two things a reader of a long chat cannot
// otherwise tell.
func TestTheSquareIsAtTheTopOfTheTrackAtTheOldestLoadedMessage(t *testing.T) {
	m := selectOldestMessage(t, mixedConversation(t, 40))

	track, rows := feedScrollOf(t, m)

	marker, ok := markerRow(track)
	if !ok {
		t.Fatalf("there is no track of %d rows on the screen", len(rows))
	}
	if marker != 0 {
		t.Errorf(
			"the square is on row %d of %d at the oldest message, want the first "+
				"row of the track",
			marker, track.rows,
		)
	}

	if !strings.Contains(plain(rows[marker]), scrollMarkerGlyph) {
		t.Errorf("the row the square should be on has no square on it:\n%q", plain(rows[marker]))
	}
}

// A reader in the middle of a conversation is somewhere in the middle of the
// track, and the square follows them up and down. Both halves matter: a
// square that is always in the middle says nothing, and a square that does
// not move with the window says something that is not true about it.
//
// The window is moved by a page of keys rather than by a few messages,
// because a few messages inside the window do not move the window at all
// (scrollCursorIntoView) — and the square must not either.
func TestTheSquareIsBetweenTheTwoEndsAndFollowsTheWindow(t *testing.T) {
	m := mixedConversation(t, 40)
	m = selectOldestMessage(t, m)
	m, _ = updateModel(t, m, pressRunes("G"))

	// Halfway up the conversation, in messages of its own.
	for range 20 {
		m, _ = updateModel(t, m, press(tea.KeyUp))
	}

	track, rows := feedScrollOf(t, m)

	marker, ok := markerRow(track)
	if !ok {
		t.Fatalf("there is no track of %d rows on the screen", len(rows))
	}
	if marker <= 0 || marker >= track.rows-1 {
		t.Fatalf(
			"the square is on row %d of %d in the middle of a conversation of %d, "+
				"want it between the two ends",
			marker, track.rows, len(m.selected().Messages),
		)
	}

	// A page down moves the window down, and the square with it.
	m, _ = updateModel(t, m, press(tea.KeyPgDown))

	down, _ := feedScrollOf(t, m)
	downMarker, ok := markerRow(down)
	if !ok {
		t.Fatal("the track is gone after a page of keys")
	}
	if downMarker <= marker {
		t.Errorf(
			"a page down moved the square from row %d to row %d of the track",
			marker, downMarker,
		)
	}

	// And a page up moves it back the other way.
	m, _ = updateModel(t, m, press(tea.KeyPgUp))

	up, _ := feedScrollOf(t, m)
	upMarker, _ := markerRow(up)
	if upMarker >= downMarker {
		t.Errorf(
			"a page up moved the square from row %d to row %d of the track",
			downMarker, upMarker,
		)
	}
}

// The square is on the row of the newest message, on the screen a user reads.
// Everything above is geometry; this is the claim itself, and it is the one
// that would fail if the square were placed by a second measurement of the
// conversation: the rows would be drawn from one window and the square would
// stand somewhere else entirely, and nothing in the package could see it.
func TestTheSquareIsFoundOnTheRowOfTheNewestMessage(t *testing.T) {
	m := mixedConversation(t, 40)
	m = selectOldestMessage(t, m)
	m, _ = updateModel(t, m, pressRunes("G"))

	messages := m.selected().Messages
	newest := messages[len(messages)-1]

	// The last row the newest message is on: a label of a message of this
	// page belongs to an album of it as well, and the row that carries the
	// square is the last one of the feed.
	row, ok := lastRowOf(plain(m.View()), messageOf(m, newest))
	if !ok {
		t.Fatalf("the newest message is not on the screen:\n%s", plain(m.View()))
	}

	if square, found := lastRowOf(plain(m.View()), scrollMarkerGlyph); !found {
		t.Fatalf("there is no square on the screen:\n%s", plain(m.View()))
	} else if square != row {
		t.Errorf(
			"the square is on row %d and the newest message is on row %d:\n%s",
			square, row, plain(m.View()),
		)
	}
}

// lastRowOf returns the last row of a screen a piece of text is on, and
// whether it is on it at all.
func lastRowOf(view, text string) (int, bool) {
	found, ok := 0, false

	for index, row := range viewLines(view) {
		if strings.Contains(row, text) {
			found, ok = index, true
		}
	}

	return found, ok
}

// The track takes the last column of a row of the feed and nothing else. A
// row the conversation did not fill is the other half: it is not a row of
// anything until the track gives it the width of the pane, because a square
// or a stripe written at the first column of such a row would be down the
// left of the conversation on every screen whose chat is shorter than its
// feed.
func TestTheTrackTakesTheLastColumnAndNothingElse(t *testing.T) {
	m := mixedConversation(t, 40)

	layout := LayoutFor(m.width, m.height)
	width := layout.ChatContentWidth()
	drawn := rowsOfTheFeed(t, m)

	track, painted := feedScrollOf(t, m)
	if len(painted) != len(drawn) {
		t.Fatalf("the feed drew %d rows and the track %d", len(drawn), len(painted))
	}

	for index, row := range painted {
		if got := m.widths.StringWidth(row); got != width {
			t.Errorf(
				"row %d of the feed is %d columns wide with the track on it, want %d",
				index, got, width,
			)
		}

		// What a reader sees is what is compared: the columns before the
		// track are the row the view drew, and the escape sequences over
		// them are the view's own.
		if head, want := m.headOf(row, width), m.headOf(drawn[index], width); head != want {
			t.Errorf(
				"the track changed row %d of the feed before its own column:\n%q\n%q",
				index, want, head,
			)
		}
	}

	// And the square is one of them: a row of the track that carries it
	// says so, and there is exactly one such row.
	squares := 0

	for index, row := range painted {
		if !strings.Contains(plain(row), scrollMarkerGlyph) {
			continue
		}

		squares++
		if !track.markerOn(index) {
			t.Errorf(
				"row %d of the feed has the square on it and the track says the square "+
					"is on row %d",
				index, track.marker,
			)
		}
	}

	if squares != 1 {
		t.Errorf("the square is on %d rows of the feed, want exactly one", squares)
	}
}

// The track stands in the margin of the feed, so no message is one column
// narrower for it and no block reaches it. The margin of the feed is three
// columns on a two-pane screen and one on a single-pane one, and the track is
// in the last of those — which is the only column of the pane that no block
// of a message can reach.
func TestNoBlockOfAMessageReachesTheColumnOfTheTrack(t *testing.T) {
	for _, size := range [][2]int{{windowWidth, windowHeight}, {60, windowHeight}} {
		m := mixedConversation(t, 40)
		m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})

		layout := LayoutFor(m.width, m.height)
		width := layout.ChatContentWidth()

		for _, side := range []messageSide{sideIncoming, sideOutgoing} {
			block := m.messageBlockFor(side, layout, width, "the text of the message", true)

			if end := block.offset + block.width; end > width-scrollTrackColumns {
				t.Errorf(
					"%dx%d: the block of a message of the %s side ends at column %d "+
						"of %d, and the track is the column after that",
					size[0], size[1], side, end, width,
				)
			}
		}
	}
}

// A conversation with nothing in it has no track: there is nothing to say
// about a position in a conversation that has not been read yet, and a
// column of the theme's own background beside an empty feed is a stripe
// where the feed says «No messages yet».
func TestTheTrackIsNotDrawnWithoutMessages(t *testing.T) {
	m := mixedConversation(t, 40)
	m = m.dropAllMessages()

	track, rows := feedScrollOf(t, m)

	if marker, ok := markerRow(track); ok {
		t.Errorf("a feed of %d rows has a square on row %d and no conversation", len(rows), marker)
	}

	for index, row := range rows {
		if strings.Contains(plain(row), scrollMarkerGlyph) {
			t.Errorf("row %d of an empty feed has a square on it:\n%q", index, plain(row))
		}
	}
}

// dropAllMessages leaves the conversation open with nothing in it.
func (m Model) dropAllMessages() Model {
	chat := m.selected()
	chat.Messages = nil
	m.chats[m.selectedChat] = chat

	return m
}

// On a terminal that has sixteen colours or none, a surface is the terminal's
// own background (§2.7) and the track is a surface, so the track is not
// drawn. The square is not a surface — it is a colour of words on one — and
// it is the one thing on the screen that says where the reader is, so it has
// to survive the profile that took the rest of the track away.
func TestTheSquareSurvivesTheProfileThatHasNoBackgroundForTheTrack(t *testing.T) {
	for _, testCase := range []struct {
		name string
		m    Model
	}{
		{name: "sixteen colours", m: coloredAt(t, theme.ProfileANSI16)},
		{name: "no colour", m: uncolored(mixedConversation(t, 40))},
	} {
		screen, row := screenRowOf(t, testCase.m, scrollMarkerGlyph)

		// The track is gone with the surfaces: what is left of a row of
		// it is the terminal's own background and nothing else, so the
		// square is the last thing written on the row and it is there.
		line := plain(screen[row])
		if !strings.HasSuffix(line, scrollMarkerGlyph) {
			t.Errorf(
				"profile %s: the square on row %d is not the last thing on the row:\n%q",
				testCase.name, row, line,
			)
		}
	}
}

// coloredAt is a conversation of the mock page drawn at the size of the
// window tests and under the given profile.
func coloredAt(t *testing.T, profile theme.Profile) Model {
	t.Helper()

	m := mixedConversation(t, 40)
	built := theme.DefaultTheme().ForProfile(profile)
	m.theme = built
	m.colorProfile = profile
	m.rendererForProfile = nil

	return m
}

// headOf is a row of the feed without the column of the track: what a reader
// reads of it, padded to the width the track is drawn at.
func (m Model) headOf(row string, width int) string {
	head := m.widths.Truncate(plain(row), width-scrollTrackColumns, "")

	return head + spaces(
		maxInt(width-scrollTrackColumns-m.widths.StringWidth(head), 0),
	)
}

// The two defects the owner found on the live account on 03.10, walking up a
// channel of long messages with k: an area of empty rows under the messages
// where the window had stopped in front of a message taller than the whole
// feed, and neither the track nor the square anywhere on that area.
//
// The walk goes up one message at a time, because that is the gesture the
// owner used and because it is the one that puts the cursor above the window —
// the state the empty rows were found in.

// tallConversation opens a conversation of short messages with one long one in
// the middle of it, and puts the cursor in the history.
//
// lines is how many lines the long message is: twenty is taller than half of
// the feed of the window tests and forty-five is taller than all of it.
func tallConversation(t *testing.T, lines int) Model {
	t.Helper()

	page := HistoryPage{HasMore: false}
	for index := 12; index >= 1; index-- {
		message := Message{
			ID:     int64(index),
			At:     mockMoment(fmt.Sprintf("12:%02d", index)),
			Text:   fmt.Sprintf("message %d", index),
			Author: "Anna Example", AuthorID: 5,
		}
		if index == 6 {
			message.Text = strings.Repeat("a long line of the long message ", lines)
		}

		page.Messages = append(page.Messages, message)
	}

	source := &recordingChatSource{pages: []HistoryPage{page}}
	m := openConversationWithHistory(t, source, 7, page)
	m, _ = updateModel(t, m, tea.WindowSizeMsg{
		Width: windowWidth, Height: windowHeight,
	})
	m = withMockClock(m).scrollToNewest()
	m.focus = FocusHistory

	return m
}

// feedWalk returns the rows the window's own walk filled and the entries it
// drew, which is the walk of the view (timelineLines).
//
// What is compared here is rows and entries rather than words, because a row
// of the feed with nothing written in it is not necessarily an empty one: a
// block of a message with two rows of text has a row of its own background
// above and below the words, and the gap between two messages is a row of the
// background of the feed. The rows of the window are counted by the walk that
// draws them, which is the only count that agrees with the screen.
func feedWalk(t *testing.T, m Model) (rows []string, drawn []timelineEntry) {
	t.Helper()

	layout := LayoutFor(m.width, m.height)
	width := layout.ChatContentWidth()
	total := m.timelineTotal()
	if total == 0 {
		return nil, nil
	}

	entries := m.feedEntries()
	top := entryIndexOfFeed(entries, clampIndex(m.timelineTop, total-1))

	filled, drawnEntries, _ := m.entryRowsFrom(
		entries, top, layout, width, m.feedRows(), m.styles(), m.timelineCut,
	)

	return filled, drawnEntries
}

// The window of the feed is filled with messages whatever the height of them,
// and the walk is a `k` at a time through a conversation with a message
// taller than a good part of the window.
//
// This is the owner's screen of 03.10: one `k` above a long message and the
// feed was that one message, thirty rows of nothing under it, and no track
// and no square anywhere on the empty part. The walk used to stop in front of
// the message that did not fit and leave the rows it had left as air, which
// is the right answer for a conversation of one-line messages — the air is
// the gap between two of them and is a row of it — and is the wrong answer
// the moment a message does not fit in what is left of the window. The entry
// is cut at the rows that are left now, and the head of it is what is kept,
// which is what the walk has always done with the entry a window opens on.
func TestTheWindowIsFilledWhateverTheHeightOfTheMessages(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		lines int
	}{
		{name: "a message taller than half of the feed", lines: 20},
		{name: "a message taller than the whole feed", lines: 45},
	} {
		m := tallConversation(t, testCase.lines)
		feed := m.feedRows()

		for step := range 12 {
			rows, drawn := feedWalk(t, m)
			if len(rows) < feed && len(drawn) < len(m.feedEntries()) {
				t.Errorf(
					"%s, step %d (cursor on message %d): the window filled %d of the "+
						"%d rows of the feed and there were messages left to draw",
					testCase.name, step, m.selectedMsg, len(rows), feed,
				)
			}

			// And the track and the square are on the screen at every
			// step: they went missing with the air.
			if _, found := lastRowOf(plain(m.View()), scrollMarkerGlyph); !found {
				t.Errorf(
					"%s, step %d: there is no square on the screen:\n%s",
					testCase.name, step, plain(m.View()),
				)
			}

			m, _ = updateModel(t, m, press(tea.KeyUp))
		}
	}
}

// The track is drawn wherever the feed is drawn, including the rows a
// conversation shorter than its feed leaves: those rows are rows of the feed
// as much as the rows with a message in them, and a track that stopped where
// the messages stop is a track with a hole in it (owner, 03.10, the screen
// where neither the track nor the square was to be seen).
//
// It is asked of the screen and not of the drawing, because the drawing is
// where the order of the two passes is decided: the track goes on after the
// rows the conversation did not fill have been put where they belong, and a
// test of withScrollTrack on its own cannot see that.
func TestTheTrackIsPaintedOnTheRowsTheConversationDoesNotFill(t *testing.T) {
	m := openedProgramModel(t, theme.ProfileTrueColor, windowWidth, windowHeight)
	m.focus = FocusHistory
	m.chats[0].Messages = []Message{{
		ID: 1, Text: "the only message of the chat", At: mockMoment("12:01"),
		Author: "Anna Example", AuthorID: 5,
	}}

	layout := LayoutFor(m.width, m.height)
	area := m.historyFeedRows(layout, layout.ChatContentWidth())

	// One message in an area of this many rows: the rows it does not fill
	// are the air of §8.3, and they are rows of the feed all the same.
	body := rowsOfTheFeed(t, m)
	if len(body) != area {
		t.Fatalf("the area of the feed is %d rows and the view drew %d", area, len(body))
	}

	// The track is the background of one cell on the last column of a row,
	// and no other thing on the screen is painted in that colour: the blocks
	// of messages are on ComposerBackground, which is a step below it. The
	// style of one cell of the track is the style the view writes it with.
	track := strings.TrimSuffix(m.styles().scrollTrack().Render("x"), "x\x1b[0m")
	cells, squares := 0, 0

	for _, row := range viewLines(m.View()) {
		if strings.Contains(row, track) {
			cells++
		}

		if strings.Contains(row, scrollMarkerGlyph) {
			squares++
		}
	}

	// One row of the area carries the square rather than a cell of the
	// track: the square is punched through the track and stands on the
	// background of the feed (styles.go, scrollMarker). Every other row of
	// the area carries a cell of the track, including the rows the
	// conversation did not fill.
	if cells != area-1 || squares != 1 {
		t.Errorf(
			"an area of %d rows has the track on %d of them and the square on %d: "+
				"want the track on %d and the square on one",
			area, cells, squares, area-1,
		)
	}

	// And the square is at the bottom of the track: the whole conversation
	// is on the screen, so the reader is at its end.
	if _, found := lastRowOf(plain(m.View()), scrollMarkerGlyph); !found {
		t.Errorf("there is no square on the screen:\n%s", plain(m.View()))
	}
}

// The square stands where the message under the cursor is, not where the
// window begins: the cursor walks inside one window without moving it, and a
// square placed by the window stands still while the reader walks up and down
// the same screenful (owner, 03.10: «маркер стоит не там»).
//
// The window is the same one in every step below — the model says so — so the
// square that moves is the only thing on the screen that answered the keys.
func TestTheSquareFollowsTheMessageUnderTheCursor(t *testing.T) {
	// maxInt is the row above every row of a track, so the first step has
	// nothing to compare itself with.
	const maxInt = 1 << 30

	m := mixedConversation(t, 40)
	m.focus = FocusHistory

	window := m.timelineTop
	seen := map[int]bool{}
	previous := maxInt

	for range 8 {
		m, _ = updateModel(t, m, press(tea.KeyUp))

		if m.timelineTop != window {
			break
		}

		track, rows := feedScrollOf(t, m)
		marker, ok := markerRow(track)
		if !ok {
			t.Fatal("the track is gone while the cursor walks inside the window")
		}
		if !strings.Contains(plain(rows[marker]), scrollMarkerGlyph) {
			t.Errorf("row %d of the feed should carry the square:\n%q", marker, plain(rows[marker]))
		}

		// The track is coarser than the conversation — three rows of
		// messages are about one row of it — so the square does not move
		// on every key. It does not go down while the reader walks up
		// either.
		if marker > previous {
			t.Errorf(
				"the square went from row %d to row %d of the track while the cursor "+
					"walked up",
				previous, marker,
			)
		}

		previous = marker
		seen[marker] = true
	}

	if len(seen) < 2 {
		t.Fatalf(
			"the square stood on %d rows while the cursor walked inside one window: "+
				"it is placed by the window and not by the message under the cursor",
			len(seen),
		)
	}
}

// The newest message is the end of the conversation, so a reader on it is at
// the bottom of the track — whatever height that message is, and however much
// of it is on the screen. A message taller than the feed is the case where the
// two answers differ: its first row is nowhere near the end of it.
func TestTheSquareIsAtTheBottomWhenTheNewestMessageIsTallerThanTheFeed(t *testing.T) {
	m := tallConversation(t, 45)
	m = selectOldestMessage(t, m)
	m, _ = updateModel(t, m, pressRunes("G"))

	track, rows := feedScrollOf(t, m)

	marker, ok := markerRow(track)
	if !ok {
		t.Fatalf("there is no track of %d rows on the screen", len(rows))
	}
	if marker != track.rows-1 {
		t.Errorf(
			"the square is on row %d of %d with the newest message focused, want the "+
				"last row of the track",
			marker, track.rows,
		)
	}
}

// The oldest loaded message is the beginning of the conversation, so a reader
// on it is at the top of the track.
func TestTheSquareIsAtTheTopWhenTheOldestLoadedMessageIsFocused(t *testing.T) {
	m := tallConversation(t, 20)
	m = selectOldestMessage(t, m)

	track, rows := feedScrollOf(t, m)

	marker, ok := markerRow(track)
	if !ok {
		t.Fatalf("there is no track of %d rows on the screen", len(rows))
	}
	if marker != 0 {
		t.Errorf(
			"the square is on row %d of %d with the oldest message focused, want the "+
				"first row of the track",
			marker, track.rows,
		)
	}
}

package tui

import (
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

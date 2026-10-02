package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"telecli/internal/tui/termwidth"
	"telecli/internal/tui/theme"
)

// The size every screen of this file is drawn at: wide enough for a
// conversation pane with a list beside it, and tall enough for the rows of
// the window tests below.
const (
	separatorWidth  = 120
	separatorHeight = 30
)

// The rows of the feed that belong to no message: the name of the day a
// message is on, and the line where the unread messages of a chat begin.
//
// The two are the same kind of row and are drawn the same way — a pill
// centred in the feed, its words in the bright step of the text ramp, with a
// row of air above and below it — and they are tested here together because
// they are the whole of "what happened when, and what I have not read yet"
// that a conversation of messages alone cannot say.

// separatorZone and separatorNow are the zone and the moment every case of
// this file is drawn at: noon on Friday 2 October 2026, ten hours east of
// Greenwich. The zone is a real one on purpose — a fixed offset would be
// enough for the arithmetic and would not catch a moment read in UTC, which
// is the bug these rows were added next to.
var (
	separatorZone = time.FixedZone("UTC+10", 10*60*60)
	separatorNow  = time.Date(2026, 10, 2, 12, 0, 0, 0, separatorZone)
)

// at returns a moment of a day of the fixture, in its zone.
func at(day int, month time.Month, hour, minute int) time.Time {
	return time.Date(2026, month, day, hour, minute, 0, 0, separatorZone)
}

// newestFirst returns the messages of a conversation in the order TDLib
// answers a history request with them.
func newestFirst(messages []Message) []Message {
	answer := make([]Message, 0, len(messages))
	for index := len(messages) - 1; index >= 0; index-- {
		answer = append(answer, messages[index])
	}

	return answer
}

// dayAbove returns the day named in the row above the block of the given
// message, and whether there was one.
//
// It walks up over the rows of the block itself — the name of the sender, and
// however many rows its text wraps into — because a separator stands above
// the whole block and not above its text.
func dayAbove(t *testing.T, m Model, message string) (string, bool) {
	t.Helper()

	view := plain(m.View())
	_, row, ok := lineWith(view, message)
	if !ok {
		t.Fatalf("the message %q is not on the screen:\n%s", message, view)
	}

	lines := viewLines(view)
	for index := row - 1; index >= 0; index-- {
		text := strings.TrimSpace(plain(lines[index]))
		if text == "" || strings.HasPrefix(text, "Enter") ||
			strings.Contains(text, "Write a message") {
			continue
		}
		// The name of the sender and the text of the message are the block.
		if text == messageAuthor(Message{Author: "Anna"}) ||
			strings.Contains(text, "Anna  ") || strings.HasPrefix(text, "›") {
			continue
		}

		return text, true
	}

	return "", false
}

// separatorsModel is a conversation of the given messages, opened, with the
// clock and the zone of this file pinned.
//
// unread is the read pointer of the chat as it was when the chat was opened,
// which is the only moment the pointer is read (see the model). It is a
// parameter rather than a field of the fixture because a chat with nothing
// unread is the case the divider has to be absent from.
func separatorsModel(
	t *testing.T,
	messages []Message,
	unread int64,
) Model {
	t.Helper()

	// A page arrives the way TDLib sends it: newest first. The model
	// reverses it where it lands, so the fixture above — written oldest
	// first, because that is how a conversation is read — is reversed here
	// rather than in every case of this file.
	page := HistoryPage{Messages: newestFirst(messages)}
	source := &recordingChatSource{pages: []HistoryPage{page}}

	m := NewModelWithSource(source)
	m.widths = termwidth.Unmeasured(termwidth.ModeAuto)
	m.theme = theme.DefaultTheme().ForProfile(theme.ProfileNoColor)
	m.colorProfile = theme.ProfileNoColor
	m.rendererForProfile = newRenderer(theme.ProfileNoColor)

	m.chats = []Chat{{
		ID: 7, Title: "Anna Example",
		LastReadInboxMessageID: unread,
	}}
	m.chatsState = loadStateLoaded
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: separatorWidth, Height: separatorHeight})
	m, _ = updateModel(t, m, press(tea.KeyEnter))
	m, _ = updateModel(t, m, historyLoadedMsg{
		chatID:    7,
		operation: m.historyOperation,
		page:      page,
	})

	return withClock(m.normalizeTimeline().scrollToNewest(), separatorNow, separatorZone)
}

// A conversation of one day says that day once, above its first message. A
// feed that named the day in the header of every message would be a column of
// labels; a feed that named it nowhere is the report of the owner: a
// conversation of three days with no boundary anywhere in it.
func TestTheFeedNamesTheDayAboveItsFirstMessage(t *testing.T) {
	m := separatorsModel(t, []Message{
		{ID: 1, Text: "yesterday's first", At: at(1, time.October, 9, 0), Author: "Anna"},
		{ID: 2, Text: "yesterday's second", At: at(1, time.October, 9, 5), Author: "Anna"},
		{ID: 3, Text: "today's first", At: at(2, time.October, 8, 0), Author: "Anna"},
		{ID: 4, Text: "today's second", At: at(2, time.October, 8, 30), Author: "Anna"},
	}, 0)

	for _, testCase := range []struct {
		message string
		want    string
	}{
		{message: "yesterday's first", want: "Yesterday"},
		{message: "today's first", want: "Today"},
	} {
		got, ok := dayAbove(t, m, testCase.message)
		if !ok {
			t.Fatalf(
				"there is nothing above the message %q:\n%s",
				testCase.message, plain(m.View()),
			)
		}
		if got != testCase.want {
			t.Errorf(
				"the row above %q is %q, want %q",
				testCase.message, got, testCase.want,
			)
		}
	}
}

// Two messages of one day share the name of it: the divider is a boundary,
// and a divider in the middle of a day is a divider between two messages
// that belong together.
func TestTwoMessagesOfOneDayShareItsName(t *testing.T) {
	m := separatorsModel(t, []Message{
		{ID: 1, Text: "the first", At: at(2, time.October, 8, 0), Author: "Anna"},
		{ID: 2, Text: "the second", At: at(2, time.October, 8, 30), Author: "Anna"},
		{ID: 3, Text: "the third", At: at(2, time.October, 9, 0), Author: "Anna"},
	}, 0)

	view := plain(m.View())
	if got := strings.Count(view, "Today"); got != 1 {
		t.Fatalf(
			"the feed names the day %d times, want once:\n%s", got, view,
		)
	}
}

// Midnight is the boundary, and it is the midnight of the reader: a message
// sent at 23:50 and one sent at 00:10 in the zone ten hours east are twenty
// minutes apart and on different days, and the reader has to be able to see
// that.
func TestTheBoundaryIsTheMidnightOfTheReader(t *testing.T) {
	m := separatorsModel(t, []Message{
		{ID: 1, Text: "just before midnight", At: at(1, time.October, 23, 50), Author: "Anna"},
		{ID: 2, Text: "just after midnight", At: at(2, time.October, 0, 10), Author: "Anna"},
	}, 0)

	for _, testCase := range []struct {
		message string
		want    string
	}{
		{message: "just before midnight", want: "Yesterday"},
		{message: "just after midnight", want: "Today"},
	} {
		got, ok := dayAbove(t, m, testCase.message)
		if !ok {
			t.Fatalf("there is nothing above %q:\n%s", testCase.message, plain(m.View()))
		}
		if got != testCase.want {
			t.Errorf("the row above %q is %q, want %q", testCase.message, got, testCase.want)
		}
	}
}

// A message of the queue is a message of the conversation and is placed by
// its own moment, so a row of yesterday's between two of today's opens the
// day it is on. A queue that ignored the day would draw "Today" above a
// message of a conversation that began before midnight.
func TestAMessageOfTheQueueOpensTheDayItIsOn(t *testing.T) {
	m := separatorsModel(t, []Message{
		{ID: 1, Text: "written today", At: at(2, time.October, 8, 0), Author: "Anna"},
		{ID: 2, Text: "written today as well", At: at(2, time.October, 9, 0), Author: "Anna"},
	}, 0)

	m.pending = []PendingMessage{{
		EntryID:   "entry-fixture",
		ChatID:    7,
		Text:      "left overnight",
		State:     MessageDeliveryQueued,
		CreatedAt: at(1, time.October, 22, 0),
	}}
	m = m.normalizeTimeline().scrollToNewest()

	got, ok := dayAbove(t, m, "left overnight")
	if !ok {
		t.Fatalf("there is nothing above the queued message:\n%s", plain(m.View()))
	}
	if got != "Yesterday" {
		t.Errorf(
			"the row above the queued message is %q, want Yesterday", got,
		)
	}
}

// The unread line stands above the first incoming message past the read
// pointer of the chat, and nowhere else. Telegram's pointer is the last
// message it has been told was read, so everything below it is what the
// account holder has not read yet.
func TestTheUnreadLineStandsAboveTheFirstMessagePastTheReadPointer(t *testing.T) {
	m := separatorsModel(t, []Message{
		{ID: 10, Text: "read long ago", At: at(1, time.October, 9, 0), Author: "Anna"},
		{ID: 11, Text: "read as well", At: at(1, time.October, 9, 1), Author: "Anna"},
		{ID: 12, Text: "not read yet", At: at(2, time.October, 8, 0), Author: "Anna"},
		{ID: 13, Text: "nor this one", At: at(2, time.October, 8, 1), Author: "Anna"},
	}, 11)

	view := plain(m.View())
	if got := strings.Count(view, unreadSeparatorText); got != 1 {
		t.Fatalf(
			"the feed says %q %d times, want once:\n%s",
			unreadSeparatorText, got, view,
		)
	}

	line, unread, ok := lineWith(view, "not read yet")
	if !ok {
		t.Fatalf("the unread message is not on the screen:\n%s", view)
	}
	_, read, _ := lineWith(view, "read as well")
	if unread <= read {
		t.Errorf(
			"the unread message is on row %d and the last read one on row %d:\n%s",
			unread, read, view,
		)
	}

	// The line is above the message it is about, not below it.
	rows := viewLines(view)
	found := -1
	for index, row := range rows {
		if strings.Contains(row, unreadSeparatorText) {
			found = index

			break
		}
	}
	if found < 0 || found > unread {
		t.Fatalf(
			"the unread line is on row %d and its message on row %d, want the "+
				"line above it:\n%s",
			found+1, unread, view,
		)
	}
	_ = line
}

// A message of this user above the pointer is not unread: the read pointer
// is the last message Telegram was told was read, and a message this account
// sent after that was written by somebody who had read up to there. A line
// above one's own message would be a line about a read nobody has to make.
func TestTheUnreadLineIsNotAboveAMessageOfThisUser(t *testing.T) {
	m := separatorsModel(t, []Message{
		{ID: 10, Outgoing: true, Text: "mine", At: at(1, time.October, 9, 0)},
		{ID: 11, Text: "theirs", At: at(1, time.October, 9, 1), Author: "Anna"},
	}, 10)

	view := plain(m.View())
	_, theirs, _ := lineWith(view, "theirs")
	rows := viewLines(view)

	for index, row := range rows {
		if strings.Contains(row, unreadSeparatorText) && index > theirs {
			t.Fatalf(
				"the unread line is on row %d, under their message on row %d:\n%s",
				index+1, theirs, view,
			)
		}
	}
}

// A chat with nothing read in it has no pointer, and a line over a
// conversation the account holder has read to the end is a claim about a
// read that nobody made.
func TestAChatWithNothingUnreadSaysNothingAboutIt(t *testing.T) {
	for name, boundary := range map[string]int64{
		"the pointer is zero":   0,
		"every message is read": 9999,
	} {
		t.Run(name, func(t *testing.T) {
			m := separatorsModel(t, []Message{
				{ID: 10, Text: "the only one", At: at(1, time.October, 9, 0), Author: "Anna"},
			}, boundary)

			if view := plain(m.View()); strings.Contains(view, unreadSeparatorText) {
				t.Fatalf(
					"the feed says %q about a conversation with nothing "+
						"unread:\n%s",
					unreadSeparatorText, view,
				)
			}
		})
	}
}

// The line stays where it was while the chat is open, even though this
// program tells Telegram what is on the screen and the pointer moves as a
// result. A line that went away as soon as the reader looked at the messages
// it stands over would be a line that says nothing about what they had not
// read — and the owner of this program asked for it in Telegram because
// Telegram's own counter falls while he is looking.
func TestTheUnreadLineStaysWhileTheChatIsOpen(t *testing.T) {
	m := separatorsModel(t, []Message{
		{ID: 10, Text: "read", At: at(1, time.October, 9, 0), Author: "Anna"},
		{ID: 11, Text: "unread", At: at(1, time.October, 9, 1), Author: "Anna"},
	}, 10)

	if view := plain(m.View()); !strings.Contains(view, unreadSeparatorText) {
		t.Fatalf("the line is not on the screen to begin with:\n%s", view)
	}

	// The read the window sends, and the answer of Telegram that the read
	// worked: the chat list comes back with the pointer moved to the newest
	// message, because it was read.
	m.chats[m.selectedChat].LastReadInboxMessageID = 11
	m, _ = updateModel(t, m, messagesViewedMsg{chatID: 7})

	if view := plain(m.View()); !strings.Contains(view, unreadSeparatorText) {
		t.Fatalf(
			"the line went away after the read that was its own cause:\n%s",
			view,
		)
	}
}

// The pointer is read when the chat is opened and not before: it is the
// state of the chat at the moment the reader walked into it, and reading it
// again after this program has told Telegram what was on the screen would
// move it past the very messages the line stands over.
func TestThePointerIsReadWhenTheChatIsOpened(t *testing.T) {
	page := HistoryPage{Messages: newestFirst([]Message{
		{ID: 10, Text: "the one", At: at(1, time.October, 9, 0), Author: "Anna"},
	})}

	m := NewModelWithSource(&recordingChatSource{pages: []HistoryPage{page}})
	m.chats = []Chat{{
		ID: 7, Title: "Anna Example", LastReadInboxMessageID: 9,
	}}
	m.chatsState = loadStateLoaded
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: separatorWidth, Height: separatorHeight})
	m, _ = updateModel(t, m, press(tea.KeyEnter))

	if m.unreadBoundary != 9 {
		t.Errorf(
			"unreadBoundary = %d after opening the chat, want the 9 the row "+
				"of the chat carried", m.unreadBoundary,
		)
	}

	// And it is not read again: the chat list comes back from Telegram with
	// the pointer at the newest message, because the read of the window
	// moved it, and the line the reader came for is still there.
	m, _ = updateModel(t, m, chatsLoadedMsg{chats: []Chat{{
		ID: 7, Title: "Anna Example", LastReadInboxMessageID: 10,
	}}})

	if m.unreadBoundary != 9 {
		t.Errorf(
			"unreadBoundary = %d after the list was read again, want the 9 "+
				"the chat was opened with", m.unreadBoundary,
		)
	}
}

// A separator is a row of the feed and not decoration over it: the window is
// placed by the heights the entries really take, and an entry that carries a
// day of its own takes it with it. A window placed by the messages alone
// would be a row too low for every day in the conversation, and the rows
// above the first message would hold nothing — which is the whole of #57.
func TestTheWindowIsFilledWithTheSeparatorsInIt(t *testing.T) {
	messages := make([]Message, 0, 24)
	for day := 26; day <= 29; day++ {
		for index := 1; index <= 6; index++ {
			messages = append(messages, Message{
				ID:     int64(day*100 + index),
				Text:   "message",
				At:     at(day, time.September, 9, index),
				Author: "Anna", AuthorID: 5,
			})
		}
	}

	m := separatorsModel(t, messages, 0)

	rows := rowsOfTheFeed(t, m)
	// The rows of air above the first message are at most the row the
	// header takes: a feed that is a row short of its area at the top is a
	// feed with a row of nothing under the header.
	area := m.feedRows()
	first := 0
	for first < len(rows) && strings.TrimSpace(plain(rows[first])) == "" {
		first++
	}

	if gap := first; gap > 1 {
		t.Errorf(
			"the messages start on row %d of %d, want at most one empty row "+
				"above them:\n%s",
			gap+1, area, strings.Join(viewLines(m.View()), "\n"),
		)
	}

	// And every message of the conversation is inside the window, which is
	// what a separator in the measurement buys: a walk that ignored them
	// would have stopped a row short for each day.
	if got := len(rows); got != area {
		t.Errorf(
			"the feed drew %d rows of the %d it has, want it full",
			got, area,
		)
	}
}

// A separator is a PILL: a short run of columns with a background of its own,
// centred in the feed, with the words of the day in the bright end of the
// text ramp, and one row of air above and below it.
//
// It was a row of dim words at the left of the feed on the background of the
// feed, and the owner read it as a message line (02.10, real account): at the
// left edge, in the muted step and on no surface of its own, it has the shape
// of a row of the conversation and nothing on the screen says otherwise. So
// each of those four things is checked here rather than the colour alone,
// because it is the four of them together that makes it a pill: a centred
// word on the background of the feed is still words in the feed, and a pill
// of the colour of the blocks is a message with nothing in it.
func TestASeparatorIsAPillInTheMiddleOfTheFeed(t *testing.T) {
	// The colour of the pill is what this case is about, and a screen drawn
	// with no colour has none to be had.
	m := focusedOn(separatorsModel(t, []Message{
		{ID: 10, Text: "read", At: at(1, time.October, 9, 0), Author: "Anna", AuthorID: 5},
		{ID: 11, Text: "unread", At: at(1, time.October, 9, 1), Author: "Anna", AuthorID: 5},
	}, 10), FocusHistory)
	m.theme = theme.DefaultTheme().ForProfile(theme.ProfileTrueColor)
	m = m.withRenderer(theme.ProfileTrueColor)

	screen, index := screenRowOf(t, m, "Yesterday")

	row := screen[index]

	tokens := m.tokens()
	pill := backgroundParameters(
		m.styles().on(tokens.SeparatorBackground, m.styles().unstyled()).Render("x"),
	)
	feed := backgroundParameters(
		m.styles().on(tokens.ChatBackground, m.styles().unstyled()).Render("x"),
	)
	bright := foregroundParameters(m.styles().text(tokens.PrimaryText).Render("x"))

	if pill == feed {
		t.Fatalf(
			"the pill of the day has the background of the feed %q, want a "+
				"surface of its own",
			pill,
		)
	}

	// The pill is the run of columns on the surface of its own, and it is
	// the whole of that run that is measured: a row whose every column is on
	// the surface is a band across the feed, which says the whole row belongs
	// to one thing, and a day belongs to no message in it.
	cells := renderedCells(t, m, row)

	var (
		columns   int
		words     strings.Builder
		dimmed    bool
		firstCell = -1
	)
	for position, cell := range cells {
		if cell.background != pill {
			continue
		}

		if firstCell < 0 {
			firstCell = position
		}

		columns++
		words.WriteString(cell.text)

		if cell.foreground != bright {
			dimmed = true
		}
	}

	if columns == 0 {
		t.Fatalf(
			"no column of the row is on the surface of the pill %q:\n%s",
			pill, plain(row),
		)
	}

	if columns == len(cells) {
		t.Errorf(
			"the pill of the day is %d columns wide, which is the whole row: "+
				"want a pill, not a band across the feed",
			columns,
		)
	}

	// The words and one column of air on each side of them: a pill is the
	// shape of the words and the air around them, and words flush against
	// the edge of their own surface read as a band rather than a pill.
	if got := words.String(); got != " Yesterday " {
		t.Errorf(
			"the pill of the day holds %q, want %q",
			got, " Yesterday ",
		)
	}

	// And it sits in the middle: the air on either side of the pill in the
	// feed, not only somewhere to the right of the left edge.
	if before := firstCell; before < 1 || before+columns >= len(cells)-1 {
		t.Errorf(
			"the pill starts at column %d and ends at column %d of a row of "+
				"%d, want it in the middle of the feed",
			before+1, before+columns, len(cells),
		)
	}

	if dimmed {
		t.Errorf(
			"the words of the day are drawn in something other than the "+
				"bright step %q",
			bright,
		)
	}
}

// The pill of a day has air above and below it, and the air is the
// background of the feed.
//
// A pill flush against a message is a line of that message, which is the
// reading this is here to undo; and a pill with no air above it is stuck to
// whatever is over it.
func TestAPillHasARowOfAirAboveAndBelowIt(t *testing.T) {
	m := focusedOn(separatorsModel(t, []Message{
		{ID: 10, Text: "read", At: at(1, time.October, 9, 0), Author: "Anna", AuthorID: 5},
		{ID: 11, Text: "unread", At: at(1, time.October, 9, 1), Author: "Anna", AuthorID: 5},
	}, 10), FocusHistory)

	screen, index := screenRowOf(t, m, "Yesterday")

	// The air below is the row the pill itself brings, and the air above is
	// the gap row every entry of the feed has (entryLines), so the row above
	// is not spent twice.
	for _, offset := range []int{-1, 1} {
		neighbour := index + offset
		if neighbour < 0 || neighbour >= len(screen) {
			t.Fatalf(
				"the day has no row %+d: the pill is at the edge of the screen",
				offset,
			)
		}

		if got := strings.TrimSpace(plain(screen[neighbour])); got != "" {
			t.Errorf(
				"row %+d from the day holds %q, want the air of the background "+
					"of the feed",
				offset, got,
			)
		}
	}
}

// screenRowOf returns the screen of a conversation and the row of it that
// holds a word, failing the case when no row does.
func screenRowOf(t *testing.T, m Model, word string) ([]string, int) {
	t.Helper()

	screen := viewLines(m.View())
	for index, line := range screen {
		if strings.Contains(plain(line), word) {
			return screen, index
		}
	}

	t.Fatalf(
		"%q is not on the screen:\n%s",
		word, strings.Join(screen, "\n"),
	)

	return nil, -1
}

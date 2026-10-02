package tui

import (
	"strings"
	"testing"

	"telecli/internal/tui/theme"
)

// The number at the right end of the header of the chat list, and the mark
// of a pinned chat. Both are answers to questions about the whole list, and
// both were wrong or missing on 02.10: the header said "99+ unread" on an
// account whose rows held single digits, and a pinned chat stood at the top
// of the list with nothing on it to say why.

// counterChats is the list the counter is asked about: a person with two
// unread messages, a channel with twelve, a silenced chat with a hundred,
// and a chat that has been read.
//
// The three shapes are what the owner's account had: one chat, one channel,
// one chat a person had silenced, and the badges beside the names were small
// numbers while the header said "99+".
func counterChats() []Chat {
	return []Chat{
		{ID: 1, Title: "Anna Example", Unread: 2, At: mockMoment("12:07")},
		{ID: 2, Title: "Xiaomi News", Unread: 12, At: mockMoment("11:29"),
			Kind: ChatKindChannel},
		{ID: 3, Title: "Release Room", Unread: 100, At: mockMoment("11:20"),
			Kind: ChatKindGroup, Muted: true},
		{ID: 4, Title: "Notes", At: mockMoment("10:58")},
	}
}

// counterModel is a model over counterChats with the counter set as asked.
func counterModel(t *testing.T, mode UnreadCounterMode) Model {
	t.Helper()

	m := sizedModel(t, 120, 24)
	m.chats = counterChats()
	m.unreadCounter = mode

	return m
}

// The default counts the chats that have something unread in them, and
// leaves out the ones a person has silenced.
//
// Both halves are the point, and they are proved on one list: the silenced
// chat has a hundred unread messages in it, so a counter that counted
// messages or that ignored the mute would stand at a hundred and up.
func TestTheHeaderCountsTheUnreadChatsAndNotTheSilencedOnes(t *testing.T) {
	m := counterModel(t, UnreadCounterChats)

	total, counted := m.chatListUnreadTotal()
	if !counted {
		t.Fatal("the default counter counts nothing, want the chats")
	}
	if total != 2 {
		t.Fatalf("unread = %d, want 2 (Anna Example and Xiaomi News)", total)
	}

	header := chatListHeaderLines(t, m)[0]
	if !strings.Contains(header, "2 unread") {
		t.Fatalf("the header = %q, want the count of the chats", header)
	}
}

// The mode the setting asks for is the mode the header draws: messages is
// the sum the header used to be, and it is still there for a reader who
// wants to know how much is waiting rather than where.
func TestTheHeaderCanCountTheUnreadMessagesInstead(t *testing.T) {
	m := counterModel(t, UnreadCounterMessages)

	total, counted := m.chatListUnreadTotal()
	if !counted {
		t.Fatal("the messages counter counts nothing, want the sum")
	}
	if total != 114 {
		t.Fatalf("unread = %d, want 114 (every message, silenced chat included)", total)
	}

	if header := chatListHeaderLines(t, m)[0]; !strings.Contains(header, "99+ unread") {
		t.Fatalf(
			"the header = %q, want the sum of the messages, which is what 99+ is",
			header,
		)
	}
}

// Off draws no number at all, and the title of the pane is still there: the
// setting is about the count and not about the header.
func TestTheHeaderCanDrawNoUnreadCountAtAll(t *testing.T) {
	m := counterModel(t, UnreadCounterOff)

	if _, counted := m.chatListUnreadTotal(); counted {
		t.Fatal("the off counter counts, want no number on the screen")
	}

	header := chatListHeaderLines(t, m)[0]
	if !strings.Contains(header, chatListTitle) {
		t.Fatalf("the header = %q, want the title of the pane", header)
	}
	if strings.Contains(header, "unread") {
		t.Fatalf("the header = %q, want no count on it", header)
	}
}

// Nothing unread says so rather than drawing a zero: a number nobody has to
// add anything to is air where a number was.
func TestAHeaderWithNothingUnreadSaysSo(t *testing.T) {
	for _, mode := range []UnreadCounterMode{
		UnreadCounterChats, UnreadCounterMessages,
	} {
		m := counterModel(t, mode)
		m.chats = []Chat{{ID: 1, Title: "Notes", At: mockMoment("10:58")}}

		if header := chatListHeaderLines(t, m)[0]; !strings.Contains(header, "no unread") {
			t.Fatalf("%s: the header = %q, want it to say there is nothing unread", mode, header)
		}
	}
}

// A counter that only ever goes up is a counter nobody trusts: reading a
// chat on the phone takes the number down, which is the whole of what the
// setting is for.
func TestTheCountFallsWhenTheChatHasBeenRead(t *testing.T) {
	m := counterModel(t, UnreadCounterChats)

	before, _ := m.chatListUnreadTotal()

	m = m.applyLiveChats([]LiveChat{
		{ID: 1, Title: "Anna Example", At: mockMoment("12:09")},
		{ID: 2, Title: "Xiaomi News", Unread: 12, At: mockMoment("11:29"),
			Kind: ChatKindGroup},
		{ID: 3, Title: "Release Room", Unread: 100, At: mockMoment("11:20"),
			Kind: ChatKindGroup, Muted: true},
		{ID: 4, Title: "Notes", At: mockMoment("10:58")},
	})

	after, _ := m.chatListUnreadTotal()
	if after != before-1 {
		t.Fatalf("unread = %d, want %d after one chat was read", after, before-1)
	}
}

// The words of the setting, and what a file that names something else gets:
// an error with the three valid words in it, because a setting that is
// quietly ignored is found out about from a screen drawn wrong.
func TestTheUnreadCounterWordsAreTheOnesInTheConfiguration(t *testing.T) {
	for value, want := range map[string]UnreadCounterMode{
		"":         UnreadCounterChats,
		"chats":    UnreadCounterChats,
		"messages": UnreadCounterMessages,
		"off":      UnreadCounterOff,
		"CHATS":    UnreadCounterChats,
		" chats ":  UnreadCounterChats,
	} {
		got, err := ParseUnreadCounterMode(value)
		if err != nil {
			t.Fatalf("ParseUnreadCounterMode(%q): %v", value, err)
		}
		if got != want {
			t.Fatalf("ParseUnreadCounterMode(%q) = %v, want %v", value, got, want)
		}
		if got.String() != want.String() {
			t.Fatalf("%v.String() = %q, want %q", got, got.String(), want.String())
		}
	}

	_, err := ParseUnreadCounterMode("chats-and-messages")
	if err == nil {
		t.Fatal("an unknown word must be reported, not ignored")
	}
	for _, word := range []string{"chats", "messages", "off"} {
		if !strings.Contains(err.Error(), word) {
			t.Fatalf("the error %q does not name %q", err, word)
		}
	}

	if got := DescribeUnreadCounter(UnreadCounterOff); got != "off" {
		t.Fatalf("DescribeUnreadCounter(off) = %q, want off", got)
	}
}

// ---- The pinned chat ----

// pinnedChats is a list as Telegram orders the main list: the pinned chats
// first in the order of their own positions, then the rest by position
// order.
func pinnedChats() []Chat {
	return []Chat{
		{ID: 1, Title: "Anime & News", Unread: 3, Pinned: true,
			Preview: "the season has started", At: mockMoment("09:02")},
		{ID: 2, Title: "Anna Example", Unread: 2, Pinned: true,
			Preview: "the build is green again", At: mockMoment("12:07")},
		{ID: 3, Title: "Xiaomi News", Unread: 12,
			Preview: "the firmware is out", At: mockMoment("11:29"),
			Kind: ChatKindChannel},
		{ID: 4, Title: "Notes", Preview: "milk, bread, coffee",
			At: mockMoment("10:58")},
	}
}

// pinnedModel is a model over pinnedChats, drawn with or without a Nerd
// Font.
func pinnedModel(t *testing.T, nerdFont bool) Model {
	t.Helper()

	m := sizedModel(t, 120, 24)
	m.chats = pinnedChats()
	m.nerdFont = nerdFont

	return m
}

// A pinned chat is marked, because a chat at the top of the list that stands
// there for no visible reason is a question every reader of the list has to
// ask (#46).
//
// It is proved on a Nerd Font and without one, because the mark is a glyph
// in one case and a word in the other, and a mark that only exists with the
// font is a mark half the readers never see.
func TestAPinnedChatIsMarkedWithAndWithoutANerdFont(t *testing.T) {
	for _, nerdFont := range []bool{true, false} {
		m := pinnedModel(t, nerdFont)
		want := chatPinWord
		if nerdFont {
			want = chatPinGlyph
		}

		rows, _ := m.chatListRows(LayoutFor(m.width, m.height), 40)

		head := renderedCells(t, m, rows[0][0])
		if !strings.Contains(cellText(head), want) {
			t.Fatalf(
				"nerdFont=%v: the row of a pinned chat = %q, want the pin %q",
				nerdFont, cellText(head), want,
			)
		}

		// The chat below the two pinned ones carries no pin, or the mark
		// says nothing at all.
		third := renderedCells(t, m, rows[2][0])
		if strings.Contains(cellText(third), want) {
			t.Fatalf(
				"nerdFont=%v: an unpinned chat is marked with %q: %q",
				nerdFont, want, cellText(third),
			)
		}
	}
}

// The pin stands at the right end of the row, before the time, and it is
// given up before the time is — §4.2 hides the extra icons first, because a
// pin is about one row of the list and a time is about every row of it.
func TestThePinIsGivenUpBeforeTheTimeOnANarrowList(t *testing.T) {
	m := pinnedModel(t, true)

	pinned := pinnedChats()[0]
	for _, room := range []int{60, 40, 30, 24, 20, 16, 14} {
		marks := m.chatListHeadMarks(pinned, room)

		if strings.Contains(marks, chatPinGlyph) && !strings.Contains(marks, "09:02") {
			t.Fatalf(
				"room %d: marks = %q, want the time kept before the pin is kept",
				room, marks,
			)
		}
		if marks == "" {
			t.Fatalf("room %d: the row carries neither the pin nor the time", room)
		}
	}

	// The narrowest row that still has both keeps both, and every row
	// narrower than that has the time and not the mark.
	wide := m.chatListHeadMarks(pinned, 200)
	if !strings.Contains(wide, chatPinGlyph) || !strings.Contains(wide, "09:02") {
		t.Fatalf("marks of a wide row = %q, want the pin and the time", wide)
	}
	if strings.Contains(wide, "\n") {
		t.Fatal("the marks of a row must be one run of the row")
	}

	// A chat that is not pinned carries no pin at any width.
	unpinned := m.chatListHeadMarks(pinnedChats()[2], 200)
	if strings.Contains(unpinned, chatPinGlyph) {
		t.Fatalf("marks of an unpinned chat = %q, want no pin", unpinned)
	}
}

// The pinned chats end where a thin line says so, and the line is drawn in
// the row that was air: a row of its own would make the two groups two
// lists, and every chat would cost one more row.
func TestThePinnedChatsAreSeparatedByAThinLine(t *testing.T) {
	m := pinnedModel(t, false)
	layout := LayoutFor(m.width, m.height)
	rows, _ := m.chatListRows(layout, 40)

	// Three rows per chat, and the third of the second chat is the gap where
	// the pinned chats end.
	if len(rows) != 4 {
		t.Fatalf("the list drew %d chats, want 4", len(rows))
	}
	for _, index := range []int{0, 1} {
		if len(rows[index]) != m.chatListRowHeight(layout) {
			t.Fatalf(
				"chat %d takes %d rows, want %d: a line of its own would cost one",
				index, len(rows[index]), m.chatListRowHeight(layout),
			)
		}
	}

	line := rows[1][len(rows[1])-1]
	if !strings.Contains(cellText(renderedCells(t, m, line)), chatListPinnedRuleGlyph) {
		t.Fatalf(
			"the gap under the last pinned chat = %q, want the line",
			cellText(renderedCells(t, m, line)),
		)
	}

	// No other gap carries it: not under the first pinned chat, and not
	// under a chat that is not pinned.
	for _, index := range []int{0, 2, 3} {
		gap := rows[index][len(rows[index])-1]
		if strings.Contains(cellText(renderedCells(t, m, gap)), chatListPinnedRuleGlyph) {
			t.Fatalf("chat %d carries the pinned line: %q", index, cellText(renderedCells(t, m, gap)))
		}
	}
}

// The line is inside the air of the row like every other cell of it: a line
// from edge to edge is the frame that §1 forbids, and it is the frame of the
// pane drawn in a colour of the theme.
func TestThePinnedLineKeepsTheAirInsideTheRow(t *testing.T) {
	m := pinnedModel(t, false)
	layout := LayoutFor(m.width, m.height)
	inset := layout.ChatListInset()
	rows, _ := m.chatListRows(layout, 40)

	line := renderedCells(t, m, rows[1][len(rows[1])-1])

	first, last := -1, -1
	for column, cell := range line {
		if cell.text != chatListPinnedRuleGlyph {
			continue
		}
		if first < 0 {
			first = column
		}
		last = column
	}
	if first < 0 {
		t.Fatalf("the gap under the last pinned chat carries no line: %q", cellText(line))
	}

	if first < inset {
		t.Fatalf(
			"the line starts at column %d, want at least the %d columns of air: %q",
			first, inset, cellText(line),
		)
	}
	if last >= len(line)-inset {
		t.Fatalf(
			"the line ends at column %d of %d, want the %d columns of air at the right",
			last, len(line), inset,
		)
	}
}

// ---- The live state the two are drawn from ----

// The pin and the mute come off the live state, not off the loaded list: a
// person pins and silences a chat in Telegram, and the row has to know it
// without the program being restarted.
func TestTheLiveListBringsThePinAndTheMuteOfAChat(t *testing.T) {
	m := sizedModel(t, 120, 24)
	m.chats = counterChats()

	m = m.applyLiveChats([]LiveChat{
		{ID: 1, Title: "Anime & News", Unread: 3, Pinned: true,
			At: mockMoment("09:02")},
		{ID: 2, Title: "Xiaomi News", Unread: 12, Kind: ChatKindGroup,
			At: mockMoment("11:29")},
		{ID: 3, Title: "Release Room", Unread: 100, Kind: ChatKindGroup,
			Muted: true, At: mockMoment("11:20")},
		{ID: 4, Title: "Notes", At: mockMoment("10:58")},
	})

	for _, chat := range m.chats {
		switch chat.ID {
		case 1:
			if !chat.Pinned {
				t.Error("the pinned chat came off the live list unmarked")
			}
		case 3:
			if !chat.Muted {
				t.Error("the silenced chat came off the live list unmuted")
			}
		default:
			if chat.Pinned || chat.Muted {
				t.Errorf("chat %d came off the live list as pinned or muted", chat.ID)
			}
		}
	}

	if total, _ := m.chatListUnreadTotal(); total != 2 {
		t.Fatalf("unread = %d, want 2 once the live state has said what is read", total)
	}
}

// A reload of the list takes neither the pins nor the mutes off the screen:
// a loaded list says nothing about either, so a row that took its answer
// from one would lose its pin until Telegram happened to send the position
// again.
func TestAReloadKeepsThePinAndTheMuteTheLiveStateGave(t *testing.T) {
	m := sizedModel(t, 120, 24)
	m.chats = []Chat{
		{ID: 1, Title: "Anime & News", Unread: 3, Pinned: true,
			At: mockMoment("09:02")},
		{ID: 2, Title: "Release Room", Unread: 100, Muted: true,
			Kind: ChatKindGroup, At: mockMoment("11:20")},
	}

	reloaded := mergeLoadedChats([]Chat{
		{ID: 1, Title: "Anime & News", Unread: 3, At: mockMoment("09:02")},
		{ID: 2, Title: "Release Room", Unread: 100, Kind: ChatKindGroup,
			At: mockMoment("11:20")},
	}, m.chats, 0)

	for _, chat := range reloaded {
		switch chat.ID {
		case 1:
			if !chat.Pinned {
				t.Error("the pin was lost by a reload of the list")
			}
		case 2:
			if !chat.Muted {
				t.Error("the mute was lost by a reload of the list")
			}
		}
	}
}

// The counter is a field of the model and not a question of the
// configuration per frame: the model is told the mode once, where the theme
// and the clock are told, and every frame draws that answer.
func TestTheCounterModeIsGivenToTheModelWithTheRestOfTheScreen(t *testing.T) {
	m, err := NewModelWithDependencies(t.Context(), Dependencies{
		MessageSubmitter: &recordingSubmitter{},
		UnreadCounter:    UnreadCounterOff,
	})
	if err != nil {
		t.Fatalf("NewModelWithDependencies: %v", err)
	}
	if m.unreadCounter != UnreadCounterOff {
		t.Fatalf("unreadCounter = %v, want off", m.unreadCounter)
	}

	// And a model built as a value counts chats, which is the default the
	// configuration says when it says nothing.
	if plain := NewModel(); plain.unreadCounter != UnreadCounterChats {
		t.Fatalf(
			"the counter of a plain model = %v, want chats",
			plain.unreadCounter,
		)
	}
}

// The mark of a pinned chat is a glyph only where the user has said the
// terminal has a font with one: an emoji the font has not got is drawn as an
// empty square, and an empty square beside a name says nothing at all.
func TestThePinIsAWordWithoutANerdFontAndAGlyphWithIt(t *testing.T) {
	m := pinnedModel(t, false)
	if got := m.chatListPinText(pinnedChats()[0]); got != chatPinWord {
		t.Fatalf("the mark without the font = %q, want %q", got, chatPinWord)
	}

	withFont := pinnedModel(t, true)
	if got := withFont.chatListPinText(pinnedChats()[0]); got != chatPinGlyph {
		t.Fatalf("the mark with the font = %q, want %q", got, chatPinGlyph)
	}

	for _, chat := range pinnedChats()[2:] {
		if got := m.chatListPinText(chat); got != "" {
			t.Fatalf("an unpinned chat is marked %q", got)
		}
	}
}

// A Short row keeps the pin: §3.4 takes the preview and the time away, and
// the pin is what says a chat is pinned at all.
func TestAShortRowKeepsThePinBesideTheBadge(t *testing.T) {
	m := sizedModel(t, 120, shortLayoutHeight-1)
	layout := LayoutFor(m.width, m.height)
	if !layout.Short() {
		t.Fatalf("a height of %d is not Short", m.height)
	}
	m.chats = pinnedChats()

	rows, _ := m.chatListRows(layout, 40)
	head := renderedCells(t, m, rows[0][0])
	if !strings.Contains(cellText(head), chatPinWord) {
		t.Fatalf("the Short row of a pinned chat = %q, want the pin", cellText(head))
	}
}

// The pin is in one column on every row of the list, so the eye can run down
// it: the time stands at the right edge of the row and the pin immediately
// before it, and a pin that moved with the length of a name would be a
// column the reader has to find again on every row.
func TestThePinStandsInOneColumnOnEveryRow(t *testing.T) {
	m := pinnedModel(t, true)
	rows, _ := m.chatListRows(LayoutFor(m.width, m.height), 40)

	column := -1
	for _, index := range []int{0, 1} {
		head := renderedCells(t, m, rows[index][0])

		found := -1
		for position, cell := range head {
			if cell.text == chatPinGlyph {
				found = position
			}
		}
		if found < 0 {
			t.Fatalf(
				"chat %d: the pin is not on the row: %q",
				index, cellText(head),
			)
		}
		if column >= 0 && found != column {
			t.Fatalf(
				"the pin of chat %d is at column %d and the pin of the row above at %d",
				index, found, column,
			)
		}
		column = found
	}
}

// The pin is drawn in the dim step of the text ramp, like the time beside
// it: it is a fact about the row rather than the row's subject, and the
// accent of the theme is the one thing on the screen that says which pane
// has the keys.
func TestThePinIsDrawnInTheMutedStep(t *testing.T) {
	m := focusedOn(programModel(t, theme.ProfileTrueColor, 120, 30), FocusChatList)
	m.chats = pinnedChats()
	m.nerdFont = true

	rows, _ := m.chatListRows(LayoutFor(m.width, m.height), 40)
	head := renderedCells(t, m, rows[0][0])

	muted := m.styles().dimmed(m.tokens().MutedText)
	want := foregroundOf(muted.Render("x"))

	for _, cell := range head {
		if cell.text != chatPinGlyph {
			continue
		}
		if cell.foreground != want {
			t.Fatalf(
				"the pin is drawn in %q, want the muted step %q",
				cell.foreground, want,
			)
		}

		return
	}

	t.Fatalf("the pin is not on the row: %q", cellText(head))
}

package tui

import (
	"context"
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"telecli/internal/tui/termwidth"
	"telecli/internal/tui/theme"
)

// The text of Telegram is drawn into a terminal, and a terminal does what a
// control character says. These tests are the two halves of that claim: the
// table below is what the cleaner does to each class of character, and the
// screens after it are what the interface draws when a chat is called
// something a terminal would act on (#53).
//
// Every character of the fixtures is written as an escape, so that what a
// reviewer reads in this file is the character under test and not the one
// their editor decided to keep.

// TestScreenTextRemovesWhatATerminalWouldActOn is the whole contract of
// the cleaner, one class of character at a time.
func TestScreenTextRemovesWhatATerminalWouldActOn(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "a carriage return",
			in:   "Anna\rExample",
			want: "AnnaExample",
		},
		{
			name: "a vertical tab and a form feed",
			in:   "Anna\v\fExample",
			want: "AnnaExample",
		},
		{
			name: "a backspace",
			in:   "Anna\bExample",
			want: "AnnaExample",
		},
		{
			name: "the next line, which some terminals draw",
			in:   "Anna\u0085Example",
			want: "AnnaExample",
		},
		{
			name: "a delete",
			in:   "Anna\x7fExample",
			want: "AnnaExample",
		},
		{
			name: "a bell",
			in:   "Anna\x07Example",
			want: "AnnaExample",
		},
		{
			name: "an escape that clears the screen",
			in:   "Anna\x1b[2JExample",
			want: "AnnaExample",
		},
		{
			name: "an escape that moves the cursor",
			in:   "Anna\x1b[10;20HExample",
			want: "AnnaExample",
		},
		{
			name: "an escape that changes the colours",
			in:   "Anna\x1b[38;2;255;0;0mExample",
			want: "AnnaExample",
		},
		{
			name: "an escape that resets the terminal",
			in:   "Anna\x1bcExample",
			want: "AnnaExample",
		},
		{
			name: "an escape that saves the cursor",
			in:   "Anna\x1b7and\x1b8Example",
			want: "AnnaandExample",
		},
		{
			name: "an escape with an intermediate byte",
			in:   "Anna\x1b(BExample",
			want: "AnnaExample",
		},
		{
			name: "a clipboard sequence ended by a bell",
			in:   "Anna\x1b]52;c;cGF5bG9hAAAA\x07",
			want: "Anna",
		},
		{
			name: "a clipboard sequence ended by a string terminator",
			in:   "Anna\x1b]52;c;cGF5bG9hAAAA\x1b\\Example",
			want: "AnnaExample",
		},
		{
			name: "a device control string",
			in:   "Anna\x1bPq#0;2;0;0;0Example\x1b\\",
			want: "Anna",
		},
		{
			name: "an application command",
			in:   "Anna\x1b_GabcExample\x1b\\",
			want: "Anna",
		},
		{
			name: "a privacy message command",
			in:   "Anna\x1b^privateExample\x1b\\",
			want: "Anna",
		},
		{
			name: "a shift out that starts a glyph",
			in:   "Anna\x1bOExample",
			want: "AnnaExample",
		},
		{
			name: "a control sequence in its eight-bit form",
			in:   "Ann\u009b2JExample",
			want: "AnnExample",
		},
		{
			name: "a string sequence in its eight-bit form",
			in:   "Ann\u009d52;c;AAAA\u009c",
			want: "Ann",
		},
		{
			name: "a sequence that is cut short",
			in:   "Anna\x1b[",
			want: "Anna",
		},
		{
			name: "a sequence whose content runs to the end",
			in:   "Anna\x1b[2J and the rest of the message",
			want: "Anna and the rest of the message",
		},
		{
			name: "an escape that is not a sequence",
			in:   "Anna\x1b",
			want: "Anna",
		},
		{
			name: "bidi controls that order words the other way round",
			in:   "\u202eAnna\u202c\u202dExample\u202c",
			want: "AnnaExample",
		},
		{
			name: "isolated bidi controls",
			in:   "\u2066\u2067\u2068\u2069",
			want: "",
		},
		{
			name: "a line separator is a line break",
			in:   "one\u2028two",
			want: "one\ntwo",
		},
		{
			name: "a paragraph separator is a line break",
			in:   "one\u2029two",
			want: "one\ntwo",
		},
		{
			name: "a tab is a space",
			in:   "Anna\tExample",
			want: "Anna Example",
		},
		{
			name: "a line break is kept in the body",
			in:   "one\ntwo",
			want: "one\ntwo",
		},
		{
			name: "a carriage return and a line feed are one break",
			in:   "one\r\ntwo",
			want: "one\ntwo",
		},
		{
			name: "a clipboard sequence behind an ordinary bracket",
			in:   "[2 photos]\x1b]52;c;cGF5bG9hAAAA\x07 and more",
			want: "[2 photos] and more",
		},
		{
			name: "words around the damage survive",
			in:   "the build\r\v\x1b[2J is green",
			want: "the build is green",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := screenBody(testCase.in); got != testCase.want {
				t.Fatalf(
					"screenBody(%q) = %q, want %q",
					testCase.in, got, testCase.want,
				)
			}
		})
	}
}

// A single row of the interface is a single row of the terminal, so a line
// break in a name or a preview is a space in it.
func TestScreenLineFoldsEveryLineBreakIntoASpace(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"one\ntwo", "one two"},
		{"one\r\ntwo", "one two"},
		{"one\vtwo", "onetwo"},
		{"one\u0085two", "onetwo"},
		{"one\u2028two", "one two"},
		{"one\u2029two", "one two"},
		{"one\ntwo\nthree", "one two three"},
		{"a name\rof another", "a nameof another"},
		{"a name\x1b[2J of another", "a name of another"},
		{"the build is\rgreen", "the build isgreen"},
		{"Anna\tExample", "Anna Example"},
		{"the build is green", "the build is green"},
	}

	for _, testCase := range cases {
		if got := screenLine(testCase.in); got != testCase.want {
			t.Errorf(
				"screenLine(%q) = %q, want %q",
				testCase.in, got, testCase.want,
			)
		}
	}
}

// What is left is what a person wrote. A conversation in emoji, in a flag
// and in a script that does not use spaces is a conversation a user reads,
// and the cleaner must not touch any of it.
func TestScreenTextKeepsWhatAPersonWrote(t *testing.T) {
	unchanged := []string{
		"Anna Example",
		"Команда Разработки",
		"созвон в 15:00",
		"日本語のチャット",
		"中文消息",
		"emoji: 👍🏽",
		"a family: 👨‍👩‍👧‍👦",
		"a flag: 🇨🇳",
		"two flags: 🇨🇳🇬🇧",
		"a keycap: 1️⃣",
		"an accented name: Café Déjâ Vu",
		"combining marks: áèô",
		"a bracket that is not a sequence: [2J and ESC \\",
		"maths: 3 × 4 ÷ 5 ≠ 6 ≤ 7",
		"quotes: “curly” and ‘single’",
		"a dash: — and an ellipsis: …",
	}

	for _, text := range unchanged {
		if got := screenBody(text); got != text {
			t.Errorf("screenBody(%q) = %q, want it unchanged", text, got)
		}
		if got := screenLine(text); got != text {
			t.Errorf("screenLine(%q) = %q, want it unchanged", text, got)
		}
	}
}

// The cleaner is asked about every string of a chat and of a message, so a
// field added to either of them is cleaned by the same call and not by a
// change to a view.
func TestTheCleanerCoversEveryStringOfBothProjections(t *testing.T) {
	dirtyLine := "a\rb\x1b[2Jc\v"
	dirtyBody := "a\r\nb\x1b[2Jc\v"

	chat := safeChat(Chat{
		Title:   dirtyLine,
		Preview: dirtyLine,
		Time:    dirtyLine,
		Aliases: []string{dirtyLine},
		Messages: []Message{{
			Text: dirtyBody, Time: dirtyLine, Author: dirtyLine,
			Media: dirtyLine, Caption: dirtyBody,
		}},
	})
	if chat.Title != "abc" || chat.Preview != "abc" || chat.Time != "abc" {
		t.Fatalf(
			"chat = %q, %q, %q, want one clean line each",
			chat.Title, chat.Preview, chat.Time,
		)
	}
	if len(chat.Aliases) != 1 || chat.Aliases[0] != "abc" {
		t.Fatalf("aliases = %q, want one clean name", chat.Aliases)
	}

	message := chat.Messages[0]
	if message.Text != "a\nbc" || message.Caption != "a\nbc" {
		t.Fatalf(
			"text = %q, caption = %q, want the line break kept",
			message.Text, message.Caption,
		)
	}
	if message.Author != "abc" || message.Media != "abc" ||
		message.Time != "abc" {
		t.Fatalf("message = %+v, want every other string on one line", message)
	}
}

// A source that handed the model its own list back must not see the
// interface reach into it: the list is copied rather than cleaned in place.
func TestTheCleanerDoesNotWriteIntoTheListItWasGiven(t *testing.T) {
	original := []Chat{{Title: "Anna\rExample", Aliases: []string{"a\vb"}}}
	_ = safeChats(original)

	if original[0].Title != "Anna\rExample" {
		t.Fatalf("title = %q, want the caller's own string", original[0].Title)
	}
	if original[0].Aliases[0] != "a\vb" {
		t.Fatalf(
			"alias = %q, want the caller's own string", original[0].Aliases[0],
		)
	}
}

// ---- the screens ----

// The names and the previews here are what anybody in a chat can make a
// chat be called. The list used to double its rows and slide them, because
// each of these strings drew a row the interface had not drawn.
func untrustedChats() []Chat {
	return []Chat{
		{
			ID:      1,
			Title:   "Anna\rExample",
			Preview: "the build\vis green",
			Time:    "12:07",
		},
		{
			ID:      2,
			Title:   "Release Room\x1b[2J",
			Preview: "the tag is\npushed",
			Time:    "12:05",
		},
		{
			ID:      3,
			Title:   "Channel\u0085of the day",
			Preview: "[2 photos]\x1b]52;c;cGF5bG9hAAAA\x07",
			Unread:  2,
		},
		{
			ID:      4,
			Title:   "Команда\u202e Разработки\u202c",
			Preview: "созвон\u2028в 15:00",
		},
	}
}

// forbiddenOnAScreen is every character the cleaner is there to keep off a
// screen: the C0 controls, DEL, the C1 controls, and the bidi controls. A
// newline is not among them because it is the character the rows of a
// screen are split on.
func forbiddenOnAScreen() []rune {
	forbidden := []rune{escapeByte, delByte}
	for r := rune(0); r < 0x20; r++ {
		forbidden = append(forbidden, r)
	}
	for r := rune(c1First); r <= rune(c1Last); r++ {
		forbidden = append(forbidden, r)
	}
	for r := rune(0x202A); r <= 0x202E; r++ {
		forbidden = append(forbidden, r)
	}
	for r := rune(0x2066); r <= 0x2069; r++ {
		forbidden = append(forbidden, r)
	}

	return forbidden
}

// assertScreenIsSafe fails unless the screen is the size it was drawn at,
// holds nothing a terminal would act on, and has no line wider than the
// terminal it is drawn for.
func assertScreenIsSafe(t *testing.T, name string, m Model) {
	t.Helper()

	view := m.View()
	lines := viewLines(view)

	if len(lines) != m.height {
		t.Errorf(
			"%s: the screen has %d rows, want %d:\n%s",
			name, len(lines), m.height, view,
		)
	}

	for index, line := range lines {
		for _, r := range forbiddenOnAScreen() {
			if strings.ContainsRune(line, r) {
				t.Errorf(
					"%s: row %d holds %U, which a terminal acts on: %q",
					name, index+1, r, line,
				)
			}
		}

		if width := m.widths.StringWidth(line); width > m.width {
			t.Errorf(
				"%s: row %d is %d columns wide, want at most %d: %q",
				name, index+1, width, m.width, line,
			)
		}
	}
}

// A chat list whose names move the cursor draws the same number of rows as
// any other, and holds none of the characters that made it draw more.
func TestChatListOfUntrustedNamesDrawsTheScreenItWasGiven(t *testing.T) {
	for _, mode := range []termwidth.Mode{
		termwidth.ModeGrapheme, termwidth.ModeCodepoint,
	} {
		for _, width := range []int{120, 80, 60} {
			name := mode.String() + "@" + strconv.Itoa(width)

			t.Run(name, func(t *testing.T) {
				m := untrustedChatList(t, mode, width, 24)
				assertScreenIsSafe(t, "chat list", m)
			})
		}
	}
}

// The same claim for the feed, with a message that carries every class of
// character the cleaner knows about.
func TestFeedOfUntrustedMessagesDrawsTheScreenItWasGiven(t *testing.T) {
	for _, mode := range []termwidth.Mode{
		termwidth.ModeGrapheme, termwidth.ModeCodepoint,
	} {
		for _, width := range []int{120, 80, 60} {
			name := mode.String() + "@" + strconv.Itoa(width)

			t.Run(name, func(t *testing.T) {
				m := untrustedConversation(t, mode, width, 24)
				assertScreenIsSafe(t, "feed", m)
			})
		}
	}
}

// A multi-line message is several lines in the feed and one line in the
// preview of the list: the two places ask different things of the same
// text and both of them are right.
func TestAMultiLineMessageInTheFeedAndInThePreview(t *testing.T) {
	m := untrustedConversation(t, termwidth.ModeGrapheme, 120, 30)
	view := plain(m.View())

	if !strings.Contains(view, "the second line") {
		t.Fatalf("the second line of the message is not on the screen:\n%s",
			view)
	}
	if !strings.Contains(m.chats[0].Messages[0].Text, "\n") {
		t.Fatal("the message lost its line break on the way into the model")
	}
	if got := m.chats[1].Preview; got != "the tag is pushed" {
		t.Fatalf("preview = %q, want the two lines of it in one row", got)
	}
}

// A chat name with a line break in it is one row of the list, and the
// search finds it by the words around the break rather than by the whole
// string with the break in it.
func TestAChatNameWithALineBreakIsOneRowAndIsSearched(t *testing.T) {
	m := untrustedChatList(t, termwidth.ModeGrapheme, 120, 24)

	if got := m.chats[0].Title; got != "AnnaExample" {
		t.Fatalf("title = %q, want one line", got)
	}

	m, _ = updateModel(t, m, pressRunes("/"))
	m, _ = updateModel(t, m, pressRunes("Example"))

	entries := m.chatListEntries()
	if len(entries) != 1 {
		t.Fatalf("the search kept %d rows, want the one chat it names",
			len(entries))
	}
	if entries[0].chat.ID != 1 {
		t.Fatalf("the search kept chat %d, want chat 1", entries[0].chat.ID)
	}
}

// untrustedChatList is a model holding a chat list of names that a terminal
// would act on, drawn at a size and in a width rule the test names.
func untrustedChatList(
	t *testing.T,
	mode termwidth.Mode,
	width, height int,
) Model {
	t.Helper()

	m, err := NewModelWithDependencies(context.Background(), Dependencies{
		AccountKey:       "account-fixture",
		Source:           &fakeChatSource{},
		MessageSubmitter: &recordingSubmitter{},
		WidthMode:        mode,
	})
	if err != nil {
		t.Fatalf("NewModelWithDependencies: %v", err)
	}
	m = m.withRenderer(theme.ProfileNoColor)

	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: width, Height: height})
	m, _ = updateModel(t, m, chatsLoadedMsg{chats: untrustedChats()})

	return m
}

// untrustedConversation is the same list with the first chat open and a
// page of history in it that carries the same characters.
func untrustedConversation(
	t *testing.T,
	mode termwidth.Mode,
	width, height int,
) Model {
	t.Helper()

	m := untrustedChatList(t, mode, width, height)
	m, _ = updateModel(t, m, press(tea.KeyEnter))
	m, _ = updateModel(t, m, historyLoadedMsg{
		chatID:    1,
		operation: m.historyOperation,
		page: HistoryPage{Messages: []Message{{
			ID: 1,
			Text: "the first line\r\nthe second line\v\f\u0085\x1b" +
				"[2J\x1b]52;c;cGF5bG9hAAAA\x07",
			Time:   "12:02",
			Author: "Anna\rExample\x1b]52;c;AAAA\x07",
		}}},
	})

	return m.scrollToNewest()
}

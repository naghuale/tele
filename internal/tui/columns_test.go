package tui

import (
	"strings"
	"testing"

	"telecli/internal/tui/termwidth"
	"telecli/internal/tui/theme"
)

// Where the letters after an emoji are drawn is not the terminal's decision.
//
// The owner's screen of 01.10: in the macOS Terminal a pagoda — U+26E9, with
// nothing behind it — is drawn two cells wide out of the emoji font and the
// cursor advances one, so a letter written after it lands inside the picture
// unless the program says where the next cell is. A message whose text mixes
// a flag with Cyrillic came out with its second line a cell left of the
// block and a dark gap at the end of the first.
//
// No width rule fixes that, because what is wrong is the terminal's own
// advance rather than the program's count of it. Two things do: every
// emoji-like cluster is given two cells of the row (termwidth.EmojiLike),
// and the cursor is placed at the end of those two cells instead of being
// left where the terminal left it.

// The bytes of a row say where the next cell is. The pagoda takes the two
// cells a terminal draws it in, and the letter after it is placed at the
// third column of a row that starts at the first.
func TestAnEmojiIsFollowedByAnAbsoluteColumn(t *testing.T) {
	m := sizedModel(t, 120, 24)
	m.widths = termwidth.Unmeasured(termwidth.ModeGrapheme)

	row := m.painter(theme.Color{}).
		add(m.styles().unstyled(), "⛩Ф").
		String()

	// The row of a pane starts after the column of the focus marker and the
	// air inside the pane, so the pagoda is in the third column of the
	// screen and the letter after it in the fifth.
	first := focusColumnWidth + contentInsetWidth + 1

	if want := "⛩" + cursorColumn(first+2) + "Ф"; !strings.Contains(row, want) {
		t.Errorf(
			"the row does not place the letter at column %d:\n%q\nwant it to contain %q",
			first+2, plain(row), want,
		)
	}
}

// A row of letters is a row of letters: a position after every character
// would be a sequence per character and a frame nobody can read in a log.
func TestARowWithoutEmojiIsNotFullOfPositions(t *testing.T) {
	m := sizedModel(t, 120, 24)
	m.widths = termwidth.Unmeasured(termwidth.ModeGrapheme)

	row := plain(m.painter(theme.Color{}).
		add(m.styles().unstyled(), "ФУЮАНЬ").
		String())

	if strings.Contains(row, "\x1b[") {
		t.Errorf("the row has an absolute column in it: %q", row)
	}
}

// Every drawn row says where it starts. The renderer paints the rows that
// changed and leaves the cursor where the last of those left it, so a row
// that does not name its first column starts where the row before it ended.
func TestEveryDrawnRowStartsAtTheFirstColumn(t *testing.T) {
	m := openedProgramModel(t, theme.ProfileTrueColor, 120, 30)

	for row, line := range strings.Split(m.View(), "\n") {
		if !strings.HasPrefix(line, cursorColumn(1)) {
			t.Errorf("row %d does not start at the first column:\n%q", row+1, line)
		}
	}
}

// The terminal of the owner's report: a symbol out of the emoji font drawn
// two cells wide with the cursor advanced one. Everything is on a screen
// that a user reads, so the claim is the whole frame at once — every row is
// the row the program drew, in the columns it measured, with the list, the
// header of the conversation and the bubble of a message in it.
//
// The two cases are the ones that broke: a title with two pagodas and a flag
// in it, and a message whose text mixes a flag with Cyrillic and wraps.
func TestTheOwnersStringsAreDrawnWhereTheyWereMeasured(t *testing.T) {
	const (
		width  = 110
		height = 30
	)

	widths := termwidth.Unmeasured(termwidth.ModeGrapheme)

	for name, text := range map[string]string{
		"a title with a pagoda and a flag": ownerTitle,
		"a message with a flag and letters": "⛩ФУЮАНЬ 🇨🇳 САХ ЭКСПО ⛩ " +
			"и ещё немного букв в конце",
	} {
		t.Run(name, func(t *testing.T) {
			m := messageOnScreen(t, ownerTitle, text, width, height)
			m.widths = widths

			frame := positionRows(m.View())

			emulator := newScreenEmulator(width, height, widths)
			emulator.advanceOneCellPerEmoji = true
			emulator.Write([]byte(autoWrapOff + frame)) //nolint:errcheck

			for row, line := range strings.Split(frame, "\n") {
				want := strings.TrimRight(plain(line), " ")
				have := strings.TrimRight(emulator.rowText(row), " ")

				if have != want {
					t.Errorf(
						"row %d reads %q on the terminal and %q in the frame",
						row+1, have, want,
					)
				}
			}
		})
	}
}

// Every line of a wrapped block starts at the block's own inner left column,
// with the text of a flag and a pagoda in it.
//
// The block is where the two halves of this meet: the text is wrapped by the
// program, so every line of it is the length the program says it is, and a
// terminal that advances one column less than the program counted draws the
// second line a column to the left of the first and leaves a gap at the end
// of it.
func TestABlockOfFlagAndLettersKeepsItsInnerLeftColumn(t *testing.T) {
	const (
		width  = 110
		height = 30
	)

	widths := termwidth.Unmeasured(termwidth.ModeGrapheme)
	text := "⛩ФУЮАНЬ 🇨🇳 САХ ЭКСПО ⛩ и ещё немного букв в конце"
	m := messageOnScreen(t, ownerTitle, text, width, height)
	m.widths = widths

	layout := LayoutFor(m.width, m.height)
	feed := width - conversationOrigin(layout) - focusColumnWidth - contentInsetWidth
	block := m.messageBlockFor(
		sideIncoming, layout, feed, text, true,
	)

	rows := m.entryLines(
		timelineEntries(m.chats[0].Messages)[0], layout, feed, m.styles(),
	)
	if len(rows) < 2 {
		t.Fatalf("the block took %d rows, want a wrapped one of two or more", len(rows))
	}

	emulator := newScreenEmulator(width, height, widths)
	emulator.advanceOneCellPerEmoji = true
	emulator.Write([]byte(autoWrapOff + positionRows(m.View()))) //nolint:errcheck

	// The words of a line of an incoming block start in the column after
	// the offset of the block and the air inside it, and nowhere earlier:
	// a line that starts a column to the left is a line whose text was
	// drawn where the terminal put it.
	inner := conversationOrigin(layout) + focusColumnWidth + contentInsetWidth +
		block.offset + block.inset

	for index, line := range rows {
		// The rows of air around the words are rows of the block and say
		// nothing about where its words start.
		if strings.TrimSpace(plain(line)) == "" {
			continue
		}

		row := rowEndingWith(emulator, plain(line))
		if row < 0 {
			t.Fatalf(
				"no row of the screen holds line %d of the block:\n%q",
				index+1, plain(line),
			)
		}

		first := -1
		for column := range width {
			if text := emulator.cells[row][column]; strings.TrimSpace(text) != "" {
				first = column + 1

				break
			}
		}

		if first != inner {
			t.Errorf(
				"line %d of the block starts at column %d, want the inner column %d:\n%q",
				index+1, first, inner, plain(line),
			)
		}
	}
}

// ownerTitle is the title of the owner's screen: a pagoda, six letters,
// another pagoda, five letters, a flag of two regional indicators and eight
// more letters.
const ownerTitle = "⛩ФУЮАНЬ⛩ЖАОХЭ🇨🇳САХЭКСПО"

// messageOnScreen returns a model with one chat of the given title and one
// message of the given text, opened at the given size.
func messageOnScreen(t *testing.T, title, text string, width, height int) Model {
	t.Helper()

	m := sizedModel(t, width, height)
	m.chats = []Chat{{
		ID:    1,
		Title: title,
		Messages: []Message{{
			ID: 1, Author: "Анна", Text: text, At: mockMoment("12:07"),
		}},
	}}
	m.chatsState = loadStateLoaded
	m.selectedChat = 0
	m.screen = ScreenConversation

	return m
}

// rowEndingWith returns the row of the screen that ends with the given text,
// or -1.
//
// The end of the row and not a part of it: a row of a block that begins with
// the first words of a chat title is the title's row as far as a substring
// is concerned, and the row this is about is the block's, whose words end
// where the block ends.
func rowEndingWith(e *screenEmulator, text string) int {
	text = strings.TrimRight(text, " ")

	for row := range len(e.cells) {
		if strings.HasSuffix(strings.TrimRight(e.rowText(row), " "), text) {
			return row
		}
	}

	return -1
}

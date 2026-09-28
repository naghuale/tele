package tui

import (
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"telecli/internal/tui/termwidth"
	"telecli/internal/tui/theme"
)

// The tests in this file are about the two sides of a conversation and the
// columns between them: a message of this user is a block as wide as its
// own text on the right, a message of the other side is on the left with no
// block behind it, and the feed keeps air on both sides of the lane they
// are drawn in.
//
// What is checked here is the cells and not the words. A row that reads
// correctly can still be a band of another colour across the whole feed,
// and a block of the right width in the wrong place is a block a reader
// has to hunt for; neither of them shows up in the text of a row.

// renderedCell is one cell of a rendered row as a terminal would paint it:
// what it holds, and the foreground and background in force where it
// stands.
//
// The colours are the SGR parameters rather than a colour, because the
// question is what the terminal was told and the answer to that is the
// sequence: a value the colour library rounded on the way out is the
// value the terminal is given.
type renderedCell struct {
	text       string
	foreground string
	background string
}

// renderedCells splits a rendered row into its cells.
//
// A grapheme cluster is one cell however many columns it takes, and the
// columns of a wide one after it belong to it rather than holding anything
// of their own: a test that counted a wide character twice would be
// counting a half of a glyph.
func renderedCells(t *testing.T, m Model, line string) []renderedCell {
	t.Helper()

	var (
		cells []renderedCell
		paint renderedCell
	)

	for rest := line; rest != ""; {
		piece, _, read, _ := ansi.DecodeSequence(rest, 0, nil)
		if read <= 0 {
			t.Fatalf("the row has a byte no sequence accounts for: %q", rest)

			return cells
		}
		rest = rest[read:]

		if strings.HasPrefix(piece, "\x1b") {
			if strings.HasSuffix(piece, "m") {
				paint = sgrRun(paint, piece)
			}

			continue
		}

		cell := renderedCell{
			text:       piece,
			foreground: paint.foreground,
			background: paint.background,
		}
		for range maxInt(m.widths.StringWidth(piece), 1) {
			cells = append(cells, cell)
		}
	}

	return cells
}

// renderedRows splits every row of a screen into its cells, which is what
// a test about a background needs: one screen, one question.
func renderedRows(t *testing.T, m Model, view string) [][]renderedCell {
	t.Helper()

	lines := strings.Split(view, "\n")
	rows := make([][]renderedCell, 0, len(lines))
	for _, line := range lines {
		rows = append(rows, renderedCells(t, m, line))
	}

	return rows
}

// sgrRun returns the paint a row is in after one more SGR sequence.
//
// It reads the sequence as the numbers a terminal reads, with the two
// codes that carry their colour behind them, because a search for a
// substring says "38;2;203" is a background whenever a true-colour
// foreground is on the row — and most of this screen is one of those.
func sgrRun(paint renderedCell, sequence string) renderedCell {
	parameters := strings.Split(
		strings.TrimSuffix(strings.TrimPrefix(sequence, "\x1b["), "m"),
		";",
	)

	for index := 0; index < len(parameters); {
		code, err := strconv.Atoi(parameters[index])
		if err != nil {
			index++

			continue
		}

		switch code {
		case 0:
			paint = renderedCell{}
			index++

		case 39:
			paint.foreground = ""
			index++

		case 49:
			paint.background = ""
			index++

		case 38, 48:
			run, read := sgrColour(parameters, index)
			if code == 38 {
				paint.foreground = run
			} else {
				paint.background = run
			}
			index += read

		default:
			index++
		}
	}

	return paint
}

// sgrColour returns the parameters of a foreground or a background and how
// many parameters past the code they take: a basic colour is the code and
// the number, an indexed one adds the index behind a 5, and a true-colour
// one adds three numbers behind a 2.
func sgrColour(parameters []string, index int) (string, int) {
	read := 2

	if index+1 < len(parameters) {
		switch parameters[index+1] {
		case "2":
			read = 5
		case "5":
			read = 3
		}
	}

	read = minInt(read, len(parameters)-index)

	return strings.Join(parameters[index:index+read], ";"), read
}

// foregroundOf is the SGR foreground a rendered run paints its text in.
//
// It is the first one the run asks for and not the last, because a run
// ends with a reset of its own and the colour of the text is in force
// before it, not after it.
func foregroundOf(rendered string) string {
	paint := renderedCell{}

	for rest := rendered; rest != ""; {
		piece, _, read, _ := ansi.DecodeSequence(rest, 0, nil)
		if read <= 0 {
			break
		}
		rest = rest[read:]

		if !strings.HasPrefix(piece, "\x1b") || !strings.HasSuffix(piece, "m") {
			continue
		}
		paint = sgrRun(paint, piece)

		if paint.foreground != "" {
			return paint.foreground
		}
	}

	return ""
}

// The three backgrounds the feed is drawn with, as the SGR parameters a
// cell has to carry for that surface to be under it.
//
// They are taken from the styles the views draw with rather than from the
// tokens directly, because the question is not which colour a surface is
// but which sequence a row carries for it, and a value the colour library
// rounded on the way out is the value the terminal is given.
func blockBackground(m Model, side messageSide) string {
	return backgroundParameters(
		m.styles().on(m.blockSurface(side, false), m.styles().unstyled()).
			Render("x"),
	)
}

func feedBackground(m Model) string {
	return backgroundParameters(
		m.styles().region(themeRegion{background: m.tokens().ChatBackground}).
			Render("x"),
	)
}

func selectionBackground(m Model) string {
	return backgroundParameters(m.styles().selected(true).Render("x"))
}

// bandOf returns the first and the last column of the cells of a row that
// carry a background, and how many there were. Both are -1 for a row with
// no cell of that background on it at all.
func bandOf(cells []renderedCell, background string) (first, last, count int) {
	first, last = -1, -1

	for index, cell := range cells {
		if cell.background != background {
			continue
		}
		if first < 0 {
			first = index
		}
		last = index
		count++
	}

	return first, last, count
}

// rowsSaying returns the rows of a screen that say a text, and the index
// of each of them on the screen.
//
// A test about where a message is drawn asks "which rows are its own", and
// the answer is a question about the words in them: a block that moved to
// the wrong column is still a block with the same words in it, and a test
// that only compared columns would not notice the words moved either.
func rowsSaying(
	rows [][]renderedCell,
	text string,
) (indexes []int, found [][]renderedCell) {
	for index, row := range rows {
		words := cellText(row)
		if !strings.Contains(words, text) {
			continue
		}

		indexes = append(indexes, index)
		found = append(found, row)
	}

	return indexes, found
}

// groupOf returns the rows of one message: every row of the screen that is
// not blank and is joined to a row of the message by rows that are not
// blank either.
//
// The head of a message of the other side says the name of whoever sent
// it, and that row belongs to the message as much as the row with its
// words in it does. The blank row above an entry is what ends the group,
// and it is there for that reason.
func groupOf(rows [][]renderedCell, from, to int, seeds []int) map[int]bool {
	group := make(map[int]bool, len(seeds))

	for _, seed := range seeds {
		for index := seed; index >= 0 && !blankRow(rows[index], from, to); index-- {
			group[index] = true
		}
		for index := seed; index < len(rows) && !blankRow(rows[index], from, to); index++ {
			group[index] = true
		}
	}

	return group
}

// blankRow reports whether every cell of a row in a range of columns is a
// space, which is what separates one message from the next.
func blankRow(row []renderedCell, from, to int) bool {
	for column := from; column <= to && column < len(row); column++ {
		if strings.TrimSpace(row[column].text) != "" {
			return false
		}
	}

	return true
}

// cellText returns what a row of cells says, one character per column and
// nothing else: the words a reader would find on the row.
func cellText(row []renderedCell) string {
	var out strings.Builder
	for _, cell := range row {
		out.WriteString(cell.text)
	}

	return out.String()
}

// entryRows renders one message of a conversation the way the timeline
// does, at a given width of the feed, and returns the cells of every row it
// takes — the blank row above it included.
func entryRows(
	t *testing.T,
	m Model,
	layout Layout,
	width int,
	message Message,
) [][]renderedCell {
	t.Helper()

	entries := timelineEntries([]Message{message})
	rendered := m.entryLines(entries[0], layout, width, m.styles())

	rows := make([][]renderedCell, 0, len(rendered))
	for _, row := range rendered {
		rows = append(rows, renderedCells(t, m, row))
	}

	return rows
}

// conversationColumns returns the first and the last column of the
// conversation on a screen of the model, which is what a test about a
// background has to look at: the chat list beside it has a background of
// its own, and a cell of the sidebar is not a cell of the feed.
func conversationColumns(m Model) (first, last int) {
	layout := LayoutFor(m.width, m.height)
	first = layout.SidebarWidth() + paneGapWidth

	if layout.TwoPane() {
		first += focusColumnWidth + contentInsetWidth
	}

	return first, m.width - 1
}

// feedColumns returns the columns of the lane the messages of a
// conversation are drawn in: the feed less the margin it keeps on each
// side.
//
// A message of the other side starts at the first of them and a message of
// this user ends at the last, which is the whole of what tells the two
// sides apart on a screen with no other mark on them.
func feedColumns(width int, layout Layout) (start, end int) {
	return layout.FeedMargin(), width - layout.FeedMargin() - 1
}

// unmeasured returns a width model that counts in the given rule, for a
// test that draws a screen in one of the two rules of termwidth.
func unmeasured(mode termwidth.Mode) termwidth.WidthModel {
	model, _ := termwidth.Select(mode, termwidth.Measurement{})

	return model
}

// The width of the feed the geometry below is proved on. It is a round
// number on purpose: a block is a number of columns and a share of a
// number, and a test that had to divide by whatever the pane turned out to
// be would be proving the pane and not the block.
const feedTestWidth = 90

// A message of this user is a block on the right, as wide as the widest
// line in it and no wider, ending at the right margin of the feed.
//
// It is the whole of what a reader of a chat looks at first, and it is
// what the interface got wrong: a block of a fixed seventy per cent of the
// feed under a two-word message is a band, and a band that starts in the
// middle of the screen says that the message belongs to nobody in
// particular.
func TestAnOutgoingBlockIsAsWideAsItsTextAndEndsAtTheRightMargin(t *testing.T) {
	m := focusedOn(
		openedProgramModel(t, theme.ProfileTrueColor, 120, 30),
		FocusHistory,
	)
	layout := LayoutFor(m.width, m.height)
	message := Message{
		ID: 1, Outgoing: true, Text: "shipped the release notes", Time: "12:05",
	}
	rows := entryRows(t, m, layout, feedTestWidth, message)

	block := blockBackground(m, sideOutgoing)
	_, wantEnd := feedColumns(feedTestWidth, layout)
	label, _ := m.historyStateLabel(message)
	wantWidth := maxInt(m.widths.StringWidth(message.Text), m.widths.StringWidth(label)) +
		2*layout.BubbleInset()

	if len(rows) < 3 {
		t.Fatalf("the message took %d rows, want a blank, its text and its state", len(rows))
	}

	for index, row := range rows[1:] {
		first, last, count := bandOf(row, block)
		if count == 0 {
			t.Fatalf("row %d of the block has no block on it", index+1)
		}
		if last-first+1 != wantWidth {
			t.Errorf(
				"row %d: the block is %d columns, want the widest line and its insets (%d)",
				index+1, last-first+1, wantWidth,
			)
		}
		if last != wantEnd {
			t.Errorf(
				"row %d: the block ends at column %d, want the last column of the feed (%d)",
				index+1, last, wantEnd,
			)
		}
	}
}

// A block is at most the share of the feed the specification gives it, and
// a text that does not fit wraps inside it rather than being cut. A
// message cut in half is a message nobody can read and nobody can answer.
func TestALongOutgoingMessageTakesTheShareAndWraps(t *testing.T) {
	m := focusedOn(
		openedProgramModel(t, theme.ProfileTrueColor, 120, 30),
		FocusHistory,
	)
	layout := LayoutFor(m.width, m.height)
	rows := entryRows(t, m, layout, feedTestWidth, Message{
		ID:       1,
		Outgoing: true,
		Text:     strings.Repeat("word ", 40),
		Time:     "12:05",
	})

	block := blockBackground(m, sideOutgoing)
	want := feedTestWidth * outgoingBubbleSharePercent / 100

	_, textRows := rowsSaying(rows, "word")
	if len(textRows) < 2 {
		t.Fatalf(
			"the text took %d rows, want it wrapped inside the block", len(textRows),
		)
	}

	for index, row := range textRows {
		first, last, count := bandOf(row, block)
		if count == 0 {
			t.Fatalf("row %d of the text has no block on it", index)
		}
		if got := last - first + 1; got != want {
			t.Fatalf("the block is %d columns, want %d", got, want)
		}
		if last > feedTestWidth-1 {
			t.Fatalf("the block ends at column %d, past the feed", last)
		}
	}
}

// Both sides are blocks, and the two are told apart in colour as well as
// in position: the block of a message from the other side is the neutral
// surface of the theme, the block of a message of this user is the accent's
// tint of it, and neither is a band across the feed.
//
// The positions alone are not enough. A reader of a chat expects their own
// messages to be the ones in the colour of the theme, and a feed whose two
// sides are two greys of the same ramp is a feed they have to read the side
// of the screen to follow — which is what the owner reported when the
// incoming side lost its block altogether and the outgoing one was the only
// thing on the screen with a surface.
func TestTheTwoSidesAreBlocksOfTheirOwnColours(t *testing.T) {
	m := focusedOn(
		openedProgramModel(t, theme.ProfileTrueColor, 120, 30),
		FocusHistory,
	)
	m.chats[m.selectedChat].Messages = []Message{
		{ID: 1, Text: "from the other side", Time: "12:00", Author: "Anna", AuthorID: 7},
		{ID: 2, Outgoing: true, Text: "from this side", Time: "12:01"},
	}
	m.historyState = loadStateLoaded
	// The cursor is past the last message, so both blocks are on the
	// surface of their own side and neither is on the selection.
	m.selectedMsg = len(m.chats[m.selectedChat].Messages)
	m.timelineTop = 0

	view := m.View()
	cells := renderedRows(t, m, view)

	feed := feedBackground(m)
	incoming := blockBackground(m, sideIncoming)
	outgoing := blockBackground(m, sideOutgoing)
	if incoming == outgoing {
		t.Fatalf("the two blocks are both %q, so the sides are told apart by position only", incoming)
	}
	if incoming == feed || outgoing == feed {
		t.Fatalf("a block is on the feed's own background %q", feed)
	}

	layout := LayoutFor(m.width, m.height)
	start, end := feedColumns(layout.ChatContentWidth(), layout)

	_, theirRows := rowsSaying(cells, "from the other side")
	_, mineRows := rowsSaying(cells, "from this side")
	if len(theirRows) == 0 || len(mineRows) == 0 {
		t.Fatalf("both messages are on the screen:\n%s", view)
	}

	first, last := conversationColumns(m)
	_ = last

	// The block of the other side's message is against the left margin and
	// is as wide as the name and the text in it.
	for _, row := range theirRows {
		from, to, count := bandOf(row[first:], incoming)
		if count == 0 {
			t.Fatalf("the message of the other side has no block on it:\n%s", view)
		}
		if from != start {
			t.Errorf("the block of the other side starts at lane column %d, want %d", from, start)
		}
		if to >= end {
			t.Errorf("the block of the other side reaches lane column %d, past the feed", to)
		}
	}

	// The block of a message of this user is against the right margin.
	for _, row := range mineRows {
		from, to, count := bandOf(row[first:], outgoing)
		if count == 0 {
			t.Fatalf("the message of this user has no block on it:\n%s", view)
		}
		if to != end {
			t.Errorf("the block of this user ends at lane column %d, want %d", to, end)
		}
		if from <= start {
			t.Errorf("the block of this user starts at lane column %d, want it right of the other side's", from)
		}
	}
}

// The selection of the message under the cursor is on the block of that
// message and on no other row, and it is on no cell of the block either
// for the other message.
//
// The block is the only background on a row of the feed, so it is the
// only place a selection can be seen: a selection that ran the width of
// the feed under a message would be the band the feed stopped having, and
// one that stopped before the block would be nowhere.
func TestTheSelectionIsOnTheMessageUnderTheCursorAndNowhereElse(t *testing.T) {
	for _, onOutgoing := range []bool{false, true} {
		m := focusedOn(
			openedProgramModel(t, theme.ProfileTrueColor, 120, 30),
			FocusHistory,
		)
		m.chats[m.selectedChat].Messages = []Message{
			{ID: 1, Text: "from the other side", Time: "12:00", Author: "Anna", AuthorID: 7},
			{ID: 2, Outgoing: true, Text: "from this side", Time: "12:01"},
		}
		m.historyState = loadStateLoaded
		if onOutgoing {
			m.selectedMsg = 1
		} else {
			m.selectedMsg = 0
		}
		m.timelineTop = 0

		view := m.View()
		rows := renderedRows(t, m, view)
		first, last := conversationColumns(m)

		incoming, _ := rowsSaying(rows, "from the other side")
		outgoing, _ := rowsSaying(rows, "from this side")
		if len(incoming) == 0 || len(outgoing) == 0 {
			t.Fatalf("both messages are on the screen:\n%s", view)
		}

		chosen, other := incoming, outgoing
		if onOutgoing {
			chosen, other = outgoing, incoming
		}

		unwanted := groupOf(rows, first, last, other)
		selected := selectionBackground(m)
		painted := 0

		for index, row := range rows {
			for column := first; column <= last && column < len(row); column++ {
				if row[column].background != selected {
					continue
				}
				painted++

				if unwanted[index] {
					t.Errorf(
						"outgoing=%v: row %d column %d is on the selection, "+
							"and it belongs to the other message:\n%s",
						onOutgoing, index, column, view,
					)
				}
			}
		}

		if painted == 0 {
			t.Errorf(
				"outgoing=%v: nothing in the feed is selected:\n%s", onOutgoing, view,
			)
		}
		if len(chosen) == 0 {
			t.Errorf("outgoing=%v: the message under the cursor is not on the screen", onOutgoing)
		}
	}
}

// The name of whoever sent a message is on the first row of its block, not
// on the feed above it.
//
// A name on the background of the feed with a text in a block under it is
// two things to read as one message, and a reader has to learn from the
// shape that they belong together. Inside one block there is nothing to
// learn, and the rows of the two are the same colour, which is what says
// they are one thing.
func TestTheNameOfTheSenderIsInsideTheBlockOfTheMessage(t *testing.T) {
	m := focusedOn(
		openedProgramModel(t, theme.ProfileTrueColor, 120, 30),
		FocusHistory,
	)
	layout := LayoutFor(m.width, m.height)
	message := Message{
		ID: 1, Text: "from the other side", Time: "12:00",
		Author: "Anna", AuthorID: 7,
	}
	rows := entryRows(t, m, layout, feedTestWidth, message)
	incoming := blockBackground(m, sideIncoming)

	if len(rows) < 3 {
		t.Fatalf("the message took %d rows, want a blank, its name and its text", len(rows))
	}

	head, _, count := bandOf(rows[1], incoming)
	if count == 0 {
		t.Fatal("the row of the name has no block on it")
	}
	if !strings.Contains(cellText(rows[1]), messageAuthor(message)) {
		t.Errorf("the name is not on the first row of the block: %q", cellText(rows[1]))
	}
	if head != layout.FeedMargin() {
		t.Errorf("the block starts at column %d, want the left margin at %d", head, layout.FeedMargin())
	}

	// And the text is inside the same block as the name, which is the
	// whole of what says they are one message.
	from, to, _ := bandOf(rows[2], incoming)
	if from != head || to != head+count-1 {
		t.Errorf(
			"the block of the text runs from %d to %d, want the same block as the name (%d to %d)",
			from, to, head, head+count-1,
		)
	}
}

// The state under a message of this user is inside its block, at the right
// edge of it, where the block is.
//
// It is a fact about the message rather than a continuation of its text, so
// it is read where the message ends rather than where the last line of the
// text starts — and it is inside the block because a state under a block
// and not in it is a line the reader has to reassemble with the message
// above it.
func TestTheStateIsInsideTheBlockAndAtItsRightEdge(t *testing.T) {
	m := focusedOn(
		openedProgramModel(t, theme.ProfileTrueColor, 120, 30),
		FocusHistory,
	)
	layout := LayoutFor(m.width, m.height)
	message := Message{ID: 1, Outgoing: true, Text: "ok", Time: "12:01"}
	label, _ := m.historyStateLabel(message)
	rows := entryRows(t, m, layout, feedTestWidth, message)
	outgoing := blockBackground(m, sideOutgoing)

	if len(rows) < 3 {
		t.Fatalf("the message took %d rows, want a blank, its text and its state", len(rows))
	}

	state := rows[len(rows)-1]
	from, to, count := bandOf(state, outgoing)
	if count == 0 {
		t.Fatal("the row of the state has no block on it")
	}

	// The state ends at the right edge of the block, and the block is the
	// same width as the row of the text above it.
	_, textTo, _ := bandOf(rows[1], outgoing)
	if to != textTo {
		t.Errorf("the state ends at column %d, the text above it at %d", to, textTo)
	}
	if !strings.Contains(cellText(state), label) {
		t.Errorf("the state is not on its own row: %q", cellText(state))
	}
	if indentOf(cellText(state)) >= from+count {
		t.Errorf("the state is not at the right edge of the block: %q", cellText(state))
	}
}

// With the setting on, each row of a block opens and closes with one half
// of a rounded end: a semicircle in the colour of the block, on the
// background of the feed, one column wide in both of the rules text is
// counted in.
//
// The width is the point of the two rules agreeing on it. A half the
// interface believes is one column and the terminal draws in two is a
// block that is a column wider on one side, and the row it is on is a
// column over the width of the feed, which the terminal wraps — and a
// wrapped row moves everything under it.
func TestTheRoundedEndsOfABlockAreOneColumnWideInBothRules(t *testing.T) {
	for _, mode := range []termwidth.Mode{termwidth.ModeGrapheme, termwidth.ModeCodepoint} {
		m := focusedOn(
			openedProgramModel(t, theme.ProfileTrueColor, 120, 30),
			FocusHistory,
		)
		m.widths = unmeasured(mode)
		m.nerdFont = true

		layout := LayoutFor(m.width, m.height)
		rows := entryRows(t, m, layout, feedTestWidth, Message{
			ID: 1, Outgoing: true, Text: "shipped the release notes", Time: "12:05",
		})

		ends := m.styles().roundedEnd(
			m.blockSurface(sideOutgoing, false), m.tokens().ChatBackground,
		)
		wantForeground := foregroundOf(ends.Render(termwidth.NerdHalfLeft))
		_, end := feedColumns(feedTestWidth, layout)

		if wantForeground == "" {
			t.Fatalf("%v: the rounded end is painted with no colour at all", mode)
		}

		for index, row := range rows[1:] {
			if got := len(row); got > feedTestWidth {
				t.Errorf(
					"%v: row %d is %d columns, the feed is %d",
					mode, index+1, got, feedTestWidth,
				)
			}

			// The two ends stand outside the block's own background, which
			// is what makes them read as cut out of it rather than painted
			// on it: the band in the middle is the block, and the halves
			// are the columns on either side of it.
			inner, last, count := bandOf(row, blockBackground(m, sideOutgoing))
			if count == 0 {
				t.Fatalf("%v: row %d of the block has no block on it", mode, index+1)
			}
			first, closing := inner-1, last+1

			if row[first].text != termwidth.NerdHalfLeft {
				t.Errorf(
					"%v: row %d opens with %q, want the left half of a rounded end",
					mode, index+1, row[first].text,
				)
			}
			if row[closing].text != termwidth.NerdHalfRight {
				t.Errorf(
					"%v: row %d closes with %q, want the right half of a rounded end",
					mode, index+1, row[closing].text,
				)
			}
			if closing != end {
				t.Errorf(
					"%v: row %d ends at column %d, want the last column of the feed (%d)",
					mode, index+1, closing, end,
				)
			}
			for _, column := range []int{first, closing} {
				if row[column].foreground != wantForeground {
					t.Errorf(
						"%v: row %d column %d is painted %q, want the colour of the block %q",
						mode, index+1, column, row[column].foreground, wantForeground,
					)
				}
				if row[column].background != feedBackground(m) {
					t.Errorf(
						"%v: row %d column %d stands on %q, want the feed's background",
						mode, index+1, column, row[column].background,
					)
				}
			}
		}
	}
}

// Without the setting there are no halves and the block has square corners,
// because a terminal without the font draws the halves as empty squares and
// two empty squares at the ends of every message are worse than the corners
// they were meant to replace.
func TestWithoutANerdFontTheEndsOfABlockAreSquare(t *testing.T) {
	m := focusedOn(
		openedProgramModel(t, theme.ProfileTrueColor, 120, 30),
		FocusHistory,
	)
	layout := LayoutFor(m.width, m.height)
	rows := entryRows(t, m, layout, feedTestWidth, Message{
		ID: 1, Outgoing: true, Text: "shipped the release notes", Time: "12:05",
	})

	for index, row := range rows {
		for column, cell := range row {
			if cell.text != termwidth.NerdHalfLeft &&
				cell.text != termwidth.NerdHalfRight {
				continue
			}
			t.Errorf(
				"row %d column %d is %q with the setting off",
				index, column, cell.text,
			)
		}
	}
}

// A terminal with no colour has no background for a block and no colour for
// a rounded end, so it draws neither: the words carry the meaning there and
// the shape of a block is not a word.
func TestNoColorDrawsNeitherTheEndsNorTheBackground(t *testing.T) {
	m := focusedOn(
		openedProgramModel(t, theme.ProfileNoColor, 120, 30),
		FocusHistory,
	)
	m.nerdFont = true

	layout := LayoutFor(m.width, m.height)
	rows := entryRows(t, m, layout, feedTestWidth, Message{
		ID: 1, Outgoing: true, Text: "shipped the release notes", Time: "12:05",
	})

	for index, row := range rows {
		for column, cell := range row {
			if cell.text == termwidth.NerdHalfLeft ||
				cell.text == termwidth.NerdHalfRight {
				t.Errorf(
					"row %d column %d draws a rounded end with no colour behind it",
					index, column,
				)
			}
			if cell.background != "" {
				t.Errorf(
					"row %d column %d is on %q, want no background at all",
					index, column, cell.background,
				)
			}
		}
	}
}

// A single-pane screen has no conversation beside it to keep a seam with,
// and a block of seventy per cent of forty columns is a message a user has
// to read three words at a time. There the block may fill the feed.
func TestANarrowScreenLetsTheBlockFillTheFeed(t *testing.T) {
	m := focusedOn(
		openedProgramModel(t, theme.ProfileTrueColor, 60, 30),
		FocusHistory,
	)
	layout := LayoutFor(m.width, m.height)
	if layout.Kind != LayoutNarrow {
		t.Fatalf("a 60 column screen is %v, want narrow", layout.Kind)
	}

	width := layout.ChatContentWidth()
	rows := entryRows(t, m, layout, width, Message{
		ID:       1,
		Outgoing: true,
		Text:     strings.Repeat("word ", 40),
		Time:     "12:05",
	})

	share := width * outgoingBubbleSharePercent / 100
	block := blockBackground(m, sideOutgoing)
	_, textRows := rowsSaying(rows, "word")
	if len(textRows) == 0 {
		t.Fatalf("the text is not on the screen")
	}

	first, last, _ := bandOf(textRows[0], block)
	if first < 0 {
		t.Fatalf("the text has no block behind it")
	}
	if got := last - first + 1; got <= share {
		t.Errorf(
			"the block is %d columns, want more than the %d of a narrow screen",
			got, share,
		)
	}
}

// The unread count is in the same column of every row, whatever the chat
// is about, and that column is the one the time above it ends in.
//
// A count that sits wherever the preview of its chat happened to end is a
// column the eye has to find on every row, and a column the eye has to
// find is a column that is not there: the number of unread messages is
// the one thing on this screen a user is scanning for, and a list that
// makes them look for it is a list they read instead.
func TestTheUnreadBadgeIsInTheSameColumnAsTheTime(t *testing.T) {
	for _, preview := range []string{"", "ok", strings.Repeat("word ", 40)} {
		m := chatListWith(t, preview)

		view := m.View()
		rows := renderedRows(t, m, view)
		layout := LayoutFor(m.width, m.height)
		width := layout.SidebarContentWidth()

		for index, chat := range m.chats {
			badge, badgeStyle := m.chatListUnreadBadge(chat)
			if badge == "" {
				continue
			}

			lines := m.chatListRowLines(m.chatListEntries()[index], false, layout, width)
			if len(lines) < 2 {
				t.Fatalf("preview %q: a chat takes %d rows", preview, len(lines))
			}

			head := renderedCells(t, m, lines[0])
			detail := renderedCells(t, m, lines[1])
			timeEnd := lastWord(head)
			from, to, count := bandOf(
				detail, backgroundParameters(badgeStyle.Render("x")),
			)

			if count == 0 {
				t.Fatalf(
					"preview %q: the badge of %q is not on the screen",
					preview, chat.Title,
				)
			}
			if to != timeEnd {
				t.Errorf(
					"preview %q: the badge of %q ends at column %d, the time above it at %d",
					preview, chat.Title, to, timeEnd,
				)
			}
			if to != width-1 {
				t.Errorf(
					"preview %q: the badge of %q ends at column %d, want the right edge of the row (%d)",
					preview, chat.Title, to, width-1,
				)
			}
			_ = from
			_ = rows
		}
	}
}

// A selected chat is selected right across: every cell of both of its rows
// and of the blank row under them, apart from the badge, which is a pill of
// its own so that the number in it can be read.
//
// A selection that stops where the words stop is a highlight under a name
// rather than a selected row, and a user cannot tell from it which chat the
// keys will open.
func TestTheSelectedChatIsSelectedRightAcrossItsRow(t *testing.T) {
	for _, preview := range []string{"ok", strings.Repeat("word ", 40)} {
		m := chatListWith(t, preview)
		layout := LayoutFor(m.width, m.height)
		width := layout.SidebarContentWidth()
		_, badgeStyle := m.chatListUnreadBadge(m.chats[0])
		pill := backgroundParameters(badgeStyle.Render("x"))

		lines := m.chatListRowLines(m.chatListEntries()[0], true, layout, width)
		if len(lines) != 3 {
			t.Fatalf("a chat takes %d rows, want its name, its preview and a blank", len(lines))
		}

		selected := selectionBackground(m)
		for index, line := range lines {
			cells := renderedCells(t, m, line)
			from, to, count := bandOf(cells, selected)
			_, _, badge := bandOf(cells, pill)

			if count+badge != width {
				t.Errorf(
					"preview %q: row %d has %d selected cells and a badge of %d, want the %d of the row",
					preview, index, count, badge, width,
				)
			}
			if from != 0 {
				t.Errorf(
					"preview %q: row %d is selected from column %d, want the first",
					preview, index, from,
				)
			}
			// The row is selected up to the badge, which is the last
			// thing in it: the cells between the end of the preview and
			// the badge are the selection, and a selection that stops
			// where the words stop is a highlight rather than a row.
			if to != width-1-badge {
				t.Errorf(
					"preview %q: row %d is selected up to column %d, want the badge to start at %d",
					preview, index, to, width-badge,
				)
			}
		}
	}
}

// chatListWith returns a model whose chat list is three rows long, with
// unread counts of two and one and one with nothing unread, and the first
// chat under the cursor.
//
// The previews are of the three lengths that matter: none, one word, and
// one longer than the row. The badge of a row must be in the same column
// in all of them.
func chatListWith(t *testing.T, preview string) Model {
	t.Helper()

	m := focusedOn(
		programModel(t, theme.ProfileTrueColor, 120, 30),
		FocusChatList,
	)
	m.chats = []Chat{
		{ID: 1, Title: "Anna Example", Unread: 2, Preview: preview, Time: "12:07"},
		{ID: 2, Title: "Release Room", Preview: "the tag is pushed", Time: "12:05", Kind: ChatKindGroup},
		{ID: 3, Title: "Notes", Unread: 7, Preview: "", Time: "11:58"},
	}
	m.chatsState = loadStateLoaded
	m.selectedChat = 0

	return m
}

// lastWord returns the column the last word of a row ends in, which is
// where the time of a chat stands.
func lastWord(row []renderedCell) int {
	for column := len(row) - 1; column >= 0; column-- {
		if strings.TrimSpace(row[column].text) != "" {
			return column
		}
	}

	return -1
}

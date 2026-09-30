package tui

import (
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
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
func blockBackground(m Model) string {
	return backgroundParameters(
		m.styles().on(m.blockSurface(false), m.styles().unstyled()).
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

// blockBody returns the rows of a block that carry its words, without the
// blank row that separates it from the message above it.
//
// That blank row is not part of the block the words are in: it is the gap
// between two messages, and a test about the width of the block or about
// the words in it is not about it.
func blockBody(rows [][]renderedCell) [][]renderedCell {
	first, last := 0, len(rows)
	for first < last && isBlankRow(rows[first]) {
		first++
	}
	for last > first && isBlankRow(rows[last-1]) {
		last--
	}

	return rows[first:last]
}

// isBlankRow reports whether a row of the feed is a row of nothing, which is
// the gap between two messages and not a row of either of them.
func isBlankRow(row []renderedCell) bool {
	for _, cell := range row {
		if strings.TrimSpace(cell.text) != "" {
			return false
		}
	}

	return true
}

// entryRows renders one message of a conversation the way the timeline
// does, at a given width of the feed, and returns the cells of every row it
// takes — the blank row above it and the air around it included.
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

// A block is never narrower than blockMinTextColumns of text, so a word in
// a message is a block and not a sliver.
//
// The floor is of the text and not of the block: the insets and the ends
// are added to it, and a test that measured the block would be measuring
// the insets along with the answer. It is also never larger than the name
// or the state of the message has to be — a block that cut "Anna Example
// <something long>" to sixteen columns to reach the floor would be a block
// that cannot say who sent the message.
func TestABlockIsNeverNarrowerThanSixteenColumnsOfText(t *testing.T) {
	m := focusedOn(
		openedProgramModel(t, theme.ProfileTrueColor, 120, 30),
		FocusHistory,
	)
	layout := LayoutFor(m.width, m.height)
	inset := layout.BlockInset()
	block := blockBackground(m)

	for name, message := range map[string]Message{
		"a word":   {ID: 1, Text: "ok", Author: "Anna", AuthorID: 5},
		"a name":   {ID: 2, Text: "ok", Author: strings.Repeat("n", 40), AuthorID: 5},
		"a text":   {ID: 3, Text: strings.Repeat("t", 30), Author: "Anna", AuthorID: 5},
		"nothing":  {ID: 4, Text: "", Author: "Anna", AuthorID: 5},
		"an emoji": {ID: 5, Text: "ok", Author: "Anna 🌍", AuthorID: 5},
	} {
		t.Run(name, func(t *testing.T) {
			rows := blockBody(entryRows(t, m, layout, feedTestWidth, message))
			if len(rows) == 0 {
				t.Fatal("the message took no rows")
			}

			first, last, count := bandOf(rows[0], block)
			if count == 0 {
				t.Fatalf("row 1 of the block has no block on it: %q", cellText(rows[0]))
			}

			want := maxInt(blockMinTextColumns, m.widths.StringWidth(message.Author))
			if got := last - first + 1 - 2*inset; got < want {
				t.Errorf(
					"the block is %d columns of text, want at least %d",
					got, want,
				)
			}
		})
	}
}

// The width of the feed the geometry below is proved on. It is a round
// number on purpose: a block is a number of columns and a share of a
// number, and a test that had to divide by whatever the pane turned out to
// be would be proving the pane and not the block.
const feedTestWidth = 90

// shortTestHeight is a screen below §3.4's shortLayoutHeight, where a
// message is its text and one row of it and nothing else. It is the screen
// a one-row block is drawn on: a block with a name or a state in it has a
// row for each, so the pill of §4.4 is a shape of a short screen.
const shortTestHeight = 16

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

	block := blockBackground(m)
	_, wantEnd := feedColumns(feedTestWidth, layout)
	label, _ := m.historyStateLabel(message)
	wantWidth := maxInt(m.widths.StringWidth(message.Text), m.widths.StringWidth(label)) +
		2*layout.BlockInset()

	rows = blockBody(rows)
	if len(rows) < 2 {
		t.Fatalf("the message took %d rows, want its text and its state", len(rows))
	}

	for index, row := range rows {
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

	block := blockBackground(m)
	want := feedTestWidth * outgoingBlockSharePercent / 100

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
// The two sides are drawn on ONE surface: the neutral one of the theme, the
// same for a message of the other side and for a message of this user. The
// own side used to have the accent's tint of it and the owner turned that
// down on 30.09, because a tinted own block almost hides the selection of
// the message under the cursor: the selection is drawn on the block, and a
// violet block under a selected surface is a selection nobody can find.
//
// So the two sides are told apart by the colour of their words and by the
// edge of the feed they are against, and this test says both: the surfaces
// are equal, the words are not, and neither surface is the feed's own.
func TestTheTwoSidesShareOneSurfaceAndAreToldApartByTheirWords(t *testing.T) {
	m := focusedOn(
		openedProgramModel(t, theme.ProfileTrueColor, 120, 30),
		FocusHistory,
	)
	m.chats[m.selectedChat].Messages = []Message{
		{ID: 1, Text: "from the other side", Time: "12:00", Author: "Anna", AuthorID: 7},
		{ID: 2, Outgoing: true, Text: "from this side", Time: "12:01"},
	}
	m.historyState = loadStateLoaded
	// The cursor is past the last message, so both blocks are on the plain
	// surface and neither is on the selection.
	m.selectedMsg = len(m.chats[m.selectedChat].Messages)
	m.timelineTop = 0

	view := m.View()
	cells := renderedRows(t, m, view)

	feed := feedBackground(m)
	own := blockBackground(m)
	if own == feed {
		t.Fatalf("a block is on the feed's own background %q", feed)
	}

	layout := LayoutFor(m.width, m.height)
	start, end := feedColumns(layout.ChatContentWidth(), layout)
	first, _ := conversationColumns(m)

	_, theirRows := rowsSaying(cells, "from the other side")
	_, mineRows := rowsSaying(cells, "from this side")
	if len(theirRows) == 0 || len(mineRows) == 0 {
		t.Fatalf("both messages are on the screen:\n%s", view)
	}

	// The surface of a block is the same on both sides: it is drawn in the
	// cells of the block, whichever side the block is on.
	for _, side := range []struct {
		name string
		rows [][]renderedCell
	}{
		{name: "the other side", rows: theirRows},
		{name: "this side", rows: mineRows},
	} {
		for _, row := range side.rows {
			from, to, count := bandOf(row[first:], own)
			if count == 0 {
				t.Fatalf(
					"the block of a message of %s is on no surface of its own:\n%s",
					side.name, view,
				)
			}

			// The block is against the margin of its own side: the left
			// one for the other side, the right one for this side.
			if side.name == "the other side" && from != start {
				t.Errorf(
					"the block of the other side starts at lane column %d, want %d",
					from, start,
				)
			}
			if side.name == "this side" && to != end {
				t.Errorf(
					"the block of this user ends at lane column %d, want %d",
					to, end,
				)
			}
			_ = to
		}
	}

	// The words are what tells the two sides apart, and the owner kept the
	// accent for the words of this user (30.09): OutgoingMessage is the
	// accent and IncomingMessage is the light neutral of the theme.
	own2 := foregroundParameters(
		m.styles().text(m.tokens().OutgoingMessage).Render("x"),
	)
	theirs := foregroundParameters(
		m.styles().text(m.tokens().IncomingMessage).Render("x"),
	)
	if own2 == theirs {
		t.Errorf(
			"the words of both sides are %q, so the sides are told apart by the edge alone",
			own2,
		)
	}
	if m.tokens().OutgoingMessage != m.tokens().Focus {
		t.Errorf(
			"the words of this user are %v, want the accent %v (the owner, 30.09)",
			m.tokens().OutgoingMessage, m.tokens().Focus,
		)
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
	incoming := blockBackground(m)

	rows = blockBody(rows)
	if len(rows) < 2 {
		t.Fatalf("the message took %d rows, want its name and its text", len(rows))
	}

	head, _, count := bandOf(rows[0], incoming)
	if count == 0 {
		t.Fatal("the row of the name has no block on it")
	}
	if !strings.Contains(cellText(rows[0]), messageAuthor(message)) {
		t.Errorf("the name is not on the first row of the block: %q", cellText(rows[0]))
	}
	if head != layout.FeedMargin() {
		t.Errorf("the block starts at column %d, want the left margin at %d", head, layout.FeedMargin())
	}

	// And the text is inside the same block as the name, which is the
	// whole of what says they are one message.
	from, to, _ := bandOf(rows[1], incoming)
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
	outgoing := blockBackground(m)

	rows = blockBody(rows)
	if len(rows) < 2 {
		t.Fatalf("the message took %d rows, want its text and its state", len(rows))
	}

	state := rows[len(rows)-1]
	from, to, count := bandOf(state, outgoing)
	if count == 0 {
		t.Fatal("the row of the state has no block on it")
	}

	// The state ends at the right edge of the block, and the block is the
	// same width as the row of the text above it.
	_, textTo, _ := bandOf(rows[0], outgoing)
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

// With the setting on, a block of one row is a pill: it opens and closes
// with one half of a rounded end, a semicircle in the colour of the block
// on the background of the feed, one column wide in both of the rules text
// is counted in.
//
// The pill is a block of one row because the halves are one row tall. The
// block proved here is a message from the other side on a screen too short
// for the name and the time, where a message is one row of words and
// nothing else — the screen where a one-line reply is one line, which is
// where the pill is the whole of the shape rather than a row of it.
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

		layout := LayoutFor(feedTestWidth, shortTestHeight)
		rows := entryRows(t, m, layout, feedTestWidth, Message{
			ID: 1, Text: "the build is green again",
			Author: "Anna Example", Time: "12:05",
		})

		ends := m.styles().blockEdge(
			m.blockSurface(false), m.tokens().ChatBackground,
		)
		wantForeground := foregroundOf(ends.Render(termwidth.NerdHalfLeft))
		start, _ := feedColumns(feedTestWidth, layout)

		if wantForeground == "" {
			t.Fatalf("%v: the rounded end is painted with no colour at all", mode)
		}

		if got := len(blockBody(rows)); got != 1 {
			t.Fatalf(
				"%v: the block took %d rows of words, want the one row a pill is",
				mode, got,
			)
		}

		for index, row := range blockBody(rows) {
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
			inner, last, count := bandOf(row, blockBackground(m))
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
			// The block is against the margin of the feed, so the left
			// half opens one column in from it — the one column the
			// selection marker stands in.
			if first != start {
				t.Errorf(
					"%v: row %d opens at column %d, want the margin of the feed (%d)",
					mode, index+1, first, start,
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

// A block of more than one row has no ends at all, with the setting on.
//
// The halves are one row tall, so a block of two rows with a half circle on
// each of them is two pills stacked on each other: a shape that reads as
// two messages rather than one, and one the report of a screenshot with it
// as a double and narrow one. So the ends belong to a block of one row, and
// a block of more is a rectangle with square sides and half a row of air at
// each end — the air carrying the roundness that the halves cannot.
//
// The block below is a message of this user, which is a row of text and a
// row of state: the shortest block of more than one row there is, and the
// one a report of a screen would have been about.
func TestABlockOfMoreThanOneRowHasNoEnds(t *testing.T) {
	for _, mode := range []termwidth.Mode{termwidth.ModeGrapheme, termwidth.ModeCodepoint} {
		m := focusedOn(
			openedProgramModel(t, theme.ProfileTrueColor, 120, 30),
			FocusHistory,
		)
		m.widths = unmeasured(mode)
		m.nerdFont = true

		layout := LayoutFor(m.width, m.height)
		rows := entryRows(t, m, layout, feedTestWidth, Message{
			ID: 1, Outgoing: true, Text: "ok", Time: "12:05",
		})

		if got := len(blockBody(rows)); got < 2 {
			t.Fatalf(
				"%v: the block took %d rows of words, want the text and the state",
				mode, got,
			)
		}

		for index, row := range rows {
			for column, cell := range row {
				if cell.text != termwidth.NerdHalfLeft &&
					cell.text != termwidth.NerdHalfRight {
					continue
				}
				t.Errorf(
					"%v: row %d column %d is %q: the halves are one row tall, "+
						"so a block of %d rows has none",
					mode, index, column, cell.text, len(blockBody(rows)),
				)
			}
		}
	}
}

// Without the setting there are no halves and the block has square corners,
// because a terminal without the font draws the halves as empty squares and
// two empty squares at the ends of every message are worse than the corners
// they were meant to replace.
//
// The block is the one-row block of the pill above, because a block of more
// than one row has square corners with the setting on as well: without a
// test of the pill, this one would pass on a block that has no ends to draw
// in the first place and would be saying nothing at all.
func TestWithoutANerdFontTheEndsOfABlockAreSquare(t *testing.T) {
	m := focusedOn(
		openedProgramModel(t, theme.ProfileTrueColor, 120, 30),
		FocusHistory,
	)
	rows := entryRows(t, m, LayoutFor(feedTestWidth, shortTestHeight), feedTestWidth, Message{
		ID: 1, Text: "the build is green again",
		Author: "Anna Example", Time: "12:05",
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

	share := width * outgoingBlockSharePercent / 100
	block := blockBackground(m)
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
//
// The column is the one the words end at rather than the edge of the pane,
// because every row keeps its columns of air inside it on each side (§4.2).
// The time and the count stop short of the edge together.
//
// It is proved on the chosen chat as well as on the others, because the
// count is the one run of the row with a surface of its own and the
// surface of the chosen chat is the selection: a count written in the wrong
// colour on the wrong background is a count nobody can read, and the
// chosen chat is the one the reader is looking at.
func TestTheUnreadBadgeIsInTheSameColumnAsTheTime(t *testing.T) {
	for _, preview := range []string{"", "ok", "[photo]", strings.Repeat("word ", 40)} {
		for _, chosen := range []bool{false, true} {
			m := chatListWith(t, preview)
			if chosen {
				m.selectedChat = 1
			}

			layout := LayoutFor(m.width, m.height)
			width := layout.SidebarContentWidth()
			inset := layout.ChatListInset()
			last := inset + maxInt(width-2*inset, 1) - 1

			for index, chat := range m.chats {
				badge := m.chatListUnreadBadge(chat, m.selectedSurface(chosen))
				if badge.inner == "" {
					continue
				}

				lines := m.chatListRowLines(
					m.chatListEntries()[index], chosen, layout, width,
				)
				if len(lines) < 2 {
					t.Fatalf("preview %q: a chat takes %d rows, want two of words and a gap",
						preview, len(lines))
				}

				head := renderedCells(t, m, lines[len(lines)-3])
				detail := renderedCells(t, m, lines[len(lines)-2])
				timeEnd := lastWord(head)
				from, to, count := bandOf(
					detail, backgroundParameters(badge.pill.Render("x")),
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
				if to != last {
					t.Errorf(
						"preview %q: the badge of %q ends at column %d, want the last word of the row (%d)",
						preview, chat.Title, to, last,
					)
				}
				// The preview is cut so that at least two columns stand
				// between it and the count: a pill one column from the last
				// letter of the preview is one run with the preview.
				if gap := from - lastWordBefore(detail, from); gap < chatListBadgeGap {
					t.Errorf(
						"preview %q: the badge of %q starts %d columns after the preview, want at least %d",
						preview, chat.Title, gap, chatListBadgeGap,
					)
				}
			}
		}
	}
}

// A selected chat is selected right across: every cell of both of its rows
// of words carries the Selected, the columns of air inside the row included
// and the cells between the end of the preview and the count with them.
//
// A selection that stops where the words stop is a highlight under a name
// rather than a selected row, and a short preview is where it stops: the
// count is a pill that sits at the right edge whatever the preview says, so
// a preview of two words leaves the whole middle of the row empty, and
// every one of those cells has to be the selection or the card has a hole
// in it. The count is the one run of the row with a surface of its own, so
// that the number in it can be read.
func TestTheSelectedChatIsSelectedRightAcrossItsRow(t *testing.T) {
	for _, preview := range []string{"", "ok", "[photo]", strings.Repeat("word ", 40)} {
		m := chatListWith(t, preview)
		layout := LayoutFor(m.width, m.height)
		width := layout.SidebarContentWidth()
		badge := m.chatListUnreadBadge(m.chats[0], m.tokens().Selected)
		pill := backgroundParameters(badge.pill.Render("x"))

		lines := m.chatListRowLines(m.chatListEntries()[0], true, layout, width)
		if len(lines) != 3 {
			t.Fatalf("a chat takes %d rows, want two of words and a gap", len(lines))
		}

		selected := selectionBackground(m)

		for index, line := range lines[:2] {
			cells := renderedCells(t, m, line)
			// The air rows of the card carry the colour of the selection
			// in the foreground of a half block; the rows of words carry it
			// in the background, and every cell of them that is not the
			// count has to carry it.
			_, _, count := bandOf(cells, selected)
			_, _, badgeColumns := bandOf(cells, pill)

			if count+badgeColumns != width {
				t.Errorf(
					"preview %q: row %d has %d selected cells and a count of %d, want the %d of the row",
					preview, index, count, badgeColumns, width,
				)
			}
			for column, cell := range cells {
				if cell.background != selected && cell.background != pill {
					t.Errorf(
						"preview %q: row %d column %d is on %q, want the selection or the count",
						preview, index, column, cell.background,
					)
				}
			}
			// The columns of air inside the row are the selection too: a
			// card that stopped two columns short on each side is a
			// stripe with an outline, which is the band §4.2 removed.
			for _, column := range []int{0, width - 1} {
				if cells[column].background != selected {
					t.Errorf(
						"preview %q: row %d column %d is the air of the row on %q, want the selection %q",
						preview, index, column, cells[column].background, selected,
					)
				}
			}
		}
	}
}

// Every row of the chat list keeps air inside it on each side — two columns
// on a two-pane screen, one on a single-pane one — so the first and the last
// cell of a row are a space on the background of the row, and the name, the
// preview, the time and the count are all strictly inside it: nothing of a
// chat touches the edge of the pane or of the gap beside it.
//
// It is checked on the cells of every row of every chat, chosen or not, and
// at all three of the layouts. A word that touches an edge is a list the
// pane is wearing rather than a list of chats, and the words of the chosen
// chat have to keep the same air as the words of every other one — the
// chosen chat is the row whose background is the Selected, and the air
// inside it is the Selected too.
func TestEveryRowOfTheListKeepsItsAirInsideIt(t *testing.T) {
	for _, size := range [][2]int{{120, 30}, {80, 30}, {60, 30}} {
		for _, chosen := range []int{0, 1, 2} {
			m := focusedOn(
				programModel(t, theme.ProfileTrueColor, size[0], size[1]),
				FocusChatList,
			)
			m.chats = []Chat{
				{ID: 1, Title: "Anna Example", Unread: 2, Preview: "the build is green", Time: "12:07"},
				{ID: 2, Title: "Release Room", Unread: 63, Preview: "[photo]", Time: "12:05"},
				{ID: 3, Title: "Notes", Preview: "", Time: "11:58"},
			}
			m.chatsState = loadStateLoaded
			m.selectedChat = chosen

			layout := LayoutFor(m.width, m.height)
			width := chatListPaneWidth(layout)
			inset := layout.ChatListInset()
			want := 2
			if layout.Kind == LayoutNarrow {
				want = 1
			}
			if inset != want {
				t.Fatalf(
					"size=%v: the air inside a row is %d columns, want %d",
					size, inset, want,
				)
			}

			rows, selected := m.chatListRows(layout, width)
			if selected < 0 {
				t.Fatalf("size=%v: the list has no row under the cursor", size)
			}

			list := backgroundParameters(
				m.styles().on(m.tokens().SidebarBackground, m.styles().unstyled()).
					Render("x"),
			)
			highlight := backgroundParameters(
				m.styles().selected(true).Render("x"),
			)

			for index, row := range rows {
				for line, rendered := range row {
					cells := renderedCells(t, m, rendered)
					if len(cells) != width {
						t.Errorf(
							"size=%v chat=%d row=%d: the row is %d columns, want %d",
							size, index, line, len(cells), width,
						)

						continue
					}

					// The card of the chosen chat is its two rows of
					// words and the air inside them; the row under it is
					// the gap between two chats, and a gap that changed
					// colour with the selection would be a band under the
					// chosen one. Every other chat is on the background
					// of the list.
					own := list
					if index == selected && line < 2 {
						own = highlight
					}

					for _, column := range []int{0, width - 1} {
						if cells[column].text != " " {
							t.Errorf(
								"size=%v chat=%d row=%d: cell %d is %q, want the air of the row",
								size, index, line, column, cells[column].text,
							)
						}
						if cells[column].background != own {
							t.Errorf(
								"size=%v chat=%d row=%d: cell %d is on %q, want the background of the row %q",
								size, index, line, column, cells[column].background, own,
							)
						}
					}

					// And no word of the row is inside the air on either
					// side: the name and the preview begin after it, and
					// the time and the count end before it.
					for _, column := range append(
						columnRange(0, inset), columnRange(width-inset, width)...,
					) {
						if strings.TrimSpace(cells[column].text) != "" {
							t.Errorf(
								"size=%v chat=%d row=%d: cell %d is %q, want the air of the row",
								size, index, line, column, cells[column].text,
							)
						}
					}
				}
			}
		}
	}
}

// chatListPaneWidth returns the width the chat list is drawn at: the width
// of the sidebar on a two-pane screen and of the whole content area on a
// single-pane one, which is where the list is when there is no conversation
// beside it to be told apart from.
func chatListPaneWidth(layout Layout) int {
	if layout.Kind == LayoutNarrow {
		return layout.FullContentWidth()
	}

	return layout.SidebarContentWidth()
}

// columnRange returns the columns of a run of the given length.
func columnRange(from, to int) []int {
	columns := make([]int, 0, maxInt(to-from, 0))
	for column := from; column < to; column++ {
		columns = append(columns, column)
	}

	return columns
}

// The count in the header of the list is at the right edge of the row, and
// the row is exactly as wide as the pane. Fitted to the pane the count is a
// row longer than the pane, and the pane cuts it: the goldens of 30899d3
// read "Chats 3 unread \u2026" on every screen of the program, and an
// ellipsis in the header of every screen is a defect in the header of every
// screen. The owner's mockup has "Chats" at the left and "N unread" at the
// right edge of the same row (30.09).
func TestTheCountInTheHeaderEndsAtTheRightEdgeOfThePane(t *testing.T) {
	for _, width := range []int{120, 100, 80, 72, 60} {
		m := focusedOn(
			programModel(t, theme.ProfileTrueColor, width, 30),
			FocusChatList,
		)
		m.chats = []Chat{
			{ID: 1, Title: "Anna Example", Unread: 1234, Time: "12:07"},
			{ID: 2, Title: "Release Room", Time: "12:05"},
		}
		m.chatsState = loadStateLoaded

		layout := LayoutFor(m.width, m.height)
		pane := chatListPaneWidth(layout)
		lines := m.chatListLines(layout, pane, layout.Height)
		heading := plain(lines[0])

		// The row is as wide as the pane, and not a column more: a row
		// longer than the pane is a row the region of the pane cuts, and
		// the cut is the ellipsis the goldens of 30899d3 were full of.
		cells := renderedCells(t, m, lines[0])
		if len(cells) != pane {
			t.Errorf(
				"width=%d: the heading is %d columns, want the %d of the pane: %q",
				width, len(cells), pane, heading,
			)
		}

		// The count is at the right edge of the words of a row, which is
		// where the mockup of the owner has it and where the time and the
		// badge of a chat are.
		if got, want := lastWord(cells), pane-1-layout.ChatListInset(); got != want {
			t.Errorf(
				"width=%d: the count ends at column %d, want %d: %q",
				width, got, want, heading,
			)
		}

		// Where the pane has the room for the title and the whole count, the
		// count is whole: an ellipsis in the header of a screen that has
		// room for the words is a word the header does not say.
		room := pane - 2*layout.ChatListInset()
		full := m.widths.StringWidth(chatListTitle) +
			chatListHeadingGap + m.widths.StringWidth(m.chatListSummaryText())
		if room >= full && strings.Contains(heading, ellipsis) {
			t.Errorf("width=%d: the header is %q, want no ellipsis in it", width, heading)
		}
	}
}

// The words of the chats start at the same column as the title of the pane
// and as the "/ search" under it, and the time, the badge and the count of
// the header share one right edge.
//
// The mockup of the owner has one left edge for the header and for every
// name and preview under it, and one right edge for the time, the count and
// the count of the header. The goldens of 30899d3 had the header on the
// third column and the names on the seventh: the marker of the selection
// took a column of its own in front of the words, and the preview took
// another, so the list looked indented for a reason nobody could find.
func TestTheHeaderAndTheWordsOfTheChatsShareTheirEdges(t *testing.T) {
	m := focusedOn(
		programModel(t, theme.ProfileTrueColor, 120, 30),
		FocusChatList,
	)
	m.chats = []Chat{
		{ID: 1, Title: "Anna Example", Unread: 2, Preview: "the build is green", Time: "12:07"},
		{ID: 2, Title: "Release Room", Preview: "the tag is pushed", Time: "12:05"},
	}
	m.chatsState = loadStateLoaded
	m.selectedChat = 0

	layout := LayoutFor(m.width, m.height)
	pane := chatListPaneWidth(layout)
	lines := m.chatListLines(layout, pane, layout.Height)
	rows, _ := m.chatListRows(layout, pane)

	// The left edge: the title of the pane, the "/ search" under it, the
	// name of the chosen chat and its preview are on one column.
	for name, cells := range map[string][]renderedCell{
		"the title of the pane": renderedCells(t, m, lines[0]),
		"the search line":       renderedCells(t, m, lines[1]),
		"a name":                renderedCells(t, m, rows[0][0]),
		"a preview":             renderedCells(t, m, rows[0][1]),
	} {
		if got := firstWord(cells); got != layout.ChatListInset() {
			t.Errorf(
				"%s starts at column %d, want the %d columns of air inside the row",
				name, got, layout.ChatListInset(),
			)
		}
	}

	// The right edge: the time, the badge and the count of the header are all
	// on the same column, and it is the column the last word of a row ends
	// in.
	right := pane - 1 - layout.ChatListInset()
	for name, cells := range map[string][]renderedCell{
		"the count of the header": renderedCells(t, m, lines[0]),
		"a time":                  renderedCells(t, m, rows[0][0]),
	} {
		if got := lastWord(cells); got != right {
			t.Errorf("%s ends at column %d, want %d", name, got, right)
		}
	}

	// The badge is a pill, so its number ends a column short of the right
	// edge: the column of the pill's own air after the number is what lines
	// up with the time and with the count of the header.
	if got := lastWord(renderedCells(t, m, rows[0][1])) + 1; got != right {
		t.Errorf("the pill of the badge ends at column %d, want %d", got, right)
	}
}

// firstWord returns the column the first word of a row starts in, and -1
// when the row has no word at all.
func firstWord(row []renderedCell) int {
	for column, cell := range row {
		if strings.TrimSpace(cell.text) != "" {
			return column
		}
	}

	return -1
}

// The count of a chat is a pill with a space on each side of the number,
// and with a Nerd Font it is rounded with the two halves the font provides,
// in the colour of the pill on the background of the row.
func TestTheCountOfAChatIsAPillAndRoundsWithTheFont(t *testing.T) {
	for _, nerd := range []bool{false, true} {
		m := chatListWith(t, "ok")
		m.nerdFont = nerd

		layout := LayoutFor(m.width, m.height)
		width := layout.SidebarContentWidth()
		badge := m.chatListUnreadBadge(m.chats[0], m.tokens().Selected)

		if badge.inner != " 2 " {
			t.Errorf("nerd_font=%v: the pill says %q, want a space on each side of the number", nerd, badge.inner)
		}
		if !nerd {
			if badge.left != "" || badge.right != "" {
				t.Errorf("the pill is %q with the setting off", badge.text())
			}

			continue
		}

		lines := m.chatListRowLines(m.chatListEntries()[0], true, layout, width)
		detail := renderedCells(t, m, lines[1])
		text := cellText(detail)
		if !strings.Contains(text, termwidth.NerdHalfLeft) ||
			!strings.Contains(text, termwidth.NerdHalfRight) {
			t.Fatalf("the pill is not rounded: %q", text)
		}

		// The halves are the colour of the pill on the background of the
		// row, which is what makes the pill a pill rather than two
		// characters of a font.
		edge := m.styles().blockEdge(m.tokens().Unread, m.tokens().Selected)
		want := foregroundOf(edge.Render(termwidth.NerdHalfLeft))
		_, left, _ := bandOf(detail, backgroundParameters(m.tokens().Selected.Print()))
		_ = left
		for index, cell := range detail {
			if cell.text != termwidth.NerdHalfLeft {
				continue
			}
			if cell.foreground != want {
				t.Errorf(
					"the left half of the pill is at column %d painted %q, want the colour of the pill %q",
					index, cell.foreground, want,
				)
			}
		}
	}
}

// A half block is a glyph and not a patch of colour, and the owner's
// Terminal draws its rows with a gap: a row of `▄` above a block and a row of
// `▀` below it do not meet the block, and the three of them come out as
// three layers of a shade rather than as one shape. So there are no half
// blocks anywhere — neither around a message nor around the card of the
// chosen chat — and this is the test that says so on the cells of a whole
// screen with both panes on it.
//
// A block is its full cells and the blank row between two messages is the
// only air there is. The rounded ends of a block with a Nerd Font are the
// only drawing left that rounds one, and they are on its left and its right.
func TestNoHalfBlockIsDrawnAnywhereOnTheScreen(t *testing.T) {
	m := focusedOn(
		programModel(t, theme.ProfileTrueColor, 120, 30),
		FocusChatList,
	)
	m.chats = []Chat{
		{ID: 1, Title: "Anna Example", Unread: 2, Preview: "the build is green", Time: "12:07"},
		{ID: 2, Title: "Release Room", Unread: 63, Preview: "[photo]", Time: "12:05"},
	}
	m.chatsState = loadStateLoaded
	m.selectedChat = 0
	m.nerdFont = true

	m, _ = updateModel(t, m, press(tea.KeyEnter))
	m, _ = updateModel(t, m, historyLoadedMsg{
		chatID:    1,
		operation: m.historyOperation,
		page: HistoryPage{Messages: []Message{
			{ID: 2, Text: "I will take the release notes", Time: "12:04", Author: "Anna", AuthorID: 5},
			{ID: 1, Text: "the build is green again", Time: "12:02", Outgoing: true},
		}},
	})
	m = m.scrollToNewest()

	for row, cells := range renderedRows(t, m, m.View()) {
		for column, cell := range cells {
			if cell.text != halfBlockLower && cell.text != halfBlockUpper {
				continue
			}

			t.Errorf(
				"row %d column %d draws %q: the owner's Terminal draws its rows "+
					"with a gap, so a row of them is a band of its own and not "+
					"air around a block",
				row, column, cell.text,
			)
		}
	}
}

// The two glyphs themselves, stated here rather than in termwidth: nothing
// draws them any more, and a constant that is kept for a drawing that is
// gone is a suggestion to draw it again.
const (
	halfBlockLower = "▄"
	halfBlockUpper = "▀"
)

// chatListWith returns a model whose chat list is three rows long, with
// unread counts of two and one and one with nothing unread, and the first
// chat under the cursor.
//
// The previews are of the three lengths that matter: none, one word, and
// one longer than the row. The count of a row must be in the same column
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

// lastWordBefore returns the column the last word of a row before a given
// column ends in, which is where the preview of a chat stands once the
// count is not counted as part of it.
func lastWordBefore(row []renderedCell, before int) int {
	for column := before - 1; column >= 0; column-- {
		if strings.TrimSpace(row[column].text) != "" {
			return column
		}
	}

	return -1
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

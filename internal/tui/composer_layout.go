package tui

// This file is how a draft becomes rows.
//
// A draft is one run of runes; a screen is rows of columns. The rows are
// worked out here, from the width of the composer, and nothing is ever
// written back into the text: a wrap is a fact about the terminal, not
// about the message, and a message that had its own newlines inserted for
// the screen would be sent with them.
//
// Wrapping is hard, at the width, rather than by word. A draft is being
// written, not read: the cursor has to be able to sit at a known column of
// a known row, and up and down have to mean the row above and the row
// below. Word wrapping would make both of them approximate.

// composerRow is one row of the draft as it is drawn.
type composerRow struct {
	// start is the index in the text of the first rune of the row.
	start int

	// text is what the row shows.
	text string
}

// composerLayout is a draft laid out in rows, with the cursor placed in
// one of them.
type composerLayout struct {
	rows []composerRow

	// cursorRow and cursorColumn are where the cursor is drawn, in rows and
	// in terminal columns.
	cursorRow    int
	cursorColumn int

	// cursorOffset is the same place as an index into the text, which is
	// how the buffer says where the cursor is. The view needs both: the
	// offset to cut the row at a cluster boundary, and the column to know
	// where the row ends on the screen.
	cursorOffset int
}

// layoutComposer lays a draft out in rows of the given width.
//
// A draft always has at least one row, so an empty composer is a row with a
// cursor in it and not an empty region.
func layoutComposer(text []rune, cursor, width int) composerLayout {
	if width < 1 {
		width = 1
	}

	cursor = clampIndex(cursor, len(text))

	var (
		layout composerLayout
		row    = composerRow{start: 0}
		column int
	)

	appendRow := func() {
		layout.rows = append(layout.rows, row)
	}

	for index, cluster := range graphemes(text) {
		if index == cursor {
			layout.cursorRow = len(layout.rows)
			layout.cursorColumn = column
			layout.cursorOffset = cursor
		}

		if cluster[0] == '\n' {
			appendRow()
			row = composerRow{start: index + 1}
			column = 0

			continue
		}

		// A cluster wider than the whole field stays on its own row and
		// overflows it. There is nowhere else to put it, and a row of its
		// own is closer to the truth than splitting the cluster in half.
		size := cellWidth(string(cluster))
		if column+size > width {
			appendRow()
			row = composerRow{start: index}
			column = 0
		}

		row.text += string(cluster)
		column += size
	}

	if index := len(text); index == cursor {
		layout.cursorRow = len(layout.rows)
		layout.cursorColumn = column
		layout.cursorOffset = cursor
	}

	appendRow()

	return layout
}

// lastRow returns the index of the last row of the layout.
func (l composerLayout) lastRow() int {
	return len(l.rows) - 1
}

// indexAt returns the index in the text of a column of a row, which is
// where up and down put the cursor.
//
// A column past the end of a row lands at the end of it: moving down into
// a short line puts the cursor where the text stops, which is what every
// editor does and the only thing that can be done with a cursor.
func (l composerLayout) indexAt(row, column int) int {
	if len(l.rows) == 0 {
		return 0
	}
	if row < 0 {
		row = 0
	}
	if row > l.lastRow() {
		row = l.lastRow()
	}

	offset := l.rows[row].start
	used := 0

	for _, cluster := range graphemes([]rune(l.rows[row].text)) {
		if used >= column {
			break
		}
		offset += len(cluster)
		used += cellWidth(string(cluster))
	}

	return offset
}

// visibleRows returns the rows the composer shows, the last of which is the
// one the cursor is on.
//
// The window follows the cursor rather than the text: §4.5 requires the
// row with the cursor to be visible, and a draft that is longer than the
// field is normal rather than an edge case.
func (l composerLayout) visibleRows(height int) []composerRow {
	if height < 1 {
		height = 1
	}

	last := l.cursorRow
	if last > l.lastRow() {
		last = l.lastRow()
	}
	if last < 0 {
		last = 0
	}

	first := last - height + 1
	if first < 0 {
		first = 0
	}
	if first > len(l.rows)-1 {
		first = len(l.rows) - 1
	}

	return l.rows[first : last+1]
}

// cursorIn reports whether the cursor is on a row and where in it, as the
// number of runes before the cursor on that row.
//
// The offset is in runes and not in columns because that is what the
// buffer holds: the cursor is an index into the text, and the row the
// cursor is on starts at a rune of it. A column would have to be turned
// back into a rune, and a cluster that is two cells wide would be turned
// back into the wrong one.
//
// It is the row number that decides, not the row: a row is its own start
// index, and asking "is this row the one the cursor is on" that way says
// yes to every row there is.
func (l composerLayout) cursorIn(row composerRow, number int) (int, bool) {
	if number < 0 || number > l.lastRow() {
		return 0, false
	}
	if number != l.cursorRow {
		return 0, false
	}

	return clampIndex(l.cursorOffset-row.start, len([]rune(row.text))), true
}

// rowNumber returns the number of a row in the layout.
func (l composerLayout) rowNumber(row composerRow) int {
	for number, candidate := range l.rows {
		if candidate.start == row.start {
			return number
		}
	}

	return -1
}

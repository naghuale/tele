package tui

import (
	"strconv"
	"strings"
)

// Where the letters after an emoji are drawn is not the terminal's decision.
//
// The owner's screen of 01.10: in the macOS Terminal a pagoda — U+26E9, with
// nothing behind it — is drawn two cells wide out of the emoji font and the
// cursor advances one. A letter written after it lands inside the picture
// unless the program says where the next cell is, and a message whose text
// mixes a flag with Cyrillic came out with its second line a cell left of
// the block and a dark gap at the end of the first.
//
// No width rule fixes that, because what is wrong is the terminal's own
// advance rather than the program's count of it. Two things do. Every
// emoji-like cluster is given two cells of the row (termwidth.EmojiLike),
// and the cursor is placed at the end of those two cells instead of being
// left where the terminal left it. A row is therefore written as an
// absolute column at its start and an absolute column after every
// emoji-like cluster in it, which is a handful of bytes on the rows that
// have one and none at all on the rows that do not.

// cursorColumn returns the sequence that puts the cursor in the given column
// of its row, counting from one as the sequence does.
//
// It is the horizontal absolute position of the terminal, and it is written
// where a cell has to be rather than where the last write left the cursor.
// The renderer paints the rows that changed and no others, so the cursor is
// wherever the last row it painted ended: a row that does not say where it
// starts is a row that starts wherever the row before it happened to end.
func cursorColumn(column int) string {
	return "\x1b[" + strconv.Itoa(maxInt(column, 1)) + "G"
}

// positionRows returns the screen with every row of it saying where it
// starts.
//
// It is one pass over the rows of a frame rather than something the
// regions do, because a region is a piece of a row and not always a whole
// one: the row of a two-pane screen is the row of the list and the row of
// the conversation, and the second of those is positioned by the joiner that
// put it there. A position written by the region it belongs to would put
// that piece back at the first column of the screen and draw it over the
// first one.
func positionRows(view string) string {
	rows := strings.Split(view, "\n")
	for index, row := range rows {
		rows[index] = cursorColumn(1) + row
	}

	return strings.Join(rows, "\n")
}

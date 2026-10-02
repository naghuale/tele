package tui

import (
	"strings"

	"telecli/internal/tui/theme"
)

// This file draws the popup of §12.3 and the question of §12.3.
//
// A popup here is a raised surface, one accent line and a shadow. It is not
// a frame: §1 and §24 forbid frames, and a frame around four words of menu
// would be the loudest thing on a screen whose whole argument is that a
// conversation is not a table.

// popupInsetColumns is how far the popup is set off the left of the
// conversation, and popupShadowColumns is how wide the shadow under it is.
//
// The inset is what tells a terminal with no colour that the menu is above
// something: a profile that cannot print a shadow still gets a menu that
// does not look like another line of the conversation.
const (
	popupInsetColumns  = 3
	popupShadowColumns = 2
	popupPaddingLeft   = 1
	popupPaddingRight  = 1
)

// popupLine is one line of a popup with its own padding inside the raised
// surface: the surface is the popup, and the padding is what keeps the text
// off its left edge and its accent line.
func popupLine(text string) string {
	return strings.Repeat(" ", popupPaddingLeft) +
		text +
		strings.Repeat(" ", popupPaddingRight)
}

// actionSheetRows returns the rows of the sheet, or none when it is closed.
//
// The rows are the whole sheet: one line per item, and no title. A title
// would say what the menu is for, and the menu is only ever about the
// message under the cursor.
func (m Model) actionSheetRows() []string {
	if !m.actionSheet.open {
		return nil
	}

	styles := m.styles()
	rows := make([]string, 0, len(m.actionSheet.items))

	for index, item := range m.actionSheet.items {
		label := item.label
		if !m.styles().attributesVisible() && index == m.actionSheet.cursor {
			// §14: without attributes the selection is a character, not a
			// colour. A menu whose selection is a colour is a menu a user
			// on a colourless terminal cannot read at all.
			label = theme.SelectionMark + " " + label
		}

		rows = append(
			rows,
			styles.popupItem(index == m.actionSheet.cursor, m.tokens()).
				Render(popupLine(label)),
		)
	}

	return m.popupRows(rows)
}

// confirmModalRows returns the rows of the question of §12.3, or of the
// question the way out asks about a draft.
func (m Model) confirmModalRows() []string {
	if !m.modal.open {
		return nil
	}

	question := m.modal.question()
	styles := m.styles()
	rows := []string{
		styles.popupTitle(m.tokens()).Render(popupLine(question.title)),
		"",
	}
	for _, line := range strings.Split(question.explanation, "\n") {
		rows = append(
			rows,
			styles.popupBody(m.tokens()).Render(popupLine(line)),
		)
	}
	rows = append(rows, "")

	for index, label := range question.items {
		text := label
		if !m.styles().attributesVisible() && index == m.modal.cursor {
			text = theme.SelectionMark + " " + text
		}
		rows = append(
			rows,
			styles.popupItem(index == m.modal.cursor, m.tokens()).
				Render(popupLine(text)),
		)
	}

	return m.popupRows(rows)
}

// popupRows sets the popup off the surface behind it and gives every row the
// width of the widest one, so that the raised surface is a block and not a
// staircase of lines of different lengths.
//
// The padding goes after the text and never before it: a row padded on the
// left is right-aligned, and a menu whose items step to the right as they
// get shorter is a menu nobody can scan.
func (m Model) popupRows(rows []string) []string {
	styles := m.styles()
	shadow := styles.popupShadow(m.tokens(), popupShadowColumns)
	width := 0

	for _, row := range rows {
		width = maxInt(width, m.widths.StringWidth(row))
	}

	bar := styles.popupFocusBar(m.tokens())

	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(
			out,
			spaces(popupInsetColumns)+bar+row+
				spaces(maxInt(width-m.widths.StringWidth(row), 0))+shadow,
		)
	}

	return out
}

// overlayPopup draws a popup over the rendered screen, from the column the
// conversation starts in.
//
// The rows replace the rows of the conversation under them, which is what a
// modal is: the rest of the screen is still there and is not drawn over,
// because a user who cannot see the message the question is about cannot
// answer the question. The popup is not drawn over the chat list either: it
// is about one message, and a menu over the list would cover the chats the
// user might want next.
func (m Model) overlayPopup(
	screen string,
	rows []string,
	firstRow int,
	firstColumn int,
) string {
	if len(rows) == 0 {
		return screen
	}

	lines := strings.Split(screen, "\n")
	for index, row := range rows {
		line := firstRow + index
		if line < 0 || line >= len(lines) {
			continue
		}
		lines[line] = m.overlayRow(lines[line], row, firstColumn)
	}

	return strings.Join(lines, "\n")
}

// overlayRow draws one popup row over one screen row, starting at a column.
//
// The row behind the popup keeps its head before the column the popup starts
// at, and its tail after the popup ends, so the parts of the conversation
// that are not covered stay readable beside the menu.
//
// The cut is by column and not by byte. A row behind the popup is a styled
// row, and a byte cut through it would print half an escape sequence as
// text: the user would see `;24;36m` in the middle of a conversation.
//
// It is a method of the model for the same reason the rest of the drawing
// is: the column the popup starts at and the width of the row behind it
// are columns of one terminal, and a menu placed with the measurements of
// another one covers the wrong words.
func (m Model) overlayRow(behind, popup string, firstColumn int) string {
	behindWidth := m.widths.StringWidth(behind)
	head := m.widths.Truncate(behind, maxInt(firstColumn, 0), "")
	head += spaces(maxInt(firstColumn-behindWidth, 0))

	// The rows of the popup say where they start, at the column of the
	// popup: the row behind them ends wherever it ends, and the row above
	// it ends wherever that one did (columns.go).
	popup = cursorColumn(maxInt(firstColumn, 0)+1) + popup

	popupEnd := maxInt(firstColumn, 0) + m.widths.StringWidth(popup)
	if behindWidth <= popupEnd {
		return head + popup + spaces(behindWidth-popupEnd)
	}

	// The tail is what is left of the row behind the popup, and it is
	// behindWidth - popupEnd columns wide. Truncating by that many columns
	// would leave a tail exactly popupEnd wide and a row popupEnd columns
	// over the screen, which the terminal then wraps: the menu pushes the
	// conversation off the right edge and every row under it moves.
	return head + popup + m.widths.TruncateLeft(behind, popupEnd, "")
}

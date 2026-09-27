package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"

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

	return m.indentPopupRows(rows)
}

// confirmModalRows returns the rows of the question of §12.3.
func (m Model) confirmModalRows() []string {
	if !m.modal.open {
		return nil
	}

	styles := m.styles()
	rows := []string{
		styles.popupTitle(m.tokens()).Render(popupLine(modalTitle)),
		"",
	}
	for _, line := range strings.Split(modalExplanation, "\n") {
		rows = append(
			rows,
			styles.popupBody(m.tokens()).Render(popupLine(line)),
		)
	}
	rows = append(rows, "")

	for index, label := range modalItems {
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

	return m.indentPopupRows(rows)
}

// indentPopupRows sets the popup off the surface behind it and gives it a
// shadow, so that it reads as above the conversation in every profile.
func (m Model) indentPopupRows(rows []string) []string {
	styles := m.styles()
	indent := spaces(popupInsetColumns)
	shadow := styles.popupShadow(m.tokens(), popupShadowColumns)
	width := 0

	for _, row := range rows {
		width = maxInt(width, cellWidth(row))
	}

	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, indent+spaces(maxInt(width-cellWidth(row), 0))+row+shadow)
	}

	return out
}

// overlayPopup draws a popup over the rendered screen.
//
// The rows replace the rows of the conversation under them, which is what a
// modal is: the rest of the screen is still there and is not drawn over,
// because a user who cannot see the message the question is about cannot
// answer the question.
func (m Model) overlayPopup(
	screen string,
	rows []string,
	firstRow int,
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
		lines[line] = m.overlayRow(lines[line], row)
	}

	return strings.Join(lines, "\n")
}

// overlayRow draws one popup row over one screen row.
//
// The popup is written over the row from its first column to its own width,
// and what is behind it is cut: a row that is longer than the popup keeps
// its tail, so the right-hand side of the conversation is still readable
// next to the menu.
//
// The cut is by column and not by byte. A row behind the popup is a styled
// row, and a byte cut through it would print half an escape sequence as
// text: the user would see `;24;36m` in the middle of a conversation.
func (m Model) overlayRow(behind, popup string) string {
	gap := cellWidth(behind) - cellWidth(popup)
	if gap <= 0 {
		return popup + spaces(-gap)
	}

	return popup + spaces(gap) + ansi.TruncateLeft(behind, gap, "")
}

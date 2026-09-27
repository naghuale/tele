package tui

// The composer is the one region the user writes in, and its rules are
// §4.5, §7 and §8.4.
//
// It grows with the draft, up to four rows and never past a third of the
// conversation, and it scrolls inside those rows with the row of the cursor
// always on screen. A draft that does not fit is a normal thing to have
// typed, not an edge case, and the composer is the last thing that may go:
// §3.4 requires it to be visible whenever a conversation is open.
//
// The rows are laid out by the composer layout and the cursor is drawn on
// the row it is at. Nothing is written back into the draft for the screen:
// a wrap is a fact about the terminal, and the text that goes to the queue
// is the text that was typed.

// composerPlaceholder is what an empty composer shows.
const composerPlaceholder = "Write a message…"

// composerPlaceholderShort is the placeholder on a screen with no room for
// the hint bar.
//
// §3.4 hides the hints when the screen is short, and a composer that can
// only be understood by keys that are not on the screen is a composer
// nobody can use. The sentence says what Enter does, which is the key
// everybody tries first.
const composerPlaceholderShort = "Write a message… (Enter to send)"

// composerMaxRows is how many rows the draft may take before the field
// stops growing (§4.5).
const composerMaxRows = 4

// composerMaxShare is the largest part of the screen the composer may take
// (§4.5: "максимум — до 30% высоты conversation view").
const composerMaxShare = 0.3

// composerLines returns the rows of the composer region: the draft, and
// then whatever the last send has to say about itself.
func (m Model) composerLines(layout Layout, width int) []string {
	rows := m.composerRowCount(layout, width)
	lines := m.composerTextLines(layout, width, rows)

	// A composer-only screen has no room for a second line, and the reason
	// a send failed is worth less than the composer a user is typing in.
	if layout.ComposerOnly() {
		return lines
	}

	return append(lines, m.sendStateLines(width)...)
}

// composerRowCount returns how many rows the draft takes on the screen.
//
// One row for a draft that fits, more as the draft grows, never more than
// four and never more than a third of the conversation. A screen too short
// for the rule — the composer-only screen of §3.4 — gives the draft
// everything there is, because the composer is what that screen is.
func (m Model) composerRowCount(layout Layout, width int) int {
	if layout.ComposerOnly() {
		return maxInt(layout.Height, 1)
	}

	limit := minInt(
		composerMaxRows,
		maxInt(int(float64(layout.Height)*composerMaxShare), 1),
	)

	layout_ := layoutComposer(m.composer, m.composerCursor, m.composerTextWidth(width))

	return minInt(maxInt(len(layout_.rows), 1), limit)
}

// composerTextWidth returns the width a row of the draft is laid out in.
//
// The inset between the focus bar and the text comes off the pane: the
// cursor has to be drawn in the columns the text is in, or it lands next to
// the text instead of on it.
func (m Model) composerTextWidth(width int) int {
	return maxInt(width-contentInsetWidth, 1)
}

// composerTextLines returns the rows the draft is drawn in, with the cursor
// on the row it is at.
func (m Model) composerTextLines(layout Layout, width, rows int) []string {
	styles := m.styles()
	textWidth := m.composerTextWidth(width)
	inset := spaces(contentInsetWidth)

	laid := layoutComposer(m.composer, m.composerCursor, textWidth)
	visible := laid.visibleRows(rows)

	if len(m.composer) == 0 {
		return m.composerPlaceholderLines(styles, layout, width, rows)
	}

	lines := make([]string, 0, len(visible))
	for _, row := range visible {
		number := laid.rowNumber(row)
		offset, onCursorRow := laid.cursorIn(row, number)

		lines = append(lines, m.composerRowLine(
			styles,
			row,
			onCursorRow,
			offset,
			textWidth,
			m.focus == FocusComposer,
			inset,
		))
	}

	return lines
}

// composerRowLine draws one row of the draft, with the cursor on it when
// the cursor is there.
//
// The cursor cell is a whole grapheme cluster and not a rune. Editing
// moved over clusters, and a cursor style in the middle of a ZWJ emoji
// does not mark it: the terminal draws the pieces one after another and
// the emoji comes apart on the screen.
//
// Where the profile prints attributes, the cursor is reverse video on the
// cluster it is at, and a bar when it is past the last one. Where it
// prints none — the Ascii profile of NO_COLOR, --no-color, TERM=dumb and
// `color = "never"` — the bar comes first and the row gives up the column
// it takes, because a cursor nobody can see is not a cursor. A row that
// was exactly full loses its last cell for it, which is the price of a
// visible cursor in a terminal that has no way to print one.
func (m Model) composerRowLine(
	styles viewStyles,
	row composerRow,
	onCursorRow bool,
	offset int,
	textWidth int,
	focused bool,
	inset string,
) string {
	if !onCursorRow {
		return styles.text(m.tokens().PrimaryText).
			Render(inset + fitCells(row.text, textWidth))
	}

	before, under, after := splitAtCluster([]rune(row.text), offset)
	cursorStyle := styles.cursor(focused)
	bar := cursorStyle.Render(cursorBar)

	if under == "" {
		// The cursor is past the last cell of the row, so the bar is the
		// last thing on it and nothing has to move.
		return inset + styles.text(m.tokens().PrimaryText).
			Render(before) + bar
	}

	attributed := styles.attributesVisible()
	budget := textWidth

	var line string
	if attributed {
		// One style run around the whole cluster, so the terminal has
		// nothing to break it apart with.
		line = styles.text(m.tokens().PrimaryText).Render(before) +
			cursorStyle.Render(under) +
			styles.text(m.tokens().PrimaryText).Render(after)
	} else {
		budget--
		line = styles.text(m.tokens().PrimaryText).Render(before) +
			bar +
			styles.text(m.tokens().PrimaryText).Render(under+after)
	}

	return inset + fitCells(line, budget)
}

// splitAtCluster cuts the text of a row at a rune offset that is on a
// cluster boundary, into the part before the cursor, the cluster under it
// and the part after it.
//
// An offset in the middle of a cluster — which a rune index can be, if the
// text was set from outside rather than typed — belongs to the cluster
// before it, the same way Backspace does. A cursor drawn inside a cluster
// is a cursor on half an emoji.
func splitAtCluster(text []rune, offset int) (before, under, after string) {
	offset = clampIndex(offset, len(text))

	used := 0

	for _, cluster := range graphemes(text) {
		// The first cluster the offset reaches or falls into is the one the
		// cursor is on. An offset that lands exactly on its first rune is
		// the cursor before it, which is where a left arrow leaves it.
		if used+len(cluster) > offset {
			return before, string(cluster), string(text[used+len(cluster):])
		}

		before += string(cluster)
		used += len(cluster)
	}

	// The offset is past the last rune, so the cursor is at the end of the
	// row and there is nothing under it.
	return before, "", ""
}

// cursorBar is the cursor drawn where there is no character under it.
const cursorBar = "▏"

// composerPlaceholderLines returns the rows of an empty composer, with the
// cursor at the start of the first one.
func (m Model) composerPlaceholderLines(
	styles viewStyles,
	layout Layout,
	width int,
	rows int,
) []string {
	text := composerPlaceholder
	style := styles.dimmed(m.tokens().MutedText)

	// §7.3: a blank draft that was sent lights the placeholder for a
	// moment. It is one style and one message, and it is the only thing on
	// the screen that changes when nothing was typed.
	if m.composerPlaceholderLit {
		text = composerPlaceholder
		style = styles.litPlaceholder()
	}

	if layout.HideHints() {
		text = composerPlaceholderShort
	}

	lines := []string{
		style.Render(spaces(contentInsetWidth) + cursorBar + text),
	}

	for len(lines) < rows {
		lines = append(lines, spaces(contentInsetWidth))
	}

	return lines[:maxInt(rows, 1)]
}

// sendStateLines returns the lines that say what happened to the last
// send, if anything did.
//
// The paused notice is prose rather than a one-line status: it says what
// stopped, what it means for the draft, and what to do about it, so it is
// wrapped to the width instead of being cut. Cutting it would leave a user
// with "Sending paused" and nothing they can act on.
func (m Model) sendStateLines(width int) []string {
	styles := m.styles()

	switch m.sendState {
	case sendStateSending:
		return []string{styles.dimmed(m.tokens().StatusActive).
			Render(fitCells("Sending...", width))}

	case sendStateError:
		// A paused composer is not a failed send: nothing was ever
		// attempted, so the wording of a failure would be a lie the user
		// has to interpret.
		if m.pausedErr != nil {
			notice := wrapCells(m.pausedErr.Error(), width)
			rendered := make([]string, 0, len(notice))
			for _, line := range notice {
				rendered = append(
					rendered,
					styles.text(m.tokens().StatusError).Render(line),
				)
			}

			return rendered
		}

		text := "Failed to send: " + sendErrorText(m.sendErr)
		return []string{styles.text(m.tokens().StatusError).
			Render(fitCells(text, width))}

	default:
		return nil
	}
}

// sendErrorText returns the user-facing text of a send failure.
//
// The error is already a safe string: the composition root resolves the
// reason before it reaches the TUI, so nothing here has to decide what may
// be shown.
func sendErrorText(err error) string {
	if err == nil {
		return ""
	}

	return err.Error()
}

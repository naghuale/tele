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

// composerLines returns the rows of the band of the composer: the draft,
// whatever the last send has to say about itself, and the keys that work in
// it.
//
// The row of space above the field is not one of them. It is the boundary
// between the feed and the band, and it is drawn on the background of the
// feed (view_boundary.go): a row of the band's own surface is the first row
// of the band rather than air, and a message the window cut at the bottom
// runs into it in the very colour it ends in.
func (m Model) composerLines(layout Layout, width int) []string {
	// A chat Telegram refuses is drawn as one line where the field would be,
	// and there is nothing else in the region: no draft to lay out, no
	// cursor to place, and no send whose failure has to be explained.
	if !m.canWrite() {
		return m.blockedComposerLines(layout, width)
	}

	rows := m.composerRowCount(layout, width)
	lines := m.composerTextLines(layout, width, rows)

	// A composer-only screen has no room for a second line, and the reason
	// a send failed is worth less than the composer a user is typing in.
	if layout.ComposerOnly() {
		return lines
	}

	lines = append(lines, m.sendStateLines(width)...)
	lines = append(lines, m.bandHintLines(layout, width)...)

	return lines
}

// blockedComposerLines returns the rows of a chat this account cannot write
// in: one line that says why.
//
// The line is drawn in the muted step and has no prompt marker and no
// cursor, because there is nothing to type into and a blinking cursor in a
// place that ignores every key is a thing on the screen that says the user
// is somewhere else. A draft typed while the chat could be written in is
// kept and comes back with the field; what is not drawn is the draft, not
// the draft's home.
func (m Model) blockedComposerLines(
	layout Layout,
	width int,
) []string {
	styles := m.styles()
	line := styles.dimmed(m.tokens().MutedText).
		Render(m.widths.Fit(m.chatAccess.Blocked.line(), width, ellipsis))

	if layout.ComposerOnly() {
		return []string{line}
	}

	return append([]string{line}, m.bandHintLines(layout, width)...)
}

// bandHintLines returns the hints as they are drawn inside the composer's
// band.
//
// They are the last row of the band and not a separate footer: the field
// and the keys that work in it are one thing, and §4.6 asks for the keys
// of the focus the user is in. A key line in a different surface under a
// field is a second thing to look at, and a user who is about to type
// looks at the field.
func (m Model) bandHintLines(layout Layout, width int) []string {
	if m.screen != ScreenConversation {
		return nil
	}

	return m.hintLines(layout, width)
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

	layout_ := m.layoutComposer(m.composer, m.composerCursor, m.composerTextWidth(width))

	return minInt(maxInt(len(layout_.rows), 1), limit)
}

// composerTextWidth returns the width a row of the draft is laid out in.
//
// The marker and its space come off the pane: the cursor has to be drawn
// in the columns the text is in, or it lands next to the text instead of
// on it.
func (m Model) composerTextWidth(width int) int {
	return maxInt(width-composerPromptWidth(), 1)
}

// composerTextLines returns the rows the draft is drawn in, with the cursor
// on the row it is at.
func (m Model) composerTextLines(layout Layout, width, rows int) []string {
	styles := m.styles()
	textWidth := m.composerTextWidth(width)
	focused := m.focus == FocusComposer

	laid := m.layoutComposer(m.composer, m.composerCursor, textWidth)
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
			onCursorRow && focused,
			offset,
			textWidth,
			focused,
		))
	}

	return lines
}

// composerPrompt is the marker in front of the draft, and the space after
// it.
//
// The space is part of the marker and not part of the field: `›Write` is
// one shape, and a user typing into the field reads the gap between the
// marker and the first letter as the edge of the field. The two columns
// are reserved whatever the profile is, so the draft starts in the same
// column in colour and without colour.
const composerPrompt = "› "

// composerPromptWidth returns the columns the marker and its space take in
// front of the draft.
//
// Every width the draft is laid out in and every width the field is
// fitted to spends it, so the cursor is drawn in the columns the text is
// in and not beside it. It is measured rather than written down, because
// the marker is two characters of text and a number that could disagree
// with them is a number that would take a column away from the draft
// without anybody noticing.
func composerPromptWidth() int {
	return len([]rune(composerPrompt))
}

// composerPromptStyle is the style of the marker in front of the draft.
func (m Model) composerPromptStyle(focused bool) string {
	if focused {
		return m.styles().text(m.tokens().Focus).Render(composerPrompt)
	}

	return m.styles().dimmed(m.tokens().MutedText).Render(composerPrompt)
}

// composerRowLine draws one row of the draft, with the cursor on it when
// the cursor is there.
//
// The cursor cell is a whole grapheme cluster and not a rune. Editing
// moved over clusters, and a cursor style in the middle of a ZWJ emoji
// does not mark it: the terminal draws the pieces one after another and
// the emoji comes apart on the screen.
//
// A row the composer does not have the keys for carries no cursor at all.
// A field the user is not typing into is not blinking at them, and a bar
// that is there in every focus is a bar that says nothing about where the
// keys are; the `›` in front of it and the colour of that are what say so.
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
) string {
	inset := m.composerPromptStyle(focused)

	if !onCursorRow {
		return inset + styles.text(m.tokens().PrimaryText).
			Render(m.widths.Fit(row.text, textWidth, ellipsis))
	}

	before, under, after := splitAtCluster([]rune(row.text), offset)
	cursorStyle := styles.cursor(true)
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

	return inset + m.widths.Fit(line, budget, ellipsis)
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
// cursor at the start of the first one while the composer has the keys.
//
// An empty composer the keys are not in carries no cursor: a bar blinking
// in a field nobody is typing into is the one thing on the screen that says
// the user is somewhere else.
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

	focused := m.focus == FocusComposer
	first := m.composerPromptStyle(focused) + style.Render(text)
	if focused {
		first += styles.cursor(true).Render(cursorBar)
	}

	lines := []string{first}
	for len(lines) < rows {
		lines = append(lines, "")
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
			Render(m.widths.Fit("Sending...", width, ellipsis))}

	case sendStateError:
		// A paused composer is not a failed send: nothing was ever
		// attempted, so the wording of a failure would be a lie the user
		// has to interpret.
		if m.pausedErr != nil {
			notice := m.widths.Wrap(m.pausedErr.Error(), width, ellipsis)
			rendered := make([]string, 0, len(notice))
			for _, line := range notice {
				rendered = append(
					rendered,
					styles.text(m.tokens().StatusError).Render(line),
				)
			}

			return rendered
		}

		// §12.1: two fixed sentences, and nothing else. The cause of a
		// failed queueing can name a keychain service, a path and a TDLib
		// error message, and this slot is a screen somebody is reading.
		//
		// The block is raised (§24): the background is behind the two
		// sentences and not across the pane, because a band the width of
		// the composer would say the whole composer had failed.
		raised := styles.raised(m.tokens().StatusError, m.tokens().PopupBackground)
		lines := make([]string, 0, 2)
		for _, text := range []string{notQueuedText, draftKeptText} {
			for _, line := range m.widths.Wrap(text, width, ellipsis) {
				lines = append(lines, raised.Render(line))
			}
		}

		return lines

	default:
		return nil
	}
}

// The two sentences of §12.1. They are fixed words and not a formatted
// error: what a user can do about a message that was not queued is nothing
// on this screen, and the one thing they must not do is lose the text.
const (
	notQueuedText = "Message was not queued"
	draftKeptText = "Your text is still in the composer"
)

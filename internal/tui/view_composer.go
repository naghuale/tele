package tui

// composerPlaceholder is what an empty composer shows.
//
// It is a sentence rather than a prompt character: the composer is a
// single line until PR-10A.3, and a lone cursor in an empty box does not
// say what the box is for.
const composerPlaceholder = "Write a message…"

// composerLines returns the lines of the composer region.
//
// The composer is always visible while a conversation is open, and it is
// one line of text: whatever else the screen gives it goes to a line of
// its own, so a send that is failing or paused is next to the draft it
// belongs to instead of somewhere else on the screen.
func (m Model) composerLines(layout Layout, width int) []string {
	lines := []string{m.composerInputLine(width)}

	// A composer-only screen has no room for a second line, and the reason
	// a send failed is worth less than the composer a user is typing in.
	if layout.ComposerOnly() {
		return lines
	}

	return append(lines, m.sendStateLines(width)...)
}

// composerInputLine returns the text of the composer, or the
// placeholder.
func (m Model) composerInputLine(width int) string {
	text := string(m.composer)
	if text == "" {
		return m.styles().
			dimmed(m.tokens().MutedText).
			Render(fitCells(composerPlaceholder, width))
	}

	// A draft longer than the composer shows its tail, because the end of
	// a sentence is what the user is still writing.
	if cellWidth(text) > width {
		return m.styles().
			text(m.tokens().PrimaryText).
			Render(tailCells(text, width))
	}

	return m.styles().
		text(m.tokens().PrimaryText).
		Render(text)
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

// tailCells returns the last width terminal columns of value.
//
// Truncating the head instead would hide the beginning of a draft, and the
// beginning is usually the part that was already sent to somebody else and
// reviewed.
func tailCells(value string, width int) string {
	if width <= 0 {
		return ""
	}

	if cellWidth(value) <= width {
		return value
	}

	runes := []rune(value)
	for len(runes) > 0 && cellWidth(string(runes[1:])) > width {
		runes = runes[1:]
	}

	return "…" + string(runes)
}

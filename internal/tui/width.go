package tui

import (
	"telecli/internal/tui/termwidth"
)

// This file is the one place the interface counts columns.
//
// Every width the views draw with is read from the model's width model, and
// nothing in this package measures text any other way. That is not tidiness:
// a layout counted in one rule and padded in another is a layout that is a
// column out of place, and the terminal wraps a line that is one column too
// wide and shifts everything under it. The focus bars step sideways, rows
// of the list repeat or disappear, the fragments of the previous frame stay
// on the screen, and the time of a message is cut off — every one of those
// is the same mistake made visible.
//
// Which rule is the right one is not a question this package answers: it is
// a question about the terminal, and internal/tui/termwidth measures the
// terminal before the first frame and says. What is left here is the words
// the interface uses for it.

// ellipsis marks a cut in the interface.
const ellipsis = "…"

// WidthMode returns how the model counts the width of text.
//
// It is the rule a screen was drawn in, which is what makes a screen drawn
// in two rules two screens rather than one screen with a difference in it.
func (m Model) WidthMode() termwidth.Mode { return m.widths.Mode() }

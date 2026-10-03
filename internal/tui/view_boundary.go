package tui

import "telecli/internal/tui/theme"

// This file is the boundary between the feed of a conversation and the panel
// under it: the band of the composer with the field and the keys, and the
// foot of a preview.
//
// The owner found the absence of it on 03.10, on a real account and in a
// channel he reads: a message the window had cut at the bottom stood on the
// panel with nothing between them, the block of the message and the band of
// the panel were one colour, and nothing on the screen said where the message
// ended and the panel began. A block cut at the bottom is the ordinary case
// rather than a rare one — it is what a message longer than the window looks
// like wherever the reader has scrolled to — and a message that reads as a
// part of the panel is a message nobody can tell from a row of hints.
//
// The boundary is one row of the background of the feed, drawn between the
// last row of the feed and the first row of the panel, and it is a row of the
// feed rather than a row of air on the panel's own surface. That last part is
// the whole of the fix, and it is a small one: the band has always had a row
// of space above the field and has always drawn it in its own surface, which
// is not air at all — it is the first row of the band, and a block the window
// cut off runs into it in the very colour that block ends in. Drawn on the
// background of the feed it is what §4.5 has always shown: the composer's
// surface starts one row below the messages.
//
// It is a surface and not a line, and both of the alternatives were turned
// down on purpose:
//
//   - a rule of its own would be a frame. §1 forbids frames and §21 names
//     three tests that hold the screen to it, and the one rule the screen
//     does draw — the heavy one under a header — is the mark of the focus
//     and not a border. A second line across the screen would be a second
//     claim of that kind for something that is not a focus.
//
//   - a tone of its own for the panel is not available. The ramp of the
//     palette is pinned by §2.2 and every step of it already means
//     something: the band is one above the feed, the blocks are on the
//     band's own step, and the step above that is the selection and the
//     pill of a day. What is below the feed is 1.05:1 to 1.25:1 away from it
//     (theme/palette.go), which is a shade and not a boundary.
//
// What is left is the gap, and it is the same difference a block of a message
// already makes against the feed — 1.34:1 to 1.49:1 in the three built-in
// themes, 1.52:1 in the light one — so it is as visible as the thing that
// makes a message a block in the first place.

// feedBoundaryRows returns how many rows separate the feed of a conversation
// from the panel below it.
//
// It is none on a composer-only screen (§3.4): there is no feed above the
// band there, and a boundary with nothing on one side of it is a dark row
// through the middle of a field.
func feedBoundaryRows(layout Layout) int {
	if layout.ComposerOnly() {
		return 0
	}

	return 1
}

// feedBoundaryRegion draws the rows that separate the feed of a conversation
// from the panel below it.
//
// They are drawn as a region of the conversation rather than as a row of the
// panel, because that is what they are: the background is the background of
// the feed, and the surface of the panel begins under them.
//
// Every cell of the row is written with a background. A run ends with a
// reset, and a reset takes the surface of the row with it, so a row that left
// its columns to the terminal would put a stripe of whatever the terminal's
// own background is along the bottom of the feed — on the row that is there
// to say where the feed ends (styles.go, on).
func (m Model) feedBoundaryRegion(layout Layout, width int) string {
	rows := feedBoundaryRows(layout)
	if rows < 1 {
		return ""
	}

	styles := m.styles()
	feed := styles.on(m.tokens().ChatBackground, styles.unstyled())

	lines := make([]string, 0, rows)
	for range rows {
		lines = append(
			lines,
			m.painterIn(conversationOrigin(layout), theme.Color{}).
				own(feed, spaces(width)).
				String(),
		)
	}

	return m.renderRegion(styles.conversation, width, lines, rows)
}

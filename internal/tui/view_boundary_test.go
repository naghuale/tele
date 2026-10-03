package tui

import (
	"strings"
	"testing"

	"telecli/internal/tui/theme"
)

// The tests in this file are about the boundary between the feed of a
// conversation and the panel under it (view_boundary.go), which is the row
// the owner found missing on 03.10 on a real account: a message the window
// had cut at the bottom stood on the panel with nothing between them.
//
// They are about the cells and not about the words, because the whole of the
// defect is invisible in the text of a screen — the rows said exactly what
// they had always said, and the block of the message and the band of the
// panel were one colour underneath them.
//
// Every screen is read as the conversation pane on its own rather than as the
// joined screen: a row of a two-pane screen is a row of the list and a row
// of the conversation at once, and a background asked of such a row is the
// background of whichever pane answered first.

// boundaryScreen is one screen with the boundary to be found on it.
type boundaryScreen struct {
	name  string
	model Model

	// pane is the conversation pane on its own, as the screen draws it.
	pane string

	// panel is how many rows the panel under the feed takes, boundary row
	// included, which is what says where the panel starts.
	panel int
}

// boundaryScreens returns the screens the boundary has to be on: the two
// bands a conversation can end in and the foot of a preview, in both modes
// and on both profiles that print a background a different way.
//
// The channel the account cannot write in is the screen of the report. The
// indexed profile is here because the two print a background differently and
// only one of them has been right before.
func boundaryScreens(t *testing.T) []boundaryScreen {
	t.Helper()

	field := focusedOn(
		openedProgramModel(t, theme.ProfileTrueColor, 120, 30), FocusHistory,
	)
	readOnly := snapshotReadOnlyChannel(t, wide(theme.ProfileTrueColor))
	preview := snapshotChatPreview(t, wide(theme.ProfileTrueColor))
	indexed := focusedOn(
		openedProgramModel(t, theme.ProfileANSI256, 120, 30), FocusHistory,
	)
	light := snapshotChannelFeedAt(
		t, lightChannelFeed(theme.ProfileTrueColor), 18,
	)

	return []boundaryScreen{
		conversationBoundary("a conversation with a field", field),
		conversationBoundary("a channel this account cannot write in", readOnly),
		previewBoundary("a preview of the chat under the cursor", preview),
		conversationBoundary("an indexed terminal", indexed),
		conversationBoundary("the light theme", light),
	}
}

// conversationBoundary returns the conversation pane of a screen and the
// rows of the band of its composer.
func conversationBoundary(name string, m Model) boundaryScreen {
	layout := LayoutFor(m.width, m.height)

	return boundaryScreen{
		name:  name,
		model: m,
		pane:  m.conversationPaneRegion(layout),
		panel: m.composerHeight(layout, layout.ChatContentWidth()),
	}
}

// previewBoundary returns the preview pane of a screen and the rows of the
// foot under it.
func previewBoundary(name string, m Model) boundaryScreen {
	layout := LayoutFor(m.width, m.height)

	return boundaryScreen{
		name:  name,
		model: m,
		pane:  m.previewPaneRegion(layout),
		panel: m.previewFooterRows(layout, layout.ChatContentWidth()),
	}
}

// The last row of the feed and the first row of the panel are drawn on
// different surfaces, and the row between them is the feed's own.
//
// That row between them is the whole of it. A row of the panel's own surface
// where the air should be is not air at all: it is the first row of the
// panel, and a block the window cut off runs into it in the very colour it
// ends in — which is what the owner saw in a channel he reads, where the
// block of a message cut at the bottom and the line of a read-only channel
// were one field of grey with no edge between them.
func TestThePanelUnderTheFeedIsSeparatedFromIt(t *testing.T) {
	for _, screen := range boundaryScreens(t) {
		t.Run(screen.name, func(t *testing.T) {
			m := screen.model
			rows := renderedRows(t, m, screen.pane)

			feed := backgroundOf(m, m.tokens().ChatBackground)
			panel := backgroundOf(m, panelBackground(m))
			if feed == "" || panel == "" {
				t.Fatalf("the theme has no surfaces to tell apart")
			}

			first, boundary := boundaryRows(t, screen.name, rows, screen.panel)

			if !everyPaintedCellOn(rows[boundary], feed) {
				t.Errorf(
					"row %d of %q is not a row of the feed: it carries %q\n%s",
					boundary+1, screen.name, rowSurface(rows[boundary]), plain(screen.pane),
				)
			}

			if anyCellOn(rows[boundary], panel) {
				t.Errorf(
					"the boundary of %q has columns of the panel on it\n%s",
					screen.name, plain(screen.pane),
				)
			}

			if got := rowSurface(rows[first]); got != panel {
				t.Errorf(
					"the first row of the panel of %q is on %q, want the surface of "+
						"the panel:\n%s",
					screen.name, got, plain(screen.pane),
				)
			}
		})
	}
}

// A message the window cut at the bottom does not stand on the panel.
//
// The screen is the one the owner found: a channel with a message taller than
// the whole window, walked one message up, so the block is cut at the bottom
// of the feed. The row above the boundary is the block's own row — the block
// on its own columns and the feed around it — and what is under it is the
// background of the feed rather than the surface of the panel.
//
// A row above the boundary with the feed's background all along it would mean
// the window ended on a gap between messages, which is not the case this is
// about, and the test would then be failing for the right reason.
func TestABlockTheWindowCutDoesNotStandOnThePanel(t *testing.T) {
	m := snapshotTallChannelFeed(t, 7, 45)
	screen := conversationBoundary("a block cut at the bottom", m)
	rows := renderedRows(t, m, screen.pane)

	feed := backgroundOf(m, m.tokens().ChatBackground)
	block := backgroundOf(m, m.blockSurface(false))

	_, boundary := boundaryRows(t, screen.name, rows, screen.panel)

	if !everyPaintedCellOn(rows[boundary], feed) {
		t.Fatalf(
			"the row above the panel is not a row of the feed: it carries %q\n%s",
			rowSurface(rows[boundary]), plain(screen.pane),
		)
	}

	cut := rows[boundary-1]
	if !anyCellOn(cut, block) {
		t.Fatalf(
			"the row above the boundary carries no block, so no message was cut "+
				"by the window:\n%s",
			plain(screen.pane),
		)
	}

	if !anyCellOn(cut, feed) {
		t.Errorf("the cut block has no columns of the feed around it:\n%s", plain(screen.pane))
	}

	if strings.TrimSpace(cellsText(cut)) == "" {
		t.Errorf("the cut row of the block has no words in it:\n%s", plain(screen.pane))
	}
}

// boundaryRows returns the row the panel under the feed starts on and the
// row of the boundary above it.
//
// They are counted from the bottom of the pane with the number of rows the
// panel says it takes, because the surface of a row of the feed can be the
// surface of the panel — that is what a block of a message is — and a row
// found by looking for that surface would be found on a block.
func boundaryRows(
	t *testing.T,
	name string,
	rows [][]renderedCell,
	panel int,
) (first, boundary int) {
	t.Helper()

	if panel < 2 {
		t.Fatalf("the panel of %q has no room for a boundary", name)
	}
	if len(rows) < panel+1 {
		t.Fatalf("the pane of %q is %d rows, want more than the %d of the panel",
			name, len(rows), panel)
	}

	boundary = len(rows) - panel

	return boundary + 1, boundary
}

// panelBackground returns the surface the panel under the feed is drawn on.
func panelBackground(m Model) theme.Color {
	if m.chatPreviewShown() {
		return m.tokens().FooterBackground
	}

	return m.tokens().ComposerBackground
}

// rowSurface returns the surface a row of a region is drawn on, read off the
// first cell that has one.
//
// The first cell with a background is the margin every region reserves at its
// left edge, and it is asked about rather than a run of words because a run
// ends with a reset: the columns a terminal paints after the last word of a
// row are whatever the terminal's own background is (styles.go, on), and a
// row whose trailing columns have no surface is not a row on the surface the
// words of it are drawn on.
func rowSurface(row []renderedCell) string {
	for _, cell := range row {
		if cell.background != "" {
			return cell.background
		}
	}

	return ""
}

// everyPaintedCellOn reports whether every cell of a row that carries a
// background at all carries that one.
//
// The column a region reserves at its left edge is printed before the
// surface of the region is in force, so it is a cell with no background of
// its own; in a joined screen it is the column of the gap between the panes,
// which the other pane has painted. It says nothing about the row, and it is
// not asked about here.
//
// A row with no painted cell at all is not a row on the background asked
// about: §2.7 clears every surface on the profiles that print none, and a
// boundary made of a colour the terminal is never told about is not a
// boundary.
func everyPaintedCellOn(row []renderedCell, background string) bool {
	if background == "" {
		return false
	}

	painted := false
	for _, cell := range row {
		if cell.background == "" {
			continue
		}
		if cell.background != background {
			return false
		}

		painted = true
	}

	return painted
}

// anyCellOn reports whether some cell of a row is painted on the given
// background.
func anyCellOn(row []renderedCell, background string) bool {
	for _, cell := range row {
		if cell.background == background {
			return true
		}
	}

	return false
}

// cellsText returns what a row says, for a test that has to know whether the
// row carries anything at all.
func cellsText(row []renderedCell) string {
	var out strings.Builder

	for _, cell := range row {
		out.WriteString(cell.text)
	}

	return out.String()
}

// backgroundOf returns the background a terminal is given for a surface of
// the theme: the SGR parameters of the token, printed the way the renderer
// prints them.
//
// The parameters are taken from a style rather than written out, because the
// question is which token a row is drawn with and not how many decimal digits
// a colour has: a hand-written sequence would go stale the first time the
// colour library rounded a value.
func backgroundOf(m Model, color theme.Color) string {
	return backgroundParameters(
		m.styles().on(color, m.styles().unstyled()).Render("x"),
	)
}

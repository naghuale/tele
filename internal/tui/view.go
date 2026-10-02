package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"telecli/internal/tui/theme"
)

// This file assembles the screen out of regions.
//
// The shape follows §1 and §3: structure comes from surfaces, the focus
// from one accent column, and there are no frames and no full-height
// dividers. A region is a block of lines with one background and, when it
// is focused, the accent column on its left. Regions are stacked inside a
// pane and the two panes are put side by side with a single column of
// space between them.
//
// The three regions of the conversation pane are separate blocks rather
// than one block with a tint per line, because each has its own token:
// the header and the timeline sit on the chat background, the composer on
// its own, and the hint bar on the footer.

type pane uint8

const (
	// listPane is the chat list.
	listPane pane = iota

	// conversationPane is the open conversation.
	conversationPane
)

// panelFocused reports whether a pane is the one the keys are in.
//
// A pane is the unit, not a region inside it: the timeline and the
// composer are two regions of one conversation, and the rule under the
// header of that conversation says the same thing about both of them. The
// search line is the other way round — it is a region of the chat list, and
// the list's own heading still belongs to the pane the keys are in.
//
// While a popup is open the popup is the focus, §5.2 in one sentence: two
// panels marked at once is a screen where a user cannot tell which one has
// the keys.
func (m Model) panelFocused(which pane) bool {
	if m.popupOpen() {
		return false
	}

	switch which {
	case listPane:
		return m.focus == FocusChatList || m.focus == FocusSearch
	default:
		return m.focus == FocusHistory || m.focus == FocusComposer
	}
}

// conversationOrigin returns the column the first cell of the conversation
// pane is in, counting from one.
//
// It is the column of the chat list on its right, the gap between the panes
// and one: on a screen with one pane the conversation is the screen, and
// there is nothing to the left of it.
func conversationOrigin(layout Layout) int {
	if !layout.TwoPane() {
		return 1
	}

	return layout.SidebarWidth() + paneGapWidth + 1
}

// View implements tea.Model.
func (m Model) View() string {
	if m.quitting {
		return ""
	}

	if m.width < minWidth || m.height < minHeight {
		return m.viewTooSmall()
	}

	layout := LayoutFor(m.width, m.height)
	view := m.viewWithoutPopup(layout)

	// Every row of the screen says where it starts, once, here at the top:
	// the renderer paints the rows that changed and leaves the cursor
	// wherever the last of those ended, so a row that does not name its
	// first column starts wherever the row above it happened to finish
	// (columns.go). It is here and not in the regions because a region is
	// a piece of a row and not always a whole one: the row of a two-pane
	// screen is the row of the list and the row of the conversation, and
	// the position of the second of those is the joiner’s to write.
	view = positionRows(view)

	// A popup is drawn over the screen and not instead of it: a menu
	// without the message it acts on cannot be read, and a question
	// without the message it is about cannot be answered.
	return m.withPopup(view, layout)
}

// viewWithoutPopup draws the screen of §1: the panes, or the one the
// layout has room for.
func (m Model) viewWithoutPopup(layout Layout) string {
	// §10.2 and §10.3: a wide and a medium screen have two panes, and the
	// right one is there before a chat is chosen. It says what to choose,
	// which is more use than a quarter of the terminal left empty, and the
	// list keeps the width §3.3 gives it either way.
	if m.screen != ScreenAuth && layout.TwoPane() {
		return m.fitHeight(m.viewTwoPanes(layout), layout)
	}

	switch m.screen {
	case ScreenConversation:
		return m.fitHeight(m.viewSinglePane(layout, conversationPane), layout)

	case ScreenAuth:
		return m.viewAuth()

	default:
		return m.fitHeight(m.viewSinglePane(layout, listPane), layout)
	}
}

// withPopup draws the sheet or the question over the screen.
//
// The question of §12.3 is centred and the sheet sits above the composer,
// because the composer is the thing a copy ends up in and a user who is
// about to write is looking at it.
func (m Model) withPopup(view string, layout Layout) string {
	rows, firstRow := m.popupRowsAndRow(layout)
	if len(rows) == 0 {
		return view
	}

	return m.fitHeight(
		m.overlayPopup(view, rows, firstRow, m.popupFirstColumn(layout)),
		layout,
	)
}

// popupFirstColumn returns the column the popup starts in.
//
// It is the conversation's first column and not the screen's: a menu drawn
// over the chat list would cover the chats a user reaches for next, and a
// menu that is not next to the message it acts on is a menu about nothing.
// A single-pane screen has one column and this is it.
func (m Model) popupFirstColumn(layout Layout) int {
	if !layout.TwoPane() {
		return 0
	}

	return layout.SidebarWidth() + paneGapWidth
}

// popupRowsAndRow returns the rows of the open popup and the screen row it
// starts on.
//
// A modal is centred, because it interrupts everything, and a sheet is
// placed above the composer, because its items are about a message and its
// results are drafts.
func (m Model) popupRowsAndRow(layout Layout) ([]string, int) {
	switch {
	case m.modal.open:
		rows := m.confirmModalRows()
		first := maxInt((layout.Height-len(rows))/2, 0)

		return rows, first

	case m.actionSheet.open:
		rows := m.actionSheetRows()
		first := maxInt(
			layout.Height-m.composerHeight(layout, layout.ChatContentWidth())-
				layout.hintLines()-len(rows)-1,
			0,
		)

		return rows, first

	default:
		return nil, 0
	}
}

// viewTwoPanes draws the chat list and the conversation side by side.
//
// The pane beside the list is the preview of the chat under the cursor, and
// the empty state of §17 while there is nothing to preview yet
// (chat_preview.go). Either way the keys are in the list: the pane is not
// focusable, and a second region that cannot be reached would be a second
// one that cannot be seen.
func (m Model) viewTwoPanes(layout Layout) string {
	right := m.conversationPaneRegion(layout)
	if m.screen != ScreenConversation {
		right = m.listSidePaneRegion(layout)
	}

	return m.joinSides(
		m.chatListRegion(layout, layout.SidebarContentWidth(), layout.Height),
		layout.SidebarWidth(),
		right,
		layout.ChatWidth(),
	)
}

// listSidePaneRegion draws the pane beside the chat list.
func (m Model) listSidePaneRegion(layout Layout) string {
	if m.chatPreviewShown() {
		return m.previewPaneRegion(layout)
	}

	return m.joinRegions(
		m.emptyConversationRegion(layout),
		m.footerRegion(layout, layout.ChatContentWidth()),
	)
}

// joinSides puts the two panes side by side with a column of space between
// them, and lines up their rows.
//
// It is not Lip Gloss's Join Horizontal, for the same reason the regions
// are not rendered as one block: Join Horizontal measures the width of a
// block with the grapheme rule, and a row of the chat list with a hand in
// it is a column wider that way than it is on a terminal that counts code
// points. The wider row makes the whole pane a column wider, the gap
// beside it a column narrower, and the conversation a column off — on
// every row, not only on the row with the hand in it.
//
// Each row of each block is padded to the width that block has, so the two
// panes are rectangles of the widths §10.1 gives them whatever is in them.
func (m Model) joinSides(left string, leftWidth int, right string, rightWidth int) string {
	rows := maxInt(lineCount(left), lineCount(right))

	side := make([]string, 0, rows)
	for index := range rows {
		// The right pane says where it starts rather than counting on
		// the left one to have ended where it was measured to: both panes
		// of a row begin by naming their first column, and the right one
		// names the column its own first cell is in.
		side = append(
			side,
			m.rowOf(left, index, leftWidth)+spaces(paneGapWidth)+
				cursorColumn(leftWidth+paneGapWidth+1)+
				m.rowOf(right, index, rightWidth),
		)
	}

	return strings.Join(side, "\n")
}

// rowOf returns one row of a rendered block, padded to the width the block
// has, and a row of spaces where the block has run out of rows.
//
// A block that is shorter than the one beside it is a pane with fewer rows
// in it, and the rows it does not have are the background of the other
// pane, not the background of the terminal.
func (m Model) rowOf(block string, index, width int) string {
	rows := strings.Split(block, "\n")
	if index >= len(rows) {
		return spaces(width)
	}

	// A row that is over the width of its block is cut rather than left to
	// wrap: the rows of a pane are fitted to its width before they get
	// here, so this is the last thing between a mistake in a view and a
	// screen the terminal has wrapped for us.
	return m.widths.Fit(rows[index], width, "")
}

// emptyConversationRegion draws the conversation pane before a chat is
// chosen.
//
// The words are §17's, and they depend on what the list is doing: a list
// that is loading or that failed says so here, in the width it has, rather
// than in the narrow column of the list where the sentence would be cut in
// half.
func (m Model) emptyConversationRegion(layout Layout) string {
	styles := m.styles()
	width := layout.ChatContentWidth()
	footer := m.footerRegion(layout, width)

	title, hint, colour := m.emptyConversationState()

	lines := []string{
		"",
		styles.text(colour).Render(m.widths.Fit(title, width, ellipsis)),
	}

	if hint != "" {
		lines = append(
			lines,
			"",
			styles.dimmed(m.tokens().SecondaryText).Render(m.widths.Fit(hint, width, ellipsis)),
		)
	}

	// The status belongs under the title of a conversation, and this pane
	// is the conversation of a program that has not been asked to open one
	// yet. It is also the only place it is drawn on a two-pane screen with
	// no chat open, and a user who cannot tell whether telecli is connected
	// is the user who is about to press Enter.
	lines = append(lines, m.statusBlock(layout, width, conversationOrigin(layout))...)

	return m.renderRegion(
		styles.conversation,
		width,
		lines,
		layout.Height-lineCount(footer),
	)
}

// emptyConversationState returns the title, the explanation and the colour
// of the empty conversation pane.
//
// The explanation is the second line of §17 and §18, and the cause of a
// failure is not one of them: it goes to the diagnostic stream, and this is
// a pane a user reads over their shoulder.
func (m Model) emptyConversationState() (string, string, theme.Color) {
	switch m.chatsState {
	case loadStateLoading:
		if m.chatsLoadSlow {
			return "Loading chats…", chatsLoadSlowText, m.tokens().StatusWarning
		}

		return "Loading chats…", "", m.tokens().SecondaryText

	case loadStateError:
		return chatLoadFailedText, chatsLoadFailedHint, m.tokens().StatusError

	case loadStateEmpty:
		// A load that succeeded with nothing in it is not a list waiting
		// to be chosen from: there is nothing to choose, and the pane says
		// that instead of asking for a chat.
		return noChatsText, noChatsHint, m.tokens().PrimaryText

	case loadStateLoaded:
		if len(m.chats) == 0 {
			return noChatsText, noChatsHint, m.tokens().PrimaryText
		}

		return emptyConversationTitle, emptyConversationHint, m.tokens().PrimaryText

	default:
		return emptyConversationTitle, emptyConversationHint, m.tokens().PrimaryText
	}
}

// The words of §17 for a conversation that has not been opened. They are
// the interface telling the user what to press, which is the only thing an
// empty pane has to say.
//
// Both keys that open the chat are named (the owner, 02.10): Tab moved
// nothing on this screen and only Enter worked, which is a dark pane with
// one key on it. The pane says what it is and how to fill it.
const (
	emptyConversationTitle = "Select a chat"
	emptyConversationHint  = "Enter or Tab to open."
)

// viewSinglePane draws one pane across the whole width.
func (m Model) viewSinglePane(layout Layout, which pane) string {
	if which == conversationPane {
		// §3.4: below composerOnlyHeight the conversation is the composer
		// and nothing else. The messages are still there when the screen
		// grows back.
		if layout.ComposerOnly() {
			return m.composerRegion(
				layout,
				layout.ChatContentWidth(),
				layout.Height,
			)
		}

		return m.conversationPaneRegion(layout)
	}

	// A list on its own takes the whole width. It would be the same list
	// of chats in a quarter of the screen, and §3.3 says the list is not
	// squeezed into a narrow screen.
	width := layout.FullContentWidth()
	footer := m.footerRegion(layout, width)

	return m.joinRegions(
		m.chatListRegion(layout, width, layout.Height-lineCount(footer)),
		footer,
	)
}

// conversationPaneRegion draws the conversation: the messages, the
// composer, and the hint bar.
//
// The composer is the last block of the pane and the messages take what is
// left, so a screen with three messages in it is three messages on the rows
// directly above the composer rather than three lines at the top. The hints
// are the last row of the composer's own band: the field and the keys that
// work in it are one thing, and a key line in a different surface under a
// field is a second thing to look at.
func (m Model) conversationPaneRegion(layout Layout) string {
	width := layout.ChatContentWidth()
	composer := m.composerRegion(layout, width, m.composerHeight(layout, width))

	history := m.conversationRegion(
		layout,
		width,
		layout.Height-lineCount(composer),
	)

	return m.joinRegions(history, composer)
}

// chatListRegion draws the chat list pane at the given content width and
// height.
//
// The search of §9 is not a region of its own: the row of the header that
// says the list can be searched becomes the field, so the pane is the same
// pane with and without a search, the same height, and nothing under the
// field moves while the query is typed. The keys being in that row is why
// the accent of the pane follows them (panelFocused).
func (m Model) chatListRegion(layout Layout, width, height int) string {
	return m.chatListBodyRegion(layout, width, height)
}

// chatListBodyRegion draws the list itself: the header, the rows, and the
// empty state of §17.
func (m Model) chatListBodyRegion(
	layout Layout,
	width int,
	height int,
) string {
	return m.renderRegion(
		m.styles().list,
		width,
		m.chatListLines(layout, width, height),
		height,
	)
}

// composerHeight returns how many rows the composer takes, which is its
// own line and, when a send says something, the lines of that.
func (m Model) composerHeight(layout Layout, width int) int {
	return len(m.composerLines(layout, width))
}

// composerRegion draws the composer.
//
// The composer is one line of text and takes the rest of its height when
// the screen is too short for anything else, which is §3.4: a screen too
// short for messages shows the composer, and it still fills the screen.
func (m Model) composerRegion(layout Layout, width, height int) string {
	return m.renderRegion(
		m.styles().composer,
		width,
		m.composerLines(layout, width),
		height,
	)
}

// footerRegion draws the hint bar, or nothing where there is no room for
// one.
func (m Model) footerRegion(layout Layout, width int) string {
	lines := m.hintLines(layout, width)
	if len(lines) == 0 {
		return ""
	}

	return m.renderRegion(
		m.styles().footer,
		width,
		lines,
		len(lines),
	)
}

// joinRegions stacks rendered regions into one pane.
func (m Model) joinRegions(blocks ...string) string {
	kept := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if block != "" {
			kept = append(kept, block)
		}
	}

	return strings.Join(kept, "\n")
}

// fitHeight cuts the screen to the terminal height.
//
// Every line is padded to the pane width, so a screen one line too tall
// is cut rather than scrolled: a layout that overflowed would push the
// composer below the fold, and §3.4 requires the composer to be visible
// whenever a conversation is open.
func (m Model) fitHeight(rendered string, layout Layout) string {
	lines := strings.Split(rendered, "\n")
	if len(lines) <= layout.Height {
		return rendered
	}

	return strings.Join(lines[:maxInt(layout.Height, 1)], "\n")
}

// renderRegion draws one block of lines as a single region.
//
// Every line is fitted to the width first, so a long chat title is cut
// with an ellipsis instead of pushing the block wider and shifting what is
// drawn to its right. A block of less than height lines is padded with
// empty ones, which is what keeps a surface covering its whole pane and
// the hint bar on the last row of the screen.
//
// Each line is rendered on its own rather than as one block of text, and
// that is not a style. Lip Gloss pads the lines of a block to the widest
// of them, and it measures width the way the grapheme rule does: a row with
// a hand in it comes out a column wider than the model fitted it, every
// other row of the pane is padded out to match, and the surface bleeds past
// the edge of the pane into the gap beside it. A line rendered alone has
// nothing to be aligned with, so the width that reaches the screen is the
// one the model measured — the one the terminal was measured for.
func (m Model) renderRegion(
	region themeRegion,
	width int,
	lines []string,
	height int,
) string {
	fitted := make([]string, 0, maxInt(len(lines), height))
	for _, line := range lines {
		fitted = append(fitted, m.widths.Fit(line, width, ellipsis))
	}

	if len(fitted) == 0 {
		fitted = append(fitted, m.widths.Fit("", width, ellipsis))
	}

	for len(fitted) < height {
		fitted = append(fitted, m.widths.Fit("", width, ellipsis))
	}

	style := m.styles().region(region)
	rendered := make([]string, 0, len(fitted))
	for _, line := range fitted {
		rendered = append(rendered, style.Render(line))
	}

	return strings.Join(rendered, "\n")
}

// lineCount returns how many rows a rendered block takes.
func lineCount(rendered string) int {
	if rendered == "" {
		return 0
	}

	return strings.Count(rendered, "\n") + 1
}

// viewTooSmall is the whole screen when there is not room for the layout.
func (m Model) viewTooSmall() string {
	return fmt.Sprintf(
		"Terminal is too small\nMinimum: %dx%d\nCurrent: %dx%d\n",
		minWidth, minHeight, m.width, m.height,
	)
}

// styles returns the view styles for the resolved theme.
func (m Model) styles() viewStyles {
	return newViewStyles(m.renderer(), m.theme)
}

// renderer returns the model's own Lip Gloss renderer.
//
// A model built without a resolution gets one on demand rather than a nil
// pointer: a zero model draws the too-small notice, which needs no styles,
// but a caller that asks for one gets a real one.
func (m Model) renderer() *lipgloss.Renderer {
	if m.rendererForProfile == nil {
		return newRenderer(m.colorProfile)
	}

	return m.rendererForProfile
}

// tokens returns the token set of the resolved theme.
func (m Model) tokens() theme.Tokens {
	return m.theme.Tokens
}

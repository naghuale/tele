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

	return m.fitHeight(m.overlayPopup(view, rows, firstRow), layout)
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
// A screen with no chat chosen draws the empty state of §17 in the right
// pane, under the same hint bar the conversation has. The empty pane is not
// focusable: the list is the only region with keys, and a second one that
// cannot be reached would be a second one that cannot be seen.
func (m Model) viewTwoPanes(layout Layout) string {
	right := m.conversationPaneRegion(layout)
	if m.screen != ScreenConversation {
		right = m.joinRegions(
			m.emptyConversationRegion(layout),
			m.footerRegion(layout, layout.ChatContentWidth()),
		)
	}

	return lipgloss.JoinHorizontal(
		lipgloss.Top,
		m.chatListRegion(layout, layout.SidebarContentWidth(), layout.Height),
		spaces(paneGapWidth),
		right,
	)
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
		styles.text(colour).Render(fitCells(title, width)),
	}

	if hint != "" {
		lines = append(
			lines,
			"",
			styles.dimmed(m.tokens().SecondaryText).Render(fitCells(hint, width)),
		)
	}

	// The status belongs under the title of a conversation, and this pane
	// is the conversation of a program that has not been asked to open one
	// yet. It is also the only place it is drawn on a two-pane screen with
	// no chat open, and a user who cannot tell whether telecli is connected
	// is the user who is about to press Enter.
	lines = append(lines, m.statusBlock(layout, width)...)

	return m.renderRegion(
		styles.conversation,
		false,
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
const (
	emptyConversationTitle = "Select a chat"
	emptyConversationHint  = "Use ↑ and ↓, then press Enter."
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
// The composer and the hint bar are the last rows of the pane and the
// messages take what is left, so a screen with three messages in it is a
// screen with a hint bar at the bottom rather than three lines at the top.
func (m Model) conversationPaneRegion(layout Layout) string {
	composer := m.composerRegion(
		layout,
		layout.ChatContentWidth(),
		m.composerHeight(layout, layout.ChatContentWidth()),
	)
	footer := m.footerRegion(layout, layout.ChatContentWidth())
	composerLines := lineCount(composer)
	footerLines := lineCount(footer)

	history := m.conversationRegion(
		layout,
		layout.ChatContentWidth(),
		layout.Height-composerLines-footerLines,
	)

	return m.joinRegions(history, composer, footer)
}

// chatListRegion draws the chat list pane at the given content width and
// height.
func (m Model) chatListRegion(layout Layout, width, height int) string {
	return m.renderRegion(
		m.styles().list,
		m.focus == FocusChatList,
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
		m.focus == FocusComposer,
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
		false,
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
func (m Model) renderRegion(
	region themeRegion,
	focused bool,
	width int,
	lines []string,
	height int,
) string {
	fitted := make([]string, 0, maxInt(len(lines), height))
	for _, line := range lines {
		fitted = append(fitted, fitCells(line, width))
	}

	if len(fitted) == 0 {
		fitted = append(fitted, fitCells("", width))
	}

	for len(fitted) < height {
		fitted = append(fitted, fitCells("", width))
	}

	return m.styles().
		region(region, focused, width).
		Render(strings.Join(fitted, "\n"))
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

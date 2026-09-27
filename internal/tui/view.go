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

	// The chat list is on the left of a two-pane screen, and a
	// conversation that is open shares the row with it. Without an open
	// conversation there is nothing to put in the second pane, so the list
	// takes the whole width rather than sitting in a quarter of the screen
	// beside empty space.
	if m.screen == ScreenConversation && layout.TwoPane() {
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

// viewTwoPanes draws the chat list and the conversation side by side.
func (m Model) viewTwoPanes(layout Layout) string {
	return lipgloss.JoinHorizontal(
		lipgloss.Top,
		m.chatListRegion(layout, layout.SidebarContentWidth(), layout.Height),
		spaces(paneGapWidth),
		m.conversationPaneRegion(layout),
	)
}

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

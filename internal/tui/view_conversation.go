package tui

import (
	"strings"
)

// conversationRegion draws the messages of the open conversation, with
// the header above them and the delivery state below.
//
// §5.2 keeps the focus off the messages: a line in front of each message
// would be a border, and there are none. The header carries the accent
// instead, and the region's own focus column says which region has the
// keys.
//
// The header is a line of its own and the timeline takes what is left of
// the block, because the composer and the hint bar are what a user must
// still see on a short screen (§3.4). Lines the timeline does not fill are
// left empty rather than spent on something else: the surface continues,
// and the composer stays on the last rows of the screen.
func (m Model) conversationRegion(
	layout Layout,
	width int,
	height int,
) string {
	styles := m.styles()

	lines := []string{
		styles.text(m.tokens().PrimaryText).
			Render(fitCells(m.conversationTitle(layout, width), width)),
	}

	// The progress of an older-page request sits below the messages it is
	// about: the messages stay on screen while the page is on its way, and
	// a failure leaves them intact for the next ↓ to retry.
	history := m.olderPageLines(layout, width)
	statuses := m.viewMessageStatuses()

	used := len(lines) + len(history)
	if statuses != "" {
		used += len(strings.Split(statuses, "\n"))
	}

	if budget := height - used; budget > 0 {
		lines = append(lines, m.timelineLines(layout, width, budget)...)
	}

	lines = append(lines, history...)

	if statuses != "" {
		lines = append(lines, strings.Split(statuses, "\n")...)
	}

	return m.renderRegion(
		styles.conversation,
		m.focus == FocusHistory,
		width,
		lines,
		height,
	)
}

// olderPageLines returns the line that says an older page is on its way,
// or that the last attempt failed.
func (m Model) olderPageLines(layout Layout, width int) []string {
	styles := m.styles()

	switch {
	case m.historyMoreLoading:
		return []string{styles.dimmed(m.tokens().SecondaryText).
			Render(fitCells("Loading older messages...", width))}

	case m.historyMoreErr != nil:
		text := "Failed to load older messages: " + m.historyMoreErr.Error()
		return []string{styles.dimmed(m.tokens().StatusError).
			Render(fitCells(text, width))}

	default:
		return nil
	}
}

// conversationTitle returns the title of the open chat, with the back
// marker a single-pane screen needs to get out of the conversation.
func (m Model) conversationTitle(layout Layout, width int) string {
	title := m.selected().Title
	if title == "" {
		title = "(no chat)"
	}

	if layout.TwoPane() {
		return title
	}

	// §3.3 shows the way back on the first line of a narrow
	// conversation. It is a word and not an arrow alone: the screen is
	// read by people who cannot see which glyph means what.
	return conversationBackMarker + "  " + title
}

// conversationBackMarker is how a single-pane conversation says where the
// way back is.
const conversationBackMarker = "< Chats"

// timelineLines returns the visible message lines.
//
// The order and the pagination are the ones the model has: this task does
// not change them, and PR-10A.2b does.
func (m Model) timelineLines(layout Layout, width int, budget int) []string {
	chat := m.selected()
	if len(chat.Messages) == 0 {
		return m.timelineEmptyLines(layout, width)
	}

	// One message takes an author line and a body line, except on a short
	// screen, where §3.4 drops the detail and the messages themselves
	// stay.
	messageHeight := timelineMessageHeight(layout)

	start, end := visibleRange(
		len(chat.Messages),
		m.selectedMsg,
		budget/messageHeight,
	)

	styles := m.styles()
	lines := make([]string, 0, (end-start)*messageHeight)

	for index := start; index < end; index++ {
		lines = append(
			lines,
			m.messageLines(chat.Messages[index], layout, width, styles)...,
		)
	}

	if end < len(chat.Messages) {
		lines = append(lines, styles.dimmed(m.tokens().MutedText).
			Render(fitCells("...", width)))
	}

	return lines
}

// timelineMessageHeight returns how many lines one message takes.
func timelineMessageHeight(layout Layout) int {
	if layout.Short() {
		return 1
	}

	return 2
}

// messageLines renders one message.
//
// The author and the time share the first line, with the time pushed to
// the right edge, which is the shape of every mock screen in §3. When
// there is no time, the line is still one line: a grid that changes shape
// per row is harder to read than one that does not.
func (m Model) messageLines(
	message Message,
	layout Layout,
	width int,
	styles viewStyles,
) []string {
	author := "Peer"
	if message.Outgoing {
		author = "You"
	}

	head := author
	if message.Time != "" {
		head = leftAndRight(author, message.Time, width)
	}

	lines := []string{
		styles.text(m.tokens().SecondaryText).Render(fitCells(head, width)),
	}

	if timelineMessageHeight(layout) == 1 {
		return []string{fitCells(
			author+": "+message.Text,
			width,
		)}
	}

	return append(lines, styles.text(m.tokens().PrimaryText).
		Render(fitCells(message.Text, width)))
}

// leftAndRight puts left at the start of a line of width columns and
// right at its end.
//
// The gap between them is filled with spaces, so two messages keep their
// timestamps in the same column and the eye can run down them.
func leftAndRight(left, right string, width int) string {
	gap := width - cellWidth(left) - cellWidth(right)
	if gap < 1 {
		return fitCells(left+" "+right, width)
	}

	return left + spaces(gap) + right
}

// timelineEmptyLines is what the conversation says before it has
// messages, or while the first page is on its way.
func (m Model) timelineEmptyLines(layout Layout, width int) []string {
	styles := m.styles()

	switch m.historyState {
	case loadStateLoading:
		return []string{styles.dimmed(m.tokens().SecondaryText).
			Render(fitCells("Loading history...", width))}

	case loadStateError:
		return []string{styles.dimmed(m.tokens().StatusError).
			Render(fitCells("Failed to load history", width))}

	case loadStateEmpty:
		return []string{styles.dimmed(m.tokens().MutedText).
			Render(fitCells("No messages yet", width))}

	default:
		return []string{styles.dimmed(m.tokens().MutedText).
			Render(fitCells("No messages", width))}
	}
}

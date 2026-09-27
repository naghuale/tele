package tui

import (
	"telecli/internal/tui/theme"
)

// This file draws the messages of a conversation.
//
// The shape is §4.4: the sender on one line with the time at the right
// edge, the text under it, an outgoing message indented a little and named
// in the accent. There is no frame and no bubble, and no line is drawn in
// front of a message to say that it is selected — §5.2 asks for one marker
// per selected message and for the accent to stay on the region, so the
// marker is a glyph in front of the message and the region's own column
// still says where the keys are.
//
// The order is chronological: the oldest message is drawn first, so the
// newest is on the last row of the screen. A text that does not fit wraps
// instead of being cut, because a message cut in the middle is a message a
// user cannot read and cannot answer.

// The words of a message line.
const (
	// outgoingAuthor is how a message of this user is signed (§4.4).
	outgoingAuthor = "You"

	// incomingAuthor stands in for the name of the other side until the
	// projection carries one. It is the word the interface has always
	// used, and a wrong name is worse than an honest one.
	incomingAuthor = "Peer"

	// outgoingIndent is how much further right an outgoing message starts.
	//
	// It is two columns: enough to tell the two sides apart at a glance,
	// little enough that a narrow pane still has room for the text.
	outgoingIndent = 2
)

// conversationRegion draws the messages of the open conversation, with
// the header above them and the delivery state below.
//
// The header is a line of its own and stays there while the messages move
// under it, which is what §4.4 means by a sticky header: the name of the
// chat is not something a user has to scroll back to.
//
// The rows the messages get are the rows that are left after the header,
// the line an older page occupies while it is on its way, and the delivery
// block. The model asks for the same number, so the two cannot disagree
// about how far a page of keys moves the cursor.
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

	// §4.3 puts the status under the title: a user reads it as part of the
	// header of the conversation rather than as the last line of it.
	lines = append(lines, m.statusBlock(layout, width)...)

	// The progress of an older-page request is at the top of the timeline,
	// where the page it is about will go: a line at the bottom would be
	// read as the end of the conversation, and the end is the newest
	// message.
	history := m.olderPageLines(layout, width)
	lines = append(lines, history...)

	// The history takes what the pending messages do not need. They are
	// below it because they are newer than anything in it, and they are in
	// the timeline rather than in a block of their own because a message
	// that is still leaving the program looks like every other message of
	// the conversation.
	pending := m.pendingMessageLines(layout, width)
	if rows := m.historyRows(layout, width); rows > 0 {
		lines = append(lines, m.timelineLines(layout, width, rows)...)
	}

	lines = append(lines, pending...)

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

// timelineLines returns the rows the messages take, oldest first.
//
// The window starts at the model's scroll anchor and moves only if the
// cursor would be outside it. That is the last word on where the cursor
// is: the model decides when the view scrolls, and the view refuses to
// draw a cursor it is not showing.
func (m Model) timelineLines(layout Layout, width, rows int) []string {
	messages := m.selected().Messages
	if len(messages) == 0 {
		return m.timelineEmptyLines(layout, width)
	}

	styles := m.styles()
	top := minInt(maxInt(m.timelineTop, 0), len(messages)-1)

	lines, drawn := m.timelineRowsFrom(messages, top, layout, width, rows, styles)
	if m.selectedMsg < top || m.selectedMsg >= top+drawn {
		lines, _ = m.timelineRowsFrom(
			messages,
			m.selectedMsg,
			layout,
			width,
			rows,
			styles,
		)
	}

	return lines
}

// timelineRowsFrom draws the messages from index first until the rows run
// out, and returns how many messages it drew.
func (m Model) timelineRowsFrom(
	messages []Message,
	first int,
	layout Layout,
	width int,
	rows int,
	styles viewStyles,
) ([]string, int) {
	var (
		lines []string
		drawn int
	)

	for index := first; index < len(messages); index++ {
		block := m.messageLines(
			messages[index],
			index == m.selectedMsg,
			layout,
			width,
			styles,
		)

		// A message taller than the rows that are left is cut at them. The
		// first one is cut rather than skipped, or a long message would
		// leave the timeline empty, and letting it grow past its rows would
		// push the composer off the screen, which §3.4 does not allow.
		if len(block) > rows-len(lines) {
			if len(lines) > 0 {
				break
			}

			block = block[:maxInt(rows, 0)]
		}

		lines = append(lines, block...)
		drawn++

		if len(lines) >= rows {
			break
		}
	}

	return lines, drawn
}

// messageLines renders one message as the rows it takes.
func (m Model) messageLines(
	message Message,
	selected bool,
	layout Layout,
	width int,
	styles viewStyles,
) []string {
	if layout.Short() {
		return m.shortMessageLines(message, selected, layout, width, styles)
	}

	indent := m.messageIndent(message)

	head := styles.selectionMark(selected, m.focus == FocusHistory).
		Render(theme.SelectionIndicator(selected)) +
		spaces(contentInsetWidth+indent) +
		styles.author(message.Outgoing, selected).
			Render(messageAuthor(message))

	if message.Time != "" {
		head = leftAndRight(
			head,
			styles.dimmed(m.tokens().MutedText).Render(message.Time),
			width,
		)
	}

	lines := []string{styles.selected(selected).Render(head)}

	return append(lines, m.messageBodyLines(message, indent, width, styles)...)
}

// shortMessageLines renders a message on a screen too short for two rows.
//
// The sender and the time go: what is left is the message itself, which is
// what a user came to read. The text still wraps, because a screen that is
// short is not a screen where a message may be cut in half.
func (m Model) shortMessageLines(
	message Message,
	selected bool,
	layout Layout,
	width int,
	styles viewStyles,
) []string {
	indent := m.messageIndent(message)
	inset := spaces(selectionMarkerWidth + contentInsetWidth + indent)
	textWidth := messageTextWidth(indent, width)

	wrapped := wrapCells(messageAuthor(message)+": "+message.Text, textWidth)
	if len(wrapped) == 0 {
		wrapped = []string{""}
	}

	body := make([]string, 0, len(wrapped))
	for _, line := range wrapped {
		body = append(body, styles.body(message.Outgoing).Render(inset+line))
	}

	head := styles.selectionMark(selected, m.focus == FocusHistory).
		Render(theme.SelectionIndicator(selected)) + body[0]

	return append([]string{styles.selected(selected).Render(head)}, body[1:]...)
}

// messageBodyLines returns the rows of the text of a message, wrapped to
// the width the message has.
func (m Model) messageBodyLines(
	message Message,
	indent int,
	width int,
	styles viewStyles,
) []string {
	textWidth := messageTextWidth(indent, width)
	if textWidth < 1 {
		return nil
	}

	// The text starts in the column the author's name starts in (§4.4), so
	// the marker column is spent on the text rows as well: a message whose
	// name is one column to the left of its own text reads as two messages.
	inset := spaces(selectionMarkerWidth + contentInsetWidth + indent)
	wrapped := wrapCells(message.Text, textWidth)
	lines := make([]string, 0, len(wrapped))

	for _, line := range wrapped {
		lines = append(lines, styles.body(message.Outgoing).Render(inset+line))
	}

	return lines
}

// messageTextWidth returns how many columns the text of a message has.
func messageTextWidth(indent, width int) int {
	return width - selectionMarkerWidth - contentInsetWidth - indent
}

// messageIndent returns how much further right a message starts than an
// incoming one.
func (m Model) messageIndent(message Message) int {
	if message.Outgoing {
		return outgoingIndent
	}

	return 0
}

// messageAuthor returns the word that stands for the sender of a message.
func messageAuthor(message Message) string {
	if message.Outgoing {
		return outgoingAuthor
	}

	return incomingAuthor
}

// leftAndRight puts left at the start of a row of width columns and right
// at its end.
//
// The gap between them is filled with spaces, so two messages keep their
// times in the same column and the eye can run down them. Both sides may
// carry escape sequences: the gap is measured in columns, not in bytes.
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
		// §17: a chat with nothing in it says so, and says where to
		// write. "No messages" on its own is the same sentence with the
		// answer removed.
		return []string{
			styles.dimmed(m.tokens().MutedText).
				Render(fitCells("No messages yet", width)),
			styles.dimmed(m.tokens().SecondaryText).
				Render(fitCells(noMessagesHint, width)),
		}

	default:
		return []string{styles.dimmed(m.tokens().MutedText).
			Render(fitCells("No messages yet", width))}
	}
}

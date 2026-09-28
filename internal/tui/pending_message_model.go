package tui

import (
	"sort"
	"time"

	"telecli/internal/tui/theme"
)

// This file is what the timeline shows above the composer: the outgoing
// messages the history does not have yet, with the state of each under its
// text (§4.4, §6).
//
// Two sources feed it. The queue is the truth about what it still holds,
// and it is read with the statuses on the same tick, so the text and the
// state of a message never come from two reads that disagree. And the
// message this session queued is added the moment the queue takes it, so
// that a user who pressed Enter sees the message instead of waiting for a
// poll to tell them it exists.

// mergePendingMessages replaces the drawn set with what the queue lists,
// plus what this session queued and the queue no longer does.
//
// A message of this session that the queue has accepted is no longer in
// its pending list — accepted is not something a queue holds — and it stays
// on the screen until the history brings it back, so that the user watches
// it go out. Its state comes from the statuses, which is the only place an
// accepted state is still visible.
func (m *Model) mergePendingMessages(
	fromQueue []PendingMessage,
	statuses []MessageStatus,
) {
	merged := make([]PendingMessage, 0, len(fromQueue)+len(m.pending))
	listed := make(map[string]struct{}, len(fromQueue))

	for _, message := range fromQueue {
		listed[message.EntryID] = struct{}{}
		merged = append(merged, message)
	}

	for _, message := range m.pending {
		if !message.local {
			continue
		}
		if _, stillQueued := listed[message.EntryID]; stillQueued {
			continue
		}

		merged = append(merged, message.withStateFrom(statuses))
	}

	sort.SliceStable(merged, func(i, j int) bool {
		return pendingMessageLess(merged[i], merged[j])
	})

	m.pending = merged
}

// withStateFrom returns the message with the state the statuses report for
// it, or unchanged where the statuses do not mention it.
//
// A message the statuses have never heard of keeps the state the queue
// gave it: an empty snapshot is not a statement that nothing happened.
func (m PendingMessage) withStateFrom(
	statuses []MessageStatus,
) PendingMessage {
	for _, status := range statuses {
		if status.EntryID != m.EntryID {
			continue
		}

		m.State = status.State
		m.Attempt = status.Attempt
		m.NextAttemptAt = status.NextAttemptAt

		return m
	}

	return m
}

// noteQueuedMessage adds a message this session has just queued.
//
// The text is the draft and the entry is what the queue returned, so the
// timeline can draw the message at once. It waits for a poll like anything
// else to learn what happens to it.
func (m *Model) noteQueuedMessage(
	entryID string,
	chatID int64,
	text string,
	state MessageDeliveryState,
	now time.Time,
) {
	if m == nil || entryID == "" {
		return
	}

	for _, message := range m.pending {
		if message.EntryID == entryID {
			return
		}
	}

	m.pending = append(m.pending, PendingMessage{
		EntryID:   entryID,
		ChatID:    chatID,
		Text:      text,
		State:     state,
		CreatedAt: now,
		local:     true,
	})
	sort.SliceStable(m.pending, func(i, j int) bool {
		return pendingMessageLess(m.pending[i], m.pending[j])
	})
}

// pendingMessageRowCount returns how many rows the pending messages take
// on the screen.
//
// They are the newest messages of the conversation and they are below the
// history, so the history takes what is left. A screen with no room for
// both is the screen of §3.4, and the composer comes first there.
func (m Model) pendingMessageRowCount(layout Layout, width int) int {
	if len(m.pending) == 0 {
		return 0
	}

	return len(m.pendingMessageLines(layout, width))
}

// pendingMessageLines returns the rows of every pending message, oldest
// first, each with the state of the message under its text.
func (m Model) pendingMessageLines(layout Layout, width int) []string {
	styles := m.styles()
	rows := make([]string, 0, len(m.pending))

	for _, message := range m.pending {
		rows = append(
			rows,
			m.pendingMessageRows(message, layout, width, styles)...,
		)
	}

	return rows
}

// pendingMessageRows returns the rows of one pending message: its text in
// the block on the right, and the time and the state of the send under it
// (§4.4).
//
// It is the same shape as a message of the history — the timeline is one
// column of messages, and a message that is still leaving the program looks
// like every other one of them — with one difference: the state, which is
// what the history does not have to say because the history is the past.
func (m Model) pendingMessageRows(
	message PendingMessage,
	layout Layout,
	width int,
	styles viewStyles,
) []string {
	blockWidth := m.outgoingBlockWidth(width)
	offset := maxInt(width-selectionMarkerWidth-blockWidth, 0)
	textWidth := maxInt(blockWidth-2*outgoingBubbleInset, 1)

	rows := make([]string, 0, 4)
	for _, line := range m.widths.Wrap(message.Text, textWidth, ellipsis) {
		rows = append(rows, m.outgoingRow(
			styles, spaces(selectionMarkerWidth), offset, blockWidth,
			line, false,
		))
	}

	if state := m.deliveryStateLabelFor(message, textWidth); state != "" {
		rows = append(rows, m.outgoingRow(
			styles, spaces(selectionMarkerWidth), offset, blockWidth,
			state, true,
		))
	}
	if at := formatMessageTime(message.CreatedAt); at != "" {
		rows = append(rows, m.outgoingRow(
			styles, spaces(selectionMarkerWidth), offset, blockWidth, at, true,
		))
	}

	// §3.1 puts the second row of an uncertain message under the state,
	// and it is the only thing in the interface that says what the user
	// has to decide: sending again may create a duplicate, and that is a
	// decision rather than an error to retry away.
	if message.State == MessageDeliveryUncertain {
		rows = append(rows, m.outgoingRow(
			styles, spaces(selectionMarkerWidth), offset, blockWidth,
			uncertainWarning, true,
		))
	}

	return rows
}

// uncertainWarning is the second row of an uncertain message.
const uncertainWarning = "Message may already have been sent"

// deliveryStateLabel returns the words of a delivery state: the symbol and
// the word, and never one without the other.
//
// The words carry the meaning and the symbol makes it readable at a glance
// (§2.7, §14), so a terminal without the glyph says the same thing. An
// uncertain message does not borrow the wording of a failure: a message
// that may have gone out is not a message that did not.
func (m Model) deliveryStateLabel(
	state MessageDeliveryState,
	width int,
) string {
	marked, known := deliveryStateOf(state)
	if !known {
		return ""
	}

	return m.widths.TruncateMarked(marked.Mark().String(), width, ellipsis)
}

// deliveryStateLabelFor returns the words of the state of a pending
// message and the time it is due at.
//
// A retry is a time, not a countdown: §6.3 asks for a screen that does not
// repaint itself every second, and a countdown is a screen that repaints
// itself every second.
func (m Model) deliveryStateLabelFor(
	message PendingMessage,
	width int,
) string {
	label := m.deliveryStateLabel(message.State, width)
	if label == "" {
		return ""
	}

	if message.State == MessageDeliveryRetrying {
		if at := formatMessageTime(message.NextAttemptAt); at != "" {
			label += " at " + at
		}
	}

	return m.widths.TruncateMarked(label, width, ellipsis)
}

// deliveryStateOf maps a TUI delivery state onto the theme's vocabulary of
// states.
//
// The switch is a check and not a cast: a state this build does not know is
// not drawn as one it does, and the message keeps its text without a label
// rather than being given a wrong one.
func deliveryStateOf(state MessageDeliveryState) (theme.StatusState, bool) {
	switch state {
	case MessageDeliveryQueued:
		return theme.StatusQueued, true

	case MessageDeliverySending:
		return theme.StatusSending, true

	case MessageDeliveryRetrying:
		return theme.StatusRetrying, true

	case MessageDeliveryFailed:
		return theme.StatusFailed, true

	case MessageDeliveryUncertain:
		return theme.StatusUncertain, true

	case MessageDeliverySent:
		return theme.StatusSent, true

	case MessageDeliveryCanceled:
		return theme.StatusCanceled, true

	default:
		return 0, false
	}
}

// formatMessageTime returns the HH:MM of an instant, or nothing when there
// is no instant to format.
//
// The same shape as the time of a message in the history, because it is the
// same column on the same screen: the time of a message queued at 14:28 and
// the time of one sent at 14:28 have to line up.
func formatMessageTime(at time.Time) string {
	if at.IsZero() {
		return ""
	}

	return at.Format("15:04")
}

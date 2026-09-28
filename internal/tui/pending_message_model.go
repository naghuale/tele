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
	m.pending = mergePendingMessages(m.pending, fromQueue, statuses)
}

// mergePendingMessages is the pending list a read of the queue produces
// out of the one on the screen.
//
// It is a function of its arguments because the delivery poll has to be able
// to ask what a read WOULD draw before deciding whether to deliver it: a
// read that changes nothing the screen shows is a repaint that flickers,
// and a read that changes something must be delivered even when the queue's
// two lists are identical to the last time.
func mergePendingMessages(
	held []PendingMessage,
	fromQueue []PendingMessage,
	statuses []MessageStatus,
) []PendingMessage {
	merged := make([]PendingMessage, 0, len(fromQueue)+len(held))
	listed := make(map[string]struct{}, len(fromQueue))

	for _, message := range fromQueue {
		listed[message.EntryID] = struct{}{}
		merged = append(merged, message)
	}

	for _, message := range held {
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

	return merged
}

// withStateFrom returns the message with the state the statuses report for
// it, or unchanged where the statuses do not mention it.
//
// A message the statuses have never heard of keeps the state the queue
// gave it: an empty snapshot is not a statement that nothing happened.
//
// The confirmed message identifier comes from the statuses too, because
// that is the only place a final one is: while the queue is still sending
// it holds a temporary identifier that no history page contains.
func (m PendingMessage) withStateFrom(
	statuses []MessageStatus,
) PendingMessage {
	for _, status := range statuses {
		if status.EntryID != m.EntryID {
			continue
		}

		m.State = status.State
		m.MessageID = status.MessageID
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
	label, colour := m.deliveryStateLabel(message)

	// The warning is a line of the block as much as the state is, and a
	// block that had to cut the sentence in half is a block that cannot
	// say what a user has to decide.
	under := []string{label}
	if message.State == MessageDeliveryUncertain {
		under = append(under, uncertainWarning)
	}

	block := m.messageBlockFor(sideOutgoing, layout, width, message.Text, under...)

	rows := make([]string, 0, 4)
	for _, line := range m.widths.Wrap(message.Text, block.text, ellipsis) {
		rows = append(rows, m.blockRow(
			false, styles, styles.unstyled(), spaces(selectionMarkerWidth),
			block, []blockRun{{
				style: styles.text(m.tokens().OutgoingMessage),
				text:  line,
			}}, false,
		))
	}

	// The state and the time it was queued are one line, because they are
	// one fact about the message: it is at this state, as of this moment.
	// Two lines of it under a two-line message is a status block the reader
	// has to assemble out of three rows.
	if label != "" {
		rows = append(rows, m.blockRow(
			false, styles, styles.unstyled(), spaces(selectionMarkerWidth),
			block, []blockRun{{style: styles.text(colour), text: label}}, true,
		))
	}

	// §3.1 puts the second row of an uncertain message under the state,
	// and it is the only thing in the interface that says what the user
	// has to decide: sending again may create a duplicate, and that is a
	// decision rather than an error to retry away.
	if message.State == MessageDeliveryUncertain {
		rows = append(rows, m.blockRow(
			false, styles, styles.unstyled(), spaces(selectionMarkerWidth),
			block, []blockRun{{
				style: styles.text(m.tokens().StatusUncertain),
				text:  uncertainWarning,
			}}, true,
		))
	}

	// The air of §4.4 is on a message of this user that is still leaving
	// the program too: it is the same block as every other message of the
	// timeline and the same drawing, and a message that grew an air row
	// the moment it was sent would be a block whose shape depended on how
	// far along it was.
	return m.withAirAround(sideOutgoing, false, styles, block, rows)
}

// deliveryStateLabel returns the words under a message of this user and the
// colour of them.
//
// The words come back whole rather than fitted to a width, for the same
// reason the state of a message of the history does: the block of a
// message is as wide as the state under it, and a state cut to the width of
// a block that was measured without it is a state that cannot name what
// happened to the message.
//
// The colour is the state's own, so the line under a message repeats what
// the words under it say rather than being the same grey everywhere. A
// state this build does not know keeps its text and is drawn in the muted
// tier rather than being given a colour that belongs to a different state.
func (m Model) deliveryStateLabel(
	message PendingMessage,
) (string, theme.Color) {
	state, known := deliveryStateOf(message.State)
	if !known {
		return "", m.tokens().MutedText
	}

	label := state.Mark().String()
	queuedAt := formatMessageTime(message.CreatedAt)

	// A retry is a time, not a countdown: §6.3 asks for a screen that does
	// not repaint itself every second, and a countdown is a screen that
	// repaints itself every second.
	//
	// The state and the time are one line because they are one fact about
	// the message, and a retrying one is the exception rather than the
	// rule: it names a time of its own, and two times on one line is a
	// sentence a user has to read twice.
	if retry := formatMessageTime(message.NextAttemptAt); retry != "" &&
		message.State == MessageDeliveryRetrying {
		label += " at " + retry
	} else if queuedAt != "" {
		label += " " + queuedAt
	}

	return label, state.Color(m.tokens())
}

// uncertainWarning is the second row of an uncertain message.
const uncertainWarning = "Message may already have been sent"

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

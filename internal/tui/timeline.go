package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// This file is the scroll model of the conversation.
//
// The cursor and the scroll anchor are two different things, and keeping
// them apart is what makes a page of older messages and a resize survivable.
// The cursor (selectedMsg) is the message the keys act on. The anchor
// (timelineTop) is the message on the first row of the screen. A reader who
// walks the cursor down a long history moves the anchor only when the
// cursor would leave the window, and a page that arrives above the window
// moves the anchor by exactly what it added, so the message that was on
// screen stays on screen.
//
// The order of the messages is chronological — the oldest first, the newest
// last — because that is how a conversation is read (§8.3). The source
// answers newest first and the reversal happens where the page arrives.
//
// The list is the history and the pending messages of §4.4, in that order:
// a message that has not been delivered yet is newer than any message of
// the history, and it is a message like any other, so the cursor walks on
// to it and the keys of §13 act on it. The two are kept apart in the model
// because they come from two sources, and together in the index space
// because they are one list on the screen.

// timelineMessageMinHeight is the fewest rows a message can take.
//
// A message is its author line and its text, and §3.4 drops the text on a
// short screen rather than the messages. Dividing the budget by this gives
// the number of messages a page of keys moves the cursor by; the view
// renders fewer of them when a text wraps, and scrolls to the cursor rather
// than trusting the estimate.
const timelineMessageMinHeight = 2

// timelinePageSize returns how many messages fit on the screen.
func (m Model) timelinePageSize() int {
	layout := LayoutFor(m.width, m.height)
	rows := m.timelineRows(layout, layout.ChatContentWidth())

	return maxInt(rows/timelineMessageMinHeight, 1)
}

// timelineRows returns how many rows of the screen the messages may take.
//
// The model and the view both ask it, so the two cannot disagree about how
// far a page of keys moves the cursor. The header is a row of its own, and
// so is the line an older page occupies while it is on its way; the
// composer and the hint bar are not the timeline's to spend.
func (m Model) timelineRows(layout Layout, width int) int {
	rows := m.conversationRegionHeight(layout, width) - 1
	rows -= m.olderPageLineCount()

	return maxInt(rows, 0)
}

// historyRows returns the rows the messages of the history get, which is
// the timeline without what the pending messages below it take.
func (m Model) historyRows(layout Layout, width int) int {
	return maxInt(
		m.timelineRows(layout, width)-m.pendingMessageRowCount(layout, width),
		0,
	)
}

// conversationRegionHeight returns how many rows the conversation has: the
// screen without the composer and without the hint bar.
func (m Model) conversationRegionHeight(layout Layout, width int) int {
	height := layout.Height -
		m.composerHeight(layout, width) -
		len(m.hintLines(layout, width))

	return maxInt(height, 0)
}

// olderPageLineCount returns the rows the progress of an older-page request
// takes, which is one row or none.
func (m Model) olderPageLineCount() int {
	if m.historyMoreLoading || m.historyMoreErr != nil {
		return 1
	}

	return 0
}

// timelineTotal returns how many messages the timeline holds: the history
// of the open chat and the pending messages below it.
func (m Model) timelineTotal() int {
	return len(m.selected().Messages) + len(m.pending)
}

// historyTotal returns how many messages of the timeline are history, which
// is the boundary a pending message's index starts at.
func (m Model) historyTotal() int {
	return len(m.selected().Messages)
}

// selectedPending returns the pending message under the cursor, if the
// cursor is on one.
func (m Model) selectedPending() (PendingMessage, bool) {
	index := m.selectedMsg - m.historyTotal()
	if index < 0 || index >= len(m.pending) {
		return PendingMessage{}, false
	}

	return m.pending[index], true
}

// selectedHistory returns the message of the history under the cursor, if
// the cursor is on one.
func (m Model) selectedHistory() (Message, bool) {
	messages := m.selected().Messages
	if m.selectedMsg < 0 || m.selectedMsg >= len(messages) {
		return Message{}, false
	}

	return messages[m.selectedMsg], true
}

// pendingMessageOf returns the delivery state of a pending message the
// timeline is showing, and whether it is showing it at all.
//
// A record the queue has moved on is dropped from the screen as soon as the
// read that says so arrives, so a user who acts on a stale record is told
// by its absence rather than by a message that contradicts it.
func (m Model) pendingMessageOf(entryID string) *MessageDeliveryState {
	for _, message := range m.pending {
		if message.EntryID == entryID {
			state := message.State

			return &state
		}
	}

	return nil
}

// dropPendingMessage removes a record from the screen after a cancel, so
// that the read that follows is not fighting a record that is not there.
func (m *Model) dropPendingMessage(entryID string) {
	kept := make([]PendingMessage, 0, len(m.pending))
	for _, message := range m.pending {
		if message.EntryID == entryID {
			continue
		}
		kept = append(kept, message)
	}
	m.pending = kept
	m.pendingSnapshot = clonePendingMessages(kept)
	m.selectedMsg = minInt(m.selectedMsg, maxInt(m.timelineTotal()-1, 0))
}

// scrollToNewest puts the cursor on the newest message and the view at the
// end of the conversation.
//
// Opening a chat and sending a message both land here: they are the two
// moments a user is looking for the newest thing in a conversation, and the
// newest thing is a pending message when there is one.
func (m Model) scrollToNewest() Model {
	total := m.timelineTotal()
	if total == 0 {
		m.selectedMsg = 0
		m.timelineTop = 0

		return m
	}

	m.selectedMsg = total - 1
	m.timelineTop = maxInt(m.historyTotal()-m.timelinePageSize(), 0)

	return m
}

// moveTimelineCursor moves the cursor by delta messages and scrolls the
// view the least it has to.
func (m Model) moveTimelineCursor(delta int) Model {
	total := m.timelineTotal()
	if total == 0 {
		return m
	}

	m.selectedMsg = minInt(maxInt(m.selectedMsg+delta, 0), total-1)

	return m.scrollCursorIntoView()
}

// scrollCursorIntoView moves the anchor so the cursor is on screen.
//
// A cursor that walks off the bottom of the window takes the window with
// it, and one that walks off the top takes it the other way. Nothing else
// moves: the window stays where it is while the cursor is inside it, which
// is what makes reading a conversation feel like reading and not like
// chasing the cursor.
//
// A cursor on a pending message is only on screen when the history is
// scrolled to its end, because the pending messages are drawn below the
// history and not inside its window. So that is where the window goes.
func (m Model) scrollCursorIntoView() Model {
	page := m.timelinePageSize()
	history := m.historyTotal()

	if m.selectedMsg >= history {
		m.timelineTop = maxInt(history-page, 0)

		return m.normalizeTimeline()
	}

	if m.selectedMsg < m.timelineTop {
		m.timelineTop = m.selectedMsg
	}
	if m.selectedMsg >= m.timelineTop+page {
		m.timelineTop = m.selectedMsg - page + 1
	}

	return m.normalizeTimeline()
}

// normalizeTimeline puts the cursor and the anchor back inside the loaded
// messages.
//
// A resize, a chat with no messages and a page that added nothing all make
// one of the two point at a message that is not there, and an anchor past
// the end of the history is a view that starts in the middle of nowhere.
func (m Model) normalizeTimeline() Model {
	total := m.timelineTotal()
	if total == 0 {
		m.selectedMsg = 0
		m.timelineTop = 0

		return m
	}

	m.selectedMsg = minInt(maxInt(m.selectedMsg, 0), total-1)
	m.timelineTop = minInt(
		maxInt(m.timelineTop, 0),
		maxInt(m.historyTotal()-1, 0),
	)

	return m
}

// updateHistoryKey handles the keys of the message timeline (§8.3).
//
// `j`/`↓` and `k`/`↑` walk the conversation, a page at a time with PgUp and
// PgDn, and `G` goes to the newest message. `g` is deliberately not here:
// it is the chat list's "first chat", and in a conversation it would mean
// something else entirely, so it does nothing. `Enter` and `i` hand the
// keys to the composer, which is the one place a message is written.
func (m Model) updateHistoryKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case isUp(msg):
		// The cursor is on the oldest loaded message, so this is the
		// gesture for reaching further back. Everything #12 does with the
		// request stays as it is; only the key it hangs on has moved.
		if m.selectedMsg > 0 {
			return m.moveTimelineCursor(-1), nil
		}

		return m.loadOlderMessages()

	case isDown(msg):
		return m.moveTimelineCursor(1), nil

	case isPageUp(msg):
		return m.moveTimelineCursor(-m.timelinePageSize()), nil

	case isPageDown(msg):
		return m.moveTimelineCursor(m.timelinePageSize()), nil

	case isLast(msg):
		return m.scrollToNewest(), nil

	case msg.Type == tea.KeyEnter || isInsertMode(msg):
		m.focus = FocusComposer

		return m, nil

	case isOpenActions(msg):
		// §13: the menu of what can be done with the message under the
		// cursor. It opens on the message and not on the chat, so a user
		// who pressed it by accident finds a menu they can close.
		m.openActionSheet()

		return m, nil
	}

	return m, nil
}

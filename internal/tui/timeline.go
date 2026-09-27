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
	rows -= m.messageStatusLineCount()

	return maxInt(rows, 0)
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

// messageStatusLineCount returns the rows the durable delivery block takes.
func (m Model) messageStatusLineCount() int {
	statuses := m.viewMessageStatuses()
	if statuses == "" {
		return 0
	}

	return lineCount(statuses)
}

// scrollToNewest puts the cursor on the newest message and the view at the
// end of the conversation.
//
// Opening a chat and sending a message both land here: they are the two
// moments a user is looking for the newest thing in a conversation.
func (m Model) scrollToNewest() Model {
	total := len(m.selected().Messages)
	if total == 0 {
		m.selectedMsg = 0
		m.timelineTop = 0

		return m
	}

	m.selectedMsg = total - 1
	m.timelineTop = maxInt(total-m.timelinePageSize(), 0)

	return m
}

// moveTimelineCursor moves the cursor by delta messages and scrolls the
// view the least it has to.
func (m Model) moveTimelineCursor(delta int) Model {
	total := len(m.selected().Messages)
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
func (m Model) scrollCursorIntoView() Model {
	page := m.timelinePageSize()

	if m.selectedMsg < m.timelineTop {
		m.timelineTop = m.selectedMsg
	}
	if m.selectedMsg >= m.timelineTop+page {
		m.timelineTop = m.selectedMsg - page + 1
	}

	return m.normalizeTimeline()
}

// normalizeTimeline puts the cursor and the anchor back inside the loaded
// history.
//
// A resize, a chat with no messages and a page that added nothing all make
// one of the two point at a message that is not there, and an anchor past
// the end of the history is a view that starts in the middle of nowhere.
func (m Model) normalizeTimeline() Model {
	total := len(m.selected().Messages)
	if total == 0 {
		m.selectedMsg = 0
		m.timelineTop = 0

		return m
	}

	m.selectedMsg = minInt(maxInt(m.selectedMsg, 0), total-1)
	m.timelineTop = minInt(maxInt(m.timelineTop, 0), total-1)

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
	}

	return m, nil
}

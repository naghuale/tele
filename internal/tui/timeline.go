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
// the number of messages a page of keys moves the cursor by, which is all
// it is for: where the window is *placed* is measured, not estimated (see
// anchoredAt), because an estimate of two rows a message placed the window
// a third of a screen too late and left the feed with empty rows in it.
const timelineMessageMinHeight = 2

// timelinePageSize returns how many messages fit on the screen.
//
// It is a page of keys, not a window: PgUp and PgDn move the cursor by it,
// and nothing else. It is a guess on purpose — a page of keys that moves by
// a guess and a window that is placed by a measurement are two different
// questions, and the second one is the one a reader looks at.
func (m Model) timelinePageSize() int {
	layout := LayoutFor(m.width, m.height)
	rows := m.timelineRows(layout, layout.ChatContentWidth())

	return maxInt(rows/timelineMessageMinHeight, 1)
}

// timelineRows returns how many rows of the screen the messages may take.
//
// The model and the view both ask it, so the two cannot disagree about how
// far a page of keys moves the cursor. The header is two rows of its own —
// the name of the chat and the rule that says the timeline has the keys —
// and so is the line an older page occupies while it is on its way; the
// composer band and the hints inside it are not the timeline's to spend.
func (m Model) timelineRows(layout Layout, width int) int {
	rows := m.conversationRegionHeight(layout, width) - conversationHeaderRows
	rows -= m.olderPageLineCount()

	return maxInt(rows, 0)
}

// conversationHeaderRows is how many rows of the conversation the heading
// takes: the name of the chat and the rule under it.
const conversationHeaderRows = 2

// historyFeedRows returns how many rows the entries of the history take on
// the screen: the rows of the region less the heading, the status block
// under it, the line an older page occupies and the rows the pending
// messages below take.
//
// This is the number the window is placed against *and* the number the view
// draws the feed into, and that is the whole of why the placement is right:
// a window measured against one budget and drawn into another is a window
// with empty rows above it or a message cut in half at the top.
func (m Model) historyFeedRows(layout Layout, width int) int {
	rows := m.conversationRegionHeight(layout, width) - conversationHeaderRows
	rows -= len(m.statusBlockLines(layout, width))
	rows -= m.olderPageLineCount()
	rows -= m.pendingMessageRowCount(layout, width)

	return maxInt(rows, 0)
}

// feedRows returns how many rows the entries of the history may take on the
// screen of the size the model has.
func (m Model) feedRows() int {
	layout := LayoutFor(m.width, m.height)

	return m.historyFeedRows(layout, layout.ChatContentWidth())
}

// entryRows returns how many rows an entry takes on the screen.
//
// It is the height the view draws it at, from the function the view draws
// it with, so the two cannot disagree about it: a message whose text wraps
// takes the rows it wraps into, an album is one entry however many parts it
// has, and the blank row that separates two messages belongs to the one
// below the gap.
func (m Model) entryRows(
	entry timelineEntry,
	layout Layout,
	width int,
	styles viewStyles,
) int {
	return len(m.entryLines(entry, layout, width, styles))
}

// anchoredAt returns the message index the window starts at when the entry
// at entryIndex is on its last row, and how many rows of that entry the
// view leaves off its top.
//
// It is the window filled from the bottom: walk back from the entry, adding
// what each entry really takes, until the rows of the feed are full. Nothing
// is drawn above the first entry of the walk, which is the whole point — a
// window placed a message too late is a feed with empty rows in it, and a
// user who scrolls up to find out why is looking at messages that were on
// the screen all along.
//
// The rows of the feed are not a whole number of messages, and the entry
// that fills the last of them is drawn from the middle rather than left
// out. Stopping at the first entry that fits *whole* — which is what a
// window placed by a guess did, and what this replaced — leaves up to the
// height of one message of empty rows above the conversation, and an empty
// row above the first message is what this whole change is about. What is
// cut is the blank row that separates the message from the one above it,
// and at worst its author line; the newest message is never the one cut,
// because the walk starts from it.
func (m Model) anchoredAt(entryIndex int) (messageIndex, cutRows int) {
	messages := m.selected().Messages
	if len(messages) == 0 {
		return 0, 0
	}

	layout := LayoutFor(m.width, m.height)
	width := layout.ChatContentWidth()
	rows := m.historyFeedRows(layout, width)
	entries := timelineEntries(messages)
	styles := m.styles()

	// The entry the cursor is on is always in the window, whatever it
	// takes: the view cuts an entry that is taller than the feed rather
	// than leaving the feed empty, and a window that started below it
	// would be a window with nothing in it.
	top := clampIndex(entryIndex, len(entries)-1)
	used := m.entryRows(entries[top], layout, width, styles)

	for index := top - 1; index >= 0 && used < rows; index-- {
		height := m.entryRows(entries[index], layout, width, styles)
		if used+height > rows {
			return entries[index].first, used + height - rows
		}

		used += height
		top = index
	}

	return entries[top].first, 0
}

// anchorAt places the window with its entry at entryIndex on the last row,
// and remembers how much of the top of that entry is left off.
func (m Model) anchorAt(entryIndex int) Model {
	m.timelineTop, m.timelineCut = m.anchoredAt(entryIndex)

	return m
}

// anchorAtNewest places the window with the newest message on the last row:
// a conversation opened at its end, a message sent, and a page of older
// messages arriving at somebody who is reading the newest thing.
func (m Model) anchorAtNewest() Model {
	messages := m.selected().Messages
	if len(messages) == 0 {
		m.timelineTop, m.timelineCut = 0, 0

		return m
	}

	return m.anchorAt(len(timelineEntries(messages)) - 1)
}

// rowsFromTopTo returns how many rows the entries from first up to and
// including last take, which is how far into the window a message sits.
func (m Model) rowsFromTopTo(
	entries []timelineEntry,
	first, last int,
	layout Layout,
	width int,
	styles viewStyles,
) int {
	rows := 0
	for index := first; index <= last && index < len(entries); index++ {
		rows += m.entryRows(entries[index], layout, width, styles)
	}

	return rows
}

// conversationRegionHeight returns how many rows the conversation has: the
// screen without the composer's band, which is the draft, the hints and the
// line of space above them.
func (m Model) conversationRegionHeight(layout Layout, width int) int {
	return maxInt(layout.Height-m.composerHeight(layout, width), 0)
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
//
// The window is filled from the bottom by the heights the entries really
// take, so the feed is full from its top row to the newest message. It used
// to start a page of keys above the newest, which was two rows a message
// where a message is three: the window started about a third of a screen
// too late and the rows above it were empty, and a user who scrolled up to
// see what was above was looking for messages that had been on the screen
// all along.
func (m Model) scrollToNewest() Model {
	total := m.timelineTotal()
	if total == 0 {
		m.selectedMsg = 0
		m.timelineTop, m.timelineCut = 0, 0

		return m
	}

	m.selectedMsg = total - 1

	return m.anchorAtNewest()
}

// timelineFollowsNewest reports whether the cursor is at the end of the
// conversation, which is where a user is after opening a chat and after
// sending a message.
//
// It is what a page of older messages and a resize have to know: a window
// that is following is placed again, and a window a reader has scrolled
// away from is left exactly where it is. A user reading upwards who is
// thrown back to the newest message by a page they asked for has lost their
// place, and the page is exactly what they asked for.
func (m Model) timelineFollowsNewest() bool {
	total := m.timelineTotal()
	if total == 0 {
		return true
	}

	return m.selectedMsg >= total-1
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
//
// Both ends of the walk are measured by the heights the entries really
// take, so the cursor is never left on a row the view is not drawing and
// the window never ends with empty rows in it.
func (m Model) scrollCursorIntoView() Model {
	messages := m.selected().Messages
	if len(messages) == 0 {
		return m
	}

	if m.selectedMsg >= m.historyTotal() {
		return m.anchorAtNewest().normalizeTimeline()
	}

	layout := LayoutFor(m.width, m.height)
	width := layout.ChatContentWidth()
	entries := timelineEntries(messages)
	cursor := entryIndexOfMessage(entries, clampIndex(m.selectedMsg, len(messages)-1))
	top := entryIndexOfMessage(entries, clampIndex(m.timelineTop, len(messages)-1))

	if cursor < top {
		// The cursor is the top of the window now, and a message the
		// cursor is on is never cut.
		m.timelineTop, m.timelineCut = m.selectedMsg, 0

		return m.normalizeTimeline()
	}

	// The cursor is below the last row the window draws: the window moves
	// down until the cursor is on its last row, filled with the entries
	// above it by their real heights.
	if m.rowsFromTopTo(
		entries, top, cursor, layout, width, m.styles(),
	) > m.historyFeedRows(layout, width) {
		m = m.anchorAt(cursor)
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
		m.timelineTop, m.timelineCut = 0, 0

		return m
	}

	m.selectedMsg = minInt(maxInt(m.selectedMsg, 0), total-1)
	anchor := minInt(
		maxInt(m.timelineTop, 0),
		maxInt(m.historyTotal()-1, 0),
	)
	if anchor != m.timelineTop {
		// The anchor was pointing at a message that is not there, so the
		// cut belongs to a window that is gone with it.
		m.timelineCut = 0
	}
	m.timelineTop = anchor

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

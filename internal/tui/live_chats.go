package tui

import (
	"strconv"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// This file applies the live Telegram state to the screen: the chat list
// that moves on its own, and the messages that arrive in the conversation
// that is open.
//
// Two things are kept apart on purpose. The list is a snapshot of the whole
// main list and is replaced as a whole, because that is what TDLib keeps
// and because a row that was half updated is a row that says a chat has
// something in it when it does not. The messages of one chat are events,
// and an event is applied to the conversation it happened in: a message
// that arrived while another chat was open is still in the store, and the
// window of that chat is read when its conversation is on the screen.

// liveOffsetUnset is the chatListOffset of a model whose window has not
// been placed yet.
const liveOffsetUnset = -1

// armLiveWait returns the command that waits for the next change of the
// live state, when one is not already on its way.
//
// One wait at a time is the whole of the subscription: the first one is
// armed where the model is built and every change arms it again, so a
// program that is running has exactly one goroutine reading the store's
// change signal however long it has been running.
func (m *Model) armLiveWait() tea.Cmd {
	if m == nil || m.live == nil || m.liveWaitArmed {
		return nil
	}

	m.liveWaitArmed = true

	return waitLiveCmd(m.live, m.ctx)
}

// updateLiveChanged is what the interface does when the live state says it
// has changed.
//
// The redraw is not immediate: at most one per liveRepaintInterval, with
// every change that arrives in between folded into it. A redraw reads the
// state, so the fold costs nothing but the frame that would have been drawn
// anyway, and a channel with a hundred messages a second is a list that
// moves ten times a second rather than a program that paints a hundred
// frames nobody can read.
func (m Model) updateLiveChanged(liveChangedMsg) (tea.Model, tea.Cmd) {
	// The wait that delivered this message is over, and the next one is
	// armed here: one wait at a time, and never none.
	m.liveWaitArmed = false
	wait := m.armLiveWait()
	if m.live == nil {
		return m, wait
	}

	if delay := m.liveRepaintDelay(); delay > 0 {
		// One redraw is already owed and on its way. Asking for a second
		// one would draw the same state twice, so this change waits for
		// the one that is coming.
		if m.liveRepaintDue {
			return m, wait
		}

		m.liveRepaintDue = true

		return m, tea.Batch(wait, liveRepaintDelayCmd(delay))
	}

	return m.drawLive(wait)
}

// updateLiveRepaintDue draws the screen with the changes that were folded
// into the redraw.
func (m Model) updateLiveRepaintDue(liveRepaintDueMsg) (tea.Model, tea.Cmd) {
	if m.live == nil {
		return m, nil
	}

	m.liveRepaintDue = false

	return m.drawLive(nil)
}

// liveRepaintDelay returns how long the redraw of a live change has to
// wait, or zero when it may be drawn now.
func (m Model) liveRepaintDelay() time.Duration {
	if m.livePaintedAt.IsZero() {
		return 0
	}

	elapsed := m.clock()().Sub(m.livePaintedAt)
	if elapsed >= liveRepaintInterval {
		return 0
	}

	return liveRepaintInterval - elapsed
}

// drawLive reads the live state into the model and asks for the screen to
// be drawn again.
//
// The whole screen is drawn rather than the rows that changed. A row of a
// chat list is two rows of words and the air inside them, and a frame that
// only repainted the rows whose text moved would leave the time and the
// badge of a row above a chat that is not there any more — see repaint.go,
// which is about the same promise.
func (m Model) drawLive(wait tea.Cmd) (tea.Model, tea.Cmd) {
	m.livePaintedAt = m.clock()()
	m.liveRepaintDue = false

	m = m.applyLiveChats(m.live.Chats())
	m, reload := m.applyLiveMessageEvents()

	return m, withRepaint(m, tea.Batch(wait, reload))
}

// applyLiveChats puts the live list on the screen.
//
// An empty live list is not an empty chat list. The store is filled by
// loadChats, and until TDLib has sent the first update of the list it
// holds nothing at all: a list that was drawn from it then would be empty
// while the list the program loaded at startup is on the screen, and the
// two would take turns. An account whose every chat has left the main list
// keeps the rows it had, which is the smaller of the two mistakes: `R` loads
// the list again and says what is there.
func (m Model) applyLiveChats(live []LiveChat) Model {
	if len(live) == 0 {
		return m
	}

	// The row the cursor is on is remembered before the list moves under
	// it, and the window is put back afterwards so that the chat the user
	// is reading stays on the row they were reading it on.
	selected := m.selectedChatID()
	row := m.selectedListRow()

	m.chats = safeChats(mergeLiveChats(m.chats, live, m.openedChat))
	m.chatsState = loadStateLoaded

	if index, found := m.indexOfChat(selected); found {
		m.selectedChat = index
		m.chatListOffset = m.chatListOffsetKeepingRow(m.selectedListRow(), row)
	} else if m.selectedChat >= len(m.chats) {
		m.selectedChat = len(m.chats) - 1
	}

	// A search that is open narrows whatever arrived, and the cursor goes
	// to the first of it, for the reason it does on a loaded list: the
	// list on the screen is a different list now.
	if m.chatSearch.open {
		m.selectFirstChatMatch()
	}

	return m
}

// mergeLiveChats folds a live list into the rows the model holds.
//
// The row a chat already has keeps everything the live state does not
// carry: the messages of the conversation behind it, the other names it is
// found by in the search, where Telegram had been told the reader had got
// to, and the kind of the chat — the store knows that a chat has more than
// one person in it, and not whether it is a channel. What TDLib keeps
// updating is what the live list brings: the name, the unread count, the
// preview and the moment of the last message.
//
// The open conversation keeps its row even when the chat has left the main
// list, for the reason mergeLoadedChats keeps it: the conversation on the
// screen is drawn from that row, and a live update that dropped it would
// empty a conversation nobody asked to close.
func mergeLiveChats(previous []Chat, live []LiveChat, openChat int64) []Chat {
	byID := make(map[int64]Chat, len(previous))
	for _, chat := range previous {
		byID[chat.ID] = chat
	}

	merged := make([]Chat, 0, len(live))
	for _, row := range live {
		was, known := byID[row.ID]
		merged = append(merged, liveChatRow(was, known, row))
	}

	if openChat == 0 {
		return merged
	}
	if _, listed := byID[openChat]; !listed {
		return merged
	}
	for _, chat := range merged {
		if chat.ID == openChat {
			return merged
		}
	}

	return append(merged, byID[openChat])
}

// liveChatRow returns the row of a chat the live list brought.
//
// known says whether the model held a row of this chat already. It is what
// decides the kind of the row: a chat the loaded list already knew is
// drawn with the kind that list was told by TDLib — a channel from a group
// — and a chat that arrives with a live update takes the kind the store
// knows, which says that a chat has more than one person in it and nothing
// more.
func liveChatRow(was Chat, known bool, live LiveChat) Chat {
	chat := was
	chat.ID = live.ID
	chat.Title = live.Title
	chat.Unread = live.Unread
	chat.Preview = live.Preview
	chat.At = live.At

	if !known {
		chat.Kind = live.Kind
	}

	return chat
}

// selectedListRow returns the row of the chat under the cursor among the
// rows the list draws, or -1 where the chat under the cursor is not one of
// them — which is what a query that found nothing leaves.
func (m Model) selectedListRow() int {
	for position, entry := range m.chatListEntries() {
		if entry.index == m.selectedChat {
			return position
		}
	}

	return -1
}

// chatListOffsetKeepingRow returns the window offset that puts the chat now
// on row where it was on row wasRow.
//
// It is the whole of what "the screen does not jump" means for the list: a
// chat that arrived at the top pushes every row below it down by one, and
// the offset moves down by one with it, so the chat under the cursor is
// still on the row the user was reading. The offset is clamped to the rows
// there are, and a window that cannot hold the chat in it takes the window
// of §10.5 for that chat instead.
func (m Model) chatListOffsetKeepingRow(row, wasRow int) int {
	if row < 0 || wasRow < 0 {
		return m.chatListOffset
	}

	layout := LayoutFor(m.width, m.height)
	available := m.chatListVisibleRows(layout)
	total := len(m.chatListEntries())

	return minInt(maxInt(row-wasRow, 0), maxInt(total-available, 0))
}

// liveCursor returns the message event cursor of a chat.
func (m Model) liveCursor(chatID int64) uint64 {
	return m.liveCursors[chatID]
}

// setLiveCursor remembers the cursor to ask a chat's events with next time.
func (m *Model) setLiveCursor(chatID int64, cursor uint64) {
	if m.liveCursors == nil {
		m.liveCursors = map[int64]uint64{}
	}

	m.liveCursors[chatID] = cursor
}

// applyLiveMessageEvents reads the message events of the open conversation
// and puts them in.
//
// Only the chat that is on the screen is read: the window of every other
// chat keeps its events until that chat is opened, which is what the store
// is for and what a user scrolling a busy channel is not asking for.
func (m Model) applyLiveMessageEvents() (Model, tea.Cmd) {
	if m.live == nil || m.screen != ScreenConversation {
		return m, nil
	}

	chatID := m.selectedChatID()
	if chatID == 0 {
		return m, nil
	}

	events := m.live.MessageEvents(chatID, m.liveCursor(chatID))
	m.setLiveCursor(chatID, events.Cursor)

	if events.Resync {
		return m.reloadLiveChat()
	}

	return m.applyLiveEvents(events.Events), nil
}

// applyLiveEvents puts the events of a chat into the conversation under the
// cursor.
//
// Whether the reader is at the newest message is asked before the first
// message goes in, for the reason updateMessageSent asks it: a message that
// has just arrived is the newest thing in the conversation, and a reader at
// the end of it is watching for that. A reader who has scrolled up to read
// something older is left exactly where they are, and the line at the
// bottom of the feed says what they are missing.
func (m Model) applyLiveEvents(events []LiveMessageEvent) Model {
	if len(events) == 0 ||
		m.selectedChat < 0 || m.selectedChat >= len(m.chats) {
		return m
	}

	following := m.timelineFollowsNewest()
	added := 0
	for _, event := range events {
		if m.applyLiveEvent(event) {
			added++
		}
	}

	if added == 0 {
		return m.normalizeTimeline()
	}

	if following {
		return m.scrollToNewest()
	}

	m.newBelow += added

	return m
}

// applyLiveEvent applies one event to the open conversation and reports
// whether it put a new message into it.
//
// The text of a message crosses into the model the same way the history
// does: it is the only text anybody else in a chat can put on the screen,
// and the cleaner of screen_text.go is what keeps a terminal from acting on
// it (#53).
func (m *Model) applyLiveEvent(event LiveMessageEvent) bool {
	messages := m.chats[m.selectedChat].Messages

	switch event.Kind {
	case LiveMessageAdded:
		// The same message can be seen twice: through the answer to a
		// send and through updateNewMessage, or through two windows of
		// events. A row drawn twice is a message a reader has to read
		// twice, so the second copy replaces the first and the row is not
		// a new one.
		fresh := !chatHasMessage(messages, event.Message.ID)
		m.chats[m.selectedChat].Messages = appendMessageByID(
			messages, safeMessage(event.Message),
		)

		return fresh

	case LiveMessageReplaced:
		m.chats[m.selectedChat].Messages = replaceMessageByID(
			messages, event.OldID, safeMessage(event.Message),
		)

		return false

	case LiveMessageDeleted:
		m.chats[m.selectedChat].Messages = deleteMessagesByID(
			messages, event.IDs,
		)

		return false

	default:
		return false
	}
}

// reloadLiveChat asks for the first page of the open conversation again.
//
// It is what a window of events that has moved past what the interface
// holds costs (ADR-0003): the conversation is read again exactly as it is
// on opening, and the messages it had are merged rather than thrown away,
// so the pages a reader has scrolled back through stay on the screen.
func (m Model) reloadLiveChat() (Model, tea.Cmd) {
	chatID := m.selectedChatID()
	if chatID == 0 || m.source == nil {
		return m, nil
	}

	m.historyRefresh = true
	m.historyState = loadStateLoading
	m.historyHasMore = true
	m.historyExhausted = false
	m.historyMoreLoading = false
	m.historyFillRequests = 0
	m.historyFillMessages = 0

	return m, loadHistoryCmd(
		m.source, chatID, 0, historyPageSize, m.historyOperation,
	)
}

// chatHasMessage reports whether a conversation already holds a message.
func chatHasMessage(messages []Message, id int64) bool {
	for _, message := range messages {
		if message.ID == id {
			return true
		}
	}

	return false
}

// replaceMessageByID puts a message where the one it replaces was, or at
// the end when the row it replaces is not there.
//
// It is a send that Telegram has finished with: the outgoing message was
// drawn with the temporary identifier it was given, and the same row is
// drawn again under its final one. A row that appears at the bottom
// instead would be a message the reader has read once already, read twice.
func replaceMessageByID(messages []Message, oldID int64, message Message) []Message {
	for index, current := range messages {
		if current.ID != oldID {
			continue
		}

		replaced := make([]Message, len(messages))
		copy(replaced, messages)
		replaced[index] = message

		return replaced
	}

	return appendMessageByID(messages, message)
}

// deleteMessagesByID drops the messages of a set of identifiers from a
// conversation.
func deleteMessagesByID(messages []Message, ids []int64) []Message {
	if len(ids) == 0 || len(messages) == 0 {
		return messages
	}

	gone := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		gone[id] = struct{}{}
	}

	kept := make([]Message, 0, len(messages))
	for _, message := range messages {
		if _, deleted := gone[message.ID]; deleted {
			continue
		}
		kept = append(kept, message)
	}

	return kept
}

// newMessagesLineCount returns the rows the line of new messages takes, and
// none at all when there is nothing to say.
func (m Model) newMessagesLineCount(layout Layout, width int) int {
	if !m.newBelowShown(layout, width) {
		return 0
	}

	return 1
}

// newBelowShown reports whether the line at the bottom of the feed is drawn:
// there are messages below the window, and the window does not end with
// them.
//
// The second half is what keeps the screen from contradicting itself. A
// conversation short enough to hold a message that arrived above the bottom
// of the feed is one where the reader can see what arrived, and a line
// pointing at it says there is something to go and look at when they are
// already looking at it.
func (m Model) newBelowShown(layout Layout, width int) bool {
	if m.newBelow <= 0 {
		return false
	}

	return !m.newestMessageDrawn(layout, width)
}

// newestMessageDrawn reports whether the newest message of the conversation
// is one of the rows the feed is drawing.
//
// It is asked of the walk the view itself draws the window with, so the two
// cannot disagree about where the window ends: the last entry of the walk is
// the last message on the screen, whatever the cursor is on.
func (m Model) newestMessageDrawn(layout Layout, width int) bool {
	total := m.timelineTotal()
	if total == 0 {
		return true
	}

	entries := m.feedEntries()
	top := entryIndexOfFeed(entries, clampIndex(m.timelineTop, total-1))
	_, drawn := m.entryRowsFrom(
		entries,
		top,
		layout,
		width,
		m.historyFeedRowsBase(layout, width),
		m.styles(),
		m.timelineCut,
	)
	last := entryIndexOfFeed(entries, total-1)

	return len(drawn) > last-top
}

// newMessagesLines is the line at the bottom of the feed that says what
// arrived below the window.
//
// It is a line of the feed rather than a row of the chat list because it is
// about the conversation and not about the account: the chat it belongs to
// is on the other side of the screen, and the number under the list would be
// a number about something else.
//
// It is drawn in the accent of the theme because it is the one thing on the
// screen a user can act on without a key: the key that takes them there is
// `G`, and a line in the dim step of the text ramp is a line nobody looks
// for.
func (m Model) newMessagesLines(layout Layout, width int) []string {
	if !m.newBelowShown(layout, width) {
		return nil
	}

	text := m.widths.Fit(newMessagesText(m.newBelow), width, ellipsis)

	return []string{m.styles().dimmed(m.tokens().StatusActive).Render(text)}
}

// newMessagesText is what the line at the bottom of the feed says.
//
// It is a count and not a word: a reader who has scrolled up to read
// something older is owed the number of what they are missing before they
// are owed the words of it.
func newMessagesText(count int) string {
	if count == 1 {
		return newMessagesMarker + " 1 new message"
	}

	return newMessagesMarker + " " + strconv.Itoa(count) + " new messages"
}

// newMessagesMarker is what the line begins with: the arrow of the key that
// takes the reader to the newest message (`G`, and End).
//
// It is the down arrow rather than a word because it is the same arrow as
// the one on the key, and a reader who is looking for `G` recognises it
// before they read anything.
const newMessagesMarker = "↓"

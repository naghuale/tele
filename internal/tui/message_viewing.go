package tui

import (
	"context"
	"fmt"
	"io"
	"slices"

	tea "github.com/charmbracelet/bubbletea"
)

// What is on the screen of an open chat has been read.
//
// TDLib does not know what a person is looking at. It knows which chat has
// been opened and how far the read pointer of that chat has moved, so the
// interface says which messages are in the window and the other side is
// told what was seen.
//
// Without it the owner reads a chat that stays unread for as long as the
// program runs: the counter in the list never falls, and the people on the
// other side of the conversation are told nothing.

// MessageViewer is told which messages of a chat are on the screen.
//
// The calls are best effort from the interface's point of view. A chat
// whose messages could not be marked read still shows them, and a count
// that does not fall is a count TDLib reports. A cause goes to the
// diagnostic stream and never to the screen, because a TDLib error message
// is not interface text (§11.3, §19).
type MessageViewer interface {
	ViewMessages(ctx context.Context, chatID int64, messageIDs []int64) error
}

// messagesViewedMsg is delivered by the command of
// markVisibleMessagesViewed.
//
// It carries the chat the read was about and whether TDLib took it, which
// are the two things the model has to know: the chat whose row is to be read
// again, and whether there is a read to answer at all.
type messagesViewedMsg struct {
	chatID int64
	err    error
}

// markVisibleMessagesViewed returns the command that tells Telegram which
// messages of the open chat are on the screen.
//
// It asks nothing unless there is something to ask: the chat list is not a
// chat anybody is reading, a chat with no messages on the screen has no
// window, and a window that has already been marked read is marked read.
func (m *Model) markVisibleMessagesViewed() tea.Cmd {
	if m == nil || m.messageViewer == nil {
		return nil
	}

	// The conversation being the screen is the whole of "the user is
	// looking at it". On the chat list the messages are not on the screen
	// at all, and on the authorization screen there is no chat.
	if m.screen != ScreenConversation {
		return nil
	}
	if m.selectedChat < 0 || m.selectedChat >= len(m.chats) {
		return nil
	}

	chatID := m.chats[m.selectedChat].ID
	ids := m.visibleMessageIDs()
	if chatID == 0 || len(ids) == 0 {
		return nil
	}

	// The same window twice is a read that says nothing, and a round trip
	// to TDLib for it. A reader who scrolls up and back again gets nothing
	// on the wire either, because the window is the one that was marked.
	if m.viewedChat == chatID && slices.Equal(m.viewedIDs, ids) {
		return nil
	}

	m.viewedChat, m.viewedIDs = chatID, ids

	viewer := m.messageViewer
	diagnostics := m.diagnostics
	ctx := m.ctx
	if ctx == nil {
		ctx = context.Background()
	}

	return func() tea.Msg {
		err := viewer.ViewMessages(ctx, chatID, ids)
		if err != nil {
			reportMessageViewCause(diagnostics, chatID, err)
		}

		return messagesViewedMsg{chatID: chatID, err: err}
	}
}

// visibleMessageIDs returns the identifiers of the messages the
// conversation is drawing right now, oldest first.
//
// It is asked of the walk the view itself draws the window with, so the two
// cannot disagree about where the window ends: a message cut in half at the
// top of the feed is on the screen, and a message a page older is not, and
// only that walk can tell the two apart. A second walk of its own would be
// a second answer to the same question, and the day the two disagree the
// owner is told a message was read that was never on the screen.
//
// A message of the queue is not among them: it has no identifier in
// Telegram yet, and there is nothing there to read.
func (m Model) visibleMessageIDs() []int64 {
	total := m.timelineTotal()
	if total == 0 {
		return nil
	}

	layout := LayoutFor(m.width, m.height)
	width := layout.ChatContentWidth()
	entries := m.feedEntries()
	top := entryIndexOfFeed(entries, clampIndex(m.timelineTop, total-1))
	_, drawn := m.entryRowsFrom(
		entries,
		top,
		layout,
		width,
		m.historyFeedRows(layout, width),
		m.styles(),
		m.timelineCut,
	)
	if len(drawn) == 0 {
		return nil
	}

	// The indices of an entry are the conversation's, and the history is
	// walked beside the entries: a row of the queue standing in the middle
	// of a conversation has no message of the history behind it, so the
	// index of a history message cannot be read off the index of an entry.
	messages := m.selected().Messages
	ids := make([]int64, 0, len(drawn))
	at := 0
	for index, entry := range entries {
		parts := historyParts(entry)
		if parts > 0 && index >= top && index < top+len(drawn) {
			for part := at; part < at+parts && part < len(messages); part++ {
				ids = append(ids, messages[part].ID)
			}
		}
		at += parts
	}

	if len(ids) == 0 {
		return nil
	}

	return ids
}

// historyParts returns how many messages of the history an entry stands
// for, which is none at all for a message of the queue.
func historyParts(entry timelineEntry) int {
	if entry.isPending() {
		return 0
	}

	return entry.count()
}

// updateMessagesViewed asks for the chat list again after a read.
//
// TDLib answers a read with updateChatReadInbox, and the counter of a row
// is TDLib's number: a row that still shows the count it showed when the
// chat was opened says a chat is unread after it has been read. The list is
// therefore read again — and only when the row has something to lose,
// because a chat already at zero is not a list that needs reading.
//
// The rows a source answers with carry no messages, so a list that replaced
// them would empty the conversation the owner is reading. The messages a
// chat had are kept across the read (see mergeLoadedChats).
func (m Model) updateMessagesViewed(msg messagesViewedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		// The cause went to the diagnostics when the call failed, and a read
		// that did not happen is not a reason to read the whole list.
		return m, nil
	}
	if !m.chatHasUnread(msg.chatID) {
		return m, nil
	}

	return m, m.refreshChatList()
}

// chatHasUnread reports whether the row of a chat has a count to lose.
func (m Model) chatHasUnread(chatID int64) bool {
	for _, chat := range m.chats {
		if chat.ID == chatID {
			return chat.Unread > 0
		}
	}

	return false
}

// refreshChatList asks the source for the chat list again without
// announcing a wait.
//
// The list is already on the screen — a two-pane screen is drawing it
// beside the very conversation that caused the read — so a load would put a
// placeholder over a chat the owner is in the middle of reading. The load
// state is therefore left alone, and the rows that arrive replace the ones
// on the screen without anything saying "wait".
func (m *Model) refreshChatList() tea.Cmd {
	if m == nil || m.source == nil {
		return nil
	}
	if m.chatsState == loadStateIdle || m.chatsState == loadStateLoading {
		return nil
	}

	return listChatsCmd(m.source)
}

// reportMessageViewCause writes the cause of a read that did not happen.
//
// The chat identifier is safe to name: it is an integer TDLib assigned, and
// it is what makes a line in a log useful. The cause is not safe - it can
// carry a TDLib error message - and it is written to a log and not to a
// screen.
func reportMessageViewCause(diagnostics io.Writer, chatID int64, err error) {
	if diagnostics == nil {
		return
	}

	fmt.Fprintf(diagnostics, "view messages of chat %d: %v\n", chatID, err)
}

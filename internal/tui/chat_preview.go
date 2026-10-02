package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// This file is the preview: the chat under the cursor is shown in the pane
// beside the list before it is opened.
//
// The owner's words (02.10): «где я выбираю чат, разговора нет, надо жать
// Enter». The pane beside the list was the empty state of §17 until Enter,
// and a person walking a list with the arrows had to press a key for every
// chat to find out whether there was anything to read in it.
//
// Three things the preview is not, and each of them is a rule rather than a
// detail:
//
//   - it is not a read. Telegram is told which messages are on the screen
//     only for a chat that was opened on purpose (§8.2), so a preview
//     leaves the read pointer of the chat where it was. Nothing in this
//     file calls ViewMessages, and markVisibleMessagesViewed asks nothing
//     on the chat list screen for the same reason — the preview is shown
//     on that screen;
//   - it is not an opening. The chat is not opened in TDLib, so presence is
//     not counted and the composer of the open conversation does not move;
//     the keys stay in the list, which is what makes Tab and Enter the
//     difference between reading and writing;
//   - it is not a load per key. The pane is filled after the cursor has
//     been still for chatPreviewPause, so holding ↓ through fifty chats
//     asks for one page, and the last selection wins: an answer for a chat
//     the cursor has already left is dropped rather than painted.

// chatPreviewPause is how long the cursor has to be still on a chat before
// its conversation is shown in the pane beside the list.
//
// It is long enough to be one pause rather than fifty: a person walking a
// list presses ↓ far faster than once in 200 ms, and each of those presses
// would be a history load of fifty messages and a repaint nobody reads. It
// is short enough that a person who has stopped is not left looking at an
// empty pane waiting for a decision to be made.
const chatPreviewPause = 200 * time.Millisecond

// chatPreviewDueMsg is delivered when the pause of a chat under the cursor
// has run out.
//
// It carries the chat and the place of the cursor, and both are checked on
// arrival: a pause that was armed for a chat the cursor has since left is a
// pause about nothing, and the last selection is the one that is on screen.
type chatPreviewDueMsg struct {
	chatID    int64
	selection int
}

// chatPreviewLoadedMsg is delivered by loadPreviewCmd.
//
// It is a message of its own and not a historyLoadedMsg with a flag on it,
// because the two have to stay apart: the page of a preview goes into the
// pane beside the list and nothing else, while the page of an open
// conversation moves the cursor and fills the feed with older messages.
type chatPreviewLoadedMsg struct {
	chatID    int64
	operation uint64
	page      HistoryPage
	err       error
}

// armChatPreview returns the command that shows the chat under the cursor
// after the pause, and forgets the preview that is on the screen.
//
// Forgetting comes first and is not a detail: what the pane shows is about
// the chat that was under the cursor a moment ago, and a pane that keeps
// showing a conversation nobody has the cursor on is a pane that answers a
// question nobody asked.
func (m *Model) armChatPreview() tea.Cmd {
	m.chatPreviewChat = 0

	if !m.chatPreviewPossible() {
		return nil
	}

	chatID := m.selectedChatID()
	if chatID == 0 {
		return nil
	}

	selection := m.selectedChat

	return tea.Tick(chatPreviewPause, func(time.Time) tea.Msg {
		return chatPreviewDueMsg{chatID: chatID, selection: selection}
	})
}

// chatPreviewPossible reports whether there is a pane beside the list to
// preview a chat in.
//
// A chat list on its own has nowhere to show it: the preview is drawn in the
// conversation pane, and a narrow screen has no conversation pane while the
// list is on the screen.
func (m Model) chatPreviewPossible() bool {
	return m.screen == ScreenChats &&
		m.selectedChat >= 0 &&
		m.selectedChat < len(m.chats) &&
		LayoutFor(m.width, m.height).TwoPane()
}

// chatPreviewShown reports whether the pane beside the list is showing the
// chat under the cursor.
//
// It asks about the chat under the cursor as well as the preview: a chat
// the cursor has moved off of is not what is in the pane, whatever the pane
// was asked for a moment ago.
func (m Model) chatPreviewShown() bool {
	if !m.chatPreviewPossible() {
		return false
	}

	return m.chatPreviewChat != 0 && m.chatPreviewChat == m.selectedChatID()
}

// updateChatPreviewDue applies the pause that has run out.
func (m Model) updateChatPreviewDue(
	msg chatPreviewDueMsg,
) (tea.Model, tea.Cmd) {
	// The cursor moved on while the pause ran, or the list is not beside a
	// pane any more: the answer would be about a chat nobody is looking at.
	if !m.chatPreviewPossible() ||
		m.selectedChat != msg.selection ||
		m.selectedChatID() != msg.chatID {
		return m, nil
	}

	m.chatPreviewChat = msg.chatID

	// A model built without Telegram holds the messages of its chats in the
	// list, so there is nothing to ask for: the pause was the wait, and the
	// pane is drawn from what the list already carries.
	if m.source == nil {
		return m, nil
	}

	// One page, and no fill above it. The preview is a look at the end of a
	// conversation and the feed holds one screenful of it; a walk through
	// older pages would be a second answer to a question the pane did not
	// ask.
	m.historyState = loadStateLoading
	m.previewOperation++

	return m, loadPreviewCmd(m.source, msg.chatID, historyPageSize, m.previewOperation)
}

// updateChatPreviewLoaded puts the page of a preview into the pane.
func (m Model) updateChatPreviewLoaded(
	msg chatPreviewLoadedMsg,
) (tea.Model, tea.Cmd) {
	// The chat the cursor moved to has asked for its own page, the chat was
	// opened while this one was on its way, or the list has no pane beside
	// it: the answer is about nothing on the screen and is dropped. This is
	// where "the latest selection wins" is enforced.
	if msg.operation != m.previewOperation ||
		!m.chatPreviewShown() ||
		m.selectedChatID() != msg.chatID {
		return m, nil
	}

	if msg.err != nil {
		// The cause goes to the diagnostic stream and the pane says what
		// happened in words that are true (§11.3, §19).
		m.historyState = loadStateError
		m.reportDiagnostic("preview of chat %d: %v\n", msg.chatID, msg.err)

		return m, nil
	}

	page := chronological(safeMessages(msg.page.Messages))
	m.chats[m.selectedChat].Messages = page
	if len(page) == 0 {
		m.historyState = loadStateEmpty
	} else {
		m.historyState = loadStateLoaded
	}

	// The window follows the page, and the pane is drawn bottom-anchored:
	// the newest message of a chat is the one a glance is about (§8.3).
	return m.scrollToNewest(), nil
}

// loadPreviewCmd asks for the newest page of the chat the pane is about to
// show.
//
// It is the history load of a chat with the answer marked as a preview's,
// and it asks for the first page (fromMessageID zero): the end of the
// conversation is what a glance at a chat is about.
func loadPreviewCmd(
	src ChatSource,
	chatID int64,
	limit int,
	operation uint64,
) tea.Cmd {
	history := loadHistoryCmd(src, chatID, 0, limit, operation)

	return func() tea.Msg {
		loaded, isHistory := history().(historyLoadedMsg)
		if !isHistory {
			return nil
		}

		return chatPreviewLoadedMsg{
			chatID:    loaded.chatID,
			operation: loaded.operation,
			page:      loaded.page,
			err:       loaded.err,
		}
	}
}

// previewHint is what the foot of a preview says.
//
// It says two things: that the pane is a preview, and what opens the chat
// for real. A foot that said only the second would be a conversation with
// no field under it and no word about why — the two things the owner named
// on 02.10, one after the other.
const previewHint = "Preview · Enter or Tab to open"

// previewFooterLines returns the rows of the foot of a preview, which is
// none on a screen too short for a hint bar (§3.4).
func (m Model) previewFooterLines(layout Layout, width int) []string {
	if layout.HideHints() {
		return nil
	}

	styles := m.styles()

	return []string{styles.dimmed(m.tokens().SecondaryText).
		Render(m.widths.Fit(previewHint, width, ellipsis))}
}

// previewFooterRows returns how many rows the foot of a preview takes.
func (m Model) previewFooterRows(layout Layout, width int) int {
	return len(m.previewFooterLines(layout, width))
}

// previewPaneRegion draws the preview of the chat under the cursor: the
// header of the conversation, the messages, and the foot that says what it
// is.
//
// There is no composer under them and no reason for one: the keys are in the
// list, and a field under a conversation with the focus elsewhere is a field
// that swallows nothing and promises a send that nothing will send.
func (m Model) previewPaneRegion(layout Layout) string {
	width := layout.ChatContentWidth()
	footer := m.previewFooterRegion(layout, width)

	return m.joinRegions(
		m.conversationRegion(layout, width, layout.Height-lineCount(footer)),
		footer,
	)
}

// previewFooterRegion draws the foot of a preview.
func (m Model) previewFooterRegion(layout Layout, width int) string {
	lines := m.previewFooterLines(layout, width)
	if len(lines) == 0 {
		return ""
	}

	return m.renderRegion(m.styles().footer, width, lines, len(lines))
}

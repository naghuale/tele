package tui

import (
	"context"
	"fmt"
	"io"

	tea "github.com/charmbracelet/bubbletea"
)

// TDLib counts the online members of a chat only while the chat is open,
// so the interface says which chat the user is looking at and stops saying
// so when they stop looking at it.
//
// A chat that is left open keeps being counted and updated for somebody
// who is not there, and a chat that is never opened is never counted at
// all: the presence of a group would be missing for as long as the program
// runs.

// ChatPresenceOpener is told which chat the user is looking at.
//
// The two calls are best effort from the interface's point of view. A chat
// that cannot be opened still shows its messages, and a presence that does
// not arrive is a presence the header does not draw. A cause goes to the
// diagnostic stream and never to the screen, because a TDLib error message
// is not interface text (§11.3, §19).
type ChatPresenceOpener interface {
	OpenChat(ctx context.Context, chatID int64) error
	CloseChat(ctx context.Context, chatID int64) error
}

// chatOpenedMsg is delivered by openChatCmd.
//
// It is what tells the model that the chat is open, which is a question the
// model cannot answer by having sent the call: a read of the messages of a
// chat TDLib has not loaded yet is refused, and a broadcast chat is one
// TDLib loads by openChat (see markVisibleMessagesViewed).
type chatOpenedMsg struct {
	chatID int64
	err    error
}

// switchConversationChat tells TDLib about the chat the user is looking at
// now, and about the one they were looking at before.
//
// It is one command rather than two so that the two calls keep their order:
// the new chat is opened after the old one is closed, and TDLib is never
// asked to close a chat it has not been told to open.
func (m *Model) switchConversationChat(chatID int64) tea.Cmd {
	if m == nil || m.presenceOpener == nil {
		return nil
	}

	previous := m.openedChat
	if previous == chatID {
		return nil
	}

	// The chat is remembered as open even when the call fails: the opener
	// is told once, and a failure is a presence that does not arrive rather
	// than a reason to ask again on every key.
	m.openedChat = chatID

	// Another chat is another chat: nothing has answered for this one yet,
	// so nothing of it may be read until openChat has answered.
	if chatID != previous {
		m.openedAck = 0
	}

	return m.switchChatCmd(previous, chatID)
}

// switchChatCmd closes the chat that was open and opens the one that is, in
// that order and in one command.
//
// It is a command and not a batch of two because a batch promises no order at
// all: Bubble Tea runs the members of a batch in goroutines of their own, so
// TDLib can be asked to open the new chat before, or beside, the close of the
// old one. The two calls then overlap, and TDLib counts a chat for as long as
// it is open — the presence of the chat that was left keeps arriving under the
// header of the one that was opened, and a broadcast chat, which TDLib loads
// by openChat (see markVisibleMessagesViewed), is read by the call that came
// out of order.
//
// The close is asked for even when the open is refused afterwards: the two are
// best effort (see ChatPresenceOpener), and a chat the user has left is closed
// whether or not the next one opened.
func (m *Model) switchChatCmd(previous, chatID int64) tea.Cmd {
	var closeCmd, openCmd tea.Cmd
	if previous != 0 {
		closeCmd = m.closeChatCmd(previous)
	}
	if chatID != 0 {
		openCmd = m.openChatCmd(chatID)
	}
	if closeCmd == nil && openCmd == nil {
		return nil
	}

	return func() tea.Msg {
		if closeCmd != nil {
			// The close answers nothing: nothing on the screen waits for
			// it, and only the open has an answer to deliver.
			closeCmd()
		}
		if openCmd == nil {
			return nil
		}

		return openCmd()
	}
}

// openChatCmd asks TDLib to open a chat.
func (m *Model) openChatCmd(chatID int64) tea.Cmd {
	opener := m.presenceOpener
	diagnostics := m.diagnostics
	ctx := m.ctx
	if ctx == nil {
		ctx = context.Background()
	}

	return func() tea.Msg {
		err := opener.OpenChat(ctx, chatID)
		if err != nil {
			reportChatLifecycleCause(diagnostics, "open", chatID, err)
		}

		return chatOpenedMsg{chatID: chatID, err: err}
	}
}

// closeChatCmd asks TDLib to close a chat.
func (m *Model) closeChatCmd(chatID int64) tea.Cmd {
	opener := m.presenceOpener
	diagnostics := m.diagnostics
	ctx := m.ctx
	if ctx == nil {
		ctx = context.Background()
	}

	return func() tea.Msg {
		if err := opener.CloseChat(ctx, chatID); err != nil {
			reportChatLifecycleCause(diagnostics, "close", chatID, err)
		}

		return nil
	}
}

// reportChatLifecycleCause writes a cause of a failed open or close.
//
// The chat identifier is safe to name: it is an integer TDLib assigned, and
// it is what makes a line in a log useful. The cause is not safe - it can
// carry a TDLib error message - and it is written to a log and not to a
// screen.
func reportChatLifecycleCause(
	diagnostics io.Writer,
	action string,
	chatID int64,
	err error,
) {
	if diagnostics == nil {
		return
	}

	fmt.Fprintf(diagnostics, "%s chat %d: %v\n", action, chatID, err)
}

// closeConversationChat is the last call before the program leaves the
// conversation: a chat that is still open when the program quits is closed
// on the way out, so TDLib is not left counting a chat for a process that
// has stopped.
func (m *Model) closeConversationChat() tea.Cmd {
	if m == nil || m.presenceOpener == nil || m.openedChat == 0 {
		return nil
	}

	cmd := m.closeChatCmd(m.openedChat)
	m.openedChat = 0
	// Nothing has answered for a chat that is not open, and nothing may be
	// read in a chat that was closed.
	m.openedAck = 0

	return cmd
}

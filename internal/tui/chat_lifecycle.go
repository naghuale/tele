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

	var cmds []tea.Cmd
	if previous != 0 {
		cmds = append(cmds, m.closeChatCmd(previous))
	}
	if chatID != 0 {
		cmds = append(cmds, m.openChatCmd(chatID))
	}

	// The chat is remembered as open even when the call fails: the opener
	// is told once, and a failure is a presence that does not arrive rather
	// than a reason to ask again on every key.
	m.openedChat = chatID

	if len(cmds) == 0 {
		return nil
	}

	return tea.Batch(cmds...)
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
		if err := opener.OpenChat(ctx, chatID); err != nil {
			reportChatLifecycleCause(diagnostics, "open", chatID, err)
		}

		return nil
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

	return cmd
}

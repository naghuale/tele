package tui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"
)

// HistoryPage is one page of a chat's message history, as delivered to
// the TUI by a ChatSource.
//
// NextFrom is an inclusive boundary message ID from the underlying
// TDLib page. Code that concatenates multiple pages must de-duplicate
// messages by ID.
//
// PR-06D.1 loads only the first page.
type HistoryPage struct {
	Messages []Message
	NextFrom int64
	HasMore  bool
}

// ChatSource is the TUI-facing data provider.
//
// Implementations live outside the tui package. The tui package must
// not import internal/telegram directly: Telegram-specific types stay
// behind this interface.
//
// A nil ChatSource keeps the model in mock-only mode, exactly as in
// PR-03.
type ChatSource interface {
	ListChats(ctx context.Context) ([]Chat, error)
	LoadHistory(ctx context.Context, chatID int64, fromMessageID int64, limit int) (HistoryPage, error)
	SendMessage(ctx context.Context, chatID int64, text string) (Message, error)
}

// loadState is the loading state of an asynchronous projection.
type loadState uint8

const (
	// loadStateIdle means no async load has been requested yet.
	loadStateIdle loadState = iota
	// loadStateLoading means a command is in flight.
	loadStateLoading
	// loadStateLoaded means the projection is present.
	loadStateLoaded
	// loadStateEmpty means the load succeeded with no items.
	loadStateEmpty
	// loadStateError means the load failed.
	loadStateError
)

// String returns a stable lowercase name for diagnostics.
func (s loadState) String() string {
	switch s {
	case loadStateIdle:
		return "idle"
	case loadStateLoading:
		return "loading"
	case loadStateLoaded:
		return "loaded"
	case loadStateEmpty:
		return "empty"
	case loadStateError:
		return "error"
	default:
		return "unknown"
	}
}

// chatsLoadedMsg is delivered by listChatsCmd.
type chatsLoadedMsg struct {
	chats []Chat
	err   error
}

// historyLoadedMsg is delivered by loadHistoryCmd.
type historyLoadedMsg struct {
	chatID int64
	page   HistoryPage
	err    error
}

// messageSentMsg is delivered by sendMessageCmd.
//
// chatID identifies the target chat.
//
// operation identifies the specific send attempt. It protects against
// a late result for the same chat being applied to a newer model state
// (for example after the user left and re-entered the chat).
type messageSentMsg struct {
	chatID    int64
	operation uint64
	message   Message
	err       error
}

// listChatsCmd returns a command that loads the chat list through src.
//
// The command runs in its own goroutine and must not touch Model.
func listChatsCmd(src ChatSource) tea.Cmd {
	return func() tea.Msg {
		chats, err := src.ListChats(context.Background())
		if err != nil {
			return chatsLoadedMsg{err: err}
		}
		return chatsLoadedMsg{chats: chats}
	}
}

// loadHistoryCmd returns a command that loads the first history page
// through src.
func loadHistoryCmd(src ChatSource, chatID int64, limit int) tea.Cmd {
	return func() tea.Msg {
		page, err := src.LoadHistory(context.Background(), chatID, 0, limit)
		if err != nil {
			return historyLoadedMsg{chatID: chatID, err: err}
		}
		return historyLoadedMsg{chatID: chatID, page: page}
	}
}

// sendMessageCmd returns a command that sends text to chatID through
// src.
//
// operation is passed back on the resulting messageSentMsg so the model
// can discard results from a send attempt that has been superseded.
func sendMessageCmd(
	src ChatSource,
	chatID int64,
	text string,
	operation uint64,
) tea.Cmd {
	return func() tea.Msg {
		message, err := src.SendMessage(context.Background(), chatID, text)
		if err != nil {
			return messageSentMsg{
				chatID:    chatID,
				operation: operation,
				err:       err,
			}
		}
		return messageSentMsg{
			chatID:    chatID,
			operation: operation,
			message:   message,
		}
	}
}

// historyPageSize is the requested page size for LoadHistory.
//
// TDLib caps the accepted limit at 100; the ChatSource implementation
// is expected to clamp as needed.
const historyPageSize = 50

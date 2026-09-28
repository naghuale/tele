package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// HistoryPage is one page of a chat's message history, as delivered to
// the TUI by a ChatSource.
//
// NextFrom is an inclusive boundary message ID from the underlying
// TDLib page. Code that concatenates multiple pages must de-duplicate
// messages by ID.
//
// HasMore says the source had more to give: it is false only when a
// page came back empty, which is the one answer TDLib gives for the end
// of a history. It was a len(messages) == limit heuristic, and it said
// false on the first page of a chat whose first answer held one or two
// messages because that is all TDLib had under its hand — so a chat
// opened on a real account showed its last two messages and never
// asked again. The model stops on a page that adds no new message
// whatever HasMore says, so a source that cannot answer the question
// is one that never loads anything.
type HistoryPage struct {
	Messages []Message
	NextFrom int64
	HasMore  bool

	// Unreadable is how many entries of the page the source could not
	// read and left out. It is a number and never a text: a message of
	// this user that is missing because the source could not read it is
	// a message that is not on the screen, and the count is what says
	// so somewhere the user is not looking over their shoulder.
	Unreadable int
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
//
// chatID identifies the target chat.
//
// fromMessageID is the boundary the request was made with. Zero means
// the first page, which replaces the chat's messages. A non-zero value
// means an older page, which is appended to them.
//
// operation identifies the history load the response belongs to. It
// protects against a late page being applied to a newer model state,
// for example after the user left the chat and re-entered it. It plays
// the same role for history that sendOperation plays for sending.
type historyLoadedMsg struct {
	chatID        int64
	fromMessageID int64
	operation     uint64
	page          HistoryPage
	err           error
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

// chatsLoadSlowAfter is how long the chat list may take before the screen
// says that the wait is longer than expected (§18).
//
// Ten seconds is long enough that a slow connection does not produce a
// message about a connection, and short enough that a user who is waiting
// is not waiting to be told. The text it leads to names R, so the wait ends
// with a key and not only with a sentence.
const chatsLoadSlowAfter = 10 * time.Second

// chatsLoadDeadlineMsg is delivered when a chat list load has been in
// flight for chatsLoadSlowAfter.
//
// It is a message rather than a clock read in the view because the screen
// has to be redrawn when the sentence appears, and a view that redraws
// itself on a timer is the loop §6.3 rules out. One message at the moment
// the text changes is not a loop.
type chatsLoadDeadlineMsg struct {
	// operation is the load this deadline belongs to, so the deadline of a
	// load the user already retried cannot announce a wait that is over.
	operation uint64
}

// scheduleChatsLoadDeadline returns the message that fires when a load has
// taken too long.
func scheduleChatsLoadDeadline(operation uint64) tea.Cmd {
	return tea.Tick(chatsLoadSlowAfter, func(time.Time) tea.Msg {
		return chatsLoadDeadlineMsg{operation: operation}
	})
}

// loadHistoryCmd returns a command that loads one history page through
// src.
//
// fromMessageID is the inclusive boundary to start from; zero requests
// the newest page. operation is echoed back on the resulting
// historyLoadedMsg so the model can discard a response that has been
// superseded.
func loadHistoryCmd(
	src ChatSource,
	chatID int64,
	fromMessageID int64,
	limit int,
	operation uint64,
) tea.Cmd {
	return func() tea.Msg {
		page, err := src.LoadHistory(context.Background(), chatID, fromMessageID, limit)
		if err != nil {
			return historyLoadedMsg{
				chatID:        chatID,
				fromMessageID: fromMessageID,
				operation:     operation,
				err:           err,
			}
		}
		return historyLoadedMsg{
			chatID:        chatID,
			fromMessageID: fromMessageID,
			operation:     operation,
			page:          page,
		}
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

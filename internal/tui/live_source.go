package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// This file is the interface's end of the live Telegram state.
//
// The state itself lives in internal/telegram and is written by the session
// pump as TDLib updates arrive (ADR-0003). The interface cannot read it: the
// TUI imports nothing from the Telegram binding, and a package that reached
// across that line would put a TDLib type on a screen and the schema of a
// wire format into the half of the program that draws words.
//
// So the changes arrive through one dependency, ChatLiveSource, and the
// implementation of it is an adapter over the store. What crosses the line
// is the smallest thing the screen needs: the chat list in Telegram's own
// order, and the message events of the chat that is open.
//
// Two rules make it a subscription rather than a poll:
//
//   - a consumer is told *that* the state changed and reads the state
//     itself, and
//   - a change is a signal, never a payload: a busy account would otherwise
//     deliver a message to the Bubble Tea loop for every update TDLib
//     sends, and a channel of updates arrives with the order it is watched
//     in rather than the order Telegram sent it in.
//
// The loop here waits for the next signal and asks again after every one,
// so the Bubble Tea program stays the only owner of the Model.

// ChatLiveSource is the live Telegram state as the interface reads it.
//
// A nil source is a program with no Telegram behind it: the chat list is
// loaded once at startup and `R` loads it again, and the status line says
// that the list is not updating by itself. Nothing else changes, because a
// list that cannot be live is the list this program has always drawn.
type ChatLiveSource interface {
	// Available reports whether the live state can be read at all.
	//
	// It is the question the status line asks, and it is asked once per
	// read rather than kept: a store that is there and says nothing is a
	// list that has not changed, and a list that has not changed is not a
	// list that is not live.
	Available() bool

	// WaitForChange blocks until the live state has changed or ctx is done.
	//
	// The signal coalesces: a burst of a thousand updates is one wake-up,
	// and the state that is read afterwards is the whole of them.
	WaitForChange(ctx context.Context)

	// Chats returns the main chat list in the order Telegram keeps it in:
	// pinned chats first, then by the order of their last message.
	//
	// It is a read of memory and it copies what it returns, so a caller may
	// hold on to the slice.
	Chats() []LiveChat

	// MessageEvents returns the events of one chat that follow cursor,
	// together with the cursor to ask with next time.
	//
	// Resync says the events after cursor are no longer all there, and the
	// consumer reloads the first page of that chat and continues from the
	// new cursor. Overflow therefore costs a page rather than a message.
	MessageEvents(chatID int64, cursor uint64) LiveMessageEvents
}

// LiveChat is one row of the live chat list.
//
// It carries what TDLib keeps updating and what the row of the list draws:
// the name, the unread count, the preview and the moment of the last
// message. Everything else a row knows — the kind of the chat, the other
// names it is found by in the search, the messages of the conversation
// behind it — belongs to the row the interface already holds, and a live
// update keeps it.
type LiveChat struct {
	ID      int64
	Title   string
	Unread  int
	Preview string
	At      time.Time

	// Kind is what kind of chat this is, as far as the badge of the row is
	// concerned. It says "more than one person in it" and nothing more: a
	// channel is a supergroup here, and the row a live update brings keeps
	// the kind the loaded list gave it.
	Kind ChatKind
}

// LiveMessageEventKind is what happened to the messages of a chat.
type LiveMessageEventKind uint8

const (
	// LiveMessageAdded is a message that appeared in a chat, incoming or
	// outgoing alike.
	LiveMessageAdded LiveMessageEventKind = iota

	// LiveMessageReplaced is an outgoing message whose temporary
	// identifier became its final one. OldID is the identifier the
	// consumer already holds.
	LiveMessageReplaced

	// LiveMessageDeleted is a set of messages that are gone from a chat.
	// IDs are the identifiers, and the set is never empty.
	LiveMessageDeleted
)

// LiveMessageEvent is one change to the messages of a chat.
type LiveMessageEvent struct {
	Kind    LiveMessageEventKind
	Message Message

	// OldID is the identifier a replaced message had before.
	OldID int64

	// IDs are the identifiers of deleted messages.
	IDs []int64
}

// LiveMessageEvents is one read of the message events of a chat.
//
// It is the whole answer of the read in one value, because the three parts
// are one decision: either the consumer has every event after the cursor it
// asked with, or the window has moved past them and the conversation is
// read again.
type LiveMessageEvents struct {
	Events []LiveMessageEvent
	Cursor uint64
	Resync bool
}

// liveChangedMsg is delivered when the live state says it has changed.
//
// It carries nothing about what changed, on purpose: a message that carried
// the change would be one Bubble Tea message per TDLib update, delivered in
// the order the loop read them rather than the order Telegram sent them.
// The model reads the state itself, once, when it draws.
type liveChangedMsg struct{}

// liveRepaintDueMsg is delivered when the coalescing delay has passed.
//
// It is a separate message rather than a field on liveChangedMsg because
// the two are different moments: one is "something changed" and the other is
// "the tenth of a second the screen is owed its next redraw has passed".
type liveRepaintDueMsg struct{}

// liveRepaintInterval is the shortest time between two redraws of the
// screen that a live change asks for.
//
// A channel can deliver hundreds of updates a second, and a redraw per
// update is a program that spends its time painting a screen nobody can
// read. A tenth of a second is ten frames a second: fast enough that a
// person watching the list sees a message arrive rather than a jump, and
// slow enough that the work of painting is a tenth of what it would be.
//
// The changes are not thrown away while they wait. The redraw reads the
// state, so a hundred updates in that tenth of a second are one redraw of
// the hundred of them and not ninety-nine redraws of nothing.
const liveRepaintInterval = 100 * time.Millisecond

// waitLiveCmd returns the command that waits for the next change of the
// live state.
//
// It blocks, and it is re-issued after every change: one wait is in flight
// at a time, which is the whole of the subscription. The wait ends with the
// program's context, so a program that is closing leaves no goroutine
// behind it.
func waitLiveCmd(src ChatLiveSource, ctx context.Context) tea.Cmd {
	if src == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	return func() tea.Msg {
		src.WaitForChange(ctx)

		return liveChangedMsg{}
	}
}

// liveRepaintDelayCmd returns the command that fires when the redraw a live
// change was owed has become due.
func liveRepaintDelayCmd(delay time.Duration) tea.Cmd {
	return tea.Tick(delay, func(time.Time) tea.Msg {
		return liveRepaintDueMsg{}
	})
}

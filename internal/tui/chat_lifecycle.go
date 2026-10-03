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

// chatOpenedMsg is delivered by the queue when it has opened a chat.
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
// Both calls go onto one queue rather than into whatever goroutine Bubble Tea
// happens to run a command in. Two switches are two commands of two updates,
// and Bubble Tea runs the commands of two updates at the same time: the close
// that belongs to the first switch and the open that belongs to the second one
// could cross, and a user who walks A, then B, then A again came back to a
// chat that the late close of the first switch closed under them (the
// auditor's RECHECK-FINDING-T6 on main adab9ea, the recheck of #67, 03.10).
// The presence in the header went with it, and so did the read marks that
// opening a chat makes on every device of the account.
//
// Ordering the two calls of one switch, as the change before this one did, is
// not the whole of it either: that is the order inside one command, and
// nothing ordered two commands with respect to each other.
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

	return m.chatLifecycle().closeThenOpen(m.ctx, previous, chatID)
}

// chatLifecycle is the queue the opens and closes go through, made when the
// first chat is entered rather than when the model is built: a program that
// opens no chat asks TDLib about no chat, and a goroutine nobody needs is a
// goroutine nobody has to keep.
func (m *Model) chatLifecycle() *chatLifecycleQueue {
	if m.lifecycle == nil {
		m.lifecycle = newChatLifecycleQueue(m.presenceOpener, m.diagnostics)
	}

	return m.lifecycle
}

// chatLifecycleQueue is the one goroutine that asks TDLib to open and close
// chats.
//
// There is one of these per program, and every switch goes through it, so
// what TDLib is told is in the order the chats were entered: the chat that was
// left is closed before the chat that is opened now, a switch that comes later
// cannot be overtaken by one that came before it, and two calls are never in
// flight at the same time.
//
// A chat TDLib counts and a chat whose messages may be read both come out of
// this one goroutine, so what the interface believes about a chat is what TDLib
// was told about it: the selection cannot be right on the screen and wrong in
// Telegram, which is the whole of what #58 was about.
//
// It is a pointer in the model because the model is passed by value and every
// copy of it has to share the one queue, and it is a pointer here because
// nothing in it may be copied: two queues would be two goroutines, and two
// goroutines are the crossing this queue was made to stop.
type chatLifecycleQueue struct {
	opener      ChatPresenceOpener
	diagnostics io.Writer
	switches    chan chatLifecycleSwitch
}

// chatLifecycleSwitch is one change of the chat the user is looking at: the
// chat to close and the chat to open, in that order.
//
// answer is where the answer of the open goes, and it has room for one
// message, so a command the program never runs is an answer nobody waits for
// rather than a queue that stops.
type chatLifecycleSwitch struct {
	ctx     context.Context
	closing int64
	opening int64
	answer  chan tea.Msg
}

// chatLifecycleQueueDepth is how many switches may be waiting for the queue.
//
// It is not nothing at all: submitting a switch must not hold the keys of a
// user who is walking through chats while TDLib is on a round trip. It is
// not a large number either: a switch that arrives when the queue is that
// full has to wait, and that is the backpressure of a queue rather than a
// number to be generous with.
const chatLifecycleQueueDepth = 8

func newChatLifecycleQueue(
	opener ChatPresenceOpener,
	diagnostics io.Writer,
) *chatLifecycleQueue {
	queue := &chatLifecycleQueue{
		opener:      opener,
		diagnostics: diagnostics,
		switches:    make(chan chatLifecycleSwitch, chatLifecycleQueueDepth),
	}

	go queue.serve()

	return queue
}

// closeThenOpen puts one change of the chat on the queue — the chat to close,
// the chat to open — and returns the command that waits for its answer.
//
// The change goes on the queue here and not when the command is run: the order
// of the queue has to be the order of the presses, and the presses are the
// order the model was updated in. A command that queued itself would let a
// later switch overtake an earlier one, which is the whole of what this queue
// is for.
//
// The command waits for the answer of the switch it belongs to even when
// there is no open in it: the close of a chat that is being left is on the
// queue and has to be asked for before the switch that follows it, and a
// command nobody runs cannot hold the queue up — the answer has room for one
// message whether it is taken or not.
func (q *chatLifecycleQueue) closeThenOpen(
	ctx context.Context,
	closing, opening int64,
) tea.Cmd {
	if ctx == nil {
		// A model built without a context is a model whose calls are not
		// cancelled by anything, which is what a background context says.
		ctx = context.Background()
	}

	answer := make(chan tea.Msg, 1)
	q.switches <- chatLifecycleSwitch{
		ctx:     ctx,
		closing: closing,
		opening: opening,
		answer:  answer,
	}

	return func() tea.Msg {
		return <-answer
	}
}

// serve is the loop of the queue: one change of the chat at a time, and the
// close before the open.
//
// The loop does not end. The queue belongs to the model and ends with the
// program, which is the only place a chat can still be open.
func (q *chatLifecycleQueue) serve() {
	for request := range q.switches {
		q.serveOne(request)
	}
}

// serveOne asks TDLib about one change of the chat and answers whoever waits
// for it.
//
// The close is asked for even when the open is refused afterwards: the two are
// best effort (see ChatPresenceOpener), and a chat the user has left is closed
// whether or not the next one opened.
//
// Only the open has an answer to deliver. A close answers nothing, because
// nothing on the screen waits for one, and an answer that says a chat was
// closed is an answer about a chat nobody is looking at any more (see
// updateChatOpened). A switch with no open in it delivers no message at all,
// and the command that waits for it is delivered nothing — which the event
// loop drops, as it drops every message that is not one.
func (q *chatLifecycleQueue) serveOne(request chatLifecycleSwitch) {
	var answer tea.Msg
	if request.closing != 0 {
		if err := q.opener.CloseChat(request.ctx, request.closing); err != nil {
			reportChatLifecycleCause(q.diagnostics, "close", request.closing, err)
		}
	}
	if request.opening != 0 {
		err := q.opener.OpenChat(request.ctx, request.opening)
		if err != nil {
			reportChatLifecycleCause(q.diagnostics, "open", request.opening, err)
		}

		answer = chatOpenedMsg{chatID: request.opening, err: err}
	}

	request.answer <- answer
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
//
// It goes onto the same queue as a switch, so a close on the way out cannot
// overtake a switch that is still on its way in, and cannot be overtaken by
// one either: the queue is emptied in the order the presses were.
func (m *Model) closeConversationChat() tea.Cmd {
	if m == nil || m.presenceOpener == nil || m.openedChat == 0 {
		return nil
	}

	chatID := m.openedChat
	m.openedChat = 0
	// Nothing has answered for a chat that is not open, and nothing may be
	// read in a chat that was closed.
	m.openedAck = 0

	return m.chatLifecycle().closeThenOpen(m.ctx, chatID, 0)
}

package tui

import (
	"context"
	"fmt"
	"io"
	"sync"

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
// Both calls go through one queue rather than into whatever goroutine Bubble
// Tea happens to run a command in. Two switches are two commands of two
// updates, and Bubble Tea runs the commands of two updates at the same time:
// the close that belongs to the first switch and the open that belongs to the
// second one could cross, and a user who walks A, then B, then A again came
// back to a chat that the late close of the first switch closed under them
// (the auditor's RECHECK-FINDING-T6 on main adab9ea, the recheck of #67,
// 03.10). The presence in the header went with it, and so did the read marks
// that opening a chat makes on every device of the account.
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

	return m.chatLifecycle().want(chatID)
}

// chatLifecycle is the queue the opens and closes go through, made when the
// first chat is entered rather than when the model is built: a program that
// opens no chat asks TDLib about no chat, and a goroutine nobody needs is a
// goroutine nobody has to keep.
func (m *Model) chatLifecycle() *chatLifecycleQueue {
	if m.lifecycle == nil {
		m.lifecycle = newChatLifecycleQueue(m.presenceOpener, m.diagnostics, m.ctx)
	}

	return m.lifecycle
}

// chatLifecycleQueue is the one goroutine that asks TDLib to open and close
// chats, and the one place where the chat the user is looking at is written
// down on the way to TDLib.
//
// There is one of these per program, and every switch goes through it, so
// what TDLib is told is in the order the chats were entered: the chat that was
// left is closed before the chat that is opened now, a switch that comes later
// cannot be overtaken by one that came before it, and two calls are never in
// flight at the same time.
//
// What is wanted is one number and not a list. TDLib may be on a round trip
// for as long as the network takes — a network down, a proxy that holds a
// connection — and the keys of a user who is walking through chats are not
// held until it comes back. So a switch writes down the chat that is wanted
// and moves on, and the switches it overtook are not executed at all: walking
// A, then B, then C, then A again while TDLib is busy asks TDLib for A and
// for nothing else — B and C are never opened, and no call is ever in flight
// beside another. What TDLib ends up counting is the chat the user is in.
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
	ctx         context.Context

	// signal is the worker being told there is something to do. It has room
	// for one, and want never waits to send on it: a wake-up that cannot be
	// sent is a wake-up already waiting, and the worker reads the whole state
	// again on every turn.
	signal chan struct{}

	// mu guards the three words below, which the model writes in Update and
	// the worker reads in its own goroutine.
	mu      sync.Mutex
	wanted  int64
	opened  int64
	waiting []chan tea.Msg
}

// chatLifecycleSignalDepth is how many wake-ups may be waiting for the worker
// of the queue: one, for the reason given on the field.
const chatLifecycleSignalDepth = 1

func newChatLifecycleQueue(
	opener ChatPresenceOpener,
	diagnostics io.Writer,
	ctx context.Context,
) *chatLifecycleQueue {
	if ctx == nil {
		// A model built without a context is a model whose calls are not
		// cancelled by anything, which is what a background context says.
		ctx = context.Background()
	}

	queue := &chatLifecycleQueue{
		opener:      opener,
		diagnostics: diagnostics,
		ctx:         ctx,
		signal:      make(chan struct{}, chatLifecycleSignalDepth),
	}

	go queue.serve()

	return queue
}

// want tells the queue which chat the user is looking at now, and returns the
// command that waits for the answer of that switch.
//
// It does not block, and that is the whole of it: writing down one number and
// walking away is all a switch is, whatever TDLib is doing at the moment.
func (q *chatLifecycleQueue) want(chatID int64) tea.Cmd {
	answer := make(chan tea.Msg, 1)

	q.mu.Lock()
	q.wanted = chatID
	q.waiting = append(q.waiting, answer)
	q.mu.Unlock()

	q.wake()

	return func() tea.Msg {
		return <-answer
	}
}

// wantNothing is the way out of a chat: nothing is wanted, so the chat TDLib
// has is closed. It is a switch like any other and waits in the same queue,
// so a close on the way out of the program cannot overtake a switch that is
// still on its way in, and a switch cannot overtake it either.
func (q *chatLifecycleQueue) wantNothing() tea.Cmd {
	return q.want(0)
}

// wake tells the worker there is something to do, and never waits for it.
func (q *chatLifecycleQueue) wake() {
	select {
	case q.signal <- struct{}{}:
	default:
	}
}

// serve is the loop of the queue: one change of the chat at a time, and the
// close before the open.
//
// The loop does not end. The queue belongs to the model and ends with the
// program, which is the only place a chat can still be open.
func (q *chatLifecycleQueue) serve() {
	for range q.signal {
		q.turn()
	}
}

// turn is one pass of the queue. It takes the chat that is wanted now and the
// commands waiting for an answer, and it does not look at the switches it has
// overtaken: what they asked for is not what is wanted any more.
func (q *chatLifecycleQueue) turn() {
	q.mu.Lock()
	wanted, opened, waiting := q.wanted, q.opened, q.waiting
	q.waiting = nil
	q.mu.Unlock()

	answer := q.bringTDLibTo(wanted, opened)

	for i, command := range waiting {
		if i == len(waiting)-1 {
			// The last switch is the one this turn was for: it is the switch
			// that wrote down what was wanted.
			command <- answer

			continue
		}

		// A switch that was overtaken is a switch about a chat the user has
		// already left. Nothing was asked for it, so it is given nothing to
		// wait for rather than an answer it would only have to throw away —
		// and a command that is given nothing still comes back.
		command <- nil
	}
}

// bringTDLibTo asks TDLib for the chat that is wanted now, when TDLib is not
// already in it, and answers what came of it.
//
// The close is asked for even when the open is refused afterwards: the two are
// best effort (see ChatPresenceOpener), and a chat the user has left is closed
// whether or not the next one opened.
//
// Only an open has an answer to deliver. A close answers nothing, because
// nothing on the screen waits for one, and an answer that says a chat was
// closed is an answer about a chat nobody is looking at any more (see
// updateChatOpened).
func (q *chatLifecycleQueue) bringTDLibTo(wanted, opened int64) tea.Msg {
	if wanted == opened {
		if wanted == 0 {
			return nil
		}

		// The chat the user is in is the chat TDLib has, and the model may
		// read it: this is the one answer the queue can give without asking
		// TDLib anything, because TDLib was asked for this chat and answered
		// one way or another.
		return chatOpenedMsg{chatID: wanted}
	}

	if opened != 0 {
		q.close(opened)
	}
	if wanted == 0 {
		return nil
	}

	return q.open(wanted)
}

// open asks TDLib to open a chat and answers about it.
func (q *chatLifecycleQueue) open(chatID int64) tea.Msg {
	err := q.opener.OpenChat(q.ctx, chatID)
	if err != nil {
		reportChatLifecycleCause(q.diagnostics, "open", chatID, err)
	}
	q.remember(chatID)

	return chatOpenedMsg{chatID: chatID, err: err}
}

// close asks TDLib to close a chat and answers nothing.
func (q *chatLifecycleQueue) close(chatID int64) {
	if err := q.opener.CloseChat(q.ctx, chatID); err != nil {
		reportChatLifecycleCause(q.diagnostics, "close", chatID, err)
	}
	q.remember(0)
}

// remember is the chat TDLib has been told about, which is not always the chat
// the model last asked about: a switch that was overtaken was never asked for,
// and a chat whose open was refused is still the chat the program believes it
// opened (ChatPresenceOpener, and openedChat beside it).
func (q *chatLifecycleQueue) remember(chatID int64) {
	q.mu.Lock()
	defer q.mu.Unlock()

	q.opened = chatID
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
// It is wanted like any other switch and through the same queue, so it is the
// chat TDLib was actually told to open that is closed, and it is the last
// thing the queue does: a switch that was still on its way in is overtaken and
// is not asked for at all.
func (m *Model) closeConversationChat() tea.Cmd {
	if m == nil || m.presenceOpener == nil || m.openedChat == 0 {
		return nil
	}

	m.openedChat = 0
	// Nothing has answered for a chat that is not open, and nothing may be
	// read in a chat that was closed.
	m.openedAck = 0

	return m.chatLifecycle().wantNothing()
}

package application

import (
	"context"
	"sync"
	"time"

	"telecli/internal/telegram"
)

// This file is the question "who sent this" asked without the interface
// waiting for the answer.
//
// The interface never waits for the network, and a name is decoration around
// a message a user can already read: a message from a sender whose name is
// not known yet goes on the screen with a placeholder above it, the question
// is asked here in the background, and the answer arrives as one more event
// of that chat (live_chat_source.go). What is left on the screen while the
// question is on its way is the same thing it was before this file: the
// truth about a name nobody could read yet.
//
// The read is a round trip to a local TDLib, and it is bounded twice over:
// by liveNameTimeout, so that a TDLib that has gone away does not keep a
// goroutine of this program, and by the program's own context, so that
// leaving the program ends every read still on its way.
//
// The names are kept in the adapter's own nameCache and nowhere else: not in
// the live store, which keeps no names on purpose (#41), not in a log line,
// not in a report and not in an error message.

// liveNameTimeout bounds one read of a sender's name.
//
// It is not the two seconds the chat service allows a name it is drawing a
// page of history with. That read stands between the interface and the next
// frame, and this one stands between nothing: a name that arrives after five
// seconds replaces the placeholder above the message it belongs to, so the
// bound is there to end a read from a TDLib that is gone rather than to
// keep a screen waiting — and thirty seconds is long enough for a TDLib that
// is fetching a user from the server.
const liveNameTimeout = 30 * time.Second

// liveSenderNames reads the name of whoever sent a message, in the
// background, and says that one has arrived.
//
// It is the whole of the answering side: one read of a name at a time, the
// names it read in the adapter's cache, and a coalesced signal for the
// interface. The signal has the shape of the store's own and is for the same
// reason — a change is a signal and never a payload, so a hundred names that
// arrive are one wake-up of the loop rather than a hundred messages in it.
type liveSenderNames struct {
	names TelegramSenderNames
	cache *nameCache
	ctx   context.Context

	mu      sync.Mutex
	reading map[string]struct{}
	wake    chan struct{}
}

// newLiveSenderNames returns the reader of the names a session can answer
// with.
//
// ctx is the program's context: every read it starts is a child of it, and
// the program leaving ends them all. A reader without one asks for names that
// no exit will ever call off, which is the state a session that has been
// closed leaves behind.
func newLiveSenderNames(
	ctx context.Context,
	names TelegramSenderNames,
	cache *nameCache,
) *liveSenderNames {
	if ctx == nil {
		ctx = context.Background()
	}

	return &liveSenderNames{
		names:   names,
		cache:   cache,
		ctx:     ctx,
		reading: make(map[string]struct{}),
		wake:    make(chan struct{}, 1),
	}
}

// askUserName starts a read of the name of a person.
//
// It answers at once: the read is on its way or it is not, and the interface
// is told which by the name it gets back, not by waiting.
func (n *liveSenderNames) askUserName(userID int64) {
	if n == nil || userID == 0 {
		return
	}

	n.ask(userNameKey(userID), func(ctx context.Context) string {
		name, err := n.names.GetUserName(ctx, userID)
		if err != nil {
			return ""
		}

		return name
	})
}

// askChatName starts a read of the title of a chat that sent a message, by
// the same rule and for the same reason as a person's name.
func (n *liveSenderNames) askChatName(chatID telegram.ChatID) {
	if n == nil || chatID == 0 {
		return
	}

	n.ask(chatNameKey(int64(chatID)), func(ctx context.Context) string {
		summary, err := n.names.GetChat(ctx, chatID)
		if err != nil {
			return ""
		}

		return summary.Title
	})
}

// ask starts a read of one name, unless the name is known or a read of it is
// already on its way.
//
// One read at a time is what keeps a busy chat from asking a hundred times
// for the name of one person: a hundred messages from a sender nobody has
// named are one question, and the answer that comes back signs all of them.
//
// A read that failed is not remembered, so the next message of that sender
// asks again. A name TDLib could not answer is a name TDLib may answer next
// time, and a read that is already over costs nothing to repeat — unlike the
// question it replaces, which is asked in the loop of the interface.
func (n *liveSenderNames) ask(key string, read func(context.Context) string) {
	if n == nil || n.names == nil || read == nil {
		return
	}
	if n.ctx.Err() != nil {
		// The program has left, and nobody is going to draw the answer.
		// The message keeps the placeholder it went out with, which is the
		// truth about a name nobody was there to read.
		return
	}
	if _, known := n.cache.lookup(key); known {
		return
	}

	n.mu.Lock()
	if _, onItsWay := n.reading[key]; onItsWay {
		n.mu.Unlock()

		return
	}
	n.reading[key] = struct{}{}
	n.mu.Unlock()

	go n.answer(key, read)
}

// answer reads one name and keeps it, or keeps nothing.
//
// The name is remembered before the signal is posted, so a loop that wakes
// up on the signal reads a cache that holds the answer rather than one that
// is about to hold it.
func (n *liveSenderNames) answer(key string, read func(context.Context) string) {
	ctx, cancel := context.WithTimeout(n.ctx, liveNameTimeout)
	defer cancel()

	name := read(ctx)

	n.forget(key)

	if name == "" {
		return
	}

	n.cache.remember(key, name)
	n.signal()
}

// forget records that the read of a name is over, so that a later message
// from that sender may ask again.
func (n *liveSenderNames) forget(key string) {
	n.mu.Lock()
	defer n.mu.Unlock()

	delete(n.reading, key)
}

// signal says that a name has arrived.
func (n *liveSenderNames) signal() {
	select {
	case n.wake <- struct{}{}:
	default:
	}
}

// changes returns the coalesced signal of a name that has arrived.
//
// It is never closed: a reader that has stopped reading it is a program that
// has stopped, and the wait on it ends with the program's context.
func (n *liveSenderNames) changes() <-chan struct{} {
	if n == nil {
		return nil
	}

	return n.wake
}

package tui

import (
	"context"
	"fmt"
	"time"
)

// PendingMessage is an outgoing message that is not in the chat history
// yet, with its delivery state.
//
// The durable queue holds the text of everything that has not been
// delivered, and the timeline has to show it: a user who pressed Enter and
// sees nothing has no way of knowing whether the message went anywhere
// (§4.4, §20 "After restart entries снова появляются в timeline").
//
// The text is the reason this type exists and the reason it is careful. It
// is the one place where message text exists outside the history, it goes
// to View and to nothing else, and String and GoString leave it out so that
// a value on its way to a log line or a crash report cannot carry it (§19).
type PendingMessage struct {
	// EntryID is the durable entry this message is queued as.
	EntryID string

	// ChatID is the chat the message belongs to.
	ChatID int64

	// Text is the message as the user typed it.
	Text string

	// State is where the message is in its delivery.
	State MessageDeliveryState

	// MessageID is the identifier Telegram knows the message by, and it
	// is set only once Telegram has confirmed the send. Before that the
	// queue holds a temporary identifier that no history page will ever
	// contain, and a row placed under it could not be recognised as the
	// same message when the history arrives.
	//
	// It arrives through withStateFrom, from the status list, which is
	// the only place a final identifier is ever written.
	MessageID int64

	// Version is the version the queue read this record at.
	//
	// It is what a cancel is made against: the queue refuses a cancel of a
	// record that has moved on since, and an interface that passed a
	// version of its own would either be refused every time or cancel
	// whatever the record had become.
	Version uint64

	// Attempt is how many delivery attempts have been made.
	Attempt int

	// NextAttemptAt is when a retrying message will be tried again. It is
	// the absolute time and never a countdown: §6.3 asks for no screen that
	// repaints itself every second.
	NextAttemptAt time.Time

	// CreatedAt is when the message was queued, and it is the order the
	// timeline shows them in.
	CreatedAt time.Time

	// local marks a message this session queued and has not seen in the
	// queue's own list, or no longer sees there because Telegram accepted
	// it. It is unexported because it is a fact about this process and not
	// about the message: a value that crosses out of the package is never
	// local.
	local bool
}

// String returns the entry, the state and the version, and never the text.
//
// The version is here because it is what a failure message about a cancel
// needs, and it is not personal data: it counts transitions of a record the
// user cannot read.
func (m PendingMessage) String() string {
	return fmt.Sprintf(
		"entry %s state %s attempt %d version %d",
		m.EntryID,
		m.State,
		m.Attempt,
		m.Version,
	)
}

// GoString returns the entry and the state, and never the text.
//
// It is here for the same reason as String: %#v on a value of this type
// would otherwise print every field, and one of them is a message.
func (m PendingMessage) GoString() string {
	return m.String()
}

// PendingMessageSource lists the outgoing messages of one chat that the
// history does not have yet.
//
// It is a separate source from MessageStatusSource on purpose. The status
// list is payload-free and is read by the parts of the interface that only
// need to know where a message is, and it must stay that way: a status in a
// log, in a doctor report or in an error must never carry what somebody
// wrote. The timeline needs the text, so it asks a source that has it, and
// only the timeline asks.
//
// A nil source is direct delivery mode, where every message is in the
// history as soon as it is sent and there is nothing pending.
type PendingMessageSource interface {
	ListPendingMessages(
		ctx context.Context,
		accountKey string,
		chatID int64,
	) ([]PendingMessage, error)
}

// pendingMessageLess reports whether a belongs before b.
//
// The order is by creation and then by entry, so that two messages queued
// in the same instant keep the same order between two reads instead of
// swapping places under the user's eyes.
func pendingMessageLess(a, b PendingMessage) bool {
	if !a.CreatedAt.Equal(b.CreatedAt) {
		return a.CreatedAt.Before(b.CreatedAt)
	}

	return a.EntryID < b.EntryID
}

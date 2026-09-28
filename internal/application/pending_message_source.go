package application

import (
	"context"
	"fmt"
	"time"

	"telecli/internal/outbox"
)

// PendingMessage is an outgoing message the history does not have yet, with
// the text the user wrote.
//
// It is the only delivery projection in this package that carries a payload,
// and it exists because the timeline has to show a message the user has just
// queued (§4.4). The text goes to the interface and nowhere else, which is
// why String and GoString leave it out: a value on its way to a log line or
// a crash report must not carry what somebody wrote (§19).
type PendingMessage struct {
	EntryID string
	ChatID  int64
	Text    string
	State   MessageDeliveryState

	// MessageID is the identifier Telegram gave the message, and it is
	// set only once Telegram has confirmed the send. It is a number TDLib
	// assigned, in the same class as the chat id, and it is what lets the
	// interface put a confirmed message into the chat history under the
	// identifier the history will come back with.
	MessageID int64

	// Version is the version the queue read this record at, and it is what
	// a cancel is made against: the store refuses a cancel of a record that
	// has moved on since it was read (§13).
	Version       uint64
	Attempt       int
	NextAttemptAt time.Time
	CreatedAt     time.Time
}

// String returns the entry, the state and the version, and never the text.
//
// The version is here because a failure about a cancel needs it, and it is
// not personal data: it counts transitions of a record nobody can read.
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
func (m PendingMessage) GoString() string {
	return m.String()
}

// PendingMessageSource lists the outgoing messages of one chat that the
// history does not have yet.
//
// It is a separate source from MessageStatusSource on purpose: the status
// list is payload-free, is read by everything that only needs to know where
// a message is, and must stay that way.
type PendingMessageSource interface {
	ListPendingMessages(
		ctx context.Context,
		accountKey string,
		chatID int64,
	) ([]PendingMessage, error)
}

// entryLister is the part of a durable store this source needs: the entries
// of the queue, with the text they hold.
//
// It is a narrow interface on purpose. A source that could also enqueue or
// claim could send a message from a read that the screen asked for, and a
// read is the one place on this path where nothing should be able to write.
type entryLister interface {
	ListAll(ctx context.Context) ([]outbox.Entry, error)
}

// OutboxPendingMessageSource lists the queue entries of a chat with the text
// they hold.
type OutboxPendingMessageSource struct {
	store entryLister
	limit int
}

// NewOutboxPendingMessageSource returns a source over a durable store.
func NewOutboxPendingMessageSource(
	store entryLister,
	limit int,
) (*OutboxPendingMessageSource, error) {
	if store == nil {
		return nil, fmt.Errorf(
			"outbox pending message source: store is required",
		)
	}
	if limit < 0 {
		return nil, fmt.Errorf(
			"outbox pending message source: limit must not be negative",
		)
	}

	return &OutboxPendingMessageSource{store: store, limit: limit}, nil
}

// ListPendingMessages returns the entries of one chat that the history does
// not have yet, oldest first.
//
// A sent entry is not one of them: Telegram has that message, and the
// history comes back with it under the final identifier the entry now
// holds. Drawing it here as well would show the user their own message
// twice (ADR-0003 §6). An accepted entry *is* one of them: TDLib has the
// message but Telegram has not confirmed it, so the temporary identifier
// the entry holds is in no history page, and the message is still on its
// way out.
func (s *OutboxPendingMessageSource) ListPendingMessages(
	ctx context.Context,
	accountKey string,
	chatID int64,
) ([]PendingMessage, error) {
	if s == nil || s.store == nil {
		return nil, fmt.Errorf(
			"outbox pending message source: store is required",
		)
	}
	if ctx == nil {
		return nil, fmt.Errorf(
			"outbox pending message source: context is required",
		)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	entries, err := s.store.ListAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("list outbox entries: %w", err)
	}

	messages := make([]PendingMessage, 0, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if entry.AccountKey != accountKey || entry.ChatID != chatID {
			continue
		}
		if entry.State == outbox.StateSent {
			continue
		}

		projected, err := projectPendingMessage(entry)
		if err != nil {
			return nil, fmt.Errorf("project pending message: %w", err)
		}

		messages = append(messages, projected)
		if s.limit > 0 && len(messages) >= s.limit {
			break
		}
	}

	return messages, nil
}

var _ PendingMessageSource = (*OutboxPendingMessageSource)(nil)

// projectPendingMessage maps a queue entry onto the projection the
// timeline draws.
func projectPendingMessage(entry outbox.Entry) (PendingMessage, error) {
	state, err := projectMessageState(entry.State)
	if err != nil {
		return PendingMessage{}, fmt.Errorf(
			"project pending message for entry %q: %w",
			entry.ID,
			err,
		)
	}

	return PendingMessage{
		EntryID: string(entry.ID),
		ChatID:  entry.ChatID,
		Text:    entry.Text,
		State:   state,
		// The version travels with the entry because a cancel is made
		// against it: the queue refuses a cancel of a record that has
		// moved on since it was read, and an interface that had no version
		// to pass could only ever guess.
		Version:       entry.Version,
		Attempt:       entry.AttemptCount,
		NextAttemptAt: entry.NextAttempt,
		CreatedAt:     entry.CreatedAt,
	}, nil
}

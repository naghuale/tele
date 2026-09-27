package application

import (
	"context"
	"errors"
	"fmt"

	"telecli/internal/outbox"
	"telecli/internal/tui"
)

// OutboxPendingMessageCanceller cancels a queued message through the
// durable store.
//
// It is a thin translation and that is all it should be: the optimistic
// version the screen read goes to the store as it is, and the two failures
// the interface has words for come back as the two sentinels it has words
// for. Everything else keeps its own error and goes to the diagnostic
// stream.
type OutboxPendingMessageCanceller struct {
	submitter MessageSubmitter
}

// NewOutboxPendingMessageCanceller builds a canceller over a submitter.
//
// The submitter is the same object the composer queues through, and it is
// the one place that knows how a record is canceled: a second writer to the
// store would be a second answer to "who may cancel this".
func NewOutboxPendingMessageCanceller(
	submitter MessageSubmitter,
) (*OutboxPendingMessageCanceller, error) {
	if submitter == nil {
		return nil, errors.New("pending message canceller: submitter is required")
	}

	return &OutboxPendingMessageCanceller{submitter: submitter}, nil
}

// CancelMessage cancels a queued message at the version the screen read.
//
// Two failures are named and translated, because the interface has a
// sentence for each and a sentence for "something went wrong" would be a
// sentence about nothing:
//
//   - a version conflict means the record moved: the user did not cancel
//     what they aimed at, and saying they did would be the one lie this
//     screen must not tell;
//   - every other refusal, including a cancel of a record that Telegram has
//     already accepted, is a queue that would not do it. The record is
//     still there and the user is told so.
func (c *OutboxPendingMessageCanceller) CancelMessage(
	ctx context.Context,
	entryID string,
	version uint64,
) error {
	if c == nil || c.submitter == nil {
		return tui.ErrMessageCancelUnavailable
	}
	if entryID == "" {
		return fmt.Errorf("%w: empty entry id", tui.ErrMessageCancelUnavailable)
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	_, err := c.submitter.CancelMessage(
		ctx,
		outbox.ID(entryID),
		version,
	)
	if err == nil {
		return nil
	}

	if isMessageStateChange(err) {
		return fmt.Errorf("%w: %w", tui.ErrMessageStateChanged, err)
	}

	return fmt.Errorf("%w: %w", tui.ErrMessageCancelUnavailable, err)
}

// isMessageStateChange reports whether a refusal means the record moved on.
//
// Every one of these is the same thing to a user: the screen read a state,
// and by the time the action arrived the record was somewhere else. The
// message is not the same - a cancel after acceptance is the queue saying
// "too late", and a missing entry is the queue saying "there is nothing
// there" - and the cause keeps its own text for the log.
func isMessageStateChange(err error) bool {
	return errors.Is(err, outbox.ErrVersionConflict) ||
		errors.Is(err, outbox.ErrInvalidTransition) ||
		errors.Is(err, outbox.ErrCancelAfterAccepted) ||
		errors.Is(err, outbox.ErrNotFound)
}

var _ tui.MessageCanceller = (*OutboxPendingMessageCanceller)(nil)

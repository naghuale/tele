package application

import (
	"context"
	"fmt"

	"telecli/internal/tui"
)

// tuiPendingMessageSourceAdapter is the composition boundary between the
// application pending-message model and the TUI contract.
//
// It is the one boundary in the program where message text crosses from the
// durable queue into the interface, and it is the only reader of it outside
// the view. The projection is explicit rather than a cast: a new application
// state must be projected or fail loudly, and a state the interface has no
// name for must not reach the timeline as one it has.
type tuiPendingMessageSourceAdapter struct {
	source PendingMessageSource
}

// newTUIPendingMessageSourceAdapter returns literal nil for a nil
// application source. Direct delivery mode has no queue to read, and a
// typed nil would make the timeline wait for messages that can never be
// pending.
func newTUIPendingMessageSourceAdapter(
	source PendingMessageSource,
) tui.PendingMessageSource {
	if source == nil {
		return nil
	}

	return &tuiPendingMessageSourceAdapter{source: source}
}

// ListPendingMessages returns the pending messages of one chat for the
// timeline.
func (a *tuiPendingMessageSourceAdapter) ListPendingMessages(
	ctx context.Context,
	accountKey string,
	chatID int64,
) ([]tui.PendingMessage, error) {
	if a == nil || a.source == nil {
		return nil, ErrPendingMessageUnavailable
	}
	if ctx == nil {
		return nil, fmt.Errorf(
			"TUI pending message source: context is required",
		)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	messages, err := a.source.ListPendingMessages(ctx, accountKey, chatID)
	if err != nil {
		return nil, fmt.Errorf("list application pending messages: %w", err)
	}

	result := make([]tui.PendingMessage, 0, len(messages))
	for _, message := range messages {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		state, err := projectTUIMessageDeliveryState(message.State)
		if err != nil {
			return nil, fmt.Errorf(
				"project pending message for entry %q: %w",
				message.EntryID,
				err,
			)
		}

		result = append(result, tui.PendingMessage{
			EntryID:       message.EntryID,
			ChatID:        message.ChatID,
			Text:          message.Text,
			State:         state,
			Attempt:       message.Attempt,
			NextAttemptAt: message.NextAttemptAt,
			CreatedAt:     message.CreatedAt,
		})
	}

	return result, nil
}

var _ tui.PendingMessageSource = (*tuiPendingMessageSourceAdapter)(nil)

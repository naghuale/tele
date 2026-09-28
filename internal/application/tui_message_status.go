package application

import (
	"context"
	"errors"
	"fmt"

	"telecli/internal/tui"
)

// tuiMessageStatusSourceAdapter is the composition boundary between the
// application status model and the TUI status contract.
//
// The TUI package declares its own payload-free status types, so the adapter
// is exhaustive by construction: a new application delivery state must be
// projected explicitly or fail loudly instead of reaching the UI silently.
type tuiMessageStatusSourceAdapter struct {
	source MessageStatusSource
}

// newTUIMessageStatusSourceAdapter returns literal nil for a nil application
// source. Direct delivery mode has no durable status to query, and a typed nil
// would wrongly enable status polling.
func newTUIMessageStatusSourceAdapter(
	source MessageStatusSource,
) tui.MessageStatusSource {
	if source == nil {
		return nil
	}
	return &tuiMessageStatusSourceAdapter{source: source}
}

func (a *tuiMessageStatusSourceAdapter) ListMessageStatuses(
	ctx context.Context,
	accountKey string,
	chatID int64,
) ([]tui.MessageStatus, error) {
	if a == nil || a.source == nil {
		return nil, ErrMessageStatusUnavailable
	}
	if ctx == nil {
		return nil, errors.New("TUI message status source: context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	statuses, err := a.source.ListMessageStatuses(ctx, accountKey, chatID)
	if err != nil {
		return nil, fmt.Errorf("list application message statuses: %w", err)
	}

	result := make([]tui.MessageStatus, 0, len(statuses))
	for _, status := range statuses {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		state, err := projectTUIMessageDeliveryState(status.State)
		if err != nil {
			return nil, fmt.Errorf(
				"project message status %q: %w",
				status.EntryID,
				err,
			)
		}

		result = append(result, tui.MessageStatus{
			EntryID:       status.EntryID,
			AccountKey:    status.AccountKey,
			ChatID:        status.ChatID,
			State:         state,
			MessageID:     status.MessageID,
			Attempt:       status.Attempt,
			NextAttemptAt: status.NextAttemptAt,
			UpdatedAt:     status.UpdatedAt,
		})
	}

	return result, nil
}

// projectTUIMessageDeliveryState maps an application delivery state onto the
// TUI contract.
//
// The switch is exhaustive on purpose: a direct type conversion would let a
// new application state reach the TUI unchecked.
func projectTUIMessageDeliveryState(
	state MessageDeliveryState,
) (tui.MessageDeliveryState, error) {
	switch state {
	case MessageDeliveryQueued:
		return tui.MessageDeliveryQueued, nil

	case MessageDeliverySending:
		return tui.MessageDeliverySending, nil

	case MessageDeliveryRetrying:
		return tui.MessageDeliveryRetrying, nil

	case MessageDeliveryFailed:
		return tui.MessageDeliveryFailed, nil

	case MessageDeliveryUncertain:
		return tui.MessageDeliveryUncertain, nil

	case MessageDeliverySent:
		return tui.MessageDeliverySent, nil

	case MessageDeliveryCanceled:
		return tui.MessageDeliveryCanceled, nil

	default:
		return "", fmt.Errorf(
			"unsupported application message delivery state %q",
			state,
		)
	}
}

var _ tui.MessageStatusSource = (*tuiMessageStatusSourceAdapter)(nil)

package application

import (
	"fmt"

	"telecli/internal/outbox"
)

func projectMessageState(
	state outbox.State,
) (MessageDeliveryState, error) {
	switch state {
	case outbox.StateQueued:
		return MessageDeliveryQueued, nil
	case outbox.StateDispatching:
		return MessageDeliverySending, nil
	case outbox.StateAccepted:
		return MessageDeliverySent, nil
	case outbox.StateFailedRetryable:
		return MessageDeliveryRetrying, nil
	case outbox.StateFailedPermanent:
		return MessageDeliveryFailed, nil
	case outbox.StateUncertain:
		return MessageDeliveryUncertain, nil
	case outbox.StateCanceled:
		return MessageDeliveryCanceled, nil
	default:
		return "", fmt.Errorf(
			"project outbox state %q: unsupported state",
			state,
		)
	}
}

func projectMessageStatus(
	entry outbox.Entry,
) (MessageStatus, error) {
	state, err := projectMessageState(entry.State)
	if err != nil {
		return MessageStatus{}, fmt.Errorf(
			"project status for entry %q: %w",
			entry.ID,
			err,
		)
	}

	return MessageStatus{
		EntryID:       string(entry.ID),
		AccountKey:    entry.AccountKey,
		ChatID:        entry.ChatID,
		State:         state,
		Attempt:       entry.AttemptCount,
		NextAttemptAt: entry.NextAttempt,
		UpdatedAt:     entry.UpdatedAt,
	}, nil
}

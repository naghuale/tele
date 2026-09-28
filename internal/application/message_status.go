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
		// TDLib took the message and Telegram has not confirmed it yet.
		// The identifier it holds is temporary and no history page will
		// ever contain it, so the message is still on its way and is
		// drawn as such: saying "Sent" here would be reporting Telegram's
		// answer before Telegram gave one.
		return MessageDeliverySending, nil
	case outbox.StateSent:
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
		MessageID:     entry.TelegramMessageID,
		Attempt:       entry.AttemptCount,
		NextAttemptAt: entry.NextAttempt,
		UpdatedAt:     entry.UpdatedAt,
	}, nil
}

func projectEntryStatus(
	status outbox.EntryStatus,
) (MessageStatus, error) {
	state, err := projectMessageState(status.State)
	if err != nil {
		return MessageStatus{}, fmt.Errorf(
			"project status for entry %q: %w",
			status.ID,
			err,
		)
	}

	return MessageStatus{
		EntryID:       status.ID,
		AccountKey:    status.AccountKey,
		ChatID:        status.ChatID,
		State:         state,
		MessageID:     status.TelegramMessageID,
		Attempt:       status.Attempt,
		NextAttemptAt: status.NextAttemptAt,
		UpdatedAt:     status.UpdatedAt,
	}, nil
}

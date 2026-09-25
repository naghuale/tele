package tui

import (
	"context"
	"time"
)

// MessageDeliveryState is the TUI-facing delivery state.
//
// It is intentionally local to the TUI package. Production composition
// adapts the application status model to this contract, avoiding a package
// dependency cycle.
type MessageDeliveryState string

const (
	// MessageDeliveryQueued means that the message is durably stored and is
	// waiting for dispatch. It does not mean that Telegram accepted it.
	MessageDeliveryQueued MessageDeliveryState = "queued"

	// MessageDeliverySending means that the durable dispatcher currently owns
	// the entry for an active delivery attempt.
	MessageDeliverySending MessageDeliveryState = "sending"

	// MessageDeliveryRetrying means that the previous attempt failed with a
	// retryable result and another attempt is scheduled.
	MessageDeliveryRetrying MessageDeliveryState = "retrying"

	// MessageDeliveryFailed means that delivery failed permanently.
	MessageDeliveryFailed MessageDeliveryState = "failed"

	// MessageDeliveryUncertain means that the sender cannot determine whether
	// Telegram accepted the message. Retrying may create a duplicate.
	MessageDeliveryUncertain MessageDeliveryState = "uncertain"

	// MessageDeliverySent means that Telegram accepted the message.
	MessageDeliverySent MessageDeliveryState = "sent"

	// MessageDeliveryCanceled means that the durable entry was canceled before
	// successful delivery.
	MessageDeliveryCanceled MessageDeliveryState = "canceled"
)

// MessageStatus is a payload-free TUI projection of delivery metadata.
//
// It intentionally excludes message text, encrypted payload, provider error
// messages, cipher metadata, and transport-specific details.
type MessageStatus struct {
	EntryID       string
	AccountKey    string
	ChatID        int64
	State         MessageDeliveryState
	Attempt       int
	NextAttemptAt time.Time
	UpdatedAt     time.Time
}

// MessageStatusSource provides payload-free delivery statuses for one account
// and chat.
//
// Direct delivery mode may provide no source. A nil source disables durable
// status polling.
type MessageStatusSource interface {
	ListMessageStatuses(
		ctx context.Context,
		accountKey string,
		chatID int64,
	) ([]MessageStatus, error)
}

// IsKnown reports whether s is one of the supported TUI delivery states.
func (s MessageDeliveryState) IsKnown() bool {
	switch s {
	case MessageDeliveryQueued,
		MessageDeliverySending,
		MessageDeliveryRetrying,
		MessageDeliveryFailed,
		MessageDeliveryUncertain,
		MessageDeliverySent,
		MessageDeliveryCanceled:
		return true

	default:
		return false
	}
}

// IsTerminal reports whether no further automatic delivery transition is
// expected for the state.
//
// Uncertain is terminal for automatic processing because retrying the same
// entry could create a duplicate.
func (s MessageDeliveryState) IsTerminal() bool {
	switch s {
	case MessageDeliveryFailed,
		MessageDeliveryUncertain,
		MessageDeliverySent,
		MessageDeliveryCanceled:
		return true

	case MessageDeliveryQueued,
		MessageDeliverySending,
		MessageDeliveryRetrying:
		return false

	default:
		return false
	}
}

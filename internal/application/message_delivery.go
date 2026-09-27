package application

import (
	"context"
	"errors"
	"time"
)

var (
	ErrMessageDeliveryUnavailable = errors.New("message delivery runtime is unavailable")
	ErrMessageStatusUnavailable   = errors.New("message status is unavailable")

	// ErrPendingMessageUnavailable is returned when the timeline asks for
	// the outgoing messages that are not in the history yet and there is
	// nothing to ask.
	ErrPendingMessageUnavailable = errors.New(
		"pending messages are unavailable",
	)
)

type MessageDeliveryState string

const (
	MessageDeliveryQueued    MessageDeliveryState = "queued"
	MessageDeliverySending   MessageDeliveryState = "sending"
	MessageDeliveryRetrying  MessageDeliveryState = "retrying"
	MessageDeliveryFailed    MessageDeliveryState = "failed"
	MessageDeliveryUncertain MessageDeliveryState = "uncertain"
	MessageDeliverySent      MessageDeliveryState = "sent"
	MessageDeliveryCanceled  MessageDeliveryState = "canceled"
)

type MessageSubmission struct {
	ID    string
	State MessageDeliveryState
}

type MessageStatus struct {
	EntryID       string
	AccountKey    string
	ChatID        int64
	State         MessageDeliveryState
	Attempt       int
	NextAttemptAt time.Time
	UpdatedAt     time.Time
}

type ComposerMessageSubmitter interface {
	SubmitMessage(
		ctx context.Context,
		accountKey string,
		chatID int64,
		text string,
	) (MessageSubmission, error)
}

type MessageStatusSource interface {
	ListMessageStatuses(
		ctx context.Context,
		accountKey string,
		chatID int64,
	) ([]MessageStatus, error)
}

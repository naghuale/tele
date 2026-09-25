package application

import (
	"context"
	"errors"

	"telecli/internal/outbox"
)

// ErrMessageDeliveryHealthUnavailable reports that durable delivery health
// metadata cannot be read right now.
//
// Direct delivery mode has no durable health source at all. A durable health
// read is available only while the runtime is running or stopping, because
// the counters come from the open outbox store.
var ErrMessageDeliveryHealthUnavailable = errors.New(
	"message delivery health is unavailable",
)

// MessageDeliveryHealthState describes the lifecycle of the durable message
// delivery runtime.
type MessageDeliveryHealthState string

const (
	// MessageDeliveryHealthStarting means that the runtime is being built and
	// the dispatcher has not been started yet.
	MessageDeliveryHealthStarting MessageDeliveryHealthState = "starting"

	// MessageDeliveryHealthRunning means that the runtime is available and the
	// dispatcher is supervised.
	MessageDeliveryHealthRunning MessageDeliveryHealthState = "running"

	// MessageDeliveryHealthStopping means that shutdown has started, new
	// submissions are unavailable, and the dispatcher or store is still being
	// closed.
	MessageDeliveryHealthStopping MessageDeliveryHealthState = "stopping"

	// MessageDeliveryHealthStopped means that the runtime completed a normal
	// shutdown.
	MessageDeliveryHealthStopped MessageDeliveryHealthState = "stopped"

	// MessageDeliveryHealthFailed means that the dispatcher stopped
	// unexpectedly or that runtime shutdown failed.
	MessageDeliveryHealthFailed MessageDeliveryHealthState = "failed"
)

// IsKnown reports whether s is a supported lifecycle state.
func (s MessageDeliveryHealthState) IsKnown() bool {
	switch s {
	case MessageDeliveryHealthStarting,
		MessageDeliveryHealthRunning,
		MessageDeliveryHealthStopping,
		MessageDeliveryHealthStopped,
		MessageDeliveryHealthFailed:
		return true

	default:
		return false
	}
}

// IsTerminal reports whether the runtime cannot return to running.
func (s MessageDeliveryHealthState) IsTerminal() bool {
	switch s {
	case MessageDeliveryHealthStopped,
		MessageDeliveryHealthFailed:
		return true

	case MessageDeliveryHealthStarting,
		MessageDeliveryHealthRunning,
		MessageDeliveryHealthStopping:
		return false

	default:
		return false
	}
}

// MessageDeliveryHealth carries the runtime lifecycle state and aggregate
// payload-free outbox counters.
//
// It intentionally excludes entry identifiers, account keys, chat IDs,
// database identity, payload data, lease ownership, transport errors and
// provider details. Consumers that need the failure reason read runtime.Err
// or the Close result instead.
type MessageDeliveryHealth struct {
	State MessageDeliveryHealthState

	Queued          int64
	Dispatching     int64
	Accepted        int64
	FailedRetryable int64
	FailedPermanent int64
	Uncertain       int64
	Canceled        int64
}

// MessageDeliveryHealthSource exposes runtime lifecycle state and aggregate
// payload-free durable outbox metadata.
//
// State stays readable after the store is closed, so a terminal lifecycle is
// still observable. ReadMessageDeliveryHealth needs a readable store and is
// therefore available only while the runtime is running or stopping.
//
// Lifecycle state and store counters are an observational pair, not one atomic
// transaction: a read may observe a lifecycle state together with counters
// captured just before the store was closed.
type MessageDeliveryHealthSource interface {
	State() MessageDeliveryHealthState

	ReadMessageDeliveryHealth(
		ctx context.Context,
	) (MessageDeliveryHealth, error)
}

func projectOperationalSnapshot(
	state MessageDeliveryHealthState,
	snapshot outbox.OperationalSnapshot,
) MessageDeliveryHealth {
	return MessageDeliveryHealth{
		State:           state,
		Queued:          snapshot.Queued,
		Dispatching:     snapshot.Dispatching,
		Accepted:        snapshot.Accepted,
		FailedRetryable: snapshot.FailedRetryable,
		FailedPermanent: snapshot.FailedPermanent,
		Uncertain:       snapshot.Uncertain,
		Canceled:        snapshot.Canceled,
	}
}

// validMessageDeliveryHealthTransition reports whether a lifecycle transition
// is allowed.
//
// Repeating the current state is allowed so that repeated lifecycle signals
// stay idempotent. Terminal states never return to a non-terminal one.
func validMessageDeliveryHealthTransition(
	from MessageDeliveryHealthState,
	to MessageDeliveryHealthState,
) bool {
	if !from.IsKnown() || !to.IsKnown() {
		return false
	}
	if from == to {
		return true
	}

	switch from {
	case MessageDeliveryHealthStarting:
		return to == MessageDeliveryHealthRunning ||
			to == MessageDeliveryHealthFailed

	case MessageDeliveryHealthRunning:
		return to == MessageDeliveryHealthStopping ||
			to == MessageDeliveryHealthFailed

	case MessageDeliveryHealthStopping:
		return to == MessageDeliveryHealthStopped ||
			to == MessageDeliveryHealthFailed

	case MessageDeliveryHealthStopped,
		MessageDeliveryHealthFailed:
		return false

	default:
		return false
	}
}

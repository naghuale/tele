package application

import (
	"context"
	"errors"
	"fmt"

	"telecli/internal/outbox"
)

// durableMessageDeliveryHealthSource observes the durable runtime lifecycle
// and the aggregate outbox counters.
//
// It is read-only: it never closes the runtime, never cancels the dispatcher
// and never triggers a delivery transition. The runtime lock is released
// before the store is queried, so a health read can never block Close or
// invert lock order.
type durableMessageDeliveryHealthSource struct {
	runtime *DurableOutboxRuntime
	reader  outbox.OperationalSnapshotReader
}

func (s *durableMessageDeliveryHealthSource) State() MessageDeliveryHealthState {
	if s == nil || s.runtime == nil {
		return ""
	}
	return s.runtime.messageDeliveryHealthState()
}

func (s *durableMessageDeliveryHealthSource) ReadMessageDeliveryHealth(
	ctx context.Context,
) (MessageDeliveryHealth, error) {
	if s == nil || s.runtime == nil || s.reader == nil {
		return MessageDeliveryHealth{}, ErrMessageDeliveryHealthUnavailable
	}
	if ctx == nil {
		return MessageDeliveryHealth{}, errors.New(
			"message delivery health: context is required",
		)
	}
	if err := ctx.Err(); err != nil {
		return MessageDeliveryHealth{}, err
	}

	// Counters come from the open store, so a read is only meaningful while
	// the runtime is running or stopping.
	state := s.runtime.messageDeliveryHealthState()
	if state != MessageDeliveryHealthRunning &&
		state != MessageDeliveryHealthStopping {
		return MessageDeliveryHealth{}, ErrMessageDeliveryHealthUnavailable
	}

	snapshot, err := s.reader.ReadOperationalSnapshot(ctx)
	if err != nil {
		return MessageDeliveryHealth{}, fmt.Errorf(
			"read outbox operational snapshot: %w",
			err,
		)
	}

	// The lifecycle state is read again on purpose: a read that started while
	// running may finish after shutdown began.
	return projectOperationalSnapshot(
		s.runtime.messageDeliveryHealthState(),
		snapshot,
	), nil
}

var _ MessageDeliveryHealthSource = (*durableMessageDeliveryHealthSource)(nil)

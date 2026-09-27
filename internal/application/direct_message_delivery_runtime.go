package application

import "errors"

type DirectMessageDeliveryRuntime struct {
	submitter ComposerMessageSubmitter
}

func NewDirectMessageDeliveryRuntime(
	submitter ComposerMessageSubmitter,
) (*DirectMessageDeliveryRuntime, error) {
	if submitter == nil {
		return nil, errors.New("direct message submitter is required")
	}
	return &DirectMessageDeliveryRuntime{submitter: submitter}, nil
}

func (r *DirectMessageDeliveryRuntime) Submitter() ComposerMessageSubmitter {
	if r == nil {
		return nil
	}
	return r.submitter
}

// StatusSource reports that direct submission has no durable status to query.
func (r *DirectMessageDeliveryRuntime) StatusSource() MessageStatusSource {
	return nil
}

// PendingMessages reports that direct submission has no queue: a message is
// in the history as soon as TDLib has it, so nothing is ever pending.
func (r *DirectMessageDeliveryRuntime) PendingMessages() PendingMessageSource {
	return nil
}

// CancelSubmitter reports that direct submission has nothing to cancel: a
// message is in the history as soon as it is sent, and there is no record
// left to name.
func (r *DirectMessageDeliveryRuntime) CancelSubmitter() MessageSubmitter {
	return nil
}

// HealthSource reports that direct submission has no durable runtime health.
func (r *DirectMessageDeliveryRuntime) HealthSource() MessageDeliveryHealthSource {
	return nil
}

func (r *DirectMessageDeliveryRuntime) Done() <-chan struct{} {
	return nil
}

func (r *DirectMessageDeliveryRuntime) Err() error {
	return nil
}

func (r *DirectMessageDeliveryRuntime) Close() error {
	return nil
}

var _ MessageDeliveryRuntime = (*DirectMessageDeliveryRuntime)(nil)

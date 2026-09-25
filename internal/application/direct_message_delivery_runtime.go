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

package application

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
)

type DurableComposerSubmitter struct {
	submitter MessageSubmitter
	available *atomic.Bool
}

func NewDurableComposerSubmitter(
	submitter MessageSubmitter,
	available *atomic.Bool,
) (*DurableComposerSubmitter, error) {
	if submitter == nil {
		return nil, errors.New("durable message submitter is required")
	}

	return &DurableComposerSubmitter{
		submitter: submitter,
		available: available,
	}, nil
}

func (s *DurableComposerSubmitter) SubmitMessage(
	ctx context.Context,
	_ string,
	chatID int64,
	text string,
) (MessageSubmission, error) {
	if s == nil || s.submitter == nil {
		return MessageSubmission{}, ErrMessageDeliveryUnavailable
	}
	if err := ctx.Err(); err != nil {
		return MessageSubmission{}, err
	}
	if s.available != nil && !s.available.Load() {
		return MessageSubmission{}, ErrMessageDeliveryUnavailable
	}

	entry, err := s.submitter.QueueMessage(ctx, chatID, text)
	if err != nil {
		return MessageSubmission{}, fmt.Errorf("queue message: %w", err)
	}

	return MessageSubmission{
		ID:    string(entry.ID),
		State: MessageDeliveryQueued,
	}, nil
}

var _ ComposerMessageSubmitter = (*DurableComposerSubmitter)(nil)

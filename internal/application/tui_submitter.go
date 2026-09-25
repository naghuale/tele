package application

import (
	"context"
	"errors"
	"strings"

	"telecli/internal/tui"
)

type TUISubmitter struct {
	delegate   ComposerMessageSubmitter
	accountKey string
}

func NewTUISubmitter(
	delegate ComposerMessageSubmitter,
	accountKey string,
) (*TUISubmitter, error) {
	if delegate == nil {
		return nil, errors.New("message submitter is required")
	}
	if strings.TrimSpace(accountKey) == "" {
		return nil, errors.New("message account key is required")
	}
	return &TUISubmitter{
		delegate:   delegate,
		accountKey: accountKey,
	}, nil
}

func (s *TUISubmitter) SubmitMessage(
	ctx context.Context,
	chatID int64,
	text string,
) (tui.Submission, error) {
	if s == nil || s.delegate == nil {
		return tui.Submission{}, ErrMessageDeliveryUnavailable
	}
	if err := ctx.Err(); err != nil {
		return tui.Submission{}, err
	}

	submission, err := s.delegate.SubmitMessage(
		ctx,
		s.accountKey,
		chatID,
		text,
	)
	if err != nil {
		return tui.Submission{}, err
	}
	return tui.Submission{
		ID:    submission.ID,
		State: tui.SubmissionState(submission.State),
	}, nil
}

var _ tui.ComposerSubmitter = (*TUISubmitter)(nil)

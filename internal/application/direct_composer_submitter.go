package application

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"telecli/internal/telegram"
)

type DirectComposerSubmitter struct {
	sender TelegramSender
}

func NewDirectComposerSubmitter(
	sender TelegramSender,
) (*DirectComposerSubmitter, error) {
	if sender == nil {
		return nil, errors.New("direct message sender is required")
	}
	return &DirectComposerSubmitter{sender: sender}, nil
}

func (s *DirectComposerSubmitter) SubmitMessage(
	ctx context.Context,
	_ string,
	chatID int64,
	text string,
) (MessageSubmission, error) {
	if s == nil || s.sender == nil {
		return MessageSubmission{}, ErrMessageDeliveryUnavailable
	}
	if err := ctx.Err(); err != nil {
		return MessageSubmission{}, err
	}

	message, err := s.sender.SendTextMessage(
		ctx,
		telegram.ChatID(chatID),
		text,
	)
	if err != nil {
		return MessageSubmission{}, fmt.Errorf("send direct message: %w", err)
	}

	return MessageSubmission{
		ID:    strconv.FormatInt(int64(message.ID), 10),
		State: MessageDeliverySent,
	}, nil
}

var _ ComposerMessageSubmitter = (*DirectComposerSubmitter)(nil)

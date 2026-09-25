package application

import (
	"context"
	"errors"

	"telecli/internal/outbox"
	"telecli/internal/telegram"
)

// TelegramSender is the narrow contract used by TelegramOutboxSender.
//
// It is satisfied by *telegram.AuthorizedSession. The adapter exists
// so internal/outbox stays transport-neutral: outbox.Sender is the
// dispatcher-facing contract, and this package translates Telegram
// results and errors into outbox types.
type TelegramSender interface {
	SendTextMessage(
		ctx context.Context,
		chatID telegram.ChatID,
		text string,
	) (telegram.Message, error)
}

// TelegramOutboxSender adapts a TelegramSender to outbox.Sender.
//
// The adapter:
//
//   - forwards chat id and text unchanged;
//   - maps telegram.Message to outbox.SentMessage;
//   - translates *telegram.TDLibError into *outbox.SendError with only
//     the numeric code, dropping TDLibError.Message (transport-
//     provided free text) so it cannot reach the store or logs;
//   - passes context errors and other non-TDLib errors through
//     unchanged, so outbox.Classify classifies them conservatively.
type TelegramOutboxSender struct {
	sender TelegramSender
}

// NewTelegramOutboxSender wires an adapter.
func NewTelegramOutboxSender(sender TelegramSender) *TelegramOutboxSender {
	return &TelegramOutboxSender{sender: sender}
}

// SendMessage implements outbox.Sender.
func (s *TelegramOutboxSender) SendMessage(
	ctx context.Context,
	chatID int64,
	text string,
) (outbox.SentMessage, error) {
	if s == nil || s.sender == nil {
		return outbox.SentMessage{}, errors.New(
			"telegram outbox sender: nil sender",
		)
	}

	message, err := s.sender.SendTextMessage(
		ctx,
		telegram.ChatID(chatID),
		text,
	)
	if err != nil {
		return outbox.SentMessage{}, translateTelegramSendError(err)
	}

	return outbox.SentMessage{
		ID:     int64(message.ID),
		ChatID: int64(message.ChatID),
	}, nil
}

// translateTelegramSendError maps transport-specific errors into
// outbox.SendError, preserving only the numeric code.
//
// The TDLibError.Message field is intentionally not copied: it is
// transport-provided free text and outbox persists only short,
// privacy-safe reasons through outbox.SafeReason.
//
// Context cancellation, generic transport errors, and any other non-
// TDLib error are returned unchanged so outbox.Classify applies its
// conservative default.
//
// A typed-nil *telegram.TDLibError is not converted: it carries no
// usable code and must not become a *outbox.SendError.
func translateTelegramSendError(err error) error {
	if err == nil {
		return nil
	}

	var tdErr *telegram.TDLibError
	if errors.As(err, &tdErr) && tdErr != nil {
		return &outbox.SendError{Code: tdErr.Code}
	}

	return err
}

// Compile-time assertion.
var _ outbox.Sender = (*TelegramOutboxSender)(nil)

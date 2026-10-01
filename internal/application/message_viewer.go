package application

import (
	"context"

	"telecli/internal/telegram"
	"telecli/internal/tui"
)

// TelegramMessageViewer marks the messages of the window the user is
// looking at as read in Telegram.
//
// TDLib does not know what is on a terminal screen. It knows which chat has
// been opened and how far the read pointer of that chat has moved, so the
// interface says what it is drawing and this adapter is the one place that
// turns that into a query.
//
// It is best effort, exactly like the presence opener next to it: a chat
// whose messages could not be marked read still shows them, and a counter
// that does not fall is a counter TDLib reports. The errors are the
// caller's to log, because a TDLib error message is not interface text.
type TelegramMessageViewer struct {
	session TelegramMessageViewing
}

// TelegramMessageViewing is the part of a session the viewer needs.
//
// It is satisfied by *telegram.AuthorizedSession. The interface exists so
// unit tests can drive the adapter without a real session.
type TelegramMessageViewing interface {
	ViewMessages(
		ctx context.Context,
		chatID telegram.ChatID,
		messageIDs []telegram.MessageID,
	) error
}

// ViewMessages implements tui.MessageViewer.
func (v *TelegramMessageViewer) ViewMessages(
	ctx context.Context,
	chatID int64,
	messageIDs []int64,
) error {
	if v == nil || v.session == nil {
		return nil
	}

	ids := make([]telegram.MessageID, 0, len(messageIDs))
	for _, id := range messageIDs {
		ids = append(ids, telegram.MessageID(id))
	}

	return v.session.ViewMessages(ctx, telegram.ChatID(chatID), ids)
}

var _ tui.MessageViewer = (*TelegramMessageViewer)(nil)

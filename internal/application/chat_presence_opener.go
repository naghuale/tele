package application

import (
	"context"

	"telecli/internal/telegram"
	"telecli/internal/tui"
)

// TelegramChatPresenceOpener tells TDLib which chat the user is looking at.
//
// TDLib only counts the online members of a chat that has been opened
// (td_api.tl:10613), and it keeps counting a chat that stays open. So the
// interface says when it opens a conversation and when it leaves one, and
// this adapter is the only place that turns that into a query.
//
// The two calls are best effort and the errors are the caller's to log: a
// chat that cannot be opened still shows its messages, and a presence that
// does not arrive is a presence the header does not draw.
type TelegramChatPresenceOpener struct {
	session TelegramChatLifecycle
}

// TelegramChatLifecycle is the part of a session the opener needs.
type TelegramChatLifecycle interface {
	OpenChat(ctx context.Context, chatID telegram.ChatID) error
	CloseChat(ctx context.Context, chatID telegram.ChatID) error
}

// OpenChat implements tui.ChatPresenceOpener.
func (o *TelegramChatPresenceOpener) OpenChat(
	ctx context.Context,
	chatID int64,
) error {
	if o == nil || o.session == nil {
		return nil
	}

	return o.session.OpenChat(ctx, telegram.ChatID(chatID))
}

// CloseChat implements tui.ChatPresenceOpener.
func (o *TelegramChatPresenceOpener) CloseChat(
	ctx context.Context,
	chatID int64,
) error {
	if o == nil || o.session == nil {
		return nil
	}

	return o.session.CloseChat(ctx, telegram.ChatID(chatID))
}

var _ tui.ChatPresenceOpener = (*TelegramChatPresenceOpener)(nil)

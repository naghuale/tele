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
		source telegram.MessageSource,
	) error
}

// ViewMessages implements tui.MessageViewer.
//
// The kind of the chat is not carried along: it is turned into the source
// TDLib is given, which is the one decision that depends on it. A chat with
// one other person is read in itself and a chat with many is read in a
// window of its history, and a read that names the wrong one of those two is
// a read TDLib refuses — which is how a channel's 89 unread stayed at 89 on
// the owner's account.
func (v *TelegramMessageViewer) ViewMessages(
	ctx context.Context,
	chatID int64,
	kind tui.ChatKind,
	messageIDs []int64,
) error {
	if v == nil || v.session == nil {
		return nil
	}

	ids := make([]telegram.MessageID, 0, len(messageIDs))
	highest := telegram.MessageID(0)
	for _, id := range messageIDs {
		converted := telegram.MessageID(id)
		ids = append(ids, converted)
		if converted > highest {
			highest = converted
		}
	}

	source := telegram.MessageSourceFor(viewedChatKind(kind), highest)

	return v.session.ViewMessages(ctx, telegram.ChatID(chatID), ids, source)
}

// viewedChatKind is the TDLib kind of a chat the interface is reading.
//
// The kind telecli keeps is three words and the kind TDLib keeps is three
// @types, and the middle one differs: telecli has no channel of its own,
// because a channel is a supergroup that says so, and a chat that is neither
// a private chat nor a supergroup is a basic group whatever it was before.
func viewedChatKind(kind tui.ChatKind) telegram.ChatKind {
	switch kind {
	case tui.ChatKindChannel:
		return telegram.ChatKindSupergroup
	case tui.ChatKindGroup:
		return telegram.ChatKindGroup
	default:
		return telegram.ChatKindPrivate
	}
}

var _ tui.MessageViewer = (*TelegramMessageViewer)(nil)

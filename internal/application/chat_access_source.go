package application

import (
	"context"
	"errors"
	"fmt"

	"telecli/internal/telegram"
	"telecli/internal/tui"
)

// TelegramChatAccessSource reads whether this account can write in a chat.
//
// It reads the live store first and asks TDLib only for a chat the store has
// never been told about, which is the whole of the reason it is a source
// rather than a field of the chat list. The store is kept up to date by the
// updates TDLib sends, so the second and every later read of a chat is a
// read of memory, and a promotion that arrives while the program is running
// is a change of the value the next read returns. Asking TDLib on every poll
// instead would be a round trip every two seconds for an answer that is on
// the other end of a channel TDLib is holding anyway.

// TelegramChatAccessReader is the part of the session the read needs.
//
// It is a narrow interface rather than *telegram.AuthorizedSession so that
// the adapter can be proved without a TDLib, and so that the method it needs
// is named in one place.
type TelegramChatAccessReader interface {
	GetChatAccess(
		ctx context.Context,
		chatID telegram.ChatID,
	) (telegram.ChatAccess, error)
}

// TelegramChatAccessStore is the part of the live store the read needs.
type TelegramChatAccessStore interface {
	ChatAccess(chatID telegram.ChatID) (telegram.ChatAccess, bool)
}

// TelegramChatAccessSource adapts a session and its live store to the
// interface's question.
type TelegramChatAccessSource struct {
	reader TelegramChatAccessReader
	store  TelegramChatAccessStore
}

// NewTelegramChatAccessSource builds the source.
//
// The store may be nil, in which case every read is a read of TDLib: a
// program with no live store still refuses to offer a field in a channel
// this account cannot post in, it just asks again each time. The reader may
// not, because a source that cannot read anything would report an answer it
// never had.
func NewTelegramChatAccessSource(
	reader TelegramChatAccessReader,
	store TelegramChatAccessStore,
) (*TelegramChatAccessSource, error) {
	if reader == nil {
		return nil, errors.New("chat access source: reader is required")
	}

	return &TelegramChatAccessSource{reader: reader, store: store}, nil
}

// ReadChatAccess implements tui.ChatAccessSource.
func (s *TelegramChatAccessSource) ReadChatAccess(
	ctx context.Context,
	chatID int64,
) (tui.ChatAccess, error) {
	if s == nil || s.reader == nil {
		return tui.ChatAccess{}, errors.New("chat access source: no reader")
	}
	if chatID == 0 {
		return tui.ChatAccess{}, fmt.Errorf(
			"%w: no chat is open", telegram.ErrInvalidChatID,
		)
	}

	if s.store != nil {
		if access, known := s.store.ChatAccess(telegram.ChatID(chatID)); known {
			return projectChatAccess(access), nil
		}
	}

	access, err := s.reader.GetChatAccess(ctx, telegram.ChatID(chatID))
	if err != nil {
		return tui.ChatAccess{}, fmt.Errorf("read chat access: %w", err)
	}

	// The read has just put what it found into the store, so the store may
	// know more than the answer that came back: a promotion that arrived
	// while the read was on its way is already applied, and it is the newer
	// of the two.
	if s.store != nil {
		if known, isKnown := s.store.ChatAccess(telegram.ChatID(chatID)); isKnown {
			access = known
		}
	}

	return projectChatAccess(access), nil
}

// projectChatAccess translates the answer of the binding into the words the
// interface draws.
//
// Every reason is named here and nowhere else. A reason the interface has no
// sentence for is a read-only line with a wrong sentence on it, and the
// binding growing a reason is a change this table has to be shown rather
// than a line that says something else.
func projectChatAccess(access telegram.ChatAccess) tui.ChatAccess {
	if access.CanSend {
		return tui.ChatAccess{CanSend: true}
	}

	switch access.Reason {
	case telegram.ChatAccessChannelReadOnly:
		return tui.ChatAccess{Blocked: tui.ChatBlockedChannel}
	case telegram.ChatAccessGroupRestricted:
		return tui.ChatAccess{Blocked: tui.ChatBlockedGroup}
	case telegram.ChatAccessPeerUnreachable:
		return tui.ChatAccess{Blocked: tui.ChatBlockedPeer}
	case telegram.ChatAccessNotAMember:
		return tui.ChatAccess{Blocked: tui.ChatBlockedNotMember}
	case telegram.ChatAccessBanned:
		return tui.ChatAccess{Blocked: tui.ChatBlockedBanned}
	case telegram.ChatAccessNone:
		// No reason is a chat that can be written in. The binding answers
		// it that way too, and a `CanSend` of false with nothing to say is
		// a shape no caller here produces.
		return tui.ChatAccess{CanSend: true}
	}

	// A reason with no sentence is not drawn as one of the reasons there
	// are. The composer disappears, which is the safe direction: a field
	// that cannot be typed in is a worse thing than a line that says less
	// than it could.
	return tui.ChatAccess{Blocked: tui.ChatBlockedGroup}
}

var _ tui.ChatAccessSource = (*TelegramChatAccessSource)(nil)

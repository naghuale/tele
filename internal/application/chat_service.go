package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"telecli/internal/telegram"
	"telecli/internal/tui"
)

const (
	defaultChatListLimit = 50
	defaultHistoryLimit  = 50

	// chatServiceTimeout bounds a single chat service call. Without it,
	// a TDLib query that never produces a response would keep the
	// tea.Cmd goroutine alive until the session is closed.
	chatServiceTimeout = 60 * time.Second
)

// TelegramChats is the narrow dependency used by TelegramChatService.
//
// It is satisfied by *telegram.AuthorizedSession. The interface exists
// so unit tests can drive the adapter without a real session.
type TelegramChats interface {
	GetChats(ctx context.Context, limit int) (telegram.ChatListSnapshot, error)
	GetChatHistory(
		ctx context.Context,
		chatID telegram.ChatID,
		fromMessageID telegram.MessageID,
		limit int,
	) (telegram.HistoryPage, error)
	SendTextMessage(
		ctx context.Context,
		chatID telegram.ChatID,
		text string,
	) (telegram.Message, error)
}

// TelegramChatService adapts a TelegramChats source to tui.ChatSource.
type TelegramChatService struct {
	chats        TelegramChats
	listLimit    int
	historyLimit int
}

// NewTelegramChatService builds a chat service over the given source.
func NewTelegramChatService(chats TelegramChats) *TelegramChatService {
	return &TelegramChatService{
		chats:        chats,
		listLimit:    defaultChatListLimit,
		historyLimit: defaultHistoryLimit,
	}
}

// ListChats implements tui.ChatSource.
func (s *TelegramChatService) ListChats(ctx context.Context) ([]tui.Chat, error) {
	if s == nil || s.chats == nil {
		return nil, errors.New("chat service: nil source")
	}

	ctx, cancel := context.WithTimeout(ctx, chatServiceTimeout)
	defer cancel()

	snapshot, err := s.chats.GetChats(ctx, s.listLimit)
	if err != nil {
		return nil, fmt.Errorf("list chats: %w", err)
	}

	out := make([]tui.Chat, 0, len(snapshot.Chats))
	for _, c := range snapshot.Chats {
		out = append(out, tui.Chat{
			ID:      int64(c.ID),
			Title:   c.Title,
			Unread:  c.UnreadCount,
			Preview: c.LastMessageText,
		})
	}
	return out, nil
}

// LoadHistory implements tui.ChatSource.
//
// The TDLib-level limit is clamped to (0, 100]; when the caller passes
// a non-positive or out-of-range value, the service substitutes its
// default history limit.
func (s *TelegramChatService) LoadHistory(
	ctx context.Context,
	chatID int64,
	fromMessageID int64,
	limit int,
) (tui.HistoryPage, error) {
	if s == nil || s.chats == nil {
		return tui.HistoryPage{}, errors.New("chat service: nil source")
	}
	if limit <= 0 || limit > 100 {
		limit = s.historyLimit
	}

	ctx, cancel := context.WithTimeout(ctx, chatServiceTimeout)
	defer cancel()

	page, err := s.chats.GetChatHistory(
		ctx,
		telegram.ChatID(chatID),
		telegram.MessageID(fromMessageID),
		limit,
	)
	if err != nil {
		return tui.HistoryPage{}, fmt.Errorf("load history: %w", err)
	}

	out := make([]tui.Message, 0, len(page.Messages))
	for _, m := range page.Messages {
		out = append(out, tui.Message{
			ID:       int64(m.ID),
			Outgoing: m.Outgoing,
			Text:     m.Text,
			Time:     m.Timestamp.Format("15:04"),
		})
	}
	return tui.HistoryPage{
		Messages: out,
		NextFrom: int64(page.NextFrom),
		HasMore:  page.HasMore,
	}, nil
}

// SendMessage implements tui.ChatSource.
//
// The TDLib-level validation of the chat ID and message text is
// performed by SendTextMessage. This adapter bounds the call duration
// and maps the Telegram message projection into the TUI projection.
func (s *TelegramChatService) SendMessage(
	ctx context.Context,
	chatID int64,
	text string,
) (tui.Message, error) {
	if s == nil || s.chats == nil {
		return tui.Message{}, errors.New("chat service: nil source")
	}

	ctx, cancel := context.WithTimeout(ctx, chatServiceTimeout)
	defer cancel()

	message, err := s.chats.SendTextMessage(
		ctx,
		telegram.ChatID(chatID),
		text,
	)
	if err != nil {
		return tui.Message{}, fmt.Errorf("send message: %w", err)
	}

	return tui.Message{
		ID:       int64(message.ID),
		Outgoing: message.Outgoing,
		Text:     message.Text,
		Time:     message.Timestamp.Format("15:04"),
	}, nil
}

// Compile-time assertion.
var _ tui.ChatSource = (*TelegramChatService)(nil)

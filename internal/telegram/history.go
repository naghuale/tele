package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Message is the minimal message projection used by the TUI.
//
// It carries who sent it and what it holds, because the interface draws
// both: a group has to name the person above every message, and a
// photograph has to say that it is a photograph and show the words under
// it. Neither is in the text, and neither is anybody else's business once
// the projection is dropped.
type Message struct {
	ID        MessageID
	ChatID    ChatID
	Outgoing  bool
	Timestamp time.Time
	Text      string

	// Sender is who sent it, as TDLib reports it: an identifier and a
	// kind. The name of that person is not here — see GetUserName and the
	// privacy claim of #41.
	Sender MessageSender

	// Media is the word the interface writes for what the message carries,
	// and Caption the words under it. Both are empty for a message that is
	// only text.
	Media   string
	Caption string

	// MediaAlbumID groups the parts of one album. Telegram sends an album
	// as consecutive messages that share it.
	MediaAlbumID int
}

// HistoryPage is one page of a chat's message history.
//
// Messages are returned in reverse chronological order, as TDLib
// produces them: newest first, oldest last.
//
// NextFrom is the ID of the last, oldest message in the page. It can
// be passed as fromMessageID for a subsequent request, but TDLib may
// return that boundary message again because GetChatHistory currently
// uses offset 0. Callers must de-duplicate messages by Message.ID when
// concatenating pages.
//
// NextFrom is zero for an empty page.
//
// HasMore is a heuristic. TDLib may return fewer messages than
// requested even when older messages exist.
type HistoryPage struct {
	Messages []Message
	NextFrom MessageID
	HasMore  bool
}

var (
	// ErrInvalidHistoryLimit is returned when a history page limit is
	// not in (0, maxHistoryLimit].
	ErrInvalidHistoryLimit = errors.New("telegram history: invalid limit")

	// ErrUnexpectedHistoryResponse is returned when TDLib returns a
	// response with the wrong @type, a message whose chat_id does not
	// match the request, or an entry that is not a message object.
	ErrUnexpectedHistoryResponse = errors.New("telegram history: unexpected response")
)

const (
	// maxHistoryLimit is TDLib's documented upper bound for
	// getChatHistory.
	maxHistoryLimit = 100
)

type getChatHistoryRequest struct {
	Type          string `json:"@type"`
	ChatID        int64  `json:"chat_id"`
	FromMessageID int64  `json:"from_message_id"`
	Offset        int    `json:"offset"`
	Limit         int    `json:"limit"`
	OnlyLocal     bool   `json:"only_local"`
}

type getChatHistoryResponse struct {
	Type       string              `json:"@type"`
	TotalCount int                 `json:"total_count"`
	Messages   []historyMessageRaw `json:"messages"`
}

type historyMessageRaw struct {
	Type         string          `json:"@type"`
	ID           int64           `json:"id"`
	ChatID       int64           `json:"chat_id"`
	IsOutgoing   bool            `json:"is_outgoing"`
	Date         int64           `json:"date"`
	Content      json.RawMessage `json:"content"`
	SenderID     json.RawMessage `json:"sender_id"`
	MediaAlbumID int             `json:"media_album_id"`
}

// GetChatHistory fetches one page of a chat's message history.
//
// Rules enforced locally:
//
//   - chatID == 0 is rejected. TDLib treats chat_id as int53 and does
//     not forbid negative chat IDs.
//   - limit must be in (0, 100]. TDLib may return fewer messages than
//     requested.
//   - fromMessageID == 0 requests the most recent page.
//   - a non-zero fromMessageID is an inclusive boundary because offset
//     is 0; callers must de-duplicate the boundary MessageID when
//     concatenating pages.
//   - only_local is false, allowing TDLib to fetch missing history.
func (s *AuthorizedSession) GetChatHistory(
	ctx context.Context,
	chatID ChatID,
	fromMessageID MessageID,
	limit int,
) (HistoryPage, error) {
	if chatID == 0 {
		return HistoryPage{}, fmt.Errorf(
			"%w: %d", ErrInvalidChatID, chatID,
		)
	}
	if limit <= 0 || limit > maxHistoryLimit {
		return HistoryPage{}, fmt.Errorf(
			"%w: %d", ErrInvalidHistoryLimit, limit,
		)
	}

	request, err := json.Marshal(getChatHistoryRequest{
		Type:          "getChatHistory",
		ChatID:        int64(chatID),
		FromMessageID: int64(fromMessageID),
		Offset:        0,
		Limit:         limit,
		OnlyLocal:     false,
	})
	if err != nil {
		return HistoryPage{}, fmt.Errorf("marshal getChatHistory: %w", err)
	}

	raw, err := s.Query(ctx, RawMessage(request))
	if err != nil {
		return HistoryPage{}, fmt.Errorf("getChatHistory: %w", err)
	}

	var response getChatHistoryResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return HistoryPage{}, fmt.Errorf(
			"decode getChatHistory response: %w", err,
		)
	}
	if response.Type != "messages" {
		return HistoryPage{}, fmt.Errorf(
			"%w: getChatHistory returned @type=%q",
			ErrUnexpectedHistoryResponse, response.Type,
		)
	}

	messages := make([]Message, 0, len(response.Messages))
	for _, m := range response.Messages {
		if m.Type != "message" {
			return HistoryPage{}, fmt.Errorf(
				"%w: history entry @type=%q",
				ErrUnexpectedHistoryResponse, m.Type,
			)
		}
		if m.ChatID != int64(chatID) {
			return HistoryPage{}, fmt.Errorf(
				"%w: getChatHistory requested chat_id=%d, message has chat_id=%d",
				ErrUnexpectedHistoryResponse, chatID, m.ChatID,
			)
		}
		media, caption := extractMedia(m.Content)

		messages = append(messages, Message{
			ID:           MessageID(m.ID),
			ChatID:       ChatID(m.ChatID),
			Outgoing:     m.IsOutgoing,
			Timestamp:    time.Unix(m.Date, 0).UTC(),
			Text:         extractMessageText(m.Content),
			Sender:       parseMessageSender(m.SenderID),
			Media:        media,
			Caption:      caption,
			MediaAlbumID: m.MediaAlbumID,
		})
	}

	page := HistoryPage{
		Messages: messages,
		HasMore:  len(messages) == limit,
	}
	if len(messages) > 0 {
		page.NextFrom = messages[len(messages)-1].ID
	}
	return page, nil
}

// parseMessageSender reads the sender of a message out of the raw object.
//
// A sender this build does not know is MessageSenderUnknown and not an
// error: TDLib adds kinds, and a message whose sender cannot be read is
// still a message the user wrote or received.
func parseMessageSender(raw json.RawMessage) MessageSender {
	if len(raw) == 0 || string(raw) == "null" {
		return MessageSender{}
	}

	var envelope messageEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return MessageSender{}
	}

	switch MessageSenderKind(envelope.Type) {
	case MessageSenderUser:
		var user struct {
			UserID int64 `json:"user_id"`
		}
		if err := json.Unmarshal(raw, &user); err != nil {
			return MessageSender{Kind: MessageSenderUser}
		}

		return MessageSender{Kind: MessageSenderUser, ID: user.UserID}

	case MessageSenderChat:
		var chat struct {
			ChatID int64 `json:"chat_id"`
		}
		if err := json.Unmarshal(raw, &chat); err != nil {
			return MessageSender{Kind: MessageSenderChat}
		}

		return MessageSender{Kind: MessageSenderChat, ID: chat.ChatID}

	default:
		return MessageSender{Kind: MessageSenderUnknown}
	}
}

// extractMessageText returns the message text for a message that is only
// text, and nothing at all for a message that carries a file.
//
// A placeholder such as "[photo]" used to stand in for the file; the
// interface now says what the message carries in its own words, through
// the Media field, and a message that is a picture has no text of its own.
func extractMessageText(content json.RawMessage) string {
	if len(content) == 0 || string(content) == "null" {
		return ""
	}

	var envelope messageEnvelope
	if err := json.Unmarshal(content, &envelope); err != nil {
		return ""
	}

	if envelope.Type != "messageText" {
		return ""
	}

	var contentText messageTextContentRaw
	if err := json.Unmarshal(content, &contentText); err == nil {
		return contentText.Text.Text
	}

	return ""
}

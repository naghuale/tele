package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Message is the minimal message projection used by the TUI.
type Message struct {
	ID        MessageID
	ChatID    ChatID
	Outgoing  bool
	Timestamp time.Time
	Text      string
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
	Type       string          `json:"@type"`
	ID         int64           `json:"id"`
	ChatID     int64           `json:"chat_id"`
	IsOutgoing bool            `json:"is_outgoing"`
	Date       int64           `json:"date"`
	Content    json.RawMessage `json:"content"`
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
		messages = append(messages, Message{
			ID:        MessageID(m.ID),
			ChatID:    ChatID(m.ChatID),
			Outgoing:  m.IsOutgoing,
			Timestamp: time.Unix(m.Date, 0).UTC(),
			Text:      extractMessageText(m.Content),
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

// extractMessageText returns the message text for a supported content
// type, or a short placeholder such as "[photo]" for the rest.
//
// The message ID is preserved by the caller for every content type;
// extractMessageText only decides what to display.
func extractMessageText(content json.RawMessage) string {
	if len(content) == 0 || string(content) == "null" {
		return ""
	}

	var envelope messageEnvelope
	if err := json.Unmarshal(content, &envelope); err != nil {
		return ""
	}

	if envelope.Type == "messageText" {
		var contentText messageTextContentRaw
		if err := json.Unmarshal(content, &contentText); err == nil {
			return contentText.Text.Text
		}
		return ""
	}

	return placeholderForContent(envelope.Type)
}

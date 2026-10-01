package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	// ErrEmptyMessageText is returned when the message contains no
	// non-whitespace characters.
	ErrEmptyMessageText = errors.New("telegram send: empty message text")

	// ErrUnexpectedSendResponse is returned when TDLib does not return
	// a message object matching the target chat, or returns a message
	// with a zero id.
	ErrUnexpectedSendResponse = errors.New("telegram send: unexpected response")
)

type sendMessageRequest struct {
	Type                string                  `json:"@type"`
	ChatID              int64                   `json:"chat_id"`
	TopicID             any                     `json:"topic_id"`
	ReplyTo             any                     `json:"reply_to"`
	Options             any                     `json:"options"`
	ReplyMarkup         any                     `json:"reply_markup"`
	InputMessageContent inputMessageTextRequest `json:"input_message_content"`
}

type inputMessageTextRequest struct {
	Type               string               `json:"@type"`
	Text               formattedTextRequest `json:"text"`
	LinkPreviewOptions any                  `json:"link_preview_options"`
	ClearDraft         bool                 `json:"clear_draft"`
}

type formattedTextRequest struct {
	Type     string `json:"@type"`
	Text     string `json:"text"`
	Entities []any  `json:"entities"`
}

type sendMessageResponse struct {
	Type       string          `json:"@type"`
	ID         tdInt           `json:"id"`
	ChatID     tdInt           `json:"chat_id"`
	IsOutgoing bool            `json:"is_outgoing"`
	Date       tdInt           `json:"date"`
	Content    json.RawMessage `json:"content"`
}

// SendTextMessage sends one plain-text message and returns the message
// object returned by TDLib.
//
// Scope of PR-07A:
//
//   - chatID == 0 is rejected. TDLib treats chat_id as int53 and does
//     not forbid negative chat IDs.
//   - empty or whitespace-only text is rejected before any request is
//     sent.
//   - text is passed verbatim; leading and trailing whitespace is
//     preserved.
//
// Replies, topics, scheduling, custom send options, reply markup, and
// formatting entities are intentionally omitted in this revision.
//
// TDLib's sendMessage response confirms acceptance of the request. It
// is not a delivery receipt: message.sending_state may remain non-zero
// until the corresponding updates arrive, which PR-07A does not yet
// track.
func (s *AuthorizedSession) SendTextMessage(
	ctx context.Context,
	chatID ChatID,
	text string,
) (Message, error) {
	if chatID == 0 {
		return Message{}, fmt.Errorf(
			"%w: %d", ErrInvalidChatID, chatID,
		)
	}
	if strings.TrimSpace(text) == "" {
		return Message{}, ErrEmptyMessageText
	}

	request, err := json.Marshal(sendMessageRequest{
		Type:        "sendMessage",
		ChatID:      int64(chatID),
		TopicID:     nil,
		ReplyTo:     nil,
		Options:     nil,
		ReplyMarkup: nil,
		InputMessageContent: inputMessageTextRequest{
			Type: "inputMessageText",
			Text: formattedTextRequest{
				Type:     "formattedText",
				Text:     text,
				Entities: []any{},
			},
			LinkPreviewOptions: nil,
			ClearDraft:         true,
		},
	})
	if err != nil {
		return Message{}, fmt.Errorf("marshal sendMessage: %w", err)
	}

	raw, err := s.Query(ctx, RawMessage(request))
	if err != nil {
		return Message{}, fmt.Errorf("sendMessage: %w", err)
	}

	var response sendMessageResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return Message{}, fmt.Errorf(
			"decode sendMessage response: %w", err,
		)
	}

	if response.Type != "message" {
		return Message{}, fmt.Errorf(
			"%w: %s",
			ErrUnexpectedSendResponse,
			unexpectedResponse("sendMessage", raw),
		)
	}
	if int64(response.ChatID) != int64(chatID) {
		return Message{}, fmt.Errorf(
			"%w: requested chat_id=%d, returned chat_id=%d for @extra=%q",
			ErrUnexpectedSendResponse, chatID, int64(response.ChatID),
			responseExtra(raw),
		)
	}
	if response.ID == 0 {
		return Message{}, fmt.Errorf(
			"%w: zero message id for @extra=%q",
			ErrUnexpectedSendResponse,
			responseExtra(raw),
		)
	}

	label := readContentLabel(response.Content)

	return Message{
		ID:          MessageID(response.ID),
		ChatID:      ChatID(response.ChatID),
		Outgoing:    response.IsOutgoing,
		Timestamp:   time.Unix(int64(response.Date), 0).UTC(),
		Text:        extractMessageText(response.Content),
		Media:       label.word,
		MediaDetail: label.detail,
		Caption:     label.caption,
		Service:     label.service,
	}, nil
}

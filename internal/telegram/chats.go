package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// ChatID is a TDLib chat identifier.
type ChatID int64

// MessageID is a TDLib message identifier.
type MessageID int64

// ChatSummary is the minimal chat projection used by the TUI.
type ChatSummary struct {
	ID              ChatID
	Title           string
	UnreadCount     int
	LastMessageID   MessageID
	LastMessageText string
}

// ChatListSnapshot is an ordered snapshot of the main chat list.
type ChatListSnapshot struct {
	Chats []ChatSummary
}

var (
	// ErrInvalidChatLimit is returned when a chat list limit is outside
	// telecli's supported range.
	ErrInvalidChatLimit = errors.New("telegram chats: invalid limit")

	// ErrInvalidChatID is returned when a chat ID is zero. TDLib does
	// not forbid negative chat IDs, so only the zero value is rejected
	// locally.
	ErrInvalidChatID = errors.New("telegram chats: invalid chat id")

	// ErrUnexpectedChatResponse is returned when TDLib returns an
	// object of the wrong @type or an id that does not match the
	// request.
	ErrUnexpectedChatResponse = errors.New("telegram chats: unexpected response")
)

const (
	// maxChatListLimit bounds snapshot fan-out in telecli.
	//
	// GetChats performs one getChat query for each returned chat ID,
	// so an application-level maximum prevents an accidentally
	// unbounded number of follow-up queries. This is a telecli policy,
	// not a documented TDLib limit for getChats.
	maxChatListLimit = 1000
)

type getChatsRequest struct {
	Type     string `json:"@type"`
	ChatList any    `json:"chat_list"`
	Limit    int    `json:"limit"`
}

type getChatsResponse struct {
	Type       string  `json:"@type"`
	TotalCount int     `json:"total_count"`
	ChatIDs    []int64 `json:"chat_ids"`
}

type getChatRequest struct {
	Type   string `json:"@type"`
	ChatID int64  `json:"chat_id"`
}

type chatResponse struct {
	Type        string          `json:"@type"`
	ID          int64           `json:"id"`
	Title       string          `json:"title"`
	UnreadCount int             `json:"unread_count"`
	LastMessage json.RawMessage `json:"last_message"`
}

// GetChats fetches the main chat list and materializes a summary for
// every returned chat ID.
//
// The call performs one getChats query followed by one getChat query
// per returned ID, all under the caller's context. Chats are returned
// in the order TDLib reports them.
//
// getChats alone returns only IDs; TDLib documents it as an
// informational snapshot and recommends loadChats plus updates for
// keeping the list coherent over time. This projection is intentionally
// snapshot-oriented.
func (s *AuthorizedSession) GetChats(
	ctx context.Context,
	limit int,
) (ChatListSnapshot, error) {
	if limit <= 0 || limit > maxChatListLimit {
		return ChatListSnapshot{}, fmt.Errorf(
			"%w: %d", ErrInvalidChatLimit, limit,
		)
	}

	request, err := json.Marshal(getChatsRequest{
		Type:     "getChats",
		ChatList: nil,
		Limit:    limit,
	})
	if err != nil {
		return ChatListSnapshot{}, fmt.Errorf("marshal getChats: %w", err)
	}

	raw, err := s.Query(ctx, RawMessage(request))
	if err != nil {
		return ChatListSnapshot{}, fmt.Errorf("getChats: %w", err)
	}

	var response getChatsResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return ChatListSnapshot{}, fmt.Errorf(
			"decode getChats response: %w", err,
		)
	}
	if response.Type != "chats" {
		return ChatListSnapshot{}, fmt.Errorf(
			"%w: getChats returned @type=%q",
			ErrUnexpectedChatResponse, response.Type,
		)
	}

	chats := make([]ChatSummary, 0, len(response.ChatIDs))
	for _, id := range response.ChatIDs {
		if err := ctx.Err(); err != nil {
			return ChatListSnapshot{}, err
		}
		summary, err := s.GetChat(ctx, ChatID(id))
		if err != nil {
			return ChatListSnapshot{}, fmt.Errorf(
				"getChat %d: %w", id, err,
			)
		}
		chats = append(chats, summary)
	}

	return ChatListSnapshot{Chats: chats}, nil
}

// GetChat fetches a single chat summary.
//
// GetChat is an offline TDLib method for regular user accounts and
// returns the chat's current title, unread count, and last message.
//
// Only the zero chat ID is rejected locally: TDLib treats chat_id as
// int53 and does not require positive values.
func (s *AuthorizedSession) GetChat(
	ctx context.Context,
	chatID ChatID,
) (ChatSummary, error) {
	if chatID == 0 {
		return ChatSummary{}, fmt.Errorf(
			"%w: %d", ErrInvalidChatID, chatID,
		)
	}

	request, err := json.Marshal(getChatRequest{
		Type:   "getChat",
		ChatID: int64(chatID),
	})
	if err != nil {
		return ChatSummary{}, fmt.Errorf("marshal getChat: %w", err)
	}

	raw, err := s.Query(ctx, RawMessage(request))
	if err != nil {
		return ChatSummary{}, fmt.Errorf("getChat: %w", err)
	}

	var response chatResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return ChatSummary{}, fmt.Errorf(
			"decode getChat response: %w", err,
		)
	}
	if response.Type != "chat" {
		return ChatSummary{}, fmt.Errorf(
			"%w: getChat returned @type=%q",
			ErrUnexpectedChatResponse, response.Type,
		)
	}
	if response.ID != int64(chatID) {
		return ChatSummary{}, fmt.Errorf(
			"%w: getChat requested id=%d, returned id=%d",
			ErrUnexpectedChatResponse, chatID, response.ID,
		)
	}

	lastID, lastText := parseLastMessage(response.LastMessage)

	return ChatSummary{
		ID:              ChatID(response.ID),
		Title:           response.Title,
		UnreadCount:     response.UnreadCount,
		LastMessageID:   lastID,
		LastMessageText: lastText,
	}, nil
}

type messageEnvelope struct {
	Type string `json:"@type"`
}

type messageRaw struct {
	Type    string          `json:"@type"`
	ID      int64           `json:"id"`
	Content json.RawMessage `json:"content"`
}

type messageTextContentRaw struct {
	Type string `json:"@type"`
	Text struct {
		Text string `json:"text"`
	} `json:"text"`
}

// parseLastMessage extracts the message ID and a short preview text
// from a chat's last_message field.
//
// Unsupported content types keep their message ID and receive a short
// placeholder such as "[photo]". An empty or null last_message returns
// (0, "").
func parseLastMessage(raw json.RawMessage) (MessageID, string) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, ""
	}

	var msg messageRaw
	if err := json.Unmarshal(raw, &msg); err != nil {
		return 0, ""
	}
	if msg.Type != "message" {
		return 0, ""
	}

	id := MessageID(msg.ID)
	if len(msg.Content) == 0 || string(msg.Content) == "null" {
		return id, ""
	}

	var envelope messageEnvelope
	if err := json.Unmarshal(msg.Content, &envelope); err != nil {
		return id, ""
	}

	if envelope.Type == "messageText" {
		var content messageTextContentRaw
		if err := json.Unmarshal(msg.Content, &content); err == nil {
			return id, content.Text.Text
		}
	}

	return id, placeholderForContent(envelope.Type)
}

// placeholderForContent maps a TDLib messageContent @type to a short
// preview string. The list covers the common cases; unknown types fall
// back to a generic placeholder.
func placeholderForContent(contentType string) string {
	switch contentType {
	case "messageText":
		return ""
	case "messageAnimation":
		return "[animation]"
	case "messageAudio":
		return "[audio]"
	case "messageContact":
		return "[contact]"
	case "messageDocument":
		return "[document]"
	case "messageGame":
		return "[game]"
	case "messageLocation":
		return "[location]"
	case "messagePhoto":
		return "[photo]"
	case "messagePoll":
		return "[poll]"
	case "messageSticker":
		return "[sticker]"
	case "messageVenue":
		return "[venue]"
	case "messageVideo":
		return "[video]"
	case "messageVideoNote":
		return "[video note]"
	case "messageVoiceNote":
		return "[voice note]"
	case "messageCall":
		return "[call]"
	case "messageUnsupported":
		return "[unsupported message]"
	default:
		return "[unsupported message]"
	}
}

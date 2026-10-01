package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ChatID is a TDLib chat identifier.
type ChatID int64

// MessageID is a TDLib message identifier.
type MessageID int64

// ChatSummary is the minimal chat projection used by the TUI.
//
// Kind, IsChannel and PeerUserID are what the interface needs to say who
// the other side of a message is and how loud a row is: a chat with one
// person in it has no authors to name, a group does, and a channel's
// messages are from the channel itself.
type ChatSummary struct {
	ID              ChatID
	Title           string
	UnreadCount     int
	LastMessageID   MessageID
	LastMessageText string

	// LastMessageTime is when the last message of the chat was sent, for
	// the time at the right edge of a row.
	LastMessageTime time.Time

	// Kind is the type of the chat.
	Kind ChatKind

	// IsChannel says whether a supergroup is a channel: a broadcast chat
	// whose messages are all from the chat itself.
	IsChannel bool

	// PeerUserID is the other person of a private chat, and is the user
	// this account is when the chat is with oneself. It is what tells the
	// own chat from a chat with a contact, since Telegram sends the
	// current user as an ordinary user.
	PeerUserID int64
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

// The answers below carry their numbers as tdInt, because TDLib writes
// some of them as JSON strings; see tdint.go.

type getChatsResponse struct {
	Type       string  `json:"@type"`
	TotalCount int     `json:"total_count"`
	ChatIDs    []tdInt `json:"chat_ids"`
}

type getChatRequest struct {
	Type   string `json:"@type"`
	ChatID int64  `json:"chat_id"`
}

type chatResponse struct {
	Type        string          `json:"@type"`
	ID          tdInt           `json:"id"`
	Title       string          `json:"title"`
	UnreadCount int             `json:"unread_count"`
	LastMessage json.RawMessage `json:"last_message"`

	Type_ json.RawMessage `json:"type"`
}

type chatTypeRaw struct {
	Type      string `json:"@type"`
	UserID    tdInt  `json:"user_id"`
	IsChannel bool   `json:"is_channel"`
}

// errChatListLoaded is the TDLib error code loadChats returns once the
// whole chat list is known. ADR-0003 treats it as a normal end of
// loading rather than a failure.
const chatListAlreadyLoadedCode = 404

// loadChatsRequest asks TDLib to send the updates for the next part of a
// chat list.
type loadChatsRequest struct {
	Type     string         `json:"@type"`
	ChatList chatListSource `json:"chat_list"`
	Limit    int            `json:"limit"`
}

// chatListSource selects a TDLib chat list by @type.
type chatListSource struct {
	Type string `json:"@type"`
}

// LoadChats asks TDLib to deliver the next part of the main chat list
// through updates, which the session pump applies to LiveState.
//
// The boolean result reports that the whole list is now known. TDLib
// answers "ok" while more chats remain and returns error 404 once the
// list is complete, which is a normal end of loading and not a failure.
//
// limit is validated exactly as in GetChats, and an invalid limit is
// rejected before any request is sent.
func (s *AuthorizedSession) LoadChats(
	ctx context.Context,
	limit int,
) (bool, error) {
	if limit <= 0 || limit > maxChatListLimit {
		return false, fmt.Errorf(
			"%w: %d", ErrInvalidChatLimit, limit,
		)
	}

	request, err := json.Marshal(loadChatsRequest{
		Type:     "loadChats",
		ChatList: chatListSource{Type: mainChatList},
		Limit:    limit,
	})
	if err != nil {
		return false, fmt.Errorf("marshal loadChats: %w", err)
	}

	raw, err := s.Query(ctx, RawMessage(request))
	if err != nil {
		var tdlibErr *TDLibError
		if errors.As(err, &tdlibErr) && tdlibErr.Code == chatListAlreadyLoadedCode {
			return true, nil
		}
		return false, fmt.Errorf("loadChats: %w", err)
	}

	var response messageEnvelope
	if err := json.Unmarshal(raw, &response); err != nil {
		return false, fmt.Errorf(
			"decode loadChats response: %w", err,
		)
	}
	if response.Type != "ok" {
		return false, fmt.Errorf(
			"%w: %s",
			ErrUnexpectedChatResponse,
			unexpectedResponse("loadChats", raw),
		)
	}
	return false, nil
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
			"%w: %s",
			ErrUnexpectedChatResponse,
			unexpectedResponse("getChats", raw),
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
			"%w: %s",
			ErrUnexpectedChatResponse,
			unexpectedResponse("getChat", raw),
		)
	}
	if int64(response.ID) != int64(chatID) {
		return ChatSummary{}, fmt.Errorf(
			"%w: getChat requested id=%d, returned id=%d for @extra=%q",
			ErrUnexpectedChatResponse, chatID, int64(response.ID),
			responseExtra(raw),
		)
	}

	lastID, lastText, lastAt := parseLastMessage(response.LastMessage)
	kind, isChannel, peerUser := parseChatType(response.Type_)

	return ChatSummary{
		ID:              ChatID(response.ID),
		Title:           response.Title,
		UnreadCount:     response.UnreadCount,
		LastMessageID:   lastID,
		LastMessageText: lastText,
		LastMessageTime: lastAt,
		Kind:            kind,
		IsChannel:       isChannel,
		PeerUserID:      peerUser,
	}, nil
}

type messageEnvelope struct {
	Type string `json:"@type"`
}

type messageRaw struct {
	Type string `json:"@type"`
	ID   tdInt  `json:"id"`
	Date tdInt  `json:"date"`
	// Content carries the file and its caption, so that a row's preview
	// can say what the last message of a chat was even when it was a
	// picture rather than words.
	Content json.RawMessage `json:"content"`
}

type messageTextContentRaw struct {
	Type string `json:"@type"`
	Text struct {
		Text string `json:"text"`
	} `json:"text"`
}

// parseChatType reads the type of a chat: which kind it is, whether a
// supergroup is a channel, and who the other person of a private chat is.
//
// A chat whose type this build does not know is reported as private, which
// is the smallest of the three claims the interface makes from it: a chat
// drawn as a private one has no author names, and an unknown kind is
// almost never a group the user recognises by its members.
func parseChatType(raw json.RawMessage) (ChatKind, bool, int64) {
	if len(raw) == 0 || string(raw) == "null" {
		return ChatKindPrivate, false, 0
	}

	var parsed chatTypeRaw
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return ChatKindPrivate, false, 0
	}

	kind := ChatKind(parsed.Type)
	switch kind {
	case ChatKindGroup, ChatKindSupergroup:
		return kind, parsed.IsChannel, 0
	case ChatKindPrivate:
		return kind, false, int64(parsed.UserID)
	default:
		return ChatKindPrivate, false, int64(parsed.UserID)
	}
}

// parseLastMessage extracts the message ID, a short preview and the moment
// it was sent from a chat's last_message field.
//
// A message that carries a file gets the label for the file and its caption,
// because "a picture" in a chat list is what the user needs to know and a
// row of nothing is not; a message that is only words gets the words, and a
// service message gets the phrase of what happened. The label is the same
// one the feed draws (message_content.go), so the two cannot disagree about
// what a message was. An empty or null last_message returns
// (0, "", time.Time{}).
func parseLastMessage(raw json.RawMessage) (MessageID, string, time.Time) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, "", time.Time{}
	}

	var msg messageRaw
	if err := json.Unmarshal(raw, &msg); err != nil {
		return 0, "", time.Time{}
	}
	if msg.Type != "message" {
		return 0, "", time.Time{}
	}

	id := MessageID(msg.ID)
	sent := time.Unix(int64(msg.Date), 0).UTC()

	if text := extractMessageText(msg.Content); text != "" {
		return id, text, sent
	}

	return id, readContentLabel(msg.Content).line(), sent
}

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
	// as consecutive messages that share it, and sends the field on every
	// message: zero is a message that is not part of an album.
	MediaAlbumID int64
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
// NextFrom is the oldest message of the answer and not the oldest one
// this build could read out of it: a boundary that skipped an entry
// would ask for the same page again and get the same entry again.
//
// NextFrom is zero for an empty page.
//
// HasMore says the answer was not empty, and the history of a chat
// ends at an empty answer and nowhere else. It was a len(messages) ==
// limit heuristic, and a real account answered the first request of a
// chat with one or two messages because that is what TDLib had under
// its hand: the interface took the first page for the whole history,
// showed the two messages it had, and never asked again — the
// conversation of a user who has been writing to somebody for years
// was the last two messages of it, and their own messages were not
// even in the two.
type HistoryPage struct {
	Messages []Message
	NextFrom MessageID
	HasMore  bool

	// Unreadable is how many entries of the page this build could not
	// read. They are left out of Messages and the count is here so
	// that a caller can say so: a page of fifty of which forty-nine
	// were read is a page where a message of this user is missing, and
	// it is missing silently unless somebody writes the number down.
	Unreadable int
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
	Type       string `json:"@type"`
	TotalCount int    `json:"total_count"`

	// Messages are read one at a time, out of their own raw objects.
	// Decoding the whole vector in one step is what let a single field of
	// a single message fail the fifty messages around it.
	Messages []json.RawMessage `json:"messages"`
}

// historyMessageRaw is one message of a page, as TDLib writes it.
//
// Every number is a tdInt, because TDLib writes some of them as strings;
// see tdint.go.
type historyMessageRaw struct {
	Type         string          `json:"@type"`
	ID           tdInt           `json:"id"`
	ChatID       tdInt           `json:"chat_id"`
	IsOutgoing   bool            `json:"is_outgoing"`
	Date         tdInt           `json:"date"`
	Content      json.RawMessage `json:"content"`
	SenderID     json.RawMessage `json:"sender_id"`
	MediaAlbumID tdInt           `json:"media_album_id"`
}

// GetChatHistory fetches one page of a chat's message history.
//
// Rules enforced locally:
//
//   - chatID == 0 is rejected. TDLib treats chat_id as int53 and does
//     not forbid negative chat IDs.
//   - limit must be in (0, 100]. TDLib answers with what it has and
//     not with what was asked for: the first request of a chat on a
//     fresh database comes back with one or two messages however many
//     were asked for, and that answer is not the end of the chat.
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
			"%w: %s",
			ErrUnexpectedHistoryResponse,
			unexpectedResponse("getChatHistory", raw),
		)
	}

	messages := make([]Message, 0, len(response.Messages))
	var (
		nextFrom   MessageID
		unreadable int
	)
	for _, entry := range response.Messages {
		message, err := decodeHistoryMessage(entry, chatID)
		switch {
		case errors.Is(err, errUnreadableMessage):
			// One message this build cannot read is one message that is
			// not shown. TDLib adds kinds of message and sends kinds
			// this build has no words for, and a page of history that
			// cannot be opened at all because of one of them is fifty
			// messages the user came to read.
			//
			// The boundary moves past it anyway. An entry left out of
			// the page that is also left out of the boundary is a page
			// asked for twice, and the user scrolls up and nothing
			// happens.
			unreadable++
			if id, ok := historyEntryID(entry); ok {
				nextFrom = id
			}

			continue
		case err != nil:
			return HistoryPage{}, err
		}

		messages = append(messages, message)
		nextFrom = message.ID
	}

	return HistoryPage{
		Messages:   messages,
		NextFrom:   nextFrom,
		HasMore:    len(response.Messages) > 0,
		Unreadable: unreadable,
	}, nil
}

// historyEntryID reads the identifier out of one entry of a page without
// reading the rest of it.
//
// It is what keeps the boundary moving across an entry the decode could
// not finish. The identifier is the one field every message of TDLib
// carries and the one field a page is continued from, so a message this
// build has no words for the text of is still a message with a place in
// the order of the conversation.
func historyEntryID(raw json.RawMessage) (MessageID, bool) {
	var entry struct {
		Type string `json:"@type"`
		ID   tdInt  `json:"id"`
	}
	if isAbsentJSON(raw) || json.Unmarshal(raw, &entry) != nil {
		return 0, false
	}
	if entry.Type != "message" || entry.ID == 0 {
		return 0, false
	}

	return MessageID(entry.ID), true
}

// errUnreadableMessage says a history entry could not be read into a
// message. It is not reported to the caller: the entry is left out of the
// page and the rest of it is shown.
var errUnreadableMessage = errors.New("telegram history: unreadable message")

// decodeHistoryMessage reads one entry of a history page into a message.
//
// There are two kinds of failure and they are not the same to a user. An
// entry that is not a message, or a message of another chat than the one
// asked about, is TDLib's way of saying that the answer is the answer to a
// different question, and the interface has to be able to say so. Anything
// else is one message this build cannot read, and it is left out of the
// page rather than taking the page with it.
func decodeHistoryMessage(
	raw json.RawMessage,
	chatID ChatID,
) (Message, error) {
	if isAbsentJSON(raw) {
		return Message{}, fmt.Errorf(
			"%w: the entry is no object", errUnreadableMessage,
		)
	}

	var m historyMessageRaw
	if err := json.Unmarshal(raw, &m); err != nil {
		return Message{}, fmt.Errorf("%w: %w", errUnreadableMessage, err)
	}

	if m.Type != "message" {
		return Message{}, fmt.Errorf(
			"%w: history entry @type=%q",
			ErrUnexpectedHistoryResponse, m.Type,
		)
	}
	if int64(m.ChatID) != int64(chatID) {
		return Message{}, fmt.Errorf(
			"%w: getChatHistory requested chat_id=%d, message has chat_id=%d",
			ErrUnexpectedHistoryResponse, chatID, int64(m.ChatID),
		)
	}

	media, caption := extractMedia(m.Content)

	return Message{
		ID:        MessageID(m.ID),
		ChatID:    ChatID(m.ChatID),
		Outgoing:  m.IsOutgoing,
		Timestamp: time.Unix(int64(m.Date), 0).UTC(),
		Text:      extractMessageText(m.Content),
		Sender:    parseMessageSender(m.SenderID),
		Media:     media,
		Caption:   caption,

		// A media_album_id of zero is not an album: Telegram sends it on
		// every message, and the timeline groups runs of messages that
		// share a non-zero id.
		MediaAlbumID: int64(m.MediaAlbumID),
	}, nil
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
			UserID tdInt `json:"user_id"`
		}
		if err := json.Unmarshal(raw, &user); err != nil {
			return MessageSender{Kind: MessageSenderUser}
		}

		return MessageSender{Kind: MessageSenderUser, ID: int64(user.UserID)}

	case MessageSenderChat:
		var chat struct {
			ChatID tdInt `json:"chat_id"`
		}
		if err := json.Unmarshal(raw, &chat); err != nil {
			return MessageSender{Kind: MessageSenderChat}
		}

		return MessageSender{Kind: MessageSenderChat, ID: int64(chat.ChatID)}

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

package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// TDLib does not know what a person is looking at. It knows which chat has
// been opened and how far the read pointer of that chat has moved, so the
// interface says what is on the screen and the session asks TDLib to move
// the pointer to it.
//
// Without that call the interface reads a chat that stays unread forever:
// the counter in the list never falls, and the other side of the
// conversation is told nothing was seen.
//
// The schema of the call, from the pinned TDLib (td_api.tl at commit
// ea97bcdd, TDLib 1.8.67):
//
//	viewMessages chat_id:int53 message_ids:vector<int53>
//	    source:MessageSource force_read:Bool = Ok;            // :13138
//
// Four of those fields carry decisions rather than values, and each of them
// is a way to be wrong in a way that is invisible — a read that does not
// happen leaves the counter where it was and says nothing about itself:
//
//   - force_read is true. With it false TDLib only marks a message read
//     when the pointer is at the end of the chat, which is true for the
//     newest message of a chat the user just opened and false for the page
//     of older messages above it.
//   - message_ids is the window, not the whole history. TDLib reads up to
//     the newest of the identifiers it is given, so one message marked read
//     takes everything below it with it: that is why the *highest* visible
//     identifier is what a source names.
//   - source is what TDLib tells the account's other devices about, and it
//     is not one value for every kind of chat. A private chat is read in the
//     chat itself (messageSourceChat); a group and a channel are read in a
//     window of their history (messageSourceHistory, ttl 0, message_id the
//     newest message read), which is what TDLib's own clients send for a
//     channel and what messages.readHistory means. telecli has no topics, so
//     it never claims a thread of a supergroup.
//   - the chat must be open first. A private chat is in the account's own
//     dialog list whatever TDLib is doing, and a broadcast chat is one
//     TDLib loads with openChat; a read sent to a chat it has not loaded is
//     refused, and nothing on the screen says so. The interface waits for
//     the answer of openChat (see markVisibleMessagesViewed).

// ErrMessageViewResponse is returned when TDLib answers viewMessages with
// something other than ok.
//
// The schema says viewMessages returns ok (:13138), so any other answer is
// a protocol surprise rather than a routine refusal, and it is reported
// instead of being read as a success. The message is left unread in that
// case, which is the smaller mistake: a counter that stays is an
// inconvenience, and a read that was never recorded is a lie to the other
// side.
var ErrMessageViewResponse = errors.New(
	"telegram message view: unexpected response",
)

// ErrNoMessagesToView is returned when viewMessages is called with no
// message identifiers.
//
// TDLib rejects an empty list itself, and a request that is sent to be
// refused is a round trip that could not have said anything.
var ErrNoMessagesToView = errors.New(
	"telegram message view: no messages to view",
)

type viewMessagesRequest struct {
	Type       string        `json:"@type"`
	ChatID     int64         `json:"chat_id"`
	MessageIDs []tdInt       `json:"message_ids"`
	Source     MessageSource `json:"source"`
	ForceRead  bool          `json:"force_read"`
}

// MessageSource is the TDLib MessageSource of a read.
//
// It is an interface with an unexported method rather than one struct with
// the fields of every source: the sources do not share their fields, and a
// struct that carried them all would write `ttl` and `message_id` on a
// private chat's read, where they mean nothing. The only way to have one is
// MessageSourceFor, which is what knows which source a kind of chat is read
// with.
type MessageSource interface {
	messageSource()
}

// messageSourceChatJSON is messageSourceChat (td_api.tl: :13008): the read
// happened in the chat the messages belong to.
type messageSourceChatJSON struct {
	Type string `json:"@type"`
}

func (messageSourceChatJSON) messageSource() {}

// messageSourceHistoryJSON is messageSourceHistory (td_api.tl: :13013): a
// window of a chat's history, ttl 0 meaning this device.
type messageSourceHistoryJSON struct {
	Type      string `json:"@type"`
	TTL       int    `json:"ttl"`
	MessageID tdInt  `json:"message_id"`
}

func (messageSourceHistoryJSON) messageSource() {}

// MessageSourceFor returns the source of a read of a window of a chat.
//
// newest is the highest identifier the user can see, because the source
// names where the read stopped rather than every message of it.
//
// The chat's own kind is the only thing that decides it:
//
//   - a private chat is read in the chat itself, which is
//     messageSourceChat: this is what an official client sends for a chat
//     with one other person, and it is what makes TDLib tell the phone and
//     the other devices;
//   - a group and a channel are read in a window of their history, which is
//     messageSourceHistory with ttl 0 (this device) and the identifier the
//     read reached. telecli shows a supergroup as one conversation and has
//     no topics, so it never claims a thread of one.
func MessageSourceFor(kind ChatKind, newest MessageID) MessageSource {
	if !kind.Grouped() {
		return messageSourceChatJSON{Type: "messageSourceChat"}
	}

	return messageSourceHistoryJSON{
		Type:      "messageSourceHistory",
		TTL:       0,
		MessageID: tdInt(newest),
	}
}

// ViewMessages tells TDLib that the messages with the given identifiers
// have been read.
//
// force_read is set: the caller says which messages the user can see, and a
// read that depends on the pointer being at the end of the chat is not the
// read the caller asked for.
//
// source is the source of the read and MessageSourceFor is what builds it,
// because which source is right depends on the kind of the chat and not on
// the caller.
//
// The call is best effort from the interface's point of view. A chat whose
// messages could not be marked read still shows them, and the count in the
// list is a count TDLib reports. The cause is the caller's to log, because
// a TDLib error message is not interface text (§11.3, §19).
func (s *AuthorizedSession) ViewMessages(
	ctx context.Context,
	chatID ChatID,
	messageIDs []MessageID,
	source MessageSource,
) error {
	if chatID == 0 {
		return fmt.Errorf("%w: %d", ErrInvalidChatID, chatID)
	}
	if source == nil {
		return fmt.Errorf("%w: no source", ErrMessageViewResponse)
	}
	if len(messageIDs) == 0 {
		return ErrNoMessagesToView
	}

	ids := make([]tdInt, 0, len(messageIDs))
	for _, id := range messageIDs {
		// A zero identifier is not a message: TDLib would read up to it
		// and mark nothing, and the interface has already dropped the
		// messages of the queue, which are the only ones that can carry
		// one.
		if id == 0 {
			continue
		}
		ids = append(ids, tdInt(id))
	}
	if len(ids) == 0 {
		return ErrNoMessagesToView
	}

	request, err := json.Marshal(viewMessagesRequest{
		Type:       "viewMessages",
		ChatID:     int64(chatID),
		MessageIDs: ids,
		Source:     source,
		ForceRead:  true,
	})
	if err != nil {
		return fmt.Errorf("marshal viewMessages: %w", err)
	}

	raw, err := s.Query(ctx, RawMessage(request))
	if err != nil {
		return fmt.Errorf("viewMessages: %w", err)
	}

	var response struct {
		Type string `json:"@type"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return fmt.Errorf("decode viewMessages response: %w", err)
	}
	if response.Type != "ok" {
		return fmt.Errorf(
			"%w: %s",
			ErrMessageViewResponse,
			unexpectedResponse("viewMessages", raw),
		)
	}

	return nil
}

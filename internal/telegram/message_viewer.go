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
// Three of those fields carry decisions rather than values, and each of them
// is a way to be wrong in a way that is invisible:
//
//   - force_read is true. With it false TDLib only marks a message read
//     when the pointer is at the end of the chat, which is true for the
//     newest message of a chat the user just opened and false for the page
//     of older messages above it. The official clients force the read for
//     the chat they are showing, and so does this one.
//   - message_ids is the window, not the whole history. TDLib takes the
//     ids and reads up to the newest of them, so a message nobody looked
//     at is marked read by being below one that was.
//   - source is messageSourceChat: the read happened in the chat itself,
//     which is what makes TDLib tell the *other* devices of the account
//     about it. TDLib documents the field as "the source of the messages,
//     which will be used to exclude the messages from the update", and
//     this program is a second client of an account whose phone is the
//     first.

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
	Type       string            `json:"@type"`
	ChatID     int64             `json:"chat_id"`
	MessageIDs []tdInt           `json:"message_ids"`
	Source     messageSourceJSON `json:"source"`
	ForceRead  bool              `json:"force_read"`
}

// messageSourceJSON is the TDLib MessageSource of a read (td_api.tl:
// messageSourceChat is :13008).
//
// Only the type is ever written. The other sources name a message or a
// thread, which is what tells TDLib which client the read came from, and
// this program has nothing to say about that beyond "the chat the user is
// looking at".
type messageSourceJSON struct {
	Type string `json:"@type"`
}

// viewMessagesSource is the source every read of this program carries.
var viewMessagesSource = messageSourceJSON{Type: "messageSourceChat"}

// ViewMessages tells TDLib that the messages with the given identifiers
// have been read.
//
// force_read is set: the caller says which messages the user can see, and a
// read that depends on the pointer being at the end of the chat is not the
// read the caller asked for.
//
// The call is best effort from the interface's point of view. A chat whose
// messages could not be marked read still shows them, and the count in the
// list is a count TDLib reports. The cause is the caller's to log, because
// a TDLib error message is not interface text (§11.3, §19).
func (s *AuthorizedSession) ViewMessages(
	ctx context.Context,
	chatID ChatID,
	messageIDs []MessageID,
) error {
	if chatID == 0 {
		return fmt.Errorf("%w: %d", ErrInvalidChatID, chatID)
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
		Source:     viewMessagesSource,
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

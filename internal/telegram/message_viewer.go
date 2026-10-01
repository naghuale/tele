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
// The schema of the call, from the pinned TDLib (testdata/td_api.tl at
// commit ea97bcdd, TDLib 1.8.67):
//
//	viewMessages chat_id:int53 message_ids:vector<int53>
//	    source:MessageSource force_read:Bool = Ok;            // :13231
//
// Three of those fields carry decisions rather than values, and each of them
// is a way to be wrong in a way that is invisible — a read that does not
// happen leaves the counter where it was and says nothing about itself:
//
//   - force_read is true. With it false TDLib only marks a message read
//     when the pointer is at the end of the chat, which is true for the
//     newest message of a chat the user just opened and false for the page
//     of older messages above it.
//   - message_ids is the window, not the whole history. TDLib reads up to
//     the newest of the identifiers it is given, so one message marked read
//     takes everything below it with it.
//   - source is messageSourceChatHistory, and there is nothing to choose
//     here: the read happens in the history of the chat that is open, for
//     every kind of chat alike. The pinned schema knows eleven sources
//     (MessageSource, testdata/td_api.tl:3205) and messageSourceChatHistory
//     is the one that says what this program did. A name the schema does not
//     have is refused by TDLib, which is what an earlier attempt cost: it
//     sent messageSourceChat and messageSourceHistory, neither of which
//     exists in this version, and every read of every kind of chat was
//     refused. The names the code sends are checked against the schema by
//     TestEveryTDLibClassTheCodeNamesIsInThePinnedSchema, so a name that is
//     not in the schema cannot reach TDLib again.
//
// The chat also has to be open before it can be read. A private chat is in
// the account's own dialog list whatever TDLib is doing, and a broadcast
// chat is one TDLib loads with openChat (td_api.tl:13220); a read sent to a
// chat it has not loaded is refused, and nothing on the screen says so. The
// interface waits for the answer of openChat (see markVisibleMessagesViewed).

// ErrMessageViewResponse is returned when TDLib answers viewMessages with
// something other than ok.
//
// The schema says viewMessages returns ok (td_api.tl:13231), so any other answer is
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
	Type       string                   `json:"@type"`
	ChatID     int64                    `json:"chat_id"`
	MessageIDs []tdInt                  `json:"message_ids"`
	Source     messageSourceChatHistory `json:"source"`
	ForceRead  bool                     `json:"force_read"`
}

// messageSourceChatHistory is messageSourceChatHistory (td_api.tl:3208), the
// source of a message read in the history of a chat.
//
// It carries no field at all, and that is the whole of why there is nothing
// to choose between kinds of chat: telecli reads a window of the history of
// the chat it is showing, and this is the one source of the eleven in the
// schema that says so.
type messageSourceChatHistory struct {
	Type string `json:"@type"`
}

// viewMessagesSource is the source every read of this program carries.
var viewMessagesSource = messageSourceChatHistory{Type: "messageSourceChatHistory"}

// ViewMessages tells TDLib that the messages with the given identifiers
// have been read.
//
// force_read is set: the caller says which messages the user can see, and a
// read that depends on the pointer being at the end of the chat is not the
// read the caller asked for. The source is the history of this chat, because
// that is where the window came from.
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

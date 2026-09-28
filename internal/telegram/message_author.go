package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// This file is everything the interface needs to say who sent a message and
// what a message carries, and the one question the live store deliberately
// does not keep the answer to (#41).
//
// The live state holds chat titles and message text, and it does not hold
// the names of the people in a chat. That is a privacy claim the store's
// own test proves, and this file does not weaken it: a name is read from
// TDLib when a message is about to be drawn, kept in the adapter's memory
// for as long as the program runs, and never written into LiveState, a log
// line, a doctor report or an error message. Nothing here has a String
// method that prints a name.

// MessageSenderKind is who a message came from, as TDLib reports it.
type MessageSenderKind string

const (
	// MessageSenderUser is a person.
	MessageSenderUser MessageSenderKind = "messageSenderUser"

	// MessageSenderChat is a chat: a channel, or the anonymous sender a
	// group can be configured with.
	MessageSenderChat MessageSenderKind = "messageSenderChat"

	// MessageSenderUnknown is a sender this build does not know, and is
	// what a message with no sender at all is drawn as.
	MessageSenderUnknown MessageSenderKind = ""
)

// MessageSender is who sent a message.
//
// The interface needs a name for the sender of a message from somebody else,
// and it cannot get one from the message: TDLib gives an identifier and a
// kind, and the name is a separate question. Which is why the identifier
// travels with the message and the name is looked up beside it.
type MessageSender struct {
	Kind MessageSenderKind
	ID   int64
}

// ChatKind is what kind of chat a chat is, as far as the words of a screen
// are concerned.
type ChatKind string

const (
	// ChatKindPrivate is a chat with one other person, or with oneself.
	ChatKindPrivate ChatKind = "chatTypePrivate"

	// ChatKindGroup is a basic group.
	ChatKindGroup ChatKind = "chatTypeGroup"

	// ChatKindSupergroup is a supergroup, which is what a channel is as
	// well: the two are told apart by IsChannel and not by the kind.
	ChatKindSupergroup ChatKind = "chatTypeSupergroup"
)

// Grouped reports whether a chat has more than one person in it.
//
// A channel counts: it is a chat with many members even though every
// message in it is from the channel itself.
func (k ChatKind) Grouped() bool {
	return k == ChatKindGroup || k == ChatKindSupergroup
}

// ErrUserNameUnavailable is returned when a name could not be read.
//
// The interface shows "Unknown" for it and the cause goes to the log; the
// error exists so that a caller can tell "this user has no name" from "the
// question was not answered".
var ErrUserNameUnavailable = errors.New("telegram user name unavailable")

type getUserRequest struct {
	Type   string `json:"@type"`
	UserID int64  `json:"user_id"`
}

type getUserResponse struct {
	Type      string `json:"@type"`
	ID        int64  `json:"id"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Username  string `json:"username"`
}

// GetUserName returns the name of a user, as their first and last names.
//
// The name is what the interface writes above a message from that person,
// and it is read from TDLib at the moment it is needed rather than kept:
// the live store deliberately does not hold it, and neither does this
// package. A caller that asks twice pays twice, and the caller that draws
// the screen is the one that caches.
//
// The fallback is the username with its leading @, and then the identifier
// on its own, so that a user who has set neither name still has a label
// rather than an empty row.
func (s *AuthorizedSession) GetUserName(
	ctx context.Context,
	userID int64,
) (string, error) {
	if userID == 0 {
		return "", fmt.Errorf("%w: id 0", ErrUserNameUnavailable)
	}

	request, err := json.Marshal(getUserRequest{Type: "getUser", UserID: userID})
	if err != nil {
		return "", fmt.Errorf("marshal getUser: %w", err)
	}

	raw, err := s.Query(ctx, RawMessage(request))
	if err != nil {
		return "", fmt.Errorf("getUser: %w", err)
	}

	var response getUserResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return "", fmt.Errorf("decode getUser response: %w", err)
	}

	if response.Type != "user" {
		return "", fmt.Errorf(
			"%w: getUser returned @type=%q",
			ErrUserNameUnavailable, response.Type,
		)
	}

	return userDisplayName(response), nil
}

// userDisplayName is the name of a user as the interface writes it.
//
// A first and a last name are written the way a person writes them, with a
// space between; a name with no last name is the first name alone rather
// than a trailing space. The username is the fallback and the identifier is
// the last one, because a row with nothing in it is a row a user cannot
// tell from a row that failed to load.
func userDisplayName(user getUserResponse) string {
	if name := strings.TrimSpace(user.FirstName + " " + user.LastName); name != "" {
		return name
	}
	if user.Username != "" {
		return "@" + user.Username
	}

	return fmt.Sprintf("user %d", user.ID)
}

// The @type of each messageContent that carries a file of some kind, and
// the word the interface writes for it.
//
// The word is the interface's, not TDLib's: a user reads "[photo]" and
// does not read "messagePhoto", and an album of documents is an album of
// files rather than an album of "document"s.
var mediaWords = map[string]string{
	"messageAnimation": "animation",
	"messageAudio":     "audio",
	"messageDocument":  "file",
	"messagePhoto":     "photo",
	"messageSticker":   "sticker",
	"messageVideo":     "video",
	"messageVideoNote": "video note",
	"messageVoiceNote": "voice note",
}

// mediaKind returns the word for a message content type, and whether the
// content is a file of some kind at all.
func mediaKind(contentType string) (string, bool) {
	word, ok := mediaWords[contentType]

	return word, ok
}

// mediaCaptionRaw is the shape of the caption of a picture or a file.
type mediaCaptionRaw struct {
	Caption struct {
		Text string `json:"text"`
	} `json:"caption"`
}

// extractMedia returns the word for what a message carries and the caption
// under it, if it carries a file at all.
//
// A message that is only text has neither, and the interface then draws
// the text alone: a row of empty columns where a picture should be is a
// message a user cannot read.
func extractMedia(content json.RawMessage) (string, string) {
	if len(content) == 0 || string(content) == "null" {
		return "", ""
	}

	var envelope messageEnvelope
	if err := json.Unmarshal(content, &envelope); err != nil {
		return "", ""
	}

	word, ok := mediaKind(envelope.Type)
	if !ok {
		return "", ""
	}

	var caption mediaCaptionRaw
	if err := json.Unmarshal(content, &caption); err != nil {
		return word, ""
	}

	return word, strings.TrimSpace(caption.Caption.Text)
}

package telegram

import (
	"encoding/json"
	"strconv"
	"strings"
)

// What a message carries, in the words the interface writes for it.
//
// TDLib sends a message as a `message` with a `content` object whose `@type`
// says what is in it, and it has 105 of those types in the pinned schema
// (testdata/td_api.tl, :5141-5690). The interface has to say something about
// every one of them, and it used to say `[unsupported message]` about the
// ones it had no words for — which is a sentence about the program rather
// than about the message, on a row the user is looking at to find out what
// was sent to them. The owner found two of them in one chat on 01.10 and
// could not tell what the chat had to say.
//
// So the types are named here. The ones a person meets get words a person
// uses — `[sticker 😀]`, `[GIF]`, `[poll: Friday?]`, `[file счёт.pdf]` — and
// everything else says what it is by the name TDLib gives it, `[messageX]`,
// which is a name a maintainer can look up in the schema and a sentence that
// is at least true. A type with no word here is a type that has to be added,
// and it says so on the screen.
//
// A label is three things, and each of them is read where the schema says it
// is:
//
//   - the word, which is the interface's and not TDLib's: a user reads
//     "[photo]" and does not read "messagePhoto", and an album of documents
//     is an album of files rather than an album of "document"s;
//   - the detail, which is whatever else the payload says about it — the
//     emoji of a sticker, the name of a file, the question of a poll, the
//     number a dice came up. It carries its own separator, because the
//     poll's is a colon and every other one is a space;
//   - the caption, which Telegram keeps apart from the text of a message and
//     which the interface writes after the label, as it does for a picture.
//
// A service message carries nothing at all, so it has no word: it says what
// happened in the chat in a short phrase of its own ("joined the chat",
// "pinned a message"), and the interface draws that phrase as a row of the
// feed of its own rather than as something somebody wrote.

// contentLabel is one message content read into the words the interface
// writes for it.
//
// It is one struct and not four functions because the three parts belong
// together: a sticker with no emoji is still a sticker, a poll with no
// question is still a poll, and a caller that read them one at a time could
// end up with a detail of one message and a caption of another.
type contentLabel struct {
	// word is the noun, the detail what else the payload says about it
	// with its own separator, and caption the words under a picture or a
	// file. All three are empty for a message that carries nothing.
	word    string
	detail  string
	caption string

	// service is what happened in the chat, in a phrase of its own, and it
	// is set instead of the word: a service message carries nothing to
	// label.
	service string
}

// readContentLabel reads one message content into the label the interface
// writes for it.
//
// Every content gets a label: one this build has words for, one that is a
// service message, or one that is named by its own @type. The last is what
// makes a type this build has not heard of addable — the name on the screen
// is the name to look for in the schema, and a word is all the truth about
// a message the program knows nothing else of.
func readContentLabel(content json.RawMessage) contentLabel {
	if len(content) == 0 || string(content) == "null" {
		return contentLabel{}
	}

	var envelope messageEnvelope
	if err := json.Unmarshal(content, &envelope); err != nil {
		return contentLabel{}
	}

	// A service message is what happened rather than what was sent, and it
	// is read before the words: a service type is not in the words table,
	// and its phrase is the whole of the message.
	if phrase := servicePhrase(envelope.Type, content); phrase != "" {
		return contentLabel{
			service: phrase + serviceDetail(envelope.Type, content),
		}
	}

	// A message of words is its own words and has no label: the text is
	// drawn instead of a noun for it, and a block that said "[messageText]"
	// above the sentence somebody wrote would say it twice. One with
	// nothing in it — a link preview and no text — is named by its type
	// like any other, because there is nothing else to draw.
	if messageTexts[envelope.Type] {
		if extractMessageText(content) != "" {
			return contentLabel{}
		}

		return contentLabel{word: envelope.Type}
	}

	word, known := contentWords[envelope.Type]
	if !known {
		// The type is drawn by the name TDLib gave it. A type this build
		// has no words for is a type somebody has to add, and the name on
		// the screen is the name to add it under.
		return contentLabel{word: envelope.Type}
	}

	return contentLabel{
		word:    word,
		detail:  contentDetail(envelope.Type, content),
		caption: contentCaption(content),
	}
}

// line is the label as a chat list and a feed write it: a phrase as it is,
// and a word with its detail in brackets and the caption after them.
//
// It is one line and not a column of a row because that is where the message
// of a chat list goes, and because a preview of "[sticker 😀]" has to be the
// same sentence the feed draws.
func (l contentLabel) line() string {
	if l.service != "" {
		return l.service
	}
	if l.word == "" {
		// A message that carries nothing and says nothing is a message
		// with no words to put in a row: an empty pair of brackets is a
		// row of brackets, and the row before this one had the text of
		// the message in it.
		return ""
	}

	line := "[" + l.word + l.detail + "]"
	if l.caption == "" {
		return line
	}

	return line + " " + l.caption
}

// messageTexts are the @types of a message whose own words are the message.
//
// An animated emoji is in here because the emoji is the message and nothing
// is attached to it: a sticker is a label with the emoji of the sticker in
// it, and an animated emoji is a message that happens to be one character.
var messageTexts = map[string]bool{
	"messageText":          true,
	"messageAnimatedEmoji": true,
}

// contentCaption reads the words Telegram keeps under a picture or a file.
//
// They are a field of their own and not part of the text of the message, and
// the interface writes them after the label: the words under a picture are
// the picture's, and a message with a caption and no text is a message that
// is entirely a picture with something written on it.
func contentCaption(content json.RawMessage) string {
	var caption mediaCaptionRaw
	if err := json.Unmarshal(content, &caption); err != nil {
		return ""
	}

	return strings.TrimSpace(caption.Caption.Text)
}

// contentWords is the noun the interface writes for each @type of a message
// content that carries something.
//
// The keys are constructors of the pinned schema, and
// TestEveryTypeTheTableNamesIsAMessageContentOfThePinnedSchema holds every
// one of them against the schema file: a name this build invented is a label
// no message can ever carry, and it is the kind of dead entry that is found
// years later by a user looking at an empty row on their screen.
var contentWords = map[string]string{
	// The media a chat is mostly made of.
	"messageAnimation": "GIF",
	"messageAudio":     "audio",
	"messageDocument":  "file",
	"messagePhoto":     "photo",
	"messageSticker":   "sticker",
	"messageVideo":     "video",
	"messageVideoNote": "video note",
	"messageVoiceNote": "voice note",

	// Media that has gone: Telegram keeps the message and drops the file,
	// and what is left of it is a message about a picture that is not
	// there. "[expired photo]" is what it is; "[photo]" would be a file
	// the user cannot open.
	"messageExpiredPhoto":     "expired photo",
	"messageExpiredVideo":     "expired video",
	"messageExpiredVideoNote": "expired video note",
	"messageExpiredVoiceNote": "expired voice note",

	// Where a message is about a place.
	"messageLiveLocation": "live location",
	"messageLocation":     "location",
	"messageVenue":        "venue",

	// What is in the message rather than what it is made of.
	"messageContact": "contact",
	"messageDice":    "dice",
	"messageGame":    "game",
	"messagePoll":    "poll",
	"messageStory":   "story",

	// The rest of the kinds a user meets in a chat. A type missing from
	// this table is not a message that cannot be named: it is one that is
	// named by its own @type until somebody gives it words.
	"messageCall":      "call",
	"messageChecklist": "checklist",
	"messageGiveaway":  "giveaway",
	"messageGift":      "gift",
	"messageInvoice":   "invoice",
	"messagePaidMedia": "paid media",
}

// contentDetail returns what the label of a content says beyond its word,
// separator included, so that the caller puts the two inside one pair of
// brackets without knowing which separator this kind uses.
//
// A content with nothing more to say returns "", and the label is the word
// alone. The detail is left out rather than invented where the payload has
// none: an audio with no title is "[audio]", and a sticker out of a set with
// no emoji is "[sticker]" — a label with a hole in it is a label about a
// thing that is not there.
func contentDetail(contentType string, raw json.RawMessage) string {
	switch contentType {
	case "messageSticker":
		return withSpace(nestedString(raw, "sticker", "emoji"))
	case "messageDocument":
		return withSpace(nestedString(raw, "document", "file_name"))
	case "messageAudio":
		return withSpace(nestedString(raw, "audio", "title"))
	case "messageVenue":
		return withSpace(nestedString(raw, "venue", "title"))
	case "messageGame":
		return withSpace(nestedString(raw, "game", "title"))
	case "messageContact":
		return withSpace(contactName(raw))
	case "messagePoll":
		// The question of a poll is not the name of it — a poll has no
		// name — and it is the whole of what a poll says, so it comes in
		// after a colon: "[poll: Friday at 19:00?]".
		return withColon(nestedString(raw, "poll", "question"))
	case "messageDice":
		return diceDetail(raw)
	default:
		return ""
	}
}

// contactName is what a contact message says about the person: the first and
// the last name, in the order a person writes them, and then the number,
// which is all a contact with no name has.
func contactName(raw json.RawMessage) string {
	var contact struct {
		FirstName string `json:"first_name"`
		LastName  string `json:"last_name"`
		Phone     string `json:"phone_number"`
	}
	if err := json.Unmarshal(nestedObject(raw, "contact"), &contact); err != nil {
		return ""
	}

	if name := strings.TrimSpace(
		contact.FirstName + " " + contact.LastName,
	); name != "" {
		return name
	}

	return strings.TrimSpace(contact.Phone)
}

// diceDetail is what a dice message says: the emoji it was thrown with, and
// the number it came up.
//
// Both fields are of the content itself and not of an object in it
// (td_api.tl:5232). A value of zero means the throw has not settled yet,
// and a zero on the screen is a die that landed on a side with no pips on
// it: the emoji alone is the whole of what is known, so that is all that is
// said.
func diceDetail(raw json.RawMessage) string {
	emoji := strings.TrimSpace(nestedString(raw, "emoji"))
	if emoji == "" {
		return ""
	}

	value := nestedInt(raw, "value")
	if value < 1 {
		return withSpace(emoji)
	}

	return withSpace(emoji + " " + strconv.Itoa(value))
}

// servicePhrases is what a service message says, per @type.
//
// A service message is something that happened in the chat rather than
// something somebody wrote, so it has no word and no brackets: the interface
// draws the phrase as a row of the feed of its own, in the muted step of the
// text ramp, the way a phone draws it. The phrases are the interface's
// English, like every other word on the screen.
//
// The kinds whose phrase is not one fixed sentence are not here, and see
// servicePhrase: a topic that was closed and a topic that is reopened are
// the same @type with the opposite flag, and a custom action is TDLib's own
// sentence for something it has no other way of describing.
var servicePhrases = map[string]string{
	"messageBasicGroupChatCreate":           "created the group",
	"messageSupergroupChatCreate":           "created the group",
	"messageChatUpgradeFrom":                "upgraded the group",
	"messageChatUpgradeTo":                  "upgraded the group",
	"messageChatChangeTitle":                "renamed the chat to",
	"messageChatChangePhoto":                "changed the chat photo",
	"messageChatDeletePhoto":                "deleted the chat photo",
	"messageChatOwnerLeft":                  "left the chat",
	"messageChatOwnerChanged":               "is now the owner of the chat",
	"messageChatHasProtectedContentToggled": "changed the protection of the chat",
	"messageChatAddMembers":                 "added",
	"messageChatJoinByLink":                 "joined the chat by a link",
	"messageChatJoinByRequest":              "joined the chat",
	"messageChatJoinFromCommunity":          "joined the chat from a community",
	"messageChatDeleteMember":               "removed a member",
	"messageChatAddedToCommunity":           "added the chat to a community",
	"messageChatRemovedFromCommunity":       "removed the chat from a community",
	"messagePinMessage":                     "pinned a message",
	"messageScreenshotTaken":                "took a screenshot",
	"messageChatSetBackground":              "changed the background",
	"messageChatSetTheme":                   "changed the theme of the chat",
	"messageChatSetMessageAutoDeleteTime":   "changed the timer of the messages",
	"messageChatBoost":                      "boosted the chat",
	"messageForumTopicCreated":              "created the topic",
	"messageForumTopicEdited":               "edited the topic",
	"messageVideoChatScheduled":             "scheduled a video chat",
	"messageVideoChatStarted":               "started a video chat",
	"messageVideoChatEnded":                 "ended the video chat",
	"messagePollOptionAdded":                "added an option to the poll",
	"messagePollOptionDeleted":              "deleted an option of the poll",
	"messageGameScore":                      "played a game",
	"messageGiveawayCreated":                "created a giveaway",
	"messageGiveawayCompleted":              "ended the giveaway",
	"messageGiveawayWinners":                "chose the winners of the giveaway",
	"messageContactRegistered":              "added a contact",
}

// servicePhrase returns what a service message says, or nothing where the
// content is not a service message this build has words for.
func servicePhrase(contentType string, raw json.RawMessage) string {
	if phrase, ok := servicePhrases[contentType]; ok {
		return phrase
	}

	switch contentType {
	case "messageCustomServiceAction":
		// The sentence of a custom action is TDLib's own words for
		// something it has no other way of describing, and it is the whole
		// of the message: there is nothing to say around it.
		return strings.TrimSpace(nestedString(raw, "text"))
	}

	// Two service messages are two phrases in one @type, and the flag says
	// which one: a topic that was closed and a topic that is reopened are
	// the same message with the other value of the flag (td_api.tl:5406).
	var toggled struct {
		Closed bool `json:"is_closed"`
		Hidden bool `json:"is_hidden"`
	}
	if err := json.Unmarshal(raw, &toggled); err != nil {
		return ""
	}

	switch contentType {
	case "messageForumTopicIsClosedToggled":
		if toggled.Closed {
			return "reopened the topic"
		}

		return "closed the topic"
	case "messageForumTopicIsHiddenToggled":
		if toggled.Hidden {
			return "unhidden the topic"
		}

		return "hidden the topic"
	default:
		return ""
	}
}

// serviceDetail returns the words a service message adds to its phrase: the
// new name of a chat, the name of a topic, and the number of members that
// were added.
//
// They are text from Telegram like any other, so the interface cleans them
// on the way to the screen as it cleans a chat title (#53).
func serviceDetail(contentType string, raw json.RawMessage) string {
	switch contentType {
	case "messageChatChangeTitle":
		return withSpace(nestedString(raw, "title"))
	case "messageForumTopicCreated":
		return withSpace(nestedString(raw, "name"))
	case "messageChatAddMembers":
		return addedMembers(memberCount(raw))
	default:
		return ""
	}
}

// addedMembers is what a message about added members adds to the word
// "added": one member, or the number of them.
//
// The schema carries a vector of identifiers rather than a number
// (td_api.tl:5346), and a chat that gained four people in one go is not the
// same row as one that gained one: "added a member" in two rows of a feed is
// a sentence a reader cannot tell one row from the other. An empty vector is
// not a message Telegram sends, and one member is the smallest answer that
// is still a sentence.
func addedMembers(count int) string {
	switch {
	case count < 2:
		return " a member"
	case count == 2:
		return " 2 members"
	default:
		return " " + strconv.Itoa(count) + " members"
	}
}

// memberCount is how many members a message carried in the vector of
// identifiers the schema gives it, and zero where it carried none.
func memberCount(raw json.RawMessage) int {
	var members struct {
		MemberUserIDs []tdInt `json:"member_user_ids"`
	}
	if err := json.Unmarshal(raw, &members); err != nil {
		return 0
	}

	return len(members.MemberUserIDs)
}

// nestedObject returns the object of a payload under the given name, and
// nothing where there is no such field.
func nestedObject(raw json.RawMessage, name string) json.RawMessage {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil
	}

	return fields[name]
}

// nestedValue reads the value at a path of a payload: one name is a field
// of the content itself, two are a field of an object inside it.
//
// Only the field on the path is read, and not the object around it. A
// decoder of an object of strings refuses a payload whose neighbour is a
// number — and a dice message is a number beside a string (td_api.tl:5232) —
// so reading the object would lose the field because of a field that has
// nothing to do with it.
//
// A field that is absent, of the wrong shape or of the wrong kind is a field
// this build cannot read, and it reads as nothing: a label without the
// detail is still a true sentence, and a label whose detail was wrong is
// not.
func nestedValue[T any](raw json.RawMessage, path ...string) (T, bool) {
	var value T
	if len(path) == 0 {
		return value, false
	}

	current := raw
	for _, step := range path[:len(path)-1] {
		current = nestedObject(current, step)
		if len(current) == 0 {
			return value, false
		}
	}

	if err := json.Unmarshal(nestedObject(current, path[len(path)-1]), &value); err != nil {
		return value, false
	}

	return value, true
}

// nestedString reads the string at a path of a payload, and nothing where
// it is not one.
func nestedString(raw json.RawMessage, path ...string) string {
	value, _ := nestedValue[string](raw, path...)

	return value
}

// nestedInt reads the whole number at a path of a payload, and zero where it
// is not one: the numbers read here are counts, and a count this build
// cannot read is no count.
func nestedInt(raw json.RawMessage, path ...string) int {
	value, _ := nestedValue[int](raw, path...)

	return value
}

// spaced returns text after a space, or nothing where there is no text.
func withSpace(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}

	return " " + text
}

// coloned returns text after a colon, or nothing where there is no text.
func withColon(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}

	return ": " + text
}

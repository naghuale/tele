package tui

import "time"

// This file holds the two projections the interface draws from: a chat and
// a message. They are the whole vocabulary of the screen, and every field
// here is a thing a user can be shown.

// ChatKind is what kind of chat a row is, which is what the interface needs
// to know to name the other side of a message and to say how loud a row is.
//
// A channel and a group are told apart from a private chat because the
// words differ: a channel's messages are from the channel, a group's are
// from the people in it, and a private chat has exactly one other person in
// it and no author to name at all.
type ChatKind uint8

const (
	// ChatKindPrivate is a chat with one other person, or with oneself.
	ChatKindPrivate ChatKind = iota

	// ChatKindGroup is a group: every message names the person who sent it.
	ChatKindGroup

	// ChatKindChannel is a channel: the messages are from the channel, and
	// the channel's own name is the author.
	ChatKindChannel
)

// Grouped reports whether a chat has more than one person in it, which is
// what decides whether a message names its author and whether a row's
// unread badge is drawn in the accent.
func (k ChatKind) Grouped() bool {
	return k == ChatKindGroup || k == ChatKindChannel
}

// String returns a stable lowercase name.
//
// A log line about a read of a chat names the kind of the chat, because the
// kind is what decides what TDLib accepts as the source of a read, and a
// word that says nothing in a diagnostic line is worse than no word.
func (k ChatKind) String() string {
	switch k {
	case ChatKindPrivate:
		return "private"
	case ChatKindGroup:
		return "group"
	case ChatKindChannel:
		return "channel"
	default:
		return "unknown"
	}
}

// Chat is one chat of the list, and the messages of it when it is open.
type Chat struct {
	ID      int64
	Title   string
	Unread  int
	Preview string

	// At is when the last message of the chat was sent, and it is what the
	// row of the list says: the time of day today, and Yesterday or a
	// date before that.
	//
	// It is the moment and not the string that was written out, because the
	// row has to be able to tell today from yesterday — which is a question
	// about the day and not about the hour — and because the hour is
	// written in the format and the zone of the machine, which is the view's
	// business and not the projection's. See clock.go.
	At time.Time

	// LastReadInboxMessageID is the last incoming message of this chat that
	// Telegram has been told was read, and it is where the unread line of
	// the feed goes.
	//
	// It is zero for a chat with nothing read in it and for a chat the
	// source could not say anything about, and both draw no unread line: a
	// line over a chat nobody has unread messages in is a claim about a
	// read that nobody made.
	LastReadInboxMessageID int64

	// Kind is what kind of chat this is.
	Kind ChatKind

	// Pinned is that a person pinned this chat in the main list of
	// Telegram, which is why it stands above the rest however old its last
	// message is.
	//
	// The pin belongs to the place of the chat in the list rather than to
	// the chat, and Telegram keeps it there: telecli reads it off the
	// position and marks the row with it, so that a reader can see why the
	// chat is where it is. telecli does not pin anything itself (#46).
	Pinned bool

	// Muted is that the chat has been silenced in Telegram, which is what
	// keeps it out of the number the header of the list draws.
	//
	// It is the chat's own mute and not the account's: a chat that defers
	// to the settings of its kind is not muted here, because those settings
	// are not about this list.
	Muted bool

	// Aliases are the other names this chat is found by in the search of
	// §9, and not the words of its title.
	//
	// A chat with oneself is called "Saved Messages" by Telegram and
	// "Избранное" by the person using it, and a user who is looking for it
	// types the word they know. The aliases never reach the screen: they
	// are a way of finding a row, not a second name for it.
	Aliases []string

	Messages []Message
}

// Message is one message of a conversation.
type Message struct {
	ID       int64
	Outgoing bool
	Text     string

	// At is when Telegram dated the message, and it is everything the screen
	// needs to know about when: the time of day above the text, which day of
	// the conversation it belongs to, and where the row goes among the
	// messages of the queue.
	//
	// It was a string written out — "09:40", "21:35" — beside this moment,
	// and that string is what the screen drew. Two things were lost by it,
	// and both were found by an owner rather than by a test. A string of a
	// clock time cannot be compared, so a message of the queue could only be
	// placed among the history by guessing which day it was; and a string
	// has no day in it at all, so a conversation three days long had no
	// boundaries and a reader could not tell a message of this morning from
	// one of last week.
	//
	// The moment is kept, the string is not built here, and everything that
	// wants a time asks the model — which knows the zone and the format of
	// the machine. See clock.go.
	At time.Time

	// Author is the name shown above an incoming message: the name of the
	// other person in a private chat, the channel's own name in a channel,
	// and the name of whoever sent it in a group.
	//
	// It is empty for an outgoing message, which is not named at all, and
	// for a projection that could not find out — the view then says
	// "Unknown" rather than leaving a row with nothing in it.
	Author string

	// AuthorID is the identifier the author colour is derived from, so
	// that the same person keeps the same colour between two messages and
	// between two conversations.
	AuthorID int64

	// Media is the word for what the message carries when it is not only
	// text: "photo", "video", "GIF", "file", "sticker", "voice note", or
	// the @type of a content nothing is known about yet, and "" for a
	// message with nothing but words.
	Media string

	// MediaDetail is what there is to say about what the message carries
	// beyond the word: the emoji of a sticker, the name of a file, the
	// question of a poll. It comes with its own separator — a colon for
	// the poll, a space for the rest — and the screen puts the word and
	// the detail inside one pair of brackets.
	MediaDetail string

	// Caption is the text under a picture or a file, which Telegram keeps
	// apart from the text of the message.
	Caption string

	// Service is what happened in the chat rather than what was written
	// in it — "joined the chat", "pinned a message" — and it is drawn as a
	// row of the feed of its own, in the muted step of the text ramp,
	// rather than as a message somebody wrote.
	Service string

	// AlbumID groups the parts of one album. Telegram sends an album as
	// consecutive messages that share it, and the interface draws one
	// entry for the album rather than one per part.
	AlbumID int64
}

// The moment and the zone the mock screen is drawn at.
//
// The mock data is deterministic by construction: every moment in it is a
// moment on this day in this zone, and the mock model reads its clock from
// here rather than from the machine. A mock screen that took the hour from
// the machine would be a different screen in every hour of the day, and the
// day labels of the feed — Today, Yesterday — would be a different set of
// them on every day of the year.
var (
	mockZone  = time.FixedZone("UTC+3", 3*60*60)
	mockClock = time.Date(2026, 3, 14, 10, 3, 0, 0, mockZone)
)

// mockMoment returns the moment of a clock time of the mock day, in the mock
// zone.
//
// It takes "10:02" and gives back a time.Time, because the alternative is a
// moment written out three times over in every fixture and a fixture whose
// hour nobody can read at a glance. The day and the zone are the ones above,
// so every mock moment is on the same day as every other one — which is what
// makes the mock feed a single day of a conversation with a Today above it.
func mockMoment(clock string) time.Time {
	hour, minute := 0, 0

	for index, letter := range clock {
		if letter == ':' {
			continue
		}

		digit := int(letter - '0')
		if index < 2 {
			hour = hour*10 + digit
			continue
		}
		minute = minute*10 + digit
	}

	return time.Date(2026, 3, 14, hour, minute, 0, 0, mockZone)
}

// mockChats returns deterministic mock data. No time.Now().
func mockChats() []Chat {
	return []Chat{
		{
			ID:      1,
			Title:   "Alice",
			Unread:  2,
			Preview: "How are you?",
			At:      mockMoment("10:02"),
			Messages: []Message{
				{ID: 1, Text: "Hello", At: mockMoment("10:00"), Author: "Alice", AuthorID: 7},
				{ID: 2, Outgoing: true, Text: "Hi", At: mockMoment("10:01")},
				{ID: 3, Text: "How are you?", At: mockMoment("10:02"), Author: "Alice", AuthorID: 7},
			},
		},
		{
			ID:      2,
			Title:   "Dev Team",
			Unread:  0,
			Preview: "PR merged",
			At:      mockMoment("09:05"),
			Kind:    ChatKindGroup,
			Messages: []Message{
				{
					ID: 1, Text: "PR-02 is ready", At: mockMoment("09:00"),
					Author: "Marta", AuthorID: 21,
				},
				{
					ID: 2, Outgoing: true, Text: "Проверка Unicode",
					At: mockMoment("09:05"),
				},
			},
		},
		{
			ID:      3,
			Title:   "Saved Messages",
			Unread:  5,
			Preview: "Note",
			At:      mockMoment("08:01"),
			Aliases: []string{"Избранное"},
			Messages: []Message{
				{ID: 1, Outgoing: true, Text: "Привет", At: mockMoment("08:00")},
				{ID: 2, Outgoing: true, Text: "Note", At: mockMoment("08:01")},
			},
		},
	}
}

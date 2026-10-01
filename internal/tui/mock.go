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

	// Time is the time of the last message of the chat, as HH:MM.
	//
	// It is a string and not a time.Time because the interface never
	// computes anything with it: it is placed at the right edge of the
	// title row, and the zone and the format belong to whoever projected
	// the message rather than to the view.
	Time string

	// Kind is what kind of chat this is.
	Kind ChatKind

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
	Time     string

	// At is when Telegram dated the message, and it is what a message of
	// the queue is placed by.
	//
	// Time above is the same moment written out, and it is what the screen
	// draws. It is a string, so it cannot be compared: "09:40" and "21:35"
	// of two different days are both clock times, and a message that goes
	// by them can only be placed by guessing which day it is. So the moment
	// itself is kept as well, and it is used for one thing only — where the
	// row goes in the conversation.
	//
	// What is written on screen is Time, and changing that is #62.
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

// mockChats returns deterministic mock data. No time.Now().
func mockChats() []Chat {
	return []Chat{
		{
			ID:      1,
			Title:   "Alice",
			Unread:  2,
			Preview: "How are you?",
			Time:    "10:02",
			Messages: []Message{
				{ID: 1, Text: "Hello", Time: "10:00", Author: "Alice", AuthorID: 7},
				{ID: 2, Outgoing: true, Text: "Hi", Time: "10:01"},
				{ID: 3, Text: "How are you?", Time: "10:02", Author: "Alice", AuthorID: 7},
			},
		},
		{
			ID:      2,
			Title:   "Dev Team",
			Unread:  0,
			Preview: "PR merged",
			Time:    "09:05",
			Kind:    ChatKindGroup,
			Messages: []Message{
				{
					ID: 1, Text: "PR-02 is ready", Time: "09:00",
					Author: "Marta", AuthorID: 21,
				},
				{
					ID: 2, Outgoing: true, Text: "Проверка Unicode",
					Time: "09:05",
				},
			},
		},
		{
			ID:      3,
			Title:   "Saved Messages",
			Unread:  5,
			Preview: "Note",
			Time:    "08:01",
			Aliases: []string{"Избранное"},
			Messages: []Message{
				{ID: 1, Outgoing: true, Text: "Привет", Time: "08:00"},
				{ID: 2, Outgoing: true, Text: "Note", Time: "08:01"},
			},
		},
	}
}

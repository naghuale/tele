package tui

import (
	"time"
)

// The presence of the other side of a chat: the word §3.1 draws next to the
// name in the header.
//
// It is a fact about a person, and it is the only part of this program that
// is: whether somebody is online and when they were last there is theirs to
// share or not. So the value here is what the header draws and nothing else,
// the store upstream keeps no names and no phone numbers, and the printing
// of a presence says what kind of presence it is rather than the times it
// holds.

// PresenceKind is what the presence of a chat is about.
type PresenceKind uint8

const (
	// PresenceNone means there is nothing to say: an unknown chat, a user
	// whose status has not arrived, or a chat with oneself.
	PresenceNone PresenceKind = iota

	// PresenceUser is a private or a secret chat.
	PresenceUser

	// PresenceGroup is a group, a supergroup or a channel.
	PresenceGroup
)

// PresenceRecency is how recently somebody was seen, when TDLib reports a
// range rather than a time.
type PresenceRecency uint8

const (
	// RecencyNone means TDLib reported a time, or nothing at all.
	RecencyNone PresenceRecency = iota
	RecencyRecently
	RecencyLastWeek
	RecencyLastMonth
)

// Presence is the presence of the other side of the open chat.
//
// A time in it is a Unix time from TDLib. Whether an online status has run
// out is decided by the view against the model's clock, and not here: the
// value carries the deadline Telegram gave, and the clock says whether it
// is still in the future.
type Presence struct {
	Kind PresenceKind

	// Bot says that the other side is a bot. A bot's online status is not
	// what a reader is looking for, and the header says "bot" and stops.
	Bot bool

	// Self says that this chat is with the user themself, which is what
	// Saved Messages is. The status of the user in it is the user's own,
	// and drawing it would put "Online" in a chat with oneself.
	Self bool

	// ExpiresAt is when an online status runs out. TDLib sends no update
	// at that moment, so a presence line has to be recomputed from the
	// clock to stop saying Online.
	ExpiresAt time.Time

	// LastSeenAt is when the other side was last online: the was_online of
	// an offline status, and the expires of an online one that has run out.
	LastSeenAt time.Time

	// Recency is the range TDLib reports when it has no time.
	Recency PresenceRecency

	// OnlineMembers is how many members of a group are online. Zero means
	// TDLib has not counted, which is not the same as nobody.
	OnlineMembers int
}

// String returns the kind of presence and nothing else.
//
// A presence on its way to a log says "presence: user" and stops. Whether
// somebody is online is not a diagnostic, and a log file is read by people
// who are not in the conversation.
func (p Presence) String() string {
	switch p.Kind {
	case PresenceUser:
		return "presence: user"

	case PresenceGroup:
		return "presence: group"

	default:
		return "presence: none"
	}
}

// GoString returns the same as String, so %#v cannot print more than the
// kind either.
func (p Presence) GoString() string { return p.String() }

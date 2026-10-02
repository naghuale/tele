package tui

import (
	"fmt"
	"strings"
)

// The number at the right end of the header of the chat list, and what it
// counts.
//
// It is the one number on the screen a user scans for, and it is the number
// that was wrong: it was the sum of the unread messages of every chat in the
// list, so an account with one busy channel stood at "99+" whatever the rows
// said — while every row of the list said a handful. The owner's account, on
// 02.10: the header read "Chats · 99+ unread" and the badges beside the names
// were small numbers.
//
// Telegram's own counter counts chats, and leaves out the ones a person has
// silenced: a counter that counts a chat they muted is a counter about a
// conversation they asked not to hear about. So the default here counts the
// same thing, and the other two values are for the reader who wants the old
// sum or wants no counter at all.
//
// The mode is a field of the model rather than a question of the
// configuration per frame, for the reason the theme and the clock are: the
// setting is resolved once, where the terminal is resolved, and every frame
// draws the answer rather than re-reading the file.

// UnreadCounterMode is what the counter of the header counts.
type UnreadCounterMode uint8

const (
	// UnreadCounterChats counts the chats that have something unread in
	// them, and leaves out the ones that have been silenced.
	//
	// It is the zero value on purpose: it is the default, and a model built
	// as a value rather than by a constructor draws the counter a user who
	// configured nothing expects. A struct field of this counter that
	// started at off would mean that a screen nobody asked about was drawn
	// with no number on it.
	UnreadCounterChats UnreadCounterMode = iota

	// UnreadCounterMessages counts the unread messages of every chat in the
	// list, silenced or not.
	//
	// It is what the header used to say, and it is here because a person
	// who wants to know how much is waiting rather than where may ask for
	// it. It is a sum over the whole list, which is why it is not the
	// default: the sum is over every chat Telegram keeps in the main list,
	// and a channel with a thousand messages behind it decides the number
	// on its own.
	UnreadCounterMessages

	// UnreadCounterOff draws no number at all.
	//
	// The row of every chat still carries its own badge: this is the
	// counter of the header and not the unread state of the list.
	UnreadCounterOff
)

// unreadCounterModeNames are the words of the setting, as they are written
// in the configuration file and reported by telecli doctor.
var unreadCounterModeNames = [...]string{"chats", "messages", "off"}

// String returns the name of the mode as it is written in the configuration
// and in telecli doctor.
func (m UnreadCounterMode) String() string {
	if int(m) < len(unreadCounterModeNames) {
		return unreadCounterModeNames[m]
	}

	return "unknown"
}

// ParseUnreadCounterMode returns the mode a configured word names.
//
// An empty word is the default, because a configuration written before the
// setting existed has none — and a program that started counting chats where
// it used to count messages would be a change nobody asked for. A word that
// is not one of the three is an error with the three in it: an interface
// setting that is quietly ignored is something a user finds out about from a
// screen that is drawn wrong.
func ParseUnreadCounterMode(value string) (UnreadCounterMode, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "chats":
		return UnreadCounterChats, nil
	case "messages":
		return UnreadCounterMessages, nil
	case "off":
		return UnreadCounterOff, nil
	default:
		return UnreadCounterChats, fmt.Errorf(
			"tui.unread_counter must be chats, messages or off, not %q",
			value,
		)
	}
}

// DescribeUnreadCounter returns what telecli doctor prints for the setting.
//
// It is the setting and not a count: the doctor prints lines and leaves, and
// what is unread right now is a question about an account it is not looking
// at.
func DescribeUnreadCounter(configured UnreadCounterMode) string {
	return configured.String()
}

// chatListUnreadTotal is what the header of the chat list counts, and
// whether it counts anything at all.
//
// The two modes answer different questions and neither is the other: a chat
// is what a user acts on, and a message is what is waiting inside it. The
// silenced chats are left out of the first, because a person who muted a
// chat has said they do not want to be reminded of it, and a counter that
// counts it anyway is a counter that cannot be silenced at all.
func (m Model) chatListUnreadTotal() (int, bool) {
	switch m.unreadCounter {
	case UnreadCounterOff:
		return 0, false
	case UnreadCounterMessages:
		messages := 0
		for _, chat := range m.chats {
			messages += chat.Unread
		}

		return messages, true
	default:
		unread := 0
		for _, chat := range m.chats {
			if chat.Unread > 0 && !chat.Muted {
				unread++
			}
		}

		return unread, true
	}
}

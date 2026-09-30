package tui

import (
	"strconv"
	"time"
)

// The words of §3.1 and §4.3 for the presence of the other side of a chat.
//
// Two rules run through all of them. A presence that is not known is drawn
// as nothing, because the interface has no word for "I do not know" and a
// made-up one is a claim about a person that nothing checked. And an
// online status that has run out is drawn as a last-seen time, because
// Telegram promised "Online" until a moment that has passed and said
// nothing since.

const (
	presenceOnlineText = "online"
	presenceBotText    = "bot"

	presenceLastSeenAtText  = "last seen at "
	presenceYesterdayPrefix = "last seen yesterday at "
	presenceLastSeenOnText  = "last seen "
	presenceRecentlyText    = "last seen recently"
	presenceLastWeekText    = "last seen within a week"
	presenceLastMonthText   = "last seen within a month"
)

// presenceText returns what the header says about the other side, or "".
func presenceText(presence Presence, now time.Time, location *time.Location) string {
	// A chat with oneself says nothing: the status in it is the user's own,
	// and "Online" over Saved Messages is a fact about nobody.
	if presence.Self {
		return ""
	}

	switch presence.Kind {
	case PresenceGroup:
		if presence.OnlineMembers <= 0 {
			return ""
		}

		return strconv.Itoa(presence.OnlineMembers) + " online"

	case PresenceUser:
		return userPresenceText(presence, now, location)

	default:
		return ""
	}
}

// userPresenceText returns what the header says about one other person.
func userPresenceText(
	presence Presence,
	now time.Time,
	location *time.Location,
) string {
	if presence.Bot {
		return presenceBotText
	}

	// An online status is a promise with a deadline, and the deadline is
	// the only thing that ends it: Telegram sends no update at that
	// moment, so the clock decides what the word is.
	if !presence.ExpiresAt.IsZero() {
		if now.Before(presence.ExpiresAt) {
			return presenceOnlineText
		}

		return lastSeenText(presence.ExpiresAt, now, location)
	}
	if !presence.LastSeenAt.IsZero() {
		return lastSeenText(presence.LastSeenAt, now, location)
	}

	switch presence.Recency {
	case RecencyRecently:
		return presenceRecentlyText
	case RecencyLastWeek:
		return presenceLastWeekText
	case RecencyLastMonth:
		return presenceLastMonthText
	default:
		return ""
	}
}

// lastSeenText names the moment somebody was last there.
//
// Today and yesterday get the time, because "today" is the difference
// between a message that may be answered and one from last week. Earlier
// than that gets the date, because a time without a day is a time of
// yesterday and a reader has to know it is not.
func lastSeenText(seen, now time.Time, location *time.Location) string {
	if location == nil {
		location = time.Local
	}

	at := seen.In(location)
	today := now.In(location)

	switch {
	case at.Year() == today.Year() &&
		at.YearDay() == today.YearDay():
		return presenceLastSeenAtText + at.Format("15:04")

	case at.Year() == today.Year() &&
		at.AddDate(0, 0, 1).YearDay() == today.YearDay():
		return presenceYesterdayPrefix + at.Format("15:04")

	default:
		return presenceLastSeenOnText + at.Format("2 Jan")
	}
}

// presenceExpiredMsg repaints the screen when an online status runs out.
//
// The data does not change at that moment: an online status carries the
// moment it expires and the view reads the clock, so the screen has to be
// repainted or the header keeps saying Online to somebody who is not there.
//
// It is one message at the moment the word changes, not a timer that wakes
// the program: §6.3 rules out a repaint loop, and this is not one.
type presenceExpiredMsg struct {
	expires time.Time
}

package tui

import (
	"fmt"
	"strings"
	"time"
)

// The clock the interface draws times in, and the words that say which day
// a moment was on.
//
// Every moment on the screen — the time of a message, the time of the last
// message of a chat, the moment somebody was last seen — is an instant, and
// an instant has no spelling until somebody says which zone it is read in
// and whether the hour is written as twelve or as twenty-four. Both of those
// are the machine's, not Telegram's: a message sent at 21:21 UTC is 07:21
// in Vladivostok and the program that showed 21:21 was answering a question
// nobody asked.
//
// So a moment crosses into this package as a time.Time and leaves it as a
// string, and the string is built here from the format of the machine and
// the zone of the model. Nothing anywhere else builds one: the projection
// that carried these moments did it in UTC and in a fixed layout, which is
// how a UTC+10 user came to read Telegram's 07:21 as 21:21.

// ClockMode is the setting as it is written in the configuration file: the
// machine's own answer, twelve hours, or twenty-four.
type ClockMode uint8

const (
	// ClockAuto asks the machine what it does.
	//
	// It is the default: a configuration that names no clock is a
	// configuration that has not decided, and the machine is the only party
	// that knows.
	ClockAuto ClockMode = iota

	// Clock12h writes the hour as it is written in English-speaking
	// countries: 08:06 PM, 07:21 AM.
	Clock12h

	// Clock24h writes the hour as it is written almost everywhere else:
	// 20:06, 07:21.
	//
	// It is also what a machine nobody could ask about is drawn with: see
	// SystemSetting below, where nothing read is 24 hours.
	Clock24h
)

// clockModeNames are the words of the setting, in the order telecli doctor
// reports them.
var clockModeNames = [...]string{"auto", "12h", "24h"}

// String returns the name of the mode as it is written in the
// configuration and in telecli doctor.
func (m ClockMode) String() string {
	if int(m) < len(clockModeNames) {
		return clockModeNames[m]
	}

	return "unknown"
}

// ResolveSystemClock returns what the machine says about the hour it writes,
// and resolves the configured setting against it.
//
// It is asked once by the composition root and nowhere else: the interface
// draws frames and asks the machine nothing between them, and a screen whose
// times change format halfway through a session is a screen whose bytes
// depend on when the frames happened to be drawn. A model that asked for
// itself would also be a model whose format depends on who built it first.
//
// A machine that cannot be asked is drawn in twenty-four hours. See
// SystemSetting for why that is the answer rather than a refusal.
func ResolveSystemClock(configured ClockMode) ClockFormat {
	return ResolveClock(configured, readSystemClock())
}

// ParseClockMode returns the mode a configured word names.
//
// An empty word is the default, because a configuration written before the
// setting existed has none. A word that is not one of the three is an error
// with the three in it: an interface setting that is quietly ignored is
// something a user finds out about from a screen that is drawn wrong.
func ParseClockMode(value string) (ClockMode, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "auto":
		return ClockAuto, nil
	case "12h":
		return Clock12h, nil
	case "24h":
		return Clock24h, nil
	default:
		return ClockAuto, fmt.Errorf(
			"tui.clock must be auto, 12h or 24h, not %q",
			value,
		)
	}
}

// SystemSetting is what the machine says about the hour it writes, which is
// what ClockAuto is resolved against.
//
// It is three words and not a boolean because the third one carries the whole
// of the risk: a machine whose setting could not be read has no answer, and
// twenty-four hours is what it is drawn with. A chat program that showed
// nothing at all, or refused to start, over a preference it could not read
// would be worse than one that guessed.
type SystemSetting uint8

const (
	// SystemUnknown is that nothing was read: either the machine has no
	// setting to read, or nobody has asked.
	SystemUnknown SystemSetting = iota

	// System12h is a machine set to a twelve-hour clock.
	System12h

	// System24h is a machine set to a twenty-four-hour clock.
	System24h
)

// String returns the word telecli doctor writes for what the machine said.
func (s SystemSetting) String() string {
	switch s {
	case System12h:
		return "12h"
	case System24h:
		return "24h"
	default:
		return "unknown"
	}
}

// ClockFormat is the resolved answer: the hour as it is actually written.
//
// There is no automatic form of it. A format is what the screen is drawn
// with, and a screen is drawn with one of two, so resolving is where the
// question of the machine is asked and where it stops being asked.
type ClockFormat uint8

const (
	// ClockFormat24h writes 20:06, and is the format of a machine nobody
	// could ask about.
	//
	// It is the zero value on purpose: a model built as a value rather than
	// by a constructor draws twenty-four hours, which is the answer for a
	// machine nothing was known about. A struct field of a clock that
	// started at twelve would mean that a screen nobody configured was
	// drawn in the format of an American keyboard.
	ClockFormat24h ClockFormat = iota

	// ClockFormat12h writes 8:06 PM.
	ClockFormat12h
)

// String returns the name of the format as it is written in the
// configuration.
func (f ClockFormat) String() string {
	if f == ClockFormat12h {
		return "12h"
	}

	return "24h"
}

// layout is the Go layout of a time of day in this format.
//
// The twelve-hour one is `03:04 PM` rather than `3:04 PM`: Telegram writes
// "07:21 AM" and not "7:21 AM", and a clock that reads differently from the
// one in the pocket is a clock a reader has to translate before they can
// look at it. The AM or the PM has a space before it, which is what the
// layout says and what `03:04PM` would not.
func (f ClockFormat) layout() string {
	if f == ClockFormat12h {
		return "03:04 PM"
	}

	return "15:04"
}

// ResolveClock turns the configured setting and what the machine says into
// the format the screen is drawn with.
//
// It is a function of its two arguments and not a call into the machine,
// because the machine is not available to a test: a test of the resolution
// that read AppleICUForce24HourTime would pass on the developer's Mac and
// fail in CI, which is a test about the runner rather than about the clock.
// The reading of the machine is ReadSystemClock, and it is the only place
// here that reaches out of the process.
//
// A configured mode is the answer and the machine is not asked at all: a
// user who wrote 12h wants twelve hours whether or not the system agrees,
// and asking anyway would be the setting being overridden by the thing it
// was written for.
func ResolveClock(configured ClockMode, system SystemSetting) ClockFormat {
	switch configured {
	case Clock12h:
		return ClockFormat12h
	case Clock24h:
		return ClockFormat24h
	default:
		if system == System12h {
			return ClockFormat12h
		}

		return ClockFormat24h
	}
}

// ClockSource is why the clock on the screen is what it is, which is what
// telecli doctor has to report: a format with no reason behind it is a
// promise, and this program only reports what it knows.
type ClockSource uint8

const (
	// ClockSourceSystem is the machine's own setting.
	ClockSourceSystem ClockSource = iota

	// ClockSourceConfigured is the word in the configuration file.
	ClockSourceConfigured

	// ClockSourceFallback is twenty-four hours because nothing answered.
	ClockSourceFallback
)

// String returns the word telecli doctor writes for the source.
func (s ClockSource) String() string {
	switch s {
	case ClockSourceConfigured:
		return "configured"
	case ClockSourceFallback:
		return "fallback"
	default:
		return "system"
	}
}

// ClockChoice is a clock and the reason it is that one.
type ClockChoice struct {
	Mode   ClockMode
	Source ClockSource
}

// String returns the line telecli doctor prints: the setting, and whether it
// was configured, asked of the machine, or the answer of a machine that did
// not answer.
//
// The reason is in the same line as the mode because a user who reads it has
// one question — why is this what I am seeing — and the answer belongs where
// the question is asked.
//
// Automatic is reported as the system, because that is where the answer comes
// from; what the machine actually says is a question for the command that
// draws the interface, and a doctor that measured the terminal would be
// reporting about a terminal it is not drawing in.
func (c ClockChoice) String() string {
	return c.Mode.String() + " (" + c.Source.String() + ")"
}

// DescribeClock returns what a configured clock resolves to when the machine
// is not asked.
//
// It is the form telecli doctor uses, for the same reason the width rule has
// one: the doctor is not the interface, it prints lines and leaves, and the
// setting of the machine is read once where the program draws.
func DescribeClock(configured ClockMode) ClockChoice {
	if configured == ClockAuto {
		return ClockChoice{Mode: ClockAuto, Source: ClockSourceSystem}
	}

	return ClockChoice{Mode: configured, Source: ClockSourceConfigured}
}

// clockText writes the time of day of a moment, or nothing when there is no
// moment to write.
//
// The zone is the model's, and it is a parameter rather than time.Local
// because a test that read the zone of the machine would draw "last seen at
// 15:00" in one country and "last seen at 02:00" in another, and both would
// be the code being right about a machine that is not the one under test.
func (m Model) clockText(at time.Time) string {
	if at.IsZero() {
		return ""
	}

	return at.In(m.timeZone()).Format(m.clockFormat().layout())
}

// clockFormat returns the format the screen is drawn with.
func (m Model) clockFormat() ClockFormat {
	return m.hourFormat
}

// The words a day of a conversation is named by.
//
// They are English because the whole interface is (the owner, 30.09), and
// they are the words Telegram uses, because a user who has read their phone
// has read these four shapes already: today, yesterday, a weekday, and a
// date.

// dayLabel names the day a moment was on, as the pill above the messages of
// it says it: Today, Yesterday, the weekday for the days of this week before
// them, and a date for everything older.
//
// The boundaries are the ones a reader of a conversation runs into rather
// than the ones a calendar draws: midnight, because that is where "yesterday"
// becomes "this week"; and the year, because "September 21" of last year is a
// different day from "September 21" and a reader of a week-old message has to
// be able to tell.
//
// A date is the whole name of its month, and the year is in it only when the
// day is not of this year. That is the owner's reference (02.10): a pill that
// says "Sep 20" is a pill a reader has to finish in their head, and one that
// says "20 September 2025" for a day of this year is a pill wider than the
// words of the message it stands over. So the month is spelled out — it is
// read, not counted — and the year is added exactly when it carries
// information the reader does not already have.
func (m Model) dayLabel(at time.Time) string {
	zone := m.timeZone()
	then := at.In(zone)
	now := m.clock()().In(zone)

	switch days := calendarDaysBetween(then, now); {
	case days == 0:
		return dayTodayText

	case days == 1:
		return dayYesterdayText

	// A weekday is only a weekday while the reader can still place it: on
	// the seventh day back it is a name of a day in some other week, and a
	// date says which week as well.
	case days >= 2 && days <= dayWeekdayDays:
		return then.Format("Monday")

	case then.Year() == now.Year():
		return then.Format("January 2")

	default:
		return then.Format("January 2, 2006")
	}
}

// The words of the separators.
const (
	dayTodayText     = "Today"
	dayYesterdayText = "Yesterday"

	// dayWeekdayDays is how far back a day is still named by its weekday.
	//
	// Six, so that the whole of the week before this one is six names and a
	// date: a reader who is looking for something from Tuesday can say
	// Tuesday about it for six days and then has to read a date.
	dayWeekdayDays = 6

	// unreadSeparatorText stands above the first message of a chat that has
	// not been read yet.
	unreadSeparatorText = "Unread messages"
)

// calendarDaysBetween returns how many calendar days lie between two
// moments in one zone: zero for the same day, one for the day before, and
// more for anything further back.
//
// It counts midnights rather than dividing by 24 hours, because a moment
// from yesterday at 23:50 is one day back and a moment from four days ago
// at 01:00 is four, while the difference between the two instants is 94
// hours and not four days. Both moments are already in the zone of the
// reader: a boundary of a day is a local midnight and not a UTC one.
func calendarDaysBetween(then, now time.Time) int {
	thenMidnight := time.Date(
		then.Year(), then.Month(), then.Day(), 0, 0, 0, 0, then.Location(),
	)
	nowMidnight := time.Date(
		now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location(),
	)

	return int(nowMidnight.Sub(thenMidnight).Hours() / 24)
}

// chatListTimeText names when the last message of a chat was, for the row of
// the chat list: the time today, Yesterday, a weekday earlier in the week,
// and a date before that.
//
// It is the same ladder as the separator of the feed with one rung changed:
// the time of day stands in for Today, because the row of a chat list is
// about the moment and the separator is about the day. Everything else is
// the same function for the same reason — a reader comparing a row with a
// separator is comparing two answers to "when was that".
func (m Model) chatListTimeText(at time.Time) string {
	if at.IsZero() {
		return ""
	}

	if m.dayLabel(at) != dayTodayText {
		return m.dayLabel(at)
	}

	return m.clockText(at)
}

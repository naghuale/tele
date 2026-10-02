package tui

import (
	"time"

	"telecli/internal/tui/theme"
)

// The rows of the feed that belong to no message: the name of the day a
// message is on, and the line where the unread messages of a chat begin.
//
// They are rows of the timeline and not decoration over it, and that is the
// whole of what makes them countable. A window placed by measuring the
// messages alone is placed too low by one row per day in the conversation, so
// the rows above it are empty — and the owner of this program found exactly
// that: a conversation of three days where the reader could not tell where
// one day ended and the next began, and a window that filled from the bottom
// as though there were nothing between the messages.

// entrySeparator is what stands above an entry of the feed.
//
// It is a field of the entry rather than a message of its own, and that is
// what makes the height of an entry the height the screen draws: the window
// is placed by walking back from the newest entry and adding what each entry
// really takes, and an entry that carries its separator takes it with it. A
// separator as a row of the conversation in its own right would have to be a
// message with an identifier and a place in the cursor's index space, and the
// cursor would then walk onto it — the cursor is a message of the
// conversation, and a day is not one.
type entrySeparator struct {
	// day is the name of the day the entry below it is on: Today,
	// Yesterday, a weekday, or a date. It is empty when the entry is not the
	// first of its day.
	day string

	// unread says that everything from here down has not been read in
	// Telegram yet.
	unread bool
}

// any reports whether there is anything to draw above the entry.
func (s entrySeparator) any() bool {
	return s.day != "" || s.unread
}

// entrySeparators returns what stands above each entry of the open
// conversation: the name of the day it opens, and the line the unread
// messages start at.
//
// Both are asked of the entries rather than of the two lists they are built
// from, for the reason the feed is one list: a message of the queue is a
// message of the conversation, and a queue row of yesterday standing between
// two messages of today is a row that opens no day and marks nothing.
//
// A day boundary is a boundary of a calendar in the zone of the reader, not
// of twenty-four hours: a conversation that crossed midnight in Vladivostok
// crosses it there and not in UTC, and the separator has to be where the
// reader's day ends.
//
// The unread line goes above the first incoming message whose identifier is
// past the read pointer of the chat, and nowhere else. Telegram's pointer is
// the last message it has been told was read, so everything above it has been
// read and everything from the next message down has not. A chat with no
// unread messages has no such message and draws no line: a line over a
// conversation the user has read to the end would be a claim about a read
// that nobody made.
func (m Model) entrySeparators(entries []timelineEntry) []entrySeparator {
	separators := make([]entrySeparator, len(entries))
	if len(entries) == 0 {
		return separators
	}

	zone := m.timeZone()

	previous := calendarDay{}
	first := true
	unreadDrawn := false

	for index, entry := range entries {
		at := m.entryMoment(entry)
		if at.IsZero() {
			continue
		}

		day := dayOf(at, zone)

		switch {
		case first:
			// The first entry of the feed opens a day even when the day
			// above it is off the screen: the reader has scrolled into the
			// middle of a conversation and the top row they can see has to
			// say which day they are looking at.
			separators[index].day = m.dayLabel(at)
			previous = day
			first = false

		case day != previous:
			separators[index].day = m.dayLabel(at)
			previous = day
		}

		if !unreadDrawn && m.opensUnreadRun(entry) {
			separators[index].unread = true
			unreadDrawn = true
		}
	}

	return separators
}

// opensUnreadRun reports whether this entry is where the unread messages of
// the chat begin.
//
// It is the first incoming message past the read pointer, and it is an
// incoming message: a chat's unread count is a count of what the other side
// said, and a line of one's own message would stand over a message nobody
// has sent. An outgoing message above the pointer is one this account sent
// after reading up to there, which is not unread at all.
//
// The pointer is the chat's as it was when the chat was opened, and it is
// remembered rather than read again because this program marks what is on the
// screen as read (message_viewing.go): a pointer read again after that would
// have moved past the very messages the line is about, and the line would go
// as soon as the reader looked at it.
func (m Model) opensUnreadRun(entry timelineEntry) bool {
	if entry.isPending() || entry.message.Outgoing {
		return false
	}
	if entry.message.ID <= m.unreadBoundary {
		return false
	}

	return m.unreadBoundary > 0
}

// entryMoment returns the moment an entry is placed by: the moment Telegram
// dated the message, or the moment the queue recorded the row.
func (m Model) entryMoment(entry timelineEntry) time.Time {
	if entry.isPending() {
		return entry.pending.CreatedAt
	}

	return entry.message.At
}

// calendarDay is a day of a calendar in one zone, which is what two moments
// are compared on to find out whether a day passed between them.
//
// It is three numbers and not a formatted string because the comparison is
// the whole of what it is for: the name of the day is written separately, in
// the words of the interface, and comparing two names would be a comparison
// of English.
type calendarDay struct {
	year  int
	month time.Month
	day   int
}

// dayOf returns the calendar day a moment falls on in a zone.
func dayOf(at time.Time, zone *time.Location) calendarDay {
	local := at.In(zone)

	return calendarDay{
		year:  local.Year(),
		month: local.Month(),
		day:   local.Day(),
	}
}

// separatorRows returns the rows that stand above an entry: the name of the
// day, and the line the unread messages start at.
//
// It is centred in the feed and in the muted step of the text ramp, because
// a separator belongs to neither side: it says something about the whole
// column rather than about one person's message in it, and a line drawn at
// the left edge above an incoming message reads as the start of that
// message's block.
//
// One row each and no more. A separator is a word in the middle of a
// conversation, and the blank row above the entry is already the air between
// one entry and the next: a separator of two rows would be a band where a
// word is meant.
func (m Model) separatorRows(
	separator entrySeparator,
	layout Layout,
	width int,
	styles viewStyles,
) []string {
	if !separator.any() {
		return nil
	}

	words := make([]string, 0, 2)
	if separator.day != "" {
		words = append(words, separator.day)
	}
	if separator.unread {
		words = append(words, unreadSeparatorText)
	}

	origin := conversationOrigin(LayoutFor(m.width, m.height))
	feed := styles.on(m.tokens().ChatBackground, styles.unstyled())
	muted := styles.dimmed(m.tokens().MutedText)

	rows := make([]string, 0, len(words))
	for _, word := range words {
		text := m.widths.Fit(word, maxInt(width-2*layout.FeedMargin(), 1), ellipsis)
		columns := m.widths.StringWidth(text)
		air := maxInt(width-columns, 0)

		rows = append(rows, m.painterIn(origin, theme.Color{}).
			own(feed, spaces(air/2)).
			own(muted, text).
			own(feed, spaces(air-air/2)).
			String())
	}

	return rows
}

// withSeparators returns the entries with what stands above each of them
// worked out: the name of the day the entry opens and the line the unread
// messages start at.
//
// It is asked once of the whole list rather than once per entry, because both
// of those are questions about the conversation rather than about a message:
// a day boundary is between two entries, and the unread line is at the first
// of several.
func (m Model) withSeparators(entries []timelineEntry) []timelineEntry {
	separators := m.entrySeparators(entries)
	for index := range entries {
		entries[index].separator = separators[index]
	}

	return entries
}

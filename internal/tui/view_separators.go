package tui

import (
	"time"

	"github.com/charmbracelet/lipgloss"

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

// separatorRows returns the rows that stand above an entry: the pill that
// names the day, and the pill that says where the unread messages begin.
//
// A separator is a PILL: a short run of columns with a background of its own,
// centred in the feed, with the words of the day in PrimaryText — the bright
// end of the text ramp, because a date is read and not counted.
//
// It was a row of dim words at the left of the feed on the background of the
// feed, and the owner read it as a message line (02.10, real account): at
// the left edge, in the muted step and on no surface of its own, it has the
// shape of a row of the conversation and nothing on the screen says
// otherwise. Three things are what makes it a pill instead:
//
//   - the surface, which is a step above the background of the feed, so the
//     row is a thing on the feed rather than words in it;
//   - the middle of the feed, where neither side of the conversation can
//     claim it, and where a reader looking for the day looks;
//   - the bright end of the text ramp, so the words are read at a glance.
//
// A band across the width of the feed is what it must not become: a band says
// the whole row belongs to one thing, and a day belongs to no message in it.
// So the pill is as wide as its words and two columns of air, and the rest of
// the row is the feed's own background.
//
// One row of air above and below the pill, and both of them are the pill's
// own: a pill flush against a message is a line of that message, which is the
// reading this is here to undo. So the air is not the gap row every entry
// already has, and entryLines spends its gap on a message with no pill above
// it rather than on two rows of air for one boundary.
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

	origin := conversationOrigin(layout)
	feed := styles.on(m.tokens().ChatBackground, styles.unstyled())
	pill := styles.on(m.tokens().SeparatorBackground, styles.text(m.tokens().PrimaryText))

	// The lane the pill is centred in is the feed, less the margin the feed
	// keeps on each side: a pill that reached the edge of the pane would be
	// a row of the pane rather than a thing in the middle of the feed.
	lane := maxInt(width-2*layout.FeedMargin(), 1)

	rows := make([]string, 0, 2*len(words)+2)
	rows = append(rows, m.feedRow(feed, origin, width))
	for index, word := range words {
		if index > 0 {
			// Air between two pills and not one row shared by both: the
			// name of a day and the line of the unread are two facts, and a
			// reader who cannot tell where one ends and the other begins is
			// reading a sentence.
			rows = append(rows, m.feedRow(feed, origin, width))
		}

		rows = append(rows, m.pillRow(word, lane, width, origin, feed, pill))
	}

	return append(rows, m.feedRow(feed, origin, width))
}

// feedRow returns one row of the feed with nothing on it but the background
// of the feed.
func (m Model) feedRow(feed lipgloss.Style, origin, width int) string {
	return m.painterIn(origin, theme.Color{}).
		own(feed, spaces(width)).
		String()
}

// pillRow returns one row with the pill of a word centred in the lane, and
// the background of the feed everywhere else.
//
// The word carries one column of air on each side inside the pill: a pill is
// the shape of the words and the air around them, and words flush against the
// edge of their own surface read as a band rather than a pill.
func (m Model) pillRow(
	word string,
	lane, width, origin int,
	feed, pill lipgloss.Style,
) string {
	// The words are cut to what the lane holds and are not padded to it: a
	// word run out to the width of the lane is a band, and the whole of what
	// makes this a pill is that it stops where the words stop.
	room := maxInt(lane-2*separatorPillAirColumns, 1)
	inner := m.widths.TruncateMarked(word, room, ellipsis)
	columns := m.widths.StringWidth(inner) + 2*separatorPillAirColumns

	air := maxInt(lane-columns, 0)

	return m.painterIn(origin, theme.Color{}).
		own(feed, spaces(air/2)).
		own(pill, spaces(separatorPillAirColumns)).
		own(pill, inner).
		own(pill, spaces(separatorPillAirColumns)).
		own(feed, spaces(lane-columns-air/2)).
		own(feed, spaces(width-lane)).
		String()
}

// separatorPillAirColumns is the air inside a pill, on each side of its
// words, and it is the same air the badge of an unread count has (the inner
// run of chatBadge): a pill with nothing between its surface and its words is
// a band of colour with a word in it.
const separatorPillAirColumns = 1

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

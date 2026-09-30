package tui

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"telecli/internal/tui/termwidth"
	"telecli/internal/tui/theme"
)

// This file draws the messages of a conversation.
//
// The shape is §4.4 as the interface ended up drawing it: the name of the
// chat and a rule under it, the status under that, and then the messages —
// the other side's on the left with the name of whoever sent them above,
// this user's on the right in a block of their own with the time and the
// state of the send under the text.
//
// The feed is bottom-anchored. A conversation with three messages in it is
// three messages directly above the composer, and the empty rows are above
// them, because the messages grow upwards from the field they are written
// in and a screen that has the newest message in its top row makes a user
// scroll to read what just arrived.
//
// The order is chronological: the oldest message is drawn first, so the
// newest is on the last row of the screen. A text that does not fit wraps
// instead of being cut, because a message cut in the middle is a message a
// user cannot read and cannot answer.

// The words of a message line.
const (
	// unknownAuthor is what a message is signed with when the name of its
	// sender could not be found.
	//
	// It is said rather than left blank: a row with nothing in it is a row
	// a user cannot tell from a row that failed to load, and the sender of
	// this message is a fact that exists whether or not the program could
	// read it.
	unknownAuthor = "Unknown"
)

// authorTimeSeparator is what holds the sender of a message and the time of
// it together: two spaces, and no dot between them (the owner, 30.09).
//
// It is not the middle dot of §3.3 that the status line uses. That dot
// joins two parts of one line of words — "online · connected" — and a name
// and a time are not that: the dot made the row read as a sentence with a
// subject and a predicate, and the mockup writes them side by side with
// the time in the dim step of the text ramp. Two spaces say the same thing
// and take one column less.
const authorTimeSeparator = "  "

// outgoingBlockSharePercent is how much of the feed, in per cent, an
// outgoing message may take.
//
// Seventy per cent is what leaves a column on the left for the other
// side's messages to start in, so a conversation with messages from both
// sides has a visible seam and a user can tell at a glance which is which
// without reading either. A narrow screen is the one place the block may
// fill the feed instead: there is no conversation beside it to keep a seam
// with, and a block of seventy per cent of forty columns is a message a
// user has to read three words at a time.
//
// It is a whole number of per cent and not 0.7 because a tenth of ninety
// columns is not a whole number of columns, and 0.7 of ninety is one
// short of it in binary floating point: a block sized to sixty-two where
// the share says sixty-three is a block that is a column narrower on one
// terminal than on another, and the row under it is a column out of place
// either way.
const outgoingBlockSharePercent = 70

// conversationRegion draws the messages of the open conversation, with
// the header above them.
//
// The header is a line of its own and stays there while the messages move
// under it, which is what §4.4 means by a sticky header: the name of the
// chat is not something a user has to scroll back to.
//
// The rows the messages get are the rows that are left after the header
// and the line an older page occupies while it is on its way. The model
// asks for the same number, so the two cannot disagree about how far a
// page of keys moves the cursor.
func (m Model) conversationRegion(
	layout Layout,
	width int,
	height int,
) string {
	styles := m.styles()

	// §5: exactly one pane carries the accent. While a popup is open the
	// popup is it, so the timeline's rule goes away — two panels marked at
	// once is a screen where the user cannot tell which one has the keys.
	focused := m.panelFocused(conversationPane)

	// The name of the chat is the brightest thing in the header and the
	// status is under it, with nothing between the two, as the mockup of
	// the owner (30.09) has them: "Дмитрий С" and then "online · connected ·
	// 1 queued". The rule that says this pane has the keys closes the
	// header, because that is what a rule under a header is.
	header := []string{m.conversationHeading(m.conversationTitle(layout, width), width)}
	header = append(header, m.statusBlock(layout, width)...)
	header = append(header, styles.focusRule(width, focused))

	// The progress of an older-page request is at the top of the timeline,
	// where the page it is about will go: a line at the bottom would be
	// read as the end of the conversation, and the end is the newest
	// message.
	header = append(header, m.olderPageLines(layout, width)...)

	rows := maxInt(height-len(header), 0)
	body := anchorTimelineToBottom(m.timelineBody(layout, width, rows), rows)

	return m.renderRegion(
		styles.conversation,
		width,
		append(header, body...),
		height,
	)
}

// anchorTimelineToBottom puts the rows of the feed on the last rows of the
// area it has.
//
// Fewer messages than the area holds means the empty rows are above them:
// the newest message sits on the row directly above the composer, which is
// where a user looks after sending something. More rows than the area holds
// means the view is over budget, and the top goes: the oldest message is
// the one a reader scrolls back for.
func anchorTimelineToBottom(body []string, rows int) []string {
	if rows < 1 {
		return nil
	}

	if len(body) < rows {
		pad := make([]string, rows-len(body))

		return append(pad, body...)
	}

	return body[len(body)-rows:]
}

// timelineBody returns the rows the messages take: the part of the history
// that fits, and the pending messages below it.
//
// The history takes what the pending messages do not need. They are below
// it because they are newer than anything in it, and they are in the
// timeline rather than in a block of their own because a message that is
// still leaving the program looks like every other message of the
// conversation.
func (m Model) timelineBody(layout Layout, width, rows int) []string {
	lines := make([]string, 0, rows)
	if history := m.historyFeedRows(layout, width); history > 0 {
		lines = append(lines, m.timelineLines(layout, width, history)...)
	}

	return append(lines, m.pendingMessageLines(layout, width)...)
}

// popupOpen reports whether a sheet or a question is on the screen.
func (m Model) popupOpen() bool {
	return m.actionSheet.open || m.modal.open
}

// olderPageLines returns the line that says an older page is on its way,
// or that the last attempt failed.
func (m Model) olderPageLines(layout Layout, width int) []string {
	styles := m.styles()

	switch {
	case m.historyMoreLoading:
		return []string{styles.dimmed(m.tokens().SecondaryText).
			Render(m.widths.Fit("Loading older messages...", width, ellipsis))}

	case m.historyMoreErr != nil:
		text := "Failed to load older messages: " + m.historyMoreErr.Error()

		return []string{styles.dimmed(m.tokens().StatusError).
			Render(m.widths.Fit(text, width, ellipsis))}

	default:
		return nil
	}
}

// conversationHeading returns the name of the open chat as the first row of
// the conversation: the brightest text of the screen, in bold, whether or
// not the pane has the keys.
//
// It is not the accent. The accent is how a pane says that it has the keys
// (the rule under this header is the rest of that), and the name of the
// chat is not a statement about the keyboard: it is the one thing a reader
// of a conversation is looking for, and the owner has it in the bright text
// of the theme in bold (30.09).
func (m Model) conversationHeading(title string, width int) string {
	return m.styles().text(m.tokens().PrimaryText).Bold(true).
		Render(m.widths.Fit(title, width, ellipsis))
}

// conversationTitle returns the title of the open chat, with the back
// marker a single-pane screen needs to get out of the conversation.
func (m Model) conversationTitle(layout Layout, width int) string {
	title := m.selected().Title
	if title == "" {
		title = "(no chat)"
	}

	if layout.TwoPane() {
		return title
	}

	// §3.3 shows the way back on the first line of a narrow
	// conversation. It is a word and not an arrow alone: the screen is
	// read by people who cannot see which glyph means what.
	return conversationBackMarker + "  " + title
}

// conversationBackMarker is how a single-pane conversation says where the
// way back is.
const conversationBackMarker = "< Chats"

// ---- the feed ----

// timelineEntry is one thing the timeline draws: a message, or an album of
// consecutive messages drawn as one entry.
//
// Telegram sends an album as a run of consecutive messages that share a
// media_album_id, and the interface draws one entry for the run rather
// than one per part: three photographs are one thing somebody did, and a
// feed that shows them as three messages is a feed of nine lines for one
// picture.
type timelineEntry struct {
	// first is the index in the message list of the first message of the
	// run, and last the index of the last one.
	first   int
	last    int
	message Message
}

// count returns how many parts the entry stands for.
func (e timelineEntry) count() int { return e.last - e.first + 1 }

// timelineEntries collapses the runs of an album into single entries.
//
// The order of the messages is not touched: the entry of a run carries the
// first message of it, and the caption of the first part that has one, so
// the run reads as what it was. A run is only an album when the parts are
// consecutive and from the same sender; two photos from two people sent
// one after another are two messages, and collapsing them would take one
// person's picture away.
func timelineEntries(messages []Message) []timelineEntry {
	entries := make([]timelineEntry, 0, len(messages))

	for index := 0; index < len(messages); {
		run := index
		if albumID := messages[index].AlbumID; albumID != 0 {
			for run+1 < len(messages) &&
				messages[run+1].AlbumID == albumID &&
				sameSender(messages[run+1], messages[index]) {
				run++
			}
		}

		entry := timelineEntry{
			first:   index,
			last:    run,
			message: messages[index],
		}
		for part := index; part <= run; part++ {
			if entry.message.Caption == "" {
				entry.message.Caption = messages[part].Caption
			}
		}

		entries = append(entries, entry)
		index = run + 1
	}

	return entries
}

// sameSender reports whether two messages came from the same person.
//
// A channel's messages come from the channel and carry its name, so the
// name alone would merge the messages of two people who share it; the
// identifier is what says who actually sent them.
func sameSender(one, other Message) bool {
	return one.Outgoing == other.Outgoing && one.AuthorID == other.AuthorID
}

// entryIndexOfMessage returns the entry a message index belongs to, or the
// last entry when the index is past the end of the list.
func entryIndexOfMessage(entries []timelineEntry, messageIndex int) int {
	for index, entry := range entries {
		if messageIndex <= entry.last {
			return index
		}
	}

	return maxInt(len(entries)-1, 0)
}

// timelineLines returns the rows the messages take, oldest first.
//
// The window starts at the model's scroll anchor and moves only if the
// cursor would be outside it. That is the last word on where the cursor
// is: the model decides when the view scrolls, and the view refuses to
// draw a cursor it is not showing.
func (m Model) timelineLines(layout Layout, width, rows int) []string {
	messages := m.selected().Messages
	if len(messages) == 0 {
		return m.timelineEmptyLines(layout, width)
	}

	styles := m.styles()
	entries := timelineEntries(messages)
	top := entryIndexOfMessage(entries, clampIndex(m.timelineTop, len(messages)-1))

	lines, drawn := m.entryRowsFrom(entries, top, layout, width, rows, styles, m.timelineCut)

	selected := entryIndexOfMessage(entries, clampIndex(m.selectedMsg, len(messages)-1))
	if selected < top || selected >= top+drawn {
		// The cursor is not in the window the model placed, and the message
		// it is on is drawn whole: the cut belongs to a window that is not
		// this one.
		lines, _ = m.entryRowsFrom(
			entries,
			selected,
			layout,
			width,
			rows,
			styles,
			0,
		)
	}

	return lines
}

// entryRowsFrom draws the entries from first onwards until the rows run
// out, and returns how many entries it drew.
//
// cutRows are the rows left off the top of the first entry, and they are
// the window the model placed: the rows of a feed are not a whole number of
// messages, and a window that ends at the newest message is full when the
// entry at its top is drawn from the middle. What is left off there is the
// blank row that separates the message from the one above it, and at worst
// its author line; the newest message is never the one cut, because the
// walk that placed the window started from it.
func (m Model) entryRowsFrom(
	entries []timelineEntry,
	first int,
	layout Layout,
	width int,
	rows int,
	styles viewStyles,
	cutRows int,
) ([]string, int) {
	var (
		lines []string
		drawn int
	)

	for index := first; index < len(entries); index++ {
		entry := entries[index]
		block := m.entryLines(entry, layout, width, styles)
		switch {
		case index == first && cutRows > 0:
			block = block[minInt(cutRows, maxInt(len(block)-1, 0)):]

		case index == first && len(block) > 0 && strings.TrimSpace(block[0]) == "":
			// The blank row at the top of a block is the gap between it
			// and the message above it, and the topmost message of the feed
			// has none: a blank row there is a bar of nothing under the
			// header, on a feed the owner reads as empty space.
			block = block[1:]
		}

		// An entry taller than the rows that are left is cut at them, and
		// it is the first one that is cut rather than skipped: a long
		// message would otherwise leave the timeline empty, and letting it
		// grow past its rows would push the composer off the screen, which
		// §3.4 does not allow. The head of the message is what is kept —
		// a message whose text is missing is a message nobody can read.
		if len(block) > rows-len(lines) {
			if len(lines) > 0 {
				break
			}

			block = block[:maxInt(rows, 0)]
		}

		lines = append(lines, block...)
		drawn++

		if len(lines) >= rows {
			break
		}
	}

	return lines, drawn
}

// entrySelected reports whether the cursor is anywhere in a run, so that
// an album stays marked while the cursor walks over its parts.
func (m Model) entrySelected(entry timelineEntry) bool {
	return m.selectedMsg >= entry.first && m.selectedMsg <= entry.last
}

// entryLines renders one entry as the rows it takes, with a blank line
// above it.
//
// The blank line is what makes a conversation a column of messages rather
// than a wall: two messages with no gap between them are a paragraph, and
// a paragraph of chat messages is not what a chat looks like. It is the
// first thing to go on a screen too short for it (§3.4), where a message
// is a line.
func (m Model) entryLines(
	entry timelineEntry,
	layout Layout,
	width int,
	styles viewStyles,
) []string {
	var block []string

	switch {
	case entry.message.Outgoing:
		block = m.outgoingMessageLines(entry, layout, width, styles)
	case layout.Short():
		block = m.shortMessageLines(entry, layout, width, styles)
	default:
		block = m.incomingMessageLines(entry, layout, width, styles)
	}

	if layout.Short() {
		return block
	}

	return append([]string{""}, block...)
}

// ---- the blocks of the two sides ----

// messageSide is which side of the feed a message is drawn on.
//
// It is a side and not a boolean because the three things that differ
// between the two are three different questions: which edge of the feed the
// block is against, which surface it is drawn on, and who sent it. A
// boolean would have all three answering the same question.
type messageSide uint8

const (
	// sideIncoming is the left: a message from the other side, on the
	// neutral surface of the theme.
	sideIncoming messageSide = iota

	// sideOutgoing is the right: a message of this user, on the accent's
	// tint of that same surface.
	sideOutgoing
)

// String names the side for a failure message.
func (s messageSide) String() string {
	if s == sideOutgoing {
		return "outgoing"
	}

	return "incoming"
}

// incomingMessageLines renders a message from the other side: a block on
// the left with the name of whoever sent it and the time on its first row
// and the text under them.
//
// The author line is inside the block rather than above it. A name on the
// background of the feed with a text in a block below it is two things to
// read as one message, and a reader has to learn from the shape that they
// belong together; inside one block there is nothing to learn. The
// separator between the name and the time is the one the status line uses,
// so one screen has one way of saying "and also".
func (m Model) incomingMessageLines(
	entry timelineEntry,
	layout Layout,
	width int,
	styles viewStyles,
) []string {
	selected := m.entrySelected(entry)
	head := m.authorLine(entry, styles)
	block := m.messageBlockFor(
		sideIncoming, layout, width, entryText(entry), true, blockRunText(head),
	)

	rows := make([]string, 0, 6)
	rows = append(rows, m.blockRow(
		selected, styles, m.selectionMarkStyle(selected),
		m.selectionMark(selected), block, head, false,
	))

	mark := m.selectionMark(selected)
	for index, line := range m.widths.Wrap(entryText(entry), block.text, ellipsis) {
		// The marker stands on the first row of the block and on no
		// other: a mark down the side of a block is a mark on the block,
		// and one on the state under the text would say that the state
		// is the message the keys act on.
		if index > 0 {
			mark = spaces(selectionMarkerWidth)
		}

		rows = append(rows, m.blockRow(
			selected, styles, m.selectionMarkStyle(selected), mark,
			block, []blockRun{{style: styles.body(false), text: line}}, false,
		))
	}

	return m.padBlock(rows, selected, styles, block)
}

// authorLine returns the runs of the first row of the block of a message
// from the other side: the name of whoever sent it, the separator, and the
// time.
//
// It is a list of runs and not a string because it is three colours on one
// row, and a colour of its own inside a block has to be written with the
// background of the block or the row loses it.
func (m Model) authorLine(entry timelineEntry, styles viewStyles) []blockRun {
	runs := []blockRun{{
		style: styles.authorColor(entry.message.AuthorID),
		text:  messageAuthor(entry.message),
	}}
	if entry.message.Time == "" {
		return runs
	}

	return append(runs,
		blockRun{style: styles.unstyled(), text: authorTimeSeparator},
		blockRun{
			style: styles.text(m.tokens().MutedText),
			text:  entry.message.Time,
		},
	)
}

// shortMessageLines renders a message on a screen too short for the head
// line and the text on separate rows.
//
// The sender and the time go: what is left is the message itself, which is
// what a user came to read. The text still wraps, because a screen that is
// short is not a screen where a message may be cut in half, and the block
// around it is the one every other message is in, because a message that
// loses its block as well as its name is a row of text.
//
// There is no air above and below the block here, and that is the one
// thing about a short screen that is not a compromise made for want of
// space: a half row of air at each end is two rows of the twenty this
// screen has, and a message of a screen this short that is two rows
// shorter is two rows of the conversation a user cannot read. The ends of
// the block are the one piece of the shape that costs nothing — they are
// the half circles of a pill in the row the message is in, and the row the
// message is in is the row it has.
func (m Model) shortMessageLines(
	entry timelineEntry,
	layout Layout,
	width int,
	styles viewStyles,
) []string {
	selected := m.entrySelected(entry)
	side, colour := sideIncoming, m.tokens().IncomingMessage
	if entry.message.Outgoing {
		side, colour = sideOutgoing, m.tokens().OutgoingMessage
	}

	// A short screen has no padding in it: two rows of the twenty it has
	// are two rows of the conversation, and the ends of the block are the
	// one piece of the shape that costs nothing (§3.4).
	block := m.messageBlockFor(side, layout, width, entryText(entry), false)

	rows := make([]string, 0, 3)
	mark := m.selectionMark(selected)
	for index, line := range m.widths.Wrap(entryText(entry), block.text, ellipsis) {
		head := spaces(selectionMarkerWidth)
		if index == 0 {
			head = mark
		}

		rows = append(rows, m.blockRow(
			selected, styles, m.selectionMarkStyle(selected), head,
			block, []blockRun{{style: styles.text(colour), text: line}}, false,
		))
	}

	return rows
}

// outgoingMessageLines renders a message of this user: a block on the
// right, as wide as the text in it and no wider than the feed allows, with
// the time and the state of the send under the text at its right edge.
//
// There is no author line: the block is on the right and in the colour of
// this user's messages, which is what says it is from this user, and "You"
// above a message the reader is looking at is a word they have to read once
// per message to learn nothing.
func (m Model) outgoingMessageLines(
	entry timelineEntry,
	layout Layout,
	width int,
	styles viewStyles,
) []string {
	selected := m.entrySelected(entry)
	text := entryText(entry)
	label, labelColour := m.historyStateLabel(entry.message)
	block := m.messageBlockFor(sideOutgoing, layout, width, text, true, label)

	rows := make([]string, 0, 6)
	mark := spaces(selectionMarkerWidth)
	for index, line := range m.widths.Wrap(text, block.text, ellipsis) {
		if index == 0 {
			mark = m.selectionMark(selected)
		}

		rows = append(rows, m.blockRow(
			selected, styles, m.selectionMarkStyle(selected), mark,
			block, []blockRun{{
				style: styles.text(m.tokens().OutgoingMessage),
				text:  line,
			}}, false,
		))
	}

	// The state is a line of the block like any other, at its right edge:
	// it belongs to the message and it is read where the block ends. A
	// state with nothing to say is no line of the block: a row of air
	// between the text and the row that closes the block is a row of
	// nothing, and it counts as one when the shape of the block is decided.
	if label != "" {
		rows = append(rows, m.blockRow(
			selected, styles, m.selectionMarkStyle(selected),
			spaces(selectionMarkerWidth), block,
			[]blockRun{{style: styles.text(labelColour), text: label}}, true,
		))
	}

	return m.padBlock(rows, selected, styles, block)
}

// blockPaddingRows is how many rows of the block's own background sit above
// the words of a block and under them.
//
// It is the inset of §4.4 on the other axis: the air inside a block is a
// column on each side of the text and a row above it and below it, and
// without the rows the text is pressed against the top and the bottom of
// its own block while it has air on its left and its right (the owner,
// 30.09 — the price is two rows a message, and the decision on the air is
// the owner's after seeing it).
const blockPaddingRows = 1

// blockPaddingRow returns one row of the block's own background above or
// below the words of a block: the block's surface across the block, the
// background of the feed everywhere else, and no marker, because a message
// that is not selected has nothing to mark and the air around it is not a
// row of the message.
func (m Model) blockPaddingRow(
	selected bool,
	styles viewStyles,
	block messageBlock,
) string {
	return m.painter(theme.Color{}).
		own(styles.on(m.tokens().ChatBackground, styles.unstyled()), spaces(block.offset)).
		own(styles.on(m.blockSurface(selected), styles.unstyled()), spaces(block.width)).
		own(styles.on(m.tokens().ChatBackground, styles.unstyled()), spaces(block.right)).
		String()
}

// padBlock returns the rows of a block with a row of the block's own
// background above them and below them — and the rows as they are for a
// block that has no air in it, which is a block of one row of text
// (the owner, 30.09).
func (m Model) padBlock(
	rows []string,
	selected bool,
	styles viewStyles,
	block messageBlock,
) []string {
	if !block.padded {
		return rows
	}

	padding := m.blockPaddingRow(selected, styles, block)
	padded := make([]string, 0, len(rows)+2*blockPaddingRows)
	for range blockPaddingRows {
		padded = append(padded, padding)
	}
	padded = append(padded, rows...)
	for range blockPaddingRows {
		padded = append(padded, padding)
	}

	return padded
}

// blockRun is one piece of a row of a block, in a colour of its own.
//
// A row of a block is written one run at a time rather than one string at a
// time, because a run inside a run ends with a reset and a reset in the
// middle of a block takes the background of the block with it: the words
// after the first would be on the feed rather than on the block, and a
// block with a stripe across it is a block nobody can read.
type blockRun struct {
	style lipgloss.Style
	text  string
}

// blockRunText returns what a row of runs says, as one string, which is
// how the width of the row is asked about.
func blockRunText(runs []blockRun) string {
	var out strings.Builder
	for _, run := range runs {
		out.WriteString(run.text)
	}

	return out.String()
}

// fitRuns cuts a row of runs to the width its block has, so a name longer
// than the block cannot make the row wider than the block.
//
// The cut is in the last run that has anything in it and the rest of the
// row keeps its own colours: the name of whoever sent a message and the
// time of it are both things a reader is looking for, and a row that
// overflows its block is a row that pushes the composer off the screen.
func (m Model) fitRuns(runs []blockRun, width int) []blockRun {
	fitted := make([]blockRun, 0, len(runs))
	used := 0

	for _, run := range runs {
		room := width - used
		text := m.widths.TruncateMarked(run.text, maxInt(room, 0), ellipsis)
		if text == "" {
			break
		}

		used += m.widths.StringWidth(text)
		fitted = append(fitted, blockRun{style: run.style, text: text})
	}

	if len(fitted) == 0 {
		return []blockRun{{text: ""}}
	}

	return fitted
}

// messageBlock is the block a message is drawn in: where it starts on its
// row, how wide it is, and the width and the air its text gets inside it.
//
// The numbers are the whole of the drawing, and a row is written from them
// and from nothing else: a block cannot be drawn at one width and padded to
// another, and the padding is what a reader sees as a band.
type messageBlock struct {
	// side is which edge of the feed the block is against.
	side messageSide

	// offset is the first column of the block on the row: the left margin
	// of the feed for a message from the other side, and the width less
	// the right margin less the block for one of this user. Two messages
	// of the same conversation line up on that edge whichever way round
	// the text goes, and the eye can read down them.
	offset int

	// width is how many columns the block takes, its two insets and its
	// two rounded ends included.
	width int

	// text is how many columns the text inside the block may take.
	text int

	// inset is the space inside the block on each side of the text.
	inset int

	// ends is how many columns the two rounded ends of the block take
	// together, which is two with a Nerd Font and none without one.
	ends int

	// padded is whether the block has a row of its own background above
	// the words and below them. It is a property of the shape and not of
	// the drawing: a block of one row of text has no air in it, and a
	// block of two rows of text and more has (the owner, 30.09).
	padded bool

	// right is how many columns of the feed are behind the block. It is a
	// number of the block rather than of the layout because a row is
	// written a run at a time and only the painter knows how far along it
	// has got.
	right int
}

// messageBlockFor returns the block a message takes on a row of the given
// width, for a text and the lines under it.
//
// It is the width of the text, capped: the block is as wide as the widest
// line of the text and as wide as the widest of the lines under it, and
// never wider than the share of the feed a message may take. A block the
// width of the feed under a two-word message is a band rather than a
// message, and a band is what the block is not; a block wider than the text
// it holds is the same band with words in the middle of it.
//
// The lines under the text count towards the width for the same reason the
// text does: they are lines of the block, and a block that had to cut the
// words off its own state is a block that is too narrow for its own
// message — which is the one thing a message that failed to be sent has to
// be able to say in full.
func (m Model) messageBlockFor(
	side messageSide,
	layout Layout,
	width int,
	text string,
	padding bool,
	under ...string,
) messageBlock {
	inset := layout.BlockInset()
	nerd := m.roundedEndColumns()
	lane := maxInt(width-2*layout.FeedMargin(), 1)
	ceiling := m.messageBlockCap(layout, width)

	// The ends of the block are a property of its shape and not of the
	// font: they are the two half circles of a pill, and a pill is one row
	// tall. Drawn on every row of a block they are a half circle on each
	// row of it, which for a block of two rows is two pills stacked on top
	// of each other — a shape nobody asked for and one that reads as two
	// messages rather than one. So a block of one row of words is a pill
	// and a block of more is a rectangle with square sides and half a row
	// of air at each end (§4.4).
	//
	// The shape is measured with the ends in, because the ends are two
	// columns of the text area and a text that only fits on one row
	// *without* them is a text the pill would push onto a second row — the
	// two stacked halves this is about. Such a block keeps its square
	// sides, and is a rectangle of one row: the alternative to a pill is a
	// rectangle, and the alternative to the rectangle is the two pills.
	//
	// The air of the block is the owner's decision of 30.09, and it is
	// about the TEXT and not about the whole block: a block whose text is
	// one row is that row and the author line or the state, with no air in
	// it, and a block of two rows of text and more keeps a row of its own
	// background above them and below them. Half a row of air is not a
	// thing a terminal can draw — the halves of a row are bands of another
	// shade, see the half blocks above — so "a little air" is a whole row
	// or nothing. The author line and the state of the send are not part
	// of the count: he counts the rows of the text.
	textArea := ceiling - 2*inset
	padded := padding && len(m.widths.Wrap(text, textArea, ellipsis)) > 1

	// The ends are counted over the whole block, because that is what the
	// half circles have to fit in: they are one row tall, and a block with
	// air in it has them in the middle of it with a row of the block's own
	// background above and below, which is a pill inside a rectangle. A
	// block of one row in all — the short screen of §3.4, where a message
	// is its text and nothing else — is the pill.
	ends := 0
	if !padded && nerd > 0 && m.blockTextRows(ceiling-2*inset-nerd, text, under) == 1 {
		ends = nerd
	}

	most := ceiling - 2*inset - ends
	if most < 1 {
		most = 1
	}

	// The text is wrapped at the share rather than at the width of the
	// text, because wrapping is what decides the lines: a text wrapped
	// wider than it needs wraps the same way, and the widest of those
	// lines is the natural width of the block.
	natural := 0
	for _, line := range m.widths.Wrap(text, most, ellipsis) {
		natural = maxInt(natural, m.widths.StringWidth(line))
	}
	for _, line := range under {
		natural = maxInt(natural, m.widths.StringWidth(line))
	}

	// The block is never narrower than the width of a message, and a
	// two-word message in a block of eight columns is a sliver: the pill
	// of §4.4 needs room to be a pill in, and a block a user cannot see
	// the words in is a block that has to be read with effort. The floor
	// is of the text and not of the block — the insets and the ends are
	// added to it — and it never exceeds what the share allows, so a
	// narrow screen still gets the block it can hold.
	least := minInt(blockMinTextColumns, most)

	block := messageBlock{
		side:   side,
		inset:  inset,
		ends:   ends,
		padded: padded,
		text:   minInt(maxInt(natural, least), most),
	}
	block.width = minInt(block.text+2*inset+ends, lane)
	block.width = maxInt(block.width, 1)
	block.text = maxInt(block.width-2*inset-ends, 1)

	if side == sideIncoming {
		block.offset = layout.FeedMargin()
	} else {
		block.offset = maxInt(width-layout.FeedMargin()-block.width, 0)
	}
	block.right = maxInt(width-block.offset-block.width, 0)

	return block
}

// blockTextRows returns how many rows of words a block of a text and the
// lines under it has.
//
// It is the shape of the block and nothing else: the lines under the text
// are rows of the block like the text is — the name of whoever sent the
// message is a row of it, and so is the state of a message of this user —
// and an empty line under the text is no row at all, because a row of air
// in the middle of a message is a row of nothing.
func (m Model) blockTextRows(area int, text string, under []string) int {
	rows := len(m.widths.Wrap(text, area, ellipsis))
	for _, line := range under {
		if strings.TrimSpace(line) != "" {
			rows++
		}
	}

	return rows
}

// messageBlockCap returns how many columns the block of a message may take
// on a row of the given width.
//
// The share is of the whole row, and the margin comes off it: a block that
// is seventy per cent of the row and stops at the margin of the feed is
// narrower than the share the number says, which is the right way round —
// the number is a ceiling, not a target.
//
// A single-pane screen is the one place the block may fill the feed: there
// is no conversation beside it to keep a seam with, and a block of seventy
// per cent of forty columns is a message a user has to read three words at
// a time.
func (m Model) messageBlockCap(layout Layout, width int) int {
	lane := maxInt(width-2*layout.FeedMargin(), 1)
	if layout.Kind == LayoutNarrow {
		return lane
	}

	return minInt(width*outgoingBlockSharePercent/100, lane)
}

// roundedEndColumns returns how many columns the two rounded ends of a
// block take together, and nothing at all when they are not drawn.
//
// Whether a block takes them is its shape and not this: a block of one row
// of words is a pill and a block of more is a rectangle (messageBlockFor).
// This is the width of the ends for the block that takes them.
//
// They are drawn when the setting says the terminal has the font and the
// block has a colour to be the colour of. A terminal with neither draws
// them as empty squares, and two empty squares at the ends of every message
// are worse than the square corners they replace.
func (m Model) roundedEndColumns() int {
	if !m.nerdFont {
		return 0
	}
	if !m.blockSurface(false).IsSet() {
		return 0
	}

	return 2 * m.widths.StringWidth(termwidth.NerdHalfLeft)
}

// blockSurface returns the surface the block of a message is drawn on: the
// Selected token while the cursor is on the message, and the neutral
// surface of the theme otherwise, on both sides of the feed.
//
// It is the Selected token and not the side's own, because a selection
// that cannot be seen on a block is a selection a user can only find by
// moving the cursor — and the block is the only background on the screen a
// selection could hide in.
//
// Both sides are drawn on the same neutral surface. The own side used to
// have the accent's tint of it, and the owner turned that down on 30.09:
// a tinted own block almost hides the one thing the tint was standing in
// for, because the selection of the message under the cursor is drawn on
// the block, and a violet block under a selected surface is a selection
// nobody can find. So the two sides are told apart by the colour of their
// words — the accent for this user, the light neutral for the other side
// — and the block under the cursor is the Selected role on either side.
func (m Model) blockSurface(selected bool) theme.Color {
	if selected {
		return m.tokens().Selected
	}

	return m.tokens().ComposerBackground
}

// blockRow draws one row of a block: the marker in front of it, the
// columns between the marker and the block, the block, and the rest of the
// feed behind it.
//
// The block is exactly as wide as it was measured to be whichever way
// round the text goes. Its surface is on its own cells and on no others:
// the columns in front of it and the feed behind it are the background of
// the feed, because a surface that ran the width of the feed under a
// message is a band, and a band under every message is the table the feed
// used to look like. The selection of the message under the cursor is on
// the block and on nothing else, for the same reason.
//
// Every cell of the row is written with a background, including the feed
// around the block. Lip Gloss ends a run with a reset, and a reset after
// the last word of a row takes the background of the region behind it with
// it, so the columns of the row outside the block would be the terminal's
// own default rather than the feed's — a stripe of whatever the terminal
// thought its background was, on every row of the feed.
func (m Model) blockRow(
	selected bool,
	styles viewStyles,
	markStyle lipgloss.Style,
	mark string,
	block messageBlock,
	runs []blockRun,
	flushRight bool,
) string {
	runs = m.fitRuns(runs, block.text)
	feed := styles.on(m.tokens().ChatBackground, styles.unstyled())
	raised := styles.on(m.blockSurface(selected), styles.unstyled())

	// The columns in front of the block, the marker in the last of them
	// and the rest of the feed behind it are all written with the
	// background of the feed, and so is the marker: a cell of a row with
	// no background at all is a cell the terminal paints with whatever it
	// thinks its own background is.
	row := m.painter(theme.Color{}).
		own(feed, spaces(block.offset-selectionMarkerWidth)).
		own(styles.on(m.tokens().ChatBackground, markStyle), mark)

	if block.ends > 0 {
		row = row.own(m.edgeStyle(styles, block.side, selected), termwidth.NerdHalfLeft)
	}

	// The air in front of the words, and the air behind them, add up to
	// the width of the text inside the block on every row of it. A row
	// whose words are shorter than the block has to carry the block to
	// the edge the next row carries it to, or two rows of one message are
	// two blocks; and a row read at the right edge has the whole of the
	// air in front of the words instead of behind them.
	air := m.flushColumns(block, runs)
	if flushRight {
		row = row.own(raised, spaces(air))
	}
	row = row.own(raised, spaces(block.inset))

	for _, run := range runs {
		row = row.own(
			styles.on(m.blockSurface(selected), run.style),
			run.text,
		)
	}

	if !flushRight {
		row = row.own(raised, spaces(air))
	}
	row = row.own(raised, spaces(block.inset))

	if block.ends > 0 {
		row = row.own(m.edgeStyle(styles, block.side, selected), termwidth.NerdHalfRight)
	}

	return row.own(feed, spaces(block.right)).String()
}

// edgeStyle is the style of one half of a rounded edge of a block: the
// colour of the block, on the background of the feed behind it.
func (m Model) edgeStyle(
	styles viewStyles,
	side messageSide,
	selected bool,
) lipgloss.Style {
	return styles.blockEdge(
		m.blockSurface(selected), m.tokens().ChatBackground,
	)
}

// flushColumns returns how many columns of a block are not covered by the
// words of a row of it.
//
// The state under a message of this user is read at the edge the block is
// against rather than at the edge the text above it starts at: it is a fact
// about the message rather than a continuation of its text, and a
// continuation would start where the last line of the text starts. The same
// number behind the words keeps the block the same width on every row of
// it, which is what makes two rows of one message one block.
func (m Model) flushColumns(block messageBlock, runs []blockRun) int {
	used := 0
	for _, run := range runs {
		used += m.widths.StringWidth(run.text)
	}

	return maxInt(block.text-used, 0)
}

// historyStateLabel returns the words under a message of this user and the
// colour of them: the state it is in, and the time it happened at, on one
// line.
//
// The history is the past, so a message of it is sent, and it is drawn as
// sent rather than left without a state: a user who sees a state on the
// messages that are still leaving is entitled to know that the ones that
// have gone are not in some state they cannot see.
//
// The colour is the state's own, so the line under a message repeats what
// the words under it say rather than being the same grey everywhere: a
// muted `! Failed` is a warning about nothing in particular.
//
// The words are returned whole rather than fitted to a width, because the
// block of a message is as wide as the state under it and the state is
// what says how wide that is. Cutting it first would let the block be
// narrower than its own state and would leave a message that could not
// name what happened to it.
func (m Model) historyStateLabel(message Message) (string, theme.Color) {
	state, known := deliveryStateOf(deliveryOfHistory(message))
	if !known {
		return "", m.tokens().MutedText
	}

	label := state.Mark().String()
	if message.Time != "" {
		label += " " + message.Time
	}

	return label, state.Color(m.tokens())
}

// deliveryOfHistory is the state a message of the history is in.
//
// The history is the past: Telegram accepted everything in it, so a message
// of the history is sent. It is drawn as such rather than left without a
// state, because a user who sees a state on the messages that are still
// leaving is entitled to know that the ones that have gone are not in some
// state they cannot see.
func deliveryOfHistory(message Message) MessageDeliveryState {
	if message.Outgoing {
		return MessageDeliverySent
	}

	return MessageDeliveryState("")
}

// selectionMark returns the column in front of the message under the
// cursor, as text and not as a rendered run.
//
// It is a blank in every profile that can show the background of a
// selected row, and a `›` in the profile that cannot: the messages of a
// timeline are the rows the keys act on, and §5.2 asks for a marker on the
// one under the cursor, but a marker on every one of them would be a
// column of chevrons down the middle of a conversation.
func (m Model) selectionMark(selected bool) string {
	glyph := m.styles().selectionMarker(selected)
	if glyph == "" {
		return spaces(selectionMarkerWidth)
	}

	return glyph
}

// selectionMarkStyle returns the style of that column.
func (m Model) selectionMarkStyle(selected bool) lipgloss.Style {
	return m.styles().selectionMark(selected, m.focus == FocusHistory)
}

// entryText returns what an entry says: the words under a picture when it
// has some, and the text otherwise.
//
// A message with neither is a message whose media word is all there is — a
// picture, a sticker, a voice note — and a row of empty columns where the
// text should be says nothing at all.
func entryText(entry timelineEntry) string {
	return messageText(entry.message, entry.count())
}

// messageText returns what a message says, for a run of parts parts long.
func messageText(message Message, parts int) string {
	label := mediaLabel(message, parts)
	if label == "" {
		return message.Text
	}

	if message.Text == "" {
		return label
	}

	return label + " " + message.Text
}

// mediaLabel returns what a message carries, in the words §4.4 uses: the
// kind in brackets, and the caption after it when the picture had one.
//
// The caption is part of the label and not part of the text, because
// Telegram keeps them apart: the words under a picture are the picture's,
// and a message with a caption and no text is a message that is entirely a
// picture with something written on it.
func mediaLabel(message Message, parts int) string {
	if message.Media == "" {
		return ""
	}

	label := "[" + mediaWord(message.Media, parts) + "]"
	if message.Caption == "" {
		return label
	}

	return label + " " + message.Caption
}

// mediaWord returns the noun for a media message: the singular for one
// part, and the count with the plural for an album of them.
//
// The plural is not the singular with an "s" on it, because a file is not
// a document in the words the interface uses: an album of documents is an
// album of files, and a user who reads "[3 documents]" has to stop and
// work out what a document is.
func mediaWord(media string, parts int) string {
	if parts < 2 {
		return mediaSingular(media)
	}

	return strconv.Itoa(parts) + " " + mediaPlural(media)
}

func mediaSingular(media string) string {
	switch media {
	case "photo":
		return "photo"
	case "video":
		return "video"
	case "voice":
		return "voice note"
	case "sticker":
		return "sticker"
	case "animation":
		return "animation"
	case "audio":
		return "audio"
	default:
		return "file"
	}
}

func mediaPlural(media string) string {
	switch media {
	case "photo":
		return "photos"
	case "video":
		return "videos"
	case "voice":
		return "voice notes"
	case "sticker":
		return "stickers"
	case "animation":
		return "animations"
	case "audio":
		return "audio files"
	default:
		return "files"
	}
}

// messageAuthor returns the word that stands for the sender of a message.
//
// An outgoing message is not named at all, and a message from somebody
// whose name could not be read says so rather than borrowing the name of
// the chat.
func messageAuthor(message Message) string {
	if message.Outgoing {
		return ""
	}
	if message.Author != "" {
		return message.Author
	}

	return unknownAuthor
}

// timelineEmptyLines is what the conversation says before it has
// messages, or while the first page is on its way.
func (m Model) timelineEmptyLines(layout Layout, width int) []string {
	styles := m.styles()

	switch m.historyState {
	case loadStateLoading:
		return []string{styles.dimmed(m.tokens().SecondaryText).
			Render(m.widths.Fit("Loading history...", width, ellipsis))}

	case loadStateError:
		return []string{styles.dimmed(m.tokens().StatusError).
			Render(m.widths.Fit("Failed to load history", width, ellipsis))}

	case loadStateEmpty:
		// §17: a chat with nothing in it says so, and says where to
		// write. "No messages" on its own is the same sentence with the
		// answer removed.
		return []string{
			styles.dimmed(m.tokens().MutedText).
				Render(m.widths.Fit("No messages yet", width, ellipsis)),
			styles.dimmed(m.tokens().SecondaryText).
				Render(m.widths.Fit(noMessagesHint, width, ellipsis)),
		}

	default:
		return []string{styles.dimmed(m.tokens().MutedText).
			Render(m.widths.Fit("No messages yet", width, ellipsis))}
	}
}

package tui

import (
	"sort"
	"strconv"
	"time"

	"github.com/charmbracelet/lipgloss"

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

// authorTimeSeparator is what holds the sender of a message and the time
// of it together. It is the middle dot of §3.3, the same one the status
// line uses, so one screen has one way of saying "and also".
const authorTimeSeparator = " · "

// outgoingBubbleShare is how much of the feed an outgoing message may
// take.
//
// Seventy per cent is what leaves a column on the left for the other
// side's messages to start in, so a conversation with messages from both
// sides has a visible seam and a user can tell at a glance which is which
// without reading either.
const outgoingBubbleShare = 0.7

// outgoingBubbleInset is the space inside the block of an outgoing message
// on each side.
//
// A block of text with its background flush against the text reads as a
// highlight rather than as a message, and a highlight is not something a
// person can be on the right-hand side of.
const outgoingBubbleInset = 1

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

	header := []string{
		m.panelHeading(m.conversationTitle(layout, width), width, focused),
		styles.focusRule(width, focused),
	}

	// §4.3 puts the status under the title: a user reads it as part of the
	// header of the conversation rather than as the last line of it.
	header = append(header, m.statusBlock(layout, width)...)

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
	if history := m.historyFeedRows(layout, width); history > 0 {
		return m.timelineLines(layout, width, history)
	}

	return nil
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
	// first is the index in the feed of the first message of the run, and
	// last the index of the last one.
	first   int
	last    int
	message Message

	// pending is set when the entry stands for a message that is still
	// leaving the program.
	//
	// The feed is one column of messages and a message that is going out
	// is one of them: it takes its place in the run, it is scrolled to
	// with the rest and it is measured by the same heights. It used to be
	// a block drawn under the history whose rows were subtracted from the
	// history's own budget, and a queue that had a few messages going out
	// emptied the conversation above them — the user lost the history to
	// see the state of messages they had just written.
	pending *PendingMessage
}

// count returns how many parts the entry stands for.
func (e timelineEntry) count() int { return e.last - e.first + 1 }

// isPending reports whether the entry stands for a message that is still
// going out.
func (e timelineEntry) isPending() bool { return e.pending != nil }

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

// feedEntries is every entry the conversation draws, oldest first: the
// history of the open chat and the messages that have not gone out yet, in
// the order they were written.
//
// The two are one list because they are one conversation, and a message
// that has not gone out is a message like any other: it takes its place in
// the order, it is scrolled to with the rest, and it is measured by the
// same heights. It used to be drawn after the whole of the history, on the
// assumption that whatever has not gone out is newer than everything that
// has. That is true of a message this session wrote and false of a record
// an earlier run left behind, and a "Delivery uncertain" row is nothing but
// that: the owner's two of yesterday evening were drawn under today's sent
// messages, held the foot of the chat, and pushed the conversation up out
// of it.
//
// So the order is the order the conversation was written in: the moment
// Telegram dated a message, and the moment the queue recorded the row. A
// row is placed by that, and not by which of the two sources it arrived in.
//
// The indices are the conversation's: they count every message in the order
// above, and they are the space the cursor and the anchor already count in.
func (m Model) feedEntries() []timelineEntry {
	messages := m.selected().Messages
	if len(m.pending) == 0 {
		// Nothing to interleave, and the history is already in order, so
		// its own indices are the conversation's.
		return timelineEntries(messages)
	}

	slots := make([]feedSlot, 0, len(messages)+len(m.pending))
	for index, message := range messages {
		slots = append(slots, feedSlot{at: message.At, message: index, pending: -1})
	}
	for index := range m.pending {
		slots = append(slots, feedSlot{
			at: m.pending[index].CreatedAt, message: -1, pending: index,
		})
	}

	// Stable, so two messages of the same instant keep the order they came
	// in rather than swapping places under the user's eyes.
	sort.SliceStable(slots, func(i, j int) bool {
		if !slots[i].at.Equal(slots[j].at) {
			return slots[i].at.Before(slots[j].at)
		}
		// One instant, two sources: what Telegram has already put in the
		// chat is drawn before what the queue has only just recorded, so a
		// message this session sent still lands under the ones already
		// there.
		//
		// A rank and not a flag, because a comparison has to be a strict
		// order: "is this one of the history" answers yes for both of two
		// history messages, and a sort given that shuffles them.
		return feedRank(slots[i]) < feedRank(slots[j])
	})

	entries := make([]timelineEntry, 0, len(slots))
	for position := 0; position < len(slots); {
		slot := slots[position]
		if slot.pending >= 0 {
			entries = append(entries, timelineEntry{
				first:   position,
				last:    position,
				pending: &m.pending[slot.pending],
			})
			position++
			continue
		}

		// A run of an album, which a row of the queue standing in the
		// middle of it breaks: the parts are no longer consecutive in the
		// conversation, and one entry for them would put the row inside
		// somebody's album.
		run := position
		if albumID := messages[slot.message].AlbumID; albumID != 0 {
			for run+1 < len(slots) &&
				continuesAlbumRun(messages, slots, run+1, slot.message, albumID) {
				run++
			}
		}

		entry := timelineEntry{
			first: position, last: run, message: messages[slot.message],
		}
		for part := position; part <= run; part++ {
			if entry.message.Caption == "" {
				entry.message.Caption = messages[slots[part].message].Caption
			}
		}

		entries = append(entries, entry)
		position = run + 1
	}

	return entries
}

// feedSlot is one row of the conversation before the parts of an album are
// put together: a message of the history or a message of the queue, with
// the moment it is placed by. A slot carries one of the two indexes and -1
// for the other.
type feedSlot struct {
	at      time.Time
	message int
	pending int
}

// feedRank orders two messages of the same instant by which source they
// came from, so the sort has a strict order to work with.
func feedRank(slot feedSlot) int {
	if slot.pending < 0 {
		return 0
	}

	return 1
}

// continuesAlbumRun reports whether the slot at next is the part after
// first of one album: the next message of the history, the same album, the
// same sender.
func continuesAlbumRun(
	messages []Message,
	slots []feedSlot,
	next, first int,
	albumID int64,
) bool {
	if slots[next].message != first+1 {
		return false
	}
	if messages[slots[next].message].AlbumID != albumID {
		return false
	}

	return sameSender(messages[slots[next].message], messages[first])
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
// entryIndexOfFeed returns the entry the feed index falls in.
func entryIndexOfFeed(entries []timelineEntry, feedIndex int) int {
	for index, entry := range entries {
		if feedIndex <= entry.last {
			return index
		}
	}

	return maxInt(len(entries)-1, 0)
}

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
	total := m.timelineTotal()
	if total == 0 {
		return m.timelineEmptyLines(layout, width)
	}

	styles := m.styles()
	entries := m.feedEntries()
	top := entryIndexOfFeed(entries, clampIndex(m.timelineTop, total-1))

	lines, drawn := m.entryRowsFrom(entries, top, layout, width, rows, styles, m.timelineCut)

	selected := entryIndexOfFeed(entries, clampIndex(m.selectedMsg, total-1))
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
		if index == first && cutRows > 0 {
			block = block[minInt(cutRows, maxInt(len(block)-1, 0)):]
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
	// A message that is still going out is drawn the way it is measured:
	// by the rows it really takes, in the same column, with the state of
	// the send under the text. The window and the view have to agree about
	// that, so both go through here.
	if entry.isPending() {
		rows := m.pendingMessageRows(*entry.pending, layout, width, styles)
		if layout.Short() {
			return rows
		}
		return append([]string{""}, rows...)
	}

	var block []string

	switch {
	case entry.message.Outgoing:
		block = m.outgoingMessageLines(entry, width, styles)
	case layout.Short():
		block = m.shortMessageLines(entry, width, styles)
	default:
		block = m.incomingMessageLines(entry, width, styles)
	}

	if layout.Short() {
		return block
	}

	return append([]string{""}, block...)
}

// incomingMessageLines renders a message from the other side: the name of
// whoever sent it and the time on one line, the text under it.
func (m Model) incomingMessageLines(
	entry timelineEntry,
	width int,
	styles viewStyles,
) []string {
	selected := m.entrySelected(entry)
	surface := m.selectedSurface(selected)
	indent := spaces(selectionMarkerWidth + contentInsetWidth)

	head := m.painter(surface).
		add(m.selectionMarkStyle(selected), m.selectionMark(selected)).
		add(
			styles.authorColor(entry.message.AuthorID),
			messageAuthor(entry.message),
		)
	if entry.message.Time != "" {
		head = head.
			add(styles.unstyled(), authorTimeSeparator).
			add(styles.text(m.tokens().MutedText), entry.message.Time)
	}

	return append(
		[]string{head.pad(width - head.width()).String()},
		m.messageBodyLines(entry, indent, width, surface, styles)...,
	)
}

// shortMessageLines renders a message on a screen too short for the head
// line and the text on separate rows.
//
// The sender and the time go: what is left is the message itself, which is
// what a user came to read. The text still wraps, because a screen that is
// short is not a screen where a message may be cut in half.
func (m Model) shortMessageLines(
	entry timelineEntry,
	width int,
	styles viewStyles,
) []string {
	indent := spaces(selectionMarkerWidth + contentInsetWidth)
	textWidth := maxInt(width-m.widths.StringWidth(indent), 1)

	selected := m.entrySelected(entry)
	surface := m.selectedSurface(selected)

	wrapped := m.widths.Wrap(entryText(entry), textWidth, ellipsis)
	if len(wrapped) == 0 {
		wrapped = []string{""}
	}

	body := make([]string, 0, len(wrapped))
	for index, line := range wrapped {
		row := m.painter(surface).add(styles.body(false), indent+line)
		if index == 0 {
			row = m.painter(surface).
				add(m.selectionMarkStyle(selected), m.selectionMark(selected)).
				add(styles.body(false), line)
		}

		body = append(body, row.pad(width-row.width()).String())
	}

	return body
}

// outgoingMessageLines renders a message of this user: a block on the
// right with the composer's own surface behind it, and the time and the
// state of the send under the text, at its right edge.
//
// There is no author line: the block is on the right, which is what says
// it is from this user, and "You" above a message the reader is looking at
// is a word they have to read once per message to learn nothing.
func (m Model) outgoingMessageLines(
	entry timelineEntry,
	width int,
	styles viewStyles,
) []string {
	selected := m.entrySelected(entry)
	surface := m.selectedSurface(selected)
	blockWidth := m.outgoingBlockWidth(width)
	textWidth := maxInt(blockWidth-2*outgoingBubbleInset, 1)
	offset := maxInt(width-selectionMarkerWidth-blockWidth, 0)

	rows := make([]string, 0, 4)
	for index, line := range m.widths.Wrap(entryText(entry), textWidth, ellipsis) {
		// The marker of the message under the cursor goes on the first row
		// of its block and on no other: a mark down the side of a block is
		// a mark on the block, and one on the state under the text would
		// say that the state is the message the keys act on.
		mark := spaces(selectionMarkerWidth)
		if index == 0 {
			mark = m.selectionMark(selected)
		}

		rows = append(rows, m.outgoingRow(
			styles, surface, m.selectionMarkStyle(selected), mark,
			offset, blockWidth, line, m.tokens().OutgoingMessage,
		))
	}

	label, stateStyle := m.historyStateRun(entry.message, textWidth)
	rows = append(rows, m.outgoingRow(
		styles, surface, m.selectionMarkStyle(selected),
		spaces(selectionMarkerWidth), offset, blockWidth,
		label, stateStyle,
	))

	return rows
}

// outgoingBlockWidth returns how many columns the block of a message of
// this user takes, marker column included.
//
// The share is of the whole feed and the marker column comes off it, so
// the row is exactly as wide as the feed: a row one column wider is a row
// the region cuts with an ellipsis at the far edge of the screen, and the
// first thing that goes is the state under the text.
func (m Model) outgoingBlockWidth(width int) int {
	shared := int(float64(width) * outgoingBubbleShare)
	block := minInt(shared, width-selectionMarkerWidth)

	return maxInt(block, 1)
}

// outgoingRow draws one row of an outgoing block: the marker column, the
// columns to the left of the block, and the block itself.
//
// The block is exactly blockWidth columns wide whichever way round the
// text goes, so two messages of the same conversation line up on their
// right edge and the eye can read down them. The surface of the composer
// is written on the columns to the left of the text as well, because a
// block whose first column is on the chat background and the rest on the
// composer's is a block with a notch in it.
func (m Model) outgoingRow(
	styles viewStyles,
	surface theme.Color,
	markStyle lipgloss.Style,
	mark string,
	offset, blockWidth int,
	content string,
	colour theme.Color,
) string {
	inner := maxInt(blockWidth-2*outgoingBubbleInset, 1)
	fitted := m.widths.Fit(content, inner, ellipsis)

	// The columns to the left of the block are the row of the message and
	// take the surface of the row: the block has the composer's own
	// surface of its own, and the selection is what is around it.
	row := m.painter(surface).add(markStyle, mark).pad(offset)

	return row.add(
		styles.bubble(),
		spaces(outgoingBubbleInset)+
			styles.text(colour).Render(fitted)+
			spaces(maxInt(inner-m.widths.StringWidth(fitted), 0))+
			spaces(outgoingBubbleInset),
	).String()
}

// historyStateRun returns the words under a message of this user and the
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
func (m Model) historyStateRun(message Message, width int) (string, theme.Color) {
	state, known := deliveryStateOf(deliveryOfHistory(message))
	if !known {
		return "", m.tokens().MutedText
	}

	label := state.Mark().String()
	if message.Time != "" {
		label += " " + message.Time
	}

	return m.widths.TruncateMarked(label, width, ellipsis), state.Color(m.tokens())
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

// messageBodyLines returns the rows of the text of a message, wrapped to
// the width the message has.
func (m Model) messageBodyLines(
	entry timelineEntry,
	indent string,
	width int,
	surface theme.Color,
	styles viewStyles,
) []string {
	textWidth := maxInt(width-m.widths.StringWidth(indent), 1)
	if textWidth < 1 {
		return nil
	}

	// The text starts in the column the author's name starts in (§4.4), so
	// the marker column is spent on the text rows as well: a message whose
	// name is one column to the left of its own text reads as two messages.
	wrapped := m.widths.Wrap(entryText(entry), textWidth, ellipsis)
	lines := make([]string, 0, len(wrapped))

	for _, line := range wrapped {
		row := m.painter(surface).add(styles.body(false), indent+line)
		lines = append(lines, row.pad(width-row.width()).String())
	}

	return lines
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

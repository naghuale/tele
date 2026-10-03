package tui

// This file is the track of the feed and the square that stands on it.
//
// The owner's words (03.10, a channel, on a real account): «чтобы перемотка
// была визуально хороша, нужно на всю длину бесцветным полупрозрачным
// фоном, и справа по всей длине на конце фона — красный квадрат, чтобы видеть
// лучше». Before this a long conversation said nothing about where the reader
// was: not how much was above, not how much was below, and not whether the
// end of the conversation was on the screen at all.
//
// So there are two things here and they are one answer between them. The
// track is a quiet column along the right edge of the feed for the whole
// height of it — a step of the theme's own surface ramp with nothing drawn
// in it, because §1 allows no line down the side of a conversation and a
// stripe of colour is the same thing in another colour. The square is the
// one mark on the screen that says where the window is, in the red of the
// palette, one cell wide and one cell tall so that it is a square and not
// a bar the reader has to measure.
//
// The position of the square is the window and not a number of its own. It
// is measured by the same walk, over the same entries, with the same
// entryRows the view draws with, in the same place in the frame that draws
// them (timelineFeed). A square placed by a second measurement of the
// conversation is a square that lies: it would stand at the top while the
// newest message was on the last row of the feed, and there would be no
// test on the screen that could catch it.

// scrollTrackColumns is how many columns of a row of the feed the track
// takes.
//
// One. The right margin of the feed is one column wider than that on a
// two-pane screen and exactly as wide on a single-pane one, so the track
// stands in the margin on the first and takes the last of it on the second
// — and neither message moves, neither the selection marker moves, and no
// block is one column narrower than it was. A track that ate a column of
// the feed would be a track that made every message on the screen wrap
// differently, and a conversation whose messages rewrap while the reader
// scrolls is a conversation nobody reads.
const scrollTrackColumns = 1

// scrollMarkerGlyph is the square on the track: a full block.
//
// It is a character every terminal font has, with a Nerd Font behind it or
// without one, so the setting of §4.4.2 changes nothing here — and it is
// one column wide in both of the width rules of internal/tui/termwidth,
// which is what keeps the track one column in the drawing and in the
// measurement of it.
const scrollMarkerGlyph = "█"

// feedWindow is where the reader is in the conversation, in the rows the
// entries really take.
//
// It is three numbers and not an index: the feed is placed by the heights of
// the entries and not by how many of them there are (timeline.go, anchoredAt),
// and a square placed by counting messages would walk past a conversation of
// long ones and crawl through a conversation of short ones. The rows are the
// measure of the whole of the scroll model, and the drawing has no other one
// either.
type feedWindow struct {
	// focus is how many rows of the conversation stand above the message
	// under the cursor.
	//
	// It is the message under the cursor and not the first row of the
	// window, because the message under the cursor is what the keys act on
	// and what the reader is reading: the window is a screenful of the
	// conversation and the cursor walks inside it without moving it, so a
	// square placed by the window stands still while j and k walk the
	// reader up and down the same screenful (owner, 03.10, a real
	// account: «маркер стоит не там»).
	focus int

	// visible is how many rows of the conversation the window is showing.
	visible int

	// total is how many rows the whole conversation takes.
	total int

	// newest is how many rows the newest entry takes: the row it starts
	// on is the end of the conversation, and the square is on the last row
	// of the track when the cursor is on it.
	newest int
}

// canScroll reports whether the conversation is longer than the window on
// it: whether there is anything to move the reader through.
func (w feedWindow) canScroll() bool { return w.total > w.visible }

// trackOn returns the track for a feed area of the given height, with the
// square standing where the message under the cursor is on it.
//
// The track is the conversation and the square is a message in it: the oldest
// loaded message stands on the first row of the track, the newest one on the
// last row, and a reader in between stands where their message stands. The
// newest message is the end of the conversation, so it is the bottom of the
// track however tall that message is and however much of it is on the screen —
// a message taller than the feed is taller than the feed, and the middle of it
// is nowhere near the end of the conversation.
//
// The division rounds down rather than to the nearest row, so that the square
// reaches the bottom of the track exactly when the cursor is on the newest
// message and not one row before it — «внизу дорожки — когда открыт самый
// низ» (владелец, 03.10).
//
// rows is the height of the area the track is drawn along and the feed is
// drawn into: every row of it carries the track, whether the row says
// something or is the air a conversation shorter than its feed leaves, and the
// square is placed against that height and not against the rows the messages
// happen to fill.
func (w feedWindow) trackOn(rows int) scrollTrack {
	if rows < 1 || w.total < 1 {
		return scrollTrack{}
	}

	track := scrollTrack{rows: rows, marker: rows - 1}

	// A conversation that fits on the screen is at its end, and says so in
	// the same place: that is where a reader is after opening a chat and
	// after sending a message, and a track that said anything else about
	// those two moments would be telling the reader a story about a
	// conversation they are not reading.
	if !w.canScroll() {
		return track
	}

	// The row the newest message starts on is the last row of the track.
	// A conversation of one entry has no such row — it is all of the
	// newest message — and its square is on the last row of the track with
	// nothing to walk through.
	end := w.total - w.newest
	if end < 1 {
		return track
	}

	track.marker = minInt(maxInt(w.focus, 0)*(rows-1)/end, rows-1)

	return track
}

// scrollTrack is the track of the feed: the rows it takes on the screen and
// the row of it the square stands on.
//
// A zero track is no track: it is what a conversation with nothing in it
// gets, and a column of the theme's own background beside an empty feed
// says nothing that the empty feed does not already say.
type scrollTrack struct {
	// rows is how many rows of the screen the track takes.
	rows int

	// marker is the row of the square, counted from the top of the track.
	marker int
}

// markerOn reports whether the square of the track stands on this row of it.
func (t scrollTrack) markerOn(row int) bool {
	return t.rows > 0 && row == t.marker
}

// timelineFeed draws the feed and measures where the reader is in it, in one
// walk over the entries of the conversation.
//
// It is one function and not two because the square of the track and the rows
// of the feed are the same question asked twice, and two answers to it are two
// answers that can differ: the anchor, the cut and the heights of the entries
// are read once here, and the walk that draws the rows is the walk of
// entryRowsFrom the view has always drawn with — the function that cuts a
// message taller than the feed and drops the air off the topmost one.
//
// The three sums the walk keeps are the three numbers the track is drawn from:
// the rows above the window (which is how much of the conversation the reader
// has walked through, and so how many rows are left to scroll), the rows above
// the message under the cursor (which is where the square stands) and the rows
// below the window (which is how long the conversation is). Every entry is
// measured once, by entryRows, which is len(entryLines(...)) — the height the
// view draws the entry at, and the height the window itself was placed against
// (anchoredAt).
func (m Model) timelineFeed(layout Layout, width, rows int) feedDrawing {
	drawing := feedDrawing{rows: m.timelineBody(layout, width, rows)}

	// The rows the feed is drawn into are the rows the model measures
	// against (historyFeedRows), which is the number the view hands in as
	// rows: a view that drew the feed into one budget and placed the square
	// against another would be a view with a square that lies about its own
	// rows. The smaller of the two is taken where they could ever differ, so
	// that the window can never claim more of the conversation than the rows
	// under it hold.
	budget := minInt(rows, m.historyFeedRows(layout, width))
	total := m.timelineTotal()
	if budget < 1 || total == 0 {
		return drawing
	}

	styles := m.styles()
	entries := m.feedEntries()
	top := entryIndexOfFeed(entries, clampIndex(m.timelineTop, total-1))
	cursor := entryIndexOfFeed(entries, clampIndex(m.selectedMsg, total-1))

	var beforeTop, beforeCursor, totalRows, newest int

	for index, entry := range entries {
		height := m.entryRows(entry, layout, width, styles)
		totalRows += height

		// The rows above the window: the entries before it.
		if index < top {
			beforeTop += height
		}

		// The rows above the message under the cursor: the entries before
		// it, which are the entries before the window and the ones inside
		// it that the cursor has already walked past.
		if index < cursor {
			beforeCursor += height
		}

		if index == len(entries)-1 {
			newest = height
		}
	}

	// The rows above the window are the entries above it whole and the rows
	// the cut takes off the top of the entry it starts on: those rows are
	// part of the conversation and part of neither the window nor the walk
	// above it, and counting them on neither side puts the square two cuts
	// away from the bottom of the track at the newest message — which is
	// exactly where it has to be.
	above := beforeTop + m.timelineCut

	drawing.window = feedWindow{
		focus:   beforeCursor,
		visible: minInt(budget, maxInt(totalRows-above, 0)),
		total:   totalRows,
		newest:  newest,
	}

	return drawing
}

// feedDrawing is the feed as it is drawn together with where it stands in
// the conversation.
//
// The two are one value because the view needs both and computes them in
// one place: the rows go on the screen, and the window says where the
// square of the track goes (conversationRegion).
type feedDrawing struct {
	// rows are the rows of the feed, as they are drawn.
	rows []string

	// window is where those rows stand in the conversation.
	window feedWindow
}

// withScrollTrack puts the track along the right edge of the rows of the
// feed.
//
// It is a pass over rows that are already drawn rather than a drawing of its
// own, because the track is not a message: it is the column beside them. A
// track drawn inside the painter of a block would be a column of the feed
// that a message of this user ends before and a message from the other side
// starts after, which is where this one is, and a painter that knew about
// it would have to know about the selection under it and the gap above it
// as well.
//
// The rows come in two kinds. A row of the feed is a row of the pane: it is
// as wide as the pane and it says so in its own background. A row the
// conversation did not fill is nothing at all — anchorTimeline leaves the
// air on the side the window is anchored to — and it is padded here to the
// width of the pane, because a track cell written at the first column of
// such a row would be a stripe down the left of the conversation on every
// screen whose chat is shorter than its feed.
func (m Model) withScrollTrack(rows []string, track scrollTrack, width int) []string {
	if track.rows < 1 || width < scrollTrackColumns {
		return rows
	}

	styles := m.styles()
	painted := make([]string, 0, len(rows))
	for index, row := range rows {
		painted = append(painted, m.scrollTrackRow(row, track.markerOn(index), width, styles))
	}

	return painted
}

// scrollTrackRow returns one row of the feed with the track in its last
// column.
//
// The column is cut by column and not by byte: a row of the feed is a
// styled row, and a byte cut through it would print half an escape sequence
// as text — a stripe of `;24;36m` down the right of a conversation.
func (m Model) scrollTrackRow(
	row string,
	marker bool,
	width int,
	styles viewStyles,
) string {
	last := maxInt(width-scrollTrackColumns, 0)
	head := m.widths.Truncate(row, last, "")
	head += spaces(maxInt(last-m.widths.StringWidth(head), 0))

	if marker {
		return head + styles.scrollMarker().Render(scrollMarkerGlyph)
	}

	return head + styles.scrollTrack().Render(spaces(scrollTrackColumns))
}

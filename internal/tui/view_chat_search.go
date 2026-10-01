package tui

import "telecli/internal/tui/theme"

// This file draws the search of §9: the field in the row of the header that
// says the list can be searched, and the matched fragment inside the titles
// below it.
//
// The field is one row, and it is the row the hint was on rather than a row
// of its own above the header. A search that pushed the list down while it
// was being typed moved the results under the query on every keystroke, and
// the results are what the user is watching (the owner, 01.10). What the
// field does not do is frame anything (§9, and §1 in general): the row sits
// on the surface of the list, and the one thing that says where the keys
// are is the accent of the pane, which is where the keys are.

// searchPlaceholder is what the line shows before anything is typed into
// it.
//
// It is the name of the thing rather than a key: the key is in the header
// of the list (§4.1) and in the hint bar, and a field that shows a key is
// a field that a user types the key into.
const searchPlaceholder = "Search chats"

// searchCursorWidth is the cell the cursor of the search line takes.
//
// The field keeps one cell for it out of its own width, whatever it is
// drawn on: a reversed cluster of the query, or a bar when the cursor is
// past the end of it. Reserving the cell rather than borrowing one is what
// keeps a long query from growing the line past the width of the pane.
const searchCursorWidth = 1

// chatSearchFieldRow draws the row of the header as the field the query is
// typed into.
//
// It is the row the "/ search" hint was on, drawn as the field instead: the
// query with its cursor, or the placeholder while there is no query. The
// field is drawn by the painter of the row so that it carries the surface of
// the list and the accent of the pane exactly as the hint did, and it is
// padded out to the width of the pane so the row is a rectangle of the same
// height whatever is in it.
func (m Model) chatSearchFieldRow(layout Layout, width, inset, room int) string {
	styles := m.styles()
	cursor := styles.cursor(m.focus == FocusSearch)
	row := m.painter(theme.Color{}).add(styles.unstyled(), spaces(inset))

	// One cell of the row is kept for the cursor out of the room the query
	// has, whatever it is drawn on: a query that filled the row would put
	// its cursor past the last column of the pane.
	room = maxInt(room-searchCursorWidth, 1)

	if len(m.chatSearch.query) == 0 {
		row = row.add(cursor, cursorBar).
			add(styles.dimmed(m.tokens().MutedText), m.searchPlaceholderFor(room))

		return row.pad(width - inset - m.widths.StringWidth(row.String())).
			String()
	}

	window, at := m.chatSearchWindow(m.chatSearch.query, m.chatSearch.queryCursor(), room)

	before, under, after := splitAtCluster([]rune(window), at)
	plain := styles.text(m.tokens().PrimaryText)

	// The cursor is past the last cluster of the window, so the bar is the
	// last thing on the row and nothing has to move.
	if under == "" {
		return row.
			add(plain, before).
			add(cursor, cursorBar).
			pad(width - inset - m.widths.StringWidth(before) - searchCursorWidth).
			String()
	}

	return row.
		add(plain, before).
		add(cursor, under).
		add(plain, after).
		pad(width - inset - m.widths.StringWidth(window)).
		String()
}

// searchPlaceholderFor returns what the field says about itself while there
// is no query in it, at the width it has.
//
// It is the name of the thing — a field that shows a key is a field a user
// types the key into — and the name it can afford is the one the row had
// before it was a field. The narrowest list there is has thirteen columns
// of text, and "Search chats" plus the cursor is fifteen, so there the
// field says the words the row said while it was a hint. A field cut to
// "Search cha" is a field nobody has read.
func (m Model) searchPlaceholderFor(room int) string {
	for _, candidate := range []string{searchPlaceholder, chatListSearchHint} {
		if m.widths.StringWidth(candidate) <= room {
			return candidate
		}
	}

	return ""
}

// chatSearchWindow returns the part of the query the line draws and the
// rune index of the cursor inside it.
//
// The line is one row and the query is one line, so the window is a run of
// grapheme clusters rather than rows: a query wider than the field shows
// its end, which is where the cursor and everything that was just typed
// are, and the run never cuts a cluster. A terminal prints the runes of
// one in sequence, so half a cluster is not a shorter word but a broken
// one.
//
// It is a method because a run of clusters is a run of columns, and the
// columns are the ones of the terminal this model is drawn in.
func (m Model) chatSearchWindow(query []rune, cursor, width int) (string, int) {
	clusters := graphemes(query)
	if len(clusters) == 0 {
		return "", 0
	}

	at := searchClusterAt(query, cursor)
	first, last, used := at, at, 0

	// To the right first, so that a cursor at the end of a query that is
	// too wide for the field shows the end of it rather than the
	// beginning.
	for last+1 < len(clusters) &&
		used+m.widths.StringWidth(string(clusters[last+1])) <= width {
		last++
		used += m.widths.StringWidth(string(clusters[last]))
	}

	for first > 0 && used+m.widths.StringWidth(string(clusters[first-1])) <= width {
		first--
		used += m.widths.StringWidth(string(clusters[first]))
	}

	var (
		text   []rune
		offset int
	)

	for index, cluster := range clusters {
		if index >= first && index < at {
			offset += len(cluster)
		}
		if index < first || index > last {
			continue
		}

		text = append(text, cluster...)
	}

	return string(text), offset
}

// searchClusterAt returns which cluster of the query the cursor is on, or
// how many clusters there are when the cursor is past the end of it.
//
// It is the rule splitAtCluster draws by: a cursor sits after the cluster
// it belongs to, a cursor between two clusters belongs to the one before
// it, and a cursor at the end is past the last one.
func searchClusterAt(query []rune, cursor int) int {
	used := 0

	for index, cluster := range graphemes(query) {
		if used+len(cluster) > cursor {
			return index
		}
		used += len(cluster)
	}

	return used
}

// chatTitleSegment is a run of a chat title as it is drawn: the fragment
// the query matched, or the text around it.
type chatTitleSegment struct {
	text    string
	matched bool
}

// chatTitleSegments splits a chat title into the fragment the query
// matched and the text around it.
//
// A title that does not match, one that no query has been typed yet, and
// one the pane is too narrow for all come out of here as a single
// unmatched segment: the highlight is an addition to a title that is
// already drawn, and a title that cannot show it is still a title.
//
// It is a method because what fits in the pane is measured in the columns
// of one terminal, and a title cut for another one is a title cut in the
// wrong place.
func (m Model) chatTitleSegments(
	title string,
	query []rune,
	width int,
) []chatTitleSegment {
	visible := m.widths.TruncateMarked(title, width, ellipsis)
	one := []chatTitleSegment{{text: visible}}

	if len(query) == 0 || visible == "" {
		return one
	}

	from, to, ok := chatSearchMatch([]rune(visible), query)
	if !ok {
		return one
	}

	// The match is in the title and the title is too wide for the pane, so
	// the part of it that is on the screen is the part that is drawn: a
	// highlight placed in a title that was cut afterwards would mark
	// nothing the user can read.
	runes := []rune(visible)
	to = minInt(to, len(runes))
	if from >= to {
		return one
	}

	segments := make([]chatTitleSegment, 0, 3)
	if from > 0 {
		segments = append(segments, chatTitleSegment{text: string(runes[:from])})
	}
	segments = append(segments, chatTitleSegment{
		text:    string(runes[from:to]),
		matched: true,
	})
	if to < len(runes) {
		segments = append(segments, chatTitleSegment{text: string(runes[to:])})
	}

	return segments
}

// chatTitle writes the name of a chat into a row, with the fragment a
// search matched in the second accent of the theme.
//
// The title is fitted to the width of the row before it is split, so the
// fragments are the words that are actually on the screen. Every fragment
// goes through the painter, so a selected row carries its background
// across the name and not only across the first letter of it.
//
// The name is in bold: the mockup of the owner writes it so, and it is the
// one run of the row a user reads before the preview under it, so it is the
// one run that is loudest.
func (m Model) chatTitle(
	p *rowPainter,
	title string,
	selected bool,
	width int,
) *rowPainter {
	styles := p.styles

	for _, segment := range m.chatTitleSegments(title, m.chatSearch.query, width) {
		style := styles.rowText(selected).Bold(true)
		if segment.matched {
			style = styles.matchRun().Bold(true)
		}

		p = p.add(style, segment.text)
	}

	return p
}

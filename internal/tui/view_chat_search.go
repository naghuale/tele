package tui

// This file draws the search of §9: the line above the chat list, and the
// matched fragment inside the titles below it.
//
// The line is one row. It is the header of a list that is 24 columns wide
// at its widest and 16 on a medium screen, and a label of its own would
// leave a field of 11 columns to type a chat name into. What it does not
// do is frame anything (§9, and §1 in general): the line sits on the
// surface of the list, and the one thing that says where the keys are is
// the accent column of the region, which is where the keys are.

// searchPlaceholder is what the line shows before anything is typed into
// it.
//
// It is the name of the thing rather than a key: the key is in the header
// of the list (§4.1) and in the hint bar, and a field that shows a key is
// a field that a user types the key into.
const searchPlaceholder = "Search chats"

// searchRegionHeight is how many rows the search line takes.
//
// One, at every width and on every height. A search that grew with the
// query would be a search whose results move while it is being typed, and
// the results are what the user is watching.
const searchRegionHeight = 1

// searchCursorWidth is the cell the cursor of the search line takes.
//
// The field keeps one cell for it out of its own width, whatever it is
// drawn on: a reversed cluster of the query, or a bar when the cursor is
// past the end of it. Reserving the cell rather than borrowing one is what
// keeps a long query from growing the line past the width of the pane.
const searchCursorWidth = 1

// chatSearchRegion draws the line above the chat list.
//
// It is a region of its own rather than the first line of the list: the
// keys are in it while it is open (§5), so the accent belongs to it, and
// the list below gives the column up. The two are stacked and not side by
// side, so the screen still has exactly one accent column (§5.2) and the
// list does not move when the focus crosses from one to the other.
func (m Model) chatSearchRegion(width int) string {
	return m.renderRegion(
		m.styles().list,
		m.focus == FocusSearch,
		width,
		m.chatSearchLines(width),
		searchRegionHeight,
	)
}

// chatSearchLines returns the row of the search line: the query with its
// cursor, or the placeholder while there is no query.
func (m Model) chatSearchLines(width int) []string {
	styles := m.styles()
	inset := spaces(contentInsetWidth)
	cursor := styles.cursor(m.focus == FocusSearch)

	if len(m.chatSearch.query) == 0 {
		return []string{inset + cursor.Render(cursorBar) +
			styles.dimmed(m.tokens().MutedText).Render(searchPlaceholder)}
	}

	window, at := chatSearchWindow(
		m.chatSearch.query,
		m.chatSearch.queryCursor(),
		maxInt(width-contentInsetWidth-searchCursorWidth, 1),
	)

	before, under, after := splitAtCluster([]rune(window), at)
	plain := styles.text(m.tokens().PrimaryText)

	// The cursor is past the last cluster of the window, so the bar is the
	// last thing on the line and nothing has to move.
	if under == "" {
		return []string{inset + plain.Render(before) + cursor.Render(cursorBar)}
	}

	return []string{inset + plain.Render(before) +
		cursor.Render(under) +
		plain.Render(after)}
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
func chatSearchWindow(query []rune, cursor, width int) (string, int) {
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
		used+cellWidth(string(clusters[last+1])) <= width {
		last++
		used += cellWidth(string(clusters[last]))
	}

	for first > 0 && used+cellWidth(string(clusters[first-1])) <= width {
		first--
		used += cellWidth(string(clusters[first]))
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
func chatTitleSegments(title string, query []rune, width int) []chatTitleSegment {
	visible := truncateCells(title, width)
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

// chatTitleLine draws a chat title, with the fragment a search matched in
// the accent of the theme.
//
// The title is fitted to the width of the row before it is split, so the
// fragments are the words that are actually on the screen.
func (m Model) chatTitleLine(
	styles viewStyles,
	title string,
	selected bool,
	width int,
) string {
	segments := chatTitleSegments(title, m.chatSearch.query, width)
	if len(segments) == 1 && !segments[0].matched {
		return styles.rowText(selected).Render(fitCells(segments[0].text, width))
	}

	var line string
	for _, segment := range segments {
		style := styles.rowText(selected)
		if segment.matched {
			style = styles.matchRun()
		}

		line += style.Render(segment.text)
	}

	return line
}

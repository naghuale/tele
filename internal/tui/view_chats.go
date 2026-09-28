package tui

import (
	"strconv"

	"github.com/charmbracelet/lipgloss"

	"telecli/internal/tui/termwidth"
	"telecli/internal/tui/theme"
)

// chatListTitle is the header of the list.
//
// It is the word §4.1 names, and it is the whole header: a title long
// enough to be cut is a title cut in the middle of a word, and at the
// width of a medium list even "telecli — Chats" would be.
const chatListTitle = "Chats"

// chatListLines returns the lines of the chat list region.
//
// The header is a title, the rule that says this panel has the keys, and
// the second line §4.1 puts under the title. The rule is a blank line of
// the same height when the focus is elsewhere, so moving the focus moves
// one line and nothing under it.
//
// The rows are two lines each with a blank line between them, and the
// preview is the first thing to go on a short screen (§3.4), because a
// name without a preview still says who someone is while a preview without
// a name says nothing about who said it.
//
// A search narrows the rows to the chats that matched and nothing else:
// the list on the screen is the list there is, so the rows that are drawn
// are the ones the query left.
func (m Model) chatListLines(layout Layout, width, height int) []string {
	lines := []string{
		m.panelHeading(chatListTitle, width, m.panelFocused(listPane)),
		m.styles().focusRule(width, m.panelFocused(listPane)),
		m.chatListHeaderSecondLine(layout, width),
	}

	rows, selected := m.chatListRows(layout, width)
	if len(rows) == 0 {
		return append(lines, m.chatListEmptyLines(layout, width)...)
	}

	budget := height - layout.hintLines() - len(lines)
	rowHeight := m.chatListRowHeight(layout)

	start, end := visibleRange(len(rows), selected, budget/rowHeight)

	for index := start; index < end; index++ {
		lines = append(lines, rows[index]...)
	}

	return lines
}

// panelHeading draws the title of a panel: the accent of the theme when
// the panel has the keys, the dim step of the text ramp when it does not.
//
// The colour and the rule under it say the same thing, and that is the
// point: the rule is what a terminal with no colour shows, and the colour
// is what a terminal with colour shows first.
func (m Model) panelHeading(title string, width int, focused bool) string {
	styles := m.styles()

	style := styles.dimmed(m.tokens().MutedText)
	if focused {
		style = styles.text(m.tokens().Focus).Bold(true)
	}

	return style.Render(m.widths.Fit(title, width, ellipsis))
}

// chatListHeaderSecondLine is the second line of the chat list header.
//
// §4.1 puts a search and an unread count there, and §3.3 puts the status
// block there on a narrow screen, where there is no conversation beside the
// list to carry it. The status wins where it is drawn: a user who cannot
// see whether telecli is connected needs that more than a count of unread
// messages, and the count is still on every chat row.
//
// A list with no status to show keeps the unread count at every width, so
// the line is never blank for a reason the user has to work out.
func (m Model) chatListHeaderSecondLine(layout Layout, width int) string {
	if !layout.TwoPane() {
		if status := m.statusBlockLines(layout, width); len(status) > 0 {
			return m.statusStyle().Render(status[0])
		}
	}

	return m.chatListSummaryLine(width)
}

// chatListSummaryLine is the second line of the header where there is no
// status: how to search the list, and how much of it is unread.
//
// §4.1 puts a search and an unread count there, and the key is in the line
// rather than only in the hint bar because the header is what a user reads
// before deciding to do anything at all, and a list that can be searched
// has to say so somewhere that is not behind a key they have to know.
func (m Model) chatListSummaryLine(width int) string {
	unread := 0
	for _, chat := range m.chats {
		unread += chat.Unread
	}

	text := unreadBadge(unread) + " unread"
	if unread == 0 {
		text = "no unread"
	}

	summary := chatListSearchHint + statusSeparator + text

	return m.styles().dimmed(m.tokens().MutedText).
		Render(m.widths.Fit(summary, width, ellipsis))
}

// chatListSearchHint is how the list says that it can be searched.
const chatListSearchHint = "/ Search"

// chatListRowHeight returns how many lines one chat takes.
//
// A chat is a name and a preview with a blank line under it, so that two
// chats are two blocks rather than four lines of one column. On a screen
// too short for that (§3.4) the row is one line and the blank lines go.
func (m Model) chatListRowHeight(layout Layout) int {
	if layout.Short() {
		return 1
	}

	// Two rows of words between two rows of half a row of air (§4.2). The
	// height a row is budgeted with is the height it is drawn at: a budget
	// of three for a row of four is a list that believes it is a row of
	// air shorter than it is, and the window it places is then a row lower
	// than the rows it is placed against.
	return 4
}

// chatListRows renders every chat the list shows into its lines, and the
// row the selection is on.
//
// The second value is the position of the selection among the rows rather
// than its index in the whole list: a search puts a chat the query kept
// first in the rows and last in the list, and the window of §10.5 has to
// be computed on what is drawn. It is -1 when the chat under the cursor is
// not one of the rows, which is what a query that found nothing leaves.
func (m Model) chatListRows(layout Layout, width int) ([][]string, int) {
	entries := m.chatListEntries()
	rows := make([][]string, 0, len(entries))
	selected := -1

	for index, entry := range entries {
		rows = append(
			rows,
			m.chatListRowLines(entry, entry.index == m.selectedChat, layout, width),
		)

		if entry.index == m.selectedChat {
			selected = index
		}
	}

	return rows, selected
}

// chatListRowLines renders one chat as four rows: half a row of air, the
// name with its time, the preview with its unread badge, and half a row of
// air again.
//
// The two rows of words are the two things a chat list is: who it is and
// what it is about. The time and the badge end in the same column, so the
// eye can run down the times without reading any of the names — and the
// badge with them, because a badge that sits wherever the preview of its
// chat happened to end is a column the eye has to find on every row, and a
// column the eye has to find is a column that is not there.
//
// The two rows of air are what make the selected chat a chat rather than a
// stripe, and they are the same drawing as the air around a message in the
// conversation: a half block in the colour of the row, which rounds the
// selection at the top and at the bottom without a row of its own. A row
// that is not selected draws them in the background of the list, where
// they are invisible, so the rows of the list keep their spacing and only
// the chosen one has an edge. They are drawn on every row, selected or not,
// because a row that was four rows long only while it was the chosen one
// would be a list whose window could not be placed against its rows.
//
// The selection also keeps the same two columns of air on the left and on
// the right that a message keeps inside its block. A selection that runs
// the width of the list is the band the list stopped having, and one
// column of air between its edge and the words of the name is the same
// mistake a block made with one column of inset.
func (m Model) chatListRowLines(
	entry chatListEntry,
	selected bool,
	layout Layout,
	width int,
) []string {
	styles := m.styles()
	chat := entry.chat
	surface := m.selectedSurface(selected)
	// The air of the selection is in the background of the list, and it is
	// written rather than left to the region behind it: a cell with no
	// background at all is a cell the terminal paints with whatever it
	// thinks its own background is.
	list := styles.on(m.tokens().SidebarBackground, styles.unstyled())
	// content is the width of the words of the row, between the two
	// columns of air on each side. Every row of the list is fitted to
	// width, which is content and the four columns of air.
	content := maxInt(width-2*chatListInset, 1)
	air := func() *rowPainter {
		return m.painter(theme.Color{}).own(list, spaces(chatListInset))
	}

	title := chat.Title
	if title == "" {
		title = untitledChatTitle
	}

	mark := styles.selectionMarker(selected)
	at := chat.Time
	badge := m.chatListUnreadBadge(chat, surface)
	timeColumns := m.widths.StringWidth(at)
	timeStyle := styles.text(m.tokens().MutedText)

	// §4.2 gives the time up before the name does, on a list too narrow to
	// carry both: a time at the end of a row is worth less than the first
	// few letters of the name it belongs to, and a cut name is a name a
	// user cannot recognise at a glance. The badge and the time are spent
	// out of the width of the name and not added to it.
	nameWidth := content - selectionMarkerWidth
	if nameWidth-timeColumns-timeGapColumns >= minNameBesideTime {
		nameWidth -= timeColumns + timeGapColumns
	} else {
		at = ""
	}

	// The marker is handed to the painter as text and a style and not as a
	// rendered run: Lip Gloss re-reads the sequences of what it is given,
	// and a run that already carries the surface loses it on the way
	// through.
	markRun := mark + " "
	markStyle := styles.selectionMark(selected, m.focus == FocusChatList)
	// edge closes a row: the two columns of air on the right of it, in the
	// background of the list.
	edge := func(row *rowPainter) string {
		return row.own(list, spaces(chatListInset)).String()
	}

	if m.chatListRowHeight(layout) == 1 {
		short := maxInt(nameWidth-m.widths.StringWidth(badge.text())-2, 1)

		// On a screen too short for a preview (§3.4) the row is one line:
		// the name, the count, the time. The count is not at the right edge
		// there, because the time is, and the time is the column this list
		// is read down.
		head := edge(badge.at(
			air().
				add(markStyle, markRun).
				add(styles.rowText(selected),
					m.widths.TruncateMarked(title, short, ellipsis)),
		).right(at, width-chatListInset, timeStyle))

		return []string{head}
	}

	head := edge(m.chatTitle(
		m.painter(surface).
			own(list, spaces(chatListInset)).
			add(markStyle, markRun),
		title,
		selected,
		nameWidth,
	).right(at, width-chatListInset, timeStyle))

	// The badge is spent out of the width of the preview and not added to
	// it: a preview that pushes the badge off the end of the row is a
	// preview that has taken the only number in it. The preview is cut
	// before the badge with chatListBadgeGap columns of air between them,
	// so the badge is in the same column whatever the chat is about — and
	// the columns between the end of the preview and the badge are the
	// row's own, so on the selected chat they are the selection and a
	// selection that stops where the words stop is a highlight under a
	// name.
	previewWidth := maxInt(
		content-selectionMarkerWidth-contentInsetWidth-
			m.widths.StringWidth(badge.text())-chatListBadgeGap,
		1,
	)
	detail := edge(badge.write(
		m.painter(surface).
			own(list, spaces(chatListInset)).
			add(markStyle, markRun+spaces(contentInsetWidth)).
			add(
				styles.text(m.tokens().SecondaryText),
				m.widths.TruncateMarked(
					m.chatListPreview(chat), previewWidth, ellipsis,
				),
			),
		width-chatListInset,
	))

	return []string{
		m.chatListAirRow(selected, styles, content, termwidth.HalfBlockLower),
		head,
		detail,
		m.chatListAirRow(selected, styles, content, termwidth.HalfBlockUpper),
	}
}

// chatListAirRow draws one row of a half block across the width of a chat
// of the list, in the colour of its selection on the background of the
// list beside it.
//
// The half block is drawn in the *foreground*: the colour of the selection
// is the colour of the glyph and the background of the list is behind it,
// which is what puts the selection in the lower half of one cell and the
// upper half of the next — half a row of air above a row of words and half
// a row below it, without either of them being a row of its own.
//
// A chat that is not selected draws the halves in the background of the
// list, where they cannot be seen. The rows are drawn either way, because a
// row of four rows only while it was the chosen one is a list whose window
// cannot be placed against its rows.
func (m Model) chatListAirRow(
	selected bool,
	styles viewStyles,
	content int,
	glyph string,
) string {
	list := styles.on(m.tokens().SidebarBackground, styles.unstyled())
	// Neither is drawn where the theme has no colour for it (halfRow),
	// which is a chat list on a screen with no colours that is a list of
	// chats and not a list of bars.
	drawn := m.tokens().SidebarBackground.IsSet()
	glyphs := list
	if selected {
		drawn = drawn && m.tokens().Selected.IsSet()
		glyphs = styles.blockEdge(
			m.tokens().Selected, m.tokens().SidebarBackground,
		)
	}

	return m.painter(theme.Color{}).
		own(list, spaces(chatListInset)).
		own(list, halfRow(glyphs, drawn, glyph, content)).
		own(list, spaces(chatListInset)).
		String()
}

// chatListInset is the air between the edge of a selected row of the list
// and the words in it, on each side.
//
// It is the same two columns a message keeps inside its block, and for the
// same reason: a selection flush against its own words is a stripe of
// colour, and the words of a name are what the row is for.
const chatListInset = 2

// chatListBadgeGap is the air between the end of a preview and the badge
// of the chat, and it is wider than the gap between a name and its time
// because the badge is a pill rather than a word: a pill one column from
// the last letter of the preview reads as one run of the row.
const chatListBadgeGap = 2

// timeGapColumns is the space kept between the name of a chat and the
// time at the other end of its row, and between a preview and its badge.
//
// It is spent out of the width of the name and not added to it: a name
// fitted to the whole row pushes the time off the end of it, and a time
// that is cut in half says nothing about when the message was.
const timeGapColumns = 1

// minNameBesideTime is the narrowest name that is still worth putting a
// time beside. A name of this many columns is a name a user recognises;
// anything shorter and the time is the first thing the row gives up.
const minNameBesideTime = 8

// untitledChatTitle is what a chat with no name is drawn as.
//
// It is said rather than left blank: a row with nothing in it is a row a
// user cannot tell from a row that failed to load, and this one has
// messages in it.
const untitledChatTitle = "(untitled)"

// chatListPreview returns the second line of a chat: what it is about.
func (m Model) chatListPreview(chat Chat) string {
	if chat.Preview == "" {
		return ""
	}

	return chat.Preview
}

// chatListUnreadBadge returns the unread count of a chat as a pill, or
// nothing when the chat has nothing unread.
//
// The pill is the accent of the theme for a chat with one other person in
// it, and the muted step for a group or a channel: a hundred unread
// messages in a channel is background, and a hundred unread messages from
// one person is not. A space stands on each side of the number, so the
// pill is a pill and not a character with a background on it.
//
// With a Nerd Font the pill is rounded with the same two halves a message
// block is rounded with, in the colour of the pill on the background of the
// row. Without one it is a plain rectangle: the halves are characters of a
// font the terminal may not have, and a count in empty squares is a count
// nobody can read.
//
// It is three runs and not one string because it is two colours: the halves
// are the outside of the pill and the number is the inside of it, and one
// run cannot be in the colour of the list in the middle and in the colour
// of the pill at the edges — a run inside a run ends with a reset, and the
// reset would take the background of the pill with it.
func (m Model) chatListUnreadBadge(chat Chat, row theme.Color) chatBadge {
	if chat.Unread <= 0 {
		return chatBadge{}
	}

	background := m.tokens().MutedText
	if !chat.Kind.Grouped() {
		background = m.tokens().Unread
	}

	behind := row
	if !behind.IsSet() {
		behind = m.tokens().SidebarBackground
	}

	badge := chatBadge{
		inner: " " + unreadBadge(chat.Unread) + " ",
		pill:  m.styles().pill(background, m.tokens().SidebarBackground),
	}
	if !m.nerdFont || !background.IsSet() || !behind.IsSet() {
		return badge
	}

	badge.left = termwidth.NerdHalfLeft
	badge.right = termwidth.NerdHalfRight
	badge.edge = m.styles().blockEdge(background, behind)

	return badge
}

// chatBadge is one unread count of a chat list: the halves that round it
// and the number inside it, each with the style it is written in.
type chatBadge struct {
	left  string
	right string
	inner string
	edge  lipgloss.Style
	pill  lipgloss.Style
}

// text returns what the badge says, which is what the row is cut to fit.
func (b chatBadge) text() string {
	return b.left + b.inner + b.right
}

// write puts the badge at the end of a row of the given width, with the
// columns that are missing filled in before it.
func (b chatBadge) write(row *rowPainter, width int) *rowPainter {
	if b.inner == "" {
		return row.pad(width - row.width())
	}

	return b.at(row.pad(width - row.width() - row.widths.StringWidth(b.text())))
}

// at writes the badge where the row has got to.
func (b chatBadge) at(row *rowPainter) *rowPainter {
	if b.left != "" {
		row = row.own(b.edge, b.left)
	}

	return row.own(b.pill, b.inner).own(b.edge, b.right)
}

// unreadBadge is the number on an unread badge (§4.2).
//
// It stops at 99+: a four-digit number on one line of a chat list is a
// number nobody reads, and a badge that grows with the backlog is a badge
// that pushes the preview out of the row.
func unreadBadge(unread int) string {
	if unread > 99 {
		return "99+"
	}

	return strconv.Itoa(unread)
}

// chatListEmptyLines is what the list says when it holds nothing, or when
// it has nothing to show yet.
//
// Beside the conversation pane there is room for the whole reason, so the
// list says the short form and the pane says the words. Alone on the
// screen the list has the whole width and says it all itself. What the user
// reads is the same either way; only the line it is wrapped onto differs.
func (m Model) chatListEmptyLines(layout Layout, width int) []string {
	texts, style := m.chatListEmptyState()

	if layout.TwoPane() {
		short := m.widths.Fit(m.chatListEmptyShort(), width, ellipsis)

		return []string{style.Render(short)}
	}

	lines := make([]string, 0, len(texts))
	for _, text := range texts {
		for _, line := range m.widths.Wrap(text, width, ellipsis) {
			lines = append(lines, style.Render(line))
		}
	}

	return lines
}

// The words of §17 and §18 for a chat list that has nothing in it.
//
// The wait is named, not just spent: a screen that says "Loading" for
// thirty seconds has stopped answering the question the word raises, and
// the sentence that answers it ends with the key that does something about
// it.
const (
	// chatLoadFailedText says the list did not come, without saying why.
	chatLoadFailedText = "Failed to load chats"

	// chatsLoadSlowText is §18's sentence for a load that has taken
	// longer than the wait allows.
	chatsLoadSlowText = "Taking longer than expected. Check connection or press R to retry."

	// chatsLoadFailedHint is the same key for the same reason: a load that
	// failed can be asked again, and asking is what the user wants to do.
	chatsLoadFailedHint = "Check connection or press R to retry."

	// noChatsText and noChatsHint are §17 for an account with no chats,
	// noChatsFoundText is §17 for a search that found nothing, and
	// noMessagesHint is §17 for a chat with nothing in it.
	noChatsText      = "No chats yet"
	noChatsHint      = "Start a new conversation or wait for chats to load."
	noChatsFoundText = "No chats found"
	noMessagesHint   = "Write the first message below."
)

// chatListEmptyState returns the sentences the list is in and the style of
// them.
//
// A load that failed says so in one line and says what to do in the next.
// The reason is not on the screen: a cause can name a file, a TDLib error
// message and a path, and §11.3 and §19 keep those out of a terminal
// somebody is looking at.
//
// A search that found nothing is not a failure and does not borrow the
// words of one. There is a list, the query is wrong for it, and the only
// thing the user has to do is change the query — which is a calm sentence
// and not a red one.
func (m Model) chatListEmptyState() ([]string, lipgloss.Style) {
	styles := m.styles()

	if m.chatSearch.filtering() && len(m.chatListEntries()) == 0 {
		return []string{noChatsFoundText}, styles.dimmed(m.tokens().MutedText)
	}

	switch m.chatsState {
	case loadStateLoading:
		if m.chatsLoadSlow {
			return []string{"Loading chats…", chatsLoadSlowText},
				styles.dimmed(m.tokens().StatusWarning)
		}

		return []string{"Loading chats…"}, styles.dimmed(m.tokens().SecondaryText)

	case loadStateError:
		return []string{chatLoadFailedText, chatsLoadFailedHint},
			styles.dimmed(m.tokens().StatusError)

	case loadStateLoaded:
		return []string{noChatsText, noChatsHint}, styles.dimmed(m.tokens().MutedText)

	default:
		return []string{noChatsText, noChatsHint}, styles.dimmed(m.tokens().MutedText)
	}
}

// chatListEmptyShort is what the list says about itself in one narrow
// column, with the whole sentence in the pane beside it.
func (m Model) chatListEmptyShort() string {
	if m.chatSearch.filtering() && len(m.chatListEntries()) == 0 {
		return noChatsFoundText
	}

	switch m.chatsState {
	case loadStateLoading:
		return "Loading…"
	case loadStateError:
		return "Load failed"
	default:
		return "No chats"
	}
}

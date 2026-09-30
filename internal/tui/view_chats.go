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
		m.chatListHeadingLine(layout, width),
		m.chatListSearchLine(layout, width),
		m.styles().focusRule(width, m.panelFocused(listPane)),
	}

	rows, selected := m.chatListRows(layout, width)
	if len(rows) == 0 {
		return append(lines, m.chatListEmptyLines(layout, width)...)
	}

	budget := height - layout.hintLines() - len(lines)

	start, end := visibleRange(len(rows), selected, budget/m.chatListRowHeight(layout))

	for index := start; index < end; index++ {
		lines = append(lines, rows[index]...)
	}

	return lines
}

// chatListHeadingLine is the first line of the header: the title of the
// pane at its left and how much of the list is unread at its right, both on
// one row.
//
// They are one row because they are two halves of one question — what this
// pane is and what is in it — and because the count is the one number on the
// screen a user scans for: on a row of its own under the title it is a line
// the eye has to find. The title is the panel heading, so it is in the
// accent of the theme when the pane has the keys.
func (m Model) chatListHeadingLine(layout Layout, width int) string {
	// The row of the header is a row of the pane like every other one, so
	// the air inside it is the same air: the title of the pane and the
	// "/ search" under it start at the same column as the name of a chat and
	// its preview, and the count of the header ends at the same column as
	// the time and the badge of a row (the mockup of the owner).
	//
	// The count is fitted to the room the title leaves and not to the width
	// of the row: fitted to the row it is a row longer than the pane, and
	// the pane cuts it with an ellipsis where the rule of the owner has the
	// right edge of the count.
	edge := maxInt(width-layout.ChatListInset(), 1)
	room := maxInt(edge-layout.ChatListInset(), 1)
	title := m.widths.Fit(chatListTitle, maxInt(room/2, 1), ellipsis)
	summary := m.widths.TruncateMarked(
		m.chatListSummaryText(),
		maxInt(room-m.widths.StringWidth(title)-chatListHeadingGap, 1),
		ellipsis,
	)

	return m.painter(theme.Color{}).
		add(m.styles().unstyled(), spaces(layout.ChatListInset())).
		add(m.panelHeadingStyle(m.panelFocused(listPane)), title).
		right(summary, edge, m.styles().dimmed(m.tokens().MutedText)).
		pad(layout.ChatListInset()).
		String()
}

// chatListHeadingGap is the air between the title of the pane and the count
// at the other end of the row, when the count is all the room there is for.
const chatListHeadingGap = 1

// chatListRowHeight returns how many lines one chat of the list takes.
//
// A chat is a name, a preview and the blank line that separates it from the
// next one, so that two chats are two blocks rather than four lines of one
// column. On a screen too short for that (§3.4) the row is one line and the
// preview and the blank line go.
//
// Every chat is the same height, the chosen one included: the chosen chat is
// a card of its two rows of words and the air inside them, and the blank
// line under it is the blank line every other chat has.
func (m Model) chatListRowHeight(layout Layout) int {
	if layout.Short() {
		return 1
	}

	return 3
}

// panelHeading draws the title of a panel: the accent of the theme when
// the panel has the keys, the dim step of the text ramp when it does not.
//
// The colour and the rule under it say the same thing, and that is the
// point: the rule is what a terminal with no colour shows, and the colour
// is what a terminal with colour shows first.
func (m Model) panelHeading(title string, width int, focused bool) string {
	return m.panelHeadingStyle(focused).
		Render(m.widths.Fit(title, width, ellipsis))
}

// panelHeadingStyle is how the title of a panel is written: the accent of
// the theme when the panel has the keys, and the dim step of the text ramp
// when it does not.
func (m Model) panelHeadingStyle(focused bool) lipgloss.Style {
	styles := m.styles()
	if focused {
		return styles.text(m.tokens().Focus).Bold(true)
	}

	return styles.dimmed(m.tokens().MutedText)
}

// chatListSummaryText is the unread count of the header, which is the words
// alone: it shares its row with the title of the pane, and the row that puts
// it at its right edge is the row that fits it to the room the title leaves.
func (m Model) chatListSummaryText() string {
	unread := 0
	for _, chat := range m.chats {
		unread += chat.Unread
	}

	if unread == 0 {
		return "no unread"
	}

	return unreadBadge(unread) + " unread"
}

// chatListSearchLine is how the list says that it can be searched.
//
// §4.1 puts the key in the header rather than only in the hint bar, because
// the header is what a user reads before deciding to do anything at all, and
// a list that can be searched has to say so somewhere that is not behind a
// key they have to know.
func (m Model) chatListSearchLine(layout Layout, width int) string {
	// The row carries the same air inside it as the row above it and as
	// every row of the list under it, so the hint starts at the column the
	// title of the pane starts at.
	inset := layout.ChatListInset()
	room := maxInt(width-2*inset, 1)

	if !layout.TwoPane() {
		if status := m.statusBlockLines(layout, room); len(status) > 0 {
			return m.painter(theme.Color{}).
				add(m.styles().unstyled(), spaces(inset)).
				add(m.statusStyle(), status[0]).
				String()
		}
	}

	return m.painter(theme.Color{}).
		add(m.styles().unstyled(), spaces(inset)).
		add(
			m.styles().dimmed(m.tokens().MutedText),
			m.widths.Fit(chatListSearchHint, room, ellipsis),
		).
		String()
}

// chatListSearchHint is how the list says that it can be searched.
const chatListSearchHint = "/ search"

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

// chatListRowLines renders one chat as two rows of words and a row of
// separation: the name with its time, the preview with its unread badge,
// and either a blank line or half a row of air above and half a row below.
//
// The two rows of words are the two things a chat list is: who it is and
// what it is about. The time and the badge end in the same column, so the
// eye can run down the times without reading any of the names — and the
// badge with them, because a badge that sits wherever the preview of its
// chat happened to end is a column the eye has to find on every row, and a
// column the eye has to find is a column that is not there.
//
// The chosen chat is a card: half a row of the colour of the selection above
// it and half a row below it, which rounds the selection at the top and at
// the bottom without a row of its own — the same drawing as the air around
// a message in the conversation. A chat that is not chosen keeps the blank
// line it had before, so the list holds a quarter more chats than a card on
// every row would leave it, and the two shapes are the same height where
// they meet: a blank line under a card is a blank line, and half a row over
// one is half a row. Nothing is drawn on the rows of the other chats,
// because a half block in the background of the list is a glyph in
// whatever foreground the terminal happens to be in, and the terminal's
// foreground is not a colour the program chose.
//
// Every row keeps two columns of air inside it on each side (one in Narrow)
// and no word of it touches the edge of the pane or of the gap beside it.
// The air is on the background of the row, which for the chosen chat is
// the Selected: the card is a band of colour with words in it, and a band
// that stopped two columns short on each side is a stripe with an outline.
// The time and the count are inside that air too, so the right edge of
// every row of the list is the same column and the two can be read down.
func (m Model) chatListRowLines(
	entry chatListEntry,
	selected bool,
	layout Layout,
	width int,
) []string {
	styles := m.styles()
	chat := entry.chat
	surface := m.selectedSurface(selected)
	// onRow is the background of the whole row, the air inside it included:
	// the Selected of a chosen chat and the background of the list on every
	// other one, so that the first and the last cell of a row are a space
	// on the background of the row rather than a cell the terminal paints
	// with whatever it thinks its own background is.
	background := m.tokens().SidebarBackground
	if selected {
		background = m.tokens().Selected
	}
	onRow := styles.on(background, styles.unstyled())
	// The blank row under a chat is the background of the list whatever
	// else is true of the row above it: it is the gap between two chats,
	// and a gap that changed colour with the selection would be a band
	// under the chosen one.
	separator := styles.on(m.tokens().SidebarBackground, styles.unstyled())
	// inset is the air on each side, content the width of the words
	// between the two of them, and every row of the list is fitted to
	// width, which is content and the four columns of air.
	inset := layout.ChatListInset()
	content := maxInt(width-2*inset, 1)
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
	//
	// What the name is spent out of is the width of the row less the air on
	// each side and less the time and the gap before it. The marker is in
	// the air, so it costs the row nothing: a name cut to a width that
	// does not leave the time's room is a name a column too long, and a row
	// a column over the width of the pane is a row the terminal wraps.
	nameWidth := content
	if nameWidth-timeColumns-timeGapColumns >= minNameBesideTime {
		nameWidth -= timeColumns + timeGapColumns
	} else {
		at = ""
	}

	// The marker is handed to the painter as text and a style and not as a
	// rendered run: Lip Gloss re-reads the sequences of what it is given,
	// and a run that already carries the surface loses it on the way
	// through.
	//
	// It stands in the air inside the row and not in front of it, so the
	// words of every chat start at the column the title of the pane and
	// the "/ search" above them start at (the mockup of the owner): a
	// marker with a column of its own in front of the words puts every
	// name one column to the right of both, and the list looks like it is
	// indented for a reason nobody can find.
	// The marker is a run with a colour of its own inside the air, so it
	// goes through own() with the background of the row written under it:
	// a cell of the row with no background at all is a cell the terminal
	// paints with whatever it thinks its own background is.
	markStyle := styles.on(background, styles.selectionMark(selected, m.focus == FocusChatList))
	lead := func(painted *rowPainter) *rowPainter {
		return painted.
			own(markStyle, mark).
			own(onRow, spaces(inset-selectionMarkerWidth))
	}
	// edge closes a row: the columns of air on the right of it, on the
	// background of the row like the ones on the left, so the right edge of
	// every row of the list is the same column and the times and the counts
	// are both on it.
	edge := func(painted *rowPainter) string {
		return painted.own(onRow, spaces(inset)).String()
	}

	if layout.Short() {
		// On a screen too short for a preview (§3.4) the row is one line:
		// the name and the count, with the count at the right edge of the
		// row where the time would be. The time is the first thing to go
		// (§4.2), because a chat list that has a preview has a time beside
		// it and a count on its own line, and a screen short enough to have
		// neither can keep one number or one timestamp but not both.
		short := maxInt(nameWidth-m.widths.StringWidth(badge.text())-2, 1)

		head := edge(badge.write(
			lead(m.painter(theme.Color{})).
				add(styles.rowText(selected).Bold(true),
					m.widths.TruncateMarked(title, short, ellipsis)),
			width-inset,
		))

		return []string{head}
	}

	head := edge(m.chatTitle(
		lead(m.painter(surface)),
		title,
		selected,
		nameWidth,
	).right(at, width-inset, timeStyle))

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
		content-m.widths.StringWidth(badge.text())-chatListBadgeGap,
		1,
	)
	detail := edge(badge.write(
		lead(m.painter(surface)).
			add(
				styles.text(m.tokens().SecondaryText),
				m.widths.TruncateMarked(
					m.chatListPreview(chat), previewWidth, ellipsis,
				),
			),
		width-inset,
	))

	// The card is the chosen chat and nothing else. A half block is a
	// glyph, so a half block drawn in a colour the program did not choose
	// is a glyph in the terminal's own foreground: on a dark theme that is
	// a light stripe under every chat in the list, and the list is then a
	// list of stripes with words in them.
	// The card of the chosen chat is its two rows of words and the air
	// inside them: full cells, and nothing above or below. A half row of
	// air at each end was tried and is gone — the owner's Terminal draws
	// its rows with a gap, so the halves do not meet the block and the
	// card comes out as three layers of a different shade instead of one
	// shape. The blank row under it is the same blank row every other chat
	// has, so the chosen one is a card in a list of the same density.
	return []string{
		head,
		detail,
		m.painter(theme.Color{}).own(separator, spaces(width)).String(),
	}
}

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

package tui

import (
	"strconv"

	"github.com/charmbracelet/lipgloss"
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

	return 3
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

// chatListRowLines renders one chat as a name with its time, a preview
// with its unread badge, and a blank line under it.
//
// The two lines of a row are the two things a chat list is: who it is and
// what it is about. The time and the badge are at the right edge of the
// rows they belong to, so the eye can run down the times without reading
// any of the names.
func (m Model) chatListRowLines(
	entry chatListEntry,
	selected bool,
	layout Layout,
	width int,
) []string {
	styles := m.styles()
	chat := entry.chat
	surface := m.selectedSurface(selected)

	title := chat.Title
	if title == "" {
		title = untitledChatTitle
	}

	mark := styles.selectionMarker(selected)
	at := chat.Time
	badgeText, badgeStyle := m.chatListUnreadBadge(chat)
	badgeColumns := m.widths.StringWidth(badgeText)
	timeColumns := m.widths.StringWidth(at)
	timeStyle := styles.text(m.tokens().MutedText)

	// §4.2 gives the time up before the name does, on a list too narrow to
	// carry both: a time at the end of a row is worth less than the first
	// few letters of the name it belongs to, and a cut name is a name a
	// user cannot recognise at a glance. The badge and the time are spent
	// out of the width of the name and not added to it.
	nameWidth := width - selectionMarkerWidth
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

	if m.chatListRowHeight(layout) == 1 {
		short := maxInt(nameWidth-badgeColumns-2, 1)

		head := m.painter(surface).
			add(markStyle, markRun).
			add(styles.rowText(selected),
				m.widths.TruncateMarked(title, short, ellipsis)).
			badge(badgeStyle, badgeText).
			right(at, width, timeStyle).
			String()

		return []string{head}
	}

	head := m.chatTitle(
		m.painter(surface).add(markStyle, markRun),
		title,
		selected,
		nameWidth,
	).
		right(at, width, timeStyle).
		String()

	// The badge is spent out of the width of the preview and not added to
	// it: a preview that pushes the badge off the end of the row is a
	// preview that has taken the only number in it.
	previewWidth := maxInt(
		width-selectionMarkerWidth-contentInsetWidth-badgeColumns-timeGapColumns,
		1,
	)
	detail := m.painter(surface).
		add(markStyle, markRun+spaces(contentInsetWidth)).
		add(
			styles.text(m.tokens().SecondaryText),
			m.widths.TruncateMarked(
				m.chatListPreview(chat), previewWidth, ellipsis,
			),
		).
		badge(badgeStyle, badgeText).
		String()

	return []string{head, detail, m.painter(surface).pad(width).String()}
}

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

// chatListUnreadBadge returns the unread count of a chat as a pill and
// the style of it, or nothing when the chat has nothing unread.
//
// The pill is the accent of the theme for a chat with one other person in
// it, and the muted step for a group or a channel: a hundred unread
// messages in a channel is background, and a hundred unread messages from
// one person is not. The text inside is the colour of the surface the pill
// is on, so the number is cut out of the pill rather than written on it.
//
// It is returned as text and a style and not as a rendered run, because
// the row writes it and the row is what knows the surface of the row.
func (m Model) chatListUnreadBadge(chat Chat) (string, lipgloss.Style) {
	if chat.Unread <= 0 {
		return "", m.styles().unstyled()
	}

	background := m.tokens().MutedText
	if !chat.Kind.Grouped() {
		background = m.tokens().Unread
	}

	return " " + unreadBadge(chat.Unread) + " ", m.styles().
		pill(background, m.tokens().SidebarBackground)
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

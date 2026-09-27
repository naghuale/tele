package tui

import (
	"strconv"

	"github.com/charmbracelet/lipgloss"

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
// §3.3 says the list is not squeezed into a narrow screen, and it is the
// one region that has to survive every width: a title line per chat and a
// second line with the unread marker and the preview. The preview is the
// first thing to go on a short screen (§3.4), because a name without a
// preview still says who someone is while a preview without a name says
// nothing about who said it.
func (m Model) chatListLines(layout Layout, width, height int) []string {
	lines := []string{
		m.styles().text(m.tokens().PrimaryText).Render(chatListTitle),
		m.chatListHeaderSecondLine(layout, width),
	}

	rows := m.chatListRows(layout, width)
	if len(rows) == 0 {
		return append(lines, m.chatListEmptyLines(layout, width)...)
	}

	budget := height - layout.hintLines() - len(lines)
	rowHeight := m.chatListRowHeight(layout)

	start, end := visibleRange(len(rows), m.selectedChat, budget/rowHeight)

	for index := start; index < end; index++ {
		lines = append(lines, rows[index]...)
	}

	return lines
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
// status: how much of the list is unread.
//
// §4.1 puts a search and an unread count there. The search is PR-10A.6 and
// is not on the screen yet, so the line says the one half that is true
// today rather than a key that does nothing.
func (m Model) chatListSummaryLine(width int) string {
	unread := 0
	for _, chat := range m.chats {
		unread += chat.Unread
	}

	text := unreadBadge(unread) + " unread"
	if unread == 0 {
		text = "no unread"
	}

	return m.styles().dimmed(m.tokens().MutedText).Render(fitCells(text, width))
}

// chatListRowHeight returns how many lines one chat takes.
func (m Model) chatListRowHeight(layout Layout) int {
	if layout.Short() {
		return 1
	}

	return 2
}

// chatListRows renders every chat of the list into its lines.
func (m Model) chatListRows(layout Layout, width int) [][]string {
	rows := make([][]string, 0, len(m.chats))

	for index, chat := range m.chats {
		rows = append(
			rows,
			m.chatListRowLines(chat, index == m.selectedChat, layout, width),
		)
	}

	return rows
}

// chatListRowLines renders one chat as a title line and a detail line.
func (m Model) chatListRowLines(
	chat Chat,
	selected bool,
	layout Layout,
	width int,
) []string {
	styles := m.styles()

	title := chat.Title
	if title == "" {
		title = "(untitled)"
	}

	// The marker sits against the name and the line below is indented by
	// the same column, which is the shape §4.1 draws: the two lines of a
	// chat stay aligned, and a row that is not selected has the same
	// columns as one that is. The glyph is a chevron and not the focus
	// bar: the two say different things, and a list that marked both with
	// `▌` read as a double line.
	lines := []string{
		styles.selected(selected).Render(
			styles.selectionMark(selected, m.focus == FocusChatList).
				Render(theme.SelectionIndicator(selected)) +
				styles.rowText(selected).Render(
					fitCells(title, width-selectionMarkerWidth),
				),
		),
	}

	if m.chatListRowHeight(layout) == 1 {
		return lines
	}

	detail := m.chatListDetail(chat, layout, width-selectionMarkerWidth)
	lines = append(
		lines,
		styles.dimmed(m.tokens().SecondaryText).
			Render(spaces(selectionMarkerWidth)+detail),
	)

	return lines
}

// chatListDetail returns the second line of a chat: how much of it is
// unread, and what it is about.
//
// The unread marker is the symbol of a queued message from the theme's
// status vocabulary followed by a count, because an unread count is a
// status and §14 says a status is never colour alone.
func (m Model) chatListDetail(
	chat Chat,
	layout Layout,
	width int,
) string {
	unread := chatListUnreadMarker(chat.Unread)
	if layout.Short() || chat.Preview == "" {
		return unread
	}

	available := width - cellWidth(unread) - 2
	if available < 1 {
		return unread
	}

	return unread + "  " + fitCells(chat.Preview, available)
}

// chatListUnreadMarker returns the unread marker of a chat, or "" when
// the chat has nothing unread.
func chatListUnreadMarker(unread int) string {
	if unread <= 0 {
		return ""
	}

	return theme.StatusQueued.Mark().Symbol + " " + unreadBadge(unread)
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
		return []string{style.Render(fitCells(m.chatListEmptyShort(), width))}
	}

	lines := make([]string, 0, len(texts))
	for _, text := range texts {
		for _, line := range wrapCells(text, width) {
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
	// and noMessagesHint is §17 for a chat with nothing in it.
	noChatsText    = "No chats yet"
	noChatsHint    = "Start a new conversation or wait for chats to load."
	noMessagesHint = "Write the first message below."
)

// chatListEmptyState returns the sentences the list is in and the style of
// them.
//
// A load that failed says so in one line and says what to do in the next.
// The reason is not on the screen: a cause can name a file, a TDLib error
// message and a path, and §11.3 and §19 keep those out of a terminal
// somebody is looking at.
func (m Model) chatListEmptyState() ([]string, lipgloss.Style) {
	styles := m.styles()

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
	switch m.chatsState {
	case loadStateLoading:
		return "Loading…"
	case loadStateError:
		return "Load failed"
	default:
		return "No chats"
	}
}

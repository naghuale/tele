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
		m.chatListSummaryLine(width),
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

// chatListSummaryLine is the second line of the header: how much of the
// list is unread.
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
	// chat stay aligned and the marker never costs a column the other
	// chats do not have.
	marker := theme.FocusNone
	if selected {
		marker = theme.FocusBar
	}

	lines := []string{
		styles.selected(selected).Render(
			styles.selectionBar(selected, m.focus == FocusChatList).Render(marker) +
				styles.rowText(selected).Render(fitCells(title, width-1)),
		),
	}

	if m.chatListRowHeight(layout) == 1 {
		return lines
	}

	detail := m.chatListDetail(chat, layout, width)
	lines = append(
		lines,
		styles.dimmed(m.tokens().SecondaryText).Render(detail),
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
	text, style := m.chatListEmptyState()

	if !layout.TwoPane() {
		return wrapCells(text, width)
	}

	return []string{style.Render(fitCells(m.chatListEmptyShort(), width))}
}

// chatListEmptyState returns the whole sentence the list is in and the
// style of it.
func (m Model) chatListEmptyState() (string, lipgloss.Style) {
	styles := m.styles()

	switch m.chatsState {
	case loadStateLoading:
		return "Loading chats...", styles.dimmed(m.tokens().SecondaryText)

	case loadStateError:
		text := "Failed to load chats"
		if m.loadErr != nil {
			text += ": " + m.loadErr.Error()
		}

		return text, styles.dimmed(m.tokens().StatusError)

	case loadStateLoaded:
		return "No chats", styles.dimmed(m.tokens().MutedText)

	default:
		return "No chats", styles.dimmed(m.tokens().MutedText)
	}
}

// chatListEmptyShort is what the list says about itself in one narrow
// column, with the whole sentence in the pane beside it.
func (m Model) chatListEmptyShort() string {
	switch m.chatsState {
	case loadStateLoading:
		return "Loading..."
	case loadStateError:
		return "Load failed"
	default:
		return "No chats"
	}
}

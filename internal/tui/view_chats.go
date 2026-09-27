package tui

import (
	"strconv"

	"telecli/internal/tui/theme"
)

// chatListTitle is the header of the list.
//
// The program and the screen are named in one line, the way the interface
// has always introduced itself: a user who lands on a chat list after
// quitting something else sees what they are in without reading the rows.
// The title is plain text; the surface and the accent around it are what
// make it a header.
const chatListTitle = "telecli — Chats"

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
		"",
	}

	rows := m.chatListRows(layout, width)
	if len(rows) == 0 {
		return append(lines, m.emptyChatListLine(width))
	}

	budget := height - layout.hintLines() - len(lines)
	rowHeight := m.chatListRowHeight(layout)

	start, end := visibleRange(len(rows), m.selectedChat, budget/rowHeight)

	for index := start; index < end; index++ {
		lines = append(lines, rows[index]...)
	}

	return lines
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

	lines := []string{
		styles.selected(selected).Render(fitCells(title, width)),
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

	return theme.StatusQueued.Mark().Symbol + " " + strconv.Itoa(unread)
}

// emptyChatListLine is what the list says when it holds nothing, or when
// the screen is too short for a row.
func (m Model) emptyChatListLine(width int) string {
	if m.chatsState == loadStateLoading {
		return m.styles().
			dimmed(m.tokens().SecondaryText).
			Render(fitCells("Loading chats...", width))
	}

	if m.chatsState == loadStateError {
		text := "Failed to load chats"
		if m.loadErr != nil {
			text += ": " + m.loadErr.Error()
		}

		return m.styles().
			dimmed(m.tokens().StatusError).
			Render(fitCells(text, width))
	}

	return m.styles().
		dimmed(m.tokens().MutedText).
		Render(fitCells("No chats", width))
}

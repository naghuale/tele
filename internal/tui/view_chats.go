package tui

import (
	"fmt"
	"strings"
)

func (m Model) viewChats() string {
	var b strings.Builder

	b.WriteString("telecli — Chats\n")
	b.WriteString(strings.Repeat("-", minInt(maxInt(m.width, minWidth), 60)))
	b.WriteString("\n")

	if len(m.chats) == 0 {
		b.WriteString("(no chats)\n")
	} else {
		available := maxInt(m.height-6, 1)
		start, end := visibleRange(len(m.chats), m.selectedChat, available)
		for i := start; i < end; i++ {
			c := m.chats[i]
			marker := "  "
			if i == m.selectedChat {
				marker = "> "
			}
			line := marker + c.Title
			if c.Unread > 0 {
				line += fmt.Sprintf("  [%d]", c.Unread)
			}
			b.WriteString(truncateRunes(line, m.width))
			b.WriteString("\n")
		}
	}

	b.WriteString(strings.Repeat("-", minInt(maxInt(m.width, minWidth), 60)))
	b.WriteString("\n")
	b.WriteString("↑/↓ j/k move  Home/End  Enter open  Esc quit\n")
	return b.String()
}

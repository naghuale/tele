package tui

import (
	"fmt"
	"strings"
)

func (m Model) viewConversation() string {
	chat := m.selected()

	var b strings.Builder

	title := "telecli — " + chat.Title
	b.WriteString(truncateRunes(title, m.width))
	b.WriteString("\n")
	b.WriteString(strings.Repeat("-", minInt(maxInt(m.width, minWidth), 60)))
	b.WriteString("\n")

	if len(chat.Messages) == 0 {
		b.WriteString("(no messages)\n")
	} else {
		available := maxInt(m.height-8, 1)
		start, end := visibleRange(len(chat.Messages), m.selectedMsg, available)
		for i := start; i < end; i++ {
			msg := chat.Messages[i]
			marker := "  "
			if m.focus == FocusHistory && i == m.selectedMsg {
				marker = "> "
			}
			who := "Peer"
			if msg.Outgoing {
				who = "You"
			}
			line := fmt.Sprintf("%s%s: %s", marker, who, msg.Text)
			b.WriteString(truncateRunes(line, m.width))
			b.WriteString("\n")
		}
	}

	// Progress of the older-page request, below the loaded messages. The
	// messages stay on screen while it runs, and a failure leaves them
	// intact for the next ↓ to retry.
	switch {
	case m.historyMoreLoading:
		b.WriteString("Loading older messages...\n")
	case m.historyMoreErr != nil:
		fmt.Fprintf(&b, "Failed to load older messages: %v\n", m.historyMoreErr)
	}

	// Durable delivery statuses of the open chat, between history and
	// composer. The block is empty in direct mode and reads snapshot state
	// only.
	if statuses := m.viewMessageStatuses(); statuses != "" {
		b.WriteString(statuses)
		b.WriteString("\n")
		b.WriteString(strings.Repeat("-", minInt(maxInt(m.width, minWidth), 60)))
		b.WriteString("\n")
	}

	b.WriteString(strings.Repeat("-", minInt(maxInt(m.width, minWidth), 60)))
	b.WriteString("\n")
	b.WriteString(m.viewComposer())

	switch m.sendState {
	case sendStateSending:
		b.WriteString("Sending...\n")
	case sendStateError:
		fmt.Fprintf(&b, "Failed to send: %v\n", m.sendErr)
	}

	b.WriteString(strings.Repeat("-", minInt(maxInt(m.width, minWidth), 60)))
	b.WriteString("\n")
	b.WriteString("Tab focus  Esc chats  Ctrl+C quit\n")
	return b.String()
}

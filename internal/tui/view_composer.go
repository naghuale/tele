package tui

import "fmt"

func (m Model) viewComposer() string {
	marker := "  "
	if m.focus == FocusComposer {
		marker = "> "
	}
	return fmt.Sprintf("%sComposer: %s\n", marker, string(m.composer))
}

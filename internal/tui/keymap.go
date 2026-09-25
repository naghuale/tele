package tui

import tea "github.com/charmbracelet/bubbletea"

func isUp(msg tea.KeyMsg) bool {
	switch msg.Type {
	case tea.KeyUp:
		return true
	case tea.KeyRunes:
		return len(msg.Runes) == 1 && msg.Runes[0] == 'k'
	}
	return false
}

func isDown(msg tea.KeyMsg) bool {
	switch msg.Type {
	case tea.KeyDown:
		return true
	case tea.KeyRunes:
		return len(msg.Runes) == 1 && msg.Runes[0] == 'j'
	}
	return false
}

func isFirst(msg tea.KeyMsg) bool {
	switch msg.Type {
	case tea.KeyHome:
		return true
	case tea.KeyRunes:
		return len(msg.Runes) == 1 && msg.Runes[0] == 'g'
	}
	return false
}

func isLast(msg tea.KeyMsg) bool {
	switch msg.Type {
	case tea.KeyEnd:
		return true
	case tea.KeyRunes:
		return len(msg.Runes) == 1 && msg.Runes[0] == 'G'
	}
	return false
}

func isTab(msg tea.KeyMsg) bool {
	return msg.Type == tea.KeyTab
}

func isShiftTab(msg tea.KeyMsg) bool {
	return msg.Type == tea.KeyShiftTab
}

func isClearComposer(msg tea.KeyMsg) bool {
	return msg.Type == tea.KeyCtrlU
}

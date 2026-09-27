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

// isPageUp and isPageDown are the two page keys of the timeline (§8.3).
// isReloadChats is the key that repeats a chat list load (§18).
//
// It is a capital R because the chat list has no text field of its own, and
// a capital letter cannot be a character somebody means to type somewhere
// else by accident.
func isReloadChats(msg tea.KeyMsg) bool {
	return msg.Type == tea.KeyRunes && len(msg.Runes) == 1 && msg.Runes[0] == 'R'
}

func isPageUp(msg tea.KeyMsg) bool {
	return msg.Type == tea.KeyPgUp
}

func isPageDown(msg tea.KeyMsg) bool {
	return msg.Type == tea.KeyPgDown
}

// isInsertMode is the second way into the composer (§8.3): Enter for a
// keyboard, `i` for the muscle memory of anyone who came from vi or less.
//
// It is a single letter and it lives in the timeline, which is why §8.4
// says single letters are not commands while the composer has the focus:
// `i` typed into a message has to be the letter.
func isInsertMode(msg tea.KeyMsg) bool {
	return msg.Type == tea.KeyRunes && len(msg.Runes) == 1 && msg.Runes[0] == 'i'
}

func isTab(msg tea.KeyMsg) bool {
	return msg.Type == tea.KeyTab
}

func isShiftTab(msg tea.KeyMsg) bool {
	return msg.Type == tea.KeyShiftTab
}

// isClearComposer, isKillLine and isDeleteWordBefore are the readline keys
// the composer answers (§8.4).
//
// Ctrl+U clears from the start of the line to the cursor and Ctrl+W takes
// the word before it, which is what they do everywhere else. Ctrl+K is the
// other half of the first pair and comes with them: a text field where two
// of the three work and one does not is a field nobody can predict.
func isClearComposer(msg tea.KeyMsg) bool {
	return msg.Type == tea.KeyCtrlU
}

func isKillLine(msg tea.KeyMsg) bool {
	return msg.Type == tea.KeyCtrlK
}

func isDeleteWordBefore(msg tea.KeyMsg) bool {
	return msg.Type == tea.KeyCtrlW
}

// isQuit reports the one single-letter key that leaves the program.
//
// `q` belongs to the chat list and to nothing else. In the composer it is
// a letter, and in the timeline the hint bar does not offer it, so a user
// who types `q` while reading a conversation must not lose their place
// over it. Ctrl+C is the deliberate way out and works everywhere.
func isQuit(msg tea.KeyMsg) bool {
	return msg.Type == tea.KeyRunes && len(msg.Runes) == 1 && msg.Runes[0] == 'q'
}

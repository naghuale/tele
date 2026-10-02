package tui

import tea "github.com/charmbracelet/bubbletea"

// The way out of the program, and the one question it asks.
//
// The owner named the confusion on 02.10: «не всегда понятно, когда нажать
// `q` для выхода, а когда `Esc`». The answer is one rule rather than three
// habits:
//
//   - `Esc` is always one step back — composer to messages, messages to the
//     chat list — and in the chat list it closes nothing. It says so in the
//     status line, because a key that does nothing is a key the user has to
//     try twice to learn;
//   - `q` leaves the program from the chat list and nowhere else. In the
//     composer and in the search it is a letter, and in the messages it is
//     nothing at all, so reading a conversation is never one stray letter
//     away from the end of the session;
//   - `Ctrl+C` leaves from anywhere, and asks first when the composer holds
//     a draft.
//
// The draft is the one thing in this interface a person cannot get back, so
// every way out asks about it rather than only the deliberate one: a draft
// typed in a chat and then left with `Tab` is still on the screen when the
// keys are in the list, and `q` there would take it away without a word.

// quitProgram ends the program.
//
// A chat that is still open when the program quits is closed on the way
// out, or TDLib keeps counting a chat nobody is looking at until the
// process is gone.
func (m Model) quitProgram() (tea.Model, tea.Cmd) {
	m.quitting = true
	m.invalidateMessageStatusPolling()

	return m, tea.Batch(m.closeConversationChat(), tea.Quit)
}

// quitOrAsk leaves the program, asking first about a draft in the
// composer.
//
// The question opens on the answer that changes nothing, the way §12.3
// opens on the record: a user who pressed the key to get rid of something
// has to press it again to mean it. The draft is not thrown away by the
// question and not by walking away from it.
//
// Ctrl+C while the question is open is the second press of the same key and
// is the answer: a person who is holding it down has said what they meant,
// and a question about a key that cannot be dismissed any other way is a
// question nobody can get out of.
func (m Model) quitOrAsk() (tea.Model, tea.Cmd) {
	if m.modal.open && m.modal.kind == modalKindQuit {
		return m.quitProgram()
	}

	if blankDraft(m.composer) {
		return m.quitProgram()
	}

	// A menu under the question would be two questions on one screen: the
	// sheet is closed without an answer, which decides nothing about a
	// message — an unanswered menu is what §13 says Esc means.
	m.closeActionSheet()

	m.modal = confirmModal{open: true, cursor: quitModalStayItem, kind: modalKindQuit}

	return m, nil
}

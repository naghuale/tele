package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

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
//   - `Ctrl+C` leaves from anywhere. It asks in one of two ways, and which
//     one depends on what is at stake: a draft in the composer is a
//     question with its own answer, and nothing at stake is a second press
//     of the same key.
//
// The draft is the one thing in this interface a person cannot get back, so
// every way out asks about it rather than only the deliberate one: a draft
// typed in a chat and then left with `Tab` is still on the screen when the
// keys are in the list, and `q` there would take it away without a word.
//
// The two presses of `Ctrl+C` are the owner's decision of 03.10: «Ctrl+C
// выходит без вопроса» — it is a key a person presses with two hands on the
// keyboard by accident, and it is the key a program with unsent text in it
// must not take as the first half of a word. The first press says what the
// second one does, the second one inside quitConfirmWindow does it, and any
// other key ends the arrangement. `q` is not part of it: a single letter in
// the list is already deliberate, and a question before it would be a
// question about a key nobody presses by accident.

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

// quitOrAsk leaves the program, asking first about a draft in the composer.
//
// It is the way out of `q` in the chat list — a single letter in a list of
// words is deliberate, and what it needs to hear about is a draft rather
// than the key itself.
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

	// The second press of Ctrl+C has no place here: the question is about a
	// draft, and a notice under it would be a sentence about a screen that
	// is not on the screen any more.
	m = m.disarmQuit()

	// A menu under the question would be two questions on one screen: the
	// sheet is closed without an answer, which decides nothing about a
	// message — an unanswered menu is what §13 says Esc means.
	m.closeActionSheet()

	m.modal = confirmModal{open: true, cursor: quitModalStayItem, kind: modalKindQuit}

	return m, nil
}

// quitConfirmWindow is how long the first Ctrl+C arms the second one.
//
// Two seconds is long enough for a person who means it and short enough
// that a key pressed by accident an hour later is not the second half of a
// word. It is a window rather than a counter because the two presses have
// to be close together in time and nothing to do with each other.
const quitConfirmWindow = 2 * time.Second

// quitArmExpiredMsg takes the second press of Ctrl+C away when its window is
// over.
type quitArmExpiredMsg struct{}

// quitAfterAKeyOrTwo is the way out of Ctrl+C: the first press arms the
// second one and says what it does, the second one inside
// quitConfirmWindow leaves the program.
//
// A draft is a question of its own and not an arm: a draft cannot be taken
// back, so the way out asks about it in words instead of counting presses.
//
// The question about a draft is also the answer here: Ctrl+C while it is
// open is the second press of the key the person is holding down.
func (m Model) quitAfterAKeyOrTwo() (tea.Model, tea.Cmd) {
	if m.modal.open && m.modal.kind == modalKindQuit {
		return m.quitProgram()
	}

	if !blankDraft(m.composer) {
		return m.quitOrAsk()
	}

	if !m.quitArmed {
		m.quitArmed = true

		withNotice, noticeCmd := m.withNoticeCleared(noticeQuitAgain)

		return withNotice, tea.Batch(noticeCmd, tea.Tick(
			quitConfirmWindow,
			func(time.Time) tea.Msg { return quitArmExpiredMsg{} },
		))
	}

	return m.quitProgram()
}

// disarmQuit takes the arm away and the sentence with it: the notice says
// what the next press will do, and after any other key there is no next
// press to mean.
func (m Model) disarmQuit() Model {
	m.quitArmed = false
	m.notice = ""

	return m
}

// updateQuitArmExpired takes the arm away when its window is over.
func (m Model) updateQuitArmExpired(quitArmExpiredMsg) Model {
	return m.disarmQuit()
}

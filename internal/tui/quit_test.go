package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// The way out of the program is one rule and not three habits, because the
// owner named the confusion on 02.10: «не всегда понятно, когда нажать `q`
// для выхода, а когда `Esc`».
//
//   - Esc is one step back, and it stops in the chat list;
//   - q leaves from the chat list and nowhere else;
//   - Ctrl+C leaves from anywhere.
//
// Each of these is a test of one half of that sentence, and the draft is
// the thing the question is about.

// Esc in the chat list closes nothing and says which key does close the
// program. The notice is what a user reads after pressing the key that
// looked like the way out and was not, and it is put in the status line
// rather than in the hint bar, which says the same thing before the key is
// pressed.
func TestEscapeInTheChatListSaysWhichKeyQuits(t *testing.T) {
	m := sizedModel(t, 120, 30)

	m, cmd := updateModel(t, m, press(tea.KeyEsc))

	if m.quitting {
		t.Fatal("Esc left the program")
	}
	if m.screen != ScreenChats {
		t.Fatalf("screen = %v, want the list", m.screen)
	}
	if m.notice != noticeQuitKey {
		t.Fatalf("notice = %q, want %q", m.notice, noticeQuitKey)
	}
	if !strings.Contains(plain(m.View()), noticeQuitKey) {
		t.Fatalf("the notice is not on the screen:\n%s", plain(m.View()))
	}
	for _, msg := range flattenOne(t, cmd) {
		if _, isQuit := msg.(tea.QuitMsg); isQuit {
			t.Fatal("Esc in the list quit the program")
		}
	}
}

// Esc walks back one step at a time and never further: the composer hands
// the keys to the messages, the messages to the list beside them, and the
// list stops. A key that sometimes quits and sometimes does not is a key
// nobody trusts.
func TestEscapeWalksBackOneStepAtATime(t *testing.T) {
	m := openedModel(t, 120, 30)

	m, _ = updateModel(t, m, press(tea.KeyEsc))
	if m.focus != FocusHistory {
		t.Fatalf("after one Esc: focus = %v, want the messages", m.focus)
	}

	m, _ = updateModel(t, m, press(tea.KeyEsc))
	if m.focus != FocusChatList {
		t.Fatalf("after two Esc: focus = %v, want the chat list", m.focus)
	}

	m, _ = updateModel(t, m, press(tea.KeyEsc))
	if m.screen != ScreenConversation {
		t.Fatalf("screen = %v, want the conversation beside the list", m.screen)
	}
	if m.quitting {
		t.Fatal("the third Esc left the program")
	}
}

// q leaves from the chat list, and from nowhere else. It is a letter in the
// composer, a letter in the search and nothing at all in the messages, so a
// conversation cannot be lost over one letter and a query cannot be cut
// short by one.
func TestQQuitsOnlyFromTheChatList(t *testing.T) {
	t.Run("the chat list", func(t *testing.T) {
		m := sizedModel(t, 120, 30)

		updated, cmd := m.Update(pressRunes("q"))
		assertQuit(t, cmd)

		if !updated.(Model).quitting {
			t.Fatal("q left the program with the keys in the list and it is still running")
		}
	})

	t.Run("the composer", func(t *testing.T) {
		m := openedModel(t, 120, 30)

		updated, cmd := m.Update(pressRunes("q"))
		if isQuitMessage(t, cmd) {
			t.Fatal("q left the program from the composer")
		}

		next := updated.(Model)
		if next.quitting {
			t.Fatal("q left the program from the composer")
		}
		if next.Composer() != "q" {
			t.Fatalf("composer = %q, want the letter", next.Composer())
		}
	})

	t.Run("the search", func(t *testing.T) {
		m := typing(t, sizedModel(t, 120, 30), "dev")

		updated, cmd := m.Update(pressRunes("q"))
		if isQuitMessage(t, cmd) {
			t.Fatal("q left the program from the search")
		}

		next := updated.(Model)
		if next.quitting {
			t.Fatal("q left the program from the search")
		}
		if got := string(next.chatSearch.query); got != "devq" {
			t.Fatalf("query = %q, want the letter in it", got)
		}
	})

	t.Run("the messages", func(t *testing.T) {
		m := focusedOn(openedModel(t, 120, 30), FocusHistory)

		updated, cmd := m.Update(pressRunes("q"))
		if isQuitMessage(t, cmd) {
			t.Fatal("q left the program from the messages")
		}

		// There is no field here to type a q into, and a reader of a
		// conversation must not lose their place over one letter.
		next := updated.(Model)
		if next.quitting {
			t.Fatal("q left the program from the messages")
		}
		if next.focus != FocusHistory {
			t.Fatalf("focus = %v, want the messages", next.focus)
		}
	})
}

// Ctrl+C leaves from every place, and it is the only key that does. A
// deliberate way out that works whatever the keys are in is what makes a
// program with text fields safe to sit in front of.
func TestCtrlCQuitsFromEveryPlace(t *testing.T) {
	places := map[string]Model{
		"the chat list": sizedModel(t, 120, 30),
		"the composer":  openedModel(t, 120, 30),
		"the messages":  focusedOn(openedModel(t, 120, 30), FocusHistory),
		"the search":    typing(t, sizedModel(t, 120, 30), "dev"),
	}

	for name, m := range places {
		t.Run(name, func(t *testing.T) {
			_, cmd := m.Update(press(tea.KeyCtrlC))
			assertQuit(t, cmd)
		})
	}
}

// A draft is the one thing in this interface a person cannot get back, so
// every way out asks about it rather than only the deliberate one: a draft
// typed in a chat and then walked away from with Tab is still on the screen
// when the keys are in the list, and q there would take it away silently.
func TestAWayOutWithADraftAsksBeforeTakingIt(t *testing.T) {
	cases := map[string]tea.KeyMsg{
		"Ctrl+C":        press(tea.KeyCtrlC),
		"q in the list": pressRunes("q"),
	}

	for name, key := range cases {
		t.Run(name, func(t *testing.T) {
			m := focusedOn(openedModel(t, 120, 30), FocusComposer)
			m, _ = updateModel(t, m, pressRunes("черновик"))
			m = focusedOn(m, FocusChatList)

			m, _ = updateModel(t, m, key)

			if !m.modal.open || m.modal.kind != modalKindQuit {
				t.Fatalf("the way out did not ask about the draft: modal = %+v", m.modal)
			}
			if m.quitting {
				t.Fatal("the program left with a draft in the composer")
			}
			if m.Composer() != "черновик" {
				t.Fatalf("composer = %q, want the draft untouched", m.Composer())
			}

			// The question opens on the answer that changes nothing, the
			// way §12.3 opens on the record.
			if m.modal.cursor != quitModalStayItem {
				t.Fatalf("cursor = %d, want the answer that stays %d", m.modal.cursor, quitModalStayItem)
			}
			view := plain(m.View())
			if !strings.Contains(view, quitModalTitle) {
				t.Fatalf("the question is not on the screen:\n%s", view)
			}
		})
	}
}

// Leaving the question alone is not a decision: Esc answers it with the
// answer that changes nothing, and the draft and the program are both
// exactly where they were.
func TestLeavingTheQuestionAboutADraftKeepsIt(t *testing.T) {
	m := focusedOn(openedModel(t, 120, 30), FocusComposer)
	m, _ = updateModel(t, m, pressRunes("черновик"))
	m, _ = updateModel(t, m, press(tea.KeyCtrlC))

	m, cmd := updateModel(t, m, press(tea.KeyEsc))

	if m.modal.open {
		t.Fatal("Esc did not close the question")
	}
	if m.quitting {
		t.Fatal("Esc left the program")
	}
	if m.Composer() != "черновик" {
		t.Fatalf("composer = %q, want the draft", m.Composer())
	}
	for _, msg := range flattenOne(t, cmd) {
		if _, isQuit := msg.(tea.QuitMsg); isQuit {
			t.Fatal("Esc in the question quit the program")
		}
	}
}

// Answering the question with the answer that quits is the way out, and it
// is the only answer that ends the program.
func TestAnsweringTheQuestionAboutADraftQuits(t *testing.T) {
	m := focusedOn(openedModel(t, 120, 30), FocusComposer)
	m, _ = updateModel(t, m, pressRunes("черновик"))
	m, _ = updateModel(t, m, press(tea.KeyCtrlC))

	m, _ = updateModel(t, m, press(tea.KeyUp))

	m, cmd := updateModel(t, m, press(tea.KeyEnter))
	assertQuit(t, cmd)

	if !m.quitting {
		t.Fatal("the program is still running")
	}
	if got := m.View(); got != "" {
		t.Fatalf("the screen is not empty after quitting: %q", got)
	}
}

// A draft of spaces is not a draft: there is nothing to lose, and a
// question about nothing is a question the user has to answer to leave.
func TestAWayOutWithABlankDraftDoesNotAsk(t *testing.T) {
	m := openedModel(t, 120, 30)
	m, _ = updateModel(t, m, pressRunes(" "))

	updated, cmd := m.Update(press(tea.KeyCtrlC))

	if updated.(Model).modal.open {
		t.Fatal("a blank draft was worth a question")
	}
	assertQuit(t, cmd)
}

// Ctrl+C while the question is open is the second press of the key and is
// the answer: a person holding it down has said what they meant, and a
// question about the only key that leaves the program must not be a place
// the program cannot be left from.
func TestCtrlCInTheQuestionAboutADraftQuits(t *testing.T) {
	m := focusedOn(openedModel(t, 120, 30), FocusComposer)
	m, _ = updateModel(t, m, pressRunes("черновик"))
	m, _ = updateModel(t, m, press(tea.KeyCtrlC))

	m, cmd := updateModel(t, m, press(tea.KeyCtrlC))

	assertQuit(t, cmd)
	if !m.quitting {
		t.Fatal("the program is still running")
	}
}

// A blank Enter lights the placeholder and nothing else, and the question
// about a draft is a question of §12.3's shape: it has its own words, its
// own answers and its own key, and it must not disturb the one that asks
// about an uncertain delivery.
func TestTheQuestionAboutADraftIsNotTheQuestionAboutAnUncertainDelivery(t *testing.T) {
	m := focusedOn(openedModel(t, 120, 30), FocusComposer)
	m, _ = updateModel(t, m, pressRunes("черновик"))
	m, _ = updateModel(t, m, press(tea.KeyCtrlC))

	uncertain := theModalQuestion
	if m.modal.question().title == uncertain.title {
		t.Fatal("the question about a draft is the question about a duplicate")
	}

	// The question of §12.3 still walks its own three answers.
	m.modal = confirmModal{open: true, cursor: modalKeepUncertain}
	m, _ = updateModel(t, m, press(tea.KeyUp))
	if m.modal.cursor != modalCreateCopyItem {
		t.Fatalf("cursor = %d, want %d", m.modal.cursor, modalCreateCopyItem)
	}
	m, _ = updateModel(t, m, press(tea.KeyDown))
	if m.modal.cursor != modalKeepUncertain {
		t.Fatalf("cursor = %d, want %d", m.modal.cursor, modalKeepUncertain)
	}
	m, _ = updateModel(t, m, press(tea.KeyDown))
	if m.modal.cursor != modalCancelItem {
		t.Fatalf("cursor = %d, want %d", m.modal.cursor, modalCancelItem)
	}
}

// The question about a draft is a question of §12.3's shape, and it must
// not be asked on top of the other one: the menu under it would be two
// questions on one screen, and a menu nobody answered decides nothing about
// a message, which is what §13 says Esc means.
func TestTheQuestionAboutADraftClosesAMenuWithoutAnAnswer(t *testing.T) {
	m := openedModel(t, 120, 30)
	m, _ = updateModel(t, m, pressRunes("черновик"))
	if m.Composer() != "черновик" {
		t.Fatalf("composer = %q, want the draft", m.Composer())
	}

	// Back into the messages and open the menu of §13 over one of them.
	m = focusedOn(m, FocusHistory)
	m, _ = updateModel(t, m, pressRunes("a"))
	if !m.actionSheet.open {
		t.Fatal("the menu did not open")
	}

	m, _ = updateModel(t, m, press(tea.KeyCtrlC))

	if m.actionSheet.open {
		t.Fatal("the menu is still open under the question")
	}
	if !m.modal.open || m.modal.kind != modalKindQuit {
		t.Fatalf("the question is not the one about the draft: modal = %+v", m.modal)
	}
	if m.Composer() != "черновик" {
		t.Fatalf("composer = %q, want the draft untouched", m.Composer())
	}
}

// isQuitMessage reports whether a command is the end of the program.
//
// The command is asked through flattenOne, which runs it on its own
// goroutine and drops a timer rather than waiting for it: the notice timer
// is three seconds out and a test is not a place to wait for one.
func isQuitMessage(t *testing.T, cmd tea.Cmd) bool {
	t.Helper()

	for _, msg := range flattenOne(t, cmd) {
		if _, isQuit := msg.(tea.QuitMsg); isQuit {
			return true
		}
	}

	return false
}

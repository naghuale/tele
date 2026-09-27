package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// pausedError stands in for the reason the composition root resolved. The
// tui package imports nothing, so it receives finished text.
var errSendingPausedFixture = errors.New(
	"Sending paused\n" +
		"Sending is paused. Your message was not sent and is still here.\n" +
		"telecli could not open its secure message queue, which keeps unsent\n" +
		"messages safe if the app closes.\n" +
		"Unlock your Keychain or allow telecli access, then restart telecli.\n" +
		"Details: https://github.com/naghuale/tele/blob/main/docs/help/sending-paused.md",
)

// h17CountingSubmitter records what reached the delivery path.
type h17CountingSubmitter struct {
	calls int
}

func (s *h17CountingSubmitter) SubmitMessage(
	context.Context,
	int64,
	string,
) (Submission, error) {
	s.calls++
	return Submission{}, errors.New("must not be called")
}

func h17PausedModel(
	t *testing.T,
	submitter ComposerSubmitter,
) Model {
	t.Helper()

	m, err := NewModelWithDependencies(
		context.Background(),
		Dependencies{
			Source:           &fakeChatSource{chats: []Chat{{ID: 7, Title: "A"}}},
			MessageSubmitter: submitter,
			SendError:        errSendingPausedFixture,
		},
	)
	if err != nil {
		t.Fatalf("NewModelWithDependencies() error = %v", err)
	}
	m.width, m.height = 80, 24

	// Reach the conversation screen, where the send-error slot lives.
	m, _ = updateModel(t, m, chatsLoadedMsg{chats: []Chat{{ID: 7, Title: "A"}}})
	m, _ = updateModel(t, m, press(tea.KeyEnter))
	if m.screen != ScreenConversation {
		t.Fatalf("screen = %v, want conversation", m.screen)
	}
	// Settle the first history load so the view is the conversation. The
	// operation must match the one the request was issued under.
	m, _ = updateModel(t, m, historyLoadedMsg{
		chatID:    7,
		operation: m.historyOperation,
		page: HistoryPage{
			Messages: []Message{{ID: 1, Text: "hi"}},
			NextFrom: 1,
		},
	})
	return m
}

// The reason is on screen before the user has pressed anything, so they
// are not invited to press Enter to discover it.
func TestPausedSendErrorIsVisibleImmediately(t *testing.T) {
	m := h17PausedModel(t, &h17CountingSubmitter{})

	if m.sendState != sendStateError {
		t.Fatalf("sendState = %v, want error", m.sendState)
	}
	view := m.View()
	for _, want := range []string{
		"Sending paused",
		"Your message was not sent and is still here",
		"Unlock your Keychain",
		"Details: ",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("view is missing %q:\n%s", want, view)
		}
	}
}

// Enter must go through the submitter, which is the only delivery path.
// It must never bypass it: a message that cannot be queued must not be
// sent by another route.
func TestPausedEnterUsesTheSubmitterAndNothingElse(t *testing.T) {
	source := &fakeChatSource{chats: []Chat{{ID: 7, Title: "A"}}}
	submitter := &h17CountingSubmitter{}
	m := h17PausedModel(t, submitter)
	m.source = source
	m.focus = FocusComposer
	m.composer = []rune("unsent draft")

	updated, cmd := m.Update(press(tea.KeyEnter))
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("Enter must return a command to attempt the submission")
	}

	msg := cmd()
	m, _ = updateModel(t, m, msg)

	if submitter.calls != 1 {
		t.Fatalf("submitter calls = %d, want 1: the submitter is the only path",
			submitter.calls)
	}
	if source.sendCall.called {
		t.Fatal("the model bypassed the submitter and called SendMessage")
	}
}

// The draft must survive a refused send, and focus must stay in the
// composer so the text can be copied out.
func TestPausedRefusalPreservesDraftAndFocus(t *testing.T) {
	submitter := &h17CountingSubmitter{}
	m := h17PausedModel(t, submitter)
	m.focus = FocusComposer
	m.composer = []rune("unsent draft")

	updated, cmd := m.Update(press(tea.KeyEnter))
	m = updated.(Model)
	m, _ = updateModel(t, m, cmd())

	if got := m.Composer(); got != "unsent draft" {
		t.Fatalf("Composer() = %q, want the draft to survive", got)
	}
	if m.focus != FocusComposer {
		t.Fatalf("focus = %v, want composer", m.focus)
	}
	if m.sendState != sendStateError {
		t.Fatalf("sendState = %v, want error so the reason stays visible",
			m.sendState)
	}
}

// Nothing in this state may reach a direct sendMessage path.
func TestPausedStateNeverFallsBackToDirectSend(t *testing.T) {
	source := &fakeChatSource{}
	m := h17PausedModel(t, &h17CountingSubmitter{})
	m.focus = FocusComposer
	m.composer = []rune("unsent draft")

	updated, cmd := m.Update(press(tea.KeyEnter))
	m = updated.(Model)
	m, _ = updateModel(t, m, cmd())

	if source.sendCall.called {
		t.Fatal("the model fell back to ChatSource.SendMessage")
	}
}

// Nothing was sent, so the screen must not claim a send failed.
func TestPausedViewDoesNotSaySendFailed(t *testing.T) {
	m := h17PausedModel(t, &h17CountingSubmitter{})

	view := m.View()
	if strings.Contains(view, "Failed to send") {
		t.Fatalf("a paused composer must not be labelled a failed send:\n%s",
			view)
	}
	if !strings.Contains(view, "Sending paused") {
		t.Fatalf("view is missing the paused headline:\n%s", view)
	}
}

// Ctrl+U clears the draft, not the reason. The condition is still true.
func TestPausedReasonSurvivesClearingTheDraft(t *testing.T) {
	m := h17PausedModel(t, &h17CountingSubmitter{})
	m.focus = FocusComposer
	m.composer = []rune("unsent draft")

	if m.sendState != sendStateError {
		t.Fatalf("sendState = %v, want error", m.sendState)
	}

	m, _ = updateModel(t, m, press(tea.KeyCtrlU))

	if got := m.Composer(); got != "" {
		t.Fatalf("Composer() = %q, want the draft cleared", got)
	}
	if m.sendState != sendStateError {
		t.Fatalf("sendState = %v, want error: the reason still holds",
			m.sendState)
	}
	if !errors.Is(m.sendErr, errSendingPausedFixture) {
		t.Fatalf("sendErr = %v, want the paused reason", m.sendErr)
	}

	view := m.View()
	if !strings.Contains(view, "Sending paused") {
		t.Fatalf("the reason disappeared after Ctrl+U:\n%s", view)
	}
}

// A real failed send keeps the "Failed to send" wording: the paused case
// must not swallow it.
func TestFailedSendStillSaysFailedToSend(t *testing.T) {
	m := h17PausedModel(t, &h17CountingSubmitter{})
	m.pausedErr = nil
	m.sendErr = errors.New("network is down")
	m.sendState = sendStateError

	view := m.View()
	if !strings.Contains(view, "Failed to send: network is down") {
		t.Fatalf("a genuine failure lost its wording:\n%s", view)
	}
}

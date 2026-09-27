package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"telecli/internal/tui/theme"
)

// §12.1: a message that could not be queued is told so in words the user
// can act on, and the cause goes to the log.

func TestAMessageThatWasNotQueuedSaysTheSpecWords(t *testing.T) {
	model := modelAfterFailedSubmission(t, &recordingSubmitter{
		err: errors.New("queue message: the outbox is closed"),
	}, nil)

	view := plain(model.View())
	if !strings.Contains(view, "Message was not queued") {
		t.Fatalf("the screen does not say the message was not queued: %q", viewLines(view))
	}
	if !strings.Contains(view, "Your text is still in the composer") {
		t.Fatalf("the screen does not say the text is safe: %q", viewLines(view))
	}
}

// §7.2: the draft stays, and the composer keeps the keys. A user who has
// lost what they wrote because a queue was closed will not press Enter
// again.
func TestTheDraftStaysAfterAQueueFailure(t *testing.T) {
	model := modelAfterFailedSubmission(t, &recordingSubmitter{
		err: errors.New("queue message: the outbox is closed"),
	}, nil)

	if got := string(model.composer); got != "текст сообщения" {
		t.Fatalf("composer = %q, want the draft", got)
	}
	if model.focus != FocusComposer {
		t.Fatalf("focus = %v, want the composer", model.focus)
	}
}

// The cause of a failed queueing can name a keychain service, a file path
// and a TDLib error message. It goes to the diagnostic stream, and the
// message the user wrote does not go anywhere at all.
func TestTheQueueFailureCauseIsNotOnScreen(t *testing.T) {
	cause := errors.New(
		"queue message: ERROR 400: api_hash 0123456789abcdef " +
			"/Users/me/Library/Application Support/telecli/outbox.db " +
			"keychain: TelegramQueue",
	)
	log := &recordingWriter{}
	model := modelAfterFailedSubmission(t, &recordingSubmitter{err: cause}, log)

	view := plain(model.View())
	for _, secret := range []string{
		"api_hash 0123456789abcdef",
		"outbox.db",
		"keychain: TelegramQueue",
		"ERROR 400",
	} {
		if strings.Contains(view, secret) {
			t.Fatalf("the screen carries %q", secret)
		}
	}
	if strings.Contains(log.String(), "текст сообщения") {
		t.Fatalf("the diagnostics carry the message: %q", log.String())
	}
	if !strings.Contains(log.String(), "outbox.db") {
		t.Fatalf("diagnostics = %q, want the cause", log.String())
	}
}

// §24: the inline error is raised on its own block and not across the
// pane. A band the width of the composer would say that the whole composer
// had failed, which is a different and a louder claim.
func TestTheInlineErrorIsRaisedOnlyUnderItsOwnText(t *testing.T) {
	model := modelAfterFailedSubmission(
		t,
		&recordingSubmitter{err: errors.New("queue message: closed")},
		nil,
		theme.ProfileTrueColor,
	)

	lines := model.sendStateLines(100)
	if len(lines) == 0 {
		t.Fatal("the failed send says nothing")
	}

	for _, line := range lines {
		text := strings.TrimRight(plain(line), " ")
		if text != notQueuedText {
			continue
		}
		if text != "Message was not queued" {
			t.Fatalf("the error line is %q, want the sentence alone", text)
		}
		if !hasBackground(line) {
			t.Fatal("the error block is not on a raised background")
		}
		return
	}

	t.Fatalf("the error sentence is not in %q", lines)
}

// A queue failure in direct delivery, where there is no queue, is the
// same sentence: the interface does not have two ways of saying that
// nothing was queued.
func TestEverySubmissionFailureSaysTheSameWords(t *testing.T) {
	for name, submitter := range map[string]*recordingSubmitter{
		"queue closed":     {err: errors.New("queue message: closed")},
		"delivery missing": {err: errors.New("message delivery runtime is unavailable")},
	} {
		t.Run(name, func(t *testing.T) {
			model := modelAfterFailedSubmission(t, submitter, nil)
			for _, line := range model.sendStateLines(100) {
				if strings.Contains(plain(line), "Failed to send") {
					t.Fatalf("the screen says %q", plain(line))
				}
			}
		})
	}
}

// ---- helpers ----

// modelAfterFailedSubmission is a model with a draft the queue refused.
func modelAfterFailedSubmission(
	t *testing.T,
	submitter *recordingSubmitter,
	diagnostics *recordingWriter,
	profiles ...theme.Profile,
) Model {
	t.Helper()

	profile := theme.ProfileNoColor
	if len(profiles) > 0 {
		profile = profiles[0]
	}

	deps := Dependencies{
		Source:           &fakeChatSource{chats: []Chat{{ID: 7, Title: "A"}}},
		MessageSubmitter: submitter,
		Theme:            theme.DefaultTheme().ForProfile(profile),
		ColorProfile:     profile,
	}
	if diagnostics != nil {
		deps.Diagnostics = diagnostics
	}

	model, err := NewModelWithDependencies(context.Background(), deps)
	if err != nil {
		t.Fatalf("NewModelWithDependencies: %v", err)
	}

	model, _ = updateModel(t, model, tea.WindowSizeMsg{Width: 100, Height: 24})
	model, _ = updateModel(t, model, chatsLoadedMsg{
		chats: []Chat{{ID: 7, Title: "A"}},
	})
	model, _ = updateModel(t, model, press(tea.KeyEnter))
	model.focus = FocusComposer
	model, _ = updateModel(t, model, pressRunes("текст сообщения"))
	model, cmd := updateModel(t, model, press(tea.KeyEnter))
	if cmd == nil {
		t.Fatal("Enter did not submit anything")
	}
	model, _ = updateModel(t, model, cmd())

	if model.sendState != sendStateError {
		t.Fatalf("sendState = %v, want the error state", model.sendState)
	}

	return model
}

// hasBackground reports whether a rendered string carries a background
// colour.
func hasBackground(rendered string) bool {
	return strings.Contains(rendered, "48;")
}

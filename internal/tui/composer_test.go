package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"telecli/internal/tui/theme"
)

// A composer with a cursor in it.
//
// The text is a run of runes and the cursor is an index into it, so a
// cursor in the middle of a word is a position and not a flag. The keys
// move over grapheme clusters rather than over code points: a ZWJ emoji is
// one thing to the person reading it and seven code points to a buffer,
// and a Backspace that leaves half a family behind is a bug a user sees
// immediately.

// typedModel returns a model with a conversation open and the focus in the
// composer, with the given draft already typed.
func typedModel(t *testing.T, text string) Model {
	t.Helper()

	m := openedProgramModel(t, theme.ProfileNoColor, 100, 24)
	m, _ = updateModel(t, m, pressRunes(text))
	m.focus = FocusComposer

	return m
}

// narrowTypedModel is typedModel on a screen with one region, where the
// focus has nowhere to go but the timeline and the composer.
func narrowTypedModel(t *testing.T, text string) Model {
	t.Helper()

	m := openedProgramModel(t, theme.ProfileNoColor, 60, 24)
	m, _ = updateModel(t, m, pressRunes(text))
	m.focus = FocusComposer

	return m
}

// setDraft puts a draft into a model with the cursor at its end, which is
// where the cursor of a typed draft is.
func setDraft(m *Model, text string) {
	m.composer = []rune(text)
	m.composerCursor = len(m.composer)
}

// cursorOf returns the cursor position of a model, for readable failures.
func cursorOf(m Model) int {
	return m.composerCursor
}

// The cursor starts at the end of whatever was typed, which is where a
// person typing expects to be.
func TestCursorStartsAtTheEndOfTheDraft(t *testing.T) {
	m := typedModel(t, "привет")

	if m.Composer() != "привет" {
		t.Fatalf("composer = %q", m.Composer())
	}
	if got := cursorOf(m); got != 6 {
		t.Fatalf("cursor = %d, want 6", got)
	}
}

// A rune goes in where the cursor is, not at the end of the draft.
func TestRunesAreInsertedAtTheCursor(t *testing.T) {
	m := typedModel(t, "helo")
	m, _ = updateModel(t, m, press(tea.KeyLeft))
	m, _ = updateModel(t, m, pressRunes("l"))

	if m.Composer() != "hello" {
		t.Fatalf("composer = %q, want %q", m.Composer(), "hello")
	}
	if got := cursorOf(m); got != 4 {
		t.Fatalf("cursor = %d, want 4", got)
	}
}

// Backspace removes the grapheme before the cursor, wherever the cursor is.
func TestBackspaceRemovesTheGraphemeBeforeTheCursor(t *testing.T) {
	cases := map[string]struct {
		text   string
		left   int
		want   string
		cursor int
	}{
		"middle of ascii":    {text: "hello", left: 3, want: "hllo", cursor: 1},
		"middle of cyrillic": {text: "привет", left: 4, want: "пивет", cursor: 1},
		// One emoji, five code points: Backspace takes the whole of it and
		// not its last piece.
		"zwj emoji": {
			text:   "a👨‍👩‍👧b",
			left:   1,
			want:   "ab",
			cursor: 1,
		},
		"at the start": {text: "abc", left: 3, want: "abc", cursor: 0},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			m := typedModel(t, testCase.text)
			for range testCase.left {
				m, _ = updateModel(t, m, press(tea.KeyLeft))
			}

			m, _ = updateModel(t, m, press(tea.KeyBackspace))

			if m.Composer() != testCase.want {
				t.Fatalf("composer = %q, want %q", m.Composer(), testCase.want)
			}
			if got := cursorOf(m); got != testCase.cursor {
				t.Fatalf("cursor = %d, want %d", got, testCase.cursor)
			}
		})
	}
}

// A flag is one grapheme, not the base letter and the combining mark.
func TestBackspaceKeepsCombiningMarksWithTheirLetter(t *testing.T) {
	// "e" and a combining acute accent: two code points, one thing on the
	// screen, and one thing to delete.
	const decomposed = "e\u0301x"

	m := typedModel(t, decomposed)
	m, _ = updateModel(t, m, press(tea.KeyBackspace))

	if m.Composer() != "e\u0301" {
		t.Fatalf("composer = %q, want the letter with its accent", m.Composer())
	}

	m, _ = updateModel(t, m, press(tea.KeyBackspace))

	if m.Composer() != "" {
		t.Fatalf("composer = %q, want empty: the whole cluster goes", m.Composer())
	}
}

// Delete removes the grapheme after the cursor.
func TestDeleteRemovesTheGraphemeAfterTheCursor(t *testing.T) {
	m := typedModel(t, "abc")
	m, _ = updateModel(t, m, press(tea.KeyHome))
	m, _ = updateModel(t, m, press(tea.KeyDelete))

	if m.Composer() != "bc" {
		t.Fatalf("composer = %q, want %q", m.Composer(), "bc")
	}
	if got := cursorOf(m); got != 0 {
		t.Fatalf("cursor = %d, want 0", got)
	}
}

// Home and End go to the ends of the line the cursor is on, and Ctrl+A and
// Ctrl+E are the readline names for the same two places.
func TestHomeAndEndGoToTheEndsOfTheLine(t *testing.T) {
	// The cursor starts at the end of the second line, so Home is the start
	// of the second one and not of the whole draft: Home and End are the
	// ends of a line, as they are in every text field.
	for name, key := range map[string]tea.KeyMsg{
		"home":   {Type: tea.KeyHome},
		"ctrl+a": {Type: tea.KeyCtrlA},
	} {
		t.Run(name, func(t *testing.T) {
			m := typedModel(t, "one\ntwo")
			m, _ = updateModel(t, m, key)
			if got := cursorOf(m); got != 4 {
				t.Fatalf("cursor = %d, want 4 (the start of the second line)", got)
			}

			m, _ = updateModel(t, m, press(tea.KeyUp))
			if got := cursorOf(m); got != 0 {
				t.Fatalf("cursor = %d, want 0 (the start of the first line)", got)
			}
		})
	}

	for name, key := range map[string]tea.KeyMsg{
		"end":    {Type: tea.KeyEnd},
		"ctrl+e": {Type: tea.KeyCtrlE},
	} {
		t.Run(name, func(t *testing.T) {
			m := typedModel(t, "one\ntwo")
			m, _ = updateModel(t, m, press(tea.KeyHome))
			m, _ = updateModel(t, m, key)
			if got := cursorOf(m); got != 7 {
				t.Fatalf("cursor = %d, want 7 (the end of the second line)", got)
			}
		})
	}
}

// Up and down move between the lines of a draft, keeping the column where
// they can and clamping where they cannot.
func TestUpAndDownMoveBetweenLines(t *testing.T) {
	m := typedModel(t, "one\ntwo")

	m, _ = updateModel(t, m, press(tea.KeyUp))
	if got := cursorOf(m); got != 3 {
		t.Fatalf("cursor = %d, want 3 (the end of the first line)", got)
	}

	m, _ = updateModel(t, m, press(tea.KeyLeft))
	m, _ = updateModel(t, m, press(tea.KeyLeft))
	if got := cursorOf(m); got != 1 {
		t.Fatalf("cursor = %d, want 1", got)
	}

	m, _ = updateModel(t, m, press(tea.KeyDown))
	if got := cursorOf(m); got != 5 {
		t.Fatalf("cursor = %d, want 5 (the same column on the line below)", got)
	}

	m, _ = updateModel(t, m, press(tea.KeyDown))
	if got := cursorOf(m); got != 5 {
		t.Fatalf(
			"cursor = %d, want 5: there is no line below the last one",
			got,
		)
	}

	m, _ = updateModel(t, m, press(tea.KeyUp))
	if got := cursorOf(m); got != 1 {
		t.Fatalf("cursor = %d, want 1 (the same column on the line above)", got)
	}
}

// Ctrl+U clears from the start of the line to the cursor, which is what
// readline does and what the key has always meant there.
func TestCtrlUClearsToTheCursor(t *testing.T) {
	m := typedModel(t, "hello world")
	for range 5 {
		m, _ = updateModel(t, m, press(tea.KeyLeft))
	}

	m, _ = updateModel(t, m, press(tea.KeyCtrlU))

	if m.Composer() != "world" {
		t.Fatalf("composer = %q, want %q", m.Composer(), "world")
	}
	if got := cursorOf(m); got != 0 {
		t.Fatalf("cursor = %d, want 0", got)
	}
}

// Ctrl+W deletes the word before the cursor.
func TestCtrlWDeletesTheWordBeforeTheCursor(t *testing.T) {
	cases := map[string]struct {
		text string
		want string
	}{
		"ascii":           {text: "hello brave world", want: "hello brave"},
		"trailing spaces": {text: "hello   ", want: "hello"},
		"cyrillic":        {text: "привет мир", want: "привет"},
		"one word":        {text: "привет", want: ""},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			m := typedModel(t, testCase.text)

			m, _ = updateModel(t, m, press(tea.KeyCtrlW))

			if m.Composer() != testCase.want {
				t.Fatalf(
					"composer = %q, want %q",
					m.Composer(),
					testCase.want,
				)
			}
		})
	}
}

// Ctrl+U with the cursor at the end clears the whole draft, which is what
// it did before the composer learned about a cursor and what a user with a
// single-line draft expects.
func TestCtrlUClearsTheWholeDraftFromTheEnd(t *testing.T) {
	m := typedModel(t, "hello")

	m, _ = updateModel(t, m, press(tea.KeyCtrlU))

	if m.Composer() != "" {
		t.Fatalf("composer = %q, want empty", m.Composer())
	}
	if got := cursorOf(m); got != 0 {
		t.Fatalf("cursor = %d, want 0", got)
	}
}

// Alt+Enter starts a new line and Enter sends. Shift+Enter is not a key
// Bubble Tea can tell apart from Enter in most terminals, so the newline
// has a combination that works everywhere: divergence 3 of the
// specification.
func TestAltEnterStartsANewLineAndEnterSends(t *testing.T) {
	source := &fakeChatSource{chats: []Chat{{ID: 7, Title: "A"}}}
	m := preparedConversation(t, source)
	m.focus = FocusComposer

	m, _ = updateModel(t, m, alt(tea.KeyEnter))
	if m.Composer() != "\n" {
		t.Fatalf("composer = %q, want a newline", m.Composer())
	}
	if m.sendState != sendStateIdle {
		t.Fatalf("sendState = %v, want idle: Alt+Enter must not send", m.sendState)
	}

	m, _ = updateModel(t, m, pressRunes("hi"))
	m, cmd := updateModel(t, m, press(tea.KeyEnter))
	if cmd == nil {
		t.Fatal("Enter must send")
	}
	if m.sendState != sendStateSending {
		t.Fatalf("sendState = %v, want sending", m.sendState)
	}
}

// alt is an Alt-modified key.
func alt(key tea.KeyType) tea.KeyMsg {
	return tea.KeyMsg{Type: key, Alt: true}
}

// A paste is text, not a command: a message copied from anywhere with
// newlines in it goes into the composer whole and is not sent by the
// newline inside it.
func TestPastedTextIsInsertedAndNotSent(t *testing.T) {
	source := &fakeChatSource{chats: []Chat{{ID: 7, Title: "A"}}}
	m := preparedConversation(t, source)
	m.focus = FocusComposer

	m, _ = updateModel(t, m, pressRunes("a"))
	paste := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("\nb"), Paste: true}

	updated, cmd := m.Update(paste)
	if cmd != nil {
		t.Fatalf("a paste returned %T, want no command", cmd())
	}

	m = updated.(Model)
	if m.Composer() != "a\nb" {
		t.Fatalf("composer = %q, want %q", m.Composer(), "a\nb")
	}
	if m.sendState != sendStateIdle {
		t.Fatalf("sendState = %v, want idle", m.sendState)
	}
}

// The text that goes to the queue is the text that was typed. Spaces and
// newlines are content, and trimming them would send a message the user did
// not write.
func TestWhitespaceInAMessageIsPreservedVerbatim(t *testing.T) {
	const draft = "  hi  \n  there "

	submitter := &recordingSubmitter{}
	m := conversationWithSubmitter(t, submitter)
	m.focus = FocusComposer
	setDraft(&m, draft)

	m, cmd := updateModel(t, m, press(tea.KeyEnter))
	if cmd == nil {
		t.Fatal("Enter must start a submission")
	}
	runCmd(t, cmd)

	if submitter.calls != 1 {
		t.Fatalf("SubmitMessage called %d times, want 1", submitter.calls)
	}
	if got := submitter.text; got != draft {
		t.Fatalf("submitted %q, want %q byte for byte", got, draft)
	}
}

// An empty or blank draft is not a message. Nothing is sent, no error is
// created, and the placeholder is briefly lit so that a user who pressed
// Enter knows the composer was there.
func TestBlankDraftIsNotSentAndThePlaceholderIsLit(t *testing.T) {
	for name, draft := range map[string]string{
		"empty":    "",
		"spaces":   "   ",
		"newlines": "\n\n",
		"mixed":    " \n \t ",
	} {
		t.Run(name, func(t *testing.T) {
			submitter := &recordingSubmitter{}
			m := conversationWithSubmitter(t, submitter)
			m.focus = FocusComposer
			setDraft(&m, draft)

			m, _ = updateModel(t, m, press(tea.KeyEnter))

			if submitter.calls != 0 {
				t.Fatal("a blank draft must not reach the queue")
			}
			if m.sendState != sendStateIdle {
				t.Fatalf("sendState = %v, want idle: no error is created", m.sendState)
			}
			if !m.composerPlaceholderLit {
				t.Fatal("the placeholder was not lit")
			}
		})
	}
}

// The lit placeholder goes out on its own, without a timer that repaints
// the screen: one message, one style change.
func TestTheLitPlaceholderGoesOutOnItsOwn(t *testing.T) {
	submitter := &recordingSubmitter{}
	m := conversationWithSubmitter(t, submitter)
	m.focus = FocusComposer

	updated, cmd := m.Update(press(tea.KeyEnter))
	m = updated.(Model)
	if !m.composerPlaceholderLit {
		t.Fatal("the placeholder was not lit")
	}
	if cmd == nil {
		t.Fatal("Enter on a blank draft must return the command that unlights it")
	}

	m, _ = updateModel(t, m, cmd())
	if m.composerPlaceholderLit {
		t.Fatal("the placeholder is still lit")
	}
}

// Typing after the placeholder was lit puts it out again: the hint has done
// its work.
func TestTypingUnlightsThePlaceholder(t *testing.T) {
	submitter := &recordingSubmitter{}
	m := conversationWithSubmitter(t, submitter)
	m.focus = FocusComposer

	updated, _ := m.Update(press(tea.KeyEnter))
	m = updated.(Model)

	m, _ = updateModel(t, m, pressRunes("h"))

	if m.composerPlaceholderLit {
		t.Fatal("the placeholder is still lit after typing")
	}
}

// A failed enqueue keeps the draft and the cursor where they were. The
// whole point of the draft is that a user does not have to write it twice.
func TestAFailedSendKeepsTheDraftAndTheCursor(t *testing.T) {
	submitter := &recordingSubmitter{err: errors.New("outbox unavailable")}
	m := conversationWithSubmitter(t, submitter)
	m.focus = FocusComposer
	m, _ = updateModel(t, m, pressRunes("hello world"))
	m, _ = updateModel(t, m, press(tea.KeyLeft))
	m, _ = updateModel(t, m, press(tea.KeyLeft))
	cursor := cursorOf(m)

	m, cmd := updateModel(t, m, press(tea.KeyEnter))
	m, _ = updateModel(t, m, runCmd(t, cmd))

	if m.sendState != sendStateError {
		t.Fatalf("sendState = %v, want error", m.sendState)
	}
	if m.Composer() != "hello world" {
		t.Fatalf("composer = %q, want the draft", m.Composer())
	}
	if got := cursorOf(m); got != cursor {
		t.Fatalf("cursor = %d, want %d", got, cursor)
	}
}

// The composer is frozen while a send is in flight, so that text typed
// after Enter is not thrown away when the queue accepts the message.
func TestTheComposerIsFrozenWhileSending(t *testing.T) {
	submitter := &recordingSubmitter{}
	m := conversationWithSubmitter(t, submitter)
	m.focus = FocusComposer

	m, _ = updateModel(t, m, pressRunes("hi"))
	m, _ = updateModel(t, m, press(tea.KeyEnter))
	if m.sendState != sendStateSending {
		t.Fatalf("sendState = %v, want sending", m.sendState)
	}

	m, _ = updateModel(t, m, pressRunes("!"))
	m, _ = updateModel(t, m, press(tea.KeyBackspace))

	if m.Composer() != "hi" {
		t.Fatalf(
			"composer = %q, want the draft frozen while sending",
			m.Composer(),
		)
	}
}

// An ordinary send error is forgotten when the draft is edited: the user is
// fixing what was wrong with the text, and the error was about the text.
func TestAnOrdinarySendErrorIsForgottenOnEdit(t *testing.T) {
	submitter := &recordingSubmitter{err: errors.New("message was not queued")}
	m := conversationWithSubmitter(t, submitter)
	m.focus = FocusComposer

	m, _ = updateModel(t, m, pressRunes("hi"))
	m, cmd := updateModel(t, m, press(tea.KeyEnter))
	m, _ = updateModel(t, m, runCmd(t, cmd))
	if m.sendState != sendStateError {
		t.Fatalf("sendState = %v, want error", m.sendState)
	}

	m, _ = updateModel(t, m, pressRunes("!"))

	if m.sendState != sendStateIdle {
		t.Fatalf("sendState = %v, want idle after editing", m.sendState)
	}
	if m.sendErr != nil {
		t.Fatalf("sendErr = %v, want nil after editing", m.sendErr)
	}
}

// A paused composer is not an error that editing clears. Nothing was ever
// attempted, the reason still holds, and a user who cannot send has to be
// told so on every keystroke rather than once.
func TestAPausedComposerKeepsItsReasonWhileTyping(t *testing.T) {
	m := h17PausedModel(t, &h17CountingSubmitter{})
	m.focus = FocusComposer
	m, _ = updateModel(t, m, pressRunes("h"))

	if m.sendState != sendStateError {
		t.Fatalf("sendState = %v, want error", m.sendState)
	}
	if !errors.Is(m.sendErr, errSendingPausedFixture) {
		t.Fatalf("sendErr = %v, want the paused reason", m.sendErr)
	}
	if !strings.Contains(plain(m.View()), "Sending paused") {
		t.Fatalf("the reason is gone from the screen:\n%s", plain(m.View()))
	}
}

// Clearing and deleting words are editing too, and they must not take the
// reason away either.
func TestAPausedComposerKeepsItsReasonThroughEditing(t *testing.T) {
	for name, key := range map[string]tea.KeyMsg{
		"ctrl+u":    press(tea.KeyCtrlU),
		"ctrl+w":    press(tea.KeyCtrlW),
		"backspace": press(tea.KeyBackspace),
	} {
		t.Run(name, func(t *testing.T) {
			m := h17PausedModel(t, &h17CountingSubmitter{})
			m.focus = FocusComposer
			m, _ = updateModel(t, m, pressRunes("hello"))

			m, _ = updateModel(t, m, key)

			if !strings.Contains(plain(m.View()), "Sending paused") {
				t.Fatalf("the reason is gone from the screen:\\n%s", plain(m.View()))
			}
		})
	}
}

// A paste is editing too.
func TestAPausedComposerKeepsItsReasonThroughAPaste(t *testing.T) {
	m := h17PausedModel(t, &h17CountingSubmitter{})
	m.focus = FocusComposer

	updated, _ := m.Update(tea.KeyMsg{
		Type:  tea.KeyRunes,
		Runes: []rune("hello"),
		Paste: true,
	})
	m = updated.(Model)

	if !strings.Contains(plain(m.View()), "Sending paused") {
		t.Fatalf("the reason is gone from the screen:\n%s", plain(m.View()))
	}
}

// The Tab keys move the focus and never insert a tab, and Esc leaves the
// composer without losing the draft: the hierarchy of §8.5 is unchanged by
// a composer that grew.
func TestTabAndEscStillMoveTheFocusAndKeepTheDraft(t *testing.T) {
	m := narrowTypedModel(t, "draft")

	m, _ = updateModel(t, m, press(tea.KeyTab))
	if m.focus != FocusHistory {
		t.Fatalf("focus = %v, want FocusHistory after Tab", m.focus)
	}
	if m.Composer() != "draft" {
		t.Fatalf("composer = %q, want no tab inserted", m.Composer())
	}

	m, _ = updateModel(t, m, press(tea.KeyShiftTab))
	if m.focus != FocusComposer {
		t.Fatalf("focus = %v, want FocusComposer after Shift+Tab", m.focus)
	}

	m, _ = updateModel(t, m, press(tea.KeyEsc))
	if m.focus != FocusHistory {
		t.Fatalf("focus = %v, want FocusHistory after Esc", m.focus)
	}
	if m.Composer() != "draft" {
		t.Fatalf("composer = %q, want the draft kept", m.Composer())
	}
}

// preparedConversation returns a model with a conversation open, driven the
// way a user drives it.
func preparedConversation(t *testing.T, source ChatSource) Model {
	t.Helper()

	m := NewModelWithSource(source)
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 24})
	m, _ = updateModel(t, m, chatsLoadedMsg{chats: []Chat{{ID: 7, Title: "A"}}})
	m, _ = updateModel(t, m, press(tea.KeyEnter))

	return m
}

// conversationWithSubmitter returns a model whose only delivery path is a
// submitter a test can read.
func conversationWithSubmitter(
	t *testing.T,
	submitter ComposerSubmitter,
) Model {
	t.Helper()

	m, err := NewModelWithDependencies(
		context.Background(),
		Dependencies{
			Source:           &fakeChatSource{},
			MessageSubmitter: submitter,
		},
	)
	if err != nil {
		t.Fatalf("NewModelWithDependencies: %v", err)
	}

	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 24})
	m, _ = updateModel(t, m, chatsLoadedMsg{chats: []Chat{{ID: 7, Title: "A"}}})
	m, _ = updateModel(t, m, press(tea.KeyEnter))
	if m.screen != ScreenConversation {
		t.Fatalf("screen = %v, want conversation", m.screen)
	}

	return m
}

// recordingSubmitter records what reached the delivery path.
type recordingSubmitter struct {
	calls int
	text  string
	err   error
}

func (s *recordingSubmitter) SubmitMessage(
	_ context.Context,
	_ int64,
	text string,
) (Submission, error) {
	s.calls++
	s.text = text

	if s.err != nil {
		return Submission{}, s.err
	}

	return Submission{ID: "1", State: SubmissionQueued}, nil
}

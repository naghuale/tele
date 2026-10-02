package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"telecli/internal/tui/theme"
)

// What the chat list says while it has nothing, and while it is trying
// (§17, §18).

// The words of §18, with the ellipsis of §18 and not the three dots the
// screen used to print: a loading line that ends in "..." is the same line
// as a status that never finishes.
const (
	loadingChatsText  = "Loading chats…"
	loadingChatsShort = "Loading chats…"
)

// chatsLoadSource is a chat source a test drives, with a load that stays
// in flight until the test answers it.
type chatsLoadSource struct {
	*fakeChatSource
	failWith error
	loads    int
	answer   chan struct{}
}

func newChatsLoadSource() *chatsLoadSource {
	return &chatsLoadSource{
		fakeChatSource: &fakeChatSource{},
		answer:         make(chan struct{}),
	}
}

// ListChats waits for the test, so a test can hold a load in flight for as
// long as it likes without sleeping.
func (s *chatsLoadSource) ListChats(ctx context.Context) ([]Chat, error) {
	s.loads++

	select {
	case <-s.answer:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	if s.failWith != nil {
		return nil, s.failWith
	}

	return s.chats, nil
}

func (s *chatsLoadSource) finish() { close(s.answer) }

// loadingModel is a model whose chat list load has been started and has
// not answered.
func loadingModel(t *testing.T, source ChatSource) Model {
	t.Helper()

	model, err := NewModelWithDependencies(context.Background(), Dependencies{
		Source:           source,
		MessageSubmitter: &recordingSubmitter{},
		Theme:            theme.DefaultTheme().ForProfile(theme.ProfileNoColor),
		ColorProfile:     theme.ProfileNoColor,
	})
	if err != nil {
		t.Fatalf("NewModelWithDependencies: %v", err)
	}

	model, _ = updateModel(t, model, tea.WindowSizeMsg{Width: 100, Height: 24})
	if cmd := model.Init(); cmd == nil {
		t.Fatal("Init of a model with a source asked for nothing")
	}

	return model
}

func TestTheChatListSaysItIsLoading(t *testing.T) {
	source := newChatsLoadSource()
	defer source.finish()
	model := loadingModel(t, source)

	view := plain(model.View())
	if !strings.Contains(view, loadingChatsText) {
		t.Fatalf("the screen does not say it is loading: %q", viewLines(view))
	}
}

// §18 waits N seconds and then says that the wait is longer than
// expected, with the one key that can do something about it.
func TestTheChatListWaitIsTenSeconds(t *testing.T) {
	if chatsLoadSlowAfter != 10*time.Second {
		t.Fatalf(
			"chatsLoadSlowAfter = %s, want 10s: §18 says the chat list gets N seconds",
			chatsLoadSlowAfter,
		)
	}
}

func TestTakingLongerThanExpectedIsSaidAfterTheWait(t *testing.T) {
	source := newChatsLoadSource()
	defer source.finish()
	model := loadingModel(t, source)

	if strings.Contains(plain(model.View()), "Taking longer than expected") {
		t.Fatal("the wait was announced before it was waited for")
	}

	model, _ = updateModel(t, model, chatsLoadDeadlineMsg{operation: model.chatsLoadOperation})

	view := plain(model.View())
	if !strings.Contains(view, "Taking longer than expected") {
		t.Fatalf("the screen does not say the wait is long: %q", viewLines(view))
	}
	if !strings.Contains(view, "press R to retry") {
		t.Fatalf("the screen does not name the key: %q", viewLines(view))
	}
}

// A load that finished before the deadline has nothing to announce, and a
// deadline of a load the user already retried is not the deadline of the
// one on the screen.
func TestTheDeadlineOfAFinishedLoadSaysNothing(t *testing.T) {
	source := newChatsLoadSource()
	source.chats = []Chat{{ID: 7, Title: "A"}}
	source.finish()
	model := loadingModel(t, source)

	model, _ = updateModel(t, model, chatsLoadedMsg{chats: source.chats})
	model, _ = updateModel(t, model, chatsLoadDeadlineMsg{operation: model.chatsLoadOperation})

	if strings.Contains(plain(model.View()), "Taking longer than expected") {
		t.Fatal("a finished load announced a long wait")
	}
}

// §18: R repeats the load. The state goes back to loading and the source
// is asked again, which is what a user who was waiting for a connection
// needs.
func TestRRepeatsTheChatListLoad(t *testing.T) {
	source := newChatsLoadSource()
	source.finish()
	model := loadingModel(t, source)
	model, _ = updateModel(t, model, chatsLoadedMsg{})

	if model.chatsState != loadStateEmpty {
		t.Fatalf("chatsState = %v, want empty", model.chatsState)
	}
	before := source.loads

	model, cmd := updateModel(t, model, pressRunes("R"))
	if cmd == nil {
		t.Fatal("R did not repeat the load")
	}
	if model.chatsState != loadStateLoading {
		t.Fatalf("chatsState = %v, want loading", model.chatsState)
	}
	_ = firstReadOf(t, cmd)
	if source.loads != before+1 {
		t.Fatalf("ListChats calls = %d, want %d", source.loads, before+1)
	}
	if !strings.Contains(plain(model.View()), loadingChatsText) {
		t.Fatal("the retry is not on the screen")
	}
}

// R is a key of the chat list and not of the composer: §8.4 turns single
// letters back into text while a message is being written, and an R there
// is the letter R.
func TestRIsALetterInTheComposer(t *testing.T) {
	model, err := NewModelWithDependencies(context.Background(), Dependencies{
		Source:           &fakeChatSource{chats: []Chat{{ID: 7, Title: "A"}}},
		MessageSubmitter: &recordingSubmitter{},
	})
	if err != nil {
		t.Fatalf("NewModelWithDependencies: %v", err)
	}
	model, _ = updateModel(t, model, tea.WindowSizeMsg{Width: 100, Height: 24})
	model, _ = updateModel(t, model, chatsLoadedMsg{chats: []Chat{{ID: 7, Title: "A"}}})
	model, _ = updateModel(t, model, press(tea.KeyEnter))
	model, _ = updateModel(t, model, pressRunes("R"))

	if got := string(model.composer); got != "R" {
		t.Fatalf("composer = %q, want R", got)
	}
}

// A load that failed says what a user can do about it, and not what went
// wrong: the cause can name a file or a TDLib error message, and the
// screen is not the place for it (§11.3, §19).
func TestAFailedLoadSaysASafeReasonAndTheRetryHint(t *testing.T) {
	cause := errors.New(
		"list chats: ERROR 400: +1 555 0100 api_hash 0123456789abcdef /Users/me/Library/telecli",
	)
	source := newChatsLoadSource()
	source.finish()
	source.failWith = cause

	log := &recordingWriter{}
	model, err := NewModelWithDependencies(context.Background(), Dependencies{
		Source:           source,
		MessageSubmitter: &recordingSubmitter{},
		Diagnostics:      log,
		Theme:            theme.DefaultTheme().ForProfile(theme.ProfileNoColor),
		ColorProfile:     theme.ProfileNoColor,
	})
	if err != nil {
		t.Fatalf("NewModelWithDependencies: %v", err)
	}
	model, _ = updateModel(t, model, tea.WindowSizeMsg{Width: 100, Height: 24})
	model, _ = updateModel(t, model, chatsLoadedMsg{err: cause})

	view := plain(model.View())
	for _, secret := range []string{
		"+1 555 0100",
		"api_hash 0123456789abcdef",
		"/Users/me/Library/telecli",
		"ERROR 400",
	} {
		if strings.Contains(view, secret) {
			t.Fatalf("the screen carries %q", secret)
		}
	}
	if !strings.Contains(view, "Failed to load chats") {
		t.Fatalf("the screen does not say the load failed: %q", viewLines(view))
	}
	if !strings.Contains(view, "press R to retry") {
		t.Fatalf("the screen does not name the key: %q", viewLines(view))
	}
	if !strings.Contains(log.String(), "api_hash 0123456789abcdef") {
		t.Fatalf("diagnostics = %q, want the cause", log.String())
	}
}

// The hint bar names R only while R does something: §4.6 has no hint for a
// key that does nothing, and a loaded list is not a thing to retry.
func TestTheRetryHintAppearsOnlyWhileARetryIsPossible(t *testing.T) {
	source := newChatsLoadSource()
	source.finish()
	model := loadingModel(t, source)

	if hint := model.hintText(LayoutFor(model.width, model.height)); hint != hintChatList {
		t.Fatalf("hint = %q, want the plain chat list hint", hint)
	}

	model, _ = updateModel(t, model, chatsLoadDeadlineMsg{operation: model.chatsLoadOperation})
	if hint := model.hintText(LayoutFor(model.width, model.height)); !strings.Contains(hint, "R retry") {
		t.Fatalf("hint = %q, want the retry key", hint)
	}
}

// ---- Empty states of §17 ----

func TestNoChatsYetAndWhatToDoAboutIt(t *testing.T) {
	model, _ := NewModelWithDependencies(context.Background(), Dependencies{
		Source:           &fakeChatSource{},
		MessageSubmitter: &recordingSubmitter{},
	})
	model, _ = updateModel(t, model, tea.WindowSizeMsg{Width: 100, Height: 24})
	model, _ = updateModel(t, model, chatsLoadedMsg{})

	view := plain(model.View())
	if !strings.Contains(view, "No chats yet") {
		t.Fatalf("the screen does not say there are no chats: %q", viewLines(view))
	}
	if !strings.Contains(view, "Start a new conversation or wait for chats to load.") {
		t.Fatalf("the screen does not say what to do: %q", viewLines(view))
	}
}

func TestNoMessagesYetAndWhereToWrite(t *testing.T) {
	model, _ := NewModelWithDependencies(context.Background(), Dependencies{
		Source:           &fakeChatSource{chats: []Chat{{ID: 7, Title: "A"}}},
		MessageSubmitter: &recordingSubmitter{},
	})
	model, _ = updateModel(t, model, tea.WindowSizeMsg{Width: 100, Height: 24})
	model, _ = updateModel(t, model, chatsLoadedMsg{chats: []Chat{{ID: 7, Title: "A"}}})
	model, _ = updateModel(t, model, press(tea.KeyEnter))
	model, _ = updateModel(t, model, historyLoadedMsg{
		chatID:    7,
		operation: model.historyOperation,
		page:      HistoryPage{},
	})

	view := plain(model.View())
	if !strings.Contains(view, "No messages yet") {
		t.Fatalf("the screen does not say the chat is empty: %q", viewLines(view))
	}
	if !strings.Contains(view, "Write the first message below.") {
		t.Fatalf("the screen does not say where to write: %q", viewLines(view))
	}
}

func TestSelectAChatAndWhatToDoAboutIt(t *testing.T) {
	model, _ := NewModelWithDependencies(context.Background(), Dependencies{
		Source:           &fakeChatSource{},
		MessageSubmitter: &recordingSubmitter{},
	})
	model, _ = updateModel(t, model, tea.WindowSizeMsg{Width: 100, Height: 24})
	model, _ = updateModel(t, model, chatsLoadedMsg{chats: []Chat{{ID: 7, Title: "A"}}})

	view := plain(model.View())
	if !strings.Contains(view, "Select a chat") {
		t.Fatalf("the screen does not ask for a chat: %q", viewLines(view))
	}
	if !strings.Contains(view, "Enter or Tab to open.") {
		t.Fatalf("the screen does not say how: %q", viewLines(view))
	}
}

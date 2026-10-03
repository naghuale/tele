package tui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"telecli/internal/tui/theme"
)

// TDLib counts the online members of a chat only while the chat is open,
// so the interface says which chat the user is looking at and stops saying
// so when they stop looking at it.

// recordingOpener is a presence opener that records what it was asked.
type recordingOpener struct {
	opened  []int64
	closed  []int64
	openErr error
}

func (o *recordingOpener) OpenChat(_ context.Context, chatID int64) error {
	o.opened = append(o.opened, chatID)
	if o.openErr != nil {
		return o.openErr
	}

	return nil
}

func (o *recordingOpener) CloseChat(_ context.Context, chatID int64) error {
	o.closed = append(o.closed, chatID)

	return nil
}

// slowCloseOpener is a presence opener that needs a round trip to close a chat
// and that remembers the order in which it was asked for things.
//
// The round trip is the whole of it. A close to TDLib is a query, and a query
// is not over by the time the program has sent the next one: in a batch the
// open of the new chat is on its way while the close of the old one is still in
// flight, and that is what this opener writes down when the two calls are a
// batch rather than one command.
//
// The order is written under a lock because the members of a batch are
// goroutines: reading a list that two of them append to is a race, and a race
// in a test of order proves nothing about the order.
type slowCloseOpener struct {
	mu    sync.Mutex
	calls []string

	closeRoundTrip time.Duration
}

// closeRoundTrip is how long closing a chat takes in slowCloseOpener.
//
// It is a hundred times longer than a goroutine takes to start, which is all
// the test asks of the machine: the open has to be running before the close is
// over for the order to be wrong, and a goroutine that has not started within
// a tenth of a second belongs to a machine that has stopped.
const closeRoundTrip = 100 * time.Millisecond

func (o *slowCloseOpener) OpenChat(_ context.Context, chatID int64) error {
	o.remember(fmt.Sprintf("open %d", chatID))

	return nil
}

func (o *slowCloseOpener) CloseChat(_ context.Context, chatID int64) error {
	time.Sleep(o.closeRoundTrip)
	o.remember(fmt.Sprintf("close %d", chatID))

	return nil
}

func (o *slowCloseOpener) remember(call string) {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.calls = append(o.calls, call)
}

// asked is what the opener was asked for, in the order it was asked.
func (o *slowCloseOpener) asked() []string {
	o.mu.Lock()
	defer o.mu.Unlock()

	return slices.Clone(o.calls)
}

// runCommands runs the commands a key press returned.
//
// The open and the close are commands and not calls: a chat that cannot be
// opened must not hold the keys of a user who is walking through chats, and
// a query to TDLib is a round trip. A test therefore runs what the model
// returned and then looks at what the opener was asked.
func runCommands(t *testing.T, cmd tea.Cmd) {
	t.Helper()

	if cmd == nil {
		return
	}
	// Running a batch yields its members rather than its result, so the
	// members are what has to be run - and a batch can be nested, because
	// entering a chat is one batch around the open and around the reads.
	runResult(cmd())
}

func runResult(msg tea.Msg) {
	batch, isBatch := msg.(tea.BatchMsg)
	if !isBatch {
		return
	}
	for _, member := range batch {
		runResult(member())
	}
}

// runLikeBubbleTea runs a command the way Bubble Tea runs it: the members of
// a batch are commands in their own right and are run at the same time as one
// another, and a batch may be nested inside another one.
//
// runCommands above is not this one, and the difference is the whole of a
// promise about order. It walks the members one after another, which is the
// order a reader of it expects and not the order the program has: the members
// of a batch are goroutines, and nothing makes one of them wait for another.
func runLikeBubbleTea(t *testing.T, cmd tea.Cmd) {
	t.Helper()

	if cmd == nil {
		return
	}

	msg := cmd()
	batch, isBatch := msg.(tea.BatchMsg)
	if !isBatch {
		return
	}

	var running sync.WaitGroup
	for _, member := range batch {
		running.Add(1)
		go func() {
			defer running.Done()

			runLikeBubbleTea(t, member)
		}()
	}
	running.Wait()
}

// openerModel is a model with a presence opener and two chats loaded.
//
// A nil opener is a nil interface and not a typed nil pointer: a nil
// pointer in an interface is a dependency that exists and panics, which is
// not what "no dependency" means.
func openerModel(t *testing.T, opener ChatPresenceOpener) Model {
	t.Helper()

	return openerModelAt(t, opener, 100)
}

// openerModelAt is the same model at a width, because a two-pane screen
// keeps the chat list beside the conversation and a single-pane one does
// not, and both have their own way of leaving a chat.
func openerModelAt(
	t *testing.T,
	opener ChatPresenceOpener,
	width int,
) Model {
	t.Helper()

	deps := Dependencies{
		Source: &fakeChatSource{chats: []Chat{
			{ID: 7, Title: "A"},
			{ID: 8, Title: "B"},
		}},
		MessageSubmitter: &recordingSubmitter{},
		Diagnostics:      &recordingWriter{},
		Theme:            theme.DefaultTheme().ForProfile(theme.ProfileNoColor),
		ColorProfile:     theme.ProfileNoColor,
	}
	if opener != nil {
		deps.PresenceOpener = opener
	}

	model, err := NewModelWithDependencies(context.Background(), deps)
	if err != nil {
		t.Fatalf("NewModelWithDependencies: %v", err)
	}

	model, _ = updateModel(t, model, tea.WindowSizeMsg{Width: width, Height: 24})
	model, _ = updateModel(t, model, chatsLoadedMsg{chats: []Chat{
		{ID: 7, Title: "A"},
		{ID: 8, Title: "B"},
	}})

	return model
}

func TestEnteringAChatOpensIt(t *testing.T) {
	opener := &recordingOpener{}
	model := openerModel(t, opener)

	model, cmd := updateModel(t, model, press(tea.KeyEnter))
	if cmd == nil {
		t.Fatal("the open was not started as a command")
	}
	if model.openedChat != 7 {
		t.Fatalf("openedChat = %d, want 7", model.openedChat)
	}
	runCommands(t, cmd)
	if len(opener.opened) != 1 || opener.opened[0] != 7 {
		t.Fatalf("opened = %v, want the chat that was entered", opener.opened)
	}
}

func TestFollowingTheSelectionClosesTheOldChatAndOpensTheNew(t *testing.T) {
	opener := &recordingOpener{}
	model := openerModel(t, opener)

	model, cmd := updateModel(t, model, press(tea.KeyEnter))
	runCommands(t, cmd)

	// The keys are on the chat list, which is where a user walks through
	// chats; on a two-pane screen the conversation follows the selection.
	model.focus = FocusChatList
	model, cmd = updateModel(t, model, press(tea.KeyDown))
	runCommands(t, cmd)

	if len(opener.closed) != 1 || opener.closed[0] != 7 {
		t.Fatalf("closed = %v, want the chat that was left", opener.closed)
	}
	if len(opener.opened) != 2 || opener.opened[1] != 8 {
		t.Fatalf("opened = %v, want the new chat opened after the old one", opener.opened)
	}
	if model.openedChat != 8 {
		t.Fatalf("openedChat = %d, want 8", model.openedChat)
	}
}

// Walking through the chat list is the fastest way a user changes the chat they
// are looking at, and TDLib has to hear of it in one order: the chat that was
// open is closed before the chat that is opened now is opened. A batch of two
// commands promises no order at all — Bubble Tea runs the members of a batch at
// the same time — so the open can reach TDLib before, or beside, the close:
// two chats counted as open at once, and a broadcast chat, which TDLib loads
// by openChat, read by the very call that came out of order.
//
// This is the check the promise in chat_lifecycle.go is kept by. It fails
// while the two calls are a batch, and it holds when they are one command.
func TestTheNewChatIsOpenedAfterTheOldOneIsClosed(t *testing.T) {
	opener := &slowCloseOpener{closeRoundTrip: closeRoundTrip}
	model := openerModel(t, opener)

	// The first chat has no chat before it, so nothing is closed here and
	// this open is not the question.
	model, cmd := updateModel(t, model, press(tea.KeyEnter))
	runCommands(t, cmd)

	// The keys are on the chat list, which is where a user walks through
	// chats; the conversation follows the selection.
	model.focus = FocusChatList
	_, cmd = updateModel(t, model, press(tea.KeyDown))
	// Run the commands as the program runs them, because a runner that walks
	// a batch one member at a time is the one thing that cannot see the two
	// calls out of order.
	runLikeBubbleTea(t, cmd)

	want := []string{"open 7", "close 7", "open 8"}
	if got := opener.asked(); !slices.Equal(got, want) {
		t.Fatalf(
			"the opener was asked %v, want %v: the chat that was left is closed before the chat that is opened",
			got,
			want,
		)
	}
}

// Opening the chat that is already open is not a second open: a user
// pressing Enter twice in a row must not make TDLib count the same chat
// twice.
func TestReopeningTheSameChatAsksNothing(t *testing.T) {
	opener := &recordingOpener{}
	model := openerModel(t, opener)

	model, cmd := updateModel(t, model, press(tea.KeyEnter))
	runCommands(t, cmd)
	_, cmd = updateModel(t, model, press(tea.KeyEnter))

	if len(opener.opened) != 1 {
		t.Fatalf("opened = %v, want one open", opener.opened)
	}
	if cmd == nil {
		t.Fatal("reopening the same chat started a command")
	}
}

func TestLeavingAConversationClosesTheChat(t *testing.T) {
	opener := &recordingOpener{}
	// A single-pane screen: a two-pane one keeps the chat list beside the
	// conversation, so there is no leaving it.
	model := openerModelAt(t, opener, 60)

	model, cmd := updateModel(t, model, press(tea.KeyEnter))
	runCommands(t, cmd)
	// The composer has the focus after Enter, so Esc goes to the timeline
	// first and only the second one leaves the conversation.
	model, _ = updateModel(t, model, press(tea.KeyEsc))
	model, cmd = updateModel(t, model, press(tea.KeyEsc))
	runCommands(t, cmd)

	if len(opener.closed) != 1 || opener.closed[0] != 7 {
		t.Fatalf("closed = %v, want the chat that was left", opener.closed)
	}
	if model.openedChat != 0 {
		t.Fatalf("openedChat = %d, want 0", model.openedChat)
	}
}

// A chat that is still open when the program quits is closed on the way
// out, or TDLib keeps counting a chat nobody is looking at until the
// process is gone.
func TestQuittingClosesTheOpenChat(t *testing.T) {
	opener := &recordingOpener{}
	model := openerModel(t, opener)

	model, cmd := updateModel(t, model, press(tea.KeyEnter))
	runCommands(t, cmd)
	// Ctrl+C is the deliberate shutdown (§8.1): q is a letter in the
	// composer, which is where a user is after entering a chat. It takes two
	// presses now (the owner, 03.10), and the first one closes nothing.
	model, _ = updateModel(t, model, press(tea.KeyCtrlC))
	if len(opener.closed) != 0 {
		t.Fatalf("closed = %v, want nothing closed by the first Ctrl+C", opener.closed)
	}

	_, cmd = updateModel(t, model, press(tea.KeyCtrlC))
	runCommands(t, cmd)

	if len(opener.closed) != 1 || opener.closed[0] != 7 {
		t.Fatalf("closed = %v, want the open chat closed on quit", opener.closed)
	}
	if cmd == nil {
		t.Fatal("quitting did not return a command")
	}
}

// A chat that cannot be opened is a presence the header does not draw, and
// it is not a screen that stops working: the messages of the chat are
// loaded either way. The cause goes to the diagnostic stream, because it
// can name a TDLib error message.
func TestAFailedOpenIsLoggedAndTheScreenGoesOn(t *testing.T) {
	log := &recordingWriter{}
	opener := &recordingOpener{
		openErr: errors.New("openChat: ERROR 400 CHAT_INVALID"),
	}

	model, err := NewModelWithDependencies(context.Background(), Dependencies{
		Source:           &fakeChatSource{chats: []Chat{{ID: 7, Title: "A"}}},
		MessageSubmitter: &recordingSubmitter{},
		PresenceOpener:   opener,
		Diagnostics:      log,
		Theme:            theme.DefaultTheme().ForProfile(theme.ProfileNoColor),
		ColorProfile:     theme.ProfileNoColor,
	})
	if err != nil {
		t.Fatalf("NewModelWithDependencies: %v", err)
	}
	model, _ = updateModel(t, model, tea.WindowSizeMsg{Width: 100, Height: 24})
	model, _ = updateModel(t, model, chatsLoadedMsg{chats: []Chat{{ID: 7, Title: "A"}}})
	model, cmd := updateModel(t, model, press(tea.KeyEnter))
	runCommands(t, cmd)

	if model.screen != ScreenConversation {
		t.Fatalf("screen = %v, want the conversation", model.screen)
	}
	if !strings.Contains(log.String(), "CHAT_INVALID") {
		t.Fatalf("diagnostics = %q, want the cause", log.String())
	}
	if strings.Contains(plain(model.View()), "CHAT_INVALID") {
		t.Fatal("the cause of a failed open is on the screen")
	}
}

// Without an opener nothing is opened and nothing is closed, and the
// conversation works exactly as before: a program built without Telegram
// has no chat to open.
func TestWithoutAnOpenerNothingIsOpened(t *testing.T) {
	model := openerModel(t, nil)

	model, openCmd := updateModel(t, model, press(tea.KeyEnter))
	runCommands(t, openCmd)
	if model.screen != ScreenConversation {
		t.Fatalf("screen = %v, want the conversation", model.screen)
	}
	if model.openedChat != 0 {
		t.Fatalf("openedChat = %d, want 0", model.openedChat)
	}
}

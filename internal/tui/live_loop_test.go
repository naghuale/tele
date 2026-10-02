package tui

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"telecli/internal/tui/theme"
)

// The tests of the live chat list on the loop the program really runs.
//
// Everything above them in live_source_test.go drives Update by hand, which
// proves what a change does to the model and says nothing about the program
// a person is looking at: whether the change reaches the loop at all,
// whether the loop wakes up, and whether the bytes the row needs reach the
// terminal without a key press. Those three questions have exactly one
// honest answer, and it is a program.
//
// So the tests below run the real Bubble Tea loop over a model with a live
// state behind it, put a change into the state the way TDLib puts one there,
// and read what the program wrote. No key is pressed between the change and
// the assertion: a list that needs a key to move is the defect these tests
// are the control for.

// The size the program is run at, and the one the frames are measured in.
const (
	liveLoopWidth  = 100
	liveLoopHeight = 24
)

// liveLoopTime is how long a test waits for the program to draw what it was
// told about, and how long it gives the subscription to let go of its wait
// when the program is closed.
//
// Both are generous next to a tenth of a second, which is all the loop takes
// to answer a change, and short enough that a test which is waiting for
// something that never happens fails instead of hanging.
const liveLoopTime = 3 * time.Second

// liveLoop is a running Bubble Tea program with a live state behind it.
type liveLoop struct {
	program *tea.Program
	screen  *lockedBuffer
	stopped chan struct{}
}

// startLiveLoop runs the program over a model with live behind it, sized and
// loaded, and returns it once the first frame has been drawn.
func startLiveLoop(t *testing.T, live *fakeLiveSource, chats []Chat) *liveLoop {
	t.Helper()

	model, err := NewModelWithDependencies(context.Background(), Dependencies{
		Source:           &fakeChatSource{},
		MessageSubmitter: &recordingSubmitter{},
		LiveUpdates:      live,
		Theme:            defaultTestTheme(),
		ColorProfile:     theme.ProfileNoColor,
	})
	if err != nil {
		t.Fatalf("NewModelWithDependencies: %v", err)
	}

	model = withClock(model, liveMoment, time.UTC)
	model, _ = updateModel(t, model, tea.WindowSizeMsg{
		Width:  liveLoopWidth,
		Height: liveLoopHeight,
	})
	model, _ = updateModel(t, model, chatsLoadedMsg{chats: chats})

	loop := &liveLoop{
		screen:  &lockedBuffer{},
		stopped: make(chan struct{}),
	}
	loop.program = tea.NewProgram(
		model,
		tea.WithInput(nil),
		tea.WithOutput(loop.screen),
		tea.WithoutSignalHandler(),
		tea.WithoutCatchPanics(),
	)

	go func() {
		defer close(loop.stopped)

		_, _ = loop.program.Run()
	}()

	// The size is not a key press, and the program cannot lay the screen out
	// without one: it is the terminal answering, not the user.
	loop.program.Send(tea.WindowSizeMsg{Width: liveLoopWidth, Height: liveLoopHeight})
	loop.waitFor(t, "the first frame", func(lines []string) bool {
		return len(chatListRowsOf(lines)) > 0
	})

	return loop
}

// close shuts the program down and waits for it to be gone.
func (l *liveLoop) close() {
	l.program.Quit()

	<-l.stopped
}

// waitFor waits until the screen satisfies the condition, and fails with the
// last frame it saw when it never does.
//
// The failure names the frame rather than a count: a list that did not move
// is a list whose rows are all still there, and the rows are the evidence.
func (l *liveLoop) waitFor(
	t *testing.T,
	what string,
	condition func([]string) bool,
) {
	t.Helper()

	deadline := wallClock().Add(liveLoopTime)
	for {
		lines := l.lines()
		if condition(lines) {
			return
		}
		if wallClock().After(deadline) {
			t.Fatalf("the program never drew %s. The list on the screen:\n%s",
				what, strings.Join(chatListRowsOf(lines), "\n"),
			)
		}

		time.Sleep(10 * time.Millisecond)
	}
}

// lines returns what the program wrote, without the escape sequences.
func (l *liveLoop) lines() []string {
	return strings.Split(ansi.Strip(l.screen.String()), "\n")
}

// chatListRowsOf returns the rows of the chat list of the newest frame.
//
// The frames of the renderer are written whole, from the top of the screen
// down, and every frame begins with the heading of the list — so the rows
// after the last heading are the rows of the frame the program drew last.
// The heading, the search hint and the rule under them are not rows.
func chatListRowsOf(lines []string) []string {
	heading := -1
	for index, line := range lines {
		if strings.Contains(line, chatListTitle) {
			heading = index
		}
	}
	if heading < 0 {
		return nil
	}

	var rows []string
	for _, line := range lines[heading+1:] {
		switch {
		case strings.Contains(line, chatListSearchHint):
		case strings.Contains(line, strings.Repeat(focusRuleGlyph, 3)):
		case strings.TrimSpace(line) == "":
		default:
			rows = append(rows, line)
		}
	}

	return rows
}

// liveAccount is the list a live account of twenty chats keeps: every chat
// with the moment of its own last message.
func liveAccount() []LiveChat {
	rows := make([]LiveChat, 0, liveAccountChats)
	for id := int64(1); id <= liveAccountChats; id++ {
		rows = append(rows, LiveChat{
			ID:      id,
			Title:   liveChatName(id),
			Preview: "the last message of " + liveChatName(id),
			At:      liveMoment.Add(-time.Duration(id) * time.Minute),
		})
	}

	return rows
}

// liveAccountChats is how many chats the account of these tests has: more
// than the list holds on a 24-row screen, so the list has a window and the
// window is the thing a message that arrives above it has to get past.
const liveAccountChats = 20

// liveAccountWith puts a message in one chat of the list: the chat is at the
// top of it, with the words of the message, the moment it was sent and one
// unread.
func liveAccountWith(chatID int64, text string) []LiveChat {
	rows := []LiveChat{{
		ID:      chatID,
		Title:   liveChatName(chatID),
		Preview: text,
		At:      liveMoment,
		Unread:  1,
	}}
	for _, row := range liveAccount() {
		if row.ID == chatID {
			continue
		}
		rows = append(rows, row)
	}

	return rows
}

// A message that arrives in a chat which is not the one on the screen is on
// the screen by itself: no key is pressed between the change and the row, and
// the row is the top row of the list.
//
// This is the owner's report of 02.10 (a real account, main 550dd16): the
// list did not move at all until a key was pressed. The wake-up was never
// the missing half — the loop was told and answered — and the row was never
// drawn, because the window of the list was moved down by the arrival: it
// keeps the chat under the cursor on the row it was on, which pushes the
// chat that received the message above the top of the window. The frame the
// program wrote was the frame it already had.
func TestAMessageInAChatThatIsNotOpenIsOnTheScreenWithoutAKeyPress(t *testing.T) {
	live := newFakeLiveSource()
	loop := startLiveLoop(t, live, liveChatList(liveAccountChats))
	defer loop.close()

	// Nobody has pressed a key: the cursor is where the loaded list put it,
	// on the first chat, and the chat that receives the message is neither
	// that one nor an open conversation.
	live.setChats(liveAccountWith(14, firmwareText))

	loop.waitFor(t, "the chat that received the message", func(lines []string) bool {
		rows := chatListRowsOf(lines)
		return len(rows) > 0 && strings.Contains(rows[0], liveChatName(14))
	})

	rows := chatListRowsOf(loop.lines())
	if !strings.Contains(rows[0], liveChatName(14)) {
		t.Fatalf("the top row is %q, want the chat that received the message (%s)",
			rows[0], liveChatName(14),
		)
	}
	if len(rows) < 2 || !strings.Contains(rows[1], firmwareText) {
		t.Fatalf("the row of the chat says %q, want the message that arrived", rows[1])
	}
	if !strings.Contains(rows[0], liveMomentText()) {
		t.Fatalf("the row of the chat does not carry the moment of the message: %q",
			rows[0],
		)
	}
	if !strings.Contains(rows[1], " 1 ") {
		t.Fatalf("the row of the chat carries no unread count: %q", rows[1])
	}
}

// firmwareText is the message the chat of these tests receives.
//
// It is short because the row of a chat on a two-pane screen has room for
// about twenty columns of preview, and a message that was cut on the screen
// is a message the test cannot look for.
const firmwareText = "firmware out"

// liveMomentText is what the row of a chat says for the moment of a message
// sent at liveMoment: the clock of the model is the clock of the fixture, so
// the row carries that moment and not the moment of the machine.
func liveMomentText() string {
	return liveMoment.Format("15:04")
}

// A burst of changes is one redraw, and the redraw shows the last of them:
// the changes are folded for a tenth of a second and the state is read after
// the fold, so the screen is the newest state and not the first one of the
// burst.
func TestABurstOfChangesLeavesTheLastStateOnTheScreen(t *testing.T) {
	live := newFakeLiveSource()
	loop := startLiveLoop(t, live, liveChatList(liveAccountChats))
	defer loop.close()

	for index := range 5 {
		live.setChats(liveAccountWith(
			int64(liveAccountChats-index),
			"burst "+string(rune('a'+index)),
		))
	}

	loop.waitFor(t, "the last change of the burst", func(lines []string) bool {
		rows := chatListRowsOf(lines)
		return len(rows) > 0 && strings.Contains(rows[0], liveChatName(liveAccountChats-4))
	})

	rows := chatListRowsOf(loop.lines())
	if !strings.Contains(rows[1], "burst e") {
		t.Fatalf("the screen says %q, want the last change of the burst", rows[1])
	}
}

// The subscription is one wait for as long as the program runs, and closing
// the program leaves none of it behind: the wait ends with the program's own
// context, and a goroutine left waiting on the change signal of a state
// nobody will write to again is a goroutine that never returns.
//
// The program here is the one the composition root runs, and it is closed
// with the key a user closes it with while the context of the application is
// still alive — which is the case in which a wait bound to the context of the
// application is a wait nobody ends.
func TestClosingTheProgramLeavesNoWaitBehind(t *testing.T) {
	live := newFakeLiveSource()
	keys, typedKeys := io.Pipe()
	out := newTerminalOutput(&lockedBuffer{}, false)

	closed := make(chan error, 1)
	go func() {
		closed <- runDependenciesProgram(
			context.Background(),
			out,
			keys,
			Dependencies{
				Source:           &fakeChatSource{},
				MessageSubmitter: &recordingSubmitter{},
				LiveUpdates:      live,
				Theme:            defaultTestTheme(),
				ColorProfile:     theme.ProfileNoColor,
			},
		)
	}()

	// One wait for the first change, and one for the change that follows it:
	// the loop woke up on something that was not a key press and armed the
	// next wait. That next wait is the one that has to be let go of.
	waitForWaits(t, live, 1)
	live.setChats(liveAccountWith(14, firmwareText))
	waitForWaits(t, live, 2)

	if _, err := typedKeys.Write([]byte("q")); err != nil {
		t.Fatalf("write the key that closes the program: %v", err)
	}

	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("the program ended with %v", err)
		}
	case <-time.After(liveLoopTime):
		t.Fatal("the program did not end on the key that closes it")
	}

	// The context of the application is the background one and is still
	// alive here: whatever is still waiting is waiting for a change nobody
	// will announce again.
	deadline := wallClock().Add(liveLoopTime)
	for waiting := live.waitingOf(); waiting > 0; waiting = live.waitingOf() {
		if wallClock().After(deadline) {
			t.Fatalf(
				"%d waits for a change are still running after the program closed",
				waiting,
			)
		}

		time.Sleep(10 * time.Millisecond)
	}
}

// waitForWaits waits until the subscription has started the number of waits
// it is asked for, and fails when it never does.
func waitForWaits(t *testing.T, live *fakeLiveSource, want int) {
	t.Helper()

	deadline := wallClock().Add(liveLoopTime)
	for {
		if waits := live.waitsOf(); waits >= want {
			return
		}
		if wallClock().After(deadline) {
			t.Fatalf(
				"the subscription started %d waits for a change, want %d",
				live.waitsOf(), want,
			)
		}

		time.Sleep(10 * time.Millisecond)
	}
}

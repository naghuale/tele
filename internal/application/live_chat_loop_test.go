package application

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"telecli/internal/livewatch"
	"telecli/internal/telegram"
	"telecli/internal/tui"
	"telecli/internal/tui/theme"
)

// The live chat list from the wire to the screen, in one test.
//
// Everything else about the live list is proved against a fake source or
// against the store alone, and neither is the whole of what a person sees:
// the owner read a list that did not move on a real account (02.10, main
// 550dd16 and cd4a275) with both of those green. So this file puts the real
// store, the real adapter, the real Bubble Tea loop and the real TDLib
// payload sequence of one arriving message together, and reads the frame
// the program wrote.
//
// The sequence is the one TDLib sends for a message in a chat that is not
// the open one (internal/telegram/live_messages.go, td_api.tl:10401,
// :10507, :10512, :10539), in the order TDLib sends it.

// recordedChats is how many chats the account of these tests has. The list
// of a live account is longer than the list holds on a 24-row screen, so
// the window of the list is what an arrival has to get past.
const recordedChats = 20

// recordedWait is how long these tests wait for the program to draw what it
// was told about: generous next to the tenth of a second the loop takes to
// answer a change, and short enough that a test waiting for something that
// never happens fails instead of hanging.
const recordedWait = 3 * time.Second

// firmwareOut is the message the chat of these tests receives. It is short
// because a row of the chat list on a two-pane screen has room for about
// twenty columns of preview.
const firmwareOut = "firmware out"

// recordedChatTitle is the name of the n-th chat of the fixture.
func recordedLoopChatTitle(id int) string {
	return "chat " + string(rune('a'+id-1))
}

// recordedAccount feeds a live store the way the session pump does: the
// chats of the main list, in the order TDLib keeps them in.
func recordedLoopAccount(t *testing.T) *telegram.LiveState {
	t.Helper()

	store := telegram.NewLiveState()
	for id := 1; id <= recordedChats; id++ {
		applyRecorded(t, store, recordedChat(
			id, recordedLoopChatTitle(id), 1000-id, 0, false, "chatTypePrivate",
		))
	}

	return store
}

// recordedIncomingMessage is the whole of what TDLib sends when a message
// arrives in a chat that is not the open one.
//
// The order is the order TDLib uses: the message itself, the last message
// of the chat with the positions that carry it to the top of the list, the
// position on its own, and the unread count. Three of the four are about
// the row of the list; the first is about the feed of the open chat.
func recordedIncomingMessage(
	t *testing.T,
	store *telegram.LiveState,
	chatID int,
	text string,
) {
	t.Helper()

	applyRecorded(t, store, recordedNewMessage(chatID, 9000+chatID, 1700000900, text))
	applyRecorded(t, store, recordedLastMessage(
		chatID, 9000+chatID, 1700000900, 9000+chatID, text,
	))
	applyRecorded(t, store, recordedPosition(chatID, 9000+chatID, false))
	applyRecorded(t, store, recordedReadInbox(chatID, 1))
}

// A message in a chat that is not the open one moves the list on the
// screen: the chat is the top row of the list, with the words of the
// message and its unread count, and nothing is pressed between the update
// and the frame.
//
// The cursor is on the first chat, which is where it is when the list has
// been loaded and nobody has pressed anything.
func TestAnIncomingMessageMovesTheChatListWithTheRealStore(t *testing.T) {
	loop := startRecordedLoop(t, recordedLoopAccount(t), nil, nil)

	recordedIncomingMessage(t, loop.store, 14, firmwareOut)

	loop.waitForRows(t, "the chat that received the message", func(rows []string) bool {
		return len(rows) > 0 && strings.Contains(rows[0], recordedLoopChatTitle(14))
	})
}

// The same with the cursor five rows down the list: the frame changes
// without a key, and the chat the reader was on is still on the screen.
//
// The arrival itself is above the window here, and that is the geometry
// rather than a rule: a window of six chats cannot hold the top of a list of
// twenty and the sixth chat at the same time. What a person must not see is
// a list that stands still while the list behind it changed — that is the
// report this task is about. What is left for a reader that far down is a
// marker of messages that arrived above the window, which the chat list has
// no room for and this task does not add.
func TestAnIncomingMessageChangesTheFrameWithTheCursorBelowTheTop(t *testing.T) {
	down := tea.KeyMsg{Type: tea.KeyDown}
	loop := startRecordedLoop(t, recordedLoopAccount(t), nil, []tea.KeyMsg{
		down, down, down, down, down,
	})

	before := loop.chatListRows()
	if len(before) < 2 {
		t.Fatalf(
			"the list on the screen is %d rows:\n%s",
			len(before), strings.Join(before, "\n"),
		)
	}

	recordedIncomingMessage(t, loop.store, 14, firmwareOut)

	loop.waitForRows(t, "a list that moved", func(rows []string) bool {
		return !sameRows(rows, before)
	})

	after := loop.chatListRows()
	if sameRows(after, before) {
		t.Fatalf(
			"the list on the screen did not change:\n%s", strings.Join(after, "\n"),
		)
	}
	if !rowCarries(after, recordedLoopChatTitle(6)) {
		t.Errorf(
			"the chat the reader was on is not on the screen any more:\n%s",
			strings.Join(after, "\n"),
		)
	}
}

// sameRows reports whether two readings of the chat list are the same rows
// in the same order.
func sameRows(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for index := range a {
		if a[index] != b[index] {
			return false
		}
	}

	return true
}

// rowCarries reports whether one of the rows carries the text.
func rowCarries(rows []string, text string) bool {
	for _, row := range rows {
		if strings.Contains(row, text) {
			return true
		}
	}

	return false
}

// The screen does not stand still for the name of a sender.
//
// The session below answers with a name in five seconds — the check the task
// asks for, at the size a gate can afford — and the program is the real one
// over the real loop. While that answer is on its way the frame must keep
// being drawn and must keep answering keys: the defect this is against is
// the loop asking TDLib for a name and standing there until TDLib answered
// (FINDING-T1, auditor, main fdadd25), and a screen that stops is what a
// person notices first. The name then reaches the message it belongs to.
func TestTheScreenKeepsDrawingWhileTheNameOfASenderIsOnItsWay(t *testing.T) {
	names := &slowNameReader{after: loopNameAnswer, name: "Marta Ivanova"}
	loop := startRecordedLoop(t, recordedLoopAccount(t), names, []tea.KeyMsg{
		{Type: tea.KeyEnter},
	})

	recordedIncomingMessage(t, loop.store, 1, firmwareOut)

	// The message is on the screen while its sender has no name to show, and
	// it is there long before the session has answered.
	loop.waitForFrame(t, "the message that arrived", liveLoopBudget*4,
		func(frame string) bool {
			return strings.Contains(frame, firmwareOut)
		})

	// A key pressed in the same stretch of time is answered in it: the loop
	// was not waiting for TDLib, it was drawing and taking keys.
	loop.program.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(typedText)})
	loop.waitForFrame(t, "the key that was pressed", liveLoopBudget*4,
		func(frame string) bool {
			return strings.Contains(frame, typedText)
		})

	// And the name that took five seconds is above the message it belongs
	// to, without the chat being read again and without a key pressed.
	loop.waitForFrame(t, "the name that arrived", loopNameAnswer+recordedWait,
		func(frame string) bool {
			return strings.Contains(frame, "Marta Ivanova")
		})
}

// loopNameAnswer is how long the session of the loop above takes to answer
// with the name of a sender.
//
// It is the five seconds of the manual check: a name that takes seconds is a
// name the program is not standing still for, and nothing in the program
// knows how long this is.
const loopNameAnswer = 5 * time.Second

// typedText is what the key of the same test presses into the composer.
//
// It is two letters that no row of the fixture carries, so the frame that
// holds it holds it because the program answered the key and not because the
// screen said it for another reason.
const typedText = "qx"

// The diagnostic of the live list writes every step of one change, in the
// order the steps happen in.
//
// The owner's next run is a run of a real account with the diagnostic on,
// and the file it writes is the only answer this defect has until then: if
// a step is missing between two that are there, that step is where the change
// stops. So the file has to carry the whole chain — the program with a live
// state behind it, the update that arrived, the signal it posted, the wait
// that returned, the message the interface was given, the window it placed,
// and whether the frame changed.
func TestTheDiagnosticCarriesEveryStepOfAChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "live.log")
	t.Setenv(livewatch.EnvVar, path)

	loop := startRecordedLoop(t, recordedLoopAccount(t), nil, nil)
	recordedIncomingMessage(t, loop.store, 14, firmwareOut)

	loop.waitForRows(t, "the chat that received the message", func(rows []string) bool {
		return len(rows) > 0 && strings.Contains(rows[0], recordedLoopChatTitle(14))
	})

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the diagnostic: %v", err)
	}

	for _, step := range []string{
		livewatch.StepProgram,
		livewatch.StepStoreUpdate,
		livewatch.StepStoreSignal,
		livewatch.StepWait,
		livewatch.StepWaitArmed,
		livewatch.StepLiveChanged,
		livewatch.StepChatList,
		livewatch.StepRepaintDrawn,
		livewatch.StepFrameChanged,
		livewatch.StepRepaint,
	} {
		if !strings.Contains(string(written), step) {
			t.Errorf("the diagnostic has no line of %q:\n%s", step, written)
		}
	}

	// The update that moves the row is named, not counted as other traffic,
	// and the frame is said to have changed.
	if !strings.Contains(string(written), "type="+livewatch.UpdateChatLastMessage) {
		t.Errorf("the diagnostic does not name updateChatLastMessage:\n%s", written)
	}
	if !strings.Contains(string(written), "changed=yes") {
		t.Errorf("the diagnostic does not say the frame changed:\n%s", written)
	}
	if !strings.Contains(string(written), "type="+livewatch.UpdateNewMessage) {
		t.Errorf("the diagnostic does not name updateNewMessage:\n%s", written)
	}

	// Nothing anybody wrote reaches the file: no text of a message, no name
	// of a chat, and no identifier behind the hash. The hash itself is
	// checked field by field rather than by a search for the number,
	// because a number of a chat is a substring of a time of day.
	for _, line := range strings.Split(string(written), "\n") {
		for _, secret := range []string{firmwareOut, recordedLoopChatTitle(14)} {
			if strings.Contains(line, secret) {
				t.Errorf("the diagnostic carries %q:\n%s", secret, line)
			}
		}
		for _, field := range strings.Fields(line) {
			_, value, isChatField := strings.Cut(field, "chat=")
			if !isChatField {
				continue
			}
			if value != "-" && !isChatHash(value) {
				t.Errorf("the chat field is %q, want a hash or a dash", value)
			}
		}
	}
}

// isChatHash reports whether a value is a chat hash: eight hexadecimal
// columns, which is the width of the hash the diagnostic writes.
func isChatHash(value string) bool {
	if len(value) != 8 {
		return false
	}
	for _, column := range value {
		if !strings.ContainsRune("0123456789abcdef", column) {
			return false
		}
	}

	return true
}

// A change in the store is always a signalled change: the store has no idea
// which chat is open, and nothing in it may depend on that.
//
// The frame on the screen cannot see this part — it is the same whether the
// store signalled once or twenty times — so the sequence is fed to the store
// on its own and every update is asked about on its own. The invariant is
// the one the store documents: an update that changes nothing posts no
// signal, so updateChatPosition here repeats the order the update before it
// has already put in the list and reports changed=no.
func TestAChangeInTheStoreIsAlwaysASignalledChange(t *testing.T) {
	store := recordedLoopAccount(t)
	drainChanged(store)

	updates := []struct {
		name string
		raw  string
	}{
		{"updateNewMessage", recordedNewMessage(14, 9014, 1700000900, firmwareOut)},
		{
			"updateChatLastMessage",
			recordedLastMessage(14, 9014, 1700000900, 9014, firmwareOut),
		},
		{"updateChatPosition", recordedPosition(14, 9014, false)},
		{"updateChatReadInbox", recordedReadInbox(14, 1)},
	}

	moved := 0
	for _, update := range updates {
		changed, err := store.ApplyUpdate(telegram.RawMessage(update.raw))
		if err != nil {
			t.Fatalf("%s: %v", update.name, err)
		}

		signalled := signalled(store)
		if changed != signalled {
			t.Errorf(
				"%s: changed=%v, signalled=%v: a change in the store is a "+
					"signalled change and nothing else is",
				update.name, changed, signalled,
			)
		}
		if changed {
			moved++
		}
	}

	if moved == 0 {
		t.Fatal("no update of an arriving message changed the store")
	}

	first := store.ChatList()[0]
	if first.ID != 14 {
		t.Fatalf("chat %d is first after the message, want 14", first.ID)
	}
	if first.LastMessage == nil || first.LastMessage.Text != firmwareOut {
		t.Fatalf(
			"the first chat carries %+v, want the message that arrived",
			first.LastMessage,
		)
	}
}

// A chat the store has never heard of changes nothing and signals nothing:
// the update that creates a chat is the only one that may create a record,
// and an update about a chat that is not in the store cannot be drawn.
//
// It is the one path through the store where a message can arrive, be
// applied and leave no trace, and it is named here because the diagnostic
// of the live list has to be able to show it: a store line with changed=no
// next to a row that did not move is this, and not a lost wake-up.
func TestAnUpdateAboutAChatTheStoreDoesNotHaveChangesNothing(t *testing.T) {
	store := recordedLoopAccount(t)
	drainChanged(store)

	changed, err := store.ApplyUpdate(telegram.RawMessage(
		recordedLastMessage(999, 9999, 1700000900, 9999, firmwareOut),
	))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if changed {
		t.Error("an update about an unknown chat changed the store")
	}
	if signalled(store) {
		t.Error("an update about an unknown chat signalled a change")
	}
}

// drainChanged takes everything the store has signalled so far, so that a
// test starts from a quiet store.
func drainChanged(store *telegram.LiveState) {
	for {
		select {
		case <-store.Changed():
		default:
			return
		}
	}
}

// signalled reports whether the store has signalled a change that nobody
// has taken yet.
func signalled(store *telegram.LiveState) bool {
	select {
	case <-store.Changed():
		return true
	default:
		return false
	}
}

// recordedLoop is a running Bubble Tea program over a model with a real
// live store behind it.
type recordedLoop struct {
	program *tea.Program
	store   *telegram.LiveState
	screen  *recordedScreen
	stopped chan struct{}
}

// startRecordedLoop runs the program over the real store of a session and
// returns it once the list is on the screen.
//
// liveNames is who the session answers the name of a sender with, and is nil
// for a session that cannot name anybody: the live store keeps no names
// (#41), so a session with no reader draws "Unknown" above the messages it
// brings and nothing else changes.
//
// keys are pressed before the loop is returned, and they are the only keys
// there are: after this, nothing is pressed, so what the screen says is
// what the program did by itself.
func startRecordedLoop(
	t *testing.T,
	store *telegram.LiveState,
	liveNames TelegramSenderNames,
	keys []tea.KeyMsg,
) *recordedLoop {
	t.Helper()

	live, err := NewTelegramLiveUpdates(t.Context(), store, liveNames)
	if err != nil {
		t.Fatalf("NewTelegramLiveUpdates: %v", err)
	}

	model, err := tui.NewModelWithDependencies(context.Background(), tui.Dependencies{
		Source:           recordedSource{},
		MessageSubmitter: recordedSubmitter{},
		LiveUpdates:      live,
		Theme:            theme.DefaultTheme().ForProfile(theme.ProfileNoColor),
		ColorProfile:     theme.ProfileNoColor,
	})
	if err != nil {
		t.Fatalf("NewModelWithDependencies: %v", err)
	}

	loop := &recordedLoop{
		store:   store,
		screen:  &recordedScreen{},
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

	t.Cleanup(func() {
		loop.program.Quit()
		<-loop.stopped
	})

	// The size is the terminal answering, not a user pressing anything.
	loop.program.Send(tea.WindowSizeMsg{Width: 100, Height: 24})
	loop.waitForRows(t, "the loaded list", func(rows []string) bool {
		return len(rows) > 0
	})

	for _, key := range keys {
		loop.program.Send(key)
	}
	if len(keys) > 0 {
		loop.waitForRows(t, "the list after the keys", func(rows []string) bool {
			return len(rows) > 1
		})
	}

	return loop
}

// waitForRows waits until the rows of the chat list satisfy the condition,
// and fails with the rows it saw when they never do.
func (l *recordedLoop) waitForRows(
	t *testing.T,
	what string,
	condition func(rows []string) bool,
) {
	t.Helper()

	deadline := time.Now().Add(recordedWait)
	for {
		rows := l.chatListRows()
		if condition(rows) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf(
				"the program never drew %s. The list on the screen:\n%s",
				what, strings.Join(rows, "\n"),
			)
		}

		time.Sleep(10 * time.Millisecond)
	}
}

// waitForFrame waits until the newest frame satisfies the condition within
// the given time, and fails with the frame it saw when it does not.
//
// The bound is a parameter because the two halves of the check below are
// about different amounts of time: a frame the program owes its reader now,
// and a name that a session answers in seconds.
func (l *recordedLoop) waitForFrame(
	t *testing.T,
	what string,
	within time.Duration,
	condition func(frame string) bool,
) {
	t.Helper()

	deadline := time.Now().Add(within)
	for {
		frame := l.newestFrame()
		if condition(frame) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf(
				"the program never drew %s within %s. The frame on the screen:\n%s",
				what, within, frame,
			)
		}

		time.Sleep(10 * time.Millisecond)
	}
}

// newestFrame returns what the program drew last: everything from the last
// heading of the chat list down, which is where the frame the renderer
// wrote last begins.
//
// The screen is a writer the frames are written into one after another, so
// the whole of it is every frame the program ever drew — and a frame that
// says what an earlier one said is not a fact about the program now.
func (l *recordedLoop) newestFrame() string {
	lines := strings.Split(ansi.Strip(l.screen.String()), "\n")

	heading := -1
	for index, line := range lines {
		if strings.Contains(line, "Chats") {
			heading = index
		}
	}
	if heading < 0 {
		return ""
	}

	return strings.Join(lines[heading:], "\n")
}

// chatListRows returns the rows of the chat list of the newest frame.
//
// The frames of the renderer are written whole, from the top of the screen
// down, and each of them begins with the heading of the list — so the lines
// after the last heading are the frame the program drew last. The heading,
// the search hint, the rule under them and the air between the chats are not
// rows.
func (l *recordedLoop) chatListRows() []string {
	lines := strings.Split(ansi.Strip(l.screen.String()), "\n")

	heading := -1
	for index, line := range lines {
		if strings.Contains(line, "Chats") {
			heading = index
		}
	}
	if heading < 0 {
		return nil
	}

	var rows []string
	for _, line := range lines[heading+1:] {
		switch {
		case strings.Contains(line, "/ search"):
		case strings.Contains(line, strings.Repeat("━", 3)):
		case strings.TrimSpace(line) == "":
		default:
			rows = append(rows, line)
		}
	}

	return rows
}

// recordedScreen is a writer that is safe to read while it is written,
// which is what the frames of the renderer are read through.
type recordedScreen struct {
	mu      sync.Mutex
	written strings.Builder
}

func (s *recordedScreen) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.written.Write(p)
}

func (s *recordedScreen) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.written.String()
}

// recordedSource is the chat list as it is loaded at startup: the chats of
// the main list, in the order TDLib keeps them in.
type recordedSource struct{}

func (recordedSource) ListChats(context.Context) ([]tui.Chat, error) {
	chats := make([]tui.Chat, 0, recordedChats)
	for id := 1; id <= recordedChats; id++ {
		chats = append(chats, tui.Chat{
			ID:    int64(id),
			Title: recordedLoopChatTitle(id),
			At:    time.Unix(1700000000-int64(id)*60, 0).UTC(),
		})
	}

	return chats, nil
}

func (recordedSource) LoadHistory(
	context.Context, int64, int64, int,
) (tui.HistoryPage, error) {
	return tui.HistoryPage{}, nil
}

func (recordedSource) SendMessage(
	context.Context, int64, string,
) (tui.Message, error) {
	return tui.Message{}, nil
}

// recordedSubmitter is a queue nothing is asked to send to: these tests are
// about the list of chats, and a send is a key press the loop must not need.
type recordedSubmitter struct{}

func (recordedSubmitter) SubmitMessage(
	context.Context, int64, string,
) (tui.Submission, error) {
	return tui.Submission{ID: strconv.Itoa(1), State: tui.SubmissionQueued}, nil
}

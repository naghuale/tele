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
// It keeps two more things, both of which are what TDLib keeps and what a
// screen shows: which chats are open right now, and how many calls were in
// flight at once. A chat counts its online members only while it is open, so
// the chats that are open at the end are the presence in the header, and two
// calls in flight at once are two switches that have crossed.
//
// The order is written under a lock because the members of a batch are
// goroutines: reading a list that two of them append to is a race, and a race
// in a test of order proves nothing about the order.
type slowCloseOpener struct {
	mu        sync.Mutex
	calls     []string
	openNow   map[int64]bool
	inFlight  int
	crossings int

	closeRoundTrip time.Duration
}

func newSlowCloseOpener(roundTrip time.Duration) *slowCloseOpener {
	return &slowCloseOpener{
		openNow:        map[int64]bool{},
		closeRoundTrip: roundTrip,
	}
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
	defer o.answered(chatID, true)

	return nil
}

func (o *slowCloseOpener) CloseChat(_ context.Context, chatID int64) error {
	o.remember(fmt.Sprintf("close %d", chatID))
	defer o.answered(chatID, false)

	// The round trip is after the call has been made and before it has come
	// back: that is where another call can reach the opener beside it.
	time.Sleep(o.closeRoundTrip)

	return nil
}

// remember writes a call down the moment it is asked for, which is the moment
// it reaches TDLib, and counts how many calls are in flight at that moment.
func (o *slowCloseOpener) remember(call string) {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.calls = append(o.calls, call)
	o.inFlight++
	if o.inFlight > 1 {
		o.crossings++
	}
}

// answered is a call that has come back from TDLib: the chat is open or it is
// not, and one call fewer is in flight.
func (o *slowCloseOpener) answered(chatID int64, open bool) {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.inFlight--
	o.openNow[chatID] = open
}

// asked is what the opener was asked for, in the order it was asked.
func (o *slowCloseOpener) asked() []string {
	o.mu.Lock()
	defer o.mu.Unlock()

	return slices.Clone(o.calls)
}

// counting is which chats TDLib would be counting right now, in order.
func (o *slowCloseOpener) counting() []int64 {
	o.mu.Lock()
	defer o.mu.Unlock()

	counted := make([]int64, 0, len(o.openNow))
	for chatID, isOpen := range o.openNow {
		if isOpen {
			counted = append(counted, chatID)
		}
	}
	slices.Sort(counted)

	return counted
}

// crossed says whether two calls were in flight at the same time.
func (o *slowCloseOpener) crossed() bool {
	o.mu.Lock()
	defer o.mu.Unlock()

	return o.crossings > 0
}

// lastCallAbout is the last call the opener was asked for about one chat, or
// an empty string if it was never asked about it.
func (o *slowCloseOpener) lastCallAbout(chatID int64) string {
	o.mu.Lock()
	defer o.mu.Unlock()

	for _, call := range slices.Backward(o.calls) {
		if strings.HasSuffix(call, fmt.Sprintf(" %d", chatID)) {
			return call
		}
	}

	return ""
}

// gatedOpener is a presence opener whose calls wait at a gate until the test
// opens it, and that keeps what TDLib keeps: which chats are open, and how
// many calls were in flight at once.
//
// The gate is TDLib on a round trip that is not coming back soon, which is a
// thing a person lives with rather than a thing a test usually sees: a network
// that is down, a proxy that holds a connection. It is the case the interface
// has to keep answering keys in, and the case in which switches pile up behind
// one another.
//
// It also says which calls have reached it, so that a test can wait for the
// queue to be inside a turn rather than hope it is: a test that does not know
// that the first switch is in flight cannot say what the switches behind it
// were overtaken by.
type gatedOpener struct {
	mu        sync.Mutex
	calls     []string
	openNow   map[int64]bool
	inFlight  int
	crossings int

	// gate is closed until the test opens it. Every call waits on it, so a
	// call that was asked for before the gate opened is still in flight when
	// the gate opens, and a call asked for after it goes straight through.
	gate chan struct{}

	// entered is where a call that has reached the opener says which call it
	// is.
	entered chan string
}

func newGatedOpener() *gatedOpener {
	return &gatedOpener{
		openNow: map[int64]bool{},
		gate:    make(chan struct{}),
		entered: make(chan string, gatedOpenerCalls),
	}
}

// gatedOpenerCalls is how many calls the opener can report having reached it
// without a test listening, which is a hundred switches and a few to spare.
const gatedOpenerCalls = 128

// openTheGate lets every call that is waiting go through, and every call after
// it.
func (o *gatedOpener) openTheGate() {
	close(o.gate)
}

func (o *gatedOpener) OpenChat(_ context.Context, chatID int64) error {
	call := fmt.Sprintf("open %d", chatID)
	o.remember(call)
	defer o.answered(chatID, true)

	o.announce(call)
	<-o.gate

	return nil
}

func (o *gatedOpener) CloseChat(_ context.Context, chatID int64) error {
	call := fmt.Sprintf("close %d", chatID)
	o.remember(call)
	defer o.answered(chatID, false)

	o.announce(call)
	<-o.gate

	return nil
}

// announce says which call has reached the opener. It waits for nothing: a
// test that is not listening to a call loses that call and not the opener.
func (o *gatedOpener) announce(call string) {
	select {
	case o.entered <- call:
	default:
	}
}

// waitForCall waits until the opener has been asked for that call, which is
// the moment the queue is inside a turn: nothing asked for after it can be
// carried out before it has finished.
func (o *gatedOpener) waitForCall(t *testing.T, call string) {
	t.Helper()

	select {
	case entered := <-o.entered:
		if entered != call {
			t.Fatalf("the first call the opener was asked for was %q, want %q", entered, call)
		}
	case <-time.After(commandDeadline):
		t.Fatalf("the opener was never asked for %q", call)
	}
}

// remember writes a call down the moment it is asked for and counts how many
// calls are in flight at that moment.
func (o *gatedOpener) remember(call string) {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.calls = append(o.calls, call)
	o.inFlight++
	if o.inFlight > 1 {
		o.crossings++
	}
}

// answered is a call that has come back: the chat is open or it is not, and
// one call fewer is in flight.
func (o *gatedOpener) answered(chatID int64, open bool) {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.inFlight--
	o.openNow[chatID] = open
}

// asked is what the opener was asked for, in the order it was asked.
func (o *gatedOpener) asked() []string {
	o.mu.Lock()
	defer o.mu.Unlock()

	return slices.Clone(o.calls)
}

// counting is which chats TDLib would be counting right now, in order.
func (o *gatedOpener) counting() []int64 {
	o.mu.Lock()
	defer o.mu.Unlock()

	counted := make([]int64, 0, len(o.openNow))
	for chatID, isOpen := range o.openNow {
		if isOpen {
			counted = append(counted, chatID)
		}
	}
	slices.Sort(counted)

	return counted
}

// crossed says whether two calls were in flight at the same time.
func (o *gatedOpener) crossed() bool {
	o.mu.Lock()
	defer o.mu.Unlock()

	return o.crossings > 0
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

// runInBackground starts a command the way Bubble Tea starts one: in a
// goroutine of its own, so that the commands of two updates are in flight
// together rather than one after another.
//
// It is what runLikeBubbleTea does for the members of a batch, one level up:
// two switches are two commands of two updates, and nothing joins them.
func runInBackground(cmd tea.Cmd) <-chan struct{} {
	finished := make(chan struct{})

	go func() {
		defer close(finished)

		if cmd == nil {
			return
		}
		runResult(cmd())
	}()

	return finished
}

// waitFor waits for a command that was started in a goroutine, and says so
// rather than hanging the suite when it never finishes.
func waitFor(t *testing.T, finished <-chan struct{}) {
	t.Helper()

	select {
	case <-finished:
	case <-time.After(commandDeadline):
		t.Fatal("a command was started and never finished")
	}
}

// commandDeadline is how long a test waits for a command that was started in a
// goroutine, and how long it waits for an Update to come back. It is not a
// measure of speed: it is there so that a command that never finishes, or an
// Update that never returns, fails the test instead of hanging the suite.
const commandDeadline = 5 * time.Second

// updateWithin is Update with a bound on how long it may take.
//
// Update is what a key press goes through, and it is what has to come back
// while TDLib is on a round trip: a person walking through chats cannot be
// held at a key because the network is down. The bound is five seconds
// because it is a bound and not a measurement — an Update of this model takes
// microseconds, and one that takes seconds is one that is waiting for
// something it must not wait for.
func updateWithin(t *testing.T, model Model, msg tea.Msg) (Model, tea.Cmd) {
	t.Helper()

	type answered struct {
		model Model
		cmd   tea.Cmd
	}
	updates := make(chan answered, 1)
	go func() {
		updated, cmd := model.Update(msg)

		next, isModel := updated.(Model)
		if !isModel {
			t.Errorf("Update returned %T, want Model", updated)

			return
		}
		updates <- answered{model: next, cmd: cmd}
	}()

	select {
	case got := <-updates:
		return got.model, got.cmd
	case <-time.After(commandDeadline):
		t.Fatalf("Update did not come back within %s of %T", commandDeadline, msg)

		return model, nil
	}
}

// isSubsequence says whether every call of got is one of the calls of want, in
// that order. A queue that coalesces switches carries out fewer calls than the
// presses asked for — that is what it is for — but never a call the presses
// did not ask for and never two of them out of order.
func isSubsequence(want, got []string) bool {
	rest := want
	for _, call := range got {
		found := slices.Index(rest, call)
		if found < 0 {
			return false
		}
		rest = rest[found+1:]
	}

	return true
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

	return openerModelOfChats(t, opener, width, twoChats)
}

// twoChats are the chats a test of two switches walks between: A and B.
var twoChats = []Chat{
	{ID: 7, Title: "A"},
	{ID: 8, Title: "B"},
}

// openerModelOfChats is the same model with a list of any length, because a
// walk of twenty switches needs twenty chats and a walk of two needs two.
func openerModelOfChats(
	t *testing.T,
	opener ChatPresenceOpener,
	width int,
	chats []Chat,
) Model {
	t.Helper()

	deps := Dependencies{
		Source:           &fakeChatSource{chats: chats},
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
	model, _ = updateModel(t, model, chatsLoadedMsg{chats: chats})

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

// Walking the cursor in the chat list opens nothing in Telegram. It used to
// open the chat under the cursor, with the chat that was open closed before
// it, and that was how a badge fell under a cursor that was only passing a
// chat by: the open and, once its page was on the screen, the read of the
// whole visible window (the owner's account on 03.10, real account, #75).
//
// What is asked of TDLib is asked by the keys that open a chat, and the test
// below walks the list first and enters afterwards — which is also the order
// a person walks it in.
func TestWalkingTheListAsksTDLibForNothing(t *testing.T) {
	opener := &recordingOpener{}
	model := openerModel(t, opener)

	model, cmd := updateModel(t, model, press(tea.KeyEnter))
	runCommands(t, cmd)
	open := len(opener.opened)

	// The keys are on the chat list, which is where a user walks through
	// chats, and a walk stops on every one of them.
	model.focus = FocusChatList
	for range 2 {
		model, cmd = updateModel(t, model, press(tea.KeyDown))
		runCommands(t, cmd)
	}

	if got := len(opener.opened); got != open {
		t.Fatalf("opened = %v, want the one chat that was entered", opener.opened)
	}
	if len(opener.closed) != 0 {
		t.Fatalf("closed = %v, want nothing: a walk closes no chat", opener.closed)
	}
	if model.openedChat != 7 {
		t.Fatalf("openedChat = %d, want the chat that was entered 7", model.openedChat)
	}
}

// Entering another chat closes the chat that was left and opens the one that
// is, which is the whole of what an entry is to TDLib.
func TestEnteringAnotherChatClosesTheOldOneAndOpensTheNew(t *testing.T) {
	opener := &recordingOpener{}
	model := openerModel(t, opener)

	model, cmd := updateModel(t, model, press(tea.KeyEnter))
	runCommands(t, cmd)

	// The keys are on the chat list, the cursor walks to the second chat, and
	// Enter enters it.
	model.focus = FocusChatList
	model, cmd = updateModel(t, model, press(tea.KeyDown))
	runCommands(t, cmd)
	model, cmd = updateModel(t, model, press(tea.KeyEnter))
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
	opener := newSlowCloseOpener(closeRoundTrip)
	model := openerModel(t, opener)

	// The first chat has no chat before it, so nothing is closed here and
	// this open is not the question.
	model, cmd := updateModel(t, model, press(tea.KeyEnter))
	runCommands(t, cmd)

	// The keys are on the chat list, the cursor walks to the second chat —
	// which is a look and asks nothing — and Enter enters it.
	model.focus = FocusChatList
	model, _ = updateModel(t, model, press(tea.KeyDown))
	_, cmd = updateModel(t, model, press(tea.KeyEnter))
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

// Two switches in a row are two commands of Bubble Tea, and Bubble Tea runs
// the commands of two updates at the same time. So the close that belongs to
// the first switch and the open that belongs to the second one can cross: a
// user who walks A -> B -> A quickly came back to a chat that the late close
// of the first switch closed under them, and with it went the presence in
// the header and the read marks an open of that chat makes on every device
// of the account (the auditor's RECHECK-FINDING-T6 on main adab9ea, the
// recheck of #67, 03.10).
//
// Ordering the two calls of one switch, as the change before this one did,
// is not enough: the order that was asked for is the order within a command,
// and nothing orders two commands with respect to each other. What TDLib
// ends up counting has to be the chat the user is in, and it can only be
// that if the calls of two switches do not happen at the same time.
func TestTwoQuickSwitchesLeaveOnlyTheChosenChatOpen(t *testing.T) {
	opener := newSlowCloseOpener(closeRoundTrip)
	model := openerModel(t, opener)

	// Three switches in three updates, and none of them waited for: this is
	// a hand walking A -> B -> A, which is faster than a round trip to
	// TDLib. A walk in the list is a look and asks nothing, so it is run
	// and none of the three commands below is run before the next switch.
	model, enterFirst := updateModel(t, model, press(tea.KeyEnter))
	model.focus = FocusChatList
	model, look := updateModel(t, model, press(tea.KeyDown))
	runCommands(t, look)
	model, enterSecond := updateModel(t, model, press(tea.KeyEnter))
	model.focus = FocusChatList
	model, look = updateModel(t, model, press(tea.KeyUp))
	runCommands(t, look)
	model, enterFirstAgain := updateModel(t, model, press(tea.KeyEnter))

	// The three commands are started the way Bubble Tea starts them: in
	// goroutines of their own, all three at once.
	intoFirst := runInBackground(enterFirst)
	intoSecond := runInBackground(enterSecond)
	intoFirstAgain := runInBackground(enterFirstAgain)
	waitFor(t, intoFirst)
	waitFor(t, intoSecond)
	waitFor(t, intoFirstAgain)

	if got := opener.counting(); !slices.Equal(got, []int64{7}) {
		t.Fatalf(
			"TDLib would be counting %v, want only the chat the user is in [7]: a chat left open is a presence under the wrong header and a chat closed late loses its read marks",
			got,
		)
	}
	if got := opener.lastCallAbout(7); got != "open 7" {
		t.Fatalf(
			"the last thing TDLib was told about the chat the user is in is %q, want %q: nothing closes that chat after it has been opened again",
			got,
			"open 7",
		)
	}
	// The calls of the three switches, and the queue may carry out fewer of
	// them than there are presses: a switch that was overtaken before the
	// queue came to it is not asked for at all. What it may never do is ask
	// for something the presses did not ask for, or ask for two of them out
	// of order, so the calls have to be a part of that list in that order.
	want := []string{"open 7", "close 7", "open 8", "close 8", "open 7"}
	if got := opener.asked(); !isSubsequence(want, got) {
		t.Fatalf(
			"the opener was asked %v, want the calls of the three switches in that order %v: the calls of two switches reach TDLib in the order the chats were entered",
			got,
			want,
		)
	}
	if opener.crossed() {
		t.Fatal(
			"two calls to TDLib were in flight at once: the close of one switch crossed the open of the next",
		)
	}
	if model.openedChat != 7 {
		t.Fatalf("openedChat = %d, want the chat the user came back to 7", model.openedChat)
	}
}

// TDLib is not always quick, and it is not always there at all: a network that
// is down or a proxy that holds a connection leaves a round trip open for as
// long as the person who is trying to walk through chats can keep pressing
// keys.
//
// So a switch must not wait for TDLib, not even by a queue of its own: twenty
// switches in twenty Updates have to come back as twenty answers from Update,
// with TDLib stuck behind the first one the whole time. What TDLib is told
// afterwards is the last chat the user entered and nothing else — the chats
// that were overtaken were never opened, which is the point of coalescing a
// switch rather than queueing it.
func TestTwentyQuickSwitchesBehindABlockedOpenerKeepTheLastOne(t *testing.T) {
	opener := newGatedOpener()
	model := openerModelOfChats(t, opener, 100, numberedChats(quickSwitches))

	var commands []tea.Cmd
	model, entering := updateWithin(t, model, press(tea.KeyEnter))
	commands = append(commands, entering)
	// The first switch is in flight and is not coming back yet, which is what
	// the nineteen switches behind it have to get past.
	opener.waitForCall(t, "open 1")

	for range quickSwitches - 1 {
		model.focus = FocusChatList
		model, _ = updateWithin(t, model, press(tea.KeyDown))
		model, entering = updateWithin(t, model, press(tea.KeyEnter))
		commands = append(commands, entering)
	}

	// TDLib answers at last, and every command comes back: the ones for the
	// switches that were overtaken are given nothing to wait for, which is
	// not the same as never coming back.
	opener.openTheGate()
	for _, command := range commands {
		waitFor(t, runInBackground(command))
	}

	// The first chat was opened, the last chat is the one that is open, and
	// the eighteen chats in between were never opened at all.
	want := []string{"open 1", "close 1", "open 20"}
	if got := opener.asked(); !slices.Equal(got, want) {
		t.Fatalf(
			"the opener was asked %v, want %v: the switches that were overtaken are not carried out",
			got,
			want,
		)
	}
	if got := opener.counting(); !slices.Equal(got, []int64{20}) {
		t.Fatalf(
			"TDLib would be counting %v, want only the chat the user is in [20]",
			got,
		)
	}
	if opener.crossed() {
		t.Fatal(
			"two calls to TDLib were in flight at once: a round trip that is not coming back is still a round trip",
		)
	}
	if model.openedChat != 20 {
		t.Fatalf("openedChat = %d, want the chat that was entered last 20", model.openedChat)
	}
}

// Leaving the program closes the chat TDLib has, not the chat the model
// remembers, and it does so with switches still waiting behind it: the way out
// is the last thing wanted, so the queue does that one thing and nothing else.
func TestQuittingClosesTheOpenChatWhileSwitchesAreWaiting(t *testing.T) {
	opener := newGatedOpener()
	model := openerModelOfChats(t, opener, 100, numberedChats(3))

	// One switch is in flight behind a gate that is closed, and two more are
	// waiting behind it.
	model, entering := updateWithin(t, model, press(tea.KeyEnter))
	opener.waitForCall(t, "open 1")
	model.focus = FocusChatList
	model, _ = updateWithin(t, model, press(tea.KeyDown))
	model, leaving := updateWithin(t, model, press(tea.KeyEnter))
	model.focus = FocusChatList
	model, _ = updateWithin(t, model, press(tea.KeyDown))
	model, leavingAgain := updateWithin(t, model, press(tea.KeyEnter))

	// Ctrl+C twice is the deliberate way out (quit.go).
	model, _ = updateWithin(t, model, press(tea.KeyCtrlC))
	quitting, quit := updateWithin(t, model, press(tea.KeyCtrlC))

	opener.openTheGate()
	waitFor(t, runInBackground(quit))
	for _, command := range []tea.Cmd{entering, leaving, leavingAgain} {
		waitFor(t, runInBackground(command))
	}

	want := []string{"open 1", "close 1"}
	if got := opener.asked(); !slices.Equal(got, want) {
		t.Fatalf(
			"the opener was asked %v, want %v: the way out closes the open chat and carries out nothing that was overtaken by it",
			got,
			want,
		)
	}
	if got := opener.counting(); len(got) != 0 {
		t.Fatalf("TDLib would be counting %v, want no chat at all", got)
	}
	if quitting.openedChat != 0 {
		t.Fatalf(
			"openedChat = %d, want 0: the chat is closed, not merely left open",
			quitting.openedChat,
		)
	}
}

// A switch that was overtaken is a switch about a chat the user has already
// left: nothing was asked of TDLib for it, so there is nothing to answer it
// with. The command it returned still comes back — a command that waits for an
// answer that will never come is a goroutine that waits for the rest of the
// program.
func TestAnOvertakenSwitchComesBackWithNothing(t *testing.T) {
	opener := newGatedOpener()
	queue := newChatLifecycleQueue(opener, &recordingWriter{}, context.Background())

	// The first switch is carried out, and the two behind it are not: the
	// queue is inside its first turn when the other two are asked for, and
	// the gate is closed until all three have been.
	carried := queue.want(2)
	opener.waitForCall(t, "open 2")
	overtaken := queue.want(3)
	wanted := queue.want(4)

	opener.openTheGate()

	if msg := waitForAnswer(t, carried); !isChatOpened(msg, 2) {
		t.Fatalf("the switch that was carried out was answered %v, want an open of chat 2", msg)
	}
	if msg := waitForAnswer(t, overtaken); msg != nil {
		t.Fatalf(
			"the overtaken switch was answered %v, want nothing: the chat it was about is not the chat the user is in",
			msg,
		)
	}
	if msg := waitForAnswer(t, wanted); !isChatOpened(msg, 4) {
		t.Fatalf("the switch that is wanted was answered %v, want an open of chat 4", msg)
	}

	want := []string{"open 2", "close 2", "open 4"}
	if got := opener.asked(); !slices.Equal(got, want) {
		t.Fatalf("the opener was asked %v, want %v", got, want)
	}
}

// isChatOpened says whether a message is the answer of an open of that chat.
func isChatOpened(msg tea.Msg, chatID int64) bool {
	opened, isOpened := msg.(chatOpenedMsg)

	return isOpened && opened.chatID == chatID
}

// waitForAnswer runs a command the way Bubble Tea runs it and hands back what
// it delivered, so that a test can look at the message and not only at the
// fact that the command came back.
func waitForAnswer(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()

	if cmd == nil {
		return nil
	}

	delivered := make(chan tea.Msg, 1)
	go func() {
		delivered <- cmd()
	}()

	select {
	case msg := <-delivered:
		return msg
	case <-time.After(commandDeadline):
		t.Fatal("a command was started and never delivered anything")

		return nil
	}
}

// quickSwitches is how many switches the walk behind a blocked opener makes:
// twenty of them, which is a hand walking a list and not a machine.
const quickSwitches = 20

// numberedChats is a list of count chats, numbered from one, which is a list
// long enough that a walk of twenty switches is not a walk of two.
func numberedChats(count int) []Chat {
	chats := make([]Chat, 0, count)
	for chatID := 1; chatID <= count; chatID++ {
		chats = append(chats, Chat{ID: int64(chatID), Title: fmt.Sprintf("chat %d", chatID)})
	}

	return chats
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

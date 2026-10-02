package tui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func updateModel(t *testing.T, model Model, msg tea.Msg) (Model, tea.Cmd) {
	t.Helper()
	updated, cmd := model.Update(msg)
	mm, ok := updated.(Model)
	if !ok {
		t.Fatalf("Update returned %T, want Model", updated)
	}
	return mm, cmd
}

func press(keyType tea.KeyType) tea.KeyMsg {
	return tea.KeyMsg{Type: keyType}
}

func pressRunes(value string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(value)}
}

func assertQuit(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected quit command")
	}
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Fatalf("command returned %T, want tea.QuitMsg", msg)
	}
}

func TestDownSelectsNextChat(t *testing.T) {
	m := NewModel()
	m, _ = updateModel(t, m, press(tea.KeyDown))
	if m.selectedChat != 1 {
		t.Fatalf("selectedChat = %d, want 1", m.selectedChat)
	}
	m, _ = updateModel(t, m, pressRunes("j"))
	if m.selectedChat != 2 {
		t.Fatalf("selectedChat = %d, want 2", m.selectedChat)
	}
}

func TestUpSelectsPreviousChat(t *testing.T) {
	m := NewModel()
	m.selectedChat = 2
	m, _ = updateModel(t, m, press(tea.KeyUp))
	if m.selectedChat != 1 {
		t.Fatalf("selectedChat = %d, want 1", m.selectedChat)
	}
	m, _ = updateModel(t, m, pressRunes("k"))
	if m.selectedChat != 0 {
		t.Fatalf("selectedChat = %d, want 0", m.selectedChat)
	}
}

func TestChatSelectionDoesNotPassFirst(t *testing.T) {
	m := NewModel()
	m, _ = updateModel(t, m, press(tea.KeyUp))
	if m.selectedChat != 0 {
		t.Fatalf("selectedChat = %d, want 0", m.selectedChat)
	}
}

func TestChatSelectionDoesNotPassLast(t *testing.T) {
	m := NewModel()
	last := len(m.chats) - 1
	m.selectedChat = last
	m, _ = updateModel(t, m, press(tea.KeyDown))
	if m.selectedChat != last {
		t.Fatalf("selectedChat = %d, want %d", m.selectedChat, last)
	}
}

func TestHomeSelectsFirstChat(t *testing.T) {
	m := NewModel()
	m.selectedChat = 2
	m, _ = updateModel(t, m, press(tea.KeyHome))
	if m.selectedChat != 0 {
		t.Fatalf("selectedChat = %d, want 0", m.selectedChat)
	}
}

func TestEndSelectsLastChat(t *testing.T) {
	m := NewModel()
	m, _ = updateModel(t, m, press(tea.KeyEnd))
	if m.selectedChat != len(m.chats)-1 {
		t.Fatalf("selectedChat = %d, want %d", m.selectedChat, len(m.chats)-1)
	}
}

// Opening a chat puts the keys where a conversation is written: the
// composer. A user who chose a chat to reply in and then has to press Tab
// to type has been made to do a step they did not ask for.
func TestEnterOpensSelectedConversationAndFocusesComposer(t *testing.T) {
	m := NewModel()
	m, _ = updateModel(t, m, press(tea.KeyEnter))
	if m.screen != ScreenConversation {
		t.Fatalf("screen = %v, want ScreenConversation", m.screen)
	}
	if m.focus != FocusComposer {
		t.Fatalf("focus = %v, want FocusComposer", m.focus)
	}
	// The conversation opens at its end: the newest message is the one a
	// user opened the chat to read (§8.3).
	if want := len(m.selected().Messages) - 1; m.selectedMsg != want {
		t.Fatalf("selectedMsg = %d, want %d (the newest message)", m.selectedMsg, want)
	}
}

// Esc goes one level out and no further (§4.3): the composer gives up the
// keys to the timeline, and the timeline is what leaves the conversation.
func TestEscapeFromComposerMovesFocusToTimeline(t *testing.T) {
	m := NewModel()
	m, _ = updateModel(t, m, press(tea.KeyEnter))
	m, _ = updateModel(t, m, press(tea.KeyEsc))
	if m.screen != ScreenConversation {
		t.Fatalf("screen = %v, want ScreenConversation", m.screen)
	}
	if m.focus != FocusHistory {
		t.Fatalf("focus = %v, want FocusHistory", m.focus)
	}
}

func TestEscapeFromTimelineLeavesTheConversation(t *testing.T) {
	m := NewModel()
	m, _ = updateModel(t, m, press(tea.KeyEnter))
	m, _ = updateModel(t, m, press(tea.KeyEsc))
	m, _ = updateModel(t, m, press(tea.KeyEsc))
	if m.screen != ScreenChats {
		t.Fatalf("screen = %v, want ScreenChats", m.screen)
	}
	if m.focus != FocusChatList {
		t.Fatalf("focus = %v, want FocusChatList", m.focus)
	}
}

// On a wide screen the timeline is beside the chat list, so Esc from it
// does not throw the conversation away: it hands the keys back to the
// list, and the conversation stays on screen where it was.
func TestEscapeFromTimelineFocusesTheListBesideTheConversation(t *testing.T) {
	m := NewModel()
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	m, _ = updateModel(t, m, press(tea.KeyEnter))
	m, _ = updateModel(t, m, press(tea.KeyEsc))
	m, _ = updateModel(t, m, press(tea.KeyEsc))
	if m.screen != ScreenConversation {
		t.Fatalf("screen = %v, want ScreenConversation", m.screen)
	}
	if m.focus != FocusChatList {
		t.Fatalf("focus = %v, want FocusChatList", m.focus)
	}
}

// The third Esc, on the list beside a conversation, leaves nothing: there
// is nowhere further out to go, and a key that sometimes quits and
// sometimes does not is a key nobody trusts. It says which key quits
// instead of doing nothing quietly, because the owner could not tell the
// two apart on 02.10.
func TestEscapeOnTheListOfAConversationDoesNotLeaveTheProgram(t *testing.T) {
	m := NewModel()
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	m, _ = updateModel(t, m, press(tea.KeyEnter))
	m, _ = updateModel(t, m, press(tea.KeyEsc))
	m, _ = updateModel(t, m, press(tea.KeyEsc))

	m, _ = updateModel(t, m, press(tea.KeyEsc))

	if m.screen != ScreenConversation {
		t.Fatalf("screen = %v, want the conversation beside the list", m.screen)
	}
	if m.focus != FocusChatList {
		t.Fatalf("focus = %v, want FocusChatList", m.focus)
	}
	if m.quitting {
		t.Fatal("Esc on the list left the program")
	}
	if m.notice != noticeQuitKey {
		t.Fatalf("notice = %q, want %q", m.notice, noticeQuitKey)
	}
}

func TestEscapeReturnsFromConversationToChats(t *testing.T) {
	m := NewModel()
	m, _ = updateModel(t, m, press(tea.KeyEnter))
	m, _ = updateModel(t, m, press(tea.KeyEsc))
	m, _ = updateModel(t, m, press(tea.KeyEsc))
	if m.screen != ScreenChats {
		t.Fatalf("screen = %v, want ScreenChats", m.screen)
	}
	if m.focus != FocusChatList {
		t.Fatalf("focus = %v, want FocusChatList", m.focus)
	}
}

// Tab walks the two regions of a conversation and comes back around
// (§4.4). Opening a chat leaves the focus on the composer, so the first
// Tab lands on the timeline.
func TestTabMovesFocusComposerToTimeline(t *testing.T) {
	m := NewModel()
	m, _ = updateModel(t, m, press(tea.KeyEnter))

	m, _ = updateModel(t, m, press(tea.KeyTab))

	if m.focus != FocusHistory {
		t.Fatalf("focus = %v, want FocusHistory", m.focus)
	}

	m, _ = updateModel(t, m, press(tea.KeyTab))

	if m.focus != FocusComposer {
		t.Fatalf("focus = %v, want FocusComposer", m.focus)
	}
}

func TestTabMovesFocusComposerToHistory(t *testing.T) {
	m := NewModel()
	m.screen = ScreenConversation
	m.focus = FocusComposer

	m, _ = updateModel(t, m, press(tea.KeyTab))

	if m.focus != FocusHistory {
		t.Fatalf("focus = %v, want FocusHistory", m.focus)
	}
}

func TestShiftTabMovesFocusHistoryToComposer(t *testing.T) {
	m := NewModel()
	m.screen = ScreenConversation
	m.focus = FocusHistory

	m, _ = updateModel(t, m, press(tea.KeyShiftTab))

	if m.focus != FocusComposer {
		t.Fatalf("focus = %v, want FocusComposer", m.focus)
	}
}

func TestShiftTabMovesFocusComposerToHistory(t *testing.T) {
	m := NewModel()
	m.screen = ScreenConversation
	m.focus = FocusComposer

	m, _ = updateModel(t, m, press(tea.KeyShiftTab))

	if m.focus != FocusHistory {
		t.Fatalf("focus = %v, want FocusHistory", m.focus)
	}
}

func TestConversationUpDownMovesSelectedMessage(t *testing.T) {
	m := NewModel()
	m, _ = updateModel(t, m, press(tea.KeyEnter))
	m.focus = FocusHistory
	last := len(m.selected().Messages) - 1
	m.selectedMsg = 1

	m, _ = updateModel(t, m, press(tea.KeyDown))
	if m.selectedMsg != 2 {
		t.Fatalf("selectedMsg = %d, want 2", m.selectedMsg)
	}
	m, _ = updateModel(t, m, press(tea.KeyUp))
	if m.selectedMsg != 1 {
		t.Fatalf("selectedMsg = %d, want 1", m.selectedMsg)
	}
	m, _ = updateModel(t, m, press(tea.KeyUp))
	if m.selectedMsg != 0 {
		t.Fatalf("selectedMsg = %d, want 0", m.selectedMsg)
	}
	m, _ = updateModel(t, m, press(tea.KeyUp))
	if m.selectedMsg != 0 {
		t.Fatalf("selectedMsg = %d, want 0 at the oldest message", m.selectedMsg)
	}
	if last != 2 {
		t.Fatalf("the mock conversation has %d messages, want 3", last+1)
	}
}

func TestConversationSelectionStaysInBounds(t *testing.T) {
	m := NewModel()
	m, _ = updateModel(t, m, press(tea.KeyEnter))
	m.focus = FocusHistory
	last := len(m.selected().Messages) - 1

	m.selectedMsg = last
	m, _ = updateModel(t, m, press(tea.KeyDown))
	if m.selectedMsg != last {
		t.Fatalf("selectedMsg = %d, want %d", m.selectedMsg, last)
	}

	m.selectedMsg = 0
	m, _ = updateModel(t, m, press(tea.KeyUp))
	if m.selectedMsg != 0 {
		t.Fatalf("selectedMsg = %d, want 0", m.selectedMsg)
	}
}

func TestRunesAppendedToComposerWhenFocused(t *testing.T) {
	m := NewModel()
	m.screen = ScreenConversation
	m.focus = FocusComposer
	m, _ = updateModel(t, m, pressRunes("привет"))
	if m.Composer() != "привет" {
		t.Fatalf("composer = %q", m.Composer())
	}
}

func TestComposerInputOnlyWhenFocused(t *testing.T) {
	m := NewModel()
	m, _ = updateModel(t, m, pressRunes("abc"))
	if m.Composer() != "" {
		t.Fatalf("composer = %q, want empty", m.Composer())
	}

	m.screen = ScreenConversation
	m.focus = FocusHistory
	m, _ = updateModel(t, m, pressRunes("abc"))
	if m.Composer() != "" {
		t.Fatalf("composer = %q, want empty", m.Composer())
	}
}

func TestBackspaceRemovesOneRune(t *testing.T) {
	m := NewModel()
	m.screen = ScreenConversation
	m.focus = FocusComposer
	m, _ = updateModel(t, m, pressRunes("аб"))
	m, _ = updateModel(t, m, press(tea.KeyBackspace))
	if m.Composer() != "а" {
		t.Fatalf("composer = %q, want %q", m.Composer(), "а")
	}
}

func TestCtrlUClearsComposer(t *testing.T) {
	m := NewModel()
	m.screen = ScreenConversation
	m.focus = FocusComposer
	m, _ = updateModel(t, m, pressRunes("hello"))
	m, _ = updateModel(t, m, press(tea.KeyCtrlU))
	if m.Composer() != "" {
		t.Fatalf("composer = %q, want empty", m.Composer())
	}
}

func TestEnterDoesNotClearComposer(t *testing.T) {
	m := NewModel()
	m.screen = ScreenConversation
	m.focus = FocusComposer
	m, _ = updateModel(t, m, pressRunes("draft"))
	m, _ = updateModel(t, m, press(tea.KeyEnter))
	if m.Composer() != "draft" {
		t.Fatalf("composer = %q, want %q", m.Composer(), "draft")
	}
}

// Esc is not a discard: the draft survives every step out of the
// conversation, so a user who went back to the list by mistake does not
// lose what they wrote.
func TestEscapePreservesComposerDraft(t *testing.T) {
	m := NewModel()
	m.screen = ScreenConversation
	m.focus = FocusComposer

	m, _ = updateModel(t, m, pressRunes("черновик"))

	m, _ = updateModel(t, m, press(tea.KeyEsc))
	m, _ = updateModel(t, m, press(tea.KeyEsc))

	if m.screen != ScreenChats {
		t.Fatalf("screen = %v, want ScreenChats", m.screen)
	}
	if got := m.Composer(); got != "черновик" {
		t.Fatalf("composer = %q, want preserved draft %q", got, "черновик")
	}
}

// `q` is a letter in the composer and a quit in the list (§4.3), so a
// message may contain it.
func TestQuitKeyIsALetterInTheComposer(t *testing.T) {
	m := NewModel()
	m.screen = ScreenConversation
	m.focus = FocusComposer

	updated, cmd := m.Update(pressRunes("q"))
	if cmd != nil {
		t.Fatalf("q in the composer returned %T, want no command", cmd())
	}

	m = updated.(Model)
	if got := m.Composer(); got != "q" {
		t.Fatalf("composer = %q, want %q", got, "q")
	}
}

func TestEmptyConversationNavigationDoesNotPanic(t *testing.T) {
	m := NewModel()
	m.chats = []Chat{
		{
			ID:    1,
			Title: "Empty",
		},
	}
	m.screen = ScreenConversation
	m.focus = FocusHistory

	m, _ = updateModel(t, m, press(tea.KeyDown))
	m, _ = updateModel(t, m, press(tea.KeyUp))

	if m.selectedMsg != 0 {
		t.Fatalf("selectedMsg = %d, want 0", m.selectedMsg)
	}
}

func TestSpaceAppendedToComposer(t *testing.T) {
	m := NewModel()
	m.screen = ScreenConversation
	m.focus = FocusComposer

	m, _ = updateModel(t, m, pressRunes("hello"))
	m, _ = updateModel(t, m, press(tea.KeySpace))
	m, _ = updateModel(t, m, pressRunes("world"))

	if got := m.Composer(); got != "hello world" {
		t.Fatalf("composer = %q, want %q", got, "hello world")
	}
}

func TestCtrlCQuitsFromChats(t *testing.T) {
	_, cmd := NewModel().Update(press(tea.KeyCtrlC))
	assertQuit(t, cmd)
}

func TestCtrlCQuitsFromConversation(t *testing.T) {
	m := NewModel()
	m, _ = updateModel(t, m, press(tea.KeyEnter))
	_, cmd := m.Update(press(tea.KeyCtrlC))
	assertQuit(t, cmd)
}

// Esc in the list is the key to leave a conversation with, so it cannot
// also be the key to leave the program (§4.3). It says which key does
// instead, because a key that closes nothing and says nothing is a key the
// user has to try twice (the owner, 02.10).
func TestEscapeInChatsDoesNotQuit(t *testing.T) {
	updated, cmd := NewModel().Update(press(tea.KeyEsc))

	m := updated.(Model)
	if m.quitting {
		t.Fatal("Esc in the list quit the program")
	}
	if m.notice != noticeQuitKey {
		t.Fatalf("notice = %q, want %q", m.notice, noticeQuitKey)
	}

	// The command is the timer that takes the notice away, and whatever it
	// answers with is not the end of the program. It is asked through
	// flattenOne, which drops a timer rather than waiting three seconds
	// for it.
	for _, msg := range flattenOne(t, cmd) {
		if _, isQuit := msg.(tea.QuitMsg); isQuit {
			t.Fatal("Esc in the list quit the program")
		}
	}
}

func TestQuitKeyQuitsFromChats(t *testing.T) {
	_, cmd := NewModel().Update(pressRunes("q"))
	assertQuit(t, cmd)
}

func TestViewAfterQuitIsEmpty(t *testing.T) {
	updated, _ := NewModel().Update(pressRunes("q"))
	mm := updated.(Model)
	if v := mm.View(); v != "" {
		t.Fatalf("expected empty view, got %q", v)
	}
}

// feedAnswers runs a command and gives the model every message it answers
// with, so a command that is a batch of two — a history load and the
// delivery tick that has to start alongside it — is fed whole.
//
// updateModel gives the model one message and returns what it answered
// with, which is the right shape for a test that already knows what it is
// waiting for. Opening a conversation now answers with two things, and a
// test that fed only the first would leave the second to a poll that never
// comes.
func feedAnswers(t *testing.T, model Model, cmd tea.Cmd) Model {
	t.Helper()

	for _, msg := range flattenBatch(t, cmd) {
		updated, _ := updateModel(t, model, msg)
		model = updated
	}

	return model
}

// flattenBatch is what a command answers with, with a batch expanded.
func flattenBatch(t *testing.T, cmd tea.Cmd) []tea.Msg {
	t.Helper()

	if cmd == nil {
		return nil
	}

	// The command is run once. A history load is the call that reaches the
	// source, so running it twice to look at its answer twice is two
	// requests, and the test's own call count is the thing that notices.
	answer := cmd()
	if answer == nil {
		return nil
	}

	batch, isBatch := answer.(tea.BatchMsg)
	if !isBatch {
		return []tea.Msg{answer}
	}

	var flattened []tea.Msg
	for _, inner := range batch {
		flattened = append(flattened, flattenOne(t, inner)...)
	}

	return flattened
}

// flattenOne is what one command of a batch answers with.
//
// A tick is dropped rather than waited for. It is a timer two seconds out,
// it is not a message the test is waiting for, and a test that waited for
// it would take two seconds per key press. A test that wants the loop to
// run says so by handing the tick to the model itself.
//
// The command runs on its own goroutine and is given a deadline, because a
// tick that is called is a two-second sleep: even dropping its answer costs
// the two seconds unless nothing waits for it.
func flattenOne(t *testing.T, cmd tea.Cmd) []tea.Msg {
	t.Helper()

	if cmd == nil {
		return nil
	}

	answers := make(chan tea.Msg, 1)
	go func() { answers <- cmd() }()

	var answer tea.Msg
	select {
	case answer = <-answers:
	case <-time.After(100 * time.Millisecond):
		// Still running: a timer, or a read this test does not need. Its
		// answer is dropped, which is what the program does with a read
		// that lands after the user has moved on.
		return nil
	}

	if answer == nil {
		return nil
	}
	if _, isTick := answer.(messageStatusPollTickMsg); isTick {
		return nil
	}
	if batch, isBatch := answer.(tea.BatchMsg); isBatch {
		var flattened []tea.Msg
		for _, inner := range batch {
			flattened = append(flattened, flattenOne(t, inner)...)
		}
		return flattened
	}

	return []tea.Msg{answer}
}

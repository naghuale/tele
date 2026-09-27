package tui

import (
	"testing"

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
	if m.selectedMsg != 0 {
		t.Fatalf("selectedMsg = %d, want 0", m.selectedMsg)
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

// The third Esc, on the list, does nothing: there is nowhere further out
// to go, and a key that sometimes quits and sometimes does not is a key
// nobody trusts.
func TestEscapeOnTheListOfAConversationDoesNothing(t *testing.T) {
	m := NewModel()
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	m, _ = updateModel(t, m, press(tea.KeyEnter))
	m, _ = updateModel(t, m, press(tea.KeyEsc))
	m, _ = updateModel(t, m, press(tea.KeyEsc))

	_, cmd := m.Update(press(tea.KeyEsc))
	if cmd != nil {
		t.Fatalf("Esc on the list returned %T, want no command", cmd())
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
	m, _ = updateModel(t, m, press(tea.KeyDown))
	if m.selectedMsg != 1 {
		t.Fatalf("selectedMsg = %d, want 1", m.selectedMsg)
	}
	m, _ = updateModel(t, m, press(tea.KeyUp))
	if m.selectedMsg != 0 {
		t.Fatalf("selectedMsg = %d, want 0", m.selectedMsg)
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
// also be the key to leave the program (§4.3).
func TestEscapeInChatsDoesNotQuit(t *testing.T) {
	_, cmd := NewModel().Update(press(tea.KeyEsc))
	if cmd != nil {
		t.Fatalf("Esc in the list returned %T, want no command", cmd())
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

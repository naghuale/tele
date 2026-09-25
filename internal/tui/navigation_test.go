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

func TestEnterOpensSelectedConversation(t *testing.T) {
	m := NewModel()
	m, _ = updateModel(t, m, press(tea.KeyEnter))
	if m.screen != ScreenConversation {
		t.Fatalf("screen = %v, want ScreenConversation", m.screen)
	}
	if m.focus != FocusHistory {
		t.Fatalf("focus = %v, want FocusHistory", m.focus)
	}
	if m.selectedMsg != 0 {
		t.Fatalf("selectedMsg = %d, want 0", m.selectedMsg)
	}
}

func TestEscapeReturnsFromConversationToChats(t *testing.T) {
	m := NewModel()
	m, _ = updateModel(t, m, press(tea.KeyEnter))
	m, _ = updateModel(t, m, press(tea.KeyEsc))
	if m.screen != ScreenChats {
		t.Fatalf("screen = %v, want ScreenChats", m.screen)
	}
	if m.focus != FocusChatList {
		t.Fatalf("focus = %v, want FocusChatList", m.focus)
	}
}

func TestTabMovesFocusHistoryToComposer(t *testing.T) {
	m := NewModel()
	m, _ = updateModel(t, m, press(tea.KeyEnter))

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

func TestEscapePreservesComposerDraft(t *testing.T) {
	m := NewModel()
	m.screen = ScreenConversation
	m.focus = FocusComposer

	m, _ = updateModel(t, m, pressRunes("черновик"))

	m, _ = updateModel(t, m, press(tea.KeyEsc))

	if m.screen != ScreenChats {
		t.Fatalf("screen = %v, want ScreenChats", m.screen)
	}
	if got := m.Composer(); got != "черновик" {
		t.Fatalf("composer = %q, want preserved draft %q", got, "черновик")
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

func TestEscapeQuitsFromChats(t *testing.T) {
	_, cmd := NewModel().Update(press(tea.KeyEsc))
	assertQuit(t, cmd)
}

func TestViewAfterQuitIsEmpty(t *testing.T) {
	updated, _ := NewModel().Update(press(tea.KeyEsc))
	mm := updated.(Model)
	if v := mm.View(); v != "" {
		t.Fatalf("expected empty view, got %q", v)
	}
}

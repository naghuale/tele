package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestNewModelDefaults(t *testing.T) {
	m := NewModel()
	if m.screen != ScreenChats {
		t.Fatalf("screen = %v, want ScreenChats", m.screen)
	}
	if m.focus != FocusChatList {
		t.Fatalf("focus = %v, want FocusChatList", m.focus)
	}
	if len(m.chats) == 0 {
		t.Fatal("expected mock chats")
	}
	if m.selectedChat != 0 {
		t.Fatalf("selectedChat = %d, want 0", m.selectedChat)
	}
	if m.selectedMsg != 0 {
		t.Fatalf("selectedMsg = %d, want 0", m.selectedMsg)
	}
	if m.Composer() != "" {
		t.Fatalf("composer = %q, want empty", m.Composer())
	}
}

// The header of the list is the word §4.1 names and the chats are the rows
// under it. The name of the program is not on the screen: the header line
// has to survive a medium list pane, and a title long enough to be cut is
// a title cut in the middle of a word.
func TestInitialViewShowsChats(t *testing.T) {
	m := NewModel()
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 120, Height: 24})
	v := m.View()
	for _, want := range []string{chatListTitle, "Alice", "Dev Team", "Saved Messages"} {
		if !strings.Contains(v, want) {
			t.Fatalf("view missing %q:\n%s", want, v)
		}
	}
}

func TestWindowSizeMsgUpdatesDimensions(t *testing.T) {
	m := NewModel()
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})
	if m.width != 120 || m.height != 40 {
		t.Fatalf("dims = %dx%d, want 120x40", m.width, m.height)
	}
}

func TestViewDoesNotPanicAtZeroSize(t *testing.T) {
	m := NewModel()
	v := m.View()
	if !strings.Contains(v, "too small") {
		t.Fatalf("expected too-small fallback, got %q", v)
	}
}

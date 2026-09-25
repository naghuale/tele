package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestVisibleRangeEmpty(t *testing.T) {
	if s, e := visibleRange(0, 0, 5); s != 0 || e != 0 {
		t.Fatalf("visibleRange(0,0,5) = (%d,%d), want (0,0)", s, e)
	}
	if s, e := visibleRange(5, 0, 0); s != 0 || e != 0 {
		t.Fatalf("visibleRange(5,0,0) = (%d,%d), want (0,0)", s, e)
	}
}

func TestVisibleRangeFitsAll(t *testing.T) {
	s, e := visibleRange(3, 1, 10)
	if s != 0 || e != 3 {
		t.Fatalf("visibleRange = (%d,%d), want (0,3)", s, e)
	}
}

func TestVisibleRangeWindowKeepsSelection(t *testing.T) {
	total, available := 100, 10
	for selected := 0; selected < total; selected++ {
		s, e := visibleRange(total, selected, available)
		if s < 0 || e > total || e-s > available {
			t.Fatalf("range out of bounds: s=%d e=%d", s, e)
		}
		if selected < s || selected >= e {
			t.Fatalf("selected %d not in [%d,%d)", selected, s, e)
		}
	}
}

func TestTruncateRunesEmpty(t *testing.T) {
	if got := truncateRunes("abc", 0); got != "" {
		t.Fatalf("truncateRunes(_, 0) = %q, want empty", got)
	}
	if got := truncateRunes("abc", -1); got != "" {
		t.Fatalf("truncateRunes(_, -1) = %q, want empty", got)
	}
}

func TestTruncateRunesFits(t *testing.T) {
	if got := truncateRunes("abc", 5); got != "abc" {
		t.Fatalf("truncateRunes = %q, want %q", got, "abc")
	}
	if got := truncateRunes("привет", 6); got != "привет" {
		t.Fatalf("truncateRunes = %q, want %q", got, "привет")
	}
}

func TestTruncateRunesTruncates(t *testing.T) {
	if got := truncateRunes("abcdef", 4); got != "abc…" {
		t.Fatalf("truncateRunes = %q, want %q", got, "abc…")
	}
	if got := truncateRunes("привет", 3); got != "пр…" {
		t.Fatalf("truncateRunes = %q, want %q", got, "пр…")
	}
	if got := truncateRunes("abcdef", 1); got != "a" {
		t.Fatalf("truncateRunes = %q, want %q", got, "a")
	}
}

func TestTooSmallViewContainsRequiredAndCurrentSize(t *testing.T) {
	m := NewModel()
	m.width = 10
	m.height = 5
	v := m.View()
	if !strings.Contains(v, "too small") {
		t.Fatalf("missing 'too small': %q", v)
	}
	if !strings.Contains(v, "40x12") {
		t.Fatalf("missing minimum size: %q", v)
	}
	if !strings.Contains(v, "10x5") {
		t.Fatalf("missing current size: %q", v)
	}
}

func TestChatsViewMarksSelectedChat(t *testing.T) {
	m := NewModel()
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	v := m.View()
	if !strings.Contains(v, "> Alice") {
		t.Fatalf("expected '> Alice' marker:\n%s", v)
	}
}

func TestConversationViewShowsSelectedChatTitle(t *testing.T) {
	m := NewModel()
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m, _ = updateModel(t, m, press(tea.KeyEnter))
	v := m.View()
	if !strings.Contains(v, m.selected().Title) {
		t.Fatalf("expected chat title %q in view:\n%s", m.selected().Title, v)
	}
}

func TestConversationViewShowsComposer(t *testing.T) {
	m := NewModel()
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m, _ = updateModel(t, m, press(tea.KeyEnter))
	v := m.View()
	if !strings.Contains(v, "Composer:") {
		t.Fatalf("missing composer:\n%s", v)
	}
}

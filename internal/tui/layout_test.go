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

// Truncation is measured in terminal columns, not in runes: "привет" is
// six columns wide and an emoji is two, so a layout counted in runes
// overflows as soon as the content is not ASCII.
func TestTruncateCellsCountsColumns(t *testing.T) {
	cases := map[string]struct {
		value string
		width int
		want  string
	}{
		"ascii":          {value: "abcdef", width: 4, want: "abc…"},
		"cyrillic":       {value: "привет", width: 3, want: "пр…"},
		"fits":           {value: "привет", width: 6, want: "привет"},
		"one column":     {value: "abcdef", width: 1, want: "a"},
		"emoji is two":   {value: "🌍🌍🌍", width: 4, want: "🌍…"},
		"emoji fits two": {value: "🌍", width: 2, want: "🌍"},
		"emoji in one":   {value: "🌍", width: 1, want: ""},
		"a lone mark":    {value: "abcdef", width: 1, want: "a"},
		"nothing fits":   {value: "abc", width: 0, want: ""},
		"negative":       {value: "abc", width: -1, want: ""},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			got := truncateCells(testCase.value, testCase.width)
			if got != testCase.want {
				t.Fatalf(
					"truncateCells(%q, %d) = %q, want %q",
					testCase.value,
					testCase.width,
					got,
					testCase.want,
				)
			}
			if width := cellWidth(got); width > testCase.width && testCase.width > 0 {
				t.Fatalf(
					"truncateCells(%q, %d) is %d columns wide",
					testCase.value,
					testCase.width,
					width,
				)
			}
		})
	}
}

func TestCellWidthCountsColumns(t *testing.T) {
	cases := map[string]struct {
		value string
		want  int
	}{
		"ascii":    {value: "abc", want: 3},
		"cyrillic": {value: "привет", want: 6},
		"emoji":    {value: "🌍", want: 2},
		"mixed":    {value: "a🌍б", want: 4},
		"accent":   {value: "café", want: 4},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			if got := cellWidth(testCase.value); got != testCase.want {
				t.Fatalf(
					"cellWidth(%q) = %d, want %d",
					testCase.value,
					got,
					testCase.want,
				)
			}
		})
	}
}

// Every line of a region is padded to the same number of columns, which
// is what keeps a pane rectangular and its background continuous.
func TestFitCellsPadsToWidth(t *testing.T) {
	if got := fitCells("abc", 6); cellWidth(got) != 6 {
		t.Fatalf("fitCells(\"abc\", 6) = %q, %d columns", got, cellWidth(got))
	}
	if got := fitCells("привет", 4); cellWidth(got) != 4 {
		t.Fatalf("fitCells(\"привет\", 4) = %q, %d columns", got, cellWidth(got))
	}
	if got := fitCells("abcdefgh", 3); cellWidth(got) != 3 {
		t.Fatalf("fitCells(\"abcdefgh\", 3) = %q, %d columns", got, cellWidth(got))
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
	if !strings.Contains(v, "40x5") {
		t.Fatalf("missing minimum size: %q", v)
	}
	if !strings.Contains(v, "10x5") {
		t.Fatalf("missing current size: %q", v)
	}
}

// The selected chat is marked with the attributes of the theme, not with
// a colour and not with a prefix. A prefix would put a character in front
// of every name and shift the column of the second line, and a colour
// would say nothing at all where the terminal shows no colour.
func TestChatsViewMarksSelectedChatWithThemeAttributes(t *testing.T) {
	m := NewModel()
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	v := m.View()

	marked := m.styles().selected(true).Render("Alice")
	if !strings.Contains(v, marked) {
		t.Fatalf("view does not mark the selected chat:\n%s", v)
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
	if !strings.Contains(v, composerPlaceholder) {
		t.Fatalf("missing composer:\n%s", v)
	}
}

package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"telecli/internal/tui/termwidth"
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
//
// The rule a screen is drawn in is the model's, so these cases hold in both
// of them: they are the cases where the two rules agree, and the ones where
// they do not are the cases of internal/tui/termwidth.
func TestTruncationCountsColumns(t *testing.T) {
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

	for _, mode := range []termwidth.Mode{termwidth.ModeGrapheme, termwidth.ModeCodepoint} {
		m := NewModel()
		m.widths = termwidth.Unmeasured(mode)

		for name, testCase := range cases {
			t.Run(name, func(t *testing.T) {
				got := m.widths.TruncateMarked(testCase.value, testCase.width, ellipsis)
				if got != testCase.want {
					t.Fatalf(
						"TruncateMarked(%q, %d, %q) = %q, want %q",
						testCase.value,
						testCase.width,
						ellipsis,
						got,
						testCase.want,
					)
				}
				if width := m.widths.StringWidth(got); width > testCase.width && testCase.width > 0 {
					t.Fatalf(
						"TruncateMarked(%q, %d, %q) is %d columns wide",
						testCase.value,
						testCase.width,
						ellipsis,
						width,
					)
				}
			})
		}
	}
}

func TestWidthCountsColumns(t *testing.T) {
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

	m := NewModel()

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			if got := m.widths.StringWidth(testCase.value); got != testCase.want {
				t.Fatalf(
					"StringWidth(%q) = %d, want %d",
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
func TestFitPadsToWidth(t *testing.T) {
	m := NewModel()

	if got := m.widths.Fit("abc", 6, ellipsis); m.widths.StringWidth(got) != 6 {
		t.Fatalf("Fit(\"abc\", 6) = %q, %d columns", got, m.widths.StringWidth(got))
	}
	if got := m.widths.Fit("привет", 4, ellipsis); m.widths.StringWidth(got) != 4 {
		t.Fatalf("Fit(\"привет\", 4) = %q, %d columns", got, m.widths.StringWidth(got))
	}
	if got := m.widths.Fit("abcdefgh", 3, ellipsis); m.widths.StringWidth(got) != 3 {
		t.Fatalf("Fit(\"abcdefgh\", 3) = %q, %d columns", got, m.widths.StringWidth(got))
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

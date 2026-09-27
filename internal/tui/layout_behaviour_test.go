package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"telecli/internal/tui/theme"
)

// These tests cover the parts of docs/TUI_SPEC.md §3, §4, §5, §8.5 and
// §10 that are about what the user finds on the screen and under their
// fingers, rather than about the shape of the geometry.

// A wide and a medium screen show the chat list and the conversation at
// the same time, and a narrow one shows one of them. The panes are
// recognisable by their own titles, which is what a user reads first.
func TestPaneCountFollowsTheWidthClass(t *testing.T) {
	cases := map[string]struct {
		width    int
		wantList bool
		wantChat bool
		kind     LayoutKind
	}{
		"wide":   {width: 120, wantList: true, wantChat: true, kind: LayoutWide},
		"medium": {width: 80, wantList: true, wantChat: true, kind: LayoutMedium},
		"narrow": {width: 60, wantList: false, wantChat: true, kind: LayoutNarrow},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			if got := LayoutFor(testCase.width, 24).Kind; got != testCase.kind {
				t.Fatalf("kind at %d = %v, want %v", testCase.width, got, testCase.kind)
			}

			view := openedModel(t, testCase.width, 24).View()

			// A title of the second chat of the list is on screen only
			// where the list is drawn, and the title of the open one only
			// where the conversation is.
			if got := strings.Contains(view, "Dev Team"); got != testCase.wantList {
				t.Fatalf("chat list on screen = %v, want %v:\n%s", got, testCase.wantList, view)
			}
			if got := strings.Contains(view, "Alice"); got != testCase.wantChat {
				t.Fatalf("conversation on screen = %v, want %v:\n%s", got, testCase.wantChat, view)
			}
		})
	}
}

// A chat list with nothing open in it takes the whole width. Squeezing it
// beside an empty pane would leave the same list unreadable for nothing.
func TestChatListTakesTheWholeWidthWithoutAConversation(t *testing.T) {
	for _, width := range []int{120, 80, 60} {
		m := sizedModel(t, width, 24)
		view := m.View()

		for index, line := range viewLines(view) {
			if got := cellWidth(line); got != width {
				t.Fatalf(
					"line %d at %d is %d columns, want %d",
					index,
					width,
					got,
					width,
				)
			}
		}
	}
}

// The height rules of §3.4: below ten rows there is no hint bar, and a
// conversation on a screen below six rows is the composer alone.
func TestShortScreenRules(t *testing.T) {
	withHints := openedModel(t, 60, 20).View()
	if !strings.Contains(withHints, "Alt+Enter newline") {
		t.Fatalf("a screen of 20 rows has no hint bar:\n%s", withHints)
	}

	withoutHints := openedModel(t, 60, 9).View()
	if strings.Contains(withoutHints, "Esc back") {
		t.Fatalf("a screen of 9 rows still draws the hint bar:\n%s", withoutHints)
	}

	composerOnly := openedModel(t, 60, 5).View()
	if !strings.Contains(composerOnly, composerPlaceholder) {
		t.Fatalf("a screen of 5 rows has no composer:\n%s", composerOnly)
	}
	if strings.Contains(composerOnly, "Alice") {
		t.Fatalf("a screen of 5 rows still draws the messages:\n%s", composerOnly)
	}
	if got := len(viewLines(composerOnly)); got != 5 {
		t.Fatalf("a screen of 5 rows is %d lines, want 5:\n%s", got, composerOnly)
	}
}

// A resize is not a restart. The chat, the conversation and the draft are
// the user's, not the screen's, and a terminal resized by accident is the
// most ordinary way a layout is tested by a human being.
func TestResizeKeepsChatConversationAndDraft(t *testing.T) {
	m := sizedModel(t, 120, 30)
	m, _ = updateModel(t, m, press(tea.KeyDown))
	m, _ = updateModel(t, m, press(tea.KeyEnter))
	m, _ = updateModel(t, m, pressRunes("черновик"))

	for _, size := range [][2]int{{60, 20}, {80, 24}, {120, 30}, {40, 5}} {
		m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})

		if m.selectedChat != 1 {
			t.Fatalf(
				"at %dx%d selectedChat = %d, want 1",
				size[0],
				size[1],
				m.selectedChat,
			)
		}
		if m.screen != ScreenConversation {
			t.Fatalf("at %dx%d screen = %v, want conversation", size[0], size[1], m.screen)
		}
		if got := m.Composer(); got != "черновик" {
			t.Fatalf(
				"at %dx%d composer = %q, want the draft",
				size[0],
				size[1],
				got,
			)
		}
	}
}

// paneOf returns the pane a focus belongs to.
func paneOf(focus Focus) pane {
	if focus == FocusChatList {
		return listPane
	}

	return conversationPane
}

// The focus is a cycle among the regions that are on screen, and one
// region at a time carries the accent. Tab is the only gesture that moves
// it, so the invariant is checked by walking the whole cycle.
func TestTabCycleKeepsOneAccentAtEveryStop(t *testing.T) {
	cases := map[string]struct {
		width   int
		height  int
		regions []Focus
	}{
		"wide": {
			width:   120,
			height:  30,
			regions: []Focus{FocusComposer, FocusChatList, FocusHistory, FocusComposer},
		},
		"narrow": {
			width:   60,
			height:  30,
			regions: []Focus{FocusComposer, FocusHistory, FocusComposer},
		},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			m := openedModel(t, testCase.width, testCase.height)

			for _, want := range testCase.regions {
				if m.focus != want {
					t.Fatalf("focus = %v, want %v", m.focus, want)
				}

				assertFocusColumn(t, m, focusColumnOf(m, paneOf(m.focus)))

				m, _ = updateModel(t, m, press(tea.KeyTab))
			}
		})
	}
}

// Esc is a way out of a place, never a way out of the program. Every step
// of the hierarchy is walked on both screen shapes and none of them quits.
func TestEscapeNeverQuits(t *testing.T) {
	for _, width := range []int{120, 60} {
		m := openedModel(t, width, 30)
		m, _ = updateModel(t, m, pressRunes("черновик"))

		for step := range 4 {
			updated, cmd := m.Update(press(tea.KeyEsc))
			if cmd != nil {
				t.Fatalf(
					"Esc at width %d, step %d returned %T, want no command",
					width,
					step,
					cmd(),
				)
			}

			m = updated.(Model)
		}

		if got := m.Composer(); got != "черновик" {
			t.Fatalf("width %d: composer = %q, want the draft", width, got)
		}
	}
}

// The colours of the screen are the ones the composition root resolved.
// A model built for True Color draws colour even where the process has no
// terminal, and a model built for no colour draws none, which is what
// `color = "always"` and `color = "never"` mean.
func TestViewColoursComeFromTheResolvedProfile(t *testing.T) {
	coloured := openedModel(t, 80, 24)
	coloured.theme = theme.DefaultTheme().ForProfile(theme.ProfileTrueColor)
	coloured = coloured.withRenderer(theme.ProfileTrueColor)

	view := coloured.View()
	if !strings.Contains(view, "48;2;") {
		t.Fatalf("a True Color profile painted no surface:\n%q", view)
	}
	if !strings.Contains(view, theme.FocusBar) {
		t.Fatalf("a True Color profile drew no accent:\n%q", view)
	}

	plain := openedModel(t, 80, 24)
	plainView := plain.View()
	if strings.Contains(plainView, "48;2;") {
		t.Fatalf("a no-colour profile painted a surface:\n%q", plainView)
	}
	if !strings.Contains(plainView, theme.FocusBar) {
		t.Fatalf("a no-colour profile drew no accent:\n%q", plainView)
	}
}

// The hint bar says what works where the user is, and it says nothing that
// does not. §4.6 asks for a bar that depends on the focus and shrinks with
// the width.
func TestHintBarFollowsFocusAndWidth(t *testing.T) {
	cases := map[string]struct {
		model Model
		want  string
	}{
		"list":        {model: sizedModel(t, 120, 30), want: hintChatList},
		"list narrow": {model: sizedModel(t, 60, 30), want: hintChatList},
		"composer wide": {
			model: openedModel(t, 120, 30),
			want:  "Enter send · Alt+Enter newline · Tab focus · Esc timeline",
		},
		"composer medium": {
			model: openedModel(t, 80, 30),
			want:  "Enter send · Alt+Enter newline · Esc timeline",
		},
		"composer narrow": {
			model: openedModel(t, 60, 30),
			want:  "Enter send · Alt+Enter newline · Esc back",
		},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			view := testCase.model.View()
			if !strings.Contains(view, testCase.want) {
				t.Fatalf("hint bar does not say %q:\n%s", testCase.want, view)
			}
		})
	}

	timeline := openedModel(t, 120, 30)
	timeline.focus = FocusHistory
	if view := timeline.View(); !strings.Contains(view, "j/k scroll") {
		t.Fatalf("the timeline has no hints of its own:\n%s", view)
	}

	// Nothing that does not work yet is offered.
	view := sizedModel(t, 120, 30).View()
	for _, absent := range []string{"/ search", "Shift+Enter", "Ctrl+U"} {
		if strings.Contains(view, absent) {
			t.Fatalf("the hint bar offers %q, which does nothing yet:\n%s", absent, view)
		}
	}
}

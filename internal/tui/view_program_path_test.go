package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"telecli/internal/tui/theme"
)

// This file builds models the way the program builds them.
//
// A view test that assembles its model by hand proves something about the
// view and nothing about the program. What the program does is
// resolveInterfaceThemeFor, which degrades the theme for the profile it
// resolved from the environment, and then hands that theme and that profile
// to NewModelWithDependencies, which builds a renderer for the same
// profile. Under the no-colour profile the theme that reaches the view has
// no colours left in it at all, which is a different model from the same
// view with a full-colour theme and a colourless renderer, and the two
// draw differently.

// programModel is a model built the way the composition root builds one.
func programModel(
	t *testing.T,
	profile theme.Profile,
	width int,
	height int,
) Model {
	t.Helper()

	model, err := NewModelWithDependencies(context.Background(), Dependencies{
		MessageSubmitter: &programSubmitter{},
		Theme:            theme.DefaultTheme().ForProfile(profile),
		ColorProfile:     profile,
	})
	if err != nil {
		t.Fatalf("NewModelWithDependencies: %v", err)
	}

	model, _ = updateModel(t, model, tea.WindowSizeMsg{Width: width, Height: height})

	return model
}

// openedProgramModel is a model built the way the composition root builds
// one, with the first chat open.
func openedProgramModel(
	t *testing.T,
	profile theme.Profile,
	width int,
	height int,
) Model {
	t.Helper()

	model := programModel(t, profile, width, height)
	model, _ = updateModel(t, model, press(tea.KeyEnter))

	return model
}

// plain is a rendered screen without its escape sequences: what the
// assertions below are about is what a user reads, and a colour between the
// marker and the name is not a thing on the screen.
func plain(view string) string {
	return ansi.Strip(view)
}

// programSubmitter is a submitter that accepts everything, so a model can
// be built without a source and still have the dependencies the program
// passes.
type programSubmitter struct{}

func (s *programSubmitter) SubmitMessage(
	_ context.Context,
	chatID int64,
	_ string,
) (Submission, error) {
	return Submission{ID: "1", State: SubmissionSent}, nil
}

// The focus of a region is a bar in the first column of that region, and
// it is a bar in every profile. §2.7 requires it in no colour at all, and
// the no-colour profile is the one that drops the attributes and the
// colours that could have carried the meaning instead.
func TestProgramPathDrawsTheFocusInNoColor(t *testing.T) {
	cases := map[string]struct {
		focus Focus
		want  pane
	}{
		"chat list": {focus: FocusChatList, want: listPane},
		"composer":  {focus: FocusComposer, want: conversationPane},
		"timeline":  {focus: FocusHistory, want: conversationPane},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			model := focusedOn(
				openedProgramModel(t, theme.ProfileNoColor, 120, 30),
				testCase.focus,
			)

			assertFocusColumn(t, model, focusColumnOf(model, testCase.want))
		})
	}
}

// The selected chat is marked with a glyph of its own, and it is a
// character rather than an attribute. termenv's Ascii profile prints no
// bold and no reverse, so a selection carried by them would be invisible on
// a terminal with NO_COLOR set, and the glyph is not the focus bar: the
// two say different things and a list that drew both as `▌` read as a
// double line.
func TestProgramPathDrawsTheSelectedChatInNoColor(t *testing.T) {
	model := focusedOn(
		programModel(t, theme.ProfileNoColor, 120, 30),
		FocusChatList,
	)
	model, _ = updateModel(t, model, press(tea.KeyDown))

	selected := model.selected().Title
	marked := theme.SelectionMark + selected
	if !strings.Contains(plain(model.View()), marked) {
		t.Fatalf(
			"view does not mark the selected chat %q:\\n%s",
			selected,
			model.View(),
		)
	}

	// And nothing else carries the mark: one selected chat, one marker.
	view := plain(model.View())
	if got := strings.Count(view, theme.SelectionMark+selected); got != 1 {
		t.Fatalf("%d marked rows, want 1:\\n%s", got, view)
	}
}

// The chat that is open stays marked while the focus is on the
// conversation: it is the one chat in the list that is not a list entry any
// more, and in colour mode the mark is a dimmer accent rather than the
// bright one the list has while it is focused.
func TestProgramPathKeepsTheOpenChatMarked(t *testing.T) {
	for _, profile := range []theme.Profile{
		theme.ProfileTrueColor,
		theme.ProfileNoColor,
	} {
		model := focusedOn(
			openedProgramModel(t, profile, 120, 30),
			FocusComposer,
		)

		view := plain(model.View())
		if !strings.Contains(view, theme.SelectionMark+model.selected().Title) {
			t.Fatalf("profile %v: the open chat is not marked:\\n%s", profile, view)
		}
	}
}

// A wide and a medium screen have two panes before a chat is chosen, and
// the right one says what to do rather than standing empty.
func TestProgramPathShowsBothPanesBeforeAChatIsOpened(t *testing.T) {
	for _, width := range []int{120, 80} {
		model := programModel(t, theme.ProfileTrueColor, width, 24)
		view := plain(model.View())

		if !strings.Contains(view, "Dev Team") {
			t.Fatalf("width %d: the chat list is missing:\\n%s", width, view)
		}
		if !strings.Contains(view, emptyConversationTitle) {
			t.Fatalf("width %d: the conversation pane is missing:\\n%s", width, view)
		}
		if !strings.Contains(view, emptyConversationHint) {
			t.Fatalf("width %d: the pane does not say what to do:\\n%s", width, view)
		}
		if !strings.Contains(view, hintChatList) {
			t.Fatalf("width %d: the hint bar is missing:\\n%s", width, view)
		}

		// The list keeps the width of §3.3 even with an empty pane beside
		// it, and every line is still exactly the width of the screen.
		for index, line := range viewLines(model.View()) {
			if got := cellWidth(line); got != width {
				t.Fatalf("line %d is %d columns, want %d", index, got, width)
			}
		}
	}
}

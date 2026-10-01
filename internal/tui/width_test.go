package tui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"telecli/internal/tui/termwidth"
)

// The width of text is counted in one place, and this test is what keeps it
// that way.
//
// Every width in this package is read from the model's width model, which
// counts columns the way the terminal this program is running in draws them.
// A second way in — a call to a width function of a library, a count of
// runes, a len of a string — is a line that is a column out of place on a
// terminal that disagrees with it, and a line one column too wide is a line
// the terminal wraps. Everything under the wrap moves: the focus bars step
// sideways, rows of the list repeat or disappear, the fragments of the last
// frame stay on the screen, and the time of a message is cut off.
//
// So the rule is not a style. A view that counted columns for itself would
// be a view deciding what the terminal can show, which is what
// internal/tui/termwidth measured the terminal to find out.
func TestNoWidthIsCountedOutsideTheWidthModel(t *testing.T) {
	// The libraries this program used to count columns with, and the
	// functions of them that answer the question. Anything here called
	// from a view is a second opinion about the width of a string, and
	// two opinions about one string are one too many.
	forbidden := map[string]map[string]bool{
		"ansi": {
			"StringWidth":  true,
			"Truncate":     true,
			"TruncateLeft": true,
			"TruncateWc":   true,
			"Wrap":         true,
			"Wordwrap":     true,
			"Hardwrap":     true,
			"Cut":          true,
		},
		"lipgloss": {
			"Width": true,
		},
		"uniseg": {
			"StringWidth": true,
		},
		"runewidth": {
			"RuneWidth":   true,
			"StringWidth": true,
			"Truncate":    true,
		},
	}

	// The file that holds the test is the one place the names appear as
	// words rather than as calls.
	self := "width_test.go"

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read the package directory: %v", err)
	}

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || name == self {
			continue
		}

		assertNoWidthCalls(t, name, forbidden)
	}
}

// assertNoWidthCalls fails if one file of the package calls a width
// function of a library directly.
func assertNoWidthCalls(
	t *testing.T,
	name string,
	forbidden map[string]map[string]bool,
) {
	t.Helper()

	path := filepath.Join(".", name)

	set := token.NewFileSet()

	file, err := parser.ParseFile(set, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}

	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}

		library, ok := call.X.(*ast.Ident)
		if !ok {
			return true
		}

		if !forbidden[library.Name][call.Sel.Name] {
			return true
		}

		position := set.Position(call.Pos())
		t.Errorf(
			"%s:%d calls %s.%s; count the width of text with the model's "+
				"width model, which is what the terminal was measured for",
			name,
			position.Line,
			library.Name,
			call.Sel.Name,
		)

		return true
	})
}

// A model built without a program counts by grapheme cluster, because that
// is the rule the renderer draws with and the rule of every terminal that
// follows the emoji rules, and because a model that had to be told a rule
// before it could be drawn would leave a zero model undrawable.
func TestAModelWithoutAMeasurementCountsByGraphemes(t *testing.T) {
	if got := NewModel().WidthMode(); got != termwidth.ModeGrapheme {
		t.Fatalf("NewModel counts in %v, want %v", got, termwidth.ModeGrapheme)
	}
	if got := NewModelWithSource(nil).WidthMode(); got != termwidth.ModeGrapheme {
		t.Fatalf("NewModelWithSource counts in %v, want %v", got, termwidth.ModeGrapheme)
	}
}

// A rule the composition root resolved is the rule the model is drawn with,
// and the measurement that came with it is what the rule was resolved
// against.
func TestTheModelIsDrawnWithTheResolvedRule(t *testing.T) {
	cases := []struct {
		name       string
		configured termwidth.Mode
		measured   *termwidth.Measurement
		want       termwidth.Mode
	}{
		{
			name:       "a configured rule is used as it is",
			configured: termwidth.ModeGrapheme,
			measured:   &termwidth.Measurement{Widths: appleAnswers},
			want:       termwidth.ModeGrapheme,
		},
		{
			name:       "a terminal that counts code points is measured",
			configured: termwidth.ModeAuto,
			measured:   &termwidth.Measurement{Widths: appleAnswers},
			want:       termwidth.ModeCodepoint,
		},
		{
			// The fallback is the rule the renderer counts with, so that a
			// terminal nobody could be asked about cannot be drawn a row
			// wider than the window in its own count (the owner, 01.10).
			name:       "nothing measured is the rule the renderer draws with",
			configured: termwidth.ModeAuto,
			want:       termwidth.ModeGrapheme,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			model, err := NewModelWithDependencies(t.Context(), Dependencies{
				MessageSubmitter: &recordingSubmitter{},
				WidthMode:        testCase.configured,
				WidthMeasured:    testCase.measured,
			})
			if err != nil {
				t.Fatalf("NewModelWithDependencies: %v", err)
			}

			if got := model.WidthMode(); got != testCase.want {
				t.Fatalf("the model counts in %v, want %v", got, testCase.want)
			}
		})
	}
}

// appleAnswers is what the macOS Terminal answers: a hand with the emoji
// selector behind it is one column, because that is what it draws.
var appleAnswers = map[string]int{
	"✌️":   1,
	"🇨🇳":   2,
	"🏃‍♂️": 2,
	"👍🏽":   2,
	"中":    2,
	"é":    1,
}

// A measured width reaches the screen, where the width is not the width of a
// glyph drawn from an emoji font.
//
// A flag of one column is the case the measurement cannot answer: the
// question was where the cursor went, and the macOS Terminal draws a pagoda
// in two cells and moves the cursor one, so a model laid out by the answer
// puts the letters after it inside the picture (the owner, 01.10). A Han
// character is the case it can: no terminal draws one in anything but two
// columns, and a terminal with a font that does not has said so.
func TestAMeasuredWidthReachesTheScreen(t *testing.T) {
	measured := &termwidth.Measurement{
		Widths: map[string]int{"🇨🇳": 1, "⛩": 1, "中": 3},
	}

	model, err := NewModelWithDependencies(t.Context(), Dependencies{
		MessageSubmitter: &recordingSubmitter{},
		WidthMode:        termwidth.ModeAuto,
		WidthMeasured:    measured,
	})
	if err != nil {
		t.Fatalf("NewModelWithDependencies: %v", err)
	}

	if got := model.widths.StringWidth("中"); got != 3 {
		t.Errorf("the measured Han character is %d columns in the model, want 3", got)
	}
	for _, emoji := range []string{"🇨🇳", "⛩"} {
		if got := model.widths.StringWidth(emoji); got != 2 {
			t.Errorf("%q measured as one column is %d in the model, want 2", emoji, got)
		}
	}
}

// The two rules draw the same screen in different columns, and both of them
// have to be a screen that fits: a line that is too wide in the rule the
// screen was drawn in is a line the terminal will wrap.
func TestAScreenFitsInTheRuleItWasDrawnIn(t *testing.T) {
	for _, mode := range []termwidth.Mode{
		termwidth.ModeGrapheme,
		termwidth.ModeCodepoint,
	} {
		model := sizedModel(t, 120, 30)
		model.widths = termwidth.Unmeasured(mode)

		for index, line := range viewLines(model.View()) {
			if got := model.widths.StringWidth(line); got > model.width {
				t.Errorf(
					"%s: line %d is %d columns, want at most %d: %q",
					mode, index, got, model.width, line,
				)
			}
		}
	}
}

// The proof that a line is never cut in the middle of a cluster is that the
// screen it is drawn on is still rectangular afterwards: a half a cluster
// is a letter the terminal cannot draw, and a line that cannot be drawn
// leaves a gap in the pane.
func TestAFittedLineKeepsWholeClusters(t *testing.T) {
	for _, mode := range []termwidth.Mode{
		termwidth.ModeGrapheme,
		termwidth.ModeCodepoint,
	} {
		model := NewModel()
		model.widths = termwidth.Unmeasured(mode)
		model.chats[0].Title = "Xiaomi News ✌️"

		for width := 1; width < 30; width++ {
			fitted := model.widths.Fit(model.chats[0].Title, width, ellipsis)
			if got := model.widths.StringWidth(fitted); got > width {
				t.Errorf(
					"%s: Fit(%q, %d) = %q is %d columns",
					mode, model.chats[0].Title, width, fitted, got,
				)
			}
			if strings.ContainsAny(fitted, "�") {
				t.Errorf(
					"%s: Fit(%q, %d) = %q drew a broken letter",
					mode, model.chats[0].Title, width, fitted,
				)
			}
		}
	}
}

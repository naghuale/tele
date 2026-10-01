package termwidth

import (
	"strings"
	"testing"
)

// The answers of two terminals that draw emoji differently.
//
// The macOS Terminal and iTerm2 follow wcwidth: the hand is one column
// whatever is behind it, and a flag is two. Ghostty, WezTerm and kitty
// implement the Unicode emoji rules, and the hand with the selector behind
// it is the emoji, which is two columns. Both are right, and a program that
// counts the width the other one uses shifts every line under it.
var (
	// appleTerminalAnswers is a terminal that counts code points: the hand
	// is one column whatever is behind it, the pagoda is one letter with or
	// without its selector, and a cup of coffee is one column unless the
	// font has a wide glyph for it.
	appleTerminalAnswers = map[string]int{
		"✌️":   1,
		"🇨🇳":   2,
		"🏃‍♂️": 2,
		"👍🏽":   2,
		"⛩️":   1,
		"☕":    1,
		"🫶":    1,
		"中":    2,
		"é":    1,
	}

	// graphemeTerminalAnswers is a terminal that counts clusters: the
	// selector widens the pagoda, a character with emoji presentation of
	// its own is two columns whether it has a selector or not, and the rest
	// is the same as it is everywhere.
	graphemeTerminalAnswers = map[string]int{
		"✌️":   2,
		"🇨🇳":   2,
		"🏃‍♂️": 2,
		"👍🏽":   2,
		"⛩️":   2,
		"☕":    2,
		"🫶":    2,
		"中":    2,
		"é":    1,
	}
)

// A terminal that disagrees with the grapheme rule is counted by code
// points, because that is the rule it is following.
//
// The width of the title is the same either way now: the hand in it is two
// columns because that is how wide a terminal draws it, not because of the
// rule. What the answers decide is the rule the program says it is drawing
// with — in `telecli doctor`, in the shape of a block that has an emoji in
// it — and not a cell of a row.
func TestATerminalThatCountsCodePointsIsDrawnByCodePoints(t *testing.T) {
	model, choice := Select(ModeAuto, Measurement{Widths: appleTerminalAnswers})

	if choice.Mode != ModeCodepoint || choice.Source != SourceMeasured {
		t.Fatalf("Select(auto, Apple Terminal) = %v, want codepoint (measured)", choice)
	}
	if got := model.StringWidth("Xiaomi News ✌️"); got != 14 {
		t.Fatalf("the title is %d columns, want 14", got)
	}
}

// A terminal that agrees with the grapheme rule about every probe is a
// terminal that draws graphemes, and is counted that way.
func TestATerminalThatCountsGraphemesIsDrawnByGraphemes(t *testing.T) {
	model, choice := Select(ModeAuto, Measurement{Widths: graphemeTerminalAnswers})

	if choice.Mode != ModeGrapheme || choice.Source != SourceMeasured {
		t.Fatalf("Select(auto, Ghostty) = %v, want grapheme (measured)", choice)
	}
	if got := model.StringWidth("Xiaomi News ✌️"); got != 14 {
		t.Fatalf("the title is %d columns, want 14", got)
	}
}

// One disagreement is enough, and only the probes that were answered count.
func TestOneDisagreementIsEnoughToLeaveTheGraphemeRule(t *testing.T) {
	answers := map[string]int{}
	for probe, width := range graphemeTerminalAnswers {
		answers[probe] = width
	}
	answers["🇨🇳"] = 1

	_, choice := Select(ModeAuto, Measurement{Widths: answers})
	if choice.Mode != ModeCodepoint {
		t.Fatalf("Select(auto, a flag of one column) = %v, want codepoint", choice)
	}
}

func TestAPartialAnswerIsStillAnAnswer(t *testing.T) {
	_, choice := Select(ModeAuto, Measurement{Widths: map[string]int{
		"✌️": 2,
		"中":  2,
	}})

	if choice.Mode != ModeGrapheme {
		t.Fatalf("Select(auto, two answers) = %v, want grapheme", choice)
	}
}

// A terminal that says nothing is a terminal nobody could ask: the output
// is a file, or the question went somewhere that does not answer.
//
// It is drawn by the grapheme rule, which is the rule the renderer counts
// with and the rule every terminal that follows the emoji rules draws by.
// Guessing the other way is not the safe half of the coin: a row counted by
// code points is a column narrower than the row the renderer is about to
// write, and a row that reaches the last column of the window in the
// terminal's own count is a row the terminal wraps, which moves everything
// under it down a row (the owner, 01.10). Where the terminal answered, the
// answer still wins.
func TestATerminalThatAnswersNothingIsDrawnByGraphemes(t *testing.T) {
	for _, measured := range []Measurement{
		{},
		{TimedOut: true},
	} {
		model, choice := Select(ModeAuto, measured)

		if choice.Mode != ModeGrapheme || choice.Source != SourceDefault {
			t.Fatalf("Select(auto, %+v) = %v, want grapheme (default)", measured, choice)
		}
		if got := model.StringWidth("✌️"); got != 2 {
			t.Fatalf("the hand is %d columns, want 2", got)
		}
	}

	// A terminal that answered for code points is counted by code points,
	// whatever the default is.
	model, choice := Select(ModeAuto, Measurement{Widths: appleTerminalAnswers})
	if choice.Mode != ModeCodepoint {
		t.Fatalf("a terminal that answered for code points chose %v", choice.Mode)
	}
	if got := model.StringWidth("✌️"); got != 2 {
		t.Fatalf("a measured terminal counts the hand as %d, want 2", got)
	}
}

// A rule the configuration named is the rule that is used, whatever the
// terminal said. The setting exists for the case where the measurement gets
// it wrong, and a setting that is second-guessed by the program is not a
// setting.
func TestAConfiguredModeIsTheOneThatIsUsed(t *testing.T) {
	for _, mode := range []Mode{ModeGrapheme, ModeCodepoint} {
		measured := Measurement{Widths: appleTerminalAnswers}

		model, choice := Select(mode, measured)

		if choice.Mode != mode || choice.Source != SourceConfigured {
			t.Fatalf("Select(%v) = %v, want %v (configured)", mode, choice, mode)
		}
		if got := model.Mode(); got != mode {
			t.Fatalf("the model counts in %v, want %v", got, mode)
		}
	}
}

// telecli doctor does not draw a frame and does not measure a terminal, so
// it reports the rule and where it came from: the rule the configuration
// named, or the one a terminal nobody could ask is drawn with.
func TestDescribe(t *testing.T) {
	cases := []struct {
		configured Mode
		want       string
	}{
		{configured: ModeAuto, want: "grapheme (default)"},
		{configured: ModeGrapheme, want: "grapheme (configured)"},
		{configured: ModeCodepoint, want: "codepoint (configured)"},
	}

	for _, testCase := range cases {
		if got := Describe(testCase.configured).String(); got != testCase.want {
			t.Errorf(
				"Describe(%v) = %q, want %q",
				testCase.configured, got, testCase.want,
			)
		}
	}
}

// A model built without a measurement is the model of a terminal nobody
// could ask, and it is the model a program draws its first frame with.
func TestUnmeasured(t *testing.T) {
	if got := Unmeasured(ModeAuto); got.Mode() != ModeGrapheme {
		t.Fatalf("Unmeasured(auto) counts in %v, want grapheme", got.Mode())
	}
	if got := Unmeasured(ModeGrapheme); got.Mode() != ModeGrapheme {
		t.Fatalf("Unmeasured(grapheme) counts in %v, want grapheme", got.Mode())
	}
	if got := Unmeasured(ModeCodepoint); got.Mode() != ModeCodepoint {
		t.Fatalf("Unmeasured(codepoint) counts in %v, want codepoint", got.Mode())
	}
}

func TestParseMode(t *testing.T) {
	cases := []struct {
		value string
		want  Mode
	}{
		{value: "", want: ModeAuto},
		{value: "auto", want: ModeAuto},
		{value: "grapheme", want: ModeGrapheme},
		{value: "codepoint", want: ModeCodepoint},
		{value: "  Grapheme ", want: ModeGrapheme},
	}

	for _, testCase := range cases {
		got, err := ParseMode(testCase.value)
		if err != nil {
			t.Errorf("ParseMode(%q): %v", testCase.value, err)
			continue
		}
		if got != testCase.want {
			t.Errorf("ParseMode(%q) = %v, want %v", testCase.value, got, testCase.want)
		}
	}
}

// A word that is not one of the three is an error that says what the three
// are. An interface setting that is quietly ignored is a setting that
// cannot fix the thing it was written for.
func TestParseModeNamesTheValidOnes(t *testing.T) {
	_, err := ParseMode("wcwidth")
	if err == nil {
		t.Fatal("ParseMode(\"wcwidth\") did not fail")
	}

	for _, want := range []string{"auto", "grapheme", "codepoint", "wcwidth"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error %q does not name %q", err, want)
		}
	}
}

func TestModeNames(t *testing.T) {
	cases := map[Mode]string{
		ModeAuto:      "auto",
		ModeGrapheme:  "grapheme",
		ModeCodepoint: "codepoint",
		Mode(9):       "unknown",
	}

	for mode, want := range cases {
		if got := mode.String(); got != want {
			t.Errorf("Mode(%d).String() = %q, want %q", mode, got, want)
		}
	}
}

func TestSourceNames(t *testing.T) {
	cases := map[Source]string{
		SourceDefault:    "default",
		SourceConfigured: "configured",
		SourceMeasured:   "measured",
		Source(9):        "unknown",
	}

	for source, want := range cases {
		if got := source.String(); got != want {
			t.Errorf("Source(%d).String() = %q, want %q", source, got, want)
		}
	}
}

// Every probe is one glyph, and every probe is a case a terminal can answer
// differently from another one.
//
// It used to be asked that the two rules disagree about every probe, and
// they no longer do: the width of a glyph drawn from an emoji font is two
// columns in both rules, because it is the drawing that decides it and not
// the counting (EmojiLike). What the probes are for now is to tell the
// program which terminal it is talking to, so each one has to be a case
// where two terminals answer differently — which is what the answers of a
// wcwidth terminal and of one that follows the emoji rules say.
func TestTheProbesAreTheCasesTerminalsDisagreeAbout(t *testing.T) {
	if len(Probes) == 0 {
		t.Fatal("there are no probes")
	}

	apple := Measurement{Widths: appleTerminalAnswers}
	graphemeTerminal := Measurement{Widths: graphemeTerminalAnswers}

	distinguishing := 0
	for _, probe := range Probes {
		if pieces := piecesOf(probe); pieces != 1 {
			t.Errorf("%q is %d pieces, want one glyph", probe, pieces)
		}

		if apple.Widths[probe] != graphemeTerminal.Widths[probe] {
			distinguishing++
		}
	}

	if distinguishing == 0 {
		t.Fatal("no probe tells two terminals apart")
	}
}

// piecesOf returns how many grapheme clusters a string is, escape sequences
// apart: a probe is one glyph or it is a string, and a terminal's answer
// about a string is about several glyphs at once.
func piecesOf(value string) int {
	pieces := 0

	for range Unmeasured(ModeGrapheme).items(value) {
		pieces++
	}

	return pieces
}

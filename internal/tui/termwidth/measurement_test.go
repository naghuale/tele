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
	// appleTerminalAnswers is a terminal that counts code points.
	appleTerminalAnswers = map[string]int{
		"✌️":   1,
		"🇨🇳":   2,
		"🏃‍♂️": 2,
		"👍🏽":   2,
		"中":    2,
		"é":    1,
	}

	// graphemeTerminalAnswers is a terminal that counts clusters.
	graphemeTerminalAnswers = map[string]int{
		"✌️":   2,
		"🇨🇳":   2,
		"🏃‍♂️": 2,
		"👍🏽":   2,
		"中":    2,
		"é":    1,
	}
)

// A terminal that disagrees with the grapheme rule is counted by code
// points, because that is the rule it is following.
func TestATerminalThatCountsCodePointsIsDrawnByCodePoints(t *testing.T) {
	model, choice := Select(ModeAuto, Measurement{Widths: appleTerminalAnswers})

	if choice.Mode != ModeCodepoint || choice.Source != SourceMeasured {
		t.Fatalf("Select(auto, Apple Terminal) = %v, want codepoint (measured)", choice)
	}
	if got := model.StringWidth("Xiaomi News ✌️"); got != 13 {
		t.Fatalf("the title is %d columns, want 13", got)
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
// is a file, or the question went somewhere that does not answer. The rule
// it is drawn with is the one the macOS Terminal follows, because that is
// the terminal this program is written for and because being wrong about a
// symbol costs a column rather than the whole screen.
func TestATerminalThatAnswersNothingIsDrawnByCodePoints(t *testing.T) {
	for _, measured := range []Measurement{
		{},
		{TimedOut: true},
	} {
		model, choice := Select(ModeAuto, measured)

		if choice.Mode != ModeCodepoint || choice.Source != SourceDefault {
			t.Fatalf("Select(auto, %+v) = %v, want codepoint (default)", measured, choice)
		}
		if got := model.StringWidth("✌️"); got != 1 {
			t.Fatalf("the hand is %d columns, want 1", got)
		}
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
		{configured: ModeAuto, want: "codepoint (default)"},
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
	if got := Unmeasured(ModeAuto); got.Mode() != ModeCodepoint {
		t.Fatalf("Unmeasured(auto) counts in %v, want codepoint", got.Mode())
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

// Every probe is a case the two rules disagree about, and a terminal that
// agrees with none of them has not been asked what it does with letters.
func TestTheProbesAreTheCasesTheRulesDisagreeAbout(t *testing.T) {
	grapheme := Unmeasured(ModeGrapheme)
	codepoint := Unmeasured(ModeCodepoint)

	if len(Probes) == 0 {
		t.Fatal("there are no probes")
	}

	distinguishing := 0
	for _, probe := range Probes {
		if grapheme.StringWidth(probe) != codepoint.StringWidth(probe) {
			distinguishing++
		}
	}

	if distinguishing == 0 {
		t.Fatal("no probe tells the two rules apart")
	}
}

package termwidth

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// The two rules of this package, over the strings they disagree about.
//
// The table is the whole case: a chat title in Telegram carries a hand, a
// flag, a runner and a Han character as easily as it carries a letter, and
// the two rules put them in different columns. Everything a user of this
// program reads as a broken screen comes out of one of these rows.
//
// The strings the owner's screen of 01.10 broke on, and what each of them
// takes.
//
// These are not exotic. A Chinese channel name with a pagoda in it, a
// trading chat behind the flag of its country, a coffee in a preview, a
// hug and a ball and a heart in a message: the width of one of these
// clusters is the difference between a row that fits the window and one
// that runs past the last column of it, and a row past the last column is
// a row the terminal wraps.
//
// Every one of them is two columns, in both rules and whichever mode drew
// the row, because that is how wide a terminal draws the glyph and how many
// cells of it belong to it (EmojiLike). The pagoda is the case that says
// why the count cannot be a measurement: the macOS Terminal draws it two
// cells wide and advances the cursor one, so an answer about where the
// cursor went is not an answer about how wide the drawing is. What the
// terminal said about it is about the terminal, and the program reports it
// rather than laying out with it.
func TestTheWidthOfTheStringsTheOwnerSendsUs(t *testing.T) {
	cases := []struct {
		name     string
		value    string
		grapheme int
	}{
		{
			name:     "a pagoda as a letter",
			value:    "⛩",
			grapheme: 2,
		},
		{
			name:     "the same pagoda asking for the emoji drawing",
			value:    "⛩️",
			grapheme: 2,
		},
		{
			name:     "a flag of two regional indicators",
			value:    "🇨🇳",
			grapheme: 2,
		},
		{
			name:     "a cup of coffee, drawn wide on its own",
			value:    "☕",
			grapheme: 2,
		},
		{
			name:     "a heart in open hands",
			value:    "🫶",
			grapheme: 2,
		},
		{
			name:     "a hug",
			value:    "🤗",
			grapheme: 2,
		},
		{
			name:     "a hug, a ball and a heart in a row",
			value:    "🤗 🎾🔥🫶",
			grapheme: 9,
		},
		{
			name:     "a runner with a sign, joined into one glyph",
			value:    "🏃‍♂️",
			grapheme: 2,
		},
		{
			name:     "the whole title, symbols among the letters",
			value:    "⛩ФУЮАНЬ⛩ЖАОХЭ🇨🇳САХЭКСПО",
			grapheme: 25,
		},
		{
			name:     "letters and nothing else",
			value:    "САХЭКСПО",
			grapheme: 8,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			// Both rules, because the width of one of these clusters is
			// stated rather than counted and a mode that counted it
			// differently would draw a row the terminal wraps.
			for _, mode := range []Mode{ModeGrapheme, ModeCodepoint} {
				model := Unmeasured(mode)

				if got := model.StringWidth(testCase.value); got != testCase.grapheme {
					t.Errorf(
						"%v: StringWidth(%q) = %d, want %d",
						mode, testCase.value, got, testCase.grapheme,
					)
				}
			}

			// The renderer counts the row with its own rule, and a row
			// that disagrees with it is a row it cuts. A cluster drawn
			// wide out of an emoji font while the renderer counts it as
			// text is the one string where the two counts differ, and it
			// is why the program writes the column of every cell after
			// such a cluster instead of leaving it to the terminal
			// (internal/tui, the row painter).
			if !termwidthEmojiRow(testCase.value) {
				if got := RendererWidth(testCase.value); got != testCase.grapheme {
					t.Errorf(
						"renderer: StringWidth(%q) = %d, want %d",
						testCase.value, got, testCase.grapheme,
					)
				}
			}
		})
	}
}

// The measure and select over the owner's strings.
//
// The rule a terminal is given is the rule its own answers fit, and the
// rule of a terminal nobody could ask has to be the one the renderer and
// every terminal that follows the emoji rules agree on.
func TestTheRuleIsPickedFromTheOwnersStrings(t *testing.T) {
	// A terminal that answers the way one which follows the emoji rules
	// does: two columns for every glyph of the emoji blocks.
	measured := Measurement{
		Widths: map[string]int{
			"⛩":    2,
			"⛩️":   2,
			"🇨🇳":   2,
			"☕":    2,
			"🫶":    2,
			"🤗":    2,
			"🏃‍♂️": 2,
			"✌️":   2,
			"👍🏽":   2,
			"中":    2,
			"é":    1,
		},
	}

	model, choice := Select(ModeAuto, measured)
	if model.Mode() != ModeGrapheme {
		t.Errorf(
			"a terminal that answers for emoji chose %s, want grapheme",
			model.Mode(),
		)
	}
	if choice.Source != SourceMeasured {
		t.Errorf("the rule was %s, want measured", choice.Source)
	}
	if got := model.StringWidth("🤗 🎾🔥🫶"); got != 9 {
		t.Errorf("the measured model counts %q as %d, want 9", "🤗 🎾🔥🫶", got)
	}

	// Nothing answered: the rule of a terminal nobody was asked about is
	// the one the renderer counts with, so no row is ever a column wider to
	// the renderer than to the program that fitted it.
	quiet, choice := Select(ModeAuto, Measurement{})
	if quiet.Mode() != ModeGrapheme {
		t.Errorf("an unmeasured terminal chose %s, want grapheme", quiet.Mode())
	}
	if choice.Source != SourceDefault {
		t.Errorf("the unmeasured rule was %s, want default", choice.Source)
	}
}

// The two rules of this package, over the strings they disagree about.
//
// The table is the whole case: a chat title in Telegram carries a hand, a
// flag, a runner and a Han character as easily as it carries a letter, and
// the two rules put them in different columns. Everything a user of this
// program reads as a broken screen comes out of one of these rows.
func TestStringWidthOfTheDifficultStrings(t *testing.T) {
	cases := []struct {
		name  string
		value string
		// The columns the two rules give the string. They are the same
		// wherever a rule the terminal follows has to agree with the
		// other, and different exactly where the terminals differ.
		grapheme  int
		codepoint int
	}{
		{
			name:      "a hand with the emoji selector behind it",
			value:     "✌️",
			grapheme:  2,
			codepoint: 2,
		},
		{
			name:     "the same hand without it",
			value:    "✌",
			grapheme: 2, codepoint: 2,
		},
		{
			name:     "a flag of two regional indicators",
			value:    "🇨🇳",
			grapheme: 2, codepoint: 2,
		},
		{
			name:     "a runner joined to a sign",
			value:    "🏃‍♂️",
			grapheme: 2, codepoint: 2,
		},
		{
			name:     "a thumb with a skin tone",
			value:    "👍🏽",
			grapheme: 2, codepoint: 2,
		},
		{
			name:     "a family of five joined together",
			value:    "👨‍👩‍👧‍👦",
			grapheme: 2, codepoint: 2,
		},
		{
			name:     "a Han character",
			value:    "中",
			grapheme: 2, codepoint: 2,
		},
		{
			name:     "two of them",
			value:    "中文",
			grapheme: 4, codepoint: 4,
		},
		{
			name:     "an e and a combining acute",
			value:    "é",
			grapheme: 1, codepoint: 1,
		},
		{
			name:     "the same letter ready made",
			value:    "é",
			grapheme: 1, codepoint: 1,
		},
		{
			name:     "a word of six letters",
			value:    "привет",
			grapheme: 6, codepoint: 6,
		},
		{
			name:     "a chat title with a hand in it",
			value:    "Xiaomi News ✌️",
			grapheme: 14, codepoint: 14,
		},
		{
			name:     "a chat title with a flag in it",
			value:    "ТФУЮАНЬ 🇨🇳 САХ",
			grapheme: 14, codepoint: 14,
		},
		{
			name:     "a title that starts with an emoji",
			value:    "🏃 Секция новостей",
			grapheme: 18, codepoint: 18,
		},
		{
			name:     "the hand between two words",
			value:    "a✌️b",
			grapheme: 4, codepoint: 4,
		},
		{
			name:     "an ellipsis",
			value:    "…",
			grapheme: 1, codepoint: 1,
		},
		{
			name:     "nothing at all",
			value:    "",
			grapheme: 0, codepoint: 0,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			for _, mode := range []Mode{ModeGrapheme, ModeCodepoint} {
				model := Unmeasured(mode)

				want := testCase.codepoint
				if mode == ModeGrapheme {
					want = testCase.grapheme
				}

				if got := model.StringWidth(testCase.value); got != want {
					t.Errorf(
						"%s: StringWidth(%q) = %d, want %d",
						mode, testCase.value, got, want,
					)
				}
			}
		})
	}
}

// The escape sequences of a rendered line are not columns. A line that is
// measured with them in it is a line that is wider than the terminal and
// wraps, and everything under the wrap moves.
func TestEscapeSequencesTakeNoColumns(t *testing.T) {
	for _, mode := range []Mode{ModeGrapheme, ModeCodepoint} {
		model := Unmeasured(mode)

		cases := []struct {
			value string
			want  int
		}{
			{value: "\x1b[38;2;1;2;3mabc\x1b[0m", want: 3},
			{value: "\x1b[1mпривет\x1b[0m", want: 6},
			{value: "\x1b]0;a title\x07ab", want: 2},
			{value: "a\x1b[38;5;208mb", want: 2},
			// The sequences take nothing. What is between them is the
			// text, and its width is the one the rule gives it.
			{value: "\x1b[4m✌️\x1b[0m", want: model.StringWidth("✌️")},
		}

		for _, testCase := range cases {
			if got := model.StringWidth(testCase.value); got != testCase.want {
				t.Errorf(
					"%s: StringWidth(%q) = %d, want %d",
					mode, testCase.value, got, testCase.want,
				)
			}
		}
	}
}

// A cut is a cut between letters. A terminal prints the code points of a
// cluster one after another, so half a cluster is a broken letter and not a
// short one.
func TestTruncationNeverSplitsACluster(t *testing.T) {
	difficult := []string{
		"✌️✌✌",
		"🇨🇳🇨🇳",
		"🏃‍♂️",
		"👍🏽👍🏽",
		"中文中",
		"éé",
		"👨‍👩‍👧‍👦👨‍👩‍👧‍👦",
	}

	for _, mode := range []Mode{ModeGrapheme, ModeCodepoint} {
		model := Unmeasured(mode)

		for _, value := range difficult {
			total := model.StringWidth(value)

			for width := 0; width < total+2; width++ {
				cut := model.Truncate(value, width, "")

				if got := model.StringWidth(cut); got > width {
					t.Errorf(
						"%s: Truncate(%q, %d) = %q is %d columns",
						mode, value, width, cut, got,
					)
				}
				if isSplitCluster(value, cut) {
					t.Errorf(
						"%s: Truncate(%q, %d) = %q cuts a cluster in half",
						mode, value, width, cut,
					)
				}
			}
		}
	}
}

// Whatever the rule, a cut is a cut between clusters. The two rules do not
// have to keep the same text — a rule that says the hand is one column can
// keep it where a rule that says two cannot — but neither of them may print
// half of one, because a terminal prints the code points of a cluster in
// sequence and half of it is a broken letter.
func TestBothRulesCutWholeClusters(t *testing.T) {
	value := "✌️🇨🇳👍🏽"

	for width := 0; width < 8; width++ {
		for _, mode := range []Mode{ModeGrapheme, ModeCodepoint} {
			model := Unmeasured(mode)
			cut := model.Truncate(value, width, "")

			if isSplitCluster(value, cut) {
				t.Errorf(
					"%s: Truncate(%q, %d) = %q cuts a cluster in half",
					mode, value, width, cut,
				)
			}
			if got := model.StringWidth(cut); got > width {
				t.Errorf(
					"%s: Truncate(%q, %d) = %q is %d columns",
					mode, value, width, cut, got,
				)
			}
		}
	}
}

// isSplitCluster reports whether cut is a prefix of value that stops in the
// middle of a cluster: it is a prefix, and what it cut off is not a whole
// cluster boundary of the rest.
func isSplitCluster(value, cut string) bool {
	if !strings.HasPrefix(value, cut) || cut == "" || cut == value {
		return false
	}

	rest := value[len(cut):]
	first, _, _ := strings.Cut(rest, "")

	// The first rune of the rest is the one that was cut in half when the
	// rest is a continuation of the cluster the prefix stopped inside: a
	// combining mark, a variation selector or a joiner never begins a
	// cluster of its own.
	runes := []rune(first)
	if len(runes) == 0 {
		return false
	}

	head := runes[0]

	return head == 0xfe0f || head == 0xfe0e || head == 0x200d ||
		(head >= 0x0300 && head <= 0x036f) ||
		(head >= 0x1f3fb && head <= 0x1f3ff)
}

func TestTruncate(t *testing.T) {
	model := Unmeasured(ModeGrapheme)

	cases := []struct {
		name  string
		value string
		width int
		tail  string
		want  string
	}{
		{name: "what fits is what it was", value: "abc", width: 5, want: "abc"},
		{name: "more room than the text needs", value: "abc", width: 5, tail: "…", want: "abc"},
		{name: "a cut is marked", value: "abcdef", width: 4, tail: "…", want: "abc…"},
		{name: "a mark with no room for text", value: "abcdef", width: 1, tail: "…", want: "…"},
		{name: "no columns at all", value: "abc", width: 0, tail: "…", want: ""},
		{name: "a negative width", value: "abc", width: -1, tail: "…", want: ""},
		{name: "an escape sequence is not cut", value: "\x1b[31mabcdef\x1b[0m", width: 3, want: "\x1b[31mabc\x1b[0m"},
		{name: "a wide cluster is left out whole", value: "中中", width: 3, want: "中"},
		{name: "a cluster wider than the line", value: "✌️✌", width: 2, tail: "…", want: "…"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := model.Truncate(testCase.value, testCase.width, testCase.tail)
			if got != testCase.want {
				t.Fatalf(
					"Truncate(%q, %d, %q) = %q, want %q",
					testCase.value, testCase.width, testCase.tail, got, testCase.want,
				)
			}
		})
	}
}

// A mark is only worth anything next to the text it marks. A one column
// field holding a lone ellipsis says nothing, so the first letter of the
// name is worth more.
func TestTruncateMarked(t *testing.T) {
	model := Unmeasured(ModeGrapheme)

	cases := []struct {
		name  string
		value string
		width int
		want  string
	}{
		{name: "a title with room for the mark", value: "abcdef", width: 4, want: "abc…"},
		{name: "a title too narrow for the mark", value: "abcdef", width: 1, want: "a"},
		{name: "a title that fits", value: "abc", width: 3, want: "abc"},
		{name: "nothing at all", value: "abc", width: 0, want: ""},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := model.TruncateMarked(testCase.value, testCase.width, "…")
			if got != testCase.want {
				t.Fatalf(
					"TruncateMarked(%q, %d) = %q, want %q",
					testCase.value, testCase.width, got, testCase.want,
				)
			}
		})
	}
}

// The tail of a line that a popup is drawn over is still on the screen, and
// it is still the colour it was drawn in. Throwing the escape sequences
// away with the text in front of them prints the tail in whatever colour
// came before, which on a coloured screen is a stripe of the wrong colour
// down the middle of a conversation.
func TestTruncateLeftKeepsTheColourOfTheTail(t *testing.T) {
	for _, mode := range []Mode{ModeGrapheme, ModeCodepoint} {
		model := Unmeasured(mode)

		line := "\x1b[48;2;24;24;36m     hello     \x1b[0m"

		tail := model.TruncateLeft(line, 5, "")
		if !strings.HasPrefix(tail, "\x1b[48;2;24;24;36m") {
			t.Errorf("%s: the tail of %q is %q, want the colour of the line", mode, line, tail)
		}
		if got := model.StringWidth(tail); got != 10 {
			t.Errorf("%s: the tail of %q is %d columns, want 10", mode, line, got)
		}
		if !strings.HasSuffix(tail, "\x1b[0m") {
			t.Errorf("%s: the tail of %q is %q, want it closed", mode, line, tail)
		}
	}
}

func TestTruncateLeft(t *testing.T) {
	model := Unmeasured(ModeGrapheme)

	cases := []struct {
		name   string
		value  string
		remove int
		head   string
		want   string
	}{
		{name: "nothing removed", value: "abcdef", want: "abcdef"},
		{name: "a negative removal", value: "abcdef", remove: -1, want: "abcdef"},
		{name: "one column off the front", value: "abcdef", remove: 1, want: "abcde"},
		{name: "more than the line holds", value: "abc", remove: 9, want: ""},
		{name: "the head says what was cut", value: "abcdef", remove: 2, head: "…", want: "…abcd"},
		{name: "a wide cluster is left out whole", value: "中文", remove: 1, want: "中"},
		{name: "a styled line keeps its sequences", value: "\x1b[31mabcdef\x1b[0m", remove: 2, want: "\x1b[31mabcd\x1b[0m"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := model.TruncateLeft(testCase.value, testCase.remove, testCase.head)
			if got != testCase.want {
				t.Fatalf(
					"TruncateLeft(%q, %d, %q) = %q, want %q",
					testCase.value, testCase.remove, testCase.head, got, testCase.want,
				)
			}
		})
	}
}

// Prose is wrapped, not cut: a paragraph that loses the part that says what
// to do about the problem has lost the part that matters.
func TestWrap(t *testing.T) {
	model := Unmeasured(ModeGrapheme)

	cases := []struct {
		name  string
		value string
		width int
		want  []string
	}{
		{
			name:  "words that fit stay together",
			value: "one two three",
			width: 9,
			want:  []string{"one two", "three"},
		},
		{
			name:  "a line the text already has",
			value: "one\ntwo",
			width: 8,
			want:  []string{"one", "two"},
		},
		{
			name:  "an empty line stays an empty line",
			value: "one\n\ntwo",
			width: 8,
			want:  []string{"one", "", "two"},
		},
		{
			name:  "a word wider than the line is marked",
			value: "see https://example.com/a/very/long/path now",
			width: 12,
			want:  []string{"see", "https://exa…", "now"},
		},
		{
			name:  "no width at all",
			value: "one two",
			width: 0,
			want:  []string{""},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := model.Wrap(testCase.value, testCase.width, "…")
			if len(got) != len(testCase.want) {
				t.Fatalf("Wrap(%q, %d) = %q, want %q", testCase.value, testCase.width, got, testCase.want)
			}
			for index := range got {
				if got[index] != testCase.want[index] {
					t.Fatalf(
						"Wrap(%q, %d) = %q, want %q",
						testCase.value, testCase.width, got, testCase.want,
					)
				}
				if width := model.StringWidth(got[index]); width > testCase.width && testCase.width > 0 {
					t.Fatalf(
						"Wrap(%q, %d) line %d is %d columns: %q",
						testCase.value, testCase.width, index, width, got[index],
					)
				}
			}
		})
	}
}

// A region is a rectangle or it is not a region: a line that ends where its
// text ended leaves the background of the pane showing through beside it.
func TestFitPadsAndCuts(t *testing.T) {
	for _, mode := range []Mode{ModeGrapheme, ModeCodepoint} {
		model := Unmeasured(mode)

		cases := []struct {
			value string
			width int
			want  string
		}{
			{value: "abc", width: 6, want: "abc   "},
			{value: "привет", width: 4, want: "при…"},
			{value: "abcdefgh", width: 3, want: "ab…"},
			{value: "abc", width: 0, want: ""},
		}

		for _, testCase := range cases {
			got := model.Fit(testCase.value, testCase.width, "…")
			if got != testCase.want {
				t.Errorf(
					"%s: Fit(%q, %d) = %q, want %q",
					mode, testCase.value, testCase.width, got, testCase.want,
				)
			}
			if width := model.StringWidth(got); width != maxInt(testCase.width, 0) {
				t.Errorf(
					"%s: Fit(%q, %d) is %d columns, want %d",
					mode, testCase.value, testCase.width, width, testCase.width,
				)
			}
		}

		// A line of a cluster the two rules disagree about is padded to
		// the width, and the disagreement is paid for in a column of air
		// at the right edge: the renderer counts the row the width it was
		// given, and a row it counts wider than the window is a row it
		// cuts, which is a row whose last cell belongs to the row above.
		hand := model.Fit("✌️", 4, "…")
		if !strings.HasPrefix(hand, "✌️") {
			t.Errorf("%s: Fit(%q, 4) = %q, want the text kept", mode, "✌️", hand)
		}
		if width := ansi.StringWidth(hand); width != 4 {
			t.Errorf(
				"%s: Fit(%q, 4) is %d columns to the renderer, want 4",
				mode, "✌️", width,
			)
		}
		if width := model.StringWidth(hand); width > 4 {
			t.Errorf(
				"%s: Fit(%q, 4) is %d columns to this program, want at most 4",
				mode, "✌️", width,
			)
		}
	}
}

// No line of a region may be wider than the window to the rule of either
// the program that laid it out or the renderer that draws it.
//
// This is the rule that keeps the frame a rectangle, and it is the whole
// of it: a row the renderer counts wider than the window is a row it cuts,
// and a row the terminal counts wider than the window is a row it wraps,
// which moves everything under it down a row and leaves the row above the
// wrap showing the tail of the one before it. The list that goes wrong when
// that happens is a list with two search lines in it, a preview under the
// wrong name, and a cell of an old background at the edge of a row.
func TestFitKeepsEveryLineWithinTheWidthOfBothRules(t *testing.T) {
	for _, mode := range []Mode{ModeGrapheme, ModeCodepoint} {
		model := Unmeasured(mode)

		for _, value := range []string{
			"abc",
			"привет",
			"Елена ✌️ Батрашина",
			"❤️",
			"⚠️ ошибка",
			"🏳️‍🌈 флаг",
			"привет, как сборка?",
		} {
			for width := 1; width <= 40; width++ {
				row := model.Fit(value, width, "…")

				if got := ansi.StringWidth(row); got > width {
					t.Errorf(
						"%s: Fit(%q, %d) is %d columns to the renderer",
						mode, value, width, got,
					)
				}
				if got := model.StringWidth(row); got > width {
					t.Errorf(
						"%s: Fit(%q, %d) is %d columns to this program",
						mode, value, width, got,
					)
				}
			}
		}
	}
}

// The model of a terminal that answered nothing is the codepoint rule, and
// a model built without saying anything at all is the same one: a screen
// has to be drawable before anybody has measured the terminal behind it.
//
// The width of an emoji is the one thing a zero model does not have to
// guess: it is two columns of a glyph that is drawn from an emoji font,
// which is the most any terminal draws and the only number that can be
// wrong by a column of air rather than by one cell of overlap.
func TestTheZeroModelCountsByCodePoint(t *testing.T) {
	var zero WidthModel

	if zero.Mode() != ModeCodepoint {
		t.Fatalf("the zero model counts in %v, want %v", zero.Mode(), ModeCodepoint)
	}
	if got := zero.StringWidth("✌️"); got != 2 {
		t.Fatalf("the zero model counts ✌️ as %d columns, want 2", got)
	}
}

// A width a terminal was asked about is not the width of a glyph drawn from
// an emoji font, and saying it is not a bug in the measurement: the
// question was where the cursor went, and the answer is that.
//
// The macOS Terminal draws a flag in two cells and advances the cursor two,
// and the macOS Terminal draws a pagoda in two cells and advances one. A
// program that laid out by the answer would be right about the flag and a
// cell of overlap wrong about the pagoda, and the owner's screen of 01.10
// is the second of those. So the width of such a cluster is two columns and
// the cursor after it is placed by the program rather than left where the
// terminal put it.
func TestAMeasuredWidthIsNotTheWidthOfAnEmoji(t *testing.T) {
	measured := Measurement{
		Widths: map[string]int{"🇨🇳": 1, "✌️": 1, "⛩": 1, "中": 2, "é": 1},
	}

	for _, configured := range []Mode{ModeAuto, ModeGrapheme, ModeCodepoint} {
		model, _ := Select(configured, measured)

		for _, emoji := range []string{"🇨🇳", "✌️", "⛩"} {
			if got := model.StringWidth(emoji); got != 2 {
				t.Errorf(
					"Select(%v, %q measured as one column) = %d, want 2",
					configured, emoji, got,
				)
			}
		}
		if got := model.StringWidth("🇨🇳🇺🇸"); got != 4 {
			t.Errorf(
				"Select(%v, a flag of one column): two flags = %d, want 4",
				configured, got,
			)
		}

		// A letter is still measured: a Han character and an e with a
		// combining acute are the cases the measurement is for.
		if got := model.StringWidth("中"); got != 2 {
			t.Errorf("Select(%v): the Han character is %d columns, want 2", configured, got)
		}
	}
}

// The rounded ends of a message block are one column in both rules, and a
// terminal that was asked about one of them does not get to say otherwise.
//
// The two halves are what the interface draws around a message of this
// user when `tui.nerd_font` is on, and the block they sit on is fitted to
// a width the interface computed from that one column. A measurement that
// disagreed would move one end of every block and not the other, and the
// row would be a column over the width of the feed — which is a row the
// terminal wraps, and a wrapped row moves everything under it.
func TestTheRoundedEndsOfABlockAreOneColumnWide(t *testing.T) {
	measured := Measurement{Widths: map[string]int{
		NerdHalfLeft:  2,
		NerdHalfRight: 2,
	}}

	for _, mode := range []Mode{ModeGrapheme, ModeCodepoint} {
		unasked := newWidthModel(mode, nil)
		asked := newWidthModel(mode, measured.Widths)

		for _, glyph := range []string{NerdHalfLeft, NerdHalfRight} {
			if got := unasked.StringWidth(glyph); got != nerdHalfWidth {
				t.Errorf(
					"%v: %q is %d columns, want %d",
					mode, glyph, got, nerdHalfWidth,
				)
			}
			if got := asked.StringWidth(glyph); got != nerdHalfWidth {
				t.Errorf(
					"%v measured at two: %q is %d columns, want %d",
					mode, glyph, got, nerdHalfWidth,
				)
			}
		}

		// The two of them are the ends of one block, so the block is
		// exactly as wide as its two ends say: two columns between them
		// and nothing else.
		block := NerdHalfLeft + "ok" + NerdHalfRight
		if got := unasked.StringWidth(block); got != 4 {
			t.Errorf("%v: the block with both ends is %d columns, want 4", mode, got)
		}
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}

	return b
}

// termwidthEmojiRow reports whether the row is one the renderer counts
// narrower than the program does: a text-default symbol of the symbol block
// drawn wide out of the emoji font.
func termwidthEmojiRow(value string) bool {
	return RendererWidth(value) != Unmeasured(ModeGrapheme).StringWidth(value)
}

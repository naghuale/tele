package theme

import "testing"

// §24 asks for the contrast of text to be checked, and WCAG AA for body
// text is 4.5:1. The built-in palettes are held to it for the two roles a
// user actually reads, on every surface those roles are drawn on.
//
// The two dimmer tiers are below the bar by design: muted and disabled
// text say nothing that the words do not already say, and a theme whose
// timestamps are the only thing at 2:1 is a theme where the timestamps
// cannot be read at all. What is checked instead is that the tiers get
// dimmer in order, so a future palette change cannot quietly make a
// secondary text brighter than a primary one.

// textBackgroundRoles are the surfaces PrimaryText and SecondaryText are
// required to be readable on.
var textBackgroundRoles = []string{
	"AppBackground",
	"SidebarBackground",
	"ChatBackground",
	"ComposerBackground",
}

// contrastTextRoles are the two roles held to the WCAG AA bar.
var contrastTextRoles = []string{"PrimaryText", "SecondaryText"}

// textRoles walks the four text tiers in the order they must lose
// prominence.
var textRoles = []string{
	"PrimaryText",
	"SecondaryText",
	"MutedText",
	"DisabledText",
}

func TestEveryBuiltInThemeHasReadableText(t *testing.T) {
	for _, name := range ThemeNames() {
		built := mustTheme(t, name)
		roles := colorRoles(t, built.Tokens)

		for _, textRole := range contrastTextRoles {
			text := roles[textRole]

			for _, backgroundRole := range textBackgroundRoles {
				background := roles[backgroundRole]

				ratio := text.ContrastRatio(background)
				if ratio < MinimumTextContrast {
					t.Errorf(
						"theme %s: %s (%s) on %s (%s) = %.2f:1, want at least %.1f:1",
						name,
						textRole,
						text,
						backgroundRole,
						background,
						ratio,
						MinimumTextContrast,
					)
				}
			}
		}
	}
}

// The tiers must lose prominence, or a "muted" label would be the
// brightest thing on the screen.
func TestTextTiersGetDimmerInOrder(t *testing.T) {
	for _, name := range ThemeNames() {
		built := mustTheme(t, name)
		roles := colorRoles(t, built.Tokens)

		previous := 0.0
		for index, role := range textRoles {
			ratio := roles[role].ContrastRatio(roles["AppBackground"])
			if ratio <= 0 {
				t.Fatalf("theme %s: role %s has no luminance", name, role)
			}

			if index > 0 && ratio > previous {
				t.Errorf(
					"theme %s: %s is %.2f:1, brighter than the tier above it at %.2f:1",
					name,
					role,
					ratio,
					previous,
				)
			}
			previous = ratio
		}
	}
}

// A status must not read as another status by colour alone, and §24 asks
// in particular that uncertain does not look like failed. The symbols and
// the words already do that; this is about the colour not undoing it.
func TestUncertainStatusDiffersFromFailed(t *testing.T) {
	for _, name := range ThemeNames() {
		built := mustTheme(t, name)

		if built.Tokens.StatusUncertain == built.Tokens.StatusError {
			t.Errorf(
				"theme %s: uncertain and failed share the colour %v",
				name,
				built.Tokens.StatusError,
			)
		}
	}
}

// The contrast ratio is a measurement, and a measurement that is wrong is
// worse than none: these are the reference values of §24.
func TestContrastRatioMatchesTheReferenceValues(t *testing.T) {
	cases := map[string]struct {
		a    Color
		b    Color
		want float64
	}{
		"black on white": {a: RGB("#000000"), b: RGB("#ffffff"), want: 21},
		"white on white": {a: RGB("#ffffff"), b: RGB("#ffffff"), want: 1},
		"grey on white":  {a: RGB("#767676"), b: RGB("#ffffff"), want: 4.54},
		"light grey on black": {
			a:    RGB("#e0e0e0"),
			b:    RGB("#000000"),
			want: 15.91,
		},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			got := testCase.a.ContrastRatio(testCase.b)

			// A tenth of a ratio is more than enough resolution for a
			// threshold, and exact equality would make the test a copy of
			// the implementation.
			if diff := got - testCase.want; diff > 0.1 || diff < -0.1 {
				t.Fatalf(
					"ContrastRatio() = %.2f, want %.2f",
					got,
					testCase.want,
				)
			}
		})
	}
}

// A colour that is not set has no luminance, and a ratio that flattered
// it would let a theme claim a readability it does not have.
func TestContrastRatioOfAnUnsetColourIsZero(t *testing.T) {
	unset := Color{}

	if got := unset.ContrastRatio(RGB("#ffffff")); got != 0 {
		t.Errorf("ratio with an unset colour = %.2f, want 0", got)
	}
	if got := ContrastRatio(RGB("#ffffff"), unset); got != 0 {
		t.Errorf("ratio against an unset colour = %.2f, want 0", got)
	}
	if got := ContrastRatio(Basic(7), RGB("#ffffff")); got != 0 {
		t.Errorf(
			"ratio with a basic colour = %.2f, want 0: its luminance is "+
				"whatever the user's terminal decided",
			got,
		)
	}
}

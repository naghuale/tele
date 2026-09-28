package theme

import "testing"

// §24 asks for the contrast of text to be checked, and WCAG AA for body
// text is 4.5:1. The built-in palettes are held to it for the two roles a
// user actually reads, on every surface those roles are drawn on.
//
// The muted tier is held to the same bar on the two surfaces a chat
// preview and a timestamp are drawn on, which is what makes them readable
// rather than merely present: a timestamp nobody can read is not a
// timestamp, and the words of the preview are somebody else's. The
// disabled tier is below it by design, because a disabled control says
// nothing the words around it do not say. What is checked instead is that
// the tiers get dimmer in order, so a future palette change cannot quietly
// make a secondary text brighter than a primary one.

// textBackgroundRoles are the surfaces PrimaryText and SecondaryText are
// required to be readable on.
var textBackgroundRoles = []string{
	"AppBackground",
	"SidebarBackground",
	"ChatBackground",
	"ComposerBackground",
}

// mutedBackgroundRoles are the surfaces MutedText is required to be
// readable on. It is the colour of a chat preview and of a time, and both
// are drawn on the list or in the timeline; the composer is a draft the
// user is writing, and a placeholder there is a hint rather than text.
var mutedBackgroundRoles = []string{
	"SidebarBackground",
	"ChatBackground",
}

// contrastTextRoles are the two roles held to the WCAG AA bar everywhere.
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

// The previews and the times are read, so the tier that draws them has to
// be held to the same bar as the words above them. A palette change that
// quietly puts a timestamp at 3:1 has taken a fact off the screen, and no
// other test would notice: the words are still there and only the colour
// has moved.
func TestMutedTextIsReadableWhereItIsDrawn(t *testing.T) {
	for _, name := range ThemeNames() {
		built := mustTheme(t, name)
		roles := colorRoles(t, built.Tokens)
		muted := roles["MutedText"]

		for _, backgroundRole := range mutedBackgroundRoles {
			background := roles[backgroundRole]

			ratio := muted.ContrastRatio(background)
			if ratio < MinimumTextContrast {
				t.Errorf(
					"theme %s: MutedText (%s) on %s (%s) = %.2f:1, want at least %.1f:1",
					name,
					muted,
					backgroundRole,
					background,
					ratio,
					MinimumTextContrast,
				)
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

// The muted tier of every preset is the first step of the walk from the
// palette's dim step towards its text ramp that clears the bar on both
// surfaces a preview and a timestamp are drawn on.
//
// That is a stronger statement than "it clears the bar": a palette that
// wrote a value one step too bright, or the text ramp itself, would still
// pass a threshold test and would be a tier that is not a tier.
func TestTheMutedTierOfEveryPresetIsTheFirstReadableStep(t *testing.T) {
	for _, name := range ThemeNames() {
		built := mustTheme(t, name)
		palette := built.Palette

		if !palette.Muted.IsSet() {
			t.Errorf("theme %s: the palette names no muted step", name)
			continue
		}

		surfaces := mutedSurfaces(palette, built.Mode != ThemeModeLight)
		want := readableMuted(palette.Overlay0, palette.Text, surfaces)

		if palette.Muted != want {
			t.Errorf(
				"theme %s: the palette names the muted step %v, want %v",
				name,
				palette.Muted,
				want,
			)
		}
	}
}

// The walk itself: a dim step a user can read is its own answer, because a
// lift nobody needed is a colour the preset did not choose; a step that
// has to move stops at the first one that clears; and a ramp on which
// nothing clears gives up at the top of it rather than returning
// something unreadable out of the middle.
func TestTheMutedWalkStopsWhereItShould(t *testing.T) {
	ramp := RGB("#ffffff")

	readable := []Color{RGB("#101010"), RGB("#0c0c0c")}
	if got := readableMuted(RGB("#909090"), ramp, readable); got != RGB("#909090") {
		t.Errorf("a readable dim step was lifted to %v, want it left alone", got)
	}

	unreadable := []Color{RGB("#404040"), RGB("#3a3a3a")}
	got := readableMuted(RGB("#3c3c3c"), ramp, unreadable)
	if got == RGB("#3c3c3c") {
		t.Errorf("an unreadable dim step was left at %v", got)
	}
	for _, surface := range unreadable {
		if got.ContrastRatio(surface) < MinimumTextContrast {
			t.Errorf(
				"the lifted step is at %.2f:1 on a surface it is drawn on",
				got.ContrastRatio(surface),
			)
		}
	}

	if got := readableMuted(RGB("#dddddd"), ramp, []Color{ramp}); got != ramp {
		t.Errorf("an unreadable ramp gave %v, want the text ramp", got)
	}
}

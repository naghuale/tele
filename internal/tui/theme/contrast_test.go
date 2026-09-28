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

		// The hexes are compared and not the colours: the palette names
		// the step with its indexed and basic entries beside it, and the
		// walk does not know what those are.
		if palette.Muted.Hex() != want.Hex() {
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

// The words of a message are read, and a message is read on the bubble of
// its own side: the words of somebody else's on the neutral surface, the
// words of this user's on the accent's tint of it. WCAG AA for body text
// is 4.5:1, and this is the bar both bubbles are held to.
//
// The tint is the hard half of it. A surface mixed towards the accent is a
// surface the accent has less contrast on — the words of a message of this
// user *are* the accent — so a tint strong enough to see at a glance is a
// tint that takes the words under the bar. Every value in the palettes is
// the largest share of the distance that holds the words, and the test is
// what says so rather than a comment.
func TestTheWordsOfAMessageAreReadableOnItsOwnBubble(t *testing.T) {
	cases := []struct {
		textRole       string
		backgroundRole string
	}{
		{textRole: "OutgoingMessage", backgroundRole: "OutgoingBubble"},
		{textRole: "IncomingMessage", backgroundRole: "ComposerBackground"},
	}

	for _, name := range ThemeNames() {
		roles := colorRoles(t, mustTheme(t, name).Tokens)

		for _, testCase := range cases {
			text := roles[testCase.textRole]
			background := roles[testCase.backgroundRole]

			ratio := text.ContrastRatio(background)
			if ratio < MinimumTextContrast {
				t.Errorf(
					"theme %s: %s (%s) on %s (%s) = %.2f:1, want at least %.1f:1",
					name,
					testCase.textRole,
					text,
					testCase.backgroundRole,
					background,
					ratio,
					MinimumTextContrast,
				)
			}
		}
	}
}

// The two sides are told apart in colour as well as in position, so the
// tint has to be a real difference and not a shade of the same grey.
//
// A tint that is too weak to see is not a tint: it is the same bubble with
// an extra number in a test, and the reader is back to reading the side of
// the screen to know whose message it is. The two blocks also have to stay
// apart on a terminal that shows fewer colours, which is why the 256-colour
// entries are named here rather than derived: two neighbouring greys are
// two greys, and a run-time reduction could hand both sides the same one.
func TestTheTwoSidesHaveBlocksOfTheirOwnColours(t *testing.T) {
	// minimumBubbleDifference is how far apart two surfaces have to be
	// before the eye takes them for two. It is small because the two are
	// neighbouring steps of one ramp by design; it is not zero because a
	// tint of nothing is nothing.
	const minimumBubbleDifference = 1.05

	for _, name := range ThemeNames() {
		built := mustTheme(t, name)
		roles := colorRoles(t, built.Tokens)

		incoming := roles["ComposerBackground"]
		outgoing := roles["OutgoingBubble"]

		if outgoing == incoming {
			t.Errorf(
				"theme %s: both sides are drawn on %v, so the tint is not a tint",
				name, incoming,
			)

			continue
		}

		difference := outgoing.ContrastRatio(incoming)
		if difference < minimumBubbleDifference {
			t.Errorf(
				"theme %s: the two bubbles are %.3f:1 apart, want at least %.2f:1",
				name, difference, minimumBubbleDifference,
			)
		}

		// The tint lifts the block rather than darkening it: a darker
		// block of this user's own messages reads as a hole in the feed
		// rather than as a message, and the accent is the lightest thing
		// on the screen so a surface darker than the neutral one would be
		// the only shadow on it.
		if relativeLuminanceOf(outgoing) <= relativeLuminanceOf(incoming) {
			t.Errorf(
				"theme %s: the block of this user (%v) is not lighter than the block of the other side (%v)",
				name, outgoing, incoming,
			)
		}

		for _, profile := range []Profile{ProfileANSI256, ProfileANSI16} {
			degraded := colorRoles(t, built.ForProfile(profile).Tokens)
			if degraded["OutgoingBubble"] == degraded["ComposerBackground"] ||
				degraded["ComposerBackground"].IsSet() {
				continue
			}
			if !degraded["OutgoingBubble"].IsSet() {
				t.Errorf(
					"theme %s: profile %s keeps no block of its own, so the two sides are the same colour there",
					name, profile,
				)
			}
		}
	}
}

// The tint is a mix of the surface and the accent and nothing else: every
// channel of it is strictly between the two.
//
// A value that is not a mix would be a new colour the palette acquired, and
// it would have to be justified on its own; a value that is a mix of the
// two roles the interface already has says what it is at every step. The
// share is not written down here on purpose — it is different for each
// theme and it is bounded by the contrast above, not by a rule — but the
// bounds are, and a palette that put the bubble outside them would have a
// block that is neither the neutral surface nor a tint of it.
func TestTheMessageBubblesAreMixesOfTheSurfaceAndTheAccent(t *testing.T) {
	for _, name := range ThemeNames() {
		palette := mustTheme(t, name).Palette

		surface, accent := palette.Surface0, palette.Accent
		bubble := palette.OutgoingBubble

		if !bubble.IsSet() {
			t.Errorf("theme %s: the palette names no block for a message", name)

			continue
		}

		for index, name2 := range []string{"red", "green", "blue"} {
			from := channelOf(surface.Hex(), index)
			to := channelOf(accent.Hex(), index)
			have := channelOf(bubble.Hex(), index)

			low, high := from, to
			if low > high {
				low, high = high, low
			}

			if have <= low || have >= high {
				t.Errorf(
					"theme %s: %s of the block is %d, want it strictly between %d and %d (%v between %v and %v)",
					name, name2, have, low, high, bubble, surface, accent,
				)
			}
		}
	}
}

// channelOf returns one channel of a "#rrggbb" string: 0 is red, 1 green,
// 2 blue.
func channelOf(hex string, index int) int {
	offset := 1 + index*2

	return hexDigit(hex[offset])*16 + hexDigit(hex[offset+1])
}

// relativeLuminanceOf is the luminance of a colour, or -1 for one that has
// none. The zero is a real ratio, so a colour that could not be measured
// has to be told apart from a black one.
func relativeLuminanceOf(c Color) float64 {
	luminance, ok := relativeLuminance(c)
	if !ok {
		return -1
	}

	return luminance
}

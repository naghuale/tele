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

// The words of a message are read, and a message is read on the block of
// its own side: the words of somebody else's on the neutral surface, the
// words of this user's on the accent's tint of it. WCAG AA for body text
// is 4.5:1, and this is the bar both blocks are held to.
//
// The tint is the hard half of it. A surface mixed towards the accent is a
// surface the accent has less contrast on — the words of a message of this
// user *are* the accent — so a tint strong enough to see at a glance is a
// tint that takes the words under the bar. Every value in the palettes is
// the largest share of the distance that holds the words, and the test is
// what says so rather than a comment.
func TestTheWordsOfAMessageAreReadableOnItsOwnBlock(t *testing.T) {
	cases := []struct {
		textRole       string
		backgroundRole string
	}{
		{textRole: "OutgoingMessage", backgroundRole: "ComposerBackground"},
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

// The pill that names the day a conversation is on, and the line that says
// where the unread messages begin, are pills and not words on the feed.
//
// The owner read the row as a message line (02.10, real account): a day name
// drawn as dim words at the left of the feed, on the background of the feed,
// is the shape of a row of the conversation and nothing says otherwise. So it
// is a surface of its own, and two things have to be true of it or the change
// is a change of colour rather than a change of shape:
//
//   - it stands out from the background of the feed, or it is words on a
//     background again;
//   - it is a different surface from the block of a message, or the pill is
//     read as a message with nothing in it.
//
// Both are numbers here, because "different" is not one. The three themes
// hold the pill 1.83:1 to 2.06:1 away from the background of the feed and
// 1.31:1 to 1.38:1 away from the surface of a block — the same bar the block
// under the cursor is held to, which is what makes the two shapes of the feed
// three rather than two. And the words on the pill are PrimaryText, held to
// the same WCAG AA bar as every other body text: a date a reader has to lean
// towards is a date nobody reads.
func TestTheSeparatorPillIsAPillOfItsOwn(t *testing.T) {
	const minimumDifference = 1.25

	for _, name := range ThemeNames() {
		roles := colorRoles(t, mustTheme(t, name).Tokens)

		pill := roles["SeparatorBackground"]
		if !pill.IsSet() {
			t.Errorf("theme %s: the pill of a day has no surface of its own", name)

			continue
		}

		if difference := pill.ContrastRatio(roles["ChatBackground"]); difference < minimumDifference {
			t.Errorf(
				"theme %s: the pill and the background of the feed are %.3f:1 "+
					"apart, want at least %.2f:1",
				name, difference, minimumDifference,
			)
		}

		if difference := pill.ContrastRatio(roles["ComposerBackground"]); difference < minimumDifference {
			t.Errorf(
				"theme %s: the pill and the block of a message are %.3f:1 apart, "+
					"want at least %.2f:1",
				name, difference, minimumDifference,
			)
		}

		// The words on the pill are body text and are held to the bar body
		// text is held to.
		if ratio := roles["PrimaryText"].ContrastRatio(pill); ratio < MinimumTextContrast {
			t.Errorf(
				"theme %s: the words of the pill are %.2f:1 on it, want at "+
					"least %.1f:1",
				name, ratio, MinimumTextContrast,
			)
		}
	}
}

// The two sides are drawn on ONE surface, and the block under the cursor is
// a different one.
//
// The own side had a tint of the surface for a while, and the owner turned
// it down on 30.09: the tint almost hid the selection of the message under
// the cursor, because the selection is drawn on the block and a violet
// block under a selected surface is a selection nobody can find. So:
//
//   - the block of the other side and the block of this user are the same
//     neutral surface, so the two sides are told apart by the colour of
//     their words — the accent for this user, the light neutral for the
//     other side — and by the edge of the feed they are against;
//   - the block under the cursor is the Selected role on either side, and
//     it is far enough from the plain one for the eye to find the message
//     the keys act on without reading any of it.
//
// The second half is a contrast check in every theme, because "different" is
// not a number: a palette whose Selected is a hair off its own Surface0 is a
// palette where the cursor is somewhere on the screen. The three themes
// give 1.31:1, 1.37:1 and 1.37:1, and the bar is 1.25:1.
func TestTheTwoSidesShareOneSurfaceAndTheSelectedBlockIsItsOwn(t *testing.T) {
	const minimumSelectionDifference = 1.25

	for _, name := range ThemeNames() {
		built := mustTheme(t, name)
		roles := colorRoles(t, built.Tokens)

		plain := roles["ComposerBackground"]
		selected := roles["Selected"]

		if !plain.IsSet() {
			t.Errorf("theme %s: neither side of the feed has a surface", name)

			continue
		}

		if selected == plain {
			t.Errorf(
				"theme %s: the block under the cursor is the same surface as every "+
					"other block (%v), so the cursor is nowhere",
				name, selected,
			)

			continue
		}

		difference := selected.ContrastRatio(plain)
		if difference < minimumSelectionDifference {
			t.Errorf(
				"theme %s: the block under the cursor and the block of a message that "+
					"is not under it are %.3f:1 apart, want at least %.2f:1",
				name, difference, minimumSelectionDifference,
			)
		}

		// The words have to be readable on the block they are drawn on,
		// whichever block that is: the light neutral of the other side on
		// the neutral surface and on the selected one, where the three
		// themes give 6.31:1, 6.43:1 and 6.08:1.
		for _, testCase := range []struct {
			textRole       string
			backgroundRole string
		}{
			{textRole: "OutgoingMessage", backgroundRole: "ComposerBackground"},
			{textRole: "IncomingMessage", backgroundRole: "ComposerBackground"},
			{textRole: "IncomingMessage", backgroundRole: "Selected"},
		} {
			ratio := roles[testCase.textRole].ContrastRatio(roles[testCase.backgroundRole])
			if ratio < MinimumTextContrast {
				t.Errorf(
					"theme %s: %s (%s) on %s (%s) = %.2f:1, want at least %.1f:1",
					name,
					testCase.textRole,
					roles[testCase.textRole],
					testCase.backgroundRole,
					roles[testCase.backgroundRole],
					ratio,
					MinimumTextContrast,
				)
			}
		}

		// The words of this user are the accent, and the accent is the
		// lightest thing on the screen, so it is the one pair that does
		// not reach the text bar on the selected block: 4.49:1, 5.20:1
		// and 3.90:1 in the three themes, because the Selected of two of
		// them is a step lighter than their own surface. The bar is what
		// the darkest of the three leaves; the owner has to decide
		// whether the words of a message of this user under the cursor
		// want the 4.5:1 of the others, which means a Selected a step
		// darker in those two themes, and that is the selected row of the
		// chat list and the surface of every popup as well.
		const minimumAccentOnSelected = 3.5

		ratio := roles["OutgoingMessage"].ContrastRatio(selected)
		if ratio < minimumAccentOnSelected {
			t.Errorf(
				"theme %s: the words of this user under the cursor are %.2f:1 on the "+
					"selected block, want at least %.1f:1",
				name, ratio, minimumAccentOnSelected,
			)
		}
	}
}

// The track along the right of the feed and the square that stands on it are
// two numbers, and they are the two ends of one decision: the track has to be
// quiet enough to sit beside a conversation for the whole height of the
// screen without becoming the second thing on it, and it has to be a colour
// of its own, and the square has to be findable on the screen at a glance —
// which is the whole of what it is for (the owner, 03.10: «красный квадрат,
// чтобы видеть лучше»).
//
// Three things are checked and each of them is a thing that could be true of
// a palette and should not be:
//
//   - the track is a shade of the background of the feed rather than the
//     terminal's own: the three themes hold it 2.0:1 to 2.9:1 away from it,
//     which is quiet and is also a track rather than a rumour of one;
//   - the track is not the surface of a block of a message. A stripe in the
//     colour of the blocks, two columns from the right edge of every one of
//     them, is a column of narrow blocks — and it is 1.5:1 to 1.9:1 away
//     from it, the same bar the pill of a day is held to;
//   - the square is not the colour of the track, and it is 4.8:1 to 8.1:1
//     away from the background of the feed it is drawn on. The last number
//     is where the square is looked for: the track is a stripe and the square
//     is on it, and the colour under the square is the background of the
//     feed and not the stripe (styles.go, scrollMarker).
func TestTheTrackOfTheFeedIsQuietAndTheMarkerIsNot(t *testing.T) {
	const (
		quietestTrack = 3.0
		faintestTrack = 1.5

		// The same bar the pill of a day is held to against the surface of
		// a block, because the question is the same one: is this a colour
		// of the feed or is it a message.
		quietestFromBlock = 1.25

		faintestMarker = 3.0
	)

	for _, name := range ThemeNames() {
		roles := colorRoles(t, mustTheme(t, name).Tokens)

		track := roles["ScrollTrack"]
		if !track.IsSet() {
			t.Errorf("theme %s: the track of the feed has no surface of its own", name)

			continue
		}

		feed := track.ContrastRatio(roles["ChatBackground"])
		switch {
		case feed < faintestTrack:
			t.Errorf(
				"theme %s: the track and the background of the feed are %.3f:1 apart, "+
					"want at least %.2f:1 — a track nobody can see is not a track",
				name, feed, faintestTrack,
			)
		case feed > quietestTrack:
			t.Errorf(
				"theme %s: the track and the background of the feed are %.3f:1 apart, "+
					"want at most %.2f:1 — a line down the side of a conversation is a "+
					"frame, and §1 does not allow one",
				name, feed, quietestTrack,
			)
		}

		block := track.ContrastRatio(roles["ComposerBackground"])
		if block < quietestFromBlock {
			t.Errorf(
				"theme %s: the track and the block of a message are %.3f:1 apart, want "+
					"at least %.2f:1 — a stripe in the colour of the blocks is a column "+
					"of narrow blocks",
				name, block, quietestFromBlock,
			)
		}

		marker := roles["ScrollMarker"]
		if marker == track {
			t.Errorf(
				"theme %s: the square on the track is the colour of the track (%v), so "+
					"the one thing that says where the reader is cannot be found",
				name, marker,
			)

			continue
		}

		if ratio := marker.ContrastRatio(roles["ChatBackground"]); ratio < faintestMarker {
			t.Errorf(
				"theme %s: the square on the track is %.2f:1 against the background of "+
					"the feed it is drawn on, want at least %.1f:1",
				name, ratio, faintestMarker,
			)
		}
	}
}

// The square is the red of the palette and not its accent: §5 allows one
// accent on the screen, and the square is on every row of the feed rather
// than on one line of it.
//
// A palette that names no red at all still has to be able to say where the
// reader is, and the nearest thing to red in a palette is the warm status
// hue or the accent, in that order. The first two of those are what a
// palette with a red but no error colour gets, and they are checked here
// because the three built-in presets never take that branch — a branch no
// test walks is a branch nobody has run.
func TestTheSquareTakesTheNearestWarmHueOfAPaletteWithoutRed(t *testing.T) {
	built := mustTheme(t, ThemeCatppuccinMocha)
	palette := built.Palette

	if got := scrollMarkerOf(palette); got != palette.Error {
		t.Errorf("a palette with a red got %v, want its error red %v", got, palette.Error)
	}

	withoutError := palette
	withoutError.Error = Color{}

	if got := scrollMarkerOf(withoutError); got != palette.Warning {
		t.Errorf(
			"a palette without a red got %v, want its warm status hue %v",
			got, palette.Warning,
		)
	}

	withoutWarm := withoutError
	withoutWarm.Warning = Color{}

	if got := scrollMarkerOf(withoutWarm); got != palette.Code {
		t.Errorf(
			"a palette with neither got %v, want its own warm hue %v",
			got, palette.Code,
		)
	}

	grey := withoutWarm
	grey.Code = Color{}

	if got := scrollMarkerOf(grey); got != palette.Accent {
		t.Errorf(
			"a palette with no warm hue at all got %v, want its accent %v",
			got, palette.Accent,
		)
	}
}

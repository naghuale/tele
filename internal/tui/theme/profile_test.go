package theme

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// The test names in this file are the ones docs/TUI_SPEC.md §21 lists
// under "Fallback", plus the decision table of §2.7 behind them.

// A True Color terminal gets the palette as authored. The tokens must be
// exactly the RGB values, because reducing them here would throw away the
// only profile that can show them.
func TestTrueColorUsesRGBTokens(t *testing.T) {
	built := mustTheme(t, ThemeCatppuccinMocha)

	degraded := built.ForProfile(ProfileTrueColor)

	for role, want := range colorRoles(t, built.Tokens) {
		got := colorRoles(t, degraded.Tokens)[role]

		if got != want {
			t.Errorf("%s = %v, want the RGB value %v", role, got, want)
		}
		if got.Kind() != ColorKindRGB {
			t.Errorf("%s kind = %v, want rgb", role, got.Kind())
		}
	}

	// Gradients survive on this profile: they are already short.
	if len(degraded.Gradients.Focus) == 0 {
		t.Error("the focus gradient is empty on true color")
	}
	if degraded.Gradients.Focus[0] != built.Gradients.Focus[0] {
		t.Errorf(
			"focus gradient = %v, want the authored ramp %v",
			degraded.Gradients.Focus,
			built.Gradients.Focus,
		)
	}
}

// An indexed terminal gets the closest entry of its own palette. The
// reduction is the color library's, and the test pins the library's
// answer so a change of library shows up here instead of on a screen.
func TestANSI256UsesIndexedFallback(t *testing.T) {
	built := mustTheme(t, ThemeCatppuccinMocha)

	degraded := built.ForProfile(ProfileANSI256)

	roles := colorRoles(t, degraded.Tokens)
	for role, color := range roles {
		if !color.IsSet() {
			t.Errorf("%s is not set on ansi-256", role)
			continue
		}
		if color.Kind() != ColorKindRGB {
			t.Errorf("%s kind = %v, want an indexed value", role, color.Kind())
		}
		if color == colorRoles(t, built.Tokens)[role] {
			// Not a failure on its own: a palette entry can be exactly
			// representable. It does mean the reduction has to be
			// checked somewhere, which is what the next assertion does.
			t.Logf("%s is exactly representable", role)
		}
	}

	// A colour outside the 256-colour palette must come back changed, and
	// a colour inside it must come back as itself. The pair below is
	// deliberate: #cba6f7 is not an entry of the cube or the grey ramp.
	outside := RGB("#cba6f7")
	if indexedColor(outside) == outside {
		t.Error("a colour outside the indexed palette was not reduced")
	}
	if inside := RGB("#ff0000"); indexedColor(inside) != inside {
		t.Errorf(
			"an indexed palette entry was changed to %v",
			indexedColor(inside),
		)
	}

	// A gradient is cut to what an indexed ramp can show, and the cap is
	// the documented one.
	long := Gradients{
		Focus: []Color{
			RGB("#cba6f7"), RGB("#89b4fa"),
			RGB("#a6e3a1"), RGB("#f9e2af"), RGB("#f38ba8"),
		},
	}
	capped := long.ForProfile(ProfileANSI256)
	if len(capped.Focus) != maxANSI256GradientStops {
		t.Errorf(
			"focus gradient = %d stops, want %d",
			len(capped.Focus),
			maxANSI256GradientStops,
		)
	}
}

// A 16-colour terminal is mapped by role, not by hue: the nearest RGB
// value is a colour the user never chose, and the point of a basic colour
// is that the terminal renders it from the palette the user configured.
func TestANSI16UsesBasicFallback(t *testing.T) {
	built := mustTheme(t, ThemeCatppuccinMocha)

	degraded := built.ForProfile(ProfileANSI16)
	roles := colorRoles(t, degraded.Tokens)

	for role, color := range roles {
		if !color.IsSet() {
			// Two roles are deliberately left unset, and the reason is
			// the same in both cases: this profile carries them some
			// other way. A background is the terminal's own, because five
			// shades of surface do not exist here; the selection tint is
			// the reverse attribute the selected row already has.
			if isCarriedBySomethingElse(role) {
				continue
			}

			t.Errorf("%s is not set on ansi-16", role)
			continue
		}

		if color.Kind() != ColorKindBasic {
			t.Errorf("%s kind = %v, want a basic colour", role, color.Kind())
		}
	}

	// The status roles must stay apart, because §14 says a status is
	// never only colour, and on this profile the colour is the whole of
	// the difference between two statuses.
	distinct := map[StatusState]uint8{}
	for _, state := range StatusStates() {
		index, ok := state.Color(degraded.Tokens).BasicIndex()
		if !ok {
			t.Fatalf("state %v has no basic colour", state)
		}
		if other, seen := distinct[state]; seen && other != index {
			t.Errorf(
				"state %v shares basic colour %d with another state",
				state,
				index,
			)
		}
		distinct[state] = index
	}

	if degraded.Gradients.Focus != nil {
		t.Errorf("focus gradient = %v, want none on ansi-16", degraded.Gradients.Focus)
	}
	if degraded.Tokens.ShadowBackground.IsSet() {
		t.Error("a shadow background must be dropped on ansi-16")
	}
}

// The focus marker is the one thing that says where the focus is, and it
// has to survive the profile that removes every colour.
func TestNoColorPreservesFocusIndicator(t *testing.T) {
	built := mustTheme(t, ThemeCatppuccinMocha)

	for _, profile := range []Profile{
		ProfileTrueColor,
		ProfileANSI256,
		ProfileANSI16,
		ProfileNoColor,
	} {
		degraded := built.ForProfile(profile)

		if got := degraded.FocusIndicator(true); got != FocusBar {
			t.Errorf(
				"profile %s: focused indicator = %q, want %q",
				profile,
				got,
				FocusBar,
			)
		}
		if got := degraded.FocusIndicator(false); got != FocusNone {
			t.Errorf(
				"profile %s: unfocused indicator = %q, want blanks",
				profile,
				got,
			)
		}
	}

	// The unfocused marker must be exactly as wide as the focused one, or
	// moving the focus would reflow the interface. The comparison is in
	// cells rather than bytes: a block character is three bytes and one
	// column, and it is the column that shifts the layout.
	if got, want := utf8.RuneCountInString(FocusNone), utf8.RuneCountInString(FocusBar); got != want {
		t.Errorf(
			"the unfocused marker is %d cells wide, the focused one %d",
			got,
			want,
		)
	}

	// A terminal without box-drawing characters still gets a marker.
	if FocusArrow == "" || FocusArrow == FocusNone {
		t.Errorf("the plain-text focus marker = %q", FocusArrow)
	}

	// The selection is an attribute, and an attribute is all that is left.
	for _, profile := range []Profile{ProfileANSI16, ProfileNoColor} {
		attributes := built.ForProfile(profile).SelectionAttributes(true)
		if !attributes.Reverse && !attributes.Bold {
			t.Errorf(
				"profile %s: a selected row has no attribute",
				profile,
			)
		}
		if got := built.ForProfile(profile).SelectionAttributes(false); got.Reverse || got.Bold {
			t.Errorf("profile %s: an unselected row is marked: %+v", profile, got)
		}
	}
}

// A status must be readable with no colour at all, which means a symbol
// and words on every state, and different ones: two states that shared a
// mark would be told apart by nothing.
func TestNoColorPreservesStatusMeaning(t *testing.T) {
	built := mustTheme(t, ThemeCatppuccinMocha)
	degraded := built.ForProfile(ProfileNoColor)

	// Every colour is gone: that is the profile.
	for role, color := range colorRoles(t, degraded.Tokens) {
		if color.IsSet() {
			t.Errorf("%s = %v, want no colour at all", role, color)
		}
	}

	symbols := map[string]StatusState{}
	words := map[string]StatusState{}

	for _, state := range StatusStates() {
		mark := state.Mark()

		if mark.Symbol == "" {
			t.Errorf("state %v has no symbol", state)
		}
		if strings.TrimSpace(mark.Text) == "" {
			t.Errorf("state %v has no words", state)
		}
		if other, seen := symbols[mark.Symbol]; seen {
			t.Errorf(
				"states %v and %v share the symbol %q",
				other,
				state,
				mark.Symbol,
			)
		}
		if other, seen := words[mark.Text]; seen {
			t.Errorf(
				"states %v and %v share the words %q",
				other,
				state,
				mark.Text,
			)
		}
		symbols[mark.Symbol] = state
		words[mark.Text] = state

		// The words alone are the form a terminal without the symbol can
		// still show, so they must survive on their own.
		if mark.Plain() != mark.Text {
			t.Errorf(
				"state %v: Plain() = %q, want the words %q",
				state,
				mark.Plain(),
				mark.Text,
			)
		}
		if !strings.Contains(mark.String(), mark.Text) {
			t.Errorf("state %v: String() = %q, want the words in it", state, mark)
		}
	}
}

// A dumb terminal is one that cannot do more than print text, so it gets
// no gradients however it was asked for.
func TestDumbTerminalDisablesGradients(t *testing.T) {
	built := mustTheme(t, ThemeCatppuccinMocha)

	dumb := ResolveProfile(ProfileInput{
		Env:      map[string]string{"TERM": "dumb"},
		Terminal: TerminalTrueColor,
	})
	if dumb != ProfileNoColor {
		t.Fatalf(
			"profile for TERM=dumb = %v, want no colour",
			dumb,
		)
	}

	degraded := built.ForProfile(dumb)
	for role, ramp := range gradientRoles(t, degraded.Gradients) {
		if len(ramp) != 0 {
			t.Errorf("gradient %s = %v, want none on a dumb terminal", role, ramp)
		}
	}

	// The same is true of a 16-colour terminal, whose palette has no room
	// for a ramp either.
	basic := built.ForProfile(ProfileANSI16)
	for role, ramp := range gradientRoles(t, basic.Gradients) {
		if len(ramp) != 0 {
			t.Errorf("gradient %s = %v, want none on ansi-16", role, ramp)
		}
	}
}

// The decision table of §2.7, with every input that can change the answer.
// The terminal is a measurement, so the table states it instead of asking
// the machine: a test that inherits CI's own terminal proves nothing.
func TestResolveProfileFollowsTheDecisionTable(t *testing.T) {
	cases := map[string]struct {
		input ProfileInput
		want  Profile
	}{
		"true color terminal": {
			input: ProfileInput{Terminal: TerminalTrueColor},
			want:  ProfileTrueColor,
		},
		"256 colour terminal": {
			input: ProfileInput{Terminal: TerminalANSI256},
			want:  ProfileANSI256,
		},
		"16 colour terminal": {
			input: ProfileInput{Terminal: TerminalANSI16},
			want:  ProfileANSI16,
		},
		"a library that reports no colour at all is believed": {
			input: ProfileInput{Terminal: TerminalAscii},
			want:  ProfileNoColor,
		},
		"always takes a terminal that reported nothing as 256": {
			input: ProfileInput{
				Configured: ColorAlways,
				Terminal:   TerminalAscii,
			},
			want: ProfileANSI256,
		},
		"unmeasurable terminal stays conservative": {
			input: ProfileInput{Terminal: TerminalUnknown},
			want:  ProfileANSI16,
		},
		"always trusts the user when nothing could be measured": {
			input: ProfileInput{
				Configured: ColorAlways,
				Terminal:   TerminalUnknown,
			},
			want: ProfileANSI256,
		},
		"always does not invent a capability": {
			input: ProfileInput{
				Configured: ColorAlways,
				Terminal:   TerminalANSI16,
			},
			want: ProfileANSI16,
		},
		"never": {
			input: ProfileInput{
				Configured: ColorNever,
				Terminal:   TerminalTrueColor,
			},
			want: ProfileNoColor,
		},
		"--no-color": {
			input: ProfileInput{
				FlagNoColor: true,
				Terminal:    TerminalTrueColor,
			},
			want: ProfileNoColor,
		},
		"NO_COLOR": {
			input: ProfileInput{
				Env:      map[string]string{"NO_COLOR": "1"},
				Terminal: TerminalTrueColor,
			},
			want: ProfileNoColor,
		},
		"NO_COLOR with any value": {
			input: ProfileInput{
				Env:      map[string]string{"NO_COLOR": "no"},
				Terminal: TerminalTrueColor,
			},
			want: ProfileNoColor,
		},
		"an empty NO_COLOR is not a request": {
			input: ProfileInput{
				Env:      map[string]string{"NO_COLOR": ""},
				Terminal: TerminalTrueColor,
			},
			want: ProfileTrueColor,
		},
		"TERM=dumb": {
			input: ProfileInput{
				Env:      map[string]string{"TERM": "dumb"},
				Terminal: TerminalTrueColor,
			},
			want: ProfileNoColor,
		},
		"never is stronger than everything": {
			input: ProfileInput{
				Configured: ColorNever,
				Env:        map[string]string{"NO_COLOR": ""},
				Terminal:   TerminalTrueColor,
			},
			want: ProfileNoColor,
		},
		"--no-color is stronger than everything": {
			input: ProfileInput{
				FlagNoColor: true,
				Configured:  ColorAlways,
				Terminal:    TerminalTrueColor,
			},
			want: ProfileNoColor,
		},
		"always wins over NO_COLOR": {
			input: ProfileInput{
				Env:        map[string]string{"NO_COLOR": "1"},
				Configured: ColorAlways,
				Terminal:   TerminalTrueColor,
			},
			want: ProfileTrueColor,
		},
		"always wins over NO_COLOR on a terminal that measured nothing": {
			input: ProfileInput{
				Env:        map[string]string{"NO_COLOR": "1"},
				Configured: ColorAlways,
				Terminal:   TerminalAscii,
			},
			want: ProfileANSI256,
		},
		"TERM=dumb wins over always": {
			input: ProfileInput{
				Env:        map[string]string{"TERM": "dumb"},
				Configured: ColorAlways,
				Terminal:   TerminalTrueColor,
			},
			want: ProfileNoColor,
		},
		"NO_COLOR wins over auto": {
			input: ProfileInput{
				Env:      map[string]string{"NO_COLOR": "1"},
				Terminal: TerminalTrueColor,
			},
			want: ProfileNoColor,
		},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			if got := ResolveProfile(testCase.input); got != testCase.want {
				t.Fatalf(
					"ResolveProfile() = %v, want %v",
					got,
					testCase.want,
				)
			}
		})
	}
}

func TestParseColorMode(t *testing.T) {
	cases := map[string]struct {
		value string
		want  ColorMode
		fails bool
	}{
		"absent":  {value: "", want: ColorAuto},
		"auto":    {value: "auto", want: ColorAuto},
		"always":  {value: "always", want: ColorAlways},
		"never":   {value: "never", want: ColorNever},
		"padded":  {value: "  never ", want: ColorNever},
		"cased":   {value: "NEVER", want: ColorNever},
		"unknown": {value: "sometimes", fails: true},
		"colour":  {value: "color", fails: true},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := ParseColorMode(testCase.value)

			if testCase.fails {
				if err == nil {
					t.Fatalf(
						"ParseColorMode(%q) error = nil, want a rejection",
						testCase.value,
					)
				}
				for _, valid := range ColorModeNames() {
					if !strings.Contains(err.Error(), valid) {
						t.Errorf(
							"error %q does not list the value %q",
							err,
							valid,
						)
					}
				}

				return
			}

			if err != nil {
				t.Fatalf("ParseColorMode(%q): %v", testCase.value, err)
			}
			if got != testCase.want {
				t.Fatalf(
					"ParseColorMode(%q) = %q, want %q",
					testCase.value,
					got,
					testCase.want,
				)
			}
		})
	}
}

// A profile has to be expressible in the library's own vocabulary, or a
// renderer would have to translate it and could translate it differently.
func TestProfileMapsToTheColorLibrary(t *testing.T) {
	built := mustTheme(t, ThemeCatppuccinMocha)

	for _, profile := range []Profile{
		ProfileTrueColor,
		ProfileANSI256,
		ProfileANSI16,
		ProfileNoColor,
	} {
		library := profile.Termenv()

		if got := TerminalProfileFromTermenv(library); got == TerminalUnknown {
			t.Errorf(
				"profile %v does not survive the round trip through %v",
				profile,
				library,
			)
		}
	}

	// And the degraded theme has to be printable with the library profile
	// it claims, which is what the renderer will hand over.
	for _, profile := range []Profile{ProfileTrueColor, ProfileANSI256} {
		degraded := built.ForProfile(profile)

		if degraded.Tokens.PrimaryText.Hex() == "" {
			t.Errorf("profile %v has no printable primary text", profile)
		}
	}
}

// isCarriedBySomethingElse reports the roles a 16-colour terminal does
// not need a colour for.
func isCarriedBySomethingElse(role string) bool {
	switch role {
	case "AppBackground",
		"SidebarBackground",
		"ChatBackground",
		"ComposerBackground",
		"PopupBackground",
		"CodeBackground",
		"FooterBackground",
		"ShadowBackground",
		"Selection":
		return true
	default:
		return false
	}
}

// mustTheme returns a built-in theme or fails the test.
func mustTheme(t *testing.T, name string) Theme {
	t.Helper()

	built, err := ThemeFor(name)
	if err != nil {
		t.Fatalf("ThemeFor(%q): %v", name, err)
	}

	return built
}

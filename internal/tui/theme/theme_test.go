package theme

import (
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// The test names in this file are the ones docs/TUI_SPEC.md §21 lists
// under "Theme contract". They are spelled as the specification spells
// them so a reader can find them, and so a rename is visible in a diff
// rather than in a search that finds nothing.

// A theme with a hole in it is not a theme: a role with no colour is
// either a renderer that emits nothing, or a screen with a silent patch
// of default terminal colours. The walk is by reflection so a role added
// to Tokens is covered by this test without editing it.
func TestEveryBuiltInThemeHasAllSemanticRoles(t *testing.T) {
	for _, name := range ThemeNames() {
		built, err := ThemeFor(name)
		if err != nil {
			t.Fatalf("ThemeFor(%q): %v", name, err)
		}

		for role, color := range colorRoles(t, built.Tokens) {
			if !color.IsSet() {
				t.Errorf("theme %s: role %s is not set", name, role)
			}
		}

		// The palette is a colour set a user palette would replace, so
		// every one of its roles has to be there too, even when no token
		// uses it yet.
		for role, color := range colorRoles(t, built.Palette) {
			if !color.IsSet() {
				t.Errorf("theme %s: palette role %s is not set", name, role)
			}
		}

		for role, ramp := range gradientRoles(t, built.Gradients) {
			if len(ramp) == 0 {
				t.Errorf("theme %s: gradient %s is empty", name, role)
			}
			for index, stop := range ramp {
				if !stop.IsSet() {
					t.Errorf(
						"theme %s: gradient %s stop %d is not set",
						name, role, index,
					)
				}
			}
		}
	}
}

func TestEveryBuiltInThemeHasUniqueName(t *testing.T) {
	seen := make(map[string]bool)

	for _, entry := range presets {
		if seen[entry.Name] {
			t.Errorf("theme name %q appears twice", entry.Name)
		}
		seen[entry.Name] = true
	}

	if len(seen) != len(ThemeNames()) {
		t.Errorf(
			"ThemeNames() = %v, %d unique names in the preset list",
			ThemeNames(),
			len(seen),
		)
	}
}

// An unknown name is an error that says what there is. A silent fallback
// would leave a user reading a theme they did not ask for and never
// finding out why.
func TestThemeRejectsUnknownName(t *testing.T) {
	_, err := ThemeFor("solarized-latte")
	if err == nil {
		t.Fatal("ThemeFor(\"solarized-latte\") error = nil, want a rejection")
	}

	message := err.Error()
	for _, name := range ThemeNames() {
		if !strings.Contains(message, name) {
			t.Errorf("error %q does not list the theme %q", message, name)
		}
	}
	if !strings.Contains(message, "solarized-latte") {
		t.Errorf("error %q does not name the rejected theme", message)
	}
}

func TestThemeDefaultsToCatppuccinMocha(t *testing.T) {
	if DefaultTheme().Name != ThemeCatppuccinMocha {
		t.Fatalf(
			"DefaultTheme().Name = %q, want %q",
			DefaultTheme().Name,
			ThemeCatppuccinMocha,
		)
	}

	// A configuration written before themes existed has no name, and must
	// get the default rather than an error.
	fromEmpty, err := ThemeFor("")
	if err != nil {
		t.Fatalf("ThemeFor(\"\"): %v", err)
	}
	if fromEmpty.Name != ThemeCatppuccinMocha {
		t.Fatalf("ThemeFor(\"\").Name = %q, want the default", fromEmpty.Name)
	}
}

// The theme says what the interface should look like; the renderer says
// how to print it. A theme that changed Lip Gloss global state would
// decide the profile of every other component in the process, including
// a library the user did not ask for.
func TestThemeDoesNotMutateGlobalStyles(t *testing.T) {
	before := renderProbe()
	beforeProfile := lipgloss.ColorProfile()

	for _, name := range ThemeNames() {
		built, err := ThemeFor(name)
		if err != nil {
			t.Fatalf("ThemeFor(%q): %v", name, err)
		}

		for _, profile := range []Profile{
			ProfileTrueColor,
			ProfileANSI256,
			ProfileANSI16,
			ProfileNoColor,
		} {
			degraded := built.ForProfile(profile)

			// Touch what a renderer would touch.
			_ = degraded.Tokens.PrimaryText.String()
			_ = degraded.FocusIndicator(true)
			_ = degraded.SelectionAttributes(true)
			_ = StatusSent.Color(degraded.Tokens)
			_ = degraded.Gradients.ForProfile(profile)
		}
	}

	if after := lipgloss.ColorProfile(); after != beforeProfile {
		t.Errorf(
			"global color profile = %v, want %v",
			after,
			beforeProfile,
		)
	}
	if after := renderProbe(); after != before {
		t.Errorf(
			"global style rendering changed:\nbefore %q\nafter  %q",
			before,
			after,
		)
	}
}

// renderProbe renders one styled string through the global renderer, so a
// change of the global state shows up as a different string.
func renderProbe() string {
	return lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#ff005f")).
		Render("probe")
}

// The tokens must be a function of the palette and not a table per
// preset. A user palette (PR-10C) is the reason: it has to work without
// a component, or without this package, being edited.
func TestTokensAreComputedFromThePalette(t *testing.T) {
	palette := Palette{
		Base:      RGB("#101010"),
		Mantle:    RGB("#0c0c0c"),
		Crust:     RGB("#080808"),
		Surface0:  RGB("#1c1c1c"),
		Surface1:  RGB("#242424"),
		Surface2:  RGB("#2c2c2c"),
		Overlay0:  RGB("#707070"),
		Overlay1:  RGB("#909090"),
		Overlay2:  RGB("#b0b0b0"),
		Text:      RGB("#f0f0f0"),
		Subtext0:  RGB("#c0c0c0"),
		Subtext1:  RGB("#a0a0a0"),
		Accent:    RGB("#00d7ff"),
		AccentAlt: RGB("#ffaf00"),
		Success:   RGB("#5fff87"),
		Warning:   RGB("#ffff87"),
		Error:     RGB("#ff5f5f"),
		Info:      RGB("#5fafff"),
		Link:      RGB("#5fafff"),
		Mention:   RGB("#ff87ff"),
		Code:      RGB("#afd7ff"),
	}

	tokens := TokensFor(palette, ThemeModeDark)

	if tokens.AppBackground != palette.Base {
		t.Errorf(
			"AppBackground = %v, want the palette base %v",
			tokens.AppBackground,
			palette.Base,
		)
	}
	if tokens.PrimaryText != palette.Text {
		t.Errorf("PrimaryText = %v, want the palette text %v", tokens.PrimaryText, palette.Text)
	}
	if tokens.ComposerBackground != palette.Surface0 {
		t.Errorf(
			"ComposerBackground = %v, want surface0 %v",
			tokens.ComposerBackground,
			palette.Surface0,
		)
	}
	if tokens.Focus != palette.Accent {
		t.Errorf("Focus = %v, want the accent %v", tokens.Focus, palette.Accent)
	}
	if tokens.StatusError != palette.Error {
		t.Errorf("StatusError = %v, want %v", tokens.StatusError, palette.Error)
	}

	// A light mode reads from the other end of the palette, so the two
	// modes cannot be the same mapping with a different name.
	light := TokensFor(palette, ThemeModeLight)
	if light.Focus != palette.AccentAlt {
		t.Errorf(
			"light Focus = %v, want the second accent %v",
			light.Focus,
			palette.AccentAlt,
		)
	}

	// A theme built from that palette carries the same tokens, which is
	// what makes the mapping and the preset agree by construction.
	gradients := GradientsFor(palette, ThemeModeDark)
	if len(gradients.Progress) == 0 {
		t.Error("the progress gradient is empty")
	}
}

// colorRoles returns every colour field of a struct, by field name.
func colorRoles[T any](t *testing.T, value T) map[string]Color {
	t.Helper()

	roles := map[string]Color{}
	reflected := reflect.ValueOf(value)

	for index := 0; index < reflected.NumField(); index++ {
		name := reflected.Type().Field(index).Name

		color, ok := reflected.Field(index).Interface().(Color)
		if !ok {
			t.Fatalf("field %s of %T is not a Color", name, value)
		}
		roles[name] = color
	}

	return roles
}

// gradientRoles returns every gradient of a Gradients, by field name.
func gradientRoles(t *testing.T, gradients Gradients) map[string][]Color {
	t.Helper()

	roles := map[string][]Color{}
	reflected := reflect.ValueOf(gradients)

	for index := 0; index < reflected.NumField(); index++ {
		name := reflected.Type().Field(index).Name

		ramp, ok := reflected.Field(index).Interface().([]Color)
		if !ok {
			t.Fatalf("field %s of Gradients is not a []Color", name)
		}
		roles[name] = ramp
	}

	return roles
}

// isBackgroundRole reports whether a role is a surface of the screen.
//
// A surface is the one role a 16-colour terminal does not need a colour
// for: the terminal's own background shows through, so a surface has no
// basic index to name and no run of text is ever printed in one.
func isBackgroundRole(role string) bool {
	switch role {
	case "AppBackground",
		"SidebarBackground",
		"ChatBackground",
		"ComposerBackground",
		"FooterBackground",
		"PopupBackground",
		"ShadowBackground",
		"CodeBackground",
		"SeparatorBackground",
		"ScrollTrack":
		return true
	default:
		return false
	}
}

// Every token of every built-in theme names the entry of the 256-colour
// palette and the index of the basic one it is printed as.
//
// A token that does not is reduced at run time, and the reduction is
// float work: two entries of a ramp can be the same distance from a
// value, and arm64 and amd64 round that tie differently. That is not a
// question about the interface, it is a question a golden file has to have
// one answer to, and a palette that leaves the answer to the arithmetic
// has two.
func TestEveryTokenNamesItsIndexedAndBasicEntry(t *testing.T) {
	for _, name := range ThemeNames() {
		built := mustTheme(t, name)

		for role, color := range colorRoles(t, built.Tokens) {
			if color.Kind() != ColorKindRGB {
				t.Errorf("theme %s: %s is %v, want a colour", name, role, color)
				continue
			}
			if _, named := color.IndexedIndex(); !named {
				t.Errorf(
					"theme %s: %s (%v) names no entry of the 256-colour palette",
					name, role, color,
				)
			}
			if isBackgroundRole(role) {
				// §2.7: a 16-colour terminal shows its own background
				// through, so a surface is never printed as a basic
				// colour and has no index to name. Every other role is
				// one the interface writes words in.
				continue
			}
			if _, named := color.BasicIndex(); !named {
				t.Errorf(
					"theme %s: %s (%v) names no index of the basic palette",
					name, role, color,
				)
			}
		}
	}
}

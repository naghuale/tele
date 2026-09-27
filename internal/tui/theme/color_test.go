package theme

import "testing"

func TestRGBNormalizesItsValue(t *testing.T) {
	color := RGB("  #1E1E2E ")

	if color.Kind() != ColorKindRGB {
		t.Fatalf("kind = %v, want rgb", color.Kind())
	}
	if color.Hex() != "#1e1e2e" {
		t.Errorf("Hex() = %q, want the lower case value", color.Hex())
	}
	if !color.IsSet() {
		t.Error("IsSet() = false for a valid colour")
	}
	if color.String() != "#1e1e2e" {
		t.Errorf("String() = %q, want the hex value", color.String())
	}
}

// A mistyped literal in a preset has to become an unset token that the
// theme contract test reports, not a colour that only fails on screen.
func TestRGBRejectsAnythingElse(t *testing.T) {
	for _, value := range []string{
		"",
		"#",
		"#12345",
		"#1234567",
		"1e1e2e",
		"#12345g",
		"#1e1e2",
		"rgb(1,2,3)",
	} {
		if color := RGB(value); color.IsSet() {
			t.Errorf("RGB(%q) = %v, want no colour", value, color)
		}
	}
}

func TestBasicRejectsAnIndexOutsideTheSixteenColours(t *testing.T) {
	color := Basic(3)

	if color.Kind() != ColorKindBasic {
		t.Fatalf("kind = %v, want basic", color.Kind())
	}
	if index, ok := color.BasicIndex(); !ok || index != 3 {
		t.Errorf("BasicIndex() = %d, %v, want 3, true", index, ok)
	}
	if color.Hex() != "" {
		t.Errorf("Hex() = %q, want no hex value for a basic colour", color.Hex())
	}
	if got := Basic(16); got.IsSet() {
		t.Errorf("Basic(16) = %v, want no colour", got)
	}
	if got := Basic(255); got.IsSet() {
		t.Errorf("Basic(255) = %v, want no colour", got)
	}
}

func TestZeroColorIsNoColour(t *testing.T) {
	var color Color

	if color.IsSet() {
		t.Error("the zero Color must not be set")
	}
	if color.Kind() != ColorKindNone {
		t.Errorf("kind = %v, want none", color.Kind())
	}
	if color.String() != "" {
		t.Errorf("String() = %q, want empty", color.String())
	}
	if _, ok := color.BasicIndex(); ok {
		t.Error("BasicIndex() reported an index for the zero Color")
	}
}

func TestColorKindsAreNamed(t *testing.T) {
	cases := map[ColorKind]string{
		ColorKindNone:  "none",
		ColorKindRGB:   "rgb",
		ColorKindBasic: "basic",
		ColorKind(9):   "unknown",
	}

	for kind, want := range cases {
		if got := kind.String(); got != want {
			t.Errorf("ColorKind(%d).String() = %q, want %q", kind, got, want)
		}
	}
}

func TestProfilesAreNamed(t *testing.T) {
	cases := map[Profile]string{
		ProfileTrueColor: "true-color",
		ProfileANSI256:   "ansi-256",
		ProfileANSI16:    "ansi-16",
		ProfileNoColor:   "no-color",
		Profile(9):       "unknown",
	}

	for profile, want := range cases {
		if got := profile.String(); got != want {
			t.Errorf("Profile(%d).String() = %q, want %q", profile, got, want)
		}
	}

	if got := len(ProfileNames()); got != 4 {
		t.Errorf("ProfileNames() = %v, want the four profiles of §2.7", ProfileNames())
	}
}

func TestTerminalProfilesAreNamed(t *testing.T) {
	cases := map[TerminalProfile]string{
		TerminalUnknown:    "unknown",
		TerminalTrueColor:  "true-color",
		TerminalANSI256:    "ansi-256",
		TerminalANSI16:     "ansi-16",
		TerminalAscii:      "ascii",
		TerminalProfile(9): "unknown",
	}

	for profile, want := range cases {
		if got := profile.String(); got != want {
			t.Errorf(
				"TerminalProfile(%d).String() = %q, want %q",
				profile,
				got,
				want,
			)
		}
	}
}

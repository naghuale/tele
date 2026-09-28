package application

import (
	"context"
	"strings"
	"testing"

	"telecli/internal/config"
	"telecli/internal/tui"
	"telecli/internal/tui/termwidth"
)

// The defaults are written in two places, because internal/config is a leaf
// that must not import the width package. They have to agree, or a default
// configuration would draw a screen in a rule nobody asked for.
func TestInterfaceDefaultsMatchTheWidthPackage(t *testing.T) {
	defaults := config.Default()

	mode, err := termwidth.ParseMode(defaults.TUI.Width)
	if err != nil {
		t.Fatalf("the default width %q is not a value: %v", defaults.TUI.Width, err)
	}
	if mode != termwidth.ModeAuto {
		t.Errorf("default width = %q, want auto", defaults.TUI.Width)
	}
}

func TestResolveInterfaceWidth(t *testing.T) {
	cases := []struct {
		value string
		want  termwidth.Mode
	}{
		{value: "", want: termwidth.ModeAuto},
		{value: "auto", want: termwidth.ModeAuto},
		{value: "grapheme", want: termwidth.ModeGrapheme},
		{value: "codepoint", want: termwidth.ModeCodepoint},
	}

	for _, testCase := range cases {
		cfg := config.Default()
		cfg.TUI.Width = testCase.value

		got, err := resolveInterfaceWidth(cfg)
		if err != nil {
			t.Errorf("resolveInterfaceWidth(%q): %v", testCase.value, err)
			continue
		}
		if got != testCase.want {
			t.Errorf("resolveInterfaceWidth(%q) = %v, want %v", testCase.value, got, testCase.want)
		}
	}
}

// A word that is not one of the three is a configuration error with the
// three in it, and it is reported by the command the user runs rather than
// at the next start of the TUI.
func TestAnUnknownWidthIsAConfigurationError(t *testing.T) {
	cfg := config.Default()
	cfg.TUI.Width = "columns"

	_, err := resolveInterfaceWidth(cfg)
	if err == nil {
		t.Fatal("a width of \"columns\" was accepted")
	}

	for _, want := range []string{"auto", "grapheme", "codepoint", "columns"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error %q does not name %q", err, want)
		}
	}
}

func TestTheDoctorRejectsAnUnknownWidth(t *testing.T) {
	path := writeWidthConfig(t, "columns")

	stdout, stderr, code := runCommand(t, "doctor", "--config", path)
	if code == 0 {
		t.Fatalf("doctor accepted a width of \"columns\":\n%s", stdout)
	}
	if !strings.Contains(stderr, "codepoint") {
		t.Errorf("doctor does not say what the valid widths are: %q", stderr)
	}
}

// The doctor is a report of state, so it says which rule the screen is
// drawn in and where the rule came from. It does not measure: the doctor
// prints lines and leaves, and a terminal is measured by a program that is
// about to draw a frame in it.
func TestDoctorReportsTheWidth(t *testing.T) {
	cases := []struct {
		name  string
		width string
		want  string
	}{
		{
			name:  "a rule that was configured",
			width: "grapheme",
			want:  "Interface width: grapheme (configured)",
		},
		{
			name:  "a rule that was not",
			width: "codepoint",
			want:  "Interface width: codepoint (configured)",
		},
		{
			name:  "no rule at all",
			width: "",
			want:  "Interface width: codepoint (default)",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			path := writeWidthConfig(t, testCase.width)

			stdout, stderr, code := runCommand(t, "doctor", "--config", path)
			if code != 0 {
				t.Fatalf("exit code = %d, want 0\n%s%s", code, stdout, stderr)
			}

			if !strings.Contains(stdout, testCase.want) {
				t.Errorf("doctor does not report %q:\n%s", testCase.want, stdout)
			}
		})
	}
}

// A configuration that left the choice to the terminal is reported as the
// rule it falls back to, with a note that the real one is decided when the
// TUI starts. A user who reads "default" and wonders what it defaults to
// has to be able to find out from the same screen.
func TestDoctorSaysThatAutoIsMeasuredAtTheTUI(t *testing.T) {
	path := writeWidthConfig(t, "")

	stdout, _, code := runCommand(t, "doctor", "--config", path)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", code, stdout)
	}

	if !strings.Contains(stdout, "measured when the TUI starts") {
		t.Errorf("doctor does not say that auto is measured:\n%s", stdout)
	}
	if strings.Contains(stdout, "(measured)") {
		t.Errorf("doctor claims to have measured a terminal:\n%s", stdout)
	}
}

// The rule has to reach the TUI, or resolving it here would be a ceremony
// with no effect: the program measures the terminal only where the
// configuration left the choice to it.
func TestTheWidthModeReachesTheTUIDependencies(t *testing.T) {
	var captured tui.Dependencies

	app := NewWithAuthAndSubmitter(
		config.Default(),
		nil,
		nil,
		func(context.Context) (AuthRunResult, error) {
			return AuthRunResult{Submitter: noopComposerSubmitter{}}, nil
		},
		func(tui.ChatSource) error { return nil },
		func(_ context.Context, deps tui.Dependencies) error {
			captured = deps
			return nil
		},
	).WithWidthMode(termwidth.ModeGrapheme)

	if err := app.RunTUI(context.Background()); err != nil {
		t.Fatalf("RunTUI: %v", err)
	}

	if captured.WidthMode != termwidth.ModeGrapheme {
		t.Errorf("the TUI was given width mode %v, want grapheme", captured.WidthMode)
	}
}

// An app that was never told a rule measures the terminal, which is what
// the zero value of the setting says: a rule nobody named is a rule the
// terminal decides.
func TestAnAppWithoutARuleMeasuresTheTerminal(t *testing.T) {
	var captured tui.Dependencies

	app := NewWithAuthAndSubmitter(
		config.Default(),
		nil,
		nil,
		func(context.Context) (AuthRunResult, error) {
			return AuthRunResult{Submitter: noopComposerSubmitter{}}, nil
		},
		func(tui.ChatSource) error { return nil },
		func(_ context.Context, deps tui.Dependencies) error {
			captured = deps
			return nil
		},
	)

	if err := app.RunTUI(context.Background()); err != nil {
		t.Fatalf("RunTUI: %v", err)
	}

	if captured.WidthMode != termwidth.ModeAuto {
		t.Errorf("the TUI was given width mode %v, want auto", captured.WidthMode)
	}
}

// writeWidthConfig writes a configuration file with a width rule in it, and
// points TELECLI_CONFIG at it.
func writeWidthConfig(t *testing.T, width string) string {
	t.Helper()

	dir := t.TempDir()
	path := dir + "/config.toml"

	cfg := config.Default()
	cfg.DataDir = dir + "/data"
	cfg.TDLib.DatabaseDir = dir + "/data/tdlib/database"
	cfg.TDLib.FilesDir = dir + "/data/tdlib/files"
	cfg.TUI.Width = width

	if err := config.WriteFile(path, cfg); err != nil {
		t.Fatalf("write config: %v", err)
	}

	t.Setenv("TELECLI_CONFIG", path)

	return path
}

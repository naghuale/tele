package application

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"telecli/internal/config"
	"telecli/internal/tui"
	"telecli/internal/tui/theme"
)

// The interface settings are resolved by the composition root, so the
// tests here state the environment and the terminal instead of inheriting
// whatever the machine has. A test that reads CI's own terminal would pass
// on a laptop and mean nothing on a runner.

// The defaults are written in two places, because internal/config is a
// leaf that must not import the theme package. They have to agree, or a
// default configuration would draw a theme the user never asked for.
func TestInterfaceDefaultsMatchTheThemePackage(t *testing.T) {
	defaults := config.Default()

	if defaults.TUI.Theme != theme.DefaultTheme().Name {
		t.Errorf(
			"config default theme = %q, the theme package default is %q",
			defaults.TUI.Theme,
			theme.DefaultTheme().Name,
		)
	}

	mode, err := theme.ParseColorMode(defaults.TUI.Color)
	if err != nil {
		t.Fatalf("the default colour mode %q is not a value: %v", defaults.TUI.Color, err)
	}
	if mode != theme.ColorAuto {
		t.Errorf("default colour mode = %q, want auto", mode)
	}

	// And the same values must resolve, so a default configuration is one
	// the interface accepts.
	resolved, profile, err := resolveInterfaceThemeFor(
		defaults,
		false,
		map[string]string{},
		theme.TerminalTrueColor,
	)
	if err != nil {
		t.Fatalf("resolveInterfaceThemeFor(defaults): %v", err)
	}
	if resolved.Name != theme.DefaultTheme().Name {
		t.Errorf("resolved theme = %q, want the default", resolved.Name)
	}
	if profile != theme.ProfileTrueColor {
		t.Errorf("resolved profile = %v, want true color", profile)
	}
}

// A mistyped theme is a configuration error, and the message has to say
// what there is: an interface setting that is silently ignored is found
// out about from a screenshot.
func TestUnknownThemeIsAConfigurationError(t *testing.T) {
	path := writeInterfaceConfig(t, "solarized-latte", "auto")

	for _, command := range []string{"doctor", "tui"} {
		t.Run(command, func(t *testing.T) {
			stdout, stderr, code := runCommand(
				t,
				command,
				"--config",
				path,
			)

			if code == 0 {
				t.Fatalf(
					"exit code = 0, want a configuration error\nstdout:\n%s",
					stdout,
				)
			}

			output := stdout + stderr
			if !strings.Contains(output, "solarized-latte") {
				t.Errorf("output does not name the rejected theme:\n%s", output)
			}
			for _, name := range theme.ThemeNames() {
				if !strings.Contains(output, name) {
					t.Errorf("output does not list %q:\n%s", name, output)
				}
			}
		})
	}
}

func TestUnknownColorModeIsAConfigurationError(t *testing.T) {
	path := writeInterfaceConfig(t, "", "sometimes")

	stdout, stderr, code := runCommand(t, "doctor", "--config", path)

	if code == 0 {
		t.Fatalf(
			"exit code = 0, want a configuration error\nstdout:\n%s",
			stdout,
		)
	}

	output := stdout + stderr
	if !strings.Contains(output, "sometimes") {
		t.Errorf("output does not name the rejected value:\n%s", output)
	}
	for _, mode := range theme.ColorModeNames() {
		if !strings.Contains(output, mode) {
			t.Errorf("output does not list %q:\n%s", mode, output)
		}
	}
}

// --no-color has to win over a terminal that could show colour, because
// the user asked for it on this command line.
func TestNoColorFlagForcesTheNoColorProfile(t *testing.T) {
	path := writeInterfaceConfig(t, "", "")

	_, _, code := runCommand(t, "tui", "--config", path, "--no-color")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}

	resolved, profile, err := resolveInterfaceThemeFor(
		loadInterfaceConfig(t, path),
		true,
		map[string]string{},
		theme.TerminalTrueColor,
	)
	if err != nil {
		t.Fatalf("resolveInterfaceThemeFor: %v", err)
	}
	if profile != theme.ProfileNoColor {
		t.Errorf("profile = %v, want no colour", profile)
	}
	if resolved.Tokens.PrimaryText.IsSet() {
		t.Error("the resolved theme still has a primary text colour")
	}
}

// A user whose terminal cannot be measured still asked for colour, and
// the setting is called "always": a pipe, `script`, tmux with an unusual
// TERM and the terminals of some IDEs all report no colour, and a user
// who writes color = "always" in a configuration file is right more often
// than the measurement is.
func TestAlwaysColorModeForcesAColourProfile(t *testing.T) {
	path := writeInterfaceConfig(t, "", "always")

	_, profile, err := resolveInterfaceThemeFor(
		loadInterfaceConfig(t, path),
		false,
		map[string]string{},
		theme.TerminalAscii,
	)
	if err != nil {
		t.Fatalf("resolveInterfaceThemeFor: %v", err)
	}
	if profile != theme.ProfileANSI256 {
		t.Errorf(
			"profile = %v, want ansi-256: always must force colour",
			profile,
		)
	}

	// And it must not survive --no-color, which is the most specific
	// thing a user can say.
	_, profile, err = resolveInterfaceThemeFor(
		loadInterfaceConfig(t, path),
		true,
		map[string]string{},
		theme.TerminalAscii,
	)
	if err != nil {
		t.Fatalf("resolveInterfaceThemeFor: %v", err)
	}
	if profile != theme.ProfileNoColor {
		t.Errorf(
			"profile = %v, want no colour: --no-color outranks the "+
				"configuration",
			profile,
		)
	}
}

// The doctor report is how a user finds out which theme and profile their
// terminal actually got, which is the only way to tell a wrong setting
// from a wrong terminal.
func TestDoctorReportsTheInterface(t *testing.T) {
	path := writeInterfaceConfig(t, "gruvbox-dark", "never")

	stdout, _, code := runCommand(t, "doctor", "--config", path)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", code, stdout)
	}

	if !strings.Contains(stdout, "gruvbox-dark") {
		t.Errorf("doctor does not report the theme:\n%s", stdout)
	}
	if !strings.Contains(stdout, "no-color") {
		t.Errorf("doctor does not report the profile:\n%s", stdout)
	}
	if !strings.Contains(stdout, "dark") {
		t.Errorf("doctor does not report the mode:\n%s", stdout)
	}
}

// The theme has to reach the model, or resolving it here would be a
// ceremony with no effect. The views do not read it yet: that is PR-10A.2.
func TestThemeReachesTheTUIDependencies(t *testing.T) {
	built, err := theme.ThemeFor("tokyo-night-storm")
	if err != nil {
		t.Fatalf("ThemeFor: %v", err)
	}
	degraded := built.ForProfile(theme.ProfileANSI256)

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
	).WithInterface(degraded, theme.ProfileANSI256)

	if err := app.RunTUI(context.Background()); err != nil {
		t.Fatalf("RunTUI: %v", err)
	}

	if captured.Theme.Name != "tokyo-night-storm" {
		t.Errorf("theme name = %q, want tokyo-night-storm", captured.Theme.Name)
	}
	if captured.ColorProfile != theme.ProfileANSI256 {
		t.Errorf(
			"color profile = %v, want ansi-256",
			captured.ColorProfile,
		)
	}
	if !captured.Theme.Tokens.PrimaryText.IsSet() {
		t.Error("the theme reached the model with no colours")
	}
}

// A model built without a resolution still has to draw something, and
// what it draws has to be legible on any terminal.
func TestModelWithoutAResolvedThemeDrawsWithoutColour(t *testing.T) {
	resolved, profile := tui.NewModel().Theme()

	if resolved.Name != theme.DefaultTheme().Name {
		t.Errorf("theme = %q, want the default %q", resolved.Name, theme.DefaultTheme().Name)
	}
	if profile != theme.ProfileNoColor {
		t.Errorf(
			"profile = %v, want no colour: a model that was not told "+
				"what the terminal can do must not guess",
			profile,
		)
	}
}

// An app built without WithInterface must not draw a zero theme, which
// would be an interface with no colours at all.
func TestAppWithoutAThemeUsesTheDefaultOne(t *testing.T) {
	app := New(config.Default(), nil, func(tui.ChatSource) error { return nil })

	if got := app.interfaceTheme().Name; got != theme.DefaultTheme().Name {
		t.Errorf("theme = %q, want the default %q", got, theme.DefaultTheme().Name)
	}
}

// ---- helpers ----

// runCommand runs telecli with the arguments and captures both streams.
func runCommand(
	t *testing.T,
	command string,
	args ...string,
) (stdout string, stderr string, code int) {
	t.Helper()

	var out, errOut bytes.Buffer

	code = Main(
		append([]string{"telecli", command}, args...),
		Environment{
			Stdout: &out,
			Stderr: &errOut,
			// The TUI is never drawn in these tests: a terminal-less run
			// would otherwise wait for input.
			RunTUI:              func(tui.ChatSource) error { return nil },
			RunTUIWithSubmitter: func(context.Context, tui.Dependencies) error { return nil },
			ReportTDLib: func(
				context.Context,
				config.Config,
				io.Writer,
			) int {
				return 0
			},
		},
	)

	return out.String(), errOut.String(), code
}

// writeInterfaceConfig writes a configuration file with the given
// interface settings in a temporary directory, and points TELECLI_CONFIG
// at it. An empty value leaves the built-in default in place.
func writeInterfaceConfig(
	t *testing.T,
	themeName string,
	colorMode string,
) string {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	cfg := config.Default()
	cfg.DataDir = filepath.Join(dir, "data")
	cfg.TDLib.DatabaseDir = filepath.Join(dir, "data", "tdlib", "database")
	cfg.TDLib.FilesDir = filepath.Join(dir, "data", "tdlib", "files")
	if themeName != "" {
		cfg.TUI.Theme = themeName
	}
	if colorMode != "" {
		cfg.TUI.Color = colorMode
	}

	if err := config.WriteFile(path, cfg); err != nil {
		t.Fatalf("write config: %v", err)
	}

	t.Setenv("TELECLI_CONFIG", path)

	return path
}

// loadInterfaceConfig loads the configuration a test wrote.
func loadInterfaceConfig(t *testing.T, path string) config.Config {
	t.Helper()

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	return cfg
}

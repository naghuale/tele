package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The interface settings are stored here but validated by
// internal/tui/theme and internal/tui/termwidth, which are leaves above
// this one. These tests cover what this package owns: the values are read,
// and the defaults are written into a configuration file so a user can see
// what was decided.

func TestInterfaceDefaultsAreWrittenIntoTheFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	if err := WriteFile(path, Default()); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	for _, want := range []string{
		"[tui]",
		`theme = "` + DefaultTUITheme + `"`,
		`color = "` + DefaultTUIColorMode + `"`,
		`width = "` + DefaultTUIWidthMode + `"`,
	} {
		if !strings.Contains(string(contents), want) {
			t.Errorf(
				"the written configuration has no %q:\n%s",
				want,
				contents,
			)
		}
	}
}

func TestInterfaceSettingsAreRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	contents := `log_level = "info"
data_dir = "/tmp/telecli-config-test"

[tui]
theme = "gruvbox-dark"
color = "never"
width = "grapheme"
`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.TUI.Theme != "gruvbox-dark" {
		t.Errorf("theme = %q, want gruvbox-dark", cfg.TUI.Theme)
	}
	if cfg.TUI.Color != "never" {
		t.Errorf("color = %q, want never", cfg.TUI.Color)
	}
	if cfg.TUI.Width != "grapheme" {
		t.Errorf("width = %q, want grapheme", cfg.TUI.Width)
	}
}

// An unknown value is carried as written rather than rejected here. The
// vocabulary belongs to the theme package, so this package cannot tell an
// unknown theme from a theme that a later build adds, and the theme
// package reports it with the valid names in the message. The
// composition root is what turns that into a configuration error.
func TestUnknownInterfaceValuesAreCarriedAsWritten(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	contents := `log_level = "info"
data_dir = "/tmp/telecli-config-test"

[tui]
theme = "solarized-latte"
color = "sometimes"
width = "columns"
`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.TUI.Theme != "solarized-latte" {
		t.Errorf("theme = %q, want the value as written", cfg.TUI.Theme)
	}
	if cfg.TUI.Color != "sometimes" {
		t.Errorf("color = %q, want the value as written", cfg.TUI.Color)
	}
	if cfg.TUI.Width != "columns" {
		t.Errorf("width = %q, want the value as written", cfg.TUI.Width)
	}
}

// A file without the section must get the defaults, because a
// configuration written before this setting existed has none.
func TestAbsentInterfaceSectionGetsTheDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	contents := "log_level = \"info\"\ndata_dir = \"/tmp/telecli-config-test\"\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.TUI.Theme != DefaultTUITheme {
		t.Errorf("theme = %q, want the default %q", cfg.TUI.Theme, DefaultTUITheme)
	}
	if cfg.TUI.Color != DefaultTUIColorMode {
		t.Errorf("color = %q, want the default %q", cfg.TUI.Color, DefaultTUIColorMode)
	}
	if cfg.TUI.Width != DefaultTUIWidthMode {
		t.Errorf("width = %q, want the default %q", cfg.TUI.Width, DefaultTUIWidthMode)
	}
}

// An unknown key in [tui] is a note, not a refusal.
//
// The owner added nerd_font to their own file before this build knew the
// setting, and a build that refused the file took every command with it,
// including the one that would have said why. So the name of the unknown
// setting is reported and the rest of the file is read; every other
// section stays strict, because there an unknown key is a misspelling of
// a setting that exists.
func TestUnknownInterfaceKeyIsAWarning(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	contents := `log_level = "info"
data_dir = "/tmp/telecli-config-test"

[tui]
theme = "gruvbox-dark"
nerd_font = true
`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("an unknown [tui] key must not stop the program: %v", err)
	}
	if cfg.TUI.Theme != "gruvbox-dark" {
		t.Errorf("theme = %q, want gruvbox-dark", cfg.TUI.Theme)
	}

	warnings := strings.Join(cfg.Warnings, "\n")
	if !strings.Contains(warnings, "nerd_font") {
		t.Errorf("the warning does not name the setting: %q", warnings)
	}
	if !strings.Contains(warnings, "tui") {
		t.Errorf("the warning does not name the section: %q", warnings)
	}
}

// TestUnknownKeyInAnotherSectionIsStillAnError pins the other half of
// the rule: [auth] holds what a user must not get wrong, so an unknown
// key there is a mistake in the file and not a setting from a newer
// build.
func TestUnknownKeyInAnotherSectionIsStillAnError(t *testing.T) {
	for name, contents := range map[string]string{
		"auth": `data_dir = "/tmp/telecli-config-test"
[auth]
api_id = 123456
nerd_font = true
`,
		"the top of the file": `data_dir = "/tmp/telecli-config-test"
nerd_font = true
`,
		"the message queue": `data_dir = "/tmp/telecli-config-test"
[message_delivery]
nerd_font = true
`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatalf("write config: %v", err)
			}

			if _, err := Load(path); err == nil {
				t.Fatal("an unknown key outside [tui] must be an error")
			}
		})
	}
}

// TestSetValueKeepsAnUnknownInterfaceKey ties the two rules together: a
// file with a setting this build does not know is still a file a person
// can change, and the line they wrote is still there afterwards.
func TestSetValueKeepsAnUnknownInterfaceKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")

	before := `log_level = "info"
data_dir = "/tmp/telecli-config-test"

[tui]
nerd_font = true
theme = "gruvbox-dark"
`
	if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if err := SetValue(path, "tui.theme", "nord"); err != nil {
		t.Fatalf("SetValue: %v", err)
	}

	after := string(readFileBytes(t, path))
	if !strings.Contains(after, "nerd_font = true") {
		t.Fatalf("the unknown setting was dropped:\n%s", after)
	}
	if !strings.Contains(after, `theme = "nord"`) {
		t.Fatalf("the value did not change:\n%s", after)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("the file must still load: %v\n%s", err, after)
	}
	if len(cfg.Warnings) != 1 {
		t.Fatalf("warnings = %v, want the note about nerd_font", cfg.Warnings)
	}
}

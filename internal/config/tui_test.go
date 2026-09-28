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
		`nerd_font = false`,
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
nerd_font = true
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
	if !cfg.TUI.NerdFont {
		t.Error("nerd_font = false, want true")
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
	if cfg.TUI.NerdFont != DefaultTUINerdFont {
		t.Errorf(
			"nerd_font = %v, want the default %v",
			cfg.TUI.NerdFont, DefaultTUINerdFont,
		)
	}
}

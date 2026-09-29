package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// `telecli configure set` changes one value in a file a person keeps by
// hand, so these tests are about the bytes: which of them change, and
// what happens to the file when the command refuses.

// sameExceptOneLine returns whether two documents differ only in the
// lines that hold the given values.
//
// The claim is about the bytes around the value, not about the
// configuration: a comment a person wrote, a blank line they left and
// the order of the sections are theirs, and a command that rewrites the
// file throws all of it away.
func sameExceptOneLine(
	t *testing.T,
	before string,
	after string,
	changed []string,
) {
	t.Helper()

	beforeLines := strings.Split(before, "\n")
	afterLines := strings.Split(after, "\n")

	if len(beforeLines) != len(afterLines) {
		t.Fatalf(
			"the document changed its shape:\n%s\n---\n%s",
			before,
			after,
		)
	}

	for i, line := range beforeLines {
		if line == afterLines[i] {
			continue
		}

		// A line that changed must be the line of the value, and it
		// must hold nothing of the old one.
		holdsChanged := false
		for _, value := range changed {
			if strings.Contains(line, value) {
				holdsChanged = true
				break
			}
		}
		if !holdsChanged {
			t.Fatalf(
				"line %d changed and is not the line of the value:\n"+
					" before: %q\n  after: %q",
				i+1,
				line,
				afterLines[i],
			)
		}
	}
}

func TestSetValueChangesOneLineAndNothingElse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")

	before := fullSettingsBody
	if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := SetValue(path, "tui.theme", "gruvbox"); err != nil {
		t.Fatal(err)
	}

	after := string(readFileBytes(t, path))
	sameExceptOneLine(t, before, after, []string{`theme = "gruvbox-dark"`})
	if !contains(after, `theme = "gruvbox"`) {
		t.Fatalf("the value is not in the file:\n%s", after)
	}

	// The file still loads, and it is the new value that loads.
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("the file must still load: %v\n%s", err, after)
	}
	if cfg.TUI.Theme != "gruvbox" {
		t.Fatalf("theme = %q, want gruvbox", cfg.TUI.Theme)
	}
	if cfg.TUI.Color != "auto" || cfg.Auth.APIID != 123456 {
		t.Fatalf("another setting moved: %#v", cfg)
	}
}

func TestSetValueChangesEveryKindOfValue(t *testing.T) {
	cases := []struct {
		key   string
		value string
		holds string
		reads string
	}{
		{"log_level", "debug", `log_level = "debug"`, "debug"},
		{"tui.color", "never", `color = "never"`, "never"},
		{
			"tdlib.receive_timeout_ms",
			"250",
			"receive_timeout_ms = 250",
			"250",
		},
		{
			"auth.api_id",
			"654321",
			"api_id = 654321",
			"654321",
		},
		{
			"message_delivery.mode",
			"durable",
			`mode = "durable"`,
			"durable",
		},
		{
			"message_delivery.database_id",
			"telecli-second",
			`database_id = "telecli-second"`,
			"telecli-second",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.key, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(
				path,
				[]byte(fullSettingsBody),
				0o600,
			); err != nil {
				t.Fatal(err)
			}

			if err := SetValue(path, testCase.key, testCase.value); err != nil {
				t.Fatal(err)
			}

			after := string(readFileBytes(t, path))
			if !contains(after, testCase.holds) {
				t.Fatalf("the file does not hold %q:\n%s", testCase.holds, after)
			}

			cfg, err := Load(path)
			if err != nil {
				t.Fatalf("the file must still load: %v\n%s", err, after)
			}

			value, err := SettingValue(cfg, testCase.key)
			if err != nil {
				t.Fatal(err)
			}
			if value != testCase.reads {
				t.Fatalf(
					"%s = %q, want %q",
					testCase.key,
					value,
					testCase.reads,
				)
			}
		})
	}
}

func TestSetValueCreatesTheSectionAndTheFile(t *testing.T) {
	t.Run("the file is created", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "telecli", "config.toml")

		if err := SetValue(path, "tui.theme", "gruvbox"); err != nil {
			t.Fatal(err)
		}

		if !contains(string(readFileBytes(t, path)), "[tui]") {
			t.Fatal("the section was not created")
		}
		assertFileMode(t, path, ConfigFileMode)
		assertDirMode(t, filepath.Dir(path), ConfigDirMode)

		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("the created file must load: %v", err)
		}
		if cfg.TUI.Theme != "gruvbox" {
			t.Fatalf("theme = %q, want gruvbox", cfg.TUI.Theme)
		}
	})

	t.Run("the section is created", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.toml")
		before := "log_level = \"info\"\n"
		if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
			t.Fatal(err)
		}

		if err := SetValue(path, "tui.width", "codepoint"); err != nil {
			t.Fatal(err)
		}

		after := string(readFileBytes(t, path))
		if !contains(after, "[tui]") || !contains(after, `width = "codepoint"`) {
			t.Fatalf("the section was not added:\n%s", after)
		}
		// The first line is still the first line.
		if !strings.HasPrefix(after, before) {
			t.Fatalf("the lines before the new section moved:\n%s", after)
		}

		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("the file must still load: %v\n%s", err, after)
		}
		if cfg.TUI.Width != "codepoint" {
			t.Fatalf("width = %q, want codepoint", cfg.TUI.Width)
		}
		if cfg.TUI.Theme != DefaultTUITheme {
			t.Fatalf("theme = %q, want the default", cfg.TUI.Theme)
		}
	})

	t.Run("a value before the first table is changed", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.toml")
		before := "log_level = \"info\"\n\n[tui]\ntheme = \"a\"\n"
		if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
			t.Fatal(err)
		}

		if err := SetValue(path, "log_level", "warn"); err != nil {
			t.Fatal(err)
		}

		after := string(readFileBytes(t, path))
		sameExceptOneLine(t, before, after, []string{`log_level = "info"`})
		if !contains(after, `log_level = "warn"`) {
			t.Fatalf("the value did not change:\n%s", after)
		}
	})
}

// TestSetValueRefusesAndKeepsTheFile pins the promise that a refused
// change costs nothing: the file is byte for byte what it was.
func TestSetValueRefusesAndKeepsTheFile(t *testing.T) {
	cases := []struct {
		name  string
		key   string
		value string
	}{
		{"an unknown key", "tui.nerd_font", "true"},
		{"an unknown section", "interface.theme", "gruvbox"},
		{"a value that is not a whole number", "auth.api_id", "twelve"},
		{"a value that is not absolute", "data_dir", "telecli-data"},
		{"a value that is empty", "tui.theme", ""},
		{"a value that is not a mode", "message_delivery.mode", "direct"},
		{"a value with a newline", "tui.theme", "gruvbox\ncolor = \"never\""},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(
				path,
				[]byte(fullSettingsBody),
				0o600,
			); err != nil {
				t.Fatal(err)
			}
			before := readFileBytes(t, path)

			if err := SetValue(path, testCase.key, testCase.value); err == nil {
				t.Fatal("expected a refusal")
			}

			if after := readFileBytes(t, path); !reflect.DeepEqual(
				before,
				after,
			) {
				t.Fatalf("the file changed:\n%s", after)
			}

			// And the file still loads: a refused change leaves a
			// working configuration.
			if _, err := Load(path); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSetValueRefusesAKeyThatIsNotInTheTable(t *testing.T) {
	err := SetValue(filepath.Join(t.TempDir(), "c.toml"), "tui.nope", "1")
	if err == nil {
		t.Fatal("expected a refusal")
	}
	for _, key := range []string{"tui.theme", "log_level"} {
		if !contains(err.Error(), key) {
			t.Fatalf("the refusal does not list %q: %v", key, err)
		}
	}
}

func TestSettingValueAnswersFromTheFileAndFromTheDefault(t *testing.T) {
	t.Run("from the file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.toml")
		if err := os.WriteFile(
			path,
			[]byte(fullSettingsBody),
			0o600,
		); err != nil {
			t.Fatal(err)
		}

		cfg, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}

		value, err := SettingValue(cfg, "tui.theme")
		if err != nil {
			t.Fatal(err)
		}
		if value != "gruvbox-dark" {
			t.Fatalf("theme = %q, want gruvbox-dark", value)
		}
	})

	t.Run("from the default", func(t *testing.T) {
		value, err := SettingValue(Default(), "tui.theme")
		if err != nil {
			t.Fatal(err)
		}
		if value != DefaultTUITheme {
			t.Fatalf("theme = %q, want %q", value, DefaultTUITheme)
		}
	})

	t.Run("an unknown key", func(t *testing.T) {
		if _, err := SettingValue(Default(), "nope"); err == nil {
			t.Fatal("expected a refusal")
		}
	})
}

// TestSettingKeysCoverTheConfiguration pins that every setting a person
// can see in the file is one a person can change, and the other way
// round: a key the help lists that the table does not have is a key the
// help promises and the command refuses.
func TestSettingKeysCoverTheConfiguration(t *testing.T) {
	keys := SettingKeys()

	for _, want := range []string{
		"log_level",
		"data_dir",
		"tdlib.library_path",
		"tdlib.database_dir",
		"tdlib.files_dir",
		"tdlib.receive_timeout_ms",
		"tdlib.shutdown_timeout_ms",
		"auth.api_id",
		"auth.credential_profile",
		"message_delivery.mode",
		"message_delivery.data_dir",
		"message_delivery.database_id",
		"message_delivery.instance_id",
		"tui.theme",
		"tui.color",
		"tui.width",
	} {
		if _, err := findSetting(want); err != nil {
			t.Errorf("%s: %v", want, err)
		}
	}

	if len(keys) != len(settings) {
		t.Fatalf("%d keys for %d settings", len(keys), len(settings))
	}
}

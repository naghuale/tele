package application

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"telecli/internal/authstore"
	"telecli/internal/config"
)

// The commands that read and change the settings file: where it lives,
// what `configure status` says about it, and the two commands that edit
// one value or remove the file. They are exercised through Main, on a
// temporary home, so the paths a person would see are the ones the
// command really resolves.

// settingsHome points the two per-user locations at temporary
// directories and returns them.
func settingsHome(t *testing.T) (settingsDir, homeDir string) {
	t.Helper()

	settingsDir = t.TempDir()
	homeDir = t.TempDir()

	t.Setenv("XDG_CONFIG_HOME", settingsDir)
	t.Setenv("HOME", homeDir)
	t.Setenv("TELECLI_CONFIG", "")

	return settingsDir, homeDir
}

// settingsCLI builds an environment whose streams and stores are fakes.
func settingsCLI(t *testing.T) (Environment, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()

	var stdout, stderr bytes.Buffer
	tuiCalls := 0

	env := newEnv(&stdout, &stderr, &tuiCalls)
	env.ReportTDLib = func(
		context.Context,
		config.Config,
		io.Writer,
	) int {
		return 0
	}
	env.NewTelegramCredentialStore = func() TelegramCredentialStore {
		return keychainTelegramCredentialStore{store: newFakeConfigureStore()}
	}

	return env, &stdout, &stderr
}

// runSettingsCLI runs one command and returns its exit code and output.
func runSettingsCLI(
	t *testing.T,
	env Environment,
	stdin string,
	args ...string,
) (int, string, string) {
	t.Helper()

	env.Stdin = strings.NewReader(stdin)
	code := Main(args, env)

	return code, env.Stdout.(*bytes.Buffer).String(), env.Stderr.(*bytes.Buffer).String()
}

// statusValue returns the value of one line of `configure status`.
func statusValue(t *testing.T, out, label string) string {
	t.Helper()

	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, label) {
			continue
		}

		rest := strings.TrimSpace(strings.TrimPrefix(trimmed, label))
		if rest == label || rest == "" {
			continue
		}

		return rest
	}

	t.Fatalf("status has no %q line:\n%s", label, out)

	return ""
}

func writeSettings(t *testing.T, path, body string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// settingsBody is a configuration with every section, as `telecli
// configure` writes it.
const settingsBody = `log_level = "info"
data_dir = "/tmp/telecli-settings-test/data"

[tdlib]
database_dir = "/tmp/telecli-settings-test/data/tdlib/database"
files_dir = "/tmp/telecli-settings-test/data/tdlib/files"
receive_timeout_ms = 100
shutdown_timeout_ms = 5000

[auth]
api_id = 123456
credential_profile = "default"

[message_delivery]
mode = "durable"
data_dir = "/tmp/telecli-settings-test/data/outbox"
database_id = "telecli-main"
instance_id = ""

[tui]
theme = "catppuccin-mocha"
color = "auto"
width = "auto"
`

// ---- configure status ----

// TestConfigureStatusSaysWhereTheFileIs covers every source the file can
// come from, and the machine that has no file at all.
//
// The source is the answer a person needs before they edit anything: a
// file in a directory they were not told about is a file they will not
// find.
func TestConfigureStatusSaysWhereTheFileIs(t *testing.T) {
	t.Run("the new place", func(t *testing.T) {
		settingsHome(t)
		current, err := config.DefaultPath()
		if err != nil {
			t.Fatal(err)
		}
		writeSettings(t, current, settingsBody)

		env, _, _ := settingsCLI(t)
		code, out, errOut := runSettingsCLI(
			t,
			env,
			"",
			"telecli",
			"configure",
			"status",
		)
		if code != 0 {
			t.Fatalf("code = %d, stderr = %s", code, errOut)
		}
		if !strings.Contains(out, current) {
			t.Fatalf("status does not name the file:\n%s", out)
		}
		if got := statusValue(t, out, "Source"); got != "the default file" {
			t.Fatalf("source = %q, want the default file", got)
		}
	})

	t.Run("no file at all", func(t *testing.T) {
		settingsHome(t)
		current, err := config.DefaultPath()
		if err != nil {
			t.Fatal(err)
		}

		env, _, _ := settingsCLI(t)
		code, out, errOut := runSettingsCLI(
			t,
			env,
			"",
			"telecli",
			"configure",
			"status",
		)
		if code != 0 {
			t.Fatalf("code = %d, stderr = %s", code, errOut)
		}
		if got := statusValue(t, out, "File"); got !=
			"none, the built-in defaults apply" {
			t.Fatalf("file = %q, want the defaults", got)
		}
		// A person who has to create the file has to be told where.
		if got := statusValue(t, out, "Create at"); got != current {
			t.Fatalf("create at = %q, want %q", got, current)
		}
	})

	t.Run("--config", func(t *testing.T) {
		settingsHome(t)
		explicit := filepath.Join(t.TempDir(), "explicit.toml")
		writeSettings(t, explicit, settingsBody)

		env, _, _ := settingsCLI(t)
		code, out, errOut := runSettingsCLI(
			t,
			env,
			"",
			"telecli",
			"configure",
			"status",
			"--config",
			explicit,
		)
		if code != 0 {
			t.Fatalf("code = %d, stderr = %s", code, errOut)
		}
		if !strings.Contains(out, explicit) {
			t.Fatalf("status does not name the file:\n%s", out)
		}
		if got := statusValue(t, out, "Source"); got != "explicit, --config" {
			t.Fatalf("source = %q, want --config", got)
		}
	})

	t.Run("TELECLI_CONFIG", func(t *testing.T) {
		settingsHome(t)
		envPath := filepath.Join(t.TempDir(), "from-env.toml")
		writeSettings(t, envPath, settingsBody)
		t.Setenv("TELECLI_CONFIG", envPath)

		env, _, _ := settingsCLI(t)
		code, out, errOut := runSettingsCLI(
			t,
			env,
			"",
			"telecli",
			"configure",
			"status",
		)
		if code != 0 {
			t.Fatalf("code = %d, stderr = %s", code, errOut)
		}
		if !strings.Contains(out, envPath) {
			t.Fatalf("status does not name the file:\n%s", out)
		}
		if got := statusValue(t, out, "Source"); got !=
			"environment, TELECLI_CONFIG" {
			t.Fatalf("source = %q, want TELECLI_CONFIG", got)
		}
	})

	t.Run("the old place", func(t *testing.T) {
		settingsHome(t)

		old := config.LegacyPath()
		if old == "" {
			t.Skip("this platform has no separate old settings path")
		}
		writeSettings(t, old, settingsBody)

		env, _, _ := settingsCLI(t)
		code, out, errOut := runSettingsCLI(
			t,
			env,
			"",
			"telecli",
			"configure",
			"status",
		)
		if code != 0 {
			t.Fatalf("code = %d, stderr = %s", code, errOut)
		}

		// The move happened, and status says both where the file is
		// now and that it was moved there.
		if got := statusValue(t, out, "Notice"); !strings.Contains(
			got,
			"moved the settings file",
		) {
			t.Fatalf("notice = %q, want the move", got)
		}
		current, err := config.DefaultPath()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, current) {
			t.Fatalf("status does not name the new file:\n%s", out)
		}
	})
}

// TestConfigureStatusRefusesAPairOfFiles pins that a machine with the
// file in both places is a refusal with both paths, not a choice.
func TestConfigureStatusRefusesAPairOfFiles(t *testing.T) {
	settingsHome(t)

	old := config.LegacyPath()
	if old == "" {
		t.Skip("this platform has no separate old settings path")
	}

	writeSettings(t, old, settingsBody)
	current, err := config.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	writeSettings(t, current, settingsBody)

	env, _, _ := settingsCLI(t)
	code, _, errOut := runSettingsCLI(
		t,
		env,
		"",
		"telecli",
		"configure",
		"status",
	)
	if code == 0 {
		t.Fatal("a pair of settings files must be refused")
	}
	for _, path := range []string{current, old} {
		if !strings.Contains(errOut, path) {
			t.Fatalf("the refusal does not name %q:\n%s", path, errOut)
		}
	}
}

// ---- configure reset ----

// TestConfigureResetAsksForAWordAndRemovesWhatItNamed covers the reason
// the confirmation changed: the owner ran the command to reset one line
// and lost the file after typing y.
//
// The listing comes first, the word is the only thing that removes
// anything, and the profile goes through the injected store so no real
// Keychain item is touched.
func TestConfigureResetAsksForAWordAndRemovesWhatItNamed(t *testing.T) {
	cases := []struct {
		name    string
		answer  string
		removed bool
	}{
		{"a y is not enough", "y\n", false},
		{"an empty line is not enough", "\n", false},
		{"a different word is not enough", "remove\n", false},
		{"no answer at all", "", false},
		{"the word removes", "delete\n", true},
		{"the word in another case removes", "DELETE\n", true},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.toml")
			writeSettings(t, path, settingsBody)

			var deleted []string
			env, stdout, _ := settingsCLI(t)
			env.DeleteCredentialProfile = func(
				_ context.Context,
				profile string,
			) error {
				deleted = append(deleted, profile)

				return nil
			}

			code, out, errOut := runSettingsCLI(
				t,
				env,
				testCase.answer,
				"telecli",
				"configure",
				"reset",
				"--config",
				path,
			)
			if code != 0 {
				t.Fatalf("code = %d, stderr = %s", code, errOut)
			}

			// Both things are named before anything is asked.
			if !strings.Contains(out, path) {
				t.Fatalf("the summary does not name the file:\n%s", out)
			}
			if !strings.Contains(out, "default") {
				t.Fatalf("the summary does not name the profile:\n%s", out)
			}
			if !strings.Contains(out, resetConfirmation) {
				t.Fatalf("the question does not ask for the word:\n%s", out)
			}
			_ = stdout

			_, statErr := os.Stat(path)
			removed := os.IsNotExist(statErr)
			if removed != testCase.removed {
				t.Fatalf("the file was removed = %v, want %v\n%s",
					removed, testCase.removed, out)
			}

			wantProfiles := 0
			if testCase.removed {
				wantProfiles = 1
			}
			if len(deleted) != wantProfiles {
				t.Fatalf(
					"the profile store saw %v, want %d removals",
					deleted,
					wantProfiles,
				)
			}
			if testCase.removed && deleted[0] != "default" {
				t.Fatalf("removed %q, want the configured profile", deleted[0])
			}
		})
	}
}

// TestConfigureResetYesSkipsTheQuestion pins the flag a script needs.
func TestConfigureResetYesSkipsTheQuestion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	writeSettings(t, path, settingsBody)

	env, _, _ := settingsCLI(t)
	code, out, errOut := runSettingsCLI(
		t,
		env,
		"",
		"telecli",
		"configure",
		"reset",
		"--config",
		path,
		"--yes",
	)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, errOut)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("--yes did not remove the file")
	}
	if strings.Contains(out, resetConfirmation) {
		t.Fatalf("--yes asked the question anyway:\n%s", out)
	}
}

// TestConfigureResetSaysWhenThereIsNothingToRemove covers an unconfigured
// machine: the summary must still answer, not print empty values.
func TestConfigureResetSaysWhenThereIsNothingToRemove(t *testing.T) {
	dir := t.TempDir()

	env, _, _ := settingsCLI(t)
	code, out, errOut := runSettingsCLI(
		t,
		env,
		"delete\n",
		"telecli",
		"configure",
		"reset",
		"--config",
		filepath.Join(dir, "absent.toml"),
	)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, errOut)
	}
	if !strings.Contains(out, "none: there is no configuration file") {
		t.Fatalf("the summary does not say there is no file:\n%s", out)
	}
	if !strings.Contains(out, "none: no credential profile is set") {
		t.Fatalf("the summary does not say there is no profile:\n%s", out)
	}
}

// TestConfigureResetKeepsTheQueue pins that the reset a person is
// afraid of does not touch the message queue or its key.
func TestConfigureResetKeepsTheQueue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	writeSettings(t, path, settingsBody)

	env, _, _ := settingsCLI(t)
	code, out, errOut := runSettingsCLI(
		t,
		env,
		"delete\n",
		"telecli",
		"configure",
		"reset",
		"--config",
		path,
	)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, errOut)
	}
	if !strings.Contains(out, "left in place") {
		t.Fatalf("the reset did not say what it kept:\n%s", out)
	}
}

// TestConfigureResetNeverReachesThePlatformStore is the guard on the
// injection point: the command under test must not be able to delete a
// real Keychain item even if a caller forgets to inject.
func TestConfigureResetUsesTheInjectedStore(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	writeSettings(t, path, settingsBody)

	called := false
	env, _, _ := settingsCLI(t)
	env.DeleteCredentialProfile = func(
		context.Context,
		string,
	) error {
		called = true

		return authstore.ErrProfileUnavailable
	}

	if code, _, errOut := runSettingsCLI(
		t,
		env,
		"delete\n",
		"telecli",
		"configure",
		"reset",
		"--config",
		path,
	); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, errOut)
	}
	if !called {
		t.Fatal("the profile store was not asked to remove the profile")
	}
}

// ---- configure set and get ----

func TestConfigureSetChangesOneValue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	writeSettings(t, path, settingsBody)

	env, _, _ := settingsCLI(t)
	code, out, errOut := runSettingsCLI(
		t,
		env,
		"",
		"telecli",
		"configure",
		"set",
		"--config",
		path,
		"tui.theme",
		"gruvbox-dark",
	)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, errOut)
	}
	if !strings.Contains(out, "tui.theme = gruvbox-dark") {
		t.Fatalf("the command did not report the value:\n%s", out)
	}

	body := readFile(t, path)
	if !strings.Contains(body, `theme = "gruvbox-dark"`) {
		t.Fatalf("the file does not hold the value:\n%s", body)
	}
	// Every other line is still there: the TDLib directories, the login
	// and the queue are what a person lost when a shell redirection
	// replaced the file.
	for _, want := range []string{
		`database_dir = "/tmp/telecli-settings-test/data/tdlib/database"`,
		`api_id = 123456`,
		`database_id = "telecli-main"`,
		`color = "auto"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("the file lost %q:\n%s", want, body)
		}
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("the file must still load: %v\n%s", err, body)
	}
	if cfg.TUI.Theme != "gruvbox-dark" {
		t.Fatalf("theme = %q, want gruvbox-dark", cfg.TUI.Theme)
	}
}

func TestConfigureSetCreatesTheFileInTheNewPlace(t *testing.T) {
	settingsHome(t)
	current, err := config.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}

	env, _, _ := settingsCLI(t)
	code, _, errOut := runSettingsCLI(
		t,
		env,
		"",
		"telecli",
		"configure",
		"set",
		"tui.theme",
		"gruvbox-dark",
	)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, errOut)
	}

	body := readFile(t, current)
	if !strings.Contains(body, "[tui]") ||
		!strings.Contains(body, `theme = "gruvbox-dark"`) {
		t.Fatalf("the file was not created with the value:\n%s", body)
	}

	assertOwnerOnly(t, current, 0o600)
	assertOwnerOnly(t, filepath.Dir(current), 0o700)
}

// TestConfigureSetRefusesAndKeepsTheFile is the other half: a refused
// change costs nothing.
func TestConfigureSetRefusesAndKeepsTheFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	writeSettings(t, path, settingsBody)
	before := readFile(t, path)

	for _, args := range [][]string{
		{"tui.theme"},
		{"tui.nerd_font", "true"},
		{"tui.theme"},
		{"tui.theme", "a-theme-this-build-does-not-have"},
		{"tui.width", "columns"},
		{"tui.color", "sometimes"},
		{"data_dir", "relative"},
		{"auth.api_id", "twelve"},
	} {
		full := append([]string{"telecli", "configure", "set"}, args...)
		full = append(full, "--config", path)

		env, _, _ := settingsCLI(t)
		if code, _, errOut := runSettingsCLI(t, env, "", full...); code == 0 {
			t.Fatalf("%v was accepted, stderr = %s", args, errOut)
		}
		if after := readFile(t, path); after != before {
			t.Fatalf("%v changed the file:\n%s", args, after)
		}
	}
}

func TestConfigureGetSaysWhereTheValueCameFrom(t *testing.T) {
	t.Run("from the file", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.toml")
		writeSettings(t, path, settingsBody)

		env, _, _ := settingsCLI(t)
		code, out, errOut := runSettingsCLI(
			t,
			env,
			"",
			"telecli",
			"configure",
			"get",
			"--config",
			path,
			"tui.theme",
		)
		if code != 0 {
			t.Fatalf("code = %d, stderr = %s", code, errOut)
		}
		if !strings.Contains(out, "tui.theme = catppuccin-mocha") {
			t.Fatalf("the value is not reported:\n%s", out)
		}
		if !strings.Contains(out, path) {
			t.Fatalf("the file is not reported:\n%s", out)
		}
	})

	t.Run("from the default", func(t *testing.T) {
		settingsHome(t)

		env, _, _ := settingsCLI(t)
		code, out, errOut := runSettingsCLI(
			t,
			env,
			"",
			"telecli",
			"configure",
			"get",
			"tui.theme",
		)
		if code != 0 {
			t.Fatalf("code = %d, stderr = %s", code, errOut)
		}
		if !strings.Contains(out, "the built-in default") {
			t.Fatalf("the source is not reported:\n%s", out)
		}
		// And the place to write it, since a person who asks where a
		// value comes from is about to change it.
		current, err := config.DefaultPath()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, current) {
			t.Fatalf("the place to create the file is not reported:\n%s", out)
		}
	})

	t.Run("an unknown key", func(t *testing.T) {
		settingsHome(t)

		env, _, _ := settingsCLI(t)
		code, _, errOut := runSettingsCLI(
			t,
			env,
			"",
			"telecli",
			"configure",
			"get",
			"tui.nerd_font",
		)
		if code == 0 {
			t.Fatal("an unknown key must be refused")
		}
		if !strings.Contains(errOut, "tui.theme") {
			t.Fatalf("the refusal does not list the settings:\n%s", errOut)
		}
	})
}

// ---- configure writes the new place ----

// TestConfigureWritesTheNewPlace covers the file a first run creates: it
// is under ~/.config, and only its owner can read it and enter the
// directory.
func TestConfigureWritesTheNewPlace(t *testing.T) {
	settingsHome(t)

	current, err := config.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(current, filepath.Join(".config", "telecli")) {
		t.Skipf("this platform keeps settings elsewhere: %s", current)
	}

	fixture := newConfigureFixture(t)
	fixture.request.Options.ConfigPath = ""
	fixture.request.Environ = nil

	result, err := fixture.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if result.ConfigPath != current {
		t.Fatalf("config path = %q, want %q", result.ConfigPath, current)
	}

	assertOwnerOnly(t, current, 0o600)
	assertOwnerOnly(t, filepath.Dir(current), 0o700)
}

// TestConfigureRefusesAPairOfFiles pins the refusal in setup too: a
// second file written beside the old one would leave every later start
// of telecli with a pair it cannot choose between.
func TestConfigureRefusesAPairOfFiles(t *testing.T) {
	settingsHome(t)

	old := config.LegacyPath()
	if old == "" {
		t.Skip("this platform has no separate old settings path")
	}

	writeSettings(t, old, settingsBody)
	current, err := config.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	writeSettings(t, current, settingsBody)

	fixture := newConfigureFixture(t)
	fixture.request.Options.ConfigPath = ""
	fixture.request.Environ = nil

	if _, err := fixture.run(t); err == nil {
		t.Fatal("setup must refuse a pair of settings files")
	} else {
		for _, path := range []string{current, old} {
			if !strings.Contains(err.Error(), path) {
				t.Fatalf("the refusal does not name %q: %v", path, err)
			}
		}
	}
}

// TestConfigureFollowsTheFileItReads pins that setup writes the file
// telecli reads, even while that file is the old one: a new file beside
// it would be a pair of them.
func TestConfigureFollowsTheFileItReads(t *testing.T) {
	settingsHome(t)

	old := config.LegacyPath()
	if old == "" {
		t.Skip("this platform has no separate old settings path")
	}

	writeSettings(t, old, settingsBody)

	// The new place is not writable, so the copy of the move fails and
	// the old file stays the one in use.
	newDir := filepath.Dir(mustDefaultPathForTest(t))
	if err := os.MkdirAll(newDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(newDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(newDir, 0o700) })
	if os.Geteuid() == 0 {
		t.Skip("the suite runs as a superuser: the mode does not refuse")
	}

	fixture := newConfigureFixture(t)
	fixture.request.Options.ConfigPath = ""
	fixture.request.Environ = nil

	result, err := fixture.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if result.ConfigPath != old {
		t.Fatalf("config path = %q, want the file in use %q", result.ConfigPath, old)
	}
	if !strings.Contains(result.Notice, "could not move") {
		t.Fatalf("the run did not report the refused move: %q", result.Notice)
	}
}

// ---- telecli doctor ----

// TestDoctorWarnsAboutTheOldFile pins that a settings file in the old
// place is a warning and not an error: telecli reads it, so a person is
// not blocked, and the doctor is where they find out.
func TestDoctorWarnsAboutTheOldFile(t *testing.T) {
	settingsHome(t)

	old := config.LegacyPath()
	if old == "" {
		t.Skip("this platform has no separate old settings path")
	}

	// A new place that cannot be written to: the move fails, the old
	// file stays the one in use, and the doctor has to say so.
	newDir := filepath.Dir(mustDefaultPathForTest(t))
	if err := os.MkdirAll(newDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(newDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(newDir, 0o700) })
	if os.Geteuid() == 0 {
		t.Skip("the suite runs as a superuser: the mode does not refuse")
	}

	writeSettings(t, old, settingsBody)

	env, _, _ := settingsCLI(t)
	code, out, errOut := runSettingsCLI(t, env, "", "telecli", "doctor")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, errOut)
	}
	if !strings.Contains(out, "warning:") {
		t.Fatalf("doctor did not warn about the old file:\n%s", out)
	}
	if !strings.Contains(out, old) {
		t.Fatalf("the warning does not name the old file:\n%s", out)
	}
	if !strings.Contains(out, "Config: OK") {
		t.Fatalf("the old file must not stop the program:\n%s", out)
	}
}

// ---- help ----

// TestHelpPointsAtConfigureSet pins the advice the orchestrator gave the
// owner: a shell redirection over config.toml would replace every
// setting in it, so the help has to offer the command that edits one
// line instead.
func TestHelpPointsAtConfigureSet(t *testing.T) {
	env, _, _ := settingsCLI(t)
	code, out, errOut := runSettingsCLI(t, env, "", "telecli", "--help")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, errOut)
	}

	for _, want := range []string{
		"telecli configure set <key> <value>",
		"telecli configure get <key>",
		"~/.config/telecli/config.toml",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("the help does not mention %q:\n%s", want, out)
		}
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	return string(data)
}

func assertOwnerOnly(t *testing.T, path string, want os.FileMode) {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("mode of %s = %o, want %o", path, got, want)
	}
}

func mustDefaultPathForTest(t *testing.T) string {
	t.Helper()

	path, err := config.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}

	return path
}

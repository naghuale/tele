package config

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// A settings file with every section, for the tests that need a file
// telecli would really load. The values are the ones a setup writes, so
// a move of this file is a move of a working configuration.
const fullSettingsBody = `log_level = "info"
data_dir = "/tmp/telecli-settings-test/data"

[tdlib]
library_path = "/opt/homebrew/lib/libtdjson.dylib"
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
theme = "gruvbox-dark"
color = "auto"
width = "grapheme"
`

// isolateSettingsHome points the two per-user locations at two temporary
// directories, so a test can have a settings file in the old place and
// none in the new one, which is the state the move of 29.09.2026 acts
// on.
//
// The two are separate on purpose: the new path follows
// $XDG_CONFIG_HOME and the old one follows the home directory, so a
// single temporary directory would hide the very thing under test.
func isolateSettingsHome(t *testing.T) (settingsDir, homeDir string) {
	t.Helper()

	settingsDir = t.TempDir()
	homeDir = t.TempDir()

	t.Setenv("XDG_CONFIG_HOME", settingsDir)
	t.Setenv("HOME", homeDir)
	t.Setenv(configPathEnvironment, "")

	// The guard is the same rule the whole package runs under: a test
	// must never see the home directory of the person running it.
	if path := mustDefaultPath(t); !strings.HasPrefix(path, settingsDir) {
		t.Fatalf(
			"settings isolation failed: default path %q is not under %q",
			path,
			settingsDir,
		)
	}
	if legacy := LegacyPath(); legacy != "" &&
		!strings.HasPrefix(legacy, homeDir) {
		t.Fatalf(
			"settings isolation failed: old path %q is not under %q",
			legacy,
			homeDir,
		)
	}

	return settingsDir, homeDir
}

// requireSeparateOldPath skips a test on a platform where the old
// settings path and the new one are the same directory.
//
// On Linux os.UserConfigDir is the new place already, so there is no old
// place to move a file from. The steps of the move are covered on every
// platform by the migration tests, which name the two paths themselves.
func requireSeparateOldPath(t *testing.T) {
	t.Helper()

	if LegacyPath() == "" {
		t.Skip("this platform has no separate old settings path: " +
			"the directory os.UserConfigDir reports is the new one")
	}
}

// ---- DefaultPath and LegacyPath ----

func TestDefaultPathIsInDotConfig(t *testing.T) {
	home := isolateHomeOnly(t)

	want := filepath.Join(
		home,
		".config",
		"telecli",
		"config.toml",
	)

	if got := mustDefaultPath(t); got != want {
		t.Fatalf("default path = %q, want %q", got, want)
	}
}

func TestDefaultPathFollowsXDGConfigHome(t *testing.T) {
	settingsDir, _ := isolateSettingsHome(t)

	want := filepath.Join(settingsDir, "telecli", "config.toml")
	if got := mustDefaultPath(t); got != want {
		t.Fatalf("default path = %q, want %q", got, want)
	}
}

// TestDefaultPathIgnoresARelativeXDGConfigHome pins the XDG rule: a
// relative directory in the variable is ignored, because a path that
// resolves from the working directory would make the same machine read
// two different files depending on where telecli was started.
func TestDefaultPathIgnoresARelativeXDGConfigHome(t *testing.T) {
	home := isolateHomeOnly(t)
	t.Setenv("XDG_CONFIG_HOME", "relative/config")

	want := filepath.Join(
		home,
		".config",
		"telecli",
		"config.toml",
	)
	if got := mustDefaultPath(t); got != want {
		t.Fatalf("default path = %q, want %q", got, want)
	}
}

func TestDefaultPathFailsWithoutAnAbsoluteHome(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "relative-home")

	if _, err := DefaultPath(); !errors.Is(
		err,
		ErrConfigDirUnavailable,
	) {
		t.Fatalf("error = %v, want ErrConfigDirUnavailable", err)
	}
}

// TestOldPathIsSeparateFromTheNewOne pins the shape of the move: the old
// place is the one os.UserConfigDir names, and it is only an old place
// while it differs from the new one.
func TestOldPathIsSeparateFromTheNewOne(t *testing.T) {
	isolateSettingsHome(t)

	legacy := LegacyPath()
	if legacy == "" {
		// The platform has no separate old directory: os.UserConfigDir
		// is the new place, so there is nothing to move.
		return
	}

	if legacy == mustDefaultPath(t) {
		t.Fatal("the old path must differ from the new one")
	}
	if !strings.HasSuffix(legacy, filepath.Join("telecli", "config.toml")) {
		t.Fatalf("old path = %q, want a telecli/config.toml under it", legacy)
	}
}

// isolateHomeOnly points the home directory at a temporary directory
// and clears the XDG variable, so the new path is the one under
// ~/.config rather than the one under a directory the test chose.
func isolateHomeOnly(t *testing.T) string {
	t.Helper()

	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", home)
	t.Setenv(configPathEnvironment, "")

	if path := mustDefaultPath(t); !strings.HasPrefix(path, home) {
		t.Fatalf(
			"home isolation failed: default path %q is not under %q",
			path,
			home,
		)
	}

	return home
}

// ---- ResolvePath ----

// TestResolvePathPlaces covers the whole order in one table, with a
// temporary home and a temporary XDG_CONFIG_HOME for every case.
//
// The two rows about the old place are skipped where the old place and
// the new one are the same directory, because there is no pair of files
// to choose between there.
func TestResolvePathPlaces(t *testing.T) {
	cases := []struct {
		name string
		// arrange writes the files the case is about.
		currentFile bool
		oldFile     bool
		// relativeXDG replaces XDG_CONFIG_HOME with a relative path
		// before the files are written, so the new path is the one
		// under the home directory.
		relativeXDG bool
		explicit    string
		env         string
		want        PathSource
		wantError   error
		needsOld    bool
	}{
		{
			name:        "only the new file",
			currentFile: true,
			want:        PathSourceDefault,
		},
		{
			name:     "only the old file is moved",
			oldFile:  true,
			want:     PathSourceDefault,
			needsOld: true,
		},
		{
			name:        "both files at once",
			currentFile: true,
			oldFile:     true,
			wantError:   ErrConfigAmbiguous,
			needsOld:    true,
		},
		{
			name: "neither file",
			want: PathSourceNone,
		},
		{
			name:        "a relative XDG_CONFIG_HOME is ignored",
			relativeXDG: true,
			currentFile: true,
			want:        PathSourceDefault,
		},
		{
			name:     "an explicit path wins",
			explicit: "explicit.toml",
			want:     PathSourceExplicit,
		},
		{
			name: "TELECLI_CONFIG wins over the new file",
			env:  "from-env.toml",
			want: PathSourceEnvironment,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			settingsDir, _ := isolateSettingsHome(t)
			if testCase.needsOld {
				requireSeparateOldPath(t)
			}

			if testCase.relativeXDG {
				t.Setenv("XDG_CONFIG_HOME", "relative/config")
			}

			current := mustDefaultPath(t)
			old := LegacyPath()

			if testCase.currentFile {
				writeConfig(t, current, "log_level = \"debug\"\n")
			}
			if testCase.oldFile {
				writeConfig(t, old, "log_level = \"debug\"\n")
			}

			explicit := ""
			if testCase.explicit != "" {
				explicit = filepath.Join(settingsDir, testCase.explicit)
				writeConfig(t, explicit, "log_level = \"debug\"\n")
			}
			if testCase.env != "" {
				envPath := filepath.Join(settingsDir, testCase.env)
				writeConfig(t, envPath, "log_level = \"debug\"\n")
				t.Setenv(configPathEnvironment, envPath)
			}

			resolved, err := ResolvePath(explicit)

			if testCase.wantError != nil {
				if !errors.Is(err, testCase.wantError) {
					t.Fatalf("error = %v, want %v", err, testCase.wantError)
				}
				if resolved.Found() {
					t.Fatal("a refused resolution must not report a path")
				}
				if testCase.wantError == ErrConfigAmbiguous {
					// The message has to name both files, or a person
					// cannot tell which one to keep.
					for _, path := range []string{current, old} {
						if !contains(err.Error(), path) {
							t.Fatalf(
								"error %q does not name %q",
								err.Error(),
								path,
							)
						}
					}
				}

				return
			}

			if err != nil {
				t.Fatal(err)
			}
			if resolved.Source != testCase.want {
				t.Fatalf(
					"source = %q, want %q",
					resolved.Source,
					testCase.want,
				)
			}

			wantPath := current
			switch {
			case explicit != "":
				wantPath = explicit
			case testCase.env != "":
				wantPath = filepath.Join(settingsDir, testCase.env)
			}
			if resolved.Source == PathSourceNone {
				// No file means no path to report: the caller uses
				// the empty one to mean "the defaults apply".
				if resolved.Path != "" {
					t.Fatalf("path = %q, want empty", resolved.Path)
				}
			} else if resolved.Path != wantPath {
				t.Fatalf("path = %q, want %q", resolved.Path, wantPath)
			}

			// Only a case that moved a file has anything to report.
			if testCase.oldFile && !testCase.currentFile {
				if !contains(resolved.Notice, old) ||
					!contains(resolved.Notice, current) {
					t.Fatalf("notice = %q, want both paths", resolved.Notice)
				}
			} else if resolved.Notice != "" {
				t.Fatalf("notice = %q, want none", resolved.Notice)
			}
		})
	}
}

// TestResolvePathKeepsTheOldFileWhenTheMoveFails pins the promise the
// move makes: telecli keeps working from the old file, and says the move
// did not happen.
func TestResolvePathKeepsTheOldFileWhenTheMoveFails(t *testing.T) {
	isolateSettingsHome(t)
	requireSeparateOldPath(t)

	old := LegacyPath()
	writeConfig(t, old, "log_level = \"debug\"\n")

	refused := errors.New("no room at the new place")
	resolved, err := resolvePath("", MigrationOps{
		Copy: func(string, string) error { return refused },
	})
	if err != nil {
		t.Fatal(err)
	}

	if resolved.Source != PathSourceLegacy {
		t.Fatalf("source = %q, want legacy", resolved.Source)
	}
	if resolved.Path != old {
		t.Fatalf("path = %q, want the old file %q", resolved.Path, old)
	}
	if !contains(resolved.Notice, "could not move") {
		t.Fatalf("notice = %q, want the refused move", resolved.Notice)
	}

	cfg, err := LoadResolved(resolved)
	if err != nil {
		t.Fatalf("telecli must still read the old file: %v", err)
	}
	if cfg.LogLevel != "debug" {
		t.Fatalf("log_level = %q, want the old value", cfg.LogLevel)
	}
}

// TestLoadResolvedReadsTheOldFile covers the file the move could not
// finish: it loads like any other, so a machine is never left without
// settings because of a directory it cannot write to.
func TestLoadResolvedReadsTheOldFile(t *testing.T) {
	isolateSettingsHome(t)
	requireSeparateOldPath(t)

	old := LegacyPath()
	writeConfig(t, old, fullSettingsBody)

	resolved, err := resolvePath("", MigrationOps{
		Copy: func(string, string) error {
			return errors.New("refused")
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadResolved(resolved)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TUI.Theme != "gruvbox-dark" {
		t.Fatalf("theme = %q, want gruvbox-dark", cfg.TUI.Theme)
	}
	if cfg.Auth.APIID != 123456 {
		t.Fatalf("api_id = %d, want 123456", cfg.Auth.APIID)
	}
}

// TestAmbiguityErrorIsRefusedForBothFiles pins the rule the two rows of
// the table above depend on: a pair of files is a refusal, with both
// paths in it, and a single file is not.
func TestAmbiguityErrorIsRefusedForBothFiles(t *testing.T) {
	isolateSettingsHome(t)
	requireSeparateOldPath(t)

	current := mustDefaultPath(t)
	old := LegacyPath()

	if err := AmbiguityError(current); err != nil {
		t.Fatalf("an empty pair must not be a refusal: %v", err)
	}

	writeConfig(t, old, "log_level = \"info\"\n")
	if err := AmbiguityError(current); err != nil {
		t.Fatalf("one file must not be a refusal: %v", err)
	}

	writeConfig(t, current, "log_level = \"info\"\n")
	err := AmbiguityError(current)
	if !errors.Is(err, ErrConfigAmbiguous) {
		t.Fatalf("error = %v, want ErrConfigAmbiguous", err)
	}
	if !contains(err.Error(), current) || !contains(err.Error(), old) {
		t.Fatalf("error %q does not name both files", err.Error())
	}
}

// TestWriteTargetNeverPointsAtTheOldFile pins where a command that
// writes settings puts them: the new place, never the one the move
// replaces.
func TestWriteTargetNeverPointsAtTheOldFile(t *testing.T) {
	isolateSettingsHome(t)
	requireSeparateOldPath(t)

	target, err := WriteTarget("")
	if err != nil {
		t.Fatal(err)
	}
	if target != mustDefaultPath(t) {
		t.Fatalf("target = %q, want the new default", target)
	}
	if target == LegacyPath() {
		t.Fatal("a command that writes must never target the old file")
	}

	explicit := filepath.Join(t.TempDir(), "explicit.toml")
	target, err = WriteTarget(explicit)
	if err != nil {
		t.Fatal(err)
	}
	if target != explicit {
		t.Fatalf("target = %q, want the explicit path", target)
	}

	envPath := filepath.Join(t.TempDir(), "from-env.toml")
	t.Setenv(configPathEnvironment, envPath)
	target, err = WriteTarget("")
	if err != nil {
		t.Fatal(err)
	}
	if target != envPath {
		t.Fatalf("target = %q, want TELECLI_CONFIG", target)
	}
}

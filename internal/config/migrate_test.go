package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The move of the settings file is covered here with both paths named by
// the test, so every platform runs it: the resolution rules above are
// what decide whether a move happens at all, and they are covered
// there.

// migrateFixture is a settings file in the old place and the place it
// is to be moved to.
type migrateFixture struct {
	old string
	new string
	dir string
}

func newMigrateFixture(t *testing.T) migrateFixture {
	t.Helper()

	dir := t.TempDir()
	fixture := migrateFixture{
		dir: dir,
		old: filepath.Join(dir, "old", "telecli", "config.toml"),
		new: filepath.Join(dir, "new", "telecli", "config.toml"),
	}

	writeConfig(t, fixture.old, fullSettingsBody)

	return fixture
}

func fixedClock() func() time.Time {
	return func() time.Time {
		return time.Date(2026, time.September, 29, 10, 30, 0, 0, time.UTC)
	}
}

// TestMoveTakesTheFileToTheNewPlace is the whole promise of the move:
// the copy holds the same settings, the old file is renamed and not
// removed, and the notice names where everything went.
func TestMoveTakesTheFileToTheNewPlace(t *testing.T) {
	fixture := newMigrateFixture(t)

	moved := migrateSettingsFile(fixture.old, fixture.new, MigrationOps{
		Now: fixedClock(),
	})
	if moved.Err != nil {
		t.Fatal(moved.Err)
	}

	// The copy is a configuration telecli loads with the same values,
	// not a file that merely has the same bytes. The old file is read
	// at its backup name, which is where the move left it.
	before, err := loadFile(firstBackup(t, fixture.old, "2026-09-29"))
	if err != nil {
		t.Fatal(err)
	}
	after, err := loadFile(fixture.new)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf(
			"the copy holds other settings:\n got %#v\nwant %#v",
			after,
			before,
		)
	}
	if after.TUI.Theme != "gruvbox-dark" || after.Auth.APIID != 123456 {
		t.Fatalf("the copy lost a setting: %#v", after)
	}

	wantBackup := fixture.old + ".moved-2026-09-29"
	if moved.Backup != wantBackup {
		t.Fatalf("backup = %q, want %q", moved.Backup, wantBackup)
	}
	if _, err := os.Stat(wantBackup); err != nil {
		t.Fatalf("the old file is not at its backup name: %v", err)
	}
	if _, err := os.Stat(fixture.old); !os.IsNotExist(err) {
		t.Fatal("the old file must not be left under its own name")
	}

	// The modes are the ones a settings file has everywhere else.
	assertFileMode(t, fixture.new, ConfigFileMode)
	assertDirMode(t, filepath.Dir(fixture.new), ConfigDirMode)

	notice := migrationNotice(moved)
	for _, want := range []string{fixture.old, fixture.new, wantBackup} {
		if !contains(notice, want) {
			t.Fatalf("notice %q does not name %q", notice, want)
		}
	}
	if contains(notice, "could not") {
		t.Fatalf("notice = %q, want the move as done", notice)
	}
}

// TestMoveKeepsTheOldFileWhenACopyFails pins the failure that a person
// runs into on a machine where ~/.config cannot be written: nothing
// changes, the old file is where it was, and there is no half-finished
// copy beside it.
func TestMoveKeepsTheOldFileWhenACopyFails(t *testing.T) {
	for name, ops := range map[string]MigrationOps{
		"the copy fails": {
			Copy: func(string, string) error {
				return errors.New("no room left on the device")
			},
		},
		"the copy is not writable": {
			Copy: copyToAReadOnlyDirectory,
		},
		"the copy holds other settings": {
			// The copy is replaced behind the move, which is what a
			// file system that answers a write with something else
			// would leave behind.
			Copy: substituteTheCopy,
		},
		"the old file cannot be renamed": {
			MoveAside: func(string, string) error {
				return errors.New("read-only file system")
			},
		},
		"the copy cannot be removed again": {
			Copy: func(string, string) error {
				return errors.New("no room left on the device")
			},
			Remove: func(string) error {
				return errors.New("and it stays there")
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newMigrateFixture(t)
			before := readFileBytes(t, fixture.old)

			moved := migrateSettingsFile(fixture.old, fixture.new, ops)
			if moved.Err == nil {
				t.Fatal("a failed step must be reported")
			}

			// The old file is untouched, byte for byte.
			if after := readFileBytes(t, fixture.old); !reflect.DeepEqual(
				before,
				after,
			) {
				t.Fatal("the old file was changed by a refused move")
			}

			// And nothing is left in the new place, so the next run
			// finds one file and not a pair.
			_, err := os.Stat(fixture.new)
			if !os.IsNotExist(err) {
				removeNote := "the copy is still there"
				if name == "the copy cannot be removed again" {
					removeNote = "the message must say how to remove it"
					if !contains(moved.Err.Error(), "by hand") {
						t.Fatalf("error = %v, %s", moved.Err, removeNote)
					}
				}
				t.Fatalf("%s: %v", removeNote, err)
			}

			notice := migrationNotice(moved)
			if !contains(notice, "could not move") {
				t.Fatalf("notice = %q, want the refused move", notice)
			}
			if !contains(notice, fixture.old) {
				t.Fatalf("notice %q does not name the old file", notice)
			}
		})
	}
}

// TestMoveIsDoneOnce pins that a second run changes nothing: the new
// file is there, so the old one is not looked at and not moved again.
func TestMoveIsDoneOnce(t *testing.T) {
	fixture := newMigrateFixture(t)

	first := migrateSettingsFile(fixture.old, fixture.new, MigrationOps{
		Now: fixedClock(),
	})
	if first.Err != nil {
		t.Fatal(first.Err)
	}

	// A second move has no file to take: the old path is empty, and
	// the resolution is what turns that into a run that does nothing.
	if _, err := os.Stat(fixture.old); !os.IsNotExist(err) {
		t.Fatalf("the old file is still under its own name: %v", err)
	}
	if _, err := loadFile(fixture.new); err != nil {
		t.Fatalf("the new file is not readable after the move: %v", err)
	}
}

// TestMoveOfTwoFilesOnOneDayKeepsBoth covers the name a second old file
// would take: the first backup is never overwritten.
func TestMoveOfTwoFilesOnOneDayKeepsBoth(t *testing.T) {
	fixture := newMigrateFixture(t)

	first := migrateSettingsFile(fixture.old, fixture.new, MigrationOps{
		Now: fixedClock(),
	})
	if first.Err != nil {
		t.Fatal(first.Err)
	}

	// A file appears at the old path again, as it would if a person put
	// a backup of their settings back.
	writeConfig(t, fixture.old, fullSettingsBody)
	if err := os.Remove(fixture.new); err != nil {
		t.Fatal(err)
	}

	second := migrateSettingsFile(fixture.old, fixture.new, MigrationOps{
		Now: fixedClock(),
	})
	if second.Err != nil {
		t.Fatal(second.Err)
	}
	if second.Backup == first.Backup {
		t.Fatalf("the second move reused the name %q", second.Backup)
	}
	if _, err := os.Stat(first.Backup); err != nil {
		t.Fatalf("the first backup is gone: %v", err)
	}
}

// TestResolvePathMovesARealOldFile ties the two halves together: the
// resolution on a platform that has an old place moves the file, and a
// second resolution does nothing.
func TestResolvePathMovesARealOldFile(t *testing.T) {
	isolateSettingsHome(t)
	requireSeparateOldPath(t)

	old := LegacyPath()
	current := mustDefaultPath(t)
	writeConfig(t, old, fullSettingsBody)

	resolved, err := ResolvePath("")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Path != current || resolved.Source != PathSourceDefault {
		t.Fatalf(
			"resolved = %q (%s), want the new file",
			resolved.Path,
			resolved.Source,
		)
	}
	if resolved.Notice == "" {
		t.Fatal("the move must be reported")
	}

	backup := old + ".moved-" +
		time.Now().Format(migrationDateLayout)
	if _, err := os.Stat(backup); err != nil {
		t.Fatalf("the old file was not kept as %q: %v", backup, err)
	}

	// A second run has nothing to say.
	again, err := ResolvePath("")
	if err != nil {
		t.Fatal(err)
	}
	if again.Notice != "" {
		t.Fatalf("a second run reported %q", again.Notice)
	}
	if again.Path != current {
		t.Fatalf("a second run read %q", again.Path)
	}
}

// firstBackup returns the name the old file keeps after a move dated
// with the given day.
func firstBackup(t *testing.T, old, day string) string {
	t.Helper()

	return old + ".moved-" + day
}

// copyToAReadOnlyDirectory writes the copy into a directory that cannot
// be written to, which is what a machine with a read-only home does.
func copyToAReadOnlyDirectory(legacyPath, newPath string) error {
	dir := filepath.Dir(newPath)
	if err := os.MkdirAll(dir, ConfigDirMode); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		return err
	}
	if os.Geteuid() == 0 {
		// A superuser is not stopped by the mode, so the case cannot
		// be produced here.
		os.Chmod(dir, ConfigDirMode)

		return errors.New("skipped: the suite runs as a superuser")
	}
	defer os.Chmod(dir, ConfigDirMode)

	return copySettingsFile(legacyPath, newPath)
}

// substituteTheCopy writes a copy that does not hold the settings of the
// old file, as an interrupted or misdirected write would.
func substituteTheCopy(legacyPath, newPath string) error {
	if err := copySettingsFile(legacyPath, newPath); err != nil {
		return err
	}

	body := strings.Replace(
		fullSettingsBody,
		`theme = "gruvbox-dark"`,
		`theme = "somebody-elses-theme"`,
		1,
	)

	return os.WriteFile(newPath, []byte(body), ConfigFileMode)
}

func assertFileMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("mode of %s = %o, want %o", path, got, want)
	}
}

func assertDirMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() {
		t.Fatalf("%s is not a directory", path)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("mode of %s = %o, want %o", path, got, want)
	}
}

func readFileBytes(t *testing.T, path string) []byte {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	return data
}

// TestTheMoveNeverTouchesAnotherFileSystem pins that the move keeps the
// file on the disk it is on: a settings file next to the data
// directories moves, and nothing else does.
func TestTheMoveNeverTouchesAnotherFileSystem(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("this test is about the file modes of a unix system")
	}

	fixture := newMigrateFixture(t)
	dataDir := filepath.Join(fixture.dir, "data", "tdlib", "database")
	if err := os.MkdirAll(dataDir, ConfigDirMode); err != nil {
		t.Fatal(err)
	}

	moved := migrateSettingsFile(fixture.old, fixture.new, MigrationOps{
		Now: fixedClock(),
	})
	if moved.Err != nil {
		t.Fatal(moved.Err)
	}

	if _, err := os.Stat(dataDir); err != nil {
		t.Fatalf("the TDLib data directory is gone: %v", err)
	}

	// The paths inside the file are the ones it had before: the move
	// takes the file, not the data it points at.
	cfg, err := loadFile(fixture.new)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TDLib.DatabaseDir != filepath.Join(
		"/tmp/telecli-settings-test/data/tdlib/database",
	) {
		t.Fatalf("database_dir = %q, want the path as it was", cfg.TDLib.DatabaseDir)
	}
}

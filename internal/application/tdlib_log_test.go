package application

import (
	"path/filepath"
	"strings"
	"testing"

	"telecli/internal/config"
	"telecli/internal/telegram"
)

// Where the journal of the Telegram library goes, and that the program
// says so out loud.
//
// The library writes an internal log to stderr on its own, and one of its
// lines appeared over a running interface on 03.10 (owner, task 5,
// review-tele5, SHA 6a77c6e). The journal is now a file this program
// names, and a file nobody can find is a file that has to stay silent.

// The runtime is told where the journal is before it sends anything, so
// the answer cannot depend on which path the doctor prints.
func TestTheRuntimeIsToldWhereTheLibraryJournalGoes(t *testing.T) {
	t.Parallel()

	dataDir := filepath.Join(t.TempDir(), "telecli")
	cfg := config.Default()
	cfg.DataDir = dataDir
	cfg.TDLib.LogVerbosity = 0

	runtimeConfig := telegramRuntimeConfig(cfg)

	if want := filepath.Join(dataDir, TDLibLogFileName); runtimeConfig.LogFilePath != want {
		t.Fatalf(
			"LogFilePath = %q, want %q: the library would keep writing "+
				"to the terminal the interface owns",
			runtimeConfig.LogFilePath,
			want,
		)
	}
	if runtimeConfig.LogVerbosity != 0 {
		t.Fatalf(
			"LogVerbosity = %d, want 0: the level of the journal is a "+
				"setting of the user, not a constant of the binding",
			runtimeConfig.LogVerbosity,
		)
	}
}

// A program with no data folder has no file for the journal, and the
// library is told to write nowhere rather than to keep the terminal.
func TestAProgramWithNoDataFolderHasNoJournalPath(t *testing.T) {
	t.Parallel()

	if path := TDLibLogPath(config.Config{}); path != "" {
		t.Fatalf("TDLibLogPath = %q, want no path", path)
	}
	if path := telegramRuntimeConfig(config.Config{}).LogFilePath; path != "" {
		t.Fatalf("LogFilePath = %q, want no path", path)
	}
}

// The journal lands beside the queue and not in the folder that holds the
// library's database and its downloaded files: nothing a user will ever
// open belongs among them.
func TestTheJournalLandsBesideTheQueue(t *testing.T) {
	t.Parallel()

	dataDir := filepath.Join(t.TempDir(), "telecli")
	cfg := config.Config{}
	cfg.MessageDelivery.DataDir = dataDir

	want := filepath.Join(dataDir, TDLibLogFileName)
	if got := TDLibLogPath(cfg); got != want {
		t.Fatalf("TDLibLogPath = %q, want %q", got, want)
	}
	if strings.Contains(TDLibLogFileName, string(filepath.Separator)) {
		t.Fatalf(
			"%q is not a name inside the data folder", TDLibLogFileName,
		)
	}
}

// telecli doctor says where the library's journal is, in the same place it
// says where its own reasons are: a user who has just watched an
// interface break wants both files.
func TestDoctorSaysWhereTheLibraryJournalIs(t *testing.T) {
	t.Parallel()

	var out strings.Builder
	cfg := config.Default()
	cfg.DataDir = "/tmp/example-telecli"
	writeUILogPath(&out, cfg)

	text := out.String()
	for _, want := range []string{
		filepath.Join("/tmp/example-telecli", TUILogFileName),
		filepath.Join("/tmp/example-telecli", TDLibLogFileName),
	} {
		if !strings.Contains(text, want) {
			t.Fatalf(
				"doctor printed %q, want the path %q in it",
				text, want,
			)
		}
	}

	// "nowhere" is an answer: with no data folder there is no file to
	// write to, and the line says what happened to the journal instead of
	// leaving the user to guess.
	empty := &strings.Builder{}
	writeUILogPath(empty, config.Config{})
	if !strings.Contains(empty.String(), "TDLib log: nowhere") {
		t.Fatalf(
			"doctor printed %q for a program with no data folder, want "+
				"it to say the library's journal goes nowhere",
			empty,
		)
	}
}

// Two journals in one folder that rolled over at different sizes would
// leave a user with two rules to remember, and no way to tell which file
// is the old one.
func TestTheTwoJournalsRollOverAtTheSameSize(t *testing.T) {
	t.Parallel()

	if tuiLogMaxBytes != telegram.TDLibLogMaxBytes {
		t.Fatalf(
			"the interface's reasons rotate at %d and the library's "+
				"journal at %d: one folder, two rules",
			tuiLogMaxBytes, telegram.TDLibLogMaxBytes,
		)
	}
}

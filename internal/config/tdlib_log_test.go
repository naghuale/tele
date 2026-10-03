package config

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The verbosity of the library's own journal is a setting of the user, and
// it has a ceiling that is not a matter of taste: from level 2 up TDLib
// writes every request it receives into that journal, and a request
// carries the api_hash of setTdlibParameters and the text of every
// message.

func TestTheDefaultJournalKeepsTheErrorsAndTheWarnings(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.TDLib.LogVerbosity != DefaultTDLibLogVerbosity {
		t.Fatalf(
			"log_verbosity = %d, want %d",
			cfg.TDLib.LogVerbosity,
			DefaultTDLibLogVerbosity,
		)
	}
}

// A configuration written before the setting existed has no log_verbosity
// line, and it must get the default rather than the zero value: an absent
// line is not a request for the quietest journal.
func TestAConfigurationWithoutTheSettingGetsTheDefault(t *testing.T) {
	path := journalConfigFile(t, "log_level = \"info\"\ndata_dir = \"/tmp/x\"\n")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.TDLib.LogVerbosity != DefaultTDLibLogVerbosity {
		t.Fatalf(
			"log_verbosity = %d, want the default %d",
			cfg.TDLib.LogVerbosity,
			DefaultTDLibLogVerbosity,
		)
	}
}

// Zero is written out as a line of its own, and it means what it says: the
// errors alone.
func TestAnExplicitQuietJournalIsAccepted(t *testing.T) {
	path := journalConfigFile(
		t,
		"log_level = \"info\"\ndata_dir = \"/tmp/x\"\n[tdlib]\nlog_verbosity = 0\n",
	)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.TDLib.LogVerbosity != 0 {
		t.Fatalf("log_verbosity = %d, want 0", cfg.TDLib.LogVerbosity)
	}
}

// The ceiling is refused, and the message says what is allowed and why: a
// user who asks for a louder journal deserves to know what that would put
// into a file on their disk.
func TestAVerbosityThatWouldWriteRequestsIntoTheJournalIsRefused(t *testing.T) {
	for _, level := range []int{2, 5, 99, -1} {
		path := journalConfigFile(
			t,
			"log_level = \"info\"\ndata_dir = \"/tmp/x\"\n"+
				"[tdlib]\nlog_verbosity = "+
				strconv.Itoa(level)+"\n",
		)

		_, err := Load(path)
		if err == nil {
			t.Fatalf("log_verbosity = %d was accepted", level)
		}
		if !strings.Contains(err.Error(), "tdlib.log_verbosity") {
			t.Fatalf("error = %v, want it to name the setting", err)
		}
		if !strings.Contains(err.Error(), "credentials") {
			t.Fatalf(
				"error = %v, want it to say what a louder journal would "+
					"write into the file", err,
			)
		}
	}
}

// journalConfigFile writes a configuration file in a scratch folder and
// returns its path. Nothing here may touch the developer's own
// configuration.
func journalConfigFile(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.toml")
	writeConfig(t, path, content)

	return path
}

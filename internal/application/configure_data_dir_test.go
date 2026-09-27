package application

import (
	"errors"
	"os"
	"testing"

	"telecli/internal/config"
)

// TestConfigureFailsWithoutHomeDirectory pins that setup refuses to
// write a relative data directory into the configuration file when no
// home directory is available, and leaves no credential profile behind.
func TestConfigureFailsWithoutHomeDirectory(t *testing.T) {
	t.Setenv("HOME", "")
	fixture := newConfigureFixture(t)

	_, err := fixture.run(t)
	if !errors.Is(err, config.ErrDataDirUnavailable) {
		t.Fatalf("error = %v, want config.ErrDataDirUnavailable", err)
	}
	if _, statErr := os.Stat(fixture.configPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("configuration file was written: %v", statErr)
	}
	if len(fixture.store.profiles) != 0 {
		t.Fatalf("credential profiles left behind: %v", fixture.store.profiles)
	}
}

// TestBuildConfiguredConfigKeepsOperatorDirectoriesWithoutHome pins that
// an unset built-in data directory never matches an operator's paths.
// An empty prefix matches every string, so those paths would otherwise
// be replaced.
func TestBuildConfiguredConfigKeepsOperatorDirectoriesWithoutHome(t *testing.T) {
	t.Setenv("HOME", "")

	existing := config.Default()
	existing.DataDir = "/srv/telecli"
	existing.TDLib.DatabaseDir = "/srv/tdlib-db"
	existing.TDLib.FilesDir = "/srv/tdlib-files"
	existing.MessageDelivery.DataDir = "/srv/outbox"

	next := buildConfiguredConfig(
		existing,
		TDLibProbeResult{},
		123456,
		"default",
		config.DefaultMessageSendMode,
	)

	for field, got := range map[string][2]string{
		"data_dir":                  {next.DataDir, "/srv/telecli"},
		"tdlib.database_dir":        {next.TDLib.DatabaseDir, "/srv/tdlib-db"},
		"tdlib.files_dir":           {next.TDLib.FilesDir, "/srv/tdlib-files"},
		"message_delivery.data_dir": {next.MessageDelivery.DataDir, "/srv/outbox"},
	} {
		if got[0] != got[1] {
			t.Fatalf("%s = %q, want %q", field, got[0], got[1])
		}
	}
}

// TestConfigureStoresTrimmedAPIHash pins that whitespace picked up from
// the clipboard around a valid hash is dropped before it is stored.
func TestConfigureStoresTrimmedAPIHash(t *testing.T) {
	const hash = "0123456789abcdef0123456789abcdef"
	fixture := newConfigureFixture(t)
	fixture.secrets.value = "  " + hash + "\t"

	if _, err := fixture.run(t); err != nil {
		t.Fatalf("configure: %v", err)
	}
	for name, profile := range fixture.store.profiles {
		if profile.APIHash != hash {
			t.Fatalf("profile %q stored API hash %q, want the trimmed hash", name, profile.APIHash)
		}
	}
	if len(fixture.store.profiles) == 0 {
		t.Fatal("no credential profile was stored")
	}
}

package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDefaultNeverUsesWorkingDirectory pins that an unavailable or
// relative home directory leaves the data directory unset instead of
// falling back to a path the working directory would resolve.
func TestDefaultNeverUsesWorkingDirectory(t *testing.T) {
	for name, home := range map[string]string{
		"unset home":    "",
		"relative home": "relative-home",
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("HOME", home)

			cfg := Default()
			for field, path := range map[string]string{
				"data_dir":           cfg.DataDir,
				"tdlib.database_dir": cfg.TDLib.DatabaseDir,
				"tdlib.files_dir":    cfg.TDLib.FilesDir,
			} {
				if path != "" {
					t.Fatalf("%s = %q, want unset", field, path)
				}
			}

			if _, err := Load(""); !errors.Is(err, ErrDataDirUnavailable) {
				t.Fatalf("Load error = %v, want ErrDataDirUnavailable", err)
			}
		})
	}
}

// TestExplicitDataDirWorksWithoutHome pins the way out: a configured
// absolute data_dir still works when there is no home directory, and the
// TDLib directories follow it.
func TestExplicitDataDirWorksWithoutHome(t *testing.T) {
	t.Setenv("HOME", "")
	dataDir := filepath.Join(t.TempDir(), "data")
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("data_dir = \""+dataDir+"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if want := filepath.Join(dataDir, "tdlib", "database"); cfg.TDLib.DatabaseDir != want {
		t.Fatalf("tdlib.database_dir = %q, want %q", cfg.TDLib.DatabaseDir, want)
	}
	if want := filepath.Join(dataDir, "tdlib", "files"); cfg.TDLib.FilesDir != want {
		t.Fatalf("tdlib.files_dir = %q, want %q", cfg.TDLib.FilesDir, want)
	}
}

// TestRelativeDirectoriesRejected pins that no stored directory may be
// relative: it would resolve from wherever telecli is started.
func TestRelativeDirectoriesRejected(t *testing.T) {
	for _, body := range []string{
		"data_dir = \"telecli-data\"\n",
		"[tdlib]\ndatabase_dir = \"db\"\n",
		"[tdlib]\nfiles_dir = \"files\"\n",
		"[message_delivery]\ndata_dir = \"outbox\"\n",
	} {
		path := filepath.Join(t.TempDir(), "config.toml")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := Load(path)
		if err == nil || !strings.Contains(err.Error(), "absolute") {
			t.Fatalf("Load(%q) error = %v, want an absolute-path error", body, err)
		}
	}
}

package application

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"telecli/internal/config"
)

// TestMain points every per-user location at a scratch directory for the
// whole package. A test that forgets its own isolation then writes into
// the scratch directory instead of the developer's real configuration.
func TestMain(m *testing.M) {
	os.Exit(runIsolated(m))
}

// isolatedHome is the home directory this run of the suite was given. It
// is kept so a test can prove the isolation is still in place.
var isolatedHome string

func runIsolated(m *testing.M) int {
	home, err := os.MkdirTemp("", "telecli-application-test-home-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "create isolated home:", err)
		return 1
	}
	defer os.RemoveAll(home)

	for key, value := range map[string]string{
		"HOME":            home,
		"XDG_CONFIG_HOME": home,
		"XDG_DATA_HOME":   home,
		"TELECLI_CONFIG":  "",
	} {
		if err := os.Setenv(key, value); err != nil {
			fmt.Fprintln(os.Stderr, "isolate", key+":", err)
			return 1
		}
	}

	isolatedHome = home

	return m.Run()
}

// TestTheSuiteRunsAgainstATemporaryHome is the guard on the isolation
// above.
//
// The commands under test resolve a settings file the way the real
// program does, so the directory they read it from comes from the
// environment. Without this isolation `telecli configure` would create a
// file in the home of whoever runs the suite, and `configure reset` would
// offer to remove it.
func TestTheSuiteRunsAgainstATemporaryHome(t *testing.T) {
	if isolatedHome == "" {
		t.Fatal("the package has no isolated home: TestMain did not run")
	}

	if !strings.HasPrefix(isolatedHome, os.TempDir()) {
		t.Fatalf(
			"the isolated home %q is not under %q",
			isolatedHome,
			os.TempDir(),
		)
	}

	for _, key := range []string{
		"HOME",
		"XDG_CONFIG_HOME",
		"XDG_DATA_HOME",
	} {
		if got := os.Getenv(key); got != isolatedHome {
			t.Fatalf("%s = %q, want the isolated home %q", key, got, isolatedHome)
		}
	}

	path, err := config.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(path, isolatedHome) {
		t.Fatalf("the settings path %q is outside the isolated home", path)
	}
	if legacy := config.LegacyPath(); legacy != "" &&
		!strings.HasPrefix(legacy, isolatedHome) {
		t.Fatalf("the old settings path %q is outside the isolated home", legacy)
	}

	// A file written by the suite goes into the scratch directory and
	// leaves nothing in a home that belongs to a person.
	marker := filepath.Join(isolatedHome, "marker")
	if err := os.WriteFile(marker, []byte("t"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
}

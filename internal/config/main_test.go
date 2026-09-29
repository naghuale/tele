package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	home, err := os.MkdirTemp("", "telecli-config-test-home-")
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
// Every test in this package reads and writes settings files, and the
// places it does that in are named by the environment. If the isolation
// ever stops holding, a test would read or write the home directory of
// whoever runs the suite, and the failure would look like a test that
// passes on one machine and not on another. So the claim is checked
// here, once, rather than trusted.
func TestTheSuiteRunsAgainstATemporaryHome(t *testing.T) {
	if isolatedHome == "" {
		t.Fatal("the package has no isolated home: TestMain did not run")
	}

	// The directory is a temporary one, so it lives under the temporary
	// directory of the process and not in a home anyone keeps files in.
	temp := os.TempDir()
	if !strings.HasPrefix(isolatedHome, temp) {
		t.Fatalf(
			"the isolated home %q is not under %q",
			isolatedHome,
			temp,
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

	// And the settings paths a test would touch are inside it: both the
	// new place under ~/.config and the old one os.UserConfigDir names.
	newPath, err := DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(newPath, isolatedHome) {
		t.Fatalf("the new settings path %q is outside the isolated home", newPath)
	}
	if oldPath := LegacyPath(); oldPath != "" &&
		!strings.HasPrefix(oldPath, isolatedHome) {
		t.Fatalf("the old settings path %q is outside the isolated home", oldPath)
	}

	// A file the suite creates lands in the scratch directory and goes
	// away with it, not in the home of the person running it.
	marker := filepath.Join(isolatedHome, "marker")
	if err := os.WriteFile(marker, []byte("t"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
}

package config

import (
	"fmt"
	"os"
	"testing"
)

// TestMain points every per-user location at a scratch directory for the
// whole package. A test that forgets its own isolation then writes into
// the scratch directory instead of the developer's real configuration.
func TestMain(m *testing.M) {
	os.Exit(runIsolated(m))
}

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
	return m.Run()
}

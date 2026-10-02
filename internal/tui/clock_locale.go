//go:build !darwin

package tui

import (
	"os"
	"os/exec"
	"strings"
)

// linuxTimeFormat returns the LC_TIME format of the locale in force, and
// whether there was one to read.
//
// LC_TIME is the variable the format of the time of day belongs to; LC_ALL
// overrides every category and LANG is what is left when neither is set. They
// are tried in that order because that is the order the C library applies
// them in, and this file is asking the machine the same question the machine
// asks itself.
func linuxTimeFormat() (string, bool) {
	for _, name := range []string{"LC_TIME", "LC_ALL", "LANG"} {
		locale := os.Getenv(name)
		if locale == "" {
			continue
		}

		format, ok := localeTimeFormat(locale)
		if ok {
			return format, true
		}
	}

	return "", false
}

// localeTimeFormat asks the C library what the time format of a locale is.
//
// `locale -k LC_TIME` prints one `name=value` per line, and the time of day
// is `t_fmt`. A locale that is not installed prints nothing for it and exits
// non-zero, which is not a failure of this program: it says the machine has
// no such locale, and the next variable is asked.
func localeTimeFormat(locale string) (string, bool) {
	cmd := exec.Command("locale", "-k", "LC_TIME")
	cmd.Env = append(os.Environ(), "LC_ALL="+locale, "LANGUAGE=")

	out, err := cmd.Output()
	if err != nil {
		return "", false
	}

	for _, line := range strings.Split(string(out), "\n") {
		name, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if !found || name != "t_fmt" {
			continue
		}

		return strings.Trim(strings.TrimSpace(value), `"`), true
	}

	return "", false
}

//go:build darwin

package tui

import (
	"os/exec"
	"strings"
)

// The machine's own answer on macOS, read once when the interface starts.
//
// The setting lives in the global preferences domain under two keys:
// AppleICUForce24HourTime and AppleICUForce12HourTime. Exactly one of them is
// set when the user has chosen, and neither is set when they have not — and
// then the format of the locale is the answer, which is what the system
// itself would use.
//
// The locale is read rather than guessed. A table of the countries that write
// the hour as twenty-four would be a table that is wrong about every locale
// somebody adds to it, and the one thing this file must not be is a second
// opinion about a machine the user has already configured: they have a
// setting for it, they set it once, and reading it is the whole of what this
// package has to do here.
//
// So: the keys first, and then `defaults read -g AppleLocale` and what that
// locale says. What the locale says is a table — there is no way round one —
// and it is a table of the *exceptions*, because the odd one out is what has
// to be named. The languages of Europe and of most of Asia write twenty-four
// hours; the languages of North America, of Australia and of the Arab world
// write twelve. A locale nobody has heard of is read as twelve, because that
// is what a reader of a name that means nothing to this table is most likely
// to have set themselves.
func readSystemClock() SystemSetting {
	if forced := readGlobalDefault("AppleICUForce24HourTime"); forced == "1" {
		return System24h
	}
	if forced := readGlobalDefault("AppleICUForce12HourTime"); forced == "1" {
		return System12h
	}

	locale := readGlobalDefault("AppleLocale")
	if locale == "" {
		return SystemUnknown
	}

	if writesTwentyFourHours(locale) {
		return System24h
	}

	return System12h
}

// readGlobalDefault reads one key of the global preferences domain, or ""
// when it is not set.
//
// `defaults read -g` exits non-zero for a key nobody has written, which is
// not a failure of this program: a key that is not set is the answer "the
// user has not chosen", and it is the case the two keys above are read for.
func readGlobalDefault(key string) string {
	out, err := exec.Command("defaults", "read", "-g", key).Output()
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(out))
}

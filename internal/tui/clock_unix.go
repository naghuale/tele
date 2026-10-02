//go:build !darwin

package tui

import "strings"

// The machine's own answer on a platform with no preferences domain to read.
//
// macOS has one key per answer and this file is the other half of the pair:
// anywhere else the answer is in the locale, and the locale is read where it
// is written down rather than in a table of countries (see clock_macos.go for
// why a territory table is the wrong shape).
//
// Linux names the locale in LC_TIME, with LC_ALL and LANG behind it, and the
// format of a locale is what says whether the hour is twelve or twenty-four:
// a locale whose time format has `%p` in it writes an AM or a PM after the
// hour, and a locale whose format has not is writing twenty-four hours. That
// is read from `locale -k LC_TIME` rather than from a table, for the reason
// the macOS file reads its keys rather than listing the countries: the user
// configured this machine, and this is where the configuration says what it
// says.
func readSystemClock() SystemSetting {
	format, ok := linuxTimeFormat()

	if !ok {
		return SystemUnknown
	}

	if containsDayPeriod(format) {
		return System12h
	}

	return System24h
}

// containsDayPeriod reports whether a locale's time format writes an AM or a
// PM, which is the mark of a twelve-hour clock.
//
// `%p` is the one that says it outright. `%r` is the locale's own twelve-hour
// time, which carries the period inside it — so a locale with `%r` and no
// `%p` is still a twelve-hour clock, and a table of only one of the two would
// get every locale whose time format is the short one wrong.
func containsDayPeriod(format string) bool {
	return strings.Contains(format, "%p") || strings.Contains(format, "%r")
}

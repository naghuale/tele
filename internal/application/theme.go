package application

import (
	"fmt"
	"io"
	"os"
	"strings"

	"telecli/internal/config"
	"telecli/internal/tui/theme"
)

// resolveInterfaceTheme turns the configured interface settings and the
// terminal into the theme the TUI is drawn with.
//
// The composition root does it, not a view, for two reasons. The profile
// is a measurement of the machine, and a test must be able to state it
// instead of inheriting whatever the machine happens to be. And the same
// settings have to resolve the same way for every command, or
// `telecli doctor` would report a theme the TUI then refuses to use.
//
// An unknown theme name or color mode is a configuration error with the
// valid values in it: an interface setting that is silently ignored is
// something a user finds out about from a screenshot.
func resolveInterfaceTheme(
	cfg config.Config,
	noColorFlag bool,
) (theme.Theme, theme.Profile, error) {
	return resolveInterfaceThemeFor(
		cfg,
		noColorFlag,
		environMap(),
		theme.DetectTerminalProfile(),
	)
}

// resolveInterfaceThemeFor is the testable form: the environment and the
// terminal measurement are inputs instead of the machine.
//
// The terminal is measured through the color library rather than by
// reading COLORTERM here: that is the library's job, and re-deciding it
// would let the theme and the renderer disagree about the terminal.
func resolveInterfaceThemeFor(
	cfg config.Config,
	noColorFlag bool,
	env map[string]string,
	terminal theme.TerminalProfile,
) (theme.Theme, theme.Profile, error) {
	selected, err := theme.ThemeFor(cfg.TUI.Theme)
	if err != nil {
		return theme.Theme{}, theme.ProfileNoColor, err
	}

	colorMode, err := theme.ParseColorMode(cfg.TUI.Color)
	if err != nil {
		return theme.Theme{}, theme.ProfileNoColor, err
	}

	profile := theme.ResolveProfile(theme.ProfileInput{
		Env:         env,
		FlagNoColor: noColorFlag,
		Configured:  colorMode,
		Terminal:    terminal,
	})

	return selected.ForProfile(profile), profile, nil
}

// environMap returns the process environment as the map the profile
// resolution reads.
//
// It is a separate function so a test can state an environment instead of
// setting one: NO_COLOR and TERM are read from here, and a test that has
// to deal with a machine exporting NO_COLOR cannot rely on unsetting it.
func environMap() map[string]string {
	environ := os.Environ()
	out := make(map[string]string, len(environ))

	for _, entry := range environ {
		if name, value, found := strings.Cut(entry, "="); found {
			out[name] = value
		}
	}

	return out
}

// writeInterfaceStatus reports the interface settings in telecli doctor.
//
// It is a report of state, so it prints the theme and the profile that
// were resolved rather than a promise: on a terminal whose capabilities
// nothing could measure, the profile is the one that was assumed.
func writeInterfaceStatus(
	out io.Writer,
	resolved theme.Theme,
	profile theme.Profile,
) {
	fmt.Fprintf(out, "Interface: %s\n", resolved.Name)
	fmt.Fprintf(
		out,
		"  Mode: %s, color profile: %s\n",
		resolved.Mode,
		profile,
	)
}

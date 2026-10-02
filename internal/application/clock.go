package application

import (
	"fmt"
	"io"

	"telecli/internal/config"
	"telecli/internal/tui"
)

// resolveInterfaceClock turns the configured setting into the format the
// interface writes the hour in.
//
// The composition root resolves it, not a view, for the same reason it
// resolves the theme and the width rule: the format is a property of the
// machine the program is running on, every command that starts an interface
// has to resolve it the same way, and `telecli doctor` reports it. A doctor
// that reported a clock the TUI then refused to use would be worse than one
// that said nothing.
//
// The machine is asked only where the configuration left the choice to it.
// A configured `12h` is the answer whatever the system preferences say: the
// setting exists for the case where the machine gets it wrong, and a setting
// that is overridden by the thing it was written for cannot fix that.
//
// A word that is not one of the three is a configuration error with the three
// in it, and it is reported by the command a user runs rather than at the
// next start of the TUI.
func resolveInterfaceClock(cfg config.Config) (tui.ClockFormat, error) {
	mode, err := tui.ParseClockMode(cfg.TUI.Clock)
	if err != nil {
		return tui.ClockFormat24h, err
	}

	return tui.ResolveSystemClock(mode), nil
}

// resolveSystemClock asks the machine and resolves the setting against
// what it says.
//
// It is a function of the two and not a call into the machine on its own,
// so that a caller which has already decided it is asking — or a test, which
// has no machine to ask — can be given both. `telecli tui` passes the
// configured setting and gets the machine's own answer through it; nothing
// in this package reads a preference domain directly.
func resolveSystemClock(configured tui.ClockMode) tui.ClockFormat {
	return tui.ResolveSystemClock(configured)
}

// writeClockStatus reports the clock in telecli doctor.
//
// It is a report of state, so it says what the screen is drawn with and
// whether that was configured or is the machine's own answer. The machine is
// not asked here for the reason it is not asked by the width rule: the doctor
// prints lines and leaves, and the reading of a machine's preferences is a
// question for the command that draws in it.
func writeClockStatus(out io.Writer, configured tui.ClockMode) {
	choice := tui.DescribeClock(configured)

	fmt.Fprintf(out, "Interface clock: %s\n", choice)
	if configured == tui.ClockAuto {
		fmt.Fprintf(
			out,
			"  auto: the setting of the machine is read when the TUI starts\n",
		)
	}
}

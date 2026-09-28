package application

import (
	"fmt"
	"io"

	"telecli/internal/config"
	"telecli/internal/tui/termwidth"
)

// resolveInterfaceWidth turns the configured width rule into the rule the
// interface is drawn with.
//
// The composition root does it, not a view, for the same reason it resolves
// the theme: the rule is a property of the machine the program is running
// on, and every command that starts an interface has to resolve it the
// same way. `telecli doctor` reports it, and a doctor that reported a rule
// the TUI then refused to use would be worse than one that said nothing.
//
// A word that is not one of the three is a configuration error with the
// three in it. The setting exists for the case where the measurement gets
// it wrong, and a setting that is quietly ignored is a setting that cannot
// fix the thing it was written for.
func resolveInterfaceWidth(cfg config.Config) (termwidth.Mode, error) {
	return termwidth.ParseMode(cfg.TUI.Width)
}

// writeWidthStatus reports the width rule in telecli doctor.
//
// It is a report of state, so it says where the rule came from and not
// what the interface is going to do about it: the doctor does not draw a
// frame, and a terminal is only measured by a program that is about to
// draw one. A configuration that left the choice to the terminal is
// reported as the rule it falls back to, with a note that the real one is
// decided when the TUI starts.
func writeWidthStatus(out io.Writer, configured termwidth.Mode) {
	choice := termwidth.Describe(configured)

	fmt.Fprintf(out, "Interface width: %s\n", choice)
	if configured == termwidth.ModeAuto {
		fmt.Fprintf(
			out,
			"  auto: the terminal is measured when the TUI starts\n",
		)
	}
}

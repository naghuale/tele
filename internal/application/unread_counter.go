package application

import (
	"fmt"
	"io"

	"telecli/internal/config"
	"telecli/internal/tui"
)

// resolveInterfaceUnreadCounter turns the configured word into what the
// counter of the chat list header counts.
//
// The composition root does it, not a view, for the reason it resolves the
// theme, the width rule and the clock: every command that starts an
// interface has to resolve the same settings the same way, and telecli
// doctor has to report the counter the TUI will draw with. A doctor that
// reported one setting and a screen that drew another is worse than a
// program that said nothing.
//
// A word that is not one of the three is a configuration error with the
// three in it: the setting was written to change what the header says, and
// a setting that is quietly ignored leaves the header saying the thing the
// reader wrote it to stop saying.
func resolveInterfaceUnreadCounter(
	cfg config.Config,
) (tui.UnreadCounterMode, error) {
	return tui.ParseUnreadCounterMode(cfg.TUI.UnreadCounter)
}

// writeUnreadCounterStatus reports what the number in the header of the chat
// list counts.
//
// It is a report of state and not a count: the doctor prints lines and
// leaves, and how much is unread right now is a question about an account it
// is not looking at. What it says about each mode is what the header leaves
// out, because that is the half a reader is most likely to be surprised by.
func writeUnreadCounterStatus(out io.Writer, configured tui.UnreadCounterMode) {
	fmt.Fprintf(
		out,
		"Interface unread counter: %s\n",
		tui.DescribeUnreadCounter(configured),
	)

	switch configured {
	case tui.UnreadCounterMessages:
		fmt.Fprint(out, "  messages: every unread message in the list, silenced chats included\n")
	case tui.UnreadCounterOff:
		fmt.Fprint(out, "  off: the badge of each chat stays, the header has no number\n")
	default:
		fmt.Fprint(out, "  chats: the chats with something unread, silenced chats left out\n")
	}
}

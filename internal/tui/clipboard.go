package tui

import (
	"encoding/base64"
	"strings"
)

// Copying is OSC 52: the terminal puts the text in the clipboard itself.
//
// It is the copy that works over ssh and in a browser tab, where a program
// cannot reach a clipboard of its own. The text goes to the terminal and
// nowhere else - not to a log, not to a file, not through a command somebody
// would have to trust with a message (§19).
//
// The sequence is written by hand rather than through a library because it is
// one escape sequence with a base64 payload, and the writer is the one the
// program hands the model: a test substitutes a buffer and reads what would
// have gone to the terminal.

// osc52Sequence returns the escape sequence that puts text in the
// clipboard of the terminal.
//
// The payload is base64, so a message with any character in it - quotes,
// newlines, a phone number pasted from a stranger - travels without
// breaking the sequence.
func osc52Sequence(text string) string {
	return "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\x07"
}

// osc52Prefix is what every OSC 52 sequence starts with, for a test that
// checks the shape of what was written without decoding it.
const osc52Prefix = "\x1b]52;c;"

// isOSC52 reports whether written is an OSC 52 copy sequence.
func isOSC52(written string) bool {
	return strings.HasPrefix(written, osc52Prefix)
}

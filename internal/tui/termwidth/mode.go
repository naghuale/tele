package termwidth

import (
	"fmt"
	"strings"
)

// Mode is how text is counted in terminal columns.
type Mode uint8

const (
	// ModeAuto measures the terminal and picks the rule that matches it.
	//
	// It is the default: a configuration that names no rule is a
	// configuration that has not decided, and the terminal is the only
	// party that knows.
	ModeAuto Mode = iota

	// ModeGrapheme counts a grapheme cluster as the Unicode emoji rules
	// say it is drawn: one column for a letter, two for a cluster with
	// emoji presentation, and two for a cluster of several code points
	// joined into one glyph.
	ModeGrapheme

	// ModeCodepoint adds up the width of every code point the way wcwidth
	// does.
	//
	// It is the rule the macOS Terminal and iTerm2 follow, and the one the
	// interface falls back to when nothing could be measured: a terminal
	// that says nothing is a terminal nobody has heard from, and the rule
	// that is wrong for a terminal with emoji support is the rule that
	// costs one column on a symbol, while the other way round shifts the
	// whole screen.
	ModeCodepoint
)

// modeNames are the words of the setting, in the order telecli doctor
// reports them.
var modeNames = [...]string{"auto", "grapheme", "codepoint"}

// String returns the name of the mode as it is written in the
// configuration and in telecli doctor.
func (m Mode) String() string {
	if int(m) < len(modeNames) {
		return modeNames[m]
	}

	return "unknown"
}

// ParseMode returns the mode a configured word names.
//
// An empty word is the default, because a configuration written before the
// setting existed has none. A word that is not one of the three is an
// error with the three in it: an interface setting that is quietly ignored
// is something a user finds out about from a screen that is drawn wrong.
func ParseMode(value string) (Mode, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "":
		return ModeAuto, nil
	case "auto":
		return ModeAuto, nil
	case "grapheme":
		return ModeGrapheme, nil
	case "codepoint":
		return ModeCodepoint, nil
	default:
		return ModeAuto, fmt.Errorf(
			"tui.width must be auto, grapheme or codepoint, not %q",
			value,
		)
	}
}

// Source is why a width mode is what it is, which is what telecli doctor
// has to report: a mode with no reason behind it is a promise, and this
// program only reports what it knows.
type Source uint8

const (
	// SourceDefault is the rule that was assumed: nothing was configured
	// and nothing answered.
	SourceDefault Source = iota

	// SourceConfigured is the rule the configuration file named.
	SourceConfigured

	// SourceMeasured is the rule the terminal's own answers chose.
	SourceMeasured
)

// String returns the word telecli doctor writes for the source.
func (s Source) String() string {
	switch s {
	case SourceDefault:
		return "default"
	case SourceConfigured:
		return "configured"
	case SourceMeasured:
		return "measured"
	default:
		return "unknown"
	}
}

// Choice is a width mode and the reason it was picked.
type Choice struct {
	Mode   Mode
	Source Source
}

// String returns the line telecli doctor prints: the mode, and whether it
// was measured, configured or assumed.
//
// The reason is in the same line as the mode because a user who reads it
// has one question — why is this what I am seeing — and the answer belongs
// where the question is asked.
func (c Choice) String() string {
	return c.Mode.String() + " (" + c.Source.String() + ")"
}

// Describe returns what a configured mode resolves to when nothing is
// measured.
//
// It is the form telecli doctor uses: the doctor is not the interface, it
// prints lines and leaves, and a measurement is a question that has to be
// asked of a terminal that is drawing frames. A configuration that says
// auto is reported as the rule auto would fall back to, with a note that
// the real one is decided when the TUI starts.
func Describe(configured Mode) Choice {
	if configured == ModeAuto {
		return Choice{Mode: ModeGrapheme, Source: SourceDefault}
	}

	return Choice{Mode: configured, Source: SourceConfigured}
}

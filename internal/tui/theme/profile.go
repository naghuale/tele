package theme

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// termenv is imported for the profile measurement in DetectTerminalProfile
// and for the values a termenv profile prints; the reduction of a hex to
// an entry of the 256-colour palette is no longer done here, because it is
// float work whose result two architectures can round differently.

// Profile is how much colour a terminal can show. It is the axis §2.7
// describes: a theme is built for one, and the same theme is usable on
// every other one through the fallbacks below.
type Profile uint8

const (
	// ProfileTrueColor is a terminal with 24-bit colour: the full
	// palette, the palette's gradients, and shadows.
	ProfileTrueColor Profile = iota

	// ProfileANSI256 is an indexed 256-colour terminal: the palette is
	// reduced to the closest indexed colour and a gradient is cut to at
	// most three steps, which is what a terminal ramp can show.
	ProfileANSI256

	// ProfileANSI16 is a terminal with the 16 basic colours: the roles
	// map to basic colours, so the terminal's own palette is used.
	// Gradients and shadows are dropped.
	ProfileANSI16

	// ProfileNoColor is a terminal that shows no colour at all. Every
	// colour becomes unset, and the interface has to stay readable
	// through symbols, attributes and words.
	ProfileNoColor
)

// String names the profile for logs and for `telecli doctor`.
func (p Profile) String() string {
	switch p {
	case ProfileTrueColor:
		return "true-color"
	case ProfileANSI256:
		return "ansi-256"
	case ProfileANSI16:
		return "ansi-16"
	case ProfileNoColor:
		return "no-color"
	default:
		return "unknown"
	}
}

// ProfileNames returns the profile names, in the order §2.7 lists them.
func ProfileNames() []string {
	return []string{
		ProfileTrueColor.String(),
		ProfileANSI256.String(),
		ProfileANSI16.String(),
		ProfileNoColor.String(),
	}
}

// maxANSI256GradientStops is how many stops an indexed ramp may have.
//
// A 256-colour terminal has a cube plus a grey ramp, and a gradient drawn
// from one cell to the next reads as a band of colour. Three stops is
// where the steps are still distinguishable instead of two flat blocks.
const maxANSI256GradientStops = 3

// ColorMode is the configured [tui] color setting.
type ColorMode string

const (
	// ColorAuto follows what the terminal reports, and stays conservative
	// when the terminal cannot be identified.
	ColorAuto ColorMode = "auto"

	// ColorAlways keeps colour whatever the environment suggests, and
	// assumes a 256-colour terminal when the terminal cannot be
	// identified. It cannot invent a capability, so a 16-colour terminal
	// still gets 16 colours.
	ColorAlways ColorMode = "always"

	// ColorNever is the user saying no colour, whatever the terminal can
	// do.
	ColorNever ColorMode = "never"
)

// ColorModeNames returns the accepted [tui] color values, sorted.
func ColorModeNames() []string {
	names := []string{
		string(ColorAuto),
		string(ColorAlways),
		string(ColorNever),
	}

	sort.Strings(names)

	return names
}

// ParseColorMode reads a configured color mode.
//
// An empty value is ColorAuto, because a configuration written before
// this setting existed has none. Anything else is an error that lists the
// values there are, because a silently ignored colour setting is a
// support question later.
func ParseColorMode(value string) (ColorMode, error) {
	normalized := ColorMode(strings.ToLower(strings.TrimSpace(value)))

	switch normalized {
	case "":
		return ColorAuto, nil
	case ColorAuto, ColorAlways, ColorNever:
		return normalized, nil
	default:
		return "", fmt.Errorf(
			"unknown tui color mode %q; valid values: %s",
			value,
			strings.Join(ColorModeNames(), ", "),
		)
	}
}

// TerminalProfile is what the color library reported about the terminal.
//
// It is separate from Profile because it is a measurement and Profile is
// a decision: the measurement is read from the machine once, and the
// decision is a pure function of the measurement, the environment, the
// command line and the configuration.
type TerminalProfile uint8

const (
	// TerminalUnknown means the library could not tell what the terminal
	// can do. The decision treats it as the least useful profile that
	// still shows colour.
	TerminalUnknown TerminalProfile = iota

	// TerminalTrueColor, TerminalANSI256 and TerminalANSI16 mirror the
	// three capabilities termenv reports.
	TerminalTrueColor
	TerminalANSI256
	TerminalANSI16

	// TerminalAscii mirrors the library's "no colour at all".
	TerminalAscii
)

// String names the measurement for logs.
func (t TerminalProfile) String() string {
	switch t {
	case TerminalUnknown:
		return "unknown"
	case TerminalTrueColor:
		return "true-color"
	case TerminalANSI256:
		return "ansi-256"
	case TerminalANSI16:
		return "ansi-16"
	case TerminalAscii:
		return "ascii"
	default:
		return "unknown"
	}
}

// ProfileInput is everything the profile decision depends on.
//
// It is a struct rather than a read of the environment so that the
// decision can be tested on any machine, including one whose terminal is
// something no CI runner has.
type ProfileInput struct {
	// Env is the environment to read. Only NO_COLOR and TERM are used.
	Env map[string]string

	// FlagNoColor is the --no-color command-line switch.
	FlagNoColor bool

	// Configured is the [tui] color setting. An empty value is auto.
	Configured ColorMode

	// Terminal is what the color library reported.
	Terminal TerminalProfile
}

// ResolveProfile decides which profile a theme is built for.
//
// Precedence, strongest first. The order follows
// https://no-color.org, which asks a user-level configuration file and a
// per-instance command line to override the NO_COLOR environment
// variable:
//
//  1. [tui] color = "never" and --no-color. Both are the user saying no
//     colour on this machine, and they win over everything: a
//     per-instance command-line argument is the most specific thing a user
//     can say.
//  2. TERM=dumb. A terminal that renders escape sequences as garbage gets
//     none, whatever was asked for. A user who set color = "always" in a
//     configuration file and runs under TERM=dumb is in something that is
//     not a terminal.
//  3. [tui] color = "always". Stronger than NO_COLOR, because it is a
//     configuration file and that is what the convention says. It never
//     invents a capability the terminal reported, and it assumes 256
//     colours when nothing could be measured or the color library
//     reported none: those are pipes, `script`, tmux with an unusual TERM
//     and the terminals of some IDEs, and a user asking for colour there
//     is right more often than the measurement is.
//  4. everything else, which is auto: NO_COLOR, then what the color
//     library reported. A library that reports no colour at all is
//     believed, because deciding again here would throw its decision away;
//     an unidentifiable terminal is assumed to be a 16-colour one.
func ResolveProfile(in ProfileInput) Profile {
	switch {
	case in.Configured == ColorNever:
		return ProfileNoColor

	case in.FlagNoColor:
		return ProfileNoColor

	case in.Env["TERM"] == "dumb":
		return ProfileNoColor

	case in.Configured == ColorAlways:
		return forcedProfile(in.Terminal)

	case hasNoColorEnvironment(in.Env):
		return ProfileNoColor
	}

	switch in.Terminal {
	case TerminalTrueColor:
		return ProfileTrueColor
	case TerminalANSI256:
		return ProfileANSI256
	case TerminalANSI16:
		return ProfileANSI16
	case TerminalAscii:
		return ProfileNoColor
	}

	// Nothing could be measured and nothing was requested, so the
	// conservative choice is the least useful profile that still shows
	// colour.
	return ProfileANSI16
}

// forcedProfile is the profile for a user who said the terminal is fine
// even though nothing could measure it.
//
// What the terminal reported is used as reported, including 16 colours: a
// terminal that said it has sixteen is believed, because asking for colour
// is not a request for a capability the terminal denied. A terminal that
// said nothing, or that the library found unable to show colour at all, is
// taken to be a 256-colour one.
func forcedProfile(terminal TerminalProfile) Profile {
	switch terminal {
	case TerminalTrueColor:
		return ProfileTrueColor
	case TerminalANSI256:
		return ProfileANSI256
	case TerminalANSI16:
		return ProfileANSI16
	}

	return ProfileANSI256
}

// hasNoColorEnvironment reports the NO_COLOR convention.
//
// https://no-color.org: any non-empty value disables colour. An empty
// value is the variable being present but saying nothing, which is not
// the same as asking for no colour.
func hasNoColorEnvironment(env map[string]string) bool {
	return env["NO_COLOR"] != ""
}

// DetectTerminalProfile reports what the terminal can show.
//
// The answer comes from Lip Gloss, the same library a renderer uses, so
// the profile the theme is built for cannot disagree with the one the
// renderer would produce on its own. The value is passed back into
// ResolveProfile, which keeps the decision itself free of the machine.
func DetectTerminalProfile() TerminalProfile {
	return TerminalProfileFromTermenv(lipgloss.ColorProfile())
}

// TerminalProfileFromTermenv converts the color library's answer.
func TerminalProfileFromTermenv(profile termenv.Profile) TerminalProfile {
	switch profile {
	case termenv.TrueColor:
		return TerminalTrueColor
	case termenv.ANSI256:
		return TerminalANSI256
	case termenv.ANSI:
		return TerminalANSI16
	case termenv.Ascii:
		return TerminalAscii
	default:
		return TerminalUnknown
	}
}

// Termenv converts a profile back to the color library's own value, so a
// renderer can hand it to Lip Gloss instead of a termenv literal.
func (p Profile) Termenv() termenv.Profile {
	switch p {
	case ProfileTrueColor:
		return termenv.TrueColor
	case ProfileANSI256:
		return termenv.ANSI256
	case ProfileANSI16:
		return termenv.ANSI
	default:
		return termenv.Ascii
	}
}

// ForProfile returns the theme as this profile can show it.
//
// The palette is left alone: it is what the user configured and what a
// fallback would want to go back to. The tokens, the gradients and the
// shadow are what change.
func (t Theme) ForProfile(profile Profile) Theme {
	out := t
	out.Tokens = t.Tokens.ForProfile(profile)
	out.Gradients = t.Gradients.ForProfile(profile)

	return out
}

// ForProfile returns the tokens as this profile can show them.
func (tokens Tokens) ForProfile(profile Profile) Tokens {
	switch profile {
	case ProfileTrueColor:
		return tokens

	case ProfileANSI256:
		// The entry of the 256-colour palette is one the palette names
		// next to its hex, and it is not worked out here. The conversion
		// from a hex is float work, two entries of a ramp can be the same
		// distance from a value, and the two architectures a runner can
		// be round that tie differently — which is not a question about
		// the interface but one a golden file has to have one answer to.
		out := tokens
		eachTokenColor(&out, indexedOf)

		return out

	case ProfileANSI16:
		// A basic colour is mapped by name too, and for the same reason
		// the palette names one next to each of its hexes: on a
		// 16-colour terminal the nearest RGB value is a colour the user
		// never chose, and the point of a basic colour is that the
		// terminal renders it with the palette the user configured.
		//
		// Backgrounds stay unset so the terminal's own background shows
		// through: a 16-colour terminal has no room for five shades of
		// surface, and a flat background is what the user expects.
		out := tokens
		eachTokenColor(&out, basicOf)
		clearBackgroundRoles(&out)

		return out

	default:
		// No colour at all. Every role becomes unset, and the interface
		// keeps its meaning through the focus indicator, the selection
		// attributes and the status marks.
		return Tokens{}
	}
}

// ForProfile returns the gradients as this profile can show them.
//
// True Color keeps them as authored: they are already short, and a
// longer ramp would be a decoration nobody can see anyway. An indexed
// terminal gets at most three stops. A basic-colour or uncoloured
// terminal gets none, which is also what a dumb terminal gets.
func (g Gradients) ForProfile(profile Profile) Gradients {
	switch profile {
	case ProfileTrueColor:
		return g

	case ProfileANSI256:
		return Gradients{
			Focus:    indexedGradient(g.Focus),
			Accent:   indexedGradient(g.Accent),
			Progress: indexedGradient(g.Progress),
		}

	default:
		return Gradients{}
	}
}

// indexedGradient reduces a ramp to indexed colours and to the number of
// stops an indexed terminal can show.
func indexedGradient(stops []Color) []Color {
	if len(stops) == 0 {
		return nil
	}

	limited := stops
	if len(limited) > maxANSI256GradientStops {
		limited = limited[:maxANSI256GradientStops]
	}

	out := make([]Color, 0, len(limited))
	for _, stop := range limited {
		out = append(out, indexedOf(stop))
	}

	return out
}

// indexedOf returns the entry of the 256-colour palette a colour is
// printed as on an indexed terminal.
//
// A colour that names no entry is left as it is, and the terminal reduces
// it itself. That is a fallback and not the path: every token of every
// built-in theme names its entry, and a test says so, so nothing the
// program draws is reduced by arithmetic the golden files cannot predict.
func indexedOf(c Color) Color {
	index, named := c.IndexedIndex()
	if !named {
		return c
	}

	return Indexed(index)
}

// basicOf returns the index of the basic palette a colour is printed as on
// a 16-colour terminal, and leaves a colour with no index alone.
func basicOf(c Color) Color {
	index, named := c.BasicIndex()
	if !named {
		return c
	}

	return Basic(index)
}

// clearBackgroundRoles leaves the surfaces unset on a terminal that has
// sixteen colours and no room for five shades of it.
//
// The terminal's own background shows through, which is what a user of a
// 16-colour terminal configured: they chose the colours their terminal
// has, and a background telecli picked for them is a background they did
// not choose.
func clearBackgroundRoles(tokens *Tokens) {
	tokens.AppBackground = Color{}
	tokens.SidebarBackground = Color{}
	tokens.ChatBackground = Color{}
	tokens.ComposerBackground = Color{}
	tokens.FooterBackground = Color{}
	tokens.PopupBackground = Color{}
	tokens.ShadowBackground = Color{}
	tokens.CodeBackground = Color{}
}

// eachTokenColor applies fn to every colour of every role.
//
// The theme contract test uses the same walk to prove that no role is
// left unset, which is why it is a function rather than 28 lines of
// repetition.
func eachTokenColor(tokens *Tokens, fn func(Color) Color) {
	*tokens = Tokens{
		AppBackground:      fn(tokens.AppBackground),
		SidebarBackground:  fn(tokens.SidebarBackground),
		ChatBackground:     fn(tokens.ChatBackground),
		ComposerBackground: fn(tokens.ComposerBackground),
		PopupBackground:    fn(tokens.PopupBackground),
		ShadowBackground:   fn(tokens.ShadowBackground),
		FooterBackground:   fn(tokens.FooterBackground),
		PrimaryText:        fn(tokens.PrimaryText),
		SecondaryText:      fn(tokens.SecondaryText),
		MutedText:          fn(tokens.MutedText),
		DisabledText:       fn(tokens.DisabledText),
		Focus:              fn(tokens.Focus),
		FocusAlt:           fn(tokens.FocusAlt),
		Selected:           fn(tokens.Selected),
		Unread:             fn(tokens.Unread),
		StatusInfo:         fn(tokens.StatusInfo),
		StatusActive:       fn(tokens.StatusActive),
		StatusSuccess:      fn(tokens.StatusSuccess),
		StatusWarning:      fn(tokens.StatusWarning),
		StatusError:        fn(tokens.StatusError),
		StatusUncertain:    fn(tokens.StatusUncertain),
		StatusCanceled:     fn(tokens.StatusCanceled),
		IncomingMessage:    fn(tokens.IncomingMessage),
		OutgoingMessage:    fn(tokens.OutgoingMessage),
		CodeBackground:     fn(tokens.CodeBackground),
		Cursor:             fn(tokens.Cursor),
		Selection:          fn(tokens.Selection),
	}
}

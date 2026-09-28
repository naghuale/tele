// Package theme holds the interface theme: a palette of raw colours, the
// semantic tokens the interface is drawn with, and the per-terminal
// fallbacks that keep every role distinguishable when a terminal cannot
// show colour.
//
// It follows docs/TUI_SPEC.md §2: components use the tokens and never a
// palette entry, which is what makes one theme replaceable with another
// without touching a component. The tokens are computed from the palette
// by one function, so a user palette (PR-10C) changes the interface
// without changing the code that draws it.
//
// The package is a leaf. It imports the color libraries and nothing of
// telecli, and it never changes Lip Gloss global state: the theme says
// what the interface should look like, and the renderer decides how to
// print it.
package theme

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ColorKind is how a colour reaches the terminal.
type ColorKind uint8

const (
	// ColorKindNone is the zero Color: no colour at all.
	//
	// A renderer must leave such text unstyled. Everything the interface
	// says must therefore survive without colour, which is what the
	// No Color profile relies on and what the focus indicator, the
	// selection attributes and the status marks are for.
	ColorKindNone ColorKind = iota

	// ColorKindRGB is a 24-bit colour written as "#rrggbb".
	ColorKindRGB

	// ColorKindBasic is one of the 16 ANSI colours.
	//
	// A basic colour is a palette index, not a value: the terminal
	// renders it with the colours the user configured for their
	// terminal, which is the point of using it on a 16-colour terminal.
	ColorKindBasic

	// ColorKindIndexed is one entry of the 256-colour palette.
	//
	// It is named rather than worked out, and the reason is in
	// CompleteColor: a hex is turned into an entry of the 256-colour
	// palette in floating point, two entries of a ramp can be the same
	// distance from a value, and the two architectures a CI runner can be
	// round that tie differently. A golden file has to be the same on
	// both, so a colour that is printed on a 256-colour terminal is
	// written down as the entry it is printed as.
	ColorKindIndexed
)

// String names the kind for logs and failure output.
func (k ColorKind) String() string {
	switch k {
	case ColorKindNone:
		return "none"
	case ColorKindRGB:
		return "rgb"
	case ColorKindBasic:
		return "basic"
	default:
		return "unknown"
	}
}

// Color is one colour of the interface.
//
// A colour that the interface prints on a terminal with colour carries
// all three of its values: the hex a 24-bit terminal shows, the entry of
// the 256-colour palette an indexed one shows, and the index of the
// basic palette a 16-colour one shows. The last two are written down
// rather than computed, because a value that is worked out in floating
// point can come out different on two machines and a golden file cannot
// be right on one of them. Profile() is what chooses between them.
//
// The zero Color means "no colour". That is a real value and not a
// missing one: it is what every token carries under the No Color profile,
// where the interface has to stay readable through symbols and
// attributes instead.
type Color struct {
	kind    ColorKind
	hex     string
	basic   uint8
	indexed uint8
}

// RGB returns an RGB colour from a "#rrggbb" string.
//
// The value is normalised to lower case. A string that is not a six
// digit hex colour yields the zero Color, so a mistyped literal in a
// preset becomes an unset token that the theme contract test reports,
// rather than an unrenderable colour that only shows up on screen.
func RGB(hex string) Color {
	normalized := strings.ToLower(strings.TrimSpace(hex))
	if !isHexColor(normalized) {
		return Color{}
	}

	return Color{kind: ColorKindRGB, hex: normalized}
}

// Basic returns one of the 16 ANSI colours by index.
//
// An index outside 0..15 yields the zero Color, for the same reason an
// unparsable hex string does.
func Basic(index uint8) Color {
	if index > 15 {
		return Color{}
	}

	return Color{kind: ColorKindBasic, basic: index}
}

// Complete returns a colour with its hex, its entry of the 256-colour
// palette and its index of the basic palette all written down.
//
// An indexed or a basic value of zero means "not named": a background
// that is left unset on a 16-colour terminal (§2.7) has no basic index
// because nothing ever prints it as a basic colour, and asking for one
// would be asking for a colour a user chose rather than a colour this
// palette happens to have.
func Complete(hex string, indexed, basic uint8) Color {
	color := RGB(hex)
	color.indexed = indexed
	color.basic = basic

	return color
}

// Indexed returns one entry of the 256-colour palette.
//
// It is a value on its own for a palette that is written in indexed
// entries rather than in hex, and it is what Profile hands back on an
// indexed terminal. The entry is a uint8 because 256 entries fit in one
// byte and because a value outside them cannot be written down as one.
func Indexed(index uint8) Color {
	return Color{kind: ColorKindIndexed, indexed: index}
}

// isHexColor reports whether value is "#" followed by six hex digits.
func isHexColor(value string) bool {
	if len(value) != 7 || value[0] != '#' {
		return false
	}

	for index := 1; index < len(value); index++ {
		switch character := value[index]; {
		case character >= '0' && character <= '9':
		case character >= 'a' && character <= 'f':
		default:
			return false
		}
	}

	return true
}

// Kind reports how the colour reaches the terminal.
func (c Color) Kind() ColorKind { return c.kind }

// IsSet reports whether the colour carries anything to render.
func (c Color) IsSet() bool { return c.kind != ColorKindNone }

// Hex returns the "#rrggbb" value, or "" for a colour that has none.
func (c Color) Hex() string {
	if c.kind != ColorKindRGB {
		return ""
	}
	return c.hex
}

// BasicIndex returns the index of the basic palette this colour is
// printed as on a 16-colour terminal, and whether there is one.
func (c Color) BasicIndex() (uint8, bool) {
	if c.basic == 0 {
		return 0, false
	}
	return c.basic, true
}

// IndexedIndex returns the entry of the 256-colour palette this colour is
// printed as on an indexed terminal, and whether there is one.
func (c Color) IndexedIndex() (uint8, bool) {
	if c.indexed == 0 {
		return 0, false
	}
	return c.indexed, true
}

// IsComplete reports whether the colour names all three of its values.
//
// It is what a test asks about every token of a built-in theme: a token
// without an indexed entry would be reduced to one at run time, and that
// reduction is float work whose result a golden file cannot depend on.
func (c Color) IsComplete() bool {
	return c.kind == ColorKindRGB && c.indexed != 0 && c.basic != 0
}

// Print returns the value a renderer is given for this colour: the hex on
// a terminal with 24 bits, the entry of the 256-colour palette on an
// indexed one, the index of the basic palette on a 16-colour one.
//
// It is a string because that is what the styling library takes, and it
// is why the values above are written down rather than derived: a library
// given a number prints that number, and a library given a hex works out
// which number to print, which is the work that cannot be done at run time.
func (c Color) Print() string {
	switch c.kind {
	case ColorKindRGB:
		return c.hex
	case ColorKindIndexed:
		return strconv.FormatUint(uint64(c.indexed), 10)
	case ColorKindBasic:
		return strconv.FormatUint(uint64(c.basic), 10)
	default:
		return ""
	}
}

// String renders the colour for logs and failure output.
//
// It is not a terminal escape: a log line that carried one would be
// unreadable in a file, and the renderer is what turns a colour into an
// escape sequence.
func (c Color) String() string {
	switch c.kind {
	case ColorKindRGB:
		return c.hex
	case ColorKindBasic:
		return fmt.Sprintf("basic:%d", c.basic)
	case ColorKindIndexed:
		return fmt.Sprintf("indexed:%d", c.indexed)
	default:
		return ""
	}
}

// MinimumTextContrast is the WCAG AA contrast ratio for body text.
//
// §24 of the specification asks for it, and it is the floor the built-in
// themes are held to for the two text roles a user actually reads.
const MinimumTextContrast = 4.5

// ContrastRatio returns the WCAG contrast ratio between two colours.
//
// 1 means the two are indistinguishable and 21 is black on white. A
// colour that is not set has no luminance, so the ratio with one is 0: an
// unset token cannot claim to be readable, and a test that asks must be
// told the truth instead of a flattering number.
func ContrastRatio(a Color, b Color) float64 {
	lighter, ok := relativeLuminance(a)
	if !ok {
		return 0
	}

	darker, ok := relativeLuminance(b)
	if !ok {
		return 0
	}

	if darker > lighter {
		lighter, darker = darker, lighter
	}

	return (lighter + 0.05) / (darker + 0.05)
}

// ContrastRatio returns the WCAG contrast ratio against other.
func (c Color) ContrastRatio(other Color) float64 {
	return ContrastRatio(c, other)
}

// relativeLuminance returns the WCAG relative luminance of a colour.
//
// Only an RGB colour has one: a basic ANSI colour is whatever the user's
// terminal decided it is, and this package cannot know that.
func relativeLuminance(c Color) (float64, bool) {
	if c.kind != ColorKindRGB {
		return 0, false
	}

	red := linearizeChannel(c.hex[1:3])
	green := linearizeChannel(c.hex[3:5])
	blue := linearizeChannel(c.hex[5:7])

	// The coefficients are the WCAG 2.1 ones for sRGB.
	luminance := 0.2126*red + 0.7152*green + 0.0722*blue

	return luminance, true
}

// linearizeChannel expands one hex channel from sRGB to linear light.
func linearizeChannel(channel string) float64 {
	value := 0.0
	for index := 0; index < len(channel); index++ {
		value = value*16 + float64(hexDigit(channel[index]))
	}

	scaled := value / 255

	// The piecewise transfer function of WCAG 2.1.
	if scaled <= 0.03928 {
		return scaled / 12.92
	}

	return math.Pow((scaled+0.055)/1.055, 2.4)
}

// hexDigit returns the value of one lower-case hex digit.
func hexDigit(character byte) int {
	switch {
	case character >= '0' && character <= '9':
		return int(character - '0')
	case character >= 'a' && character <= 'f':
		return int(character-'a') + 10
	default:
		return 0
	}
}

package theme

import (
	"fmt"
	"sort"
	"strings"
)

// ThemeMode is whether a theme is built for a dark or a light
// background.
type ThemeMode string

const (
	// ThemeModeDark is a dark theme. It is the only mode that ships in
	// this step: §2.6 asks for exactly three dark presets, and a light
	// theme needs a light palette as well as a light token mapping.
	ThemeModeDark ThemeMode = "dark"

	// ThemeModeLight is a light theme.
	ThemeModeLight ThemeMode = "light"
)

// Tokens are the semantic roles the interface is drawn with, the second
// level of §2.1. A component uses these and never a Palette field, which
// is what lets one theme replace another without touching a component.
//
// Every field is a role rather than a colour: PrimaryText is what body
// text is drawn in on AppBackground, not "the lightest colour of the
// palette". The field names follow §2.3.
type Tokens struct {
	AppBackground      Color
	SidebarBackground  Color
	ChatBackground     Color
	ComposerBackground Color
	PopupBackground    Color
	ShadowBackground   Color
	FooterBackground   Color

	PrimaryText   Color
	SecondaryText Color
	MutedText     Color
	DisabledText  Color

	Focus    Color
	FocusAlt Color
	Selected Color
	Unread   Color

	StatusInfo      Color
	StatusActive    Color
	StatusSuccess   Color
	StatusWarning   Color
	StatusError     Color
	StatusUncertain Color
	StatusCanceled  Color

	IncomingMessage Color
	OutgoingMessage Color
	CodeBackground  Color
	Cursor          Color
	Selection       Color
}

// Gradients are the optional decorative ramps of a theme.
//
// They are the one part of a theme a renderer may skip entirely: §2.1
// lists a gradient as an optional decoration, and the fallback profiles
// remove them where a terminal cannot show a smooth ramp.
type Gradients struct {
	Focus    []Color
	Accent   []Color
	Progress []Color
}

// Theme is one complete interface theme.
type Theme struct {
	Name      string
	Mode      ThemeMode
	Palette   Palette
	Tokens    Tokens
	Gradients Gradients
}

// Built-in theme names. The default is the one §2.6 lists first and the
// one the specification's own example is drawn in.
const (
	// ThemeCatppuccinMocha is the default theme.
	ThemeCatppuccinMocha = "catppuccin-mocha"

	// ThemeGruvboxDark is the warm, high-contrast dark theme.
	ThemeGruvboxDark = "gruvbox-dark"

	// ThemeTokyoNightStorm is the cool, low-contrast dark theme.
	ThemeTokyoNightStorm = "tokyo-night-storm"
)

// preset is the part of a built-in theme that is not computed.
type preset struct {
	Name    string
	Mode    ThemeMode
	Palette Palette
}

// presets are the built-in themes, in the order ThemeNames and the error
// for an unknown name report them.
//
// §2.6 asks for exactly three dark themes in this step and warns against
// shipping twelve at once: a dozen themes means a dozen contrast bugs to
// find and a dozen sets of mistakes to hide behind. The rest are PR-10C.
var presets = []preset{
	{Name: ThemeCatppuccinMocha, Mode: ThemeModeDark, Palette: catppuccinMochaPalette()},
	{Name: ThemeGruvboxDark, Mode: ThemeModeDark, Palette: gruvboxDarkPalette()},
	{Name: ThemeTokyoNightStorm, Mode: ThemeModeDark, Palette: tokyoNightStormPalette()},
}

// ThemeNames returns the built-in theme names, sorted.
//
// The order is alphabetical so that an error message and a help line
// read the same way every time.
func ThemeNames() []string {
	names := make([]string, 0, len(presets))
	for _, entry := range presets {
		names = append(names, entry.Name)
	}

	sort.Strings(names)

	return names
}

// DefaultTheme returns the built-in theme used when nothing is
// configured.
func DefaultTheme() Theme {
	return themeFor(ThemeCatppuccinMocha)
}

// ThemeFor returns the built-in theme with the given name.
//
// An empty name is the default theme, because a configuration written
// before this setting existed has none. An unknown name is an error that
// lists the names there are, since the alternative is a silent fallback
// to a theme the user did not ask for and would not notice.
func ThemeFor(name string) (Theme, error) {
	requested := strings.TrimSpace(name)
	if requested == "" {
		return DefaultTheme(), nil
	}

	for _, entry := range presets {
		if entry.Name == requested {
			return themeFor(entry.Name), nil
		}
	}

	return Theme{}, fmt.Errorf(
		"unknown theme %q; available themes: %s",
		name,
		strings.Join(ThemeNames(), ", "),
	)
}

// themeFor builds the theme of a known preset.
func themeFor(name string) Theme {
	for _, entry := range presets {
		if entry.Name != name {
			continue
		}

		return Theme{
			Name:      entry.Name,
			Mode:      entry.Mode,
			Palette:   entry.Palette,
			Tokens:    TokensFor(entry.Palette, entry.Mode),
			Gradients: GradientsFor(entry.Palette, entry.Mode),
		}
	}

	// Unreachable: the caller passes a preset name, and the list above is
	// the only source of them. A zero theme would be a silent "no
	// colours" interface, which is exactly the failure this package
	// exists to prevent, so it is worth a panic rather than a shrug.
	panic("tui theme: unknown preset " + name)
}

// TokensFor computes the semantic roles of a palette.
//
// This is the only place a palette becomes tokens, and it is a function
// rather than a table per preset for two reasons: a user palette
// (PR-10C) then works without touching this code, and a theme cannot
// disagree with itself about which of its own colours is the accent.
//
// The mapping:
//
//   - the surface roles take the palette ramp in order, from the base up,
//     so the interface gets its structure from the surfaces rather than
//     from borders (§3, "frames: absent");
//   - the text roles take the text ramp, with the two dimmest roles below
//     it: the built-in palettes have exactly one step too dim to read
//     comfortably, so muted and disabled text share it and the renderer
//     tells them apart with an attribute. Muted text is therefore dimmer
//     than secondary text in all three themes, which is not what the
//     Catppuccin subtext ramp does on its own;
//   - the focus, unread, cursor and outgoing roles take the accent, and
//     the second accent is the alternative focus and the uncertain status,
//     so uncertain does not look like failed (§24);
//   - the remaining status roles take the palette's status colours.
//
// The mode chooses where the ramp starts. A dark mode builds on the dark
// end of the palette and reads with the light end; a light mode does the
// opposite, which is why a light mode needs a light palette: pairing
// ThemeModeLight with a dark palette yields tokens the contrast test
// rejects, and no light palette ships in this step.
func TokensFor(palette Palette, mode ThemeMode) Tokens {
	dark := mode != ThemeModeLight

	// Accent is the theme's own emphasis colour and accentAlt its second
	// one. On a light background a pale accent disappears into it, so the
	// second accent leads instead.
	accent, accentAlt := palette.Accent, palette.AccentAlt
	if !dark {
		accent, accentAlt = accentAlt, accent
	}

	// Dim is the one step of every built-in palette that is too dark to
	// read comfortably. It stays the shadow, the cancelled status and the
	// disabled tier.
	dim := palette.Overlay0

	// The muted tier is the palette's own value and not a step of its
	// ramp: §24 holds a chat preview and a timestamp to WCAG AA, and
	// every preset names the step of its own ramp that clears the bar on
	// the surfaces they are drawn on. A value worked out at run time would
	// be a value a golden file could not be right about on two machines.
	muted := palette.Muted
	if !muted.IsSet() {
		// A palette written before the role existed — a user palette
		// (PR-10C) — has none, and dim is the dimmest step it has. The
		// contrast test says so rather than letting it pass.
		muted = dim
	}

	return Tokens{
		// Surfaces: the ramp in order, so the structure comes from the
		// backgrounds rather than from borders (§3, "frames: absent").
		AppBackground:      palette.Base,
		SidebarBackground:  palette.Mantle,
		ChatBackground:     palette.Crust,
		ComposerBackground: palette.Surface0,
		FooterBackground:   palette.Surface0,
		PopupBackground:    palette.Surface1,
		CodeBackground:     palette.Surface2,
		ShadowBackground:   dim,

		// Text: the text ramp for the two roles a user reads, and the
		// palette's own readable dim step for the two below them.
		PrimaryText:   palette.Text,
		SecondaryText: palette.Subtext0,
		MutedText:     muted,
		DisabledText:  dim,

		// Focus and the accent-coloured roles. The second accent is the
		// alternative focus, so uncertain does not look like failed
		// (§24).
		Focus:           accent,
		FocusAlt:        accentAlt,
		Selected:        palette.Surface1,
		Selection:       palette.Overlay1,
		Unread:          accent,
		Cursor:          accent,
		IncomingMessage: palette.Text,
		OutgoingMessage: accent,

		// Status: the palette's own status colours, with active on the
		// accent and uncertain on the second one.
		StatusInfo:      palette.Info,
		StatusActive:    accent,
		StatusSuccess:   palette.Success,
		StatusWarning:   palette.Warning,
		StatusError:     palette.Error,
		StatusUncertain: accentAlt,
		StatusCanceled:  dim,
	}
}

// GradientsFor derives the decorative ramps of a palette.
//
// They are derived rather than authored so that they cannot drift away
// from the palette they belong to, and they are short: two or three stops
// each, because a gradient in a terminal is a handful of cells at best.
func GradientsFor(palette Palette, mode ThemeMode) Gradients {
	dark := mode != ThemeModeLight

	primary, secondary := palette.Accent, palette.AccentAlt
	if !dark {
		primary, secondary = palette.AccentAlt, palette.Accent
	}

	return Gradients{
		Focus:    []Color{primary, secondary},
		Accent:   []Color{primary, secondary},
		Progress: []Color{palette.Success, palette.Warning, palette.Error},
	}
}

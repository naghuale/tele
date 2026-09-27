package theme

// Palette is a set of raw colours: the first level of the two levels in
// §2.1. It is what a preset supplies and what a user palette would
// supply, and it is never used by a component.
//
// The field names follow the three built-in palettes, which are all
// Catppuccin-shaped: a dark base with a mantle and crust below it, three
// surfaces above it, three overlays above those, and a text ramp at the
// light end. The names stay the same across themes so that a user who
// knows one palette knows the field names of all of them.
//
// The accent and status roles are the only ones a theme really varies:
// Catppuccin, Tokyo Night and Gruvbox each name their hues differently,
// and the mapping onto these fields is stated with each preset.
type Palette struct {
	Base   Color
	Mantle Color
	Crust  Color

	Surface0 Color
	Surface1 Color
	Surface2 Color

	Overlay0 Color
	Overlay1 Color
	Overlay2 Color

	Text     Color
	Subtext0 Color
	Subtext1 Color

	Accent    Color
	AccentAlt Color

	Success Color
	Warning Color
	Error   Color
	Info    Color

	Link    Color
	Mention Color
	Code    Color
}

// The three palettes below are the official ones, not approximations.
// Each block says which official roles its fields come from, so a reader
// can check a value against the upstream source without guessing, and a
// later edit knows what it has to keep in step.
//
// Measured against §24 (WCAG AA, 4.5:1 for body text) on the four
// backgrounds the tokens use for text, the worst ratio in each palette is
// 8.3:1 for PrimaryText and 5.3:1 for SecondaryText. The two dimmest
// tiers are below 4.5:1 by design and carry no information that is not in
// the text itself; see TokensFor.

// catppuccinMochaPalette is Catppuccin Mocha.
//
// Source: https://catppuccin.com/palette and the mocha.json of
// github.com/catppuccin/catppuccin, the flavour telecli ships by default.
// The twenty values are the official ones, taken from the named roles:
// base, mantle, crust, surface0..2, overlay0..2, text, subtext0, subtext1,
// mauve and blue for the two accents, green, yellow, red and teal for the
// status roles, and blue, pink and peach for link, mention and code.
func catppuccinMochaPalette() Palette {
	return Palette{
		Base:      RGB("#1e1e2e"),
		Mantle:    RGB("#181825"),
		Crust:     RGB("#11111b"),
		Surface0:  RGB("#313244"),
		Surface1:  RGB("#45475a"),
		Surface2:  RGB("#585b70"),
		Overlay0:  RGB("#6c7086"),
		Overlay1:  RGB("#7f849c"),
		Overlay2:  RGB("#9399b2"),
		Text:      RGB("#cdd6f4"),
		Subtext0:  RGB("#a6adc8"),
		Subtext1:  RGB("#bac2de"),
		Accent:    RGB("#cba6f7"),
		AccentAlt: RGB("#89b4fa"),
		Success:   RGB("#a6e3a1"),
		Warning:   RGB("#f9e2af"),
		Error:     RGB("#f38ba8"),
		Info:      RGB("#89dceb"),
		Link:      RGB("#89b4fa"),
		Mention:   RGB("#f5c2e7"),
		Code:      RGB("#fab387"),
	}
}

// tokyoNightStormPalette is the Storm variant of Tokyo Night.
//
// Source: github.com/enkia/tokyonight, the tokyo-night-storm theme. Its
// backgrounds are bg, bg_dark and black; the surface ramp continues into
// the palette's darkest neutrals, bg_highlight, fg_gutter and
// terminal_black, which are the only greys it has; overlay0 is comment,
// and overlay1 and overlay2 are fg_highlight and fg_dark. The text ramp
// is fg, fg_dark and comment, the accents are blue and magenta, and the
// status roles are green, yellow, red and cyan.
//
// Tokyo Night has no fifth text step, so subtext1 is comment, the same
// value as overlay0, and muted text is dimmer than the theme's own
// secondary text rather than brighter, which is the opposite of what
// Catppuccin's subtext0 and subtext1 do.
func tokyoNightStormPalette() Palette {
	return Palette{
		Base:      RGB("#24283b"),
		Mantle:    RGB("#1a1b26"),
		Crust:     RGB("#15161e"),
		Surface0:  RGB("#292e42"),
		Surface1:  RGB("#3b4261"),
		Surface2:  RGB("#414868"),
		Overlay0:  RGB("#565f89"),
		Overlay1:  RGB("#7aa2f7"),
		Overlay2:  RGB("#a9b1d6"),
		Text:      RGB("#c0caf5"),
		Subtext0:  RGB("#a9b1d6"),
		Subtext1:  RGB("#565f89"),
		Accent:    RGB("#7aa2f7"),
		AccentAlt: RGB("#bb9af7"),
		Success:   RGB("#9ece6a"),
		Warning:   RGB("#e0af68"),
		Error:     RGB("#f7768e"),
		Info:      RGB("#7dcfff"),
		Link:      RGB("#7aa2f7"),
		Mention:   RGB("#bb9af7"),
		Code:      RGB("#ff9e64"),
	}
}

// gruvboxDarkPalette is Gruvbox Dark.
//
// Source: github.com/morhetz/gruvbox, gruvbox.pal of the dark theme. The
// background ramp is bg0, bg0_soft and bg0_hard; the surface ramp is
// bg1, bg2 and bg3; the overlay ramp is bg4, fg_gutter and fg_dim; the
// text ramp is fg, fg_dim and fg_gutter, in that order of prominence;
// yellow is the accent because it is the one Gruvbox is known for, purple
// is the second accent, and the status roles are green, orange, red and
// blue.
//
// Gruvbox's four text steps are one short of a five-step ramp, so
// subtext1 shares fg_gutter with overlay1 and muted text takes overlay0
// (bg4), the one step below the text ramp.
func gruvboxDarkPalette() Palette {
	return Palette{
		Base:      RGB("#282828"),
		Mantle:    RGB("#32302f"),
		Crust:     RGB("#1d2021"),
		Surface0:  RGB("#3c3836"),
		Surface1:  RGB("#504945"),
		Surface2:  RGB("#665c54"),
		Overlay0:  RGB("#7c6f64"),
		Overlay1:  RGB("#928374"),
		Overlay2:  RGB("#bdae93"),
		Text:      RGB("#ebdbb2"),
		Subtext0:  RGB("#bdae93"),
		Subtext1:  RGB("#928374"),
		Accent:    RGB("#fabd2f"),
		AccentAlt: RGB("#d3869b"),
		Success:   RGB("#b8bb26"),
		Warning:   RGB("#fe8019"),
		Error:     RGB("#fb4934"),
		Info:      RGB("#83a598"),
		Link:      RGB("#83a598"),
		Mention:   RGB("#d3869b"),
		Code:      RGB("#8ec07c"),
	}
}

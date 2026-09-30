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

	// Muted is the step the chat previews and the timestamps are drawn
	// in.
	//
	// It is a value of its own and not Overlay0 because none of the three
	// palettes has a step that is both dim enough to sit below Subtext0
	// and readable: §24 holds a preview to WCAG AA, and a timestamp
	// nobody can read is not a timestamp. Each preset names the value
	// rather than having it computed, because a colour that is worked out
	// at run time is a colour that can come out different on two
	// machines, and a golden file cannot be right on one of them.
	Muted Color

	// OutgoingBlock is the surface the block of a message of this user is
	// drawn on: the theme's own step of the surface ramp, mixed a little
	// towards the accent.
	//
	// The other side's messages are drawn on Surface0, which is a neutral
	// grey, and this one is a tint of it. A tint is what tells the two
	// sides apart in colour as well as in position, which is what a reader
	// of a chat already expects: their own messages are the ones in the
	// colour of the theme.
	//
	// The share is small, and the reason is the text on it. The words of a
	// message of this user are the accent, which is the lightest thing on
	// the screen, and a surface mixed towards the accent is a surface the
	// accent has less contrast on: at a tenth of the distance the words
	// of a message are already at 4.47:1 on Tokyo Night and below the
	// 4.5:1 §24 asks for. Every value here is written down rather than
	// computed, and TestTheMessageBubblesAreMixesOfTheSurfaceAndTheAccent
	// says so, because a value worked out at run time is a value that can
	// come out different on two machines.
	OutgoingBlock Color

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
		Base:     Complete("#1e1e2e", 232, 0),
		Mantle:   Complete("#181825", 232, 0),
		Crust:    Complete("#11111b", 232, 0),
		Surface0: Complete("#313244", 59, 0),
		// surface1 is the surface of the selected row and the popup, and
		// the only role of it that survives a 16-colour terminal is the
		// name on the selected row.
		Surface1: Complete("#45475a", 59, 15),
		Surface2: Complete("#585b70", 59, 0),
		Overlay0: Complete("#6c7086", 60, 8),
		Overlay1: Complete("#7f849c", 103, 8),
		Overlay2: Complete("#9399b2", 103, 7),
		Text:     Complete("#cdd6f4", 189, 15),
		Subtext0: Complete("#a6adc8", 146, 7),
		Subtext1: Complete("#bac2de", 146, 7),

		// overlay0 lifted towards the text ramp until it reads on the
		// mantle and the crust: 4.75:1 and 5.07:1.
		Muted: Complete("#7e839b", 102, 8),
		// Surface0 mixed three fifteenths of the way to mauve, which is
		// as far as the words of a message of this user go: the words
		// ARE the accent, and the accent on the block is 4.61:1 here, a
		// step over the 4.5:1 of §24. A sixth of the way would be a
		// clearer difference between the two blocks and would put the
		// words at 4.14:1, so the two of them together are what fixes
		// this value. The block and Surface0 are 1.34:1 apart, which is
		// the "clearly a different block" the owner asked for on
		// 30.09 — the eighth of a way it was before was 1.17:1, and the
		// two sides looked alike. Entry 61 is two steps above the 59 of
		// Surface0 and carries the mauve, so the two blocks are told
		// apart on an indexed terminal too.
		OutgoingBlock: Complete("#48435f", 61, 8),
		Accent:        Complete("#cba6f7", 183, 14),
		AccentAlt:     Complete("#89b4fa", 111, 12),
		Success:       Complete("#a6e3a1", 151, 2),
		Warning:       Complete("#f9e2af", 223, 3),
		Error:         Complete("#f38ba8", 211, 1),
		Info:          Complete("#89dceb", 117, 6),
		Link:          Complete("#89b4fa", 111, 6),
		Mention:       Complete("#f5c2e7", 218, 5),
		Code:          Complete("#fab387", 216, 3),
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
		Base:     Complete("#24283b", 17, 0),
		Mantle:   Complete("#1a1b26", 232, 0),
		Crust:    Complete("#15161e", 232, 0),
		Surface0: Complete("#292e42", 17, 0),
		Surface1: Complete("#3b4261", 59, 15),
		Surface2: Complete("#414868", 59, 0),
		Overlay0: Complete("#565f89", 60, 8),
		Overlay1: Complete("#7aa2f7", 111, 8),
		Overlay2: Complete("#a9b1d6", 146, 7),
		Text:     Complete("#c0caf5", 153, 15),
		Subtext0: Complete("#a9b1d6", 146, 7),
		Subtext1: Complete("#565f89", 60, 7),

		// comment lifted towards fg until it reads on the mantle and the
		// crust: 4.66:1 and 4.90:1.
		Muted: Complete("#7e87b2", 103, 8),
		// A twentieth of the way to blue, and half as far again as the
		// other two: this accent is the darkest of the three, so the
		// words of a message have the least room on any tint of the
		// surface under them. 4.89:1 for the words and 4.66:1 for a
		// failed send, where a tenth of the distance would leave the red
		// at 4.26:1.
		// Nine hundredths of the way to the blue, and the blue of this
		// theme is a dark one: the words of a message of this user are
		// the accent, and the accent of tokyo-night is the darkest of the
		// three, so this is as far as the block can go and still hold
		// them at 4.5:1 — a tenth of the way puts them at 4.47:1. The
		// block and Surface0 are 1.15:1 apart, which is the least of the
		// three themes and the most this palette can say: telling its two
		// blocks apart further means giving the words of this user a
		// lighter colour, and that is a decision about the whole ramp of
		// the theme rather than about this one value. The owner is asked
		// to look at it (30.09).
		OutgoingBlock: Complete("#2f3850", 18, 8),
		Accent:        Complete("#7aa2f7", 111, 14),
		AccentAlt:     Complete("#bb9af7", 141, 12),
		Success:       Complete("#9ece6a", 149, 2),
		Warning:       Complete("#e0af68", 179, 3),
		Error:         Complete("#f7768e", 210, 1),
		Info:          Complete("#7dcfff", 117, 6),
		Link:          Complete("#7aa2f7", 111, 6),
		Mention:       Complete("#bb9af7", 141, 5),
		Code:          Complete("#ff9e64", 215, 3),
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
		Base:     Complete("#282828", 232, 0),
		Mantle:   Complete("#32302f", 232, 0),
		Crust:    Complete("#1d2021", 232, 0),
		Surface0: Complete("#3c3836", 59, 0),
		Surface1: Complete("#504945", 59, 15),
		Surface2: Complete("#665c54", 59, 0),
		Overlay0: Complete("#7c6f64", 95, 8),
		Overlay1: Complete("#928374", 102, 8),
		Overlay2: Complete("#bdae93", 144, 7),
		Text:     Complete("#ebdbb2", 223, 15),
		Subtext0: Complete("#bdae93", 144, 7),
		Subtext1: Complete("#928374", 102, 7),

		// bg4 lifted towards fg until it reads on the mantle and the
		// crust: 5.13:1 and 6.02:1.
		Muted: Complete("#a69881", 138, 8),
		// An eighth of the way to yellow, and the same distance as
		// Catppuccin because the yellow is light enough for the words to
		// have room: 5.75:1 where the other two are at 5.31 and 4.89.
		//
		// This is the one theme whose state words are already below the
		// bar on the neutral surface — 3.37:1 for the error red on
		// Surface0 — and a tint takes that to 2.83:1. It is a real cost
		// of a tint in a palette with a light accent, and it is paid for
		// knowingly: the words of the message itself, which are what
		// §24 holds to the bar, are at 5.75:1.
		// Just under a fifth of the way to the yellow: the words of a
		// message of this user are the accent, and 4.63:1 is what is
		// left of the bar at that share. The block and Surface0 are
		// 1.48:1 apart, the widest of the three themes, because the
		// yellow of this palette is the lightest of the three accents
		// and the block can go further towards it than the others can.
		OutgoingBlock: Complete("#5e5035", 242, 8),
		Accent:        Complete("#fabd2f", 214, 14),
		AccentAlt:     Complete("#d3869b", 174, 12),
		Success:       Complete("#b8bb26", 142, 2),
		Warning:       Complete("#fe8019", 208, 3),
		Error:         Complete("#fb4934", 203, 1),
		Info:          Complete("#83a598", 108, 6),
		Link:          Complete("#83a598", 108, 6),
		Mention:       Complete("#d3869b", 174, 5),
		Code:          Complete("#8ec07c", 108, 3),
	}
}

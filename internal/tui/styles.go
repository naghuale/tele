package tui

import (
	"os"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"telecli/internal/tui/theme"
)

// The interface is drawn through its own Lip Gloss renderer.
//
// The global renderer decides its own colour profile from the process and
// its output, which would throw away everything the composition root
// resolved: a `color = "always"` in a configuration file, or a NO_COLOR
// the user set for a program they did not want to shout, would be
// overruled by whether stdout happens to be a terminal. A private
// renderer whose profile is given to it keeps that decision in one place
// and leaves the global one alone for any library that still uses it.
func newRenderer(profile theme.Profile) *lipgloss.Renderer {
	// The profile is passed to the output rather than set afterwards, so
	// the renderer never probes the terminal at all: a measurement here
	// would be a second opinion about something already decided.
	renderer := lipgloss.NewRenderer(
		os.Stdout,
		termenv.WithProfile(profile.Termenv()),
	)
	renderer.SetColorProfile(profile.Termenv())

	return renderer
}

// themeRegion is a surface of the screen and the accent that marks it.
//
// The pairs come from the theme's tokens: each region is a background of
// the palette with an accent drawn on its left edge. Naming them here
// keeps "which token is this" in one place instead of spread over the
// views.
type themeRegion struct {
	background theme.Color
	accent     theme.Color
}

// viewStyles holds everything a view needs to draw the screen.
//
// It is built once per model from the theme already degraded to the
// profile, so a pane never has to know how a colour is printed, and a
// colour is never chosen outside the theme.
type viewStyles struct {
	renderer *lipgloss.Renderer
	theme    theme.Theme

	list         themeRegion
	conversation themeRegion
	composer     themeRegion
	footer       themeRegion
}

// newViewStyles binds a renderer to a theme.
//
// The regions come from the theme rather than from a table of variables,
// because the colours of a table would be frozen at init time from
// whichever theme was constructed first.
func newViewStyles(
	renderer *lipgloss.Renderer,
	resolved theme.Theme,
) viewStyles {
	tokens := resolved.Tokens

	return viewStyles{
		renderer: renderer,
		theme:    resolved,
		list: themeRegion{
			background: tokens.SidebarBackground,
			accent:     tokens.Focus,
		},
		conversation: themeRegion{
			background: tokens.ChatBackground,
			accent:     tokens.Focus,
		},
		composer: themeRegion{
			background: tokens.ComposerBackground,
			accent:     tokens.Cursor,
		},
		footer: themeRegion{
			background: tokens.FooterBackground,
			accent:     tokens.SecondaryText,
		},
	}
}

// region returns the style of one focusable region of the screen.
//
// A focused region carries the accent line of §5.1: a left border one
// column wide, drawn in the region's accent. An unfocused region reserves
// the same column with a margin, so moving the focus changes no cell to
// its right. That is also what keeps the composer from shifting when the
// focus moves between the chat list and the timeline.
//
// textWidth is the width of the text, and the rendered block is that plus
// the focus column and the inset. Lip Gloss is told the width of the
// content box, padding included, so it wraps a line at exactly the width
// the lines were fitted to: a line that wrapped inside the style would
// push the region's own height past what the layout budgeted for it.
//
// The border glyph is the theme's own focus marker, which is the `▌` of
// §2.7 and of every mock screen in the specification. A left border drawn
// with a different character would mean two symbols for one thing: one in
// colour and another without it.
func (s viewStyles) region(
	region themeRegion,
	focused bool,
	textWidth int,
) lipgloss.Style {
	style := s.renderer.NewStyle().
		PaddingLeft(contentInsetWidth).
		Width(maxInt(textWidth+contentInsetWidth, 1))

	if region.background.Kind() == theme.ColorKindRGB {
		style = style.Background(
			lipgloss.Color(region.background.Hex()),
		)
	}

	if focused && region.accent.Kind() == theme.ColorKindRGB {
		return style.
			BorderLeft(true).
			BorderStyle(lipgloss.Border{Left: theme.FocusBar}).
			BorderForeground(lipgloss.Color(region.accent.Hex()))
	}

	if !focused {
		style = style.MarginLeft(1)
	}

	return style
}

// text returns the style for a run of text in a colour.
//
// A colour that the profile cannot show yields a plain style on purpose:
// under a basic or uncoloured profile the meaning is carried by a symbol
// or an attribute, and painting a default colour would undo that.
func (s viewStyles) text(color theme.Color) lipgloss.Style {
	if color.Kind() != theme.ColorKindRGB {
		return s.renderer.NewStyle()
	}

	return s.renderer.NewStyle().
		Foreground(lipgloss.Color(color.Hex()))
}

// dimmed returns the style for de-emphasized text: faint where the
// terminal can dim, plain where it cannot.
//
// A dimmer colour is the primary way of de-emphasizing text, and a
// terminal that shows no colour is the one case where it cannot work, so
// the faint attribute is the fallback rather than the mechanism.
func (s viewStyles) dimmed(color theme.Color) lipgloss.Style {
	style := s.text(color)
	if color.Kind() != theme.ColorKindRGB {
		return style
	}

	return style.Faint(true)
}

// selected returns the style of the selected row of a list.
//
// The selection is an attribute and not a colour: it has to survive the
// no-colour profile, where it is the only thing that says which chat is
// selected.
func (s viewStyles) selected(selectedRow bool) lipgloss.Style {
	attributes := s.theme.SelectionAttributes(selectedRow)

	return s.renderer.NewStyle().
		Bold(attributes.Bold).
		Reverse(attributes.Reverse)
}

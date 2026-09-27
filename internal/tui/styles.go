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
// The bar is drawn whether or not the profile can show colour. It is a
// character, not a colour: the accent decides how bright it is and the
// glyph decides that it is there, and a terminal that shows no colour is
// exactly the terminal where the glyph is all there is (§2.7). The
// earlier version drew the bar only for an RGB accent and left a focused
// region with no marker and no reserved column at all under the no-colour
// profile, which is the profile of every NO_COLOR, --no-color, TERM=dumb
// and `color = "never"` run.
//
// textWidth is the width of the text, and the rendered block is that plus
// the focus column and the inset. Lip Gloss is told the width of the
// content box, padding included, so it wraps a line at exactly the width
// the lines were fitted to: a line that wrapped inside the style would
// push the region's own height past what the layout budgeted for it.
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

	if !focused {
		return style.MarginLeft(focusColumnWidth)
	}

	style = style.
		BorderLeft(true).
		BorderStyle(lipgloss.Border{Left: theme.FocusBar})

	if region.accent.Kind() == theme.ColorKindRGB {
		style = style.BorderForeground(lipgloss.Color(region.accent.Hex()))
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

// selectionMark is the marker in front of a selected row or message.
//
// It is the accent of the region while the region has the focus and a
// dimmer mark of the same shape when the focus is elsewhere, which is what
// keeps the chat that is open visible in the list beside it (§4.1) and the
// message the cursor is on visible in a timeline the keys have left (§5.2).
//
// The attributes come from the theme and the colour from the tokens, and
// neither is what makes the selection visible: the glyph is. A terminal
// that prints no attributes and no colours still shows the mark, and a
// terminal that prints both shows it in the accent.
func (s viewStyles) selectionMark(selected, regionFocused bool) lipgloss.Style {
	style := s.dimmed(s.theme.Tokens.SecondaryText)
	if !selected {
		return style
	}

	if regionFocused {
		style = s.text(s.theme.Tokens.Focus)
	}

	return style.Bold(true).Reverse(true)
}

// author is the style of the word that stands for the sender of a message.
//
// The outgoing name is in the accent (§4.4), and the name of the message
// the cursor is on is the selected colour so that it is the one name in the
// column that is brighter than the rest.
func (s viewStyles) author(outgoing, selected bool) lipgloss.Style {
	if selected {
		return s.text(s.theme.Tokens.Selected)
	}
	if outgoing {
		return s.text(s.theme.Tokens.OutgoingMessage)
	}

	return s.text(s.theme.Tokens.SecondaryText)
}

// body is the style of the text of a message.
//
// The two sides are told apart by colour and by indent (§4.4): the accent
// of this user's own messages against the ordinary text of the other side.
func (s viewStyles) body(outgoing bool) lipgloss.Style {
	if outgoing {
		return s.text(s.theme.Tokens.OutgoingMessage)
	}

	return s.text(s.theme.Tokens.IncomingMessage)
}

// rowText is the colour of the text of a row: the selected colour for the
// row a user is acting on, the ordinary one for the rest (§4.2).
func (s viewStyles) rowText(selected bool) lipgloss.Style {
	if selected {
		return s.text(s.theme.Tokens.Selected)
	}

	return s.text(s.theme.Tokens.PrimaryText)
}

// cursor is the style of the cursor of the composer.
//
// It is reverse where the terminal prints attributes and the cursor colour
// where it does not, and the glyph it sits on or the bar it becomes is what
// makes it visible in both. §4.5 asks for a bright cursor, and brightness
// is the one thing a terminal without colour cannot do — so the bar, not the
// colour, is the part that has to work everywhere.
func (s viewStyles) cursor(focused bool) lipgloss.Style {
	if focused {
		return s.renderer.NewStyle().
			Foreground(lipgloss.Color(s.theme.Tokens.Cursor.Hex())).
			Reverse(true)
	}

	return s.dimmed(s.theme.Tokens.Cursor)
}

// litPlaceholder is the placeholder after a blank Enter (§7.3).
//
// It is a change of style and nothing else: the same words in the accent of
// the composer, for a moment, and no timer anywhere near the screen.
func (s viewStyles) litPlaceholder() lipgloss.Style {
	if s.theme.Tokens.Cursor.Kind() != theme.ColorKindRGB {
		return s.renderer.NewStyle().Bold(true).Reverse(true)
	}

	return s.renderer.NewStyle().
		Foreground(lipgloss.Color(s.theme.Tokens.Cursor.Hex())).
		Bold(true)
}

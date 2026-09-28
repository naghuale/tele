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

// raised returns the style of a block that sits on the surface below it.
//
// The background is the one of the block and not of the row it is drawn in:
// a raised band the width of a pane says that the whole pane belongs to the
// block, and a user reads that as something larger than the two sentences
// inside it (§24).
//
// A profile that cannot show a colour gets the foreground alone, which is
// the same reason every other style degrades to its text: the words carry
// the meaning and the colour only repeats it.
func (s viewStyles) raised(foreground, background theme.Color) lipgloss.Style {
	if foreground.Kind() != theme.ColorKindRGB ||
		background.Kind() != theme.ColorKindRGB {
		return s.text(foreground)
	}

	return s.renderer.NewStyle().
		Foreground(lipgloss.Color(foreground.Hex())).
		Background(lipgloss.Color(background.Hex()))
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

// matchRun is the style of the part of a chat title a search matched
// (§9: "match fragment подсвечивается").
//
// It is the accent of the theme in bold, which is what the interface marks
// things with everywhere else, rather than a second colour on the row: a
// match has to be visible on a row that is already in the selection's
// colour, and the accent is the one thing that differs from both the
// ordinary text and the selected text.
//
// Under the no-colour profile this style is empty, and the search is
// still a search: §2.7 asks for a meaning that does not depend on
// colour, and a list a query has narrowed is short without any highlight
// in it at all.
func (s viewStyles) matchRun() lipgloss.Style {
	return s.text(s.theme.Tokens.Focus).Bold(true)
}

// attributesVisible reports whether the profile can print bold, reverse
// and the rest of the SGR attributes.
//
// The Ascii profile cannot: termenv answers Ascii for NO_COLOR, for
// `color = "never"`, for TERM=dumb and for --no-color, and Lip Gloss
// strips every attribute under it. Anything a view marks with an attribute
// alone is invisible there, so this is the question a view asks before
// trusting one.
func (s viewStyles) attributesVisible() bool {
	return s.renderer.ColorProfile() != termenv.Ascii
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

// popupItem is one line of a menu: a raised background, the accent line of
// the focused region, and the marker of the selected item where the profile
// has no attributes to carry it.
//
// There is no frame and no second accent line. §5 allows one accent line
// per screen and §24 asks for a raised background instead of a border, and
// a menu is a region like any other.
func (s viewStyles) popupItem(
	selected bool,
	tokens theme.Tokens,
) lipgloss.Style {
	return s.popupBase(tokens).
		Bold(selected).
		Foreground(lipgloss.Color(popupForegroundColor(tokens, selected)))
}

// popupTitle is the question of §12.3, and popupBody the two lines under
// it. Both are the popup's own surface: a question in the colour of the
// screen behind it would be a question the user has to find.
func (s viewStyles) popupTitle(tokens theme.Tokens) lipgloss.Style {
	return s.popupBase(tokens).
		Bold(true).
		Foreground(lipgloss.Color(tokens.PrimaryText.Hex()))
}

func (s viewStyles) popupBody(tokens theme.Tokens) lipgloss.Style {
	return s.popupBase(tokens).
		Foreground(lipgloss.Color(tokens.SecondaryText.Hex()))
}

// popupBase is the surface and the padding every popup line shares.
func (s viewStyles) popupBase(tokens theme.Tokens) lipgloss.Style {
	// The focus line of a popup is a character and not a Lip Gloss border,
	// so it survives the Ascii profile that strips them; see
	// popupFocusBar, which draws it.
	style := s.renderer.NewStyle()

	if tokens.PopupBackground.Kind() != theme.ColorKindRGB {
		return style
	}

	return style.
		Background(lipgloss.Color(tokens.PopupBackground.Hex())).
		BorderForeground(lipgloss.Color(tokens.Focus.Hex()))
}

// popupFocusBar is the accent line of a popup, drawn as a character.
func (s viewStyles) popupFocusBar(tokens theme.Tokens) string {
	return s.dimmed(tokens.Focus).Render(theme.FocusBar)
}

// popupShadow is the column of the surface below the popup that makes it
// read as above the conversation.
//
// Where the profile has no colour to make a shadow with, the popup is set
// off by an indent instead: a menu that starts in the first column looks
// like a line of the conversation, and a user who reads it that way answers
// the wrong question.
func (s viewStyles) popupShadow(tokens theme.Tokens, columns int) string {
	if tokens.ShadowBackground.Kind() != theme.ColorKindRGB {
		return spaces(columns)
	}

	return s.renderer.NewStyle().
		Background(lipgloss.Color(tokens.ShadowBackground.Hex())).
		Render(spaces(columns))
}

// popupForegroundColor is the colour of an item of a menu.
//
// A selected item is in the accent of the theme and an unselected one in
// its own text colour, so the selection is a colour as well as a marker
// where there are attributes, and only a marker where there are none.
func popupForegroundColor(tokens theme.Tokens, selected bool) string {
	if selected {
		return tokens.Focus.Hex()
	}

	return tokens.PrimaryText.Hex()
}

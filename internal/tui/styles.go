package tui

import (
	"os"
	"strings"

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

// themeRegion is a surface of the screen and the token it is filled with.
//
// The pairs come from the theme's tokens: each region is a background of
// the palette. Naming them here keeps "which token is this" in one place
// instead of spread over the views.
type themeRegion struct {
	background theme.Color
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
		},
		conversation: themeRegion{
			background: tokens.ChatBackground,
		},
		composer: themeRegion{
			background: tokens.ComposerBackground,
		},
		footer: themeRegion{
			background: tokens.FooterBackground,
		},
	}
}

// region returns the style of one focusable region of the screen.
//
// A region is a surface and nothing else. The focus is not drawn here: it
// is one accent line under the header of the panel that has the keys
// (focusRule), and a line of focus down the side of every row of a list is
// a line the eye reads as a column of text rather than as where the keys
// are. Every region therefore reserves the same column at its left edge
// whether or not it is focused, so the focus moving between the list and
// the conversation changes no cell to the right of it.
//
// Lip Gloss is told the colours of a region and not its width. It measures
// width with the grapheme rule — the rule a terminal following wcwidth
// disagrees with — and a style with a width wraps what it renders to it: a
// row the model fitted to the pane in the codepoint rule comes out one
// column "too wide" to the style, so the style wraps it, and the second
// line of a chat title appears under the first.
//
// Every line is fitted to the width of the region before it gets here, in
// the rule the terminal was measured for, and the background covers
// exactly those lines.
func (s viewStyles) region(region themeRegion) lipgloss.Style {
	style := s.renderer.NewStyle().
		MarginLeft(focusColumnWidth).
		PaddingLeft(contentInsetWidth)

	if region.background.Kind() == theme.ColorKindRGB {
		style = style.Background(
			lipgloss.Color(region.background.Hex()),
		)
	}

	return style
}

// focusRule is the one line that says which panel has the keys.
//
// It is a full-width rule in the accent of the theme, drawn under the
// header of the focused panel and not anywhere else. A line of focus along
// the side of a list and along the side of a timeline is read as a column
// of the list, and in a list of chats it is read as the first letter of
// every title; a rule under the header is one line, in one place, and
// cannot be mistaken for anything else on the screen.
//
// The unfocused panel gets a blank line of the same height, so moving the
// focus moves a rule and nothing else. It is a character and not a colour,
// so it survives the profile that has no colour at all (§2.7).
func (s viewStyles) focusRule(width int, focused bool) string {
	if !focused {
		return spaces(maxInt(width, 1))
	}

	return s.text(s.theme.Tokens.Focus).
		Render(strings.Repeat(focusRuleGlyph, maxInt(width, 1)))
}

// focusRuleGlyph is the character the rule under a header is drawn with.
//
// It is a heavy horizontal — a rule, and a heavy one so that it is the
// strongest line on the screen — rather than the block bar of §5.1, which
// was a marker for a row of the list and would read as a border.
const focusRuleGlyph = "━"

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

// selected is the style of the selected row of a list.
//
// The selection is the background of the row, the token the specification
// calls Selected, and the name on it in the accent. It is a surface and not
// a marker: a `›` in front of the name is read as the first letter of the
// name by half the people who see it, and a filled square in the same
// place is read as a character of the chat before it. The rows below and
// beside it are the same width either way, so nothing moves when the
// selection does.
//
// The no-colour profile has no background to show, and there the marker
// comes back in its own column with the row in bold — §2.7 asks the
// selection to survive a terminal that prints nothing, and a row that is
// only a colour is not a selection.
func (s viewStyles) selected(selectedRow bool) lipgloss.Style {
	if !selectedRow {
		return s.renderer.NewStyle()
	}

	if s.theme.Tokens.Selected.Kind() != theme.ColorKindRGB {
		return s.renderer.NewStyle().Bold(true)
	}

	return s.renderer.NewStyle().
		Background(lipgloss.Color(s.theme.Tokens.Selected.Hex()))
}

// selectionMark is the marker in front of a selected row or message where
// the profile cannot show a background.
//
// It is drawn in a column of its own, in front of the name, and never
// anywhere else: a list whose rows and whose messages both carry it is a
// list in which the marker says nothing about what it marks.
func (s viewStyles) selectionMark(selected, regionFocused bool) lipgloss.Style {
	style := s.dimmed(s.theme.Tokens.SecondaryText)
	if !selected {
		return style
	}

	if regionFocused {
		style = s.text(s.theme.Tokens.Focus)
	}

	return style.Bold(true)
}

// selectionMarker returns the glyph in front of a selected row, and a
// blank of the same width where nothing is selected.
//
// It is a glyph in the colour profile that has a background and in the
// profile that has not: with a background the row says it is selected, and
// a glyph on top of it says it twice. Under no colour there is no
// background, so the glyph is the only thing left that can say it.
func (s viewStyles) selectionMarker(selected bool) string {
	if selected && s.theme.Tokens.Selected.Kind() != theme.ColorKindRGB {
		return theme.SelectionMark
	}

	return theme.SelectionNone
}

// authorColor returns the colour of one author's name in a conversation
// with several people in it.
//
// It is six colours out of the theme's own roles — the two accents and
// three of the status hues, plus the step the selection marker uses — and
// it is picked by the hash of the author's identifier, so the same person
// is the same colour in every message, in every chat and after a restart.
// Six are more than a group usually has going at once and fewer than a
// chat list has chats, so two people can share one — and the name beside
// it is what tells them apart, not the colour.
func (s viewStyles) authorColor(authorID int64) lipgloss.Style {
	palette := []theme.Color{
		s.theme.Tokens.Focus,
		s.theme.Tokens.FocusAlt,
		s.theme.Tokens.StatusSuccess,
		s.theme.Tokens.StatusInfo,
		s.theme.Tokens.StatusWarning,
		s.theme.Tokens.Selection,
	}

	if len(palette) == 0 {
		return s.text(s.theme.Tokens.Focus)
	}

	return s.text(palette[authorColorIndex(authorID, len(palette))])
}

// authorColorIndex is which of the author colours an identifier gets.
func authorColorIndex(authorID int64, count int) int {
	if count < 1 {
		return 0
	}

	return int(authorHash(authorID) % uint64(count))
}

// authorHash is a small non-negative hash of a sender identifier.
//
// It is FNV-1a rather than a modulo of the identifier itself because
// Telegram hands out user identifiers in ascending order, and a colour
// chosen by "identifier mod six" walks the six colours in turn and puts
// three neighbours of a group in the same one.
func authorHash(authorID int64) uint64 {
	const (
		offset = 14695981039346656037
		prime  = 1099511628211
	)

	hash := uint64(offset)
	value := uint64(authorID)

	for shift := 0; shift < 64; shift += 8 {
		hash ^= value >> shift
		hash *= prime
	}

	return hash
}

// bubble is the block a message of this user is drawn in: the text on the
// composer's own surface, inset by a column on each side.
//
// It is a raised block and not a frame (§1, §24), and it is the surface of
// the composer rather than a new colour, so a message of this user and the
// field it was written in are visibly the same thing.
func (s viewStyles) bubble() lipgloss.Style {
	if s.theme.Tokens.ComposerBackground.Kind() != theme.ColorKindRGB {
		return s.renderer.NewStyle()
	}

	return s.renderer.NewStyle().
		Background(lipgloss.Color(s.theme.Tokens.ComposerBackground.Hex()))
}

// pill is the badge of an unread count: a count on a background of its
// own, in the colour the surface is behind it.
//
// The text is the background it sits on rather than the app's darkest
// colour, so a badge over a list is a badge over that list — the words
// inside it are the words of the surface they are on, and the pill is the
// one thing in the row that is not a word.
func (s viewStyles) pill(background theme.Color, surface theme.Color) lipgloss.Style {
	if background.Kind() != theme.ColorKindRGB || surface.Kind() != theme.ColorKindRGB {
		return s.renderer.NewStyle().Bold(true)
	}

	return s.renderer.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(surface.Hex())).
		Background(lipgloss.Color(background.Hex()))
}

// body is the style of the text of a message.
//
// The two sides are told apart by the side they are on and by the surface
// behind them (§4.4): a message of this user is in the bubble on the
// right, and the other side's is on the chat background on the left.
func (s viewStyles) body(outgoing bool) lipgloss.Style {
	if outgoing {
		return s.text(s.theme.Tokens.OutgoingMessage)
	}

	return s.text(s.theme.Tokens.IncomingMessage)
}

// rowText is the colour of the name of a chat row.
//
// The selected row is in the accent, and so is the row the cursor is on —
// §4.2 makes the selected chat the one with the accent title, and the two
// are the same row.
func (s viewStyles) rowText(selected bool) lipgloss.Style {
	if selected {
		return s.text(s.theme.Tokens.Focus).Bold(true)
	}

	return s.text(s.theme.Tokens.PrimaryText).Bold(true)
}

// matchRun is the style of the part of a chat title a search matched
// (§9: "match fragment подсвечивается").
//
// It is the second accent of the theme in bold, and not the first: a
// selected row is in the first accent, so a match inside it would be the
// same colour as the name around it and would stop saying that the query
// found something there.
//
// Under the no-colour profile this style is empty, and the search is
// still a search: §2.7 asks for a meaning that does not depend on
// colour, and a list a query has narrowed is short without any highlight
// in it at all.
func (s viewStyles) matchRun() lipgloss.Style {
	return s.text(s.theme.Tokens.FocusAlt).Bold(true)
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

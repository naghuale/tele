package theme

import "strings"

// Everything in this file is what the interface has instead of colour.
//
// §14 requires the interface not to depend on colour, and §2.7 says what
// carries the meaning when colour is gone: a symbol for the focused
// region, reverse or bold for the selected one, and a symbol plus words
// for a status. These are theme roles rather than view helpers, so the
// guarantee is stated once, here, and the renderer has nothing to
// remember.

// FocusIndicator is the block bar drawn on the focused edge of a region,
// and the plain-text fallback for terminals without box-drawing
// characters (§2.7, §14: "works without a Nerd Font").
const (
	// FocusBar is the marker of the focused region.
	FocusBar = "▌"

	// FocusArrow is the same marker for a terminal that cannot show a
	// block bar.
	FocusArrow = ">"

	// FocusNone is what an unfocused region shows: the width of the
	// marker, so the layout does not shift when the focus moves.
	FocusNone = " "
)

// FocusIndicator returns the marker of a region.
//
// The marker is the same in every profile, including no colour at all: a
// user who cannot see colour is exactly the user who must be able to see
// where the focus is. An unfocused region gets blanks of the same width,
// so moving the focus never reflows the interface.
func (t Theme) FocusIndicator(focused bool) string {
	if !focused {
		return FocusNone
	}

	return FocusBar
}

// Attributes are the text attributes a renderer applies to a run.
//
// They are part of the theme because they are the second thing that
// survives no colour, and because a theme should not leave the choice of
// "bold or reverse" to each component.
type Attributes struct {
	Bold    bool
	Reverse bool
}

// SelectionAttributes returns the attributes of a selected row.
//
// Reverse is the marker: it is the one attribute that works on every
// terminal, in every profile, and on every background. Bold is added
// because a selected row is also the one a user is acting on, and §2.7
// asks for reverse or bold.
func (t Theme) SelectionAttributes(selected bool) Attributes {
	if !selected {
		return Attributes{}
	}

	return Attributes{Reverse: true, Bold: true}
}

// StatusState is a delivery state as the interface names it.
//
// The seven states are the ones of §6 and §15. They live here because
// "a status is never only colour" is a property of the theme: a renderer
// that has no status of its own still gets a symbol and words.
type StatusState uint8

const (
	// StatusQueued is a message waiting to be sent.
	StatusQueued StatusState = iota

	// StatusSending is a message on its way out.
	StatusSending

	// StatusRetrying is a failed send that will be tried again.
	StatusRetrying

	// StatusSent is a message Telegram accepted.
	StatusSent

	// StatusFailed is a send TDLib refused for good.
	StatusFailed

	// StatusUncertain is a send whose result is unknown, so a resend may
	// duplicate it. It is deliberately not styled like a failure (§24).
	StatusUncertain

	// StatusCanceled is a message the user withdrew.
	StatusCanceled
)

// StatusMark is a status written out: a symbol and words.
//
// Both parts are always present, in every profile. The symbol is what
// makes a status readable at a glance, and the words are what make it
// readable with no symbol support at all.
type StatusMark struct {
	// Symbol is the one-character mark of the state.
	Symbol string

	// Text is the word or words of the state. The uncertain state spells
	// itself out, because "Uncertain" alone does not say what the user
	// has to do about it.
	Text string
}

// String renders the symbol and the words, as §2.7 requires: "status: a
// symbol plus text".
func (m StatusMark) String() string {
	return strings.TrimSpace(m.Symbol + " " + m.Text)
}

// Plain returns the words alone, which is the form for a terminal that
// cannot show the symbol.
func (m StatusMark) Plain() string {
	return m.Text
}

// The marks and words are the ones §14 lists, so the interface says the
// same thing everywhere it shows a delivery state. The words are English
// because that is the vocabulary of the specification; the screens keep
// their own labels until PR-10A.4 rewrites them.
var statusMarks = map[StatusState]StatusMark{
	StatusQueued:    {Symbol: "●", Text: "Queued"},
	StatusSending:   {Symbol: "◐", Text: "Sending"},
	StatusRetrying:  {Symbol: "↻", Text: "Retrying"},
	StatusSent:      {Symbol: "✓", Text: "Sent"},
	StatusFailed:    {Symbol: "!", Text: "Failed"},
	StatusUncertain: {Symbol: "?", Text: "Delivery uncertain"},
	StatusCanceled:  {Symbol: "⊘", Text: "Canceled"},
}

// Mark returns the symbol and words of a state.
//
// An unknown state is reported as its own words rather than as a known
// one, so a state a future build adds cannot be shown as "Sent".
func (s StatusState) Mark() StatusMark {
	if mark, ok := statusMarks[s]; ok {
		return mark
	}

	return StatusMark{Symbol: "?", Text: "Unknown state"}
}

// Color returns the colour role of a state in a token set.
//
// Under the no-colour profile it is unset, and the symbol and words are
// all that is left: which is why they are always filled in.
func (s StatusState) Color(tokens Tokens) Color {
	switch s {
	case StatusQueued:
		return tokens.StatusInfo
	case StatusSending:
		return tokens.StatusActive
	case StatusRetrying:
		return tokens.StatusWarning
	case StatusSent:
		return tokens.StatusSuccess
	case StatusFailed:
		return tokens.StatusError
	case StatusUncertain:
		return tokens.StatusUncertain
	case StatusCanceled:
		return tokens.StatusCanceled
	default:
		return Color{}
	}
}

// StatusStates returns every delivery state, in the order the interface
// lists them.
func StatusStates() []StatusState {
	return []StatusState{
		StatusQueued,
		StatusSending,
		StatusRetrying,
		StatusSent,
		StatusFailed,
		StatusUncertain,
		StatusCanceled,
	}
}

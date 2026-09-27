package theme

import "testing"

// §2.7 and §14 are what this file is about: the interface has to say the
// same thing with no colour as with colour, and the parts that carry the
// meaning belong to the theme rather than to each view.

func TestStatusMarkCarriesSymbolAndWords(t *testing.T) {
	// The marks and words of §14, so the interface says one thing about a
	// delivery state everywhere it shows it.
	cases := map[StatusState]StatusMark{
		StatusQueued:    {Symbol: "●", Text: "Queued"},
		StatusSending:   {Symbol: "◐", Text: "Sending"},
		StatusRetrying:  {Symbol: "↻", Text: "Retrying"},
		StatusSent:      {Symbol: "✓", Text: "Sent"},
		StatusFailed:    {Symbol: "!", Text: "Failed"},
		StatusUncertain: {Symbol: "?", Text: "Delivery uncertain"},
		StatusCanceled:  {Symbol: "⊘", Text: "Canceled"},
	}

	for state, want := range cases {
		if got := state.Mark(); got != want {
			t.Errorf("state %d mark = %+v, want %+v", state, got, want)
		}
	}
}

func TestUnknownStatusStateIsNotShownAsAKnownOne(t *testing.T) {
	mark := StatusState(42).Mark()

	if mark.Text == "" || mark.Symbol == "" {
		t.Fatalf("mark = %+v, want a symbol and words", mark)
	}

	// It must not borrow a known state's wording: a status a future build
	// adds cannot be shown as "Sent".
	for _, state := range StatusStates() {
		if mark == state.Mark() {
			t.Fatalf("an unknown state is shown as %+v", state.Mark())
		}
	}

	if color := StatusState(42).Color(Tokens{StatusError: Basic(1)}); color.IsSet() {
		t.Errorf("an unknown state has the colour %v", color)
	}
}

// Every state must reach a status token, and no two states may share one:
// on a terminal that shows colour, the colour is the only difference
// between "queued" and "sending".
func TestEveryStatusStateHasItsOwnToken(t *testing.T) {
	for _, name := range ThemeNames() {
		built := mustTheme(t, name)
		roles := colorRoles(t, built.Tokens)

		seen := map[string]StatusState{}
		for _, state := range StatusStates() {
			role := statusRoleName(state)
			color, ok := roles[role]
			if !ok {
				t.Fatalf("theme %s: no role %q", name, role)
			}
			if !color.IsSet() {
				t.Errorf("theme %s: role %s is not set", name, role)
			}
			if other, duplicate := seen[color.String()]; duplicate {
				t.Errorf(
					"theme %s: states %v and %v share the colour %v",
					name,
					other,
					state,
					color,
				)
			}
			seen[color.String()] = state

			// And the state must point at the token it names, so a caller
			// cannot be handed a different colour than the test checked.
			if state.Color(built.Tokens) != color {
				t.Errorf(
					"theme %s: state %d reports %v, the role %s is %v",
					name,
					state,
					state.Color(built.Tokens),
					role,
					color,
				)
			}
		}
	}
}

// statusRoleName is the token role a state is drawn with. It is the mirror
// of StatusState.Color, and the test above compares the two so a change in
// one without the other is caught.
func statusRoleName(state StatusState) string {
	switch state {
	case StatusQueued:
		return "StatusInfo"
	case StatusSending:
		return "StatusActive"
	case StatusRetrying:
		return "StatusWarning"
	case StatusSent:
		return "StatusSuccess"
	case StatusFailed:
		return "StatusError"
	case StatusUncertain:
		return "StatusUncertain"
	case StatusCanceled:
		return "StatusCanceled"
	default:
		return ""
	}
}

func TestSelectionAttributesAreOffWhenNothingIsSelected(t *testing.T) {
	built := mustTheme(t, ThemeCatppuccinMocha)

	if got := built.SelectionAttributes(false); got != (Attributes{}) {
		t.Errorf("SelectionAttributes(false) = %+v, want no attribute", got)
	}

	selected := built.SelectionAttributes(true)
	if !selected.Reverse && !selected.Bold {
		t.Errorf("SelectionAttributes(true) = %+v, want reverse or bold", selected)
	}
}

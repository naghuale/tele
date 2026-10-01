package termwidth

import "sort"

// Probes are the strings the measurement asks the terminal about.
//
// Each one is a case the two rules disagree on, so a single answer is
// enough to tell them apart:
//
//   - `✌️` is two columns to a grapheme counter and one to a terminal that
//     follows wcwidth, because the variation selector behind the hand does
//     not widen it;
//   - `🇨🇳` is a flag, which is two regional indicators that no terminal
//     draws as two letters;
//   - `🏃‍♂️` is a runner, a joiner and a sign, and the rule that counts
//     code points has to keep it at the width of the first one;
//   - `👍🏽` is a thumb and a skin tone, and the tone is not a second
//     thumb;
//   - `⛩️` is a pagoda and a selector, and it is a different question from
//     the hand: `⛩` on its own is one letter and `⛩️` is the same letter
//     asking to be drawn as an emoji, which is the whole of what a terminal
//     with emoji support and a terminal without it disagree about;
//   - `☕` is a character that has no selector and is still drawn two
//     columns wide, because it has emoji presentation of its own. A
//     terminal that has to be asked about it is a terminal whose font, not
//     its rules, is the question;
//   - `🫶` is that too, and a newer one: a terminal that has never heard of
//     it draws a box, and a box is not two columns of the glyph a message
//     was written with;
//   - `中` is a Han character, which every terminal that speaks about
//     East Asian widths draws two columns wide;
//   - `é` written as an e and a combining acute is two code points and one
//     letter, and the two rules say the same thing about it: one column.
//
// The last one is in the list to catch a terminal that answers with
// nonsense. A rule that agreed with the others and disagreed here is not
// measuring glyphs.
//
// The probes are the ones the strings of real chats are made of, and the
// strings of real chats came from the owner's screen of 01.10: a Chinese
// channel name with a pagoda in it, a trading chat behind a flag, a coffee
// in a preview and a hug in a message. A measurement that cannot answer for
// those falls back to the rule the renderer draws with, which costs a column
// of air where the terminal would have drawn one and saves the screen
// everywhere else.
var Probes = []string{
	"✌️",
	"🇨🇳",
	"🏃‍♂️",
	"👍🏽",
	"⛩️",
	"☕",
	"🫶",
	"中",
	"é",
}

// Measurement is what one run of the measurement learned about a terminal.
//
// It is a value and not a result of a call the views make: the
// measurement runs once, before the first frame, and what it found is
// carried with the model for every frame after it.
type Measurement struct {
	// Widths is what the terminal said each probe takes, in columns. A
	// probe it did not answer about is not in the map.
	Widths map[string]int

	// Pending is what was read from the terminal that was not an answer to
	// a request of ours.
	//
	// It is a keystroke a user pressed in the window between the program
	// starting and the measurement finishing, and dropping it would eat a
	// key press for a reason the user cannot see.
	Pending []byte

	// TimedOut says the measurement ran out of its budget before the
	// terminal answered.
	TimedOut bool
}

// Answered returns how many probes the terminal answered about.
func (m Measurement) Answered() int { return len(m.Widths) }

// Usable reports whether the measurement is worth anything.
//
// A terminal that answered nothing is a terminal that is not a terminal:
// the output is a file, or the request went somewhere that does not
// answer, and both are cases the fallback rule is written for.
func (m Measurement) Usable() bool { return len(m.Widths) > 0 }

// Select returns the model a configured mode and a measurement resolve to,
// and the reason for it.
//
// The rule of the decision is one line: a terminal that agrees with the
// grapheme rule about every probe it answered about is a terminal that
// draws graphemes, and a terminal that does not is counted by code points.
// Everything else is a rule from the configuration, and a terminal that
// answered nothing at all is the grapheme rule as well.
//
// The fallback is the grapheme rule and not the code point one, and that is
// the whole of the reason the two are not interchangeable. The renderer
// draws every row with its own counting, which is the grapheme rule,
// whatever the mode says: a row counted by code points is a column narrower
// than the row the renderer is about to write, and a row that reaches the
// last column of the window in the terminal's own count is a row the
// terminal wraps, which moves everything under it down a row. So a program
// that guesses wrong in the direction of code points does not merely leave
// a column of air on a symbol: it shifts the screen under a title with a
// flag in it (the owner, 01.10). Where the terminal answered, the answer
// still wins, and `tui.width = "codepoint"` is still a way to ask for the
// other rule by name.
//
// The measured widths are carried into the model either way. They are the
// terminal's own answer about a flag, and no rule knows what a terminal
// does with one: it is the one case where a rule is a guess and a
// measurement is not.
func Select(configured Mode, m Measurement) (WidthModel, Choice) {
	if configured != ModeAuto {
		return newWidthModel(configured, m.Widths), Choice{
			Mode:   configured,
			Source: SourceConfigured,
		}
	}

	if m.Usable() {
		rule, source := ModeGrapheme, SourceMeasured
		if !agreesWithGrapheme(m) {
			rule, source = ModeCodepoint, SourceMeasured
		}

		return newWidthModel(rule, m.Widths), Choice{Mode: rule, Source: source}
	}

	return newWidthModel(ModeGrapheme, nil), Choice{
		Mode:   ModeGrapheme,
		Source: SourceDefault,
	}
}

// Unmeasured returns the model of a program that measured nothing: the
// configured rule, or the grapheme rule where the configuration left the
// choice to the terminal.
//
// It is what a model built without a program is drawn with, and it says
// what it is doing rather than leaving a zero model to be interpreted: the
// rule of a terminal nobody could ask is a decision, and a decision that
// is only visible in the absence of a value is a decision nobody can
// check.
func Unmeasured(configured Mode) WidthModel {
	model, _ := Select(configured, Measurement{})

	return model
}

// agreesWithGrapheme reports whether every answer the terminal gave is the
// answer the grapheme rule would have given.
//
// One disagreement is enough, and only the probes that were answered
// count: a terminal that had nothing to say about half of them has still
// said what it does with the ones it did.
func agreesWithGrapheme(m Measurement) bool {
	grapheme := newWidthModel(ModeGrapheme, nil)

	probes := make([]string, 0, len(m.Widths))
	for probe := range m.Widths {
		probes = append(probes, probe)
	}
	sort.Strings(probes)

	for _, probe := range probes {
		if grapheme.StringWidth(probe) != m.Widths[probe] {
			return false
		}
	}

	return true
}

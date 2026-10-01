package termwidth

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
)

// WidthModel counts the width of text in one terminal.
//
// It is a value and not a service: a model is built once, when the terminal
// has been measured or the measurement has been given up on, and every
// view reads the same one. Two models in one program would be two answers
// to the same question, and a row laid out with one and padded with the
// other is a row that is a column out of place.
//
// The zero model counts by code points, which is the rule of a terminal
// nobody could ask. That is what a model built before the measurement
// gets, and it is what makes a zero model drawable rather than a program
// that has to be told a width rule before it can show a screen.
type WidthModel struct {
	mode Mode

	// measured is what the terminal said about the probes, keyed by the
	// probe. It is a handful of entries and it is read on every column of
	// every frame, so a lookup that misses must be cheap: it is a map
	// access, not a measurement.
	measured map[string]int
}

// newWidthModel returns a model that counts in the given mode, with the
// measured widths of a terminal on top of it.
func newWidthModel(mode Mode, measured map[string]int) WidthModel {
	if len(measured) == 0 {
		measured = nil
	}

	return WidthModel{mode: mode, measured: measured}
}

// Mode returns the rule this model counts in. It is never ModeAuto: the
// rule is decided before the model is built, because a view cannot be
// asked to measure anything.
func (w WidthModel) Mode() Mode {
	if w.mode == ModeGrapheme {
		return ModeGrapheme
	}

	return ModeCodepoint
}

// StringWidth returns how many terminal columns value occupies.
//
// Escape sequences take no columns and are skipped, a grapheme cluster is
// never counted as two things, and a string the terminal was asked about
// counts for whatever the terminal said.
func (w WidthModel) StringWidth(value string) int {
	width := 0

	for item := range w.items(value) {
		width += w.itemWidth(item)
	}

	return width
}

// Truncate cuts value to width terminal columns and puts tail where the cut
// was.
//
// A cluster that does not fit whole is left out rather than cut in half: a
// terminal prints the code points of one in sequence, so half of a cluster
// is a broken letter rather than a short one. A tail too wide for the
// width is cut the same way, which is what keeps a one column field from
// drawing an ellipsis that is two columns wide.
func (w WidthModel) Truncate(value string, width int, tail string) string {
	if width <= 0 {
		return ""
	}
	if w.StringWidth(value) <= width {
		return value
	}

	budget := width - w.StringWidth(tail)
	if budget < 0 {
		return w.keep(tail, width)
	}

	return w.keep(value, budget) + tail
}

// TruncateMarked cuts value to width terminal columns and says that it was
// cut, by putting mark where the cut was.
//
// The mark goes only where there is room for it and for something beside
// it: a one column field holding a lone ellipsis is not a name, and
// showing the first letter of it is better than showing nothing.
func (w WidthModel) TruncateMarked(value string, width int, mark string) string {
	if width <= 0 {
		return ""
	}
	if w.StringWidth(value) <= width {
		return value
	}

	if marked := w.Truncate(value, width, mark); marked != mark {
		return marked
	}

	return w.Truncate(value, width, "")
}

// TruncateLeft removes remove columns from the start of value and puts head
// where they were.
//
// The escape sequences stay, in the order they were in: the ones in front
// of the cut keep the tail coloured as it was, and the ones behind it close
// whatever is left open, so that a line cut in the middle of a coloured run
// does not paint the next line in that colour. A line behind a popup is a
// coloured line, and a byte or a colour that leaks out of it lands in the
// middle of a conversation.
func (w WidthModel) TruncateLeft(value string, remove int, head string) string {
	if remove <= 0 {
		return value
	}

	limit := w.StringWidth(value) - remove

	var (
		out, rest strings.Builder
		used      int
		cut       bool
	)

	for item := range w.items(value) {
		if item.escape {
			// The sequences before the cut are what the tail is printed
			// in, and the ones after it are what closes them.
			if cut {
				rest.WriteString(item.text)
			} else {
				out.WriteString(item.text)
			}

			continue
		}

		used += w.itemWidth(item)
		if used > limit {
			cut = true

			continue
		}

		out.WriteString(item.text)
	}

	if !cut {
		return value
	}

	return head + out.String() + rest.String()
}

// Wrap breaks value into lines of at most width terminal columns, keeping
// the words whole, and marks a word that does not fit with tail.
//
// It is for prose a user has to be able to read in full: a paragraph cut
// with an ellipsis loses the part that says what to do about the problem,
// and a message body is not a label. A word wider than the line, such as a
// URL, is cut rather than allowed to push the layout wide, and the tail is
// what says that it was.
//
// Lines that were already separated in value stay separated, so a draft
// the user typed over several rows is not joined into one.
func (w WidthModel) Wrap(value string, width int, tail string) []string {
	if width <= 0 {
		return []string{""}
	}

	var (
		lines  []string
		blocks = strings.Split(value, "\n")
	)

	for _, block := range blocks {
		words := strings.Fields(block)
		if len(words) == 0 {
			lines = append(lines, "")
			continue
		}

		line := words[0]
		for _, word := range words[1:] {
			if w.StringWidth(line)+1+w.StringWidth(word) <= width {
				line += " " + word
				continue
			}

			lines = append(lines, w.TruncateMarked(line, width, tail))
			line = word
		}

		lines = append(lines, w.TruncateMarked(line, width, tail))
	}

	return lines
}

// Fit cuts value to width terminal columns, marking the cut with mark, and
// pads it to them.
//
// Every line of a region is fitted to the same width, so the regions below
// and beside it stay rectangular and a background covers the whole pane
// instead of ending where the text happened to end. A line that is too
// wide is cut rather than left to wrap: one that wraps is one that moves
// everything under it.
func (w WidthModel) Fit(value string, width int, mark string) string {
	if width <= 0 {
		return ""
	}

	missing := width - w.StringWidth(value)
	if missing <= 0 {
		return w.drawnByWidthRenderer(w.TruncateMarked(value, width, mark), width)
	}

	return w.drawnByWidthRenderer(value+strings.Repeat(" ", missing), width)
}

// RendererWidth returns the number of columns row takes to the renderer
// that draws the screen, which counts with a rule of its own.
//
// It is the second of the two counts of a row and not a third: the first is
// the model's, and this one is the count the row is held to before it is
// handed over. A row is a rectangle when both counts are the width of the
// window, and the renderer is the one that decides first whether to cut a
// row that is over.
func RendererWidth(row string) int {
	return ansi.StringWidth(row)
}

// drawnByWidthRenderer returns row no wider than width to either of the
// two rules, and exactly width to the one that fitted it.
//
// The two rules part company over an emoji that asks for the emoji drawing:
// a model that adds up its code points gives the cluster one column and the
// renderer gives it two, so a row fitted here arrives a column too wide and
// the renderer cuts it. A row the renderer cuts is a row whose last cell
// keeps what the row before it drew, and that is what a dark cell at the
// edge of a list is. The row is therefore cut by as many columns as the
// renderer counts it over, which costs a column of air on a row with such
// a cluster in it and saves the frame on every row.
func (w WidthModel) drawnByWidthRenderer(row string, width int) string {
	over := RendererWidth(row) - width
	if over <= 0 {
		return row
	}

	return w.Truncate(row, w.StringWidth(row)-over, "")
}

// keep returns the part of value that fits in budget columns, followed by
// the escape sequences of the part that does not.
//
// A cluster that would cross the budget is left out whole, so what comes
// back is always a whole number of letters. The sequences that were among
// the letters that went are kept, because they say what colour is in force
// where the cut was: a prefix that ends with a colour open paints the tail
// that follows it in that colour, which is the same mistake as cutting
// through a sequence and printing the rest of it as text.
func (w WidthModel) keep(value string, budget int) string {
	if budget <= 0 {
		return ""
	}

	var (
		out, rest strings.Builder
		used      int
		cut       bool
	)

	for item := range w.items(value) {
		if cut {
			if item.escape {
				rest.WriteString(item.text)
			}

			continue
		}

		if item.escape {
			out.WriteString(item.text)
			continue
		}

		width := w.itemWidth(item)
		if used+width > budget {
			cut = true

			continue
		}

		used += width
		out.WriteString(item.text)
	}

	return out.String() + rest.String()
}

// itemWidth returns how many columns one item of a line takes.
func (w WidthModel) itemWidth(item lineItem) int {
	if item.escape {
		return 0
	}

	if columns, ok := fixedWidth[item.text]; ok {
		return columns
	}

	if measured, ok := w.measured[item.text]; ok {
		return measured
	}

	if w.Mode() == ModeGrapheme {
		return item.width
	}

	return codepointWidth(item.text)
}

// item is one piece of a line: an escape sequence, or a grapheme cluster.
//
// The two are together because they are together in a rendered line, and a
// cut or a count that forgot the difference would measure escape sequences
// as letters and cut them in half.
type lineItem struct {
	// text is the piece itself, escape sequence included.
	text string

	// escape says whether it is a sequence, which takes no columns and is
	// never cut.
	escape bool

	// width is what the width library says a printable cluster takes,
	// which is the grapheme rule.
	width int
}

// items walks a line one piece at a time, escape sequences and grapheme
// clusters both, and never in the middle of either.
//
// The decoder is given no parser: it is asked where the pieces are and how
// wide they are, and the parameters of an escape sequence are of no use to
// a program that is counting columns.
func (w WidthModel) items(value string) func(func(lineItem) bool) {
	return func(yield func(lineItem) bool) {
		for rest := value; rest != ""; {
			sequence, width, read, _ := ansi.DecodeSequence(rest, 0, nil)
			if read <= 0 {
				// A byte the decoder did not take. It is counted as one
				// column rather than dropped: a line that is too wide is
				// noticed, and a line that is silently shorter than the
				// text in it is not.
				if !yield(lineItem{text: rest[:1], width: 1}) {
					return
				}

				rest = rest[1:]

				continue
			}

			item := lineItem{
				text:   sequence,
				escape: isEscape(sequence),
				width:  width,
			}
			if !yield(item) {
				return
			}

			rest = rest[read:]
		}
	}
}

// isEscape reports whether a decoded piece is an escape sequence rather
// than something the terminal draws.
//
// The decoder hands back both, and an escape sequence is the one that
// starts with ESC or with one of the C1 controls.
func isEscape(sequence string) bool {
	if sequence == "" {
		return false
	}

	head := sequence[0]

	return head == 0x1b || (head >= 0x80 && head <= 0x9f)
}

// The two halves of the rounded end of a Nerd Font block, and the columns
// they take.
//
// They are the glyphs the interface draws at the ends of a message of this
// user when the setting `tui.nerd_font` is on: U+E0B6 on the left and
// U+E0B4 on the right, each painted in the colour of the block so that it
// reads as a semicircle cut out of it. They are declared here rather than
// counted by a rule, because the two rules of this package both happen to
// say one column today and neither of them says it for a reason: a
// private-use code point is a letter to wcwidth and a letter to the emoji
// tables, and the day a version of either of them decides otherwise the
// block is a column wider on one side, the row is a column over the width
// of the feed, and the terminal wraps it and moves everything under it.
//
// So the width is a fact about the glyph and not a result of the counting,
// and it is the same one column in both rules: a block whose ends were two
// columns in one of them would be a different shape in each.
const (
	// NerdHalfLeft rounds the left end of a block.
	NerdHalfLeft = "\ue0b6"

	// NerdHalfRight rounds the right end of a block.
	NerdHalfRight = "\ue0b4"

	// nerdHalfWidth is how many columns either of them takes.
	nerdHalfWidth = 1
)

// fixedWidth is the glyphs whose width this package states rather than
// counts, and what it states.
//
// It is keyed by the whole cluster and not by the code point, so a glyph
// made of several code points can be in here too, and it is consulted
// before the measurement as well as before the rules: a terminal cannot
// draw a semicircle out of a rounded rectangle, and the whole of the
// drawing it is a part of is sized for the one column the interface
// promised.
var fixedWidth = map[string]int{
	NerdHalfLeft:  nerdHalfWidth,
	NerdHalfRight: nerdHalfWidth,
}

// The code points that join a cluster together without taking a column of
// their own.
const (
	// zeroWidthJoiner glues the parts of one glyph together: a family, a
	// runner with a sign, a pirate flag. Everything after it inside the
	// cluster is part of the glyph the joiner is standing in and takes no
	// column.
	zeroWidthJoiner = 0x200d

	// variationSelector16 asks for the emoji drawing of the character
	// before it, and variationSelector15 asks for the text one. Neither
	// is a column: a terminal that honours the selector changes the shape
	// it draws in the columns it already has, and one that does not ignores
	// it.
	variationSelector16 = 0xfe0f
	variationSelector15 = 0xfe0e

	// firstSkinTone and lastSkinTone bound the five modifiers that colour
	// a hand or a person: they change the glyph they follow and are not a
	// second glyph.
	firstSkinTone = 0x1f3fb
	lastSkinTone  = 0x1f3ff
)

// codepointWidth returns how many columns a grapheme cluster takes when
// its code points are added up one by one.
//
// The rules are wcwidth's, and they are the rules of a terminal that
// follows wcwidth rather than the emoji tables: the joiners and the
// modifiers take nothing, the rest takes what its East Asian Width says,
// and a code point with emoji presentation takes two. A flag is two
// regional indicators, which come to two columns between them, and the
// measured width of the flag wins over both when the terminal was asked.
func codepointWidth(cluster string) int {
	total := 0
	joined := false

	for _, value := range cluster {
		width := 0

		switch {
		case joined:
			// The other half of a joined glyph. It is drawn in the
			// columns of the half it is joined to.

		case value == zeroWidthJoiner:
			// A joiner is nothing on its own; what follows it is the
			// part that would be drawn next.

		case value == variationSelector16, value == variationSelector15:
			// A selector chooses a drawing, and a drawing is the width it
			// is.

		case value >= firstSkinTone && value <= lastSkinTone:
			// A skin tone is a colour, not a second hand.

		default:
			width = runewidth.RuneWidth(value)
		}

		joined = value == zeroWidthJoiner
		total += width
	}

	return total
}

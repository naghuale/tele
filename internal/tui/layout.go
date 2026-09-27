package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// The screen has three width classes and two height rules, and every
// number that decides a shape lives here so a pane, the hint bar and a
// test cannot disagree about them (docs/TUI_SPEC.md §10.1).
//
// The breakpoints are starting tokens rather than a promise: the
// invariant is the one §10.1 names, that a narrow screen shows a single
// region and a wider one shows both.
const (
	// wideLayoutMinWidth is the width at which both panes fit.
	wideLayoutMinWidth = 100

	// mediumLayoutMinWidth is the width at which both panes still fit
	// with a narrower list.
	mediumLayoutMinWidth = 72

	// shortLayoutHeight is the height below which secondary hints, chat
	// previews and the extra status lines go away (§3.4).
	shortLayoutHeight = 20

	// noHintBarHeight is the height below which the hint bar is not drawn
	// at all.
	noHintBarHeight = 10

	// composerOnlyHeight is the height below which the conversation shows
	// nothing but the composer.
	composerOnlyHeight = 6

	// wideSidebarWidth and mediumSidebarWidth are the fixed widths of the
	// chat list pane, including its focus column.
	//
	// The list is not a flexible share of the width: a chat list that
	// grows with the terminal leaves the conversation unreadable, and one
	// that shrinks makes titles useless. §3.3 says the same for the
	// narrow case, where it is not squeezed at all.
	wideSidebarWidth   = 24
	mediumSidebarWidth = 16

	// paneGapWidth is the space between the two panes.
	//
	// The panes are separated by their backgrounds, the gap and the focus
	// column, and by nothing else: §1 forbids a full-height vertical
	// divider, and one column of space is what is left of that idea
	// without a line.
	paneGapWidth = 1
)

// focusColumnWidth is the single column a focus marker occupies, and
// contentInsetWidth the space between it and the text of a region.
//
// Every region reserves both whether or not it is focused, so moving the
// focus never changes a width and never shifts what is drawn to its right.
// The inset is what keeps text off the marker: `▌` and the first letter
// with no space between them read as one shape.
const (
	focusColumnWidth  = 1
	contentInsetWidth = 1
)

// selectionMarkerWidth is the single column the marker of a selected row or
// message takes.
//
// Every row reserves it, selected or not, so a row does not shift when the
// selection moves to it.
const selectionMarkerWidth = 1

// minWidth and minHeight are the smallest screen that is laid out at all.
//
// Below them the interface says so instead of drawing something that does
// not fit. The narrow layout is the supported way to use a small
// terminal, and this is where that stops being true.
const (
	minWidth  = 40
	minHeight = 5
)

// LayoutKind is the width class of the screen.
type LayoutKind uint8

const (
	// LayoutWide is a screen of wideLayoutMinWidth columns or more.
	LayoutWide LayoutKind = iota

	// LayoutMedium is a screen that fits both panes, narrowly.
	LayoutMedium

	// LayoutNarrow is a screen that shows one region at a time.
	LayoutNarrow
)

// String returns a stable lowercase name.
func (k LayoutKind) String() string {
	switch k {
	case LayoutWide:
		return "wide"
	case LayoutMedium:
		return "medium"
	case LayoutNarrow:
		return "narrow"
	default:
		return "unknown"
	}
}

// Layout is the resolved shape of the screen for one size.
//
// It is computed from the size alone. Whether a conversation is open is
// model state, not layout, so the two never have to be reconciled: a
// pane decides whether it has anything to draw.
type Layout struct {
	Kind   LayoutKind
	Width  int
	Height int
}

// LayoutFor returns the layout of a screen of the given size.
func LayoutFor(width, height int) Layout {
	kind := LayoutNarrow
	switch {
	case width >= wideLayoutMinWidth:
		kind = LayoutWide
	case width >= mediumLayoutMinWidth:
		kind = LayoutMedium
	}

	return Layout{Kind: kind, Width: width, Height: height}
}

// TwoPane reports whether both panes fit on the screen.
//
// Narrow is a single-region screen (§10.4), and the chat list is not
// squeezed into it (§3.3).
func (l Layout) TwoPane() bool { return l.Kind != LayoutNarrow }

// SidebarWidth returns the width of the chat list pane, or 0 when the
// screen shows a single region.
func (l Layout) SidebarWidth() int {
	switch l.Kind {
	case LayoutWide:
		return wideSidebarWidth
	case LayoutMedium:
		return mediumSidebarWidth
	default:
		return 0
	}
}

// ChatWidth returns the width of the conversation pane, or the full width
// when the screen shows a single region.
func (l Layout) ChatWidth() int {
	if !l.TwoPane() {
		return l.Width
	}

	return l.Width - l.SidebarWidth() - paneGapWidth
}

// Short reports whether the screen is too short for the secondary hints
// (§3.4).
func (l Layout) Short() bool { return l.Height < shortLayoutHeight }

// HideHints reports whether the hint bar is not drawn at all.
func (l Layout) HideHints() bool { return l.Height < noHintBarHeight }

// ComposerOnly reports whether the conversation shows nothing but the
// composer.
func (l Layout) ComposerOnly() bool { return l.Height < composerOnlyHeight }

// hintLines returns the number of rows the hint bar takes, which is one
// row or none.
func (l Layout) hintLines() int {
	if l.HideHints() {
		return 0
	}

	return 1
}

// SidebarContentWidth returns the text width inside the chat list pane,
// after its focus column and its inset.
func (l Layout) SidebarContentWidth() int {
	width := l.SidebarWidth() - focusColumnWidth - contentInsetWidth
	if width < 1 {
		return 1
	}

	return width
}

// FullContentWidth returns the text width of a region that has the whole
// screen to itself.
func (l Layout) FullContentWidth() int {
	width := l.Width - focusColumnWidth - contentInsetWidth
	if width < 1 {
		return 1
	}

	return width
}

// ChatContentWidth returns the text width inside the conversation pane,
// after the focus column and the inset.
func (l Layout) ChatContentWidth() int {
	width := l.ChatWidth() - focusColumnWidth - contentInsetWidth
	if width < 1 {
		return 1
	}

	return width
}

// visibleRange returns the [start, end) slice window of a list of total
// items such that selected is included. end is exclusive.
//
// If total <= 0 or available <= 0, returns (0, 0).
// If available >= total, returns (0, total).
// visibleRange does not mutate any input.
func visibleRange(total, selected, available int) (int, int) {
	if total <= 0 || available <= 0 {
		return 0, 0
	}
	if available >= total {
		return 0, total
	}
	if selected < 0 {
		selected = 0
	}
	if selected >= total {
		selected = total - 1
	}

	half := available / 2
	start := selected - half
	if start < 0 {
		start = 0
	}
	end := start + available
	if end > total {
		end = total
		start = end - available
		if start < 0 {
			start = 0
		}
	}
	return start, end
}

// cellWidth returns how many terminal columns value occupies.
//
// Terminals count columns, not runes: "привет" is six columns wide and an
// emoji is two, so a layout measured in runes overflows the moment the
// content is not ASCII. Counting columns is also what keeps a long chat
// title from pushing the composer off the screen.
func cellWidth(value string) int {
	return ansi.StringWidth(value)
}

// truncateCells cuts value to width terminal columns and marks the cut.
//
// The mark is an ellipsis, so a truncated title says that it was cut
// rather than looking like the whole name. A column too narrow to hold the
// mark and some text gets the text without the mark, because a lone
// ellipsis is not a name and something is better than nothing.
func truncateCells(value string, width int) string {
	if width <= 0 {
		return ""
	}

	truncated := ansi.Truncate(value, width, "…")
	if cellWidth(truncated) >= width && truncated != ellipsis {
		return truncated
	}

	marked := ansi.Truncate(value, width, ellipsis)
	if marked != ellipsis {
		return marked
	}

	return ansi.Truncate(value, width, "")
}

// ellipsis marks a cut in the interface.
const ellipsis = "…"

// wrapCells breaks value into lines of at most width terminal columns,
// keeping the words whole.
//
// It is for prose the user has to be able to read in full: a paragraph
// that is cut with an ellipsis loses the part that says what to do about
// the problem, and a message body is not a label. A word wider than the
// line, such as a URL, is cut rather than allowed to push the layout wide.
func wrapCells(value string, width int) []string {
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
			if cellWidth(line)+1+cellWidth(word) <= width {
				line += " " + word
				continue
			}

			lines = append(lines, truncateCells(line, width))
			line = word
		}

		lines = append(lines, truncateCells(line, width))
	}

	return lines
}

// padCells extends value with spaces to exactly width terminal columns.
//
// Every line of a region is padded to the same width, so the regions
// below and beside it stay rectangular and a background covers the whole
// pane instead of ending where the text happened to end.
func padCells(value string, width int) string {
	if width <= 0 {
		return ""
	}

	missing := width - cellWidth(value)
	if missing <= 0 {
		return truncateCells(value, width)
	}

	return value + spaces(missing)
}

// spaces returns n spaces.
func spaces(n int) string {
	if n <= 0 {
		return ""
	}

	out := make([]byte, n)
	for index := range out {
		out[index] = ' '
	}

	return string(out)
}

// fitCells truncates or pads value to exactly width terminal columns.
func fitCells(value string, width int) string {
	return padCells(truncateCells(value, width), width)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

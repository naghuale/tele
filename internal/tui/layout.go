package tui

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

	// wideSidebarSharePercent is how much of a wide screen the chat list
	// pane takes, and wideSidebarLeast and wideSidebarMost the bounds it
	// is kept between.
	//
	// The list is a share of the width and not a fixed number, because
	// neither a fixed number nor a percentage alone is right: a chat list
	// that grows with the terminal leaves the conversation unreadable, and
	// one that does not grow cuts a name in half while the conversation
	// beside it has empty columns. A quarter of the width is what the
	// approved drawing gives the list, and it is never less than 28
	// columns (a name of a length anybody has) and never more than 40
	// (a list wider than that is not a list a person reads). §3.3 says
	// the same for the narrow case, where it is not squeezed at all.
	wideSidebarSharePercent = 28
	wideSidebarLeast        = 28
	wideSidebarMost         = 40

	// mediumSidebarWidth is the fixed width of the chat list pane on a
	// medium screen, including the column every region reserves.
	mediumSidebarWidth = 16

	// paneGapWidth is the space between the two panes.
	//
	// The panes are separated by their backgrounds, the gap and the focus
	// column, and by nothing else: §1 forbids a full-height vertical
	// divider, and one column of space is what is left of that idea
	// without a line.
	paneGapWidth = 1

	// feedMargin is the space on each side of the feed, and
	// feedMarginNarrow the same space on a single-pane screen.
	//
	// Three columns is the air a conversation has in the approved
	// drawing: without it the other side's messages start against the
	// edge of the region and this user's end at it, and a feed whose
	// messages touch both edges is a column of text rather than a
	// conversation. A single-pane screen has no pane beside the
	// conversation to be told apart from and a message of this user
	// takes most of it there, so the margin is one column: a margin the
	// width of the chat list beside it would be a margin that makes the
	// message smaller for no reason.
	feedMargin       = 3
	feedMarginNarrow = 1

	// blockInset is the space inside the block of a message on each side
	// of its text.
	//
	// Two columns is the air inside the block, and it is two at every
	// width: text flush against the background of its own block reads as
	// a highlight rather than as a message, and a single column of air
	// reads as a mistake on the way to the same thing. A single-pane
	// screen is narrower and gets the same two, because the block there
	// is already as wide as the feed allows and the inset is the only
	// thing between the words and the edge of the screen.
	blockInset = 2

	// blockMinTextColumns is the narrowest a block of a message may be
	// inside, its insets and its rounded ends not counted.
	//
	// A block that is as wide as its words is the right answer for a
	// message and the wrong one for a word: "ok" in a block of six columns
	// is a sliver, and a sliver is a shape a user has to aim at. Sixteen
	// columns is about two ordinary words with the space around them, which
	// is where a block stops being a mark on the screen and starts being
	// something with words in it. The lines under the text — the name of
	// whoever sent it, the state of a message of this user — are as wide as
	// they are whatever this is, so the floor never cuts a name or a state
	// short to reach it.
	blockMinTextColumns = 16

	// chatListInset is the air inside a row of the chat list on each side
	// of its words, and chatListInsetNarrow the same on a single-pane
	// screen.
	//
	// A name, a preview, a time and a count that touch the edge of the
	// pane are a list the pane is wearing rather than a list of chats: the
	// words have nothing between them and the edge, and the eye reads the
	// edge of the window as part of the text. Two columns is that air on a
	// two-pane screen, which has room for it, and one on a single-pane one,
	// where the list is the whole screen and a column of air is a fifth of
	// a name.
	//
	// The air is inside the row rather than outside it, so the Selected of
	// a chosen chat covers it: the card is a band of colour with words in
	// it, and a band that stops two columns short on each side is a stripe
	// with an outline.
	chatListInset       = 2
	chatListInsetNarrow = 1
)

// ChatListInset returns the air inside a row of the chat list on each side
// of its words, which is two columns on a two-pane screen and one on a
// single-pane one.
func (l Layout) ChatListInset() int {
	if l.Kind == LayoutNarrow {
		return chatListInsetNarrow
	}

	return chatListInset
}

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
		return wideSidebarWidth(l.Width)
	case LayoutMedium:
		return mediumSidebarWidth
	default:
		return 0
	}
}

// wideSidebarWidth is the width of the chat list on a screen of the given
// width: a quarter of it, kept between wideSidebarLeast and
// wideSidebarMost.
func wideSidebarWidth(width int) int {
	share := width * wideSidebarSharePercent / 100

	return minInt(maxInt(share, wideSidebarLeast), wideSidebarMost)
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

// FeedMargin returns the space the feed of a conversation keeps on each of
// its sides, which is where the selection marker of a message stands and
// where a message of this user ends.
//
// It is a method and not a constant because a single-pane screen gives it
// a column where a two-pane one gives it three, and the two answers have
// to come from the same place as every other number that decides a shape.
func (l Layout) FeedMargin() int {
	if l.Kind == LayoutNarrow {
		return feedMarginNarrow
	}

	return feedMargin
}

// BlockInset returns the space inside the block of a message on each
// side of its text, which is the same at every width.
func (l Layout) BlockInset() int { return blockInset }

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

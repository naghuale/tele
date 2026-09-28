package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"telecli/internal/tui/termwidth"
	"telecli/internal/tui/theme"
)

// The tests in this file are about the shape of the screen rather than
// about its contents: that the two panes add up to the width, that
// nothing is framed, and that exactly one region carries the accent.
// The names are the ones §21 of the specification reserves for them.

// boxDrawing is every glyph a frame is made of.
//
// §1 forbids frames: structure comes from the surfaces of the theme, and a
// box around a region would say "this is a box" where the interface is
// supposed to say "this is a chat list". A test has to name the
// characters it forbids, or it only forbids the ones somebody thought of.
// The heavy rule of the focused panel is not in this list: it is the mark
// of the focus and not a frame, and the tests below check for it by name.
const boxDrawing = "─│┌┐└┘├┤┬┴┼┃╭╮╰╯║═╔╗╚╝╠╣╦╩╬╞╡"

// uncolored rebuilds a model for a terminal that shows no colour.
//
// It is the state the program is in with NO_COLOR, --no-color, TERM=dumb
// or `color = "never"`: the composition root degrades the theme for the
// profile, so the tokens have no colour left in them, and the renderer
// prints neither colour nor attributes. Anything that was carried by
// colour alone is gone with it, which is why a test that only changes the
// profile on a full-colour theme proves nothing about this case.
func uncolored(m Model) Model {
	m.colorProfile = theme.ProfileNoColor
	m.theme = m.theme.ForProfile(theme.ProfileNoColor)
	m.rendererForProfile = nil

	return m
}

// sizedModel returns a model on the chat list of a given size.
func sizedModel(t *testing.T, width, height int) Model {
	t.Helper()

	m := NewModel()
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: width, Height: height})

	return m
}

// openedModel returns a model with the first chat open, at a given size.
func openedModel(t *testing.T, width, height int) Model {
	t.Helper()

	m := sizedModel(t, width, height)
	m, _ = updateModel(t, m, press(tea.KeyEnter))

	return m
}

// viewLines splits a rendered screen into its lines.
func viewLines(view string) []string {
	return strings.Split(view, "\n")
}

// panelRuleLines returns the indexes of the rows that carry the rule of a
// focused panel.
func panelRuleLines(lines []string) []int {
	var found []int

	for index, line := range lines {
		if strings.Contains(line, focusRuleGlyph) {
			found = append(found, index)
		}
	}

	return found
}

// assertPanelRule fails unless the screen draws exactly one rule, and it
// is the row under the heading of the pane that has the keys.
//
// §5.2 is one focused region at a time, and the mark of a region is a
// rule under its header. Two rules would be two claims on the keys, and a
// user cannot choose between two regions that both say they have them.
//
// A screen with no conversation on it — the composer-only screen of §3.4,
// which has no header to put a rule under — is the one case where there is
// no rule at all, and it is said here rather than excused in every test.
func assertPanelRule(t *testing.T, m Model, want pane) {
	t.Helper()

	view := m.View()
	lines := viewLines(view)
	found := panelRuleLines(lines)

	layout := LayoutFor(m.width, m.height)
	if !layout.TwoPane() && layout.ComposerOnly() {
		if len(found) != 0 {
			t.Fatalf("a composer-only screen draws a panel rule:\n%s", view)
		}

		return
	}

	if len(found) != 1 {
		t.Fatalf("the screen draws %d panel rules, want 1:\n%s", len(found), view)
	}

	// The rule belongs to the pane the focus is in, and it sits on the row
	// directly under that pane's heading, which is the first row of the
	// screen. The search line is above the list's heading, so the list's
	// rule is one row further down with it open.
	wantRow := 1
	if want == listPane && m.chatSearch.open {
		wantRow = searchRegionHeight + 1
	}
	if found[0] != wantRow {
		t.Fatalf(
			"the rule is on row %d, want row %d under the heading of the %v pane:\n%s",
			found[0],
			wantRow,
			want,
			view,
		)
	}
}

// TestOnlyFocusedRegionHasAccentLine is the invariant the whole focus
// design rests on (§5.2): one region is focused, so exactly one pane of
// the screen carries the accent. Two would leave a user choosing between
// two regions that both claim to have the keys.
func TestOnlyFocusedRegionHasAccentLine(t *testing.T) {
	cases := map[string]struct {
		model Model
		want  pane
	}{
		"chat list":        {model: sizedModel(t, 120, 30), want: listPane},
		"composer focused": {model: openedModel(t, 120, 30), want: conversationPane},
		"timeline focused": {model: focusedOn(openedModel(t, 120, 30), FocusHistory), want: conversationPane},
		"list beside a chat": {
			model: focusedOn(openedModel(t, 120, 30), FocusChatList),
			want:  listPane,
		},
		"narrow conversation": {model: openedModel(t, 60, 30), want: conversationPane},
		"narrow list":         {model: sizedModel(t, 60, 30), want: listPane},
		"short conversation":  {model: openedModel(t, 120, 12), want: conversationPane},
		"composer only":       {model: openedModel(t, 120, 5), want: conversationPane},
		"search": {
			model: searching(t, sizedModel(t, 120, 30)),
			want:  listPane,
		},
		"search beside a chat": {
			model: searching(t, focusedOn(openedModel(t, 120, 30), FocusChatList)),
			want:  listPane,
		},
		"narrow search": {
			model: searching(t, sizedModel(t, 60, 30)),
			want:  listPane,
		},
		"uncoloured": {
			model: uncolored(openedModel(t, 120, 30)),
			want:  conversationPane,
		},
		"uncoloured list": {
			model: uncolored(focusedOn(sizedModel(t, 120, 30), FocusChatList)),
			want:  listPane,
		},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			assertPanelRule(t, testCase.model, testCase.want)
		})
	}
}

// searching returns a model with the search over the chat list open and
// the keys in it.
func searching(t *testing.T, m Model) Model {
	t.Helper()

	m, _ = updateModel(t, m, pressRunes("/"))
	m, _ = updateModel(t, m, pressRunes("dev"))

	return m
}

// focusedOn returns a model with the focus on a region.
func focusedOn(m Model, focus Focus) Model {
	m.focus = focus

	return m
}

// The focus is a bar in the first column of the region, not a border
// around it: a frame would draw three sides the interface has no use for
// and would put a line under the last message of a conversation.
func TestChatLayoutContainsNoFullBorders(t *testing.T) {
	cases := map[string]Model{
		"chat list":           sizedModel(t, 120, 30),
		"conversation":        openedModel(t, 120, 30),
		"narrow conversation": openedModel(t, 60, 30),
		"short conversation":  openedModel(t, 120, 12),
	}

	for name, model := range cases {
		t.Run(name, func(t *testing.T) {
			view := model.View()
			for _, glyph := range boxDrawing {
				if strings.ContainsRune(view, glyph) {
					t.Fatalf("view contains the box glyph %q:\n%s", glyph, view)
				}
			}
		})
	}
}

// The two panes are separated by a column of space, not by a line
// (§1). A vertical rule down a chat application is a table border, and
// the chat list is not a column of a table.
func TestSidebarContainsNoRightBorder(t *testing.T) {
	m := openedModel(t, 120, 30)
	layout := LayoutFor(m.width, m.height)

	view := m.View()
	for index, line := range viewLines(view) {
		if m.widths.StringWidth(line) < layout.SidebarWidth() {
			continue
		}

		// The column after the sidebar is the gap, and the gap is a space.
		// A border glyph here would be a vertical rule drawn between the
		// panes.
		gap := line[layout.SidebarWidth():]
		if !strings.HasPrefix(gap, " ") {
			t.Fatalf(
				"line %d has %q after the sidebar, want a space:\n%s",
				index,
				gap[:minInt(2, m.widths.StringWidth(gap))],
				view,
			)
		}
	}
}

// The composer is a band across the width of the conversation and nothing
// else: a box around the place a message is written would put a second
// line under the messages and a line above the hint bar, both of which
// the screen does not have room for and neither of which says anything
// extra. What says where the keys are is the rule over the header of the
// pane and the `›` in front of the draft.
func TestComposerIsABandAndNoFrame(t *testing.T) {
	m := openedModel(t, 120, 30)
	m.focus = FocusComposer

	view := m.View()
	composer := composerLineOf(view)
	if composer == "" {
		t.Fatalf("view has no composer line:\n%s", view)
	}

	if strings.ContainsRune(composer, firstRune(theme.FocusBar)) {
		t.Fatalf("the composer carries a focus bar: %q", composer)
	}
	if !strings.ContainsRune(composer, firstRune(composerPrompt)) {
		t.Fatalf("the composer has no prompt: %q", composer)
	}
	if m.widths.StringWidth(composer) != m.width {
		t.Fatalf("composer line is %d columns, want %d", m.widths.StringWidth(composer), m.width)
	}
}

// composerLineOf returns the line of the screen that holds the composer.
func composerLineOf(view string) string {
	for _, line := range viewLines(view) {
		if strings.Contains(line, composerPlaceholder) {
			return line
		}
	}

	return ""
}

// A terminal that shows no colour still has to say where the keys go
// (§2.7). The rule is a character and not a colour in the first place, so
// it survives the profile that throws colour away, and the theme loses its
// colours on the way there: this is the state a user with `color =
// "never"`, a NO_COLOR environment variable or a TERM=dumb terminal is in.
func TestNoColorPreservesFocusIndicator(t *testing.T) {
	conversation := uncolored(openedModel(t, 120, 30))
	assertPanelRule(t, conversation, conversationPane)

	// The chat list is where the selection is, and a selection carried by
	// a bold attribute alone disappears with the attributes: termenv's
	// Ascii profile prints none of them. The `›` in its own column is
	// what is left.
	list := uncolored(focusedOn(sizedModel(t, 120, 30), FocusChatList))
	assertPanelRule(t, list, listPane)

	if _, _, ok := lineWith(plain(list.View()), theme.SelectionMark); !ok {
		t.Fatalf("the selected chat has no marker without colour:\n%s", plain(list.View()))
	}
}

// The smallest supported screen still draws a conversation, because the
// composer is what a conversation is for (§3.4).
func TestLayoutAtMinimumSupportedWidth(t *testing.T) {
	m := openedModel(t, minWidth, minHeight)

	lines := viewLines(m.View())
	if len(lines) != minHeight {
		t.Fatalf("lines = %d, want %d", len(lines), minHeight)
	}

	for index, line := range lines {
		if got := m.widths.StringWidth(line); got != minWidth {
			t.Fatalf("line %d is %d columns, want %d: %q", index, got, minWidth, line)
		}
	}

	if !strings.Contains(m.View(), composerPlaceholder) {
		t.Fatalf("minimum screen has no composer:\n%s", m.View())
	}
}

// A wide screen is two panes of a known width, and every line of it adds
// up to the width of the terminal. A line one column short leaves a seam;
// one column over wraps the screen and pushes the composer away.
func TestLayoutAtWideWidth(t *testing.T) {
	const width, height = 120, 30

	m := openedModel(t, width, height)
	layout := LayoutFor(width, height)

	if got, want := layout.SidebarWidth(), width*wideSidebarSharePercent/100; got != want {
		t.Fatalf("sidebar = %d, want %d", got, want)
	}
	if got := layout.SidebarWidth() + paneGapWidth + layout.ChatWidth(); got != width {
		t.Fatalf(
			"sidebar %d + gap %d + chat %d = %d, want %d",
			layout.SidebarWidth(),
			paneGapWidth,
			layout.ChatWidth(),
			got,
			width,
		)
	}

	lines := viewLines(m.View())
	if len(lines) != height {
		t.Fatalf("lines = %d, want %d", len(lines), height)
	}

	for index, line := range lines {
		if got := m.widths.StringWidth(line); got != width {
			t.Fatalf("line %d is %d columns, want %d: %q", index, got, width, line)
		}
	}
}

// Widths are counted in terminal columns. Content that is not ASCII is
// the rule, not the exception, so a layout measured in runes would
// overflow on the first non-Latin name a user has.
func TestLayoutWithCyrillic(t *testing.T) {
	m := openedModel(t, 100, 24)
	m.chats[m.selectedChat].Title = "Привет из Москвы"
	m.chats[m.selectedChat].Messages = []Message{
		{ID: 1, Text: "Как дела? Всё хорошо, thanks", Outgoing: true},
		{ID: 2, Text: "Отлично, увидимся в четверг"},
	}

	assertRectangularView(t, m)
}

func TestLayoutWithEmoji(t *testing.T) {
	m := openedModel(t, 100, 24)
	m.chats[m.selectedChat].Title = "Team 🌍 Standup"
	m.chats[m.selectedChat].Messages = []Message{
		{ID: 1, Text: "shipped 🚀🎉", Outgoing: true},
		{ID: 2, Text: "congrats 👏"},
	}

	assertRectangularView(t, m)
}

// A wide character that no single column holds is the case a rune count
// gets wrong twice over.
func TestLayoutWithWideUnicode(t *testing.T) {
	m := openedModel(t, 100, 24)
	m.chats[m.selectedChat].Title = "🧑‍🚀🧑‍🚀🧑‍🚀 family"
	m.chats[m.selectedChat].Messages = []Message{
		{ID: 1, Text: "👨‍👩‍👧‍👦👨‍👩‍👧‍👦👨‍👩‍👧‍👦 the whole clan is here"},
	}

	assertRectangularView(t, m)
}

// assertRectangularView fails unless every line of the screen is exactly
// as wide as the terminal.
func assertRectangularView(t *testing.T, m Model) {
	t.Helper()

	view := m.View()
	for index, line := range viewLines(view) {
		if got, want := m.widths.StringWidth(line), m.width; got != want {
			t.Fatalf(
				"line %d is %d columns, want %d: %q",
				index,
				got,
				want,
				line,
			)
		}
	}
}

// A resize redraws the screen from the new size. What is left of the old
// one would be a fragment of an accent bar in a column that is now the
// middle of a pane, which is the one thing a user cannot unsee.
func TestResizeDoesNotLeaveBorderFragments(t *testing.T) {
	m := openedModel(t, 120, 30)

	for _, size := range [][2]int{{120, 30}, {60, 30}, {120, 12}, {40, 5}, {80, 24}} {
		m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		view := m.View()

		for _, glyph := range boxDrawing {
			if strings.ContainsRune(view, glyph) {
				t.Fatalf("view at %dx%d contains %q:\n%s", size[0], size[1], glyph, view)
			}
		}

		for index, line := range viewLines(view) {
			if got := m.widths.StringWidth(line); got != size[0] {
				t.Fatalf(
					"line %d at %dx%d is %d columns: %q",
					index,
					size[0],
					size[1],
					got,
					line,
				)
			}
		}
	}
}

// A long title is the one piece of content the interface does not control
// and cannot ask to be shorter. It is cut to its own pane, and the pane
// beside it does not move: a composer that shifts while a user is typing
// in it is a composer they have to look at again.
func TestLongChatTitleDoesNotShiftComposer(t *testing.T) {
	short := openedModel(t, 120, 30)
	before := composerColumnOf(short.widths, short.View())

	long := openedModel(t, 120, 30)
	long.chats[long.selectedChat].Title = strings.Repeat("очень длинное название чата ", 6)
	after := composerColumnOf(long.widths, long.View())

	if before != after {
		t.Fatalf("composer moved from column %d to %d", before, after)
	}
}

// composerColumnOf returns the column the composer starts in.
func composerColumnOf(widths termwidth.WidthModel, view string) int {
	composer := composerLineOf(view)
	if composer == "" {
		return -1
	}

	return widths.StringWidth(
		composer[:strings.Index(composer, composerPlaceholder)],
	)
}

// The chat list is a quarter of a wide screen, kept between a name long
// enough to be somebody's and a list wide enough to be a list. A pane
// narrower than the least cuts every name in it while the conversation
// beside it has empty columns, and one wider than the most takes columns
// the words of a message need.
func TestTheWideChatListIsAQuarterOfTheWidthAndNoMore(t *testing.T) {
	for _, width := range []int{
		wideLayoutMinWidth, 110, 120, 140, 160, 200, 400,
	} {
		sidebar := LayoutFor(width, 30).SidebarWidth()

		if sidebar < wideSidebarLeast {
			t.Errorf("width %d: the list is %d columns, want at least %d",
				width, sidebar, wideSidebarLeast)
		}
		if sidebar > wideSidebarMost {
			t.Errorf("width %d: the list is %d columns, want at most %d",
				width, sidebar, wideSidebarMost)
		}

		share := width * wideSidebarSharePercent / 100
		if sidebar != minInt(maxInt(share, wideSidebarLeast), wideSidebarMost) {
			t.Errorf("width %d: the list is %d columns, want a quarter of it",
				width, sidebar)
		}
	}
}

// A medium screen keeps the fixed list of §10.3: a share of the width
// there would leave the conversation too narrow to read a message in.
func TestTheMediumChatListKeepsItsFixedWidth(t *testing.T) {
	for _, width := range []int{mediumLayoutMinWidth, 80, wideLayoutMinWidth - 1} {
		if got := LayoutFor(width, 30).SidebarWidth(); got != mediumSidebarWidth {
			t.Errorf("width %d: the list is %d columns, want %d",
				width, got, mediumSidebarWidth)
		}
	}
}

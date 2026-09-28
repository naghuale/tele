package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"telecli/internal/tui/theme"
)

// The composer in rows.
//
// §4.5: one row for a draft that fits, more as it grows, four at most and
// never more than a third of the screen, with the row of the cursor always
// on screen. A draft longer than the field is a normal thing to have typed,
// so the scrolling and the cap are what these tests are about.

// composerRowsOf returns how many rows the draft takes on the screen.
func composerRowsOf(m Model) int {
	layout := LayoutFor(m.width, m.height)

	return m.composerRowCount(layout, layout.ChatContentWidth())
}

// composerModel returns a conversation with a draft in it, at a given size.
func composerModel(t *testing.T, width, height int, lines ...string) Model {
	t.Helper()

	m := openedProgramModel(t, theme.ProfileNoColor, width, height)
	for _, line := range lines {
		if line == "\n" {
			m, _ = updateModel(t, m, alt(tea.KeyEnter))
			continue
		}
		m, _ = updateModel(t, m, pressRunes(line))
	}

	return m
}

// The field is one row for a draft that fits in one.
func TestTheComposerIsOneRowForOneLine(t *testing.T) {
	m := composerModel(t, 100, 24, "hello")

	if got := composerRowsOf(m); got != 1 {
		t.Fatalf("composer rows = %d, want 1", got)
	}
}

// It grows with the draft, one row per line of it.
func TestTheComposerGrowsWithTheDraft(t *testing.T) {
	for _, want := range []int{1, 2, 3, 4} {
		lines := make([]string, 0, want)
		for range want {
			lines = append(lines, "line", "\n")
		}

		m := composerModel(t, 100, 24, lines[:len(lines)-1]...)

		if got := composerRowsOf(m); got != want {
			t.Fatalf("composer rows = %d, want %d", got, want)
		}
	}
}

// Four rows is the cap of §4.5, and the field scrolls inside them with the
// row of the cursor last, so that what is being written is on screen.
func TestTheComposerStopsAtFourRowsAndScrolls(t *testing.T) {
	m := composerModel(
		t, 100, 24,
		"one", "\n", "two", "\n", "three", "\n", "four", "\n", "five",
	)

	if got := composerRowsOf(m); got != 4 {
		t.Fatalf("composer rows = %d, want 4", got)
	}

	view := plain(m.View())
	if strings.Contains(view, " one") {
		t.Fatalf("the first line of the draft is still drawn:\n%s", view)
	}
	for _, want := range []string{"two", "three", "four", "five"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the row %q is not on the screen:\n%s", want, view)
		}
	}
}

// The cursor row is the one that stays on the screen: a field that scrolls
// past what is being written is a field a user types into blind.
func TestTheRowWithTheCursorIsAlwaysOnScreen(t *testing.T) {
	m := composerModel(
		t, 100, 24,
		"one", "\n", "two", "\n", "three", "\n", "four", "\n", "five",
	)

	for range 4 {
		m, _ = updateModel(t, m, press(tea.KeyUp))

		row := cursorRowText(m)
		if row == "" {
			t.Fatalf("the cursor is on no row of the draft")
		}

		// The cursor bar sits inside the row it marks, so it is taken out
		// before looking for the text of the row: a row with the cursor in
		// the middle of it is not the text of that row.
		view := strings.ReplaceAll(plain(m.View()), cursorBar, "")
		if !strings.Contains(view, row) {
			t.Fatalf("the cursor row %q is not on the screen:\n%s", row, view)
		}
	}
}

// cursorRowText returns the text of the row the cursor is on.
func cursorRowText(m Model) string {
	layout := LayoutFor(m.width, m.height)
	laid := m.layoutComposer(
		m.composer,
		m.composerCursor,
		m.composerTextWidth(layout.ChatContentWidth()),
	)

	return laid.rows[laid.cursorRow].text
}

// A third of the screen is the other cap of §4.5, and it is the one that
// bites on a short screen: a four-row field on a ten-row screen would be
// most of the conversation.
func TestTheComposerTakesAtMostAThirdOfTheScreen(t *testing.T) {
	m := composerModel(
		t, 100, 10,
		"one", "\n", "two", "\n", "three", "\n", "four", "\n", "five",
	)

	limit := maxInt(int(10*composerMaxShare), 1)
	if got := composerRowsOf(m); got > limit {
		t.Fatalf("composer rows = %d, want at most %d", got, limit)
	}
}

// Nothing the composer draws is wider than the pane, whatever the draft
// holds: a line one column over wraps the screen and pushes the timeline
// away.
func TestNoRowOfTheComposerIsWiderThanTheScreen(t *testing.T) {
	for _, size := range [][2]int{{120, 30}, {80, 24}, {60, 20}, {40, 12}} {
		m := composerModel(
			t, size[0], size[1],
			"Очень длинный текст 🌍 с кириллицей и эмодзи, который переносится",
			"\n",
			"и вторая строка, которая тоже довольно длинная",
		)

		for index, line := range viewLines(m.View()) {
			if got := m.widths.StringWidth(line); got > m.width {
				t.Fatalf(
					"at %dx%d row %d is %d columns, want at most %d: %q",
					size[0],
					size[1],
					index,
					got,
					m.width,
					line,
				)
			}
		}
	}
}

// The placeholder explains Enter on a screen with no hint bar, because a
// composer that can only be understood by keys that are not on the screen
// is a composer nobody can use (§3.4, §7.3).
func TestThePlaceholderExplainsEnterWhenTheHintsAreHidden(t *testing.T) {
	m := openedProgramModel(t, theme.ProfileNoColor, 100, 9)
	m.focus = FocusComposer

	view := plain(m.View())
	if !strings.Contains(view, composerPlaceholderShort) {
		t.Fatalf("the placeholder does not explain Enter:\\n%s", view)
	}
	if strings.Contains(view, composerPlaceholder+"\n") {
		t.Fatalf("the short placeholder still uses the short form:\\n%s", view)
	}
}

// With the hints on screen the placeholder is the short sentence, and the
// hint bar names the keys.
func TestThePlaceholderIsShortWhenTheHintsAreThere(t *testing.T) {
	m := openedProgramModel(t, theme.ProfileNoColor, 100, 24)
	m.focus = FocusComposer

	view := plain(m.View())
	if !strings.Contains(view, composerPlaceholder) {
		t.Fatalf("the placeholder is missing:\\n%s", view)
	}
	if !strings.Contains(view, "Alt+Enter newline") {
		t.Fatalf("the hint bar does not name the newline key:\\n%s", view)
	}
}

// The lit placeholder is a style change and nothing more: the words stay
// and the field stays empty.
func TestTheLitPlaceholderChangesTheStyleAndNothingElse(t *testing.T) {
	lit := openedProgramModel(t, theme.ProfileNoColor, 100, 24)
	lit.focus = FocusComposer
	lit, _ = updateModel(t, lit, press(tea.KeyEnter))

	if !lit.composerPlaceholderLit {
		t.Fatal("the placeholder was not lit")
	}

	plainView := plain(lit.View())
	if !strings.Contains(plainView, composerPlaceholder) {
		t.Fatalf("the lit placeholder says something else:\\n%s", plainView)
	}
	if !strings.Contains(lit.View(), cursorBar) {
		t.Fatal("an empty composer has no cursor to light")
	}
}

// The composer is the last thing on the screen: §3.4 requires it to be
// visible whenever a conversation is open, whatever the draft holds.
func TestTheComposerSurvivesALongDraftOnAShortScreen(t *testing.T) {
	m := composerModel(
		t, 100, 8,
		"one", "\n", "two", "\n", "three", "\n", "four", "\n", "five",
	)

	view := plain(m.View())
	if !strings.Contains(view, "five") {
		t.Fatalf("the cursor row is gone:\\n%s", view)
	}
	if _, ok := lineIndexWith(view, cursorBar); !ok {
		t.Fatalf("the composer has no cursor:\\n%s", view)
	}
}

// On the composer-only screen of §3.4 the field is the whole screen, and
// the draft still gets a cursor in it.
func TestTheComposerOnlyScreenIsTheField(t *testing.T) {
	m := composerModel(t, 100, 5, "hi")

	view := plain(m.View())
	if !strings.Contains(view, cursorBar) {
		t.Fatalf("the composer-only screen has no cursor:\\n%s", view)
	}
	if got := len(viewLines(view)); got != 5 {
		t.Fatalf("rows = %d, want 5", got)
	}
}

// The draft is drawn as it is, and never with a wrap inserted into it: what
// goes to the queue is what was typed.
func TestWrappingDoesNotChangeTheDraft(t *testing.T) {
	m := composerModel(
		t, 60, 20,
		"Очень длинный текст, который точно перенесется по ширине области",
	)

	if !strings.Contains(m.Composer(), "перенесется") {
		t.Fatalf("composer = %q, want the draft as typed", m.Composer())
	}
	if strings.Contains(m.Composer(), "\n") {
		t.Fatalf("composer = %q, want no wrap written into the draft", m.Composer())
	}

	// The draft is shown in full, whatever the field happened to break it
	// at: a word split across two rows is a normal thing to have typed,
	// and what must not happen is a row of it that is not on the screen.
	// The comparison is letter by letter, because the field may have cut
	// a word in half between two rows and that is not a loss of it.
	rows := drawnComposerRows(plain(m.View()))

	var drawn strings.Builder
	for _, row := range rows {
		text := row
		if _, after, found := strings.Cut(text, composerPrompt); found {
			text = after
		}

		drawn.WriteString(strings.ReplaceAll(
			strings.ReplaceAll(text, cursorBar, ""), " ", "",
		))
	}

	if want := strings.ReplaceAll(m.Composer(), " ", ""); !strings.Contains(
		drawn.String(), want,
	) {
		t.Fatalf(
			"the screen lost part of the draft %q:\n%s",
			want,
			strings.Join(rows, "\n"),
		)
	}
}

// drawnComposerRows returns the rows of the screen the draft is drawn in.
func drawnComposerRows(view string) []string {
	var rows []string

	for _, line := range viewLines(view) {
		if strings.Contains(line, composerPrompt) ||
			strings.Contains(line, cursorBar) {
			rows = append(rows, line)
		}
	}

	return rows
}

// The cursor is drawn on a cell, and a cell is a grapheme cluster.
//
// Editing moved over clusters, so drawing has to as well: a cursor style
// in the middle of a ZWJ emoji does not mark it, it breaks it into pieces
// the terminal then draws one after another.
func TestTheCursorIsDrawnOnAWholeGrapheme(t *testing.T) {
	const family = "👨‍👩‍👧"

	cases := map[string]struct {
		profile     theme.Profile
		profileName string
	}{
		"true color": {profile: theme.ProfileTrueColor, profileName: "true color"},
		"no colour":  {profile: theme.ProfileNoColor, profileName: "no colour"},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			m := openedProgramModel(t, testCase.profile, 100, 24)
			m.focus = FocusComposer
			m, _ = updateModel(t, m, pressRunes("вторая "+family))

			// The cursor at the end of a draft that ends in a cluster is a
			// bar after it, whatever the cluster is.
			row := composerRowOf(t, m, "вторая")
			if !strings.HasSuffix(strings.TrimRight(plain(row), " "), cursorBar) {
				t.Fatalf("the row does not end with the cursor: %q", plain(row))
			}

			// One step back puts the cursor on the cluster, and the whole
			// cluster has to be inside one style run: an escape sequence in
			// the middle of it is the terminal drawing it in pieces.
			m, _ = updateModel(t, m, press(tea.KeyLeft))

			styled := composerRowOf(t, m, "вторая")
			if !cursorOnCluster(t, styled, family) {
				t.Fatalf(
					"the cluster is not inside one style run: %q",
					styled,
				)
			}

			for index, line := range viewLines(m.View()) {
				if got := m.widths.StringWidth(line); got > m.width {
					t.Fatalf(
						"row %d is %d columns, want at most %d",
						index,
						got,
						m.width,
					)
				}
			}
		})
	}
}

// A letter and a combining accent are one cell, and the cursor style must
// not come between them either: the mark belongs to the letter on the
// screen and it belongs to it in the buffer.
func TestTheCursorDoesNotSplitACombiningMark(t *testing.T) {
	const marked = "и" + "́"

	m := openedProgramModel(t, theme.ProfileTrueColor, 100, 24)
	m.focus = FocusComposer
	m, _ = updateModel(t, m, pressRunes("да "+marked))
	m, _ = updateModel(t, m, press(tea.KeyLeft))

	if !cursorOnCluster(t, composerRowOf(t, m, "да "), marked) {
		t.Fatalf("the letter and its mark are in different runs: %q", composerRowOf(t, m, "да "))
	}
}

// A terminal that prints no attributes prints no reverse either, so the
// cursor has to be a character there: a bar in front of the cell it is at,
// and the row gives up the column it takes.
func TestNoColorDrawsTheCursorAsACharacter(t *testing.T) {
	m := openedProgramModel(t, theme.ProfileNoColor, 100, 24)
	m.focus = FocusComposer
	m, _ = updateModel(t, m, pressRunes("abc"))

	m, _ = updateModel(t, m, press(tea.KeyLeft))
	if row := plain(composerRowOf(t, m, "ab")); !strings.Contains(row, "ab"+cursorBar+"c") {
		t.Fatalf("the cursor is not drawn in the middle: %q", row)
	}

	m, _ = updateModel(t, m, press(tea.KeyHome))
	if row := plain(composerRowOf(t, m, "abc")); !strings.Contains(row, cursorBar+"abc") {
		t.Fatalf("the cursor is not drawn at the start: %q", row)
	}

	for index, line := range viewLines(m.View()) {
		if got := m.widths.StringWidth(line); got > m.width {
			t.Fatalf("row %d is %d columns, want at most %d", index, got, m.width)
		}
	}
}

// composerRowOf returns the rendered composer row that holds a piece of
// the draft.
//
// It is found by what is in the row and not by the row of the layout: the
// cursor bar comes between the parts of a row, so the text of the row is
// not a substring of the line that draws it.
func composerRowOf(t *testing.T, m Model, want string) string {
	t.Helper()

	for _, line := range viewLines(m.View()) {
		if strings.Contains(plain(line), want) {
			return line
		}
	}

	t.Fatalf("the composer row with %q is not on the screen:\n%s", want, plain(m.View()))

	return ""
}

// cursorOnCluster reports whether a whole cluster sits inside one style
// run of a rendered row, which is what "the cursor is on it" means once
// the escape sequences are stripped.
func cursorOnCluster(t *testing.T, rendered, cluster string) bool {
	t.Helper()

	for _, run := range splitStyleRuns(rendered) {
		if strings.Contains(run, cluster) {
			return true
		}
	}

	return false
}

// splitStyleRuns returns the pieces of a rendered row between the escape
// sequences, which is how a terminal reads it.
func splitStyleRuns(rendered string) []string {
	var (
		runs    []string
		current strings.Builder
	)

	for index := 0; index < len(rendered); index++ {
		if rendered[index] == 0x1b {
			if current.Len() > 0 {
				runs = append(runs, current.String())
				current.Reset()
			}

			for index < len(rendered) && rendered[index] != 'm' {
				index++
			}

			continue
		}
		current.WriteByte(rendered[index])
	}

	if current.Len() > 0 {
		runs = append(runs, current.String())
	}

	return runs
}

package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// The search field is the row the "/ search" hint was on, and the search
// takes nothing else.
//
// The owner's screen of 01.10 showed a field above the header of the list
// with the hint still under the header, and everything below moved down a
// row to make room. A search that pushes the list down while it is being
// typed is a search whose results move while the query is written, and the
// results are what the user is watching. So the hint row itself becomes
// the field: the same row, the same height, the header above it and the
// list below it exactly where they were.
func TestTheSearchFieldIsTheRowTheHintWasOn(t *testing.T) {
	for _, size := range [][2]int{{176, 43}, {120, 24}, {80, 24}, {72, 20}} {
		closed := searchable(t, size[0], size[1])
		open, _ := updateModel(t, closed, pressRunes("/"))

		closedRows := viewLines(plain(closed.View()))
		openRows := viewLines(plain(open.View()))

		if len(openRows) != len(closedRows) {
			t.Errorf(
				"at %dx%d the screen is %d rows with the search open and %d with it closed",
				size[0], size[1], len(openRows), len(closedRows),
			)

			continue
		}

		// The second row of the screen is the second row of the header of
		// the list: it carries the "/ search" hint on a wide screen and the
		// status of the connection on a narrow one. It is the row the field
		// takes, and it is the same row whichever of the two it was.
		if !strings.Contains(openRows[searchRow], cursorBar) {
			t.Errorf(
				"at %dx%d row %d has no cursor in it:\n%q",
				size[0], size[1], searchRow+1, openRows[searchRow],
			)
		}
		if openRows[searchRow] == closedRows[searchRow] {
			t.Errorf(
				"at %dx%d row %d is the same with the search open as with it closed:\n%q",
				size[0], size[1], searchRow+1, openRows[searchRow],
			)
		}

		// The field names itself, in full: the narrowest list cannot afford
		// "Search chats" and says the words the row said before.
		want := []string{searchPlaceholder, chatListSearchHint}
		if size[0] >= 100 {
			want = want[:1]
		}
		if !containsAny(openRows[searchRow], want...) {
			t.Errorf(
				"at %dx%d the field on row %d names neither the thing nor its own keys:\n%q",
				size[0], size[1], searchRow+1, openRows[searchRow],
			)
		}

		// Nothing above the field moves: the heading of the pane is the row
		// it was, with and without the search open.
		for row := range searchRow {
			if openRows[row] != closedRows[row] {
				t.Errorf(
					"at %dx%d row %d moved:\nwith the search:\n%s\nwithout it:\n%s",
					size[0], size[1], row+1, openRows[row], closedRows[row],
				)
			}
		}
	}
}

// searchRow is the row of the screen the search field is drawn on: the
// second row of the header of the chat list, under its title.
const searchRow = 1

// The keys are in the field, so the cursor is on its row: the cursor bar of
// the field is drawn where the hint's words were.
func TestTheCursorIsOnTheRowOfTheSearchField(t *testing.T) {
	m := searchable(t, 120, 24)
	m, _ = updateModel(t, m, pressRunes("/"))

	if m.focus != FocusSearch {
		t.Fatalf("focus = %v, want FocusSearch", m.focus)
	}

	rows := viewLines(plain(m.View()))
	for row, line := range rows {
		if !strings.Contains(line, searchPlaceholder) {
			continue
		}

		if !strings.Contains(line, cursorBar) {
			t.Errorf("row %d has the field without a cursor:\n%q", row+1, line)
		}

		return
	}

	t.Fatalf("the field is not on the screen:\n%s", plain(m.View()))
}

// The row of the hint is the row of the field again when the search is
// closed, whether it is closed with Esc or was never opened at all.
func TestTheFieldBecomesTheHintAgainWhenTheSearchCloses(t *testing.T) {
	m := typing(t, searchable(t, 120, 24), "dev")

	if !strings.Contains(plain(m.View()), "dev") {
		t.Fatalf("the query is not on the screen:\n%s", plain(m.View()))
	}

	m, _ = updateModel(t, m, press(tea.KeyEsc))

	view := plain(m.View())
	if strings.Contains(view, searchPlaceholder) {
		t.Errorf("the field is still on the screen after Esc:\n%s", view)
	}
	if !strings.Contains(view, chatListSearchHint) {
		t.Errorf("the hint did not come back after Esc:\n%s", view)
	}
}

// An empty field and Esc close the search too: Esc is the way out of
// whatever is on the screen, and the field with nothing in it is a thing
// to be got out of.
func TestAnEmptyFieldClosesOnEscape(t *testing.T) {
	m := searchable(t, 120, 24)
	m, _ = updateModel(t, m, pressRunes("/"))
	m, _ = updateModel(t, m, press(tea.KeyEsc))

	if m.chatSearch.open {
		t.Fatal("the search is still open after Esc in an empty field")
	}
	if !strings.Contains(plain(m.View()), chatListSearchHint) {
		t.Errorf("the hint did not come back:\n%s", plain(m.View()))
	}
}

// The list below the field is still filtered by what is typed into it, and
// a query that found nothing still says so.
func TestTheListBelowTheFieldIsFilteredByTheQuery(t *testing.T) {
	m := typing(t, searchable(t, 120, 24), "dev")

	assertVisible(t, m, []string{"Dev Team"}, []string{"Alice", "Привет из Москвы"})

	missing := typing(t, searchable(t, 120, 24), "nothing here")

	assertVisible(t, missing, []string{noChatsFoundText}, []string{"Alice"})
}

// The hint row is a row of the header, so the rows under it are the rows
// they were before: the same chats, at the same rows, and the focus rule
// still under the header.
func TestTheHeaderAndTheRuleStayWhereTheyAre(t *testing.T) {
	closed := searchable(t, 120, 24)
	open, _ := updateModel(t, closed, pressRunes("/"))

	closedRows := viewLines(plain(closed.View()))
	openRows := viewLines(plain(open.View()))

	rule := indexOfLineWith(closedRows, focusRuleGlyph)
	if rule < 0 {
		t.Fatalf("there is no focus rule:\n%s", plain(closed.View()))
	}

	if closedRows[rule] != openRows[rule] {
		t.Errorf(
			"the focus rule moved with the search:\nwith the search:\n%s\nwithout it:\n%s",
			openRows[rule], closedRows[rule],
		)
	}

	first := indexOfLineWith(closedRows, "Alice")
	if first != indexOfLineWith(openRows, "Alice") {
		t.Errorf(
			"the first row of the list moved with the search: %d, want %d",
			indexOfLineWith(openRows, "Alice"), first,
		)
	}
}

// containsAny reports whether line has one of the words in it.
func containsAny(line string, words ...string) bool {
	for _, word := range words {
		if strings.Contains(line, word) {
			return true
		}
	}

	return false
}

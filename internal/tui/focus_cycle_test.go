package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// The cycle of Tab is one order at every width: chat list → composer →
// messages → chat list, and the same order backwards for Shift+Tab. The
// owner asked for it on 29.09 and again on 02.10, the second time with the
// report that the right pane of a fresh screen was dark and that only Enter
// moved the keys into it.
//
// A region that is not on the screen is not in the cycle: a chat this
// account cannot write in has no composer, and a narrow screen has no chat
// list beside the conversation. Those are the two shapes a person meets
// after a resize, and a cycle that did not account for them would send the
// keys to a region that is not drawn.

func TestTabWalksTheRegionsInOneOrder(t *testing.T) {
	cases := map[string]struct {
		width  int
		height int
		open   bool
		steps  [][2]Focus
	}{
		"chat open beside the list": {
			width:  120,
			height: 30,
			open:   true,
			steps: [][2]Focus{
				{FocusChatList, FocusComposer},
				{FocusComposer, FocusHistory},
				{FocusHistory, FocusChatList},
			},
		},
		"chat open on a narrow screen": {
			width:  60,
			height: 30,
			open:   true,
			steps: [][2]Focus{
				{FocusComposer, FocusHistory},
				{FocusHistory, FocusComposer},
			},
		},
		"no chat open": {
			width:  120,
			height: 30,
			open:   false,
			steps: [][2]Focus{
				// Tab opens the chat under the cursor and leaves the keys
				// in the messages, which is the only place the keys can
				// be: the composer is the other end of the circle and
				// Enter is what goes there.
				{FocusChatList, FocusHistory},
				{FocusHistory, FocusChatList},
			},
		},
		"no chat open on a narrow screen": {
			width:  60,
			height: 30,
			open:   false,
			steps: [][2]Focus{
				// The list is the only region of this screen, so Tab has
				// nowhere to walk to — and the chat under the cursor is
				// opened into it, which is the same thing Tab does on a
				// wide screen. Esc is the way back to the list there.
				{FocusChatList, FocusHistory},
				{FocusHistory, FocusComposer},
			},
		},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			m := sizedModel(t, testCase.width, testCase.height)
			if testCase.open {
				m = openedModel(t, testCase.width, testCase.height)
			}

			for _, step := range testCase.steps {
				m = focusedOn(m, step[0])
				m, _ = updateModel(t, m, press(tea.KeyTab))

				if m.focus != step[1] {
					t.Fatalf(
						"Tab from %v: focus = %v, want %v",
						step[0], m.focus, step[1],
					)
				}
			}
		})
	}
}

// Shift+Tab walks the same circle the other way round, and a terminal
// sends it in two forms: the key with a name and the sequence `ESC [ Z`.
// Both walk backwards, or a key that works on one terminal and does nothing
// on the next is a key nobody can rely on (the task's own risk list).
func TestShiftTabWalksTheCircleBackwardsInBothForms(t *testing.T) {
	forms := map[string]tea.KeyMsg{
		"named": press(tea.KeyShiftTab),
		"the sequence a terminal sends": {
			Type:  tea.KeyRunes,
			Runes: []rune(shiftTabSequence),
		},
	}

	for form, key := range forms {
		t.Run(form, func(t *testing.T) {
			m := focusedOn(openedModel(t, 120, 30), FocusHistory)

			m, _ = updateModel(t, m, key)
			if m.focus != FocusComposer {
				t.Fatalf("Shift+Tab from the messages: focus = %v, want %v", m.focus, FocusComposer)
			}

			m, _ = updateModel(t, m, key)
			if m.focus != FocusChatList {
				t.Fatalf("Shift+Tab from the composer: focus = %v, want %v", m.focus, FocusChatList)
			}

			m, _ = updateModel(t, m, key)
			if m.focus != FocusHistory {
				t.Fatalf("Shift+Tab from the list: focus = %v, want %v", m.focus, FocusHistory)
			}
		})
	}
}

// A whole circle of Tab changes where the keys are and nothing else: the
// draft, the cursor in it and the message the reader is on are all exactly
// where they were. A walk that lost the cursor in a draft, or moved the
// conversation a reader had scrolled into, would be a step of the cycle
// taking something away.
func TestAFullCircleOfTabKeepsTheDraftAndTheMessageCursor(t *testing.T) {
	m := focusedOn(openedModel(t, 120, 30), FocusComposer)
	m, _ = updateModel(t, m, pressRunes("черновик"))
	m, _ = updateModel(t, m, press(tea.KeyLeft))

	draft, cursor := m.Composer(), cursorOf(m)
	m.selectedMsg = 1

	for range 3 {
		m, _ = updateModel(t, m, press(tea.KeyTab))
	}

	if m.focus != FocusComposer {
		t.Fatalf("focus = %v, want the composer the circle started at", m.focus)
	}
	if m.Composer() != draft {
		t.Fatalf("composer = %q, want %q", m.Composer(), draft)
	}
	if cursorOf(m) != cursor {
		t.Fatalf("cursor = %d, want %d", cursorOf(m), cursor)
	}
	if m.selectedMsg != 1 {
		t.Fatalf("selectedMsg = %d, want the message 1 the reader was on", m.selectedMsg)
	}
}

// Tab in the list opens the chat the cursor is on and puts the keys in the
// messages: the defect of 02.10, where the list was there, the right pane
// was dark and Tab did nothing while Enter worked. Enter opens the same
// chat and leaves the keys in the composer, which is the whole difference
// between the two keys.
func TestTabInTheChatListOpensTheChatAndReadsIt(t *testing.T) {
	m := sizedModel(t, 120, 30)
	m, _ = updateModel(t, m, pressRunes("j"))

	opened := m.selected().ID

	m, _ = updateModel(t, m, press(tea.KeyTab))

	if m.screen != ScreenConversation {
		t.Fatalf("screen = %v, want ScreenConversation", m.screen)
	}
	if got := m.selected().ID; got != opened {
		t.Fatalf("chat = %d, want the chat under the cursor %d", got, opened)
	}
	if m.focus != FocusHistory {
		t.Fatalf("focus = %v, want the messages", m.focus)
	}
}

// With no chat to open there is nowhere for Tab to go, and a key that
// silently does nothing is a key the user has to try twice to learn: the
// status line says what is missing instead.
func TestTabInAnEmptyChatListSaysWhatIsMissing(t *testing.T) {
	m := sizedModel(t, 120, 30)
	m.chats = nil
	m.chatsState = loadStateEmpty

	m, _ = updateModel(t, m, press(tea.KeyTab))

	if m.screen != ScreenChats {
		t.Fatalf("screen = %v, want the list", m.screen)
	}
	if m.focus != FocusChatList {
		t.Fatalf("focus = %v, want the list", m.focus)
	}
	if m.notice != noticeOpenAChatFirst {
		t.Fatalf("notice = %q, want %q", m.notice, noticeOpenAChatFirst)
	}
}

// Shift+Tab on the list of a program that has opened nothing opens the chat
// as well: there is no region behind the list to go back to, and a key that
// does nothing here would be the one step of the circle nobody can take.
func TestShiftTabInTheChatListOpensTheChatToo(t *testing.T) {
	m := sizedModel(t, 120, 30)

	m, _ = updateModel(t, m, press(tea.KeyShiftTab))

	if m.screen != ScreenConversation {
		t.Fatalf("screen = %v, want ScreenConversation", m.screen)
	}
	if m.focus != FocusHistory {
		t.Fatalf("focus = %v, want the messages", m.focus)
	}
}

// Tab in the list with a query typed walks into the query and back out of
// it, because the search line is a region of the list (§5): a search that
// Tab closed would be a search with no way to type a two-word name.
func TestTabInTheListWithASearchOpenWalksTheQuery(t *testing.T) {
	m := typing(t, sizedModel(t, 120, 30), "dev")

	m, _ = updateModel(t, m, press(tea.KeyTab))
	if m.focus != FocusChatList {
		t.Fatalf("focus = %v, want the rows of the list", m.focus)
	}

	m, _ = updateModel(t, m, press(tea.KeyTab))
	if m.focus != FocusSearch {
		t.Fatalf("focus = %v, want the query again", m.focus)
	}
	if got := string(m.chatSearch.query); got != "dev" {
		t.Fatalf("query = %q, want it kept while the keys are elsewhere", got)
	}
	if m.screen != ScreenChats {
		t.Fatalf("screen = %v, want the list the search is above", m.screen)
	}
}

// A chat this account cannot write in has no composer to visit, so the
// circle is the list and the messages. Tab does not stop on the line that
// says the chat is read-only: there are no keys there.
func TestTheCircleOfAChatThatCannotBeWrittenInSkipsTheComposer(t *testing.T) {
	m := readOnlyModel(t, &recordingSubmitter{}, ChatBlockedChannel)

	m, _ = updateModel(t, m, press(tea.KeyTab))
	if m.focus != FocusChatList {
		t.Fatalf("Tab from the messages: focus = %v, want %v", m.focus, FocusChatList)
	}

	m, _ = updateModel(t, m, press(tea.KeyTab))
	if m.focus != FocusHistory {
		t.Fatalf("Tab from the list: focus = %v, want the messages", m.focus)
	}
}

// The hint bar of every focus names where Tab goes, and the words are read
// off the cycle rather than written per focus: a bar that promises a step
// the keys do not make is a promise the interface cannot keep (§4.6).
func TestEveryHintBarNamesWhereTabGoes(t *testing.T) {
	cases := map[string]struct {
		model Model
		want  string
	}{
		"the chat list with a conversation beside it": {
			model: focusedOn(openedModel(t, 120, 30), FocusChatList),
			want:  "Tab composer",
		},
		"the composer": {
			model: openedModel(t, 120, 30),
			want:  "Tab timeline",
		},
		"the messages": {
			model: focusedOn(openedModel(t, 120, 30), FocusHistory),
			want:  "Tab chats",
		},
		"the messages on a narrow screen": {
			model: focusedOn(openedModel(t, 60, 30), FocusHistory),
			want:  "Tab composer",
		},
		"the search": {
			model: typing(t, sizedModel(t, 120, 30), "dev"),
			want:  "Tab chats",
		},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			hint := testCase.model.hintText(LayoutFor(testCase.model.width, testCase.model.height))
			if !containsWord(hint, testCase.want) {
				t.Fatalf("the hint does not name where Tab goes (%q): %q", testCase.want, hint)
			}
		})
	}
}

// The bar is fitted to the width it is drawn in, so a bar that is longer
// than the narrowest screen of its class is a bar with its last key cut off
// — and the last key of each of these is the way out, which is the one a
// user needs to read. The check is at the smallest size of each class.
func TestTheHintBarIsNotCutAtTheSmallestSizeOfEachClass(t *testing.T) {
	sizes := map[string][2]int{
		"wide":   {wideLayoutMinWidth, 24},
		"medium": {mediumLayoutMinWidth, 24},
		"narrow": {minWidth, 24},
	}

	for class, size := range sizes {
		for _, focus := range []Focus{FocusChatList, FocusHistory, FocusComposer} {
			t.Run(class+"/"+focus.String(), func(t *testing.T) {
				m := focusedOn(openedModel(t, size[0], size[1]), focus)
				if focus == FocusChatList {
					m = focusedOn(sizedModel(t, size[0], size[1]), FocusChatList)
				}

				layout := LayoutFor(m.width, m.height)
				hint := m.hintText(layout)
				width := layout.ChatContentWidth()
				if !layout.TwoPane() {
					width = layout.FullContentWidth()
				}

				if got := m.widths.StringWidth(hint); got > width {
					t.Fatalf(
						"the hint is %d columns and the bar is %d: %q is cut",
						got, width, hint,
					)
				}
			})
		}
	}
}

// containsWord reports whether a hint says something as a whole word rather
// than as a part of another one: "Tab chats" is not in "Tab chatlist".
func containsWord(hint, want string) bool {
	if len(want) > len(hint) {
		return false
	}
	for index := 0; index+len(want) <= len(hint); index++ {
		if hint[index:index+len(want)] != want {
			continue
		}

		before := index == 0 || hint[index-1] == ' '
		after := index+len(want) == len(hint) || hint[index+len(want)] == ' '

		if before && after {
			return true
		}
	}

	return false
}

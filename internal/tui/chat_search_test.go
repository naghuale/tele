package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"telecli/internal/tui/theme"
)

// The tests in this file are about §9, the search over the chat list: what
// the keys do while it is open, what the list is left showing, and what
// the screen looks like at the three widths there are.

// searchChats is the list the search tests run against: one name in each
// case a title can be in, so that a query that is too weak or too strong
// is visible as the wrong number of rows.
func searchChats() []Chat {
	return []Chat{
		{ID: 1, Title: "Alice", Unread: 2, Preview: "How are you?"},
		{ID: 2, Title: "Dev Team", Preview: "PR merged"},
		{ID: 3, Title: "Saved Messages", Unread: 5, Preview: "Note"},
		{ID: 4, Title: "Привет из Москвы", Preview: "Поговорим"},
		{ID: 5, Title: "Ёлка", Preview: "Подарки"},
		{ID: 6, Title: "Team 🌍 Standup", Preview: "shipped"},
	}
}

// searchable returns a model of a given size whose list is searchChats.
func searchable(t *testing.T, width, height int) Model {
	t.Helper()

	m := NewModel()
	m.chats = searchChats()
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: width, Height: height})

	return m
}

// typing returns a model with the search open and a query typed into it.
func typing(t *testing.T, m Model, query string) Model {
	t.Helper()

	m, _ = updateModel(t, m, pressRunes("/"))
	for _, letter := range query {
		m, _ = updateModel(t, m, pressRunes(string(letter)))
	}

	return m
}

// assertVisible asserts which chat titles the screen shows and which it
// does not, and nothing else: a list that shows a chat the query left out
// is as wrong as one that hides a chat it kept.
func assertVisible(t *testing.T, m Model, present, absent []string) {
	t.Helper()

	view := plain(m.View())
	for _, title := range present {
		if !strings.Contains(view, title) {
			t.Fatalf("the list does not show %q:\n%s", title, view)
		}
	}
	for _, title := range absent {
		if strings.Contains(view, title) {
			t.Fatalf("the list still shows %q:\n%s", title, view)
		}
	}
}

// ---- Opening and closing ----

// `/` is the key of §8.2 that opens the search, and the line it opens is
// the one §9 draws above the list. The keys go into it: that is the whole
// of what "the focus is in the search" means, and everything a user types
// after it has to land in the field rather than in the list.
func TestSlashOpensTheSearchOverTheChatList(t *testing.T) {
	m := sizedModel(t, 120, 24)

	m, _ = updateModel(t, m, pressRunes("/"))

	if !m.chatSearch.open {
		t.Fatal("the search is not open")
	}
	if m.focus != FocusSearch {
		t.Fatalf("focus = %v, want FocusSearch", m.focus)
	}

	view := m.View()
	if !strings.Contains(view, searchPlaceholder) {
		t.Fatalf("the search line has no placeholder:\n%s", view)
	}
	if !strings.Contains(view, hintChatSearch) {
		t.Fatalf("the search does not name its own keys:\n%s", view)
	}
}

// The search is the only difference between the list with a filter and
// the list without one, and a list that looks the same either way is a
// list nobody trusts.
func TestTheSearchLineIsAboveTheList(t *testing.T) {
	m := searching(t, sizedModel(t, 120, 24))

	lines := viewLines(m.View())
	field, first := indexOfLineWith(lines, "dev"), indexOfLineWith(lines, "Dev Team")
	if field < 0 || first < 0 {
		t.Fatalf("the query or the result is not on the screen:\n%s", m.View())
	}
	if field > first {
		t.Fatalf("the search line is under the list it filters:\n%s", m.View())
	}
}

// Every letter is a letter while the keys are in the search (§8.4). The
// one that matters is `q`: it is how the program is left from the chat
// list, and a user whose chat is called "Q&A" has to be able to type it.
func TestEveryLetterIsACharacterWhileTheSearchIsOpen(t *testing.T) {
	for _, key := range []string{"q", "j", "k", "g", "G", "i", "R", "/"} {
		t.Run(key, func(t *testing.T) {
			m := sizedModel(t, 120, 24)
			m, _ = updateModel(t, m, pressRunes("/"))

			updated, cmd := m.Update(pressRunes(key))
			if cmd != nil {
				t.Fatalf(
					"%q in the search returned %T, want no command",
					key,
					cmd(),
				)
			}

			m = updated.(Model)
			if m.quitting {
				t.Fatalf("%q in the search quit the program", key)
			}
			if got := m.chatSearch.text(); got != key {
				t.Fatalf("query = %q, want %q", got, key)
			}
			if m.focus != FocusSearch {
				t.Fatalf("focus = %v, want FocusSearch", m.focus)
			}
		})
	}
}

// A query that is a key of the program is still a query. The list narrows
// to nothing and says so, and the program is still running: this is the
// difference between a filter and a mode that eats the keyboard.
func TestAQueryThatIsAKeyDoesNotRunTheKey(t *testing.T) {
	m := typing(t, sizedModel(t, 120, 24), "qjkg")

	if m.screen != ScreenChats {
		t.Fatalf("screen = %v, want ScreenChats", m.screen)
	}
	if m.quitting {
		t.Fatal("typing q in the search quit the program")
	}
	if !strings.Contains(m.View(), noChatsFoundText) {
		t.Fatalf("a query of keys is not a chat name:\n%s", m.View())
	}
}

// Ctrl+C is the deliberate way out and it works everywhere (§8.1), search
// included: a line with no q in it still has to be leavable.
func TestCtrlCStillQuitsWhileSearching(t *testing.T) {
	m := typing(t, sizedModel(t, 120, 24), "dev")

	_, cmd := m.Update(press(tea.KeyCtrlC))

	assertQuit(t, cmd)
}

// `/` is the key of the chat list, and a character everywhere else. In
// the composer it is part of a message, and the search in a conversation
// is PR-10D.
func TestSlashIsACharacterOutsideTheChatList(t *testing.T) {
	m := openedModel(t, 120, 24)
	m.focus = FocusComposer

	m, _ = updateModel(t, m, pressRunes("/secret"))

	if m.chatSearch.open {
		t.Fatal("the composer opened the chat search")
	}
	if got := m.Composer(); got != "/secret" {
		t.Fatalf("composer = %q, want %q", got, "/secret")
	}
}

// Esc is the way out of the search and the way out of nothing else: §8.5
// puts the search above the composer in the hierarchy, so the first Esc
// closes the line and puts the chat list back the way it was.
func TestEscapeClosesTheSearchAndRestoresTheSelection(t *testing.T) {
	// The screen is tall enough for every chat of the list: a window
	// placed against the last of six chats has to leave room for the
	// first five above it, and a chat scrolled out of the window is a
	// chat this test would then report as hidden (§4.2).
	m := searchable(t, 120, 28)
	m.selectedChat = 3
	m, _ = updateModel(t, m, press(tea.KeyDown))
	m.selectedChat = 4
	before := m.selectedChat

	m = typing(t, m, "saved")
	if m.selectedChat == before {
		t.Fatal("the search did not move the cursor")
	}

	m, _ = updateModel(t, m, press(tea.KeyEsc))

	if m.chatSearch.open {
		t.Fatal("Esc did not close the search")
	}
	if m.focus != FocusChatList {
		t.Fatalf("focus = %v, want FocusChatList", m.focus)
	}
	if m.selectedChat != before {
		t.Fatalf("selectedChat = %d, want %d (the chat from before the search)", m.selectedChat, before)
	}
	assertVisible(t, m, []string{"Alice", "Dev Team", "Saved Messages"}, nil)
}

// Esc closes the search even when the focus has been moved on to the list
// by Tab, because the hierarchy of §8.5 is about the line and not about
// the region the keys happen to be in.
func TestEscapeClosesTheSearchFromTheListBesideIt(t *testing.T) {
	m := searching(t, focusedOn(openedModel(t, 120, 24), FocusChatList))
	m, _ = updateModel(t, m, press(tea.KeyShiftTab))
	if m.focus != FocusChatList {
		t.Fatalf("focus = %v, want FocusChatList beside the open search", m.focus)
	}

	m, _ = updateModel(t, m, press(tea.KeyEsc))

	if m.chatSearch.open {
		t.Fatal("Esc did not close the search")
	}
	if m.screen != ScreenConversation {
		t.Fatalf("screen = %v, want the conversation Esc did not leave", m.screen)
	}
}

// Tab walks the regions of §5, and the search is one of them: the list
// with the line above it is two regions while the search is open, and the
// way into the query and back out of it is Tab.
func TestTabWalksTheSearchAndTheList(t *testing.T) {
	m := typing(t, searchable(t, 120, 24), "e")
	if m.focus != FocusSearch {
		t.Fatalf("focus = %v, want FocusSearch", m.focus)
	}

	m, _ = updateModel(t, m, press(tea.KeyTab))
	if m.focus != FocusChatList {
		t.Fatalf("focus = %v, want FocusChatList after Tab", m.focus)
	}

	m, _ = updateModel(t, m, press(tea.KeyTab))
	if m.focus != FocusSearch {
		t.Fatalf("focus = %v, want FocusSearch after Tab around", m.focus)
	}

	m, _ = updateModel(t, m, press(tea.KeyShiftTab))
	if m.focus != FocusChatList {
		t.Fatalf("focus = %v, want FocusChatList after Shift+Tab", m.focus)
	}

	// And the list has its own keys again, and they walk the results.
	if m.selectedChat != 0 {
		t.Fatalf("selectedChat = %d, want the first result 0", m.selectedChat)
	}
	m, _ = updateModel(t, m, pressRunes("j"))
	if m.selectedChat != 1 {
		t.Fatalf("selectedChat = %d, want 1 (j is a key of the list)", m.selectedChat)
	}
}

// On a two-pane screen the conversation follows the selection, because it
// is drawn from it — but not while a search is open. Following the cursor
// through the results would open a chat for every one of them, and a
// history request is not something a user asks for by reading a list.
// Enter is the key that opens a chat.
func TestTheConversationFollowsTheListUnlessASearchIsOpen(t *testing.T) {
	m := focusedOn(openedModel(t, 120, 30), FocusChatList)
	m, _ = updateModel(t, m, pressRunes("j"))
	if m.selectedChat != 1 {
		t.Fatalf("selectedChat = %d, want 1", m.selectedChat)
	}
	if want := len(m.selected().Messages) - 1; m.selectedMsg != want {
		t.Fatalf(
			"selectedMsg = %d, want %d: the conversation did not follow the list",
			m.selectedMsg,
			want,
		)
	}

	searching := typing(t, focusedOn(openedModel(t, 120, 30), FocusChatList), "e")
	opened := searching.selectedMsg
	searching, _ = updateModel(t, searching, press(tea.KeyShiftTab))
	searching, _ = updateModel(t, searching, pressRunes("j"))

	if searching.selectedChat != 1 {
		t.Fatalf("selectedChat = %d, want 1", searching.selectedChat)
	}
	if searching.selectedMsg != opened {
		t.Fatalf(
			"selectedMsg = %d, want the open chat's %d: a search opened a chat nobody asked for",
			searching.selectedMsg,
			opened,
		)
	}
	if searching.screen != ScreenConversation {
		t.Fatalf("screen = %v, want the chat that was open", searching.screen)
	}
}

// A search is drawn on the list, and a list that a narrow screen shows on
// its own is the only place the search is. Losing the pane to a resize
// loses the line with it: a field that is not on the screen is not one
// anybody can type into.
func TestAResizeThatHidesTheListClosesTheSearch(t *testing.T) {
	m := searching(t, focusedOn(openedModel(t, 120, 24), FocusChatList))

	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 60, Height: 24})

	if m.chatSearch.open {
		t.Fatal("a narrow screen has no chat list for the search to be on")
	}
	if m.focus != FocusComposer {
		t.Fatalf("focus = %v, want the composer the screen has", m.focus)
	}
	if !strings.Contains(m.View(), composerPlaceholder) {
		t.Fatalf("the conversation is not on the screen:\n%s", m.View())
	}
}

// ---- What the list shows ----

// §9: the results narrow as the query is typed, they keep the order of the
// list they came from, and the case of the query is not the case of the
// names.
func TestTheQueryNarrowsTheListWhateverItsCase(t *testing.T) {
	for _, query := range []string{"dev", "DEV", "DeV", "dev t", "TEAM"} {
		t.Run(query, func(t *testing.T) {
			m := typing(t, searchable(t, 120, 24), query)

			assertVisible(t, m, []string{"Dev Team"}, []string{"Alice", "Saved", "Москвы"})
		})
	}
}

// The order of the results is the order of the whole list. A search that
// ranked its own results would have to say how, and the answer — that it
// did not — is what a user can rely on.
func TestTheResultsKeepTheOrderOfTheList(t *testing.T) {
	m := typing(t, searchable(t, 120, 24), "e")

	entries := m.chatListEntries()
	var titles []string
	for _, entry := range entries {
		titles = append(titles, entry.chat.Title)
	}

	if len(entries) < 2 {
		t.Fatalf("the query %q left %d rows, want more than one to compare", m.chatSearch.text(), len(entries))
	}
	for index := 1; index < len(entries); index++ {
		if entries[index-1].index >= entries[index].index {
			t.Fatalf(
				"results out of order: %q is at %d before %q at %d",
				titles[index-1],
				entries[index-1].index,
				titles[index],
				entries[index].index,
			)
		}
	}
}

// Cyrillic is not a special case of anything: the folding is Unicode's,
// so a query in one case finds a name in another, in Latin or in
// Cyrillic.
func TestTheQueryNarrowsCyrillicNames(t *testing.T) {
	for _, query := range []string{"привет", "ПРИВЕТ", "Москвы", "из мо"} {
		t.Run(query, func(t *testing.T) {
			m := typing(t, searchable(t, 120, 24), query)

			assertVisible(t, m, []string{"Привет из Москвы"}, []string{"Alice", "Dev Team"})
		})
	}
}

// "ё" and "е" are two letters and a name is written with one of them. A
// search that folded them into each other would find a chat the user did
// not ask for, and the reason would be on their screen as a wrong result.
func TestYoIsNotYe(t *testing.T) {
	assertVisible(
		t,
		typing(t, searchable(t, 120, 24), "елк"),
		nil,
		[]string{"Ёлка"},
	)
	assertVisible(
		t,
		typing(t, searchable(t, 120, 24), "ёлк"),
		[]string{"Ёлка"},
		nil,
	)
}

// A title is a run of whatever a person put in it, and the query finds
// the part it is in. The emoji is in the name and the name is found by
// what is around it.
func TestTheQueryFindsANameWithAnEmojiInIt(t *testing.T) {
	for _, query := range []string{"standup", "team 🌍", "stand"} {
		t.Run(query, func(t *testing.T) {
			m := typing(t, searchable(t, 120, 24), query)

			assertVisible(t, m, []string{"Standup"}, []string{"Alice", "Dev Team"})
		})
	}
}

// Pressing `/` and seeing the whole list is the answer to "what does this
// do": an empty query is not a query that matches nothing, it is a
// question that has not been asked yet.
func TestAnEmptyQueryLeavesTheListAlone(t *testing.T) {
	m := searchable(t, 120, 30)
	m, _ = updateModel(t, m, pressRunes("/"))

	assertVisible(t, m, []string{"Alice", "Dev Team", "Saved Messages", "Ёлка"}, nil)
	if got := len(m.chatListEntries()); got != len(searchChats()) {
		t.Fatalf("rows = %d, want all %d chats", got, len(searchChats()))
	}
	if m.selectedChat != 0 {
		t.Fatalf("selectedChat = %d, want the chat it was on", m.selectedChat)
	}
}

// §9: the first result is selected without a key, and every keystroke
// says so again. A user who types a name and presses Enter has asked for
// the first chat that name fits, and making them walk to it would be a
// step they did not ask for.
func TestTheFirstResultIsSelected(t *testing.T) {
	cases := map[string]int{
		"saved":     2,
		"привет":    3,
		"team":      1,
		"standup":   5,
		"ёлка":      4,
		"saved mes": 2,
	}

	for query, want := range cases {
		t.Run(query, func(t *testing.T) {
			m := typing(t, searchable(t, 120, 24), query)

			if m.selectedChat != want {
				t.Fatalf(
					"selectedChat = %d (%q), want %d",
					m.selectedChat,
					m.chats[m.selectedChat].Title,
					want,
				)
			}
			if m.chats[m.selectedChat].Title == "" {
				t.Fatal("the cursor is on nothing")
			}
		})
	}
}

// The arrow keys walk the results, and they stop at both ends of them. A
// cursor that leaves the results is a cursor on a chat the list does not
// show, which is the one thing a filtered list cannot have.
func TestTheArrowsWalkTheResults(t *testing.T) {
	m := searchable(t, 120, 24)
	m.chats = []Chat{
		{ID: 1, Title: "Alpha"},
		{ID: 2, Title: "Beta"},
		{ID: 3, Title: "Alpine"},
	}
	m = typing(t, m, "al")
	if m.selectedChat != 0 {
		t.Fatalf("selectedChat = %d, want the first result 0", m.selectedChat)
	}

	for _, step := range []struct {
		key  tea.KeyMsg
		want int
	}{
		{key: press(tea.KeyDown), want: 2},
		{key: press(tea.KeyDown), want: 2},
		{key: press(tea.KeyUp), want: 0},
		{key: press(tea.KeyUp), want: 0},
	} {
		m, _ = updateModel(t, m, step.key)
		if m.selectedChat != step.want {
			t.Fatalf(
				"after %v selectedChat = %d, want %d",
				step.key,
				m.selectedChat,
				step.want,
			)
		}
	}
}

// The keys of the list walk the results as well, because Tab has put the
// keys back in the list and a list of three rows is a list whose `j` moves
// a row.
func TestTheListKeysWalkTheResultsToo(t *testing.T) {
	m := searchable(t, 120, 24)
	m.chats = []Chat{
		{ID: 1, Title: "Alpha"},
		{ID: 2, Title: "Beta"},
		{ID: 3, Title: "Alpine"},
	}
	m = typing(t, m, "al")
	m, _ = updateModel(t, m, press(tea.KeyTab))

	m, _ = updateModel(t, m, pressRunes("j"))
	if m.selectedChat != 2 {
		t.Fatalf("selectedChat = %d, want the second result 2", m.selectedChat)
	}

	// And the ends of the list are the ends of the results.
	m, _ = updateModel(t, m, pressRunes("g"))
	if m.selectedChat != 0 {
		t.Fatalf("selectedChat = %d, want the first result 0", m.selectedChat)
	}

	m, _ = updateModel(t, m, pressRunes("G"))
	if m.selectedChat != 2 {
		t.Fatalf("selectedChat = %d, want the last result 2", m.selectedChat)
	}
}

// Enter opens what is under the cursor and leaves the search: the chat is
// the answer to the question, and a field that stayed open above a
// conversation would be a field nobody asked to keep typing in.
func TestEnterOpensTheSelectedResultAndClosesTheSearch(t *testing.T) {
	m := typing(t, searchable(t, 120, 24), "stand")
	if m.selectedChat != 5 {
		t.Fatalf("selectedChat = %d, want the result 5", m.selectedChat)
	}

	m, _ = updateModel(t, m, press(tea.KeyEnter))

	if m.chatSearch.open {
		t.Fatal("Enter left the search open")
	}
	if m.screen != ScreenConversation {
		t.Fatalf("screen = %v, want ScreenConversation", m.screen)
	}
	if m.focus != FocusComposer {
		t.Fatalf("focus = %v, want FocusComposer", m.focus)
	}
	if got := m.selected().ID; got != 6 {
		t.Fatalf("opened chat %d, want 6 (Team Standup)", got)
	}
	if strings.Contains(m.View(), searchPlaceholder) {
		t.Fatalf("the search line is still drawn:\n%s", m.View())
	}
}

// Enter opens the result the arrows walked to, not the one the query
// happened to find first: a user who has moved the cursor has chosen.
func TestEnterOpensTheResultTheArrowsWalkedTo(t *testing.T) {
	m := searchable(t, 120, 24)
	m.chats = []Chat{
		{ID: 1, Title: "Alpha", Messages: []Message{{ID: 1, Text: "one"}}},
		{ID: 2, Title: "Beta"},
		{ID: 3, Title: "Alpine", Messages: []Message{{ID: 1, Text: "three"}}},
	}
	m = typing(t, m, "al")

	m, _ = updateModel(t, m, press(tea.KeyDown))
	m, _ = updateModel(t, m, press(tea.KeyEnter))

	if got := m.selected().Title; got != "Alpine" {
		t.Fatalf("opened %q, want Alpine", got)
	}
}

// A query that found nothing is a calm sentence and not an error: the
// list is there, the account is fine, and the only thing to do is change
// the query. Nothing about the state of the load changes either, so a
// search that found nothing cannot be mistaken for a list that failed.
func TestAQueryThatFindsNothingIsACalmEmptyState(t *testing.T) {
	m := typing(t, searchable(t, 120, 24), "qqqqq")

	view := m.View()
	if !strings.Contains(view, noChatsFoundText) {
		t.Fatalf("the list does not say it found nothing:\n%s", view)
	}
	if m.chatsState != loadStateLoaded {
		t.Fatalf("chatsState = %s, want loaded", m.chatsState)
	}
	if m.loadErr != nil {
		t.Fatalf("loadErr = %v, want none", m.loadErr)
	}
	for _, alarming := range []string{chatLoadFailedText, "error", "Error", "retry"} {
		if strings.Contains(view, alarming) {
			t.Fatalf("a search that found nothing says %q:\n%s", alarming, view)
		}
	}
}

// Nothing is opened out of an empty result: the list has no rows, and a
// chat the user cannot see is not the one they chose.
func TestEnterOpensNothingWhenTheQueryFoundNothing(t *testing.T) {
	m := typing(t, searchable(t, 120, 24), "qqqqq")

	updated, cmd := m.Update(press(tea.KeyEnter))
	if cmd != nil {
		t.Fatalf("Enter on an empty result returned %T, want no command", cmd())
	}

	m = updated.(Model)
	if m.screen != ScreenChats {
		t.Fatalf("screen = %v, want ScreenChats", m.screen)
	}
	if !m.chatSearch.open {
		t.Fatal("Enter closed a search that opened nothing")
	}
	if got := m.chatSearch.text(); got != "qqqqq" {
		t.Fatalf("query = %q, want it kept", got)
	}
}

// The search runs over the chats that are on the screen. Asking Telegram
// for chats that are not loaded is a different feature with a different
// answer on a slow network, and the boundary of this one is that it asks
// nothing at all: not one key of the search returns a command.
func TestTheSearchAsksTelegramForNothing(t *testing.T) {
	source := &fakeChatSource{chats: searchChats()}
	m := NewModelWithSource(source)
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 120, Height: 24})
	m, _ = updateModel(t, m, chatsLoadedMsg{chats: searchChats()})

	for _, key := range []tea.KeyMsg{
		pressRunes("/"),
		pressRunes("d"),
		pressRunes("e"),
		press(tea.KeyDown),
		press(tea.KeyBackspace),
		press(tea.KeyEsc),
	} {
		updated, cmd := m.Update(key)
		if cmd != nil {
			t.Fatalf("the search returned a command for %v: %T", key, cmd())
		}
		m = updated.(Model)
	}
}

// ---- Editing the query ----

// The readline keys of §8.4 edit the query, and they do it the way they
// do everywhere else: one cluster at a time, to the start of the line, and
// the word in front of the cursor with the gap that separated it.
func TestTheReadlineKeysEditTheQuery(t *testing.T) {
	cases := []struct {
		name  string
		start string
		keys  []tea.KeyMsg
		want  string
	}{
		{
			name:  "backspace takes one character",
			start: "dev",
			keys:  []tea.KeyMsg{press(tea.KeyBackspace)},
			want:  "de",
		},
		{
			name:  "backspace takes a whole cluster",
			start: "team 🌍",
			keys:  []tea.KeyMsg{press(tea.KeyBackspace)},
			want:  "team ",
		},
		{
			name:  "backspace on an empty query does nothing",
			start: "",
			keys:  []tea.KeyMsg{press(tea.KeyBackspace)},
			want:  "",
		},
		{
			name:  "ctrl+u clears the query",
			start: "dev team",
			keys:  []tea.KeyMsg{press(tea.KeyCtrlU)},
			want:  "",
		},
		{
			name:  "ctrl+w takes the word and the gap before it",
			start: "dev team",
			keys:  []tea.KeyMsg{press(tea.KeyCtrlW)},
			want:  "dev",
		},
		{
			name:  "ctrl+w twice takes both words",
			start: "dev team alpine",
			keys:  []tea.KeyMsg{press(tea.KeyCtrlW), press(tea.KeyCtrlW)},
			want:  "dev",
		},
		{
			name:  "ctrl+w on one word leaves the gap in front of it",
			start: " one",
			keys:  []tea.KeyMsg{press(tea.KeyCtrlW)},
			want:  "",
		},
		{
			name:  "an edit narrows the list again",
			start: "s",
			keys:  []tea.KeyMsg{press(tea.KeyCtrlU)},
			want:  "",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			m := typing(t, searchable(t, 120, 24), testCase.start)

			for _, key := range testCase.keys {
				m, _ = updateModel(t, m, key)
			}

			if got := m.chatSearch.text(); got != testCase.want {
				t.Fatalf("query = %q, want %q", got, testCase.want)
			}
		})
	}
}

// Clearing the query shows the whole list again. An edit that narrowed the
// list once and never widened it again would be a field that only loses.
func TestClearingTheQueryBringsTheListBack(t *testing.T) {
	m := typing(t, searchable(t, 120, 24), "standup")
	assertVisible(t, m, []string{"Standup"}, []string{"Alice", "Dev Team"})

	m, _ = updateModel(t, m, press(tea.KeyCtrlU))

	if got := len(m.chatListEntries()); got != len(searchChats()) {
		t.Fatalf("rows = %d, want all %d chats", got, len(searchChats()))
	}
	if m.selectedChat != 0 {
		t.Fatalf("selectedChat = %d, want the first chat of the whole list", m.selectedChat)
	}
}

// A space is a character in a query and not a focus key, and a chat name
// with a space in it is the normal case rather than the unusual one.
func TestASpaceIsPartOfTheQuery(t *testing.T) {
	m := searchable(t, 120, 24)
	m, _ = updateModel(t, m, pressRunes("/"))
	m, _ = updateModel(t, m, pressRunes("saved"))
	m, _ = updateModel(t, m, press(tea.KeySpace))
	m, _ = updateModel(t, m, pressRunes("messages"))

	if got := m.chatSearch.text(); got != "saved messages" {
		t.Fatalf("query = %q, want %q", got, "saved messages")
	}
	if m.focus != FocusSearch {
		t.Fatalf("focus = %v, want FocusSearch", m.focus)
	}
	assertVisible(t, m, []string{"Saved Messages"}, []string{"Alice", "Dev Team"})
}

// A paste is text, in whole. What somebody copied from a file is not a
// command, in the composer and in the search alike.
func TestAPasteGoesIntoTheQueryWhole(t *testing.T) {
	m := searchable(t, 120, 24)
	m, _ = updateModel(t, m, pressRunes("/"))

	m, _ = updateModel(t, m, tea.KeyMsg{
		Type:  tea.KeyRunes,
		Paste: true,
		Runes: []rune("saved messages"),
	})

	if got := m.chatSearch.text(); got != "saved messages" {
		t.Fatalf("query = %q, want %q", got, "saved messages")
	}
	assertVisible(t, m, []string{"Saved Messages"}, []string{"Alice", "Dev Team"})
}

// ---- The header and the hint bar ----

// §4.1 puts the search and the unread count in the header of the list.
// The key is there because a list that can be searched has to say so
// before anybody goes looking for the key, and a line that names a key
// that does nothing is a promise the interface cannot keep.
func TestTheHeaderNamesTheSearchAndTheUnreadCount(t *testing.T) {
	m := sizedModel(t, 120, 24)

	header := strings.Join(chatListHeaderLines(t, m), " ")
	if !strings.Contains(header, "/ Search") {
		t.Fatalf("the header = %q, want the search", header)
	}
	if !strings.Contains(header, "7 unread") {
		t.Fatalf("the header = %q, want the unread count", header)
	}
}

// A narrow screen has no conversation beside the list, and §3.3 puts the
// status in the header there. The status keeps the line, and the search is
// named in the hint bar below, which is the other half of §4.6: a line
// that has two sentences on it belongs to the one that cannot be read
// anywhere else.
func TestANarrowHeaderKeepsTheStatusAndTheSearchIsInTheHintBar(t *testing.T) {
	m := modelWithSummary(t, theme.ProfileNoColor, 60, 24, StatusSummary{
		Connection: ConnectionReady,
		Queue:      QueueSummary{Known: true, Queued: 2},
	})
	// Back to the list: on a narrow screen this is the screen, and it is
	// the only place the list and its hint bar are drawn.
	m, _ = updateModel(t, m, press(tea.KeyEsc))
	m, _ = updateModel(t, m, press(tea.KeyEsc))
	if m.screen != ScreenChats {
		t.Fatalf("screen = %v, want the chat list", m.screen)
	}

	header := strings.Join(chatListHeaderLines(t, m), " ")
	if !strings.Contains(header, "Connected · 2 queued") {
		t.Fatalf("the narrow header = %q, want the status", header)
	}
	if !strings.Contains(m.View(), hintChatList) {
		t.Fatalf("the list does not name the search key:\n%s", m.View())
	}
}

// The hint bar of the search names Esc and not q: while the query is being
// typed, q is a character in it, and a bar that offered q quit next to a
// field would be naming a key that does something else.
func TestTheSearchHintBarNamesEscAndNotQ(t *testing.T) {
	m := typing(t, sizedModel(t, 120, 24), "dev")

	hint := m.hintText(LayoutFor(120, 24))
	if hint != "Enter open · Esc cancel" {
		t.Fatalf("hint = %q, want %q", hint, "Enter open · Esc cancel")
	}
}

// ---- The shape of the screen ----

// §9 has to work on every width there is. The search is drawn at the
// width of the list, and the list is the whole screen where the layout
// has one region (the narrow case of §3.3).
func TestTheSearchWorksAtEveryWidth(t *testing.T) {
	for _, size := range [][2]int{{120, 30}, {80, 30}, {60, 30}} {
		m := typing(t, searchable(t, size[0], size[1]), "stand")

		entries := m.chatListEntries()
		if len(entries) != 1 || entries[0].chat.Title != "Team 🌍 Standup" {
			t.Fatalf(
				"at %dx%d the list has %d rows, want the one chat named by the query",
				size[0],
				size[1],
				len(entries),
			)
		}

		assertRectangularView(t, m)

		view := m.View()
		if !strings.Contains(view, "stand") {
			t.Fatalf("at %dx%d the query is not on the screen:\n%s", size[0], size[1], view)
		}
		if !strings.Contains(view, m.selected().Title[:5]) {
			t.Fatalf("at %dx%d the result is not on the screen:\n%s", size[0], size[1], view)
		}
	}
}

// A query is longer than a list pane more often than not, and a field that
// grew the line would push the rest of the screen out of shape. The line
// shows the end of the query — the cursor and what was just typed — and
// nothing is ever cut in the middle of a character.
func TestTheSearchLineIsNeverWiderThanTheList(t *testing.T) {
	query := "a query that is much longer than the pane it is typed into"
	for _, size := range [][2]int{{120, 30}, {80, 30}, {60, 30}, {40, 12}} {
		m := typing(t, searchable(t, size[0], size[1]), query)

		assertRectangularView(t, m)

		field := searchFieldLine(t, m)
		if field == "" {
			t.Fatalf("at %dx%d the search line is not on the screen:\n%s", size[0], size[1], m.View())
		}
		if strings.Contains(field, ellipsis) {
			t.Fatalf("at %dx%d the query was cut: %q", size[0], size[1], plain(field))
		}
		if !strings.HasSuffix(plain(field), "into") && !strings.Contains(plain(field), "▏") {
			t.Fatalf("at %dx%d the query is not drawn: %q", size[0], size[1], plain(field))
		}
	}
}

// searchFieldLine returns the line of the screen the search is drawn on.
func searchFieldLine(t *testing.T, m Model) string {
	t.Helper()

	layout := LayoutFor(m.width, m.height)
	width := layout.SidebarContentWidth()
	if !layout.TwoPane() {
		width = layout.FullContentWidth()
	}

	return m.chatSearchRegion(width)
}

// The placeholder fits the narrowest list there is. A field whose own name
// is cut on a medium screen is a field nobody can read.
func TestThePlaceholderFitsTheNarrowestList(t *testing.T) {
	m := searchable(t, 80, 24)
	m, _ = updateModel(t, m, pressRunes("/"))

	layout := LayoutFor(80, 24)
	field := plain(m.chatSearchRegion(layout.SidebarContentWidth()))
	if !strings.Contains(field, searchPlaceholder) {
		t.Fatalf("the search line = %q, want the placeholder", field)
	}
	if strings.Contains(field, ellipsis) {
		t.Fatalf("the search line = %q, want the whole placeholder", field)
	}
	assertRectangularView(t, m)
}

// ---- The matched fragment ----

// §9 asks for the matched fragment to be highlighted, and the highlight is
// exactly the fragment: the runs around it are the rest of the title, in
// the order they are in the name, with nothing of the match in them and
// nothing of the title in it.
func TestTheMatchedFragmentIsExactlyTheMatch(t *testing.T) {
	cases := []struct {
		name  string
		title string
		query string
		width int
		want  []chatTitleSegment
	}{
		{
			name:  "at the start of the title",
			title: "Dev Team",
			query: "dev",
			want: []chatTitleSegment{
				{text: "Dev", matched: true},
				{text: " Team"},
			},
		},
		{
			name:  "in the middle of the title",
			title: "Saved Messages",
			query: "mes",
			want: []chatTitleSegment{
				{text: "Saved "},
				{text: "Mes", matched: true},
				{text: "sages"},
			},
		},
		{
			name:  "the whole title",
			title: "Alice",
			query: "alice",
			want:  []chatTitleSegment{{text: "Alice", matched: true}},
		},
		{
			name:  "another case of the same letters",
			title: "Dev Team",
			query: "TEAM",
			want: []chatTitleSegment{
				{text: "Dev "},
				{text: "Team", matched: true},
			},
		},
		{
			name:  "cyrillic",
			title: "Привет из Москвы",
			query: "ПРИВЕТ",
			want: []chatTitleSegment{
				{text: "Привет", matched: true},
				{text: " из Москвы"},
			},
		},
		{
			name:  "an emoji is a cluster and is not cut in half",
			title: "Team 🌍 Standup",
			query: "m 🌍",
			want: []chatTitleSegment{
				{text: "Tea"},
				{text: "m 🌍", matched: true},
				{text: " Standup"},
			},
		},
		{
			name:  "a query that starts inside an emoji is widened to it",
			title: "Team 🌍 Standup",
			query: "🌍",
			want: []chatTitleSegment{
				{text: "Team "},
				{text: "🌍", matched: true},
				{text: " Standup"},
			},
		},
		{
			name: "a letter with a combining accent is a cluster too",
			// The e and the combining acute below are two runes and one
			// letter: what the reader sees is one é, and a highlight on
			// one of its runes would draw it in halves.
			title: "cafe\u0301 latte",
			query: "e",
			want: []chatTitleSegment{
				{text: "caf"},
				{text: "e\u0301", matched: true},
				{text: " latte"},
			},
		},
		{
			name:  "a title that does not match is not split",
			title: "Dev Team",
			query: "alice",
			want:  []chatTitleSegment{{text: "Dev Team"}},
		},
		{
			name:  "no query highlights nothing",
			title: "Dev Team",
			query: "",
			want:  []chatTitleSegment{{text: "Dev Team"}},
		},
		{
			name:  "a title wider than the row is fitted first",
			title: "Development team of the whole company",
			query: "team of",
			width: 30,
			want: []chatTitleSegment{
				{text: "Development "},
				{text: "team of", matched: true},
				{text: " the whole…"},
			},
		},
	}

	m := NewModel()

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			width := testCase.width
			if width == 0 {
				width = m.widths.StringWidth(testCase.title)
			}

			got := m.chatTitleSegments(
				testCase.title,
				[]rune(testCase.query),
				width,
			)

			assertSegments(t, got, testCase.want)
		})
	}
}

// The width the row has is what the title is fitted to, and a match that
// the fitting cut away leaves the rest of the title alone. A highlight in
// a title that is not on the screen marks nothing a user can read.
func TestAMatchThatTheWidthCutIsNotHighlighted(t *testing.T) {
	got := NewModel().chatTitleSegments(
		"Dev Team of the company", []rune("company"), 8,
	)

	if len(got) != 1 || got[0].matched {
		t.Fatalf("segments = %+v, want one unmatched run of the fitted title", got)
	}
	if got[0].text != "Dev Tea…" {
		t.Fatalf("segments = %+v, want the title fitted to 8 columns", got)
	}
}

// assertSegments fails unless the segments are exactly the wanted ones.
func assertSegments(t *testing.T, got, want []chatTitleSegment) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("segments = %+v, want %+v", got, want)
	}

	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("segment %d = %+v, want %+v", index, got[index], want[index])
		}
	}
}

// The highlight is drawn in the accent of the theme, and the run of the
// match is the only run of the row that is in it. This is the part of §9
// that has to be checked on the screen rather than on the segments: a
// title that is split correctly and drawn in one colour is a title with
// the list filtered and nothing marked.
// The matched fragment is in the second accent and not the first: a
// selected row is in the first, and a match inside the selected row would
// be the same colour as the name around it.
func TestTheMatchedFragmentIsDrawnInTheAccent(t *testing.T) {
	m := typing(t, searchable(t, 120, 24), "saved")
	m.theme = theme.DefaultTheme().ForProfile(theme.ProfileTrueColor)
	m = m.withRenderer(theme.ProfileTrueColor)

	row := rowLineOf(t, m, "Saved Messages")

	// The parameters are taken from the style the row is drawn with rather
	// than from the token, because the colour library rounds a value on
	// the way to the terminal and the row is what the terminal is given.
	accent := foregroundParameters(m.styles().matchRun().Render("x"))

	var matched, accented int
	for _, run := range styleRuns(row) {
		if strings.Contains(run.sgr, accent) {
			accented++
		}
		if run.text == "Saved" {
			matched++
			if !strings.Contains(run.sgr, accent) {
				t.Fatalf(
					"the match is in %q, want the accent %q: %q",
					run.sgr,
					accent,
					row,
				)
			}
		}
	}

	if matched != 1 {
		t.Fatalf("the match is %d runs of the row, want 1: %q", matched, row)
	}
	if accented != 1 {
		t.Fatalf("the accent is on %d runs of the row, want 1: %q", accented, row)
	}
}

// Without colour there is no highlight to draw, and the list a query has
// narrowed is short enough to read without one (§2.7). The search is not a
// colour-only feature: what it says is the list itself.
func TestANoColourSearchFiltersTheListWithoutAHighlight(t *testing.T) {
	m := uncolored(typing(t, searchable(t, 120, 24), "saved"))

	view := plain(m.View())
	if !strings.Contains(view, "Saved Messages") {
		t.Fatalf("the list does not show the match:\n%s", view)
	}
	if strings.Contains(view, "Alice") {
		t.Fatalf("the list shows a chat the query left out:\n%s", view)
	}
	if strings.Contains(m.View(), "38;2;") {
		t.Fatalf("a no-colour profile painted something:\n%q", m.View())
	}
}

// rowLineOf returns the line of the screen a chat is drawn on.
func rowLineOf(t *testing.T, m Model, title string) string {
	t.Helper()

	for _, line := range viewLines(m.View()) {
		if strings.Contains(plain(line), title) {
			return line
		}
	}

	t.Fatalf("no line holds %q:\n%s", title, m.View())

	return ""
}

// styleRun is a run of plain text of a rendered line with the escape
// sequence in force where it starts.
type styleRun struct {
	sgr  string
	text string
}

// styleRuns splits a rendered line into the runs of plain text in it and
// the SGR sequence each of them is printed in.
//
// It is a test-only reading of what a terminal would do: a reset means
// "nothing in force", and a run is the text between two sequences. The
// width of the run in columns is not asked about, and the line is not
// measured with this — the layout tests measure lines with the model's
// width model,
// which is what counts the columns of a rendered line.
func styleRuns(line string) []styleRun {
	var (
		runs []styleRun
		sgr  string
	)

	for rest := line; rest != ""; {
		if strings.HasPrefix(rest, "\x1b[") {
			end := strings.Index(rest, "m")
			if end < 0 {
				break
			}

			sgr = rest[2:end]
			if sgr == "0" {
				sgr = ""
			}
			rest = rest[end+1:]

			continue
		}

		text := rest
		if next := strings.Index(rest, "\x1b["); next >= 0 {
			text = rest[:next]
		}

		runs = append(runs, styleRun{sgr: sgr, text: text})
		rest = rest[len(text):]
	}

	return runs
}

// rgbParameters returns the SGR parameters a theme colour is printed
// with, in the form lipgloss writes them into a sequence.
// foregroundSGR starts the true-colour foreground parameters of an SGR
// sequence. Bold and the other attributes may come before them, so a test
// that looks for the escape itself rather than for these numbers would
// miss every run that has an attribute on it.
const foregroundSGR = "38;2;"

// foregroundParameters is the SGR colour parameters of a rendered run.
func foregroundParameters(rendered string) string {
	start := strings.Index(rendered, foregroundSGR)
	if start < 0 {
		return ""
	}

	rest := rendered[start:]
	end := strings.Index(rest, "m")
	if end < 0 {
		return ""
	}

	return rest[:end]
}

// The folding of the search is the folding of strings.EqualFold, and
// proving it against the standard library is what keeps the two from
// drifting: a name found by one is a name found by the other.
func TestTheSearchFoldsWhatEqualFoldFolds(t *testing.T) {
	pairs := [][2]string{
		{"Dev Team", "DEV TEAM"},
		{"Привет", "ПРИВЕТ"},
		{"Σοφία", "σοφία"},
		{"Team", "TEAM"},
		{"K", "k"},
		{"K", "K"},
		{"ﬁle", "file"},
	}

	for _, pair := range pairs {
		t.Run(fmt.Sprintf("%s=%s", pair[0], pair[1]), func(t *testing.T) {
			if !strings.EqualFold(pair[0], pair[1]) {
				t.Skip("the standard library does not fold this pair")
			}

			if foldIndexOf([]rune(pair[0]), []rune(pair[1])) != 0 {
				t.Fatalf("foldIndexOf(%q, %q) = -1, want 0", pair[0], pair[1])
			}
			if foldIndexOf([]rune(pair[1]), []rune(pair[0])) != 0 {
				t.Fatalf("foldIndexOf(%q, %q) = -1, want 0", pair[1], pair[0])
			}
		})
	}
}

// A query that differs from the title by more than its case is not found
// in it, and one that differs only by its case always is. This is the
// other half of the folding, and it is what a user expects of a name they
// cannot type exactly: near enough, not something else entirely.
func TestTheSearchFindsOnlyWhatFoldsToTheQuery(t *testing.T) {
	found := [][2]string{
		{"Saved Messages", "sav"},
		{"Dev Team", "d"},
		{"Привет", "П"},
		{"Team 🌍 Standup", "S"},
	}
	for _, pair := range found {
		if foldIndexOf([]rune(pair[0]), []rune(pair[1])) < 0 {
			t.Fatalf("%q is not found in %q", pair[1], pair[0])
		}
	}

	missing := [][2]string{
		{"Saved Messages", "savi"},
		{"Dev Team", "developer"},
		{"Привет", "превет"},
		{"Team 🌍 Standup", "standup team"},
		{"Dev Team", ""},
		{"", "dev"},
	}
	for _, pair := range missing {
		if foldIndexOf([]rune(pair[0]), []rune(pair[1])) >= 0 {
			t.Fatalf("%q is found in %q", pair[1], pair[0])
		}
	}
}

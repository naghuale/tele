package tui

import (
	"unicode"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
)

// This file is the search of §9: the line above the chat list, what is
// typed into it, and the list it narrows.
//
// The search runs over the chats that are on the screen and over nothing
// else. It asks TDLib no questions: a filter over what is loaded is
// instant, and a chat Telegram has not loaded cannot be found by a list
// that does not have it. Asking Telegram is a different feature with
// different answers about slow networks, and it is PR-10D.
//
// The query is a run of runes, like the draft of the composer, and the
// edits are the readline ones of §8.4: Backspace, Ctrl+U and Ctrl+W, all
// of which work backwards from the end. The cursor is at the end of the
// query and stays there, because a search is a prefix of what the user is
// looking for and the list below the field narrows on every keystroke: the
// two things a user watches while typing a query are the query and the
// list, and a cursor in the middle of the first one moves what neither of
// them says.

type chatSearch struct {
	// open says whether the line above the list is on the screen and
	// holds the keys.
	open bool

	// query is what has been typed into it.
	query []rune

	// before is the chat that was selected when the search opened, and
	// Esc puts the cursor back on it (§9). A user who searched to check
	// one name and found nothing is looking at the chat they were on, not
	// at whatever the filter happened to leave selected.
	before int
}

// text returns the query as a string.
func (s chatSearch) text() string {
	return string(s.query)
}

// queryCursor returns where the cursor is in the query, which is its end.
//
// The query is a prefix of what a user is looking for and the cursor is
// not moved into it: see the note at the head of this file. Every edit
// works from the end, so the cursor is at the end of whatever survived it.
func (s chatSearch) queryCursor() int {
	return len(s.query)
}

// filtering reports whether the query narrows the list.
//
// An empty query does not: a search that has just been opened shows the
// whole list, so that pressing `/` and seeing the list unchanged is
// itself the answer to "what does this do".
func (s chatSearch) filtering() bool {
	return s.open && len(s.query) > 0
}

// matchesTitle reports whether the query is in a title.
func (s chatSearch) matchesTitle(title string) bool {
	return foldIndexOf([]rune(title), s.query) >= 0
}

// matchesChat reports whether a query finds a chat by its title or by one
// of its other names.
//
// Telegram calls the chat with oneself "Saved Messages" and the person
// using it calls it "Избранное", and a user who is looking for it types
// the word they know. §9 is a search over what is on the screen, and the
// other names of a chat are as much a part of it as its title is.
func (s chatSearch) matchesChat(chat Chat) bool {
	if !s.filtering() {
		return true
	}

	if s.matchesTitle(chat.Title) {
		return true
	}

	for _, alias := range chat.Aliases {
		if s.matchesTitle(alias) {
			return true
		}
	}

	return false
}

// openChatSearch puts the keys into the line above the list (§9).
func (m Model) openChatSearch() Model {
	if m.chatSearch.open {
		return m
	}

	m.chatSearch = chatSearch{open: true, before: m.selectedChat}
	m.focus = FocusSearch

	return m
}

// closeChatSearch takes the line away and leaves the cursor where it is.
//
// This is the way out for a search that opened a chat: the chat that was
// opened is the one the cursor belongs on, and putting the old one back
// would be a second answer to the same key.
func (m Model) closeChatSearch() Model {
	if !m.chatSearch.open {
		return m
	}

	m.chatSearch = chatSearch{}
	if m.focus == FocusSearch {
		m.focus = FocusChatList
	}

	return m
}

// cancelChatSearch closes the search and puts the cursor back on the chat
// it was on before the search opened (§9).
func (m Model) cancelChatSearch() Model {
	if !m.chatSearch.open {
		return m
	}

	before := m.chatSearch.before
	m = m.closeChatSearch()
	m.selectedChat = clampIndex(before, len(m.chats))

	return m
}

// updateChatSearchKey handles the keys of the search line.
//
// Esc is not here: §8.5 puts the search above the composer in the
// hierarchy, so Esc closes it wherever the focus is, and updateKey takes
// it before the screen does.
//
// Every letter is a letter. `q` does not leave the program, `j` and `k`
// do not walk the list and `g` does not go to the first chat, because a
// query has to be able to be a `q`: the same reason §8.4 turns the
// single-letter keys back into text in the composer. The arrow keys are
// the way to walk the results, and they are arrows rather than letters
// for exactly that reason.
func (m Model) updateChatSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// §8.1 makes Tab a key of the whole interface, and §5 makes the search
	// one of its regions: the focus can leave the line without the query
	// being lost, and the line stays above the list with what was typed in
	// it. A search that Tab closed would be a search with no way to type a
	// two-word name.
	switch {
	case isTab(msg):
		return m.cycleFocus(1), nil

	case isShiftTab(msg):
		return m.cycleFocus(-1), nil
	}

	// A paste is text, in whole: what was copied elsewhere is not a
	// command.
	if msg.Paste {
		m.chatSearch.query = append(m.chatSearch.query, msg.Runes...)
		return m.applyChatSearch(), nil
	}

	// The readline keys of §8.4, in the same order and with the same
	// meaning as in the composer: a text field where two of the three
	// work and one does not is a field nobody can predict. Ctrl+K is not
	// among them and cannot be: it takes what is in front of the cursor,
	// and the cursor of a query is at the end of it, so in this field it
	// could only ever do nothing. A key that cannot work is not offered.
	switch {
	case isClearComposer(msg):
		m.chatSearch.query, _ = clearToCursor(m.chatSearch.query, len(m.chatSearch.query))
		return m.applyChatSearch(), nil

	case isDeleteWordBefore(msg):
		m.chatSearch.query, _ = deleteWordBefore(
			m.chatSearch.query,
			len(m.chatSearch.query),
		)
		return m.applyChatSearch(), nil
	}

	switch msg.Type {
	case tea.KeyRunes:
		m.chatSearch.query = append(m.chatSearch.query, msg.Runes...)
		return m.applyChatSearch(), nil

	case tea.KeySpace:
		m.chatSearch.query = append(m.chatSearch.query, ' ')
		return m.applyChatSearch(), nil

	case tea.KeyBackspace:
		m.chatSearch.query, _ = deleteBefore(
			m.chatSearch.query,
			len(m.chatSearch.query),
		)
		return m.applyChatSearch(), nil

	case tea.KeyUp:
		return m.moveChatSelection(-1)

	case tea.KeyDown:
		return m.moveChatSelection(1)

	case tea.KeyEnter:
		return m.openSelectedResult(true)
	}

	return m, nil
}

// applyChatSearch re-narrows the list after the query changed.
//
// The cursor goes to the first result every time, which is what §9 says
// a search does: the user is looking for one chat, the first match is the
// one most of them mean, and Enter opens it without a key in between.
// Every keystroke re-selecting the first match is what makes the query a
// prefix rather than an answer that has to be walked.
func (m Model) applyChatSearch() Model {
	m.selectFirstChatMatch()
	return m
}

// chatListEntry is one chat of the list as the list shows it: the chat
// itself and the index it has in the whole list.
//
// The index is what the selection and the conversation are counted in, so
// a filter over the list does not renumber anything the model already
// knows.
type chatListEntry struct {
	chat  Chat
	index int
}

// chatListEntries returns the chats the list draws, in the order of the
// whole list.
//
// A search narrows the list and keeps the order it already had: the
// results are the chats a user knows are there, in the order they were in
// before, and not a new ranking of them that would have to be explained.
func (m Model) chatListEntries() []chatListEntry {
	entries := make([]chatListEntry, 0, len(m.chats))

	for index, chat := range m.chats {
		if !m.chatSearch.matchesChat(chat) {
			continue
		}

		entries = append(entries, chatListEntry{chat: chat, index: index})
	}

	return entries
}

// selectFirstChatMatch puts the cursor on the first chat the query
// matched.
//
// A list that found nothing keeps the cursor where it was: there is
// nothing to put it on, and the empty state says so. The chat under the
// cursor is remembered for Esc either way.
func (m *Model) selectFirstChatMatch() {
	entries := m.chatListEntries()
	if len(entries) == 0 {
		return
	}

	m.selectedChat = entries[0].index
}

// moveChatSearchSelection moves the cursor by delta over the results of
// the search, clamped to them.
//
// The first result is where a search starts, so walking down from it lands
// on the second one; a cursor that is not on a result at all — which is
// what a list that found nothing leaves behind — lands on the first of
// them rather than on nothing.
func (m Model) moveChatSearchSelection(delta int) Model {
	entries := m.chatListEntries()
	if len(entries) == 0 {
		return m
	}

	position := 0
	for index, entry := range entries {
		if entry.index == m.selectedChat {
			position = index
			break
		}
	}

	// The clamp is on the ends of the results and not on the length of
	// the slice: the last result is a row, and a cursor one past it is
	// nothing anybody can see.
	position = minInt(maxInt(position+delta, 0), len(entries)-1)
	m.selectedChat = entries[position].index

	return m
}

// chatListEdgeIndex returns the index of the first or the last chat the
// list can be on: the first or the last result while a search narrows it,
// and the first or the last chat otherwise.
//
// `g` and `G` in the list go to the ends of what is on the screen. With a
// search open that is the ends of the results, because a chat the query
// left out is not somewhere a user can walk to.
func (m Model) chatListEdgeIndex(last bool) int {
	if m.chatSearch.open {
		entries := m.chatListEntries()
		if len(entries) == 0 {
			return 0
		}
		if last {
			return entries[len(entries)-1].index
		}

		return entries[0].index
	}

	if last {
		return maxInt(len(m.chats)-1, 0)
	}

	return 0
}

// openSelectedResult opens the selected chat and leaves a search behind
// it.
//
// Nothing is opened when the query found nothing: the list is empty, and
// a chat that is not on the screen is not the one the user chose.
func (m Model) openSelectedResult(focusComposer bool) (tea.Model, tea.Cmd) {
	if m.chatSearch.open {
		if len(m.chatListEntries()) == 0 {
			return m, nil
		}

		m = m.closeChatSearch()
	}

	return m.openSelectedChat(focusComposer)
}

// chatListDrawn reports whether the screen shows the chat list.
func (m Model) chatListDrawn() bool {
	return m.screen == ScreenChats || LayoutFor(m.width, m.height).TwoPane()
}

// chatSearchMatch returns the runes of a title that the query matched,
// widened to whole grapheme clusters, or false when it did not match.
//
// A match is a match whichever way the highlight lands: the chat is in the
// list either way, and only the ends of the range move.
func chatSearchMatch(title, query []rune) (int, int, bool) {
	at := foldIndexOf(title, query)
	if at < 0 {
		return 0, 0, false
	}

	from, to := clusterRange(title, at, at+len(query))

	return from, to, true
}

// clusterRange widens a rune range to the grapheme clusters at its ends.
//
// A range that starts or ends inside a cluster would be a highlight in
// the middle of an emoji or of a letter with a combining accent, and a
// terminal prints the runes of one in sequence: the highlight would cut
// the thing in two and show neither half whole. The widened range begins
// at the first cluster that overlaps it and ends after the last one.
func clusterRange(text []rune, from, to int) (int, int) {
	start, end, widened := from, to, false
	used := 0

	for _, cluster := range graphemes(text) {
		first, last := used, used+len(cluster)
		used = last

		if last <= from {
			continue
		}
		if first >= to {
			break
		}

		if !widened {
			start, widened = first, true
		}
		if last > end {
			end = last
		}
	}

	return start, end
}

// foldIndexOf returns the rune index in text where query starts, or -1.
//
// The comparison is Unicode simple case folding one rune at a time, which
// is what strings.EqualFold does: "dev" is found in "Dev Team" and
// "ПРИВЕТ" in "Привет", and a title that has nothing else in common with
// the query is not found in it.
//
// Two things it deliberately does not do. It does not fold "ё" into "е":
// they are different letters in a name and a search for one of them must
// not find the other. And it does not normalise anything else either, so
// a title and a query that were typed on different keyboards are found,
// or not found, exactly as they were typed. A search that quietly
// rewrites what somebody typed is a search whose results cannot be
// explained to them afterwards.
func foldIndexOf(text, query []rune) int {
	if len(query) == 0 || len(query) > len(text) {
		return -1
	}

	for start := range len(text) - len(query) + 1 {
		if foldEqual(text[start:start+len(query)], query) {
			return start
		}
	}

	return -1
}

// foldEqual reports whether two runs of runes are the same under simple
// case folding.
func foldEqual(text, query []rune) bool {
	if len(text) != len(query) {
		return false
	}

	for index := range text {
		if !foldRuneEqual(text[index], query[index]) {
			return false
		}
	}

	return true
}

// foldRuneEqual reports whether two runes are the same letter under
// Unicode simple case folding.
//
// SimpleFold walks the cycle a letter's case goes round — k, K, K and
// back to k — which is the whole of what strings.EqualFold compares. The
// cycle is a set of one-letter spellings of one letter, so "ß" and "ss"
// are not folded into each other: folding never changes the length of a
// run, and a query of one letter cannot find a name that spells the same
// sound with two.
func foldRuneEqual(a, b rune) bool {
	if a == b {
		return true
	}

	// Two ASCII runes have one fold of their own, and taking it here
	// rather than through the table is what keeps a search over a list of
	// chat titles cheap.
	if a < utf8.RuneSelf && b < utf8.RuneSelf {
		return lowerASCII(a) == lowerASCII(b)
	}

	folded := unicode.SimpleFold(a)
	for folded != a && folded != b {
		folded = unicode.SimpleFold(folded)
	}

	return folded == b
}

// lowerASCII lowercases one ASCII letter and leaves everything else as it
// is. The letters outside ASCII are not in this case, and a rune this
// function would have to guess at has no business being guessed at in a
// search.
func lowerASCII(value rune) rune {
	if value >= 'A' && value <= 'Z' {
		return value + ('a' - 'A')
	}

	return value
}

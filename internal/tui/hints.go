package tui

// The hint bar names the keys that work, in the focus the user is in and
// at the width they are looking at.
//
// §4.6 asks for both: the hints depend on the focus, not only on the
// width, and they are shorter as the screen narrows. What is left out
// matters as much as what is in: a hint for a key that does nothing yet is
// a promise the interface cannot keep, so there is no `Shift+Enter` before
// PR-10A.3 and none for the search in a conversation, which is PR-10D.
//
// Two of the keys in it are named after the owner's report of 02.10, when
// a user could not tell which key leaves a place and which key writes a
// letter: every bar ends with the way out or the way back (`Esc back`,
// `q quit`), and every bar that has a Tab to offer names where it goes.
//
// A hint is dropped by the width class, never by what the key does. The
// newline key is what a narrow bar loses first, because the focus key and
// the way out are the two the owner asked to be able to see; Alt+Enter
// still works there, and docs/help/keys.md says so.

// hintLines returns the lines of the hint bar, which is none on a short
// screen (§3.4) and one otherwise.
func (m Model) hintLines(layout Layout, width int) []string {
	if layout.HideHints() {
		return nil
	}

	styles := m.styles()

	return []string{styles.dimmed(m.tokens().SecondaryText).
		Render(m.widths.Fit(m.hintText(layout), width, ellipsis))}
}

// hintWidth returns how many columns the hint bar is drawn in.
//
// It is the width of the region the bar belongs to: the conversation band
// on a two-pane screen, the whole screen where there is one pane. The hints
// are fitted to it, and a bar that decides what to say has to know how much
// room it has.
func hintWidth(layout Layout) int {
	if layout.TwoPane() {
		return layout.ChatContentWidth()
	}

	return layout.FullContentWidth()
}

// hintFits reports whether a hint can be drawn in the bar whole.
//
// It is the test every hint about to be cut by the width is put through
// first: a bar that is cut has lost its last word, and the last word of
// every bar here is the way out of the place the user is in. So a key is
// named while there is room for it and the way out is named whatever the
// screen is.
func (m Model) hintFits(layout Layout, hint string) bool {
	return m.widths.StringWidth(hint) <= hintWidth(layout)
}

// hintText returns the hints of the current focus.
func (m Model) hintText(layout Layout) string {
	// A popup is a focus region of its own (§5), so it names its own keys
	// while it is open: the keys of the timeline do nothing underneath it,
	// and a hint for a key that does nothing is a promise the interface
	// cannot keep (§4.6).
	if m.modal.open {
		return hintModal
	}
	if m.actionSheet.open {
		return hintActionSheet
	}

	// §5: the search is a focus region of its own, so it names its own
	// keys. While they are in it, Enter opens a result and Esc leaves it,
	// and q is a letter rather than the way out of the program — which is
	// why the bar names Esc and not q.
	if m.focus == FocusSearch {
		return hintChatSearch
	}

	if m.screen == ScreenChats || m.focus == FocusChatList {
		return m.chatListHint()
	}

	// A chat this account cannot write in has no composer, and the keys that
	// work are the keys of the messages. The bar says that rather than
	// "Enter send": §4.6 asks for the keys of the focus, and a bar that
	// promises a send in a chat that refuses it is the same defect as the
	// field it would be under.
	if m.focus == FocusComposer && m.canWrite() {
		return m.composerHint(layout)
	}

	return m.timelineHint(layout)
}

// composerHint returns the hints of the composer.
//
// The newline key is in the bar while there is room for it. Alt+Enter
// starts a line in every conversation, and a bar of 34 columns cannot say
// `Enter send`, `Alt+Enter newline`, `Tab timeline` and `Esc back` all at
// once — so the newline key is the one that goes, and it goes by width and
// not by screen size. What stays whatever the screen is: the key of the
// field, where Tab goes, and the way back out. The key that was left out
// is in docs/help/keys.md.
func (m Model) composerHint(layout Layout) string {
	const (
		shortHint = "Enter send · Tab timeline · Esc back"
		wideHint  = "Enter send · Alt+Enter newline · Tab timeline · Esc back"
	)

	if m.hintFits(layout, wideHint) {
		return wideHint
	}

	return shortHint
}

// timelineHint returns the hints of the message timeline.
//
// The action key is in every one of them, because §13 gives every message a
// menu and a key the user has to know exists is a key they will not press.
// It goes before the focus key, because the focus key is the one that is
// given up when the bar is too narrow to hold it.
func (m Model) timelineHint(layout Layout) string {
	keys := []string{"j/k scroll", "a actions"}

	// A chat this account cannot write in has no composer to go into, so
	// neither the key that enters it nor the key that cycles the focus to
	// it is in the bar. §4.6 asks for the keys of the focus, and a bar that
	// names a key that does nothing is a promise about nothing.
	if m.canWrite() && layout.Kind == LayoutWide {
		keys = append(keys, "Enter composer")
	}

	// Tab goes to the chat list where the list is on the screen and back to
	// the composer where it is not. The bar says which of the two this
	// width has, rather than the word `focus`, which is the same on every
	// screen and answers nothing on any of them.
	if withTab := hintJoined(append(keys, m.tabHint(), "Esc back")...); m.hintFits(layout, withTab) {
		return withTab
	}

	return hintJoined(append(keys, "Esc back")...)
}

// chatListHint is what the chat list says, with the retry key when a
// retry is possible.
//
// §4.6 has no hint for a key that does nothing, so R is named while the
// load is slow or has failed - the two states where R asks again - and not
// while the list is there, where it would only be a promise.
func (m Model) chatListHint() string {
	// Tab opens the chat under the cursor when there is no conversation on
	// the screen yet, and the Enter beside it opens the same one. With a
	// conversation beside the list it is a plain step of the cycle, and the
	// bar names where it goes instead of repeating the key.
	hint := hintChatList
	if m.screen == ScreenConversation {
		hint = "Enter open · " + m.tabHint() + " · / search · q quit"
	}

	if !m.chatsRetryPossible() {
		return hint
	}

	return hint + " · R retry"
}

// chatsRetryPossible reports whether pressing R has anything to do.
func (m Model) chatsRetryPossible() bool {
	return m.chatsLoadSlow || m.chatsState == loadStateError
}

// The hints of a popup. They are the keys of a list, and Esc is named with
// the thing it does rather than with the letter: in an uncertain message it
// is what keeps the record.
const (
	hintActionSheet = "j/k select · Enter run · Esc close"
	hintModal       = "j/k select · Enter answer · Esc cancel"
)

// hintChatSearch is what the search line says.
//
// Esc is named with the thing it does and not with the letter that leaves
// the program: while the search is open `q` is a character, and a hint
// bar that said "q quit" next to a query a user is typing into would name
// a key that does something else. Tab leaves the line and the query stays
// behind it, so it is named as well.
const hintChatSearch = "Enter open · Tab chats · Esc cancel"

// hintChatList is what the chat list says at every width.
//
// The search is in it because the list is the one region where a key can
// be reached for and not found, and §4.6 has no hint for a key that does
// nothing. `q quit` is in it for the reason the owner gave on 02.10: it is
// the only key that leaves the program from here, and a user who does not
// know that presses Esc and is told nothing. The bar is the shortest one
// the interface has at every width, and a list that can be searched says
// so rather than making a user read the header to find out.
const hintChatList = "Enter or Tab open · / search · q quit"

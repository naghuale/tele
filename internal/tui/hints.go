package tui

import "strings"

// The hint bar names the keys that work, in the focus the user is in and
// at the width they are looking at.
//
// §4.6 asks for both: the hints depend on the focus, not only on the
// width, and they are shorter as the screen narrows. What is left out
// matters as much as what is in: a hint for a key that does nothing yet is
// a promise the interface cannot keep, so there is no `Shift+Enter` before
// PR-10A.3 and none for the search in a conversation, which is PR-10D.
//
// A hint is dropped by the width class, never by what the key does. Tab
// moves the focus on a medium screen as well as on a wide one; the medium
// bar simply has less room to say so, and a user who wants to know can try
// it. Cutting the hint instead of the key is the difference between a
// short bar and a lying one.

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
// The newline key is in every one of them and is never cut: it is the key
// a user cannot guess, because Bubble Tea v1 does not tell Shift+Enter from
// Enter in most terminals (divergence 3) and Alt+Enter is not what anybody
// tries first. The focus and the way out are what shrink as the screen does.
func (m Model) composerHint(layout Layout) string {
	switch {
	case !layout.TwoPane():
		return "Enter send · Alt+Enter newline · Esc back"

	case layout.Kind == LayoutMedium:
		return "Enter send · Alt+Enter newline · Esc timeline"

	default:
		return "Enter send · Alt+Enter newline · Tab focus · Esc timeline"
	}
}

// timelineHint returns the hints of the message timeline.
//
// The action key is in every one of them, because §13 gives every message a
// menu and a key the user has to know exists is a key they will not press.
func (m Model) timelineHint(layout Layout) string {
	keys := []string{"j/k scroll", "a actions"}

	// A chat this account cannot write in has no composer to go into, so
	// neither the key that enters it nor the key that cycles the focus to
	// it is in the bar. §4.6 asks for the keys of the focus, and a bar that
	// names a key that does nothing is a promise about nothing.
	composable := m.canWrite()
	if composable {
		keys = append(keys, "Enter composer")
	}

	if !layout.TwoPane() {
		return strings.Join(append(keys, "Esc back"), " · ")
	}
	if composable && layout.Kind == LayoutWide {
		keys = append(keys, "Tab focus")
	}

	return strings.Join(append(keys, "Esc chats"), " · ")
}

// chatListHint is what the chat list says, with the retry key when a
// retry is possible.
//
// §4.6 has no hint for a key that does nothing, so R is named while the
// load is slow or has failed - the two states where R asks again - and not
// while the list is there, where it would only be a promise.
func (m Model) chatListHint() string {
	if !m.chatsRetryPossible() {
		return hintChatList
	}

	return hintChatList + " · R retry"
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
// a key that does something else.
const hintChatSearch = "Enter open · Esc cancel"

// hintChatList is what the chat list says at every width.
//
// The search is in it because the list is the one region where a key can
// be reached for and not found, and §4.6 has no hint for a key that does
// nothing. The bar is the shortest one the interface has at every width,
// and a list that can be searched says so rather than making a user read
// the header to find out.
const hintChatList = "Enter open · / search · q quit"

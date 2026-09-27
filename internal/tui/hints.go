package tui

// The hint bar names the keys that work, in the focus the user is in and
// at the width they are looking at.
//
// §4.6 asks for both: the hints depend on the focus, not only on the
// width, and they are shorter as the screen narrows. What is left out
// matters as much as what is in: a hint for a key that does nothing yet is
// a promise the interface cannot keep, so there is no `/ search` before
// PR-10A.6 and no `Shift+Enter` before PR-10A.3.
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
		Render(fitCells(m.hintText(layout), width))}
}

// hintText returns the hints of the current focus.
func (m Model) hintText(layout Layout) string {
	if m.screen == ScreenChats || m.focus == FocusChatList {
		return hintChatList
	}

	if m.focus == FocusComposer {
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
func (m Model) timelineHint(layout Layout) string {
	if !layout.TwoPane() {
		return "j/k scroll · Enter composer · Esc back"
	}

	if layout.Kind == LayoutMedium {
		return "j/k scroll · Enter composer · Esc chats"
	}

	return "j/k scroll · Enter composer · Tab focus · Esc chats"
}

// hintChatList is what the chat list says at every width.
//
// The list has two keys and a medium screen has room for both, so there is
// nothing to take away: §4.6 asks for a shorter bar as the screen narrows,
// and this is already the shortest one the interface has.
const hintChatList = "Enter open · q quit"

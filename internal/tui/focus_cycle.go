package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// The focus cycle of §5: Tab walks the regions of the screen in a circle
// and comes back to where it started.
//
// The order is the chat list, the composer, the messages, the chat list —
// the round trip the owner wrote down on 29.09 and again on 02.10: from the
// field to the messages, from the messages back to the list. It is the one
// order in the interface that is not a reading order, and it is the order
// the keys have, so a person can learn it once.
//
// A region that is not on the screen is not in the circle, which is what
// makes a narrow conversation walk between the composer and the messages
// alone and leaves the chat list to Esc.
//
// The keys are §8.1: Tab and Shift+Tab, in the two forms a terminal sends
// them. The draft and the message cursor are not touched by a step of this
// circle — the walk is a question about where the keys are, not about what
// the screen holds.

// focusStep returns the focus delta of Tab and of Shift+Tab: zero for
// every other key.
func focusStep(msg tea.KeyMsg) int {
	switch {
	case isTab(msg):
		return 1
	case isShiftTab(msg):
		return -1
	default:
		return 0
	}
}

// tabFromChatList takes Tab in the chat list, which is the one region with
// something to open rather than somewhere to walk to.
//
// With no conversation open, Tab opens the chat under the cursor and puts
// the keys in the messages: the owner reported on 02.10 that the right
// pane was dark, that Tab moved nothing there and that only Enter worked,
// and a key that is a region key everywhere else must not be a dead key on
// the screen the program opens on. Enter opens the same chat and leaves
// the keys in the composer, which is the difference between the two.
//
// With a conversation open beside the list, Tab is an ordinary step of the
// cycle, and a search line above the list is a region of the list (§5), so
// Tab walks into the query and back out of it.
//
// A list with no chat in it has nothing to open: the focus stays where it
// is and the status line says what is missing, because a key that silently
// does nothing is a key the user has to try twice to learn.
func (m Model) tabFromChatList(delta int) (tea.Model, tea.Cmd) {
	if m.chatSearch.open || m.screen == ScreenConversation {
		return m.cycleFocus(delta), nil
	}

	if len(m.chats) == 0 {
		return m.withNoticeCleared(noticeOpenAChatFirst)
	}

	return m.openSelectedChat(FocusHistory)
}

// cycleFocus moves the focus by delta positions among the visible
// regions.
//
// The order is the one above, and a region that is not drawn is not in it.
func (m Model) cycleFocus(delta int) Model {
	regions := m.visibleFocusRegions()
	if len(regions) == 0 {
		return m
	}

	next := 0
	for index, region := range regions {
		if region == m.focus {
			next = index + delta
			break
		}
	}

	// A focus that is not in the order starts at its first region rather
	// than at itself: the invariant is that exactly one region is
	// focused, and an invisible one is not it.
	m.focus = regions[((next%len(regions))+len(regions))%len(regions)]

	return m
}

// normalizeFocus puts the focus on a region the screen is drawing.
//
// A resize can take a region away. A terminal squeezed from wide to
// narrow loses the chat list pane, and a screen too short for messages
// loses the timeline, and a focus left on a region that is not drawn is
// worse than no focus at all: every key would go to a region the user
// cannot see highlighted.
func (m Model) normalizeFocus() Model {
	layout := LayoutFor(m.width, m.height)

	// A screen that shows nothing but the composer has one region, and it
	// is the composer (§3.4).
	if m.screen == ScreenConversation && layout.ComposerOnly() {
		m.focus = FocusComposer
		return m
	}

	for _, region := range m.visibleFocusRegions() {
		if region == m.focus {
			return m
		}
	}

	// The focus was on a region this size does not draw. The composer is
	// where a conversation is written, so a conversation lands there and
	// every other screen on the only region it has. A conversation that
	// cannot be written in lands on the messages instead: the composer of
	// such a chat is a line and not a place the keys are.
	if m.screen == ScreenConversation {
		if m.canWrite() {
			m.focus = FocusComposer
		} else {
			m.focus = FocusHistory
		}

		return m
	}

	m.focus = FocusChatList

	return m
}

// visibleFocusRegions returns the regions Tab visits, in order.
func (m Model) visibleFocusRegions() []Focus {
	if m.screen != ScreenConversation {
		return m.chatListFocusRegions()
	}

	// The chat list is on the screen only where there are two panes.
	regions := []Focus{}
	if LayoutFor(m.width, m.height).TwoPane() {
		regions = m.chatListFocusRegions()
	}

	// A chat this account cannot write in has no composer to visit: the
	// region under the messages is a line that says why, and Tab has no use
	// for stopping on a place with no keys.
	if m.canWrite() {
		regions = append(regions, FocusComposer)
	}

	return append(regions, FocusHistory)
}

// chatListFocusRegions returns the regions of the chat list, which are the
// list itself and the search above it while the search is open (§5).
//
// They are one after the other because the search belongs to the list: a
// user who Tabs past it and Tabs back arrives where they left.
func (m Model) chatListFocusRegions() []Focus {
	if m.chatSearch.open {
		return []Focus{FocusChatList, FocusSearch}
	}

	return []Focus{FocusChatList}
}

// focusLabel is what a region is called in the hint bar, so that the bar
// can name where Tab goes without the model assembling the words itself.
//
// The words are the ones the bar has always used: the composer and the
// timeline are regions of a conversation on the screen, and a help page
// that explains them is docs/help/keys.md.
const (
	focusLabelChats    = "chats"
	focusLabelComposer = "composer"
	focusLabelTimeline = "timeline"
	focusLabelSearch   = "search"
)

// focusLabel returns the name of a region.
func focusLabel(focus Focus) string {
	switch focus {
	case FocusChatList:
		return focusLabelChats
	case FocusHistory:
		return focusLabelTimeline
	case FocusComposer:
		return focusLabelComposer
	case FocusSearch:
		return focusLabelSearch
	default:
		return ""
	}
}

// tabHint returns the half of a hint that says where Tab goes from here,
// and nothing else when there is no step to take.
//
// It is read off the cycle rather than written per focus, so a bar cannot
// promise a step the keys do not make: the name is the next region of the
// circle, with Shift+Tab named on the way back to the list.
func (m Model) tabHint() string {
	regions := m.visibleFocusRegions()
	if len(regions) < 2 {
		return ""
	}

	next := FocusComposer
	for index, region := range regions {
		if region != m.focus {
			continue
		}
		next = regions[(index+1)%len(regions)]

		break
	}

	return "Tab " + focusLabel(next)
}

// hintJoined joins the halves of a hint and drops the empty ones.
func hintJoined(parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			kept = append(kept, part)
		}
	}

	return strings.Join(kept, " · ")
}

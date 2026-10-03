package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// This file is the preview: the chat under the cursor is shown in the pane
// beside the list before it is opened.
//
// The owner's words (02.10): «где я выбираю чат, разговора нет, надо жать
// Enter». The pane beside the list was the empty state of §17 until Enter,
// and a person walking a list with the arrows had to press a key for every
// chat to find out whether there was anything to read in it.
//
// Three things the preview is not, and each of them is a rule rather than a
// detail:
//
//   - it is not a read. Telegram is told which messages are on the screen
//     only for a conversation that is on the screen, and the pane beside
//     the list is a preview for as long as the keys are in the list (§8.2),
//     so a preview leaves the read pointer of the chat where it was.
//     Nothing in this file calls ViewMessages, and
//     markVisibleMessagesViewed asks nothing while this pane is what the
//     right of the screen shows;
//   - it is not an opening. The chat is not opened in TDLib, so presence is
//     not counted and the composer of the open conversation does not move;
//     the keys stay in the list, which is what makes Tab and Enter the
//     difference between reading and writing;
//   - it is not a load per key. The pane is filled after the cursor has
//     been still for chatPreviewPause, so holding ↓ through fifty chats
//     asks for one preview, and the last selection wins: an answer for a
//     chat the cursor has already left is dropped rather than painted.
//
// One preview is not one page. The owner looked at the result on 03.10 and
// said the pane showed «only the newest messages» and that the space above
// them was dark: «кто не пользовался, подумает, что там ничего нет или что
// сломано». So the preview reads the same way an opened chat is read on its
// first screen (#58): pages are asked for until the pane is covered, and
// what is above the oldest message on the screen is a sentence rather than
// emptiness — `Loading history…` while a page is on its way and `Beginning
// of the chat` where the chat really does begin there.

// chatPreviewPause is how long the cursor has to be still on a chat before
// its conversation is shown in the pane beside the list.
//
// It is long enough to be one pause rather than fifty: a person walking a
// list presses ↓ far faster than once in 200 ms, and each of those presses
// would be a history load of fifty messages and a repaint nobody reads. It
// is short enough that a person who has stopped is not left looking at an
// empty pane waiting for a decision to be made.
const chatPreviewPause = 200 * time.Millisecond

// chatPreviewDueMsg is delivered when the pause of a chat under the cursor
// has run out.
//
// It carries the chat and the place of the cursor, and both are checked on
// arrival: a pause that was armed for a chat the cursor has since left is a
// pause about nothing, and the last selection is the one that is on screen.
type chatPreviewDueMsg struct {
	chatID    int64
	selection int

	// boundary is the message the page starts above — zero for the newest
	// page of the chat — and newest says which page this is.
	//
	// The two travel with the pause rather than being read from the model on
	// arrival because the pane keeps asking for pages while the cursor
	// stands still: a pause that read the boundary when it arrives would ask
	// for the same page again for ever, and a page that came back empty means
	// two different things depending on which page it was.
	boundary int64
	newest   bool
}

// chatPreviewLoadedMsg is delivered by loadPreviewCmd.
//
// It is a message of its own and not a historyLoadedMsg with a flag on it,
// because the two have to stay apart: the page of a preview goes into the
// pane beside the list and nothing else, while the page of an open
// conversation moves the cursor and fills the feed with older messages.
type chatPreviewLoadedMsg struct {
	chatID    int64
	operation uint64
	page      HistoryPage
	err       error

	// newest says that this is the newest page of the chat, which is the
	// page that replaces what was read and the one whose empty answer means
	// the chat has nothing in it. An empty page above the newest one is a
	// different answer: there is nothing older than the boundary, which is
	// what turns the line at the top of the pane from `Loading history…`
	// into `Beginning of the chat`.
	newest bool
}

// armPreviewIfNeeded returns the pause for a preview the screen has not got,
// and nothing at all where the pane is already showing the chat under the
// cursor.
//
// A pane beside the list with nothing in it is what the owner named twice: on
// 02.10 «где я выбираю чат, разговора нет, надо жать Enter», and on 03.10 the
// chat under the cursor was there at startup and the pane stayed empty until
// something was pressed. It is armed wherever the chat under the cursor can
// change without a key — a list that arrives, a live list that moves it, a
// resize that gave the screen its second pane — and nowhere else, so a read
// of the list that keeps the same chat under the cursor costs nothing.
func (m Model) armPreviewIfNeeded() tea.Cmd {
	// A screen with no pane beside the list is not asked anything: the
	// preview that was on it is not on the screen either, and a chat the
	// cursor has not left does not need reading twice because the terminal
	// changed its mind about its width.
	if m.chatPreviewShown() || !m.chatPreviewPossible() {
		return nil
	}

	return m.armChatPreview()
}

// armChatPreview returns the command that shows the chat under the cursor
// after the pause, and forgets the preview that is on the screen.
//
// Forgetting comes first and is not a detail: what the pane shows is about
// the chat that was under the cursor a moment ago, and a pane that keeps
// showing a conversation nobody has the cursor on is a pane that answers a
// question nobody asked.
func (m *Model) armChatPreview() tea.Cmd {
	m.chatPreviewChat = 0

	if !m.chatPreviewPossible() {
		return nil
	}

	chatID := m.selectedChatID()
	if chatID == 0 {
		return nil
	}

	selection := m.selectedChat

	return tea.Tick(chatPreviewPause, func(time.Time) tea.Msg {
		return chatPreviewDueMsg{
			chatID:    chatID,
			selection: selection,
			boundary:  0,
			newest:    true,
		}
	})
}

// chatPreviewPossible reports whether there is a pane beside the list to
// preview a chat in.
//
// The pane is a preview while the keys are in the list, wherever that is: on
// the chat list screen nothing is open, and beside a conversation the keys
// went back to the list to walk it. Both are one state — a person is choosing
// a chat — and a cursor that moves there is looking at chats either way. It
// used to be asked of the screen rather than of the focus, so a conversation
// open beside the list kept showing itself while the cursor walked off it,
// and the highlight and the words on the right were about two different
// chats (#75).
//
// A chat list on its own has nowhere to show it: the preview is drawn in the
// conversation pane, and a narrow screen has no conversation pane while the
// list is on the screen.
func (m Model) chatPreviewPossible() bool {
	return m.screen != ScreenAuth &&
		m.focus == FocusChatList &&
		m.selectedChat >= 0 &&
		m.selectedChat < len(m.chats) &&
		LayoutFor(m.width, m.height).TwoPane()
}

// conversationPaneShown reports whether the pane beside the list is the
// conversation that was opened rather than a preview of the chat under the
// cursor.
//
// The keys are what decides it: they are in a conversation when they are in
// one of its regions, and in the list when they are in the list. The focus
// circle, Tab in the list and the drawing of the pane are all asked of this
// one answer, so they cannot disagree about what the right of the screen is.
func (m Model) conversationPaneShown() bool {
	return m.screen == ScreenConversation && !m.chatPreviewShown()
}

// chatPreviewShown reports whether the pane beside the list is showing the
// chat under the cursor.
//
// It asks about the chat under the cursor as well as the preview: a chat
// the cursor has moved off of is not what is in the pane, whatever the pane
// was asked for a moment ago.
func (m Model) chatPreviewShown() bool {
	if !m.chatPreviewPossible() {
		return false
	}

	return m.chatPreviewChat != 0 && m.chatPreviewChat == m.selectedChatID()
}

// updateChatPreviewDue applies the pause that has run out.
func (m Model) updateChatPreviewDue(
	msg chatPreviewDueMsg,
) (tea.Model, tea.Cmd) {
	// The cursor moved on while the pause ran, or the list is not beside a
	// pane any more: the answer would be about a chat nobody is looking at.
	if !m.chatPreviewPossible() ||
		m.selectedChat != msg.selection ||
		m.selectedChatID() != msg.chatID {
		return m, nil
	}

	m.chatPreviewChat = msg.chatID

	// The fill belongs to the chat that was there a moment ago: what was
	// read of it, whether it was the beginning and whether an ask failed are
	// answers about another chat, and a preview of this one starts with
	// nothing known. The bounds are counted as well, so a cursor that has
	// stood still on fifty chats does not carry fifty chats' worth of pages
	// into the fifty-first.
	m.previewBeginning = false
	m.previewHasMore = false
	m.previewMoreLoading = false
	m.previewMoreErr = nil
	m.previewFillRequests = 0
	m.previewFillMessages = 0

	// A model built without Telegram holds the messages of its chats in the
	// list, so there is nothing to ask for: the pause was the wait, and the
	// pane is drawn from what the list already carries.
	if m.source == nil {
		return m, nil
	}

	m.historyState = loadStateLoading
	m.previewOperation++

	return m, loadPreviewCmd(
		m.source,
		msg.chatID,
		msg.boundary,
		historyPageSize,
		m.previewOperation,
		msg.newest,
	)
}

// fillChatPreview asks for the page above the one on the screen while the
// pane is not covered.
//
// This is the fill of #58 — a chat opens with a screen of messages in it,
// and the ask repeats until there are enough of them to fill it — with the
// same bounds and the same question. A page of nothing above the oldest
// message on the screen is the answer "this is the beginning of the chat",
// and it is the flag that puts that sentence at the top of the pane instead
// of darkness (previewBeginning).
func (m Model) fillChatPreview() (Model, tea.Cmd) {
	if !m.chatPreviewShown() || m.source == nil {
		return m, nil
	}
	if m.previewBeginning || m.previewMoreLoading || m.previewMoreErr != nil {
		return m, nil
	}
	if m.historyState != loadStateLoaded {
		return m, nil
	}

	// A page that says "no more" is the only answer that ends the fill, and
	// asking for nothing at all is not an answer.
	if !m.previewHasMore {
		return m, nil
	}
	if m.previewFillRequests >= maxHistoryFillRequests ||
		m.previewFillMessages >= maxHistoryFillMessages {
		return m, nil
	}

	// The window of the pane, measured the way the view measures it: by the
	// rows the messages take on the screen, not by a count against a guess
	// at how many messages fit in them.
	if m.feedIsFull() {
		return m, nil
	}

	boundary := historyBoundary(m.selected())
	if boundary == 0 {
		return m, nil
	}

	m.previewMoreLoading = true
	m.previewMoreErr = nil

	return m, loadPreviewCmd(
		m.source,
		m.selectedChatID(),
		boundary,
		historyPageSize,
		m.previewOperation,
		false,
	)
}

// updateChatPreviewLoaded puts the page of a preview into the pane.
func (m Model) updateChatPreviewLoaded(
	msg chatPreviewLoadedMsg,
) (tea.Model, tea.Cmd) {
	// The chat the cursor moved to has asked for its own page, the chat was
	// opened while this one was on its way, or the list has no pane beside
	// it: the answer is about nothing on the screen and is dropped. This is
	// where "the latest selection wins" is enforced.
	if msg.operation != m.previewOperation ||
		!m.chatPreviewShown() ||
		m.selectedChatID() != msg.chatID {
		return m, nil
	}

	if msg.err != nil {
		// The cause goes to the diagnostic stream and the pane says what
		// happened in words that are true (§11.3, §19).
		m.historyState = loadStateError
		m.reportDiagnostic("preview of chat %d: %v\n", msg.chatID, msg.err)

		return m, nil
	}

	m.previewMoreLoading = false
	m.previewFillRequests++

	page := chronological(safeMessages(msg.page.Messages))
	m.previewFillMessages += len(page)

	// A page that came back empty is the end of the fill, and there is
	// nothing older than it to ask about.
	if len(page) == 0 {
		m.previewHasMore = false

		// The newest page of a chat that has nothing in it is an empty chat,
		// and the pane already says so ("No messages yet"). An empty page
		// above the newest one is the other answer: there is nothing older
		// than the boundary, so the oldest message on the screen is where
		// the chat begins — and nothing above it is worth asking about.
		if !msg.newest {
			m.previewBeginning = true
		}

		return m, nil
	}

	if msg.newest {
		// The newest page of a chat that has something in it is the whole
		// of what has been read, and the fill starts from it.
		m.chats[m.selectedChat].Messages = page
		m.previewHasMore = msg.page.HasMore
	} else {
		m.chats[m.selectedChat].Messages = prependOlderMessages(
			m.chats[m.selectedChat].Messages,
			page,
		)
		m.historyExhausted = false
	}

	if len(m.chats[m.selectedChat].Messages) == 0 {
		m.historyState = loadStateEmpty
	} else {
		m.historyState = loadStateLoaded
	}

	// The window follows the page and is placed again where a page came in
	// above it, which is what keeps the newest message at the bottom: a
	// preview is a look at the end of a conversation (§8.3, §10.5).
	m = m.scrollToNewest()

	return m.fillChatPreview()
}

// loadPreviewCmd asks for the newest page of the chat the pane is about to
// show.
//
// It is the history load of a chat with the answer marked as a preview's,
// and it asks for the first page (fromMessageID zero): the end of the
// conversation is what a glance at a chat is about.
func loadPreviewCmd(
	src ChatSource,
	chatID int64,
	boundary int64,
	limit int,
	operation uint64,
	newest bool,
) tea.Cmd {
	history := loadHistoryCmd(src, chatID, boundary, limit, operation)

	return func() tea.Msg {
		loaded, isHistory := history().(historyLoadedMsg)
		if !isHistory {
			return nil
		}

		return chatPreviewLoadedMsg{
			chatID:    loaded.chatID,
			operation: loaded.operation,
			page:      loaded.page,
			err:       loaded.err,
			newest:    newest,
		}
	}
}

// The words at the top of a preview.
//
// The owner looked at a preview that showed only the newest messages on
// 03.10 and said the space above them was dark, and that «кто не
// пользовался, подумает, что там ничего нет или что сломано». Three
// sentences for three states, and none of them is silence:
//
//   - nothing has been asked for yet: the quiet empty state of §17, which
//     is what the pane says until the pause has run out;
//   - a page is on its way: `Loading history…`, the same words the timeline
//     uses while a first page is being read;
//   - the pane holds everything the chat has: `Beginning of the chat`,
//     which is what is above the oldest message rather than emptiness.
//
// The last one is the newest of the three and the one the owner asked for:
// a chat with three messages does not have a hole above them, it has an end.
const previewBeginningText = "Beginning of the chat"

// previewBeginningLines returns the line at the top of a preview.
//
// The loading line comes first: a pane that has asked for a page and has
// not got it is a wait, and a wait has to be named — the same rule as
// olderPageLines above the feed of an open conversation.
func (m Model) previewBeginningLines(layout Layout, width int) []string {
	// A chat with nothing on the screen has the sentence of the empty state
	// where its feed would be ("No messages yet", `Loading history...`, the
	// failure), and a second sentence above it says the same thing twice.
	if !m.chatPreviewShown() || len(m.selected().Messages) == 0 {
		return nil
	}

	styles := m.styles()

	switch {
	case m.historyState == loadStateLoading || m.previewMoreLoading:
		return []string{styles.dimmed(m.tokens().SecondaryText).
			Render(m.widths.Fit(previewLoadingText, width, ellipsis))}

	case m.previewBeginning:
		return []string{styles.dimmed(m.tokens().MutedText).
			Render(m.widths.Fit(previewBeginningText, width, ellipsis))}

	default:
		return nil
	}
}

// previewBeginningLineCount returns how many rows the line at the top of a
// preview takes, so that the messages below it are measured against what is
// really there.
func (m Model) previewBeginningLineCount(layout Layout, width int) int {
	return len(m.previewBeginningLines(layout, width))
}

// previewLoadingText is what the pane says while a page is on its way.
//
// It is the same sentence the timeline says while the first page of an
// opened chat is being read (timelineEmptyLines), because it is the same
// wait: the words were not invented for the preview and a user who has read
// one does not have to read the other.
const previewLoadingText = "Loading history..."

// previewHint is what the foot of a preview says.
//
// It says two things: that the pane is a preview, and what opens the chat
// for real. A foot that said only the second would be a conversation with
// no field under it and no word about why — the two things the owner named
// on 02.10, one after the other.
const previewHint = "Preview · Enter or Tab to open"

// previewFooterLines returns the rows of the foot of a preview, which is
// none on a screen too short for a hint bar (§3.4).
func (m Model) previewFooterLines(layout Layout, width int) []string {
	if layout.HideHints() {
		return nil
	}

	styles := m.styles()

	return []string{styles.dimmed(m.tokens().SecondaryText).
		Render(m.widths.Fit(previewHint, width, ellipsis))}
}

// previewFooterRows returns how many rows the foot of a preview takes.
func (m Model) previewFooterRows(layout Layout, width int) int {
	return len(m.previewFooterLines(layout, width))
}

// previewPaneRegion draws the preview of the chat under the cursor: the
// header of the conversation, the messages, and the foot that says what it
// is.
//
// There is no composer under them and no reason for one: the keys are in the
// list, and a field under a conversation with the focus elsewhere is a field
// that swallows nothing and promises a send that nothing will send.
// previewPaneRegion draws the preview of the chat under the cursor.
//
// It is the conversation region of an opened chat with its own foot under
// it, and it is the same drawing for the same reason — the pane is a
// conversation. The sentence about what is above the oldest message is drawn
// with the region (pageLinesBelowRule), where the progress of an older-page
// request stands in an open one, so the top of a preview reads as one region
// rather than as a sentence floating over the feed.
func (m Model) previewPaneRegion(layout Layout) string {
	width := layout.ChatContentWidth()
	footer := m.previewFooterRegion(layout, width)

	return m.joinRegions(
		m.conversationRegion(layout, width, layout.Height-lineCount(footer)),
		footer,
	)
}

// previewFooterRegion draws the foot of a preview.
func (m Model) previewFooterRegion(layout Layout, width int) string {
	lines := m.previewFooterLines(layout, width)
	if len(lines) == 0 {
		return ""
	}

	return m.renderRegion(m.styles().footer, width, lines, len(lines))
}

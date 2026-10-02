package tui

import (
	"context"
	"io"
	"sort"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"telecli/internal/tui/termwidth"
	"telecli/internal/tui/theme"
)

// sendState is the state of the current outbound send.
type sendState uint8

const (
	sendStateIdle sendState = iota
	sendStateSending
	sendStateError
)

// String returns a stable lowercase name for diagnostics.
func (s sendState) String() string {
	switch s {
	case sendStateIdle:
		return "idle"
	case sendStateSending:
		return "sending"
	case sendStateError:
		return "error"
	default:
		return "unknown"
	}
}

// Model is the top-level Bubble Tea model.
//
// When source == nil the model renders deterministic mock data,
// exactly as in PR-03. When source != nil the model renders
// asynchronous projections loaded through the ChatSource.
type Model struct {
	screen Screen
	focus  Focus

	source    ChatSource
	ctx       context.Context
	submitter ComposerSubmitter

	// accountKey is the stable account identifier used for delivery status
	// reads. It is empty when the TUI runs without an account.
	accountKey string

	// messageStatuses is the optional durable status source. It is nil in
	// direct delivery mode.
	messageStatuses MessageStatusSource

	// statusSummaries is the optional source of what the status line shows
	// about the connection and the queue. It is nil in a model built
	// without it, and then the interface draws no status line rather than
	// an empty one.
	statusSummaries StatusSummarySource

	// chatAccessSource is the optional source of whether this account can
	// write in the open chat, and chatAccess is what it last said.
	//
	// chatAccessKnown is what keeps the two apart. Without it the composer
	// would have to be drawn from a read that has not happened, and a chat
	// nobody asked about is a chat that can be written in: a field in a chat
	// Telegram refuses is the failure this answers, and it must not become
	// a composer that is missing in every chat TDLib was slow to answer
	// about.
	//
	// chatAccessChatID is the chat the read is about, and it is a field of
	// its own because an answer that arrives after the user has walked into
	// another chat is an answer about a chat that is no longer on the
	// screen.
	chatAccessSource  ChatAccessSource
	chatAccess        ChatAccess
	chatAccessKnown   bool
	chatAccessChatID  int64
	chatAccessLoading bool

	// presenceOpener is told which chat the user is looking at, and
	// openedChat is the one it was last told about. It is nil in a program
	// built without Telegram.
	//
	// openedAck is the chat whose openChat has been answered. It is a
	// separate word because having sent the call is not knowing that it
	// worked, and the messages of a chat TDLib has not loaded yet cannot
	// be read: a broadcast chat is loaded by openChat, and a read that
	// arrives first is refused with nothing on the screen to say so.
	presenceOpener ChatPresenceOpener
	openedChat     int64
	openedAck      int64

	// messageViewer is told which messages of the open chat are on the
	// screen, and viewedChat and viewedIDs are the window it was last told
	// about. It is nil in a program built without Telegram, and then
	// nothing anywhere is marked read.
	//
	// The window is remembered rather than asked about every time, because
	// a window that has not moved is a read that would say nothing, and a
	// round trip to TDLib for it.
	messageViewer MessageViewer
	viewedChat    int64
	viewedIDs     []int64

	// canceller cancels a queued message by the version the screen read,
	// and clipboard is the program's terminal: the frames and the copies
	// both go through it, under one lock.
	//
	// A nil canceller is a queue this program does not own, and the items
	// that need it are the ones it cannot perform. A nil clipboard is a
	// program with nowhere to send a copy.
	canceller MessageCanceller
	clipboard *terminalOutput

	// actionSheet is the menu of §13 and modal the question of §12.3.
	// Both are values on the model rather than screens: a popup is drawn
	// over the conversation, and the Esc hierarchy has one entry for it.
	actionSheet actionSheet
	modal       confirmModal

	// notice is the sentence in the status line that says what an action
	// did, noticeGeneration which notice is on the screen, and
	// noticeDeadline when it started being read.
	notice           string
	noticeGeneration uint64
	noticeDeadline   time.Time

	// summary is the last accepted status summary, and summaryErr the last
	// failure to read one.
	//
	// A failed read leaves summary alone: the counts of a queue that could
	// not be opened are not the counts of an empty one, and the connection
	// a client reported a moment ago has not stopped being true.
	summary        StatusSummary
	summaryErr     error
	summaryLoading bool

	// summaryReadSeq and statusReadSeq number the reads of each source, so
	// a read that was slow can be recognised and discarded after a newer
	// one answered.
	summaryReadSeq uint64
	statusReadSeq  uint64

	// deliveryStatuses is the last accepted status snapshot for the active
	// chat, replaced as a whole on every successful poll.
	deliveryStatuses []MessageStatus

	// pollTickArmed says a delivery tick is on its way.
	//
	// The loop is armed where polling becomes possible rather than at
	// startup: at startup there is no chat, deliveryPolling is false, and
	// the command that would have armed the tick returns nothing. Opening
	// a chat then did one read and no tick, so the read that answered it
	// was the last read of the session — which is the owner's report, a
	// message that stayed on Queued while the queue said sent, and a
	// screen that no amount of waiting would move.
	//
	// The flag is what keeps it to one loop. Arming on every chat change
	// without it would leave a tick per chat opened, each re-arming the
	// next.
	pollTickArmed bool

	messageStatusLoading    bool
	messageStatusErr        error
	messageStatusGeneration uint64
	messageStatusAccountKey string
	messageStatusChatID     int64

	chats        []Chat
	selectedChat int
	selectedMsg  int

	// chatSearch is the search of §9: the line above the list, the query
	// in it, and the list it narrows. It is a value on the model rather
	// than a screen, for the reason the action sheet is one: it is drawn
	// over the chat list rather than instead of it, and it has one entry
	// in the Esc hierarchy above the list (§8.5).
	chatSearch chatSearch

	chatsState   loadState
	historyState loadState
	loadErr      error

	// chatsLoadOperation counts the chat list loads, and chatsLoadSlow
	// says that the current one has taken longer than §18 allows.
	//
	// The count is what a deadline belongs to: a load the user retried has
	// a new one, and the deadline of the load before it must not announce
	// a wait that is already over.
	chatsLoadOperation uint64
	chatsLoadSlow      bool

	// historyExhausted records that a page added no new message, which is
	// the answer to a request for older messages that no older messages
	// exist. It is kept beside HistoryPage.HasMore because a source that
	// always says true is one that scrolls forever.
	historyExhausted bool

	// historyHasMore is what the last page said about the rest of the
	// history. It is false only for an empty page, and an empty page is
	// the one answer that means the beginning of the chat.
	historyHasMore bool

	// historyFillRequests counts the pages asked for after the first one,
	// and historyFillMessages how many messages they brought. They bound
	// the repeat below.
	historyFillRequests int
	historyFillMessages int

	// historyRefresh says the first page on arrival is a refresh of a chat
	// this program has drawn before, so it is merged rather than
	// replacing. Opening a conversation always asks for its newest page,
	// because a message Telegram holds and this program never learned
	// about is never asked for any other way.
	historyRefresh bool

	// historyMoreLoading reports that an older page request is in flight,
	// so repeated ↓ presses do not start a second request.
	historyMoreLoading bool

	// historyMoreErr is the last older-page failure, rendered below the
	// history. It is cleared by the next attempt.
	historyMoreErr error

	// pendingMessages is the source of the outgoing messages that the
	// history does not have yet, and pending is what the timeline draws.
	//
	// pending holds more than the source's own list: a message this session
	// queued stays on the screen after the queue has accepted it, so that
	// the user sees it go out. It leaves when the history brings it back.
	pendingMessages PendingMessageSource
	pending         []PendingMessage

	// pendingSnapshot is the last pending list a source returned, without
	// the messages this session queued by hand.
	//
	// The poll compares a read against this one and delivers nothing when
	// they are equal, and it cannot compare against pending: that one has
	// the local additions in it, so a queue that never changes would look
	// like it changed on every read.
	pendingSnapshot []PendingMessage

	// timelineTop is the index of the first message the conversation shows.
	//
	// It is the scroll position of §10.5: the message a user scrolled to
	// is the one that stays on screen when the terminal is resized or when
	// a page of older messages arrives above it. The cursor is
	// selectedMsg, and the two are separate because a reader who scrolls
	// with the keys and a reader who has scrolled to read something older
	// are in different places.
	timelineTop int

	// timelineCut is how many rows the view leaves off the top of the entry
	// timelineTop names.
	//
	// The rows of the feed are not a whole number of messages, and a window
	// that is filled to the last row of it has to draw the entry at its top
	// from the middle: what is left off is the blank row above the message
	// and, at worst, its author line. It is part of the anchor rather than
	// a scroll position of its own, and it is zero whenever the window was
	// not placed by anchoredAt — a message the cursor is on is never cut.
	timelineCut int

	// historyOperation identifies the current history load. It is bumped
	// on every entry into a conversation so that a page still in flight
	// for a previous entry is discarded instead of appended.
	historyOperation uint64

	sendState     sendState
	sendErr       error
	sendOperation uint64

	// lastSubmission is what the last durable submission answered, or nil,
	// and lastSubmittedText is the text that submission carried.
	//
	// The text is kept beside the submission and not inside it because a
	// submission is a queue fact and this is what the user wrote: the
	// timeline needs the text to draw the message, and the queue has it
	// only until the entry is accepted.
	lastSubmission    *Submission
	lastSubmittedText string

	// pausedErr is set when the composition root reported that delivery
	// cannot work at all. It outlives a per-chat send error, so the reason
	// follows the user into every chat instead of being cleared on entry.
	pausedErr error

	// diagnostics receives the causes the screen must not show: why a
	// message could not be queued, why the chat list could not be read.
	//
	// The interface shows a fixed sentence for those (§12.1) and the cause
	// goes here instead, so a TDLib error message or a file path stays out
	// of a terminal somebody is looking at over their shoulder. A nil
	// writer discards them, which is what a program built without one
	// gets.
	diagnostics io.Writer

	// composer is the draft and composerCursor the index in it.
	//
	// The cursor is its own field because a draft is not written at the
	// end: it is written where the user is looking, and a composer that can
	// only append cannot be corrected without deleting and retyping.
	composer       []rune
	composerCursor int

	// composerPlaceholderLit is the one style change of §7.3: pressing
	// Enter on a blank draft lights the placeholder for a moment, so that a
	// user who pressed Enter knows the composer was there.
	//
	// It is put out by one message rather than by a timer that repaints the
	// screen: §6.3 asks for no full-screen refresh loops, and one message
	// is not one.
	composerPlaceholderLit bool

	authPrompt   AuthPromptKind
	authCanceled bool

	// theme and colorProfile are the resolved interface theme and the
	// profile it was built for, and rendererForProfile is the Lip Gloss
	// renderer the views draw through.
	//
	// The renderer is built from the profile rather than from the
	// terminal, so a `color = "always"` from the configuration file is
	// what decides the colours. It is built once, where the profile is
	// resolved. A model that was constructed without a resolution has
	// none and gets one when it is asked to draw, which keeps a zero
	// model drawable rather than nil.
	theme              theme.Theme
	colorProfile       theme.Profile
	rendererForProfile *lipgloss.Renderer

	// widths is how every column of the screen is counted.
	//
	// It is a field of the model rather than a call because the terminal
	// is measured once, before the first frame, and a view that counted
	// columns for itself would be a view deciding what the terminal can
	// show. The zero value counts by code points, which is the rule of a
	// terminal nobody could ask.
	widths termwidth.WidthModel

	// nerdFont says that the terminal is drawn with a Nerd Font, and that
	// the block of a message of this user is rounded with the two halves
	// the font provides.
	//
	// It is false unless the configuration asked for it: a terminal
	// without the font draws the halves as empty squares, and empty
	// squares at both ends of every message of this user are worse than
	// the square corners they were meant to replace. A terminal does not
	// report its font, so this is the one thing about the screen the
	// program has to be told rather than measure.
	nerdFont bool

	width  int
	height int

	// now is the clock the views read a time from, and location the zone
	// they read it in.
	//
	// They are fields rather than calls into the time package because a
	// presence line says "last seen at 14:05" and a test of that sentence
	// has to know both the moment and the zone. A user reads the time in
	// their own zone, which is what the default is.
	now      func() time.Time
	location *time.Location

	// unreadBoundary is the last message of the open chat Telegram had been
	// told was read when the chat was opened, and it is what the unread line
	// of the feed is drawn from.
	//
	// It is remembered rather than read again because this program marks
	// what is on the screen as read (message_viewing.go). A pointer read
	// again after the first read of a window would have moved past the very
	// messages the line stands over, and the line would go as soon as the
	// reader looked at the conversation — which is exactly the moment it is
	// for. Telegram is told the window; the screen keeps the line.
	//
	// It is zero for a chat with nothing read in it and for a chat the
	// source could not say anything about, and both draw no line at all.
	unreadBoundary int64

	// hourFormat is the format the hour is written in: 20:06 or 08:06 PM.
	//
	// It is a field for the reason now and location are. A message of
	// 21:21 UTC is 07:21 in Vladivostok, and which of those two spellings
	// is on the screen is the machine's answer — and a test of the screen
	// has to be able to state that answer instead of reading it from the
	// runner. See clock.go.
	hourFormat ClockFormat

	quitting bool
}

// Theme returns the interface theme the model draws with and the colour
// profile it was built for.
//
// The views read it as they draw, and it is how a caller tells which theme
// a model uses without looking at the screen.
func (m Model) Theme() (theme.Theme, theme.Profile) {
	return m.theme, m.colorProfile
}

// NewModel returns a model in the Chats screen with deterministic mock
// data and focus on the chat list.
//
// It is the mock-only constructor: Init returns no command, and View
// renders mockChats immediately.
//
// The model starts on the default theme with no colour profile. A model
// built without a resolution cannot know what the terminal can show, and
// unstyled text is always legible, while a profile guessed to be True
// Color on a terminal that cannot show it would be neither.
func NewModel() Model {
	return Model{
		screen:       ScreenChats,
		focus:        FocusChatList,
		ctx:          context.Background(),
		chats:        mockChats(),
		chatsState:   loadStateLoaded,
		historyState: loadStateIdle,
		sendState:    sendStateIdle,
		theme:        theme.DefaultTheme(),
		colorProfile: theme.ProfileNoColor,
		// A model built without a measurement counts by code points, which
		// is what a terminal nobody could ask is drawn with.
		widths: termwidth.Unmeasured(termwidth.ModeAuto),
		// The clock of the mock screen is the moment its data was written
		// for, in the zone it was written in. The mock data is fixed, so a
		// screen drawn from the machine's clock would say a different day
		// every day of the year — the feed of §8.3 names the day a message
		// is on, and a mock screen whose "Today" is not today is a screen
		// that cannot be read.
		now:      func() time.Time { return mockClock },
		location: mockZone,
	}.withRenderer(theme.ProfileNoColor)
}

// NewModelWithSource returns a model that loads chats and history from
// source.
//
// Init returns listChatsCmd; View renders a loading placeholder until
// chatsLoadedMsg arrives.
func NewModelWithSource(source ChatSource) Model {
	return Model{
		screen:       ScreenChats,
		focus:        FocusChatList,
		source:       source,
		ctx:          context.Background(),
		chatsState:   loadStateLoading,
		historyState: loadStateIdle,
		sendState:    sendStateIdle,
		colorProfile: theme.ProfileNoColor,
		widths:       termwidth.Unmeasured(termwidth.ModeAuto),
		now:          time.Now,
		location:     time.Local,
	}.withRenderer(theme.ProfileNoColor)
}

// withRenderer returns the model with a renderer for a profile.
//
// The renderer is built once per model rather than per frame: a view that
// built its own would be deciding for itself what the terminal can show,
// which is the one decision the composition root already made.
func (m Model) withRenderer(profile theme.Profile) Model {
	m.colorProfile = profile
	m.rendererForProfile = newRenderer(profile)

	return m
}

// Init implements tea.Model.
//
// The delivery poll starts here rather than when a chat is opened: the
// status line is in the chat list header on a narrow screen, where no chat
// is open, and the queue it counts is the program's rather than a chat's.
func (m Model) Init() tea.Cmd {
	var cmds []tea.Cmd
	if m.source != nil && m.chatsState == loadStateLoading {
		m.chatsLoadOperation++
		cmds = append(
			cmds,
			listChatsCmd(m.source),
			scheduleChatsLoadDeadline(m.chatsLoadOperation),
		)
	}
	if cmd := m.pollDeliverySources(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if len(cmds) == 0 {
		return nil
	}

	return tea.Batch(cmds...)
}

// Update implements tea.Model.
//
// The read of the window is asked here rather than in the places that move
// it: the window moves on a key, on a resize, on a page of history and on a
// message sent, and a place that forgets one is a message the owner can see
// and nobody marked read. One question after every message is a question
// that cannot be forgotten, and it asks nothing while the answer is the
// window that was marked last.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	updated, cmd := m.update(msg)

	next, isModel := updated.(Model)
	if !isModel {
		return updated, cmd
	}

	return next, tea.Batch(cmd, next.markVisibleMessagesViewed())
}

func (m Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m.updateWindowSize(msg)

	case chatsLoadedMsg:
		return m.updateChatsLoaded(msg)

	case historyLoadedMsg:
		return m.updateHistoryLoaded(msg)

	case messageSentMsg:
		return m.updateMessageSent(msg)

	case composerSubmissionMsg:
		return m.updateComposerSubmission(msg)

	case chatOpenedMsg:
		return m.updateChatOpened(msg), nil

	case messagesViewedMsg:
		return m.updateMessagesViewed(msg)

	case messageStatusesLoadedMsg:
		return m.handleMessageStatusesLoaded(msg)

	case messageStatusesFailedMsg:
		return m.handleMessageStatusesFailed(msg)

	case statusSummaryLoadedMsg:
		return m.handleStatusSummaryLoaded(msg)

	case statusSummaryFailedMsg:
		return m.handleStatusSummaryFailed(msg)

	case chatAccessLoadedMsg:
		return m.handleChatAccessLoaded(msg)

	case chatAccessFailedMsg:
		return m.handleChatAccessFailed(msg)

	case chatsLoadDeadlineMsg:
		return m.updateChatsLoadDeadline(msg)

	case presenceExpiredMsg:
		return m.handlePresenceExpired(msg)

	case messageCancelFailedMsg:
		return m.handleMessageCancelFailed(msg)

	case noticeExpiredMsg:
		return m.handleNoticeExpired(msg)

	case composerPlaceholderExpiredMsg:
		m.composerPlaceholderLit = false
		return m, nil

	case messageStatusPollTickMsg:
		return m.handleMessageStatusPollTick(msg)

	case tea.KeyMsg:
		return m.updateKey(msg)

	default:
		return m, nil
	}
}

func (m Model) updateWindowSize(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	// Whether the window is following the conversation is asked before the
	// size changes: it is a question about where the user is, and the rows
	// of the feed are what the answer is measured against.
	following := m.timelineFollowsNewest()

	m.width = msg.Width
	m.height = msg.Height

	// The focus follows the screen, and so does the scroll: a size that
	// took a region away must not leave the keys on a region that is no
	// longer drawn, and a narrower screen shows the same message at the
	// top of the window (§10.5).
	m = m.normalizeTimeline().normalizeFocus()

	// A window that was following is filled from the bottom again, because
	// the rows of the feed are not the rows it was placed against: a taller
	// screen would leave empty rows under the newest message and a shorter
	// one would cut it off. A window a reader has scrolled away from keeps
	// its message at the top (§10.5), and the cut that belonged to the rows
	// it was placed against is nothing to do with the new ones.
	if following {
		m = m.anchorAtNewest()
	} else {
		m.timelineCut = 0
	}

	// The search is a region of the chat list, and a narrow screen has no
	// chat list beside the conversation. A search that is not on the
	// screen is not one anybody can type into, so the resize that hides
	// it closes it — the same rule as the focus: a region that is not
	// drawn does not keep the keys.
	if m.chatSearch.open && !m.chatListDrawn() {
		m = m.cancelChatSearch()
	}

	return m, nil
}

func (m Model) updateChatsLoaded(msg chatsLoadedMsg) (tea.Model, tea.Cmd) {
	m.chatsLoadSlow = false

	if msg.err != nil {
		m.chatsState = loadStateError
		m.loadErr = msg.err
		// The cause can name a file, a TDLib error message or a path, and
		// the screen shows a fixed sentence instead (§11.3, §19).
		m.reportDiagnostic("chat list unavailable: %v\n", msg.err)
		return m, nil
	}

	m.loadErr = nil

	// The chat under the cursor is remembered by its identifier and not by
	// its place in the list. The list is read again after every read of a
	// chat (updateMessagesViewed), and a list whose order moved under the
	// cursor would otherwise show another conversation than the one that was
	// on the screen a moment ago — a chat that received a message while this
	// one was being read is enough to move it.
	selected := m.selectedChatID()

	// The list crosses into the model here, and it is cleaned on the way:
	// every name and every preview in it is text from Telegram, and a name
	// the terminal acts on is a row of the list that moves the cursor
	// instead of being drawn. See screen_text.go.
	m.chats = mergeLoadedChats(safeChats(msg.chats), m.chats, m.openedChat)

	if len(msg.chats) == 0 {
		m.chatsState = loadStateEmpty
		m.selectedChat = 0
		return m, nil
	}

	m.chatsState = loadStateLoaded

	if index, found := m.indexOfChat(selected); found {
		m.selectedChat = index
	} else if m.selectedChat >= len(m.chats) {
		m.selectedChat = len(m.chats) - 1
	}

	// A search that is open narrows whatever arrived, and the cursor goes
	// to the first of it: the list the user was looking at is a different
	// list now, and the chat under the cursor has to be one of the chats
	// that are on the screen.
	if m.chatSearch.open {
		m.selectFirstChatMatch()
	}

	return m, nil
}

// mergeLoadedChats keeps what a chat had when the list is read again.
//
// A row of the list carries no messages: the history of a conversation is a
// separate read, and a list that arrived without it would empty the
// conversation beside it and the one behind it on the way back in. The
// messages are what the owner is reading at that moment, and re-opening the
// chat asks for its newest page anyway (openSelectedChat), so keeping them
// costs nothing and takes away a screen that goes blank for no reason.
//
// The row of the open chat is kept as well, and for the same reason. The
// list is read with a limit, so a chat can fall outside it without anything
// having removed it, and the conversation on the screen is drawn from that
// row: a read that took the open chat off the screen would be a read that
// cost the user the chat they were in.
func mergeLoadedChats(loaded, previous []Chat, openChat int64) []Chat {
	if len(previous) == 0 {
		return loaded
	}

	byID := make(map[int64]Chat, len(previous))
	for _, chat := range previous {
		byID[chat.ID] = chat
	}

	for index := range loaded {
		if was, known := byID[loaded[index].ID]; known {
			loaded[index].Messages = was.Messages
		}
	}

	if openChat == 0 {
		return loaded
	}
	if _, listed := byID[openChat]; !listed {
		return loaded
	}
	for _, chat := range loaded {
		if chat.ID == openChat {
			return loaded
		}
	}

	return append(loaded, byID[openChat])
}

// selectedChatID is the chat under the cursor, or zero where there is none.
func (m Model) selectedChatID() int64 {
	if m.selectedChat < 0 || m.selectedChat >= len(m.chats) {
		return 0
	}

	return m.chats[m.selectedChat].ID
}

// indexOfChat returns where a chat is in the list, and whether it is there.
func (m Model) indexOfChat(chatID int64) (int, bool) {
	if chatID == 0 {
		return 0, false
	}

	for index, chat := range m.chats {
		if chat.ID == chatID {
			return index, true
		}
	}

	return 0, false
}

// clock returns the model's clock, building a default one for a model that
// was constructed as a value rather than by a constructor.
func (m Model) clock() func() time.Time {
	if m.now == nil {
		return time.Now
	}

	return m.now
}

// timeZone returns the zone the views read times in.
func (m Model) timeZone() *time.Location {
	if m.location == nil {
		return time.Local
	}

	return m.location
}

// updateChatsLoadDeadline announces a chat list load that has taken
// longer than §18 allows.
//
// The announcement is a sentence about the wait and a key that ends it. A
// spinner would say that something is happening; what a user needs to know
// is that the list is not coming on its own and that R will ask again.
func (m Model) updateChatsLoadDeadline(
	msg chatsLoadDeadlineMsg,
) (tea.Model, tea.Cmd) {
	if msg.operation != m.chatsLoadOperation {
		return m, nil
	}
	if m.chatsState != loadStateLoading {
		return m, nil
	}

	m.chatsLoadSlow = true

	return m, nil
}

// startChatsLoad begins a chat list load and arms the wait of §18.
func (m *Model) startChatsLoad() tea.Cmd {
	if m == nil || m.source == nil {
		return nil
	}

	m.chatsState = loadStateLoading
	m.chatsLoadSlow = false
	m.loadErr = nil
	m.chatsLoadOperation++

	return tea.Batch(
		listChatsCmd(m.source),
		scheduleChatsLoadDeadline(m.chatsLoadOperation),
	)
}

// updateHistoryLoaded applies a history response.
//
// A response is discarded when it belongs to another chat, or when it
// belongs to a previous entry into the current chat.
//
// A response for fromMessageID == 0 is the first page and replaces the
// chat's messages. A response for a non-zero boundary is an older page
// and is appended, keeping the selection where it is.
func (m Model) updateHistoryLoaded(msg historyLoadedMsg) (tea.Model, tea.Cmd) {
	if m.selectedChat < 0 ||
		m.selectedChat >= len(m.chats) ||
		m.chats[m.selectedChat].ID != msg.chatID {
		return m, nil
	}

	if msg.operation != m.historyOperation {
		return m, nil
	}

	if msg.fromMessageID != 0 {
		return m.appendOlderHistory(msg)
	}

	if msg.err != nil {
		m.historyState = loadStateError
		m.loadErr = msg.err
		return m, nil
	}

	m.loadErr = nil

	// A page crosses into the model here, and it is cleaned on the way:
	// the text of a message is the only text anybody else in a chat can
	// put on the screen. See screen_text.go.
	page := chronological(safeMessages(msg.page.Messages))
	if m.historyRefresh {
		m.chats[m.selectedChat].Messages = mergeMessages(
			m.chats[m.selectedChat].Messages, page,
		)
	} else {
		m.chats[m.selectedChat].Messages = page
	}
	m.historyRefresh = false

	// A new first page re-opens the history: nothing is known to be
	// missing from it yet.
	m.historyExhausted = false
	m.historyHasMore = msg.page.HasMore
	m.historyMoreErr = nil
	m.historyFillRequests = 1
	m.historyFillMessages = len(m.chats[m.selectedChat].Messages)
	m.reportUnreadableHistory(msg)

	if len(msg.page.Messages) == 0 {
		m.historyState = loadStateEmpty
	} else {
		m.historyState = loadStateLoaded
	}

	// A first page opens the conversation at its end: the newest message is
	// the one a user came to read (§8.3).
	//
	// The page that came back is not necessarily the page the chat has: a
	// conversation opens at its end and the messages above it are what the
	// screen is for, so the ask repeats until there are enough of them to
	// fill it.
	return m.scrollToNewest().fillHistory()
}

// reportUnreadableHistory writes the count of the entries of a page the
// source could not read.
//
// It is a number and never a text: a message of this user that is missing
// because the source could not read it is a message that is not on the
// screen, and the interface has no way of saying so without putting a
// sentence about somebody's message history in front of them. The chat
// identifier is safe — it is an integer TDLib assigned, and it is what
// makes a line in a log useful.
func (m Model) reportUnreadableHistory(msg historyLoadedMsg) {
	if msg.err != nil || msg.page.Unreadable == 0 {
		return
	}

	m.reportDiagnostic(
		"history of chat %d: %d entries of the page could not be read\n",
		msg.chatID, msg.page.Unreadable,
	)
}

// appendOlderHistory puts a page of older messages on top of the ones
// already loaded.
//
// NextFrom is inclusive, so the boundary message of the previous page is
// repeated here and is dropped by ID. A page that adds nothing means the
// history is exhausted, whatever HistoryPage.HasMore claims.
//
// A failure keeps the loaded messages and the cursor, so the next ↑
// repeats the same request.
func (m Model) appendOlderHistory(msg historyLoadedMsg) (tea.Model, tea.Cmd) {
	m.historyMoreLoading = false

	if msg.err != nil {
		m.historyMoreErr = msg.err
		return m, nil
	}

	m.historyMoreErr = nil
	m.historyHasMore = msg.page.HasMore
	m.historyFillRequests++
	m.historyFillMessages += len(msg.page.Messages)
	m.reportUnreadableHistory(msg)

	existing := m.chats[m.selectedChat].Messages
	// Whether the window is following the conversation is asked before the
	// page goes on top: it is a question about where the user is, and the
	// page moves every index below.
	following := m.timelineFollowsNewest()

	merged := prependOlderMessages(
		existing,
		chronological(safeMessages(msg.page.Messages)),
	)
	m.chats[m.selectedChat].Messages = merged

	// The page went on top, so the message that was on screen is exactly
	// what was added lower down. Moving the cursor by that much is what
	// keeps a reader in place: this is the one place where reading upwards
	// could punish a user for asking for more.
	added := len(merged) - len(existing)
	m.selectedMsg += added

	// The window is placed one way or the other, and which way is the
	// difference between a conversation that fills the screen and one that
	// does not. A user at the end of the conversation gets the window
	// filled again from the bottom, so the newest message is on the last
	// row and the rows above it are messages: a window moved down by the
	// length of the page would show a screenful of older messages and cut
	// the newest off the bottom, which is a conversation that opens with a
	// few messages in the corner of an empty feed. A user reading upwards
	// gets the window moved by what the page added, which is what keeps the
	// message they are reading on the same row.
	if following {
		m = m.anchorAtNewest()
	} else {
		m.timelineTop, m.timelineCut = m.timelineTop+added, 0
	}

	if len(merged) == len(existing) {
		m.historyExhausted = true
	}

	// A page that arrived after the first one is one the user asked for,
	// and the ask repeats itself while the feed is still short of a
	// screenful: the whole point of the page is to have messages in the
	// conversation, and half a screen of them is not a conversation.
	return m.normalizeTimeline().fillHistory()
}

// fillHistory asks for the page above the one that arrived, while the
// conversation has fewer messages in it than the feed has rows for it.
//
// It is what makes a chat open with a screen of messages rather than with
// the one or two TDLib had under its hand: the first page of a real
// account comes back with whatever the local database holds, however many
// were asked for, and a conversation that stops there is a user reading
// the last two messages of a chat they have been writing in for years.
//
// The repeat is bounded three ways, and every one of them is a real cost
// rather than a precaution: a page that is empty is the end of the
// history, a source that has nothing more to give says so, and a channel
// with a hundred thousand messages in it must not be read into memory to
// draw one screen of it.
func (m Model) fillHistory() (tea.Model, tea.Cmd) {
	if m.source == nil ||
		m.historyExhausted ||
		!m.historyHasMore ||
		m.historyMoreLoading {
		return m, nil
	}

	if m.historyFillRequests >= maxHistoryFillRequests ||
		m.historyFillMessages >= maxHistoryFillMessages {
		return m, nil
	}

	chat := m.selected()
	boundary := historyBoundary(chat)
	if chat.ID == 0 || boundary == 0 {
		return m, nil
	}

	if m.feedIsFull() {
		return m, nil
	}

	m.historyMoreLoading = true

	return m, loadHistoryCmd(
		m.source,
		chat.ID,
		boundary,
		historyPageSize,
		m.historyOperation,
	)
}

// feedIsFull reports whether the window is drawn over the whole of the
// messages that are loaded, which is the same question the placement of the
// window answers and the same answer: an anchor that is not the oldest
// loaded message means there are messages above the first row of the feed.
//
// It is asked in messages and answered by the window on purpose. The window
// knows how tall the messages are and this does not, and the version of
// this that counted messages against a guess at how many of them fit is
// what opened a chat with a screenful that was a third of a screen short
// (#57).
//
// A model nobody has drawn yet is a model with no rows at all, and rows it
// does not have are not rows it can ask for a page to fill: the size
// arrives before anything else in a program, and the window is placed again
// by the first one that does.
func (m Model) feedIsFull() bool {
	if m.width < 1 || m.height < 1 {
		return true
	}

	return m.timelineTop > 0
}

const (
	// maxHistoryFillRequests is how many pages one conversation asks for
	// above the first before it draws what it has.
	//
	// Five is a screenful several times over on a tall terminal and one
	// screenful on a short one, and it is enough for a TDLib that answers
	// two messages at a time to fill the feed. The bound is here so that a
	// source which never says "no" costs a fixed number of round trips
	// rather than the whole history of a channel.
	maxHistoryFillRequests = 5

	// maxHistoryFillMessages is how many messages those requests may
	// bring in, whatever they asked for.
	//
	// TDLib caps a page at 100 messages, and five pages of them is five
	// hundred messages of a chat held in memory to draw thirty rows of it.
	// The feed is what the screen needs; the rest is what the user asks
	// for one page at a time.
	maxHistoryFillMessages = 100
)

// chronological returns the messages of a page oldest first.
//
// TDLib answers a history request newest first: the first message of the
// page is the newest one it has. A conversation is read the other way
// round (§8.3, and divergence 1 of the specification), so the order is
// reversed here rather than in the source. The source is what it is, and
// the interface is what the specification describes.
// ConversationMessageIDs returns the identifiers of the messages the open
// conversation is holding, oldest first.
//
// It is exported for one reason: a message this program sent has to end up
// in a conversation under the identifier Telegram gave it, and that is a
// claim about the identifiers rather than about the drawing. A test inside
// this package can read the messages directly; the composition root, which
// is where the whole path from a key press to a delivered message is driven
// end to end, cannot.
//
// It is a read of a slice the model already holds: no copy is made and
// nothing is fetched.
func (m Model) ConversationMessageIDs() []int64 {
	if m.selectedChat < 0 || m.selectedChat >= len(m.chats) {
		return nil
	}

	messages := m.chats[m.selectedChat].Messages
	ids := make([]int64, 0, len(messages))
	for _, message := range messages {
		ids = append(ids, message.ID)
	}

	return ids
}

// mergeMessages folds a page into what is already held, by identifier.
//
// It is what a refresh does rather than a replacement: a page that arrives
// on a chat this program has drawn before brings whatever Telegram has that
// the program did not know about, and the messages it already had are still
// true. Replacing would throw away the pages a user has scrolled back
// through and make every visit to a chat re-read them.
//
// The order is by identifier, ascending, because that is the order the rest
// of the file keeps a conversation in: the cache is oldest first, a page
// arrives newest first, and the boundary older pages are asked from is
// Messages[0].
func mergeMessages(held, page []Message) []Message {
	if len(held) == 0 {
		return chronological(page)
	}
	if len(page) == 0 {
		return held
	}

	byID := make(map[int64]Message, len(held)+len(page))
	ids := make([]int64, 0, len(held)+len(page))
	for _, message := range append(
		append(make([]Message, 0, len(held)+len(page)), held...), page...,
	) {
		if _, seen := byID[message.ID]; !seen {
			ids = append(ids, message.ID)
		}
		// The page wins for a message both hold: it is what Telegram says
		// now, and it is a request away.
		byID[message.ID] = message
	}

	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	merged := make([]Message, 0, len(ids))
	for _, id := range ids {
		merged = append(merged, byID[id])
	}

	return merged
}

func chronological(messages []Message) []Message {
	if len(messages) < 2 {
		return messages
	}

	result := make([]Message, 0, len(messages))
	for index := len(messages) - 1; index >= 0; index-- {
		result = append(result, messages[index])
	}

	return result
}

// prependOlderMessages puts a page of older messages on top of the loaded
// ones, dropping every message whose ID is already loaded.
//
// The page is older than everything loaded, so it goes first: the order of
// the conversation is the order it is read in, and a page that arrived
// wrong would put the newest message in the middle.
func prependOlderMessages(messages []Message, older []Message) []Message {
	if len(older) == 0 {
		return messages
	}

	seen := make(map[int64]struct{}, len(messages)+len(older))
	for _, current := range messages {
		seen[current.ID] = struct{}{}
	}

	result := make([]Message, 0, len(messages)+len(older))
	for _, candidate := range older {
		if _, duplicate := seen[candidate.ID]; duplicate {
			continue
		}
		seen[candidate.ID] = struct{}{}
		result = append(result, candidate)
	}

	return append(result, messages...)
}

// historyBoundary returns the inclusive boundary for the next older page
// of chat, or zero when there is nothing to continue from.
//
// HistoryPage.NextFrom is by definition the ID of the oldest message in
// the page it came from, so the boundary is read from the oldest loaded
// message rather than stored. Deriving it means the cursor can never
// drift from the cache: re-entering a chat whose messages are already
// loaded keeps paginating without refetching the first page.
//
// The oldest message is the first one: Chat.Messages is oldest first.
func historyBoundary(chat Chat) int64 {
	if len(chat.Messages) == 0 {
		return 0
	}

	return chat.Messages[0].ID
}

// loadOlderMessages requests the next older page of the open chat's
// history.
//
// It returns no command when the request is not applicable: mock mode, an
// exhausted history, a source that has said the history ended, a request
// already in flight, a history that has not loaded yet, or no boundary to
// continue from.
func (m Model) loadOlderMessages() (tea.Model, tea.Cmd) {
	if m.source == nil {
		return m, nil
	}
	if m.historyExhausted || m.historyMoreLoading {
		return m, nil
	}
	if !m.historyHasMore {
		return m, nil
	}
	if m.historyState != loadStateLoaded && m.historyState != loadStateEmpty {
		return m, nil
	}

	chat := m.selected()
	boundary := historyBoundary(chat)
	if chat.ID == 0 || boundary == 0 {
		return m, nil
	}

	m.historyMoreLoading = true
	m.historyMoreErr = nil

	return m, loadHistoryCmd(
		m.source,
		chat.ID,
		boundary,
		historyPageSize,
		m.historyOperation,
	)
}

// updateMessageSent applies an outbound send result.
//
// Staleness is checked in two steps:
//
//  1. msg.operation must equal m.sendOperation. A late result from a
//     superseded attempt (for example after the user left and
//     re-entered the same chat) is discarded.
//  2. msg.chatID must match the currently selected chat.
//
// On success:
//
//   - composer is cleared;
//   - sendState becomes idle;
//   - the message is prepended newest-first;
//   - duplicate message IDs are removed.
//
// On error:
//
//   - sendState becomes error;
//   - sendErr is retained for rendering;
//   - composer is preserved verbatim.
func (m Model) updateMessageSent(msg messageSentMsg) (tea.Model, tea.Cmd) {
	if msg.operation != m.sendOperation {
		return m, nil
	}
	if m.selectedChat < 0 ||
		m.selectedChat >= len(m.chats) ||
		m.chats[m.selectedChat].ID != msg.chatID {
		return m, nil
	}

	if msg.err != nil {
		m.sendState = sendStateError
		m.sendErr = msg.err
		// The cause goes to the log and the screen keeps §12.1's two
		// sentences. The text that was not queued is deliberately absent
		// here: a log of failures is a log somebody pastes into an issue.
		m.reportDiagnostic(
			"message was not queued for chat %d: %v\n",
			msg.chatID,
			msg.err,
		)

		return m, nil
	}

	m.sendState = sendStateIdle
	m.sendErr = nil
	m.composer = nil

	// A message that was sent is the newest one, so it goes to the end of
	// the conversation. A reader who is at the end follows it: they are
	// watching this conversation for what is new in it. A reader who has
	// scrolled up to read something older is left where they are, because
	// a message arriving is not a reason to lose their place.
	// Whether the reader is at the newest thing in the conversation, which
	// is the feed's own question and not the history's: a row of the queue
	// from yesterday is not the newest message of the chat, and asking the
	// history alone would have answered about the wrong list.
	atNewest := m.timelineFollowsNewest()

	m.chats[m.selectedChat].Messages = appendMessageByID(
		m.chats[m.selectedChat].Messages,

		// The answer to a send is TDLib's copy of what was sent, and it
		// crosses into the model like any other message. See
		// screen_text.go.
		safeMessage(msg.message),
	)

	if atNewest {
		return m.scrollToNewest(), nil
	}

	return m, nil
}

func (m Model) updateComposerSubmission(msg composerSubmissionMsg) (tea.Model, tea.Cmd) {
	if msg.operation != m.sendOperation {
		return m, nil
	}
	if m.selectedChat < 0 ||
		m.selectedChat >= len(m.chats) ||
		m.chats[m.selectedChat].ID != msg.chatID {
		return m, nil
	}

	if msg.err != nil {
		m.sendState = sendStateError
		m.sendErr = msg.err
		// The cause goes to the log and the screen keeps §12.1's two
		// sentences. The text that was not queued is deliberately absent
		// here: a log of failures is a log somebody pastes into an issue.
		m.reportDiagnostic(
			"message was not queued for chat %d: %v\n",
			msg.chatID,
			msg.err,
		)

		return m, nil
	}

	m.sendState = sendStateIdle
	m.sendErr = nil

	// Whether the reader was watching the newest thing, asked BEFORE the
	// row is added: a message a user has just sent is the newest thing in
	// the conversation, and a reader who was at the end is watching for it.
	// A reader scrolled up into the history is left exactly where they
	// are, which is the same rule every other arrival follows.
	wasAtNewest := m.timelineFollowsNewest()

	// The message is in the timeline before the draft is gone: a user who
	// pressed Enter has to see where the message went, and the history does
	// not have it yet — Telegram has not seen it at all (§4.4).
	m.noteQueuedMessage(
		msg.submission.ID,
		msg.chatID,
		string(m.lastSubmittedText),
		deliveryStateOfSubmission(msg.submission.State),
		m.clock()(),
	)

	// The window and the cursor follow the message that was just sent.
	//
	// The row is the newest thing in the conversation, and the window is
	// not moved onto it, so the message a user has just sent is drawn off
	// the bottom of a feed that is already full — the reader presses Enter,
	// and the screen does not move. Then the read that confirms the send
	// finds the cursor one message behind the end of the conversation, so
	// it does not place the window either, and the message the queue
	// accepted stays off the screen until a key is pressed.
	//
	// Placing the window here is what makes the delivery's own placement
	// work: a reader at the end is a reader at the end afterwards too.
	if wasAtNewest {
		m = m.scrollToNewest()
	}

	// The draft is cleared only now, after the queue has taken it (§7.1),
	// and the cursor goes with it: the next draft starts at its beginning.
	m.composer = nil
	m.composerCursor = 0
	submission := msg.submission
	m.lastSubmission = &submission

	// The screen is asked to redraw, and the queue is asked again at once.
	//
	// Without this the model returned a nil command and the feed sat on
	// what the submission said — Queued — until the next tick, which is
	// two seconds away and needs no key to arrive but does need the tick
	// loop to still be running. The read is the immediate half: the
	// submission's own answer predates the dispatch, and the state the
	// user is watching is whatever the queue says a moment later.
	return m, withRepaint(m, m.refreshMessageStatuses())
}

// deliveryStateOfSubmission maps what the queue answered onto the state the
// timeline draws.
//
// The queue answers queued or sent; anything else is drawn as queued,
// because a message the queue has not accepted yet is a message the
// dispatcher owns, and the first read that follows will say more.
func deliveryStateOfSubmission(state SubmissionState) MessageDeliveryState {
	if state == SubmissionSent {
		return MessageDeliverySent
	}

	return MessageDeliveryQueued
}

// appendMessageByID puts a message at the end of a conversation, replacing
// an entry with the same ID rather than repeating it.
//
// The end is where a message that has just been sent belongs: it is the
// newest one, and a conversation is read with the newest at the bottom.
func appendMessageByID(messages []Message, message Message) []Message {
	result := make([]Message, 0, len(messages)+1)
	for _, current := range messages {
		if current.ID == message.ID {
			continue
		}
		result = append(result, current)
	}

	return append(result, message)
}

func (m Model) updateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyCtrlC {
		if m.screen == ScreenAuth {
			m.composer = nil
			m.composerCursor = 0
			m.authCanceled = true
		}
		m.quitting = true
		m.invalidateMessageStatusPolling()
		// A chat that is still open when the program quits is closed on
		// the way out, or TDLib keeps counting a chat nobody is looking
		// at until the process is gone.
		return m, tea.Batch(m.closeConversationChat(), tea.Quit)
	}

	// §8.5 puts the search above the composer in the Esc hierarchy, so
	// Esc closes it before it means anything else — including when the
	// focus has moved on to another region while the search is open. A
	// popup is higher in the hierarchy still, and it is the thing whose
	// question or menu the Esc is about.
	if m.chatSearch.open && msg.Type == tea.KeyEsc && !m.popupOpen() {
		return m.cancelChatSearch(), nil
	}

	switch m.screen {
	case ScreenChats:
		return m.updateChatsKey(msg)
	case ScreenConversation:
		return m.updateConversationKey(msg)
	case ScreenAuth:
		return m.updateAuthKey(msg)
	default:
		return m, nil
	}
}

// updateChatsKey handles the chat list screen.
//
// Esc does nothing here. §8.5 ends the Esc hierarchy at the chat list,
// and leaving the program is `q`, which is the one key the hint bar names:
// a key that quits is a key a user presses by accident, and there is
// Ctrl+C for the deliberate case.
func (m Model) updateChatsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// The search line is a region of the list (§5), so while the keys are
	// in it they are its: the list keeps its own keys for when the focus
	// has been moved back to the rows.
	if m.focus == FocusSearch {
		return m.updateChatSearchKey(msg)
	}

	switch {
	case msg.Type == tea.KeyEsc:
		return m, nil

	case isQuit(msg):
		m.quitting = true
		m.invalidateMessageStatusPolling()
		// A chat that is still open when the program quits is closed on
		// the way out.
		return m, tea.Batch(m.closeConversationChat(), tea.Quit)

	case isTab(msg):
		// §8.1 makes Tab a key of the whole interface, and with a search
		// open the chat list is two regions rather than one (§5). With no
		// search there is nothing to walk to, which is what the key has
		// always done on this screen.
		return m.cycleFocus(1), nil

	case isShiftTab(msg):
		return m.cycleFocus(-1), nil

	case isReloadChats(msg):
		// §18: the waiting is over and the load is asked again. R is a
		// key of the list and not of the composer, where §8.4 turns
		// single letters back into text.
		return m, m.startChatsLoad()

	case isChatSearch(msg):
		// §9: the search line opens above the list and takes the keys.
		return m.openChatSearch(), nil

	case msg.Type == tea.KeyEnter:
		if len(m.chats) == 0 {
			return m, nil
		}
		// Enter opens the selected chat and puts the cursor where the
		// next message is written. Every mock screen of §3.1 to §3.3
		// shows an open conversation with the composer focused, and a
		// user who opened a chat is about to write in it. A search
		// narrows what Enter means to the results, and the search is
		// left behind.
		return m.openSelectedResult(true)

	case isUp(msg):
		return m.moveChatSelection(-1)

	case isDown(msg):
		return m.moveChatSelection(1)

	case isFirst(msg):
		return m.selectChatAt(m.chatListEdgeIndex(false))

	case isLast(msg):
		return m.selectChatAt(m.chatListEdgeIndex(true))
	}

	return m, nil
}

// moveChatSelection moves the selected chat by delta, clamped to the
// list.
//
// A search narrows what the list is, so the movement is over the results
// and clamped to them: a cursor on a chat the query left out is a cursor
// on nothing the user can see.
//
// On a two-pane screen the conversation follows the selection, because it
// is drawn from it — but not while a search is open, where following it
// would open a chat for every result the user arrows past, and a history
// request is not something a user asks for by looking at a list. Enter is
// the key that opens a chat. On a single-pane screen nothing is opened at
// all, so the selection is all that moves.
func (m Model) moveChatSelection(delta int) (tea.Model, tea.Cmd) {
	if m.chatSearch.open {
		return m.moveChatSearchSelection(delta), nil
	}

	target := m.selectedChat + delta
	if target < 0 {
		target = 0
	}
	if target > len(m.chats)-1 {
		target = len(m.chats) - 1
	}

	return m.selectChatAt(target)
}

// selectChatAt makes chatIndex the selected chat.
//
// An open conversation follows the selection without the focus moving to
// the composer: the user is still walking the list, and Tab or Enter says
// where they want the keys to go. A search is the one case where the
// conversation stays where it is: the list is a filter here, not a
// selection of what to read.
//
// A selection that moved is drawn over the whole screen and not over the
// rows that changed: see repaint.go.
func (m Model) selectChatAt(chatIndex int) (tea.Model, tea.Cmd) {
	if chatIndex < 0 || chatIndex >= len(m.chats) {
		return m, nil
	}

	moved := chatIndex != m.selectedChat
	m.selectedChat = chatIndex

	// The conversation is only drawn from the selection where the list and
	// the conversation share the screen. Everywhere else the selection is
	// only a selection, and nothing is opened behind the user's back.
	if moved &&
		!m.chatSearch.open &&
		m.screen == ScreenConversation &&
		LayoutFor(m.width, m.height).TwoPane() {
		return m.openSelectedChat(false)
	}

	if moved {
		return m, repaintCmd(m)
	}

	return m, nil
}

// openSelectedChat opens the selected chat in the conversation pane.
//
// focusComposer says where the keys go afterwards. The two callers differ
// in nothing else: opening a chat from the list and following the
// selection into another chat are the same operation, and the focus is the
// only difference between them.
func (m Model) openSelectedChat(
	focusComposer bool,
) (tea.Model, tea.Cmd) {
	if len(m.chats) == 0 {
		return m, nil
	}

	m.screen = ScreenConversation
	if focusComposer {
		m.focus = FocusComposer
	}
	m.sendState = sendStateIdle
	m.sendErr = nil
	if m.pausedErr != nil {
		// Sending is still impossible in this chat.
		m.sendState = sendStateError
		m.sendErr = m.pausedErr
	}

	// Opening a conversation starts a new history operation, so a page
	// still in flight for a previous visit is discarded on arrival. The
	// pagination flags belong to the chat being left; the boundary itself
	// is derived from the messages, not stored.
	//
	// historyHasMore starts true rather than false: a chat whose messages
	// are already loaded has not been asked what is above them in this
	// visit, and the only thing that ends a history is a page that comes
	// back empty. Answering "no more" because nothing was asked is the
	// mistake this whole change is about.
	m.historyOperation++
	m.historyExhausted = false
	m.historyHasMore = true
	m.historyMoreLoading = false
	m.historyMoreErr = nil
	m.historyFillRequests = 0
	m.historyFillMessages = 0

	// Where the unread messages of this chat begin, read once, as the chat
	// was when it was opened.
	//
	// It is read here and never again while the chat is open, because this
	// program tells Telegram what is on the screen (message_viewing.go) and
	// Telegram's own pointer would move past the very messages the line
	// stands over. A line that went away as soon as the reader looked at it
	// is a line that says nothing about what they had not read.
	m.unreadBoundary = m.chats[m.selectedChat].LastReadInboxMessageID

	statusCmd := m.setMessageStatusTarget(
		m.accountKey,
		m.chats[m.selectedChat].ID,
	)

	// What this account may write in the chat it is about to look at. The
	// composer of a channel it may not post in is a line, and which of the
	// two this is has to be known before the first frame of the
	// conversation rather than after the next poll.
	m.setChatAccessTarget(m.chats[m.selectedChat].ID)
	if accessCmd := m.loadChatAccess(); accessCmd != nil {
		statusCmd = tea.Batch(statusCmd, accessCmd)
	}

	// TDLib only counts the online members of a chat that has been opened,
	// so the chat the user is looking at is the chat TDLib is told about.
	openCmd := m.switchConversationChat(m.chats[m.selectedChat].ID)

	if openCmd != nil {
		statusCmd = tea.Batch(openCmd, statusCmd)
	}

	if m.source != nil {
		// A conversation asks for its newest page every time it is opened.
		//
		// It used to ask only when the chat held no messages, on the
		// reasoning that the messages it held were therefore the ones it
		// knew about. That reasoning is wrong about a message this program
		// has never learned about and never will: the queue stops listing a
		// record the moment Telegram confirms it, and a record confirmed
		// while this chat was closed — or while another chat was open,
		// which clears the pending list — has no row on the screen and no
		// place in the cache. Its text is here, though. Telegram holds the
		// message, and the newest page is where Telegram keeps what it
		// holds, so this is the request that brings it back.
		//
		// Re-reading replaces the cache, which is the right shape here: a
		// conversation opens at its end, so the older messages are about
		// to be asked for again by the same fill that asks for them the
		// first time.
		m.historyState = loadStateLoading
		m.loadErr = nil
		// A chat that already holds messages is being refreshed, not
		// opened cold, so the page merges with what is there.
		m.historyRefresh = len(m.chats[m.selectedChat].Messages) > 0

		// The cursor waits at the newest message it knows of and the view
		// fills from there (§8.3).
		return m.scrollToNewest(), withRepaint(m, tea.Batch(
			loadHistoryCmd(
				m.source,
				m.chats[m.selectedChat].ID,
				0,
				historyPageSize,
				m.historyOperation,
			),
			statusCmd,
		))
	}

	// A conversation opens at its end however it was loaded: the newest
	// message is the one a user opened the chat to read (§8.3).
	return m.scrollToNewest(), withRepaint(m, statusCmd)
}

// updateConversationKey handles the conversation pane.
//
// Esc follows the hierarchy of §8.5: composer to timeline, timeline to
// the chat list, and then it stops. It never leaves the program, and it
// never discards a draft.
func (m Model) updateConversationKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// §8.5: a popup is the first level of the hierarchy, so Esc closes a
	// question or a menu before it does anything else - and in an
	// uncertain message, closing the menu is how the user keeps the record.
	if m.actionSheet.open || m.modal.open {
		return m.updatePopupKey(msg)
	}

	switch {
	case msg.Type == tea.KeyEsc:
		return m.leaveConversationRegion()

	case isTab(msg):
		return m.cycleFocus(1), nil

	case isShiftTab(msg):
		return m.cycleFocus(-1), nil
	}

	// A two-pane screen shows the chat list beside the conversation, so
	// the list keeps its own keys while the focus is on it.
	if m.focus == FocusChatList {
		return m.updateChatsKey(msg)
	}

	switch m.focus {
	case FocusHistory:
		return m.updateHistoryKey(msg)
	case FocusComposer:
		return m.updateComposerKey(msg)
	case FocusSearch:
		return m.updateChatSearchKey(msg)
	}
	return m, nil
}

// leaveConversationRegion applies one step of the Esc hierarchy and the
// command that came with it.
//
// Leaving the conversation for good is not one of its steps: the
// hierarchy ends at the chat list, and a screen with two panes puts the
// chat list next to the conversation rather than behind it. Only a
// single-pane screen returns to the list screen, and only the chat list
// stops there.
func (m Model) leaveConversationRegion() (Model, tea.Cmd) {
	switch m.focus {
	case FocusComposer:
		// The draft stays. §8.5 says the composer is left without a loss,
		// and losing what someone typed to look at the messages is not
		// what they asked for.
		m.focus = FocusHistory
		return m, nil

	case FocusHistory:
		if LayoutFor(m.width, m.height).TwoPane() {
			m.focus = FocusChatList
			return m, nil
		}

		return m.leaveConversation()

	default:
		// The chat list, with a conversation open beside it: there is
		// nothing above it to go back to.
		return m, nil
	}
}

// leaveConversation returns to the chat list screen, invalidating any
// work in flight for the conversation being left.
//
// The chat is closed on the way out: TDLib counts the members of an open
// chat, and a user who has left it is not one of them.
//
// The screen is drawn over again on the way out as well: a conversation
// that closes takes a whole pane with it, and the rows the pane leaves
// behind are the ones most worth drawing rather than leaving. See
// repaint.go.
func (m Model) leaveConversation() (Model, tea.Cmd) {
	// Invalidate any in-flight send: a late result for the same chat must
	// not be applied to a newer conversation state.
	m.sendOperation++
	m.sendState = sendStateIdle
	m.sendErr = nil
	// Leaving the conversation stops status polling and discards the
	// in-flight response of the chat that is no longer active.
	m.invalidateMessageStatusPolling()

	// The rights of a chat are read for the chat that is open, and nothing
	// on the chat list has an opinion about them.
	m.setChatAccessTarget(0)

	m.screen = ScreenChats
	m.focus = FocusChatList

	return m, withRepaint(m, m.closeConversationChat())
}

// cycleFocus moves the focus by delta positions among the visible
// regions.
//
// The order is the one of §5: chat list, timeline, composer. A region
// that is not on screen is not in the order, which is what makes a narrow
// conversation screen cycle between the timeline and the composer alone.
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

	// A chat this account cannot write in has no composer to visit: the
	// region under the messages is a line that says why, and Tab has no use
	// for stopping on a place with no keys.
	regions := m.conversationFocusRegions()
	if m.canWrite() {
		regions = append(regions, FocusComposer)
	}

	return regions
}

// conversationFocusRegions returns the regions of a conversation before the
// composer: the chat list beside it, and the timeline of the open chat.
func (m Model) conversationFocusRegions() []Focus {
	if !LayoutFor(m.width, m.height).TwoPane() {
		return []Focus{FocusHistory}
	}

	return append(m.chatListFocusRegions(), FocusHistory)
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

// updateComposerKey handles the keys of the composer (§8.4, §7.4).
//
// While a send is in flight the composer is frozen: no rune, paste or
// deletion is applied. Text typed after Enter must not be thrown away when
// the queue accepts the message.
//
// Alt+Enter is the newline. Shift+Enter is not offered as one because
// Bubble Tea v1 cannot tell it from Enter in most terminals — divergence 3
// of the specification — so the key that starts a line is the one that
// works everywhere, and the hint bar names it.
//
// A chat this account cannot write in has no keys here at all. The region
// under the messages is a line that says why, and every key of this function
// would be editing a draft that goes nowhere: a send refused by Telegram is
// a message that stays in the feed with `! failed` under it, and the field
// that invited it is the defect. The composer of such a chat is inert, which
// is also what makes the hint bar honest — it names the keys of the timeline
// because the timeline is where the keys are.
func (m Model) updateComposerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.sendState == sendStateSending {
		return m, nil
	}
	if !m.canWrite() {
		return m, nil
	}

	// A paste is text. It goes in whole, newlines and all, and it is never
	// a send: a message copied from a file must not leave the composer
	// because it had a line break in it.
	if msg.Paste {
		return m.edit(func(m *Model) {
			m.composer, m.composerCursor = insertText(
				m.composer,
				m.composerCursor,
				msg.Runes,
			)
		}), nil
	}

	// The readline keys of §8.4: clear to the cursor, kill the line, delete
	// the word before the cursor.
	switch {
	case isClearComposer(msg):
		return m.edit(func(m *Model) {
			m.composer, m.composerCursor = clearToCursor(
				m.composer,
				m.composerCursor,
			)
		}), nil

	case isKillLine(msg):
		return m.edit(func(m *Model) {
			m.composer, m.composerCursor = clearLine(
				m.composer,
				m.composerCursor,
			)
		}), nil

	case isDeleteWordBefore(msg):
		return m.edit(func(m *Model) {
			m.composer, m.composerCursor = deleteWordBefore(
				m.composer,
				m.composerCursor,
			)
		}), nil
	}

	switch msg.Type {
	case tea.KeyRunes:
		return m.edit(func(m *Model) {
			m.composer, m.composerCursor = insertText(
				m.composer,
				m.composerCursor,
				msg.Runes,
			)
		}), nil

	case tea.KeySpace, tea.KeyTab:
		// Tab moves the focus (§8.4), so a space is the only key that adds
		// a gap here.
		return m.edit(func(m *Model) {
			m.composer, m.composerCursor = insertText(
				m.composer,
				m.composerCursor,
				[]rune{' '},
			)
		}), nil

	case tea.KeyEnter:
		if msg.Alt {
			return m.edit(func(m *Model) {
				m.composer, m.composerCursor = insertText(
					m.composer,
					m.composerCursor,
					[]rune{'\n'},
				)
			}), nil
		}

		return m.handleComposerEnter()

	case tea.KeyBackspace:
		return m.edit(func(m *Model) {
			m.composer, m.composerCursor = deleteBefore(
				m.composer,
				m.composerCursor,
			)
		}), nil

	case tea.KeyDelete:
		return m.edit(func(m *Model) {
			m.composer, m.composerCursor = deleteAfter(
				m.composer,
				m.composerCursor,
			)
		}), nil

	case tea.KeyLeft:
		return m.edit(func(m *Model) {
			m.composerCursor = moveLeft(m.composer, m.composerCursor)
		}), nil

	case tea.KeyRight:
		return m.edit(func(m *Model) {
			m.composerCursor = moveRight(m.composer, m.composerCursor)
		}), nil

	case tea.KeyUp:
		return m.edit(func(m *Model) { m.moveComposerRow(-1) }), nil

	case tea.KeyDown:
		return m.edit(func(m *Model) { m.moveComposerRow(1) }), nil

	case tea.KeyHome, tea.KeyCtrlA:
		return m.edit(func(m *Model) {
			m.composerCursor = lineStart(m.composer, m.composerCursor)
		}), nil

	case tea.KeyEnd, tea.KeyCtrlE:
		return m.edit(func(m *Model) {
			m.composerCursor = lineEnd(m.composer, m.composerCursor)
		}), nil

	}

	return m, nil
}

// edit applies a change to the draft and puts the screen state right
// afterwards.
//
// Every key that touches the text goes through here, so two things cannot
// be forgotten by one key and remembered by another: a draft that is no
// longer blank puts the lit placeholder out, and a send that failed
// because of the text is forgotten.
func (m Model) edit(change func(m *Model)) Model {
	// A draft that grows takes rows from the feed, so the rows the window
	// was placed against are not the rows it has afterwards. The question
	// of where the user is has to be asked before the change, the same way
	// updateWindowSize asks it before a resize.
	following := m.timelineFollowsNewest()
	rowsBefore := m.feedRows()

	change(&m)

	m.composerPlaceholderLit = false
	m.forgetSendError()

	m.composerCursor = clampIndex(m.composerCursor, len(m.composer))

	// A window that is following is filled from the bottom again, because
	// the rows of the feed are not the rows it was placed against: a draft
	// that grew leaves the newest message above the last row of the feed,
	// and the view answers that by drawing the message under the cursor
	// alone — a feed with one message in it and blank rows above it (the
	// owner, 01.10). A window a reader has scrolled away from keeps its
	// message at the top, and the cut that belonged to the rows it was
	// placed against is nothing to do with the new ones.
	if following && m.feedRows() != rowsBefore {
		m = m.anchorAtNewest()
	}

	return m
}

// forgetSendError clears the state of a send that failed, so that editing
// the draft is a fresh start.
//
// It does nothing while sending is paused. Nothing was ever attempted, the
// reason still holds, and a user who cannot send is told so on every
// keystroke rather than once: a reason that disappears when a key is
// pressed is a reason the user has to remember.
func (m *Model) forgetSendError() {
	if m.pausedErr != nil {
		return
	}
	if m.sendState == sendStateError {
		m.sendState = sendStateIdle
		m.sendErr = nil
	}
}

// moveComposerRow moves the cursor one row up or down, keeping its column
// where the row it lands on is long enough and putting it at the end of it
// where it is not.
func (m *Model) moveComposerRow(delta int) {
	layout := m.layoutComposer(m.composer, m.composerCursor, m.composerWidth())
	row, column := layout.cursorRow, layout.cursorColumn

	row += delta
	if row < 0 {
		row = 0
	}
	if row > layout.lastRow() {
		row = layout.lastRow()
	}

	m.composerCursor = layout.indexAt(row, column)
}

// composerWidth returns the width a row of the draft is laid out in.
func (m Model) composerWidth() int {
	layout := LayoutFor(m.width, m.height)

	return maxInt(layout.ChatContentWidth()-composerPromptWidth(), 1)
}

// handleComposerEnter sends the draft.
//
// The guards before the text are the whole of what stands between a person and
// a message they cannot send: a chat with no source, a send already in flight,
// and a chat this account has no right to write in. The last one is here and
// not only in the keys above it because this is the function that owns the
// send, and a guard that exists somewhere else is a guard that a later key
// can walk around.
func (m Model) handleComposerEnter() (tea.Model, tea.Cmd) {
	if m.source == nil && m.submitter == nil {
		return m, nil
	}
	if m.sendState == sendStateSending {
		return m, nil
	}
	if !m.canWrite() {
		return m, nil
	}

	text := string(m.composer)
	if blankDraft(m.composer) {
		// §7.3: nothing is sent and no error is made, and the placeholder
		// is lit for a moment so that a user who pressed Enter knows the
		// composer was there.
		m.composerPlaceholderLit = true

		return m, composerPlaceholderCmd()
	}

	chat := m.selected()
	if chat.ID == 0 {
		return m, nil
	}

	m.sendOperation++
	operation := m.sendOperation
	m.lastSubmission = nil
	m.lastSubmittedText = text
	m.sendState = sendStateSending
	m.sendErr = nil

	if m.submitter != nil {
		return m, submitComposerCmd(
			m.ctx,
			m.submitter,
			chat.ID,
			text,
			operation,
		)
	}

	return m, sendMessageCmd(m.source, chat.ID, text, operation)
}

func (m Model) updateAuthKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.composer = nil
		m.authCanceled = true
		m.quitting = true
		m.invalidateMessageStatusPolling()
		return m, tea.Quit

	case tea.KeyEnter:
		m.quitting = true
		m.invalidateMessageStatusPolling()
		return m, tea.Quit

	case tea.KeyRunes:
		m.composer = append(m.composer, msg.Runes...)
	case tea.KeySpace:
		m.composer = append(m.composer, ' ')
	case tea.KeyBackspace:
		if len(m.composer) > 0 {
			m.composer = m.composer[:len(m.composer)-1]
		}
	case tea.KeyCtrlU:
		m.composer = nil
	}
	return m, nil
}

// The view itself lives in view.go: View has to answer for the whole
// screen, and splitting it between the model state and a pane renderer
// would put half of every layout decision in two files.

func (m Model) selected() Chat {
	if len(m.chats) == 0 || m.selectedChat < 0 || m.selectedChat >= len(m.chats) {
		return Chat{}
	}
	return m.chats[m.selectedChat]
}

// Composer returns the current composer text.
func (m Model) Composer() string { return string(m.composer) }

func (m Model) LastSubmission() (Submission, bool) {
	if m.lastSubmission == nil {
		return Submission{}, false
	}
	return *m.lastSubmission, true
}

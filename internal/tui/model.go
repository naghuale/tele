package tui

import (
	"context"
	"io"
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

	// presenceOpener is told which chat the user is looking at, and
	// openedChat is the one it was last told about. It is nil in a program
	// built without Telegram.
	presenceOpener ChatPresenceOpener
	openedChat     int64

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
	// the only reliable end-of-history signal. HistoryPage.HasMore cannot
	// be used: it is a len(messages) == limit heuristic that reports
	// false on a short first page while older messages still exist.
	historyExhausted bool

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
		widths:   termwidth.Unmeasured(termwidth.ModeAuto),
		now:      time.Now,
		location: time.Local,
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
	if cmd := m.pollDeliverySources(m.messageStatusGeneration); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if len(cmds) == 0 {
		return nil
	}

	return tea.Batch(cmds...)
}

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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

	case messageStatusesLoadedMsg:
		return m.handleMessageStatusesLoaded(msg)

	case messageStatusesFailedMsg:
		return m.handleMessageStatusesFailed(msg)

	case statusSummaryLoadedMsg:
		return m.handleStatusSummaryLoaded(msg)

	case statusSummaryFailedMsg:
		return m.handleStatusSummaryFailed(msg)

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
	m.width = msg.Width
	m.height = msg.Height

	// The focus follows the screen, and so does the scroll: a size that
	// took a region away must not leave the keys on a region that is no
	// longer drawn, and a narrower screen shows the same message at the
	// top of the window (§10.5).
	m = m.normalizeTimeline().normalizeFocus()

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
	m.chats = msg.chats

	if len(msg.chats) == 0 {
		m.chatsState = loadStateEmpty
		m.selectedChat = 0
		return m, nil
	}

	m.chatsState = loadStateLoaded

	if m.selectedChat >= len(m.chats) {
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
	m.chats[m.selectedChat].Messages = chronological(msg.page.Messages)

	// A new first page re-opens the history: nothing is known to be
	// missing from it yet.
	m.historyExhausted = false
	m.historyMoreErr = nil

	if len(msg.page.Messages) == 0 {
		m.historyState = loadStateEmpty
	} else {
		m.historyState = loadStateLoaded
	}

	// A first page opens the conversation at its end: the newest message is
	// the one a user came to read (§8.3).
	return m.scrollToNewest(), nil
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

	existing := m.chats[m.selectedChat].Messages
	merged := prependOlderMessages(existing, chronological(msg.page.Messages))
	m.chats[m.selectedChat].Messages = merged

	// The page went on top, so the message that was on screen is exactly
	// what was added lower down. Moving the cursor and the scroll anchor by
	// that much is what keeps a reader in place: this is the one place
	// where reading upwards could punish a user for asking for more.
	added := len(merged) - len(existing)
	m.selectedMsg += added
	m.timelineTop += added

	if len(merged) == len(existing) {
		m.historyExhausted = true
	}

	return m.normalizeTimeline(), nil
}

// chronological returns the messages of a page oldest first.
//
// TDLib answers a history request newest first: the first message of the
// page is the newest one it has. A conversation is read the other way
// round (§8.3, and divergence 1 of the specification), so the order is
// reversed here rather than in the source. The source is what it is, and
// the interface is what the specification describes.
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
// It returns no command when the request is not applicable: mock mode,
// an exhausted history, a request already in flight, a history that has
// not loaded yet, or no boundary to continue from.
func (m Model) loadOlderMessages() (tea.Model, tea.Cmd) {
	if m.source == nil {
		return m, nil
	}
	if m.historyExhausted || m.historyMoreLoading {
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
	atNewest := m.selectedMsg >= len(m.chats[m.selectedChat].Messages)-1

	m.chats[m.selectedChat].Messages = appendMessageByID(
		m.chats[m.selectedChat].Messages,
		msg.message,
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

	// The draft is cleared only now, after the queue has taken it (§7.1),
	// and the cursor goes with it: the next draft starts at its beginning.
	m.composer = nil
	m.composerCursor = 0
	submission := msg.submission
	m.lastSubmission = &submission
	return m, nil
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
	m.historyOperation++
	m.historyExhausted = false
	m.historyMoreLoading = false
	m.historyMoreErr = nil

	statusCmd := m.setMessageStatusTarget(
		m.accountKey,
		m.chats[m.selectedChat].ID,
	)

	// TDLib only counts the online members of a chat that has been opened,
	// so the chat the user is looking at is the chat TDLib is told about.
	openCmd := m.switchConversationChat(m.chats[m.selectedChat].ID)

	if openCmd != nil {
		statusCmd = tea.Batch(openCmd, statusCmd)
	}

	if m.source != nil {
		if len(m.chats[m.selectedChat].Messages) == 0 {
			m.historyState = loadStateLoading
			m.loadErr = nil

			// A conversation opens at its end, and the page is on its way:
			// the cursor waits at the newest message it knows of and the
			// view fills from there (§8.3).
			return m.scrollToNewest(), tea.Batch(
				loadHistoryCmd(
					m.source,
					m.chats[m.selectedChat].ID,
					0,
					historyPageSize,
					m.historyOperation,
				),
				statusCmd,
			)
		}
		// The messages are already cached, so this chat is loaded even
		// though nothing was requested. Without this the previous
		// chat's loadStateError would block pagination here.
		m.historyState = loadStateLoaded
		m.loadErr = nil
	}

	// A conversation opens at its end however it was loaded: the newest
	// message is the one a user opened the chat to read (§8.3).
	return m.scrollToNewest(), statusCmd
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
func (m Model) leaveConversation() (Model, tea.Cmd) {
	// Invalidate any in-flight send: a late result for the same chat must
	// not be applied to a newer conversation state.
	m.sendOperation++
	m.sendState = sendStateIdle
	m.sendErr = nil
	// Leaving the conversation stops status polling and discards the
	// in-flight response of the chat that is no longer active.
	m.invalidateMessageStatusPolling()

	m.screen = ScreenChats
	m.focus = FocusChatList

	return m, m.closeConversationChat()
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
	// every other screen on the only region it has.
	if m.screen == ScreenConversation {
		m.focus = FocusComposer
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

	if !LayoutFor(m.width, m.height).TwoPane() {
		return []Focus{FocusHistory, FocusComposer}
	}

	return append(m.chatListFocusRegions(), FocusHistory, FocusComposer)
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
func (m Model) updateComposerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.sendState == sendStateSending {
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
	change(&m)

	m.composerPlaceholderLit = false
	m.forgetSendError()

	m.composerCursor = clampIndex(m.composerCursor, len(m.composer))

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
	width := layout.ChatContentWidth() -
		selectionMarkerWidth -
		contentInsetWidth

	return maxInt(width, 1)
}

func (m Model) handleComposerEnter() (tea.Model, tea.Cmd) {
	if m.source == nil && m.submitter == nil {
		return m, nil
	}
	if m.sendState == sendStateSending {
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

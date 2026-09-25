package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
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

	chatsState   loadState
	historyState loadState
	loadErr      error

	sendState      sendState
	sendErr        error
	sendOperation  uint64
	lastSubmission *Submission

	composer []rune

	authPrompt   AuthPromptKind
	authCanceled bool

	width  int
	height int

	quitting bool
}

// NewModel returns a model in the Chats screen with deterministic mock
// data and focus on the chat list.
//
// It is the mock-only constructor: Init returns no command, and View
// renders mockChats immediately.
func NewModel() Model {
	return Model{
		screen:       ScreenChats,
		focus:        FocusChatList,
		ctx:          context.Background(),
		chats:        mockChats(),
		chatsState:   loadStateLoaded,
		historyState: loadStateIdle,
		sendState:    sendStateIdle,
	}
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
	}
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd {
	if m.source == nil {
		return nil
	}
	if m.chatsState == loadStateLoading {
		return listChatsCmd(m.source)
	}
	return nil
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
	return m, nil
}

func (m Model) updateChatsLoaded(msg chatsLoadedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.chatsState = loadStateError
		m.loadErr = msg.err
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
	return m, nil
}

// updateHistoryLoaded applies a history response.
//
// A response whose chatID does not match the currently selected chat is
// treated as stale and ignored.
func (m Model) updateHistoryLoaded(msg historyLoadedMsg) (tea.Model, tea.Cmd) {
	if m.selectedChat < 0 ||
		m.selectedChat >= len(m.chats) ||
		m.chats[m.selectedChat].ID != msg.chatID {
		return m, nil
	}

	if msg.err != nil {
		m.historyState = loadStateError
		m.loadErr = msg.err
		return m, nil
	}

	m.loadErr = nil
	m.chats[m.selectedChat].Messages = msg.page.Messages
	m.selectedMsg = 0

	if len(msg.page.Messages) == 0 {
		m.historyState = loadStateEmpty
	} else {
		m.historyState = loadStateLoaded
	}
	return m, nil
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
		return m, nil
	}

	m.sendState = sendStateIdle
	m.sendErr = nil
	m.composer = nil

	m.chats[m.selectedChat].Messages = prependMessageByID(
		m.chats[m.selectedChat].Messages,
		msg.message,
	)
	m.selectedMsg = 0

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
		return m, nil
	}

	m.sendState = sendStateIdle
	m.sendErr = nil
	m.composer = nil
	submission := msg.submission
	m.lastSubmission = &submission
	return m, nil
}

// prependMessageByID inserts message at the head of messages and drops
// any existing entry with the same ID.
func prependMessageByID(messages []Message, message Message) []Message {
	result := make([]Message, 0, len(messages)+1)
	result = append(result, message)
	for _, current := range messages {
		if current.ID == message.ID {
			continue
		}
		result = append(result, current)
	}
	return result
}

func (m Model) updateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyCtrlC {
		if m.screen == ScreenAuth {
			m.composer = nil
			m.authCanceled = true
		}
		m.quitting = true
		m.invalidateMessageStatusPolling()
		return m, tea.Quit
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

func (m Model) updateChatsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case msg.Type == tea.KeyEsc:
		m.quitting = true
		m.invalidateMessageStatusPolling()
		return m, tea.Quit

	case msg.Type == tea.KeyEnter:
		if len(m.chats) == 0 {
			return m, nil
		}
		m.screen = ScreenConversation
		m.focus = FocusHistory
		m.selectedMsg = 0
		m.sendState = sendStateIdle
		m.sendErr = nil

		statusCmd := m.setMessageStatusTarget(
			m.accountKey,
			m.chats[m.selectedChat].ID,
		)

		if m.source != nil && len(m.chats[m.selectedChat].Messages) == 0 {
			m.historyState = loadStateLoading
			m.loadErr = nil
			return m, tea.Batch(
				loadHistoryCmd(
					m.source,
					m.chats[m.selectedChat].ID,
					historyPageSize,
				),
				statusCmd,
			)
		}
		return m, statusCmd

	case isUp(msg):
		if m.selectedChat > 0 {
			m.selectedChat--
		}
		return m, nil

	case isDown(msg):
		if m.selectedChat < len(m.chats)-1 {
			m.selectedChat++
		}
		return m, nil

	case isFirst(msg):
		m.selectedChat = 0
		return m, nil

	case isLast(msg):
		if len(m.chats) > 0 {
			m.selectedChat = len(m.chats) - 1
		}
		return m, nil
	}
	return m, nil
}

func (m Model) updateConversationKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case msg.Type == tea.KeyEsc:
		// Invalidate any in-flight send: a late result for the same
		// chat must not be applied to a newer conversation state.
		m.sendOperation++
		m.sendState = sendStateIdle
		m.sendErr = nil
		// Leaving the conversation stops status polling and discards the
		// in-flight response of the chat that is no longer active.
		m.invalidateMessageStatusPolling()

		m.screen = ScreenChats
		m.focus = FocusChatList
		return m, nil

	case isTab(msg):
		switch m.focus {
		case FocusHistory:
			m.focus = FocusComposer
		case FocusComposer:
			m.focus = FocusHistory
		}
		return m, nil

	case isShiftTab(msg):
		switch m.focus {
		case FocusHistory:
			m.focus = FocusComposer
		case FocusComposer:
			m.focus = FocusHistory
		}
		return m, nil

	case isClearComposer(msg):
		if m.focus == FocusComposer && m.sendState != sendStateSending {
			m.composer = nil
			m.sendState = sendStateIdle
			m.sendErr = nil
		}
		return m, nil
	}

	switch m.focus {
	case FocusHistory:
		return m.updateHistoryKey(msg)
	case FocusComposer:
		return m.updateComposerKey(msg)
	}
	return m, nil
}

func (m Model) updateHistoryKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	msgs := len(m.selected().Messages)
	switch {
	case isUp(msg):
		if m.selectedMsg > 0 {
			m.selectedMsg--
		}
	case isDown(msg):
		if m.selectedMsg < msgs-1 {
			m.selectedMsg++
		}
	}
	return m, nil
}

// updateComposerKey handles composer editing.
//
// While a send is in flight the composer is frozen: no rune, space,
// backspace, or clear action is applied. This prevents text typed after
// Enter from being discarded when the successful send clears the
// composer.
func (m Model) updateComposerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.sendState == sendStateSending {
		return m, nil
	}

	switch msg.Type {
	case tea.KeyRunes:
		m.composer = append(m.composer, msg.Runes...)
		if m.sendState == sendStateError {
			m.sendState = sendStateIdle
			m.sendErr = nil
		}

	case tea.KeySpace:
		m.composer = append(m.composer, ' ')
		if m.sendState == sendStateError {
			m.sendState = sendStateIdle
			m.sendErr = nil
		}

	case tea.KeyBackspace:
		if len(m.composer) > 0 {
			m.composer = m.composer[:len(m.composer)-1]
		}
		if m.sendState == sendStateError {
			m.sendState = sendStateIdle
			m.sendErr = nil
		}

	case tea.KeyEnter:
		return m.handleComposerEnter()
	}

	return m, nil
}

// handleComposerEnter implements the composer Enter action.
func (m Model) handleComposerEnter() (tea.Model, tea.Cmd) {
	if m.source == nil && m.submitter == nil {
		return m, nil
	}
	if m.sendState == sendStateSending {
		return m, nil
	}

	text := string(m.composer)
	if strings.TrimSpace(text) == "" {
		return m, nil
	}

	chat := m.selected()
	if chat.ID == 0 {
		return m, nil
	}

	m.sendOperation++
	operation := m.sendOperation
	m.lastSubmission = nil
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

// View implements tea.Model.
func (m Model) View() string {
	if m.quitting {
		return ""
	}

	if m.width < minWidth || m.height < minHeight {
		return m.viewTooSmall()
	}

	switch m.screen {
	case ScreenChats:
		switch m.chatsState {
		case loadStateLoading:
			return "Loading chats...\n"
		case loadStateError:
			return fmt.Sprintf("Failed to load chats: %v\n", m.loadErr)
		case loadStateEmpty:
			return "No chats\n"
		}
		return m.viewChats()

	case ScreenConversation:
		switch m.historyState {
		case loadStateLoading:
			return "Loading history...\n"
		case loadStateError:
			return fmt.Sprintf("Failed to load history: %v\n", m.loadErr)
		}
		return m.viewConversation()

	case ScreenAuth:
		return m.viewAuth()

	default:
		return "telecli: invalid screen\n"
	}
}

func (m Model) viewTooSmall() string {
	return fmt.Sprintf(
		"Terminal is too small\nMinimum: %dx%d\nCurrent: %dx%d\n",
		minWidth, minHeight, m.width, m.height,
	)
}

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

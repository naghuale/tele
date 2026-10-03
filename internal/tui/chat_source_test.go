package tui

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// fakeChatSource is a scripted ChatSource.
//
// The lock is here because Bubble Tea calls a source from several goroutines
// at once: the members of a batch are commands of their own, and two commands
// of two updates are in flight together. A fake that cannot take that is not
// a stand-in for the source, it is a race detector with a grudge (#58).
type fakeChatSource struct {
	mu sync.Mutex

	chats       []Chat
	chatsErr    error
	history     HistoryPage
	historyErr  error
	historyCall struct {
		chatID        int64
		fromMessageID int64
		limit         int
	}

	// pages answers a history load by the boundary it was asked with, so a
	// test of the fill can hand out a newest page and an older one. A
	// boundary that is not staged is answered with history, which is what a
	// source with nothing staged in it does.
	pages map[int64]HistoryPage

	// pagesByChat answers a history load by the chat and the boundary it was
	// asked with, for a test in which two chats each have a newest page of
	// their own: the newest page of every chat is asked for with the
	// boundary zero, so one table cannot hold the newest pages of two.
	pagesByChat map[int64]map[int64]HistoryPage

	// historyChats is every chat a history load was asked for, in order.
	// The last call is the one historyCall holds, and this is how many there
	// were: a screen that asks for a page per key press is the question the
	// pause of the preview exists to answer, and it can only be asked of the
	// whole list.
	historyChats []int64
	historyCalls []int64

	sentMessage Message
	sendErr     error
	sendCall    struct {
		chatID int64
		text   string
		called bool
	}
}

func (f *fakeChatSource) ListChats(ctx context.Context) ([]Chat, error) {
	if f.chatsErr != nil {
		return nil, f.chatsErr
	}
	return f.chats, nil
}

func (f *fakeChatSource) LoadHistory(
	ctx context.Context,
	chatID int64,
	fromMessageID int64,
	limit int,
) (HistoryPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.historyCall.chatID = chatID
	f.historyCall.fromMessageID = fromMessageID
	f.historyCall.limit = limit
	f.historyChats = append(f.historyChats, chatID)
	f.historyCalls = append(f.historyCalls, fromMessageID)
	if f.historyErr != nil {
		return HistoryPage{}, f.historyErr
	}
	if page, staged := f.pages[fromMessageID]; staged {
		return page, nil
	}
	if byBoundary, known := f.pagesByChat[chatID]; known {
		if page, staged := byBoundary[fromMessageID]; staged {
			return page, nil
		}
	}

	return f.history, nil
}

// historyBoundaries returns the boundaries every history load was asked
// with, in order, which is what tells a fill from one page: a fill asks for
// one boundary after another, going up the chat.
func (f *fakeChatSource) historyBoundaries() []int64 {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.historyCalls
}

func (f *fakeChatSource) SendMessage(
	ctx context.Context,
	chatID int64,
	text string,
) (Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.sendCall.called = true
	f.sendCall.chatID = chatID
	f.sendCall.text = text
	if f.sendErr != nil {
		return Message{}, f.sendErr
	}
	return f.sentMessage, nil
}

// runCmd runs cmd() synchronously and returns the tea.Msg.
func runCmd(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	if cmd == nil {
		t.Fatal("cmd is nil")
	}
	return cmd()
}

// ---- Model state ----

func TestNewModelWithSourceStartsLoading(t *testing.T) {
	m := NewModelWithSource(&fakeChatSource{})

	if m.chatsState != loadStateLoading {
		t.Fatalf("chatsState = %s, want loading", m.chatsState)
	}
	if m.source == nil {
		t.Fatal("source is nil")
	}
	if len(m.chats) != 0 {
		t.Fatalf("chats = %d, want 0", len(m.chats))
	}
}

func TestNewModelMockDoesNotStartLoading(t *testing.T) {
	m := NewModel()

	if m.chatsState != loadStateLoaded {
		t.Fatalf("chatsState = %s, want loaded", m.chatsState)
	}
	if m.source != nil {
		t.Fatal("source is not nil in mock-only mode")
	}
	if len(m.chats) == 0 {
		t.Fatal("mock chats must be preloaded")
	}
}

func TestInitWithoutSourceReturnsNil(t *testing.T) {
	if cmd := NewModel().Init(); cmd != nil {
		t.Fatal("Init with mock model must return nil")
	}
}

func TestInitWithSourceReturnsListChatsCmd(t *testing.T) {
	m := NewModelWithSource(&fakeChatSource{})
	if cmd := m.Init(); cmd == nil {
		t.Fatal("Init with source must return a command")
	}
}

// ---- chatsLoadedMsg ----

func TestChatsLoadedMsgPopulatesChats(t *testing.T) {
	m := NewModelWithSource(&fakeChatSource{})
	m, _ = updateModel(t, m, chatsLoadedMsg{
		chats: []Chat{{ID: 1, Title: "A"}, {ID: 2, Title: "B"}},
	})

	if m.chatsState != loadStateLoaded {
		t.Fatalf("chatsState = %s, want loaded", m.chatsState)
	}
	if len(m.chats) != 2 {
		t.Fatalf("len(chats) = %d, want 2", len(m.chats))
	}
}

func TestChatsLoadedMsgEmptyState(t *testing.T) {
	m := NewModelWithSource(&fakeChatSource{})
	m, _ = updateModel(t, m, chatsLoadedMsg{chats: []Chat{}})

	if m.chatsState != loadStateEmpty {
		t.Fatalf("chatsState = %s, want empty", m.chatsState)
	}
}

func TestChatsLoadedMsgErrorState(t *testing.T) {
	m := NewModelWithSource(&fakeChatSource{})
	err := errors.New("boom")
	m, _ = updateModel(t, m, chatsLoadedMsg{err: err})

	if m.chatsState != loadStateError {
		t.Fatalf("chatsState = %s, want error", m.chatsState)
	}
	if !errors.Is(m.loadErr, err) {
		t.Fatalf("loadErr = %v, want %v", m.loadErr, err)
	}
}

func TestChatsLoadedMsgClearsPreviousError(t *testing.T) {
	m := NewModelWithSource(&fakeChatSource{})
	m, _ = updateModel(t, m, chatsLoadedMsg{err: errors.New("boom")})
	m, _ = updateModel(t, m, chatsLoadedMsg{chats: []Chat{{ID: 1}}})
	if m.loadErr != nil {
		t.Fatalf("loadErr = %v, want nil after success", m.loadErr)
	}
}

func TestChatsLoadedMsgClampsSelection(t *testing.T) {
	m := NewModelWithSource(&fakeChatSource{})
	m.selectedChat = 5
	m, _ = updateModel(t, m, chatsLoadedMsg{
		chats: []Chat{{ID: 1}, {ID: 2}},
	})
	if m.selectedChat != 1 {
		t.Fatalf("selectedChat = %d, want 1", m.selectedChat)
	}
}

// ---- historyLoadedMsg ----

func TestHistoryLoadedMsgPopulatesSelectedChat(t *testing.T) {
	source := &fakeChatSource{}
	m := NewModelWithSource(source)
	m, _ = updateModel(t, m, chatsLoadedMsg{
		chats: []Chat{{ID: 10, Title: "A"}, {ID: 20, Title: "B"}},
	})
	m.selectedChat = 0

	m, _ = updateModel(t, m, historyLoadedMsg{
		chatID: 10,
		page: HistoryPage{
			Messages: []Message{
				{ID: 100, Outgoing: true, Text: "hi", At: mockMoment("10:00")},
				{ID: 99, Text: "hello", At: mockMoment("09:59")},
			},
		},
	})

	if m.historyState != loadStateLoaded {
		t.Fatalf("historyState = %s, want loaded", m.historyState)
	}
	if len(m.chats[0].Messages) != 2 {
		t.Fatalf("chats[0].Messages = %d, want 2", len(m.chats[0].Messages))
	}
	if m.chats[1].Messages != nil {
		t.Fatal("chats[1] must not be modified")
	}
}

func TestHistoryLoadedMsgEmptyState(t *testing.T) {
	m := NewModelWithSource(&fakeChatSource{})
	m.chats = []Chat{{ID: 1}}
	m.selectedChat = 0
	m, _ = updateModel(t, m, historyLoadedMsg{chatID: 1, page: HistoryPage{}})

	if m.historyState != loadStateEmpty {
		t.Fatalf("historyState = %s, want empty", m.historyState)
	}
}

func TestHistoryLoadedMsgErrorState(t *testing.T) {
	m := NewModelWithSource(&fakeChatSource{})
	m.chats = []Chat{{ID: 1}}
	m.selectedChat = 0
	err := errors.New("history fail")
	m, _ = updateModel(t, m, historyLoadedMsg{chatID: 1, err: err})

	if m.historyState != loadStateError {
		t.Fatalf("historyState = %s, want error", m.historyState)
	}
	if !errors.Is(m.loadErr, err) {
		t.Fatalf("loadErr = %v, want %v", m.loadErr, err)
	}
}

func TestHistoryLoadedMsgIgnoresStaleChat(t *testing.T) {
	m := NewModelWithSource(&fakeChatSource{})

	m, _ = updateModel(t, m, chatsLoadedMsg{
		chats: []Chat{
			{ID: 10, Title: "A"},
			{ID: 20, Title: "B"},
		},
	})

	m.selectedChat = 1
	m.historyState = loadStateLoading

	m, _ = updateModel(t, m, historyLoadedMsg{
		chatID: 10,
		page: HistoryPage{
			Messages: []Message{{ID: 100, Text: "stale"}},
		},
	})

	if m.historyState != loadStateLoading {
		t.Fatalf("historyState = %s, want loading", m.historyState)
	}
	if len(m.chats[0].Messages) != 0 {
		t.Fatal("stale response must not modify chat A")
	}
	if len(m.chats[1].Messages) != 0 {
		t.Fatal("stale response must not modify chat B")
	}
	if m.selectedMsg != 0 {
		t.Fatalf("selectedMsg = %d, want 0", m.selectedMsg)
	}
	if m.loadErr != nil {
		t.Fatalf("loadErr = %v, want nil", m.loadErr)
	}
}

// ---- Enter triggers history load ----

func TestEnterWithSourceTriggersHistoryCmd(t *testing.T) {
	source := &fakeChatSource{}
	m := NewModelWithSource(source)
	m, _ = updateModel(t, m, chatsLoadedMsg{
		chats: []Chat{{ID: 7, Title: "A"}},
	})

	updated, cmd := m.Update(press(tea.KeyEnter))
	mm := updated.(Model)

	if mm.screen != ScreenConversation {
		t.Fatalf("screen = %v, want conversation", mm.screen)
	}
	if mm.historyState != loadStateLoading {
		t.Fatalf("historyState = %s, want loading", mm.historyState)
	}
	if cmd == nil {
		t.Fatal("Enter must return a load-history command")
	}

	msg := runCmd(t, cmd)
	hist, ok := msg.(historyLoadedMsg)
	if !ok {
		t.Fatalf("cmd returned %T", msg)
	}
	if hist.chatID != 7 {
		t.Fatalf("history chatID = %d, want 7", hist.chatID)
	}
	if source.historyCall.limit != historyPageSize {
		t.Fatalf("limit = %d, want %d", source.historyCall.limit, historyPageSize)
	}
}

func TestEnterWithMockDoesNotTriggerHistoryCmd(t *testing.T) {
	m := NewModel()
	updated, cmd := m.Update(press(tea.KeyEnter))
	mm := updated.(Model)

	if mm.screen != ScreenConversation {
		t.Fatalf("screen = %v, want conversation", mm.screen)
	}
	if cmd != nil {
		t.Fatal("mock path must not return a command on Enter")
	}
}

// ---- Commands ----

func TestListChatsCmdReturnsChats(t *testing.T) {
	source := &fakeChatSource{chats: []Chat{{ID: 1}, {ID: 2}}}
	msg := runCmd(t, listChatsCmd(source))
	loaded, ok := msg.(chatsLoadedMsg)
	if !ok {
		t.Fatalf("cmd returned %T", msg)
	}
	if loaded.err != nil {
		t.Fatalf("err = %v", loaded.err)
	}
	if len(loaded.chats) != 2 {
		t.Fatalf("chats = %d, want 2", len(loaded.chats))
	}
}

func TestListChatsCmdReturnsError(t *testing.T) {
	err := errors.New("list fail")
	source := &fakeChatSource{chatsErr: err}
	msg := runCmd(t, listChatsCmd(source))
	loaded, ok := msg.(chatsLoadedMsg)
	if !ok {
		t.Fatalf("cmd returned %T", msg)
	}
	if !errors.Is(loaded.err, err) {
		t.Fatalf("err = %v, want %v", loaded.err, err)
	}
}

func TestLoadHistoryCmdReturnsPage(t *testing.T) {
	source := &fakeChatSource{history: HistoryPage{
		Messages: []Message{{ID: 1, Text: "hi"}},
		NextFrom: 1,
		HasMore:  true,
	}}
	msg := runCmd(t, loadHistoryCmd(source, 42, 0, 25, 3))
	loaded, ok := msg.(historyLoadedMsg)
	if !ok {
		t.Fatalf("cmd returned %T", msg)
	}
	if loaded.err != nil {
		t.Fatalf("err = %v", loaded.err)
	}
	if loaded.chatID != 42 {
		t.Fatalf("chatID = %d, want 42", loaded.chatID)
	}
	if loaded.operation != 3 {
		t.Fatalf("operation = %d, want 3", loaded.operation)
	}
	if source.historyCall.fromMessageID != 0 {
		t.Fatalf("fromMessageID = %d, want 0", source.historyCall.fromMessageID)
	}
	if source.historyCall.limit != 25 {
		t.Fatalf("limit = %d, want 25", source.historyCall.limit)
	}
}

func TestSendMessageCmdReturnsMessage(t *testing.T) {
	source := &fakeChatSource{
		sentMessage: Message{ID: 9001, Outgoing: true, Text: "hi", At: mockMoment("10:00")},
	}

	msg := runCmd(t, sendMessageCmd(source, 42, "hi", 1))
	sent, ok := msg.(messageSentMsg)
	if !ok {
		t.Fatalf("cmd returned %T", msg)
	}
	if sent.err != nil {
		t.Fatalf("err = %v", sent.err)
	}
	if sent.chatID != 42 {
		t.Fatalf("chatID = %d, want 42", sent.chatID)
	}
	if sent.operation != 1 {
		t.Fatalf("operation = %d, want 1", sent.operation)
	}
	if sent.message.ID != 9001 {
		t.Fatalf("message.ID = %d, want 9001", sent.message.ID)
	}
	if !source.sendCall.called {
		t.Fatal("ChatSource.SendMessage was not called")
	}
	if source.sendCall.chatID != 42 {
		t.Fatalf("SendMessage chatID = %d, want 42", source.sendCall.chatID)
	}
	if source.sendCall.text != "hi" {
		t.Fatalf("SendMessage text = %q, want hi", source.sendCall.text)
	}
}

func TestSendMessageCmdReturnsError(t *testing.T) {
	err := errors.New("send fail")
	source := &fakeChatSource{sendErr: err}

	msg := runCmd(t, sendMessageCmd(source, 42, "hi", 7))
	sent, ok := msg.(messageSentMsg)
	if !ok {
		t.Fatalf("cmd returned %T", msg)
	}
	if !errors.Is(sent.err, err) {
		t.Fatalf("err = %v, want %v", sent.err, err)
	}
	if sent.chatID != 42 {
		t.Fatalf("chatID = %d, want 42", sent.chatID)
	}
	if sent.operation != 7 {
		t.Fatalf("operation = %d, want 7", sent.operation)
	}
}

// ---- Composer send state machine ----

// prepareConversation returns a source-mode model focused on the
// composer with one chat selected.
func prepareConversation(t *testing.T, source ChatSource) Model {
	t.Helper()

	m := NewModelWithSource(source)
	m, _ = updateModel(t, m, chatsLoadedMsg{
		chats: []Chat{{ID: 7, Title: "A"}},
	})
	m.screen = ScreenConversation
	m.focus = FocusComposer
	m.historyState = loadStateLoaded
	return m
}

func TestEnterComposerSendsMessage(t *testing.T) {
	source := &fakeChatSource{}
	m := prepareConversation(t, source)

	m, _ = updateModel(t, m, pressRunes("hello"))

	updated, cmd := m.Update(press(tea.KeyEnter))
	mm := updated.(Model)

	if mm.sendState != sendStateSending {
		t.Fatalf("sendState = %s, want sending", mm.sendState)
	}
	if mm.sendOperation == 0 {
		t.Fatal("sendOperation must be incremented on send")
	}
	if cmd == nil {
		t.Fatal("Enter must return a send command")
	}

	msg := runCmd(t, cmd)
	sent, ok := msg.(messageSentMsg)
	if !ok {
		t.Fatalf("cmd returned %T", msg)
	}
	if sent.operation != mm.sendOperation {
		t.Fatalf("message operation = %d, want %d",
			sent.operation, mm.sendOperation)
	}
	if !source.sendCall.called {
		t.Fatal("ChatSource.SendMessage was not called")
	}
	if source.sendCall.chatID != 7 {
		t.Fatalf("send chatID = %d, want 7", source.sendCall.chatID)
	}
	if source.sendCall.text != "hello" {
		t.Fatalf("send text = %q, want hello", source.sendCall.text)
	}
}

func TestEnterComposerRejectsWhitespace(t *testing.T) {
	source := &fakeChatSource{}
	m := prepareConversation(t, source)

	m, _ = updateModel(t, m, pressRunes("   "))

	updated, cmd := m.Update(press(tea.KeyEnter))
	mm := updated.(Model)

	// The command that comes back puts the lit placeholder out again
	// (§7.3); what must not come back is a send.
	if cmd != nil {
		if _, ok := runCmd(t, cmd).(composerPlaceholderExpiredMsg); !ok {
			t.Fatal("whitespace-only composer must not produce a send")
		}
	}
	if mm.sendState != sendStateIdle {
		t.Fatalf("sendState = %s, want idle", mm.sendState)
	}
	if source.sendCall.called {
		t.Fatal("SendMessage must not be called")
	}
}

func TestEnterComposerWhileSendingDoesNothing(t *testing.T) {
	source := &fakeChatSource{}
	m := prepareConversation(t, source)

	m, _ = updateModel(t, m, pressRunes("hello"))
	m, _ = updateModel(t, m, press(tea.KeyEnter))

	operation := m.sendOperation

	updated, cmd := m.Update(press(tea.KeyEnter))
	mm := updated.(Model)

	if cmd != nil {
		t.Fatal("Enter while sending must not produce a command")
	}
	if mm.sendState != sendStateSending {
		t.Fatalf("sendState = %s, want sending", mm.sendState)
	}
	if mm.sendOperation != operation {
		t.Fatalf("sendOperation changed: %d, want %d",
			mm.sendOperation, operation)
	}
}

func TestMockComposerDoesNotSend(t *testing.T) {
	m := NewModel()
	m.screen = ScreenConversation
	m.focus = FocusComposer

	m, _ = updateModel(t, m, pressRunes("hello"))

	updated, cmd := m.Update(press(tea.KeyEnter))
	mm := updated.(Model)

	if cmd != nil {
		t.Fatal("mock mode must not produce a send command")
	}
	if mm.sendState != sendStateIdle {
		t.Fatalf("sendState = %s, want idle", mm.sendState)
	}
}

// Composer editing is frozen for the whole duration of a send.
func TestComposerCannotChangeWhileSending(t *testing.T) {
	source := &fakeChatSource{}
	m := prepareConversation(t, source)

	m, _ = updateModel(t, m, pressRunes("hello"))
	m, _ = updateModel(t, m, press(tea.KeyEnter))

	m, _ = updateModel(t, m, pressRunes(" world"))
	m, _ = updateModel(t, m, press(tea.KeyBackspace))
	m, _ = updateModel(t, m, press(tea.KeySpace))
	m, _ = updateModel(t, m, press(tea.KeyCtrlU))

	if got := m.Composer(); got != "hello" {
		t.Fatalf("composer = %q, want %q", got, "hello")
	}
	if m.sendState != sendStateSending {
		t.Fatalf("sendState = %s, want sending", m.sendState)
	}
}

func TestMessageSentClearsComposer(t *testing.T) {
	source := &fakeChatSource{}
	m := prepareConversation(t, source)

	m, _ = updateModel(t, m, pressRunes("hello"))
	m, _ = updateModel(t, m, press(tea.KeyEnter))

	m, _ = updateModel(t, m, messageSentMsg{
		chatID:    7,
		operation: m.sendOperation,
		message:   Message{ID: 9001, Outgoing: true, Text: "hello", At: mockMoment("10:00")},
	})

	if m.Composer() != "" {
		t.Fatalf("composer = %q, want empty after success", m.Composer())
	}
	if m.sendState != sendStateIdle {
		t.Fatalf("sendState = %s, want idle", m.sendState)
	}
	if m.sendErr != nil {
		t.Fatalf("sendErr = %v, want nil", m.sendErr)
	}
}

// A message that was just sent is the newest one, so it goes to the end
// of the conversation (§8.3). Prepending it would put the newest message
// above the older ones, which is a conversation nobody has ever had.
func TestMessageSentAppendsMessage(t *testing.T) {
	source := &fakeChatSource{}
	m := prepareConversation(t, source)

	m.chats[0].Messages = []Message{
		{ID: 99, Text: "oldest"},
		{ID: 100, Text: "older"},
	}
	m, _ = updateModel(t, m, pressRunes("hi"))
	m, _ = updateModel(t, m, press(tea.KeyEnter))

	m, _ = updateModel(t, m, messageSentMsg{
		chatID:    7,
		operation: m.sendOperation,
		message:   Message{ID: 200, Outgoing: true, Text: "hi"},
	})

	messages := m.chats[0].Messages
	if len(messages) != 3 {
		t.Fatalf("len(messages) = %d, want 3", len(messages))
	}
	if messages[2].ID != 200 {
		t.Fatalf("messages[2].ID = %d, want 200", messages[2].ID)
	}
	if messages[0].ID != 99 || messages[1].ID != 100 {
		t.Fatalf("previous order not preserved: %+v", messages)
	}
	if m.selectedMsg != 0 {
		t.Fatalf("selectedMsg = %d, want 0", m.selectedMsg)
	}
}

func TestMessageSentDeduplicatesID(t *testing.T) {
	source := &fakeChatSource{}
	m := prepareConversation(t, source)

	m.chats[0].Messages = []Message{
		{ID: 100, Text: "older"},
		{ID: 200, Text: "already present"},
	}
	m.sendOperation = 1

	m, _ = updateModel(t, m, messageSentMsg{
		chatID:    7,
		operation: 1,
		message:   Message{ID: 200, Outgoing: true, Text: "replacement"},
	})

	messages := m.chats[0].Messages
	if len(messages) != 2 {
		t.Fatalf("len(messages) = %d, want 2", len(messages))
	}
	if messages[1].ID != 200 || messages[1].Text != "replacement" {
		t.Fatalf("messages[1] = %+v, want replaced entry", messages[1])
	}
	if messages[0].ID != 100 {
		t.Fatalf("messages[0].ID = %d, want 100", messages[0].ID)
	}
}

func TestMessageSendErrorPreservesComposer(t *testing.T) {
	source := &fakeChatSource{}
	m := prepareConversation(t, source)

	m, _ = updateModel(t, m, pressRunes("hello"))
	m, _ = updateModel(t, m, press(tea.KeyEnter))

	sendErr := errors.New("network")
	m, _ = updateModel(t, m, messageSentMsg{
		chatID:    7,
		operation: m.sendOperation,
		err:       sendErr,
	})

	if m.Composer() != "hello" {
		t.Fatalf("composer = %q, want preserved", m.Composer())
	}
	if m.sendState != sendStateError {
		t.Fatalf("sendState = %s, want error", m.sendState)
	}
	if !errors.Is(m.sendErr, sendErr) {
		t.Fatalf("sendErr = %v, want %v", m.sendErr, sendErr)
	}
}

func TestMessageSendingIsRendered(t *testing.T) {
	source := &fakeChatSource{}
	m := prepareConversation(t, source)

	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m, _ = updateModel(t, m, pressRunes("hello"))
	m, _ = updateModel(t, m, press(tea.KeyEnter))

	if !strings.Contains(m.View(), "Sending...") {
		t.Fatalf("view = %q, want Sending...", m.View())
	}
}

func TestMessageSendErrorIsRendered(t *testing.T) {
	source := &fakeChatSource{}
	m := prepareConversation(t, source)

	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m, _ = updateModel(t, m, pressRunes("hello"))
	m, _ = updateModel(t, m, press(tea.KeyEnter))

	m, _ = updateModel(t, m, messageSentMsg{
		chatID:    7,
		operation: m.sendOperation,
		err:       errors.New("boom"),
	})

	// §12.1: a message that was not queued is said in fixed words, and
	// the error itself is not on the screen.
	if !strings.Contains(m.View(), "Message was not queued") {
		t.Fatalf("view = %q, want the fixed wording", m.View())
	}
	if strings.Contains(plain(m.View()), "boom") {
		t.Fatalf("the error text is on the screen: %q", plain(m.View()))
	}
}

func TestStaleMessageSentIsIgnored(t *testing.T) {
	source := &fakeChatSource{}
	m := prepareConversation(t, source)
	m.chats = []Chat{
		{ID: 7, Title: "A"},
		{ID: 8, Title: "B"},
	}
	m.selectedChat = 1
	m.historyState = loadStateLoading
	m.composer = []rune("draft")
	m.sendState = sendStateSending
	m.sendOperation = 1

	m, _ = updateModel(t, m, messageSentMsg{
		chatID:    7, // stale: selected chat is 8
		operation: 1,
		message:   Message{ID: 9001, Text: "stale"},
	})

	if m.sendState != sendStateSending {
		t.Fatalf("sendState = %s, want sending", m.sendState)
	}
	if m.Composer() != "draft" {
		t.Fatalf("composer = %q, want draft preserved", m.Composer())
	}
	if len(m.chats[0].Messages) != 0 {
		t.Fatal("stale response must not modify chat A")
	}
	if len(m.chats[1].Messages) != 0 {
		t.Fatal("stale response must not modify chat B")
	}
}

// A result from a superseded send attempt for the same chat must not be
// applied even when the chatID matches: only the current operation
// counts.
func TestOldSendResultForReopenedChatIsIgnored(t *testing.T) {
	source := &fakeChatSource{}
	m := prepareConversation(t, source)

	m.composer = []rune("first")
	m.sendOperation = 10
	m.sendState = sendStateSending

	// Simulate leaving and re-entering the same chat: sendOperation is
	// bumped and the model is back to idle with a fresh draft.
	m.sendOperation = 11
	m.sendState = sendStateIdle
	m.composer = []rune("new draft")

	m, _ = updateModel(t, m, messageSentMsg{
		chatID:    7,
		operation: 10, // stale attempt
		message:   Message{ID: 9001, Text: "first"},
	})

	if got := m.Composer(); got != "new draft" {
		t.Fatalf("composer = %q, want %q", got, "new draft")
	}
	if len(m.chats[0].Messages) != 0 {
		t.Fatal("obsolete send result modified history")
	}
}

// Esc from the conversation invalidates any in-flight operation.
func TestEscapeInvalidatesInFlightOperation(t *testing.T) {
	source := &fakeChatSource{}
	m := prepareConversation(t, source)

	m, _ = updateModel(t, m, pressRunes("hello"))
	m, _ = updateModel(t, m, press(tea.KeyEnter))

	if m.sendState != sendStateSending {
		t.Fatalf("sendState = %s, want sending", m.sendState)
	}

	operation := m.sendOperation

	// Esc hands the keys to the timeline first and leaves the
	// conversation on the next one, so leaving takes two of them.
	updated, _ := m.Update(press(tea.KeyEsc))
	updated, _ = updated.(Model).Update(press(tea.KeyEsc))
	mm := updated.(Model)

	if mm.screen != ScreenChats {
		t.Fatalf("screen = %v, want chats", mm.screen)
	}
	if mm.sendState != sendStateIdle {
		t.Fatalf("sendState = %s, want idle", mm.sendState)
	}
	if mm.sendOperation == operation {
		t.Fatalf("sendOperation = %d, want != %d", mm.sendOperation, operation)
	}

	// Late result for the previous attempt must be ignored.
	mm, _ = updateModel(t, mm, messageSentMsg{
		chatID:    7,
		operation: operation,
		message:   Message{ID: 9001, Text: "hello"},
	})

	if len(mm.chats[0].Messages) != 0 {
		t.Fatal("late result from invalidated operation modified history")
	}
}

// Ctrl+U behavior depends on send state.
func TestCtrlUBehaviorDependsOnSendState(t *testing.T) {
	t.Run("error clears draft and error", func(t *testing.T) {
		source := &fakeChatSource{}
		m := prepareConversation(t, source)

		m, _ = updateModel(t, m, pressRunes("hello"))
		m, _ = updateModel(t, m, press(tea.KeyEnter))
		m, _ = updateModel(t, m, messageSentMsg{
			chatID:    7,
			operation: m.sendOperation,
			err:       errors.New("boom"),
		})

		if m.sendState != sendStateError {
			t.Fatalf("sendState = %s, want error", m.sendState)
		}

		m, _ = updateModel(t, m, press(tea.KeyCtrlU))

		if m.Composer() != "" {
			t.Fatalf("composer = %q, want empty", m.Composer())
		}
		if m.sendState != sendStateIdle {
			t.Fatalf("sendState = %s, want idle", m.sendState)
		}
		if m.sendErr != nil {
			t.Fatalf("sendErr = %v, want nil", m.sendErr)
		}
	})

	t.Run("sending is a no-op", func(t *testing.T) {
		source := &fakeChatSource{}
		m := prepareConversation(t, source)

		m, _ = updateModel(t, m, pressRunes("hello"))
		m, _ = updateModel(t, m, press(tea.KeyEnter))

		m, _ = updateModel(t, m, press(tea.KeyCtrlU))

		if m.Composer() != "hello" {
			t.Fatalf("composer = %q, want hello", m.Composer())
		}
		if m.sendState != sendStateSending {
			t.Fatalf("sendState = %s, want sending", m.sendState)
		}
	})
}

// ---- View ----

func TestViewShowsLoadingChats(t *testing.T) {
	m := NewModelWithSource(&fakeChatSource{})
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	if !strings.Contains(m.View(), "Loading chats") {
		t.Fatalf("view = %q, want loading", m.View())
	}
}

func TestViewShowsErrorChats(t *testing.T) {
	m := NewModelWithSource(&fakeChatSource{})
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m, _ = updateModel(t, m, chatsLoadedMsg{err: errors.New("boom")})

	if !strings.Contains(m.View(), "Failed to load chats") {
		t.Fatalf("view = %q", m.View())
	}
}

func TestViewShowsEmptyChats(t *testing.T) {
	m := NewModelWithSource(&fakeChatSource{})
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m, _ = updateModel(t, m, chatsLoadedMsg{chats: []Chat{}})

	if !strings.Contains(m.View(), "No chats") {
		t.Fatalf("view = %q", m.View())
	}
}

func TestViewShowsLoadingHistory(t *testing.T) {
	m := NewModelWithSource(&fakeChatSource{})
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m, _ = updateModel(t, m, chatsLoadedMsg{chats: []Chat{{ID: 1, Title: "A"}}})

	updated, _ := m.Update(press(tea.KeyEnter))
	mm := updated.(Model)
	mm, _ = updateModel(t, mm, tea.WindowSizeMsg{Width: 80, Height: 24})

	if !strings.Contains(mm.View(), "Loading history") {
		t.Fatalf("view = %q", mm.View())
	}
}

// ---- Mock path unchanged ----

func TestMockPathRendersChats(t *testing.T) {
	m := NewModel()
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	view := m.View()
	if !strings.Contains(view, "Alice") {
		t.Fatalf("mock view = %q", view)
	}
}

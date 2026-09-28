package tui

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// historyCall is one recorded LoadHistory invocation.
type historyCall struct {
	chatID        int64
	fromMessageID int64
	limit         int
}

// recordingChatSource records every LoadHistory call and answers each one
// from a scripted queue of pages. The queue is consumed in order, so a
// test can describe a multi-page history.
type recordingChatSource struct {
	pages []HistoryPage
	errs  []error

	mu    sync.Mutex
	calls []historyCall
}

func (r *recordingChatSource) ListChats(ctx context.Context) ([]Chat, error) {
	return nil, nil
}

func (r *recordingChatSource) LoadHistory(
	ctx context.Context,
	chatID int64,
	fromMessageID int64,
	limit int,
) (HistoryPage, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.calls = append(r.calls, historyCall{
		chatID:        chatID,
		fromMessageID: fromMessageID,
		limit:         limit,
	})

	index := len(r.calls) - 1
	if index < len(r.errs) && r.errs[index] != nil {
		return HistoryPage{}, r.errs[index]
	}
	if index >= len(r.pages) {
		return HistoryPage{}, nil
	}
	return r.pages[index], nil
}

func (r *recordingChatSource) SendMessage(
	ctx context.Context,
	chatID int64,
	text string,
) (Message, error) {
	return Message{}, nil
}

// callCount returns how many times LoadHistory was called.
func (r *recordingChatSource) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

// lastCall returns the most recent LoadHistory call.
func (r *recordingChatSource) lastCall(t *testing.T) historyCall {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.calls) == 0 {
		t.Fatal("LoadHistory was never called")
	}
	return r.calls[len(r.calls)-1]
}

// openConversationWithHistory drives a model into a conversation whose
// first history page is already loaded, the way the TUI does on Enter.
func openConversationWithHistory(
	t *testing.T,
	source ChatSource,
	chatID int64,
	first HistoryPage,
) Model {
	t.Helper()

	m := NewModelWithSource(source)
	m, _ = updateModel(t, m, chatsLoadedMsg{chats: []Chat{{ID: chatID, Title: "A"}}})

	m, cmd := updateModel(t, m, press(tea.KeyEnter))
	if cmd == nil {
		t.Fatal("Enter did not start a history load")
	}
	m, _ = updateModel(t, m, runCmd(t, cmd))

	if m.historyState != loadStateLoaded {
		t.Fatalf("historyState = %s, want loaded", m.historyState)
	}
	return m
}

// selectOldestMessage moves the cursor onto the oldest loaded message.
//
// The conversation is chronological (§8.3), so the oldest message is the
// first one and ↑ is the gesture that reaches it: the same walk as before,
// in the other direction.
func selectOldestMessage(t *testing.T, m Model) Model {
	t.Helper()

	m.focus = FocusHistory
	for m.selectedMsg > 0 {
		m, _ = updateModel(t, m, press(tea.KeyUp))
	}

	return m
}

// firstPage is a two-message newest-first page ending at message 99.
func firstPage() HistoryPage {
	return HistoryPage{
		Messages: []Message{
			{ID: 100, Text: "newest"},
			{ID: 99, Text: "oldest loaded"},
		},
		NextFrom: 99,
		HasMore:  true,
	}
}

// olderPage overlaps firstPage on message 99, which NextFrom names
// inclusively, and adds two older messages.
func olderPage() HistoryPage {
	return HistoryPage{
		Messages: []Message{
			{ID: 99, Text: "oldest loaded"},
			{ID: 98, Text: "older"},
			{ID: 97, Text: "oldest"},
		},
		NextFrom: 97,
		HasMore:  true,
	}
}

// ---- Load more on ↑ at the oldest loaded message ----

// The gesture for reaching further back is ↑ at the top of the
// conversation, because that is where the older messages are (§8.3). What
// the request does once it has been made is #12's business and has not
// changed.
func TestUpAtOldestLoadedMessageRequestsNextPage(t *testing.T) {
	source := &recordingChatSource{pages: []HistoryPage{firstPage(), olderPage()}}
	m := openConversationWithHistory(t, source, 7, firstPage())
	m = selectOldestMessage(t, m)

	if source.callCount() != 1 {
		t.Fatalf("callCount before ↑ = %d, want 1", source.callCount())
	}

	_, cmd := updateModel(t, m, press(tea.KeyUp))
	if cmd == nil {
		t.Fatal("↑ on the oldest loaded message must request the next page")
	}
	runCmd(t, cmd)

	call := source.lastCall(t)
	if call.fromMessageID != 99 {
		t.Fatalf("fromMessageID = %d, want 99 (NextFrom of the first page)", call.fromMessageID)
	}
	if call.chatID != 7 {
		t.Fatalf("chatID = %d, want 7", call.chatID)
	}
	if call.limit != historyPageSize {
		t.Fatalf("limit = %d, want %d", call.limit, historyPageSize)
	}
}

func TestKAtOldestLoadedMessageRequestsNextPage(t *testing.T) {
	source := &recordingChatSource{pages: []HistoryPage{firstPage(), olderPage()}}
	m := openConversationWithHistory(t, source, 7, firstPage())
	m = selectOldestMessage(t, m)

	_, cmd := updateModel(t, m, pressRunes("k"))
	if cmd == nil {
		t.Fatal("k on the oldest loaded message must request the next page")
	}
	runCmd(t, cmd)

	if got := source.lastCall(t).fromMessageID; got != 99 {
		t.Fatalf("fromMessageID = %d, want 99", got)
	}
}

// The newest message is where a request would make no sense: there is
// nothing newer to ask for, and a key that only sometimes loads a page is a
// key nobody trusts.
func TestDownAtTheNewestMessageDoesNotRequestNextPage(t *testing.T) {
	source := &recordingChatSource{pages: []HistoryPage{firstPage()}}
	m := openConversationWithHistory(t, source, 7, firstPage())
	m.focus = FocusHistory
	m.selectedMsg = len(m.selected().Messages) - 1

	m, cmd := updateModel(t, m, press(tea.KeyDown))
	if cmd != nil {
		t.Fatal("↓ on the newest message must not request a page")
	}
	if m.selectedMsg != 1 {
		t.Fatalf("selectedMsg = %d, want 1 (the newest message)", m.selectedMsg)
	}
	if source.callCount() != 1 {
		t.Fatalf("callCount = %d, want 1", source.callCount())
	}
}

// ---- Adding a page on top ----

// The page goes on top, because it is older: a conversation read from the
// top down would otherwise show the newest message in the middle of itself.
func TestOlderPageAddsMessagesOnTopAndDropsDuplicates(t *testing.T) {
	source := &recordingChatSource{pages: []HistoryPage{firstPage(), olderPage()}}
	m := openConversationWithHistory(t, source, 7, firstPage())
	m = selectOldestMessage(t, m)

	_, cmd := updateModel(t, m, press(tea.KeyUp))
	m, _ = updateModel(t, m, runCmd(t, cmd))

	got := m.selected().Messages
	want := []int64{97, 98, 99, 100}
	if len(got) != len(want) {
		t.Fatalf("messages = %d (%v), want %d (%v)", len(got), messageIDs(got), len(want), want)
	}
	for i, id := range want {
		if got[i].ID != id {
			t.Fatalf("messages[%d].ID = %d, want %d (full: %v)", i, got[i].ID, id, messageIDs(got))
		}
	}
}

func TestOlderPageKeepsSelectionOnTheSameMessage(t *testing.T) {
	source := &recordingChatSource{pages: []HistoryPage{firstPage(), olderPage()}}
	m := openConversationWithHistory(t, source, 7, firstPage())
	m = selectOldestMessage(t, m)

	selectedID := m.selected().Messages[m.selectedMsg].ID

	_, cmd := updateModel(t, m, press(tea.KeyUp))
	m, _ = updateModel(t, m, runCmd(t, cmd))

	if got := m.selected().Messages[m.selectedMsg].ID; got != selectedID {
		t.Fatalf("selected message ID = %d, want %d (selection must not move)", got, selectedID)
	}
}

func TestOlderPageMovesTheBoundaryOlder(t *testing.T) {
	source := &recordingChatSource{pages: []HistoryPage{firstPage(), olderPage()}}
	m := openConversationWithHistory(t, source, 7, firstPage())
	m = selectOldestMessage(t, m)

	m, cmd := updateModel(t, m, press(tea.KeyUp))
	m, _ = updateModel(t, m, runCmd(t, cmd))

	if got := historyBoundary(m.selected()); got != 97 {
		t.Fatalf("boundary = %d, want 97 (the oldest message after the page)", got)
	}
}

func TestThirdPageRequestsFromTheOldestMessageOfTheSecond(t *testing.T) {
	third := HistoryPage{
		Messages: []Message{{ID: 97, Text: "oldest"}, {ID: 96, Text: "deepest"}},
		NextFrom: 96,
		HasMore:  false,
	}
	source := &recordingChatSource{
		pages: []HistoryPage{firstPage(), olderPage(), third},
	}
	m := openConversationWithHistory(t, source, 7, firstPage())
	m = selectOldestMessage(t, m)

	_, cmd := updateModel(t, m, press(tea.KeyUp))
	m, _ = updateModel(t, m, runCmd(t, cmd))

	m = selectOldestMessage(t, m)
	m, cmd = updateModel(t, m, press(tea.KeyUp))
	m, _ = updateModel(t, m, runCmd(t, cmd))

	if got := source.lastCall(t).fromMessageID; got != 97 {
		t.Fatalf("third page fromMessageID = %d, want 97", got)
	}
	if len(m.selected().Messages) != 5 {
		t.Fatalf("messages = %d (%v), want 5", len(m.selected().Messages), messageIDs(m.selected().Messages))
	}
}

// ---- One request in flight at a time ----

func TestSecondUpWhileLoadingDoesNotStartAnotherRequest(t *testing.T) {
	source := &recordingChatSource{pages: []HistoryPage{firstPage(), olderPage()}}
	m := openConversationWithHistory(t, source, 7, firstPage())
	m = selectOldestMessage(t, m)

	m, cmd := updateModel(t, m, press(tea.KeyUp))
	if cmd == nil {
		t.Fatal("the first ↑ must start a request")
	}
	// Count the call, but deliberately do not deliver the response: the
	// request stays in flight.
	runCmd(t, cmd)

	if source.callCount() != 2 {
		t.Fatalf("callCount = %d, want 2", source.callCount())
	}

	_, again := updateModel(t, m, press(tea.KeyUp))
	if again != nil {
		t.Fatal("↑ while a page is in flight must not start a second request")
	}
	if source.callCount() != 2 {
		t.Fatalf("callCount = %d, want 2 (no second request)", source.callCount())
	}
}

func TestRequestAllowedAgainAfterTheResponseArrives(t *testing.T) {
	source := &recordingChatSource{pages: []HistoryPage{firstPage(), olderPage()}}
	m := openConversationWithHistory(t, source, 7, firstPage())
	m = selectOldestMessage(t, m)

	_, cmd := updateModel(t, m, press(tea.KeyUp))
	m, _ = updateModel(t, m, runCmd(t, cmd))

	m = selectOldestMessage(t, m)
	_, cmd = updateModel(t, m, press(tea.KeyUp))
	if cmd == nil {
		t.Fatal("↑ after the response arrived must be able to request again")
	}
	runCmd(t, cmd)

	if source.callCount() != 3 {
		t.Fatalf("callCount = %d, want 3", source.callCount())
	}
}

// ---- Exhaustion ----

func TestPageWithoutNewMessagesMarksHistoryExhausted(t *testing.T) {
	// A page that repeats only already-loaded messages adds nothing.
	stale := HistoryPage{
		Messages: []Message{{ID: 99, Text: "oldest loaded"}},
		NextFrom: 99,
		HasMore:  true,
	}
	source := &recordingChatSource{pages: []HistoryPage{firstPage(), stale}}
	m := openConversationWithHistory(t, source, 7, firstPage())
	m = selectOldestMessage(t, m)

	_, cmd := updateModel(t, m, press(tea.KeyUp))
	m, _ = updateModel(t, m, runCmd(t, cmd))

	if !m.historyExhausted {
		t.Fatal("a page that added no new message must exhaust the history")
	}

	m = selectOldestMessage(t, m)
	_, cmd = updateModel(t, m, press(tea.KeyUp))
	if cmd != nil {
		t.Fatal("an exhausted history must not request more pages")
	}
	if source.callCount() != 2 {
		t.Fatalf("callCount = %d, want 2", source.callCount())
	}
}

func TestEmptyPageMarksHistoryExhausted(t *testing.T) {
	source := &recordingChatSource{pages: []HistoryPage{
		firstPage(),
		{Messages: nil, NextFrom: 0, HasMore: false},
	}}
	m := openConversationWithHistory(t, source, 7, firstPage())
	m = selectOldestMessage(t, m)

	_, cmd := updateModel(t, m, press(tea.KeyUp))
	m, _ = updateModel(t, m, runCmd(t, cmd))

	if !m.historyExhausted {
		t.Fatal("an empty page must exhaust the history")
	}
}

// The end of a history is an empty page and nothing else.
//
// It used to be the other way round: HasMore was a len(messages) ==
// limit heuristic, so a first page of one or two messages said "there is
// nothing more" about a chat with years of it in, and the conversation a
// user opened was the last two messages of the chat. A source that says
// the history ended is now believed, and that is what stops the ↑ key
// asking forever.
func TestPageThatSaysThereIsNoMoreStopsTheLoading(t *testing.T) {
	short := HistoryPage{
		Messages: []Message{{ID: 5, Text: "only message"}},
		NextFrom: 5,
		HasMore:  false,
	}
	source := &recordingChatSource{pages: []HistoryPage{short, {}}}
	m := openConversationWithHistory(t, source, 7, short)
	m = selectOldestMessage(t, m)

	_, cmd := updateModel(t, m, press(tea.KeyUp))
	if cmd != nil {
		t.Fatal("a page that said the history ended must not be asked again")
	}
	if source.callCount() != 1 {
		t.Fatalf("callCount = %d, want 1: the ↑ asked again", source.callCount())
	}
}

// The other end of the same rule: a page that says there is more is
// believed too, and the messages above it arrive.
func TestPageThatSaysThereIsMoreKeepsTheLoading(t *testing.T) {
	short := HistoryPage{
		Messages: []Message{{ID: 5, Text: "only message"}},
		NextFrom: 5,
		HasMore:  true,
	}
	older := HistoryPage{
		Messages: []Message{{ID: 5, Text: "only message"}, {ID: 4, Text: "older"}},
		NextFrom: 4,
		HasMore:  true,
	}
	source := &recordingChatSource{pages: []HistoryPage{short, older}}
	m := openConversationWithHistory(t, source, 7, short)
	m = selectOldestMessage(t, m)

	_, cmd := updateModel(t, m, press(tea.KeyUp))
	if cmd == nil {
		t.Fatal("a page that said there was more must be asked again")
	}
	m, _ = updateModel(t, m, runCmd(t, cmd))

	if len(m.selected().Messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(m.selected().Messages))
	}
}

// ---- Errors ----

func TestOlderPageErrorKeepsLoadedMessages(t *testing.T) {
	boom := errors.New("older page failed")
	source := &recordingChatSource{
		pages: []HistoryPage{firstPage()},
		errs:  []error{nil, boom},
	}
	m := openConversationWithHistory(t, source, 7, firstPage())
	m = selectOldestMessage(t, m)

	_, cmd := updateModel(t, m, press(tea.KeyUp))
	m, _ = updateModel(t, m, runCmd(t, cmd))

	if !errors.Is(m.historyMoreErr, boom) {
		t.Fatalf("historyMoreErr = %v, want %v", m.historyMoreErr, boom)
	}
	if len(m.selected().Messages) != 2 {
		t.Fatalf("messages = %d, want 2 (an error must not clear history)", len(m.selected().Messages))
	}
	if m.historyExhausted {
		t.Fatal("an error must not exhaust the history")
	}
	if m.historyState != loadStateLoaded {
		t.Fatalf("historyState = %s, want loaded", m.historyState)
	}
}

func TestOlderPageErrorKeepsTheCursorForRetry(t *testing.T) {
	boom := errors.New("older page failed")
	source := &recordingChatSource{
		pages: []HistoryPage{firstPage(), olderPage(), olderPage()},
		errs:  []error{nil, boom},
	}
	m := openConversationWithHistory(t, source, 7, firstPage())
	m = selectOldestMessage(t, m)

	m, cmd := updateModel(t, m, press(tea.KeyUp))
	m, _ = updateModel(t, m, runCmd(t, cmd))

	if m.historyMoreErr == nil {
		t.Fatal("the first attempt must fail")
	}

	m = selectOldestMessage(t, m)
	m, cmd = updateModel(t, m, press(tea.KeyUp))
	if cmd == nil {
		t.Fatal("↑ after an error must retry the request")
	}
	m, _ = updateModel(t, m, runCmd(t, cmd))

	if got := source.lastCall(t).fromMessageID; got != 99 {
		t.Fatalf("retry fromMessageID = %d, want 99", got)
	}
	if len(m.selected().Messages) != 4 {
		t.Fatalf("messages = %d, want 4 after a successful retry", len(m.selected().Messages))
	}
	if m.historyMoreErr != nil {
		t.Fatalf("historyMoreErr = %v, want nil after a successful retry", m.historyMoreErr)
	}
}

// The failure of an older page belongs at the top of the timeline, where
// the page it is about would have gone.
func TestErrorLineIsVisibleAtTheTopOfHistory(t *testing.T) {
	boom := errors.New("older page failed")
	source := &recordingChatSource{
		pages: []HistoryPage{firstPage()},
		errs:  []error{nil, boom},
	}
	m := openConversationWithHistory(t, source, 7, firstPage())
	m = selectOldestMessage(t, m)

	_, cmd := updateModel(t, m, press(tea.KeyUp))
	m, _ = updateModel(t, m, runCmd(t, cmd))

	m.width, m.height = 80, 24
	view := m.View()
	if !strings.Contains(view, "older page failed") {
		t.Fatalf("view does not report the load error:\n%s", view)
	}
}

// ---- Stale responses ----

func TestOlderPageForAnotherChatIsIgnored(t *testing.T) {
	source := &recordingChatSource{pages: []HistoryPage{firstPage(), olderPage()}}
	m := NewModelWithSource(source)
	m, _ = updateModel(t, m, chatsLoadedMsg{chats: []Chat{
		{ID: 7, Title: "A"},
		{ID: 8, Title: "B"},
	}})

	m, cmd := updateModel(t, m, press(tea.KeyEnter))
	m, _ = updateModel(t, m, runCmd(t, cmd))

	m.selectedChat = 1
	m.focus = FocusHistory
	m.selectedMsg = 0

	before := len(m.selected().Messages)
	m, _ = updateModel(t, m, historyLoadedMsg{
		chatID:        7,
		fromMessageID: 99,
		operation:     m.historyOperation,
		page:          olderPage(),
	})

	if len(m.selected().Messages) != before {
		t.Fatal("a page for another chat must not change the current chat")
	}
}

func TestOlderPageFromAPriorEntryIntoTheSameChatIsIgnored(t *testing.T) {
	source := &recordingChatSource{pages: []HistoryPage{firstPage(), olderPage()}}
	m := openConversationWithHistory(t, source, 7, firstPage())
	m = selectOldestMessage(t, m)

	_, cmd := updateModel(t, m, press(tea.KeyUp))
	stale := runCmd(t, cmd)

	// The user leaves the chat and comes back: a new history operation
	// starts, so the in-flight page belongs to the previous entry.
	m, _ = updateModel(t, m, press(tea.KeyEsc))
	m.selectedChat = 0
	m, cmd = updateModel(t, m, press(tea.KeyEnter))
	if cmd != nil {
		t.Fatal("re-entering a chat with messages loaded must not refetch history")
	}
	m, _ = updateModel(t, m, stale)

	if len(m.selected().Messages) != 2 {
		t.Fatalf(
			"messages = %d (%v), want 2: a page from the previous entry must be dropped",
			len(m.selected().Messages),
			messageIDs(m.selected().Messages),
		)
	}
}

// ---- Cursor is independent of sending ----

func TestSentMessageDoesNotMoveTheHistoryCursor(t *testing.T) {
	source := &recordingChatSource{pages: []HistoryPage{firstPage()}}
	m := openConversationWithHistory(t, source, 7, firstPage())
	m = selectOldestMessage(t, m)

	boundary := historyBoundary(m.selected())

	m, _ = updateModel(t, m, messageSentMsg{
		chatID:    7,
		operation: m.sendOperation,
		message:   Message{ID: 101, Outgoing: true, Text: "sent"},
	})

	if got := historyBoundary(m.selected()); got != boundary {
		t.Fatalf("boundary = %d, want %d (a sent message must not move the cursor)", got, boundary)
	}
	if last := m.selected().Messages[len(m.selected().Messages)-1]; last.ID != 101 {
		t.Fatalf("the newest message is %d, want 101 (a sent message goes to the end)",
			last.ID)
	}
	if m.selectedMsg != 0 {
		t.Fatalf("selectedMsg = %d, want 0 (the cursor is still on the oldest)", m.selectedMsg)
	}
}

// ---- Mock mode and the first load are unchanged ----

func TestMockModeDoesNotRequestOlderPages(t *testing.T) {
	m := NewModel()
	m.screen = ScreenConversation
	m.focus = FocusHistory
	m.selectedMsg = 0

	_, cmd := updateModel(t, m, press(tea.KeyUp))
	if cmd != nil {
		t.Fatal("mock mode must not return a command on ↑")
	}
}

func TestFirstPageLoadIsUnchanged(t *testing.T) {
	source := &recordingChatSource{pages: []HistoryPage{firstPage()}}
	m := openConversationWithHistory(t, source, 7, firstPage())

	call := source.lastCall(t)
	if call.fromMessageID != 0 {
		t.Fatalf("first page fromMessageID = %d, want 0", call.fromMessageID)
	}
	if len(m.selected().Messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(m.selected().Messages))
	}
	if m.selectedMsg != 1 {
		t.Fatalf("selectedMsg = %d, want 1 (the newest message)", m.selectedMsg)
	}
}

// ---- Re-entering a chat with cached messages ----

func TestReentryIntoCachedChatCanStillPaginate(t *testing.T) {
	source := &recordingChatSource{pages: []HistoryPage{firstPage(), olderPage()}}
	m := openConversationWithHistory(t, source, 7, firstPage())
	m, _ = updateModel(t, m, press(tea.KeyEsc))
	m, _ = updateModel(t, m, press(tea.KeyEnter))
	m = selectOldestMessage(t, m)

	m, cmd := updateModel(t, m, press(tea.KeyUp))
	if cmd == nil {
		t.Fatal("↓ on the oldest cached message did not request an older page")
	}
	m, _ = updateModel(t, m, runCmd(t, cmd))

	if len(m.selected().Messages) != 4 {
		t.Fatalf(
			"messages = %d (%v), want 4: re-entering a chat must keep pagination working",
			len(m.selected().Messages),
			messageIDs(m.selected().Messages),
		)
	}
}

func TestReentryIntoCachedChatRequestsFromTheOldestCachedMessage(t *testing.T) {
	source := &recordingChatSource{pages: []HistoryPage{firstPage(), olderPage(), olderPage()}}
	m := openConversationWithHistory(t, source, 7, firstPage())
	m = selectOldestMessage(t, m)

	m, cmd := updateModel(t, m, press(tea.KeyUp))
	m, _ = updateModel(t, m, runCmd(t, cmd))
	if m.historyExhausted {
		t.Fatal("the second page adds messages, so history must not be exhausted")
	}

	m, _ = updateModel(t, m, press(tea.KeyEsc))
	m, _ = updateModel(t, m, press(tea.KeyEnter))
	m = selectOldestMessage(t, m)
	m, cmd = updateModel(t, m, press(tea.KeyUp))
	if cmd == nil {
		t.Fatal("↓ after re-entry must request a page")
	}
	runCmd(t, cmd)

	if got := source.lastCall(t).fromMessageID; got != 97 {
		t.Fatalf("fromMessageID = %d, want 97 (the oldest cached message)", got)
	}
}

func TestReentryAfterAFailedFirstLoadInAnotherChatCanPaginate(t *testing.T) {
	boom := errors.New("history fail")
	source := &recordingChatSource{
		pages: []HistoryPage{{Messages: nil}, olderPage(), olderPage()},
		errs:  []error{boom},
	}

	m := NewModelWithSource(source)
	m, _ = updateModel(t, m, chatsLoadedMsg{chats: []Chat{
		{ID: 7, Title: "A"},
		{ID: 8, Title: "B"},
	}})

	// Chat A: the first page fails, leaving historyState as error.
	m, cmd := updateModel(t, m, press(tea.KeyEnter))
	m, _ = updateModel(t, m, runCmd(t, cmd))
	if m.historyState != loadStateError {
		t.Fatalf("historyState = %s, want error", m.historyState)
	}

	// Chat B already has cached messages, so no first page is requested.
	// The cache is chronological like everything else.
	m.chats[1].Messages = []Message{
		{ID: 99, Text: "oldest loaded"},
		{ID: 100, Text: "newest"},
	}
	// Two of them: the composer hands over to the timeline, and the
	// timeline leaves.
	m, _ = updateModel(t, m, press(tea.KeyEsc))
	m, _ = updateModel(t, m, press(tea.KeyEsc))
	m.selectedChat = 1
	m, _ = updateModel(t, m, press(tea.KeyEnter))

	if m.historyState != loadStateLoaded {
		t.Fatalf(
			"historyState = %s, want loaded: another chat's failure must not block this one",
			m.historyState,
		)
	}

	m = selectOldestMessage(t, m)
	m, cmd = updateModel(t, m, press(tea.KeyUp))
	if cmd == nil {
		t.Fatal("↓ on the oldest cached message must request a page")
	}
	m, _ = updateModel(t, m, runCmd(t, cmd))

	if len(m.selected().Messages) != 4 {
		t.Fatalf("messages = %d (%v), want 4", len(m.selected().Messages), messageIDs(m.selected().Messages))
	}
}

func TestEnteringAnotherChatResetsTheHistoryFlags(t *testing.T) {
	source := &recordingChatSource{
		pages: []HistoryPage{firstPage(), olderPage(), firstPage()},
	}
	m := openConversationWithHistory(t, source, 7, firstPage())
	m = selectOldestMessage(t, m)

	m, cmd := updateModel(t, m, press(tea.KeyUp))
	m, _ = updateModel(t, m, runCmd(t, cmd))
	if got := historyBoundary(m.selected()); got != 97 {
		t.Fatalf("boundary = %d, want 97", got)
	}

	// Two of them: the composer hands over to the timeline, and the
	// timeline leaves.
	m, _ = updateModel(t, m, press(tea.KeyEsc))
	m, _ = updateModel(t, m, press(tea.KeyEsc))
	m.selectedChat = 1
	m.chats = append(m.chats, Chat{ID: 8, Title: "B"})
	m, cmd = updateModel(t, m, press(tea.KeyEnter))
	m, _ = updateModel(t, m, runCmd(t, cmd))

	if got := historyBoundary(m.selected()); got != 99 {
		t.Fatalf("boundary = %d, want 99 for the newly entered chat", got)
	}
	if m.historyExhausted {
		t.Fatal("entering a chat must clear the exhausted flag")
	}
	if m.historyMoreErr != nil {
		t.Fatalf("historyMoreErr = %v, want nil", m.historyMoreErr)
	}
}

// ---- View ----

func TestLoadingLineIsVisibleWhileAnOlderPageLoads(t *testing.T) {
	source := &recordingChatSource{pages: []HistoryPage{firstPage(), olderPage()}}
	m := openConversationWithHistory(t, source, 7, firstPage())
	m = selectOldestMessage(t, m)

	m, cmd := updateModel(t, m, press(tea.KeyUp))
	if cmd == nil {
		t.Fatal("↓ must start a request")
	}
	// The response is deliberately not delivered: the request is in flight.

	m.width, m.height = 80, 24
	if view := m.View(); !strings.Contains(view, "Loading older messages...") {
		t.Fatalf("view does not show the loading line:\n%s", view)
	}
}

func TestLoadingLineIsGoneAfterThePageArrives(t *testing.T) {
	source := &recordingChatSource{pages: []HistoryPage{firstPage(), olderPage()}}
	m := openConversationWithHistory(t, source, 7, firstPage())
	m = selectOldestMessage(t, m)

	m, cmd := updateModel(t, m, press(tea.KeyUp))
	m, _ = updateModel(t, m, runCmd(t, cmd))

	m.width, m.height = 80, 24
	if view := m.View(); strings.Contains(view, "Loading older messages...") {
		t.Fatalf("view still shows the loading line:\n%s", view)
	}
}

func TestLoadedMessagesStayVisibleDuringALoad(t *testing.T) {
	source := &recordingChatSource{pages: []HistoryPage{firstPage(), olderPage()}}
	m := openConversationWithHistory(t, source, 7, firstPage())
	m = selectOldestMessage(t, m)

	updated, cmd := updateModel(t, m, press(tea.KeyUp))
	if cmd == nil {
		t.Fatal("↓ must start a request")
	}
	// The response is deliberately not delivered: the request is in flight.
	m = updated
	m.width, m.height = 80, 24

	view := m.View()
	for _, want := range []string{"newest", "oldest loaded"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view lost %q while loading:\n%s", want, view)
		}
	}
}

// messageIDs extracts message IDs for readable failure output.
func messageIDs(messages []Message) []int64 {
	ids := make([]int64, 0, len(messages))
	for _, msg := range messages {
		ids = append(ids, msg.ID)
	}
	return ids
}

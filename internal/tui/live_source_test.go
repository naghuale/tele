package tui

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"telecli/internal/tui/theme"
)

// The tests of the live chat list: a chat that receives a message while
// the list is on the screen, and the messages that arrive in the
// conversation that is open.
//
// The source below is the whole of the Telegram side of them. It is a fake
// rather than a session because a session needs a TDLib, and the interface
// asks four questions of the live state: is it there, has it changed, what
// does the list say now, and what has happened in the chat that is open.

// fakeLiveSource is the live state a test drives.
//
// The three things a test writes are the list, the events of a chat and the
// signal that says something changed. Everything else is read back: the
// number of reads says how many redraws asked for the list, and the number
// of waits how many goroutines the subscription left behind.
type fakeLiveSource struct {
	mu        sync.Mutex
	chats     []LiveChat
	events    map[int64]LiveMessageEvents
	available bool
	reads     int
	waits     int
	waiting   int
	signal    chan struct{}
}

// newFakeLiveSource returns a live state that is there and quiet.
func newFakeLiveSource() *fakeLiveSource {
	return &fakeLiveSource{
		available: true,
		events:    map[int64]LiveMessageEvents{},
		signal:    make(chan struct{}, 1),
	}
}

// setChats puts a list on the source and says that it changed.
func (f *fakeLiveSource) setChats(chats []LiveChat) {
	if f == nil {
		return
	}

	f.mu.Lock()
	f.chats = chats
	f.mu.Unlock()

	f.notify()
}

// setEvents puts the events of one chat on the source and says that
// something changed.
func (f *fakeLiveSource) setEvents(chatID int64, events LiveMessageEvents) {
	if f == nil {
		return
	}

	f.mu.Lock()
	if f.events == nil {
		f.events = map[int64]LiveMessageEvents{}
	}
	f.events[chatID] = events
	f.mu.Unlock()

	f.notify()
}

// notify posts the change signal, the way the store does: coalesced, and
// never blocking the pump that writes it.
func (f *fakeLiveSource) notify() {
	if f == nil || f.signal == nil {
		return
	}

	select {
	case f.signal <- struct{}{}:
	default:
	}
}

// readsOf returns how many times the chat list has been read.
func (f *fakeLiveSource) readsOf() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.reads
}

// waitsOf returns how many waits for a change the subscription has started.
func (f *fakeLiveSource) waitsOf() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.waits
}

// waitingOf returns how many of those waits are running right now.
//
// The difference between the two counts is a goroutine: a wait that was
// started is a fact of the past, and a wait that is running is a goroutine
// that is still parked on the change signal.
func (f *fakeLiveSource) waitingOf() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.waiting
}

// Available implements ChatLiveSource.
func (f *fakeLiveSource) Available() bool {
	if f == nil {
		return false
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	return f.available
}

// WaitForChange implements ChatLiveSource.
//
// The wait that is running is counted as well as the one that was started,
// because a program that was closed without letting go of its subscription
// is a program with a goroutine left waiting.
func (f *fakeLiveSource) WaitForChange(ctx context.Context) {
	if f == nil {
		return
	}

	f.mu.Lock()
	f.waits++
	f.waiting++
	f.mu.Unlock()

	defer func() {
		f.mu.Lock()
		f.waiting--
		f.mu.Unlock()
	}()

	select {
	case <-f.signal:
	case <-ctx.Done():
	}
}

// Chats implements ChatLiveSource.
func (f *fakeLiveSource) Chats() []LiveChat {
	if f == nil {
		return nil
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	f.reads++

	return append([]LiveChat(nil), f.chats...)
}

// MessageEvents implements ChatLiveSource.
func (f *fakeLiveSource) MessageEvents(
	chatID int64,
	_ uint64,
) LiveMessageEvents {
	if f == nil {
		return LiveMessageEvents{}
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	return f.events[chatID]
}

var _ ChatLiveSource = (*fakeLiveSource)(nil)

// liveMoment is when the messages of the fixtures were sent, and the clock
// every model of this file reads: a screen that says when a message arrived
// is a screen whose bytes are the bytes of the machine that drew it unless
// the moment is given.
var liveMoment = time.Date(2026, 3, 14, 12, 9, 0, 0, time.UTC)

// liveChat is one row of the list a test drives.
func liveChat(id int64, title string, unread int) LiveChat {
	return LiveChat{
		ID:      id,
		Title:   title,
		Preview: "the last message of " + title,
		At:      liveMoment,
		Unread:  unread,
	}
}

// liveModel is a model with a live list behind it: sized, loaded, and
// reading the clock of the fixture.
func liveModel(t *testing.T, live *fakeLiveSource, chats []Chat) Model {
	t.Helper()

	model, err := NewModelWithDependencies(context.Background(), Dependencies{
		Source:           &fakeChatSource{},
		MessageSubmitter: &recordingSubmitter{},
		LiveUpdates:      live,
		Theme:            defaultTestTheme(),
		ColorProfile:     theme.ProfileNoColor,
	})
	if err != nil {
		t.Fatalf("NewModelWithDependencies: %v", err)
	}

	model = withClock(model, liveMoment, time.UTC)
	model, _ = updateModel(t, model, tea.WindowSizeMsg{Width: 100, Height: 24})
	model, _ = updateModel(t, model, chatsLoadedMsg{chats: chats})

	return model
}

// liveChatList is ten chats in the order Telegram keeps them in: the newest
// message of a chat is the top of the list.
func liveChatList(count int) []Chat {
	chats := make([]Chat, 0, count)
	for id := int64(1); id <= int64(count); id++ {
		chats = append(chats, Chat{
			ID:    id,
			Title: liveChatName(id),
			At:    liveMoment.Add(-time.Duration(id) * time.Minute),
		})
	}

	return chats
}

// liveChatName is the name of the n-th chat of the fixture.
func liveChatName(id int64) string {
	return "chat " + string(rune('a'+id-1))
}

// liveListWith puts the ten chats of the fixture on a live list in the
// order they had, with the given chat first.
//
// It is what a message in the middle of a list looks like to the store: the
// chat that received it is at the top and everything else kept its place.
func liveListWith(chats []Chat, first int64) []LiveChat {
	rows := make([]LiveChat, 0, len(chats))
	rows = append(rows, liveChat(first, liveChatName(first), 1))
	for _, chat := range chats {
		if chat.ID == first {
			continue
		}
		rows = append(rows, liveChat(chat.ID, chat.Title, 0))
	}

	return rows
}

// drawLive is what the interface does when the live state has changed.
func drawLive(t *testing.T, model Model) Model {
	t.Helper()

	updated, _ := model.Update(liveChangedMsg{})

	return updated.(Model)
}

// afterRedrawInterval returns the model reading a moment one redraw
// interval later.
//
// A test that delivers one change after another has to let the interval
// pass between them, or the second change is folded into the redraw the
// first one is still owed — which is the behaviour of a burst, and not what
// the test is about.
func afterRedrawInterval(model Model) Model {
	return withClock(
		model, model.clock()().Add(liveRepaintInterval), time.UTC,
	)
}

// A new message in the middle of the list puts the chat at the top of it,
// the way Telegram does, and the cursor stays on the chat it was on.
func TestANewMessagePutsTheChatFirstAndTheCursorStays(t *testing.T) {
	live := newFakeLiveSource()
	chats := liveChatList(10)

	model := liveModel(t, live, chats)
	model.selectedChat = 4
	if model.selectedChatID() != 5 {
		t.Fatalf("the cursor is on chat %d, want 5", model.selectedChatID())
	}

	live.setChats(liveListWith(chats, 5))
	model = drawLive(t, model)

	if model.chats[0].ID != 5 {
		t.Fatalf(
			"chat %d is first after a message arrived, want 5: %v",
			model.chats[0].ID, liveTitlesOf(model),
		)
	}
	if model.selectedChatID() != 5 {
		t.Fatalf(
			"the cursor is on chat %d, want the chat it was on (5)",
			model.selectedChatID(),
		)
	}
}

// A chat that received a message is drawn with the row Telegram keeps for
// it: the preview, the moment of the message and the unread count.
func TestTheRowOfAChatThatReceivedAMessage(t *testing.T) {
	live := newFakeLiveSource()
	chats := liveChatList(3)

	model := liveModel(t, live, chats)

	live.setChats([]LiveChat{
		{
			ID:      2,
			Title:   "chat b",
			Preview: "the tag is pushed",
			At:      liveMoment.Add(time.Minute),
			Unread:  3,
		},
		liveChat(1, "chat a", 0),
		liveChat(3, "chat c", 0),
	})
	model = drawLive(t, model)

	row := model.chats[0]
	if row.Preview != "the tag is pushed" {
		t.Fatalf("preview = %q, want the message that arrived", row.Preview)
	}
	if !row.At.After(chats[1].At) {
		t.Fatalf("At = %v, want the moment of the message that arrived", row.At)
	}
	if row.Unread != 3 {
		t.Fatalf("unread = %d, want 3", row.Unread)
	}
}

// The counter of a chat that was read on another device falls with the same
// update, which is the whole of what updateChatReadInbox is for.
func TestTheUnreadCounterFallsWhenTheChatIsReadElsewhere(t *testing.T) {
	live := newFakeLiveSource()
	chats := []Chat{
		{ID: 1, Title: "chat a", Unread: 7, At: liveMoment},
		{ID: 2, Title: "chat b", At: liveMoment.Add(-time.Minute)},
	}

	model := liveModel(t, live, chats)

	live.setChats([]LiveChat{liveChat(1, "chat a", 0), liveChat(2, "chat b", 0)})
	model = drawLive(t, model)

	for _, chat := range model.chats {
		if chat.Unread != 0 {
			t.Fatalf("chat %d has %d unread, want 0", chat.ID, chat.Unread)
		}
	}
}

// Pinned chats are above the rest of the list whatever the moment of their
// last message is, and the list the live state hands over is drawn in that
// order.
func TestPinnedChatsStayAboveTheList(t *testing.T) {
	live := newFakeLiveSource()
	chats := []Chat{
		{ID: 1, Title: "newest", At: liveMoment},
		{ID: 2, Title: "pinned", At: liveMoment.Add(-time.Hour)},
		{ID: 3, Title: "oldest", At: liveMoment.Add(-2 * time.Hour)},
	}

	model := liveModel(t, live, chats)

	live.setChats([]LiveChat{
		liveChat(2, "pinned", 0),
		liveChat(1, "newest", 0),
		liveChat(3, "oldest", 0),
	})
	model = drawLive(t, model)

	if model.chats[0].ID != 2 {
		t.Fatalf(
			"chat %d is first, want the pinned chat 2: %v",
			model.chats[0].ID, liveTitlesOf(model),
		)
	}
}

// A chat that has left the main list is gone from the list, and the cursor
// is on a chat that is still there.
func TestAChatThatLeftTheMainListIsGoneAndTheCursorIsOnANeighbour(t *testing.T) {
	live := newFakeLiveSource()
	chats := []Chat{
		{ID: 1, Title: "one", At: liveMoment},
		{ID: 2, Title: "two", At: liveMoment.Add(-time.Minute)},
		{ID: 3, Title: "three", At: liveMoment.Add(-2 * time.Minute)},
	}

	model := liveModel(t, live, chats)
	model.selectedChat = 1

	live.setChats([]LiveChat{liveChat(1, "one", 0), liveChat(3, "three", 0)})
	model = drawLive(t, model)

	if len(model.chats) != 2 {
		t.Fatalf(
			"the list has %d chats, want 2: %v",
			len(model.chats), liveTitlesOf(model),
		)
	}
	for _, chat := range model.chats {
		if chat.ID == 2 {
			t.Fatal("a chat that left the main list is still on the screen")
		}
	}
	if model.selectedChatID() != 3 {
		t.Fatalf(
			"the cursor is on chat %d, want a chat that is still there (3)",
			model.selectedChatID(),
		)
	}
}

// The list moves under the cursor and the cursor follows the chat it was on:
// a chat that received a message goes to the top, and the chat the reader
// was reading is still the chat under the cursor.
func TestTheCursorFollowsItsChatWhenTheListMoves(t *testing.T) {
	live := newFakeLiveSource()
	chats := liveChatList(20)

	model := liveModel(t, live, chats)
	model.selectedChat = 9

	live.setChats(liveListWith(append(chats, Chat{ID: 21, Title: "brand new"}), 21))
	model = drawLive(t, model)

	if model.selectedChatID() != 10 {
		t.Fatalf("the cursor moved to chat %d, want 10", model.selectedChatID())
	}
	if model.chats[0].ID != 21 {
		t.Fatalf(
			"chat %d is first after the message, want 21: %v",
			model.chats[0].ID, liveTitlesOf(model),
		)
	}
}

// The window of the list is placed so that the chat that received a message
// is a row of the screen.
//
// It used to keep the row the cursor was on instead, which moved the window
// down by one row with every chat that arrived above the cursor and left the
// chat that received the message above the window: the frame the program
// wrote was the frame the terminal already had, and the list looked frozen
// until a key was pressed (the owner's report of 02.10).
func TestTheWindowOfTheListKeepsTheChatThatReceivedAMessageOnTheScreen(t *testing.T) {
	live := newFakeLiveSource()
	chats := liveChatList(20)

	model := liveModel(t, live, chats)
	model.selectedChat = 1

	live.setChats(liveListWith(append(chats, Chat{ID: 21, Title: "brand new"}), 21))
	model = drawLive(t, model)

	rows := model.chatListVisibleRows(LayoutFor(model.width, model.height))
	start, _ := model.chatListWindow(len(model.chats), model.selectedListRow(), rows)

	if start != 0 {
		t.Fatalf("the window of the list starts at row %d, want the top", start)
	}
	if model.chats[start].ID != 21 {
		t.Fatalf(
			"the top row of the window is chat %d, want the chat that received the message (21)",
			model.chats[start].ID,
		)
	}
	if model.selectedChatID() != 2 {
		t.Fatalf(
			"the cursor is on chat %d, want the chat it was on (2)",
			model.selectedChatID(),
		)
	}
}

// A reader below the top window keeps the chat under the cursor on the
// screen: the offset is given up and the window of §10.5 takes over, so a
// message that arrives at the top of a long list does not take the reader's
// chat off the screen.
func TestAReaderBelowTheTopWindowKeepsTheirChatOnTheScreen(t *testing.T) {
	live := newFakeLiveSource()
	chats := liveChatList(20)

	model := liveModel(t, live, chats)
	model.selectedChat = 9

	live.setChats(liveListWith(append(chats, Chat{ID: 21, Title: "brand new"}), 21))
	model = drawLive(t, model)

	rows := model.chatListVisibleRows(LayoutFor(model.width, model.height))
	start, end := model.chatListWindow(len(model.chats), model.selectedListRow(), rows)

	if model.selectedChat < start || model.selectedChat >= end {
		t.Fatalf(
			"the cursor is on row %d and the window holds rows %d..%d",
			model.selectedChat, start, end,
		)
	}
}

// A thousand changes inside one redraw interval are one redraw.
//
// The clock of the model is the clock of the fixture, so every change of
// the burst arrives at the same moment — which is what a channel with a
// hundred messages a second looks like from here.
func TestABurstOfChangesIsOneRedraw(t *testing.T) {
	live := newFakeLiveSource()
	model := liveModel(t, live, []Chat{{ID: 1, Title: "one", At: liveMoment}})

	for range 1000 {
		model = drawLive(t, model)
	}

	if reads := live.readsOf(); reads != 1 {
		t.Fatalf("the list was read %d times for 1000 changes, want 1", reads)
	}
	if !model.liveRepaintDue {
		t.Fatal("the redraw that the burst is owed is not on its way")
	}

	// The redraw that is due reads everything that came in the meantime.
	updated, _ := model.Update(liveRepaintDueMsg{})
	model = updated.(Model)

	if reads := live.readsOf(); reads != 2 {
		t.Fatalf(
			"the list was read %d times, want 2 (the change and the redraw)",
			reads,
		)
	}
	if model.liveRepaintDue {
		t.Fatal("a redraw is still on its way after it has been drawn")
	}
}

// A change that arrives on its own is drawn at once: a list that waits a
// tenth of a second for a message that arrived alone is late for no reason.
func TestASingleChangeIsDrawnAtOnce(t *testing.T) {
	live := newFakeLiveSource()
	model := liveModel(t, live, []Chat{{ID: 1, Title: "one", At: liveMoment}})

	live.setChats([]LiveChat{liveChat(1, "one renamed", 0)})
	model = drawLive(t, model)

	if reads := live.readsOf(); reads != 1 {
		t.Fatalf("the list was read %d times, want 1", reads)
	}
	if model.chats[0].Title != "one renamed" {
		t.Fatalf("the row says %q, want the new name", model.chats[0].Title)
	}
	if model.liveRepaintDue {
		t.Fatal("a single change is waiting for a redraw of its own")
	}
}

// The subscription is one wait at a time, armed from the start: a list
// that only listened after a chat was opened would be quiet until somebody
// opened something.
func TestTheSubscriptionIsArmedOnceAndReArmedAfterAChange(t *testing.T) {
	live := newFakeLiveSource()
	model := liveModel(t, live, []Chat{{ID: 1, Title: "one", At: liveMoment}})

	if !model.liveWaitArmed {
		t.Fatal("no wait for the first change of the live state")
	}
	if again := model.armLiveWait(); again != nil {
		t.Fatal("a second wait was armed while one was on its way")
	}

	// Init is where the armed wait is started, and a model without a live
	// state has nothing to wait for.
	if cmd := model.Init(); cmd == nil {
		t.Fatal("Init started nothing")
	}
	if quiet := liveModel(t, nil, nil).Init(); quiet != nil {
		t.Fatal("a program with no live state waits for changes")
	}

	// A change arms the next wait, so the subscription is a loop and not a
	// single question: a list that listened once would go quiet at the
	// first message.
	live.notify()
	updated, cmd := model.Update(liveChangedMsg{})
	model = updated.(Model)

	if !model.liveWaitArmed {
		t.Fatal("no wait after a change")
	}

	delivered := false
	for _, msg := range runLiveCommand(cmd) {
		if _, isChange := msg.(liveChangedMsg); isChange {
			delivered = true
		}
	}
	if !delivered {
		t.Fatal("the change asked for no further wait")
	}
}

// runLiveCommand runs a command and every command of a batch of it, and
// returns the messages they delivered.
//
// A command of the live loop either blocks on the change signal of a source
// that has one waiting or paints the screen, so running them here is what
// the Bubble Tea loop does with them.
func runLiveCommand(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}

	msg := cmd()
	batch, isBatch := msg.(tea.BatchMsg)
	if !isBatch {
		return []tea.Msg{msg}
	}

	var msgs []tea.Msg
	for _, inner := range batch {
		msgs = append(msgs, runLiveCommand(inner)...)
	}

	return msgs
}

// An incoming message is at the end of the conversation of a reader who is
// watching the end of it.
func TestAnIncomingMessageIsAtTheEndOfTheConversation(t *testing.T) {
	live := newFakeLiveSource()
	model := openLiveChat(t, live)

	live.setEvents(1, LiveMessageEvents{
		Cursor: 1,
		Events: []LiveMessageEvent{{
			Kind: LiveMessageAdded,
			Message: Message{
				ID:   20,
				Text: "the tag is pushed",
				At:   liveMoment.Add(time.Minute),
			},
		}},
	})
	model = drawLive(t, model)

	messages := model.selected().Messages
	last := messages[len(messages)-1]
	if last.ID != 20 || last.Text != "the tag is pushed" {
		t.Fatalf("the last message is %+v, want the message that arrived", last)
	}
	if model.newBelow != 0 {
		t.Fatalf(
			"newBelow = %d, want 0 for a reader at the newest message",
			model.newBelow,
		)
	}
	if !model.timelineFollowsNewest() {
		t.Fatal("the window did not follow a message that arrived below it")
	}
}

// The same message seen twice is one row: the answer to a send and the
// update TDLib sends about it are one message under two identifiers.
func TestTheSameMessageTwiceIsOneRow(t *testing.T) {
	live := newFakeLiveSource()
	model := openLiveChat(t, live)

	live.setEvents(1, LiveMessageEvents{
		Cursor: 1,
		Events: []LiveMessageEvent{{
			Kind:    LiveMessageAdded,
			Message: Message{ID: 20, Text: "first", At: liveMoment.Add(time.Minute)},
		}},
	})
	model = drawLive(t, model)

	model = afterRedrawInterval(model)
	live.setEvents(1, LiveMessageEvents{
		Cursor: 2,
		Events: []LiveMessageEvent{{
			Kind:    LiveMessageAdded,
			Message: Message{ID: 20, Text: "second", At: liveMoment.Add(time.Minute)},
		}},
	})
	model = drawLive(t, model)

	messages := model.selected().Messages
	count := 0
	for _, message := range messages {
		if message.ID == 20 {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("the conversation holds %d rows of message 20, want 1", count)
	}
	if model.newBelow != 0 {
		t.Fatalf(
			"newBelow = %d, want 0: the second copy is not a new message",
			model.newBelow,
		)
	}
}

// A sent message keeps its row when Telegram gives it its final
// identifier, and a message that is deleted goes away from the
// conversation.
func TestAReplacedMessageKeepsItsRowAndADeletedOneGoes(t *testing.T) {
	live := newFakeLiveSource()
	model := openLiveChat(t, live)

	live.setEvents(1, LiveMessageEvents{
		Cursor: 1,
		Events: []LiveMessageEvent{{
			Kind:    LiveMessageAdded,
			Message: Message{ID: 70, Outgoing: true, Text: "shipped", At: liveMoment},
		}},
	})
	model = drawLive(t, model)
	messages := model.selected().Messages
	position := -1
	for index, message := range messages {
		if message.ID == 70 {
			position = index
		}
	}
	if position < 0 {
		t.Fatalf("the message that was sent is not in the conversation: %v", messages)
	}

	model = afterRedrawInterval(model)
	live.setEvents(1, LiveMessageEvents{
		Cursor: 2,
		Events: []LiveMessageEvent{{
			Kind:    LiveMessageReplaced,
			OldID:   70,
			Message: Message{ID: 71, Outgoing: true, Text: "shipped", At: liveMoment},
		}},
	})
	model = drawLive(t, model)

	messages = model.selected().Messages
	if messages[position].ID != 71 {
		t.Fatalf(
			"row %d is message %d, want the replaced one (71)",
			position, messages[position].ID,
		)
	}

	model = afterRedrawInterval(model)
	live.setEvents(1, LiveMessageEvents{
		Cursor: 3,
		Events: []LiveMessageEvent{{Kind: LiveMessageDeleted, IDs: []int64{71}}},
	})
	model = drawLive(t, model)

	for _, message := range model.selected().Messages {
		if message.ID == 71 {
			t.Fatal("a deleted message is still in the conversation")
		}
	}
}

// A reader who has scrolled up to read something older is left where they
// are, and the line at the bottom of the feed says what they are missing.
// `G` takes them there and ends the line.
func TestAMessageBelowAReaderWhoHasScrolledUp(t *testing.T) {
	live := newFakeLiveSource()
	model := openLiveChat(t, live)

	// The reader walks up into the history, past everything the feed can
	// show, so that the newest message is below the window rather than in
	// it. Esc is how the keys leave the composer for the messages (§8.5).
	model, _ = updateModel(t, model, press(tea.KeyEsc))
	for range 24 {
		model, _ = updateModel(t, model, pressRunes("k"))
	}
	if model.timelineFollowsNewest() {
		t.Fatal("the reader did not scroll up")
	}
	top := model.timelineTop

	live.setEvents(1, LiveMessageEvents{
		Cursor: 1,
		Events: []LiveMessageEvent{{
			Kind: LiveMessageAdded,
			Message: Message{
				ID:   21,
				Text: "while you were reading",
				At:   liveMoment.Add(time.Minute),
			},
		}},
	})
	model = drawLive(t, model)

	if model.timelineTop != top {
		t.Fatalf(
			"the window moved from %d to %d for a message below it",
			top, model.timelineTop,
		)
	}
	if model.newBelow != 1 {
		t.Fatalf("newBelow = %d, want 1", model.newBelow)
	}
	if screen := plain(model.View()); !strings.Contains(screen, "1 new message") {
		t.Fatalf("the screen does not say what arrived:\n%s", screen)
	}

	// `G` is the key that takes a reader to the newest message, and it
	// ends the line with it.
	model, _ = updateModel(t, model, pressRunes("G"))

	if model.newBelow != 0 {
		t.Fatalf("newBelow = %d after G, want 0", model.newBelow)
	}
	if screen := plain(model.View()); strings.Contains(screen, "new message") {
		t.Fatalf("the line is still on the screen after G:\n%s", screen)
	}
	if !model.timelineFollowsNewest() {
		t.Fatal("G did not take the reader to the newest message")
	}
}

// Walking down to the newest message ends the line as well: there is
// nothing left below the window to be told about.
func TestWalkingDownToTheNewestMessageEndsTheLine(t *testing.T) {
	live := newFakeLiveSource()
	model := openLiveChat(t, live)

	model, _ = updateModel(t, model, press(tea.KeyEsc))
	for range 24 {
		model, _ = updateModel(t, model, pressRunes("k"))
	}
	live.setEvents(1, LiveMessageEvents{
		Cursor: 1,
		Events: []LiveMessageEvent{{
			Kind:    LiveMessageAdded,
			Message: Message{ID: 21, Text: "below you", At: liveMoment.Add(time.Minute)},
		}},
	})
	model = drawLive(t, model)
	if model.newBelow != 1 {
		t.Fatalf("newBelow = %d, want 1", model.newBelow)
	}

	for range 32 {
		model, _ = updateModel(t, model, press(tea.KeyDown))
	}
	if model.newBelow != 0 {
		t.Fatalf("newBelow = %d after walking to the newest, want 0", model.newBelow)
	}
	if !model.timelineFollowsNewest() {
		t.Fatal("walking down did not reach the newest message")
	}
}

// A window of events that has moved past what the interface holds costs a
// page of the conversation and nothing else: the messages on the screen
// stay, and the page that comes back is merged into them.
func TestAResyncReadsTheConversationAgain(t *testing.T) {
	live := newFakeLiveSource()
	model := openLiveChat(t, live)

	live.setEvents(1, LiveMessageEvents{Resync: true, Cursor: 40})
	updated, cmd := model.Update(liveChangedMsg{})
	model = updated.(Model)

	if cmd == nil {
		t.Fatal("a resync asked for nothing")
	}
	if !model.historyRefresh {
		t.Fatal("the page that comes back would replace the messages on the screen")
	}
	if len(model.selected().Messages) == 0 {
		t.Fatal("the conversation lost its messages on a resync")
	}
}

// The events of a chat that is not open are not read: the store keeps them
// until the conversation is on the screen.
func TestTheEventsOfAChatThatIsNotOpenAreLeftAlone(t *testing.T) {
	live := newFakeLiveSource()
	model := liveModel(t, live, liveChatList(3))

	live.setEvents(2, LiveMessageEvents{
		Cursor: 1,
		Events: []LiveMessageEvent{{
			Kind:    LiveMessageAdded,
			Message: Message{ID: 23, Text: "into a chat nobody is reading", At: liveMoment},
		}},
	})
	model = drawLive(t, model)

	for _, chat := range model.chats {
		for _, message := range chat.Messages {
			if message.ID == 23 {
				t.Fatal("an event of a chat that is not open was put in it")
			}
		}
	}
}

// The text of a live message is cleaned the way the text of a page is: a
// message body is the one text anybody else in a chat can put on the
// screen, and a terminal does what the control characters say.
func TestTheTextOfALiveMessageIsCleaned(t *testing.T) {
	live := newFakeLiveSource()
	model := openLiveChat(t, live)

	live.setEvents(1, LiveMessageEvents{
		Cursor: 1,
		Events: []LiveMessageEvent{{
			Kind: LiveMessageAdded,
			Message: Message{
				ID:   22,
				Text: "before\x1b[2Jafter",
				At:   liveMoment.Add(time.Minute),
			},
		}},
	})
	model = drawLive(t, model)

	messages := model.selected().Messages
	last := messages[len(messages)-1]
	if strings.Contains(last.Text, "\x1b") {
		t.Fatalf("the message kept an escape sequence: %q", last.Text)
	}
	if last.Text != "beforeafter" {
		t.Fatalf("the message says %q, want the escape sequence gone", last.Text)
	}
}

// The preview of a chat that received a message is cleaned the same way, so
// a row of the list is no less safe than a row of the feed.
func TestThePreviewOfALiveChatIsCleaned(t *testing.T) {
	live := newFakeLiveSource()
	model := liveModel(t, live, []Chat{{ID: 1, Title: "one", At: liveMoment}})

	live.setChats([]LiveChat{{
		ID:      1,
		Title:   "one",
		Preview: "hello\x1b[31mred",
		At:      liveMoment,
	}})
	model = drawLive(t, model)

	if strings.Contains(model.chats[0].Preview, "\x1b") {
		t.Fatalf("the preview kept an escape sequence: %q", model.chats[0].Preview)
	}
	if model.chats[0].Preview != "hellored" {
		t.Fatalf("preview = %q, want the sequence gone", model.chats[0].Preview)
	}
}

// The open conversation keeps its messages when the list is read again: the
// feed is drawn from that row, and a live update that emptied it would blank
// a conversation nobody asked to close.
func TestTheOpenConversationKeepsItsMessages(t *testing.T) {
	live := newFakeLiveSource()
	model := openLiveChat(t, live)

	live.setChats(liveListWith(liveChatList(3), 1))
	model = drawLive(t, model)

	if len(model.selected().Messages) != 12 {
		t.Fatalf(
			"the open conversation holds %d messages, want 12",
			len(model.selected().Messages),
		)
	}
}

// A list that cannot follow Telegram says so in the status line, and `R`
// still loads the list again — which is what the sentence names.
func TestAListThatIsNotLiveSaysSo(t *testing.T) {
	model := liveModel(t, nil, []Chat{{ID: 1, Title: "one", At: liveMoment}})

	if !model.liveListUnavailable() {
		t.Fatal("a model with a source and no live state says its list is live")
	}
	if line := statusLineOf(t, model); !strings.Contains(line, liveListUnavailableText) {
		t.Fatalf("the status line does not say it: %q", line)
	}

	if _, cmd := updateModel(t, model, pressRunes("R")); cmd == nil {
		t.Fatal("R loaded nothing")
	}
}

// A live state that says it cannot be read is said about in the same way,
// and the program stops waiting for changes it cannot receive.
func TestALiveStateThatCannotBeReadSaysSo(t *testing.T) {
	live := newFakeLiveSource()
	live.available = false

	model := liveModel(t, live, []Chat{{ID: 1, Title: "one", At: liveMoment}})

	if line := statusLineOf(t, model); !strings.Contains(line, liveListUnavailableText) {
		t.Fatalf("the status line does not say it: %q", line)
	}
}

// A mock program says nothing about a live list: there is no Telegram to
// follow, and a sentence about one would be about a program nobody runs.
func TestAMockProgramSaysNothingAboutTheLiveList(t *testing.T) {
	model := NewModel()

	if model.liveListUnavailable() {
		t.Fatal("a mock program says its chat list is not live")
	}
	if line := statusLineOf(t, model); strings.Contains(line, liveListUnavailableText) {
		t.Fatalf("the status line of a mock program says it: %q", line)
	}
}

// openLiveChat is a model with one chat open and a conversation longer than
// the feed in it, at the end of the conversation.
//
// It is longer than the feed on purpose: a window that ends with the newest
// message has nothing below it, and both the line at the bottom of the feed
// and the reader who scrolled up out of the newest message need a
// conversation with something above them.
func openLiveChat(t *testing.T, live *fakeLiveSource) Model {
	t.Helper()

	model := liveModel(t, live, []Chat{{
		ID:      1,
		Title:   "Release Room",
		Preview: "the tag is pushed",
		At:      liveMoment,
	}})

	page := make([]Message, 0, 12)
	for id := 12; id >= 1; id-- {
		page = append(page, Message{
			ID:   int64(id),
			Text: "message " + strconv.Itoa(id),
			At:   liveMoment.Add(-time.Duration(id) * time.Minute),
		})
	}

	model, _ = updateModel(t, model, press(tea.KeyEnter))
	model, _ = updateModel(t, model, historyLoadedMsg{
		chatID:    1,
		operation: model.historyOperation,
		page:      HistoryPage{Messages: page},
	})

	return model.scrollToNewest()
}

// liveTitlesOf returns the names of the rows on the screen, for a failure
// message that has to say which list it was.
func liveTitlesOf(model Model) []string {
	titles := make([]string, 0, len(model.chats))
	for _, chat := range model.chats {
		titles = append(titles, chat.Title)
	}

	return titles
}

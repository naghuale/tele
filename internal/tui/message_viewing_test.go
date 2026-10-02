package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"telecli/internal/tui/theme"
)

// What is on the screen of an open chat has been read, so the counter of
// the row falls and the other side of the conversation is told it was seen.

// recordingViewer records the windows it was told about.
type recordingViewer struct {
	chatID  int64
	windows [][]int64
	err     error
}

func (v *recordingViewer) ViewMessages(
	_ context.Context,
	chatID int64,
	messageIDs []int64,
) error {
	v.chatID = chatID
	v.windows = append(v.windows, append([]int64(nil), messageIDs...))

	return v.err
}

// last returns the window of the last read, which is the one on the screen.
func (v *recordingViewer) last() []int64 {
	if len(v.windows) == 0 {
		return nil
	}

	return v.windows[len(v.windows)-1]
}

func (v *recordingViewer) count() int {
	return len(v.windows)
}

// readChat is one chat of the list with a long enough history for the
// window to be smaller than the conversation.
func readChat(unread int, messages int) Chat {
	return readChatOf(ChatKindPrivate, unread, messages)
}

// readChatOf is readChat of a given kind, because the kind is what decides
// the source TDLib is given.
func readChatOf(kind ChatKind, unread int, messages int) Chat {
	chat := Chat{ID: 7, Title: "Anna", Unread: unread, Kind: kind}
	for id := 1; id <= messages; id++ {
		chat.Messages = append(chat.Messages, Message{
			ID:     int64(id),
			Text:   fmt.Sprintf("line %02d", id),
			Author: "Anna",
			At:     m12.Add(time.Duration(id) * time.Minute),
		})
	}

	return chat
}

var m12 = time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)

// viewingModel is a model with a presence opener and a viewer, one chat
// loaded, and a source that can be asked for the list again.
func viewingModel(
	t *testing.T,
	viewer *recordingViewer,
	chats []Chat,
	width, height int,
) (Model, *fakeChatSource) {
	t.Helper()

	return viewingModelWithLog(t, viewer, &recordingWriter{}, chats, width, height)
}

// viewingModelWithLog is viewingModel with a diagnostics stream of its own,
// because what a read is said about in the log is part of what it does.
func viewingModelWithLog(
	t *testing.T,
	viewer *recordingViewer,
	diagnostics *recordingWriter,
	chats []Chat,
	width, height int,
) (Model, *fakeChatSource) {
	t.Helper()

	source := &fakeChatSource{}
	deps := Dependencies{
		Source:           source,
		MessageSubmitter: &recordingSubmitter{},
		PresenceOpener:   &recordingOpener{},
		LiveUpdates:      newFakeLiveSource(),
		Theme:            theme.DefaultTheme().ForProfile(theme.ProfileNoColor),
		ColorProfile:     theme.ProfileNoColor,
	}
	if diagnostics != nil {
		deps.Diagnostics = diagnostics
	}
	if viewer != nil {
		deps.MessageViewer = viewer
	}

	model, err := NewModelWithDependencies(context.Background(), deps)
	if err != nil {
		t.Fatalf("NewModelWithDependencies: %v", err)
	}

	model, _ = updateModel(t, model, tea.WindowSizeMsg{Width: width, Height: height})
	source.chats = chats
	model, _ = updateModel(t, model, chatsLoadedMsg{chats: chats})

	return model, source
}

// deliverOpen runs the commands a key returned and feeds the answer of the
// open back into the model, which is what the Bubble Tea loop does with
// every message it gets.
//
// Without that answer the model has been told a chat is open and does not
// know that TDLib agreed, and the read of its messages is held back until
// the answer arrives — so a test that does not deliver it would be testing
// a read that never happens.
func deliverOpen(t *testing.T, model Model, cmd tea.Cmd) Model {
	t.Helper()

	for _, msg := range collectMsgs(cmd) {
		if _, isOpen := msg.(chatOpenedMsg); !isOpen {
			continue
		}

		// What the answer produced is run as well: the read of the window
		// is a command of every message, and the answer of the open is the
		// message that lets it go.
		var afterOpen tea.Cmd
		model, afterOpen = updateModel(t, model, msg)
		runCommands(t, afterOpen)
	}

	return model
}

// openReadChat opens the chat and settles its first page, which is the
// moment the conversation is on the screen with its messages in it.
func openReadChat(t *testing.T, model Model) Model {
	t.Helper()

	model, cmd := updateModel(t, model, press(tea.KeyEnter))
	if model.screen != ScreenConversation {
		t.Fatalf("screen = %v, want the conversation", model.screen)
	}
	model = deliverOpen(t, model, cmd)

	chat := model.chats[model.selectedChat]
	var readCmd tea.Cmd
	model, readCmd = updateModel(t, model, historyLoadedMsg{
		chatID:    chat.ID,
		operation: model.historyOperation,
		page:      HistoryPage{Messages: chat.Messages, NextFrom: 1},
	})
	// The read of the window is a command of this message too, and it is
	// where the first viewMessages of a chat goes out.
	runCommands(t, readCmd)

	return model
}

// collectMsgs runs a command and everything it batches, and returns the
// messages. Running a batch yields its members rather than its result, so
// the messages of a read have to be picked out of them.
func collectMsgs(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}

	msg := cmd()
	batch, isBatch := msg.(tea.BatchMsg)
	if !isBatch {
		return []tea.Msg{msg}
	}

	var out []tea.Msg
	for _, member := range batch {
		out = append(out, collectMsgs(member)...)
	}

	return out
}

// The window the conversation is drawing is marked read, and the messages
// above it are not: a chat of forty messages on a screen of twenty-four
// rows is the case where the two can be told apart.
func TestTheVisibleWindowOfAnOpenChatIsMarkedRead(t *testing.T) {
	viewer := &recordingViewer{}
	model, _ := viewingModel(t, viewer, []Chat{readChat(3, 40)}, 100, 24)
	model = openReadChat(t, model)

	if viewer.count() == 0 {
		t.Fatal("nothing was marked read in an open chat")
	}
	if viewer.chatID != 7 {
		t.Fatalf("chat id = %d, want the chat that is open", viewer.chatID)
	}

	marked := viewer.last()
	if len(marked) == 0 {
		t.Fatal("the window was empty")
	}

	// A chat opens at its end (§8.3), so the newest message is on the
	// screen and is marked.
	if marked[len(marked)-1] != 40 {
		t.Fatalf(
			"the newest message is not in the window: marked = %v",
			marked,
		)
	}
	for index := 1; index < len(marked); index++ {
		if marked[index] != marked[index-1]+1 {
			t.Fatalf(
				"the window is not the run of messages on the screen: %v",
				marked,
			)
		}
	}
	if marked[0] == 1 {
		t.Fatalf("the whole chat was marked read: %v", marked)
	}

	// What was marked is on the screen, and what was not is not: the frame
	// is the other witness, so the two cannot agree by accident.
	view := plain(model.View())
	for _, id := range marked {
		if !strings.Contains(view, fmt.Sprintf("line %02d", id)) {
			t.Errorf("message %d was marked read and is not on the screen", id)
		}
	}
	if strings.Contains(view, "line 01") {
		t.Fatal("the oldest message is on the screen and was not marked")
	}
}

// The read waits for the answer of openChat. A read batched with the open
// is a read sent before TDLib has loaded the chat, and a broadcast chat is
// one TDLib loads with openChat: the read is refused, the counter stays at
// what it was, and nothing on the screen says why. That is the owner's
// channel of 89 unread, and the order of the two calls is what it was.
func TestAReadWaitsForTheAnswerOfTheOpen(t *testing.T) {
	viewer := &recordingViewer{}
	model, _ := viewingModel(t, viewer, []Chat{readChatOf(ChatKindChannel, 89, 40)}, 100, 24)

	model, cmd := updateModel(t, model, press(tea.KeyEnter))
	if model.screen != ScreenConversation {
		t.Fatalf("screen = %v, want the conversation", model.screen)
	}

	// Everything the key press returned runs: the conversation is on the
	// screen with its messages in it, and the open of the chat is on its way
	// to TDLib. Nothing of it may be a read.
	runCommands(t, cmd)
	if viewer.count() != 0 {
		t.Fatalf(
			"%d reads went out before TDLib answered that the chat is open",
			viewer.count(),
		)
	}

	// TDLib answers, and the window may be read now.
	model, afterOpen := updateModel(t, model, chatOpenedMsg{chatID: 7})
	runCommands(t, afterOpen)

	if viewer.count() != 1 {
		t.Fatalf("reads = %d, want the one the answer allowed", viewer.count())
	}
	if model.openedAck != 7 {
		t.Fatalf("openedAck = %d, want the chat TDLib answered for", model.openedAck)
	}
	if viewer.last()[len(viewer.last())-1] != 40 {
		t.Fatalf("the window read = %v, want the newest message in it", viewer.last())
	}
}

// An answer about another chat says nothing about the one on the screen, so
// it cannot let that one's messages be read.
func TestAnAnswerForAnotherChatDoesNotOpenThisOne(t *testing.T) {
	viewer := &recordingViewer{}
	model, _ := viewingModel(t, viewer, []Chat{readChat(89, 40)}, 100, 24)
	model = openReadChat(t, model)

	if viewer.count() == 0 {
		t.Fatal("nothing was marked read in an open chat")
	}

	// The window moves, so a read is due, and the answer that arrives is
	// about a chat the model is not looking at.
	model, _ = updateModel(t, model, press(tea.KeyEsc))
	model, _ = updateModel(t, model, press(tea.KeyPgUp))
	marked := viewer.count()

	model, _ = updateModel(t, model, chatOpenedMsg{chatID: 8})

	if model.openedAck != 7 {
		t.Fatalf("openedAck = %d, want it unchanged by an answer about 8", model.openedAck)
	}
	if viewer.count() != marked {
		t.Fatal("an answer about another chat let a read of this one go")
	}
}

// The window is the window: a reader who scrolls up gets the messages that
// come into view, and the ones that left the window are not marked again.
func TestScrollingUpMarksWhatComesIntoView(t *testing.T) {
	viewer := &recordingViewer{}
	model, _ := viewingModel(t, viewer, []Chat{readChat(0, 40)}, 100, 24)
	model = openReadChat(t, model)

	newest := viewer.last()
	if len(newest) == 0 {
		t.Fatal("the window was empty")
	}
	oldestMarked := newest[0]

	// The keys of the window are the timeline's, and Enter leaves them on
	// the composer (§8.3).
	model, _ = updateModel(t, model, press(tea.KeyEsc))
	if model.focus != FocusHistory {
		t.Fatalf("focus = %v, want the timeline", model.focus)
	}

	// A page of keys up: the anchor walks with the cursor, so the window is
	// a different one and the messages above it are marked.
	model, cmd := updateModel(t, model, press(tea.KeyPgUp))
	runCommands(t, cmd)

	scrolled := viewer.last()
	if len(scrolled) == 0 {
		t.Fatal("nothing was marked read after a page up")
	}
	if scrolled[0] >= oldestMarked {
		t.Fatalf(
			"a page up did not bring older messages into the window: %v then %v",
			newest, scrolled,
		)
	}
	for index := 1; index < len(scrolled); index++ {
		if scrolled[index] != scrolled[index-1]+1 {
			t.Fatalf(
				"the window is not the run of messages on the screen: %v",
				scrolled,
			)
		}
	}
	// The newest message has left the window, and it is no longer in what is
	// marked: a window is what is on the screen, not everything that has
	// been on it.
	if scrolled[len(scrolled)-1] == 40 {
		t.Fatalf("the window still holds a message that left the screen: %v", scrolled)
	}

	view := plain(model.View())
	for _, id := range scrolled {
		if !strings.Contains(view, fmt.Sprintf("line %02d", id)) {
			t.Errorf("message %d was marked read and is not on the screen", id)
		}
	}
}

// A window that has not moved is not marked again: the same window twice is
// a read that says nothing, and a round trip to TDLib for it.
func TestAnUnchangedWindowIsNotMarkedTwice(t *testing.T) {
	viewer := &recordingViewer{}
	model, _ := viewingModel(t, viewer, []Chat{readChat(0, 40)}, 100, 24)
	model = openReadChat(t, model)

	marked := viewer.count()

	// A key that moves nothing on the screen: a letter in the composer.
	_, cmd := updateModel(t, model, pressRunes("h"))
	runCommands(t, cmd)
	_, cmd = updateModel(t, model, pressRunes("i"))
	runCommands(t, cmd)

	if viewer.count() != marked {
		t.Fatalf(
			"an unchanged window was marked %d more times",
			viewer.count()-marked,
		)
	}
}

// The chat list is not a chat anybody is reading, so nothing on it is
// marked. A screen with two panes shows both, and the conversation is what
// is on the screen there.
func TestNothingIsMarkedReadOnTheChatList(t *testing.T) {
	viewer := &recordingViewer{}
	model, _ := viewingModel(t, viewer, []Chat{readChat(3, 40)}, 100, 24)

	_, cmd := updateModel(t, model, press(tea.KeyDown))
	runCommands(t, cmd)
	_, cmd = updateModel(t, model, press(tea.KeyDown))
	runCommands(t, cmd)

	if viewer.count() != 0 {
		t.Fatalf("marked %d windows on the chat list", viewer.count())
	}
}

// Leaving the conversation stops the marking: what is not on the screen is
// not read, and the next chat is a different chat with its own window.
func TestLeavingTheConversationStopsTheMarking(t *testing.T) {
	viewer := &recordingViewer{}
	// A single-pane screen: on a two-pane one the conversation stays drawn
	// beside the list, and leaving it is only a change of focus.
	model, _ := viewingModel(t, viewer, []Chat{readChat(0, 40)}, 60, 24)
	model = openReadChat(t, model)

	marked := viewer.count()
	if marked == 0 {
		t.Fatal("nothing was marked read in an open chat")
	}

	// Enter leaves the focus on the composer, so Esc goes to the timeline
	// first and only the second one leaves the conversation.
	model, cmd := updateModel(t, model, press(tea.KeyEsc))
	runCommands(t, cmd)
	model, cmd = updateModel(t, model, press(tea.KeyEsc))
	runCommands(t, cmd)

	if model.screen != ScreenChats {
		t.Fatalf("screen = %v, want the chat list", model.screen)
	}
	model, cmd = updateModel(t, model, press(tea.KeyDown))
	runCommands(t, cmd)

	if viewer.count() != marked {
		t.Fatalf("marked %d more windows after leaving", viewer.count()-marked)
	}
}

// Without a viewer nothing is marked and nothing breaks: a program built
// without Telegram has nothing to ask.
func TestWithoutAViewerNothingIsMarkedRead(t *testing.T) {
	model, _ := viewingModel(t, nil, []Chat{readChat(3, 40)}, 100, 24)

	model, cmd := updateModel(t, model, press(tea.KeyEnter))
	runCommands(t, cmd)
	if model.screen != ScreenConversation {
		t.Fatalf("screen = %v, want the conversation", model.screen)
	}
}

// The counter of a row is TDLib's number, and only TDLib has reset it: the
// list is read again after a read, and the row then shows what Telegram
// says. The conversation beside it keeps its messages, because a row of the
// list carries none.
func TestTheCounterOfAReadChatFalls(t *testing.T) {
	viewer := &recordingViewer{}
	chat := readChat(3, 40)
	model, source := viewingModel(t, viewer, []Chat{chat}, 100, 24)
	model = openReadChat(t, model)

	// TDLib took the read and answered with updateChatReadInbox, so the
	// list it answers with says zero for this chat.
	source.chats = []Chat{{ID: 7, Title: "Anna", Kind: ChatKindPrivate}}

	_, cmd := updateModel(t, model, messagesViewedMsg{chatID: viewer.chatID})
	msgs := collectMsgs(cmd)
	if len(msgs) == 0 {
		t.Fatal("the chat list was not read again after the chat was read")
	}

	model, _ = updateModel(t, model, chatsLoadedMsg{chats: source.chats})

	if model.chats[0].Unread != 0 {
		t.Fatalf("Unread = %d, want 0", model.chats[0].Unread)
	}
	if len(model.chats[0].Messages) == 0 {
		t.Fatal("the read emptied the conversation beside the list")
	}
	if len(model.visibleMessageIDs()) != len(viewer.last()) {
		t.Fatal("the conversation is not the one that was read")
	}
}

// A chat whose row is already at zero is not a list that needs reading
// again, and a read that did not happen is not a reason to read it.
func TestAReadChatAtZeroIsNotReadAgain(t *testing.T) {
	viewer := &recordingViewer{}
	model, _ := viewingModel(t, viewer, []Chat{readChat(0, 40)}, 100, 24)
	model = openReadChat(t, model)

	_, cmd := updateModel(t, model, messagesViewedMsg{chatID: viewer.chatID})
	if cmd != nil {
		t.Fatal("a chat with nothing to lose was read again")
	}
}

// A read that TDLib refused leaves the chat unread, which is the smaller
// mistake: a counter that stays is an inconvenience, and a read that was
// never recorded is a lie to the other side.
//
// The line in the log is the point of the test. A refusal is the one thing
// nothing on the screen explains — the counter simply stays where it was —
// so every read says what was asked and what TDLib answered, and the cause
// goes to the diagnostic stream and not to the screen (§11.3, §19).
func TestEveryReadSaysWhatItAskedAndWhatItGot(t *testing.T) {
	for _, testCase := range []struct {
		name string
		kind ChatKind
		err  error
	}{
		{name: "read", kind: ChatKindPrivate},
		{
			name: "refused",
			kind: ChatKindChannel,
			err:  errors.New("viewMessages: TDLib response: code=400 message=\"Chat not found\""),
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			log := &recordingWriter{}
			viewer := &recordingViewer{err: testCase.err}

			model, _ := viewingModelWithLog(t, viewer, log, []Chat{
				readChatOf(testCase.kind, 89, 40),
			}, 100, 24)
			model = openReadChat(t, model)

			if viewer.count() == 0 {
				t.Fatal("nothing was marked read in an open chat")
			}
			line := log.String()
			if !strings.Contains(line, "viewMessages chat=7") {
				t.Fatalf("diagnostics = %q, want the chat of the read", line)
			}
			if !strings.Contains(line, "kind="+testCase.kind.String()) {
				t.Fatalf("diagnostics = %q, want the kind of the chat", line)
			}
			if !strings.Contains(line, "ids=7") {
				t.Fatalf("diagnostics = %q, want how many identifiers went", line)
			}
			// TDLib reads up to the newest identifier it is given, so that
			// one is the number a stuck counter is read by.
			if !strings.Contains(line, "highest=40") {
				t.Fatalf("diagnostics = %q, want the highest identifier", line)
			}
			if strings.Contains(plain(model.View()), "Chat not found") {
				t.Fatal("the cause of a refused read is on the screen")
			}

			if testCase.err == nil {
				if !strings.Contains(line, "answer=ok") {
					t.Fatalf("diagnostics = %q, want the answer", line)
				}

				return
			}
			if !strings.Contains(line, "answer=error:") {
				t.Fatalf("diagnostics = %q, want a refused answer", line)
			}
			if !strings.Contains(line, "Chat not found") {
				t.Fatalf("diagnostics = %q, want the cause TDLib gave", line)
			}
		})
	}
}

// Without a diagnostics writer there is nowhere to put the line, and a read
// that cannot be written about is still a read.
func TestAReadWithoutDiagnosticsDoesNotStop(t *testing.T) {
	viewer := &recordingViewer{}
	model, _ := viewingModelWithLog(t, viewer, nil, []Chat{readChat(89, 40)}, 100, 24)
	openReadChat(t, model)

	if viewer.count() == 0 {
		t.Fatal("nothing was marked read in an open chat")
	}
}

// The rows of the list carry no messages, so a list that arrived without
// them would empty the conversation beside it: `R` reads the list again and
// the conversation stays where it was.
func TestReloadingTheListKeepsTheConversation(t *testing.T) {
	viewer := &recordingViewer{}
	model, _ := viewingModel(t, viewer, []Chat{readChat(3, 40)}, 100, 24)
	model = openReadChat(t, model)

	// The keys are on the chat list beside the conversation, and R reads the
	// list again (§18). The commands are not run: the answer of the list is
	// fed below by hand, and one of them is the ten-second wait of §18.
	model.focus = FocusChatList
	model, _ = updateModel(t, model, pressRunes("R"))

	// What the source answers with: rows, and no messages in them.
	model, _ = updateModel(t, model, chatsLoadedMsg{chats: []Chat{
		{ID: 7, Title: "Anna", Kind: ChatKindPrivate},
	}})

	if got := len(model.chats[0].Messages); got != 40 {
		t.Fatalf("the conversation has %d messages, want the 40 it had", got)
	}
	if strings.Contains(plain(model.View()), "Loading chats") {
		t.Fatal("the reload put a wait in place of the conversation")
	}
}

// The list is read again after every read, and a list whose order moved must
// not take the conversation with it: a chat that received a message while
// this one was being read is enough to move it. The chat under the cursor is
// remembered by its identifier.
func TestAReorderedListKeepsTheChatUnderTheCursor(t *testing.T) {
	viewer := &recordingViewer{}
	model, source := viewingModel(t, viewer, []Chat{
		readChat(89, 40),
		{ID: 8, Title: "Dev Team", Kind: ChatKindGroup},
	}, 100, 24)
	model = openReadChat(t, model)

	if model.selectedChatID() != 7 {
		t.Fatalf("selected chat = %d, want the open one", model.selectedChatID())
	}

	// The same two chats, the other one first: the list a source answers
	// with is ordered by Telegram and moves when a chat gets a message.
	source.chats = []Chat{
		{ID: 8, Title: "Dev Team", Kind: ChatKindGroup},
		{ID: 7, Title: "Anna", Unread: 89, Kind: ChatKindPrivate},
	}
	model, _ = updateModel(t, model, chatsLoadedMsg{chats: source.chats})

	if got := model.selectedChatID(); got != 7 {
		t.Fatalf("selected chat = %d, want the one that was open", got)
	}
	if len(model.chats[model.selectedChat].Messages) != 40 {
		t.Fatal("the conversation on the screen was lost")
	}
}

// The list is read with a limit, so the open chat can fall outside it without
// anything having removed it. The row it is drawn from stays.
func TestAnOpenChatOutsideTheListKeepsItsRow(t *testing.T) {
	viewer := &recordingViewer{}
	model, source := viewingModel(t, viewer, []Chat{readChat(89, 40)}, 100, 24)
	model = openReadChat(t, model)

	// A list that no longer answers with the open chat, which is what a
	// limit of fifty does to the fiftieth-first chat of an account.
	source.chats = []Chat{{ID: 8, Title: "Dev Team", Kind: ChatKindGroup}}
	model, _ = updateModel(t, model, chatsLoadedMsg{chats: source.chats})

	index, found := model.indexOfChat(7)
	if !found {
		t.Fatalf("the open chat is not in the list: %v", model.chats)
	}
	if len(model.chats[index].Messages) != 40 {
		t.Fatal("the row the conversation is drawn from lost its messages")
	}
}

// A row of the queue standing in the middle of the conversation is drawn
// and has no identifier in Telegram yet: it is on the screen, it is not
// marked, and the messages of the history around it are marked by their own
// identifiers rather than by where the row pushed them.
func TestAMessageOfTheQueueInsideTheWindowIsNotMarkedRead(t *testing.T) {
	viewer := &recordingViewer{}
	model, _ := viewingModel(t, viewer, []Chat{readChat(0, 40)}, 100, 24)
	model = openReadChat(t, model)

	marked := viewer.count()

	// The keys of the window are the timeline's, and Enter leaves them on
	// the composer (§8.3).
	model, _ = updateModel(t, model, press(tea.KeyEsc))

	// A row of the queue from 10:36:30, between two messages of the
	// window: it takes a row of the feed, and walking onto it moves the
	// entries of the history behind it by one.
	model.pending = []PendingMessage{{
		EntryID:   "queue-1",
		ChatID:    7,
		Text:      "queued line",
		CreatedAt: m12.Add(36*time.Minute + 30*time.Second),
	}}
	model, cmd := updateModel(t, model, press(tea.KeyDown))
	runCommands(t, cmd)

	if viewer.count() == marked {
		t.Fatal("a row of the queue did not move the window")
	}

	window := viewer.last()
	if len(window) == 0 {
		t.Fatal("the window was empty")
	}
	for index, id := range window {
		if id == 0 {
			t.Fatalf("the row of the queue was marked read: %v", window)
		}
		if id < 34 || id > 40 {
			t.Fatalf("message %d is not in the window: %v", id, window)
		}
		if index > 0 && id != window[index-1]+1 {
			t.Fatalf("the window is not the run of messages on the screen: %v", window)
		}
	}

	view := plain(model.View())
	if !strings.Contains(view, "queued line") {
		t.Fatal("the row of the queue is not on the screen")
	}
	for _, id := range window {
		if !strings.Contains(view, fmt.Sprintf("line %02d", id)) {
			t.Errorf("message %d was marked read and is not on the screen", id)
		}
	}
}

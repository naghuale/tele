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

// The preview of the chat under the cursor (chat_preview.go): what it is,
// what it is not, and the three tests that keep it from becoming something
// else.
//
// The owner's words (02.10): «где я выбираю чат, разговора нет, надо жать
// Enter». The pane beside the list is now a preview of the chat the cursor
// is on, filled after the cursor has been still for chatPreviewPause.

// previewModel is a model with the preview's three witnesses wired up: a
// source that counts history loads, a viewer that records what was marked
// read, and an opener that records which chats TDLib was told to open.
func previewModel(
	t *testing.T,
	viewer *recordingViewer,
	opener *recordingOpener,
) (Model, *fakeChatSource) {
	t.Helper()

	source := &fakeChatSource{}

	model, err := NewModelWithDependencies(context.Background(), Dependencies{
		Source:           source,
		MessageSubmitter: &recordingSubmitter{},
		MessageViewer:    viewer,
		PresenceOpener:   opener,
		Theme:            theme.DefaultTheme().ForProfile(theme.ProfileNoColor),
		ColorProfile:     theme.ProfileNoColor,
	})
	if err != nil {
		t.Fatalf("NewModelWithDependencies: %v", err)
	}

	model, _ = updateModel(t, model, tea.WindowSizeMsg{Width: 120, Height: 30})

	return model, source
}

// previewChats is the list the preview walks: four chats and nothing else,
// because rows of a chat list carry no messages (mergeLoadedChats) and the
// page under the pane is what a preview asks the source for.
func previewChats() []Chat {
	return []Chat{
		previewChat(1, "Alpha"),
		previewChat(2, "Beta"),
		previewChat(3, "Gamma"),
		previewChat(4, "Delta"),
	}
}

// previewChat is one row of that list.
func previewChat(id int64, title string) Chat {
	return Chat{ID: id, Title: title, Kind: ChatKindPrivate, Unread: 4}
}

// previewPage is the newest page of a chat: six messages, which is less
// than one screenful of a conversation.
func previewPage(id int64, title string) HistoryPage {
	return previewPageFrom(id, title, 1, 6)
}

// previewPageFrom is a page of the messages first through last of a range of
// identifiers, as TDLib answers it: newest first.
func previewPageFrom(id int64, title string, first, last int) HistoryPage {
	// HasMore says there is something above the oldest message of the page,
	// which is what the fill asks about and what an empty answer above the
	// boundary then contradicts.
	page := HistoryPage{NextFrom: int64(first), HasMore: true}
	for message := last; message >= first; message-- {
		page.Messages = append(page.Messages, Message{
			ID:     id*100 + int64(message),
			Text:   fmt.Sprintf("%s line %02d", title, message),
			Author: title,
			At:     m12.Add(time.Duration(message) * time.Minute),
		})
	}

	return page
}

// loadedModel is a model with the list of the preview on the screen and the
// chat list loaded, which is the state the owner was in: a list of chats and
// nothing in the pane beside them.
func loadedModel(t *testing.T) (Model, *fakeChatSource, *recordingViewer, *recordingOpener) {
	t.Helper()

	viewer := &recordingViewer{}
	opener := &recordingOpener{}
	model, source := previewModel(t, viewer, opener)

	chats := previewChats()
	source.chats = chats
	model, _ = updateModel(t, model, chatsLoadedMsg{chats: chats})

	return model, source, viewer, opener
}

// previewDue runs the pause the way the program runs it, and gives the
// model what the command answered with.
//
// The pause is a timer of 200 ms and a test is not a place to wait for one:
// the timer is asked for the message it carries and nothing else, which is
// exactly what the Bubble Tea loop does with it a moment later.
func previewDue(t *testing.T, model Model, chatID int64, selection int) (Model, tea.Cmd) {
	t.Helper()

	return updateModel(t, model, chatPreviewDueMsg{
		chatID:    chatID,
		selection: selection,
		boundary:  0,
		newest:    true,
	})
}

// The pane beside the list shows the conversation of the chat under the
// cursor, and it says under it that this is a preview and what opens the
// chat for real.
func TestThePaneBesideTheListShowsTheChatUnderTheCursor(t *testing.T) {
	model, source, _, _ := loadedModel(t)
	source.history = previewPage(1, "Alpha")

	model, cmd := previewDue(t, model, 1, 0)

	// The page is on its way here, and the pane says so rather than
	// claiming a chat has nothing in it.
	if loading := plain(model.View()); !strings.Contains(loading, "Loading history...") {
		t.Fatalf("the pane does not say the page is on its way:\n%s", loading)
	}

	model = feedAnswers(t, model, cmd)

	view := plain(model.View())
	if !strings.Contains(view, "Alpha line 06") {
		t.Fatalf("the pane does not show the chat under the cursor:\n%s", view)
	}
	if !strings.Contains(view, previewHint) {
		t.Fatalf("the pane does not say it is a preview:\n%s", view)
	}

	// The preview has no composer: the keys are in the list, and a field
	// under a conversation with the focus elsewhere is a field that
	// swallows nothing.
	if strings.Contains(view, "Write a message") {
		t.Fatalf("the preview drew a composer:\n%s", view)
	}
}

// The pause is what makes a walk through the list one load and not one load
// per key: the timer is armed on every step, and only the pause that belongs
// to the chat the cursor stopped on is answered with a load.
func TestAFastWalkThroughTheListLoadsOnlyTheLastChat(t *testing.T) {
	model, source, _, _ := loadedModel(t)

	// Two steps down as fast as a person can press them, and one pause is
	// armed by the list that arrived: three pauses for three chats, and
	// the person is looking at the third one.
	for step := 1; step <= 2; step++ {
		var cmd tea.Cmd
		model, cmd = updateModel(t, model, press(tea.KeyDown))
		if cmd == nil {
			t.Fatalf("step %d armed no pause", step)
		}
	}

	// And then the loop gives the model every pause, in the order the
	// timers were armed. The first two are about chats the cursor has
	// already left.
	for _, due := range []chatPreviewDueMsg{
		{chatID: 1, selection: 0},
		{chatID: 2, selection: 1},
		{chatID: 3, selection: 2},
	} {
		var cmd tea.Cmd
		model, cmd = updateModel(t, model, due)
		runCommands(t, cmd)
	}

	// One chat and one chat only: every load is a page of the chat the
	// cursor stopped on, and nothing was asked for the two it walked past.
	if len(source.historyChats) == 0 {
		t.Fatal("the pause asked for nothing")
	}
	for _, chatID := range source.historyChats {
		if chatID != 3 {
			t.Fatalf(
				"history loads = %v, want pages of the chat the cursor "+
					"stopped on 3",
				source.historyChats,
			)
		}
	}
}

// The last selection wins: a page that arrives for a chat the cursor has
// walked past is dropped, so the pane beside the list is never a
// conversation of a chat that is not on the screen.
func TestAPreviewPageForAChatTheCursorLeftIsDropped(t *testing.T) {
	model, source, _, _ := loadedModel(t)
	source.history = previewPage(1, "Alpha")

	model, cmd := previewDue(t, model, 1, 0)
	late := collectMsgs(cmd)
	if len(late) == 0 {
		t.Fatal("the pause asked for nothing")
	}
	if _, isPreview := late[0].(chatPreviewLoadedMsg); !isPreview {
		t.Fatalf("the pause asked for %T, want the preview's own message", late[0])
	}

	// The cursor walks on to the second chat and opens it while the page of
	// the first is on its way.
	model, _ = updateModel(t, model, press(tea.KeyDown))

	var openCmd tea.Cmd
	model, openCmd = updateModel(t, model, press(tea.KeyEnter))
	model = deliverOpen(t, model, openCmd)

	model, readCmd := updateModel(t, model, historyLoadedMsg{
		chatID:    2,
		operation: model.historyOperation,
		page:      previewPage(2, "Beta"),
	})
	runCommands(t, readCmd)

	// And now the late page of the first chat arrives.
	for _, msg := range late {
		model, _ = updateModel(t, model, msg)
	}

	if got := model.selected().ID; got != 2 {
		t.Fatalf("the open chat is %d, want the one Enter opened 2", got)
	}
	for _, message := range model.selected().Messages {
		if strings.HasPrefix(message.Text, "Alpha") {
			t.Fatalf(
				"the page of a chat the cursor left landed on the screen: %q",
				message.Text,
			)
		}
	}
	if model.screen != ScreenConversation {
		t.Fatalf("screen = %v, want the open conversation", model.screen)
	}
}

// settlePreview gives the model every message the commands of a preview
// answer with, until there is nothing left to run.
//
// The fill is a chain rather than one answer: a page that arrives asks for
// the page above it, and a test that fed only the first would be testing a
// pane that was never covered. The rounds are bounded by the same bound the
// fill itself is, so a fill that never ends fails here instead of running
// for ever.
func settlePreview(t *testing.T, model Model, cmd tea.Cmd) Model {
	t.Helper()

	for round := 0; round <= maxHistoryFillRequests+1; round++ {
		if cmd == nil {
			return model
		}

		var next tea.Cmd
		for _, msg := range flattenBatch(t, cmd) {
			var one tea.Cmd
			model, one = updateModel(t, model, msg)
			next = tea.Batch(next, one)
		}
		cmd = next
	}

	t.Fatalf("the preview is still asking for pages after %d rounds", maxHistoryFillRequests+1)

	return model
}

// applyOne gives the model the first message a command answers with and
// hands back the command that message answered with, which is how a fill is
// watched one page at a time.
func applyOne(t *testing.T, model Model, cmd tea.Cmd) (Model, tea.Cmd) {
	t.Helper()

	for _, msg := range flattenBatch(t, cmd) {
		return updateModel(t, model, msg)
	}

	t.Fatal("the command answered with nothing")

	return model, nil
}

// The preview is not a read. What Telegram is told about is a chat that was
// opened on purpose, and a preview that marked messages read would move the
// read pointer of a chat nobody opened.
func TestThePreviewNeverMarksMessagesRead(t *testing.T) {
	model, source, viewer, opener := loadedModel(t)
	source.history = previewPage(1, "Alpha")

	model, cmd := previewDue(t, model, 1, 0)
	model = feedAnswers(t, model, cmd)

	// The page has been through the model, and the messages of the chat are
	// on the screen: give it one more key to be sure the window that is on
	// the screen is not one the model marks.
	model, cmd = updateModel(t, model, press(tea.KeyDown))
	model = feedAnswers(t, model, cmd)

	if viewer.count() != 0 {
		t.Fatalf("the preview marked messages read: %v", viewer.windows)
	}
	if len(opener.opened) != 0 {
		t.Fatalf("the preview opened a chat in Telegram: %v", opener.opened)
	}
	if model.screen != ScreenChats {
		t.Fatalf("screen = %v, want the list: a preview does not open", model.screen)
	}
	if model.focus != FocusChatList {
		t.Fatalf("focus = %v, want the list: a preview does not move the keys", model.focus)
	}
}

// The keys are what open the chat, and opening is what marks it read: the
// preview is a look at a chat, and both keys are the ways to stop looking
// and start reading.
func TestEnterAndTabOpenThePreviewedChatAndMarkItRead(t *testing.T) {
	cases := map[string]struct {
		key   tea.KeyMsg
		focus Focus
	}{
		"Enter": {key: press(tea.KeyEnter), focus: FocusComposer},
		"Tab":   {key: press(tea.KeyTab), focus: FocusHistory},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			viewer := &recordingViewer{}
			opener := &recordingOpener{}
			model, source := previewModel(t, viewer, opener)

			chats := previewChats()
			source.chats = chats
			source.history = previewPage(1, "Alpha")
			model, _ = updateModel(t, model, chatsLoadedMsg{chats: chats})

			model, _ = previewDue(t, model, 1, 0)

			var cmd tea.Cmd
			model, cmd = updateModel(t, model, testCase.key)
			model = deliverOpen(t, model, cmd)

			if model.screen != ScreenConversation {
				t.Fatalf("screen = %v, want the conversation", model.screen)
			}
			if model.focus != testCase.focus {
				t.Fatalf("focus = %v, want %v", model.focus, testCase.focus)
			}
			if len(opener.opened) != 1 || opener.opened[0] != 1 {
				t.Fatalf("TDLib was told to open %v, want the chat 1", opener.opened)
			}

			// The page of the open conversation, and then the read of the
			// window that is on the screen: the read is the answer to the
			// open, not to the preview.
			model, readCmd := updateModel(t, model, historyLoadedMsg{
				chatID:    1,
				operation: model.historyOperation,
				page:      previewPage(1, "Alpha"),
			})
			runCommands(t, readCmd)

			if viewer.count() == 0 {
				t.Fatal("the opened chat was not marked read")
			}
			if viewer.chatID != 1 {
				t.Fatalf("marked read chat %d, want 1", viewer.chatID)
			}
		})
	}
}

// Until the pause has run out, the pane is the empty state of §17: a
// preview of a chat nobody has looked at yet is not a conversation.
func TestTheEmptyPaneIsThereUntilThePreviewIsArmed(t *testing.T) {
	model, _, _, _ := loadedModel(t)

	view := plain(model.View())
	if !strings.Contains(view, emptyConversationTitle) {
		t.Fatalf("the pane is not the empty state before the preview:\n%s", view)
	}
	if strings.Contains(view, previewHint) {
		t.Fatalf("the pane is already a preview before the pause:\n%s", view)
	}

	model, _ = previewDue(t, model, 1, 0)

	if strings.Contains(plain(model.View()), emptyConversationTitle) {
		t.Fatalf("the pane is still the empty state after the pause:\n%s", plain(model.View()))
	}
}

// The preview is not drawn on a single-pane screen: there is no pane beside
// the list, and the conversation is what the way back in the header is for.
func TestThereIsNoPreviewOnASinglePaneScreen(t *testing.T) {
	viewer := &recordingViewer{}
	opener := &recordingOpener{}
	model, source := previewModel(t, viewer, opener)

	chats := previewChats()
	source.chats = chats
	model, _ = updateModel(t, model, tea.WindowSizeMsg{Width: 60, Height: 30})
	model, _ = updateModel(t, model, chatsLoadedMsg{chats: chats})

	model, _ = previewDue(t, model, 1, 0)

	if model.chatPreviewShown() {
		t.Fatal("a single-pane screen drew a preview")
	}
	if strings.Contains(plain(model.View()), previewHint) {
		t.Fatalf("a single-pane screen drew the foot of a preview:\n%s", plain(model.View()))
	}
	if len(source.historyChats) != 0 {
		t.Fatalf("a single-pane screen asked for pages: %v", source.historyChats)
	}
}

// A preview whose page could not be read says so in the pane and says
// nothing else: the cause goes to the log, where a TDLib message or a file
// name stays out of a terminal somebody is reading over their shoulder
// (§11.3, §19).
func TestAPreviewThatCouldNotBeReadSaysSoAndNothingMore(t *testing.T) {
	diagnostics := &recordingWriter{}
	viewer := &recordingViewer{}
	source := &fakeChatSource{historyErr: errors.New("chat is gone")}

	model, err := NewModelWithDependencies(context.Background(), Dependencies{
		Source:           source,
		MessageSubmitter: &recordingSubmitter{},
		MessageViewer:    viewer,
		PresenceOpener:   &recordingOpener{},
		Diagnostics:      diagnostics,
		Theme:            theme.DefaultTheme().ForProfile(theme.ProfileNoColor),
		ColorProfile:     theme.ProfileNoColor,
	})
	if err != nil {
		t.Fatalf("NewModelWithDependencies: %v", err)
	}
	model, _ = updateModel(t, model, tea.WindowSizeMsg{Width: 120, Height: 30})
	source.chats = previewChats()
	model, _ = updateModel(t, model, chatsLoadedMsg{chats: source.chats})

	model, cmd := previewDue(t, model, 1, 0)
	model = feedAnswers(t, model, cmd)

	view := plain(model.View())
	if !strings.Contains(view, "Failed to load history") {
		t.Fatalf("the pane does not say the preview failed:\n%s", view)
	}
	if strings.Contains(view, "chat is gone") {
		t.Fatalf("the cause is on the screen:\n%s", view)
	}
	if !strings.Contains(diagnostics.String(), "preview of chat 1") {
		t.Fatalf("the cause is not in the log: %q", diagnostics.String())
	}
	if viewer.count() != 0 {
		t.Fatal("a preview that failed marked something read")
	}
}

// A preview is read the way an opened chat is read on its first screen
// (#58): pages are asked for until the pane is covered, because a pane with
// a screen of messages at the bottom and darkness above them is a pane
// somebody who has not used the program reads as broken (the owner, 03.10).
func TestThePreviewReadsPagesUntilThePaneIsCovered(t *testing.T) {
	model, source, _, _ := loadedModel(t)
	source.pages = map[int64]HistoryPage{
		0: previewPage(1, "Alpha"),
		// Two more pages, each of them older than the last and asked from
		// the oldest message of the one before it: the newest page holds
		// messages 1 through 6, and a chat is kept oldest first, so its
		// oldest message is 101.
		101: previewPageFrom(1, "Alpha", 11, 20),
		111: previewPageFrom(1, "Alpha", 21, 30),
	}

	model, cmd := previewDue(t, model, 1, 0)
	model = settlePreview(t, model, cmd)

	if len(source.historyBoundaries()) < 2 {
		t.Fatalf(
			"the preview asked for one page: boundaries %v",
			source.historyBoundaries(),
		)
	}
	if source.historyBoundaries()[1] != 101 {
		t.Fatalf(
			"the page above the newest one was asked from %d, want the "+
				"oldest message of it 101",
			source.historyBoundaries()[1],
		)
	}
	if !model.feedIsFull() {
		t.Fatalf(
			"the pane is not covered: %d messages of a pane of %d rows",
			len(model.selected().Messages),
			model.feedRows(),
		)
	}
	if model.previewBeginning {
		t.Fatal("a chat with pages above it says it begins")
	}
}

// A chat with fewer messages than the pane has is not a hole above them: it
// has an end, and the pane says where it is.
func TestThePreviewOfAChatThatBeginsOnTheScreenSaysSo(t *testing.T) {
	model, source, _, _ := loadedModel(t)

	// Every page above the newest one comes back empty, which is the answer
	// "there is nothing older". The first of them was asked for the beginning
	// of the chat, and that is the one that puts the sentence on the screen.
	source.pages = map[int64]HistoryPage{
		0:   previewPage(1, "Alpha"),
		101: HistoryPage{},
	}

	model, cmd := previewDue(t, model, 1, 0)
	model = settlePreview(t, model, cmd)

	if !model.previewBeginning {
		t.Fatal("the pane does not know it has the whole chat")
	}

	view := plain(model.View())
	if !strings.Contains(view, previewBeginningText) {
		t.Fatalf("the pane does not say where the chat begins:\n%s", view)
	}
	if strings.Contains(view, "Failed to load history") {
		t.Fatalf("the pane says the history failed:\n%s", view)
	}
}

// A pane that has asked for a page and has not got it says so, and it says
// it where the emptiness is rather than in a corner of the feed.
func TestThePreviewSaysItIsLoadingWhereTheEmptinessWouldBe(t *testing.T) {
	model, source, _, _ := loadedModel(t)
	source.pages = map[int64]HistoryPage{
		0:   previewPage(1, "Alpha"),
		101: previewPageFrom(1, "Alpha", 11, 20),
	}

	model, cmd := previewDue(t, model, 1, 0)

	// The first page is on its way.
	if loading := plain(model.View()); !strings.Contains(loading, previewLoadingText) {
		t.Fatalf("the pane does not say the page is on its way:\n%s", loading)
	}

	// And so is a page above it: the six messages that came back do not cover
	// the pane, so the fill asks for the one above them.
	model, next := applyOne(t, model, cmd)
	if !model.previewMoreLoading {
		t.Fatal("the pane did not ask for the page above what it has")
	}
	if filled := plain(model.View()); !strings.Contains(filled, previewLoadingText) {
		t.Fatalf("the pane does not say the next page is on its way:\n%s", filled)
	}
	if next == nil {
		t.Fatal("the pane asked for nothing above what it has")
	}
}

// The fill is bounded the way the fill of an opened chat is: a source that
// always has another page cannot cost a fixed number of round trips per
// chat.
func TestTheFillOfThePreviewIsBounded(t *testing.T) {
	model, source, _, _ := loadedModel(t)
	source.history = previewPage(1, "Alpha")

	model, cmd := previewDue(t, model, 1, 0)
	settlePreview(t, model, cmd)

	if got := len(source.historyBoundaries()); got > maxHistoryFillRequests+1 {
		t.Fatalf(
			"the preview asked for %d pages, want no more than %d",
			got, maxHistoryFillRequests+1,
		)
	}
}

// The fill is still not a read and still not an opening: it asks Telegram
// for pages and tells it nothing.
func TestTheFillOfThePreviewStillMarksNothingRead(t *testing.T) {
	model, source, viewer, opener := loadedModel(t)
	source.pages = map[int64]HistoryPage{
		0:   previewPage(1, "Alpha"),
		101: previewPageFrom(1, "Alpha", 11, 20),
	}

	model, cmd := previewDue(t, model, 1, 0)
	model = settlePreview(t, model, cmd)

	if len(source.historyBoundaries()) < 2 {
		t.Fatal("the pane was never filled")
	}
	if viewer.count() != 0 {
		t.Fatalf("the filled pane marked messages read: %v", viewer.windows)
	}
	if len(opener.opened) != 0 {
		t.Fatalf("the filled pane opened a chat: %v", opener.opened)
	}
	if model.screen != ScreenChats || model.focus != FocusChatList {
		t.Fatalf(
			"screen = %v, focus = %v: a filled preview opens nothing and "+
				"takes no keys",
			model.screen, model.focus,
		)
	}
}

// The pane beside the list shows the chat under the cursor without a key
// being pressed at all. The owner looked at a program that had been started
// for a minute, with chats in the list and nothing in the pane, and had to
// press Enter to find out why (03.10): the pause was armed only where the
// cursor moved, and at startup the cursor does not move.
func TestThePreviewIsShownForTheChatUnderTheCursorWithoutAKey(t *testing.T) {
	viewer := &recordingViewer{}
	opener := &recordingOpener{}
	source := &fakeChatSource{}

	model, err := NewModelWithDependencies(context.Background(), Dependencies{
		Source:           source,
		MessageSubmitter: &recordingSubmitter{},
		MessageViewer:    viewer,
		PresenceOpener:   opener,
		Theme:            theme.DefaultTheme().ForProfile(theme.ProfileNoColor),
		ColorProfile:     theme.ProfileNoColor,
	})
	if err != nil {
		t.Fatalf("NewModelWithDependencies: %v", err)
	}

	// Nothing here is a key. The list arrives, and the pause it arms is what
	// the program does next, over and over, until the pane is filled.
	chats := previewChats()
	source.chats = chats
	source.history = previewPage(1, "Alpha")

	model, _ = updateModel(t, model, tea.WindowSizeMsg{Width: 120, Height: 30})
	model, cmd := updateModel(t, model, chatsLoadedMsg{chats: chats})
	if cmd == nil {
		t.Fatal("the list that arrived armed no pause")
	}

	model, _ = previewDue(t, model, 1, 0)
	model = settlePreview(t, model, cmd)

	if !model.chatPreviewShown() {
		t.Fatal("the pane is not showing the chat under the cursor")
	}
	if !strings.Contains(plain(model.View()), "Alpha line 06") {
		t.Fatalf("the pane is not showing the chat:\n%s", plain(model.View()))
	}
	if model.focus != FocusChatList || model.screen != ScreenChats {
		t.Fatalf(
			"screen = %v, focus = %v: a preview takes no keys and opens nothing",
			model.screen, model.focus,
		)
	}
	if viewer.count() != 0 || len(opener.opened) != 0 {
		t.Fatal("the preview marked something read or opened the chat")
	}
}

// A live list that moves the chat under the cursor arms the pause again, for
// the same reason the first list does: the pane has to say which chat it is
// showing, and it says so after the pause rather than at once.
func TestTheLiveListMovingTheCursorArmsThePauseAgain(t *testing.T) {
	live := newFakeLiveSource()
	source := &fakeChatSource{}

	model, err := NewModelWithDependencies(context.Background(), Dependencies{
		Source:           source,
		MessageSubmitter: &recordingSubmitter{},
		LiveUpdates:      live,
		Theme:            theme.DefaultTheme().ForProfile(theme.ProfileNoColor),
		ColorProfile:     theme.ProfileNoColor,
	})
	if err != nil {
		t.Fatalf("NewModelWithDependencies: %v", err)
	}

	chats := previewChats()
	source.chats = chats
	model, _ = updateModel(t, model, tea.WindowSizeMsg{Width: 120, Height: 30})
	model, _ = updateModel(t, model, chatsLoadedMsg{chats: chats})
	model, _ = previewDue(t, model, 1, 0)

	if !model.chatPreviewShown() {
		t.Fatal("the first chat was not previewed")
	}

	// The chat under the cursor is gone from the list — a chat that left the
	// main list while nobody was looking — and the pane is showing it.
	live.setChats([]LiveChat{{ID: 3, Title: "Gamma"}, {ID: 4, Title: "Delta"}})

	updated, cmd := model.Update(liveChangedMsg{})
	after := updated.(Model)

	if after.selectedChatID() != 3 {
		t.Fatalf(
			"the cursor is on chat %d, want the first of the new list 3",
			after.selectedChatID(),
		)
	}
	if cmd == nil {
		t.Fatal("the live list moved the cursor and armed no pause")
	}
	if after.chatPreviewShown() {
		t.Fatal("the pane is still showing the chat the cursor left")
	}
}

// The window is the first thing a terminal sends, so a list can arrive
// before the screen has a width — and a preview that was not possible on a
// screen with one pane has to be armed when the second pane appears, without
// a key: that is the other way the pane used to stay empty at startup.
func TestAScreenThatGainsItsSecondPanePreviewsWithoutAKey(t *testing.T) {
	model, _, _, _ := loadedModel(t)

	// One pane: the chat list is the whole screen, and there is nothing
	// beside it to show a conversation in.
	model, _ = updateModel(t, model, tea.WindowSizeMsg{Width: 60, Height: 30})

	updated, cmd := model.Update(tea.WindowSizeMsg{Width: 120, Height: 30})

	if cmd == nil {
		t.Fatal("the second pane appeared with no pause armed")
	}
	if updated.(Model).chatPreviewShown() {
		t.Fatal("the pane is showing a chat before its pause has run out")
	}
}

// A screen that keeps its second pane and its chat under the cursor is not
// asked to read the chat again: a resize is not a reason for a load.
func TestAResizeDoesNotReadThePreviewedChatAgain(t *testing.T) {
	model, source, _, _ := loadedModel(t)
	source.history = previewPage(1, "Alpha")

	model, cmd := previewDue(t, model, 1, 0)
	model = settlePreview(t, model, cmd)

	loads := len(source.historyBoundaries())

	updated, cmd := model.Update(tea.WindowSizeMsg{Width: 124, Height: 30})

	if !updated.(Model).chatPreviewShown() {
		t.Fatal("the pane forgot the chat it was showing")
	}
	if cmd != nil {
		t.Fatal("the resize armed a pause for a chat that did not change")
	}
	if got := len(source.historyBoundaries()); got != loads {
		t.Fatalf("the resize asked for %d pages, want none", got-loads)
	}
}

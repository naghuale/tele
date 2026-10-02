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

// previewPage is the newest page of a chat: six messages, which is more than
// one screenful of a short conversation.
func previewPage(id int64, title string) HistoryPage {
	page := HistoryPage{}
	for message := 1; message <= 6; message++ {
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

	return updateModel(t, model, chatPreviewDueMsg{chatID: chatID, selection: selection})
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

	if len(source.historyChats) != 1 {
		t.Fatalf(
			"history loads = %v, want one: the chat the cursor stopped on",
			source.historyChats,
		)
	}
	if source.historyChats[0] != 3 {
		t.Fatalf("history load for chat %d, want the last one 3", source.historyChats[0])
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

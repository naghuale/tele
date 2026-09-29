package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"telecli/internal/tui/theme"
)

// The window of the feed fills itself with older messages.
//
// The owner's screen, after a send and again after switching to another
// chat and back: the last two messages at the bottom of a 43-row window
// and empty rows above them, with the conversation coming back as soon as
// the arrow keys were used. The messages were in the model the whole time.
// The window was placed as if nothing older existed, so the feed held two
// messages' worth of rows and the rest of it was blank.
//
// The window is filled by walking back from the newest entry and adding
// what each entry really takes. That walk stops early when it is told the
// feed has no room, so the thing to check is what it is told.
func TestTheFeedWindowFillsItselfWithOlderMessages(t *testing.T) {
	t.Parallel()

	// 168x43 is the owner's window: a conversation that has to be full from
	// its top row to the newest message, with the composer under it.
	const (
		width  = 168
		height = 43
	)

	base := time.Date(2026, time.September, 30, 9, 0, 0, 0, time.UTC)
	messages := make([]Message, 0, 40)
	for i := range 40 {
		messages = append(messages, Message{
			ID:       int64(1000 + i),
			Outgoing: i%3 == 0,
			Text:     feedMessageText(i),
			Time:     base.Add(time.Duration(i) * time.Minute).Format("15:04"),
			At:       base.Add(time.Duration(i) * time.Minute),
			Author:   "Anna Example",
			AuthorID: 5,
		})
	}

	t.Run("after a send and its delivery", func(t *testing.T) {
		model := chatOfForty(t, width, height, messages, base)
		model = sendAndDeliver(t, model, "ого что то работает")

		assertTheFeedIsFull(t, model, "after the message was delivered")
	})

	t.Run("a send with an older uncertain row in the queue", func(t *testing.T) {
		model := chatOfForty(t, width, height, messages, base)

		// The record the owner's queue was still holding: written the day
		// before, so it belongs near the top of the conversation, and the
		// status under the newest message is the one it makes.
		model.pending = []PendingMessage{{
			EntryID: "stale", ChatID: 7, Text: "вчера: под вопросом",
			State: MessageDeliveryUncertain, Version: 1,
			CreatedAt: base.Add(-30 * time.Hour),
		}}
		model.pendingSnapshot = clonePendingMessages(model.pending)
		model = model.normalizeTimeline().scrollToNewest()

		model = sendAndDeliver(t, model, "ого что то работает")

		assertTheFeedIsFull(t, model, "after a send with an uncertain row")
	})

	t.Run("after switching away and back", func(t *testing.T) {
		model := chatOfForty(t, width, height, messages, base)

		// Away: the list is re-read with both chats in it, and the other
		// one is opened.
		model, _ = updateModel(t, model, chatsLoadedMsg{chats: []Chat{
			{ID: 7, Title: "Anna Example", Messages: messages},
			{ID: 8, Title: "Boris Example", Messages: []Message{{
				ID: 2000, Text: "другой чат", Time: "08:00",
				At: base.Add(-time.Hour), Author: "Boris Example",
			}}},
		}})
		opened, _ := model.selectChatAt(1)
		model = opened.(Model)
		if model.selectedChat != 1 {
			t.Fatalf("selectedChat = %d, want the other chat", model.selectedChat)
		}
		model = model.scrollToNewest()

		// And back.
		back, _ := model.selectChatAt(0)
		model = back.(Model)
		if model.selectedChat != 0 {
			t.Fatalf("selectedChat = %d, want the first chat back", model.selectedChat)
		}

		assertTheFeedIsFull(t, model, "after coming back to the chat")
	})
}

// chatOfForty is the model with one chat of forty messages open in a
// 168x43 window and the cursor on the newest.
func chatOfForty(
	t *testing.T,
	width, height int,
	messages []Message,
	base time.Time,
) Model {
	t.Helper()

	model, err := NewModelWithDependencies(nil, Dependencies{
		Source:           &fakeChatSource{},
		MessageSubmitter: &recordingSubmitter{},
		PendingMessages:  &pendingSource{},
		AccountKey:       "account-1",
		Theme:            theme.DefaultTheme().ForProfile(theme.ProfileNoColor),
		ColorProfile:     theme.ProfileNoColor,
	})
	if err != nil {
		t.Fatalf("NewModelWithDependencies: %v", err)
	}

	model, _ = updateModel(t, model, tea.WindowSizeMsg{
		Width: width, Height: height,
	})
	// The clock, so the row a send makes is dated 09:40 — after the last
	// message of the conversation — and not by whatever the machine thinks
	// the time is. A row that lands in the middle of the conversation by
	// accident is a different test.
	model.now = func() time.Time { return base.Add(40 * time.Minute) }
	model.historyState = loadStateLoaded
	model.chats = []Chat{{ID: 7, Title: "Anna Example", Messages: messages}}
	model.selectedChat = 0
	// The conversation, opened as a user opens it: the two moments the
	// owner saw happen are inside it, and a selection made in the list is
	// not the same code path.
	model.screen = ScreenConversation
	model.focus = FocusHistory
	model = model.normalizeTimeline().scrollToNewest()

	return model
}

// sendAndDeliver types a message, sends it, and lets the read that reports
// it as sent arrive: the two moments the owner saw the feed go.
//
// The row that is confirmed is the one the submission itself made, and the
// queue's own list no longer carries it — a record Telegram holds is not a
// record this program is waiting to send, so the read that confirms it is
// also the read that stops listing it.
func sendAndDeliver(t *testing.T, model Model, text string) Model {
	t.Helper()

	// The composer, as a user is in it when they send.
	model.focus = FocusComposer
	setDraft(&model, text)
	model, cmd := updateModel(t, model, press(tea.KeyEnter))
	if cmd == nil {
		t.Fatal("Enter on a draft produced no command: nothing was sent")
	}
	// The answer to the send, which is what puts the row in the queue.
	model, _ = updateModel(t, model, composerSubmissionMsg{
		chatID:     7,
		operation:  model.sendOperation,
		submission: Submission{ID: "entry-1", State: SubmissionQueued},
	})
	// The row the send just made, which is the one the read will confirm.
	// The queue may be holding others — an older uncertain record, say —
	// and they stay.
	var sent PendingMessage
	for _, row := range model.pending {
		if row.EntryID == "entry-1" {
			sent = row
		}
	}
	if sent.EntryID == "" {
		t.Fatalf(
			"the submission left no row of its own in the queue: %+v",
			model.pending,
		)
	}

	// The read this answer belongs to. A response that does not name the
	// active read is discarded, which is right, and is why a test has to
	// say which read it is answering.
	model.messageStatusGeneration = 0
	model.statusReadSeq = 0
	model.messageStatusAccountKey = "account-1"
	model.messageStatusChatID = 7

	model, _ = updateModel(t, model, messageStatusesLoadedMsg{
		accountKey: "account-1",
		chatID:     7,
		statuses: []MessageStatus{{
			EntryID: sent.EntryID, State: MessageDeliverySent, MessageID: 9001,
		}},
	})

	return model
}

// assertTheFeedIsFull is the owner's screen as a fact about the model: the
// newest message is on the last row of the feed, and there is no empty row
// above the conversation while there are older messages to fill the window
// with.
func assertTheFeedIsFull(t *testing.T, model Model, when string) {
	t.Helper()

	rows := rowsOfTheFeed(t, model)
	if len(rows) == 0 {
		t.Fatalf("%s: the feed takes no rows at all", when)
	}

	screen := strings.Join(rows, "\n")

	// The newest thing in the conversation, by the feed's own order rather
	// than by an index: a message the queue has just delivered is the
	// newest of all and it is not one of the fixtures.
	entries := model.feedEntries()
	if len(entries) == 0 {
		t.Fatalf("%s: the feed has no entries at all", when)
	}
	newest := entries[len(entries)-1]
	newestText := newest.message.Text
	if newest.isPending() {
		newestText = newest.pending.Text
	}
	if !strings.Contains(screen, newestText) {
		t.Fatalf(
			"%s: the newest thing in the conversation (%q) is not on the "+
				"screen, so the window is not on the end of it.\n%s",
			when, newestText, screen,
		)
	}

	firstContent := len(rows)
	for index, row := range rows {
		if strings.TrimSpace(row) != "" {
			firstContent = index
			break
		}
	}
	if firstContent > 0 {
		t.Fatalf(
			"%s: %d empty rows at the top of the feed and %d rows of "+
				"conversation below them. The window was placed as if "+
				"nothing older than the newest message existed.\n%s",
			when, firstContent, len(rows)-firstContent,
			strings.Join(rows, "\n"),
		)
	}

	// And the window is full of the newest ones: as many of the
	// conversation as its rows hold, and they are the newest.
	drawn, first := drawnOfTheConversation(screen, len(model.selected().Messages))
	if drawn < 3 {
		t.Fatalf(
			"%s: the feed draws %d of the %d messages of the chat in %d "+
				"rows. The window was not filled with the conversation.\n%s",
			when, drawn, len(model.selected().Messages), len(rows), screen,
		)
	}
	for index := first; index < first+drawn; index++ {
		if !strings.Contains(screen, feedMessageText(index)) {
			t.Fatalf(
				"%s: message %d of the chat is on the screen but the %d "+
					"the feed draws are not the newest ones — it starts at "+
					"%d, so the window is %d messages behind the end.\n%s",
				when, index, drawn, first,
				len(model.selected().Messages)-(first+drawn), screen,
			)
		}
	}
}

// drawnOfTheConversation counts how many of the chat's messages are on the
// screen and which of them is the first, by their own text. It reads the
// words rather than a marker, so it says the same thing under every
// profile and with no colour support.
func drawnOfTheConversation(screen string, total int) (drawn, first int) {
	first = -1
	for index := range total {
		if !strings.Contains(screen, feedMessageText(index)) {
			continue
		}
		if first < 0 {
			first = index
		}
		drawn++
	}

	return drawn, first
}

func feedMessageText(index int) string {
	return "сообщение " + string(rune('a'+index%26)) + string(rune('0'+index/26))
}

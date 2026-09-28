package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"telecli/internal/tui/theme"
)

// The bug this file is about, as a person sees it:
//
//	send a message   it appears at the bottom on `● Queued`
//	wait             `✓ Sent` never arrives
//	look elsewhere   the message is gone from the feed
//
// Two things caused it, and both are proved here. The delivery poll
// stopped at the first chat switch, so nothing after the first `Queued`
// was ever read again; and a message the queue had confirmed was dropped
// from the timeline rather than becoming a message of the conversation, so
// it disappeared the moment the user looked at another chat.

// chatSwitchingModel is a conversation with the delivery poll armed, so a
// test can watch what the loop does across the switch that used to end it.
func chatSwitchingModel(
	t *testing.T,
	width, height int,
) (Model, *pendingSource) {
	t.Helper()

	source := &pendingSource{}
	m, err := NewModelWithDependencies(context.Background(), Dependencies{
		Source:           &fakeChatSource{},
		MessageSubmitter: &recordingSubmitter{},
		PendingMessages:  source,
		AccountKey:       "account-1",
		Theme:            theme.DefaultTheme().ForProfile(theme.ProfileNoColor),
		ColorProfile:     theme.ProfileNoColor,
	})
	if err != nil {
		t.Fatalf("NewModelWithDependencies: %v", err)
	}

	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: width, Height: height})
	// The program starts: this is where the delivery poll is first armed.
	_ = m.Init()

	return m, source
}

// The delivery poll has to outlive a chat switch. It is armed once, at
// startup, and every chat the user opens starts a new generation — so a
// tick that belonged to the previous one used to end the loop for good,
// and a message queued afterwards was never read again. That is the whole
// of the `● Queued` that never became `✓ Sent`.
func TestTheDeliveryPollSurvivesTheChatSwitchThatQueuesTheMessage(t *testing.T) {
	t.Parallel()

	m, source := chatSwitchingModel(t, 100, 24)
	startGeneration := m.messageStatusGeneration

	// The user opens the chat they are going to write in.
	m, _ = updateModel(
		t, m, chatsLoadedMsg{chats: []Chat{{ID: 7, Title: "Избранное"}}},
	)
	m, _ = updateModel(t, m, press(tea.KeyEnter))
	if m.messageStatusGeneration == startGeneration {
		t.Fatal("opening a chat must start a new generation")
	}

	// The tick that was armed before the switch arrives, late. It is a
	// timer and it fires whatever generation it was armed for.
	afterSwitch, cmd := m.handleMessageStatusPollTick(
		messageStatusPollTickMsg{},
	)
	if cmd == nil {
		t.Fatal(
			"the delivery poll stopped at the chat switch: nothing will " +
				"ever be read again, so a message stays on Queued for as " +
				"long as the program runs",
		)
	}

	// And the read it starts is one for the chat that is open now, not
	// for the one the stale tick was armed for. The tick command itself
	// is not run: it sleeps for the poll interval, and the question here
	// is what it was armed for, not how long it waits.
	readsBefore := source.calls
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatal("the poll is not a batch of reads and the next tick")
	}
	if len(batch) < 2 {
		t.Fatalf(
			"the poll batch has %d commands, want a read and the next tick",
			len(batch),
		)
	}
	for _, read := range batch[:len(batch)-1] {
		_ = read()
	}
	if source.calls == readsBefore {
		t.Fatal(
			"the stale tick started no read of the chat that is open: the " +
				"delivery states of an open chat are never read again",
		)
	}
	if afterSwitch.messageStatusChatID != 7 {
		t.Fatalf(
			"the poll is for chat %d, want the one that is open",
			afterSwitch.messageStatusChatID,
		)
	}
}

// A message that Telegram has confirmed becomes a message of the
// conversation: it stops being drawn as one on its way out, and it is
// still there under its final identifier when the user comes back.
func TestAConfirmedMessageBecomesAMessageOfTheConversation(t *testing.T) {
	t.Parallel()

	m, _ := chatSwitchingModel(t, 100, 24)
	m, _ = updateModel(t, m, chatsLoadedMsg{chats: []Chat{
		{ID: 7, Title: "Избранное"},
		{ID: 8, Title: "Работа"},
	}})
	m, _ = updateModel(t, m, press(tea.KeyEnter))

	m.focus = FocusComposer
	m, _ = updateModel(t, m, pressRunes("проверяю сборку"))
	m, _ = updateModel(t, m, press(tea.KeyEnter))
	m, _ = updateModel(t, m, deliverSubmission(m, "entry-1"))

	// The queue still holds it, so it is drawn on its way out.
	if view := plain(m.View()); !strings.Contains(view, "Queued") {
		t.Fatalf("the queued message is not on the screen:\n%s", view)
	}

	// Telegram confirms the send, and the queue reports the final
	// identifier the history will come back with.
	m, _ = updateModel(t, m, messageStatusesLoadedMsg{
		generation: m.messageStatusGeneration,
		read:       m.statusReadSeq,
		accountKey: m.messageStatusAccountKey,
		chatID:     m.messageStatusChatID,
		statuses: []MessageStatus{{
			EntryID:    "entry-1",
			AccountKey: m.messageStatusAccountKey,
			ChatID:     7,
			State:      MessageDeliverySent,
			MessageID:  501,
		}},
		pending: []PendingMessage{},
	})

	// It is in the conversation now, once, under the final identifier.
	messages := m.selected().Messages
	if len(messages) != 1 || messages[0].ID != 501 {
		t.Fatalf(
			"messages = %#v, want the one confirmed message under id 501",
			messages,
		)
	}
	if messages[0].Text != "проверяю сборку" || !messages[0].Outgoing {
		t.Fatalf("message = %#v, want the text the user wrote", messages[0])
	}
	if len(m.pending) != 0 {
		t.Fatalf(
			"pending = %#v, want none: a message Telegram has is a message "+
				"of the conversation, not one on its way out", m.pending,
		)
	}

	// It is still in the feed after the user looks at another chat and
	// comes back, because it is a message of the conversation now.
	//
	// Leaving the conversation and walking the list onto the other chat
	// and back is the whole of what a user does when they look away.
	m, _ = updateModel(t, m, press(tea.KeyEsc))
	m, _ = updateModel(t, m, press(tea.KeyEsc))
	m, _ = updateModel(t, m, press(tea.KeyDown))
	if m.selected().ID != 8 {
		t.Fatalf("chat = %d, want the other one", m.selected().ID)
	}
	m, _ = updateModel(t, m, press(tea.KeyUp))
	if m.selected().ID != 7 {
		t.Fatalf("chat = %d, want the one the message was sent to", m.selected().ID)
	}
	if m.screen != ScreenConversation {
		t.Fatalf("screen = %v, want the conversation open again", m.screen)
	}

	view := plain(m.View())
	if !strings.Contains(view, "проверяю сборку") {
		t.Fatalf(
			"the message is not in the feed after the chat switch:\n%s", view,
		)
	}
	if count := strings.Count(view, "проверяю сборку"); count != 1 {
		t.Fatalf(
			"the message is drawn %d times after the chat switch, want 1:\n%s",
			count, view,
		)
	}
}

// A confirmation the history later brings back replaces the row that is
// already there rather than adding a second one. The two carry the same
// identifier, which is what makes them the same message.
func TestTheHistoryReplacesAMessageThatWasAlreadyDelivered(t *testing.T) {
	t.Parallel()

	m, _ := chatSwitchingModel(t, 100, 24)
	m, _ = updateModel(
		t, m, chatsLoadedMsg{chats: []Chat{{ID: 7, Title: "Избранное"}}},
	)
	m, _ = updateModel(t, m, press(tea.KeyEnter))
	m.focus = FocusComposer
	m, _ = updateModel(t, m, pressRunes("проверяю сборку"))
	m, _ = updateModel(t, m, press(tea.KeyEnter))
	m, _ = updateModel(t, m, deliverSubmission(m, "entry-1"))

	m, _ = updateModel(t, m, messageStatusesLoadedMsg{
		generation: m.messageStatusGeneration,
		read:       m.statusReadSeq,
		accountKey: m.messageStatusAccountKey,
		chatID:     m.messageStatusChatID,
		statuses: []MessageStatus{{
			EntryID:    "entry-1",
			AccountKey: m.messageStatusAccountKey,
			ChatID:     7,
			State:      MessageDeliverySent,
			MessageID:  501,
		}},
	})

	// The first history page, with the same message and a time Telegram
	// assigned rather than the one the queue guessed.
	m.historyOperation++
	m, _ = updateModel(t, m, historyLoadedMsg{
		chatID:        7,
		fromMessageID: 0,
		operation:     m.historyOperation,
		page: HistoryPage{Messages: []Message{{
			ID: 501, Outgoing: true, Text: "проверяю сборку", Time: "14:30",
		}}},
	})

	if got := m.selected().Messages; len(got) != 1 {
		t.Fatalf("messages = %#v, want the one the history brought", got)
	}
	if got := m.selected().Messages[0].Time; got != "14:30" {
		t.Fatalf("time = %q, want the one the history brought", got)
	}
}

// A message the queue has not confirmed stays where it is: it is still on
// its way out, it is still drawn as such, and a status with no final
// identifier yet never turns into a message of the conversation.
func TestAConfirmationWithNoIdentifierLeavesTheMessagePending(t *testing.T) {
	t.Parallel()

	m, _ := chatSwitchingModel(t, 100, 24)
	m, _ = updateModel(
		t, m, chatsLoadedMsg{chats: []Chat{{ID: 7, Title: "Избранное"}}},
	)
	m, _ = updateModel(t, m, press(tea.KeyEnter))
	m.focus = FocusComposer
	m, _ = updateModel(t, m, pressRunes("проверяю сборку"))
	m, _ = updateModel(t, m, press(tea.KeyEnter))
	m, _ = updateModel(t, m, deliverSubmission(m, "entry-1"))

	m, _ = updateModel(t, m, messageStatusesLoadedMsg{
		generation: m.messageStatusGeneration,
		read:       m.statusReadSeq,
		accountKey: m.messageStatusAccountKey,
		chatID:     m.messageStatusChatID,
		statuses: []MessageStatus{{
			EntryID:    "entry-1",
			AccountKey: m.messageStatusAccountKey,
			ChatID:     7,
			State:      MessageDeliverySent,
			// No identifier: the queue has the temporary one and the
			// history will not have that.
			MessageID: 0,
		}},
	})

	if len(m.selected().Messages) != 0 {
		t.Fatalf(
			"messages = %#v, want none: a row placed under a temporary "+
				"identifier could not be recognised when the history "+
				"arrives", m.selected().Messages,
		)
	}
	if len(m.pending) != 1 {
		t.Fatalf("pending = %#v, want the message to stay on the screen", m.pending)
	}
}

// A failed message stays on the screen with the reason under it. Nothing
// here turns a failure into a message of the conversation: Telegram
// refused it, so there is no message of the conversation to have.
func TestAFailedMessageStaysOnTheScreenWithItsState(t *testing.T) {
	t.Parallel()

	m, _ := chatSwitchingModel(t, 100, 24)
	m, _ = updateModel(
		t, m, chatsLoadedMsg{chats: []Chat{{ID: 7, Title: "Избранное"}}},
	)
	m, _ = updateModel(t, m, press(tea.KeyEnter))
	m.focus = FocusComposer
	m, _ = updateModel(t, m, pressRunes("не отправится"))
	m, _ = updateModel(t, m, press(tea.KeyEnter))
	m, _ = updateModel(t, m, deliverSubmission(m, "entry-1"))

	m, _ = updateModel(t, m, messageStatusesLoadedMsg{
		generation: m.messageStatusGeneration,
		read:       m.statusReadSeq,
		accountKey: m.messageStatusAccountKey,
		chatID:     m.messageStatusChatID,
		pending: []PendingMessage{{
			EntryID: "entry-1", ChatID: 7, Text: "не отправится",
			State: MessageDeliveryFailed,
		}},
		statuses: []MessageStatus{{
			EntryID:    "entry-1",
			AccountKey: m.messageStatusAccountKey,
			ChatID:     7,
			State:      MessageDeliveryFailed,
		}},
	})

	view := plain(m.View())
	if !strings.Contains(view, "не отправится") {
		t.Fatalf("the failed message left the screen:\n%s", view)
	}
	if !strings.Contains(view, "Failed") {
		t.Fatalf("the failure is not on the screen:\n%s", view)
	}
	if len(m.selected().Messages) != 0 {
		t.Fatalf(
			"messages = %#v, want none: Telegram refused this message",
			m.selected().Messages,
		)
	}
}

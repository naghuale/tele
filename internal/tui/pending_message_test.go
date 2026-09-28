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

// An outgoing message that is not in the history yet, and its delivery
// state under it (§4.4, §6).
//
// The text lives in exactly one place on the way out of the program: the
// view. It is not in the status list, not in an error, and not in what
// fmt prints when something goes wrong with a value on its way to a log.

// deliveredFor returns a model with one poll delivered, which is when the
// pending messages of a chat reach the screen.
func deliveredFor(t *testing.T, source *pendingSource) Model {
	t.Helper()

	m := conversationWithPending(t, theme.ProfileNoColor, 100, 24, source)
	m, _ = updateModel(t, m, deliveryRefresh(t, m, source))

	return m
}

// pendingModel is a conversation with a pending source and one draft
// queued, the way a message leaves the composer.
func pendingModel(
	t *testing.T,
	profile theme.Profile,
	width int,
	height int,
) (Model, *pendingSource) {
	t.Helper()

	source := &pendingSource{}

	m := conversationWithPending(t, profile, width, height, source)
	m.focus = FocusComposer
	m, _ = updateModel(t, m, pressRunes("проверяю сборку"))

	return m, source
}

// conversationWithPending returns a model with a pending source wired and
// a conversation open.
func conversationWithPending(
	t *testing.T,
	profile theme.Profile,
	width int,
	height int,
	source PendingMessageSource,
) Model {
	t.Helper()

	// The account key is set so that the delivery poll is a real one: a
	// model without it has nothing to read, and these tests are about what
	// the read produces.
	m, err := NewModelWithDependencies(context.Background(), Dependencies{
		Source:           &fakeChatSource{},
		MessageSubmitter: &recordingSubmitter{},
		PendingMessages:  source,
		AccountKey:       "account-1",
		Theme:            theme.DefaultTheme().ForProfile(profile),
		ColorProfile:     profile,
	})
	if err != nil {
		t.Fatalf("NewModelWithDependencies: %v", err)
	}

	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: width, Height: height})
	m, _ = updateModel(t, m, chatsLoadedMsg{chats: []Chat{{ID: 7, Title: "A"}}})
	m, _ = updateModel(t, m, press(tea.KeyEnter))

	if m.screen != ScreenConversation {
		t.Fatalf("screen = %v, want conversation", m.screen)
	}

	return m
}

// pendingSource answers with whatever a test puts in it.
type pendingSource struct {
	messages []PendingMessage
	err      error
	calls    int
}

func (s *pendingSource) ListPendingMessages(
	_ context.Context,
	_ string,
	chatID int64,
) ([]PendingMessage, error) {
	s.calls++

	if s.err != nil {
		return nil, s.err
	}

	out := make([]PendingMessage, 0, len(s.messages))
	for _, message := range s.messages {
		if message.ChatID == chatID {
			out = append(out, message)
		}
	}

	return out, nil
}

// Every state of §6 is drawn under the text of its message, with the
// symbol and the word of the state, in every profile: the word is what
// carries the meaning and the symbol is what makes it readable at a glance.
func TestEveryDeliveryStateIsDrawnUnderItsMessage(t *testing.T) {
	cases := map[string]struct {
		state    MessageDeliveryState
		want     string
		unwanted string
	}{
		"queued":   {state: MessageDeliveryQueued, want: "Queued"},
		"sending":  {state: MessageDeliverySending, want: "Sending"},
		"retrying": {state: MessageDeliveryRetrying, want: "Retrying"},
		"sent":     {state: MessageDeliverySent, want: "Sent"},
		"failed":   {state: MessageDeliveryFailed, want: "Failed", unwanted: "Retry"},
		"uncertain": {
			state:    MessageDeliveryUncertain,
			want:     "Delivery uncertain",
			unwanted: "Failed",
		},
		"canceled": {state: MessageDeliveryCanceled, want: "Canceled"},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			for _, profile := range []theme.Profile{
				theme.ProfileTrueColor,
				theme.ProfileNoColor,
			} {
				source := &pendingSource{messages: []PendingMessage{{
					EntryID: "entry-" + name,
					ChatID:  7,
					Text:    "текст сообщения",
					State:   testCase.state,
				}}}

				m := conversationWithPending(t, profile, 100, 24, source)
				m, _ = updateModel(t, m, deliveryRefresh(t, m, source))

				view := plain(m.View())
				if !strings.Contains(view, "текст сообщения") {
					t.Fatalf("%v: the text of the message is not on the screen:\n%s", profile, view)
				}
				if !strings.Contains(view, testCase.want) {
					t.Fatalf("%v: the state label %q is missing:\n%s", profile, testCase.want, view)
				}
				if got := stateSymbol(testCase.state); !strings.Contains(view, got) {
					t.Fatalf("%v: the symbol %q is missing:\n%s", profile, got, view)
				}
				if testCase.unwanted != "" &&
					strings.Contains(view, testCase.unwanted) {
					t.Fatalf(
						"%v: the screen says %q about a %s message:\n%s",
						profile,
						testCase.unwanted,
						name,
						view,
					)
				}
			}
		})
	}
}

// The state line sits under the text of its own message, not beside it and
// not somewhere else in the conversation.
func TestTheStateLineIsUnderTheTextOfItsMessage(t *testing.T) {
	source := &pendingSource{messages: []PendingMessage{{
		EntryID: "entry-1",
		ChatID:  7,
		Text:    "раз",
		State:   MessageDeliveryQueued,
	}}}

	m := conversationWithPending(t, theme.ProfileNoColor, 100, 24, source)
	m, _ = updateModel(t, m, deliveryRefresh(t, m, source))

	view := plain(m.View())
	textAt, ok := lineIndexWith(view, "раз")
	if !ok {
		t.Fatalf("the text is not on the screen:\n%s", view)
	}

	stateAt, ok := lineIndexWith(view, "Queued")
	if !ok {
		t.Fatalf("the state is not on the screen:\n%s", view)
	}
	if stateAt <= textAt {
		t.Fatalf("the state is above the text:\n%s", view)
	}
}

// Uncertain and failed are different things and must not look alike: one
// may already have been delivered, the other was not. The words are what
// tell them apart where there is no colour.
func TestUncertainAndFailedAreNotTheSameThing(t *testing.T) {
	uncertain := &pendingSource{messages: []PendingMessage{{
		EntryID: "u", ChatID: 7, Text: "a", State: MessageDeliveryUncertain,
	}}}
	failed := &pendingSource{messages: []PendingMessage{{
		EntryID: "f", ChatID: 7, Text: "a", State: MessageDeliveryFailed,
	}}}

	first := plain(deliveredFor(t, uncertain).View())
	second := plain(deliveredFor(t, failed).View())

	if !strings.Contains(first, "Delivery uncertain") {
		t.Fatalf("an uncertain message says nothing about the uncertainty:\n%s", first)
	}
	if !strings.Contains(first, "Message may already have been sent") {
		t.Fatalf("an uncertain message does not warn about the duplicate:\n%s", first)
	}
	if strings.Contains(first, "Failed") {
		t.Fatalf("an uncertain message looks like a failed one:\n%s", first)
	}
	if !strings.Contains(second, "Failed") {
		t.Fatalf("a failed message does not say it failed:\n%s", second)
	}
	if strings.Contains(second, "Delivery uncertain") {
		t.Fatalf("a failed message looks like an uncertain one:\n%s", second)
	}
}

// Retrying says when, and it says it once: an absolute time, not a
// countdown that would need a timer repainting the screen every second
// (§6.3).
func TestRetryingNamesAnAbsoluteTime(t *testing.T) {
	at := time.Date(2026, 9, 28, 14, 35, 0, 0, time.UTC)
	source := &pendingSource{messages: []PendingMessage{{
		EntryID:       "entry-1",
		ChatID:        7,
		Text:          "текст",
		State:         MessageDeliveryRetrying,
		Attempt:       2,
		NextAttemptAt: at,
	}}}

	m := conversationWithPending(t, theme.ProfileNoColor, 100, 24, source)
	m, _ = updateModel(t, m, deliveryRefresh(t, m, source))

	view := plain(m.View())
	if !strings.Contains(view, "14:35") {
		t.Fatalf("the retry time is not on the screen:\n%s", view)
	}
	if strings.Contains(view, "через") || strings.Contains(view, "попытка") {
		t.Fatalf("the retry is a countdown instead of a time:\n%s", view)
	}
}

// A message queued in this session is in the timeline at once, from the
// draft and the entry the queue gave back. It does not wait for a poll:
// the user pressed Enter and the message has to be there.
func TestAMessageQueuedInThisSessionAppearsAtOnce(t *testing.T) {
	m, _ := pendingModel(t, theme.ProfileNoColor, 100, 24)

	m, _ = updateModel(t, m, press(tea.KeyEnter))
	m, _ = updateModel(t, m, deliverSubmission(m, "entry-1"))

	view := plain(m.View())
	if !strings.Contains(view, "проверяю сборку") {
		t.Fatalf("the message is not in the timeline:\n%s", view)
	}
	if !strings.Contains(view, "Queued") {
		t.Fatalf("the state of the message is not on the screen:\n%s", view)
	}
}

// After the queue has taken the message, its state follows the poll: the
// dispatcher owns it now.
func TestTheStateOfAQueuedMessageFollowsTheQueue(t *testing.T) {
	m, source := pendingModel(t, theme.ProfileNoColor, 100, 24)
	m, _ = updateModel(t, m, press(tea.KeyEnter))
	m, _ = updateModel(t, m, deliverSubmission(m, "entry-1"))

	// The queue lists the entry as dispatching, with the text it holds.
	source.messages = []PendingMessage{{
		EntryID: "entry-1",
		ChatID:  7,
		Text:    "проверяю сборку",
		State:   MessageDeliverySending,
	}}
	m, _ = updateModel(t, m, deliveryRefresh(t, m, source))

	view := plain(m.View())
	if !strings.Contains(view, "Sending") {
		t.Fatalf("the state did not follow the queue:\n%s", view)
	}
	if strings.Contains(view, "Queued") {
		t.Fatalf("the old state is still on the screen:\n%s", view)
	}
}

// A message Telegram accepted is left in the timeline for this session,
// so that the user sees it go out, and it is gone after the history brings
// it back: from then on it is an ordinary message of the conversation.
func TestAnAcceptedMessageStaysUntilTheHistoryBringsItBack(t *testing.T) {
	// A narrow screen, where a conversation is left the way a single-pane
	// screen is: on a wide one the list is beside the conversation and the
	// conversation is not left at all.
	m, source := pendingModel(t, theme.ProfileNoColor, 60, 24)
	m, _ = updateModel(t, m, press(tea.KeyEnter))
	m, _ = updateModel(t, m, deliverSubmission(m, "entry-1"))

	// The queue accepts it and drops it from the pending list.
	source.messages = nil
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
		}},
	})

	view := plain(m.View())
	if !strings.Contains(view, "Sent") {
		t.Fatalf("an accepted message is not marked as sent:\n%s", view)
	}
	if !strings.Contains(view, "проверяю сборку") {
		t.Fatalf("an accepted message left the timeline:\n%s", view)
	}

	// Re-entering the chat reloads the history, and the message is in it.
	// Two of them: the composer hands over to the timeline, and the
	// timeline leaves.
	m, _ = updateModel(t, m, press(tea.KeyEsc))
	m, _ = updateModel(t, m, press(tea.KeyEsc))
	m, _ = updateModel(t, m, press(tea.KeyEnter))

	view = plain(m.View())
	if strings.Contains(view, "Sent") {
		t.Fatalf("an accepted message is still drawn separately:\n%s", view)
	}
}

// A message from a previous session is in the timeline with its text: the
// queue kept it, and the history does not have it yet.
func TestAMessageFromAPreviousSessionIsInTheTimeline(t *testing.T) {
	source := &pendingSource{messages: []PendingMessage{{
		EntryID:   "entry-old",
		ChatID:    7,
		Text:      "отправлено до перезапуска",
		State:     MessageDeliveryQueued,
		CreatedAt: time.Date(2026, 9, 27, 9, 0, 0, 0, time.UTC),
	}}}

	m := conversationWithPending(t, theme.ProfileNoColor, 100, 24, source)
	m, _ = updateModel(t, m, deliveryRefresh(t, m, source))

	view := plain(m.View())
	if !strings.Contains(view, "отправлено до перезапуска") {
		t.Fatalf("the message of the previous session is not in the timeline:\n%s", view)
	}
	if !strings.Contains(view, "Queued") {
		t.Fatalf("its state is not on the screen:\n%s", view)
	}
}

// Pending messages are ordered by when they were created, oldest first, so
// that the newest is the last thing above the composer.
func TestPendingMessagesAreOrderedByCreation(t *testing.T) {
	source := &pendingSource{messages: []PendingMessage{
		{
			EntryID:   "new",
			ChatID:    7,
			Text:      "второе",
			State:     MessageDeliveryQueued,
			CreatedAt: time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC),
		},
		{
			EntryID:   "old",
			ChatID:    7,
			Text:      "первое",
			State:     MessageDeliveryQueued,
			CreatedAt: time.Date(2026, 9, 27, 9, 0, 0, 0, time.UTC),
		},
	}}

	m := conversationWithPending(t, theme.ProfileNoColor, 100, 24, source)
	m, _ = updateModel(t, m, deliveryRefresh(t, m, source))

	view := plain(m.View())
	firstAt, _ := lineIndexWith(view, "первое")
	secondAt, _ := lineIndexWith(view, "второе")
	if firstAt < 0 || secondAt < 0 || firstAt > secondAt {
		t.Fatalf("pending messages are out of order:\n%s", view)
	}
}

// The text of a pending message never leaves the view. It is not in a
// status, not in an error, and not in what fmt prints for one of these
// values on its way to a log line or a crash report (§19).
func TestPendingMessagePrintsWithoutItsText(t *testing.T) {
	const text = "секретный текст сообщения"

	pending := PendingMessage{
		EntryID: "entry-1",
		ChatID:  7,
		Text:    text,
		State:   MessageDeliveryQueued,
	}

	for _, format := range []string{"%v", "%+v", "%#v", "%s"} {
		printed := fmt.Sprintf(format, pending)
		if strings.Contains(printed, text) {
			t.Fatalf(
				"%s of a pending message printed its text: %q",
				format,
				printed,
			)
		}
		if !strings.Contains(printed, "entry-1") {
			t.Fatalf(
				"%s of a pending message lost its entry: %q",
				format,
				printed,
			)
		}
	}

	// A slice of them, which is what a log would print.
	list := fmt.Sprintf("%v", []PendingMessage{pending})
	if strings.Contains(list, text) {
		t.Fatalf("a slice of pending messages printed its text: %q", list)
	}
}

// The status list stays without text: the privacy invariant of #21 is not
// weakened by a source that has the text.
func TestTheStatusListStillHasNoText(t *testing.T) {
	source := &pendingSource{messages: []PendingMessage{{
		EntryID: "entry-1",
		ChatID:  7,
		Text:    "текст",
		State:   MessageDeliveryQueued,
	}}}

	m := conversationWithPending(t, theme.ProfileNoColor, 100, 24, source)
	m, _ = updateModel(t, m, deliveryRefresh(t, m, source))

	for _, status := range m.deliveryStatuses {
		if strings.Contains(fmt.Sprintf("%+v", status), "текст") {
			t.Fatalf("a status carries message text: %+v", status)
		}
	}
}

// A read that fails leaves the last known delivery state on the screen: a
// transient failure of a read is not a reason to make a message of the
// conversation disappear.
func TestAFailedReadKeepsTheMessagesOnScreen(t *testing.T) {
	source := &pendingSource{messages: []PendingMessage{{
		EntryID: "entry-1", ChatID: 7, Text: "текст", State: MessageDeliveryQueued,
	}}}

	m := conversationWithPending(t, theme.ProfileNoColor, 100, 24, source)
	m, _ = updateModel(t, m, deliveryRefresh(t, m, source))

	source.err = errors.New("read failed")
	m, _ = updateModel(t, m, deliveryRefresh(t, m, source))

	if !strings.Contains(plain(m.View()), "текст") {
		t.Fatalf("a failed read emptied the timeline:\n%s", plain(m.View()))
	}
}

// Uncertain is not terminal: §6.2 keeps it open until the user decides,
// and a state that is terminal here is a state the interface stops
// watching.
func TestUncertainIsNotTerminal(t *testing.T) {
	if MessageDeliveryUncertain.IsTerminal() {
		t.Fatal("uncertain must not be terminal: it waits for a user decision")
	}

	for _, state := range []MessageDeliveryState{
		MessageDeliverySent,
		MessageDeliveryFailed,
		MessageDeliveryCanceled,
	} {
		if !state.IsTerminal() {
			t.Fatalf("%s must be terminal", state)
		}
	}

	for _, state := range []MessageDeliveryState{
		MessageDeliveryQueued,
		MessageDeliverySending,
		MessageDeliveryRetrying,
		MessageDeliveryUncertain,
	} {
		if state.IsTerminal() {
			t.Fatalf("%s must not be terminal", state)
		}
	}
}

// The block that used to sit between the timeline and the composer is
// gone: the state of a message is under the message.
func TestThereIsNoSeparateDeliveryBlock(t *testing.T) {
	source := &pendingSource{messages: []PendingMessage{{
		EntryID: "entry-1", ChatID: 7, Text: "текст", State: MessageDeliveryQueued,
	}}}

	m := conversationWithPending(t, theme.ProfileNoColor, 100, 24, source)
	m, _ = updateModel(t, m, deliveryRefresh(t, m, source))

	view := plain(m.View())
	for _, gone := range []string{"Delivery", "Обновление…"} {
		if strings.Contains(view, gone) {
			t.Fatalf("the delivery block is still on the screen (%q):\n%s", gone, view)
		}
	}
	if !strings.Contains(view, "Queued") {
		t.Fatalf("the state of the message went with the block:\n%s", view)
	}
}

// deliveryRefresh is what a poll delivers: the statuses of the chat and
// the pending messages with their text, in one read.
//
// The model of these tests has no account key, so its own poll would never
// start; the message is built here instead, with the same shape the poll
// produces.
func deliveryRefresh(t *testing.T, m Model, source PendingMessageSource) tea.Msg {
	t.Helper()

	msg := messageStatusesLoadedMsg{
		generation: m.messageStatusGeneration,
		read:       m.statusReadSeq,
		accountKey: m.messageStatusAccountKey,
		chatID:     m.messageStatusChatID,
	}

	if source == nil {
		return msg
	}

	pending, err := source.ListPendingMessages(
		context.Background(),
		msg.accountKey,
		msg.chatID,
	)
	if err != nil {
		return messageStatusesFailedMsg{
			generation: msg.generation,
			read:       msg.read,
			accountKey: msg.accountKey,
			chatID:     msg.chatID,
			err:        err,
		}
	}

	msg.pending = pending

	return msg
}

// deliverSubmission is the answer of the queue to an Enter.
func deliverSubmission(m Model, entryID string) tea.Msg {
	return composerSubmissionMsg{
		chatID:     7,
		operation:  m.sendOperation,
		submission: Submission{ID: entryID, State: SubmissionQueued},
	}
}

// stateSymbol returns the symbol of a state, so that a test says what it
// expects instead of repeating the theme's table.
func stateSymbol(state MessageDeliveryState) string {
	themed, known := deliveryStateOf(state)
	if !known {
		return "?"
	}

	return themed.Mark().Symbol
}

// The five names §21 reserves for the status vocabulary, in the place the
// vocabulary is now used: under the message it belongs to.
//
// Each of them is the same contract — a state is a symbol and a word, and
// the word is what carries the meaning — said about one state, so that a
// change to one of them is a change to a named thing.

func TestQueuedUsesSymbolAndText(t *testing.T) {
	assertStateLabel(t, MessageDeliveryQueued, "●", "Queued")
}

func TestRetryingUsesSymbolAndText(t *testing.T) {
	assertStateLabel(t, MessageDeliveryRetrying, "↻", "Retrying")
}

func TestFailedUsesSymbolAndText(t *testing.T) {
	assertStateLabel(t, MessageDeliveryFailed, "!", "Failed")
}

// Uncertain carries a warning of its own, because the user has to decide
// what to do with it and a state name does not tell them what.
func TestUncertainUsesWarningText(t *testing.T) {
	assertStateLabel(t, MessageDeliveryUncertain, "?", "Delivery uncertain")

	view := plain(deliveredFor(t, &pendingSource{messages: []PendingMessage{{
		EntryID: "entry-1", ChatID: 7, Text: "текст", State: MessageDeliveryUncertain,
	}}}).View())

	if !strings.Contains(view, uncertainWarning) {
		t.Fatalf("an uncertain message does not warn about the duplicate:\n%s", view)
	}
	if strings.Contains(view, "Retry") {
		t.Fatalf("an uncertain message offers a retry:\n%s", view)
	}
}

// A status is never only colour and never only a symbol: the word is on
// the screen in the profile that prints nothing at all, which is the only
// place where the proof is worth anything.
func TestStatusNeverDependsOnlyOnColor(t *testing.T) {
	for state, word := range map[MessageDeliveryState]string{
		MessageDeliveryQueued:    "Queued",
		MessageDeliverySending:   "Sending",
		MessageDeliveryRetrying:  "Retrying",
		MessageDeliveryFailed:    "Failed",
		MessageDeliveryUncertain: "Delivery uncertain",
		MessageDeliverySent:      "Sent",
		MessageDeliveryCanceled:  "Canceled",
	} {
		t.Run(string(state), func(t *testing.T) {
			view := plain(deliveredFor(t, &pendingSource{messages: []PendingMessage{{
				EntryID: "entry-1",
				ChatID:  7,
				Text:    "текст",
				State:   state,
			}}}).View())

			if !strings.Contains(view, word) {
				t.Fatalf("the state has no word without colour: %q", word)
			}
		})
	}
}

// assertStateLabel checks that one state is drawn as a symbol and a word,
// with no colour anywhere in the answer.
func assertStateLabel(
	t *testing.T,
	state MessageDeliveryState,
	symbol string,
	word string,
) {
	t.Helper()

	view := plain(deliveredFor(t, &pendingSource{messages: []PendingMessage{{
		EntryID: "entry-1",
		ChatID:  7,
		Text:    "текст",
		State:   state,
	}}}).View())

	if !strings.Contains(view, symbol) {
		t.Fatalf("the state has no symbol %q:\n%s", symbol, view)
	}
	if !strings.Contains(view, word) {
		t.Fatalf("the state has no word %q:\n%s", word, view)
	}
}

// A retry is a time, not a countdown. The only timer on this path is the
// delivery poll, which reads the queue again every couple of seconds; the
// screen is not repainted to count down to it, because a screen that
// repaints itself every second is a screen that spends the user's battery
// and their attention on a number that changes by itself (§6.3).
func TestRetryingSchedulesNoCountdown(t *testing.T) {
	source := &pendingSource{messages: []PendingMessage{{
		EntryID:       "entry-1",
		ChatID:        7,
		Text:          "текст",
		State:         MessageDeliveryRetrying,
		NextAttemptAt: testClock.Add(time.Minute),
	}}}

	m := conversationWithPending(t, theme.ProfileNoColor, 100, 24, source)
	m, _ = updateModel(t, m, deliveryRefresh(t, m, source))

	// The poll is the only thing the tick schedules, and the tick is the
	// same one every delivery read uses.
	_, cmd := m.handleMessageStatusPollTick(messageStatusPollTickMsg{})
	if cmd == nil {
		t.Fatal("the delivery poll stopped")
	}
	if messageStatusPollInterval != 2*time.Second {
		t.Fatalf(
			"poll interval = %s: it is a read of the queue, not a countdown",
			messageStatusPollInterval,
		)
	}
}

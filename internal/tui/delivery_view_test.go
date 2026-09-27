package tui

import (
	"context"
	"strings"
	"testing"

	"telecli/internal/tui/theme"
)

// The delivery view is a pure read.
//
// The states of the messages of a conversation are drawn under the messages
// themselves, and drawing them must not reach anything: a screen that
// submits a message while it draws, or that reads a source while it draws,
// or that changes what it was given, is a screen that behaves differently
// the second time it is looked at. These tests are what is left of the
// block's own tests after the block went away, and they are the part of
// them that still matters.

// statusOnlyModel returns a model whose only delivery source is a status
// list, which is direct delivery mode with a status reader.
// defaultTestTheme is the theme a model draws with when a test does not
// care which one it is. It is the default with no colour, so the assertions
// are about what is on the screen and not about how it is painted.
func defaultTestTheme() theme.Theme {
	return theme.DefaultTheme().ForProfile(theme.ProfileNoColor)
}

func statusOnlyModel(statuses []MessageStatus) Model {
	m := Model{
		ctx:              context.Background(),
		accountKey:       "account-1",
		deliveryStatuses: statuses,
		theme:            defaultTestTheme(),
	}
	m.screen = ScreenConversation
	m.focus = FocusComposer
	m.width = 80
	m.height = 24
	m.chats = []Chat{{ID: 42, Title: "Peer"}}

	return m
}

// pendingOnlyModel returns a model whose only delivery source lists pending
// messages with their text.
func pendingOnlyModel(messages []PendingMessage) Model {
	m := Model{
		ctx:             context.Background(),
		accountKey:      "account-1",
		pendingMessages: &pendingSource{messages: messages},
		theme:           defaultTestTheme(),
	}
	m.screen = ScreenConversation
	m.focus = FocusComposer
	m.width = 80
	m.height = 24
	m.chats = []Chat{{ID: 42, Title: "Peer"}}

	return m
}

// Drawing the screen reads nothing. The statuses are what the model already
// knows, and asking the source again from a view would be a read the model
// did not ask for.
func TestTheDeliveryViewDoesNotCallTheStatusSource(t *testing.T) {
	var calls int
	model := Model{
		ctx: context.Background(),
		messageStatuses: &countingStatusSource{
			calls: &calls,
		},
		theme: defaultTestTheme(),
	}
	model.screen = ScreenConversation
	model.width = 80
	model.height = 24
	model.chats = []Chat{{ID: 42, Title: "Peer"}}

	_ = model.View()

	if calls != 0 {
		t.Fatalf("the view called the status source %d times, want 0", calls)
	}
}

// Drawing the screen never sends anything.
func TestTheDeliveryViewDoesNotCallTheSubmitter(t *testing.T) {
	submitter := &recordingSubmitter{}
	model := pendingOnlyModel([]PendingMessage{{
		EntryID: "entry-1",
		ChatID:  42,
		Text:    "текст",
		State:   MessageDeliveryQueued,
	}})
	model.submitter = submitter
	model.pending = []PendingMessage{{
		EntryID: "entry-1",
		ChatID:  42,
		Text:    "текст",
		State:   MessageDeliveryQueued,
	}}

	_ = model.View()

	if submitter.calls != 0 {
		t.Fatalf("the view called the submitter %d times, want 0", submitter.calls)
	}
}

// Drawing the screen changes nothing it was given: the snapshot belongs to
// the model, and a view that sorted or filtered it in place would reorder
// what the next draw sees.
func TestTheDeliveryViewDoesNotMutateTheSnapshot(t *testing.T) {
	statuses := []MessageStatus{
		{EntryID: "entry-1", ChatID: 42, State: MessageDeliverySent},
		{EntryID: "entry-2", ChatID: 42, State: MessageDeliveryRetrying},
	}
	model := statusOnlyModel(statuses)

	_ = model.View()

	for index, status := range statuses {
		if status.EntryID != model.deliveryStatuses[index].EntryID {
			t.Fatalf(
				"the snapshot was reordered in place: %+v",
				model.deliveryStatuses,
			)
		}
	}
}

// The text of a message is not in a status, and the view has nothing else
// to draw it from: a status that carried the text would put it in every
// place a status travels (§19).
func TestTheDeliveryViewDoesNotExposeStatusPayload(t *testing.T) {
	const text = "секретный текст"
	model := pendingOnlyModel(nil)
	model.deliveryStatuses = []MessageStatus{{
		EntryID:    "entry-1",
		AccountKey: "account-1",
		ChatID:     42,
		State:      MessageDeliveryQueued,
	}}

	if strings.Contains(model.View(), text) {
		t.Fatal("the view drew text that is in no message")
	}
}

// A state this build does not know is not drawn as one it does. The
// message keeps its text and loses its label, which is the honest thing to
// do with a state nobody can name.
func TestAnUnknownDeliveryStateIsNotDrawnAsAKnownOne(t *testing.T) {
	model := pendingOnlyModel([]PendingMessage{{
		EntryID: "entry-1",
		ChatID:  42,
		Text:    "текст",
		State:   MessageDeliveryState("teleported"),
	}})
	model.pending, _ = model.pendingMessages.ListPendingMessages(
		context.Background(),
		"account-1",
		42,
	)

	view := model.View()
	if !strings.Contains(view, "текст") {
		t.Fatalf("the message is not on the screen:\n%s", view)
	}
	for _, known := range []string{"Queued", "Sending", "Sent", "Failed"} {
		if strings.Contains(view, known) {
			t.Fatalf("an unknown state was drawn as %q:\n%s", known, view)
		}
	}
}

// countingStatusSource counts the reads and answers with nothing.
type countingStatusSource struct {
	calls *int
}

func (s *countingStatusSource) ListMessageStatuses(
	context.Context,
	string,
	int64,
) ([]MessageStatus, error) {
	*s.calls = *s.calls + 1

	return nil, nil
}

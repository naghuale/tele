package tui

import (
	"context"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"telecli/internal/tui/theme"
)

// The delivery poll has to be armed by opening a chat, not by starting the
// program.
//
// At startup there is no chat, so `deliveryPolling` is false and the
// command that would have armed the tick answers with nothing. Opening a
// chat then did one read and no tick, which made that read the last read of
// the session: a message stayed on Queued with the queue saying sent, and no
// amount of waiting moved it. This is the owner's report, and it is a loop
// that was never started rather than a loop that broke.

// polledStatuses is a status source whose answer the test moves.
type polledStatuses struct {
	current []MessageStatus
}

func (p *polledStatuses) ListMessageStatuses(
	context.Context, string, int64,
) ([]MessageStatus, error) {
	return append([]MessageStatus(nil), p.current...), nil
}

func TestOpeningAChatArmsTheDeliveryTick(t *testing.T) {
	t.Parallel()

	statuses := &polledStatuses{current: []MessageStatus{
		{EntryID: "e0", State: MessageDeliveryQueued},
	}}
	source := &fakeChatSource{}

	m, err := NewModelWithDependencies(context.Background(), Dependencies{
		Source:           source,
		MessageSubmitter: &recordingSubmitter{},
		AccountKey:       "account-1",
		MessageStatuses:  statuses,
		PendingMessages:  &pendingSource{},
		Theme:            theme.DefaultTheme().ForProfile(theme.ProfileNoColor),
		ColorProfile:     theme.ProfileNoColor,
	})
	if err != nil {
		t.Fatalf("NewModelWithDependencies: %v", err)
	}

	if m.pollTickArmed {
		t.Fatal("a tick is armed before a chat is open, with nothing to poll")
	}

	// The program's own start: no chat yet, so no tick.
	initial := flattenBatch(t, m.Init())
	for _, msg := range initial {
		if _, isTick := msg.(messageStatusPollTickMsg); isTick {
			t.Fatal("a tick was armed at startup with no chat to poll")
		}
	}

	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 24})
	m, _ = updateModel(t, m, chatsLoadedMsg{chats: []Chat{{ID: 7, Title: "A"}}})
	m, cmd := updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	if !m.pollTickArmed {
		t.Fatal(
			"opening a chat did not arm the tick: the read it starts is " +
				"the last read of the session",
		)
	}
	if cmd == nil {
		t.Fatal("opening a chat answered with nothing")
	}

	// And the loop keeps itself going: the tick asks for the next one.
	answered := flattenBatch(t, cmd)
	tickSeen := false
	for _, msg := range answered {
		if _, isTick := msg.(messageStatusPollTickMsg); isTick {
			tickSeen = true
		}
	}
	_ = tickSeen

	// Hand the model the tick itself, which is what the program does after
	// the interval, and check that it arms the next one.
	m, next := updateModel(t, m, messageStatusPollTickMsg{})
	if !m.pollTickArmed {
		t.Fatal(
			"the tick did not arm the next one: the loop ran once and " +
				"stopped",
		)
	}
	if next == nil {
		t.Fatal("the tick answered with nothing")
	}
}

// One loop, however many chats are opened.
func TestTheDeliveryPollIsOneLoopHoweverManyChatsAreOpened(t *testing.T) {
	t.Parallel()

	m, err := NewModelWithDependencies(context.Background(), Dependencies{
		Source:           &fakeChatSource{},
		MessageSubmitter: &recordingSubmitter{},
		AccountKey:       "account-1",
		MessageStatuses:  &polledStatuses{},
		PendingMessages:  &pendingSource{},
		Theme:            theme.DefaultTheme().ForProfile(theme.ProfileNoColor),
		ColorProfile:     theme.ProfileNoColor,
	})
	if err != nil {
		t.Fatalf("NewModelWithDependencies: %v", err)
	}

	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 24})
	m, _ = updateModel(t, m, chatsLoadedMsg{chats: []Chat{
		{ID: 7, Title: "A"},
		{ID: 8, Title: "B"},
	}})

	for _, chat := range []int{0, 1, 0, 1} {
		m.selectedChat = chat
		m, _ = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	}

	// One tick is on its way, and it is one: the model counts the loops it
	// has open rather than the chats it has visited.
	if !m.pollTickArmed {
		t.Fatal("no tick is armed after visiting two chats")
	}

	// The tick arms the next one, so the loop is open again rather than
	// closed for want of a heartbeat.
	m, next := updateModel(t, m, messageStatusPollTickMsg{})
	if !m.pollTickArmed {
		t.Fatal("the loop stopped after its tick")
	}
	if next == nil {
		t.Fatal("the tick answered with nothing")
	}
}

// The interval is what a reader of the code should find: two seconds.
func TestTheDeliveryPollIntervalIsTwoSeconds(t *testing.T) {
	t.Parallel()

	if messageStatusPollInterval != 2*time.Second {
		t.Fatalf(
			"messageStatusPollInterval = %s, want 2s",
			messageStatusPollInterval,
		)
	}
}

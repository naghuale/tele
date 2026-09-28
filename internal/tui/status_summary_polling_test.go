package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// The status summary is read on the delivery poll, and a read that learned
// nothing delivers nothing.

func TestTheStatusSummaryIsReadOnTheDeliveryTick(t *testing.T) {
	t.Parallel()

	summary := &summarySource{summary: StatusSummary{
		Connection: ConnectionReady,
		Queue:      QueueSummary{Known: true, Queued: 2},
	}}
	statuses := &h6c2bStatusSource{
		listMessageStatuses: func(
			context.Context,
			string,
			int64,
		) ([]MessageStatus, error) {
			return []MessageStatus{{EntryID: "entry-1"}}, nil
		},
	}

	model := newPollingTestModel(statuses)
	model.statusSummaries = summary
	model.messageStatusAccountKey = "account-1"
	model.messageStatusChatID = 42
	model.messageStatusGeneration = 3

	_, cmd := model.handleMessageStatusPollTick(
		messageStatusPollTickMsg{},
	)
	if cmd == nil {
		t.Fatal("the tick did not read anything")
	}

	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatal("the poll is not a batch")
	}
	// One read per source and the next tick.
	if len(batch) != 3 {
		t.Fatalf("the poll batch has %d commands, want 2 reads and a tick", len(batch))
	}
	for _, read := range batch[:2] {
		_ = read()
	}
	if summary.calls != 1 {
		t.Fatalf("summary reads = %d, want 1", summary.calls)
	}
}

// The status line is in the chat list header on a narrow screen, where no
// conversation is open. A poll that waited for a chat would leave the
// program silent exactly where a user is least able to act.
func TestTheStatusIsReadWithoutAnOpenChat(t *testing.T) {
	t.Parallel()

	summary := &summarySource{summary: StatusSummary{Connection: ConnectionReady}}
	model := newPollingTestModel(nil)
	model.statusSummaries = summary

	cmd := model.loadStatusSummary()
	if cmd == nil {
		t.Fatal("without a chat there was nothing to read")
	}
	_ = cmd()
	if summary.calls != 1 {
		t.Fatalf("summary reads = %d, want 1", summary.calls)
	}
}

func TestAnUnchangedSummaryDeliversNothing(t *testing.T) {
	t.Parallel()

	summary := &summarySource{summary: StatusSummary{Connection: ConnectionReady}}
	model := newPollingTestModel(nil)
	model.statusSummaries = summary

	first := model.loadStatusSummary()()
	loaded, ok := first.(statusSummaryLoadedMsg)
	if !ok {
		t.Fatalf("the first read delivered %T, want a loaded message", first)
	}
	model, _ = model.handleStatusSummaryLoaded(loaded)

	// The model now knows the summary, and the next read finds the same
	// one. A repaint that changes no pixels is a repaint a user can see
	// and a battery pays for.
	if again := model.loadStatusSummary()(); again != nil {
		t.Fatalf("an unchanged summary delivered %T", again)
	}
}

func TestAnUnchangedDeliveryReadDeliversNothing(t *testing.T) {
	t.Parallel()

	statuses := &h6c2bStatusSource{
		listMessageStatuses: func(
			context.Context,
			string,
			int64,
		) ([]MessageStatus, error) {
			return []MessageStatus{{EntryID: "entry-1"}}, nil
		},
	}
	model := newPollingTestModel(statuses)
	model.messageStatusAccountKey = "account-1"
	model.messageStatusChatID = 42
	model.messageStatusGeneration = 1

	first := model.loadMessageStatuses()()
	loaded, ok := first.(messageStatusesLoadedMsg)
	if !ok {
		t.Fatalf("the first read delivered %T, want a loaded message", first)
	}
	model, _ = model.handleMessageStatusesLoaded(loaded)

	if again := model.loadMessageStatuses()(); again != nil {
		t.Fatalf("an unchanged delivery read delivered %T", again)
	}
}

// The two reads are separate commands on purpose. A queue that cannot be
// read must not cost the delivery states under the messages, and a failed
// status read must not cost the connection.
func TestAFailedSummaryReadLeavesTheDeliveryStates(t *testing.T) {
	t.Parallel()

	summary := &summarySource{}
	statuses := &h6c2bStatusSource{
		listMessageStatuses: func(
			context.Context,
			string,
			int64,
		) ([]MessageStatus, error) {
			return []MessageStatus{{EntryID: "entry-1"}}, nil
		},
	}
	model := newPollingTestModel(statuses)
	model.statusSummaries = summary
	model.messageStatusAccountKey = "account-1"
	model.messageStatusChatID = 42
	model.messageStatusGeneration = 4

	// A summary that was read once, so there is a connection to keep.
	model.summary = StatusSummary{Connection: ConnectionReady}
	summary.err = errors.New("the outbox store is closed")

	model, _ = model.handleStatusSummaryFailed(statusSummaryFailedMsg{
		generation: 4,
		read:       model.summaryReadSeq,
		err:        summary.err,
	})
	if got := model.summary.Connection; got != ConnectionReady {
		t.Fatalf("connection = %q, want the last known one", got)
	}

	// The other read of the same tick is unaffected.
	model, _ = model.handleMessageStatusesLoaded(messageStatusesLoadedMsg{
		generation: 4,
		read:       model.statusReadSeq,
		accountKey: "account-1",
		chatID:     42,
		statuses:   []MessageStatus{{EntryID: "entry-1"}},
	})
	if len(model.deliveryStatuses) != 1 {
		t.Fatalf("deliveryStatuses = %#v, want the read applied", model.deliveryStatuses)
	}
}

// The cause of a read that failed goes to the log and not to the screen,
// and the log must not carry the text of a message either.
func TestAFailedSummaryReadIsReportedToTheDiagnostics(t *testing.T) {
	t.Parallel()

	log := &recordingWriter{}
	model := newPollingTestModel(nil)
	model.diagnostics = log

	cause := errors.New("ERROR 400: the phone number is invalid")
	model, _ = model.handleStatusSummaryFailed(statusSummaryFailedMsg{
		generation: model.messageStatusGeneration,
		read:       model.summaryReadSeq,
		err:        cause,
	})

	if !strings.Contains(log.String(), "the phone number is invalid") {
		t.Fatalf("diagnostics = %q, want the cause", log.String())
	}
}

func TestASlowSummaryReadIsDiscarded(t *testing.T) {
	t.Parallel()

	model := newPollingTestModel(nil)
	model.summary = StatusSummary{Connection: ConnectionReady}
	model.summaryReadSeq = 2

	updated, _ := model.handleStatusSummaryLoaded(statusSummaryLoadedMsg{
		generation: model.messageStatusGeneration,
		read:       1,
		summary:    StatusSummary{Connection: ConnectionWaitingForNetwork},
	})
	if updated.summary.Connection != ConnectionReady {
		t.Fatalf(
			"connection = %q, want the older read discarded",
			updated.summary.Connection,
		)
	}
}

// A model with no source keeps no timer: a program that wakes every two
// seconds to read nothing is a program nobody can reason about.
func TestNothingIsPolledWithoutASource(t *testing.T) {
	t.Parallel()

	model := newPollingTestModel(nil)
	if model.deliveryPolling() {
		t.Fatal("a model without sources must not poll")
	}
	if cmd := model.pollDeliverySources(); cmd != nil {
		t.Fatal("a model without sources scheduled a poll")
	}
}

// recordingWriter collects what a model writes to its diagnostic stream.
type recordingWriter struct {
	written strings.Builder
}

func (w *recordingWriter) Write(p []byte) (int, error) {
	return w.written.Write(p)
}

func (w *recordingWriter) String() string {
	return w.written.String()
}

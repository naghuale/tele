package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"telecli/internal/telegram"
	"telecli/internal/tui"
)

// The status line's source: the connection TDLib reports and the counters
// of the durable queue, in the shape the interface draws.
//
// The two parts come from two systems that fail independently, and the
// whole point of this adapter is that one of them failing does not take
// the other off the screen.

func TestEveryTDLibConnectionStateReachesTheStatusLine(t *testing.T) {
	for name, testCase := range map[string]struct {
		tdlib telegram.ConnectionState
		want  tui.ConnectionState
	}{
		"ready":                 {tdlib: telegram.ConnectionStateReady, want: tui.ConnectionReady},
		"connecting":            {tdlib: telegram.ConnectionStateConnecting, want: tui.ConnectionConnecting},
		"updating":              {tdlib: telegram.ConnectionStateUpdating, want: tui.ConnectionUpdating},
		"waiting":               {tdlib: telegram.ConnectionStateWaitingForNetwork, want: tui.ConnectionWaitingForNetwork},
		"connecting proxy":      {tdlib: telegram.ConnectionStateConnectingToProxy, want: tui.ConnectionConnectingToProxy},
		"telecli does not know": {tdlib: telegram.ConnectionStateUnknown, want: tui.ConnectionUnknown},
	} {
		t.Run(name, func(t *testing.T) {
			source, err := NewLiveStatusSummarySource(
				fixedConnectionState{state: testCase.tdlib},
				fixedHealthSource{health: MessageDeliveryHealth{
					State: MessageDeliveryHealthRunning,
				}},
				0,
			)
			if err != nil {
				t.Fatalf("NewLiveStatusSummarySource: %v", err)
			}

			summary, err := source.ReadStatusSummary(
				context.Background(),
				0,
			)
			if err != nil {
				t.Fatalf("ReadStatusSummary: %v", err)
			}
			if summary.Connection != testCase.want {
				t.Fatalf(
					"connection = %q, want %q",
					summary.Connection,
					testCase.want,
				)
			}
		})
	}
}

// The counts are the ones the durable store reports. A retried entry is
// one that failed and will be tried again, which is what "retrying" means
// to a user, and an entry that failed for good is neither queued nor
// retried: the interface says nothing about it, because the message itself
// says "Failed" under its own text.
func TestTheQueueCountsComeFromTheHealthSnapshot(t *testing.T) {
	source, err := NewLiveStatusSummarySource(
		fixedConnectionState{state: telegram.ConnectionStateReady},
		fixedHealthSource{health: MessageDeliveryHealth{
			State:           MessageDeliveryHealthRunning,
			Queued:          2,
			Dispatching:     1,
			FailedRetryable: 3,
			FailedPermanent: 4,
			Uncertain:       5,
			Canceled:        6,
		}},
		0,
	)
	if err != nil {
		t.Fatalf("NewLiveStatusSummarySource: %v", err)
	}

	summary, err := source.ReadStatusSummary(context.Background(), 0)
	if err != nil {
		t.Fatalf("ReadStatusSummary: %v", err)
	}
	if !summary.Queue.Known {
		t.Fatal("the queue was read and is not known")
	}
	if summary.Queue.Queued != 2 {
		t.Fatalf("queued = %d, want 2", summary.Queue.Queued)
	}
	if summary.Queue.Retrying != 3 {
		t.Fatalf("retrying = %d, want 3", summary.Queue.Retrying)
	}
	if summary.Queue.Recovering {
		t.Fatal("a running runtime is not recovering")
	}
}

// §18: while the delivery runtime is still starting, the entries of a
// previous run are being picked up, and the screen says so rather than
// showing an empty queue with no explanation.
func TestAStartingDeliveryRuntimeIsRecovering(t *testing.T) {
	source, err := NewLiveStatusSummarySource(
		fixedConnectionState{state: telegram.ConnectionStateReady},
		fixedHealthSource{health: MessageDeliveryHealth{
			State:  MessageDeliveryHealthStarting,
			Queued: 3,
		}},
		0,
	)
	if err != nil {
		t.Fatalf("NewLiveStatusSummarySource: %v", err)
	}

	summary, err := source.ReadStatusSummary(context.Background(), 0)
	if err != nil {
		t.Fatalf("ReadStatusSummary: %v", err)
	}
	if !summary.Queue.Recovering {
		t.Fatal("a starting runtime is not recovering")
	}
}

// A queue that cannot be read is not a queue with nothing in it, and the
// connection is still worth saying: the two parts come from two systems
// and one of them failing is not news about the other.
func TestAQueueThatCannotBeReadIsNotAnEmptyQueue(t *testing.T) {
	source, err := NewLiveStatusSummarySource(
		fixedConnectionState{state: telegram.ConnectionStateReady},
		fixedHealthSource{err: ErrMessageDeliveryHealthUnavailable},
		0,
	)
	if err != nil {
		t.Fatalf("NewLiveStatusSummarySource: %v", err)
	}

	summary, err := source.ReadStatusSummary(context.Background(), 0)
	if !errors.Is(err, ErrMessageDeliveryHealthUnavailable) {
		t.Fatalf("err = %v, want the health error", err)
	}
	if summary.Queue.Known {
		t.Fatal("a queue that failed to read must not be known")
	}
	if summary.Connection != tui.ConnectionReady {
		t.Fatalf("connection = %q, want the last known one", summary.Connection)
	}
}

// A program with no session has no connection to report, and that is not
// a connection state any of them: the interface must be able to run
// without Telegram and say nothing about the network.
func TestWithoutASessionTheConnectionIsUnknown(t *testing.T) {
	source, err := NewLiveStatusSummarySource(
		nil,
		fixedHealthSource{health: MessageDeliveryHealth{
			State:  MessageDeliveryHealthRunning,
			Queued: 1,
		}},
		0,
	)
	if err != nil {
		t.Fatalf("NewLiveStatusSummarySource: %v", err)
	}

	summary, err := source.ReadStatusSummary(context.Background(), 0)
	if err != nil {
		t.Fatalf("ReadStatusSummary: %v", err)
	}
	if summary.Connection != tui.ConnectionUnknown {
		t.Fatalf("connection = %q, want unknown", summary.Connection)
	}
	if summary.Queue.Queued != 1 {
		t.Fatalf("queued = %d, want 1", summary.Queue.Queued)
	}
}

// The summary is drawn, and a value on its way to a log must not carry
// more than counts: a cause can name a message body, and the summary is
// what the diagnostics stream receives.
func TestTheSummaryPrintsWithoutSecrets(t *testing.T) {
	source, err := NewLiveStatusSummarySource(
		fixedConnectionState{state: telegram.ConnectionStateReady},
		fixedHealthSource{err: fmt.Errorf(
			"read health: ERROR 400 +1 555 0100 api_hash 0123456789abcdef",
		)},
		0,
	)
	if err != nil {
		t.Fatalf("NewLiveStatusSummarySource: %v", err)
	}

	summary, _ := source.ReadStatusSummary(context.Background(), 0)
	for _, secret := range []string{
		"+1 555 0100",
		"api_hash 0123456789abcdef",
		"ERROR 400",
	} {
		// %v and %+v are the two forms a value on its way to a log takes
		// in this codebase, and StatusSummary has no text field to hide, so
		// a secret can only be in the error it came from.
		if strings.Contains(fmt.Sprintf("%v %+v", summary, summary), secret) {
			t.Fatalf("the summary carries %q", secret)
		}
	}
}

func TestTheStatusSummarySourceNeedsAQueue(t *testing.T) {
	if _, err := NewLiveStatusSummarySource(
		fixedConnectionState{state: telegram.ConnectionStateReady},
		nil,
		0,
	); err == nil {
		t.Fatal("a source without a queue must be refused")
	}
}

var _ tui.StatusSummarySource = (*LiveStatusSummarySource)(nil)

// fixedConnectionState is a live state that always answers the same.
type fixedConnectionState struct {
	state    telegram.ConnectionState
	presence telegram.Presence
}

func (s fixedConnectionState) ConnectionState() telegram.ConnectionState {
	return s.state
}

func (s fixedConnectionState) ChatPresence(
	telegram.ChatID,
) telegram.Presence {
	return s.presence
}

// fixedHealthSource is a health source that answers what a test puts in it.
type fixedHealthSource struct {
	health MessageDeliveryHealth
	state  MessageDeliveryHealthState
	err    error
}

func (s fixedHealthSource) State() MessageDeliveryHealthState {
	if s.state != "" {
		return s.state
	}

	return s.health.State
}

func (s fixedHealthSource) ReadMessageDeliveryHealth(
	context.Context,
) (MessageDeliveryHealth, error) {
	if s.err != nil {
		return MessageDeliveryHealth{}, s.err
	}

	return s.health, nil
}

var _ MessageDeliveryHealthSource = fixedHealthSource{}

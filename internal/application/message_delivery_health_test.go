package application

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"telecli/internal/outbox"
)

func TestMessageDeliveryHealthStatesAreDistinctAndKnown(t *testing.T) {
	t.Parallel()

	states := []MessageDeliveryHealthState{
		MessageDeliveryHealthStarting,
		MessageDeliveryHealthRunning,
		MessageDeliveryHealthStopping,
		MessageDeliveryHealthStopped,
		MessageDeliveryHealthFailed,
	}

	seen := make(map[MessageDeliveryHealthState]struct{}, len(states))
	for _, state := range states {
		if state == "" {
			t.Fatal("message delivery health state must not be empty")
		}
		if !state.IsKnown() {
			t.Fatalf("IsKnown() = false for supported state %q", state)
		}
		if _, exists := seen[state]; exists {
			t.Fatalf("duplicate message delivery health state %q", state)
		}
		seen[state] = struct{}{}
	}
}

func TestMessageDeliveryHealthStateRejectsUnknownValues(t *testing.T) {
	t.Parallel()

	for _, state := range []MessageDeliveryHealthState{
		"",
		"unknown",
		"healthy",
		"unavailable",
	} {
		if state.IsKnown() {
			t.Fatalf("IsKnown() = true for unsupported state %q", state)
		}
		if state.IsTerminal() {
			t.Fatalf("IsTerminal() = true for unsupported state %q", state)
		}
	}
}

func TestMessageDeliveryHealthStateTerminalClassification(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		state    MessageDeliveryHealthState
		terminal bool
	}{
		{name: "starting", state: MessageDeliveryHealthStarting},
		{name: "running", state: MessageDeliveryHealthRunning},
		{name: "stopping", state: MessageDeliveryHealthStopping},
		{name: "stopped", state: MessageDeliveryHealthStopped, terminal: true},
		{name: "failed", state: MessageDeliveryHealthFailed, terminal: true},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := test.state.IsTerminal(); got != test.terminal {
				t.Fatalf("IsTerminal() = %v, want %v", got, test.terminal)
			}
		})
	}
}

func TestMessageDeliveryHealthAllowsExpectedTransitions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		from MessageDeliveryHealthState
		to   MessageDeliveryHealthState
	}{
		{
			name: "starting to running",
			from: MessageDeliveryHealthStarting,
			to:   MessageDeliveryHealthRunning,
		},
		{
			name: "starting to failed",
			from: MessageDeliveryHealthStarting,
			to:   MessageDeliveryHealthFailed,
		},
		{
			name: "running to stopping",
			from: MessageDeliveryHealthRunning,
			to:   MessageDeliveryHealthStopping,
		},
		{
			name: "running to failed",
			from: MessageDeliveryHealthRunning,
			to:   MessageDeliveryHealthFailed,
		},
		{
			name: "stopping to stopped",
			from: MessageDeliveryHealthStopping,
			to:   MessageDeliveryHealthStopped,
		},
		{
			name: "stopping to failed",
			from: MessageDeliveryHealthStopping,
			to:   MessageDeliveryHealthFailed,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if !validMessageDeliveryHealthTransition(test.from, test.to) {
				t.Fatalf(
					"validMessageDeliveryHealthTransition(%q, %q) = false, want true",
					test.from,
					test.to,
				)
			}
		})
	}
}

func TestMessageDeliveryHealthAllowsIdempotentTransitions(t *testing.T) {
	t.Parallel()

	states := []MessageDeliveryHealthState{
		MessageDeliveryHealthStarting,
		MessageDeliveryHealthRunning,
		MessageDeliveryHealthStopping,
		MessageDeliveryHealthStopped,
		MessageDeliveryHealthFailed,
	}

	for _, state := range states {
		state := state
		t.Run(string(state), func(t *testing.T) {
			t.Parallel()

			if !validMessageDeliveryHealthTransition(state, state) {
				t.Fatalf(
					"validMessageDeliveryHealthTransition(%q, %q) = false, want true",
					state,
					state,
				)
			}
		})
	}
}

func TestMessageDeliveryHealthRejectsInvalidTransitions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		from MessageDeliveryHealthState
		to   MessageDeliveryHealthState
	}{
		{
			name: "starting to stopping",
			from: MessageDeliveryHealthStarting,
			to:   MessageDeliveryHealthStopping,
		},
		{
			name: "starting to stopped",
			from: MessageDeliveryHealthStarting,
			to:   MessageDeliveryHealthStopped,
		},
		{
			name: "running to starting",
			from: MessageDeliveryHealthRunning,
			to:   MessageDeliveryHealthStarting,
		},
		{
			name: "running to stopped",
			from: MessageDeliveryHealthRunning,
			to:   MessageDeliveryHealthStopped,
		},
		{
			name: "stopping to starting",
			from: MessageDeliveryHealthStopping,
			to:   MessageDeliveryHealthStarting,
		},
		{
			name: "stopping to running",
			from: MessageDeliveryHealthStopping,
			to:   MessageDeliveryHealthRunning,
		},
		{
			name: "stopped to starting",
			from: MessageDeliveryHealthStopped,
			to:   MessageDeliveryHealthStarting,
		},
		{
			name: "stopped to running",
			from: MessageDeliveryHealthStopped,
			to:   MessageDeliveryHealthRunning,
		},
		{
			name: "stopped to stopping",
			from: MessageDeliveryHealthStopped,
			to:   MessageDeliveryHealthStopping,
		},
		{
			name: "stopped to failed",
			from: MessageDeliveryHealthStopped,
			to:   MessageDeliveryHealthFailed,
		},
		{
			name: "failed to starting",
			from: MessageDeliveryHealthFailed,
			to:   MessageDeliveryHealthStarting,
		},
		{
			name: "failed to running",
			from: MessageDeliveryHealthFailed,
			to:   MessageDeliveryHealthRunning,
		},
		{
			name: "failed to stopping",
			from: MessageDeliveryHealthFailed,
			to:   MessageDeliveryHealthStopping,
		},
		{
			name: "failed to stopped",
			from: MessageDeliveryHealthFailed,
			to:   MessageDeliveryHealthStopped,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if validMessageDeliveryHealthTransition(test.from, test.to) {
				t.Fatalf(
					"validMessageDeliveryHealthTransition(%q, %q) = true, want false",
					test.from,
					test.to,
				)
			}
		})
	}
}

func TestMessageDeliveryHealthRejectsUnknownTransitionStates(t *testing.T) {
	t.Parallel()

	unknown := MessageDeliveryHealthState("unknown")

	tests := []struct {
		name string
		from MessageDeliveryHealthState
		to   MessageDeliveryHealthState
	}{
		{name: "unknown source", from: unknown, to: MessageDeliveryHealthRunning},
		{
			name: "unknown target",
			from: MessageDeliveryHealthRunning,
			to:   unknown,
		},
		{name: "both unknown", from: unknown, to: unknown},
		{name: "empty source", from: "", to: MessageDeliveryHealthRunning},
		{name: "empty target", from: MessageDeliveryHealthRunning, to: ""},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if validMessageDeliveryHealthTransition(test.from, test.to) {
				t.Fatalf(
					"validMessageDeliveryHealthTransition(%q, %q) = true, want false",
					test.from,
					test.to,
				)
			}
		})
	}
}

func TestProjectOperationalSnapshotCopiesAllCounts(t *testing.T) {
	t.Parallel()

	snapshot := outbox.OperationalSnapshot{
		Queued:          1,
		Dispatching:     2,
		Accepted:        3,
		FailedRetryable: 4,
		FailedPermanent: 5,
		Uncertain:       6,
		Canceled:        7,
	}

	got := projectOperationalSnapshot(
		MessageDeliveryHealthRunning,
		snapshot,
	)

	want := MessageDeliveryHealth{
		State:           MessageDeliveryHealthRunning,
		Queued:          1,
		Dispatching:     2,
		Accepted:        3,
		FailedRetryable: 4,
		FailedPermanent: 5,
		Uncertain:       6,
		Canceled:        7,
	}
	if got != want {
		t.Fatalf("projectOperationalSnapshot() = %#v, want %#v", got, want)
	}
}

func TestProjectOperationalSnapshotCopiesZeroSnapshot(t *testing.T) {
	t.Parallel()

	got := projectOperationalSnapshot(
		MessageDeliveryHealthStopping,
		outbox.OperationalSnapshot{},
	)
	want := MessageDeliveryHealth{State: MessageDeliveryHealthStopping}
	if got != want {
		t.Fatalf("projectOperationalSnapshot() = %#v, want %#v", got, want)
	}
}

func TestMessageDeliveryHealthDoesNotExposeIdentifiersOrPayload(t *testing.T) {
	t.Parallel()

	healthType := reflect.TypeOf(MessageDeliveryHealth{})

	for _, forbidden := range []string{
		"ID",
		"EntryID",
		"AccountKey",
		"ChatID",
		"DatabaseID",
		"DatabasePath",
		"Text",
		"Payload",
		"EncryptedText",
		"Ciphertext",
		"Nonce",
		"Error",
		"Err",
		"ErrorMessage",
		"ProviderMessage",
		"ProviderCode",
		"LeaseOwner",
		"InstanceID",
		"Path",
	} {
		if _, exists := healthType.FieldByName(forbidden); exists {
			t.Fatalf("MessageDeliveryHealth contains forbidden field %q", forbidden)
		}
	}
}

func TestMessageDeliveryHealthContainsExpectedFieldsOnly(t *testing.T) {
	t.Parallel()

	healthType := reflect.TypeOf(MessageDeliveryHealth{})

	want := []string{
		"State",
		"Queued",
		"Dispatching",
		"Accepted",
		"Sent",
		"FailedRetryable",
		"FailedPermanent",
		"Uncertain",
		"Canceled",
	}

	got := make([]string, 0, healthType.NumField())
	for index := 0; index < healthType.NumField(); index++ {
		got = append(got, healthType.Field(index).Name)
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("MessageDeliveryHealth fields = %#v, want %#v", got, want)
	}
}

func TestMessageDeliveryHealthSourceContract(t *testing.T) {
	t.Parallel()

	var source MessageDeliveryHealthSource = &h7bHealthSourceStub{}

	if got := source.State(); got != MessageDeliveryHealthRunning {
		t.Fatalf("State() = %q, want %q", got, MessageDeliveryHealthRunning)
	}

	health, err := source.ReadMessageDeliveryHealth(context.Background())
	if err != nil {
		t.Fatalf("ReadMessageDeliveryHealth() error = %v", err)
	}
	if health.State != MessageDeliveryHealthRunning {
		t.Fatalf("health.State = %q, want running", health.State)
	}
}

func TestMessageDeliveryHealthSourcePropagatesCanceledContext(t *testing.T) {
	t.Parallel()

	source := &h7bHealthSourceStub{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := source.ReadMessageDeliveryHealth(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestMessageDeliveryHealthUnavailableIsSentinel(t *testing.T) {
	t.Parallel()

	wrapped := errors.Join(
		errors.New("health source stopped"),
		ErrMessageDeliveryHealthUnavailable,
	)
	if !errors.Is(wrapped, ErrMessageDeliveryHealthUnavailable) {
		t.Fatal("errors.Is() = false, want true")
	}
}

type h7bHealthSourceStub struct{}

func (s *h7bHealthSourceStub) State() MessageDeliveryHealthState {
	return MessageDeliveryHealthRunning
}

func (s *h7bHealthSourceStub) ReadMessageDeliveryHealth(
	ctx context.Context,
) (MessageDeliveryHealth, error) {
	if err := ctx.Err(); err != nil {
		return MessageDeliveryHealth{}, err
	}
	return MessageDeliveryHealth{
		State: MessageDeliveryHealthRunning,
	}, nil
}

var _ MessageDeliveryHealthSource = (*h7bHealthSourceStub)(nil)

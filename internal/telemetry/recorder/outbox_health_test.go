package recorder

import (
	"errors"
	"reflect"
	"testing"
)

type h7c1RecordingHealthRecorder struct {
	health OutboxHealth
	calls  int
}

func (r *h7c1RecordingHealthRecorder) SetOutboxHealth(health OutboxHealth) {
	r.calls++
	r.health = health
}

var _ OutboxHealthRecorder = (*h7c1RecordingHealthRecorder)(nil)

func TestOutboxHealthStatesAreValidAndDistinct(t *testing.T) {
	t.Parallel()

	states := []OutboxHealthState{
		OutboxHealthStarting,
		OutboxHealthRunning,
		OutboxHealthStopping,
		OutboxHealthStopped,
		OutboxHealthFailed,
	}

	seen := make(map[OutboxHealthState]struct{}, len(states))
	for _, state := range states {
		if state == "" {
			t.Fatal("outbox health state must not be empty")
		}
		if !state.Valid() {
			t.Fatalf("Valid() = false for %q", state)
		}
		if state.String() != string(state) {
			t.Fatalf("String() = %q, want %q", state.String(), state)
		}
		if _, exists := seen[state]; exists {
			t.Fatalf("duplicate outbox health state %q", state)
		}
		seen[state] = struct{}{}
	}
}

func TestOutboxHealthStateRejectsUnknownValues(t *testing.T) {
	t.Parallel()

	for _, state := range []OutboxHealthState{
		"",
		"unknown",
		"healthy",
		"direct",
	} {
		if state.Valid() {
			t.Fatalf("Valid() = true for %q", state)
		}
	}
}

func TestOutboxHealthValidateAcceptsCompleteObservation(t *testing.T) {
	t.Parallel()

	health := OutboxHealth{
		State:           OutboxHealthRunning,
		Queued:          1,
		Dispatching:     2,
		Accepted:        3,
		FailedRetryable: 4,
		FailedPermanent: 5,
		Uncertain:       6,
		Canceled:        7,
	}
	if err := health.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestOutboxHealthValidateRejectsUnknownState(t *testing.T) {
	t.Parallel()

	health := OutboxHealth{State: OutboxHealthState("unknown")}
	if err := health.Validate(); !errors.Is(err, ErrInvalidOutboxHealth) {
		t.Fatalf("Validate() error = %v, want ErrInvalidOutboxHealth", err)
	}
}

func TestOutboxHealthValidateRejectsNegativeCounters(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*OutboxHealth)
	}{
		{name: "queued", mutate: func(h *OutboxHealth) { h.Queued = -1 }},
		{name: "dispatching", mutate: func(h *OutboxHealth) { h.Dispatching = -1 }},
		{name: "accepted", mutate: func(h *OutboxHealth) { h.Accepted = -1 }},
		{
			name:   "failed retryable",
			mutate: func(h *OutboxHealth) { h.FailedRetryable = -1 },
		},
		{
			name:   "failed permanent",
			mutate: func(h *OutboxHealth) { h.FailedPermanent = -1 },
		},
		{name: "uncertain", mutate: func(h *OutboxHealth) { h.Uncertain = -1 }},
		{name: "canceled", mutate: func(h *OutboxHealth) { h.Canceled = -1 }},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			health := OutboxHealth{State: OutboxHealthRunning}
			test.mutate(&health)
			if err := health.Validate(); !errors.Is(
				err,
				ErrInvalidOutboxHealth,
			) {
				t.Fatalf("Validate() error = %v, want ErrInvalidOutboxHealth", err)
			}
		})
	}
}

func TestOutboxHealthContainsExpectedFieldsOnly(t *testing.T) {
	t.Parallel()

	healthType := reflect.TypeOf(OutboxHealth{})

	want := []string{
		"State",
		"Queued",
		"Dispatching",
		"Accepted",
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
		t.Fatalf("OutboxHealth fields = %#v, want %#v", got, want)
	}
}

func TestOutboxHealthDoesNotExposeIdentifiersOrPayload(t *testing.T) {
	t.Parallel()

	healthType := reflect.TypeOf(OutboxHealth{})

	for _, forbidden := range []string{
		"ID",
		"EntryID",
		"AccountKey",
		"ChatID",
		"DatabaseID",
		"DatabasePath",
		"InstanceID",
		"Path",
		"Text",
		"Payload",
		"EncryptedText",
		"Ciphertext",
		"Nonce",
		"Error",
		"ErrorMessage",
		"ProviderMessage",
		"LeaseOwner",
		"TelegramMessageID",
	} {
		if _, exists := healthType.FieldByName(forbidden); exists {
			t.Fatalf("OutboxHealth contains forbidden field %q", forbidden)
		}
	}
}

func TestNoopRecorderAcceptsOutboxHealth(t *testing.T) {
	t.Parallel()

	var healthRecorder OutboxHealthRecorder = Noop{}
	healthRecorder.SetOutboxHealth(OutboxHealth{State: OutboxHealthRunning})
}

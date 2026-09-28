package application

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"telecli/internal/telemetry/recorder"
)

type h7c1RecordingOutboxHealthRecorder struct {
	health recorder.OutboxHealth
	calls  int
}

func (r *h7c1RecordingOutboxHealthRecorder) SetOutboxHealth(
	health recorder.OutboxHealth,
) {
	r.calls++
	r.health = health
}

func TestNewMessageDeliveryHealthRecorderRejectsNilRecorder(t *testing.T) {
	t.Parallel()

	healthRecorder, err := NewMessageDeliveryHealthRecorder(nil)
	if err == nil {
		t.Fatal("NewMessageDeliveryHealthRecorder() error = nil, want non-nil")
	}
	if healthRecorder != nil {
		t.Fatalf("recorder = %#v, want nil", healthRecorder)
	}
}

func TestMessageDeliveryHealthRecorderRecordsKnownState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		health MessageDeliveryHealthState
		want   recorder.OutboxHealthState
	}{
		{name: "starting", health: MessageDeliveryHealthStarting, want: recorder.OutboxHealthStarting},
		{name: "running", health: MessageDeliveryHealthRunning, want: recorder.OutboxHealthRunning},
		{name: "stopping", health: MessageDeliveryHealthStopping, want: recorder.OutboxHealthStopping},
		{name: "stopped", health: MessageDeliveryHealthStopped, want: recorder.OutboxHealthStopped},
		{name: "failed", health: MessageDeliveryHealthFailed, want: recorder.OutboxHealthFailed},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			sink := &h7c1RecordingOutboxHealthRecorder{}
			healthRecorder, err := NewMessageDeliveryHealthRecorder(sink)
			if err != nil {
				t.Fatalf("NewMessageDeliveryHealthRecorder() error = %v", err)
			}
			if err := healthRecorder.RecordMessageDeliveryHealth(
				context.Background(),
				MessageDeliveryHealth{State: test.health},
			); err != nil {
				t.Fatalf("RecordMessageDeliveryHealth() error = %v", err)
			}
			if sink.calls != 1 {
				t.Fatalf("recorder calls = %d, want 1", sink.calls)
			}
			if sink.health.State != test.want {
				t.Fatalf("recorded state = %q, want %q", sink.health.State, test.want)
			}
		})
	}
}

func TestMessageDeliveryHealthRecorderRecordsAllCounters(t *testing.T) {
	t.Parallel()

	sink := &h7c1RecordingOutboxHealthRecorder{}
	healthRecorder, err := NewMessageDeliveryHealthRecorder(sink)
	if err != nil {
		t.Fatalf("NewMessageDeliveryHealthRecorder() error = %v", err)
	}

	health := MessageDeliveryHealth{
		State:           MessageDeliveryHealthRunning,
		Queued:          1,
		Dispatching:     2,
		Accepted:        3,
		FailedRetryable: 4,
		FailedPermanent: 5,
		Uncertain:       6,
		Canceled:        7,
	}
	if err := healthRecorder.RecordMessageDeliveryHealth(
		context.Background(),
		health,
	); err != nil {
		t.Fatalf("RecordMessageDeliveryHealth() error = %v", err)
	}

	want := recorder.OutboxHealth{
		State:           recorder.OutboxHealthRunning,
		Queued:          1,
		Dispatching:     2,
		Accepted:        3,
		FailedRetryable: 4,
		FailedPermanent: 5,
		Uncertain:       6,
		Canceled:        7,
	}
	if sink.health != want {
		t.Fatalf("recorded health = %#v, want %#v", sink.health, want)
	}
}

func TestMessageDeliveryHealthRecorderRecordsZeroCounts(t *testing.T) {
	t.Parallel()

	sink := &h7c1RecordingOutboxHealthRecorder{}
	healthRecorder, err := NewMessageDeliveryHealthRecorder(sink)
	if err != nil {
		t.Fatalf("NewMessageDeliveryHealthRecorder() error = %v", err)
	}

	if err := healthRecorder.RecordMessageDeliveryHealth(
		context.Background(),
		MessageDeliveryHealth{State: MessageDeliveryHealthRunning},
	); err != nil {
		t.Fatalf("RecordMessageDeliveryHealth() error = %v", err)
	}
	if sink.health != (recorder.OutboxHealth{
		State: recorder.OutboxHealthRunning,
	}) {
		t.Fatalf("recorded health = %#v, want zero counters", sink.health)
	}
}

func TestMessageDeliveryHealthRecorderRecordsFailedState(t *testing.T) {
	t.Parallel()

	sink := &h7c1RecordingOutboxHealthRecorder{}
	healthRecorder, err := NewMessageDeliveryHealthRecorder(sink)
	if err != nil {
		t.Fatalf("NewMessageDeliveryHealthRecorder() error = %v", err)
	}

	if err := healthRecorder.RecordMessageDeliveryHealth(
		context.Background(),
		MessageDeliveryHealth{
			State:     MessageDeliveryHealthFailed,
			Uncertain: 4,
		},
	); err != nil {
		t.Fatalf("RecordMessageDeliveryHealth() error = %v", err)
	}
	if sink.health.State != recorder.OutboxHealthFailed {
		t.Fatalf("recorded state = %q, want failed", sink.health.State)
	}
	if sink.health.Uncertain != 4 {
		t.Fatalf("recorded uncertain = %d, want 4", sink.health.Uncertain)
	}
}

func TestMessageDeliveryHealthRecorderRejectsUnknownState(t *testing.T) {
	t.Parallel()

	sink := &h7c1RecordingOutboxHealthRecorder{}
	healthRecorder, err := NewMessageDeliveryHealthRecorder(sink)
	if err != nil {
		t.Fatalf("NewMessageDeliveryHealthRecorder() error = %v", err)
	}

	err = healthRecorder.RecordMessageDeliveryHealth(
		context.Background(),
		MessageDeliveryHealth{
			State: MessageDeliveryHealthState("unknown"),
		},
	)
	if err == nil {
		t.Fatal("RecordMessageDeliveryHealth() error = nil, want non-nil")
	}
	if sink.calls != 0 {
		t.Fatalf("recorder calls = %d, want 0", sink.calls)
	}
}

func TestMessageDeliveryHealthRecorderRejectsNegativeCounters(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		health MessageDeliveryHealth
	}{
		{
			name:   "queued",
			health: MessageDeliveryHealth{Queued: -1},
		},
		{
			name:   "dispatching",
			health: MessageDeliveryHealth{Dispatching: -1},
		},
		{
			name:   "accepted",
			health: MessageDeliveryHealth{Accepted: -1},
		},
		{
			name:   "failed retryable",
			health: MessageDeliveryHealth{FailedRetryable: -1},
		},
		{
			name:   "failed permanent",
			health: MessageDeliveryHealth{FailedPermanent: -1},
		},
		{
			name:   "uncertain",
			health: MessageDeliveryHealth{Uncertain: -1},
		},
		{
			name:   "canceled",
			health: MessageDeliveryHealth{Canceled: -1},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			sink := &h7c1RecordingOutboxHealthRecorder{}
			healthRecorder, err := NewMessageDeliveryHealthRecorder(sink)
			if err != nil {
				t.Fatalf("NewMessageDeliveryHealthRecorder() error = %v", err)
			}
			test.health.State = MessageDeliveryHealthRunning

			recordErr := healthRecorder.RecordMessageDeliveryHealth(
				context.Background(),
				test.health,
			)
			if recordErr == nil {
				t.Fatal("RecordMessageDeliveryHealth() error = nil, want non-nil")
			}
			if !errors.Is(recordErr, recorder.ErrInvalidOutboxHealth) {
				t.Fatalf(
					"error = %v, want ErrInvalidOutboxHealth",
					recordErr,
				)
			}
			if sink.calls != 0 {
				t.Fatalf("recorder calls = %d, want 0", sink.calls)
			}
		})
	}
}

func TestMessageDeliveryHealthRecorderPropagatesCanceledContext(t *testing.T) {
	t.Parallel()

	sink := &h7c1RecordingOutboxHealthRecorder{}
	healthRecorder, err := NewMessageDeliveryHealthRecorder(sink)
	if err != nil {
		t.Fatalf("NewMessageDeliveryHealthRecorder() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := healthRecorder.RecordMessageDeliveryHealth(
		ctx,
		MessageDeliveryHealth{State: MessageDeliveryHealthRunning},
	); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestMessageDeliveryHealthRecorderDoesNotCallRecorderWithCanceledContext(t *testing.T) {
	t.Parallel()

	sink := &h7c1RecordingOutboxHealthRecorder{}
	healthRecorder, err := NewMessageDeliveryHealthRecorder(sink)
	if err != nil {
		t.Fatalf("NewMessageDeliveryHealthRecorder() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_ = healthRecorder.RecordMessageDeliveryHealth(
		ctx,
		MessageDeliveryHealth{State: MessageDeliveryHealthRunning},
	)
	if sink.calls != 0 {
		t.Fatalf("recorder calls = %d, want 0", sink.calls)
	}
}

func TestMessageDeliveryHealthRecorderRejectsNilContext(t *testing.T) {
	t.Parallel()

	sink := &h7c1RecordingOutboxHealthRecorder{}
	healthRecorder, err := NewMessageDeliveryHealthRecorder(sink)
	if err != nil {
		t.Fatalf("NewMessageDeliveryHealthRecorder() error = %v", err)
	}

	if err := healthRecorder.RecordMessageDeliveryHealth(
		nil,
		MessageDeliveryHealth{State: MessageDeliveryHealthRunning},
	); err == nil {
		t.Fatal("RecordMessageDeliveryHealth() error = nil, want non-nil")
	}
	if sink.calls != 0 {
		t.Fatalf("recorder calls = %d, want 0", sink.calls)
	}
}

func TestMessageDeliveryHealthRecorderNilReceiver(t *testing.T) {
	t.Parallel()

	var healthRecorder *messageDeliveryHealthRecorder
	if err := healthRecorder.RecordMessageDeliveryHealth(
		context.Background(),
		MessageDeliveryHealth{State: MessageDeliveryHealthRunning},
	); !errors.Is(
		err,
		ErrMessageDeliveryHealthTelemetryUnavailable,
	) {
		t.Fatalf("error = %v, want ErrMessageDeliveryHealthTelemetryUnavailable", err)
	}
}

func TestMessageDeliveryHealthRecorderAcceptsNoopRecorder(t *testing.T) {
	t.Parallel()

	healthRecorder, err := NewMessageDeliveryHealthRecorder(
		recorder.NewNoopOutboxHealth(),
	)
	if err != nil {
		t.Fatalf("NewMessageDeliveryHealthRecorder() error = %v", err)
	}
	if err := healthRecorder.RecordMessageDeliveryHealth(
		context.Background(),
		MessageDeliveryHealth{State: MessageDeliveryHealthRunning},
	); err != nil {
		t.Fatalf("RecordMessageDeliveryHealth() error = %v", err)
	}
}

func TestMessageDeliveryHealthTelemetryContainsExpectedFieldsOnly(t *testing.T) {
	t.Parallel()

	telemetryType := reflect.TypeOf(recorder.OutboxHealth{})

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

	got := make([]string, 0, telemetryType.NumField())
	for index := 0; index < telemetryType.NumField(); index++ {
		got = append(got, telemetryType.Field(index).Name)
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("telemetry fields = %#v, want %#v", got, want)
	}
}

func TestMessageDeliveryHealthTelemetryDoesNotRecordIdentifiersOrPayload(t *testing.T) {
	t.Parallel()

	const (
		entryID    = "entry-secret"
		accountKey = "account-secret"
	)

	sink := &h7c1RecordingOutboxHealthRecorder{}
	healthRecorder, err := NewMessageDeliveryHealthRecorder(sink)
	if err != nil {
		t.Fatalf("NewMessageDeliveryHealthRecorder() error = %v", err)
	}

	health := MessageDeliveryHealth{
		State:           MessageDeliveryHealthRunning,
		Queued:          1,
		FailedPermanent: 1,
	}
	if err := healthRecorder.RecordMessageDeliveryHealth(
		context.Background(),
		health,
	); err != nil {
		t.Fatalf("RecordMessageDeliveryHealth() error = %v", err)
	}

	printed := strings.ToLower(fmt.Sprintf("%#v", sink.health))
	for _, forbidden := range []string{
		entryID,
		accountKey,
		"text",
		"payload",
		"encryptedtext",
		"ciphertext",
		"nonce",
		"accountkey",
		"chatid",
		"databaseid",
		"databasepath",
		"instanceid",
		"providermessage",
		"errormessage",
		"telegrammessageid",
	} {
		if strings.Contains(printed, forbidden) {
			t.Fatalf("recorded telemetry %q exposes %q", printed, forbidden)
		}
	}
}

func TestMessageDeliveryHealthTelemetryDoesNotRecordRuntimeError(t *testing.T) {
	t.Parallel()

	const secret = "dispatcher failed: sqlite /var/tele/outbox.db"

	sink := &h7c1RecordingOutboxHealthRecorder{}
	healthRecorder, err := NewMessageDeliveryHealthRecorder(sink)
	if err != nil {
		t.Fatalf("NewMessageDeliveryHealthRecorder() error = %v", err)
	}

	// The runtime error is only used to build the health state; its text must
	// never reach telemetry.
	runtimeErr := errors.New(secret)
	if err := healthRecorder.RecordMessageDeliveryHealth(
		context.Background(),
		MessageDeliveryHealth{
			State:     MessageDeliveryHealthFailed,
			Uncertain: int64(len(runtimeErr.Error()) / 100),
		},
	); err != nil {
		t.Fatalf("RecordMessageDeliveryHealth() error = %v", err)
	}

	printed := fmt.Sprintf("%#v", sink.health)
	if strings.Contains(printed, secret) ||
		strings.Contains(printed, "outbox.db") {
		t.Fatalf("recorded telemetry %q exposes the runtime error", printed)
	}
}

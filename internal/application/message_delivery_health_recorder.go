package application

import (
	"context"
	"errors"
	"fmt"

	"telecli/internal/telemetry/recorder"
)

// ErrMessageDeliveryHealthTelemetryUnavailable reports that a health
// observation cannot be recorded because no telemetry recorder is wired.
var ErrMessageDeliveryHealthTelemetryUnavailable = errors.New(
	"message delivery health telemetry is unavailable",
)

// MessageDeliveryHealthRecorder records durable delivery health.
//
// It is a narrow application contract so callers do not depend on a concrete
// telemetry backend. Recording is best effort: an error from this method never
// changes delivery behavior.
type MessageDeliveryHealthRecorder interface {
	RecordMessageDeliveryHealth(
		ctx context.Context,
		health MessageDeliveryHealth,
	) error
}

type messageDeliveryHealthRecorder struct {
	recorder recorder.OutboxHealthRecorder
}

// NewMessageDeliveryHealthRecorder adapts application health to the existing
// telemetry recorder.
func NewMessageDeliveryHealthRecorder(
	healthRecorder recorder.OutboxHealthRecorder,
) (MessageDeliveryHealthRecorder, error) {
	if healthRecorder == nil {
		return nil, errors.New("telemetry recorder is required")
	}
	return &messageDeliveryHealthRecorder{recorder: healthRecorder}, nil
}

func (r *messageDeliveryHealthRecorder) RecordMessageDeliveryHealth(
	ctx context.Context,
	health MessageDeliveryHealth,
) error {
	if r == nil || r.recorder == nil {
		return ErrMessageDeliveryHealthTelemetryUnavailable
	}
	if ctx == nil {
		return errors.New("record message delivery health: context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	observation, err := newOutboxHealthObservation(health)
	if err != nil {
		return fmt.Errorf("record message delivery health: %w", err)
	}
	if err := observation.Validate(); err != nil {
		return fmt.Errorf("record message delivery health: %w", err)
	}

	r.recorder.SetOutboxHealth(observation)
	return nil
}

// newOutboxHealthObservation projects health onto the telemetry value with an
// explicit allowlist.
//
// The projection is explicit on purpose: a struct copy or a marshalled
// struct could silently add identifiers or payload fields to telemetry. The
// lifecycle mapping is an exhaustive switch instead of an unchecked cast, so
// a new application state cannot pass through unnoticed.
func newOutboxHealthObservation(
	health MessageDeliveryHealth,
) (recorder.OutboxHealth, error) {
	state, err := projectOutboxHealthState(health.State)
	if err != nil {
		return recorder.OutboxHealth{}, err
	}

	return recorder.OutboxHealth{
		State:           state,
		Queued:          health.Queued,
		Dispatching:     health.Dispatching,
		Accepted:        health.Accepted,
		FailedRetryable: health.FailedRetryable,
		FailedPermanent: health.FailedPermanent,
		Uncertain:       health.Uncertain,
		Canceled:        health.Canceled,
	}, nil
}

func projectOutboxHealthState(
	state MessageDeliveryHealthState,
) (recorder.OutboxHealthState, error) {
	switch state {
	case MessageDeliveryHealthStarting:
		return recorder.OutboxHealthStarting, nil

	case MessageDeliveryHealthRunning:
		return recorder.OutboxHealthRunning, nil

	case MessageDeliveryHealthStopping:
		return recorder.OutboxHealthStopping, nil

	case MessageDeliveryHealthStopped:
		return recorder.OutboxHealthStopped, nil

	case MessageDeliveryHealthFailed:
		return recorder.OutboxHealthFailed, nil

	default:
		return "", fmt.Errorf(
			"unsupported message delivery health state %q",
			state,
		)
	}
}

var _ MessageDeliveryHealthRecorder = (*messageDeliveryHealthRecorder)(nil)

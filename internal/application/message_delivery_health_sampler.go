package application

import (
	"context"
	"errors"
	"time"

	"telecli/internal/outbox"
)

// defaultMessageDeliveryHealthSampleInterval is the policy for how often
// durable delivery health is sampled for telemetry.
//
// It is an application policy rather than user configuration: observability
// frequency must not become a delivery setting.
const defaultMessageDeliveryHealthSampleInterval = 30 * time.Second

// MessageDeliveryHealthSamplerConfig tunes the sampling loop.
type MessageDeliveryHealthSamplerConfig struct {
	Interval time.Duration
}

// MessageDeliveryHealthSamplerDeps are the sampler collaborators.
//
// Clock reuses the outbox clock contract so tests can drive ticks without
// sleeping.
type MessageDeliveryHealthSamplerDeps struct {
	Source   MessageDeliveryHealthSource
	Recorder MessageDeliveryHealthRecorder
	Clock    outbox.Clock
}

// MessageDeliveryHealthSampler samples durable delivery health and
// records it as telemetry.
//
// The sampler is an observability component only. It never closes the
// delivery runtime, never cancels it, never changes delivery mode and never
// triggers a retry. A read or record failure is skipped: the next attempt
// happens only after the configured interval, so a failing source cannot
// create a tight loop.
type MessageDeliveryHealthSampler struct {
	source   MessageDeliveryHealthSource
	recorder MessageDeliveryHealthRecorder
	clock    outbox.Clock
	interval time.Duration
}

// NewMessageDeliveryHealthSampler validates the sampler dependencies.
//
// The constructor starts no goroutine: ownership of the sampling goroutine
// belongs to the application lifecycle.
func NewMessageDeliveryHealthSampler(
	cfg MessageDeliveryHealthSamplerConfig,
	deps MessageDeliveryHealthSamplerDeps,
) (*MessageDeliveryHealthSampler, error) {
	if deps.Source == nil {
		return nil, errors.New("message delivery health sampler: source is required")
	}
	if deps.Recorder == nil {
		return nil, errors.New("message delivery health sampler: recorder is required")
	}
	if deps.Clock == nil {
		return nil, errors.New("message delivery health sampler: clock is required")
	}
	if cfg.Interval <= 0 {
		return nil, errors.New(
			"message delivery health sampler: interval must be positive",
		)
	}
	return &MessageDeliveryHealthSampler{
		source:   deps.Source,
		recorder: deps.Recorder,
		clock:    deps.Clock,
		interval: cfg.Interval,
	}, nil
}

// Run samples health until the context is canceled.
//
// Cancellation is a normal shutdown, so Run returns nil. A nil receiver is
// also a no-op so that an absent sampler can never panic its owner.
func (s *MessageDeliveryHealthSampler) Run(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if ctx == nil {
		return errors.New("message delivery health sampler: context is required")
	}
	if ctx.Err() != nil {
		return nil
	}

	for {
		s.sample(ctx)

		tick := s.clock.After(s.interval)
		select {
		case <-ctx.Done():
			return nil
		case <-tick:
		}
	}
}

// sample performs one read and one record.
//
// A read error skips the record, and the raw error is neither stored nor
// forwarded to telemetry. The context is rechecked before recording so a
// shutdown that overlapped the read cannot emit a stale observation.
func (s *MessageDeliveryHealthSampler) sample(ctx context.Context) {
	health, err := s.source.ReadMessageDeliveryHealth(ctx)
	if err != nil {
		return
	}
	if ctx.Err() != nil {
		return
	}

	// Recording is best effort: a validation or context error must not end
	// the sampling loop.
	_ = s.recorder.RecordMessageDeliveryHealth(ctx, health)
}

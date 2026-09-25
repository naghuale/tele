package outbox

import (
	"math/rand"
	"time"
)

// Backoff computes the delay between retry attempts.
//
// The policy uses a fixed sequence of delays with optional symmetric
// jitter. Once the sequence is exhausted, automatic retry stops.
type Backoff struct {
	// Delays contains the delay after attempt N at index N-1.
	Delays []time.Duration

	// Jitter is the maximum random offset added to the base delay.
	// The offset is selected from [-Jitter, +Jitter].
	Jitter time.Duration

	// Rand is the randomness source. Tests provide a deterministic
	// source. When nil, NextDelay creates a time-seeded source.
	Rand *rand.Rand
}

// DefaultBackoff returns the default retry schedule.
func DefaultBackoff() Backoff {
	return Backoff{
		Delays: []time.Duration{
			5 * time.Second,
			15 * time.Second,
			time.Minute,
			5 * time.Minute,
			15 * time.Minute,
		},
		Jitter: 2 * time.Second,
	}
}

// NextDelay returns the delay after attemptCount attempts.
//
// The boolean result is false when attemptCount is invalid or the
// configured retry schedule has been exhausted.
func (b Backoff) NextDelay(
	attemptCount int,
) (time.Duration, bool) {
	if attemptCount <= 0 {
		return 0, false
	}

	index := attemptCount - 1
	if index >= len(b.Delays) {
		return 0, false
	}

	delay := b.Delays[index]

	if b.Jitter > 0 {
		delay += b.jitter()
		if delay < 0 {
			delay = 0
		}
	}

	return delay, true
}

func (b Backoff) jitter() time.Duration {
	span := int64(b.Jitter)
	if span <= 0 {
		return 0
	}

	random := b.Rand
	if random == nil {
		random = rand.New(
			rand.NewSource(
				time.Now().UnixNano(),
			),
		)
	}

	return time.Duration(
		random.Int63n(2*span+1) - span,
	)
}

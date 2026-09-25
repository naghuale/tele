package outbox

import (
	"math/rand"
	"testing"
	"time"
)

func TestBackoffDefaultSequence(t *testing.T) {
	policy := Backoff{
		Delays: []time.Duration{
			5 * time.Second,
			15 * time.Second,
			time.Minute,
			5 * time.Minute,
			15 * time.Minute,
		},
	}

	want := []time.Duration{
		5 * time.Second,
		15 * time.Second,
		time.Minute,
		5 * time.Minute,
		15 * time.Minute,
	}

	for index, expected := range want {
		got, ok := policy.NextDelay(index + 1)
		if !ok {
			t.Fatalf(
				"attempt %d: not scheduled",
				index+1,
			)
		}

		if got != expected {
			t.Fatalf(
				"attempt %d: got %v, want %v",
				index+1,
				got,
				expected,
			)
		}
	}
}

func TestBackoffBeyondPolicyStops(t *testing.T) {
	policy := Backoff{
		Delays: []time.Duration{
			5 * time.Second,
			15 * time.Second,
		},
	}

	for _, attempt := range []int{-1, 0, 3} {
		if _, ok := policy.NextDelay(attempt); ok {
			t.Fatalf(
				"attempt %d should not be scheduled",
				attempt,
			)
		}
	}
}

func TestBackoffJitterDeterministic(t *testing.T) {
	policy := Backoff{
		Delays: []time.Duration{
			10 * time.Second,
		},
		Jitter: 2 * time.Second,
		Rand: rand.New(
			rand.NewSource(1),
		),
	}

	got, ok := policy.NextDelay(1)
	if !ok {
		t.Fatal("attempt 1 should be scheduled")
	}

	if got < 8*time.Second ||
		got > 12*time.Second {
		t.Fatalf(
			"delay %v outside jitter range",
			got,
		)
	}
}

func TestBackoffJitterNeverNegative(t *testing.T) {
	policy := Backoff{
		Delays: []time.Duration{
			time.Second,
		},
		Jitter: 5 * time.Second,
		Rand: rand.New(
			rand.NewSource(1),
		),
	}

	for index := 0; index < 100; index++ {
		got, ok := policy.NextDelay(1)
		if !ok {
			t.Fatal("attempt should be scheduled")
		}

		if got < 0 {
			t.Fatalf(
				"negative delay: %v",
				got,
			)
		}
	}
}

func TestDefaultBackoffHasFiveAttempts(t *testing.T) {
	policy := DefaultBackoff()

	if len(policy.Delays) != 5 {
		t.Fatalf(
			"len = %d, want 5",
			len(policy.Delays),
		)
	}

	for attempt := 1; attempt <= 5; attempt++ {
		if _, ok := policy.NextDelay(attempt); !ok {
			t.Fatalf(
				"attempt %d should be scheduled",
				attempt,
			)
		}
	}

	if _, ok := policy.NextDelay(6); ok {
		t.Fatal(
			"attempt 6 should not be scheduled",
		)
	}
}

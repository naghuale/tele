package outbox

import "time"

// Clock abstracts wall-clock time and timer creation.
//
// Dispatcher tests use a fake clock so retries and polling do not
// depend on real sleeps.
type Clock interface {
	Now() time.Time
	After(time.Duration) <-chan time.Time
}

// SystemClock uses the standard library clock.
type SystemClock struct{}

// Now implements Clock.
func (SystemClock) Now() time.Time {
	return time.Now()
}

// After implements Clock.
func (SystemClock) After(
	delay time.Duration,
) <-chan time.Time {
	return time.After(delay)
}

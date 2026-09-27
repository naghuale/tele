package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"telecli/internal/outbox"
)

// h7c2ManualClock hands out one channel per After call so a stale tick cannot
// be reused by a later cycle.
//
// Waiters live in a FIFO queue and notifications are coalesced, so a test can
// either take the next tick channel or simply wait for the next cycle without
// consuming a channel the sampler is about to block on.
type h7c2ManualClock struct {
	mu        sync.Mutex
	waiters   []chan time.Time
	intervals []time.Duration
	notify    chan struct{}
}

func newH7c2ManualClock() *h7c2ManualClock {
	return &h7c2ManualClock{notify: make(chan struct{}, 1)}
}

func (c *h7c2ManualClock) Now() time.Time {
	return time.Unix(1700000000, 0).UTC()
}

func (c *h7c2ManualClock) After(duration time.Duration) <-chan time.Time {
	tick := make(chan time.Time, 1)
	c.mu.Lock()
	c.waiters = append(c.waiters, tick)
	c.intervals = append(c.intervals, duration)
	c.mu.Unlock()

	select {
	case c.notify <- struct{}{}:
	default:
	}
	return tick
}

// next returns the channel of the next requested timer.
func (c *h7c2ManualClock) next(t *testing.T) chan time.Time {
	t.Helper()
	for {
		c.mu.Lock()
		if len(c.waiters) > 0 {
			tick := c.waiters[0]
			c.waiters = c.waiters[1:]
			c.mu.Unlock()
			return tick
		}
		c.mu.Unlock()

		select {
		case <-c.notify:
		case <-time.After(2 * time.Second):
			t.Fatal("sampler did not request a timer")
		}
	}
}

// awaitCycle waits until the sampler finished a sample and requested its next
// timer, without taking that timer.
func (c *h7c2ManualClock) awaitCycle(t *testing.T) {
	t.Helper()
	for {
		c.mu.Lock()
		pending := len(c.waiters)
		c.mu.Unlock()
		if pending > 0 {
			return
		}

		select {
		case <-c.notify:
		case <-time.After(2 * time.Second):
			t.Fatal("sampler did not request another timer")
		}
	}
}

func (c *h7c2ManualClock) observedIntervals() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.intervals...)
}

type h7c2HealthSource struct {
	mu       sync.Mutex
	health   []MessageDeliveryHealth
	err      error
	reads    int
	blockCtx bool
	started  chan struct{}
	release  chan struct{}
}

func (s *h7c2HealthSource) State() MessageDeliveryHealthState {
	return MessageDeliveryHealthRunning
}

func (s *h7c2HealthSource) ReadMessageDeliveryHealth(
	ctx context.Context,
) (MessageDeliveryHealth, error) {
	s.mu.Lock()
	s.reads++
	index := s.reads - 1
	health := MessageDeliveryHealth{State: MessageDeliveryHealthRunning}
	if len(s.health) > 0 {
		if index >= len(s.health) {
			index = len(s.health) - 1
		}
		health = s.health[index]
	}
	err := s.err
	blocking := s.blockCtx
	started := s.started
	release := s.release
	s.mu.Unlock()

	if blocking {
		if started != nil {
			select {
			case started <- struct{}{}:
			default:
			}
		}
		select {
		case <-ctx.Done():
			return MessageDeliveryHealth{}, ctx.Err()
		case <-release:
		}
	}
	if err != nil {
		return MessageDeliveryHealth{}, err
	}
	return health, nil
}

func (s *h7c2HealthSource) readCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reads
}

type h7c2RecordingHealthRecorder struct {
	mu       sync.Mutex
	observed []MessageDeliveryHealth
	err      error
}

func (r *h7c2RecordingHealthRecorder) RecordMessageDeliveryHealth(
	_ context.Context,
	health MessageDeliveryHealth,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	r.observed = append(r.observed, health)
	return nil
}

func (r *h7c2RecordingHealthRecorder) records() []MessageDeliveryHealth {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]MessageDeliveryHealth(nil), r.observed...)
}

func h7c2Sampler(
	t *testing.T,
	source MessageDeliveryHealthSource,
	recorder MessageDeliveryHealthRecorder,
	clock outbox.Clock,
	interval time.Duration,
) *MessageDeliveryHealthSampler {
	t.Helper()
	sampler, err := NewMessageDeliveryHealthSampler(
		MessageDeliveryHealthSamplerConfig{Interval: interval},
		MessageDeliveryHealthSamplerDeps{
			Source:   source,
			Recorder: recorder,
			Clock:    clock,
		},
	)
	if err != nil {
		t.Fatalf("NewMessageDeliveryHealthSampler() error = %v", err)
	}
	return sampler
}

func h7c2RunningHealth() MessageDeliveryHealth {
	return MessageDeliveryHealth{
		State:           MessageDeliveryHealthRunning,
		Queued:          1,
		Dispatching:     1,
		Accepted:        1,
		FailedRetryable: 1,
		FailedPermanent: 1,
		Uncertain:       1,
		Canceled:        1,
	}
}

// h7c2Start runs the sampler in a goroutine and returns a stop helper.
func h7c2Start(
	t *testing.T,
	sampler *MessageDeliveryHealthSampler,
) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- sampler.Run(ctx)
	}()
	return cancel, done
}

func TestNewMessageDeliveryHealthSamplerRejectsNilSource(t *testing.T) {
	t.Parallel()

	sampler, err := NewMessageDeliveryHealthSampler(
		MessageDeliveryHealthSamplerConfig{Interval: time.Second},
		MessageDeliveryHealthSamplerDeps{
			Recorder: &h7c2RecordingHealthRecorder{},
			Clock:    outbox.SystemClock{},
		},
	)
	if err == nil {
		t.Fatal("NewMessageDeliveryHealthSampler() error = nil, want non-nil")
	}
	if sampler != nil {
		t.Fatalf("sampler = %#v, want nil", sampler)
	}
}

func TestNewMessageDeliveryHealthSamplerRejectsNilRecorder(t *testing.T) {
	t.Parallel()

	sampler, err := NewMessageDeliveryHealthSampler(
		MessageDeliveryHealthSamplerConfig{Interval: time.Second},
		MessageDeliveryHealthSamplerDeps{
			Source: &h7c2HealthSource{},
			Clock:  outbox.SystemClock{},
		},
	)
	if err == nil {
		t.Fatal("NewMessageDeliveryHealthSampler() error = nil, want non-nil")
	}
	if sampler != nil {
		t.Fatalf("sampler = %#v, want nil", sampler)
	}
}

func TestNewMessageDeliveryHealthSamplerRejectsNilClock(t *testing.T) {
	t.Parallel()

	sampler, err := NewMessageDeliveryHealthSampler(
		MessageDeliveryHealthSamplerConfig{Interval: time.Second},
		MessageDeliveryHealthSamplerDeps{
			Source:   &h7c2HealthSource{},
			Recorder: &h7c2RecordingHealthRecorder{},
		},
	)
	if err == nil {
		t.Fatal("NewMessageDeliveryHealthSampler() error = nil, want non-nil")
	}
	if sampler != nil {
		t.Fatalf("sampler = %#v, want nil", sampler)
	}
}

func TestNewMessageDeliveryHealthSamplerRejectsNonPositiveInterval(t *testing.T) {
	t.Parallel()

	for _, interval := range []time.Duration{0, -time.Second} {
		sampler, err := NewMessageDeliveryHealthSampler(
			MessageDeliveryHealthSamplerConfig{Interval: interval},
			MessageDeliveryHealthSamplerDeps{
				Source:   &h7c2HealthSource{},
				Recorder: &h7c2RecordingHealthRecorder{},
				Clock:    outbox.SystemClock{},
			},
		)
		if err == nil {
			t.Fatalf("interval %s accepted", interval)
		}
		if sampler != nil {
			t.Fatalf("sampler = %#v, want nil", sampler)
		}
	}
}

func TestNewMessageDeliveryHealthSamplerUsesDefaultIntervalPolicy(t *testing.T) {
	t.Parallel()

	if defaultMessageDeliveryHealthSampleInterval <= 0 {
		t.Fatalf(
			"default interval = %s, want positive",
			defaultMessageDeliveryHealthSampleInterval,
		)
	}

	clock := newH7c2ManualClock()
	sampler := h7c2Sampler(
		t,
		&h7c2HealthSource{},
		&h7c2RecordingHealthRecorder{},
		clock,
		defaultMessageDeliveryHealthSampleInterval,
	)
	if sampler.interval != defaultMessageDeliveryHealthSampleInterval {
		t.Fatalf("interval = %s, want %s", sampler.interval, defaultMessageDeliveryHealthSampleInterval)
	}
}

func TestMessageDeliveryHealthSamplerRecordsInitialSnapshot(t *testing.T) {
	t.Parallel()

	clock := newH7c2ManualClock()
	source := &h7c2HealthSource{health: []MessageDeliveryHealth{h7c2RunningHealth()}}
	recorder := &h7c2RecordingHealthRecorder{}
	sampler := h7c2Sampler(t, source, recorder, clock, time.Second)

	cancel, done := h7c2Start(t, sampler)
	tick := clock.next(t)
	_ = tick

	if got := len(recorder.records()); got != 1 {
		t.Fatalf("records = %d, want 1", got)
	}
	if source.readCount() != 1 {
		t.Fatalf("reads = %d, want 1", source.readCount())
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestMessageDeliveryHealthSamplerRecordsInitialState(t *testing.T) {
	t.Parallel()

	clock := newH7c2ManualClock()
	health := h7c2RunningHealth()
	health.State = MessageDeliveryHealthFailed
	source := &h7c2HealthSource{health: []MessageDeliveryHealth{health}}
	recorder := &h7c2RecordingHealthRecorder{}
	sampler := h7c2Sampler(t, source, recorder, clock, time.Second)

	cancel, done := h7c2Start(t, sampler)
	_ = clock.next(t)

	records := recorder.records()
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	if records[0].State != MessageDeliveryHealthFailed {
		t.Fatalf("recorded state = %q, want failed", records[0].State)
	}

	cancel()
	<-done
}

func TestMessageDeliveryHealthSamplerRecordsAllInitialCounters(t *testing.T) {
	t.Parallel()

	clock := newH7c2ManualClock()
	source := &h7c2HealthSource{health: []MessageDeliveryHealth{h7c2RunningHealth()}}
	recorder := &h7c2RecordingHealthRecorder{}
	sampler := h7c2Sampler(t, source, recorder, clock, time.Second)

	cancel, done := h7c2Start(t, sampler)
	_ = clock.next(t)

	records := recorder.records()
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	if records[0] != h7c2RunningHealth() {
		t.Fatalf("recorded health = %#v, want %#v", records[0], h7c2RunningHealth())
	}

	cancel()
	<-done
}

func TestMessageDeliveryHealthSamplerRecordsPeriodicSnapshots(t *testing.T) {
	t.Parallel()

	clock := newH7c2ManualClock()
	source := &h7c2HealthSource{health: []MessageDeliveryHealth{h7c2RunningHealth()}}
	recorder := &h7c2RecordingHealthRecorder{}
	sampler := h7c2Sampler(t, source, recorder, clock, time.Second)

	cancel, done := h7c2Start(t, sampler)

	for cycle := 0; cycle < 3; cycle++ {
		tick := clock.next(t)
		tick <- time.Now()
		// The next timer request proves the cycle finished its sample.
		clock.awaitCycle(t)
	}

	if got := len(recorder.records()); got != 4 {
		t.Fatalf("records = %d, want 4 (initial plus three ticks)", got)
	}

	cancel()
	<-done
}

func TestMessageDeliveryHealthSamplerUsesUpdatedSnapshot(t *testing.T) {
	t.Parallel()

	clock := newH7c2ManualClock()
	first := h7c2RunningHealth()
	first.Queued = 1
	second := h7c2RunningHealth()
	second.Queued = 5
	source := &h7c2HealthSource{health: []MessageDeliveryHealth{first, second}}
	recorder := &h7c2RecordingHealthRecorder{}
	sampler := h7c2Sampler(t, source, recorder, clock, time.Second)

	cancel, done := h7c2Start(t, sampler)
	tick := clock.next(t)
	tick <- time.Now()
	clock.awaitCycle(t)

	records := recorder.records()
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2", len(records))
	}
	if records[0].Queued != 1 || records[1].Queued != 5 {
		t.Fatalf("recorded queued = %d then %d, want 1 then 5", records[0].Queued, records[1].Queued)
	}

	cancel()
	<-done
}

func TestMessageDeliveryHealthSamplerWaitsForInterval(t *testing.T) {
	t.Parallel()

	clock := newH7c2ManualClock()
	source := &h7c2HealthSource{health: []MessageDeliveryHealth{h7c2RunningHealth()}}
	sampler := h7c2Sampler(
		t,
		source,
		&h7c2RecordingHealthRecorder{},
		clock,
		7*time.Second,
	)

	cancel, done := h7c2Start(t, sampler)
	_ = clock.next(t)

	intervals := clock.observedIntervals()
	if len(intervals) != 1 {
		t.Fatalf("timer requests = %d, want 1", len(intervals))
	}
	if intervals[0] != 7*time.Second {
		t.Fatalf("interval = %s, want 7s", intervals[0])
	}
	if source.readCount() != 1 {
		t.Fatalf("reads = %d, want 1 before the first tick", source.readCount())
	}

	cancel()
	<-done
}

func TestMessageDeliveryHealthSamplerDoesNotTightLoop(t *testing.T) {
	t.Parallel()

	clock := newH7c2ManualClock()
	source := &h7c2HealthSource{health: []MessageDeliveryHealth{h7c2RunningHealth()}}
	sampler := h7c2Sampler(
		t,
		source,
		&h7c2RecordingHealthRecorder{},
		clock,
		time.Minute,
	)

	cancel, done := h7c2Start(t, sampler)
	_ = clock.next(t)

	if got := source.readCount(); got != 1 {
		t.Fatalf("reads = %d, want 1: a sample must wait for the timer", got)
	}

	cancel()
	<-done
}

func TestMessageDeliveryHealthSamplerRequestsOneTimerPerCycle(t *testing.T) {
	t.Parallel()

	clock := newH7c2ManualClock()
	source := &h7c2HealthSource{health: []MessageDeliveryHealth{h7c2RunningHealth()}}
	sampler := h7c2Sampler(t, source, &h7c2RecordingHealthRecorder{}, clock, time.Second)

	cancel, done := h7c2Start(t, sampler)

	tick := clock.next(t)
	tick <- time.Now()
	clock.awaitCycle(t)

	intervals := clock.observedIntervals()
	if len(intervals) != 2 {
		t.Fatalf("timer requests = %d, want 2 (one per cycle plus the pending one)", len(intervals))
	}
	for _, interval := range intervals {
		if interval != time.Second {
			t.Fatalf("interval = %s, want 1s", interval)
		}
	}

	cancel()
	<-done
}

func TestMessageDeliveryHealthSamplerSkipsRecordAfterReadFailure(t *testing.T) {
	t.Parallel()

	clock := newH7c2ManualClock()
	source := &h7c2HealthSource{err: errors.New("read failed")}
	recorder := &h7c2RecordingHealthRecorder{}
	sampler := h7c2Sampler(t, source, recorder, clock, time.Second)

	cancel, done := h7c2Start(t, sampler)
	_ = clock.next(t)

	if got := len(recorder.records()); got != 0 {
		t.Fatalf("records = %d, want 0", got)
	}

	cancel()
	<-done
}

func TestMessageDeliveryHealthSamplerContinuesAfterReadFailure(t *testing.T) {
	t.Parallel()

	clock := newH7c2ManualClock()
	failing := &h7c2FlakySource{
		inner:    &h7c2HealthSource{health: []MessageDeliveryHealth{h7c2RunningHealth()}},
		failures: 1,
	}
	recorder := &h7c2RecordingHealthRecorder{}
	sampler := h7c2Sampler(t, failing, recorder, clock, time.Second)

	cancel, done := h7c2Start(t, sampler)
	tick := clock.next(t)
	if got := len(recorder.records()); got != 0 {
		t.Fatalf("records after failure = %d, want 0", got)
	}

	tick <- time.Now()
	clock.awaitCycle(t)
	if got := len(recorder.records()); got != 1 {
		t.Fatalf("records after recovery = %d, want 1", got)
	}

	cancel()
	<-done
}

func TestMessageDeliveryHealthSamplerWaitsAfterReadFailure(t *testing.T) {
	t.Parallel()

	clock := newH7c2ManualClock()
	source := &h7c2FlakySource{
		inner:    &h7c2HealthSource{health: []MessageDeliveryHealth{h7c2RunningHealth()}},
		failures: 1,
	}
	sampler := h7c2Sampler(t, source, &h7c2RecordingHealthRecorder{}, clock, time.Minute)

	cancel, done := h7c2Start(t, sampler)
	tick := clock.next(t)

	if got := source.readCount(); got != 1 {
		t.Fatalf("reads = %d, want 1", got)
	}

	cancel()
	<-done
	_ = tick
}

func TestMessageDeliveryHealthSamplerHandlesHealthUnavailable(t *testing.T) {
	t.Parallel()

	clock := newH7c2ManualClock()
	source := &h7c2HealthSource{err: ErrMessageDeliveryHealthUnavailable}
	recorder := &h7c2RecordingHealthRecorder{}
	sampler := h7c2Sampler(t, source, recorder, clock, time.Second)

	cancel, done := h7c2Start(t, sampler)
	_ = clock.next(t)

	if got := len(recorder.records()); got != 0 {
		t.Fatalf("records = %d, want 0", got)
	}
	if source.readCount() != 1 {
		t.Fatalf("reads = %d, want 1", source.readCount())
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
}

func TestMessageDeliveryHealthSamplerDoesNotExposeReadError(t *testing.T) {
	t.Parallel()

	const secret = "sqlite: /var/tele/outbox.db: provider failure"

	clock := newH7c2ManualClock()
	source := &h7c2HealthSource{err: errors.New(secret)}
	recorder := &h7c2RecordingHealthRecorder{}
	sampler := h7c2Sampler(t, source, recorder, clock, time.Second)

	cancel, done := h7c2Start(t, sampler)
	_ = clock.next(t)
	cancel()

	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	printed := fmt.Sprintf("%#v", recorder.records())
	if strings.Contains(printed, secret) {
		t.Fatalf("records %q expose the read error", printed)
	}
}

func TestMessageDeliveryHealthSamplerContinuesAfterRecorderFailure(t *testing.T) {
	t.Parallel()

	clock := newH7c2ManualClock()
	source := &h7c2HealthSource{health: []MessageDeliveryHealth{h7c2RunningHealth()}}
	recorder := &h7c2RecordingHealthRecorder{err: errors.New("record rejected")}
	sampler := h7c2Sampler(t, source, recorder, clock, time.Second)

	cancel, done := h7c2Start(t, sampler)
	tick := clock.next(t)
	tick <- time.Now()
	clock.awaitCycle(t)

	if source.readCount() != 2 {
		t.Fatalf("reads = %d, want 2: a record failure must not stop the loop", source.readCount())
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
}

func TestMessageDeliveryHealthSamplerTreatsCanceledContextAsNormal(t *testing.T) {
	t.Parallel()

	clock := newH7c2ManualClock()
	source := &h7c2HealthSource{health: []MessageDeliveryHealth{h7c2RunningHealth()}}
	sampler := h7c2Sampler(
		t,
		source,
		&h7c2RecordingHealthRecorder{},
		clock,
		time.Second,
	)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := sampler.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	if source.readCount() != 0 {
		t.Fatalf("reads = %d, want 0", source.readCount())
	}
}

func TestMessageDeliveryHealthSamplerStopsWhileWaitingForTick(t *testing.T) {
	t.Parallel()

	clock := newH7c2ManualClock()
	source := &h7c2HealthSource{health: []MessageDeliveryHealth{h7c2RunningHealth()}}
	sampler := h7c2Sampler(t, source, &h7c2RecordingHealthRecorder{}, clock, time.Hour)

	cancel, done := h7c2Start(t, sampler)
	_ = clock.next(t)

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run() did not return after cancellation")
	}

	if source.readCount() != 1 {
		t.Fatalf("reads = %d, want 1", source.readCount())
	}
}

func TestMessageDeliveryHealthSamplerDoesNotReadAfterCancellation(t *testing.T) {
	t.Parallel()

	clock := newH7c2ManualClock()
	source := &h7c2HealthSource{health: []MessageDeliveryHealth{h7c2RunningHealth()}}
	sampler := h7c2Sampler(t, source, &h7c2RecordingHealthRecorder{}, clock, time.Second)

	cancel, done := h7c2Start(t, sampler)
	tick := clock.next(t)
	cancel()
	<-done

	// A late tick from the cycle in flight must not start a new read.
	tick <- time.Now()

	if got := source.readCount(); got != 1 {
		t.Fatalf("reads = %d, want 1", got)
	}
}

func TestMessageDeliveryHealthSamplerDoesNotRecordAfterCancellation(t *testing.T) {
	t.Parallel()

	clock := newH7c2ManualClock()
	source := &h7c2HealthSource{health: []MessageDeliveryHealth{h7c2RunningHealth()}}
	recorder := &h7c2RecordingHealthRecorder{}
	sampler := h7c2Sampler(t, source, recorder, clock, time.Second)

	cancel, done := h7c2Start(t, sampler)
	_ = clock.next(t)
	cancel()
	<-done

	if got := len(recorder.records()); got != 1 {
		t.Fatalf("records = %d, want 1", got)
	}
}

func TestMessageDeliveryHealthSamplerInFlightReadFinishesOnCancel(t *testing.T) {
	t.Parallel()

	clock := newH7c2ManualClock()
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	source := &h7c2HealthSource{
		health:   []MessageDeliveryHealth{h7c2RunningHealth()},
		blockCtx: true,
		started:  started,
		release:  release,
	}
	recorder := &h7c2RecordingHealthRecorder{}
	sampler := h7c2Sampler(t, source, recorder, clock, time.Hour)

	cancel, done := h7c2Start(t, sampler)

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("sampler did not start the read")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run() did not return while a read was in flight")
	}

	if got := len(recorder.records()); got != 0 {
		t.Fatalf("records = %d, want 0: a canceled read must not be recorded", got)
	}
	close(release)
}

func TestMessageDeliveryHealthSamplerNilReceiver(t *testing.T) {
	t.Parallel()

	var sampler *MessageDeliveryHealthSampler
	if err := sampler.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
}

func TestMessageDeliveryHealthSamplerRejectsNilContext(t *testing.T) {
	t.Parallel()

	clock := newH7c2ManualClock()
	sampler := h7c2Sampler(
		t,
		&h7c2HealthSource{},
		&h7c2RecordingHealthRecorder{},
		clock,
		time.Second,
	)

	if err := sampler.Run(nil); err == nil {
		t.Fatal("Run() error = nil, want non-nil")
	}
}

func TestMessageDeliveryHealthSamplerConcurrentCancellation(t *testing.T) {
	t.Parallel()

	clock := newH7c2ManualClock()
	source := &h7c2HealthSource{health: []MessageDeliveryHealth{h7c2RunningHealth()}}
	sampler := h7c2Sampler(t, source, &h7c2RecordingHealthRecorder{}, clock, time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	for index := 0; index < 8; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cancel()
		}()
	}
	wg.Add(1)
	results := make(chan error, 1)
	go func() {
		defer wg.Done()
		results <- sampler.Run(ctx)
	}()

	wg.Wait()
	select {
	case err := <-results:
		if err != nil {
			t.Fatalf("Run() error = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run() did not return after concurrent cancellation")
	}
}

// h7c2FlakySource fails the first reads and then delegates.
type h7c2FlakySource struct {
	inner    *h7c2HealthSource
	mu       sync.Mutex
	failures int
	reads    int
}

func (s *h7c2FlakySource) readCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reads
}

func (s *h7c2FlakySource) State() MessageDeliveryHealthState {
	return s.inner.State()
}

func (s *h7c2FlakySource) ReadMessageDeliveryHealth(
	ctx context.Context,
) (MessageDeliveryHealth, error) {
	s.mu.Lock()
	s.reads++
	if s.failures > 0 {
		s.failures--
		s.mu.Unlock()
		return MessageDeliveryHealth{}, errors.New("transient read failure")
	}
	s.mu.Unlock()
	return s.inner.ReadMessageDeliveryHealth(ctx)
}

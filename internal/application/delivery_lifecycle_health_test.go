package application

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"telecli/internal/config"
	"telecli/internal/outbox"
	"telecli/internal/telemetry/recorder"
)

// h7c2bEventLog records the observable lifecycle order.
type h7c2bEventLog struct {
	mu     sync.Mutex
	events []string
}

func (l *h7c2bEventLog) add(event string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, event)
}

func (l *h7c2bEventLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.events...)
}

func (l *h7c2bEventLog) indexOf(event string) int {
	for index, current := range l.snapshot() {
		if current == event {
			return index
		}
	}
	return -1
}

// h7c2bHealthSource is a health source owned by the test.
type h7c2bHealthSource struct {
	log         *h7c2bEventLog
	reads       chan struct{}
	health      MessageDeliveryHealth
	unavailable bool
}

func (s *h7c2bHealthSource) State() MessageDeliveryHealthState {
	return MessageDeliveryHealthRunning
}

func (s *h7c2bHealthSource) ReadMessageDeliveryHealth(
	ctx context.Context,
) (MessageDeliveryHealth, error) {
	if s.unavailable {
		return MessageDeliveryHealth{}, ErrMessageDeliveryHealthUnavailable
	}
	if s.log != nil {
		s.log.add("sampler read")
	}
	select {
	case s.reads <- struct{}{}:
	default:
	}
	return s.health, nil
}

type h7c2bRuntime struct {
	submitter    ComposerMessageSubmitter
	statusSource MessageStatusSource
	healthSource MessageDeliveryHealthSource
	log          *h7c2bEventLog
}

func (r *h7c2bRuntime) Submitter() ComposerMessageSubmitter {
	return r.submitter
}

func (r *h7c2bRuntime) StatusSource() MessageStatusSource {
	return r.statusSource
}

func (r *h7c2bRuntime) HealthSource() MessageDeliveryHealthSource {
	return r.healthSource
}

func (r *h7c2bRuntime) Done() <-chan struct{} { return nil }

func (r *h7c2bRuntime) Err() error { return nil }

func (r *h7c2bRuntime) Close() error {
	if r.log != nil {
		r.log.add("delivery close")
	}
	return nil
}

func h7c2bSampling(
	healthRecorder MessageDeliveryHealthRecorder,
	clock outbox.Clock,
) deliveryHealthSampling {
	return deliveryHealthSampling{
		Recorder: healthRecorder,
		Clock:    clock,
		Interval: time.Millisecond,
	}
}

func h7c2bApplicationRecorder(
	t *testing.T,
	backend recorder.OutboxHealthRecorder,
) MessageDeliveryHealthRecorder {
	t.Helper()
	healthRecorder, err := NewMessageDeliveryHealthRecorder(backend)
	if err != nil {
		t.Fatalf("NewMessageDeliveryHealthRecorder() error = %v", err)
	}
	return healthRecorder
}

func TestDeliveryLifecycleDoesNotStartHealthSamplerInDirectMode(t *testing.T) {
	t.Parallel()

	log := &h7c2bEventLog{}
	recorder := &h7c2bHealthRecorder{}
	clock := newH7c2ManualClock()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// A direct runtime has no health source at all.
	handle := startDeliveryHealthSampling(
		ctx,
		nil,
		h7c2bSampling(h7c2bApplicationRecorder(t, recorder), clock),
	)
	if handle != nil {
		t.Fatal("direct mode started a health sampler")
	}

	clock.mu.Lock()
	timers := len(clock.intervals)
	clock.mu.Unlock()
	if timers != 0 {
		t.Fatalf("timer requests = %d, want 0", timers)
	}
	if got := recorder.records(); got != 0 {
		t.Fatalf("records = %d, want 0: direct mode must not record synthetic health", got)
	}
	if events := log.snapshot(); len(events) != 0 {
		t.Fatalf("events = %#v, want none", events)
	}
}

func TestDeliveryLifecycleStartsOneHealthSamplerInDurableMode(t *testing.T) {
	t.Parallel()

	source := &h7c2bHealthSource{
		reads:  make(chan struct{}, 4),
		health: MessageDeliveryHealth{State: MessageDeliveryHealthRunning},
	}
	recorder := &h7c2bHealthRecorder{}
	clock := newH7c2ManualClock()
	ctx, cancel := context.WithCancel(context.Background())

	handle := startDeliveryHealthSampling(
		ctx,
		source,
		h7c2bSampling(h7c2bApplicationRecorder(t, recorder), clock),
	)
	if handle == nil {
		t.Fatal("durable mode did not start a health sampler")
	}

	select {
	case <-source.reads:
	case <-time.After(2 * time.Second):
		t.Fatal("sampler did not perform the initial read")
	}

	handle.stop()
	cancel()
	if got := recorder.records(); got != 1 {
		t.Fatalf("records = %d, want 1", got)
	}
}

func TestDeliveryLifecyclePassesRuntimeHealthSourceToSampler(t *testing.T) {
	t.Parallel()

	source := &h7c2bHealthSource{
		reads:  make(chan struct{}, 1),
		health: MessageDeliveryHealth{State: MessageDeliveryHealthRunning},
	}
	runtime := &h7c2bRuntime{
		submitter:    &h5bComposerStub{},
		healthSource: source,
	}
	recorder := &h7c2bHealthRecorder{}
	ctx, cancel := context.WithCancel(context.Background())

	handle := startDeliveryHealthSampling(
		ctx,
		runtime.HealthSource(),
		h7c2bSampling(h7c2bApplicationRecorder(t, recorder), newH7c2ManualClock()),
	)
	if handle == nil {
		t.Fatal("sampler was not started from the runtime health source")
	}

	select {
	case <-source.reads:
	case <-time.After(2 * time.Second):
		t.Fatal("sampler did not read from the runtime health source")
	}
	handle.stop()
	cancel()
}

func TestDeliveryLifecyclePassesOutboxHealthRecorderToSampler(t *testing.T) {
	t.Parallel()

	source := &h7c2bHealthSource{
		reads:  make(chan struct{}, 1),
		health: MessageDeliveryHealth{State: MessageDeliveryHealthRunning, Queued: 3},
	}
	sink := &h7c2bHealthRecorder{}
	ctx, cancel := context.WithCancel(context.Background())

	handle := startDeliveryHealthSampling(
		ctx,
		source,
		h7c2bSampling(
			h7c2bApplicationRecorder(t, sink),
			newH7c2ManualClock(),
		),
	)
	if handle == nil {
		t.Fatal("sampler was not started")
	}

	select {
	case <-source.reads:
	case <-time.After(2 * time.Second):
		t.Fatal("sampler did not read")
	}
	handle.stop()
	cancel()

	observed := sink.notes()
	if len(observed) != 1 {
		t.Fatalf("recorded observations = %d, want 1", len(observed))
	}
	if observed[0].Queued != 3 {
		t.Fatalf("recorded queued = %d, want 3", observed[0].Queued)
	}
}

func TestDeliveryLifecycleStopsHealthSamplerBeforeDeliveryClose(t *testing.T) {
	t.Parallel()

	log := &h7c2bEventLog{}
	source := &h7c2bHealthSource{
		log:    log,
		reads:  make(chan struct{}, 1),
		health: MessageDeliveryHealth{State: MessageDeliveryHealthRunning},
	}
	runtime := &h7c2bRuntime{
		submitter:    &h5bComposerStub{},
		healthSource: source,
		log:          log,
	}
	clock := newH7c2ManualClock()
	ctx, cancel := context.WithCancel(context.Background())

	handle := startDeliveryHealthSampling(
		ctx,
		runtime.HealthSource(),
		h7c2bSampling(h7c2bApplicationRecorder(t, &h7c2bHealthRecorder{}), clock),
	)
	if handle == nil {
		t.Fatal("sampler was not started")
	}
	<-source.reads

	log.add("sampler cancel")
	handle.stop()
	log.add("sampler done")

	if err := runtime.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	log.add("session close")
	cancel()

	events := log.snapshot()
	readIndex := log.indexOf("sampler read")
	cancelIndex := log.indexOf("sampler cancel")
	doneIndex := log.indexOf("sampler done")
	deliveryIndex := log.indexOf("delivery close")
	sessionIndex := log.indexOf("session close")

	if readIndex < 0 || cancelIndex < 0 || doneIndex < 0 ||
		deliveryIndex < 0 || sessionIndex < 0 {
		t.Fatalf("events = %#v, want the full lifecycle", events)
	}
	if !(readIndex < cancelIndex &&
		cancelIndex < doneIndex &&
		doneIndex < deliveryIndex &&
		deliveryIndex < sessionIndex) {
		t.Fatalf("events = %#v, want read < cancel < done < delivery < session", events)
	}
}

func TestDeliveryLifecycleWaitsForHealthSamplerBeforeDeliveryClose(t *testing.T) {
	t.Parallel()

	// This source ignores context cancellation, so only the completion of the
	// sampling goroutine can release stop().
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	source := &h7c2bStubbornHealthSource{started: started, release: release}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	handle := startDeliveryHealthSampling(
		ctx,
		source,
		h7c2bSampling(
			h7c2bApplicationRecorder(t, &h7c2bHealthRecorder{}),
			newH7c2ManualClock(),
		),
	)
	if handle == nil {
		t.Fatal("sampler was not started")
	}

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("sampler did not start the blocking read")
	}

	stopped := make(chan struct{})
	go func() {
		handle.stop()
		close(stopped)
	}()

	select {
	case <-stopped:
		t.Fatal("stop returned while a health read was still in flight")
	case <-time.After(50 * time.Millisecond):
	}

	// Releasing the read lets the sampling goroutine finish, and only then may
	// the caller close the durable store.
	close(release)
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("stop did not finish after the read completed")
	}

	if got := source.reads(); got != 1 {
		t.Fatalf("reads = %d, want 1", got)
	}
}

func TestDeliveryLifecycleDoesNotReadHealthAfterDeliveryClose(t *testing.T) {
	t.Parallel()

	log := &h7c2bEventLog{}
	source := &h7c2bHealthSource{
		log:         log,
		reads:       make(chan struct{}, 1),
		health:      MessageDeliveryHealth{State: MessageDeliveryHealthRunning},
		unavailable: true,
	}
	runtime := &h7c2bRuntime{
		submitter:    &h5bComposerStub{},
		healthSource: source,
		log:          log,
	}
	ctx, cancel := context.WithCancel(context.Background())
	clock := newH7c2ManualClock()

	handle := startDeliveryHealthSampling(
		ctx,
		runtime.HealthSource(),
		h7c2bSampling(h7c2bApplicationRecorder(t, &h7c2bHealthRecorder{}), clock),
	)
	if handle == nil {
		t.Fatal("sampler was not started")
	}
	tick := clock.next(t)

	// The runtime closes while a sample is pending.
	if err := runtime.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	log.add("delivery closed")

	// The sampler is stopped only afterwards, and no new read is possible.
	handle.stop()
	cancel()

	if index := log.indexOf("sampler read"); index >= 0 {
		t.Fatalf("events = %#v, want no read after delivery close", log.snapshot())
	}
	if tick != nil {
		// A late tick must not restart sampling.
		tick <- time.Now()
	}
	if index := log.indexOf("sampler read"); index >= 0 {
		t.Fatalf("events = %#v, want no read after a late tick", log.snapshot())
	}
}

func TestDeliveryLifecycleClosesSessionAfterDelivery(t *testing.T) {
	t.Parallel()

	log := &h7c2bEventLog{}
	source := &h7c2bHealthSource{
		log:    log,
		reads:  make(chan struct{}, 1),
		health: MessageDeliveryHealth{State: MessageDeliveryHealthRunning},
	}
	runtime := &h7c2bRuntime{
		submitter:    &h5bComposerStub{},
		healthSource: source,
		log:          log,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	handle := startDeliveryHealthSampling(
		ctx,
		runtime.HealthSource(),
		h7c2bSampling(h7c2bApplicationRecorder(t, &h7c2bHealthRecorder{}), newH7c2ManualClock()),
	)
	if handle == nil {
		t.Fatal("sampler was not started")
	}
	<-source.reads

	handle.stop()
	if err := runtime.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	events := log.snapshot()
	deliveryIndex := log.indexOf("delivery close")
	if deliveryIndex < 0 {
		t.Fatalf("events = %#v, want delivery close", events)
	}
	if len(events) > 0 && events[len(events)-1] != "delivery close" {
		t.Fatalf("events = %#v, want delivery close last", events)
	}
}

func TestHealthSamplerReadFailureDoesNotCancelDelivery(t *testing.T) {
	t.Parallel()

	source := &h7c2bHealthSource{
		reads:       make(chan struct{}, 1),
		unavailable: true,
	}
	runtime := &h7c2bRuntime{
		submitter:    &h5bComposerStub{},
		healthSource: source,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	handle := startDeliveryHealthSampling(
		ctx,
		runtime.HealthSource(),
		h7c2bSampling(h7c2bApplicationRecorder(t, &h7c2bHealthRecorder{}), newH7c2ManualClock()),
	)
	if handle == nil {
		t.Fatal("sampler was not started")
	}
	handle.stop()

	if err := ctx.Err(); err != nil {
		t.Fatalf("context error = %v, want nil: sampling must not cancel delivery", err)
	}
	if err := runtime.Err(); err != nil {
		t.Fatalf("runtime error = %v, want nil", err)
	}
	if runtime.Submitter() == nil {
		t.Fatal("submitter was removed by sampling failure")
	}
	if err := runtime.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestHealthSamplerConstructionFailureDisablesSamplingOnly(t *testing.T) {
	t.Parallel()

	runtime := &h7c2bRuntime{
		submitter:    &h5bComposerStub{},
		healthSource: &h7c2bHealthSource{reads: make(chan struct{}, 1)},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// A nil recorder disables sampling without touching the runtime.
	handle := startDeliveryHealthSampling(
		ctx,
		runtime.HealthSource(),
		deliveryHealthSampling{Interval: time.Millisecond},
	)
	if handle != nil {
		t.Fatal("sampling started without a recorder")
	}
	if runtime.Submitter() == nil {
		t.Fatal("runtime lost its submitter")
	}
	if runtime.HealthSource() == nil {
		t.Fatal("runtime lost its health source")
	}
}

func TestStartDeliveryHealthSamplingDefaultsClockAndInterval(t *testing.T) {
	t.Parallel()

	source := &h7c2bHealthSource{
		reads:  make(chan struct{}, 1),
		health: MessageDeliveryHealth{State: MessageDeliveryHealthRunning},
	}
	ctx, cancel := context.WithCancel(context.Background())

	handle := startDeliveryHealthSampling(
		ctx,
		source,
		deliveryHealthSampling{
			Recorder: h7c2bApplicationRecorder(t, &h7c2bHealthRecorder{}),
		},
	)
	if handle == nil {
		t.Fatal("sampler was not started")
	}
	handle.stop()
	cancel()

	if !strings.Contains(
		defaultMessageDeliveryHealthSampleInterval.String(),
		"s",
	) {
		t.Fatalf(
			"default interval = %s, want a second-based policy",
			defaultMessageDeliveryHealthSampleInterval,
		)
	}
}

func TestPrepareDeliveryAuthResultPassesSamplingToDurableRuntime(t *testing.T) {
	t.Parallel()

	cfg := h5bConfig(t)
	cfg.MessageDelivery.Mode = config.MessageSendModeDurable
	sink := &h7c2bHealthRecorder{}
	statusSource, err := NewOutboxMessageStatusSource(
		&h6bFakeEntryStatusReader{},
		0,
	)
	if err != nil {
		t.Fatalf("NewOutboxMessageStatusSource() error = %v", err)
	}
	runtime := &h5bRuntime{
		submitter:    &h5bComposerStub{},
		statusSource: statusSource,
		healthSource: &h7c2bHealthSource{
			reads:  make(chan struct{}, 1),
			health: MessageDeliveryHealth{State: MessageDeliveryHealthRunning},
		},
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)

	result, err := prepareDeliveryAuthResult(
		ctx,
		cfg,
		&h5bSession{},
		cancel,
		func(
			context.Context,
			MessageDeliveryRuntimeConfig,
			MessageDeliveryRuntimeDeps,
		) (MessageDeliveryRuntime, error) {
			return runtime, nil
		},
		h7c2bSampling(h7c2bApplicationRecorder(t, sink), newH7c2ManualClock()),
	)
	if err != nil {
		t.Fatalf("prepareDeliveryAuthResult() error = %v", err)
	}
	if err := result.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestPrepareDeliveryAuthResultSkipsSamplerInDirectMode(t *testing.T) {
	t.Parallel()

	recorder := &h7c2bHealthRecorder{}
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)

	result, err := prepareDeliveryAuthResult(
		ctx,
		h5bConfig(t),
		&h5bSession{},
		cancel,
		func(
			context.Context,
			MessageDeliveryRuntimeConfig,
			MessageDeliveryRuntimeDeps,
		) (MessageDeliveryRuntime, error) {
			return &h5bRuntime{submitter: &h5bComposerStub{}}, nil
		},
		h7c2bSampling(h7c2bApplicationRecorder(t, recorder), newH7c2ManualClock()),
	)
	if err != nil {
		t.Fatalf("prepareDeliveryAuthResult() error = %v", err)
	}
	if err := result.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if got := recorder.records(); got != 0 {
		t.Fatalf("recorded observations = %d, want 0 in direct mode", got)
	}
}

type h7c2bBlockingHealthSource struct {
	started chan struct{}
	release chan struct{}
}

func (s *h7c2bBlockingHealthSource) State() MessageDeliveryHealthState {
	return MessageDeliveryHealthRunning
}

func (s *h7c2bBlockingHealthSource) ReadMessageDeliveryHealth(
	ctx context.Context,
) (MessageDeliveryHealth, error) {
	select {
	case s.started <- struct{}{}:
	default:
	}
	select {
	case <-ctx.Done():
		return MessageDeliveryHealth{}, ctx.Err()
	case <-s.release:
		return MessageDeliveryHealth{State: MessageDeliveryHealthRunning}, nil
	}
}

// h7c2bHealthRecorder captures the telemetry observations the sampler records.
type h7c2bHealthRecorder struct {
	mu       sync.Mutex
	observed []recorder.OutboxHealth
}

func (r *h7c2bHealthRecorder) SetOutboxHealth(health recorder.OutboxHealth) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.observed = append(r.observed, health)
}

func (r *h7c2bHealthRecorder) notes() []recorder.OutboxHealth {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]recorder.OutboxHealth(nil), r.observed...)
}

func (r *h7c2bHealthRecorder) records() int {
	return len(r.notes())
}

var _ recorder.OutboxHealthRecorder = (*h7c2bHealthRecorder)(nil)

// h7c2bStubbornHealthSource blocks until released and ignores cancellation, so
// a test can prove that the lifecycle waits for the sampling goroutine.
type h7c2bStubbornHealthSource struct {
	started chan struct{}
	release chan struct{}
	mu      sync.Mutex
	count   int
}

func (s *h7c2bStubbornHealthSource) State() MessageDeliveryHealthState {
	return MessageDeliveryHealthRunning
}

func (s *h7c2bStubbornHealthSource) ReadMessageDeliveryHealth(
	context.Context,
) (MessageDeliveryHealth, error) {
	s.mu.Lock()
	s.count++
	s.mu.Unlock()

	select {
	case s.started <- struct{}{}:
	default:
	}
	<-s.release

	return MessageDeliveryHealth{State: MessageDeliveryHealthRunning}, nil
}

func (s *h7c2bStubbornHealthSource) reads() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.count
}

package application

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"telecli/internal/outbox"
)

// h7bStatusOnlyStore keeps the status reader capability but has no
// operational snapshot capability.
type h7bStatusOnlyStore struct {
	outbox.Store
}

func (s h7bStatusOnlyStore) ListEntryStatuses(
	ctx context.Context,
	query outbox.ListEntryStatusesQuery,
) ([]outbox.EntryStatus, error) {
	reader, ok := s.Store.(outbox.EntryStatusReader)
	if !ok {
		return nil, errors.New("h7b: store has no status reader")
	}
	return reader.ListEntryStatuses(ctx, query)
}

// h7bBlockingRunner holds the dispatcher after cancellation so that the
// stopping lifecycle state can be observed without a sleep.
func h7bBlockingRunner(
	cancelObserved chan struct{},
	release chan struct{},
) func(context.Context, *outbox.Dispatcher) error {
	return func(ctx context.Context, _ *outbox.Dispatcher) error {
		<-ctx.Done()
		close(cancelObserved)
		<-release
		return ctx.Err()
	}
}

func h7bRuntime(
	t *testing.T,
	run func(context.Context, *outbox.Dispatcher) error,
	closeOutbox func(*outbox.Outbox) error,
) (*DurableOutboxRuntime, *outbox.MemoryStore) {
	t.Helper()

	store := outbox.NewMemoryStore()
	session := &durableRuntimeTestSession{}
	factory := durableRuntimeTestFactory(
		durableRuntimeTestOpened(store, session),
		run,
		closeOutbox,
	)
	runtime, err := openDurableOutboxRuntime(
		context.Background(),
		durableRuntimeTestConfig(t),
		durableRuntimeTestDeps(
			session,
			newDurableRuntimeTestKeyProvider(),
			durableRuntimeTestIDGenerator("h7b"),
		),
		factory,
	)
	if err != nil {
		t.Fatalf("openDurableOutboxRuntime() error = %v", err)
	}
	return runtime, store
}

func TestOpenDurableOutboxRuntimeBuildsHealthSource(t *testing.T) {
	t.Parallel()

	runtime, _ := h7bRuntime(
		t,
		durableRuntimeRunUntilCanceled(make(chan struct{})),
		nil,
	)
	t.Cleanup(func() {
		if err := runtime.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	source := runtime.HealthSource()
	if source == nil {
		t.Fatal("HealthSource() = nil")
	}
	if got := source.State(); got != MessageDeliveryHealthRunning {
		t.Fatalf("State() = %q, want running", got)
	}
}

func TestOpenDurableOutboxRuntimeRejectsStoreWithoutOperationalSnapshotReader(t *testing.T) {
	t.Parallel()

	session := &durableRuntimeTestSession{}
	var closeCalls atomic.Int32
	var runCalls atomic.Int32
	factory := durableRuntimeTestFactory(
		durableRuntimeTestOpened(
			h7bStatusOnlyStore{Store: outbox.NewMemoryStore()},
			session,
		),
		func(context.Context, *outbox.Dispatcher) error {
			runCalls.Add(1)
			return nil
		},
		func(*outbox.Outbox) error {
			closeCalls.Add(1)
			return nil
		},
	)

	runtime, err := openDurableOutboxRuntime(
		context.Background(),
		durableRuntimeTestConfig(t),
		durableRuntimeTestDeps(
			session,
			newDurableRuntimeTestKeyProvider(),
			durableRuntimeTestIDGenerator("h7b-capability"),
		),
		factory,
	)
	if err == nil {
		t.Fatal("openDurableOutboxRuntime() error = nil, want non-nil")
	}
	if runtime != nil {
		t.Fatalf("runtime = %#v, want nil", runtime)
	}
	if !strings.Contains(err.Error(), "operational") {
		t.Fatalf("error = %v, want operational capability error", err)
	}
	if closeCalls.Load() != 1 {
		t.Fatalf("close calls = %d, want 1", closeCalls.Load())
	}
}

func TestOpenDurableOutboxRuntimeDoesNotStartDispatcherWithoutHealthCapability(t *testing.T) {
	t.Parallel()

	session := &durableRuntimeTestSession{}
	started := make(chan struct{})
	var once sync.Once
	factory := durableRuntimeTestFactory(
		durableRuntimeTestOpened(
			h7bStatusOnlyStore{Store: outbox.NewMemoryStore()},
			session,
		),
		func(context.Context, *outbox.Dispatcher) error {
			once.Do(func() { close(started) })
			return nil
		},
		nil,
	)

	if _, err := openDurableOutboxRuntime(
		context.Background(),
		durableRuntimeTestConfig(t),
		durableRuntimeTestDeps(
			session,
			newDurableRuntimeTestKeyProvider(),
			durableRuntimeTestIDGenerator("h7b-nodispatch"),
		),
		factory,
	); err == nil {
		t.Fatal("openDurableOutboxRuntime() error = nil, want non-nil")
	}

	select {
	case <-started:
		t.Fatal("dispatcher started without health capability")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestDirectMessageDeliveryRuntimeHealthSourceIsNil(t *testing.T) {
	t.Parallel()

	runtime, err := NewDirectMessageDeliveryRuntime(
		&h4RecordingComposerSubmitter{},
	)
	if err != nil {
		t.Fatalf("NewDirectMessageDeliveryRuntime() error = %v", err)
	}
	if source := runtime.HealthSource(); source != nil {
		t.Fatalf("HealthSource() = %#v, want nil", source)
	}
}

func TestMessageDeliveryRuntimeForwardsHealthSource(t *testing.T) {
	t.Parallel()

	source := &h7bHealthSourceStub{}
	statusSource, err := NewOutboxMessageStatusSource(
		&h6bFakeEntryStatusReader{},
		0,
	)
	if err != nil {
		t.Fatalf("NewOutboxMessageStatusSource() error = %v", err)
	}

	stub := &h4StubRuntime{
		submitter:    &h4RecordingComposerSubmitter{},
		statusSource: statusSource,
		healthSource: source,
	}
	durable, err := openMessageDeliveryRuntime(
		context.Background(),
		MessageDeliveryRuntimeConfig{Mode: "durable"},
		MessageDeliveryRuntimeDeps{},
		h4FactoryWithRuntime(stub, nil),
	)
	if err != nil {
		t.Fatalf("openMessageDeliveryRuntime() error = %v", err)
	}
	if durable.HealthSource() != source {
		t.Fatal("durable runtime did not forward the health source")
	}
}

func TestDurableRuntimeHealthReadsOperationalCounts(t *testing.T) {
	t.Parallel()

	runtime, store := h7bRuntime(
		t,
		durableRuntimeRunUntilCanceled(make(chan struct{})),
		nil,
	)
	t.Cleanup(func() {
		if err := runtime.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	if _, err := runtime.Submitter().SubmitMessage(
		context.Background(),
		"account-1",
		42,
		"hello",
	); err != nil {
		t.Fatalf("SubmitMessage() error = %v", err)
	}

	health, err := runtime.HealthSource().ReadMessageDeliveryHealth(
		context.Background(),
	)
	if err != nil {
		t.Fatalf("ReadMessageDeliveryHealth() error = %v", err)
	}
	if health.State != MessageDeliveryHealthRunning {
		t.Fatalf("health.State = %q, want running", health.State)
	}

	entries, err := store.ListAll(context.Background())
	if err != nil {
		t.Fatalf("ListAll() error = %v", err)
	}
	total := int64(len(entries))
	sum := health.Queued +
		health.Dispatching +
		health.Accepted +
		health.FailedRetryable +
		health.FailedPermanent +
		health.Uncertain +
		health.Canceled
	if sum != total {
		t.Fatalf("counter sum = %d, want %d", sum, total)
	}
	if health.Queued+health.Dispatching < 1 {
		t.Fatalf("health = %#v, want a pending entry", health)
	}
}

func TestDurableRuntimeHealthPreservesReaderError(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("operational read failed")
	store := &h7bFailingOperationalStore{
		Store: outbox.NewMemoryStore(),
		err:   sentinel,
	}
	session := &durableRuntimeTestSession{}
	factory := durableRuntimeTestFactory(
		durableRuntimeTestOpened(store, session),
		durableRuntimeRunUntilCanceled(make(chan struct{})),
		nil,
	)
	runtime, err := openDurableOutboxRuntime(
		context.Background(),
		durableRuntimeTestConfig(t),
		durableRuntimeTestDeps(
			session,
			newDurableRuntimeTestKeyProvider(),
			durableRuntimeTestIDGenerator("h7b-reader-error"),
		),
		factory,
	)
	if err != nil {
		t.Fatalf("openDurableOutboxRuntime() error = %v", err)
	}
	t.Cleanup(func() {
		if err := runtime.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	if _, err := runtime.HealthSource().ReadMessageDeliveryHealth(
		context.Background(),
	); !errors.Is(err, sentinel) {
		t.Fatalf("error = %v, want wrapped %v", err, sentinel)
	}
}

func TestDurableRuntimeHealthPropagatesCanceledContext(t *testing.T) {
	t.Parallel()

	runtime, _ := h7bRuntime(
		t,
		durableRuntimeRunUntilCanceled(make(chan struct{})),
		nil,
	)
	t.Cleanup(func() {
		if err := runtime.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := runtime.HealthSource().ReadMessageDeliveryHealth(
		ctx,
	); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestDurableRuntimeHealthFailedAfterDispatcherError(t *testing.T) {
	t.Parallel()

	dispatcherErr := errors.New("dispatcher failed")
	runtime, _ := h7bRuntime(
		t,
		func(context.Context, *outbox.Dispatcher) error {
			return dispatcherErr
		},
		nil,
	)
	t.Cleanup(func() {
		// The dispatcher error is expected and surfaces through Close.
		_ = runtime.Close()
	})

	durableRuntimeTestWait(t, runtime.Done())
	if got := runtime.HealthSource().State(); got != MessageDeliveryHealthFailed {
		t.Fatalf("State() = %q, want failed", got)
	}
	if !errors.Is(runtime.Err(), dispatcherErr) {
		t.Fatalf("Err() = %v, want %v", runtime.Err(), dispatcherErr)
	}
}

func TestDurableRuntimeHealthTreatsUnexpectedNilDispatcherExitAsFailed(t *testing.T) {
	t.Parallel()

	runtime, _ := h7bRuntime(
		t,
		func(context.Context, *outbox.Dispatcher) error {
			return nil
		},
		nil,
	)
	t.Cleanup(func() {
		if err := runtime.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	durableRuntimeTestWait(t, runtime.Done())
	if got := runtime.HealthSource().State(); got != MessageDeliveryHealthFailed {
		t.Fatalf("State() = %q, want failed", got)
	}
}

func TestDurableRuntimeHealthStopsReadingAfterDispatcherFailure(t *testing.T) {
	t.Parallel()

	runtime, _ := h7bRuntime(
		t,
		func(context.Context, *outbox.Dispatcher) error {
			return nil
		},
		nil,
	)
	t.Cleanup(func() {
		if err := runtime.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	durableRuntimeTestWait(t, runtime.Done())
	_, err := runtime.HealthSource().ReadMessageDeliveryHealth(
		context.Background(),
	)
	if !errors.Is(err, ErrMessageDeliveryHealthUnavailable) {
		t.Fatalf("error = %v, want ErrMessageDeliveryHealthUnavailable", err)
	}
}

func TestDurableRuntimeHealthBecomesUnavailableForSubmissionsAfterFailure(t *testing.T) {
	t.Parallel()

	runtime, _ := h7bRuntime(
		t,
		func(context.Context, *outbox.Dispatcher) error {
			return nil
		},
		nil,
	)
	t.Cleanup(func() {
		if err := runtime.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	durableRuntimeTestWait(t, runtime.Done())
	if _, err := runtime.Submitter().SubmitMessage(
		context.Background(),
		"account-1",
		42,
		"hello",
	); !errors.Is(err, ErrMessageDeliveryUnavailable) {
		t.Fatalf("error = %v, want ErrMessageDeliveryUnavailable", err)
	}
}

func TestDurableRuntimeHealthDoesNotExposeDispatcherError(t *testing.T) {
	t.Parallel()

	const secret = "provider failure: sqlite /var/tele/outbox.db"

	runtime, _ := h7bRuntime(
		t,
		func(context.Context, *outbox.Dispatcher) error {
			return errors.New(secret)
		},
		nil,
	)
	t.Cleanup(func() {
		_ = runtime.Close()
	})

	durableRuntimeTestWait(t, runtime.Done())
	health, err := runtime.HealthSource().ReadMessageDeliveryHealth(
		context.Background(),
	)
	if err == nil {
		t.Fatal("ReadMessageDeliveryHealth() error = nil, want non-nil")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatal("health read surfaced the raw dispatcher error")
	}
	if health != (MessageDeliveryHealth{}) {
		t.Fatalf("health = %#v, want zero", health)
	}
}

func TestDurableRuntimeHealthStoppingDuringClose(t *testing.T) {
	t.Parallel()

	cancelObserved := make(chan struct{})
	release := make(chan struct{})
	closeDone := make(chan error, 1)

	runtime, _ := h7bRuntime(
		t,
		h7bBlockingRunner(cancelObserved, release),
		nil,
	)

	go func() {
		closeDone <- runtime.Close()
	}()

	durableRuntimeTestWait(t, cancelObserved)
	if got := runtime.HealthSource().State(); got != MessageDeliveryHealthStopping {
		t.Fatalf("State() = %q, want stopping", got)
	}

	close(release)
	if err := <-closeDone; err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if got := runtime.HealthSource().State(); got != MessageDeliveryHealthStopped {
		t.Fatalf("State() = %q, want stopped", got)
	}
}

func TestDurableRuntimeHealthStoppedAfterSuccessfulClose(t *testing.T) {
	t.Parallel()

	runtime, _ := h7bRuntime(
		t,
		durableRuntimeRunUntilCanceled(make(chan struct{})),
		nil,
	)

	if err := runtime.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if got := runtime.HealthSource().State(); got != MessageDeliveryHealthStopped {
		t.Fatalf("State() = %q, want stopped", got)
	}
	if _, err := runtime.HealthSource().ReadMessageDeliveryHealth(
		context.Background(),
	); !errors.Is(err, ErrMessageDeliveryHealthUnavailable) {
		t.Fatalf("error = %v, want ErrMessageDeliveryHealthUnavailable", err)
	}
}

func TestDurableRuntimeHealthFailedAfterCloseError(t *testing.T) {
	t.Parallel()

	runtime, _ := h7bRuntime(
		t,
		durableRuntimeRunUntilCanceled(make(chan struct{})),
		func(*outbox.Outbox) error {
			return errors.New("close failed")
		},
	)

	if err := runtime.Close(); err == nil {
		t.Fatal("Close() error = nil, want non-nil")
	}
	if got := runtime.HealthSource().State(); got != MessageDeliveryHealthFailed {
		t.Fatalf("State() = %q, want failed", got)
	}
}

func TestDurableRuntimeHealthPreservesFailedDuringClose(t *testing.T) {
	t.Parallel()

	runtime, _ := h7bRuntime(
		t,
		func(context.Context, *outbox.Dispatcher) error {
			return nil
		},
		nil,
	)

	durableRuntimeTestWait(t, runtime.Done())
	if got := runtime.HealthSource().State(); got != MessageDeliveryHealthFailed {
		t.Fatalf("State() = %q, want failed", got)
	}

	// A dispatcher that stopped without an error leaves no close error, so
	// the failed lifecycle is the only observable trace.
	_ = runtime.Close()
	if got := runtime.HealthSource().State(); got != MessageDeliveryHealthFailed {
		t.Fatalf("State() = %q, want failed", got)
	}
}

func TestDurableRuntimeHealthRejectsTransitionFromTerminalState(t *testing.T) {
	t.Parallel()

	runtime, _ := h7bRuntime(
		t,
		durableRuntimeRunUntilCanceled(make(chan struct{})),
		nil,
	)
	if err := runtime.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if runtime.transitionMessageDeliveryHealth(MessageDeliveryHealthRunning) {
		t.Fatal("transition from stopped to running was accepted")
	}
	if runtime.transitionMessageDeliveryHealth(MessageDeliveryHealthStopping) {
		t.Fatal("transition from stopped to stopping was accepted")
	}
	if got := runtime.HealthSource().State(); got != MessageDeliveryHealthStopped {
		t.Fatalf("State() = %q, want stopped", got)
	}
}

func TestDurableRuntimeConcurrentCloseEndsInSingleTerminalHealthState(t *testing.T) {
	t.Parallel()

	runtime, _ := h7bRuntime(
		t,
		durableRuntimeRunUntilCanceled(make(chan struct{})),
		nil,
	)
	source := runtime.HealthSource()

	const callers = 32

	start := make(chan struct{})
	results := make(chan error, callers)
	for index := 0; index < callers; index++ {
		go func() {
			<-start
			results <- runtime.Close()
		}()
	}
	close(start)

	for index := 0; index < callers; index++ {
		if err := <-results; err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	}

	if got := source.State(); got != MessageDeliveryHealthStopped {
		t.Fatalf("State() = %q, want stopped", got)
	}
}

func TestDurableRuntimeHealthConcurrentReadsDuringShutdown(t *testing.T) {
	t.Parallel()

	cancelObserved := make(chan struct{})
	release := make(chan struct{})
	runtime, _ := h7bRuntime(
		t,
		h7bBlockingRunner(cancelObserved, release),
		nil,
	)
	source := runtime.HealthSource()

	const readers = 8
	var wg sync.WaitGroup
	for index := 0; index < readers; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				state := source.State()
				if state == MessageDeliveryHealthStopped {
					return
				}
				if _, err := source.ReadMessageDeliveryHealth(
					context.Background(),
				); err != nil &&
					!errors.Is(err, ErrMessageDeliveryHealthUnavailable) {
					t.Errorf("ReadMessageDeliveryHealth() error = %v", err)
					return
				}
				time.Sleep(time.Millisecond)
			}
		}()
	}

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- runtime.Close()
	}()

	durableRuntimeTestWait(t, cancelObserved)
	close(release)
	if err := <-closeDone; err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	wg.Wait()

	if got := source.State(); got != MessageDeliveryHealthStopped {
		t.Fatalf("State() = %q, want stopped", got)
	}
}

func TestDurableRuntimeHealthRejectsTransitionsFromStopped(t *testing.T) {
	t.Parallel()

	runtime := &DurableOutboxRuntime{
		healthState: MessageDeliveryHealthStopped,
	}

	if runtime.transitionMessageDeliveryHealth(
		MessageDeliveryHealthRunning,
	) {
		t.Fatal("stopped -> running transition was accepted")
	}
	if runtime.transitionMessageDeliveryHealth(
		MessageDeliveryHealthStopping,
	) {
		t.Fatal("stopped -> stopping transition was accepted")
	}
	if got := runtime.messageDeliveryHealthState(); got != MessageDeliveryHealthStopped {
		t.Fatalf("State() = %q, want stopped", got)
	}
}

func TestDurableRuntimeHealthRejectsTransitionsFromFailed(t *testing.T) {
	t.Parallel()

	runtime := &DurableOutboxRuntime{
		healthState: MessageDeliveryHealthFailed,
	}

	if runtime.transitionMessageDeliveryHealth(
		MessageDeliveryHealthStopping,
	) {
		t.Fatal("failed -> stopping transition was accepted")
	}
	if runtime.transitionMessageDeliveryHealth(
		MessageDeliveryHealthRunning,
	) {
		t.Fatal("failed -> running transition was accepted")
	}
	if got := runtime.messageDeliveryHealthState(); got != MessageDeliveryHealthFailed {
		t.Fatalf("State() = %q, want failed", got)
	}
}

func TestDurableRuntimeHealthReadsSnapshotCounts(t *testing.T) {
	t.Parallel()

	runtime, store := h7bRuntime(
		t,
		durableRuntimeRunUntilCanceled(make(chan struct{})),
		nil,
	)
	t.Cleanup(func() {
		_ = runtime.Close()
	})

	base := time.Unix(1700000000, 0).UTC()
	seed := []outbox.State{
		outbox.StateQueued,
		outbox.StateQueued,
		outbox.StateDispatching,
		outbox.StateAccepted,
		outbox.StateFailedRetryable,
		outbox.StateFailedPermanent,
		outbox.StateUncertain,
		outbox.StateCanceled,
	}
	for index, state := range seed {
		h7bSeedEntry(
			t,
			store,
			"s-"+string(rune('a'+index)),
			base.Add(time.Duration(index)*time.Second),
			state,
		)
	}

	health, err := runtime.HealthSource().ReadMessageDeliveryHealth(
		context.Background(),
	)
	if err != nil {
		t.Fatalf("ReadMessageDeliveryHealth() error = %v", err)
	}
	want := MessageDeliveryHealth{
		State:           MessageDeliveryHealthRunning,
		Queued:          2,
		Dispatching:     1,
		Accepted:        1,
		FailedRetryable: 1,
		FailedPermanent: 1,
		Uncertain:       1,
		Canceled:        1,
	}
	if health != want {
		t.Fatalf("health = %#v, want %#v", health, want)
	}
}

func TestDurableRuntimeHealthSourceNilReceiver(t *testing.T) {
	t.Parallel()

	var runtime *DurableOutboxRuntime
	if source := runtime.HealthSource(); source != nil {
		t.Fatalf("HealthSource() = %#v, want nil", source)
	}
	if got := runtime.messageDeliveryHealthState(); got != "" {
		t.Fatalf("messageDeliveryHealthState() = %q, want empty", got)
	}
}

func TestMessageDeliveryHealthSourceNilReceiver(t *testing.T) {
	t.Parallel()

	var source *durableMessageDeliveryHealthSource
	if got := source.State(); got != "" {
		t.Fatalf("State() = %q, want empty", got)
	}
	if _, err := source.ReadMessageDeliveryHealth(
		context.Background(),
	); !errors.Is(err, ErrMessageDeliveryHealthUnavailable) {
		t.Fatalf("error = %v, want ErrMessageDeliveryHealthUnavailable", err)
	}
}

type h7bFailingOperationalStore struct {
	outbox.Store
	err error
}

func (s *h7bFailingOperationalStore) ListEntryStatuses(
	ctx context.Context,
	query outbox.ListEntryStatusesQuery,
) ([]outbox.EntryStatus, error) {
	reader, ok := s.Store.(outbox.EntryStatusReader)
	if !ok {
		return nil, errors.New("h7b: store has no status reader")
	}
	return reader.ListEntryStatuses(ctx, query)
}

func (s *h7bFailingOperationalStore) ReadOperationalSnapshot(
	context.Context,
) (outbox.OperationalSnapshot, error) {
	return outbox.OperationalSnapshot{}, s.err
}

// h7bSeedEntry stores one entry and drives it into the requested state.
func h7bSeedEntry(
	t *testing.T,
	store outbox.Store,
	id string,
	at time.Time,
	state outbox.State,
) {
	t.Helper()
	ctx := context.Background()

	entry := outbox.Entry{
		ID:         outbox.ID(id),
		AccountKey: "account-1",
		ChatID:     42,
		Text:       "payload-" + id,
		State:      outbox.StateQueued,
		CreatedAt:  at,
		UpdatedAt:  at,
	}
	if err := store.Enqueue(ctx, entry); err != nil {
		t.Fatalf("Enqueue(%s) error = %v", id, err)
	}

	switch state {
	case outbox.StateQueued:
		return
	case outbox.StateCanceled:
		if _, err := store.Cancel(
			ctx,
			entry.ID,
			entry.Version,
			at,
		); err != nil {
			t.Fatalf("Cancel(%s) error = %v", id, err)
		}
		return
	}

	claimed, err := store.Claim(
		ctx,
		entry.ID,
		entry.Version,
		"owner",
		at.Add(time.Hour),
		at,
	)
	if err != nil {
		t.Fatalf("Claim(%s) error = %v", id, err)
	}

	switch state {
	case outbox.StateDispatching:
		return
	case outbox.StateAccepted:
		if _, err := store.MarkAccepted(
			ctx,
			entry.ID,
			claimed.Version,
			1,
			at.Add(time.Second),
		); err != nil {
			t.Fatalf("MarkAccepted(%s) error = %v", id, err)
		}
	case outbox.StateFailedRetryable:
		if _, err := store.MarkRetryable(
			ctx,
			entry.ID,
			claimed.Version,
			at.Add(time.Minute),
			500,
			"retry",
			at.Add(time.Second),
		); err != nil {
			t.Fatalf("MarkRetryable(%s) error = %v", id, err)
		}
	case outbox.StateFailedPermanent:
		if _, err := store.MarkPermanentFailure(
			ctx,
			entry.ID,
			claimed.Version,
			400,
			"failed",
			at.Add(time.Second),
		); err != nil {
			t.Fatalf("MarkPermanentFailure(%s) error = %v", id, err)
		}
	case outbox.StateUncertain:
		if _, err := store.MarkUncertain(
			ctx,
			entry.ID,
			claimed.Version,
			"uncertain",
			at.Add(time.Second),
		); err != nil {
			t.Fatalf("MarkUncertain(%s) error = %v", id, err)
		}
	default:
		t.Fatalf("unsupported seed state %q", state)
	}
}

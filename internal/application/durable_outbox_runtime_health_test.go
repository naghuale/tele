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

	direct, err := openMessageDeliveryRuntime(
		context.Background(),
		MessageDeliveryRuntimeConfig{Mode: "direct"},
		MessageDeliveryRuntimeDeps{
			DirectSubmitter: &h4RecordingComposerSubmitter{},
		},
		h4FactoryWithRuntime(nil, nil),
	)
	if err != nil {
		t.Fatalf("openMessageDeliveryRuntime() error = %v", err)
	}
	if direct.HealthSource() != nil {
		t.Fatal("direct runtime forwarded a health source")
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

func TestDurableRuntimeHealthFailsWhenDispatcherStopsWithoutError(t *testing.T) {
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

func TestDurableRuntimeHealthConcurrentCloseEndsInSingleTerminalState(t *testing.T) {
	t.Parallel()

	runtime, _ := h7bRuntime(
		t,
		durableRuntimeRunUntilCanceled(make(chan struct{})),
		nil,
	)

	var closers sync.WaitGroup
	for i := 0; i < 4; i++ {
		closers.Add(1)
		go func() {
			defer closers.Done()
			_ = runtime.Close()
		}()
	}
	closers.Wait()

	if got := runtime.HealthSource().State(); got != MessageDeliveryHealthStopped {
		t.Fatalf("State() = %q, want stopped", got)
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

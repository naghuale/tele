package application

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"telecli/internal/outbox"
	"telecli/internal/telegram"
)

type durableRuntimeTestKeyProvider struct {
	key         []byte
	loadErr     error
	createErr   error
	loadCalls   atomic.Int32
	createCalls atomic.Int32
}

func newDurableRuntimeTestKeyProvider() *durableRuntimeTestKeyProvider {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	return &durableRuntimeTestKeyProvider{
		key:     key,
		loadErr: outbox.ErrOutboxKeyUnavailable,
	}
}

func (p *durableRuntimeTestKeyProvider) LoadKey(
	ctx context.Context,
	_ string,
) ([]byte, error) {
	p.loadCalls.Add(1)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p.loadErr != nil {
		return nil, p.loadErr
	}
	return append([]byte(nil), p.key...), nil
}

func (p *durableRuntimeTestKeyProvider) CreateKey(
	ctx context.Context,
	_ string,
) ([]byte, error) {
	p.createCalls.Add(1)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p.createErr != nil {
		return nil, p.createErr
	}
	return append([]byte(nil), p.key...), nil
}

type durableRuntimeTestSend struct {
	chatID telegram.ChatID
	text   string
}

type durableRuntimeTestSession struct {
	sent  chan durableRuntimeTestSend
	err   error
	calls atomic.Int32
}

func (s *durableRuntimeTestSession) SendTextMessage(
	ctx context.Context,
	chatID telegram.ChatID,
	text string,
) (telegram.Message, error) {
	if err := ctx.Err(); err != nil {
		return telegram.Message{}, err
	}
	s.calls.Add(1)
	if s.sent != nil {
		select {
		case s.sent <- durableRuntimeTestSend{chatID: chatID, text: text}:
		default:
		}
	}
	if s.err != nil {
		return telegram.Message{}, s.err
	}
	return telegram.Message{
		ID:        telegram.MessageID(1),
		ChatID:    chatID,
		Outgoing:  true,
		Text:      text,
		Timestamp: time.Unix(1, 0).UTC(),
	}, nil
}

func durableRuntimeTestIDGenerator(
	prefix string,
) OutboxIDGenerator {
	var next atomic.Int64
	return func() (outbox.ID, error) {
		return outbox.ID(
			prefix + "-" + strconv.FormatInt(next.Add(1), 10),
		), nil
	}
}

func durableRuntimeTestConfig(
	t *testing.T,
) DurableOutboxRuntimeConfig {
	t.Helper()
	dataDir := t.TempDir()
	if err := os.Chmod(dataDir, 0o700); err != nil {
		t.Fatalf("chmod data dir: %v", err)
	}
	cfg := DurableOutboxRuntimeConfig{
		Outbox: outbox.Config{
			DataDir:    dataDir,
			DatabaseID: "durable-runtime-test",
			InstanceID: "durable-runtime-test",
		},
	}
	cfg.Outbox.Dispatcher = outbox.DefaultDispatcherConfig("durable-runtime-test")
	cfg.Outbox.Dispatcher.PollInterval = time.Millisecond
	cfg.Outbox.Dispatcher.RequestTimeout = time.Second
	cfg.Outbox.Dispatcher.LeaseDuration = time.Second
	return cfg
}

func durableRuntimeTestDeps(
	session TelegramSender,
	provider outbox.KeyProvider,
	idGenerator OutboxIDGenerator,
) DurableOutboxRuntimeDeps {
	return DurableOutboxRuntimeDeps{
		KeyProvider: provider,
		Session:     session,
		IDGenerator: idGenerator,
		AccountKey:  "account-1",
	}
}

func durableRuntimeTestOpened(
	store outbox.Store,
	session TelegramSender,
) *outbox.Outbox {
	dispatcher := outbox.NewDispatcher(
		store,
		NewTelegramOutboxSender(session),
		outbox.SystemClock{},
		outbox.DefaultDispatcherConfig("durable-runtime-fake"),
	)
	return &outbox.Outbox{
		Store:      store,
		Dispatcher: dispatcher,
	}
}

func durableRuntimeTestFactory(
	opened *outbox.Outbox,
	run func(context.Context, *outbox.Dispatcher) error,
	closeOutbox func(*outbox.Outbox) error,
) durableOutboxRuntimeFactory {
	factory := productionDurableOutboxRuntimeFactory()
	factory.openOutbox = func(
		context.Context,
		outbox.Config,
		outbox.Deps,
	) (*outbox.Outbox, error) {
		return opened, nil
	}
	if run != nil {
		factory.runDispatcher = run
	}
	if closeOutbox != nil {
		factory.closeOutbox = closeOutbox
	}
	return factory
}

func durableRuntimeTestWait(
	t *testing.T,
	ch <-chan struct{},
) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for runtime signal")
	}
}

func durableRuntimeTestWaitSend(
	t *testing.T,
	ch <-chan durableRuntimeTestSend,
) durableRuntimeTestSend {
	t.Helper()
	select {
	case send := <-ch:
		return send
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Telegram send")
		return durableRuntimeTestSend{}
	}
}

func durableRuntimeRunUntilCanceled(
	started chan struct{},
) func(context.Context, *outbox.Dispatcher) error {
	return func(
		ctx context.Context,
		_ *outbox.Dispatcher,
	) error {
		close(started)
		<-ctx.Done()
		return nil
	}
}

func TestOpenDurableOutboxRuntimeRejectsCanceledContext(t *testing.T) {
	t.Parallel()

	provider := newDurableRuntimeTestKeyProvider()
	session := &durableRuntimeTestSession{}
	var openCalls atomic.Int32
	factory := productionDurableOutboxRuntimeFactory()
	factory.openOutbox = func(
		context.Context,
		outbox.Config,
		outbox.Deps,
	) (*outbox.Outbox, error) {
		openCalls.Add(1)
		return nil, errors.New("unexpected open")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := openDurableOutboxRuntime(
		ctx,
		durableRuntimeTestConfig(t),
		durableRuntimeTestDeps(session, provider, durableRuntimeTestIDGenerator("canceled")),
		factory,
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if openCalls.Load() != 0 {
		t.Fatalf("outbox open calls = %d, want 0", openCalls.Load())
	}
	if provider.loadCalls.Load() != 0 {
		t.Fatalf("key provider calls = %d, want 0", provider.loadCalls.Load())
	}
}

func TestOpenDurableOutboxRuntimeRejectsNilKeyProvider(t *testing.T) {
	t.Parallel()

	session := &durableRuntimeTestSession{}
	var openCalls atomic.Int32
	factory := productionDurableOutboxRuntimeFactory()
	factory.openOutbox = func(
		context.Context,
		outbox.Config,
		outbox.Deps,
	) (*outbox.Outbox, error) {
		openCalls.Add(1)
		return nil, errors.New("unexpected open")
	}

	_, err := openDurableOutboxRuntime(
		context.Background(),
		durableRuntimeTestConfig(t),
		DurableOutboxRuntimeDeps{
			Session:     session,
			IDGenerator: durableRuntimeTestIDGenerator("no-key"),
		},
		factory,
	)
	if err == nil {
		t.Fatal("error = nil, want non-nil")
	}
	if openCalls.Load() != 0 {
		t.Fatalf("outbox open calls = %d, want 0", openCalls.Load())
	}
}

func TestOpenDurableOutboxRuntimeRejectsNilSession(t *testing.T) {
	t.Parallel()

	provider := newDurableRuntimeTestKeyProvider()
	var openCalls atomic.Int32
	factory := productionDurableOutboxRuntimeFactory()
	factory.openOutbox = func(
		context.Context,
		outbox.Config,
		outbox.Deps,
	) (*outbox.Outbox, error) {
		openCalls.Add(1)
		return nil, errors.New("unexpected open")
	}

	_, err := openDurableOutboxRuntime(
		context.Background(),
		durableRuntimeTestConfig(t),
		DurableOutboxRuntimeDeps{
			KeyProvider: provider,
			IDGenerator: durableRuntimeTestIDGenerator("no-session"),
		},
		factory,
	)
	if err == nil {
		t.Fatal("error = nil, want non-nil")
	}
	if openCalls.Load() != 0 {
		t.Fatalf("outbox open calls = %d, want 0", openCalls.Load())
	}
}

func TestOpenDurableOutboxRuntimeRejectsNilIDGenerator(t *testing.T) {
	t.Parallel()

	provider := newDurableRuntimeTestKeyProvider()
	session := &durableRuntimeTestSession{}
	var openCalls atomic.Int32
	factory := productionDurableOutboxRuntimeFactory()
	factory.openOutbox = func(
		context.Context,
		outbox.Config,
		outbox.Deps,
	) (*outbox.Outbox, error) {
		openCalls.Add(1)
		return nil, errors.New("unexpected open")
	}

	_, err := openDurableOutboxRuntime(
		context.Background(),
		durableRuntimeTestConfig(t),
		DurableOutboxRuntimeDeps{
			KeyProvider: provider,
			Session:     session,
		},
		factory,
	)
	if err == nil {
		t.Fatal("error = nil, want non-nil")
	}
	if openCalls.Load() != 0 {
		t.Fatalf("outbox open calls = %d, want 0", openCalls.Load())
	}
}

func TestOpenDurableOutboxRuntimeRejectsIncompleteOpened(t *testing.T) {
	t.Parallel()

	session := &durableRuntimeTestSession{}
	provider := newDurableRuntimeTestKeyProvider()
	store := outbox.NewMemoryStore()
	valid := durableRuntimeTestOpened(store, session)

	tests := []struct {
		name   string
		opened *outbox.Outbox
	}{
		{name: "nil outbox"},
		{name: "nil store", opened: &outbox.Outbox{Dispatcher: valid.Dispatcher}},
		{name: "nil dispatcher", opened: &outbox.Outbox{Store: store}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var closeCalls atomic.Int32
			factory := durableRuntimeTestFactory(
				test.opened,
				func(context.Context, *outbox.Dispatcher) error { return nil },
				func(*outbox.Outbox) error {
					closeCalls.Add(1)
					return nil
				},
			)
			_, err := openDurableOutboxRuntime(
				context.Background(),
				durableRuntimeTestConfig(t),
				durableRuntimeTestDeps(session, provider, durableRuntimeTestIDGenerator("incomplete")),
				factory,
			)
			if err == nil {
				t.Fatal("error = nil, want non-nil")
			}
			if test.opened != nil && closeCalls.Load() != 1 {
				t.Fatalf("close calls = %d, want 1", closeCalls.Load())
			}
		})
	}
}

func TestOpenDurableOutboxRuntimeBuildsTelegramSender(t *testing.T) {
	t.Parallel()

	provider := newDurableRuntimeTestKeyProvider()
	session := &durableRuntimeTestSession{
		sent: make(chan durableRuntimeTestSend, 1),
	}
	cfg := durableRuntimeTestConfig(t)
	runtime, err := OpenDurableOutboxRuntime(
		context.Background(),
		cfg,
		durableRuntimeTestDeps(
			session,
			provider,
			durableRuntimeTestIDGenerator("sender"),
		),
	)
	if err != nil {
		t.Fatalf("OpenDurableOutboxRuntime() error = %v", err)
	}

	submission, err := runtime.Submitter().SubmitMessage(
		context.Background(),
		"account-1",
		42,
		"hello",
	)
	if err != nil {
		t.Fatalf("SubmitMessage() error = %v", err)
	}
	if submission.State != MessageDeliveryQueued {
		t.Fatalf("submission state = %q, want queued", submission.State)
	}
	send := durableRuntimeTestWaitSend(t, session.sent)
	if send.chatID != 42 || send.text != "hello" {
		t.Fatalf("send = %#v, want chat 42 and hello", send)
	}
	if err := runtime.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestOpenDurableOutboxRuntimeBuildsMessageSubmitter(t *testing.T) {
	t.Parallel()

	store := outbox.NewMemoryStore()
	session := &durableRuntimeTestSession{}
	started := make(chan struct{})
	opened := durableRuntimeTestOpened(store, session)
	factory := durableRuntimeTestFactory(
		opened,
		durableRuntimeRunUntilCanceled(started),
		nil,
	)
	runtime, err := openDurableOutboxRuntime(
		context.Background(),
		durableRuntimeTestConfig(t),
		durableRuntimeTestDeps(session, newDurableRuntimeTestKeyProvider(), durableRuntimeTestIDGenerator("submitter")),
		factory,
	)
	if err != nil {
		t.Fatalf("openDurableOutboxRuntime() error = %v", err)
	}
	if runtime.Submitter() == nil {
		t.Fatal("Submitter() = nil")
	}
	durableRuntimeTestWait(t, started)

	if _, err := runtime.Submitter().SubmitMessage(
		context.Background(),
		"account-1",
		42,
		"hello",
	); err != nil {
		t.Fatalf("SubmitMessage() error = %v", err)
	}
	entries, err := store.ListAll(context.Background())
	if err != nil {
		t.Fatalf("ListAll() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("store entries = %d, want 1", len(entries))
	}
	if err := runtime.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestOpenDurableOutboxRuntimeStartsDispatcher(t *testing.T) {
	t.Parallel()

	store := outbox.NewMemoryStore()
	session := &durableRuntimeTestSession{}
	started := make(chan struct{})
	opened := durableRuntimeTestOpened(store, session)
	factory := durableRuntimeTestFactory(
		opened,
		durableRuntimeRunUntilCanceled(started),
		nil,
	)
	runtime, err := openDurableOutboxRuntime(
		context.Background(),
		durableRuntimeTestConfig(t),
		durableRuntimeTestDeps(session, newDurableRuntimeTestKeyProvider(), durableRuntimeTestIDGenerator("dispatcher")),
		factory,
	)
	if err != nil {
		t.Fatalf("openDurableOutboxRuntime() error = %v", err)
	}
	durableRuntimeTestWait(t, started)
	if err := runtime.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestOpenDurableOutboxRuntimeReturnsQueuedSubmission(t *testing.T) {
	t.Parallel()

	store := outbox.NewMemoryStore()
	session := &durableRuntimeTestSession{}
	started := make(chan struct{})
	opened := durableRuntimeTestOpened(store, session)
	factory := durableRuntimeTestFactory(
		opened,
		durableRuntimeRunUntilCanceled(started),
		nil,
	)
	runtime, err := openDurableOutboxRuntime(
		context.Background(),
		durableRuntimeTestConfig(t),
		durableRuntimeTestDeps(session, newDurableRuntimeTestKeyProvider(), durableRuntimeTestIDGenerator("queued")),
		factory,
	)
	if err != nil {
		t.Fatalf("openDurableOutboxRuntime() error = %v", err)
	}
	durableRuntimeTestWait(t, started)

	got, err := runtime.Submitter().SubmitMessage(
		context.Background(),
		"account-1",
		42,
		"hello",
	)
	if err != nil {
		t.Fatalf("SubmitMessage() error = %v", err)
	}
	want := MessageSubmission{ID: "queued-1", State: MessageDeliveryQueued}
	if got != want {
		t.Fatalf("SubmitMessage() = %#v, want %#v", got, want)
	}
	if err := runtime.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestDurableOutboxRuntimeDoneClosesWhenDispatcherStops(t *testing.T) {
	t.Parallel()

	stop := make(chan struct{})
	store := outbox.NewMemoryStore()
	session := &durableRuntimeTestSession{}
	opened := durableRuntimeTestOpened(store, session)
	factory := durableRuntimeTestFactory(
		opened,
		func(context.Context, *outbox.Dispatcher) error {
			<-stop
			return nil
		},
		nil,
	)
	runtime, err := openDurableOutboxRuntime(
		context.Background(),
		durableRuntimeTestConfig(t),
		durableRuntimeTestDeps(session, newDurableRuntimeTestKeyProvider(), durableRuntimeTestIDGenerator("done")),
		factory,
	)
	if err != nil {
		t.Fatalf("openDurableOutboxRuntime() error = %v", err)
	}
	close(stop)
	durableRuntimeTestWait(t, runtime.Done())
	if err := runtime.Err(); err != nil {
		t.Fatalf("Err() = %v, want nil", err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestDurableOutboxRuntimeErrBeforeDispatcherStops(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	store := outbox.NewMemoryStore()
	session := &durableRuntimeTestSession{}
	opened := durableRuntimeTestOpened(store, session)
	factory := durableRuntimeTestFactory(
		opened,
		durableRuntimeRunUntilCanceled(started),
		nil,
	)
	runtime, err := openDurableOutboxRuntime(
		context.Background(),
		durableRuntimeTestConfig(t),
		durableRuntimeTestDeps(session, newDurableRuntimeTestKeyProvider(), durableRuntimeTestIDGenerator("err-before")),
		factory,
	)
	if err != nil {
		t.Fatalf("openDurableOutboxRuntime() error = %v", err)
	}
	durableRuntimeTestWait(t, started)
	if err := runtime.Err(); err != nil {
		t.Fatalf("Err() = %v, want nil", err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestOpenDurableOutboxRuntimeSurfacesDispatcherError(t *testing.T) {
	t.Parallel()

	dispatcherErr := errors.New("dispatcher failed")
	store := outbox.NewMemoryStore()
	session := &durableRuntimeTestSession{}
	opened := durableRuntimeTestOpened(store, session)
	factory := durableRuntimeTestFactory(
		opened,
		func(context.Context, *outbox.Dispatcher) error {
			return dispatcherErr
		},
		nil,
	)
	runtime, err := openDurableOutboxRuntime(
		context.Background(),
		durableRuntimeTestConfig(t),
		durableRuntimeTestDeps(session, newDurableRuntimeTestKeyProvider(), durableRuntimeTestIDGenerator("dispatcher-error")),
		factory,
	)
	if err != nil {
		t.Fatalf("openDurableOutboxRuntime() error = %v", err)
	}
	durableRuntimeTestWait(t, runtime.Done())
	if !errors.Is(runtime.Err(), dispatcherErr) {
		t.Fatalf("Err() = %v, want %v", runtime.Err(), dispatcherErr)
	}
	if err := runtime.Close(); !errors.Is(err, dispatcherErr) {
		t.Fatalf("Close() error = %v, want %v", err, dispatcherErr)
	}
}

func TestOpenDurableOutboxRuntimeRejectsSubmissionAfterDispatcherStops(t *testing.T) {
	t.Parallel()

	store := outbox.NewMemoryStore()
	session := &durableRuntimeTestSession{}
	opened := durableRuntimeTestOpened(store, session)
	factory := durableRuntimeTestFactory(
		opened,
		func(context.Context, *outbox.Dispatcher) error { return nil },
		nil,
	)
	runtime, err := openDurableOutboxRuntime(
		context.Background(),
		durableRuntimeTestConfig(t),
		durableRuntimeTestDeps(session, newDurableRuntimeTestKeyProvider(), durableRuntimeTestIDGenerator("stopped")),
		factory,
	)
	if err != nil {
		t.Fatalf("openDurableOutboxRuntime() error = %v", err)
	}
	durableRuntimeTestWait(t, runtime.Done())
	_, err = runtime.Submitter().SubmitMessage(
		context.Background(),
		"account-1",
		42,
		"secret message payload",
	)
	if !errors.Is(err, ErrMessageDeliveryUnavailable) {
		t.Fatalf("SubmitMessage() error = %v, want ErrMessageDeliveryUnavailable", err)
	}
	entries, err := store.ListAll(context.Background())
	if err != nil {
		t.Fatalf("ListAll() error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("store entries = %d, want 0", len(entries))
	}
	if err := runtime.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestOpenDurableOutboxRuntimeCloseStopsDispatcher(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	stopped := make(chan struct{})
	store := outbox.NewMemoryStore()
	session := &durableRuntimeTestSession{}
	opened := durableRuntimeTestOpened(store, session)
	factory := durableRuntimeTestFactory(
		opened,
		func(ctx context.Context, _ *outbox.Dispatcher) error {
			close(started)
			<-ctx.Done()
			close(stopped)
			return ctx.Err()
		},
		nil,
	)
	runtime, err := openDurableOutboxRuntime(
		context.Background(),
		durableRuntimeTestConfig(t),
		durableRuntimeTestDeps(session, newDurableRuntimeTestKeyProvider(), durableRuntimeTestIDGenerator("close-stops")),
		factory,
	)
	if err != nil {
		t.Fatalf("openDurableOutboxRuntime() error = %v", err)
	}
	durableRuntimeTestWait(t, started)
	if err := runtime.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	durableRuntimeTestWait(t, stopped)
	durableRuntimeTestWait(t, runtime.Done())
}

func TestOpenDurableOutboxRuntimeCloseWaitsForDispatcher(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	cancelSeen := make(chan struct{})
	release := make(chan struct{})
	outboxClosed := make(chan struct{})
	store := outbox.NewMemoryStore()
	session := &durableRuntimeTestSession{}
	opened := durableRuntimeTestOpened(store, session)
	factory := durableRuntimeTestFactory(
		opened,
		func(ctx context.Context, _ *outbox.Dispatcher) error {
			close(started)
			<-ctx.Done()
			close(cancelSeen)
			<-release
			return nil
		},
		func(*outbox.Outbox) error {
			close(outboxClosed)
			return nil
		},
	)
	runtime, err := openDurableOutboxRuntime(
		context.Background(),
		durableRuntimeTestConfig(t),
		durableRuntimeTestDeps(session, newDurableRuntimeTestKeyProvider(), durableRuntimeTestIDGenerator("close-waits")),
		factory,
	)
	if err != nil {
		t.Fatalf("openDurableOutboxRuntime() error = %v", err)
	}
	durableRuntimeTestWait(t, started)

	closeResult := make(chan error, 1)
	go func() { closeResult <- runtime.Close() }()
	durableRuntimeTestWait(t, cancelSeen)
	select {
	case <-outboxClosed:
		t.Fatal("outbox closed before dispatcher release")
	default:
	}
	close(release)
	if err := <-closeResult; err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestOpenDurableOutboxRuntimeCloseClosesOutbox(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	var closeCalls atomic.Int32
	store := outbox.NewMemoryStore()
	session := &durableRuntimeTestSession{}
	opened := durableRuntimeTestOpened(store, session)
	factory := durableRuntimeTestFactory(
		opened,
		durableRuntimeRunUntilCanceled(started),
		func(*outbox.Outbox) error {
			closeCalls.Add(1)
			return nil
		},
	)
	runtime, err := openDurableOutboxRuntime(
		context.Background(),
		durableRuntimeTestConfig(t),
		durableRuntimeTestDeps(session, newDurableRuntimeTestKeyProvider(), durableRuntimeTestIDGenerator("close-outbox")),
		factory,
	)
	if err != nil {
		t.Fatalf("openDurableOutboxRuntime() error = %v", err)
	}
	durableRuntimeTestWait(t, started)
	if err := runtime.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if closeCalls.Load() != 1 {
		t.Fatalf("close calls = %d, want 1", closeCalls.Load())
	}
}

func TestOpenDurableOutboxRuntimeCloseIsIdempotent(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	var closeCalls atomic.Int32
	store := outbox.NewMemoryStore()
	session := &durableRuntimeTestSession{}
	opened := durableRuntimeTestOpened(store, session)
	factory := durableRuntimeTestFactory(
		opened,
		durableRuntimeRunUntilCanceled(started),
		func(*outbox.Outbox) error {
			closeCalls.Add(1)
			return nil
		},
	)
	runtime, err := openDurableOutboxRuntime(
		context.Background(),
		durableRuntimeTestConfig(t),
		durableRuntimeTestDeps(session, newDurableRuntimeTestKeyProvider(), durableRuntimeTestIDGenerator("idempotent")),
		factory,
	)
	if err != nil {
		t.Fatalf("openDurableOutboxRuntime() error = %v", err)
	}
	durableRuntimeTestWait(t, started)
	firstErr := runtime.Close()
	secondErr := runtime.Close()
	if firstErr != nil || secondErr != nil {
		t.Fatalf("Close() errors = %v, %v, want nil", firstErr, secondErr)
	}
	if closeCalls.Load() != 1 {
		t.Fatalf("close calls = %d, want 1", closeCalls.Load())
	}
}

func TestOpenDurableOutboxRuntimeCloseIsConcurrencySafe(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	var closeCalls atomic.Int32
	store := outbox.NewMemoryStore()
	session := &durableRuntimeTestSession{}
	opened := durableRuntimeTestOpened(store, session)
	factory := durableRuntimeTestFactory(
		opened,
		durableRuntimeRunUntilCanceled(started),
		func(*outbox.Outbox) error {
			closeCalls.Add(1)
			return nil
		},
	)
	runtime, err := openDurableOutboxRuntime(
		context.Background(),
		durableRuntimeTestConfig(t),
		durableRuntimeTestDeps(session, newDurableRuntimeTestKeyProvider(), durableRuntimeTestIDGenerator("concurrent")),
		factory,
	)
	if err != nil {
		t.Fatalf("openDurableOutboxRuntime() error = %v", err)
	}
	durableRuntimeTestWait(t, started)

	const callers = 32
	start := make(chan struct{})
	results := make(chan error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- runtime.Close()
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatalf("concurrent Close() error = %v", err)
		}
	}
	if closeCalls.Load() != 1 {
		t.Fatalf("close calls = %d, want 1", closeCalls.Load())
	}
}

func TestOpenDurableOutboxRuntimeCloseRejectsNewSubmissions(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	cancelSeen := make(chan struct{})
	release := make(chan struct{})
	store := outbox.NewMemoryStore()
	session := &durableRuntimeTestSession{}
	opened := durableRuntimeTestOpened(store, session)
	factory := durableRuntimeTestFactory(
		opened,
		func(ctx context.Context, _ *outbox.Dispatcher) error {
			close(started)
			<-ctx.Done()
			close(cancelSeen)
			<-release
			return nil
		},
		nil,
	)
	runtime, err := openDurableOutboxRuntime(
		context.Background(),
		durableRuntimeTestConfig(t),
		durableRuntimeTestDeps(session, newDurableRuntimeTestKeyProvider(), durableRuntimeTestIDGenerator("close-submit")),
		factory,
	)
	if err != nil {
		t.Fatalf("openDurableOutboxRuntime() error = %v", err)
	}
	durableRuntimeTestWait(t, started)
	closeResult := make(chan error, 1)
	go func() { closeResult <- runtime.Close() }()
	durableRuntimeTestWait(t, cancelSeen)
	_, err = runtime.Submitter().SubmitMessage(
		context.Background(),
		"account-1",
		42,
		"secret message payload",
	)
	if !errors.Is(err, ErrMessageDeliveryUnavailable) {
		t.Fatalf("SubmitMessage() error = %v, want ErrMessageDeliveryUnavailable", err)
	}
	close(release)
	if err := <-closeResult; err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestOpenDurableOutboxRuntimeDoesNotStartDispatcherAfterOpenFailure(t *testing.T) {
	t.Parallel()

	var runCalls atomic.Int32
	factory := productionDurableOutboxRuntimeFactory()
	factory.openOutbox = func(
		context.Context,
		outbox.Config,
		outbox.Deps,
	) (*outbox.Outbox, error) {
		return nil, errors.New("open failed")
	}
	factory.runDispatcher = func(context.Context, *outbox.Dispatcher) error {
		runCalls.Add(1)
		return nil
	}
	_, err := openDurableOutboxRuntime(
		context.Background(),
		durableRuntimeTestConfig(t),
		durableRuntimeTestDeps(
			&durableRuntimeTestSession{},
			newDurableRuntimeTestKeyProvider(),
			durableRuntimeTestIDGenerator("open-failure"),
		),
		factory,
	)
	if err == nil {
		t.Fatal("error = nil, want non-nil")
	}
	if runCalls.Load() != 0 {
		t.Fatalf("dispatcher run calls = %d, want 0", runCalls.Load())
	}
}

func TestOpenDurableOutboxRuntimeDoesNotFallbackToMemoryStore(t *testing.T) {
	t.Parallel()

	factory := productionDurableOutboxRuntimeFactory()
	factory.openOutbox = func(
		context.Context,
		outbox.Config,
		outbox.Deps,
	) (*outbox.Outbox, error) {
		return nil, outbox.ErrOutboxKeyUnavailable
	}
	runtime, err := openDurableOutboxRuntime(
		context.Background(),
		durableRuntimeTestConfig(t),
		durableRuntimeTestDeps(
			&durableRuntimeTestSession{},
			newDurableRuntimeTestKeyProvider(),
			durableRuntimeTestIDGenerator("no-memory"),
		),
		factory,
	)
	if err == nil || runtime != nil {
		t.Fatalf("runtime = %#v, error = %v, want nil and error", runtime, err)
	}
}

func TestOpenDurableOutboxRuntimeDoesNotFallbackToDirectSend(t *testing.T) {
	t.Parallel()

	session := &durableRuntimeTestSession{}
	factory := productionDurableOutboxRuntimeFactory()
	factory.openOutbox = func(
		context.Context,
		outbox.Config,
		outbox.Deps,
	) (*outbox.Outbox, error) {
		return nil, outbox.ErrOutboxKeyUnavailable
	}
	_, err := openDurableOutboxRuntime(
		context.Background(),
		durableRuntimeTestConfig(t),
		durableRuntimeTestDeps(
			session,
			newDurableRuntimeTestKeyProvider(),
			durableRuntimeTestIDGenerator("no-direct"),
		),
		factory,
	)
	if err == nil {
		t.Fatal("error = nil, want non-nil")
	}
	if session.calls.Load() != 0 {
		t.Fatalf("Telegram calls = %d, want 0", session.calls.Load())
	}
}

func TestOpenDurableOutboxRuntimePreservesFactoryError(t *testing.T) {
	t.Parallel()

	factory := productionDurableOutboxRuntimeFactory()
	factory.openOutbox = func(
		context.Context,
		outbox.Config,
		outbox.Deps,
	) (*outbox.Outbox, error) {
		return nil, outbox.ErrOutboxDataDirInsecure
	}
	_, err := openDurableOutboxRuntime(
		context.Background(),
		durableRuntimeTestConfig(t),
		durableRuntimeTestDeps(
			&durableRuntimeTestSession{},
			newDurableRuntimeTestKeyProvider(),
			durableRuntimeTestIDGenerator("factory-error"),
		),
		factory,
	)
	if !errors.Is(err, outbox.ErrOutboxDataDirInsecure) {
		t.Fatalf("error = %v, want ErrOutboxDataDirInsecure", err)
	}
}

func TestOpenDurableOutboxRuntimeErrorDoesNotContainText(t *testing.T) {
	t.Parallel()

	const text = "secret message payload"
	store := outbox.NewMemoryStore()
	session := &durableRuntimeTestSession{}
	opened := durableRuntimeTestOpened(store, session)
	factory := durableRuntimeTestFactory(
		opened,
		func(context.Context, *outbox.Dispatcher) error { return nil },
		nil,
	)
	runtime, err := openDurableOutboxRuntime(
		context.Background(),
		durableRuntimeTestConfig(t),
		durableRuntimeTestDeps(session, newDurableRuntimeTestKeyProvider(), durableRuntimeTestIDGenerator("error-text")),
		factory,
	)
	if err != nil {
		t.Fatalf("openDurableOutboxRuntime() error = %v", err)
	}
	durableRuntimeTestWait(t, runtime.Done())
	_, err = runtime.Submitter().SubmitMessage(
		context.Background(),
		"account-1",
		42,
		text,
	)
	if !errors.Is(err, ErrMessageDeliveryUnavailable) {
		t.Fatalf("SubmitMessage() error = %v, want ErrMessageDeliveryUnavailable", err)
	}
	if strings.Contains(err.Error(), text) {
		t.Fatal("SubmitMessage() error contains message text")
	}
	_ = runtime.Close()
}

func TestDurableOutboxRuntimeNilReceiver(t *testing.T) {
	t.Parallel()

	var runtime *DurableOutboxRuntime
	if runtime.Submitter() != nil {
		t.Fatal("Submitter() != nil")
	}
	if runtime.StatusSource() != nil {
		t.Fatal("StatusSource() != nil")
	}
	if runtime.Done() != nil {
		t.Fatal("Done() != nil")
	}
	if runtime.Err() != nil {
		t.Fatal("Err() != nil")
	}
	if err := runtime.Close(); err != nil {
		t.Fatalf("Close() error = %v, want nil", err)
	}
}

type durableRuntimeStoreWithoutStatusReader struct {
	outbox.Store
}

func TestOpenDurableOutboxRuntimeExposesStatusSource(t *testing.T) {
	t.Parallel()

	store := outbox.NewMemoryStore()
	session := &durableRuntimeTestSession{}
	started := make(chan struct{})
	opened := durableRuntimeTestOpened(store, session)
	factory := durableRuntimeTestFactory(
		opened,
		durableRuntimeRunUntilCanceled(started),
		nil,
	)
	runtime, err := openDurableOutboxRuntime(
		context.Background(),
		durableRuntimeTestConfig(t),
		durableRuntimeTestDeps(
			session,
			newDurableRuntimeTestKeyProvider(),
			durableRuntimeTestIDGenerator("status-source"),
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
	durableRuntimeTestWait(t, started)

	source := runtime.StatusSource()
	if source == nil {
		t.Fatal("StatusSource() = nil")
	}

	if _, err := runtime.Submitter().SubmitMessage(
		context.Background(),
		"account-1",
		42,
		"hello",
	); err != nil {
		t.Fatalf("SubmitMessage() error = %v", err)
	}

	statuses, err := source.ListMessageStatuses(
		context.Background(),
		"account-1",
		42,
	)
	if err != nil {
		t.Fatalf("ListMessageStatuses() error = %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("statuses = %#v, want one entry", statuses)
	}
	if statuses[0].AccountKey != "account-1" || statuses[0].ChatID != 42 {
		t.Fatalf("status = %#v, want account-1 chat 42", statuses[0])
	}
	if statuses[0].EntryID == "" {
		t.Fatal("status entry id is empty")
	}
	switch statuses[0].State {
	case MessageDeliveryQueued, MessageDeliverySending, MessageDeliverySent:
	default:
		t.Fatalf("status state = %q, want delivery progress", statuses[0].State)
	}
}

func TestOpenDurableOutboxRuntimeRejectsStoreWithoutStatusReader(t *testing.T) {
	t.Parallel()

	session := &durableRuntimeTestSession{}
	opened := durableRuntimeTestOpened(
		durableRuntimeStoreWithoutStatusReader{Store: outbox.NewMemoryStore()},
		session,
	)

	var closeCalls atomic.Int32
	var runCalls atomic.Int32
	factory := durableRuntimeTestFactory(
		opened,
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
			durableRuntimeTestIDGenerator("no-status-reader"),
		),
		factory,
	)
	if err == nil {
		t.Fatal("error = nil, want non-nil")
	}
	if runtime != nil {
		t.Fatalf("runtime = %#v, want nil", runtime)
	}
	if !strings.Contains(err.Error(), "status") {
		t.Fatalf("error = %v, want status capability error", err)
	}
	if closeCalls.Load() != 1 {
		t.Fatalf("close calls = %d, want 1", closeCalls.Load())
	}
	if runCalls.Load() != 0 {
		t.Fatalf("dispatcher runs = %d, want 0", runCalls.Load())
	}
}

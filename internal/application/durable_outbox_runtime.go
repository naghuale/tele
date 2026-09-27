package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"telecli/internal/outbox"
)

type DurableOutboxRuntimeConfig struct {
	Outbox outbox.Config
}

type DurableOutboxRuntimeDeps struct {
	KeyProvider outbox.KeyProvider
	Session     TelegramSender
	Clock       outbox.Clock
	IDGenerator OutboxIDGenerator
	Notifier    DispatchNotifier
	AccountKey  string
}

type DurableOutboxRuntime struct {
	submitter      ComposerMessageSubmitter
	statusSource   MessageStatusSource
	pendingSources PendingMessageSource
	healthSource   MessageDeliveryHealthSource
	opened         *outbox.Outbox

	dispatcherCancel context.CancelFunc
	dispatcherDone   chan struct{}

	available atomic.Bool

	mu            sync.Mutex
	dispatcherErr error
	closeErr      error
	healthState   MessageDeliveryHealthState

	closeOnce sync.Once
	closed    chan struct{}

	closeOutbox func(*outbox.Outbox) error
}

type durableOutboxRuntimeFactory struct {
	openOutbox    func(context.Context, outbox.Config, outbox.Deps) (*outbox.Outbox, error)
	runDispatcher func(context.Context, *outbox.Dispatcher) error
	closeOutbox   func(*outbox.Outbox) error
}

func productionDurableOutboxRuntimeFactory() durableOutboxRuntimeFactory {
	return durableOutboxRuntimeFactory{
		openOutbox: outbox.Open,
		runDispatcher: func(
			ctx context.Context,
			dispatcher *outbox.Dispatcher,
		) error {
			return dispatcher.Run(ctx)
		},
		closeOutbox: func(opened *outbox.Outbox) error {
			if opened == nil {
				return nil
			}
			return opened.Close()
		},
	}
}

func OpenDurableOutboxRuntime(
	ctx context.Context,
	cfg DurableOutboxRuntimeConfig,
	deps DurableOutboxRuntimeDeps,
) (*DurableOutboxRuntime, error) {
	return openDurableOutboxRuntime(
		ctx,
		cfg,
		deps,
		productionDurableOutboxRuntimeFactory(),
	)
}

func openDurableOutboxRuntime(
	ctx context.Context,
	cfg DurableOutboxRuntimeConfig,
	deps DurableOutboxRuntimeDeps,
	factory durableOutboxRuntimeFactory,
) (*DurableOutboxRuntime, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if factory.openOutbox == nil {
		return nil, errors.New("durable outbox runtime: nil outbox factory")
	}
	if factory.runDispatcher == nil {
		return nil, errors.New("durable outbox runtime: nil dispatcher runner")
	}
	if deps.KeyProvider == nil {
		return nil, errors.New("durable outbox runtime: key provider is required")
	}
	if deps.Session == nil {
		return nil, errors.New("durable outbox runtime: session is required")
	}
	if deps.IDGenerator == nil {
		return nil, errors.New("durable outbox runtime: id generator is required")
	}

	accountKey, err := durableOutboxAccountKey(cfg, deps)
	if err != nil {
		return nil, err
	}

	sender := NewTelegramOutboxSender(deps.Session)
	opened, err := factory.openOutbox(ctx, cfg.Outbox, outbox.Deps{
		KeyProvider: deps.KeyProvider,
		Sender:      sender,
		Clock:       deps.Clock,
		// A session that will send messages owns the queue: it holds
		// the run lock, so `telecli outbox reset` refuses to move the
		// queue file out from under it.
		Exclusive: true,
	})
	if err != nil {
		return nil, err
	}
	if opened == nil {
		return nil, errors.New("durable outbox runtime: factory returned nil outbox")
	}

	cleanup := func(primary error) (*DurableOutboxRuntime, error) {
		if factory.closeOutbox == nil {
			return nil, primary
		}
		return nil, errors.Join(primary, factory.closeOutbox(opened))
	}

	if opened.Store == nil {
		return cleanup(errors.New("durable outbox runtime: opened store is nil"))
	}
	if opened.Dispatcher == nil {
		return cleanup(errors.New("durable outbox runtime: opened dispatcher is nil"))
	}

	statusReader, ok := opened.Store.(outbox.EntryStatusReader)
	if !ok {
		return cleanup(errors.New(
			"durable outbox runtime: opened store does not support status queries",
		))
	}
	statusSource, err := NewOutboxMessageStatusSource(statusReader, 0)
	if err != nil {
		return cleanup(fmt.Errorf(
			"durable outbox runtime: status source: %w",
			err,
		))
	}

	operationalReader, ok := opened.Store.(outbox.OperationalSnapshotReader)
	if !ok {
		return cleanup(errors.New(
			"durable outbox runtime: opened store does not support operational snapshots",
		))
	}

	// Without an explicit notifier a submission wakes the dispatcher,
	// so a new message is sent at once rather than after PollInterval.
	var notifier DispatchNotifier = opened.Dispatcher
	if deps.Notifier != nil {
		notifier = deps.Notifier
	}

	queue, err := NewOutboxMessageSubmitter(
		opened.Store,
		deps.Clock,
		deps.IDGenerator,
		notifier,
		accountKey,
	)
	if err != nil {
		return cleanup(err)
	}

	// The timeline needs the text of what is still in the queue, and the
	// status reader has none of it: that reader is payload-free on purpose.
	// This one is the only reader of the text and the only one the
	// interface sees it through.
	pendingSource, err := NewOutboxPendingMessageSource(opened.Store, 0)
	if err != nil {
		return cleanup(fmt.Errorf(
			"durable outbox runtime: pending message source: %w",
			err,
		))
	}

	runtime := &DurableOutboxRuntime{
		statusSource:   statusSource,
		pendingSources: pendingSource,
		opened:         opened,
		dispatcherDone: make(chan struct{}),
		closed:         make(chan struct{}),
		closeOutbox:    factory.closeOutbox,
		healthState:    MessageDeliveryHealthStarting,
	}
	runtime.healthSource = &durableMessageDeliveryHealthSource{
		runtime: runtime,
		reader:  operationalReader,
	}

	composer, err := NewDurableComposerSubmitter(queue, &runtime.available)
	if err != nil {
		return cleanup(err)
	}
	runtime.submitter = composer

	dispatcherCtx, cancel := context.WithCancel(ctx)
	runtime.dispatcherCancel = cancel
	runtime.available.Store(true)
	if !runtime.transitionMessageDeliveryHealth(
		MessageDeliveryHealthRunning,
	) {
		cancel()
		return cleanup(errors.New(
			"durable outbox runtime: transition health to running",
		))
	}
	go runtime.runDispatcher(
		dispatcherCtx,
		factory.runDispatcher,
		opened.Dispatcher,
	)

	return runtime, nil
}

func durableOutboxAccountKey(
	cfg DurableOutboxRuntimeConfig,
	deps DurableOutboxRuntimeDeps,
) (string, error) {
	accountKey := strings.TrimSpace(deps.AccountKey)
	if accountKey == "" {
		accountKey = strings.TrimSpace(cfg.Outbox.DatabaseID)
	}
	if accountKey == "" {
		return "", errors.New("durable outbox runtime: account key is required")
	}
	return accountKey, nil
}

func (r *DurableOutboxRuntime) runDispatcher(
	ctx context.Context,
	run func(context.Context, *outbox.Dispatcher) error,
	dispatcher *outbox.Dispatcher,
) {
	defer func() {
		r.available.Store(false)
		close(r.dispatcherDone)
	}()

	err := run(ctx, dispatcher)
	canceled := errors.Is(ctx.Err(), context.Canceled)
	if errors.Is(err, context.Canceled) && canceled {
		err = nil
	}

	r.mu.Lock()
	r.dispatcherErr = err
	r.mu.Unlock()

	// A dispatcher that returns without an expected cancellation is a
	// terminal failure even when it reports no error.
	if !canceled {
		r.transitionMessageDeliveryHealth(MessageDeliveryHealthFailed)
	}
}

func (r *DurableOutboxRuntime) Done() <-chan struct{} {
	if r == nil {
		return nil
	}
	return r.dispatcherDone
}

func (r *DurableOutboxRuntime) Err() error {
	if r == nil {
		return nil
	}

	select {
	case <-r.dispatcherDone:
	default:
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	return r.dispatcherErr
}

func (r *DurableOutboxRuntime) Submitter() ComposerMessageSubmitter {
	if r == nil {
		return nil
	}
	return r.submitter
}

func (r *DurableOutboxRuntime) StatusSource() MessageStatusSource {
	if r == nil {
		return nil
	}
	return r.statusSource
}

// PendingMessages returns the queue as the timeline reads it: every entry of
// a chat that Telegram has not accepted yet, with the text the user wrote.
func (r *DurableOutboxRuntime) PendingMessages() PendingMessageSource {
	if r == nil {
		return nil
	}
	return r.pendingSources
}

func (r *DurableOutboxRuntime) HealthSource() MessageDeliveryHealthSource {
	if r == nil {
		return nil
	}
	return r.healthSource
}

func (r *DurableOutboxRuntime) messageDeliveryHealthState() MessageDeliveryHealthState {
	if r == nil {
		return ""
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	return r.healthState
}

// transitionMessageDeliveryHealth applies a lifecycle transition.
//
// A rejected transition reports false instead of panicking: an internal
// invariant violation must not shadow the original runtime error.
func (r *DurableOutboxRuntime) transitionMessageDeliveryHealth(
	next MessageDeliveryHealthState,
) bool {
	if r == nil {
		return false
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if !validMessageDeliveryHealthTransition(r.healthState, next) {
		return false
	}
	r.healthState = next
	return true
}

func (r *DurableOutboxRuntime) Close() error {
	if r == nil {
		return nil
	}
	if r.closed == nil {
		return nil
	}

	r.closeOnce.Do(func() {
		r.available.Store(false)
		r.transitionMessageDeliveryHealth(MessageDeliveryHealthStopping)
		if r.dispatcherCancel != nil {
			r.dispatcherCancel()
		}
		if r.dispatcherDone != nil {
			<-r.dispatcherDone
		}

		var openedCloseErr error
		if r.opened != nil && r.closeOutbox != nil {
			openedCloseErr = r.closeOutbox(r.opened)
		}

		r.mu.Lock()
		r.closeErr = errors.Join(
			wrapRuntimeError("dispatcher stopped", r.dispatcherErr),
			wrapRuntimeError("close durable outbox", openedCloseErr),
		)
		r.mu.Unlock()

		// A runtime that already failed stays failed: a clean cleanup does
		// not rewrite the original cause.
		if r.closeErr != nil {
			r.transitionMessageDeliveryHealth(MessageDeliveryHealthFailed)
		} else {
			r.transitionMessageDeliveryHealth(MessageDeliveryHealthStopped)
		}
		close(r.closed)
	})

	<-r.closed
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.closeErr
}

func wrapRuntimeError(operation string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", operation, err)
}

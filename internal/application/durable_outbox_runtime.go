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
	submitter    ComposerMessageSubmitter
	statusSource MessageStatusSource
	opened       *outbox.Outbox

	dispatcherCancel context.CancelFunc
	dispatcherDone   chan struct{}

	available atomic.Bool

	mu            sync.Mutex
	dispatcherErr error
	closeErr      error

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

	queue, err := NewOutboxMessageSubmitter(
		opened.Store,
		deps.Clock,
		deps.IDGenerator,
		deps.Notifier,
		accountKey,
	)
	if err != nil {
		return cleanup(err)
	}

	runtime := &DurableOutboxRuntime{
		statusSource:   statusSource,
		opened:         opened,
		dispatcherDone: make(chan struct{}),
		closed:         make(chan struct{}),
		closeOutbox:    factory.closeOutbox,
	}

	composer, err := NewDurableComposerSubmitter(queue, &runtime.available)
	if err != nil {
		return cleanup(err)
	}
	runtime.submitter = composer

	dispatcherCtx, cancel := context.WithCancel(ctx)
	runtime.dispatcherCancel = cancel
	runtime.available.Store(true)
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
	if errors.Is(err, context.Canceled) &&
		errors.Is(ctx.Err(), context.Canceled) {
		err = nil
	}

	r.mu.Lock()
	r.dispatcherErr = err
	r.mu.Unlock()
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

func (r *DurableOutboxRuntime) Close() error {
	if r == nil {
		return nil
	}
	if r.closed == nil {
		return nil
	}

	r.closeOnce.Do(func() {
		r.available.Store(false)
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

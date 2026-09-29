package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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

	// MessageEvents is the live store the send-result reconciler reads.
	//
	// It is optional: a runtime opened without one still sends, and its
	// entries stay accepted until the next run resolves them. That is a
	// smaller program than a client with Telegram attached, and it is
	// the shape a test drives the durable runtime in.
	MessageEvents telegramMessageEvents

	// Logger receives the reconciler's and the dispatcher's diagnostics.
	// Message text is never logged; error strings go through
	// outbox.SafeReason.
	//
	// The composition root passes the logger the program's mode calls
	// for: a file while the interface is on the screen, the terminal
	// outside it. A nil value discards, because the default destination
	// is the terminal and the terminal may belong to a running interface.
	Logger *slog.Logger

	// History reads a chat's recent messages so that records a
	// previous process left accepted can be settled at startup. It is
	// optional: without it the settlement reports that it could not run
	// rather than guessing, and the records stay as they are.
	History TelegramHistoryReader
}

// logger is the logger the dependency block falls back to.
//
// A nil logger discards. The composition root passes the real one, and a
// component built without it says nothing rather than writing over a
// running interface.
func (d DurableOutboxRuntimeDeps) logger() *slog.Logger {
	if d.Logger == nil {
		return discardLogger()
	}
	return d.Logger
}

type DurableOutboxRuntime struct {
	submitter      ComposerMessageSubmitter
	cancelSubmit   MessageSubmitter
	statusSource   MessageStatusSource
	pendingSources PendingMessageSource
	healthSource   MessageDeliveryHealthSource
	opened         *outbox.Outbox

	dispatcherCancel context.CancelFunc
	dispatcherDone   chan struct{}

	// reconcilerCancel and reconcilerDone own the send-result
	// reconciler, which is stopped and awaited before the store is
	// closed for the same reason the dispatcher is: the store must never
	// be read after shutdown.
	reconcilerCancel context.CancelFunc
	reconcilerDone   chan struct{}

	// reconciler is kept so the counters can be flushed when the
	// runtime stops. The reconciler flushes them itself on the way out,
	// and this is the belt to that pair of braces: a runtime that is
	// closed with the goroutine already gone still leaves a reading for
	// the doctor that follows.
	reconciler *sendResultReconciler

	available atomic.Bool

	mu            sync.Mutex
	dispatcherErr error
	reconcilerErr error
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

	// The dispatcher is the other half of the queue that logs, and it is
	// given the same logger as the reconciler. One message going out is
	// logged twice otherwise — once by each — and the two halves have to
	// end up in the same file or the report is half a story.
	if cfg.Outbox.Dispatcher.Logger == nil {
		cfg.Outbox.Dispatcher.Logger = deps.logger()
	}

	sender := NewTelegramOutboxSender(deps.Session)
	opened, err := factory.openOutbox(ctx, cfg.Outbox, outbox.Deps{
		KeyProvider: deps.KeyProvider,
		Sender:      sender,
		Clock:       deps.Clock,
		// The store reports the records a read could not make sense of
		// through this, and the dispatcher's own diagnostics go to the
		// same place. It is the same logger the reconciler was given, so
		// everything the queue has to say lands in one file.
		Logger: deps.logger(),
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
		submitter:      nil,
		cancelSubmit:   queue,
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

	// Records a previous process left accepted are settled before
	// anything new is dispatched.
	//
	// It runs first, and synchronously, because the whole point is that
	// no live update is coming for them: this is the only moment at
	// which the queue can still ask the chat what became of a message
	// whose confirmation was delivered to a process that is gone.
	//
	// A failure here is logged and not fatal. The program can send, and
	// a queue whose old records are unresolved is still better than a
	// program that refuses to start.
	if settler, ok := opened.Store.(outbox.UnsettledAcceptedStore); ok {
		counts, settleErr := (&restartSettler{
			store:      settler,
			entries:    opened.Store,
			history:    deps.History,
			accountKey: accountKey,
			clock:      deps.Clock,
			logger:     deps.Logger,
		}).Settle(ctx)
		switch {
		case settleErr != nil:
			// The step and the kind, never the cause's text: a reason can
			// name a file and a chat, and this is a file on the owner's
			// disk. And never the type of this package's own wrapper,
			// which is what the last three runs of this reported.
			attrs := []any{
				slog.String("summary", settlementSummary(counts)),
			}
			attrs = append(attrs, settlementErrorAttrs(settleErr)...)
			deps.logger().Warn(
				"startup settlement of earlier records stopped",
				attrs...,
			)
		case counts.considered > 0:
			deps.logger().Info(
				"startup settlement of earlier records",
				slog.String("summary", settlementSummary(counts)),
			)
		}
	}

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

	// The reconciler runs beside the dispatcher, not inside it: the
	// dispatcher's job ends when TDLib takes the message, and everything
	// after that arrives as an update with no request behind it.
	//
	// It is started only when the store can correlate a send result and
	// the caller brought a live store to read. A store without the
	// capability would leave entries accepted forever, which is the bug
	// this exists to fix, so it is reported rather than started quietly.
	resultStore, supportsResults := opened.Store.(outbox.SendResultStore)
	switch {
	case deps.MessageEvents == nil:
		// No live store: nothing to reconcile from.
	case !supportsResults:
		return cleanup(errors.New(
			"durable outbox runtime: opened store cannot apply send results",
		))
	default:
		reconciler := &sendResultReconciler{
			store:      resultStore,
			events:     deps.MessageEvents,
			accountKey: accountKey,
			clock:      deps.Clock,
			logger:     deps.Logger,
			counters:   &sendResultCounters{},
		}

		// The counters are written next to the queue so that a later
		// `telecli doctor` can read them. A user whose messages are
		// stuck cannot attach a debugger to a running program, and a
		// number is the whole of what doctor is allowed to print.
		if dataDir := strings.TrimSpace(cfg.Outbox.DataDir); dataDir != "" {
			reconciler.sink = newSendResultCounterFile(dataDir)
		}

		reconcilerCtx, cancelReconciler :=
			context.WithCancel(ctx)
		runtime.reconcilerCancel = cancelReconciler
		runtime.reconcilerDone = make(chan struct{})
		runtime.reconciler = reconciler
		go runtime.runReconciler(
			reconcilerCtx, runtime.reconcilerDone, reconciler,
		)
	}

	return runtime, nil
}

// runReconciler runs the reconciler and records that it has stopped.
//
// A reconciler that returns while the runtime is still running is a
// defect rather than an exit condition: it means the store went away
// under a program that is still sending, and the only honest response is
// to fail the runtime rather than to keep a queue nothing resolves.
func (r *DurableOutboxRuntime) runReconciler(
	ctx context.Context,
	done chan struct{},
	reconciler *sendResultReconciler,
) {
	defer close(done)

	if err := reconciler.Run(ctx); err != nil {
		if ctx.Err() != nil {
			return
		}
		r.mu.Lock()
		r.reconcilerErr = err
		r.mu.Unlock()
		r.transitionMessageDeliveryHealth(MessageDeliveryHealthFailed)
	}
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

// CancelSubmitter returns the durable submitter for the action sheet of
// §13, which cancels a record the composer submitter cannot name.
func (r *DurableOutboxRuntime) CancelSubmitter() MessageSubmitter {
	if r == nil {
		return nil
	}

	return r.cancelSubmit
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
		// The reconciler is stopped and awaited before the store is
		// closed, for the same reason: it reads and writes the store, and
		// a store read after shutdown is a use-after-close that SQLite
		// may answer with anything.
		if r.reconcilerCancel != nil {
			r.reconcilerCancel()
		}
		if r.reconcilerDone != nil {
			<-r.reconcilerDone
		}
		if r.reconciler != nil {
			r.reconciler.flushCounters()
		}

		var openedCloseErr error
		if r.opened != nil && r.closeOutbox != nil {
			openedCloseErr = r.closeOutbox(r.opened)
		}

		r.mu.Lock()
		r.closeErr = errors.Join(
			wrapRuntimeError("dispatcher stopped", r.dispatcherErr),
			wrapRuntimeError(
				"send result reconciler stopped", r.reconcilerErr,
			),
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

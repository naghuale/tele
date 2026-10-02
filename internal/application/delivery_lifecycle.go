package application

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"telecli/internal/config"
	"telecli/internal/outbox"
	"telecli/internal/telegram"
	"telecli/internal/tui"
)

type deliverySession interface {
	TelegramChats
	TelegramSender
	TelegramChatLifecycle
	TelegramMessageViewing
	TelegramChatAccessReader

	// LiveState is the store TDLib updates are applied to, and the only
	// place the connection state exists. It is a required capability
	// rather than an optional one: the interface is started through this
	// session, and a session that cannot say whether it is connected
	// would leave the status line silent for a reason nobody chose.
	//
	// The store answers for itself when it is nil, so a session that has
	// none reports the connection as unknown rather than failing here.
	LiveState() *telegram.LiveState

	Close(context.Context) error
}

type deliveryOpenFunc func(
	context.Context,
	MessageDeliveryRuntimeConfig,
	MessageDeliveryRuntimeDeps,
) (MessageDeliveryRuntime, error)

// deliveryHealthSampling configures durable health sampling.
//
// Sampling is optional: a nil Recorder disables it and delivery continues
// unchanged. A nil Clock selects the system clock and a non-positive Interval
// selects the application default.
type deliveryHealthSampling struct {
	Recorder MessageDeliveryHealthRecorder
	Clock    outbox.Clock
	Interval time.Duration
}

// messageDeliveryHealthSamplingHandle owns the sampling goroutine.
type messageDeliveryHealthSamplingHandle struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// stop cancels sampling and waits for the goroutine to finish.
//
// Waiting is mandatory: the health source reads the open outbox store, so
// sampling must never outlive it.
func (h *messageDeliveryHealthSamplingHandle) stop() {
	if h == nil {
		return
	}
	h.cancel()
	<-h.done
}

// startDeliveryHealthSampling starts at most one sampler for a durable
// runtime.
//
// Direct mode has no health source, so no sampler and no goroutine are
// created and no synthetic health is recorded. A construction failure disables
// sampling instead of failing startup: telemetry is an observability path, not
// a delivery dependency.
func startDeliveryHealthSampling(
	ctx context.Context,
	source MessageDeliveryHealthSource,
	sampling deliveryHealthSampling,
) *messageDeliveryHealthSamplingHandle {
	if source == nil || sampling.Recorder == nil {
		return nil
	}

	clock := sampling.Clock
	if clock == nil {
		clock = outbox.SystemClock{}
	}
	interval := sampling.Interval
	if interval <= 0 {
		interval = defaultMessageDeliveryHealthSampleInterval
	}

	sampler, err := NewMessageDeliveryHealthSampler(
		MessageDeliveryHealthSamplerConfig{Interval: interval},
		MessageDeliveryHealthSamplerDeps{
			Source:   source,
			Recorder: sampling.Recorder,
			Clock:    clock,
		},
	)
	if err != nil {
		return nil
	}

	samplerCtx, cancel := context.WithCancel(ctx)
	handle := &messageDeliveryHealthSamplingHandle{
		cancel: cancel,
		done:   make(chan struct{}),
	}
	go func() {
		defer close(handle.done)
		// A sampler error is never surfaced as a delivery error.
		_ = sampler.Run(samplerCtx)
	}()

	return handle
}

func messageDeliveryConfigFromConfig(
	cfg config.Config,
) MessageDeliveryRuntimeConfig {
	return MessageDeliveryRuntimeConfig{
		Mode: cfg.MessageDelivery.Mode,
		Durable: DurableOutboxRuntimeConfig{
			Outbox: outbox.Config{
				DataDir:    cfg.MessageDelivery.DataDir,
				DatabaseID: cfg.MessageDelivery.DatabaseID,
				InstanceID: cfg.MessageDelivery.InstanceID,
				Dispatcher: outbox.DefaultDispatcherConfig(
					cfg.MessageDelivery.InstanceID,
				),
			},
		},
	}
}

func deliveryAccountKey(cfg config.Config) string {
	if key := strings.TrimSpace(cfg.MessageDelivery.DatabaseID); key != "" {
		return key
	}
	return "telecli"
}

func productionOutboxID() (outbox.ID, error) {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", fmt.Errorf("generate outbox id: %w", err)
	}
	return outbox.ID(hex.EncodeToString(data[:])), nil
}

func productionDurableRuntimeDeps(
	session TelegramSender,
	live *telegram.LiveState,
	cfg config.Config,
	logger *slog.Logger,
) DurableOutboxRuntimeDeps {
	return DurableOutboxRuntimeDeps{
		KeyProvider: outbox.NewPlatformKeyProvider(),
		Session:     session,
		IDGenerator: productionOutboxID,
		AccountKey:  deliveryAccountKey(cfg),
		// The live store is where TDLib's send results land. Without it
		// the queue would know a message was accepted and never learn
		// that Telegram sent it, which is the whole chain this runtime
		// exists to carry out.
		MessageEvents: live,
		// The session is also how a record a previous process left
		// accepted is settled at startup: its confirmation was delivered
		// to a process that no longer exists, and the chat is the only
		// place left that can say what became of the message.
		History: sessionHistoryReader(session),
		// Every reason the queue produces goes where the program's mode
		// says: a file while the interface is on the screen, the
		// terminal outside it. Nothing here falls back to a default,
		// because a default is the terminal.
		Logger: logger,
	}
}

// sessionHistoryReader returns the session as a history reader, or nil
// when it is not one.
//
// A typed nil in this interface would read as "a history reader that
// cannot read anything", which is worse than no reader at all: the
// settlement would ask and be answered with an error, once per chat,
// instead of saying up front that it cannot run.
func sessionHistoryReader(session TelegramSender) TelegramHistoryReader {
	reader, ok := session.(TelegramHistoryReader)
	if !ok {
		return nil
	}
	return reader
}

// deliveryLoggerOption carries the logger the delivery components report
// through.
//
// It is a slice of options rather than a parameter because the same
// function builds the runtime for every caller, and the ones that are
// tests do not have a log file to offer. A nil option is the whole
// default: components discard.
type deliveryLoggerOption func(*deliveryLoggerSettings)

type deliveryLoggerSettings struct {
	logger *slog.Logger
}

// deliveryLogger returns the option that gives the delivery components a
// logger.
func deliveryLogger(logger *slog.Logger) deliveryLoggerOption {
	return func(settings *deliveryLoggerSettings) {
		settings.logger = logger
	}
}

func prepareDeliveryAuthResult(
	ctx context.Context,
	cfg config.Config,
	session deliverySession,
	cancel context.CancelCauseFunc,
	openDelivery deliveryOpenFunc,
	sampling deliveryHealthSampling,
	ownUserID int64,
	options ...deliveryLoggerOption,
) (AuthRunResult, error) {
	settings := &deliveryLoggerSettings{}
	for _, option := range options {
		if option != nil {
			option(settings)
		}
	}
	if err := ctx.Err(); err != nil {
		return AuthRunResult{}, err
	}
	if session == nil {
		return AuthRunResult{}, errors.New("Telegram session is required")
	}
	if cancel == nil {
		return AuthRunResult{}, errors.New("application cancel function is required")
	}
	if openDelivery == nil {
		return AuthRunResult{}, errors.New("message delivery opener is required")
	}

	// No direct submitter is built here. Durable is the only send mode,
	// so a composer submitter that can bypass the outbox is never
	// constructed on a production path.
	deps := MessageDeliveryRuntimeDeps{}
	if cfg.MessageDelivery.Mode == config.MessageSendModeDurable {
		deps.Durable = productionDurableRuntimeDeps(
			session, session.LiveState(), cfg, settings.logger,
		)
	}

	delivery, err := openDelivery(
		ctx,
		messageDeliveryConfigFromConfig(cfg),
		deps,
	)
	if err != nil {
		// The durable outbox could not be opened. That is not a startup
		// failure: the session is usable, the user can read and write in
		// the composer, and only sending is paused. Falling back to a
		// direct send would lose messages silently, which is the thing
		// the outbox exists to prevent, so the TUI starts with a
		// submitter that refuses and keeps the draft.
		return sendingPausedAuthResult(session, cfg, ownUserID, err), nil
	}
	if delivery == nil {
		return AuthRunResult{}, errors.Join(
			errors.New("message delivery runtime is nil"),
			closeDeliverySession(session, cfg.TDLib.ShutdownTimeoutMS),
		)
	}
	if delivery.Submitter() == nil {
		return AuthRunResult{}, errors.Join(
			errors.New("message delivery runtime returned nil submitter"),
			delivery.Close(),
			closeDeliverySession(session, cfg.TDLib.ShutdownTimeoutMS),
		)
	}

	accountKey := deliveryAccountKey(cfg)

	tuiSubmitter, err := NewTUISubmitter(
		delivery.Submitter(),
		accountKey,
	)
	if err != nil {
		return AuthRunResult{}, errors.Join(
			fmt.Errorf("create TUI message submitter: %w", err),
			delivery.Close(),
			closeDeliverySession(session, cfg.TDLib.ShutdownTimeoutMS),
		)
	}

	// The sheet of §13 cancels through the same submitter the composer
	// queues through: one object answers both questions about a record, and
	// a second writer to the store would be a second answer.
	//
	// A runtime with no durable submitter has nothing to cancel - direct
	// delivery puts a message in the history as soon as it is sent - and
	// the interface then has a menu whose cancel cannot be performed. That
	// is a smaller thing than a program that will not start.
	var canceller tui.MessageCanceller
	if cancelSubmit := delivery.CancelSubmitter(); cancelSubmit != nil {
		built, err := NewOutboxPendingMessageCanceller(cancelSubmit)
		if err != nil {
			return AuthRunResult{}, errors.Join(
				fmt.Errorf("create pending message canceller: %w", err),
				delivery.Close(),
				closeDeliverySession(session, cfg.TDLib.ShutdownTimeoutMS),
			)
		}
		canceller = built
	}

	samplingHandle := startDeliveryHealthSampling(
		ctx,
		delivery.HealthSource(),
		sampling,
	)

	var closing atomic.Bool
	if done := delivery.Done(); done != nil {
		go func() {
			select {
			case <-done:
				if closing.Load() {
					return
				}
				deliveryErr := delivery.Err()
				if deliveryErr == nil {
					deliveryErr = errors.New(
						"message delivery runtime stopped unexpectedly",
					)
				}
				cancel(fmt.Errorf(
					"message delivery runtime stopped: %w",
					deliveryErr,
				))
			case <-ctx.Done():
			}
		}()
	}

	// The status line reads the same two sources the rest of the delivery
	// path reads: the live Telegram state for the connection, and the
	// durable health for the queue. Neither is a subscription, so the
	// interface keeps its own cadence over them.
	//
	// A runtime with no health source has no queue to count, and a
	// half-built source would report an empty queue for a store it never
	// read. Without it the interface draws no status line, which is the
	// truth about a mode that has no queue.
	var statusSummaries tui.StatusSummarySource
	if health := delivery.HealthSource(); health != nil {
		summarySource, err := NewLiveStatusSummarySource(
			session.LiveState(),
			health,
			ownUserID,
		)
		if err != nil {
			return AuthRunResult{}, errors.Join(
				fmt.Errorf("create status summary source: %w", err),
				delivery.Close(),
				closeDeliverySession(session, cfg.TDLib.ShutdownTimeoutMS),
			)
		}
		statusSummaries = summarySource
	}

	// What this account may write in the chat that is open, so that a
	// channel it does not post in is drawn with a line where the composer
	// would be. It is built whether or not there is a queue: the rights of a
	// chat are a property of the account, and a mode without a durable
	// store has no reason to offer a field Telegram will refuse.
	chatAccess, err := NewTelegramChatAccessSource(session, session.LiveState())
	if err != nil {
		return AuthRunResult{}, errors.Join(
			fmt.Errorf("create chat access source: %w", err),
			delivery.Close(),
			closeDeliverySession(session, cfg.TDLib.ShutdownTimeoutMS),
		)
	}

	// The chat list that moves on its own. It is asked of TDLib before the
	// interface starts, because a store nobody has asked for a list in is
	// empty and there is nothing live to read from it.
	liveUpdates := liveChatUpdatesFor(ctx, session, settings.logger)

	return AuthRunResult{
		Source:           NewTelegramChatServiceFor(session, ownUserID),
		LiveUpdates:      liveUpdates,
		Submitter:        tuiSubmitter,
		AccountKey:       accountKey,
		MessageStatuses:  newTUIMessageStatusSourceAdapter(delivery.StatusSource()),
		PendingMessages:  newTUIPendingMessageSourceAdapter(delivery.PendingMessages()),
		StatusSummaries:  statusSummaries,
		ChatAccess:       chatAccess,
		PresenceOpener:   &TelegramChatPresenceOpener{session: session},
		MessageViewer:    &TelegramMessageViewer{session: session},
		MessageCanceller: canceller,
		Close: func(shutdownCtx context.Context) error {
			closing.Store(true)
			if shutdownCtx == nil {
				shutdownCtx = context.Background()
			}
			// Sampling is stopped and awaited before the durable outbox is
			// closed, so the store is never read after shutdown.
			samplingHandle.stop()
			return errors.Join(
				wrapCloseError(
					"close message delivery runtime",
					delivery.Close(),
				),
				wrapCloseError(
					"close Telegram session",
					session.Close(shutdownCtx),
				),
			)
		},
	}, nil
}

// liveChatUpdatesFor builds the adapter over the live store of a session and
// asks TDLib for the chat list that fills it.
//
// A store that cannot be built is not a failure of the program: the list is
// loaded at startup, `R` loads it again, and the interface says in its
// status line that the list is not moving by itself. That sentence is why a
// nil here is allowed to reach the model rather than stopping the start.
func liveChatUpdatesFor(
	ctx context.Context,
	session LiveChatSession,
	logger *slog.Logger,
) tui.ChatLiveSource {
	if session == nil {
		return nil
	}

	store := session.LiveState()
	if store == nil {
		return nil
	}

	// The two capabilities of a session that the chat list needs are asked
	// for by what it can answer, the same way the chat service asks for the
	// names it reads. A session that can answer neither still has a live
	// list: it is one that never moves, and the status line says so.
	adapter, err := NewTelegramLiveUpdates(store, senderNamesOf(session))
	if err != nil {
		return nil
	}

	loader, canLoad := session.(LiveChatLoader)
	if !canLoad {
		return adapter
	}

	// The refusal goes to the log and not to the screen: the cause can
	// name a file and a path, and the status line says what a user can do
	// about it (R reloads the list) rather than why it failed.
	if err := adapter.Load(ctx, loader, nil); err != nil && logger != nil {
		logger.Warn(
			"telegram live chat list not loaded",
			slog.String("error", outbox.SafeReason(err)),
		)
	}

	return adapter
}

// LiveChatSession is the part of a session the live chat list is built from.
//
// It is one method rather than the whole deliverySession because the live
// list needs three things of a session and none of the rest: the store, the
// names of whoever sent a message, and the question that fills the store
// with a chat list. The last two are asked of the session by what it can
// answer, so a session that has neither still has a live list — one that
// never moves, which the interface says in its status line.
type LiveChatSession interface {
	LiveState() *telegram.LiveState
}

// senderNamesOf returns the reader of names a session can answer with, or
// nil where it cannot.
func senderNamesOf(session LiveChatSession) TelegramSenderNames {
	names, canName := session.(TelegramSenderNames)
	if !canName {
		return nil
	}

	return names
}

// sendingPausedAuthResult builds the result for a session whose durable
// outbox could not be opened.
//
// The TUI is fully usable: chats, history and the composer all work. Only
// sending is paused, and the submitter refuses rather than falling back to
// a direct send. MessageStatuses is nil because there is no outbox to read
// delivery state from.
func sendingPausedAuthResult(
	session deliverySession,
	cfg config.Config,
	ownUserID int64,
	openErr error,
) AuthRunResult {
	reason := classifySendingPaused(openErr)
	paused := &SendingPausedError{reason: reason, cause: openErr}

	// Only sending is paused. The chat list is read from Telegram and the
	// conversation is read from it, so a paused queue does not stop the
	// list from moving either.
	liveUpdates := liveChatUpdatesFor(context.Background(), session, nil)

	accountKey := deliveryAccountKey(cfg)

	tuiSubmitter, err := NewTUISubmitter(
		NewUnavailableComposerSubmitter(reason),
		accountKey,
	)
	if err != nil {
		// Unreachable: the submitter and account key are both present.
		// Returning a nil submitter here would be worse than panicking,
		// so fall back to a submitter that always refuses.
		tuiSubmitter = &TUISubmitter{
			delegate:   NewUnavailableComposerSubmitter(reason),
			accountKey: accountKey,
		}
	}

	return AuthRunResult{
		Source:          NewTelegramChatServiceFor(session, ownUserID),
		LiveUpdates:     liveUpdates,
		Submitter:       tuiSubmitter,
		AccountKey:      accountKey,
		MessageStatuses: nil,
		SendingPaused:   paused,
		Close: func(shutdownCtx context.Context) error {
			if shutdownCtx == nil {
				shutdownCtx = context.Background()
			}
			return wrapCloseError(
				"close Telegram session",
				session.Close(shutdownCtx),
			)
		},
	}
}

func closeDeliverySession(
	session deliverySession,
	shutdownTimeoutMS int,
) error {
	timeout := time.Duration(shutdownTimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return wrapCloseError("close Telegram session", session.Close(ctx))
}

func wrapCloseError(operation string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", operation, err)
}

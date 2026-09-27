package application

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
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
	cfg config.Config,
) DurableOutboxRuntimeDeps {
	return DurableOutboxRuntimeDeps{
		KeyProvider: outbox.NewPlatformKeyProvider(),
		Session:     session,
		IDGenerator: productionOutboxID,
		AccountKey:  deliveryAccountKey(cfg),
	}
}

func prepareDeliveryAuthResult(
	ctx context.Context,
	cfg config.Config,
	session deliverySession,
	cancel context.CancelCauseFunc,
	openDelivery deliveryOpenFunc,
	sampling deliveryHealthSampling,
) (AuthRunResult, error) {
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
		deps.Durable = productionDurableRuntimeDeps(session, cfg)
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
		return sendingPausedAuthResult(session, cfg, err), nil
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

	return AuthRunResult{
		Source:          NewTelegramChatService(session),
		Submitter:       tuiSubmitter,
		AccountKey:      accountKey,
		MessageStatuses: newTUIMessageStatusSourceAdapter(delivery.StatusSource()),
		PendingMessages: newTUIPendingMessageSourceAdapter(delivery.PendingMessages()),
		StatusSummaries: statusSummaries,
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
	openErr error,
) AuthRunResult {
	reason := classifySendingPaused(openErr)
	paused := &SendingPausedError{reason: reason, cause: openErr}

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
		Source:          NewTelegramChatService(session),
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

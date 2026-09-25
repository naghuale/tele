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
)

type deliverySession interface {
	TelegramChats
	TelegramSender
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

	direct, err := NewDirectComposerSubmitter(session)
	if err != nil {
		return AuthRunResult{}, errors.Join(
			fmt.Errorf("create direct message submitter: %w", err),
			closeDeliverySession(session, cfg.TDLib.ShutdownTimeoutMS),
		)
	}

	deps := MessageDeliveryRuntimeDeps{
		DirectSubmitter: direct,
	}
	if cfg.MessageDelivery.Mode == config.MessageSendModeDurable {
		deps.Durable = productionDurableRuntimeDeps(session, cfg)
	}

	delivery, err := openDelivery(
		ctx,
		messageDeliveryConfigFromConfig(cfg),
		deps,
	)
	if err != nil {
		return AuthRunResult{}, errors.Join(
			fmt.Errorf("open message delivery runtime: %w", err),
			closeDeliverySession(session, cfg.TDLib.ShutdownTimeoutMS),
		)
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

	return AuthRunResult{
		Source:          NewTelegramChatService(session),
		Submitter:       tuiSubmitter,
		AccountKey:      accountKey,
		MessageStatuses: newTUIMessageStatusSourceAdapter(delivery.StatusSource()),
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

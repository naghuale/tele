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

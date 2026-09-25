package application

import (
	"context"
	"errors"
	"fmt"

	"telecli/internal/config"
)

type MessageDeliveryRuntime interface {
	Submitter() ComposerMessageSubmitter
	StatusSource() MessageStatusSource
	HealthSource() MessageDeliveryHealthSource
	Done() <-chan struct{}
	Err() error
	Close() error
}

type MessageDeliveryRuntimeConfig struct {
	Mode    config.MessageSendMode
	Durable DurableOutboxRuntimeConfig
}

type MessageDeliveryRuntimeDeps struct {
	DirectSubmitter ComposerMessageSubmitter
	Durable         DurableOutboxRuntimeDeps
}

type messageDeliveryRuntimeFactory struct {
	openDurable func(
		context.Context,
		DurableOutboxRuntimeConfig,
		DurableOutboxRuntimeDeps,
	) (MessageDeliveryRuntime, error)
}

func productionMessageDeliveryRuntimeFactory() messageDeliveryRuntimeFactory {
	return messageDeliveryRuntimeFactory{
		openDurable: func(
			ctx context.Context,
			cfg DurableOutboxRuntimeConfig,
			deps DurableOutboxRuntimeDeps,
		) (MessageDeliveryRuntime, error) {
			return OpenDurableOutboxRuntime(ctx, cfg, deps)
		},
	}
}

func OpenMessageDeliveryRuntime(
	ctx context.Context,
	cfg MessageDeliveryRuntimeConfig,
	deps MessageDeliveryRuntimeDeps,
) (MessageDeliveryRuntime, error) {
	return openMessageDeliveryRuntime(
		ctx,
		cfg,
		deps,
		productionMessageDeliveryRuntimeFactory(),
	)
}

func openMessageDeliveryRuntime(
	ctx context.Context,
	cfg MessageDeliveryRuntimeConfig,
	deps MessageDeliveryRuntimeDeps,
	factory messageDeliveryRuntimeFactory,
) (MessageDeliveryRuntime, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	switch cfg.Mode {
	case config.MessageSendModeDirect:
		runtime, err := NewDirectMessageDeliveryRuntime(deps.DirectSubmitter)
		if err != nil {
			return nil, fmt.Errorf(
				"open direct message delivery runtime: %w",
				err,
			)
		}
		return runtime, nil

	case config.MessageSendModeDurable:
		if factory.openDurable == nil {
			return nil, errors.New(
				"durable message delivery factory is required",
			)
		}
		runtime, err := factory.openDurable(
			ctx,
			cfg.Durable,
			deps.Durable,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"open durable message delivery runtime: %w",
				err,
			)
		}
		if runtime == nil {
			return nil, errors.New(
				"durable message delivery factory returned nil runtime",
			)
		}
		return runtime, nil

	default:
		return nil, fmt.Errorf(
			"unsupported message send mode %q",
			cfg.Mode,
		)
	}
}

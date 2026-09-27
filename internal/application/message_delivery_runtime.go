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

	// PendingMessages lists the outgoing messages the history does not have
	// yet. It is nil in direct delivery mode, where a message is in the
	// history as soon as it is sent.
	PendingMessages() PendingMessageSource
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

// openMessageDeliveryRuntime opens the runtime for the configured mode.
//
// There is no direct branch. A message that cannot be queued must not be
// sent by another route: direct send loses it when the process exits
// between Enter and TDLib's answer, and the user would not find out. When
// the durable runtime cannot be opened the caller starts the TUI with
// sending paused instead of falling back.
func openMessageDeliveryRuntime(
	ctx context.Context,
	cfg MessageDeliveryRuntimeConfig,
	deps MessageDeliveryRuntimeDeps,
	factory messageDeliveryRuntimeFactory,
) (MessageDeliveryRuntime, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if cfg.Mode.Validate() != nil {
		return nil, fmt.Errorf(
			"unsupported message send mode %q",
			cfg.Mode,
		)
	}

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
}

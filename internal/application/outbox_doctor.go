package application

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"telecli/internal/config"
	"telecli/internal/outbox"
)

// writeConfigWarnings prints the non-fatal notes a load produced.
//
// A retired send mode is reported on every doctor run, not once: the
// configuration file is deliberately left as the user wrote it, so this
// is the only place the migration is visible.
func writeConfigWarnings(out io.Writer, warnings []string) {
	for _, warning := range warnings {
		fmt.Fprintf(out, "warning: %s\n", warning)
	}
}

// OutboxProbe opens the durable outbox just far enough to learn whether it
// can be used, then closes it.
//
// It is a probe, not a runtime: no dispatcher is started and no Telegram
// session is involved, so doctor can report a broken queue without
// authorizing anything.
type OutboxProbe func(
	context.Context,
	outbox.Config,
	outbox.Deps,
) (io.Closer, error)

// outboxProbeTimeout bounds the probe.
//
// The platform key provider can block on a user prompt or on a locked
// system service, and a diagnostic command must never hang. On timeout the
// queue is reported unavailable, which is the safe reading: the messages
// cannot be queued until the cause is fixed.
const outboxProbeTimeout = 5 * time.Second

// productionOutboxProbe opens the outbox with the platform key provider.
func productionOutboxProbe(
	ctx context.Context,
	cfg outbox.Config,
	deps outbox.Deps,
) (io.Closer, error) {
	opened, err := outbox.Open(ctx, cfg, deps)
	if err != nil {
		return nil, err
	}
	return opened, nil
}

// reportOutboxStatus prints whether the message queue can be opened.
//
// Every remedy for every case is a path or a Keychain operation, so the
// data folder is always printed. The full cause is printed here and in
// the log, and never on the TUI screen.
func reportOutboxStatus(
	out io.Writer,
	ctx context.Context,
	cfg config.Config,
	probe OutboxProbe,
) {
	if probe == nil {
		probe = productionOutboxProbe
	}

	ctx, cancel := context.WithTimeout(ctx, outboxProbeTimeout)
	defer cancel()

	dataDir := strings.TrimSpace(cfg.MessageDelivery.DataDir)
	if dataDir == "" {
		dataDir = strings.TrimSpace(cfg.DataDir)
	}

	opened, err := probe(
		ctx,
		outbox.Config{
			DataDir:    dataDir,
			DatabaseID: cfg.MessageDelivery.DatabaseID,
			InstanceID: cfg.MessageDelivery.InstanceID,
			Dispatcher: outbox.DefaultDispatcherConfig(
				cfg.MessageDelivery.InstanceID,
			),
		},
		outbox.Deps{KeyProvider: outbox.NewPlatformKeyProvider()},
	)
	if err == nil && opened != nil {
		_ = opened.Close()
		fmt.Fprintf(out, "Message queue: OK (data_dir=%s)\n", dataDir)
		return
	}

	fmt.Fprint(out, DescribeSendingPaused(err, dataDir))
}

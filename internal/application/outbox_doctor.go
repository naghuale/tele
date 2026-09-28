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

// outboxSnapshotReader is what the probe hands back when it can count the
// queue's states.
//
// A snapshot is aggregate and payload-free: how many entries are in each
// state, and nothing else. It is asked for separately from the probe so
// that a probe which cannot count still answers the question doctor
// actually opens with — can this queue be used — and does not turn a
// missing count into a broken queue.
type outboxSnapshotReader interface {
	ReadOperationalSnapshot(
		context.Context,
	) (outbox.OperationalSnapshot, error)
}

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

// reportOutboxStatus prints whether the message queue can be opened, and
// where each message in it is.
//
// Every remedy for every case is a path or a Keychain operation, so the
// data folder is always printed. The full cause is printed here and in
// the log, and never on the TUI screen.
//
// The per-state counts are the chain a message walks, written as numbers:
// how many are waiting to be sent, how many TDLib has taken, how many it
// has confirmed, and how many ended. They say numbers and states and
// never the text of a message (§19), and they are what answers "is my
// message in there, and how far has it got" without opening the program.
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

	// A diagnostic command must not change anything. outbox.Open creates
	// the data directory, the SQLite file and a new Keychain item when
	// they are absent, and macOS may raise an access dialog. So the
	// database is checked first and Open is only reached for a queue that
	// already exists.
	if !outbox.DatabaseExists(dataDir) {
		fmt.Fprint(out,
			"Message queue: not created yet (it is created on first start)\n")
		fmt.Fprintf(out, "  Data folder: %s\n", dataDir)
		return
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
		writeOutboxChain(out, ctx, opened)
		_ = opened.Close()
		fmt.Fprintf(out, "Message queue: OK (data_dir=%s)\n", dataDir)
		return
	}

	fmt.Fprint(out, DescribeSendingPaused(err, dataDir))
}

// writeOutboxChain prints how many entries are in each state.
//
// The order is the order a message walks, so the line reads as the
// journey rather than as an alphabetical list. Nothing is printed when
// the queue cannot be counted: a line of zeroes would say the queue is
// empty, and a queue that could not be read is not an empty one.
func writeOutboxChain(
	out io.Writer,
	ctx context.Context,
	opened io.Closer,
) {
	reader, ok := opened.(outboxSnapshotReader)
	if !ok {
		return
	}

	snapshot, err := reader.ReadOperationalSnapshot(ctx)
	if err != nil {
		fmt.Fprintf(out, "  Message states: could not be read (%v)\n", err)
		return
	}

	fmt.Fprintf(
		out,
		"  Messages: %d queued, %d sending, %d on their way, %d sent, "+
			"%d retrying, %d failed, %d uncertain, %d canceled\n",
		snapshot.Queued,
		snapshot.Dispatching,
		snapshot.Accepted,
		snapshot.Sent,
		snapshot.FailedRetryable,
		snapshot.FailedPermanent,
		snapshot.Uncertain,
		snapshot.Canceled,
	)
}

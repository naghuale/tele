package application

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"telecli/internal/config"
	"telecli/internal/outbox"
)

// The texts of the command are fixed here and repeated in
// docs/help/sending-paused.md. The reason hints come from decision 3 of
// docs/TUI_SPEC.md, so a user who has seen one of them recognizes the
// other.

// outboxResetHeadline opens the summary. It says why the queue cannot be
// read, because a reset of a queue that is merely locked would throw
// away messages that are still there.
const outboxResetHeadline = "The key for your message queue is missing, " +
	"so its contents cannot be read."

// outboxResetUnreadable is shown when the queue itself cannot be read.
// It reports the uncertainty instead of guessing an empty queue, which
// would understate what the user is about to lose.
const outboxResetUnreadable = "telecli could not read what the old " +
	"queue holds, so it cannot tell you how many messages are in it."

// outboxResetEmpty is shown for a queue with no entries at all.
const outboxResetEmpty = "The old queue holds no messages."

// outboxResetUnrecoverable is shown when the queue holds messages that
// never reached Telegram.
const outboxResetUnrecoverable = "They cannot be recovered without " +
	"the old key."

// outboxResetNothingUnsent is shown when every entry in the queue
// already reached Telegram or was canceled.
const outboxResetNothingUnsent = "Nothing in it is waiting to be sent."

// outboxResetPlan is shown before the confirmation: it names what will
// happen to the old queue before anything happens to it.
const outboxResetPlan = "telecli will keep the old queue file as a " +
	"backup and start a new, empty queue with a new key."

// outboxResetInUse is the refusal for a queue another session holds.
const outboxResetInUse = "Another telecli session is using the message " +
	"queue. Close your other telecli windows and run this again."

// outboxResetNoTerminal is the refusal for a run that cannot ask.
const outboxResetNoTerminal = "telecli outbox reset needs a terminal " +
	"to confirm, or the --yes flag for an unattended run."

// outboxResetDeclinedText is printed when the user does not confirm.
const outboxResetDeclinedText = "Nothing was changed."

// outboxResetDataFolder is printed with every refusal, because the
// remedy for every reason below is in that folder.
const outboxResetDataFolder = "Data folder: %s\n"

// outboxResetStateLabels are the state names a user is shown, in the
// order they are printed. They are the delivery states of the interface,
// not the storage names: "sending" is what a user calls a message that is
// on its way out.
var outboxResetStateLabels = []struct {
	state outbox.State
	label string
}{
	{outbox.StateQueued, "queued"},
	{outbox.StateDispatching, "sending"},
	{outbox.StateFailedRetryable, "retrying"},
	{outbox.StateUncertain, "uncertain"},
	{outbox.StateFailedPermanent, "failed"},
	{outbox.StateAccepted, "sent"},
	{outbox.StateCanceled, "canceled"},
}

// outboxResetTimeLayout is how the oldest unsent entry is shown: local
// reading order, with the zone spelled out because the queue stores
// UTC.
const outboxResetTimeLayout = "2006-01-02 15:04"

// outboxResetLabelWidth aligns the counters.
const outboxResetLabelWidth = 9

// runOutboxReset implements `telecli outbox reset`.
//
// It is the only way a new message queue is ever started for an existing
// data folder, and it is deliberately slow about it: the key must be
// provably gone, the loss must be counted out loud first, and the user
// has to type a word.
func runOutboxReset(args []string, env Environment) int {
	fs := flag.NewFlagSet("outbox reset", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)

	var (
		configPath = fs.String(
			"config",
			"",
			"path to the configuration file",
		)
		assumeYes = fs.Bool(
			"yes",
			false,
			"confirm the reset without asking, for an "+
				"unattended run",
		)
	)

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}

		return 2
	}

	command := &outboxResetCommand{
		env:       env,
		ops:       env.OutboxResetOps,
		out:       env.Stdout,
		errOut:    env.Stderr,
		in:        env.Stdin,
		assumeYes: *assumeYes,
	}

	return command.run(context.Background(), *configPath)
}

// outboxResetQueueState is what became of the old queue file.
type outboxResetQueueState uint8

const (
	// outboxQueuePresent is a queue that is still the live file, so
	// nothing has moved yet and the user has still said yes to nothing.
	outboxQueuePresent outboxResetQueueState = iota

	// outboxQueueMoved is a queue that is under its backup name.
	outboxQueueMoved

	// outboxQueueGone is a queue that is neither the live file nor a
	// backup: something outside this command removed it. There is
	// nothing left to keep, and the reset is finished as far as the data
	// folder goes.
	outboxQueueGone
)

// outboxResetCommand is one run of the reset command.
type outboxResetCommand struct {
	env    Environment
	ops    OutboxResetOperations
	out    io.Writer
	errOut io.Writer
	in     io.Reader

	assumeYes bool

	configPath string
	cfg        config.Config
	dataDir    string
	queueID    string

	provider outbox.KeyProvider
	lock     outbox.RunLock
}

// run performs the command and returns the process exit code.
//
// The exit code is 0 only when the queue was reset or the user chose not
// to reset it, and 1 whenever the command refused to act. A caller can
// therefore tell "the queue is new again" from "the queue is as it was".
func (c *outboxResetCommand) run(ctx context.Context, configPath string) int {
	if code, ready := c.prepare(ctx, configPath); !ready {
		return code
	}
	defer c.release()

	journal, err := c.ops.ReadJournal(c.dataDir)
	if err != nil {
		return c.refuse(
			"The data folder holds a record of an unfinished queue "+
				"reset that telecli cannot read.",
			"",
			err,
		)
	}

	if journal != nil {
		return c.resume(ctx, journal)
	}

	return c.start(ctx)
}

// prepare resolves the configuration, the queue identity and the run
// lock.
//
// The lock is taken before anything is inspected or changed, and is held
// until the command returns: a session that started while the queue was
// being reset would otherwise keep using a queue that is being moved.
func (c *outboxResetCommand) prepare(
	ctx context.Context,
	configPath string,
) (int, bool) {
	c.ops = c.ops.withDefaults()
	c.configPath = configPath

	resolved, err := config.ResolvePath(configPath)
	if err != nil {
		return c.refuse(
			"telecli outbox reset needs a configuration file to "+
				"record the new queue in.",
			"",
			err,
		), false
	}
	// Resolving the settings file may have moved it to the place this
	// build keeps it, and a file that moved has to be said out loud
	// here too: the queue identity is written into that file a few
	// lines below, and a person who cannot find it has no way to check
	// what was recorded.
	writeConfigNotice(c.out, resolved)
	if !resolved.Found() {
		return c.refuse(
			"No configuration file was found, so there is no "+
				"message queue to reset.",
			"",
			nil,
		), false
	}

	cfg, err := config.LoadResolved(resolved)
	if err != nil {
		return c.refuse(
			"The configuration file could not be read.",
			"",
			err,
		), false
	}
	c.cfg = cfg
	c.configPath = resolved.Path

	c.dataDir = strings.TrimSpace(cfg.MessageDelivery.DataDir)
	if c.dataDir == "" {
		c.dataDir = strings.TrimSpace(cfg.DataDir)
	}
	if c.dataDir == "" {
		return c.refuse(
			"The configuration has no data folder, so there is "+
				"no message queue to reset.",
			"",
			nil,
		), false
	}

	c.queueID = strings.TrimSpace(cfg.MessageDelivery.DatabaseID)
	if c.queueID == "" {
		return c.refuse(
			"The configuration names no message queue, so there "+
				"is nothing to reset.",
			"",
			nil,
		), false
	}

	c.provider = c.env.NewOutboxKeyProvider()
	if c.provider == nil {
		c.provider = outbox.NewPlatformKeyProvider()
	}

	lock, err := c.ops.AcquireLock(ctx, c.dataDir)
	if err != nil {
		if errors.Is(err, outbox.ErrOutboxQueueInUse) {
			return c.refuse(outboxResetInUse, "", err), false
		}

		return c.refuse(
			"The message queue could not be locked, so telecli "+
				"will not change it.",
			"",
			err,
		), false
	}
	c.lock = lock

	return 0, true
}

// release gives the queue back.
func (c *outboxResetCommand) release() {
	if c.lock == nil {
		return
	}

	lock := c.lock
	c.lock = nil

	_ = lock.Release()
}

// start runs a reset that has not been started before.
func (c *outboxResetCommand) start(ctx context.Context) int {
	if !outbox.DatabaseExists(c.dataDir) {
		return c.refuse(
			"There is no message queue in this data folder, so "+
				"there is nothing to reset. telecli creates "+
				"one on the first start.",
			"",
			nil,
		)
	}

	if code, ok := c.checkKeyIsGone(ctx); !ok {
		return code
	}

	journal, err := c.newJournal()
	if err != nil {
		return c.refuse(
			"telecli could not prepare a new message queue.",
			"",
			err,
		)
	}

	if code, ok := c.askForReset(ctx, journal, false); !ok {
		return code
	}

	return c.perform(ctx, journal, outboxQueuePresent, false)
}

// resume finishes a reset that an earlier run started.
//
// The journal is the only thing that makes this safe, so it is consulted
// before anything else: a repeated run finishes the reset that was
// recorded, and never starts a second one.
func (c *outboxResetCommand) resume(
	ctx context.Context,
	journal *outbox.ResetJournal,
) int {
	switch {
	case c.queueID == journal.NewDatabaseID:
		// The configuration already points at the new queue. Only the
		// record of the reset is left, so the reset is over and saying
		// so must not start it again.
		if err := c.ops.ClearJournal(c.dataDir); err != nil {
			return c.refuse(
				"The queue was already reset, but the record of "+
					"it could not be removed.",
				"",
				err,
			)
		}

		// The old queue is under its backup name by now: that is what
		// this reset did before it was interrupted.
		c.writeOutcome(journal, outboxQueueMoved, true)

		return 0

	case c.queueID != journal.OldDatabaseID:
		return c.refuse(
			"The configuration no longer points at the queue "+
				"this reset started from, so telecli will not "+
				"change either one.",
			"",
			nil,
		)
	}

	queuePresent := outbox.DatabaseExists(c.dataDir)
	backupPresent := regularFileExists(
		filepath.Join(c.dataDir, journal.BackupName),
	)

	switch {
	case queuePresent && backupPresent:
		return c.refuse(
			"The data folder holds both the message queue and "+
				"the backup of an earlier reset, which telecli "+
				"cannot tell apart. Run telecli doctor.",
			"",
			nil,
		)

	case queuePresent:
		// Nothing has been moved yet, so the user still has to say yes.
		if code, ok := c.checkKeyIsGone(ctx); !ok {
			return code
		}

		if code, ok := c.askForReset(ctx, journal, true); !ok {
			return code
		}

		return c.perform(ctx, journal, outboxQueuePresent, false)

	case backupPresent:
		// The queue is already moved aside, so the user said yes to this
		// reset before. Only the rest of it is left, and the move itself
		// is not repeated.
		return c.perform(ctx, journal, outboxQueueMoved, true)

	default:
		// Neither the queue nor its backup is here. The file was removed
		// outside this command, so there is nothing left to keep: the
		// rest of the reset is finished anyway, because refusing would
		// leave the user with a queue that cannot be opened and a file
		// to delete by hand.
		return c.perform(ctx, journal, outboxQueueGone, true)
	}
}

// newJournal plans a reset: a new identity and unused backup names.
func (c *outboxResetCommand) newJournal() (*outbox.ResetJournal, error) {
	now := c.ops.Now().UTC()

	backupName, noteName, err := outbox.NewOrphanNames(
		c.dataDir,
		now,
	)
	if err != nil {
		return nil, err
	}

	newID, err := c.ops.NewDatabaseID()
	if err != nil {
		return nil, err
	}

	return &outbox.ResetJournal{
		OldDatabaseID: c.queueID,
		NewDatabaseID: newID,
		BackupName:    backupName,
		NoteName:      noteName,
		StartedAt:     now,
	}, nil
}

// checkKeyIsGone refuses everything but a provably missing key.
//
// This is the rule the whole command rests on. A key that cannot be
// read right now, or that exists but is unusable, means the queue may
// still be readable; resetting then would throw away messages that were
// not lost.
func (c *outboxResetCommand) checkKeyIsGone(ctx context.Context) (int, bool) {
	condition, cause := c.ops.InspectKey(ctx, c.provider, c.queueID)

	// The condition decides, not the error. A key store that refuses to
	// answer is a case with its own advice, and printing one generic
	// "could not be checked" for all of them is exactly what the
	// classification exists to avoid.
	if condition == outbox.KeyConditionAbsent {
		return 0, true
	}

	explanation, hint := describeKeyCondition(condition)

	return c.refuse(explanation, hint, cause), false
}

// describeKeyCondition explains why a queue with a key is not reset.
//
// The hints are the ones of decision 3 in docs/TUI_SPEC.md, so a user
// who has seen the same case in the interface is told the same thing.
func describeKeyCondition(
	condition outbox.KeyCondition,
) (explanation string, hint string) {
	switch condition {
	case outbox.KeyConditionPresent:
		return "The key of the message queue is in place, so the " +
				"queue can be opened and nothing was lost.",
			"Run telecli doctor to see why sending is paused."

	case outbox.KeyConditionMalformed:
		return "The key of the message queue exists but cannot be " +
				"used, so the messages in the queue are not lost yet.",
			"Run telecli doctor: a restored key store backup can " +
				"still fix this."

	case outbox.KeyConditionRefused:
		return "The key of the message queue could not be read, so " +
				"telecli cannot tell whether it is missing.",
			SendingPausedKeychainLocked.Hint()

	case outbox.KeyConditionUnsupported:
		return "This system has no secure key storage, so the " +
				"message queue cannot be opened at all.",
			SendingPausedNoKeyStorage.Hint()

	case outbox.KeyConditionFailed:
		return "The key of the message queue could not be checked, " +
				"so telecli did not change anything.",
			SendingPausedOther.Hint()

	default:
		return "The state of the message queue key is unknown, so " +
				"telecli did not change anything.",
			SendingPausedOther.Hint()
	}
}

// askForReset writes the summary and waits for the confirmation.
func (c *outboxResetCommand) askForReset(
	ctx context.Context,
	journal *outbox.ResetJournal,
	resuming bool,
) (int, bool) {
	summary, err := c.ops.InspectQueue(ctx, c.dataDir)
	if err != nil {
		return c.refuse(
			"The message queue could not be inspected, so telecli "+
				"did not change anything.",
			"",
			err,
		), false
	}

	c.writeSummary(summary, resuming)

	if c.assumeYes {
		return 0, true
	}

	if !c.ops.HasTerminal(c.in) {
		return c.refuse(outboxResetNoTerminal, "", nil), false
	}

	answer, err := askOutboxResetConfirmation(c.out, c.in)
	if err != nil {
		return c.refuse(
			"The confirmation could not be read, so telecli did "+
				"not change anything.",
			"",
			err,
		), false
	}

	if answer != outboxResetAnswerConfirmed {
		// An unfinished reset that has not moved anything yet is
		// dropped here: leaving the record behind would keep every
		// later start of telecli from creating a queue, and the state
		// on disk is exactly what it was before the first attempt.
		if err := c.ops.ClearJournal(c.dataDir); err != nil {
			return c.refuse(
				"Nothing was changed, but the record of the "+
					"unfinished reset could not be removed.",
				"",
				err,
			), false
		}

		fmt.Fprintln(c.out, outboxResetDeclinedText)

		return 0, false
	}

	return 0, true
}

// perform runs the steps of the reset.
//
// Order, and why each step is where it is:
//
//  1. the journal is written first, before anything is moved or
//     created. From this moment on, a start of telecli refuses to
//     create a queue, so nothing can quietly take the place of the reset
//     if the process dies;
//  2. the old queue is moved aside, with its write-ahead log, and the
//     note next to it is written. Until this point a refusal leaves the
//     previous state exactly as it was;
//  3. the key of the new identity is created. It is unused until the
//     configuration names that identity, and an existing key is treated
//     as this step being done, which is what a repeated run needs;
//  4. the configuration is written last, atomically. Until it names the
//     new identity, the missing old key keeps the queue unopenable, so
//     an interruption at any earlier point fails closed instead of
//     starting a queue nobody asked for.
//
// The reverse of this order is what the acceptance criteria forbid: a
// configuration that names an identity with no key, or no backup of the
// queue it replaced.
func (c *outboxResetCommand) perform(
	ctx context.Context,
	journal *outbox.ResetJournal,
	state outboxResetQueueState,
	resuming bool,
) int {
	// movedQueue tells failStep whether the old queue is already under
	// its backup name, so a report never points the user at a file that
	// does not exist yet.
	movedQueue := state != outboxQueuePresent

	steps := []struct {
		name string
		run  func() error
	}{
		{"record the reset", func() error {
			return c.ops.WriteJournal(c.dataDir, *journal)
		}},
		{"move the old queue aside", func() error {
			err := c.ops.OrphanQueue(c.dataDir, *journal)
			if err == nil {
				movedQueue = true
			}

			return err
		}},
		{"create the key of the new queue", func() error {
			return c.createKey(ctx, journal.NewDatabaseID)
		}},
		{"record the new queue in the configuration", func() error {
			return c.writeConfig(journal.NewDatabaseID)
		}},
		{"remove the record of the reset", func() error {
			return c.ops.ClearJournal(c.dataDir)
		}},
	}

	// A queue that is not the live file is not moved again: the step is
	// idempotent when it is under its backup name, and there is nothing
	// to move when the file is gone.
	if state == outboxQueueGone {
		steps = append(
			steps[:1],
			steps[2:]...,
		)
	}

	for _, step := range steps {
		if err := step.run(); err != nil {
			return c.failStep(step.name, err, journal, movedQueue)
		}
	}

	c.writeOutcome(journal, state, resuming)

	return 0
}

// createKey creates the key of the new queue identity.
//
// A key that already exists is not a failure here: it is what a repeated
// run finds after a crash between the key and the configuration write,
// and it means the step is done.
func (c *outboxResetCommand) createKey(
	ctx context.Context,
	databaseID string,
) error {
	err := c.ops.CreateKey(ctx, c.provider, databaseID)
	if err == nil || errors.Is(err, outbox.ErrOutboxKeyExists) {
		return nil
	}

	return err
}

// writeConfig records the new queue identity in the configuration.
func (c *outboxResetCommand) writeConfig(databaseID string) error {
	next := c.cfg
	next.MessageDelivery.DatabaseID = databaseID

	return c.ops.WriteConfig(c.configPath, next)
}

// failStep reports an interrupted reset and how to finish it.
//
// The queue is never left in a state the user has to repair by hand: the
// record of the reset is on disk, so running the same command again
// finishes the same reset.
func (c *outboxResetCommand) failStep(
	name string,
	err error,
	journal *outbox.ResetJournal,
	movedQueue bool,
) int {
	fmt.Fprintf(
		c.errOut,
		"outbox reset error: %s: %v\n",
		name,
		err,
	)
	fmt.Fprintf(
		c.out,
		"telecli could not %s, so the reset is not finished.\n",
		name,
	)

	if movedQueue {
		fmt.Fprintf(
			c.out,
			"The old queue is kept as: %s\n",
			filepath.Join(c.dataDir, journal.BackupName),
		)
	} else {
		fmt.Fprintln(
			c.out,
			"The old queue was not touched.",
		)
	}

	fmt.Fprint(
		c.out,
		"Run telecli outbox reset again to finish it. Nothing else "+
			"was changed.\n",
	)

	return 1
}

// writeSummary tells the user what the old queue holds.
//
// The counts come from the scheduling metadata, which is stored in the
// clear (ADR-0002), so the queue can be described without the key that
// is gone. Message text, chat ids and phone numbers are not part of it
// and are not shown: telecli cannot read them, and the command has no
// reason to try.
func (c *outboxResetCommand) writeSummary(
	summary outbox.QueueSummary,
	resuming bool,
) {
	fmt.Fprintln(c.out, outboxResetHeadline)
	fmt.Fprintln(c.out)

	switch {
	case !summary.Readable:
		fmt.Fprintln(c.out, outboxResetUnreadable)
		c.writeSummaryCause(summary.Cause)

	case summary.Total == 0:
		fmt.Fprintln(c.out, outboxResetEmpty)

	default:
		fmt.Fprintf(
			c.out,
			"The old queue holds %d %s:\n",
			summary.Total,
			pluralMessages(summary.Total),
		)

		for _, entry := range outboxResetStateLabels {
			fmt.Fprintf(
				c.out,
				"  %-*s %d\n",
				outboxResetLabelWidth,
				entry.label,
				summary.Counts[entry.state],
			)
		}

		c.writeUnknownStates(summary)
	}

	c.writeLossSummary(summary)

	if resuming {
		fmt.Fprintln(c.out)
		fmt.Fprint(
			c.out,
			"A reset of this queue was started before and did not "+
				"finish. Answering finishes that same reset.\n",
		)
	}

	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, outboxResetPlan)
}

// writeLossSummary names the messages that are about to be lost.
func (c *outboxResetCommand) writeLossSummary(summary outbox.QueueSummary) {
	if summary.Unsent == 0 {
		fmt.Fprintln(c.out)
		fmt.Fprintln(c.out, outboxResetNothingUnsent)

		return
	}

	fmt.Fprintln(c.out)

	if !summary.OldestUnsent.IsZero() {
		fmt.Fprintf(
			c.out,
			"The oldest of the %d messages that were not sent is from %s %s.\n",
			summary.Unsent,
			summary.OldestUnsent.Format(outboxResetTimeLayout),
			summary.OldestUnsent.Location(),
		)
	} else {
		fmt.Fprintf(
			c.out,
			"The queue holds %d %s that %s not sent.\n",
			summary.Unsent,
			pluralMessages(summary.Unsent),
			pluralHave(summary.Unsent),
		)
	}

	if summary.Unsent == 1 {
		fmt.Fprintln(
			c.out,
			"It cannot be recovered without the old key.",
		)

		return
	}

	fmt.Fprintln(c.out, outboxResetUnrecoverable)
}

// writeSummaryCause reports why the queue could not be counted.
//
// The cause is technical and goes to the error stream, next to the
// counts it replaces, never into the sentence a user reads first.
func (c *outboxResetCommand) writeSummaryCause(cause error) {
	if cause == nil {
		return
	}

	fmt.Fprintf(c.errOut, "outbox reset: inspect queue: %v\n", cause)
}

// writeUnknownStates counts entries in a state this build does not
// know, so a damaged queue cannot look emptier than it is.
func (c *outboxResetCommand) writeUnknownStates(
	summary outbox.QueueSummary,
) {
	known := 0
	for _, entry := range outboxResetStateLabels {
		known += summary.Counts[entry.state]
	}

	unknown := summary.Total - known
	if unknown <= 0 {
		return
	}

	fmt.Fprintf(
		c.out,
		"  %-*s %d\n",
		outboxResetLabelWidth,
		"unknown",
		unknown,
	)
}

// writeOutcome reports what the queue looks like now.
func (c *outboxResetCommand) writeOutcome(
	journal *outbox.ResetJournal,
	state outboxResetQueueState,
	resuming bool,
) {
	if resuming {
		fmt.Fprintln(c.out, "The queue reset is finished.")
	}

	if state == outboxQueueGone {
		fmt.Fprintln(
			c.out,
			"The old queue file was gone already, so there was "+
				"nothing to keep.",
		)
	} else {
		fmt.Fprintf(
			c.out,
			"The old queue was kept as: %s\n",
			filepath.Join(c.dataDir, journal.BackupName),
		)
		fmt.Fprintf(
			c.out,
			"Its database id is in the note next to it: %s\n",
			filepath.Join(c.dataDir, journal.NoteName),
		)
	}
	fmt.Fprint(
		c.out,
		"telecli now has a new, empty queue with its own key. Start "+
			"telecli as usual and sending works again.\n",
	)
	fmt.Fprint(
		c.out,
		"If the old key ever comes back, the note next to the backup "+
			"explains how to read the old messages again.\n",
	)
}

// refuse reports that the command did nothing and returns the exit code
// for it.
func (c *outboxResetCommand) refuse(
	explanation string,
	hint string,
	cause error,
) int {
	fmt.Fprintf(c.out, "%s\n", explanation)

	if c.dataDir != "" {
		fmt.Fprintf(c.out, outboxResetDataFolder, c.dataDir)
	}

	if strings.TrimSpace(hint) != "" {
		fmt.Fprintf(c.out, "%s\n", hint)
	}

	if cause != nil {
		fmt.Fprintf(c.errOut, "outbox reset error: %v\n", cause)
	}

	return 1
}

// pluralMessages renders the noun after a count.
func pluralMessages(count int) string {
	if count == 1 {
		return "message"
	}

	return "messages"
}

// pluralHave renders the verb after a count.
func pluralHave(count int) string {
	if count == 1 {
		return "was"
	}

	return "were"
}

// regularFileExists reports whether path is a regular file.
func regularFileExists(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular()
}

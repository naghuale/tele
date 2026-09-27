package application

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"telecli/internal/config"
	"telecli/internal/outbox"
)

// runOutbox implements `telecli outbox` and its subcommands.
func runOutbox(args []string, env Environment) int {
	if len(args) > 0 {
		switch args[0] {
		case "reset":
			return runOutboxReset(args[1:], env)
		default:
			fmt.Fprintf(
				env.Stderr,
				"unknown subcommand: outbox %s\n\n",
				args[0],
			)
			writeOutboxUsage(env.Stderr)

			return 2
		}
	}

	writeOutboxUsage(env.Stdout)

	return 0
}

func writeOutboxUsage(w io.Writer) {
	fmt.Fprint(w, `Usage:
  telecli outbox reset [--config path] [--yes]

reset  Start a new empty message queue when the key of the old one is
       missing. It keeps the old queue as a backup and never starts
       without an explicit confirmation.
`)
}

// outboxResetConfirmation is the word a user has to type to reset the
// message queue.
//
// A single keystroke is not enough for an action whose lost messages
// cannot be recovered, so neither "y" nor an empty line is accepted.
const outboxResetConfirmation = "reset"

// outboxResetAnswer is what the confirmation question produced.
type outboxResetAnswer uint8

const (
	// outboxResetAnswerRefused means the user did not confirm. Nothing
	// changed.
	outboxResetAnswerRefused outboxResetAnswer = iota

	// outboxResetAnswerConfirmed means the user typed the word.
	outboxResetAnswerConfirmed

	// outboxResetAnswerUnaskable means there was no terminal to ask in.
	outboxResetAnswerUnaskable
)

// OutboxResetOperations are the steps of `telecli outbox reset`.
//
// The command is a sequence of file and key changes, and every step of
// it has to be observable: a test must be able to make the rename, the
// key creation or the configuration write fail on its own and then
// check what a repeated run does about it. The real Keychain and the
// real configuration file are therefore reached only through this
// struct, and a nil field keeps the production step.
type OutboxResetOperations struct {
	InspectKey    func(context.Context, outbox.KeyProvider, string) (outbox.KeyCondition, error)
	InspectQueue  func(context.Context, string) (outbox.QueueSummary, error)
	AcquireLock   func(context.Context, string) (outbox.RunLock, error)
	ReadJournal   func(string) (*outbox.ResetJournal, error)
	WriteJournal  func(string, outbox.ResetJournal) error
	ClearJournal  func(string) error
	OrphanQueue   func(string, outbox.ResetJournal) error
	CreateKey     func(context.Context, outbox.KeyProvider, string) error
	WriteConfig   func(string, config.Config) error
	NewDatabaseID func() (string, error)
	Now           func() time.Time
	HasTerminal   func(io.Reader) bool
}

// withDefaults returns a copy with every unset field bound to its
// production step.
func (o OutboxResetOperations) withDefaults() OutboxResetOperations {
	if o.InspectKey == nil {
		o.InspectKey = outbox.InspectKey
	}
	if o.InspectQueue == nil {
		o.InspectQueue = outbox.InspectQueue
	}
	if o.AcquireLock == nil {
		o.AcquireLock = outbox.AcquireRunLock
	}
	if o.ReadJournal == nil {
		o.ReadJournal = outbox.ReadResetJournal
	}
	if o.WriteJournal == nil {
		o.WriteJournal = outbox.WriteResetJournal
	}
	if o.ClearJournal == nil {
		o.ClearJournal = outbox.ClearResetJournal
	}
	if o.OrphanQueue == nil {
		o.OrphanQueue = outbox.OrphanQueue
	}
	if o.CreateKey == nil {
		o.CreateKey = outbox.CreateQueueKey
	}
	if o.WriteConfig == nil {
		o.WriteConfig = config.WriteFile
	}
	if o.NewDatabaseID == nil {
		o.NewDatabaseID = outbox.NewDatabaseID
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.HasTerminal == nil {
		o.HasTerminal = stdinIsTerminal
	}

	return o
}

// stdinIsTerminal reports whether in is an interactive terminal.
//
// The confirmation asks the user to type a word, and only a person can
// read what the command is about to do with the queue. A script uses
// --yes, which says exactly the same thing on purpose.
func stdinIsTerminal(in io.Reader) bool {
	file, ok := in.(*os.File)
	if !ok {
		return false
	}

	info, err := file.Stat()
	if err != nil {
		return false
	}

	return info.Mode()&os.ModeCharDevice != 0
}

// askOutboxResetConfirmation prints the question and returns the
// answer.
//
// A line that is not the confirmation word, and an input that ends
// before an answer, are both a refusal: the queue keeps whatever it
// has, and the user can read the summary and run the command again.
func askOutboxResetConfirmation(
	out io.Writer,
	in io.Reader,
) (outboxResetAnswer, error) {
	if _, err := io.WriteString(
		out,
		`Type "`+outboxResetConfirmation+`" to continue: `,
	); err != nil {
		return outboxResetAnswerUnaskable, err
	}

	scanner := newLineScanner(in)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return outboxResetAnswerUnaskable, err
		}

		// The input ended without an answer. Nothing is lost, and the
		// question stays unanswered on purpose.
		return outboxResetAnswerRefused, nil
	}

	answer := strings.TrimSpace(scanner.Text())
	if !strings.EqualFold(answer, outboxResetConfirmation) {
		return outboxResetAnswerRefused, nil
	}

	return outboxResetAnswerConfirmed, nil
}

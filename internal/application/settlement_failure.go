package application

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"

	"telecli/internal/outbox"
	"telecli/internal/telegram"
)

// What a failed settlement step is called, so a log line says where.
//
// The owner read `startup settlement of earlier records failed
// error="send failure type=*fmt.wrapError"` three times and could not act
// on any of it, and that is a fault of this file's own making: every
// error was wrapped with `%w` on its way out, and outbox.SafeReason
// classifies an unrecognised error by its Go type — so the type in the log
// was the type of *this package's wrapper* rather than of the failure. A
// diagnostic that reports the shape of its own plumbing is worse than one
// that reports nothing, because it looks like an answer.
//
// A failure therefore carries the step it happened at and the kind of the
// cause, and never the cause's text: a reason can name a file, a chat and
// a message, and this is a log file on the user's disk.
const (
	stepListAwaiting     = "list accepted records"
	stepListSettled      = "list records an earlier settlement could not find"
	stepReadHistory      = "read the history of a chat"
	stepMarkSent         = "mark a record sent"
	stepMarkUncertain    = "mark a record uncertain"
	stepSettleContextEnd = "the settlement was cut short"
)

// settlementFailure is a step that failed, with the cause described by its
// kind.
type settlementFailure struct {
	step string
	kind string
	err  error
}

// Error names the step and the kind, and the step is the part a reader
// needs first: "read the history of a chat" says more than any type does.
func (f *settlementFailure) Error() string {
	return fmt.Sprintf("restart settlement: %s: %s", f.step, f.kind)
}

// Unwrap keeps errors.Is and errors.As working through the failure.
func (f *settlementFailure) Unwrap() error { return f.err }

// LogValue is the record the line is written from.
func (f *settlementFailure) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("step", f.step),
		slog.String("kind", f.kind),
	)
}

// settlementErrorf names the step a failure happened at.
func settlementErrorf(step string, err error) error {
	return &settlementFailure{step: step, kind: errorKind(err), err: err}
}

// settlementErrorAttrs is what the caller logs about a failure: the step
// and the kind, and nothing that could carry what the user wrote.
func settlementErrorAttrs(err error) []any {
	var failure *settlementFailure
	if errors.As(err, &failure) {
		return []any{
			slog.String("step", failure.step),
			slog.String("kind", failure.kind),
		}
	}
	return []any{slog.String("kind", errorKind(err))}
}

// errorKind names what an error IS without saying what it says.
//
// It walks to the innermost cause, because the outermost error of a chain
// is the wrapper somebody wrote and the innermost is the thing that
// happened. A sentinel is named; anything else is named by its type, and
// by the closest sentinel above it in the chain, which is what turns
// "context canceled" from a type into a fact.
func errorKind(err error) string {
	if err == nil {
		return "none"
	}

	// TDLib's answer is checked before anything else, and before the walk
	// to the cause, because TDLibError unwraps to a sentinel: walking
	// past it would report the sentinel and lose the code, which is the
	// one number of the three that is worth having.
	var tdlibErr *telegram.TDLibError
	if errors.As(err, &tdlibErr) && tdlibErr != nil {
		return fmt.Sprintf("TDLib code=%d", tdlibErr.Code)
	}

	for _, known := range []struct {
		sentinel error
		kind     string
	}{
		{context.Canceled, "canceled"},
		{context.DeadlineExceeded, "deadline exceeded"},
		{sql.ErrNoRows, "no rows"},
		{outbox.ErrNoAcceptedEntry, "no accepted entry"},
		{outbox.ErrVersionConflict, "the record moved on"},
		{outbox.ErrInvalidEntry, "malformed record"},
		{outbox.ErrInvalidTransition, "transition not allowed"},
	} {
		if errors.Is(err, known.sentinel) {
			return known.kind
		}
	}

	// Then the cause: the outermost error of a chain is the wrapper
	// somebody wrote and the innermost is the thing that happened.
	if cause := errors.Unwrap(err); cause != nil {
		return errorKind(cause)
	}

	return fmt.Sprintf("%T", err)
}

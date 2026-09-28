package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"telecli/internal/outbox"
	"telecli/internal/telegram"
)

// The chain this file completes, and where each link used to stop.
//
//	sendMessage            the dispatcher calls it and gets a message back
//	                       with a *temporary* identifier
//	updateMessageSendSucceeded
//	                       TDLib replaces that identifier with the final
//	                       one, and carries the temporary one as
//	                       old_message_id
//	the queue record       has to learn the final identifier, or it keeps
//	                       a number nothing else in the program can name
//	the interface          has to learn that the message is on its way out
//
// Every one of those four links exists; none of them was connected. The
// live store decoded the send result into the same window as everything
// else and nothing read it, so the queue stayed in the state TDLib's own
// answer put it in, and the interface had nothing newer to read than the
// state it had already drawn.

// telegramMessageEvents is the part of the live store this file reads.
//
// It is an interface rather than *telegram.LiveState so a test can feed
// the recorded updates of a real TDLib session without a TDLib, and so
// the reconciler states exactly what it depends on: the window of message
// events and the signal that says it moved.
type telegramMessageEvents interface {
	// MessageEventsSince returns the message events of a chat that
	// follow seq, the cursor to continue from, and whether the window
	// could answer at all.
	MessageEventsSince(
		chatID telegram.ChatID,
		seq uint64,
	) ([]telegram.MessageEvent, uint64, bool)

	// Changed is the coalesced signal that something was applied.
	Changed() <-chan struct{}
}

// sendResultReconciler moves accepted queue entries to sent or to a
// permanent failure, from what TDLib reports about them.
//
// It is the one consumer of the send results the session pump decodes.
// Without it the queue is a queue of messages whose identifier no other
// part of the program can name, and the interface can only ever say the
// message is on its way out.
type sendResultReconciler struct {
	store  outbox.SendResultStore
	events telegramMessageEvents

	// accountKey scopes every entry to the account this process is
	// authorized as. The same queue on another account must not be
	// resolved by this session's results.
	accountKey string

	clock  outbox.Clock
	logger *slog.Logger

	// interval is how long to wait before looking again when nothing
	// has changed. It is the backstop for a result that arrives while
	// the loop is not waiting, and the cadence the interface's own poll
	// runs on.
	interval time.Duration
}

// sendResultReconcileInterval is how often the reconciler looks when the
// live store says nothing moved.
//
// It is a backstop rather than the main path: the loop waits on the
// store's own change signal, so a confirmation is applied as soon as
// TDLib delivers it. The interval covers a signal that was coalesced
// away while the loop was applying the previous one.
const sendResultReconcileInterval = time.Second

// Run applies send results until ctx is done.
//
// The loop waits on the live store's change signal and on the interval,
// and reconciles after either. A reconcile failure is logged and the
// loop continues: a result that could not be written now is a result
// the next pass will try again, and a store that has gone away takes the
// runtime down with it rather than being retried here forever.
func (r *sendResultReconciler) Run(ctx context.Context) error {
	if r == nil || r.store == nil || r.events == nil {
		return errors.New(
			"send result reconciler: store and events are required",
		)
	}

	interval := r.interval
	if interval <= 0 {
		interval = sendResultReconcileInterval
	}

	for {
		if err := ctx.Err(); err != nil {
			return nil
		}

		if _, err := r.ReconcileOnce(ctx); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			r.log().Warn(
				"send result reconcile failed; retrying",
				slog.String("error", outbox.SafeReason(err)),
			)
		}

		if err := ctx.Err(); err != nil {
			return nil
		}

		select {
		case <-ctx.Done():
			return nil
		case <-r.events.Changed():
		case <-r.after(interval):
		}
	}
}

// ReconcileOnce applies every send result the window holds for the
// entries this queue is waiting on, and returns how many entries it
// moved.
//
// It is exported onto the type rather than kept private so a test can
// drive one pass with recorded updates and no goroutine.
func (r *sendResultReconciler) ReconcileOnce(
	ctx context.Context,
) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	awaiting, err := r.store.ListAwaitingSendResult(ctx, r.accountKey, 0)
	if err != nil {
		return 0, fmt.Errorf(
			"list entries awaiting a send result: %w", err,
		)
	}
	if len(awaiting) == 0 {
		return 0, nil
	}

	moved := 0
	for _, entry := range awaiting {
		if err := ctx.Err(); err != nil {
			return moved, err
		}

		applied, err := r.reconcileEntry(ctx, entry)
		if err != nil {
			return moved, fmt.Errorf(
				"reconcile send result for entry %q: %w", entry.ID, err,
			)
		}
		if applied {
			moved++
		}
	}
	return moved, nil
}

// reconcileEntry applies the result the window holds for one entry.
//
// The window is read from the beginning of what this chat produced, not
// from a cursor: the result of a send can land before the queue has
// finished recording the acceptance it belongs to, and a consumer that
// started at "now" would miss exactly that message. A chat produces a
// bounded number of events, so re-reading the window is cheap and the
// race disappears.
func (r *sendResultReconciler) reconcileEntry(
	ctx context.Context,
	entry outbox.AwaitingSendResult,
) (bool, error) {
	events, _, resync := r.events.MessageEventsSince(
		telegram.ChatID(entry.ChatID), 0,
	)
	if resync {
		// The window can no longer answer for the whole chat. The
		// entries this pass is holding are not in it, so nothing is
		// applied; the next pass reads what is there and the entry is
		// resolved when its result is still held.
		r.log().Warn(
			"send result window no longer covers the chat",
			slog.Int64("chat_id", entry.ChatID),
			slog.String("entry_id", string(entry.ID)),
		)
		return false, nil
	}

	for _, event := range events {
		result, ok := sendResultOf(event, entry)
		if !ok {
			continue
		}

		applied, err := r.apply(ctx, entry, result)
		if err != nil {
			return false, err
		}
		if applied {
			return true, nil
		}
	}
	return false, nil
}

// apply writes one send result to the store and says whether the entry
// moved.
//
// A result for an entry that is no longer accepted is not an error: the
// same update reaches every consumer of the window, and an entry another
// writer already resolved is not this one's to write again.
func (r *sendResultReconciler) apply(
	ctx context.Context,
	entry outbox.AwaitingSendResult,
	result outbox.SendResult,
) (bool, error) {
	updated, err := r.store.ApplySendResult(
		ctx, r.accountKey, result, r.now(),
	)
	if err != nil {
		if errors.Is(err, outbox.ErrNoAcceptedEntry) {
			return false, nil
		}
		return false, err
	}

	r.log().Info(
		"outbox send result applied",
		slog.String("entry_id", string(entry.ID)),
		slog.Int64("chat_id", entry.ChatID),
		slog.Int64("temporary_message_id", entry.TelegramMessageID),
		slog.Int64("message_id", updated.TelegramMessageID),
		slog.String("state", string(updated.State)),
	)
	return true, nil
}

// sendResultOf is the send result an event carries for one entry, if it
// carries one at all.
//
// The match is on old_message_id, which is the only thing that links the
// two: the temporary identifier in the update is the one sendMessage
// returned, and no other message of the chat can be confused for it
// because TDLib allocates temporary identifiers that no history page
// will ever contain.
func sendResultOf(
	event telegram.MessageEvent,
	entry outbox.AwaitingSendResult,
) (outbox.SendResult, bool) {
	temporary := int64(entry.TelegramMessageID)

	switch typed := event.(type) {
	case telegram.MessageReplaced:
		if int64(typed.OldID) != temporary {
			return outbox.SendResult{}, false
		}
		return outbox.SendResult{
			OldMessageID: temporary,
			MessageID:    int64(typed.Message.ID),
		}, true

	case telegram.MessageFailed:
		if int64(typed.OldID) != temporary {
			return outbox.SendResult{}, false
		}
		return outbox.SendResult{
			OldMessageID: temporary,
			Failed:       true,
			ErrorCode:    typed.Error.Code,
			// Reason, not Description: the description is TDLib's own
			// text and this string is persisted and logged.
			ErrorReason: typed.Error.Reason(),
		}, true

	default:
		return outbox.SendResult{}, false
	}
}

// after waits for the given duration on the reconciler's clock.
func (r *sendResultReconciler) after(d time.Duration) <-chan time.Time {
	if r.clock == nil {
		return time.After(d)
	}
	return r.clock.After(d)
}

// now is the clock the reconciler stamps a result with.
func (r *sendResultReconciler) now() time.Time {
	if r.clock == nil {
		return time.Now()
	}
	return r.clock.Now()
}

func (r *sendResultReconciler) log() *slog.Logger {
	if r.logger == nil {
		return slog.Default()
	}
	return r.logger
}

// Compile-time assertion: the reconciler reads the live store through
// the narrow contract it declares.
var _ telegramMessageEvents = (*telegram.LiveState)(nil)

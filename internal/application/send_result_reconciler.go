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

	// counters is what this reconciler saw, for `telecli doctor` to
	// print. It is never nil: a reconciler with no counters would leave
	// the owner of a stuck message with nothing to read.
	counters *sendResultCounters

	// firstSeen is when each temporary identifier in the window was first
	// read, so that a result is judged once and judged late.
	//
	// A result that matches nothing *right now* has not proved it will
	// match nothing: the record it belongs to may be a request TDLib is
	// still answering, and the window is read from the beginning, so the
	// result is in it before the queue knows the identifier. Counting it
	// at once would report a perfectly working queue as one full of
	// unmatched confirmations — several messages sent in a row are exactly
	// that, for the fraction of a second between the confirmation and the
	// acceptance.
	//
	// So a result is only counted as naming no record once it has been
	// sitting in the window, matching nothing, for longer than
	// unmatchedGrace. By then the record it belonged to is either accepted
	// or is never going to be, and the number means what it says.
	firstSeen map[int64]time.Time

	// sink is where the counters are written so a later process can
	// read them. It may be nil, which keeps the counters in memory.
	sink *sendResultCounterFile
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
	if r.counters == nil {
		r.counters = &sendResultCounters{}
	}

	interval := r.interval
	if interval <= 0 {
		interval = sendResultReconcileInterval
	}

	for {
		if err := ctx.Err(); err != nil {
			r.flushCounters()
			return nil
		}

		if _, err := r.ReconcileOnce(ctx); err != nil {
			if ctx.Err() != nil {
				r.flushCounters()
				return nil
			}
			r.log().Warn(
				"send result reconcile failed; retrying",
				slog.String("error", outbox.SafeReason(err)),
			)
		}

		if err := ctx.Err(); err != nil {
			r.flushCounters()
			return nil
		}

		select {
		case <-ctx.Done():
			r.flushCounters()
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
	if r.counters == nil {
		r.counters = &sendResultCounters{}
	}
	r.counters.passes.Add(1)

	awaiting, err := r.store.ListAwaitingSendResult(ctx, r.accountKey, 0)
	if err != nil {
		return 0, fmt.Errorf(
			"list entries awaiting a send result: %w", err,
		)
	}
	if len(awaiting) == 0 {
		// Nothing is waiting, so there is nothing to match a result
		// against. The results themselves are not counted here: a
		// result that arrived before the queue recorded the acceptance
		// it belongs to is read again on the pass that finds the entry,
		// because the window is read from its beginning.
		return 0, nil
	}

	// The entries of one chat are matched against one reading of that
	// chat's window. Reading it per entry would be quadratic in the
	// number of messages still going out, which is exactly when it
	// matters: a queue that has stopped resolving entries is the queue
	// with the most of them.
	byChat := make(map[int64][]outbox.AwaitingSendResult, 1)
	for _, entry := range awaiting {
		byChat[entry.ChatID] = append(byChat[entry.ChatID], entry)
	}

	moved := 0
	for chatID, entries := range byChat {
		if err := ctx.Err(); err != nil {
			return moved, err
		}

		applied, err := r.reconcileChat(ctx, telegram.ChatID(chatID), entries)
		if err != nil {
			return moved, fmt.Errorf(
				"reconcile send results for chat %d: %w", chatID, err,
			)
		}
		moved += applied
	}

	r.writeCounters()
	return moved, nil
}

// reconcileChat applies the results one chat's window holds.
//
// The window is read from the beginning of what the chat produced, not
// from a cursor: the result of a send can land before the queue has
// finished recording the acceptance it belongs to, and a consumer that
// started at "now" would miss exactly that message. A chat produces a
// bounded number of events, so re-reading the window is cheap and the
// race disappears.
func (r *sendResultReconciler) reconcileChat(
	ctx context.Context,
	chatID telegram.ChatID,
	entries []outbox.AwaitingSendResult,
) (int, error) {
	events, _, resync := r.events.MessageEventsSince(chatID, 0)
	if resync {
		// The window can no longer answer for the whole chat. The
		// entries this pass is holding are not in it, so nothing is
		// applied; the next pass reads what is there and an entry is
		// resolved when its result is still held.
		//
		// This is counted rather than only logged. It is the one way a
		// confirmation can be delivered and never acted on, and a user
		// whose messages are stuck needs to see that this happened
		// rather than a log line they have to know to look for.
		r.counters.windowGap.Add(1)
		r.log().Warn(
			"send result window no longer covers the chat",
			slog.Int64("chat_id", int64(chatID)),
			slog.Int("entries", len(entries)),
		)
		return 0, nil
	}

	results := sendResultsOf(events)
	r.counters.seen.Add(uint64(len(results)))
	if len(results) == 0 {
		return 0, nil
	}

	// Every result is matched against the entries, and every result that
	// matched nothing is counted. A confirmation for a message this
	// queue no longer holds is a fact about the program, and it is the
	// fact that tells a user their message did not go out.
	applied := make(map[int64]bool, len(entries))
	matched := 0
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return matched, err
		}
		for _, result := range results {
			if result.OldMessageID != entry.TelegramMessageID {
				continue
			}
			ok, err := r.apply(ctx, result)
			if err != nil {
				return matched, err
			}
			if ok {
				applied[entry.TelegramMessageID] = true
				matched++
			}
			break
		}
	}

	// Results nobody wanted are counted, not applied. The same update
	// reaches every consumer of the window, so a result for a message
	// another writer already resolved is expected and harmless.
	claimed := make(map[int64]bool, len(entries))
	for _, entry := range entries {
		claimed[entry.TelegramMessageID] = true
	}
	now := r.now()
	for _, result := range results {
		if claimed[result.OldMessageID] {
			continue
		}
		first := r.markSeen(result.OldMessageID, now)
		if now.Sub(first) < unmatchedGrace {
			// Too soon to say: the record this belongs to may be a
			// request TDLib is still answering.
			continue
		}
		r.forget(result.OldMessageID)
		r.counters.noEntry.Add(1)
		r.log().Warn(
			"send result named no entry this queue holds",
			slog.Int64("chat_id", int64(chatID)),
			slog.Int64("temporary_message_id", result.OldMessageID),
		)
	}

	r.counters.matched.Add(uint64(matched))
	return matched, nil
}

// apply writes one send result to the store and says whether the entry
// moved.
//
// A result for an entry that is no longer accepted is not an error: the
// same update reaches every consumer of the window, and an entry another
// writer already resolved is not this one's to write again. It is
// counted as unmatched, because "the confirmation arrived and the queue
// had already moved the record on" is a different fact from "the
// confirmation arrived and nothing wanted it".
func (r *sendResultReconciler) apply(
	ctx context.Context,
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
	r.markSeen(result.OldMessageID, r.now())

	r.log().Info(
		"outbox send result applied",
		slog.Int64("message_id", updated.TelegramMessageID),
		slog.String("state", string(updated.State)),
	)
	return true, nil
}

// unmatchedGrace is how long a confirmation may sit in the window
// matching no record before it is counted as one that never will.
//
// It is a delay, not a timeout: the result is already known to be
// unmatched, and the grace only keeps the reconciler from reporting a
// record the dispatcher has not finished writing yet. It is short enough
// that a user watching doctor sees the number settle, and long enough to
// cover a slow send and a slow disk.
const unmatchedGrace = 30 * time.Second

// firstSeenLimit bounds the map of identifiers this reconciler watches.
//
// A chat's window holds a few hundred events, so a map this size covers
// every result still in any window the reconciler can read, and it cannot
// grow without bound in a program that runs for weeks.
const firstSeenLimit = 4096

// markSeen records when a temporary identifier was first read, and
// returns that moment.
//
// The reconciler is the only writer of this map and each pass is
// sequential, so it needs no lock of its own.
func (r *sendResultReconciler) markSeen(
	id int64,
	now time.Time,
) time.Time {
	if r.firstSeen == nil {
		r.firstSeen = make(map[int64]time.Time, firstSeenLimit)
	}
	if len(r.firstSeen) >= firstSeenLimit {
		// The map is full. Rather than grow it, start again: the worst
		// case is that a very old confirmation is counted a second time,
		// which is a smaller lie than a number that never stops growing.
		r.firstSeen = make(map[int64]time.Time, firstSeenLimit)
	}
	if first, seen := r.firstSeen[id]; seen {
		return first
	}
	r.firstSeen[id] = now

	return now
}

// forget drops an identifier the reconciler has finished with.
func (r *sendResultReconciler) forget(id int64) {
	if r.firstSeen != nil {
		delete(r.firstSeen, id)
	}
}

// writeCounters records what this pass saw, if the writer wants it.
func (r *sendResultReconciler) writeCounters() {
	if r.sink == nil || r.counters == nil {
		return
	}
	if err := r.sink.write(r.counters, false); err != nil {
		r.log().Warn(
			"send result counters not written",
			slog.String("error", outbox.SafeReason(err)),
		)
	}
}

// flushCounters writes the counters whatever the throttle says, for the
// end of a run.
func (r *sendResultReconciler) flushCounters() {
	if r.sink == nil || r.counters == nil {
		return
	}
	if err := r.sink.write(r.counters, true); err != nil {
		r.log().Warn(
			"send result counters not written",
			slog.String("error", outbox.SafeReason(err)),
		)
	}
}

// sendResultsOf is every send result in a chat's window.
//
// The match is on old_message_id, which is the only thing that links the
// two identifiers: the temporary one sendMessage returned and the final
// one Telegram assigned. No other message of the chat can be confused
// for it, because TDLib allocates temporary identifiers that no history
// page will ever contain.
func sendResultsOf(
	events []telegram.MessageEvent,
) []outbox.SendResult {
	var results []outbox.SendResult
	for _, event := range events {
		switch typed := event.(type) {
		case telegram.MessageReplaced:
			results = append(results, outbox.SendResult{
				OldMessageID: int64(typed.OldID),
				MessageID:    int64(typed.Message.ID),
			})

		case telegram.MessageFailed:
			results = append(results, outbox.SendResult{
				OldMessageID: int64(typed.OldID),
				Failed:       true,
				ErrorCode:    typed.Error.Code,
				// Reason, not Description: the description is TDLib's
				// own text and this string is persisted and logged.
				ErrorReason: typed.Error.Reason(),
			})
		}
	}
	return results
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

// Package outbox defines the durable outgoing message model.
//
// The domain model is backend-agnostic: it does not reference files,
// SQL, or any specific store. It is used by the in-memory Store in
// PR-08B and by the dispatcher in PR-08D.
//
// At-rest privacy policy for a future durable backend is documented in
// docs/adr/ADR-0002-durable-outbox-storage.md. Until that ADR moves to
// Accepted, only the in-memory Store is shipped.
package outbox

import (
	"fmt"
	"strings"
	"time"
)

// ID is a stable identifier for an outbox entry.
type ID string

// State is the lifecycle state of an outbox entry.
type State string

const (
	// StateQueued: entry is persisted, TDLib has not been invoked.
	StateQueued State = "queued"

	// StateDispatching: a dispatcher claimed the entry and is about
	// to call SendMessage. An interrupted dispatch becomes uncertain
	// on recovery.
	StateDispatching State = "dispatching"

	// StateAccepted: TDLib returned a message object with a non-zero
	// ID. The message is still being sent: the ID is the temporary
	// one sendMessage returns, and Telegram has not confirmed it yet.
	StateAccepted State = "accepted"

	// StateSent: Telegram confirmed the send. The entry now holds the
	// final message ID. Terminal.
	StateSent State = "sent"

	// StateFailedRetryable: the send did not reach TDLib, or TDLib
	// reported a transient error. May be retried after NextAttempt.
	StateFailedRetryable State = "failed_retryable"

	// StateFailedPermanent: TDLib reported a terminal error. Not
	// retried automatically.
	StateFailedPermanent State = "failed_permanent"

	// StateUncertain: the process exited after dispatch started but
	// before the result was recorded. Never retried automatically.
	StateUncertain State = "uncertain"

	// StateCanceled: the user canceled the entry before acceptance.
	// Terminal.
	StateCanceled State = "canceled"
)

// Valid reports whether s is a known State.
func (s State) Valid() bool {
	switch s {
	case StateQueued,
		StateDispatching,
		StateAccepted,
		StateSent,
		StateFailedRetryable,
		StateFailedPermanent,
		StateUncertain,
		StateCanceled:
		return true
	}
	return false
}

// String returns the canonical string representation.
func (s State) String() string { return string(s) }

// Terminal reports whether the state is terminal for automatic
// processing by the dispatcher.
//
// The question is the dispatcher's, not the message's: an uncertain
// entry requires an explicit user decision and must never be selected
// automatically, and an accepted entry is already in Telegram's hands —
// only a send result moves it on from there, and that is the
// reconciler's work, never a redispatch.
func (s State) Terminal() bool {
	switch s {
	case StateAccepted,
		StateSent,
		StateFailedPermanent,
		StateUncertain,
		StateCanceled:
		return true
	}
	return false
}

// Entry is one durable outgoing message.
//
// Entry never stores credentials, session keys, or full TDLib JSON
// responses. Only what is required to dispatch and reconcile the send.
type Entry struct {
	ID ID

	// AccountKey scopes the entry to one authenticated account. It is
	// an application-chosen opaque string, not a credential.
	AccountKey string

	ChatID int64
	Text   string

	State State

	AttemptCount int

	// NextAttempt is set only in StateFailedRetryable. It is cleared
	// on every transition out of failed_retryable, including a direct
	// claim of a retryable entry.
	NextAttempt time.Time

	// TelegramMessageID is populated in StateAccepted, where it is the
	// temporary identifier sendMessage returned, and in StateSent, where
	// it is the final one Telegram assigned. AcceptedAt is the moment
	// TDLib took the message; SentAt is the moment Telegram confirmed it.
	TelegramMessageID int64
	AcceptedAt        time.Time
	SentAt            time.Time

	// LastErrorCode and LastErrorMessage capture the most recent
	// failure. They are retained across states as history and do not
	// affect scheduling. LastErrorMessage must not contain message
	// text.
	LastErrorCode    int
	LastErrorMessage string

	CreatedAt time.Time
	UpdatedAt time.Time

	// LeaseOwner and LeaseUntil are set while StateDispatching.
	// Recovery converts an expired lease to StateUncertain.
	LeaseOwner string
	LeaseUntil time.Time

	// Version increases on every successful mutation. Store methods
	// use it for optimistic concurrency.
	Version uint64
}

// Validate returns an error if the entry violates a domain invariant.
//
// It is called on Enqueue and on every state transition. It rejects
// partially formed states such as:
//
//   - dispatching without a lease owner or deadline;
//   - accepted without a message ID or accepted time;
//   - sent without a sent time;
//   - failed_retryable without NextAttempt;
//   - NextAttempt outside failed_retryable;
//   - lease fields outside dispatching;
//   - message id or accepted time outside accepted and sent;
//   - sent time outside sent.
func (e Entry) Validate() error {
	if e.ID == "" {
		return fmt.Errorf("%w: empty id", ErrInvalidEntry)
	}
	if strings.TrimSpace(e.AccountKey) == "" {
		return fmt.Errorf("%w: empty account key", ErrInvalidEntry)
	}
	if e.ChatID == 0 {
		return fmt.Errorf("%w: zero chat id", ErrInvalidEntry)
	}
	if strings.TrimSpace(e.Text) == "" {
		return fmt.Errorf("%w: blank text", ErrInvalidEntry)
	}
	if !e.State.Valid() {
		return fmt.Errorf("%w: unknown state %q", ErrInvalidEntry, e.State)
	}
	if e.AttemptCount < 0 {
		return fmt.Errorf("%w: negative attempt count", ErrInvalidEntry)
	}
	if e.CreatedAt.IsZero() {
		return fmt.Errorf("%w: zero created time", ErrInvalidEntry)
	}
	if e.UpdatedAt.IsZero() {
		return fmt.Errorf("%w: zero updated time", ErrInvalidEntry)
	}

	switch e.State {
	case StateFailedRetryable:
		if e.NextAttempt.IsZero() {
			return fmt.Errorf("%w: retryable without next attempt",
				ErrInvalidEntry)
		}
	default:
		if !e.NextAttempt.IsZero() {
			return fmt.Errorf("%w: next attempt outside retryable state",
				ErrInvalidEntry)
		}
	}

	switch e.State {
	case StateDispatching:
		if strings.TrimSpace(e.LeaseOwner) == "" {
			return fmt.Errorf("%w: dispatching without lease owner",
				ErrInvalidEntry)
		}
		if e.LeaseUntil.IsZero() {
			return fmt.Errorf("%w: dispatching without lease deadline",
				ErrInvalidEntry)
		}
	default:
		if e.LeaseOwner != "" || !e.LeaseUntil.IsZero() {
			return fmt.Errorf("%w: lease outside dispatching state",
				ErrInvalidEntry)
		}
	}

	// accepted and sent are the states in which Telegram definitely holds
	// the message. accepted carries the temporary identifier sendMessage
	// returned and sent the final one, so a record in either state has a
	// message id and the moment TDLib took it; only sent says when
	// Telegram confirmed it.
	switch e.State.MessageIDPolicy() {
	case messageIDRequired:
		if e.TelegramMessageID == 0 {
			return fmt.Errorf("%w: %s without message id",
				ErrInvalidEntry, e.State)
		}
		if e.AcceptedAt.IsZero() {
			return fmt.Errorf("%w: %s without accepted time",
				ErrInvalidEntry, e.State)
		}

	case messageIDOptional:
		// Both of the pair or neither: half of it is a record that claims
		// Telegram named a message at a moment, or took one at a moment,
		// and not the other.
		if (e.TelegramMessageID == 0) != e.AcceptedAt.IsZero() {
			return fmt.Errorf(
				"%w: uncertain with only one of message id and accepted time",
				ErrInvalidEntry,
			)
		}

	default:
		if e.TelegramMessageID != 0 {
			return fmt.Errorf(
				"%w: message id outside accepted, sent and uncertain",
				ErrInvalidEntry,
			)
		}
		if !e.AcceptedAt.IsZero() {
			return fmt.Errorf(
				"%w: accepted time outside accepted, sent and uncertain",
				ErrInvalidEntry,
			)
		}
	}

	if e.State == StateSent {
		if e.SentAt.IsZero() {
			return fmt.Errorf("%w: sent without sent time", ErrInvalidEntry)
		}
	} else if !e.SentAt.IsZero() {
		return fmt.Errorf("%w: sent time outside sent state", ErrInvalidEntry)
	}

	return nil
}

// MessageIDPolicy is what a state says about the identifier TDLib gave
// the message.
type MessageIDPolicy int

const (
	// messageIDForbidden: a record in this state may not hold one. The
	// message is either not TDLib's yet or no longer its concern.
	messageIDForbidden MessageIDPolicy = iota

	// messageIDRequired: it must. These are the states in which Telegram
	// holds the message and the record is the proof.
	messageIDRequired

	// messageIDOptional: it may, and if it does then the accepted time
	// must be with it.
	//
	// This is uncertain, and it is uncertain for a reason worth writing
	// down. An uncertain record has two origins that do not look alike. A
	// lost lease leaves one that was dispatching: TDLib may or may not
	// have taken the message, and there is no identifier because there was
	// no answer. A record the restart settlement could not find was
	// accepted: TDLib did take it, and the identifier is the evidence of
	// that — which is what lets a later run tell the two apart and look
	// only at the second.
	//
	// So the identifier is kept. It is a temporary one that names nothing
	// outside this queue and is in no history page, and the settlement
	// finds the message by its text and its time rather than by it.
	messageIDOptional
)

// MessageIDPolicy reports what this state says about the identifier TDLib
// gave the message.
//
// It is the single rule about it. The writer that fills a record in and
// the reader that reports one both ask here, because when they each had
// their own copy they stopped agreeing — the writer learned to keep the
// identifier on an uncertain record and the reader kept refusing it, and a
// queue holding one of those records could not be read at all.
func (s State) MessageIDPolicy() MessageIDPolicy {
	switch s {
	case StateAccepted, StateSent:
		return messageIDRequired
	case StateUncertain:
		return messageIDOptional
	default:
		return messageIDForbidden
	}
}

// CanTransition reports whether the state machine allows a transition
// from e.State to next.
//
// Allowed transitions:
//
//	queued           -> dispatching, canceled
//	dispatching      -> accepted, failed_retryable,
//	                    failed_permanent, uncertain
//	accepted         -> sent, failed_permanent
//	failed_retryable -> dispatching, queued, canceled
//	uncertain        -> canceled
//
// sent, failed_permanent, canceled are terminal.
//
// failed_retryable may transition directly to dispatching so the
// dispatcher can claim a retry without an intermediate queued step.
// The two-step path failed_retryable -> queued -> dispatching would
// introduce an extra concurrency window without adding safety.
//
// The two transitions a send result makes out of accepted are sent and
// failed_permanent. TDLib has already taken the message, so the entry is
// never sent again: updateMessageSendSucceeded replaces the temporary
// identifier with the final one, and updateMessageSendFailed ends the
// attempt without a retry, because Telegram itself will not try again.
//
// accepted -> uncertain is the third, and no send result makes it. It is
// what a process that was not running when the result was delivered
// resolves an entry with: the confirmation is gone with the process that
// received it, and rather than leave the record looking like a message
// still on its way out, the next run either finds the message in the
// chat and marks it sent or admits it does not know.
//
// uncertain -> sent closes that loop. A settlement that cannot find a
// message has said "I do not know", and saying it is not the same as being
// finished: the lookup can be wrong, and it was. A record this queue put
// into uncertain because it could not find the message is looked at again
// on the next run, and becomes sent when the message turns up. A record
// that is uncertain because a lease was lost is not — there the honest
// answer is the user's, and only a human looking in Telegram can give it.
func (e Entry) CanTransition(next State) bool {
	if !next.Valid() {
		return false
	}
	switch e.State {
	case StateQueued:
		return next == StateDispatching || next == StateCanceled
	case StateDispatching:
		return next == StateAccepted ||
			next == StateFailedRetryable ||
			next == StateFailedPermanent ||
			next == StateUncertain
	case StateAccepted:
		// sent and failed_permanent are what a send result makes, and
		// uncertain is what a restart makes: the confirmation for this
		// entry was delivered to a process that is gone, and TDLib does
		// not send it again, so nothing will ever move this record on by
		// itself. Leaving it accepted would draw it as "on its way out"
		// for as long as the program runs, which is a claim about
		// Telegram that the queue cannot support.
		return next == StateSent ||
			next == StateFailedPermanent ||
			next == StateUncertain
	case StateFailedRetryable:
		return next == StateDispatching ||
			next == StateQueued ||
			next == StateCanceled
	case StateUncertain:
		// canceled is the user saying they have looked. sent is a later
		// run finding the message in the chat: a settlement is allowed to
		// be wrong once, and the record it was wrong about is the one it
		// is obliged to look at again. There is no live update coming for
		// this record — the process that would have delivered it is gone —
		// so the chat is the only thing left that can say.
		return next == StateSent || next == StateCanceled
	case StateSent, StateFailedPermanent, StateCanceled:
		return false
	}
	return false
}

// transition applies next to a copy of e and increments Version.
//
// It is an internal helper for simple transitions that do not carry
// state-specific payload (for example canceled). Complex transitions
// (Claim, Accept, MarkRetryable, MarkPermanentFailure, MarkUncertain)
// are exposed as dedicated methods so callers cannot construct
// partially formed states.
//
// transition clears the lease when leaving dispatching and clears
// NextAttempt when leaving failed_retryable.
func (e Entry) transition(next State) (Entry, error) {
	if !e.CanTransition(next) {
		return Entry{}, fmt.Errorf(
			"%w: %s -> %s",
			ErrInvalidTransition, e.State, next,
		)
	}

	out := e
	out.State = next
	out.Version++

	if out.State != StateDispatching {
		out.LeaseOwner = ""
		out.LeaseUntil = time.Time{}
	}
	if out.State != StateFailedRetryable {
		out.NextAttempt = time.Time{}
	}

	if err := out.Validate(); err != nil {
		return Entry{}, err
	}
	return out, nil
}

// Claim transitions a queued or failed_retryable entry into
// dispatching.
//
// It atomically sets the lease owner, the lease deadline, increments
// AttemptCount, and clears NextAttempt.
//
// leaseUntil must be strictly after now.
func (e Entry) Claim(
	owner string,
	leaseUntil time.Time,
	now time.Time,
) (Entry, error) {
	if !e.CanTransition(StateDispatching) {
		return Entry{}, fmt.Errorf(
			"%w: %s -> %s",
			ErrInvalidTransition, e.State, StateDispatching,
		)
	}
	if strings.TrimSpace(owner) == "" {
		return Entry{}, fmt.Errorf(
			"%w: empty lease owner", ErrInvalidEntry,
		)
	}
	if !leaseUntil.After(now) {
		return Entry{}, fmt.Errorf(
			"%w: lease must expire after claim time", ErrInvalidEntry,
		)
	}

	out := e
	out.State = StateDispatching
	out.AttemptCount++
	out.NextAttempt = time.Time{}
	out.LeaseOwner = owner
	out.LeaseUntil = leaseUntil
	out.UpdatedAt = now
	out.Version++

	if err := out.Validate(); err != nil {
		return Entry{}, err
	}
	return out, nil
}

// Accept transitions a dispatching entry into accepted.
//
// messageID is the identifier sendMessage returned, which is temporary:
// TDLib replaces it with the final one when Telegram confirms the send.
// AcceptedAt and UpdatedAt are both set to now.
func (e Entry) Accept(
	messageID int64,
	now time.Time,
) (Entry, error) {
	if !e.CanTransition(StateAccepted) {
		return Entry{}, fmt.Errorf(
			"%w: %s -> %s",
			ErrInvalidTransition, e.State, StateAccepted,
		)
	}
	if messageID == 0 {
		return Entry{}, fmt.Errorf(
			"%w: zero Telegram message id", ErrInvalidEntry,
		)
	}

	out := e
	out.State = StateAccepted
	out.TelegramMessageID = messageID
	out.AcceptedAt = now
	out.UpdatedAt = now
	out.NextAttempt = time.Time{}
	out.LeaseOwner = ""
	out.LeaseUntil = time.Time{}
	out.Version++

	if err := out.Validate(); err != nil {
		return Entry{}, err
	}
	return out, nil
}

// Sent transitions an accepted entry into sent.
//
// It is what updateMessageSendSucceeded makes, and it replaces the
// temporary identifier with the final one Telegram assigned. AcceptedAt
// is kept: it is when TDLib took the message, which is a different
// moment from the confirmation. SentAt and UpdatedAt are set to now.
func (e Entry) Sent(
	messageID int64,
	now time.Time,
) (Entry, error) {
	if !e.CanTransition(StateSent) {
		return Entry{}, fmt.Errorf(
			"%w: %s -> %s",
			ErrInvalidTransition, e.State, StateSent,
		)
	}
	if messageID == 0 {
		return Entry{}, fmt.Errorf(
			"%w: zero Telegram message id", ErrInvalidEntry,
		)
	}

	out := e
	out.State = StateSent
	out.TelegramMessageID = messageID
	out.SentAt = now
	out.UpdatedAt = now
	out.Version++

	if err := out.Validate(); err != nil {
		return Entry{}, err
	}
	return out, nil
}

// MarkSendFailed transitions an accepted entry into failed_permanent.
//
// It is what updateMessageSendFailed makes, and it is permanent because
// Telegram is the one that gave up: the message is not going out, and a
// second sendMessage would be a second message rather than a retry. code
// is Telegram's own error code, and message must be free of message
// text.
func (e Entry) MarkSendFailed(
	code int,
	message string,
	now time.Time,
) (Entry, error) {
	if !e.CanTransition(StateFailedPermanent) {
		return Entry{}, fmt.Errorf(
			"%w: %s -> %s",
			ErrInvalidTransition, e.State, StateFailedPermanent,
		)
	}

	out := e
	out.State = StateFailedPermanent
	out.LastErrorCode = code
	out.LastErrorMessage = message
	out.UpdatedAt = now
	// The message is not in Telegram, so it is not in Telegram's message
	// list either, and the identifier of a message that was never sent
	// must not be left behind as if it were.
	out.TelegramMessageID = 0
	out.AcceptedAt = time.Time{}
	out.SentAt = time.Time{}
	out.NextAttempt = time.Time{}
	out.LeaseOwner = ""
	out.LeaseUntil = time.Time{}
	out.Version++

	if err := out.Validate(); err != nil {
		return Entry{}, err
	}
	return out, nil
}

// MarkRetryable transitions a dispatching entry into failed_retryable.
//
// nextAttempt must not be before now. Lease fields are cleared.
func (e Entry) MarkRetryable(
	nextAttempt time.Time,
	code int,
	message string,
	now time.Time,
) (Entry, error) {
	if !e.CanTransition(StateFailedRetryable) {
		return Entry{}, fmt.Errorf(
			"%w: %s -> %s",
			ErrInvalidTransition, e.State, StateFailedRetryable,
		)
	}
	if nextAttempt.Before(now) {
		return Entry{}, fmt.Errorf(
			"%w: next attempt before update time", ErrInvalidEntry,
		)
	}

	out := e
	out.State = StateFailedRetryable
	out.NextAttempt = nextAttempt
	out.LastErrorCode = code
	out.LastErrorMessage = message
	out.UpdatedAt = now
	out.LeaseOwner = ""
	out.LeaseUntil = time.Time{}
	out.Version++

	if err := out.Validate(); err != nil {
		return Entry{}, err
	}
	return out, nil
}

// MarkPermanentFailure transitions a dispatching entry into
// failed_permanent.
//
// Lease and NextAttempt are cleared.
func (e Entry) MarkPermanentFailure(
	code int,
	message string,
	now time.Time,
) (Entry, error) {
	if !e.CanTransition(StateFailedPermanent) {
		return Entry{}, fmt.Errorf(
			"%w: %s -> %s",
			ErrInvalidTransition, e.State, StateFailedPermanent,
		)
	}

	out := e
	out.State = StateFailedPermanent
	out.NextAttempt = time.Time{}
	out.LastErrorCode = code
	out.LastErrorMessage = message
	out.UpdatedAt = now
	out.LeaseOwner = ""
	out.LeaseUntil = time.Time{}
	out.Version++

	if err := out.Validate(); err != nil {
		return Entry{}, err
	}
	return out, nil
}

// MarkUncertain transitions a dispatching or accepted entry into
// uncertain.
//
// A dispatching entry becomes uncertain when its lease was lost: the
// send may or may not have reached TDLib. An accepted entry becomes
// uncertain when a later run could not find out what became of a message
// TDLib had already taken. Both are the same claim: the queue cannot
// say, and only the user can resolve it.
//
// reason must be non-blank and must not contain the message text. The
// entry is not retried automatically.
func (e Entry) MarkUncertain(
	reason string,
	now time.Time,
) (Entry, error) {
	if !e.CanTransition(StateUncertain) {
		return Entry{}, fmt.Errorf(
			"%w: %s -> %s",
			ErrInvalidTransition, e.State, StateUncertain,
		)
	}
	if strings.TrimSpace(reason) == "" {
		return Entry{}, fmt.Errorf(
			"%w: empty uncertain reason", ErrInvalidEntry,
		)
	}

	out := e
	out.State = StateUncertain
	out.LastErrorMessage = reason
	out.UpdatedAt = now
	out.NextAttempt = time.Time{}
	out.LeaseOwner = ""
	out.LeaseUntil = time.Time{}
	out.Version++

	if err := out.Validate(); err != nil {
		return Entry{}, err
	}
	return out, nil
}

// Cancel transitions a queued, failed_retryable, or uncertain entry
// into canceled.
//
// Canceling an uncertain entry is an explicit user resolution. It
// does not prove that Telegram did not accept the original send.
//
// Cancel is rejected for dispatching, accepted, sent,
// failed_permanent, and already canceled entries.
func (e Entry) Cancel(now time.Time) (Entry, error) {
	if e.State == StateAccepted || e.State == StateSent {
		return Entry{}, ErrCancelAfterAccepted
	}
	out, err := e.transition(StateCanceled)
	if err != nil {
		return Entry{}, err
	}
	out.UpdatedAt = now
	if err := out.Validate(); err != nil {
		return Entry{}, err
	}
	return out, nil
}

// IsReadyAt reports whether the entry is eligible for dispatch at t.
//
// queued entries are always eligible. failed_retryable entries become
// eligible once NextAttempt has passed.
//
// Terminal states, including uncertain, are never ready.
func (e Entry) IsReadyAt(t time.Time) bool {
	if e.State.Terminal() {
		return false
	}
	switch e.State {
	case StateQueued:
		return true
	case StateFailedRetryable:
		return !e.NextAttempt.After(t)
	}
	return false
}

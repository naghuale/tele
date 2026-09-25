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
	// ID. Terminal.
	StateAccepted State = "accepted"

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
// processing.
//
// An uncertain entry requires an explicit user decision and must never
// be selected automatically by the dispatcher.
func (s State) Terminal() bool {
	switch s {
	case StateAccepted,
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

	// TelegramMessageID is populated only in StateAccepted.
	TelegramMessageID int64

	// LastErrorCode and LastErrorMessage capture the most recent
	// failure. They are retained across states as history and do not
	// affect scheduling. LastErrorMessage must not contain message
	// text.
	LastErrorCode    int
	LastErrorMessage string

	CreatedAt  time.Time
	UpdatedAt  time.Time
	AcceptedAt time.Time

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
//   - failed_retryable without NextAttempt;
//   - NextAttempt outside failed_retryable;
//   - lease fields outside dispatching;
//   - message id or accepted time outside accepted.
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

	if e.State == StateAccepted {
		if e.TelegramMessageID == 0 {
			return fmt.Errorf("%w: accepted without message id",
				ErrInvalidEntry)
		}
		if e.AcceptedAt.IsZero() {
			return fmt.Errorf("%w: accepted without accepted time",
				ErrInvalidEntry)
		}
	} else {
		if e.TelegramMessageID != 0 {
			return fmt.Errorf("%w: message id outside accepted state",
				ErrInvalidEntry)
		}
		if !e.AcceptedAt.IsZero() {
			return fmt.Errorf("%w: accepted time outside accepted state",
				ErrInvalidEntry)
		}
	}

	return nil
}

// CanTransition reports whether the state machine allows a transition
// from e.State to next.
//
// Allowed transitions:
//
//	queued           -> dispatching, canceled
//	dispatching      -> accepted, failed_retryable,
//	                    failed_permanent, uncertain
//	failed_retryable -> dispatching, queued, canceled
//	uncertain        -> canceled
//
// accepted, failed_permanent, canceled are terminal.
//
// failed_retryable may transition directly to dispatching so the
// dispatcher can claim a retry without an intermediate queued step.
// The two-step path failed_retryable -> queued -> dispatching would
// introduce an extra concurrency window without adding safety.
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
	case StateFailedRetryable:
		return next == StateDispatching ||
			next == StateQueued ||
			next == StateCanceled
	case StateUncertain:
		return next == StateCanceled
	case StateAccepted, StateFailedPermanent, StateCanceled:
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
// messageID must be non-zero. AcceptedAt and UpdatedAt are both set
// to now.
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

// MarkUncertain transitions a dispatching entry into uncertain.
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
// Cancel is rejected for dispatching, accepted, failed_permanent, and
// already canceled entries.
func (e Entry) Cancel(now time.Time) (Entry, error) {
	if e.State == StateAccepted {
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

package outbox

import (
	"context"
	"time"
)

// Store is the persistence contract for outbox entries.
//
// The contract is deliberately backend-agnostic: it does not expose
// files, SQL, transactions, or migrations. Implementations in PR-08
// are limited to the in-memory store; a durable backend is added in
// PR-08C once ADR-0002 moves to Accepted.
//
// Concurrency model:
//
//   - All mutation methods that change Entry.State require the caller
//     to pass expectedVersion, taken from the Entry that was read
//     before the mutation. The Store returns ErrVersionConflict when
//     the current version does not match.
//   - Claim is the only method that acquires the dispatching lease.
//     It must be atomic with respect to other Claim calls and to
//     Cancel.
//   - RecoverInterrupted converts dispatching entries with an expired
//     lease to uncertain. It never returns them to queued.
type Store interface {
	// Enqueue stores a new entry. The entry must be in StateQueued and
	// pass Validate. Returning ErrDuplicateID for an existing ID is
	// expected.
	Enqueue(ctx context.Context, entry Entry) error

	// Get returns the entry with the given ID.
	Get(ctx context.Context, id ID) (Entry, error)

	// ListReady returns entries that are eligible for dispatch at now,
	// ordered by CreatedAt ascending. limit caps the result set.
	//
	// Terminal entries, including uncertain and canceled, are never
	// returned.
	ListReady(ctx context.Context, now time.Time, limit int) ([]Entry, error)

	// ListAll returns every entry, ordered by CreatedAt ascending.
	ListAll(ctx context.Context) ([]Entry, error)

	// Claim atomically transitions a queued or failed_retryable entry
	// into dispatching.
	//
	// It returns the updated entry. If the lease is already held by
	// another owner and has not expired, it returns ErrLeaseHeld. If
	// expectedVersion does not match the current version, it returns
	// ErrVersionConflict.
	Claim(
		ctx context.Context,
		id ID,
		expectedVersion uint64,
		owner string,
		leaseUntil time.Time,
		now time.Time,
	) (Entry, error)

	// MarkAccepted persists the accepted state for a dispatching
	// entry.
	MarkAccepted(
		ctx context.Context,
		id ID,
		expectedVersion uint64,
		messageID int64,
		now time.Time,
	) (Entry, error)

	// MarkRetryable persists a retryable failure for a dispatching
	// entry.
	MarkRetryable(
		ctx context.Context,
		id ID,
		expectedVersion uint64,
		nextAttempt time.Time,
		code int,
		message string,
		now time.Time,
	) (Entry, error)

	// MarkPermanentFailure persists a permanent failure for a
	// dispatching entry.
	MarkPermanentFailure(
		ctx context.Context,
		id ID,
		expectedVersion uint64,
		code int,
		message string,
		now time.Time,
	) (Entry, error)

	// MarkUncertain persists an uncertain outcome for a dispatching
	// entry. reason must be non-blank and must not contain message
	// text.
	MarkUncertain(
		ctx context.Context,
		id ID,
		expectedVersion uint64,
		reason string,
		now time.Time,
	) (Entry, error)

	// Cancel marks a queued, retryable, or uncertain entry as
	// canceled.
	//
	// Canceling an uncertain entry is an explicit user resolution. It
	// does not prove that Telegram did not accept the original send.
	//
	// Cancel is rejected for dispatching, accepted, permanently
	// failed, and already canceled entries.
	Cancel(
		ctx context.Context,
		id ID,
		expectedVersion uint64,
		now time.Time,
	) (Entry, error)

	// RecoverInterrupted converts dispatching entries whose lease has
	// expired into uncertain. It returns the number of recovered
	// entries.
	//
	// It never returns a dispatching entry to queued: a process exit
	// after dispatch started cannot prove whether TDLib accepted the
	// request.
	RecoverInterrupted(ctx context.Context, now time.Time) (int, error)
}

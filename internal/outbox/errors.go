package outbox

import "errors"

var (
	// ErrInvalidEntry is returned when an entry violates a domain
	// invariant.
	ErrInvalidEntry = errors.New("outbox: invalid entry")

	// ErrInvalidTransition is returned when a state transition is not
	// allowed by the domain model.
	ErrInvalidTransition = errors.New("outbox: invalid state transition")

	// ErrVersionConflict is returned by Store methods that use
	// optimistic concurrency and observe an unexpected entry version.
	ErrVersionConflict = errors.New("outbox: version conflict")

	// ErrNotFound is returned when an entry does not exist.
	ErrNotFound = errors.New("outbox: entry not found")

	// ErrDuplicateID is returned when Enqueue receives an ID that is
	// already present.
	ErrDuplicateID = errors.New("outbox: duplicate id")

	// ErrLeaseHeld is returned when Claim observes an active lease
	// held by another owner.
	ErrLeaseHeld = errors.New("outbox: lease held")

	// ErrCancelAfterAccepted is returned when a caller attempts to
	// cancel an entry that has already been accepted by TDLib.
	ErrCancelAfterAccepted = errors.New("outbox: cancel after accepted")
)

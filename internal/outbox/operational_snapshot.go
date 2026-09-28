package outbox

import (
	"context"
	"errors"
	"fmt"
)

// OperationalSnapshot is aggregate, payload-free outbox metadata.
//
// It reports how many entries exist in each persisted state and nothing else.
// It intentionally excludes entry identifiers, account keys, chat IDs,
// message bodies, encrypted payloads, cipher material, lease ownership,
// database identity, and provider error details.
//
// A snapshot is a read-time observation. It is never a retry decision and
// must not be used to trigger state transitions.
type OperationalSnapshot struct {
	Queued          int64
	Dispatching     int64
	Accepted        int64
	Sent            int64
	FailedRetryable int64
	FailedPermanent int64
	Uncertain       int64
	Canceled        int64
}

// OperationalSnapshotReader provides aggregate payload-free outbox metadata.
//
// It is a separate capability on purpose: the transactional Store contract
// stays unchanged, so callers that do not need operational visibility do not
// have to implement or depend on it.
type OperationalSnapshotReader interface {
	ReadOperationalSnapshot(
		ctx context.Context,
	) (OperationalSnapshot, error)
}

// addOperationalCount folds one persisted state and its count into a
// snapshot.
//
// Both store implementations share this helper so the state matrix and the
// rejection of unknown states are defined exactly once.
func addOperationalCount(
	snapshot *OperationalSnapshot,
	state State,
	count int64,
) error {
	if snapshot == nil {
		return errors.New("outbox operational: snapshot is required")
	}
	if count < 0 {
		return fmt.Errorf(
			"outbox operational: state %q count %d must not be negative",
			state,
			count,
		)
	}

	switch state {
	case StateQueued:
		snapshot.Queued += count
	case StateDispatching:
		snapshot.Dispatching += count
	case StateAccepted:
		snapshot.Accepted += count
	case StateSent:
		snapshot.Sent += count
	case StateFailedRetryable:
		snapshot.FailedRetryable += count
	case StateFailedPermanent:
		snapshot.FailedPermanent += count
	case StateUncertain:
		snapshot.Uncertain += count
	case StateCanceled:
		snapshot.Canceled += count
	default:
		return fmt.Errorf(
			"%w: operational state %q is not supported",
			ErrInvalidEntry,
			state,
		)
	}

	return nil
}

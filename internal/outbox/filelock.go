package outbox

import (
	"context"
	"errors"
)

// ErrFileLockUnavailable is returned when the file lock backing
// CreateKey cannot be acquired.
var ErrFileLockUnavailable = errors.New(
	"outbox: file lock unavailable",
)

// fileLock is a single-owner advisory lock on a per-database file. It
// serializes CreateKey across cooperating processes on one machine.
// Release is idempotent.
type fileLock interface {
	Release() error
}

// fileLockFactory acquires per-database locks.
type fileLockFactory interface {
	Acquire(
		ctx context.Context,
		databaseID string,
	) (fileLock, error)
}

//go:build !linux && !darwin

package outbox

import "context"

// AcquireRunLock returns a lock that holds nothing on platforms without
// advisory file locks.
//
// The durable outbox is already unavailable there: NewPlatformKeyProvider
// returns ErrOutboxKeyProviderUnsupported, so no queue can be open and
// nothing can be in use. A lock that never blocks keeps the callers
// portable without pretending to protect a queue that cannot exist.
func AcquireRunLock(
	context.Context,
	string,
) (RunLock, error) {
	return unsupportedRunLock{}, nil
}

// unsupportedRunLock is a no-op RunLock.
type unsupportedRunLock struct{}

// Release does nothing and always succeeds.
func (unsupportedRunLock) Release() error { return nil }

var _ RunLock = unsupportedRunLock{}

package outbox

import "errors"

// ErrOutboxQueueInUse is returned when the queue of a data directory is
// already open in another telecli process.
//
// Only telecli takes this lock, so it means exactly one thing: a
// telecli session holds the queue. That is enough to refuse an operation
// which would move the queue file out from under a running session.
var ErrOutboxQueueInUse = errors.New(
	"outbox: queue is in use by another process",
)

// RunLock is an exclusive hold on the message queue of one data
// directory.
//
// It exists so a process that rearranges the queue on disk can tell that
// no other telecli has it open. Release is idempotent, and the lock
// disappears with the process that holds it, so a killed process never
// leaves the queue locked.
type RunLock interface {
	Release() error
}

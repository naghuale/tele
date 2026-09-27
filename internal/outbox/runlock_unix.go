//go:build linux || darwin

package outbox

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

// runLockPrefix namespaces the run lock inside the shared lock
// directory, so it can never collide with the key-creation lock of a
// queue identity that happens to be spelled like a data folder.
const runLockPrefix = "queue:"

// AcquireRunLock takes the exclusive lock of the queue in dataDir.
//
// It never waits. A queue that is in use must be reported to the user
// at once, because the alternative is a command that looks hung while a
// session they cannot see keeps the queue.
//
// The lock lives in the shared lock directory next to the key locks
// rather than in the data folder: a diagnostic that must not change
// anything has no business creating a file there, and the queue folder
// may not even exist yet on a machine that never sent a message.
func AcquireRunLock(
	ctx context.Context,
	dataDir string,
) (RunLock, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(dataDir) == "" {
		return nil, fmt.Errorf(
			"%w: empty data dir",
			ErrOutboxInvalidConfig,
		)
	}

	factory, err := NewFileLockFactory()
	if err != nil {
		return nil, err
	}

	return factory.TryAcquire(ctx, runLockIdentity(dataDir))
}

// runLockIdentity names the lock of one data folder.
//
// The identity is hashed into the lock file name, so the folder path
// itself is not written to disk.
func runLockIdentity(dataDir string) string {
	return runLockPrefix + filepath.Clean(dataDir)
}

var _ RunLock = (*flockHandle)(nil)

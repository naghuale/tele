//go:build linux || darwin

package outbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

const fileLockRetryInterval = 25 * time.Millisecond

type dirFileLockFactory struct {
	dir string
}

// NewFileLockFactory prepares a lock directory for the current
// process owner.
//
// The directory is created with 0700 if it does not exist. If it
// exists, ownership and permissions are validated: the directory must
// belong to the current UID and must not grant write access to group
// or other users.
func NewFileLockFactory() (fileLockFactory, error) {
	dir, err := defaultLockDir()
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf(
			"%w: create lock directory: %v",
			ErrFileLockUnavailable,
			err,
		)
	}

	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf(
			"%w: stat lock directory: %v",
			ErrFileLockUnavailable,
			err,
		)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf(
			"%w: lock path is not a directory",
			ErrFileLockUnavailable,
		)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf(
			"%w: insecure lock directory permissions %o",
			ErrFileLockUnavailable,
			info.Mode().Perm(),
		)
	}

	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil, fmt.Errorf(
			"%w: cannot determine lock directory owner",
			ErrFileLockUnavailable,
		)
	}
	if stat.Uid != uint32(os.Getuid()) {
		return nil, fmt.Errorf(
			"%w: lock directory owner mismatch",
			ErrFileLockUnavailable,
		)
	}

	return &dirFileLockFactory{dir: dir}, nil
}

func defaultLockDir() (string, error) {
	if runtimeDir := os.Getenv("XDG_RUNTIME_DIR"); runtimeDir != "" {
		return filepath.Join(
			runtimeDir, "telecli-outbox-locks",
		), nil
	}
	if tempDir := os.Getenv("TMPDIR"); tempDir != "" {
		return filepath.Join(
			tempDir,
			fmt.Sprintf(
				"telecli-outbox-locks-%d", os.Getuid(),
			),
		), nil
	}
	return filepath.Join(
		"/tmp",
		fmt.Sprintf(
			"telecli-outbox-locks-%d", os.Getuid(),
		),
	), nil
}

// Acquire opens (creating if needed) a lock file for databaseID and
// takes an exclusive advisory lock on it.
//
// flock is performed with LOCK_NB in a bounded retry loop, so ctx
// cancellation while waiting is observed.
func (f *dirFileLockFactory) Acquire(
	ctx context.Context,
	databaseID string,
) (fileLock, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateDatabaseID(databaseID); err != nil {
		return nil, err
	}
	if f == nil || f.dir == "" {
		return nil, ErrFileLockUnavailable
	}

	path := filepath.Join(
		f.dir, lockFileName(databaseID),
	)

	file, err := os.OpenFile(
		path,
		os.O_CREATE|os.O_RDWR,
		0o600,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"%w: open lock file: %v",
			ErrFileLockUnavailable,
			err,
		)
	}

	ticker := time.NewTicker(fileLockRetryInterval)
	defer ticker.Stop()

	for {
		err := syscall.Flock(
			int(file.Fd()),
			syscall.LOCK_EX|syscall.LOCK_NB,
		)

		switch {
		case err == nil:
			return &flockHandle{file: file}, nil

		case errors.Is(err, syscall.EWOULDBLOCK),
			errors.Is(err, syscall.EAGAIN):
			// Wait for the next retry or context cancellation.

		default:
			_ = file.Close()
			return nil, fmt.Errorf(
				"%w: flock: %v",
				ErrFileLockUnavailable,
				err,
			)
		}

		select {
		case <-ctx.Done():
			_ = file.Close()
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func lockFileName(databaseID string) string {
	sum := sha256.Sum256([]byte(databaseID))
	return hex.EncodeToString(sum[:]) + ".lock"
}

type flockHandle struct {
	file *os.File
}

// Release unlocks and closes the lock file. Idempotent.
func (h *flockHandle) Release() error {
	if h == nil || h.file == nil {
		return nil
	}
	file := h.file
	h.file = nil

	unlockErr := syscall.Flock(
		int(file.Fd()), syscall.LOCK_UN,
	)
	closeErr := file.Close()

	if unlockErr != nil {
		return fmt.Errorf(
			"%w: unlock: %v",
			ErrFileLockUnavailable,
			unlockErr,
		)
	}
	return closeErr
}

var _ fileLockFactory = (*dirFileLockFactory)(nil)
var _ fileLock = (*flockHandle)(nil)

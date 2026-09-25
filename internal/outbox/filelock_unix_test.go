//go:build linux || darwin

package outbox

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func TestFileLockSerializesAcquire(t *testing.T) {
	dir := t.TempDir()
	factory := &dirFileLockFactory{dir: dir}

	first, err := factory.Acquire(context.Background(), "database-1")
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		second, err := factory.Acquire(
			context.Background(), "database-1",
		)
		if second != nil {
			_ = second.Release()
		}
		done <- err
	}()

	// Release first; second must then acquire.
	if err := first.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("second Acquire: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("second Acquire did not complete after Release")
	}
}

func TestFileLockWaitHonoursContextCancellation(t *testing.T) {
	factory := &dirFileLockFactory{dir: t.TempDir()}

	first, err := factory.Acquire(context.Background(), "database-1")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Release() }()

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		second, err := factory.Acquire(ctx, "database-1")
		if second != nil {
			_ = second.Release()
		}
		done <- err
	}()

	// Give the goroutine a chance to enter the retry loop.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Acquire did not stop after context cancellation")
	}
}

func TestFileLockHonoursCancelledContextBeforeAcquire(t *testing.T) {
	factory := &dirFileLockFactory{dir: t.TempDir()}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := factory.Acquire(ctx, "canceled")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestFileLockRejectsEmptyDatabaseID(t *testing.T) {
	factory := &dirFileLockFactory{dir: t.TempDir()}

	for _, id := range []string{"", " ", "\t"} {
		if _, err := factory.Acquire(
			context.Background(), id,
		); !errors.Is(err, ErrOutboxKeyInvalidDatabaseID) {
			t.Fatalf("id=%q: err = %v", id, err)
		}
	}
}

func TestNewFileLockFactoryCreatesSecureDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)

	if _, err := NewFileLockFactory(); err != nil {
		t.Fatalf("NewFileLockFactory: %v", err)
	}

	info, err := os.Stat(dir + "/telecli-outbox-locks")
	if err != nil {
		t.Fatalf("lock dir not created: %v", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("lock dir permissions = %o", info.Mode().Perm())
	}
}

func TestNewFileLockFactoryRejectsInsecurePermissions(t *testing.T) {
	dir := t.TempDir()
	insecure := dir + "/telecli-outbox-locks"
	if err := os.MkdirAll(insecure, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_RUNTIME_DIR", dir)

	if _, err := NewFileLockFactory(); !errors.Is(
		err, ErrFileLockUnavailable,
	) {
		t.Fatalf("err = %v, want ErrFileLockUnavailable", err)
	}
}

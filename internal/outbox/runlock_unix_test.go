//go:build linux || darwin

package outbox

import (
	"context"
	"errors"
	"testing"
)

// A second holder must be refused at once. Waiting would look like a
// hung command while a session the user cannot see keeps the queue.
func TestAcquireRunLockRefusesASecondHolder(t *testing.T) {
	t.Parallel()

	dir := tempQueueDir(t)

	first, err := AcquireRunLock(context.Background(), dir)
	if err != nil {
		t.Fatalf("AcquireRunLock: %v", err)
	}
	defer func() { _ = first.Release() }()

	if _, err := AcquireRunLock(
		context.Background(),
		dir,
	); !errors.Is(err, ErrOutboxQueueInUse) {
		t.Fatalf("error = %v, want ErrOutboxQueueInUse", err)
	}
}

func TestAcquireRunLockIsReleased(t *testing.T) {
	t.Parallel()

	dir := tempQueueDir(t)

	first, err := AcquireRunLock(context.Background(), dir)
	if err != nil {
		t.Fatalf("AcquireRunLock: %v", err)
	}
	if err := first.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	// Release is idempotent, so a second call must not fail a shutdown
	// that already released the queue.
	if err := first.Release(); err != nil {
		t.Fatalf("Release twice: %v", err)
	}

	second, err := AcquireRunLock(context.Background(), dir)
	if err != nil {
		t.Fatalf("AcquireRunLock after release: %v", err)
	}
	if err := second.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
}

func TestAcquireRunLockRejectsUnusableInput(t *testing.T) {
	t.Parallel()

	if _, err := AcquireRunLock(
		context.Background(),
		"  ",
	); !errors.Is(err, ErrOutboxInvalidConfig) {
		t.Fatalf("error = %v, want ErrOutboxInvalidConfig", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := AcquireRunLock(
		ctx,
		tempQueueDir(t),
	); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

// A session owns the queue while it runs, and a diagnostic must not be
// the reason it cannot start.
func TestOpenExclusiveRefusesAQueueInUse(t *testing.T) {
	t.Parallel()

	dir := tempQueueDir(t)
	provider := newFactoryKeyProvider()

	held, err := AcquireRunLock(context.Background(), dir)
	if err != nil {
		t.Fatalf("AcquireRunLock: %v", err)
	}
	defer func() { _ = held.Release() }()

	if _, err := Open(
		context.Background(),
		Config{DataDir: dir, DatabaseID: "primary"},
		Deps{KeyProvider: provider, Exclusive: true},
	); !errors.Is(err, ErrOutboxQueueInUse) {
		t.Fatalf("error = %v, want ErrOutboxQueueInUse", err)
	}

	// A refused open must not leave a key behind: nothing was opened.
	if _, ok := provider.keys["primary"]; ok {
		t.Fatal("a refused exclusive open created a key")
	}
}

func TestOpenExclusiveHoldsTheQueueUntilClose(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dir := tempQueueDir(t)
	provider := newFactoryKeyProvider()

	box, err := Open(
		ctx,
		Config{DataDir: dir, DatabaseID: "primary"},
		Deps{KeyProvider: provider, Exclusive: true},
	)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	if _, err := AcquireRunLock(ctx, dir); !errors.Is(
		err,
		ErrOutboxQueueInUse,
	) {
		t.Fatalf("error = %v, want ErrOutboxQueueInUse while open", err)
	}

	if err := box.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	again, err := AcquireRunLock(ctx, dir)
	if err != nil {
		t.Fatalf("AcquireRunLock after Close: %v", err)
	}
	if err := again.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
}

// A probe that is not exclusive must keep working while a session holds
// the queue, or telecli doctor would report a busy queue instead of the
// state of the key.
func TestOpenWithoutExclusiveIgnoresTheRunLock(t *testing.T) {
	t.Parallel()

	dir := tempQueueDir(t)
	provider := newFactoryKeyProvider()

	held, err := AcquireRunLock(context.Background(), dir)
	if err != nil {
		t.Fatalf("AcquireRunLock: %v", err)
	}
	defer func() { _ = held.Release() }()

	box, err := Open(
		context.Background(),
		Config{DataDir: dir, DatabaseID: "primary"},
		Deps{KeyProvider: provider},
	)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := box.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

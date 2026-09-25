package outbox

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// sqliteFactory returns a factory that opens a fresh SQLite store
// backed by a temporary file.
func sqliteFactory(t *testing.T) (Store, string) {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "outbox.db")

	store, err := NewSQLiteStore(
		context.Background(),
		SQLiteStoreConfig{Path: path},
		&fakeCipher{},
	)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}

	t.Cleanup(func() {
		if closer, ok := store.(interface{ Close() error }); ok {
			_ = closer.Close()
		}
	})
	return store, path
}

// assertFileDoesNotContain reads path and fails when secret appears.
// A missing file is not a failure: not every SQLite mode creates every
// sidecar.
func assertFileDoesNotContain(
	t *testing.T,
	path string,
	secret []byte,
) {
	t.Helper()

	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if bytes.Contains(raw, secret) {
		t.Fatalf(
			"%s contains plaintext message body",
			filepath.Base(path),
		)
	}
}

// ---- Constructor guards ----

func TestNewSQLiteStoreRejectsNilCipher(t *testing.T) {
	_, err := NewSQLiteStore(
		context.Background(),
		SQLiteStoreConfig{
			Path: filepath.Join(t.TempDir(), "outbox.db"),
		},
		nil,
	)
	if err == nil {
		t.Fatal("expected nil cipher error")
	}
}

func TestNewSQLiteStoreRejectsEmptyPath(t *testing.T) {
	_, err := NewSQLiteStore(
		context.Background(),
		SQLiteStoreConfig{},
		&fakeCipher{},
	)
	if err == nil {
		t.Fatal("expected empty path error")
	}
}

// ---- Contract ----

func TestSQLiteStoreContract(t *testing.T) {
	runStoreContract(t, func(t *testing.T) Store {
		store, _ := sqliteFactory(t)
		return store
	})
}

// ---- Migration regression ----

func TestSQLiteStoreCreatesInitialSchema(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "outbox.db")

	store, err := NewSQLiteStore(
		context.Background(),
		SQLiteStoreConfig{Path: path},
		&fakeCipher{},
	)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer func() {
		if closer, ok := store.(interface{ Close() error }); ok {
			_ = closer.Close()
		}
	}()

	entry := queuedEntry("op-1", 42, "hello")
	if err := store.Enqueue(context.Background(), entry); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
}

func TestSQLiteStoreReopenKeepsSchemaVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "outbox.db")

	for attempt := 0; attempt < 2; attempt++ {
		store, err := NewSQLiteStore(
			context.Background(),
			SQLiteStoreConfig{Path: path},
			&fakeCipher{},
		)
		if err != nil {
			t.Fatalf("open %d: %v", attempt+1, err)
		}
		closer, ok := store.(interface{ Close() error })
		if !ok {
			t.Fatalf("store does not implement Close")
		}
		if err := closer.Close(); err != nil {
			t.Fatalf("close %d: %v", attempt+1, err)
		}
	}
}

func TestSQLiteStoreUsesWALMode(t *testing.T) {
	rawStore, _ := sqliteFactory(t)

	store, ok := rawStore.(*sqliteStore)
	if !ok {
		t.Fatalf("store type = %T, want *sqliteStore", rawStore)
	}

	var mode string
	if err := store.db.QueryRowContext(
		context.Background(),
		`PRAGMA journal_mode`,
	).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Fatalf("journal mode = %q, want wal", mode)
	}
}

// ---- Durability across reopen ----

func TestSQLiteStorePersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "outbox.db")

	open := func() Store {
		store, err := NewSQLiteStore(
			context.Background(),
			SQLiteStoreConfig{Path: path},
			&fakeCipher{},
		)
		if err != nil {
			t.Fatalf("NewSQLiteStore: %v", err)
		}
		return store
	}

	store := open()
	entry := queuedEntry("op-1", 42, "hello")
	if err := store.Enqueue(context.Background(), entry); err != nil {
		t.Fatal(err)
	}
	if closer, ok := store.(interface{ Close() error }); ok {
		if err := closer.Close(); err != nil {
			t.Fatal(err)
		}
	}

	reopened := open()
	got, err := reopened.Get(context.Background(), entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "hello" {
		t.Fatalf("Text = %q, want hello", got.Text)
	}
	if got.State != StateQueued {
		t.Fatalf("State = %s", got.State)
	}
}

// ---- Encryption: no plaintext on disk ----

func TestSQLiteStoreDoesNotWritePlaintextMessage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "outbox.db")

	store, err := NewSQLiteStore(
		context.Background(),
		SQLiteStoreConfig{Path: path},
		&fakeCipher{},
	)
	if err != nil {
		t.Fatal(err)
	}

	secret := []byte("top-secret-message-body")
	entry := queuedEntry("op-1", 42, string(secret))
	if err := store.Enqueue(context.Background(), entry); err != nil {
		t.Fatal(err)
	}

	// Inspect while the connection remains open. In WAL mode, recently
	// committed data may still reside in the WAL sidecar.
	assertFileDoesNotContain(t, path, secret)
	assertFileDoesNotContain(t, path+"-wal", secret)
	assertFileDoesNotContain(t, path+"-journal", secret)
	assertFileDoesNotContain(t, path+"-shm", secret)

	closer, ok := store.(interface{ Close() error })
	if !ok {
		t.Fatal("store does not implement Close")
	}
	if err := closer.Close(); err != nil {
		t.Fatal(err)
	}

	// Inspect again after close and any final checkpoint.
	assertFileDoesNotContain(t, path, secret)
	assertFileDoesNotContain(t, path+"-wal", secret)
	assertFileDoesNotContain(t, path+"-journal", secret)
	assertFileDoesNotContain(t, path+"-shm", secret)
}

// ---- Reopen after expired dispatch recovers uncertain ----

func TestSQLiteStoreRecoverInterruptedAfterReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "outbox.db")

	open := func() Store {
		store, err := NewSQLiteStore(
			context.Background(),
			SQLiteStoreConfig{Path: path},
			&fakeCipher{},
		)
		if err != nil {
			t.Fatalf("NewSQLiteStore: %v", err)
		}
		return store
	}

	store := open()
	entry := queuedEntry("op-1", 42, "hello")
	if err := store.Enqueue(context.Background(), entry); err != nil {
		t.Fatal(err)
	}

	now := time.Unix(1700000000, 0).UTC()
	claimed, err := store.Claim(
		context.Background(),
		entry.ID,
		entry.Version,
		"previous-instance",
		now.Add(time.Minute),
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if closer, ok := store.(interface{ Close() error }); ok {
		if err := closer.Close(); err != nil {
			t.Fatal(err)
		}
	}

	reopened := open()
	recovered, err := reopened.RecoverInterrupted(
		context.Background(),
		now.Add(time.Hour),
	)
	if err != nil {
		t.Fatal(err)
	}
	if recovered != 1 {
		t.Fatalf("recovered = %d, want 1", recovered)
	}

	got, err := reopened.Get(context.Background(), claimed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateUncertain {
		t.Fatalf("State = %s, want uncertain", got.State)
	}
}

// ---- Cipher failure rollback ----

func TestSQLiteStoreCipherFailureRollsBackEnqueue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "outbox.db")

	cipherErr := errors.New("cipher failed")
	store, err := NewSQLiteStore(
		context.Background(),
		SQLiteStoreConfig{Path: path},
		&fakeCipher{failOnEncrypt: cipherErr},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closer, ok := store.(interface{ Close() error }); ok {
			_ = closer.Close()
		}
	}()

	entry := queuedEntry("op-1", 42, "hello")
	if err := store.Enqueue(context.Background(), entry); err == nil {
		t.Fatal("expected cipher failure")
	}

	all, err := store.ListAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 0 {
		t.Fatalf("store contains %d entries after failed enqueue", len(all))
	}
}

func TestSQLiteStoreCipherFailureRollsBackMutation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "outbox.db")

	cipher := &fakeCipher{}

	rawStore, err := NewSQLiteStore(
		context.Background(),
		SQLiteStoreConfig{Path: path},
		cipher,
	)
	if err != nil {
		t.Fatal(err)
	}

	store, ok := rawStore.(*sqliteStore)
	if !ok {
		t.Fatalf("store type = %T, want *sqliteStore", rawStore)
	}
	defer func() { _ = store.Close() }()

	entry := queuedEntry("op-1", 42, "hello")
	if err := store.Enqueue(context.Background(), entry); err != nil {
		t.Fatal(err)
	}

	cipher.failOnEncrypt = errors.New("mutation encryption failed")

	now := time.Unix(1700000000, 0).UTC()
	_, err = store.Claim(
		context.Background(),
		entry.ID,
		entry.Version,
		"dispatcher-1",
		now.Add(time.Minute),
		now,
	)
	if err == nil {
		t.Fatal("expected cipher failure")
	}
	if !strings.Contains(err.Error(), "mutation encryption failed") {
		t.Fatalf("error = %v, want cipher failure", err)
	}

	// Restore cipher operation before reading the original row.
	cipher.failOnEncrypt = nil

	got, err := store.Get(context.Background(), entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateQueued {
		t.Fatalf("state = %s, want queued", got.State)
	}
	if got.Version != entry.Version {
		t.Fatalf("version = %d, want %d", got.Version, entry.Version)
	}
	if got.AttemptCount != entry.AttemptCount {
		t.Fatalf("attempt count = %d, want %d",
			got.AttemptCount, entry.AttemptCount)
	}
	if got.LeaseOwner != "" || !got.LeaseUntil.IsZero() {
		t.Fatalf("lease changed after rollback: owner=%q until=%v",
			got.LeaseOwner, got.LeaseUntil)
	}
}

// ---- Persisted state validation ----

func TestSQLiteStoreRejectsInvalidPersistedState(t *testing.T) {
	rawStore, _ := sqliteFactory(t)

	store, ok := rawStore.(*sqliteStore)
	if !ok {
		t.Fatalf("store type = %T, want *sqliteStore", rawStore)
	}

	entry := queuedEntry("op-1", 42, "hello")
	if err := store.Enqueue(context.Background(), entry); err != nil {
		t.Fatal(err)
	}

	if _, err := store.db.ExecContext(
		context.Background(),
		`UPDATE outbox_entries SET state = 'invalid-state' WHERE id = ?`,
		string(entry.ID),
	); err != nil {
		t.Fatal(err)
	}

	_, err := store.Get(context.Background(), entry.ID)
	if !errors.Is(err, ErrInvalidEntry) {
		t.Fatalf("error = %v, want ErrInvalidEntry", err)
	}
}

func TestSQLiteStoreRejectsNegativePersistedVersion(t *testing.T) {
	rawStore, _ := sqliteFactory(t)

	store, ok := rawStore.(*sqliteStore)
	if !ok {
		t.Fatalf("store type = %T, want *sqliteStore", rawStore)
	}

	entry := queuedEntry("op-1", 42, "hello")
	if err := store.Enqueue(context.Background(), entry); err != nil {
		t.Fatal(err)
	}

	if _, err := store.db.ExecContext(
		context.Background(),
		`UPDATE outbox_entries SET version = -1 WHERE id = ?`,
		string(entry.ID),
	); err != nil {
		t.Fatal(err)
	}

	_, err := store.Get(context.Background(), entry.ID)
	if !errors.Is(err, ErrInvalidEntry) {
		t.Fatalf("error = %v, want ErrInvalidEntry", err)
	}
}

// ---- Constraint on duplicate ID ----

func TestSQLiteStoreRejectsDuplicateID(t *testing.T) {
	store, _ := sqliteFactory(t)

	entry := queuedEntry("op-1", 42, "hello")
	if err := store.Enqueue(context.Background(), entry); err != nil {
		t.Fatal(err)
	}
	if err := store.Enqueue(context.Background(), entry); !errors.Is(err, ErrDuplicateID) {
		t.Fatalf("err = %v, want ErrDuplicateID", err)
	}
}

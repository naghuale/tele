package outbox

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestEntryStatusReaderContractSQLiteStore(t *testing.T) {
	t.Parallel()

	runEntryStatusReaderContract(t, func(t *testing.T) (Store, EntryStatusReader) {
		store := newStatusSQLiteStore(t, &fakeCipher{})
		return store, store.(EntryStatusReader)
	})
}

func TestSQLiteListEntryStatusesDoesNotDecryptPayload(t *testing.T) {
	t.Parallel()

	store := newStatusSQLiteStore(t, &fakeCipher{
		failOnDecrypt: errors.New("decrypt called"),
	})
	entry := queuedEntry("op-1", 42, "secret message payload")
	if err := store.Enqueue(context.Background(), entry); err != nil {
		t.Fatal(err)
	}
	if _, err := store.(EntryStatusReader).ListEntryStatuses(context.Background(), ListEntryStatusesQuery{
		AccountKey: entry.AccountKey,
		ChatID:     entry.ChatID,
	}); err != nil {
		t.Fatalf("ListEntryStatuses() error = %v", err)
	}
}

func TestSQLiteListEntryStatusesAfterReopen(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "outbox.db")
	store := newStatusSQLiteStoreAt(t, path, &fakeCipher{})
	entry := queuedEntry("op-1", 42, "payload")
	if err := store.Enqueue(context.Background(), entry); err != nil {
		t.Fatal(err)
	}
	if err := store.(interface{ Close() error }).Close(); err != nil {
		t.Fatal(err)
	}

	reopened := newStatusSQLiteStoreAt(t, path, &fakeCipher{})
	statuses, err := reopened.(EntryStatusReader).ListEntryStatuses(context.Background(), ListEntryStatusesQuery{
		AccountKey: entry.AccountKey,
		ChatID:     entry.ChatID,
	})
	if err != nil {
		t.Fatalf("ListEntryStatuses() error = %v", err)
	}
	if len(statuses) != 1 || statuses[0].ID != string(entry.ID) {
		t.Fatalf("statuses = %#v, want one entry", statuses)
	}
}

func TestSQLiteStatusIndexExists(t *testing.T) {
	t.Parallel()

	store := newStatusSQLiteStore(t, &fakeCipher{}).(*sqliteStore)
	var name string
	if err := store.db.QueryRowContext(
		context.Background(),
		`SELECT name FROM sqlite_master WHERE type = 'index' AND name = 'outbox_status_idx'`,
	).Scan(&name); err != nil {
		t.Fatalf("status index missing: %v", err)
	}
	if name != "outbox_status_idx" {
		t.Fatalf("index name = %q", name)
	}
}

func TestSQLiteListEntryStatusesRejectsInvalidPersistedMetadata(t *testing.T) {
	t.Parallel()

	store := newStatusSQLiteStore(t, &fakeCipher{}).(*sqliteStore)
	_, err := store.db.ExecContext(context.Background(), `
INSERT INTO outbox_entries (
    id, account_key, chat_id, encrypted_text, state, attempt_count,
    next_attempt_ns, telegram_message_id, last_error_code,
    last_error_message, created_at_ns, updated_at_ns, accepted_at_ns,
    lease_owner, lease_until_ns, version
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`,
		"bad",
		"account-1",
		int64(42),
		[]byte("ciphertext"),
		"future",
		int64(0),
		nil,
		nil,
		int64(0),
		"reason",
		int64(1700000000000000000),
		int64(1700000000000000000),
		nil,
		"",
		nil,
		int64(0),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.ListEntryStatuses(context.Background(), ListEntryStatusesQuery{
		AccountKey: "account-1",
		ChatID:     42,
	})
	if !errors.Is(err, ErrInvalidEntry) {
		t.Fatalf("error = %v, want ErrInvalidEntry", err)
	}
}

func newStatusSQLiteStore(
	t *testing.T,
	cipher PayloadCipher,
) Store {
	t.Helper()
	return newStatusSQLiteStoreAt(
		t,
		filepath.Join(t.TempDir(), "outbox.db"),
		cipher,
	)
}

func newStatusSQLiteStoreAt(
	t *testing.T,
	path string,
	cipher PayloadCipher,
) Store {
	t.Helper()
	store, err := NewSQLiteStore(
		context.Background(),
		SQLiteStoreConfig{Path: path},
		cipher,
	)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	return store
}

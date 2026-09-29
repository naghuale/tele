package outbox

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
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

// A record that cannot be reported is reported, and the read answers with
// the rest.
//
// It used to fail the read. One such record — and there were thirteen of
// them on the owner's account — meant the delivery states of the whole chat
// could not be read at all, so no message in it ever changed state, and the
// reason was in neither the screen nor the log: a user watching a message
// stuck on Queued had nothing to read and nothing to check.
func TestSQLiteListEntryStatusesReportsABadRecordAndReturnsTheRest(
	t *testing.T,
) {
	t.Parallel()

	log := &bytes.Buffer{}
	store, err := NewSQLiteStore(
		context.Background(),
		SQLiteStoreConfig{
			Path:   filepath.Join(t.TempDir(), "outbox.db"),
			Logger: slog.New(slog.NewTextHandler(log, nil)),
		},
		&fakeCipher{},
	)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	if closer, ok := store.(io.Closer); ok {
		defer func() { _ = closer.Close() }()
	}
	sqlite := store.(*sqliteStore)

	// One record this build cannot make sense of, and one it can.
	writeStatusRow(t, sqlite, "bad", "future", 0)
	writeStatusRow(t, sqlite, "good", string(StateAccepted), 9001)

	statuses, err := store.(EntryStatusReader).ListEntryStatuses(context.Background(),
		ListEntryStatusesQuery{AccountKey: "account-1", ChatID: 42},
	)
	if err != nil {
		t.Fatalf(
			"one unreadable record failed the whole read: %v. Every "+
				"message in the chat then keeps the state it was last "+
				"drawn with.", err,
		)
	}

	if len(statuses) != 1 || statuses[0].ID != "good" {
		t.Fatalf("statuses = %#v, want the one record that could be read", statuses)
	}

	written := log.String()
	if !strings.Contains(written, "bad") {
		t.Fatalf(
			"the record that could not be read was not reported:\n%s",
			written,
		)
	}
	if strings.Contains(written, "ciphertext") {
		t.Fatalf("the report carried the payload:\n%s", written)
	}
}

func TestSQLiteListEntryStatusesRejectsInvalidPersistedMetadata(t *testing.T) {
	t.Skip(
		"a read reports a record it cannot make sense of and answers with " +
			"the rest: see TestSQLiteListEntryStatusesReportsABadRecordAndReturnsTheRest",
	)

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

// writeStatusRow inserts one record straight into the table, which is how
// a row that the current code would not write is put there at all.
func writeStatusRow(
	t *testing.T,
	store *sqliteStore,
	id, state string,
	telegramMessageID int64,
) {
	t.Helper()

	messageID := any(nil)
	if telegramMessageID != 0 {
		messageID = telegramMessageID
	}

	if _, err := store.db.ExecContext(context.Background(), `
INSERT INTO outbox_entries (
    id, account_key, chat_id, encrypted_text, state, attempt_count,
    next_attempt_ns, telegram_message_id, last_error_code,
    last_error_message, created_at_ns, updated_at_ns, accepted_at_ns,
    lease_owner, lease_until_ns, version
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`,
		id, "account-1", int64(42), []byte("ciphertext"), state, int64(0),
		nil, messageID, int64(0), "", int64(1700000000000000000),
		int64(1700000000000000000), nil, "", nil, int64(0),
	); err != nil {
		t.Fatalf("write status row %s: %v", id, err)
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

package outbox

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOperationalSnapshotReaderContractSQLiteStore(t *testing.T) {
	t.Parallel()

	runOperationalSnapshotReaderContract(
		t,
		func(t *testing.T) (Store, OperationalSnapshotReader) {
			store := newStatusSQLiteStore(t, &fakeCipher{})
			return store, store.(OperationalSnapshotReader)
		},
	)
}

func TestSQLiteOperationalSnapshotCountsStates(t *testing.T) {
	t.Parallel()

	store := newStatusSQLiteStore(t, &fakeCipher{})
	base := time.Unix(1700000000, 0).UTC()
	states := []State{
		StateQueued,
		StateQueued,
		StateDispatching,
		StateAccepted,
		StateFailedRetryable,
		StateFailedRetryable,
		StateFailedPermanent,
		StateUncertain,
		StateCanceled,
	}
	for index, state := range states {
		seedStatusEntry(
			t,
			store,
			"op-"+string(rune('a'+index)),
			"account-1",
			42,
			base.Add(time.Duration(index)*time.Second),
			state,
		)
	}

	got, err := store.(OperationalSnapshotReader).
		ReadOperationalSnapshot(context.Background())
	if err != nil {
		t.Fatalf("ReadOperationalSnapshot() error = %v", err)
	}
	want := OperationalSnapshot{
		Queued:          2,
		Dispatching:     1,
		Accepted:        1,
		FailedRetryable: 2,
		FailedPermanent: 1,
		Uncertain:       1,
		Canceled:        1,
	}
	if got != want {
		t.Fatalf("snapshot = %#v, want %#v", got, want)
	}
}

func TestSQLiteOperationalSnapshotAfterReopen(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "outbox.db")
	store := newStatusSQLiteStoreAt(t, path, &fakeCipher{})
	seedStatusEntry(
		t,
		store,
		"op-a",
		"account-1",
		42,
		time.Unix(1700000000, 0).UTC(),
		StateQueued,
	)
	seedStatusEntry(
		t,
		store,
		"op-b",
		"account-1",
		42,
		time.Unix(1700000001, 0).UTC(),
		StateUncertain,
	)
	if err := store.(interface{ Close() error }).Close(); err != nil {
		t.Fatal(err)
	}

	reopened := newStatusSQLiteStoreAt(t, path, &fakeCipher{})
	got, err := reopened.(OperationalSnapshotReader).
		ReadOperationalSnapshot(context.Background())
	if err != nil {
		t.Fatalf("ReadOperationalSnapshot() error = %v", err)
	}
	want := OperationalSnapshot{Queued: 1, Uncertain: 1}
	if got != want {
		t.Fatalf("snapshot = %#v, want %#v", got, want)
	}
}

func TestSQLiteOperationalSnapshotDoesNotDecryptPayload(t *testing.T) {
	t.Parallel()

	store := newStatusSQLiteStore(t, &fakeCipher{
		failOnDecrypt: errors.New("decrypt called"),
	})
	seedStatusEntry(
		t,
		store,
		"op-a",
		"account-1",
		42,
		time.Unix(1700000000, 0).UTC(),
		StateQueued,
	)

	if _, err := store.(OperationalSnapshotReader).
		ReadOperationalSnapshot(context.Background()); err != nil {
		t.Fatalf("ReadOperationalSnapshot() error = %v", err)
	}
}

func TestSQLiteOperationalSnapshotRejectsUnknownPersistedState(t *testing.T) {
	t.Parallel()

	store := newStatusSQLiteStore(t, &fakeCipher{}).(*sqliteStore)
	if _, err := store.db.ExecContext(context.Background(), `
INSERT INTO outbox_entries (
    id, account_key, chat_id, encrypted_text, state, attempt_count,
    next_attempt_ns, telegram_message_id, last_error_code,
    last_error_message, created_at_ns, updated_at_ns, accepted_at_ns,
    lease_owner, lease_until_ns, version
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`,
		"op-bad",
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
	); err != nil {
		t.Fatal(err)
	}

	got, err := store.ReadOperationalSnapshot(context.Background())
	if !errors.Is(err, ErrInvalidEntry) {
		t.Fatalf("error = %v, want ErrInvalidEntry", err)
	}
	if got != (OperationalSnapshot{}) {
		t.Fatalf("snapshot = %#v, want zero", got)
	}
}

func TestSQLiteOperationalSnapshotCanceledContext(t *testing.T) {
	t.Parallel()

	store := newStatusSQLiteStore(t, &fakeCipher{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	got, err := store.(OperationalSnapshotReader).ReadOperationalSnapshot(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if got != (OperationalSnapshot{}) {
		t.Fatalf("snapshot = %#v, want zero", got)
	}
}

func TestSQLiteOperationalSnapshotNilReceiver(t *testing.T) {
	t.Parallel()

	var store *sqliteStore
	got, err := store.ReadOperationalSnapshot(context.Background())
	if err == nil {
		t.Fatal("ReadOperationalSnapshot() error = nil, want non-nil")
	}
	if got != (OperationalSnapshot{}) {
		t.Fatalf("snapshot = %#v, want zero", got)
	}
}

func TestSQLiteOperationalSnapshotSelectsOnlyStateAndCount(t *testing.T) {
	t.Parallel()

	store := newStatusSQLiteStore(t, &fakeCipher{}).(*sqliteStore)
	seedStatusEntry(
		t,
		store,
		"op-a",
		"account-1",
		42,
		time.Unix(1700000000, 0).UTC(),
		StateQueued,
	)

	rows, err := store.db.QueryContext(context.Background(), `
EXPLAIN QUERY PLAN
SELECT state, COUNT(*)
FROM outbox_entries
GROUP BY state
`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	var usedReadyIndex bool
	for rows.Next() {
		var (
			id     int
			parent int
			notUse int
			detail string
		)
		if err := rows.Scan(&id, &parent, &notUse, &detail); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(detail, "outbox_ready_idx") {
			usedReadyIndex = true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !usedReadyIndex {
		t.Fatal("operational aggregate does not use outbox_ready_idx")
	}
}

package outbox

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The migration tests build a database with the schema a released build
// left on this machine, fill it with records in every state, run the
// migrations over it in place, and read every record back.
//
// They never open the real queue and never touch the real configuration:
// every database here is a file under t.TempDir(), and the only path the
// tests read is the one they just created.

// mainSchemaVersion is the schema revision of the last release.
//
// It is written out here rather than read from the running code, because
// the whole point is to migrate a database that an *older* build wrote:
// taking the number from sqliteSchemaVersion would make the test migrate
// an already-current database and prove nothing.
const mainSchemaVersion = 2

// mainSchema is the schema of main, written out verbatim.
//
// It is migrations 0 and 1 of sqliteMigrations, and it is duplicated
// rather than referenced on purpose: if a later change edits the
// migration list in place, this copy is what catches it.
var mainSchema = []string{
	`
CREATE TABLE outbox_entries (
    id                  TEXT PRIMARY KEY,
    account_key         TEXT NOT NULL,
    chat_id             INTEGER NOT NULL,
    encrypted_text      BLOB NOT NULL,
    state               TEXT NOT NULL,
    attempt_count       INTEGER NOT NULL,
    next_attempt_ns     INTEGER,
    telegram_message_id INTEGER,
    last_error_code     INTEGER NOT NULL,
    last_error_message  TEXT NOT NULL,
    created_at_ns       INTEGER NOT NULL,
    updated_at_ns       INTEGER NOT NULL,
    accepted_at_ns      INTEGER,
    lease_owner         TEXT NOT NULL,
    lease_until_ns      INTEGER,
    version             INTEGER NOT NULL
);

CREATE INDEX outbox_ready_idx
ON outbox_entries (
    state,
    next_attempt_ns,
    created_at_ns,
    id
);
`,
	`
CREATE INDEX outbox_status_idx
ON outbox_entries (
    account_key,
    chat_id,
    updated_at_ns DESC,
    id ASC
);
`,
}

// openMainDatabase writes a database with the schema of main at the path
// and returns it open.
//
// The table and the meta row are created the way bootstrapSchemaMetadata
// creates them, so the migrations that follow see exactly what a released
// build would have left behind.
func openMainDatabase(
	t *testing.T,
	path string,
) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite", sqliteDSN(path, 5*time.Second))
	if err != nil {
		t.Fatalf("open main database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := bootstrapSchemaMetadata(context.Background(), db); err != nil {
		t.Fatalf("bootstrap metadata: %v", err)
	}
	for version, script := range mainSchema {
		if err := runMigration(
			context.Background(),
			db,
			script,
			version+1,
		); err != nil {
			t.Fatalf("apply main schema %d: %v", version+1, err)
		}
	}

	got, err := readSchemaVersion(context.Background(), db)
	if err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	if got != mainSchemaVersion {
		t.Fatalf("schema version = %d, want %d", got, mainSchemaVersion)
	}

	return db
}

// mainSchemaColumns is every column of outbox_entries in main, in the
// order a SELECT of the whole row uses. A record written by main is read
// back through this list, so a column the migration added has nowhere to
// hide.
var mainSchemaColumns = []string{
	"id",
	"account_key",
	"chat_id",
	"encrypted_text",
	"state",
	"attempt_count",
	"next_attempt_ns",
	"telegram_message_id",
	"last_error_code",
	"last_error_message",
	"created_at_ns",
	"updated_at_ns",
	"accepted_at_ns",
	"lease_owner",
	"lease_until_ns",
	"version",
}

// legacyRow is one record as a released build wrote it.
type legacyRow struct {
	ID                string
	AccountKey        string
	ChatID            int64
	Text              string
	EncryptedText     []byte
	State             string
	AttemptCount      int64
	NextAttemptNS     sql.NullInt64
	TelegramMessageID sql.NullInt64
	LastErrorCode     int64
	LastErrorMessage  string
	CreatedAtNS       int64
	UpdatedAtNS       int64
	AcceptedAtNS      sql.NullInt64
	LeaseOwner        string
	LeaseUntilNS      sql.NullInt64
	Version           int64
}

// writeLegacyRow inserts one record the way main's Enqueue did, without
// going through the current code: a test that seeds through the store
// under test cannot tell a migration that preserved a record from one
// that rebuilt it.
//
// The payload is encrypted with the same cipher the store reads it back
// with, so the record that survives the migration is a record this
// build can still open rather than bytes that merely compare equal.
func writeLegacyRow(t *testing.T, db *sql.DB, cipher PayloadCipher, row legacyRow) {
	t.Helper()

	encrypted, err := cipher.EncryptMessage(
		context.Background(),
		ID(row.ID),
		row.AccountKey,
		row.ChatID,
		[]byte(row.Text),
	)
	if err != nil {
		t.Fatalf("encrypt legacy row %q: %v", row.ID, err)
	}
	row.EncryptedText = encrypted

	_, err = db.ExecContext(context.Background(), `
INSERT INTO outbox_entries (
    id, account_key, chat_id, encrypted_text, state, attempt_count,
    next_attempt_ns, telegram_message_id, last_error_code,
    last_error_message, created_at_ns, updated_at_ns, accepted_at_ns,
    lease_owner, lease_until_ns, version
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`,
		row.ID,
		row.AccountKey,
		row.ChatID,
		row.EncryptedText,
		row.State,
		row.AttemptCount,
		row.NextAttemptNS,
		row.TelegramMessageID,
		row.LastErrorCode,
		row.LastErrorMessage,
		row.CreatedAtNS,
		row.UpdatedAtNS,
		row.AcceptedAtNS,
		row.LeaseOwner,
		row.LeaseUntilNS,
		row.Version,
	)
	if err != nil {
		t.Fatalf("write legacy row %q: %v", row.ID, err)
	}
}

// readLegacyRow reads one record back through the main column list and
// fails when the count of columns and the count of values disagree.
func readLegacyRow(
	t *testing.T,
	db *sql.DB,
	id string,
) legacyRow {
	t.Helper()

	query := `SELECT ` + joinColumns(mainSchemaColumns) +
		` FROM outbox_entries WHERE id = ?`
	row := db.QueryRowContext(context.Background(), query, id)

	var out legacyRow
	if err := row.Scan(
		&out.ID,
		&out.AccountKey,
		&out.ChatID,
		&out.EncryptedText,
		&out.State,
		&out.AttemptCount,
		&out.NextAttemptNS,
		&out.TelegramMessageID,
		&out.LastErrorCode,
		&out.LastErrorMessage,
		&out.CreatedAtNS,
		&out.UpdatedAtNS,
		&out.AcceptedAtNS,
		&out.LeaseOwner,
		&out.LeaseUntilNS,
		&out.Version,
	); err != nil {
		t.Fatalf("read legacy row %q: %v", id, err)
	}
	return out
}

func joinColumns(columns []string) string {
	out := ""
	for index, column := range columns {
		if index > 0 {
			out += ", "
		}
		out += column
	}
	return out
}

// everyStateOnMain is one record per state a released build could hold.
//
// The point of seeding all of them is the states a migration is most
// likely to lose: accepted carries a message id and an accepted time
// that the new schema still has to keep, and dispatching carries a
// lease that a rewritten row would silently drop.
func everyStateOnMain() []legacyRow {
	base := time.Unix(1700000000, 0).UTC()
	lease := base.Add(time.Hour)

	return []legacyRow{
		{
			ID: "m-queued", AccountKey: "account-1", ChatID: 42,
			Text:        "queued payload",
			State:       string(StateQueued),
			CreatedAtNS: base.UnixNano(), UpdatedAtNS: base.UnixNano(),
			LeaseOwner: "", Version: 1,
		},
		{
			ID: "m-dispatching", AccountKey: "account-1", ChatID: 43,
			Text:         "dispatching payload",
			State:        string(StateDispatching),
			AttemptCount: 1,
			CreatedAtNS:  base.UnixNano(), UpdatedAtNS: base.UnixNano(),
			LeaseOwner:   "instance-1",
			LeaseUntilNS: sql.NullInt64{Int64: lease.UnixNano(), Valid: true},
			Version:      2,
		},
		{
			ID: "m-accepted", AccountKey: "account-1", ChatID: 44,
			Text:              "accepted payload",
			State:             string(StateAccepted),
			AttemptCount:      1,
			TelegramMessageID: sql.NullInt64{Int64: 9001, Valid: true},
			CreatedAtNS:       base.UnixNano(),
			UpdatedAtNS:       base.UnixNano(),
			AcceptedAtNS:      sql.NullInt64{Int64: base.UnixNano(), Valid: true},
			LeaseOwner:        "", Version: 3,
		},
		{
			ID: "m-retryable", AccountKey: "account-1", ChatID: 45,
			Text:             "retryable payload",
			State:            string(StateFailedRetryable),
			AttemptCount:     2,
			NextAttemptNS:    sql.NullInt64{Int64: lease.UnixNano(), Valid: true},
			LastErrorCode:    420,
			LastErrorMessage: "send error code=420",
			CreatedAtNS:      base.UnixNano(),
			UpdatedAtNS:      base.UnixNano(),
			LeaseOwner:       "", Version: 4,
		},
		{
			ID: "m-permanent", AccountKey: "account-1", ChatID: 46,
			Text:             "permanent payload",
			State:            string(StateFailedPermanent),
			AttemptCount:     3,
			LastErrorCode:    400,
			LastErrorMessage: "send error code=400",
			CreatedAtNS:      base.UnixNano(),
			UpdatedAtNS:      base.UnixNano(),
			LeaseOwner:       "", Version: 5,
		},
		{
			ID: "m-uncertain", AccountKey: "account-2", ChatID: 47,
			Text:             "uncertain payload",
			State:            string(StateUncertain),
			AttemptCount:     1,
			LastErrorMessage: "dispatch lease expired during recovery",
			CreatedAtNS:      base.UnixNano(),
			UpdatedAtNS:      base.UnixNano(),
			LeaseOwner:       "", Version: 2,
		},
		{
			ID: "m-canceled", AccountKey: "account-2", ChatID: 48,
			Text:        "canceled payload",
			State:       string(StateCanceled),
			CreatedAtNS: base.UnixNano(), UpdatedAtNS: base.UnixNano(),
			LeaseOwner: "", Version: 2,
		},
	}
}

// The migration to the schema that carries sent_at_ns must upgrade a
// database main left on this machine, in place, without losing a record
// or changing one.
func TestMigrationFromMainPreservesEveryRecord(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "outbox.db")
	db := openMainDatabase(t, path)
	cipher := &fakeCipher{}

	rows := everyStateOnMain()
	for _, row := range rows {
		writeLegacyRow(t, db, cipher, row)
	}

	if err := applyMigrations(context.Background(), db); err != nil {
		t.Fatalf("applyMigrations: %v", err)
	}

	version, err := readSchemaVersion(context.Background(), db)
	if err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	if version != sqliteSchemaVersion {
		t.Fatalf(
			"schema version = %d, want %d", version, sqliteSchemaVersion,
		)
	}

	for _, want := range rows {
		got := readLegacyRow(t, db, want.ID)
		if got.ID != want.ID ||
			got.AccountKey != want.AccountKey ||
			got.ChatID != want.ChatID ||
			got.State != want.State ||
			got.AttemptCount != want.AttemptCount ||
			got.NextAttemptNS != want.NextAttemptNS ||
			got.TelegramMessageID != want.TelegramMessageID ||
			got.LastErrorCode != want.LastErrorCode ||
			got.LastErrorMessage != want.LastErrorMessage ||
			got.CreatedAtNS != want.CreatedAtNS ||
			got.UpdatedAtNS != want.UpdatedAtNS ||
			got.AcceptedAtNS != want.AcceptedAtNS ||
			got.LeaseOwner != want.LeaseOwner ||
			got.LeaseUntilNS != want.LeaseUntilNS ||
			got.Version != want.Version {
			t.Fatalf(
				"record %q changed by the migration:\n got %+v\nwant %+v",
				want.ID, got, want,
			)
		}

		// The payload is compared by what it decrypts to, not byte for
		// byte: a record that still opens with the user's key and says
		// what it said is the record that survived.
		plaintext, err := cipher.DecryptMessage(
			context.Background(),
			ID(got.ID),
			got.AccountKey,
			got.ChatID,
			got.EncryptedText,
		)
		if err != nil {
			t.Fatalf("record %q no longer decrypts: %v", want.ID, err)
		}
		if string(plaintext) != want.Text {
			t.Fatalf(
				"record %q text = %q, want %q",
				want.ID, plaintext, want.Text,
			)
		}
	}

	// The count is checked apart from the contents: a migration that
	// dropped a record and kept every row it did keep would pass the
	// loop above.
	var count int
	if err := db.QueryRowContext(
		context.Background(),
		`SELECT COUNT(*) FROM outbox_entries`,
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != len(rows) {
		t.Fatalf("rows = %d, want %d", count, len(rows))
	}
}

// A record that was accepted before the migration carries the temporary
// identifier sendMessage returned and the moment TDLib took it. The new
// schema must keep both and leave the sent time empty, so the queue can
// still be waiting for a confirmation it has not had.
func TestMigratedAcceptedEntryStillAwaitsItsSendResult(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "outbox.db")
	db := openMainDatabase(t, path)
	cipher := &fakeCipher{}
	for _, row := range everyStateOnMain() {
		writeLegacyRow(t, db, cipher, row)
	}
	if err := applyMigrations(context.Background(), db); err != nil {
		t.Fatalf("applyMigrations: %v", err)
	}

	store, err := NewSQLiteStore(
		context.Background(),
		SQLiteStoreConfig{Path: path},
		cipher,
	)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	if closer, ok := store.(io.Closer); ok {
		defer func() { _ = closer.Close() }()
	}

	awaiting, err := store.(SendResultStore).ListAwaitingSendResult(
		context.Background(), "account-1", 0,
	)
	if err != nil {
		t.Fatalf("ListAwaitingSendResult: %v", err)
	}
	if len(awaiting) != 1 {
		t.Fatalf("awaiting = %#v, want the one accepted entry", awaiting)
	}
	if awaiting[0].ID != "m-accepted" ||
		awaiting[0].TelegramMessageID != 9001 ||
		awaiting[0].ChatID != 44 {
		t.Fatalf("awaiting = %#v, want the accepted entry as main wrote it", awaiting[0])
	}

	// The confirmation TDLib would have sent later still lands on the
	// record the migration kept.
	confirmed, err := store.(SendResultStore).ApplySendResult(
		context.Background(),
		"account-1",
		SendResult{OldMessageID: 9001, MessageID: 5001},
		time.Unix(1700000600, 0).UTC(),
	)
	if err != nil {
		t.Fatalf("ApplySendResult: %v", err)
	}
	if confirmed.State != StateSent || confirmed.TelegramMessageID != 5001 {
		t.Fatalf("confirmed = %#v, want sent with the final id", confirmed)
	}
	if confirmed.Text != "accepted payload" {
		t.Fatalf("text = %q, want the payload the migration kept", confirmed.Text)
	}
}

// The send-result index has to exist after the migration, because the
// lookup it serves is the one that decides whether a sent message is
// ever recorded.
func TestMigrationCreatesTheSendResultIndex(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "outbox.db")
	db := openMainDatabase(t, path)
	if err := applyMigrations(context.Background(), db); err != nil {
		t.Fatalf("applyMigrations: %v", err)
	}

	indexes := map[string]bool{}
	rows, err := db.QueryContext(context.Background(), `
SELECT name
FROM sqlite_master
WHERE type = 'index' AND tbl_name = 'outbox_entries'
`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		indexes[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{
		"outbox_ready_idx", "outbox_status_idx", "outbox_send_result_idx",
	} {
		if !indexes[name] {
			t.Fatalf(
				"index %q is missing after the migration: %v",
				name, indexes,
			)
		}
	}
}

// A database already at the current version must not be migrated again:
// the ADD COLUMN would fail, and a program that cannot open its own queue
// is a program that cannot send.
func TestMigrationIsANoOpOnACurrentDatabase(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "outbox.db")
	db := openMainDatabase(t, path)
	cipher := &fakeCipher{}
	for _, row := range everyStateOnMain() {
		writeLegacyRow(t, db, cipher, row)
	}

	if err := applyMigrations(context.Background(), db); err != nil {
		t.Fatalf("first applyMigrations: %v", err)
	}
	if err := applyMigrations(context.Background(), db); err != nil {
		t.Fatalf("second applyMigrations: %v", err)
	}

	var count int
	if err := db.QueryRowContext(
		context.Background(),
		`SELECT COUNT(*) FROM outbox_entries`,
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != len(everyStateOnMain()) {
		t.Fatalf("rows = %d, want %d", count, len(everyStateOnMain()))
	}
}

// A database from a future build is refused rather than downgraded: the
// records in it are not this build's to interpret.
func TestMigrationRefusesANewerDatabase(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "outbox.db")
	db := openMainDatabase(t, path)

	if _, err := db.ExecContext(
		context.Background(),
		`UPDATE outbox_meta SET schema_version = ?`,
		sqliteSchemaVersion+1,
	); err != nil {
		t.Fatal(err)
	}

	err := applyMigrations(context.Background(), db)
	if err == nil {
		t.Fatal("applyMigrations() error = nil, want a refusal")
	}
	if !strings.Contains(err.Error(), "newer than supported") ||
		!strings.Contains(err.Error(), fmt.Sprint(sqliteSchemaVersion)) {
		t.Fatalf("error = %v, want a newer-database refusal", err)
	}
}

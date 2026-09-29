package outbox

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// sqliteStore is a durable Store backed by SQLite.
//
// Only the message body is encrypted. Scheduling metadata (state,
// attempts, timestamps, lease, version) is stored as plaintext so the
// dispatcher can operate without the cipher.
//
// PR-08C1 ships sqliteStore together with the PayloadCipher seam and a
// fake cipher. Real cryptographic primitives arrive in PR-08C2, and
// the platform KeyProvider arrives in PR-08C3. Production activation
// is PR-08C4.
type sqliteStore struct {
	db     *sql.DB
	cipher PayloadCipher

	// logger is told about the records a read could not report. It is nil
	// when the store was opened without one, and then the reasons are
	// dropped rather than written to a terminal nobody asked to write to.
	logger *slog.Logger
}

// log is the logger this store reports a read problem through, discarding
// when there is none.
func (s *sqliteStore) log() *slog.Logger {
	if s == nil || s.logger == nil {
		return slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return s.logger
}

// SQLiteStoreConfig tunes the underlying database.
type SQLiteStoreConfig struct {
	// Path is the SQLite file location. Use ":memory:" for tests.
	Path string

	// BusyTimeout is applied as a PRAGMA. Zero selects 5 seconds.
	BusyTimeout time.Duration

	// Logger receives the reasons a read could not report every record.
	//
	// It is not the store's business to refuse a read because one row of
	// it cannot be made sense of: a chat's delivery states are read as a
	// set, and one unreadable record used to take the whole set with it —
	// which is a chat whose messages never change state, with nothing on
	// the screen or in the log to say why. A record it could not report is
	// therefore reported here, by entry identifier and reason, and the
	// read answers with the rest.
	//
	// A nil logger discards. It never falls back to slog.Default(), whose
	// destination is a terminal an interface may own.
	Logger *slog.Logger
}

// NewSQLiteStore opens (or creates) a SQLite-backed Store.
//
// The cipher must be non-nil: even in PR-08C1 the store refuses to
// write plaintext message bodies. Tests inject a fake cipher; PR-08C4
// injects the real one.
func NewSQLiteStore(
	ctx context.Context,
	cfg SQLiteStoreConfig,
	cipher PayloadCipher,
) (Store, error) {
	if cipher == nil {
		return nil, errors.New("outbox: nil payload cipher")
	}
	if cfg.Path == "" {
		return nil, errors.New("outbox: empty sqlite path")
	}

	busy := cfg.BusyTimeout
	if busy <= 0 {
		busy = 5 * time.Second
	}

	dsn := sqliteDSN(cfg.Path, busy)

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("outbox: open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if err := ctx.Err(); err != nil {
		_ = db.Close()
		return nil, err
	}

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("outbox: connect sqlite: %w", err)
	}

	if err := applyMigrations(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	return &sqliteStore{db: db, cipher: cipher, logger: logger}, nil
}

// sqliteDSN builds a DSN for modernc.org/sqlite.
//
// WAL is enabled explicitly so production-like tests can inspect both
// the database and WAL sidecar for accidental plaintext payloads.
func sqliteDSN(
	path string,
	busy time.Duration,
) string {
	separator := "?"
	if strings.Contains(path, "?") {
		separator = "&"
	}

	return path +
		separator +
		"_pragma=foreign_keys(1)" +
		"&_pragma=busy_timeout(" +
		strconv.FormatInt(busy.Milliseconds(), 10) +
		")" +
		"&_pragma=journal_mode(WAL)" +
		// Deleted rows hold encrypted message text; overwrite them
		// instead of leaving the pages in the free list.
		"&_pragma=secure_delete(1)"
}

// Close releases the underlying database handle.
func (s *sqliteStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// bootstrapSchemaMetadata creates outbox_meta when it does not exist
// and guarantees exactly one row with the current schema version.
//
// It is idempotent: reopening an existing database must not modify
// the stored schema version.
func bootstrapSchemaMetadata(
	ctx context.Context,
	db *sql.DB,
) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf(
			"outbox: begin metadata bootstrap: %w", err,
		)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS outbox_meta (
    schema_version INTEGER NOT NULL
);
`); err != nil {
		return fmt.Errorf(
			"outbox: create metadata table: %w", err,
		)
	}

	if _, err := tx.ExecContext(ctx, `
INSERT INTO outbox_meta (schema_version)
SELECT 0
WHERE NOT EXISTS (
    SELECT 1 FROM outbox_meta
);
`); err != nil {
		return fmt.Errorf(
			"outbox: initialize metadata: %w", err,
		)
	}

	var count int
	if err := tx.QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM outbox_meta`,
	).Scan(&count); err != nil {
		return fmt.Errorf(
			"outbox: count metadata rows: %w", err,
		)
	}
	if count != 1 {
		return fmt.Errorf(
			"outbox: metadata row count %d, want 1", count,
		)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf(
			"outbox: commit metadata bootstrap: %w", err,
		)
	}
	return nil
}

// applyMigrations advances the database to sqliteSchemaVersion.
func applyMigrations(
	ctx context.Context,
	db *sql.DB,
) error {
	if err := bootstrapSchemaMetadata(ctx, db); err != nil {
		return err
	}

	current, err := readSchemaVersion(ctx, db)
	if err != nil {
		return err
	}
	if current > sqliteSchemaVersion {
		return fmt.Errorf(
			"outbox: database schema version %d "+
				"is newer than supported %d",
			current, sqliteSchemaVersion,
		)
	}

	for current < sqliteSchemaVersion {
		next := current + 1
		if current >= len(sqliteMigrations) {
			return fmt.Errorf(
				"outbox: missing migration %d -> %d",
				current, next,
			)
		}
		if err := runMigration(
			ctx,
			db,
			sqliteMigrations[current],
			next,
		); err != nil {
			return fmt.Errorf(
				"outbox: migration %d -> %d: %w",
				current, next, err,
			)
		}
		current = next
	}
	return nil
}

// readSchemaVersion reads the single schema version row.
func readSchemaVersion(
	ctx context.Context,
	db *sql.DB,
) (int, error) {
	var version int
	err := db.QueryRowContext(
		ctx,
		`SELECT schema_version FROM outbox_meta`,
	).Scan(&version)
	if err != nil {
		return 0, fmt.Errorf(
			"outbox: read schema version: %w", err,
		)
	}
	if version < 0 {
		return 0, fmt.Errorf(
			"outbox: invalid negative schema version %d",
			version,
		)
	}
	return version, nil
}

// runMigration applies a migration script and writes nextVersion in a
// single transaction, so the schema and its recorded version cannot
// diverge.
func runMigration(
	ctx context.Context,
	db *sql.DB,
	script string,
	nextVersion int,
) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, script); err != nil {
		return err
	}

	result, err := tx.ExecContext(
		ctx,
		`UPDATE outbox_meta SET schema_version = ?`,
		nextVersion,
	)
	if err != nil {
		return fmt.Errorf(
			"write schema version: %w", err,
		)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf(
			"schema version rows affected: %w", err,
		)
	}
	if affected != 1 {
		return fmt.Errorf(
			"schema version rows affected = %d, want 1",
			affected,
		)
	}

	return tx.Commit()
}

// Enqueue implements Store.
func (s *sqliteStore) Enqueue(ctx context.Context, entry Entry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if entry.State != StateQueued {
		return fmt.Errorf(
			"%w: enqueue requires queued state, got %s",
			ErrInvalidEntry, entry.State,
		)
	}
	if err := entry.Validate(); err != nil {
		return err
	}

	encrypted, err := s.cipher.EncryptMessage(
		ctx,
		entry.ID,
		entry.AccountKey,
		entry.ChatID,
		[]byte(entry.Text),
	)
	if err != nil {
		return fmt.Errorf("outbox: encrypt message: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("outbox: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	_, err = tx.ExecContext(ctx, `
INSERT INTO outbox_entries (
    id, account_key, chat_id, encrypted_text, state, attempt_count,
    next_attempt_ns, telegram_message_id, last_error_code,
    last_error_message, created_at_ns, updated_at_ns, accepted_at_ns,
    sent_at_ns, lease_owner, lease_until_ns, version
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`,
		string(entry.ID),
		entry.AccountKey,
		entry.ChatID,
		encrypted,
		string(entry.State),
		entry.AttemptCount,
		nullableTime(entry.NextAttempt),
		nullableInt64(entry.TelegramMessageID),
		entry.LastErrorCode,
		entry.LastErrorMessage,
		entry.CreatedAt.UnixNano(),
		entry.UpdatedAt.UnixNano(),
		nullableTime(entry.AcceptedAt),
		nullableTime(entry.SentAt),
		entry.LeaseOwner,
		nullableTime(entry.LeaseUntil),
		int64(entry.Version),
	)
	if err != nil {
		if isUniqueViolation(err) {
			return ErrDuplicateID
		}
		return fmt.Errorf("outbox: insert entry: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("outbox: commit enqueue: %w", err)
	}
	return nil
}

// Get implements Store.
func (s *sqliteStore) Get(
	ctx context.Context,
	id ID,
) (Entry, error) {
	if err := ctx.Err(); err != nil {
		return Entry{}, err
	}

	row := s.db.QueryRowContext(ctx, `
SELECT id, account_key, chat_id, encrypted_text, state, attempt_count,
       next_attempt_ns, telegram_message_id, last_error_code,
       last_error_message, created_at_ns, updated_at_ns, accepted_at_ns,
       sent_at_ns, lease_owner, lease_until_ns, version
FROM outbox_entries
WHERE id = ?
`, string(id))

	entry, err := s.scanEntry(ctx, row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Entry{}, ErrNotFound
		}
		return Entry{}, err
	}
	return entry, nil
}

// ListAll implements Store.
func (s *sqliteStore) ListAll(ctx context.Context) ([]Entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, account_key, chat_id, encrypted_text, state, attempt_count,
       next_attempt_ns, telegram_message_id, last_error_code,
       last_error_message, created_at_ns, updated_at_ns, accepted_at_ns,
       sent_at_ns, lease_owner, lease_until_ns, version
FROM outbox_entries
ORDER BY created_at_ns ASC, id ASC
`)
	if err != nil {
		return nil, fmt.Errorf("outbox: list entries: %w", err)
	}
	defer rows.Close()

	var out []Entry
	for rows.Next() {
		entry, err := s.scanEntry(ctx, rows)
		if err != nil {
			return nil, err
		}
		out = append(out, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// ListReady implements Store.
func (s *sqliteStore) ListReady(
	ctx context.Context,
	now time.Time,
	limit int,
) ([]Entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		return nil, nil
	}

	rows, err := s.db.QueryContext(ctx, `
SELECT id, account_key, chat_id, encrypted_text, state, attempt_count,
       next_attempt_ns, telegram_message_id, last_error_code,
       last_error_message, created_at_ns, updated_at_ns, accepted_at_ns,
       sent_at_ns, lease_owner, lease_until_ns, version
FROM outbox_entries AS e
WHERE (e.state = 'queued'
       OR (e.state = 'failed_retryable'
           AND e.next_attempt_ns IS NOT NULL
           AND e.next_attempt_ns <= ?))
  -- Only the oldest unfinished entry of a chat is ready, so a later
  -- message can never overtake one that is in flight or waiting for a
  -- retry.
  AND NOT EXISTS (
      SELECT 1
      FROM outbox_entries AS p
      WHERE p.account_key = e.account_key
        AND p.chat_id = e.chat_id
        AND p.state IN ('queued', 'dispatching', 'failed_retryable')
        AND (p.created_at_ns < e.created_at_ns
             OR (p.created_at_ns = e.created_at_ns AND p.id < e.id)))
ORDER BY e.created_at_ns ASC, e.id ASC
LIMIT ?
`, now.UnixNano(), limit)
	if err != nil {
		return nil, fmt.Errorf("outbox: list ready: %w", err)
	}
	defer rows.Close()

	var ready []Entry
	for rows.Next() {
		entry, err := s.scanEntry(ctx, rows)
		if err != nil {
			return nil, err
		}
		ready = append(ready, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// The DB filter already excludes non-ready states; keep IsReadyAt
	// as a safety net in case the schema filter diverges.
	filtered := ready[:0]
	for _, entry := range ready {
		if entry.IsReadyAt(now) {
			filtered = append(filtered, entry)
		}
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		if filtered[i].CreatedAt.Equal(filtered[j].CreatedAt) {
			return filtered[i].ID < filtered[j].ID
		}
		return filtered[i].CreatedAt.Before(filtered[j].CreatedAt)
	})
	return filtered, nil
}

// Claim implements Store.
func (s *sqliteStore) Claim(
	ctx context.Context,
	id ID,
	expectedVersion uint64,
	owner string,
	leaseUntil time.Time,
	now time.Time,
) (Entry, error) {
	if err := ctx.Err(); err != nil {
		return Entry{}, err
	}
	if strings.TrimSpace(owner) == "" {
		return Entry{}, fmt.Errorf(
			"%w: empty lease owner", ErrInvalidEntry,
		)
	}
	if !leaseUntil.After(now) {
		return Entry{}, fmt.Errorf(
			"%w: lease must expire after claim time", ErrInvalidEntry,
		)
	}

	return s.mutate(ctx, id, expectedVersion, func(entry Entry) (Entry, error) {
		if entry.State == StateDispatching &&
			entry.LeaseOwner != "" &&
			entry.LeaseOwner != owner &&
			entry.LeaseUntil.After(now) {
			return Entry{}, ErrLeaseHeld
		}
		return entry.Claim(owner, leaseUntil, now)
	})
}

// MarkAccepted implements Store.
func (s *sqliteStore) MarkAccepted(
	ctx context.Context,
	id ID,
	expectedVersion uint64,
	messageID int64,
	now time.Time,
) (Entry, error) {
	return s.mutate(ctx, id, expectedVersion, func(entry Entry) (Entry, error) {
		return entry.Accept(messageID, now)
	})
}

// MarkSent implements Store.
func (s *sqliteStore) MarkSent(
	ctx context.Context,
	id ID,
	expectedVersion uint64,
	messageID int64,
	now time.Time,
) (Entry, error) {
	if err := ctx.Err(); err != nil {
		return Entry{}, err
	}
	return s.mutate(ctx, id, expectedVersion, func(entry Entry) (Entry, error) {
		return entry.Sent(messageID, now)
	})
}

// MarkSendFailed implements Store.
func (s *sqliteStore) MarkSendFailed(
	ctx context.Context,
	id ID,
	expectedVersion uint64,
	code int,
	message string,
	now time.Time,
) (Entry, error) {
	if err := ctx.Err(); err != nil {
		return Entry{}, err
	}
	return s.mutate(ctx, id, expectedVersion, func(entry Entry) (Entry, error) {
		return entry.MarkSendFailed(code, message, now)
	})
}

// MarkRetryable implements Store.
func (s *sqliteStore) MarkRetryable(
	ctx context.Context,
	id ID,
	expectedVersion uint64,
	nextAttempt time.Time,
	code int,
	message string,
	now time.Time,
) (Entry, error) {
	return s.mutate(ctx, id, expectedVersion, func(entry Entry) (Entry, error) {
		return entry.MarkRetryable(nextAttempt, code, message, now)
	})
}

// MarkPermanentFailure implements Store.
func (s *sqliteStore) MarkPermanentFailure(
	ctx context.Context,
	id ID,
	expectedVersion uint64,
	code int,
	message string,
	now time.Time,
) (Entry, error) {
	return s.mutate(ctx, id, expectedVersion, func(entry Entry) (Entry, error) {
		return entry.MarkPermanentFailure(code, message, now)
	})
}

// MarkUncertain implements Store.
func (s *sqliteStore) MarkUncertain(
	ctx context.Context,
	id ID,
	expectedVersion uint64,
	reason string,
	now time.Time,
) (Entry, error) {
	return s.mutate(ctx, id, expectedVersion, func(entry Entry) (Entry, error) {
		return entry.MarkUncertain(reason, now)
	})
}

// Cancel implements Store.
func (s *sqliteStore) Cancel(
	ctx context.Context,
	id ID,
	expectedVersion uint64,
	now time.Time,
) (Entry, error) {
	return s.mutate(ctx, id, expectedVersion, func(entry Entry) (Entry, error) {
		return entry.Cancel(now)
	})
}

// PurgeFinished implements Store.
//
// Accepted is not purged: TDLib took the message and Telegram has not
// answered yet, and a record whose outcome is unknown is still a record
// somebody may come back to. It is left to Sent or to a send failure,
// and an accepted entry older than the retention window is a defect
// worth seeing rather than a thing to tidy away.
func (s *sqliteStore) PurgeFinished(
	ctx context.Context,
	cutoff time.Time,
) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	result, err := s.db.ExecContext(ctx, `
DELETE FROM outbox_entries
WHERE state IN ('sent', 'canceled')
  AND updated_at_ns < ?
`, cutoff.UnixNano())
	if err != nil {
		return 0, fmt.Errorf("outbox: purge finished: %w", err)
	}
	purged, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("outbox: purge finished: %w", err)
	}
	return int(purged), nil
}

// RecoverInterrupted implements Store.
func (s *sqliteStore) RecoverInterrupted(
	ctx context.Context,
	now time.Time,
) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("outbox: begin recover tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.QueryContext(ctx, `
SELECT id, account_key, chat_id, encrypted_text, state, attempt_count,
       next_attempt_ns, telegram_message_id, last_error_code,
       last_error_message, created_at_ns, updated_at_ns, accepted_at_ns,
       sent_at_ns, lease_owner, lease_until_ns, version
FROM outbox_entries
WHERE state = 'dispatching'
  AND lease_until_ns IS NOT NULL
  AND lease_until_ns <= ?
`, now.UnixNano())
	if err != nil {
		return 0, fmt.Errorf("outbox: recover select: %w", err)
	}

	var stale []Entry
	for rows.Next() {
		entry, err := s.scanEntry(ctx, rows)
		if err != nil {
			rows.Close()
			return 0, err
		}
		stale = append(stale, entry)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()

	recovered := 0
	for _, entry := range stale {
		out, err := entry.MarkUncertain(
			"dispatch lease expired during recovery",
			now,
		)
		if err != nil {
			return recovered, err
		}
		if err := s.updateTx(ctx, tx, entry.Version, out); err != nil {
			return recovered, err
		}
		recovered++
	}

	if err := tx.Commit(); err != nil {
		return recovered, fmt.Errorf("outbox: commit recover: %w", err)
	}
	return recovered, nil
}

// mutate reads the entry, applies fn, and writes it back within a
// single transaction, enforcing expectedVersion.
func (s *sqliteStore) mutate(
	ctx context.Context,
	id ID,
	expectedVersion uint64,
	fn func(Entry) (Entry, error),
) (Entry, error) {
	if err := ctx.Err(); err != nil {
		return Entry{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Entry{}, fmt.Errorf("outbox: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	row := tx.QueryRowContext(ctx, `
SELECT id, account_key, chat_id, encrypted_text, state, attempt_count,
       next_attempt_ns, telegram_message_id, last_error_code,
       last_error_message, created_at_ns, updated_at_ns, accepted_at_ns,
       sent_at_ns, lease_owner, lease_until_ns, version
FROM outbox_entries
WHERE id = ?
`, string(id))

	entry, err := s.scanEntry(ctx, row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Entry{}, ErrNotFound
		}
		return Entry{}, err
	}
	if entry.Version != expectedVersion {
		return Entry{}, ErrVersionConflict
	}

	out, err := fn(entry)
	if err != nil {
		return Entry{}, err
	}

	if err := s.updateTx(ctx, tx, expectedVersion, out); err != nil {
		return Entry{}, err
	}

	if err := tx.Commit(); err != nil {
		return Entry{}, fmt.Errorf("outbox: commit mutation: %w", err)
	}
	return out, nil
}

// updateTx persists out and verifies the version guard inside the
// transaction.
func (s *sqliteStore) updateTx(
	ctx context.Context,
	tx *sql.Tx,
	expectedVersion uint64,
	out Entry,
) error {
	encrypted, err := s.cipher.EncryptMessage(
		ctx,
		out.ID,
		out.AccountKey,
		out.ChatID,
		[]byte(out.Text),
	)
	if err != nil {
		return fmt.Errorf("outbox: encrypt message: %w", err)
	}

	res, err := tx.ExecContext(ctx, `
UPDATE outbox_entries
SET account_key = ?,
    chat_id = ?,
    encrypted_text = ?,
    state = ?,
    attempt_count = ?,
    next_attempt_ns = ?,
    telegram_message_id = ?,
    last_error_code = ?,
    last_error_message = ?,
    updated_at_ns = ?,
    accepted_at_ns = ?,
    sent_at_ns = ?,
    lease_owner = ?,
    lease_until_ns = ?,
    version = ?
WHERE id = ? AND version = ?
`,
		out.AccountKey,
		out.ChatID,
		encrypted,
		string(out.State),
		out.AttemptCount,
		nullableTime(out.NextAttempt),
		nullableInt64(out.TelegramMessageID),
		out.LastErrorCode,
		out.LastErrorMessage,
		out.UpdatedAt.UnixNano(),
		nullableTime(out.AcceptedAt),
		nullableTime(out.SentAt),
		out.LeaseOwner,
		nullableTime(out.LeaseUntil),
		int64(out.Version),
		string(out.ID),
		int64(expectedVersion),
	)
	if err != nil {
		return fmt.Errorf("outbox: update entry: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrVersionConflict
	}
	return nil
}

// rowScanner abstracts *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanEntry decodes a database row and validates the resulting Entry.
//
// It rejects negative AttemptCount and Version before converting them
// to unsigned or platform-sized types. LastErrorCode is intentionally
// not validated: transport-specific error codes may be negative.
// State and all other fields are checked by Entry.Validate.
func (s *sqliteStore) scanEntry(
	ctx context.Context,
	row rowScanner,
) (Entry, error) {
	var (
		idRaw             string
		accountKey        string
		chatID            int64
		encrypted         []byte
		stateRaw          string
		attemptCount      int64
		nextAttemptNS     sql.NullInt64
		telegramMessageID sql.NullInt64
		lastErrorCode     int64
		lastErrorMessage  string
		createdAtNS       int64
		updatedAtNS       int64
		acceptedAtNS      sql.NullInt64
		sentAtNS          sql.NullInt64
		leaseOwner        string
		leaseUntilNS      sql.NullInt64
		version           int64
	)

	if err := row.Scan(
		&idRaw,
		&accountKey,
		&chatID,
		&encrypted,
		&stateRaw,
		&attemptCount,
		&nextAttemptNS,
		&telegramMessageID,
		&lastErrorCode,
		&lastErrorMessage,
		&createdAtNS,
		&updatedAtNS,
		&acceptedAtNS,
		&sentAtNS,
		&leaseOwner,
		&leaseUntilNS,
		&version,
	); err != nil {
		return Entry{}, err
	}

	if attemptCount < 0 {
		return Entry{}, fmt.Errorf(
			"%w: persisted entry %s has negative attempt count",
			ErrInvalidEntry, idRaw,
		)
	}
	if version < 0 {
		return Entry{}, fmt.Errorf(
			"%w: persisted entry %s has negative version",
			ErrInvalidEntry, idRaw,
		)
	}

	plaintext, err := s.cipher.DecryptMessage(
		ctx,
		ID(idRaw),
		accountKey,
		chatID,
		encrypted,
	)
	if err != nil {
		return Entry{}, fmt.Errorf(
			"outbox: decrypt message %s: %w", idRaw, err,
		)
	}

	entry := Entry{
		ID:                ID(idRaw),
		AccountKey:        accountKey,
		ChatID:            chatID,
		Text:              string(plaintext),
		State:             State(stateRaw),
		AttemptCount:      int(attemptCount),
		NextAttempt:       timeFromNull(nextAttemptNS),
		TelegramMessageID: telegramMessageID.Int64,
		LastErrorCode:     int(lastErrorCode),
		LastErrorMessage:  lastErrorMessage,
		CreatedAt:         time.Unix(0, createdAtNS).UTC(),
		UpdatedAt:         time.Unix(0, updatedAtNS).UTC(),
		AcceptedAt:        timeFromNull(acceptedAtNS),
		SentAt:            timeFromNull(sentAtNS),
		LeaseOwner:        leaseOwner,
		LeaseUntil:        timeFromNull(leaseUntilNS),
		Version:           uint64(version),
	}

	if err := entry.Validate(); err != nil {
		return Entry{}, fmt.Errorf(
			"outbox: invalid persisted entry %s: %w", idRaw, err,
		)
	}

	return entry, nil
}

func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UnixNano()
}

func nullableInt64(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

func timeFromNull(n sql.NullInt64) time.Time {
	if !n.Valid {
		return time.Time{}
	}
	return time.Unix(0, n.Int64).UTC()
}

// isUniqueViolation reports whether err is a SQLite UNIQUE constraint
// violation. modernc/sqlite exposes the SQLite message text; matching
// on it is intentionally narrow.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed") ||
		strings.Contains(msg, "constraint failed: UNIQUE")
}

// Compile-time assertion.
var _ Store = (*sqliteStore)(nil)

package outbox

// sqliteSchemaVersion is the schema revision expected by sqliteStore.
// Bump this and append a migration when the schema changes.
const sqliteSchemaVersion = 2

// sqliteMigrations[i] brings the database from version i to i+1.
//
// The outbox_meta table is created separately by
// bootstrapSchemaMetadata. Migrations must not create or touch it:
// doing so would double-create the table on a fresh database and
// break the open path.
//
// Each migration script is executed inside a single transaction
// together with the schema_version update, so a crash cannot leave
// the database with an applied script but an old version.
var sqliteMigrations = []string{
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

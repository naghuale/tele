# ADR-0002: Durable outbox storage and at-rest privacy

## Status

Accepted.

Option B is selected with application-level encryption of
user-controlled payload fields.

SQLite stores transactional and scheduling metadata. Message text and
future user-controlled payload fields are encrypted before they are
passed to SQLite.

The data-encryption key is stored outside the database:

- macOS: Keychain Services;
- Linux desktop: Secret Service.

Plaintext fallback is not allowed. If the platform key provider is
unavailable, locked, or unsupported, production outbox activation
fails closed.

> **Amended 2026-09-27 by docs/TUI_SPEC.md, decision 1.** There is no
> direct-send fallback. `durable` is the only production send mode.
> When the outbox cannot be opened, the TUI starts with sending paused
> and refuses to queue or send the message, keeping the draft. Startup
> does not fail because of it. The screen shows the texts from decision 3
> (`Sending paused`, the explanation, the reason hint and the
> `Details:` link); the full cause goes to the log and to
> `telecli doctor` only. The cause is matched to one of the five cases by
> sentinel error with `errors.Is`, never by message text.
>
> A configuration that still says `mode = "direct"` is read as a
> retired value: it starts on durable and `telecli doctor` reports it
> every run. `telecli configure` no longer offers a mode and refuses
> `--mode direct` with an explanation.

Headless Linux remains unsupported for production outbox persistence
until a separate explicit key-source policy is accepted.

## Context

PR-08 introduces a durable outbox for outgoing Telegram messages. Its
purpose is to prevent message loss when the process exits between the
user pressing Enter and TDLib accepting the request.

A durable outbox necessarily stores message text on disk until the
message reaches a terminal state. This is a new category of local data:

- Previous PRs stored no user-generated content outside TDLib's own
  database.
- TDLib's database lives under ~/.local/share/telecli/tdlib/ and is
  subject to its own storage policy.
- The outbox would hold plaintext copies of unsent messages.

The domain model, Store contract, dispatcher, and application seams
introduced by PR-08A/B/D/E are transport-neutral and backend-agnostic.
Selecting a durable backend now is the next architectural step; the
alternative (deferring the decision further) no longer reduces risk,
it only keeps PR-08C and PR-08H blocked.

## Decision

Use SQLite as the transactional durable outbox store and encrypt
user-controlled payload fields at the application layer.

The first encrypted field is message text. Scheduling metadata remains
available to the Store without decrypting message content.

Each outbox database uses a randomly generated data-encryption key. The
key is stored outside the database through a platform KeyProvider:

- Keychain Services on macOS;
- Secret Service on Linux desktop.

The database must never contain the data-encryption key. The key must
not be supplied through command-line arguments, written to logs, or
stored in repository configuration.

A new key is never created silently for an existing database. A key is
created only while a database identity is being initialized, or by
`telecli outbox reset`, which exists for the one case where the key of an
existing database is provably absent (the key store answered that there
is no record for it). That command:

- requires the explicit word `reset` as confirmation, or `--yes` for an
  unattended run, and does nothing without either;
- tells the user what the old queue holds first, read from the plaintext
  scheduling metadata alone, and never reads a message body, a chat id
  or a phone number;
- keeps the old database as `outbox.db.orphaned-<UTC time>` next to
  itself, with a note naming its `database_id` and how to put it back if
  the old key is ever found, so the old queue is never deleted and never
  silently replaced;
- gives the new queue a new `database_id`, recorded in the configuration
  file, and never reuses the old one: a key restored from a backup for
  the old identity must not end up on the new, empty queue.

A key that exists but is unusable, a locked key store, a denied access
request and a platform without secure key storage are all cases where the
queue may still be readable, so the command refuses them and changes
nothing. The same is true of a queue another telecli process has open.

Production activation fails closed when the key provider is
unavailable. There is no automatic plaintext fallback.

SQLCipher remains a possible future backend if full-database
encryption becomes a requirement, but it is not required by this ADR.

## Rationale

### Why Option A (plaintext with 0700) was rejected

Option A does not satisfy the privacy boundary already established by
the outbox design:

> Message text must not silently become plaintext-at-rest.

0700 permissions protect against other unprivileged users in normal
operation, but they do not protect the payload in filesystem backups,
VM snapshots, or direct reads of the file by a process with the user's
own privileges.

### Why Option C (no durable backend) was rejected

Option C was the correct temporary block while the model, Store
contract, dispatcher, and error-privacy rules were being established.
That work is now complete. Continuing Option C no longer reduces
architectural risk; it only keeps PR-08C and PR-08H blocked without
adding safety.

### Why application-level encryption is preferable to a first SQLCipher backend

The current model only needs the user-controlled payload encrypted.
Scheduling fields must be readable without decryption:

    State
    NextAttempt
    LeaseUntil
    Version

This yields clear boundaries:

- the Store contract is unchanged;
- the SQL schema stays standard SQLite;
- pure-Go package tests remain possible with a fake cipher;
- the key is never stored beside the database;
- message text is absent from plaintext database pages;
- the headless policy can be enforced explicitly rather than by
  accident.

SQLCipher would encrypt the whole database and add a separate native
dependency, its own key/rekey/migration APIs, and its own failure
modes. That cost is not justified for fields that the ADR already
treats as scheduling metadata.

## Data model

### Encrypted payload fields

- `Text` (message body)

Future user-controlled payload fields must be added to this list.

### Plaintext scheduling metadata

- `ID`
- `AccountKey`
- `ChatID`
- `State`
- `AttemptCount`
- `NextAttempt`
- `TelegramMessageID`
- error `Code`
- timestamps (`CreatedAt`, `UpdatedAt`, `AcceptedAt`)
- lease fields (`LeaseOwner`, `LeaseUntil`)
- `Version`

`AccountKey` is an opaque application-chosen string, not a credential.
It scopes entries to one authenticated account but does not contain
authentication material.

### Forbidden content anywhere in the outbox

- API ID and API hash
- phone number
- 2FA password
- Telegram authentication code
- session database encryption keys
- any credential or session material
- message text in plaintext form (columns, WAL, journal, temporary
  files, logs)

## Cryptographic envelope for PR-08C

Proposed binary envelope, produced by the PayloadCipher:

    version       1 byte
    algorithm     1 byte
    nonce length  1 byte
    nonce         variable
    ciphertext    variable, includes authentication tag

Associated data binds the ciphertext to the outbox entry:

- schema version
- entry ID
- account key
- chat ID

This prevents silent relocation of an encrypted payload to a different
outbox entry.

Interface:

```go
type PayloadCipher interface {
	EncryptMessage(
		ctx context.Context,
		entryID outbox.ID,
		accountKey string,
		chatID int64,
		plaintext []byte,
	) ([]byte, error)

	DecryptMessage(
		ctx context.Context,
		entryID outbox.ID,
		accountKey string,
		chatID int64,
		ciphertext []byte,
	) ([]byte, error)
}
### Linux key initialization serialization

The Secret Service API does not expose an atomic create-if-absent
operation for matching attributes. telecli therefore serializes key
initialization across cooperating telecli processes on the same
machine with a per-database advisory file lock.

While holding the lock, telecli performs `SearchItems` followed by
`CreateItem` with `replace=false`. This guarantees that cooperating
telecli processes using the same lock path do not overwrite or
independently create keys for the same database identity.

The lock does not synchronize unrelated applications that bypass the
telecli locking convention. The Secret Service attributes include the
telecli-specific service value `telecli-outbox` to avoid accidental
collisions.

Lock files are stored in one of:

- `$XDG_RUNTIME_DIR/telecli-outbox-locks/`;
- `$TMPDIR/telecli-outbox-locks-<uid>/`;
- `/tmp/telecli-outbox-locks-<uid>/`.

The lock directory must be owned by the current user and must not
grant write access to group or other users. Each lock file is named
from `sha256(databaseID)` and is created with mode `0600`.

Cross-machine key sharing and synchronization are out of scope.

### Queue reset serialization and ordering

`telecli outbox reset` is the only operation that rearranges a queue on
disk, so it is serialized with the same convention: an exclusive
advisory lock named from the queue's data directory, in the same lock
directory as the key locks. A session holds the queue through the lock
for as long as it is open; the lock is opt-in per caller, so a read-only
probe such as `telecli doctor` keeps working while a session runs.

The steps run in one order only: record, move the database aside
together with its write-ahead log, create the key of the new identity,
write the new identity to the configuration file, clear the record. The
record (`outbox.reset` in the data directory) is written before the
first destructive step and removed after the last, so a run that is
interrupted at any point is finished by running the same command again,
and never leaves a configuration that names an identity without its key
or without the database it replaced.

While the record exists, `Open` refuses to create a key
(`ErrOutboxResetPending`). Without that, a start between the two halves
of a reset would create a fresh key for the old identity, and the user
would see a working empty queue instead of the reset they were told to
run.

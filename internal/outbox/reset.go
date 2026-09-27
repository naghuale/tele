package outbox

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var (
	// ErrOutboxResetPending is returned when a guided reset of the queue
	// was started but has not finished, and the queue would otherwise be
	// created from scratch.
	//
	// It stops the fail-closed path from quietly undoing an interrupted
	// reset: without it, a start between the two halves of the reset
	// would create a fresh key for the old identity and hide the fact
	// that the old queue was set aside.
	ErrOutboxResetPending = errors.New(
		"outbox: queue reset is pending",
	)

	// ErrOutboxQueueOrphan is returned when the old queue database could
	// not be moved aside.
	ErrOutboxQueueOrphan = errors.New(
		"outbox: orphan the queue database",
	)

	// ErrOutboxQueueAbsent is returned when there is no queue database
	// to inspect.
	ErrOutboxQueueAbsent = errors.New(
		"outbox: queue database is absent",
	)

	// ErrOutboxResetJournal is returned when the on-disk record of an
	// interrupted reset is missing, unreadable or of an unknown version.
	ErrOutboxResetJournal = errors.New(
		"outbox: reset journal",
	)
)

// KeyCondition is what the key provider knows about the key of one
// queue identity.
//
// The conditions exist because "the key is gone" and "the key cannot be
// read right now" lead to opposite actions: the first is recoverable by
// starting a new queue, the second is not. They are told apart by
// sentinel error, never by message text.
type KeyCondition uint8

const (
	// KeyConditionPresent is a usable key of the expected length.
	KeyConditionPresent KeyCondition = iota

	// KeyConditionAbsent is the provider's answer that no record exists.
	//
	// It is the only condition that makes a lost key certain. Everything
	// else must leave the queue alone.
	KeyConditionAbsent

	// KeyConditionMalformed is a key that exists but is unusable, for
	// example of the wrong length.
	//
	// The key is still in the key store, so a restored backup may fix
	// it, and starting a new queue would throw away a queue that is
	// still readable.
	KeyConditionMalformed

	// KeyConditionRefused is a key store that holds the key and will not
	// hand it over: a locked keychain or a denied access request.
	KeyConditionRefused

	// KeyConditionUnsupported is a platform without secure key storage.
	KeyConditionUnsupported

	// KeyConditionFailed is any other provider failure.
	KeyConditionFailed
)

// String names the condition for logs and telecli doctor.
func (c KeyCondition) String() string {
	switch c {
	case KeyConditionPresent:
		return "key present"
	case KeyConditionAbsent:
		return "key absent"
	case KeyConditionMalformed:
		return "key malformed"
	case KeyConditionRefused:
		return "key access denied"
	case KeyConditionUnsupported:
		return "no secure key storage"
	case KeyConditionFailed:
		return "key lookup failed"
	default:
		return "unknown key condition"
	}
}

// InspectKey loads the key of databaseID and classifies the answer.
//
// It never creates a key and never falls back: the whole point is to
// know what the key store says before anything is changed. A nil
// provider, an empty identity or a cancelled context is reported as
// KeyConditionFailed with the cause.
//
// The returned error is the provider cause and is nil only for
// KeyConditionPresent and KeyConditionAbsent. Absence is not a failure:
// it is the answer a caller acts on.
func InspectKey(
	ctx context.Context,
	provider KeyProvider,
	databaseID string,
) (KeyCondition, error) {
	if err := ctx.Err(); err != nil {
		return KeyConditionFailed, err
	}
	if provider == nil {
		return KeyConditionFailed, fmt.Errorf(
			"%w: nil key provider",
			ErrOutboxInvalidConfig,
		)
	}
	if err := validateDatabaseID(databaseID); err != nil {
		return KeyConditionFailed, err
	}

	key, err := provider.LoadKey(ctx, databaseID)
	if err == nil {
		_, validationErr := validateLoadedKey(key)
		clearBytes(key)

		if validationErr != nil {
			return KeyConditionMalformed, validationErr
		}

		return KeyConditionPresent, nil
	}

	switch {
	case errors.Is(err, ErrOutboxKeyUnavailable):
		return KeyConditionAbsent, nil

	case errors.Is(err, ErrOutboxKeyMalformed):
		return KeyConditionMalformed, err

	case errors.Is(err, ErrOutboxKeyAccessDenied):
		return KeyConditionRefused, err

	case errors.Is(err, ErrOutboxKeyProviderUnsupported):
		return KeyConditionUnsupported, err

	default:
		return KeyConditionFailed, err
	}
}

// QueueCounts is the number of entries in the queue per state.
//
// A state that is not one of the known domain states appears under its
// stored value, so a damaged queue is reported rather than hidden.
type QueueCounts map[State]int

// QueueSummary is what a queue holds, learned without its key.
//
// Only scheduling metadata is counted. Message text is never read,
// because reading it needs the key that is gone; the counts are
// therefore an honest description of the queue even though its
// contents cannot be shown.
type QueueSummary struct {
	// Readable reports whether the counts could be read. A false value
	// means the database is absent, empty or damaged; Counts is then
	// empty and Cause explains why.
	Readable bool

	Counts QueueCounts

	// Total is every entry in the queue, including entries in a state
	// this build does not know.
	Total int

	// Unsent counts the entries that never reached Telegram: everything
	// except accepted and canceled. These are the messages a user loses
	// by starting a new queue.
	Unsent int

	// OldestUnsent is the creation time of the oldest unsent entry. It
	// is zero when nothing is unsent.
	OldestUnsent time.Time

	// Cause is the inspection failure when Readable is false.
	Cause error
}

// InspectQueue counts the entries in the queue in dataDir.
//
// The database and its sidecars are copied into a scratch directory and
// the copy is read, so the queue itself is never opened and never
// modified: the summary a user sees before confirming a reset cannot
// change a single byte of the queue it describes, not even by creating
// the sidecar files SQLite would otherwise write beside it.
//
// A queue that cannot be counted is reported as unreadable with its
// cause instead of as empty. Guessing "no messages" there would
// understate the loss, which is the one thing this summary must not do.
func InspectQueue(
	ctx context.Context,
	dataDir string,
) (QueueSummary, error) {
	if err := ctx.Err(); err != nil {
		return QueueSummary{}, err
	}
	if strings.TrimSpace(dataDir) == "" {
		return QueueSummary{}, fmt.Errorf(
			"%w: empty data dir",
			ErrOutboxInvalidConfig,
		)
	}

	scratch, err := os.MkdirTemp("", "telecli-outbox-inspect-")
	if err != nil {
		return QueueSummary{}, fmt.Errorf(
			"outbox: inspect queue: %w",
			err,
		)
	}
	defer func() { _ = os.RemoveAll(scratch) }()

	source := filepath.Join(dataDir, outboxDatabaseFileName)

	// The write-ahead log holds committed pages that are not in the main
	// database file yet. A queue whose process was killed can have data
	// only there, so it is copied too; the index beside it is a
	// rebuildable cache and is skipped.
	for _, suffix := range []string{"", "-wal"} {
		err := copyFileIfPresent(
			source+suffix,
			filepath.Join(
				scratch,
				outboxDatabaseFileName+suffix,
			),
		)
		if err != nil {
			return QueueSummary{}, err
		}
	}

	if !regularFileExists(source) {
		return QueueSummary{
			Readable: false,
			Cause:    ErrOutboxQueueAbsent,
		}, nil
	}

	return readQueueSummary(ctx, filepath.Join(
		scratch,
		outboxDatabaseFileName,
	))
}

// readQueueSummary counts the entries of a scratch copy read-only.
func readQueueSummary(
	ctx context.Context,
	path string,
) (QueueSummary, error) {
	db, err := sql.Open("sqlite", sqliteReadOnlyDSN(path))
	if err != nil {
		return unreadableSummary(err), nil
	}
	defer func() { _ = db.Close() }()

	summary, err := scanQueueSummary(ctx, db)
	if err != nil {
		return unreadableSummary(err), nil
	}

	return summary, nil
}

// sqliteReadOnlyDSN builds a read-only DSN for path.
//
// The path is percent-encoded because the driver passes a "file:" DSN
// to SQLite as a URI, where "?" and "#" would otherwise be read as the
// start of a query string or a fragment. mode=ro keeps the inspection
// copy out of the way of writes even though the scratch directory holds
// nothing else.
func sqliteReadOnlyDSN(path string) string {
	dsn := url.URL{
		Scheme:   "file",
		Path:     path,
		RawQuery: "mode=ro",
	}

	return dsn.String()
}

// scanQueueSummary runs the two counting queries.
//
// The oldest unsent time is asked for separately because it is the one
// fact a user acts on, and a queue with many entries must not have it
// lost to a second query failing.
func scanQueueSummary(
	ctx context.Context,
	db *sql.DB,
) (QueueSummary, error) {
	rows, err := db.QueryContext(ctx, `
SELECT state, COUNT(*)
FROM outbox_entries
GROUP BY state
`)
	if err != nil {
		return QueueSummary{}, fmt.Errorf(
			"outbox: count queue entries: %w",
			err,
		)
	}
	defer func() { _ = rows.Close() }()

	counts := QueueCounts{}
	total := 0
	unsent := 0

	for rows.Next() {
		var (
			stateRaw string
			count    int
		)

		if err := rows.Scan(&stateRaw, &count); err != nil {
			return QueueSummary{}, fmt.Errorf(
				"outbox: scan queue entry count: %w",
				err,
			)
		}

		state := State(stateRaw)
		counts[state] += count
		total += count

		if stateSent(state) {
			continue
		}
		unsent += count
	}
	if err := rows.Err(); err != nil {
		return QueueSummary{}, fmt.Errorf(
			"outbox: read queue entry counts: %w",
			err,
		)
	}

	oldest, err := readOldestUnsent(ctx, db)
	if err != nil {
		return QueueSummary{}, err
	}

	return QueueSummary{
		Readable:     true,
		Counts:       counts,
		Total:        total,
		Unsent:       unsent,
		OldestUnsent: oldest,
	}, nil
}

// readOldestUnsent returns the creation time of the oldest entry that
// never reached Telegram.
func readOldestUnsent(
	ctx context.Context,
	db *sql.DB,
) (time.Time, error) {
	var createdAtNS sql.NullInt64

	err := db.QueryRowContext(ctx, `
SELECT MIN(created_at_ns)
FROM outbox_entries
WHERE state <> ? AND state <> ?
`, StateAccepted, StateCanceled).Scan(&createdAtNS)
	if err != nil {
		return time.Time{}, fmt.Errorf(
			"outbox: read oldest unsent entry: %w",
			err,
		)
	}

	if !createdAtNS.Valid {
		return time.Time{}, nil
	}

	return time.Unix(0, createdAtNS.Int64).UTC(), nil
}

// stateSent reports whether an entry in this state did reach Telegram.
//
// Anything that is not accepted and not canceled counts as unsent: a
// permanently failed or uncertain message is exactly as lost to the
// user as one that never left the queue.
func stateSent(state State) bool {
	return state == StateAccepted || state == StateCanceled
}

// unreadableSummary wraps a failure to read a queue.
func unreadableSummary(err error) QueueSummary {
	return QueueSummary{Readable: false, Cause: err}
}

// copyFileIfPresent copies src to dst when src is a regular file.
//
// A missing or unusable source is not an error: the caller inspects
// the copy and reports an absent or damaged queue from what it finds
// there.
func copyFileIfPresent(src, dst string) error {
	info, err := os.Lstat(src)

	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil
	case err != nil:
		return fmt.Errorf(
			"outbox: inspect %s: %w",
			src,
			err,
		)
	case !info.Mode().IsRegular():
		return nil
	}

	contents, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("outbox: read %s: %w", src, err)
	}

	if err := os.WriteFile(dst, contents, 0o600); err != nil {
		return fmt.Errorf("outbox: write %s: %w", dst, err)
	}

	return nil
}

// resetJournalName is the file that records a reset in progress.
const resetJournalName = "outbox.reset"

// resetJournalVersion is the journal layout this build writes.
//
// A journal of any other version is refused rather than interpreted: a
// half-understood record of a destructive operation is worse than none.
const resetJournalVersion = 1

// ResetJournal is the on-disk record of a reset that has not finished.
//
// It is what makes the command safe to interrupt. Everything the later
// steps need is written down before the first destructive step, so a
// repeated run finishes the same reset instead of starting a new one,
// and a reader can always tell which queue the configuration points at
// and where the old queue was put.
type ResetJournal struct {
	Version int `json:"version"`

	// OldDatabaseID is the identity the queue had before the reset. It
	// is never used again: a key restored from a backup for it would
	// then be loaded for the new, empty queue.
	OldDatabaseID string `json:"old_database_id"`

	// NewDatabaseID is the identity the new queue gets.
	NewDatabaseID string `json:"new_database_id"`

	// BackupName and NoteName are file names inside the data
	// directory, not paths: the journal must not be able to name a
	// location outside it.
	BackupName string `json:"backup_name"`
	NoteName   string `json:"note_name"`

	// StartedAt is when the reset was confirmed, in UTC.
	StartedAt time.Time `json:"started_at"`
}

// ResetJournalPath is where the journal of an in-flight reset lives.
func ResetJournalPath(dataDir string) string {
	return filepath.Join(dataDir, resetJournalName)
}

// PendingReset reports whether dataDir holds a journal of a reset that
// has not finished.
//
// The queue open path uses it to refuse creating a key while a reset is
// in flight.
func PendingReset(dataDir string) bool {
	_, err := os.Lstat(ResetJournalPath(dataDir))
	return err == nil
}

// ReadResetJournal returns the journal of an in-flight reset.
//
// A missing journal is reported as a nil journal and no error: there is
// simply nothing to resume.
func ReadResetJournal(dataDir string) (*ResetJournal, error) {
	contents, err := os.ReadFile(ResetJournalPath(dataDir))

	switch {
	case err == nil:
		// Continue below.

	case errors.Is(err, os.ErrNotExist):
		return nil, nil

	default:
		return nil, fmt.Errorf(
			"%w: read %s: %v",
			ErrOutboxResetJournal,
			ResetJournalPath(dataDir),
			err,
		)
	}

	var journal ResetJournal
	if err := json.Unmarshal(contents, &journal); err != nil {
		return nil, fmt.Errorf(
			"%w: parse %s: %v",
			ErrOutboxResetJournal,
			ResetJournalPath(dataDir),
			err,
		)
	}

	if journal.Version != resetJournalVersion {
		return nil, fmt.Errorf(
			"%w: version %d, want %d",
			ErrOutboxResetJournal,
			journal.Version,
			resetJournalVersion,
		)
	}

	if err := journal.validate(); err != nil {
		return nil, err
	}

	return &journal, nil
}

// validate rejects a journal that cannot describe a safe reset.
func (j ResetJournal) validate() error {
	switch {
	case strings.TrimSpace(j.OldDatabaseID) == "":
		return fmt.Errorf(
			"%w: empty old database id",
			ErrOutboxResetJournal,
		)
	case strings.TrimSpace(j.NewDatabaseID) == "":
		return fmt.Errorf(
			"%w: empty new database id",
			ErrOutboxResetJournal,
		)
	case j.OldDatabaseID == j.NewDatabaseID:
		return fmt.Errorf(
			"%w: the new database id reuses the old one",
			ErrOutboxResetJournal,
		)
	case !safeDataDirName(j.BackupName):
		return fmt.Errorf(
			"%w: unusable backup name %q",
			ErrOutboxResetJournal,
			j.BackupName,
		)
	case !safeDataDirName(j.NoteName):
		return fmt.Errorf(
			"%w: unusable note name %q",
			ErrOutboxResetJournal,
			j.NoteName,
		)
	}

	return nil
}

// safeDataDirName reports whether name is a plain file name inside the
// data directory.
//
// A journal is not a trusted input: it decides what gets moved and what
// gets written, so a name with a separator or a parent reference is
// refused instead of resolved.
func safeDataDirName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}

	return filepath.Base(name) == name &&
		!strings.ContainsRune(name, os.PathSeparator)
}

// WriteResetJournal records a reset that is about to start.
//
// The journal is written before the first destructive step and removed
// after the last one, so its presence means "a reset of this queue was
// started" and never anything else.
func WriteResetJournal(dataDir string, journal ResetJournal) error {
	journal.Version = resetJournalVersion

	if err := journal.validate(); err != nil {
		return err
	}

	contents, err := json.MarshalIndent(journal, "", "  ")
	if err != nil {
		return fmt.Errorf("%w: encode: %v", ErrOutboxResetJournal, err)
	}
	contents = append(contents, '\n')

	return writeFileAtomic(ResetJournalPath(dataDir), contents)
}

// ClearResetJournal removes the journal of a finished reset.
//
// A missing journal is not an error: the goal state is reached either
// way.
func ClearResetJournal(dataDir string) error {
	err := os.Remove(ResetJournalPath(dataDir))

	switch {
	case err == nil, errors.Is(err, os.ErrNotExist):
		return nil
	default:
		return fmt.Errorf(
			"%w: remove %s: %v",
			ErrOutboxResetJournal,
			ResetJournalPath(dataDir),
			err,
		)
	}
}

// writeFileAtomic writes contents to path through a temporary sibling.
//
// A reader never sees a partial journal, and a failure leaves any
// previous file in place.
func writeFileAtomic(path string, contents []byte) error {
	dir := filepath.Dir(path)

	temp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("%w: %s: %v", ErrOutboxResetJournal, dir, err)
	}

	tempName := temp.Name()

	cleanup := func() {
		_ = temp.Close()
		_ = os.Remove(tempName)
	}

	if err := temp.Chmod(0o600); err != nil {
		cleanup()

		return fmt.Errorf(
			"%w: chmod temporary file: %v",
			ErrOutboxResetJournal,
			err,
		)
	}

	if _, err := temp.Write(contents); err != nil {
		cleanup()

		return fmt.Errorf(
			"%w: write temporary file: %v",
			ErrOutboxResetJournal,
			err,
		)
	}

	if err := temp.Sync(); err != nil {
		cleanup()

		return fmt.Errorf(
			"%w: sync temporary file: %v",
			ErrOutboxResetJournal,
			err,
		)
	}

	if err := temp.Close(); err != nil {
		_ = os.Remove(tempName)

		return fmt.Errorf(
			"%w: close temporary file: %v",
			ErrOutboxResetJournal,
			err,
		)
	}

	if err := os.Rename(tempName, path); err != nil {
		_ = os.Remove(tempName)

		return fmt.Errorf(
			"%w: rename into place: %v",
			ErrOutboxResetJournal,
			err,
		)
	}

	return nil
}

// orphanTimeLayout is the timestamp in an orphaned queue file name.
//
// It is UTC, it sorts chronologically, and it avoids the characters that
// some filesystems reject in a name.
const orphanTimeLayout = "20060102T150405Z"

// orphanNameSuffix marks a queue file that a reset set aside.
const orphanNameSuffix = ".orphaned-"

// NewOrphanNames returns unused backup and note names for a reset that
// starts at now.
//
// The names live beside the queue, and a name already taken by an
// earlier reset is not reused: two resets in the same second must not
// overwrite each other's backup.
func NewOrphanNames(
	dataDir string,
	now time.Time,
) (backupName string, noteName string, err error) {
	stamp := now.UTC().Format(orphanTimeLayout)

	for attempt := 0; ; attempt++ {
		suffix := stamp
		if attempt > 0 {
			suffix = fmt.Sprintf("%s-%d", stamp, attempt)
		}

		backupName = outboxDatabaseFileName + orphanNameSuffix + suffix
		noteName = backupName + ".txt"

		taken := regularFileExists(
			filepath.Join(dataDir, backupName),
		) || regularFileExists(filepath.Join(dataDir, noteName))

		if !taken {
			return backupName, noteName, nil
		}

		if attempt >= 100 {
			return "", "", fmt.Errorf(
				"%w: no free backup name in %s",
				ErrOutboxQueueOrphan,
				dataDir,
			)
		}
	}
}

// OrphanQueue moves the queue database aside and writes its note.
//
// The old queue is never deleted: a key that comes back, from a key
// store backup or from another machine, is the only thing that can make
// its messages readable again.
//
// Order, and why:
//
//  1. the database is renamed first. A crash after the rename leaves a
//     backup without a note, which the next run repairs by writing the
//     note; the opposite order would leave a note describing a file that
//     is still the live queue.
//  2. the write-ahead log moves with it, because committed entries that
//     are not in the main file yet live only there. Leaving it behind
//     would hand the next queue pages that belong to the old one.
//  3. the shared-memory index is removed, not moved. SQLite rebuilds it
//     from the log, and one left under the name of the new queue would
//     describe pages that are about to change.
//
// The step is idempotent: a run that finds the database already moved
// only writes the note, so a repeated run after a crash converges.
func OrphanQueue(dataDir string, journal ResetJournal) error {
	if err := journal.validate(); err != nil {
		return err
	}

	source := filepath.Join(dataDir, outboxDatabaseFileName)
	backup := filepath.Join(dataDir, journal.BackupName)
	wal := source + "-wal"
	shm := source + "-shm"

	switch {
	case regularFileExists(source) && regularFileExists(backup):
		return fmt.Errorf(
			"%w: both %s and %s exist",
			ErrOutboxQueueOrphan,
			outboxDatabaseFileName,
			journal.BackupName,
		)

	case regularFileExists(source):
		if err := os.Rename(source, backup); err != nil {
			return fmt.Errorf(
				"%w: rename %s: %v",
				ErrOutboxQueueOrphan,
				outboxDatabaseFileName,
				err,
			)
		}

	case regularFileExists(backup):
		// Already moved by an earlier run. Continue with the note.

	default:
		return fmt.Errorf(
			"%w: neither %s nor %s exists",
			ErrOutboxQueueOrphan,
			outboxDatabaseFileName,
			journal.BackupName,
		)
	}

	if regularFileExists(wal) {
		if err := os.Rename(wal, backup+"-wal"); err != nil {
			return fmt.Errorf(
				"%w: rename write-ahead log: %v",
				ErrOutboxQueueOrphan,
				err,
			)
		}
	}

	if regularFileExists(shm) {
		if err := os.Remove(shm); err != nil {
			return fmt.Errorf(
				"%w: remove shared-memory index: %v",
				ErrOutboxQueueOrphan,
				err,
			)
		}
	}

	return writeOrphanNote(dataDir, journal)
}

// writeOrphanNote records beside the backup which identity it belongs
// to, and how to use it again.
func writeOrphanNote(dataDir string, journal ResetJournal) error {
	note := strings.Join([]string{
		"telecli set this message queue aside instead of deleting it.",
		"",
		"Set aside at: " + journal.StartedAt.UTC().Format(time.RFC3339),
		"Queue database: " + journal.BackupName,
		"Old database id: " + journal.OldDatabaseID,
		"",
		"The messages in it cannot be read: the key for that database id",
		"is not in your key storage. This file holds no key.",
		"",
		"If you find the old key again, for example in a key store backup",
		"or on the machine the queue came from:",
		"",
		"  1. stop telecli;",
		"  2. put the old database id back into the configuration file as",
		"     message_delivery.database_id;",
		"  3. rename this database (and its -wal file, if there is one)",
		"     back to " + outboxDatabaseFileName + ".",
		"",
		"telecli will then open the old queue and send what it holds.",
		"",
	}, "\n")

	path := filepath.Join(dataDir, journal.NoteName)

	if err := writeFileAtomic(path, []byte(note)); err != nil {
		return fmt.Errorf("%w: write note: %v", ErrOutboxQueueOrphan, err)
	}

	return nil
}

// NewDatabaseID returns a fresh, opaque queue identity.
//
// A reset never reuses the old identity. A key that comes back for the
// old one would then be loaded for the new, empty queue, and the queue
// that key belongs to would stay unreadable forever.
func NewDatabaseID() (string, error) {
	var suffix [8]byte

	if _, err := rand.Read(suffix[:]); err != nil {
		return "", fmt.Errorf(
			"outbox: generate database id: %w",
			err,
		)
	}

	return "telecli-" + hex.EncodeToString(suffix[:]), nil
}

// CreateQueueKey creates the data-encryption key of a queue identity
// and discards it.
//
// The key is not returned on purpose. A caller that only has to know
// that the key exists must not be able to keep a copy of it: the queue
// reads the key back through LoadKey when it opens, and a second copy
// in memory is one more thing that has to be wiped.
//
// An identity that already has a key returns ErrOutboxKeyExists, which a
// resuming reset reads as "this step is done".
func CreateQueueKey(
	ctx context.Context,
	provider KeyProvider,
	databaseID string,
) error {
	if provider == nil {
		return fmt.Errorf(
			"%w: nil key provider",
			ErrOutboxInvalidConfig,
		)
	}

	key, err := provider.CreateKey(ctx, databaseID)
	if key != nil {
		clearBytes(key)
	}

	return err
}

// regularFileExists reports whether path is a regular file.
//
// It follows the same rule as DatabaseExists: a symbolic link or a
// directory is not a queue file.
func regularFileExists(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular()
}

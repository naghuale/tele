package outbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---- Key inspection ----

// The condition is the only thing a caller may act on, so each provider
// answer has to land on its own one.
func TestInspectKeyClassifiesEveryProviderAnswer(t *testing.T) {
	t.Parallel()

	good := make([]byte, outboxDataEncryptionKeySize)
	for index := range good {
		good[index] = byte(index + 1)
	}

	cases := map[string]struct {
		load func() ([]byte, error)
		want KeyCondition
	}{
		"present": {
			load: func() ([]byte, error) {
				return append([]byte(nil), good...), nil
			},
			want: KeyConditionPresent,
		},
		"absent": {
			load: func() ([]byte, error) {
				return nil, ErrOutboxKeyUnavailable
			},
			want: KeyConditionAbsent,
		},
		"malformed length": {
			load: func() ([]byte, error) {
				return []byte{1, 2, 3}, nil
			},
			want: KeyConditionMalformed,
		},
		"malformed sentinel": {
			load: func() ([]byte, error) {
				return nil, ErrOutboxKeyMalformed
			},
			want: KeyConditionMalformed,
		},
		"locked or denied": {
			load: func() ([]byte, error) {
				return nil, ErrOutboxKeyAccessDenied
			},
			want: KeyConditionRefused,
		},
		"no secure storage": {
			load: func() ([]byte, error) {
				return nil, ErrOutboxKeyProviderUnsupported
			},
			want: KeyConditionUnsupported,
		},
		"other failure": {
			load: func() ([]byte, error) {
				return nil, errors.New("key store exploded")
			},
			want: KeyConditionFailed,
		},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			condition, _ := InspectKey(
				context.Background(),
				&scriptedKeyProvider{load: testCase.load},
				"primary",
			)
			if condition != testCase.want {
				t.Fatalf(
					"condition = %v, want %v",
					condition,
					testCase.want,
				)
			}
		})
	}
}

// A condition that is not present or absent must carry the cause, so a
// caller can report it without asking the provider twice.
func TestInspectKeyKeepsTheCauseOfAFailedAnswer(t *testing.T) {
	t.Parallel()

	cause := ErrOutboxKeyAccessDenied

	condition, err := InspectKey(
		context.Background(),
		&scriptedKeyProvider{
			load: func() ([]byte, error) {
				return nil, cause
			},
		},
		"primary",
	)

	if condition != KeyConditionRefused {
		t.Fatalf("condition = %v, want refused", condition)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("error = %v, want %v", err, cause)
	}
}

// Absence is the answer a caller acts on, not a failure: it must not
// arrive as an error, or every caller would have to unwrap it first.
func TestInspectKeyReportsAbsenceWithoutAnError(t *testing.T) {
	t.Parallel()

	condition, err := InspectKey(
		context.Background(),
		&scriptedKeyProvider{
			load: func() ([]byte, error) {
				return nil, ErrOutboxKeyUnavailable
			},
		},
		"primary",
	)

	if err != nil {
		t.Fatalf("error = %v, want nil", err)
	}
	if condition != KeyConditionAbsent {
		t.Fatalf("condition = %v, want absent", condition)
	}
}

// Inspecting a key must never create one, whatever the answer is.
func TestInspectKeyNeverCreatesAKey(t *testing.T) {
	t.Parallel()

	for _, load := range []func() ([]byte, error){
		func() ([]byte, error) { return nil, ErrOutboxKeyUnavailable },
		func() ([]byte, error) { return nil, ErrOutboxKeyAccessDenied },
		func() ([]byte, error) { return []byte{1}, nil },
	} {
		provider := &scriptedKeyProvider{load: load}

		if _, err := InspectKey(
			context.Background(),
			provider,
			"primary",
		); err != nil && !errors.Is(
			err,
			ErrOutboxKeyMalformed,
		) && !errors.Is(
			err,
			ErrOutboxKeyAccessDenied,
		) {
			t.Fatalf("error = %v, want nil or a cause", err)
		}

		if provider.created != "" {
			t.Fatalf(
				"CreateKey called for %q, want no call",
				provider.created,
			)
		}
	}
}

func TestInspectKeyRejectsUnusableInput(t *testing.T) {
	t.Parallel()

	if _, err := InspectKey(
		context.Background(),
		nil,
		"primary",
	); !errors.Is(err, ErrOutboxInvalidConfig) {
		t.Fatalf("error = %v, want ErrOutboxInvalidConfig", err)
	}

	provider := &scriptedKeyProvider{
		load: func() ([]byte, error) { return nil, nil },
	}

	if _, err := InspectKey(
		context.Background(),
		provider,
		"  ",
	); !errors.Is(err, ErrOutboxKeyInvalidDatabaseID) {
		t.Fatalf("error = %v, want ErrOutboxKeyInvalidDatabaseID", err)
	}
}

// scriptedKeyProvider answers LoadKey from a function and records the
// identity CreateKey was called with.
type scriptedKeyProvider struct {
	load    func() ([]byte, error)
	created string
}

func (p *scriptedKeyProvider) LoadKey(
	context.Context,
	string,
) ([]byte, error) {
	return p.load()
}

func (p *scriptedKeyProvider) CreateKey(
	_ context.Context,
	databaseID string,
) ([]byte, error) {
	p.created = databaseID

	return make([]byte, outboxDataEncryptionKeySize), nil
}

var _ KeyProvider = (*scriptedKeyProvider)(nil)

// ---- Queue inspection ----

// The summary must describe the queue from its plaintext metadata alone,
// and must leave the queue byte for byte as it found it: a command that
// shows what will be lost may not change what will be lost.
func TestInspectQueueCountsWithoutTouchingTheQueue(t *testing.T) {
	t.Parallel()

	dir, provider := openQueueWithEntries(
		t,
		queueFixture{
			text:   "the message body must never be shown",
			states: 3,
		},
	)

	before := snapshotDir(t, dir)

	summary, err := InspectQueue(context.Background(), dir)
	if err != nil {
		t.Fatalf("InspectQueue: %v", err)
	}

	if !summary.Readable {
		t.Fatalf("summary = %+v, want readable", summary)
	}
	if summary.Total != 3 {
		t.Fatalf("Total = %d, want 3", summary.Total)
	}
	if summary.Counts[StateQueued] != 3 {
		t.Fatalf(
			"queued = %d, want 3",
			summary.Counts[StateQueued],
		)
	}
	if summary.Unsent != 3 {
		t.Fatalf("Unsent = %d, want 3", summary.Unsent)
	}
	if summary.OldestUnsent.IsZero() {
		t.Fatal("OldestUnsent is zero, want the oldest entry")
	}
	if want := time.Unix(1700000000, 0).UTC(); !summary.OldestUnsent.Equal(want) {
		t.Fatalf(
			"OldestUnsent = %s, want %s",
			summary.OldestUnsent,
			want,
		)
	}

	if got := snapshotDir(t, dir); !equalSnapshots(before, got) {
		t.Fatalf(
			"data folder changed:\nbefore %v\nafter  %v",
			before,
			got,
		)
	}

	_ = provider
}

// A summary that cannot be read must say so. Guessing an empty queue
// would understate the loss the user is agreeing to.
func TestInspectQueueReportsAnUnreadableQueue(t *testing.T) {
	t.Parallel()

	cases := map[string]func(t *testing.T, dir string){
		"empty file": func(t *testing.T, dir string) {
			writeFile(t, filepath.Join(dir, outboxDatabaseFileName), nil)
		},
		"damaged file": func(t *testing.T, dir string) {
			writeFile(t, filepath.Join(
				dir,
				outboxDatabaseFileName,
			), []byte("not a sqlite database at all"))
		},
		"no queue": func(t *testing.T, _ string) {},
	}

	for name, arrange := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := tempQueueDir(t)
			arrange(t, dir)

			summary, err := InspectQueue(
				context.Background(),
				dir,
			)
			if err != nil {
				t.Fatalf("InspectQueue: %v", err)
			}
			if summary.Readable {
				t.Fatalf("summary = %+v, want unreadable", summary)
			}
			if summary.Cause == nil {
				t.Fatal("Cause = nil, want the reason")
			}
			if summary.Total != 0 || summary.Unsent != 0 {
				t.Fatalf(
					"summary = %+v, want no counts",
					summary,
				)
			}
		})
	}
}

func TestInspectQueueRejectsAnEmptyDataDir(t *testing.T) {
	t.Parallel()

	if _, err := InspectQueue(
		context.Background(),
		"  ",
	); !errors.Is(err, ErrOutboxInvalidConfig) {
		t.Fatalf("error = %v, want ErrOutboxInvalidConfig", err)
	}
}

// ---- The reset journal ----

func TestResetJournalRoundTrip(t *testing.T) {
	t.Parallel()

	dir := tempQueueDir(t)
	journal := ResetJournal{
		Version:       resetJournalVersion,
		OldDatabaseID: "telecli-main",
		NewDatabaseID: "telecli-ab12cd34ef56",
		BackupName:    outboxDatabaseFileName + ".orphaned-20260927T101500Z",
		NoteName:      outboxDatabaseFileName + ".orphaned-20260927T101500Z.txt",
		StartedAt:     time.Date(2026, 9, 27, 10, 15, 0, 0, time.UTC),
	}

	if PendingReset(dir) {
		t.Fatal("PendingReset = true before the journal is written")
	}

	if err := WriteResetJournal(dir, journal); err != nil {
		t.Fatalf("WriteResetJournal: %v", err)
	}
	if !PendingReset(dir) {
		t.Fatal("PendingReset = false after the journal is written")
	}

	got, err := ReadResetJournal(dir)
	if err != nil {
		t.Fatalf("ReadResetJournal: %v", err)
	}
	if got == nil {
		t.Fatal("ReadResetJournal = nil, want the journal")
	}
	if *got != journal {
		t.Fatalf("journal = %+v, want %+v", *got, journal)
	}

	if err := ClearResetJournal(dir); err != nil {
		t.Fatalf("ClearResetJournal: %v", err)
	}
	if PendingReset(dir) {
		t.Fatal("PendingReset = true after the journal is cleared")
	}

	// Clearing a journal that is not there is the goal state already.
	if err := ClearResetJournal(dir); err != nil {
		t.Fatalf("ClearResetJournal twice: %v", err)
	}
}

func TestReadResetJournalReportsNoJournal(t *testing.T) {
	t.Parallel()

	got, err := ReadResetJournal(tempQueueDir(t))
	if err != nil {
		t.Fatalf("ReadResetJournal: %v", err)
	}
	if got != nil {
		t.Fatalf("journal = %+v, want nil", got)
	}
}

// A journal decides what gets moved, so a name that leaves the data
// folder is refused instead of resolved.
func TestResetJournalRejectsUnusableRecords(t *testing.T) {
	t.Parallel()

	valid := ResetJournal{
		OldDatabaseID: "old",
		NewDatabaseID: "new",
		BackupName:    "outbox.db.orphaned-20260927T101500Z",
		NoteName:      "outbox.db.orphaned-20260927T101500Z.txt",
	}

	cases := map[string]func(journal *ResetJournal){
		"no old id":       func(j *ResetJournal) { j.OldDatabaseID = " " },
		"no new id":       func(j *ResetJournal) { j.NewDatabaseID = "" },
		"reused id":       func(j *ResetJournal) { j.NewDatabaseID = j.OldDatabaseID },
		"backup escapes":  func(j *ResetJournal) { j.BackupName = "../evil" },
		"absolute note":   func(j *ResetJournal) { j.NoteName = "/etc/passwd" },
		"empty backup":    func(j *ResetJournal) { j.BackupName = "" },
		"parent as note":  func(j *ResetJournal) { j.NoteName = ".." },
		"nested in queue": func(j *ResetJournal) { j.NoteName = "sub/note.txt" },
	}

	for name, corrupt := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			journal := valid
			corrupt(&journal)

			if err := WriteResetJournal(
				tempQueueDir(t),
				journal,
			); !errors.Is(err, ErrOutboxResetJournal) {
				t.Fatalf("error = %v, want ErrOutboxResetJournal", err)
			}
		})
	}
}

// A journal written by another build is not interpreted. Guessing would
// mean moving files on the word of a record nobody can read.
func TestReadResetJournalRejectsAnUnknownVersion(t *testing.T) {
	t.Parallel()

	dir := tempQueueDir(t)
	writeFile(t, ResetJournalPath(dir), []byte(`{
		"version": 99,
		"old_database_id": "old",
		"new_database_id": "new",
		"backup_name": "outbox.db.orphaned-20260927T101500Z",
		"note_name": "outbox.db.orphaned-20260927T101500Z.txt"
	}`))

	if _, err := ReadResetJournal(dir); !errors.Is(
		err,
		ErrOutboxResetJournal,
	) {
		t.Fatalf("error = %v, want ErrOutboxResetJournal", err)
	}
}

func TestReadResetJournalRejectsDamagedContent(t *testing.T) {
	t.Parallel()

	dir := tempQueueDir(t)
	writeFile(t, ResetJournalPath(dir), []byte("not json"))

	if _, err := ReadResetJournal(dir); !errors.Is(
		err,
		ErrOutboxResetJournal,
	) {
		t.Fatalf("error = %v, want ErrOutboxResetJournal", err)
	}
}

// ---- Orphaning the queue ----

// The old queue is the only copy of those messages. It is moved, never
// deleted, and the note beside it says which identity it belongs to.
func TestOrphanQueueKeepsTheQueueAndWritesTheNote(t *testing.T) {
	t.Parallel()

	dir, _ := openQueueWithEntries(
		t,
		queueFixture{text: "body", states: 1},
	)

	// A write-ahead log and its index, as a killed process would leave
	// them. The log holds committed pages, so it belongs to the old
	// queue; the index is a cache SQLite rebuilds.
	writeFile(t, filepath.Join(dir, outboxDatabaseFileName+"-wal"), []byte("log"))
	writeFile(t, filepath.Join(dir, outboxDatabaseFileName+"-shm"), []byte("index"))

	journal := resetFixtureJournal()
	if err := OrphanQueue(dir, journal); err != nil {
		t.Fatalf("OrphanQueue: %v", err)
	}

	backup := filepath.Join(dir, journal.BackupName)
	if !regularFileExists(backup) {
		t.Fatalf("no backup at %s", backup)
	}
	if DatabaseExists(dir) {
		t.Fatalf("%s still exists", outboxDatabaseFileName)
	}
	if _, err := os.Stat(backup + "-wal"); err != nil {
		t.Fatalf("the write-ahead log did not move: %v", err)
	}
	if regularFileExists(
		filepath.Join(dir, outboxDatabaseFileName+"-shm"),
	) {
		t.Fatal("the shared-memory index was left behind")
	}

	note, err := os.ReadFile(
		filepath.Join(dir, journal.NoteName),
	)
	if err != nil {
		t.Fatalf("read note: %v", err)
	}
	if !strings.Contains(string(note), "old-identity") {
		t.Fatalf("note does not name the old identity:\n%s", note)
	}
	if strings.Contains(string(note), "body") {
		t.Fatalf("note leaks the queue contents:\n%s", note)
	}
}

// A repeated run after a crash between the rename and the note must
// converge instead of failing.
func TestOrphanQueueIsIdempotent(t *testing.T) {
	t.Parallel()

	dir, _ := openQueueWithEntries(
		t,
		queueFixture{text: "body", states: 1},
	)

	journal := resetFixtureJournal()
	if err := OrphanQueue(dir, journal); err != nil {
		t.Fatalf("OrphanQueue: %v", err)
	}
	if err := OrphanQueue(dir, journal); err != nil {
		t.Fatalf("OrphanQueue twice: %v", err)
	}

	if !regularFileExists(filepath.Join(dir, journal.BackupName)) {
		t.Fatal("the backup is gone after a repeated run")
	}
	if !regularFileExists(filepath.Join(dir, journal.NoteName)) {
		t.Fatal("the note is missing after a repeated run")
	}
}

// A folder that holds both the queue and a backup of the same reset is
// one this command cannot reason about, so it changes nothing.
func TestOrphanQueueRefusesAnAmbiguousFolder(t *testing.T) {
	t.Parallel()

	dir, _ := openQueueWithEntries(
		t,
		queueFixture{text: "body", states: 1},
	)

	journal := resetFixtureJournal()
	writeFile(t, filepath.Join(dir, journal.BackupName), []byte("older"))

	if err := OrphanQueue(dir, journal); !errors.Is(
		err,
		ErrOutboxQueueOrphan,
	) {
		t.Fatalf("error = %v, want ErrOutboxQueueOrphan", err)
	}
	if !DatabaseExists(dir) {
		t.Fatal("the queue was moved out of an ambiguous folder")
	}
}

func TestOrphanQueueRefusesAMissingQueue(t *testing.T) {
	t.Parallel()

	err := OrphanQueue(tempQueueDir(t), resetFixtureJournal())
	if !errors.Is(err, ErrOutboxQueueOrphan) {
		t.Fatalf("error = %v, want ErrOutboxQueueOrphan", err)
	}
}

func TestNewOrphanNamesAvoidsAnExistingBackup(t *testing.T) {
	t.Parallel()

	dir := tempQueueDir(t)
	now := time.Date(2026, 9, 27, 10, 15, 0, 0, time.UTC)

	first, _, err := NewOrphanNames(dir, now)
	if err != nil {
		t.Fatalf("NewOrphanNames: %v", err)
	}
	if !strings.HasPrefix(first, outboxDatabaseFileName+".orphaned-") {
		t.Fatalf("backup name = %q, want the orphaned prefix", first)
	}
	if !strings.HasSuffix(first, "20260927T101500Z") {
		t.Fatalf("backup name = %q, want the UTC stamp", first)
	}

	writeFile(t, filepath.Join(dir, first), []byte("older"))

	second, _, err := NewOrphanNames(dir, now)
	if err != nil {
		t.Fatalf("NewOrphanNames: %v", err)
	}
	if second == first {
		t.Fatalf("backup name = %q, want a name that is free", second)
	}
}

func TestNewDatabaseIDIsFreshAndOpaque(t *testing.T) {
	t.Parallel()

	first, err := NewDatabaseID()
	if err != nil {
		t.Fatalf("NewDatabaseID: %v", err)
	}
	second, err := NewDatabaseID()
	if err != nil {
		t.Fatalf("NewDatabaseID: %v", err)
	}

	if first == second {
		t.Fatalf("database id = %q twice, want a fresh one", first)
	}
	if err := validateDatabaseID(first); err != nil {
		t.Fatalf("database id %q is unusable: %v", first, err)
	}
}

// ---- Refusing to create a key under a pending reset ----

// Without this guard a start between the two halves of a reset would
// create a fresh key for the old identity, and the user would see a
// working empty queue instead of the reset they were told to run.
func TestOpenRefusesToCreateAKeyWhileAResetIsPending(t *testing.T) {
	t.Parallel()

	dir, provider := openQueueWithEntries(
		t,
		queueFixture{text: "body", states: 1},
	)
	delete(provider.keys, "primary")

	// The queue file is moved aside, which is the moment a start must
	// not invent a new queue.
	if err := OrphanQueue(dir, resetFixtureJournal()); err != nil {
		t.Fatalf("OrphanQueue: %v", err)
	}
	journal := resetFixtureJournal()
	if err := WriteResetJournal(dir, journal); err != nil {
		t.Fatalf("WriteResetJournal: %v", err)
	}

	createCalls := provider.createCalls

	_, err := Open(
		context.Background(),
		Config{DataDir: dir, DatabaseID: "primary"},
		Deps{KeyProvider: provider},
	)
	if !errors.Is(err, ErrOutboxResetPending) {
		t.Fatalf("error = %v, want ErrOutboxResetPending", err)
	}
	if provider.createCalls != createCalls {
		t.Fatalf(
			"CreateKey calls = %d, want %d",
			provider.createCalls,
			createCalls,
		)
	}
}

// A journal left behind by a reset that got as far as writing the new
// identity must not keep the new queue from opening: by then the key
// exists, so the queue is ready and the record is only a leftover.
func TestOpenIgnoresAJournalForAQueueThatIsReady(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dir, provider := openQueueWithEntries(
		t,
		queueFixture{text: "body", states: 1},
	)

	journal := resetFixtureJournal()
	journal.NewDatabaseID = "new-identity"
	if err := OrphanQueue(dir, journal); err != nil {
		t.Fatalf("OrphanQueue: %v", err)
	}
	if err := CreateQueueKey(ctx, provider, "new-identity"); err != nil {
		t.Fatalf("CreateQueueKey: %v", err)
	}
	if err := WriteResetJournal(dir, journal); err != nil {
		t.Fatalf("WriteResetJournal: %v", err)
	}

	box, err := Open(
		ctx,
		Config{DataDir: dir, DatabaseID: "new-identity"},
		Deps{KeyProvider: provider},
	)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := box.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// ---- Helpers ----

// queueFixture describes the queue a test needs on disk.
type queueFixture struct {
	text   string
	states int
}

// openQueueWithEntries builds a real queue in a scratch data directory.
//
// It goes through Open and the real store, so the bytes on disk are the
// bytes production writes. The provider is returned so a test can take
// the key away afterwards.
func openQueueWithEntries(
	t *testing.T,
	fixture queueFixture,
) (string, *factoryKeyProvider) {
	t.Helper()

	dir := tempQueueDir(t)
	provider := newFactoryKeyProvider()

	box, err := Open(
		context.Background(),
		Config{
			DataDir:    dir,
			DatabaseID: "primary",
			InstanceID: "test-instance",
		},
		Deps{KeyProvider: provider},
	)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	for index := 0; index < fixture.states; index++ {
		entry := queuedEntry(
			ID(strings.Repeat("e", index+1)),
			42,
			fixture.text,
		)

		if err := box.Store.Enqueue(
			context.Background(),
			entry,
		); err != nil {
			_ = box.Close()
			t.Fatalf("Enqueue: %v", err)
		}
	}

	if err := box.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	return dir, provider
}

// tempQueueDir returns a data directory with the permissions Open
// requires.
func tempQueueDir(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("chmod data dir: %v", err)
	}

	return dir
}

// resetFixtureJournal returns a journal with fixed names, so a test can
// look for the files it expects.
func resetFixtureJournal() ResetJournal {
	return ResetJournal{
		OldDatabaseID: "old-identity",
		NewDatabaseID: "new-identity",
		BackupName:    outboxDatabaseFileName + ".orphaned-20260927T101500Z",
		NoteName:      outboxDatabaseFileName + ".orphaned-20260927T101500Z.txt",
		StartedAt:     time.Date(2026, 9, 27, 10, 15, 0, 0, time.UTC),
	}
}

// snapshotDir maps every file in dir to a digest of its contents.
func snapshotDir(t *testing.T, dir string) map[string]string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}

	out := make(map[string]string, len(entries))
	for _, entry := range entries {
		contents, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		out[entry.Name()] = digest(contents)
	}

	return out
}

func equalSnapshots(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}

	for name, sum := range a {
		if b[name] != sum {
			return false
		}
	}

	return true
}

func digest(contents []byte) string {
	sum := sha256.Sum256(contents)

	return hex.EncodeToString(sum[:])
}

// writeFile writes contents with the permissions a queue file has.
func writeFile(t *testing.T, path string, contents []byte) {
	t.Helper()

	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

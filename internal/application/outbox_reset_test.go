package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"telecli/internal/config"
	"telecli/internal/outbox"
)

// outboxResetOldID is the queue identity a fixture starts with.
const outboxResetOldID = "telecli-main"

// outboxResetNewID is the identity the command is given in tests, so the
// expected configuration is known.
const outboxResetNewID = "telecli-0123456789ab"

// outboxResetText is the message body a fixture queue holds.
//
// It exists to be searched for: the summary of a queue whose key is
// gone must describe the queue without ever showing what is in it.
const outboxResetText = "fixture-message-body-must-not-be-shown"

// outboxResetTime is the fixed clock of the command under test, so the
// backup name is predictable.
var outboxResetTime = time.Date(
	2026, 9, 27, 10, 15, 0, 0, time.UTC,
)

// outboxResetFixture is a machine with a real queue on disk whose key
// has been lost, plus a configuration file that names it.
type outboxResetFixture struct {
	dataDir    string
	configPath string
	provider   *h7dKeyProvider
	ops        OutboxResetOperations

	// keyProvider replaces the fixture key store for one run. It is nil
	// unless a test needs a key store that answers something the
	// in-memory one cannot.
	keyProvider outbox.KeyProvider

	// answers is what the next run reads at the confirmation.
	answers string
}

func newOutboxResetFixture(
	t *testing.T,
	entries int,
) *outboxResetFixture {
	t.Helper()

	root := t.TempDir()
	dataDir := filepath.Join(root, "outbox")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatalf("create data dir: %v", err)
	}

	provider := newH7dKeyProvider()
	configPath := filepath.Join(root, "config.toml")

	writeOutboxResetConfig(t, configPath, dataDir, outboxResetOldID)

	fixture := &outboxResetFixture{
		dataDir:    dataDir,
		configPath: configPath,
		provider:   provider,
		ops: OutboxResetOperations{
			NewDatabaseID: func() (string, error) {
				return outboxResetNewID, nil
			},
			Now:         func() time.Time { return outboxResetTime },
			HasTerminal: func(io.Reader) bool { return true },
		},
		answers: outboxResetConfirmation + "\n",
	}

	fixture.seedQueue(t, entries)

	return fixture
}

// keyStore returns the key store the next run uses.
func (f *outboxResetFixture) keyStore() outbox.KeyProvider {
	if f.keyProvider != nil {
		return f.keyProvider
	}

	return f.provider
}

// seedQueue fills the queue through the real open path, so the bytes on
// disk are the bytes production writes.
func (f *outboxResetFixture) seedQueue(t *testing.T, entries int) {
	t.Helper()

	opened, err := outbox.Open(
		context.Background(),
		outbox.Config{
			DataDir:    f.dataDir,
			DatabaseID: outboxResetOldID,
			InstanceID: "fixture",
		},
		outbox.Deps{KeyProvider: f.provider},
	)
	if err != nil {
		t.Fatalf("seed open: %v", err)
	}

	for index := 0; index < entries; index++ {
		entry := outbox.Entry{
			ID:         outbox.ID(fmt.Sprintf("entry-%d", index)),
			AccountKey: "account",
			ChatID:     4242,
			Text:       outboxResetText,
			State:      outbox.StateQueued,
			CreatedAt:  outboxResetTime,
			UpdatedAt:  outboxResetTime,
		}

		if err := opened.Store.Enqueue(
			context.Background(),
			entry,
		); err != nil {
			_ = opened.Close()
			t.Fatalf("seed enqueue: %v", err)
		}
	}

	if err := opened.Close(); err != nil {
		t.Fatalf("seed close: %v", err)
	}
}

// loseKey removes the queue key, which is the situation the command
// exists for.
func (f *outboxResetFixture) loseKey() {
	f.provider.deleteKey(outboxResetOldID)
}

// run executes `telecli outbox reset` and returns the exit code with
// both streams.
func (f *outboxResetFixture) run(
	t *testing.T,
	args ...string,
) (code int, stdout string, stderr string) {
	t.Helper()

	var out, errOut bytes.Buffer

	env := Environment{
		Stdout: &out,
		Stderr: &errOut,
		Stdin:  strings.NewReader(f.answers),
		NewOutboxKeyProvider: func() outbox.KeyProvider {
			return f.keyStore()
		},
		OutboxResetOps: f.ops,
	}

	argv := append(
		[]string{"reset", "--config", f.configPath},
		args...,
	)

	return runOutbox(argv, env), out.String(), errOut.String()
}

// queueFileNames lists the queue files in the data folder.
func (f *outboxResetFixture) backupName(t *testing.T) string {
	t.Helper()

	// The note beside the backup is named after it, so the pattern ends
	// at the timestamp and never matches the note.
	matches, err := filepath.Glob(
		filepath.Join(f.dataDir, "outbox.db.orphaned-*Z"),
	)
	if err != nil {
		t.Fatalf("glob backups: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("backups = %v, want exactly one", matches)
	}

	return filepath.Base(matches[0])
}

func (f *outboxResetFixture) config(t *testing.T) config.Config {
	t.Helper()

	cfg, err := config.Load(f.configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	return cfg
}

func (f *outboxResetFixture) queueDigest(t *testing.T) string {
	t.Helper()

	contents, err := os.ReadFile(
		filepath.Join(f.dataDir, "outbox.db"),
	)
	if err != nil {
		t.Fatalf("read queue: %v", err)
	}

	sum := sha256.Sum256(contents)

	return hex.EncodeToString(sum[:])
}

func (f *outboxResetFixture) configDigest(t *testing.T) string {
	t.Helper()

	contents, err := os.ReadFile(f.configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	sum := sha256.Sum256(contents)

	return hex.EncodeToString(sum[:])
}

// dirEntries maps every file in dir to a digest, so a test can state
// that nothing in it changed.
func dirEntries(t *testing.T, dir string) map[string]string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}

	out := make(map[string]string, len(entries))
	for _, entry := range entries {
		contents, err := os.ReadFile(
			filepath.Join(dir, entry.Name()),
		)
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}

		sum := sha256.Sum256(contents)
		out[entry.Name()] = hex.EncodeToString(sum[:])
	}

	return out
}

func writeOutboxResetConfig(
	t *testing.T,
	path string,
	dataDir string,
	databaseID string,
) {
	t.Helper()

	cfg := config.Default()
	cfg.DataDir = filepath.Dir(dataDir)
	cfg.TDLib.DatabaseDir = filepath.Join(cfg.DataDir, "tdlib", "database")
	cfg.TDLib.FilesDir = filepath.Join(cfg.DataDir, "tdlib", "files")
	cfg.MessageDelivery = config.MessageDeliveryConfig{
		Mode:       config.MessageSendModeDurable,
		DataDir:    dataDir,
		DatabaseID: databaseID,
		InstanceID: "fixture",
	}

	if err := config.WriteFile(path, cfg); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

// ---- The reset itself ----

// The whole point of the command: a new, empty queue that works, and an
// old queue that is still there with a note saying which identity it
// belongs to.
func TestOutboxResetStartsANewQueueAndKeepsTheOldOne(t *testing.T) {
	t.Parallel()

	fixture := newOutboxResetFixture(t, 3)
	fixture.loseKey()

	code, stdout, stderr := fixture.run(t)

	if code != 0 {
		t.Fatalf(
			"exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s",
			code,
			stdout,
			stderr,
		)
	}

	backup := filepath.Join(fixture.dataDir, fixture.backupName(t))
	if _, err := os.Stat(backup); err != nil {
		t.Fatalf("the old queue is gone: %v", err)
	}
	if outbox.DatabaseExists(fixture.dataDir) {
		t.Fatal("the old queue is still the live queue")
	}

	note, err := os.ReadFile(backup + ".txt")
	if err != nil {
		t.Fatalf("read note: %v", err)
	}
	if !strings.Contains(string(note), outboxResetOldID) {
		t.Fatalf(
			"note does not name the old identity:\n%s",
			note,
		)
	}

	cfg := fixture.config(t)
	if cfg.MessageDelivery.DatabaseID != outboxResetNewID {
		t.Fatalf(
			"database_id = %q, want %q",
			cfg.MessageDelivery.DatabaseID,
			outboxResetNewID,
		)
	}
	if cfg.MessageDelivery.DatabaseID == outboxResetOldID {
		t.Fatal("the old database id was reused")
	}
	if cfg.MessageDelivery.DataDir != fixture.dataDir {
		t.Fatalf(
			"data_dir = %q, want %q",
			cfg.MessageDelivery.DataDir,
			fixture.dataDir,
		)
	}

	// The new queue must be openable with the key the command created.
	opened, err := outbox.Open(
		context.Background(),
		outbox.Config{
			DataDir:    fixture.dataDir,
			DatabaseID: outboxResetNewID,
			InstanceID: "fixture",
		},
		outbox.Deps{KeyProvider: fixture.provider},
	)
	if err != nil {
		t.Fatalf("open the new queue: %v", err)
	}
	if err := opened.Close(); err != nil {
		t.Fatalf("close the new queue: %v", err)
	}
}

// The summary must count the queue and say how old it is, without ever
// showing what is in it: the text needs the key that is gone.
func TestOutboxResetSummaryCountsWithoutTheMessageText(t *testing.T) {
	t.Parallel()

	fixture := newOutboxResetFixture(t, 3)
	fixture.loseKey()
	fixture.answers = "\n"

	_, stdout, _ := fixture.run(t)

	if !strings.Contains(stdout, "queued") {
		t.Fatalf("summary has no state counts:\n%s", stdout)
	}
	if !strings.Contains(stdout, "3") {
		t.Fatalf("summary has no counts:\n%s", stdout)
	}
	if !strings.Contains(stdout, "2026-09-20") &&
		!strings.Contains(stdout, "2026-09-27") {
		t.Fatalf("summary has no oldest unsent time:\n%s", stdout)
	}
	if strings.Contains(stdout, outboxResetText) {
		t.Fatalf("summary shows the message body:\n%s", stdout)
	}
	if strings.Contains(stdout, "4242") {
		t.Fatalf("summary shows a chat id:\n%s", stdout)
	}
}

// The command works on metadata alone, so a queue that cannot be read
// must still be resettable, with the uncertainty stated.
func TestOutboxResetProceedsWhenTheQueueCannotBeCounted(t *testing.T) {
	t.Parallel()

	fixture := newOutboxResetFixture(t, 0)
	fixture.loseKey()

	if err := os.WriteFile(
		filepath.Join(fixture.dataDir, "outbox.db"),
		[]byte("not a database"),
		0o600,
	); err != nil {
		t.Fatalf("damage queue: %v", err)
	}

	code, stdout, _ := fixture.run(t, "--yes")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", code, stdout)
	}
	if !strings.Contains(stdout, "could not read") {
		t.Fatalf("summary does not admit the uncertainty:\n%s", stdout)
	}
	if _, err := os.Stat(
		filepath.Join(fixture.dataDir, fixture.backupName(t)),
	); err != nil {
		t.Fatalf("the queue was not kept: %v", err)
	}
}

// ---- Refusals ----

// The command acts only on a key that is provably gone. Every other
// answer means the messages may still be readable, and it must change
// nothing at all.
func TestOutboxResetRefusesWhenTheKeyIsNotProvablyMissing(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		answer   func() ([]byte, error)
		wantHint string
	}{
		"keychain locked or denied": {
			answer: func() ([]byte, error) {
				return nil, outbox.ErrOutboxKeyAccessDenied
			},
			wantHint: SendingPausedKeychainLocked.Hint(),
		},
		"no secure key storage": {
			answer: func() ([]byte, error) {
				return nil, outbox.ErrOutboxKeyProviderUnsupported
			},
			wantHint: SendingPausedNoKeyStorage.Hint(),
		},
		"key is there": {
			answer: func() ([]byte, error) {
				return make([]byte, 32), nil
			},
			wantHint: "Run telecli doctor",
		},
		"key is unusable": {
			answer: func() ([]byte, error) {
				return []byte{1, 2, 3}, nil
			},
			wantHint: "Run telecli doctor",
		},
		"key store failed": {
			answer: func() ([]byte, error) {
				return nil, errors.New("key store exploded")
			},
			wantHint: SendingPausedOther.Hint(),
		},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			fixture := newOutboxResetFixture(t, 1)
			fixture.answers = outboxResetConfirmation + "\n"

			var created string

			before := dirEntries(t, fixture.dataDir)
			configBefore := fixture.configDigest(t)

			// The key store answers with a scripted result, so the case
			// does not depend on which platform provider a developer
			// happens to run this on. The classification itself is the
			// production one.
			key, answerErr := testCase.answer()
			fixture.keyProvider = outboxResetScriptedKey{
				key:     key,
				keyErr:  answerErr,
				created: &created,
			}

			code, stdout, _ := fixture.run(t)

			if code == 0 {
				t.Fatalf("exit code = 0, want non-zero\n%s", stdout)
			}
			if !strings.Contains(stdout, testCase.wantHint) {
				t.Fatalf(
					"output does not advise %q:\n%s",
					testCase.wantHint,
					stdout,
				)
			}
			if !strings.Contains(stdout, fixture.dataDir) {
				t.Fatalf("output does not name the data folder:\n%s", stdout)
			}

			after := dirEntries(t, fixture.dataDir)
			if len(after) != len(before) {
				t.Fatalf(
					"data folder changed:\nbefore %v\nafter %v",
					before,
					after,
				)
			}
			for name, sum := range before {
				if after[name] != sum {
					t.Fatalf("file %s changed", name)
				}
			}
			if got := fixture.configDigest(t); got != configBefore {
				t.Fatal("the configuration changed")
			}
			if !outbox.DatabaseExists(fixture.dataDir) {
				t.Fatal("the queue was moved")
			}
			if created != "" {
				t.Fatalf(
					"a key was created for %q, want no key",
					created,
				)
			}
		})
	}
}

// outboxResetScriptedKey is a key store that answers with a fixed
// result and records the identity a key was requested for.
type outboxResetScriptedKey struct {
	key     []byte
	keyErr  error
	created *string
}

func (p outboxResetScriptedKey) LoadKey(
	context.Context,
	string,
) ([]byte, error) {
	if p.keyErr != nil {
		return nil, p.keyErr
	}

	return append([]byte(nil), p.key...), nil
}

func (p outboxResetScriptedKey) CreateKey(
	_ context.Context,
	databaseID string,
) ([]byte, error) {
	if p.created != nil {
		*p.created = databaseID
	}

	return make([]byte, 32), nil
}

var _ outbox.KeyProvider = outboxResetScriptedKey{}

// A key that a real provider refuses must be reported as that key
// problem, not as a missing key: the classification goes through the
// production helper, not through a test stub.
func TestOutboxResetTellsAMalformedKeyFromAMissingOne(t *testing.T) {
	t.Parallel()

	fixture := newOutboxResetFixture(t, 1)
	fixture.provider.keys[outboxResetOldID] = []byte{1, 2, 3}

	before := fixture.queueDigest(t)

	code, stdout, _ := fixture.run(t, "--yes")

	if code == 0 {
		t.Fatalf("exit code = 0, want non-zero\n%s", stdout)
	}
	if !strings.Contains(stdout, "exists but cannot be used") {
		t.Fatalf("output does not explain a malformed key:\n%s", stdout)
	}
	if got := fixture.queueDigest(t); got != before {
		t.Fatal("the queue changed")
	}
}

func TestOutboxResetRefusesAQueueInUse(t *testing.T) {
	t.Parallel()

	fixture := newOutboxResetFixture(t, 1)
	fixture.loseKey()

	// A session holds the queue.
	held, err := outbox.AcquireRunLock(
		context.Background(),
		fixture.dataDir,
	)
	if err != nil {
		t.Fatalf("AcquireRunLock: %v", err)
	}
	defer func() { _ = held.Release() }()

	before := fixture.queueDigest(t)
	configBefore := fixture.configDigest(t)

	code, stdout, _ := fixture.run(t, "--yes")

	if code == 0 {
		t.Fatalf("exit code = 0, want non-zero\n%s", stdout)
	}
	if !strings.Contains(stdout, "Close your other telecli windows") {
		t.Fatalf("output does not tell the user to close the session:\n%s", stdout)
	}
	if got := fixture.queueDigest(t); got != before {
		t.Fatal("the queue changed while another session had it")
	}
	if got := fixture.configDigest(t); got != configBefore {
		t.Fatal("the configuration changed while another session had the queue")
	}
}

func TestOutboxResetRefusesWhenThereIsNoQueue(t *testing.T) {
	t.Parallel()

	fixture := newOutboxResetFixture(t, 0)
	fixture.loseKey()

	if err := os.Remove(
		filepath.Join(fixture.dataDir, "outbox.db"),
	); err != nil {
		t.Fatalf("remove queue: %v", err)
	}

	code, stdout, _ := fixture.run(t, "--yes")

	if code == 0 {
		t.Fatalf("exit code = 0, want non-zero\n%s", stdout)
	}
	if !strings.Contains(stdout, "nothing to reset") {
		t.Fatalf("output does not say there is nothing to do:\n%s", stdout)
	}
	if fixture.config(t).MessageDelivery.DatabaseID != outboxResetOldID {
		t.Fatal("the configuration changed")
	}
}

func TestOutboxResetNeedsAConfigurationFile(t *testing.T) {
	t.Parallel()

	fixture := newOutboxResetFixture(t, 1)
	fixture.loseKey()

	var out, errOut bytes.Buffer

	code := runOutbox(
		[]string{"reset", "--config", filepath.Join(t.TempDir(), "absent.toml")},
		Environment{
			Stdout: &out,
			Stderr: &errOut,
			Stdin:  strings.NewReader(""),
			NewOutboxKeyProvider: func() outbox.KeyProvider {
				return fixture.provider
			},
			OutboxResetOps: fixture.ops,
		},
	)

	if code == 0 {
		t.Fatalf("exit code = 0, want non-zero\n%s", out.String())
	}
	if !strings.Contains(out.String(), "configuration file") {
		t.Fatalf("output does not mention the configuration:\n%s", out.String())
	}
}

// ---- The confirmation ----

// A single keystroke is not a confirmation for something whose messages
// cannot be recovered, so only the word counts.
func TestOutboxResetChangesNothingWithoutTheConfirmationWord(t *testing.T) {
	t.Parallel()

	for _, answer := range []string{"y\n", "yes\n", "\n", "no\n", "RESET ME\n"} {
		t.Run(strings.TrimSpace(answer)+"-answer", func(t *testing.T) {
			t.Parallel()

			fixture := newOutboxResetFixture(t, 2)
			fixture.loseKey()
			fixture.answers = answer

			before := fixture.queueDigest(t)
			configBefore := fixture.configDigest(t)

			code, stdout, _ := fixture.run(t)

			if code != 0 {
				t.Fatalf(
					"exit code = %d, want 0 for a refusal\n%s",
					code,
					stdout,
				)
			}
			if !strings.Contains(stdout, "Nothing was changed") {
				t.Fatalf("output does not say nothing changed:\n%s", stdout)
			}
			if got := fixture.queueDigest(t); got != before {
				t.Fatal("the queue changed without a confirmation")
			}
			if got := fixture.configDigest(t); got != configBefore {
				t.Fatal("the configuration changed without a confirmation")
			}
			if outbox.PendingReset(fixture.dataDir) {
				t.Fatal("a record of the reset was left behind")
			}
		})
	}
}

// Without a terminal there is nobody to read what is about to happen, so
// the command refuses rather than assuming yes.
func TestOutboxResetRefusesWithoutATerminal(t *testing.T) {
	t.Parallel()

	fixture := newOutboxResetFixture(t, 1)
	fixture.loseKey()
	fixture.ops.HasTerminal = func(io.Reader) bool { return false }

	configBefore := fixture.configDigest(t)

	code, stdout, _ := fixture.run(t)

	if code == 0 {
		t.Fatalf("exit code = 0, want non-zero\n%s", stdout)
	}
	if !strings.Contains(stdout, "--yes") {
		t.Fatalf("output does not mention --yes:\n%s", stdout)
	}
	if got := fixture.configDigest(t); got != configBefore {
		t.Fatal("the configuration changed without a terminal")
	}
}

func TestOutboxResetAcceptsTheConfirmationWord(t *testing.T) {
	t.Parallel()

	fixture := newOutboxResetFixture(t, 1)
	fixture.loseKey()
	fixture.answers = "  RESET  \n"

	if code, stdout, _ := fixture.run(t); code != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", code, stdout)
	}
}

// ---- Interruption ----

// Each step of the reset can fail on its own. Whatever fails, a repeated
// run must finish the same reset, and the state in between must never be
// a configuration that names an identity without a key or without the
// backup of the queue it replaced.
func TestOutboxResetResumesAfterAFailureAtEveryStep(t *testing.T) {
	t.Parallel()

	steps := map[string]func(*outboxResetFixture) OutboxResetOperations{
		"recording the reset": func(f *outboxResetFixture) OutboxResetOperations {
			return OutboxResetOperations{
				WriteJournal: func(string, outbox.ResetJournal) error {
					return errors.New("disk is full")
				},
			}
		},
		"moving the queue aside": func(f *outboxResetFixture) OutboxResetOperations {
			return OutboxResetOperations{
				OrphanQueue: func(string, outbox.ResetJournal) error {
					return errors.New("rename failed")
				},
			}
		},
		"creating the key": func(f *outboxResetFixture) OutboxResetOperations {
			return OutboxResetOperations{
				CreateKey: func(
					context.Context,
					outbox.KeyProvider,
					string,
				) error {
					return errors.New("key store refused")
				},
			}
		},
		"writing the configuration": func(f *outboxResetFixture) OutboxResetOperations {
			return OutboxResetOperations{
				WriteConfig: func(string, config.Config) error {
					return errors.New("config is read only")
				},
			}
		},
		"removing the record": func(f *outboxResetFixture) OutboxResetOperations {
			return OutboxResetOperations{
				ClearJournal: func(string) error {
					return errors.New("permission denied")
				},
			}
		},
	}

	for name, breakStep := range steps {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			fixture := newOutboxResetFixture(t, 2)
			fixture.loseKey()

			working := fixture.ops
			broken := breakStep(fixture)

			fixture.ops = withBrokenStep(working, broken)

			code, stdout, _ := fixture.run(t, "--yes")
			if code == 0 {
				t.Fatalf(
					"exit code = 0, want non-zero for a failed step\n%s",
					stdout,
				)
			}
			if !strings.Contains(stdout, "again to finish it") {
				t.Fatalf("output does not say how to finish:\n%s", stdout)
			}

			assertNoHalfSwappedQueue(t, fixture)

			// The second run must finish the same reset.
			fixture.ops = working

			code, stdout, stderr := fixture.run(t, "--yes")
			if code != 0 {
				t.Fatalf(
					"second run exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s",
					code,
					stdout,
					stderr,
				)
			}

			if outbox.PendingReset(fixture.dataDir) {
				t.Fatal("the record of the reset was left behind")
			}
			if got := fixture.config(t).MessageDelivery.DatabaseID; got != outboxResetNewID {
				t.Fatalf("database_id = %q, want %q", got, outboxResetNewID)
			}
			if _, err := os.Stat(
				filepath.Join(
					fixture.dataDir,
					fixture.backupName(t),
				),
			); err != nil {
				t.Fatalf("the old queue is gone: %v", err)
			}

			opened, err := outbox.Open(
				context.Background(),
				outbox.Config{
					DataDir:    fixture.dataDir,
					DatabaseID: outboxResetNewID,
					InstanceID: "fixture",
				},
				outbox.Deps{KeyProvider: fixture.provider},
			)
			if err != nil {
				t.Fatalf("open the new queue: %v", err)
			}
			if err := opened.Close(); err != nil {
				t.Fatalf("close the new queue: %v", err)
			}
		})
	}
}

// A queue whose key is gone must stay unopenable after an interrupted
// reset, or a start would invent a new key for the old identity and the
// user would never learn that the reset was not finished.
func TestOpenRefusesTheQueueWhileAResetIsUnfinished(t *testing.T) {
	t.Parallel()

	fixture := newOutboxResetFixture(t, 1)
	fixture.loseKey()
	fixture.ops.CreateKey = func(
		context.Context,
		outbox.KeyProvider,
		string,
	) error {
		return errors.New("key store refused")
	}

	if code, stdout, _ := fixture.run(t, "--yes"); code == 0 {
		t.Fatalf("exit code = 0, want non-zero\n%s", stdout)
	}

	_, err := outbox.Open(
		context.Background(),
		outbox.Config{
			DataDir:    fixture.dataDir,
			DatabaseID: outboxResetOldID,
		},
		outbox.Deps{KeyProvider: fixture.provider},
	)
	if !errors.Is(err, outbox.ErrOutboxResetPending) {
		t.Fatalf("error = %v, want ErrOutboxResetPending", err)
	}
}

// A record of a finished reset is only a leftover: clearing it must not
// touch the new queue, which by then is the live one.
func TestOutboxResetClearsALeftoverRecord(t *testing.T) {
	t.Parallel()

	fixture := newOutboxResetFixture(t, 1)
	fixture.loseKey()

	if code, stdout, _ := fixture.run(t, "--yes"); code != 0 {
		t.Fatalf("first run exit code = %d, want 0\n%s", code, stdout)
	}

	// A record that survived a crash after the configuration write.
	leftover := outbox.ResetJournal{
		OldDatabaseID: outboxResetOldID,
		NewDatabaseID: outboxResetNewID,
		BackupName:    "outbox.db.orphaned-20260927T101500Z",
		NoteName:      "outbox.db.orphaned-20260927T101500Z.txt",
		StartedAt:     outboxResetTime,
	}
	if err := outbox.WriteResetJournal(
		fixture.dataDir,
		leftover,
	); err != nil {
		t.Fatalf("write leftover record: %v", err)
	}

	// Put a message in the new queue, so a run that moved it aside would
	// be visible.
	opened, err := outbox.Open(
		context.Background(),
		outbox.Config{
			DataDir:    fixture.dataDir,
			DatabaseID: outboxResetNewID,
			InstanceID: "fixture",
		},
		outbox.Deps{KeyProvider: fixture.provider},
	)
	if err != nil {
		t.Fatalf("open the new queue: %v", err)
	}
	if err := opened.Store.Enqueue(
		context.Background(),
		outbox.Entry{
			ID:         "new-entry",
			AccountKey: "account",
			ChatID:     4242,
			Text:       "in the new queue",
			State:      outbox.StateQueued,
			CreatedAt:  outboxResetTime,
			UpdatedAt:  outboxResetTime,
		},
	); err != nil {
		_ = opened.Close()
		t.Fatalf("enqueue: %v", err)
	}
	if err := opened.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	before := fixture.queueDigest(t)

	code, stdout, _ := fixture.run(t, "--yes")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", code, stdout)
	}
	if outbox.PendingReset(fixture.dataDir) {
		t.Fatal("the leftover record was not cleared")
	}
	if got := fixture.queueDigest(t); got != before {
		t.Fatal("the new queue was touched")
	}
}

// An attempt that moved nothing has not changed anything, so a refusal
// after it must leave no record: a record would keep every later start
// of telecli from opening a queue.
func TestOutboxResetDropsTheRecordWhenTheUserRefuses(t *testing.T) {
	t.Parallel()

	fixture := newOutboxResetFixture(t, 1)
	fixture.loseKey()

	// A record of an attempt that was interrupted before it moved
	// anything.
	journal := outbox.ResetJournal{
		OldDatabaseID: outboxResetOldID,
		NewDatabaseID: outboxResetNewID,
		BackupName:    "outbox.db.orphaned-20260927T101500Z",
		NoteName:      "outbox.db.orphaned-20260927T101500Z.txt",
		StartedAt:     outboxResetTime,
	}
	if err := outbox.WriteResetJournal(
		fixture.dataDir,
		journal,
	); err != nil {
		t.Fatalf("write record: %v", err)
	}

	fixture.answers = "n\n"

	code, stdout, _ := fixture.run(t)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", code, stdout)
	}
	if outbox.PendingReset(fixture.dataDir) {
		t.Fatal("the record of an unfinished attempt was left behind")
	}
	if !outbox.DatabaseExists(fixture.dataDir) {
		t.Fatal("the queue was moved")
	}
	if got := fixture.config(t).MessageDelivery.DatabaseID; got != outboxResetOldID {
		t.Fatalf("database_id = %q, want the old one", got)
	}
}

// A record of an unfinished reset whose queue file is gone from outside
// this command is still finished rather than left to the user: there is
// nothing to keep, and refusing would mean a queue that cannot be opened
// and a file to delete by hand.
func TestOutboxResetFinishesWhenTheQueueFileIsGone(t *testing.T) {
	t.Parallel()

	fixture := newOutboxResetFixture(t, 1)
	fixture.loseKey()

	if err := outbox.WriteResetJournal(
		fixture.dataDir,
		outbox.ResetJournal{
			OldDatabaseID: outboxResetOldID,
			NewDatabaseID: outboxResetNewID,
			BackupName:    "outbox.db.orphaned-20260927T101500Z",
			NoteName:      "outbox.db.orphaned-20260927T101500Z.txt",
			StartedAt:     outboxResetTime,
		},
	); err != nil {
		t.Fatalf("write record: %v", err)
	}

	if err := os.Remove(
		filepath.Join(fixture.dataDir, "outbox.db"),
	); err != nil {
		t.Fatalf("remove queue: %v", err)
	}

	code, stdout, stderr := fixture.run(t, "--yes")

	if code != 0 {
		t.Fatalf(
			"exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s",
			code,
			stdout,
			stderr,
		)
	}
	if !strings.Contains(stdout, "nothing to keep") {
		t.Fatalf("output does not say there was nothing to keep:\n%s", stdout)
	}
	if got := fixture.config(t).MessageDelivery.DatabaseID; got != outboxResetNewID {
		t.Fatalf("database_id = %q, want %q", got, outboxResetNewID)
	}
	if outbox.PendingReset(fixture.dataDir) {
		t.Fatal("the record of the reset was left behind")
	}

	opened, err := outbox.Open(
		context.Background(),
		outbox.Config{
			DataDir:    fixture.dataDir,
			DatabaseID: outboxResetNewID,
			InstanceID: "fixture",
		},
		outbox.Deps{KeyProvider: fixture.provider},
	)
	if err != nil {
		t.Fatalf("open the new queue: %v", err)
	}
	if err := opened.Close(); err != nil {
		t.Fatalf("close the new queue: %v", err)
	}
}

// A record that names a queue the configuration no longer points at is
// not this command's business.
func TestOutboxResetRefusesAChangedConfiguration(t *testing.T) {
	t.Parallel()

	fixture := newOutboxResetFixture(t, 1)
	fixture.loseKey()

	if err := outbox.WriteResetJournal(
		fixture.dataDir,
		outbox.ResetJournal{
			OldDatabaseID: "some-other-identity",
			NewDatabaseID: outboxResetNewID,
			BackupName:    "outbox.db.orphaned-20260927T101500Z",
			NoteName:      "outbox.db.orphaned-20260927T101500Z.txt",
			StartedAt:     outboxResetTime,
		},
	); err != nil {
		t.Fatalf("write record: %v", err)
	}

	before := fixture.queueDigest(t)

	code, stdout, _ := fixture.run(t, "--yes")

	if code == 0 {
		t.Fatalf("exit code = 0, want non-zero\n%s", stdout)
	}
	if got := fixture.queueDigest(t); got != before {
		t.Fatal("the queue changed")
	}
}

// ---- What the user is told elsewhere ----

// The screen and telecli doctor must point at the command, or a user
// with a missing key would have no way out.
func TestMissingKeyAdvicePointsAtTheResetCommand(t *testing.T) {
	t.Parallel()

	report := DescribeSendingPaused(
		outbox.ErrOutboxKeyUnavailable,
		"/tmp/queue",
	)
	if !strings.Contains(report, "telecli outbox reset") {
		t.Fatalf("doctor does not advise the reset:\n%s", report)
	}

	// An unfinished reset is the same case with the same remedy.
	if reason := classifySendingPaused(
		outbox.ErrOutboxResetPending,
	); reason != SendingPausedKeyMissing {
		t.Fatalf("reason = %v, want key missing", reason)
	}

	hint := SendingPausedKeyMissing.Hint()
	if !strings.Contains(hint, "telecli outbox reset") {
		t.Fatalf("hint does not advise the reset: %q", hint)
	}
}

// ---- The command line ----

func TestOutboxWithoutASubcommandPrintsUsage(t *testing.T) {
	t.Parallel()

	var out, errOut bytes.Buffer

	if code := runOutbox(nil, Environment{
		Stdout: &out,
		Stderr: &errOut,
	}); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "telecli outbox reset") {
		t.Fatalf("usage does not mention the reset:\n%s", out.String())
	}
}

func TestOutboxRejectsAnUnknownSubcommand(t *testing.T) {
	t.Parallel()

	var out, errOut bytes.Buffer

	if code := runOutbox([]string{"wipe"}, Environment{
		Stdout: &out,
		Stderr: &errOut,
	}); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
}

func TestMainRoutesTheOutboxCommand(t *testing.T) {
	t.Parallel()

	var out, errOut bytes.Buffer

	code := Main(
		[]string{"telecli", "outbox"},
		Environment{Stdout: &out, Stderr: &errOut},
	)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "telecli outbox reset") {
		t.Fatalf("usage does not mention the reset:\n%s", out.String())
	}
}

func TestMainHelpMentionsTheResetCommand(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	if code := Main(
		[]string{"telecli", "--help"},
		Environment{Stdout: &out, Stderr: &out},
	); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "telecli outbox reset") {
		t.Fatalf("help does not mention the reset:\n%s", out.String())
	}
}

// ---- Helpers ----

// withBrokenStep keeps the working operations and replaces the step a
// test wants to fail.
func withBrokenStep(
	working OutboxResetOperations,
	broken OutboxResetOperations,
) OutboxResetOperations {
	merged := working

	if broken.InspectKey != nil {
		merged.InspectKey = broken.InspectKey
	}
	if broken.InspectQueue != nil {
		merged.InspectQueue = broken.InspectQueue
	}
	if broken.AcquireLock != nil {
		merged.AcquireLock = broken.AcquireLock
	}
	if broken.ReadJournal != nil {
		merged.ReadJournal = broken.ReadJournal
	}
	if broken.WriteJournal != nil {
		merged.WriteJournal = broken.WriteJournal
	}
	if broken.ClearJournal != nil {
		merged.ClearJournal = broken.ClearJournal
	}
	if broken.OrphanQueue != nil {
		merged.OrphanQueue = broken.OrphanQueue
	}
	if broken.CreateKey != nil {
		merged.CreateKey = broken.CreateKey
	}
	if broken.WriteConfig != nil {
		merged.WriteConfig = broken.WriteConfig
	}
	if broken.NewDatabaseID != nil {
		merged.NewDatabaseID = broken.NewDatabaseID
	}
	if broken.Now != nil {
		merged.Now = broken.Now
	}
	if broken.HasTerminal != nil {
		merged.HasTerminal = broken.HasTerminal
	}

	return merged
}

// assertNoHalfSwappedQueue checks the two states an interrupted reset
// must never leave behind: a configuration that names an identity with
// no key, and a configuration that names a new identity while the queue
// it replaced was never kept.
func assertNoHalfSwappedQueue(
	t *testing.T,
	fixture *outboxResetFixture,
) {
	t.Helper()

	databaseID := fixture.config(t).MessageDelivery.DatabaseID

	if databaseID == outboxResetOldID {
		// The old identity is still configured. Then either the old
		// queue is still the live one, or a record of the reset says it
		// was moved aside. Without that record a start would create a
		// fresh key for the old identity and the user would never learn
		// that the reset was not finished.
		if outbox.DatabaseExists(fixture.dataDir) {
			return
		}
		if !outbox.PendingReset(fixture.dataDir) {
			t.Fatal("the queue is gone with no record of the reset")
		}

		_, err := outbox.Open(
			context.Background(),
			outbox.Config{
				DataDir:    fixture.dataDir,
				DatabaseID: databaseID,
			},
			outbox.Deps{KeyProvider: fixture.provider},
		)
		if !errors.Is(err, outbox.ErrOutboxResetPending) {
			t.Fatalf("a start would not notice the reset: %v", err)
		}

		return
	}

	if databaseID != outboxResetNewID {
		t.Fatalf("database_id = %q, want one of the two identities", databaseID)
	}

	// The new identity is configured, so its key must exist: without it
	// the next start invents a queue nobody asked for.
	if err := outbox.CreateQueueKey(
		context.Background(),
		fixture.provider,
		databaseID,
	); err != nil && !errors.Is(err, outbox.ErrOutboxKeyExists) {
		t.Fatalf("the new identity has no key: %v", err)
	}

	// And the queue it replaced must be somewhere.
	backups, err := filepath.Glob(
		filepath.Join(fixture.dataDir, "outbox.db.orphaned-*Z"),
	)
	if err != nil {
		t.Fatalf("glob backups: %v", err)
	}
	if len(backups) == 0 && !outbox.DatabaseExists(fixture.dataDir) {
		t.Fatal("the configuration names a new queue and the old one is gone")
	}
}

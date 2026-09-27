package outbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

var (
	// ErrOutboxInvalidConfig is returned when Open receives an
	// incomplete or inconsistent Config or Deps value.
	ErrOutboxInvalidConfig = errors.New(
		"outbox: invalid config",
	)

	// ErrOutboxInconsistentInit is returned when initialization has
	// already created the database path but the data-encryption key is
	// missing.
	//
	// Open never creates a replacement key for an existing database
	// path, including a zero-byte database file.
	ErrOutboxInconsistentInit = errors.New(
		"outbox: inconsistent initialization",
	)

	// ErrOutboxDataDirInvalid is returned when DataDir is not a usable
	// directory, is owned by another user, or is a symbolic link.
	ErrOutboxDataDirInvalid = errors.New(
		"outbox: data directory is invalid",
	)

	// ErrOutboxDataDirInsecure is returned when DataDir permissions
	// grant access to group or other users.
	ErrOutboxDataDirInsecure = errors.New(
		"outbox: data directory has insecure permissions",
	)
)

// outboxDatabaseFileName is the SQLite database file inside DataDir.
const outboxDatabaseFileName = "outbox.db"

// DatabaseExists reports whether the queue database is already present in
// dataDir.
//
// It exists so a read-only caller can tell an existing queue from a clean
// install without calling Open, which would create the directory, the
// database and a key. The file name stays here so the two cannot drift.
func DatabaseExists(dataDir string) bool {
	if strings.TrimSpace(dataDir) == "" {
		return false
	}
	info, err := os.Lstat(filepath.Join(dataDir, outboxDatabaseFileName))
	return err == nil && info.Mode().IsRegular()
}

// Config describes a durable outbox instance.
type Config struct {
	// DataDir holds the SQLite database and its sidecars.
	//
	// Open creates it with mode 0700 when it does not exist. Existing
	// symbolic links, non-directories, directories owned by another
	// user, and directories accessible by group or other users are
	// rejected.
	DataDir string

	// DatabaseID scopes the data-encryption key inside KeyProvider.
	//
	// It is an application-chosen opaque string, not a credential.
	DatabaseID string

	// InstanceID is used when Dispatcher.InstanceID is empty.
	//
	// An explicit Dispatcher.InstanceID takes precedence.
	InstanceID string

	// Dispatcher configures the dispatcher. When Deps.Sender is nil,
	// no dispatcher is created and this field is ignored.
	Dispatcher DispatcherConfig
}

// Deps carries process-wide dependencies for Open.
//
// KeyProvider is required. Sender is optional: when nil, Open returns
// a Store-only outbox. Clock may be nil and defaults to SystemClock.
type Deps struct {
	KeyProvider KeyProvider
	Sender      Sender
	Clock       Clock

	// Exclusive asks Open to hold the run lock of the data directory
	// for the lifetime of the returned Outbox, and to fail with
	// ErrOutboxQueueInUse when another telecli process already holds
	// the queue.
	//
	// It is opt-in per caller. A session that will send messages sets
	// it, so nothing else can move the queue while it runs. A read-only
	// probe leaves it false: a diagnostic must keep working while a
	// session is open, and must not block one either.
	Exclusive bool
}

// Outbox is the assembled durable outbox.
type Outbox struct {
	Store      Store
	Dispatcher *Dispatcher
	Cipher     PayloadCipher

	closer  io.Closer
	runLock RunLock
}

// Close releases the underlying SQLite handle.
//
// Close is idempotent.
func (o *Outbox) Close() error {
	if o == nil {
		return nil
	}

	closer := o.closer
	o.closer = nil

	runLock := o.runLock
	o.runLock = nil

	var errs []error
	if closer != nil {
		errs = append(errs, closer.Close())
	}
	if runLock != nil {
		errs = append(errs, runLock.Release())
	}

	switch len(errs) {
	case 0:
		return nil
	case 1:
		return errs[0]
	default:
		return errors.Join(errs...)
	}
}

// Open assembles a durable outbox.
//
// Sequence:
//
//  1. Validate Config and Deps.
//  2. Prepare DataDir and verify its type, ownership, and permissions.
//  3. Take the run lock when Deps.Exclusive is set.
//  4. Inspect the database path without following symbolic links.
//  5. Resolve the data-encryption key.
//  6. Build AEADCipher and open the SQLite Store.
//  7. Build Dispatcher when Deps.Sender is non-nil.
//
// Fail-closed behavior:
//
//   - existing database path plus missing key returns both
//     ErrOutboxInconsistentInit and ErrOutboxKeyUnavailable;
//   - a zero-byte database file is still considered an existing,
//     partially initialized database;
//   - a non-regular database path is rejected;
//   - key-provider failures never fall back to plaintext or memory;
//   - a queue with an unfinished reset is never created from scratch;
//   - insecure, foreign-owned, or symlinked DataDir values are
//     rejected.
func Open(
	ctx context.Context,
	cfg Config,
	deps Deps,
) (opened *Outbox, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	if deps.KeyProvider == nil {
		return nil, fmt.Errorf(
			"%w: nil key provider",
			ErrOutboxInvalidConfig,
		)
	}

	if deps.Clock == nil {
		deps.Clock = SystemClock{}
	}

	if err := prepareDataDir(
		cfg.DataDir,
	); err != nil {
		return nil, err
	}

	var runLock RunLock
	if deps.Exclusive {
		acquired, lockErr := AcquireRunLock(ctx, cfg.DataDir)
		if lockErr != nil {
			return nil, lockErr
		}
		runLock = acquired

		// Every failure below must hand the queue back, or a refused
		// Open would leave a session that never started holding the
		// only lock that says the queue is free.
		defer func() {
			if err != nil {
				_ = runLock.Release()
			}
		}()
	}

	dbPath := filepath.Join(
		cfg.DataDir,
		outboxDatabaseFileName,
	)

	key, err := resolveKey(
		ctx,
		cfg,
		deps.KeyProvider,
		dbPath,
	)
	if err != nil {
		return nil, err
	}
	defer clearBytes(key)

	payloadCipher, err := NewAEADCipher(key)
	if err != nil {
		return nil, fmt.Errorf(
			"outbox: build cipher: %w",
			err,
		)
	}

	store, err := NewSQLiteStore(
		ctx,
		SQLiteStoreConfig{
			Path: dbPath,
		},
		payloadCipher,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"outbox: open store: %w",
			err,
		)
	}

	closer, ok := store.(io.Closer)
	if !ok {
		if closeable, closeOK := store.(interface{ Close() error }); closeOK {
			_ = closeable.Close()
		}

		return nil, fmt.Errorf(
			"%w: store does not implement io.Closer",
			ErrOutboxInvalidConfig,
		)
	}

	result := &Outbox{
		Store:   store,
		Cipher:  payloadCipher,
		closer:  closer,
		runLock: runLock,
	}

	if deps.Sender != nil {
		dispatcherConfig := cfg.Dispatcher

		if dispatcherConfig.InstanceID == "" {
			dispatcherConfig.InstanceID =
				cfg.InstanceID
		}

		if dispatcherConfig.InstanceID == "" {
			dispatcherConfig.InstanceID =
				"dispatcher"
		}

		if dispatcherConfig.Logger == nil {
			dispatcherConfig.Logger =
				slog.Default()
		}

		result.Dispatcher = NewDispatcher(
			store,
			deps.Sender,
			deps.Clock,
			dispatcherConfig,
		)
	}

	return result, nil
}

// validate rejects an unusable Config.
func (c Config) validate() error {
	if c.DataDir == "" {
		return fmt.Errorf(
			"%w: empty data dir",
			ErrOutboxInvalidConfig,
		)
	}

	if err := validateDatabaseID(
		c.DatabaseID,
	); err != nil {
		return fmt.Errorf(
			"%w: database id: %v",
			ErrOutboxInvalidConfig,
			err,
		)
	}

	return nil
}

// prepareDataDir creates DataDir with mode 0700 when missing and
// validates the resulting path without following a final symbolic
// link.
func prepareDataDir(
	dir string,
) error {
	if err := os.MkdirAll(
		dir,
		0o700,
	); err != nil {
		return fmt.Errorf(
			"%w: create data dir: %v",
			ErrOutboxDataDirInvalid,
			err,
		)
	}

	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf(
			"%w: inspect data dir: %v",
			ErrOutboxDataDirInvalid,
			err,
		)
	}

	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf(
			"%w: symbolic links are not allowed",
			ErrOutboxDataDirInvalid,
		)
	}

	if !info.IsDir() {
		return fmt.Errorf(
			"%w: path is not a directory",
			ErrOutboxDataDirInvalid,
		)
	}

	if err := validateDataDirOwner(
		info,
	); err != nil {
		return err
	}

	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf(
			"%w: permissions %o",
			ErrOutboxDataDirInsecure,
			info.Mode().Perm(),
		)
	}

	return nil
}

// resolveKey returns the data-encryption key.
//
// The database path is the source of truth for whether initialization
// has already started:
//
//   - database path absent plus key absent calls CreateKey;
//   - database path absent plus key present reuses the existing key;
//   - database path present plus key present reuses the key;
//   - any database path present plus key absent fails closed;
//   - a non-regular database path is inconsistent initialization;
//   - an unfinished reset fails closed instead of creating a key.
func resolveKey(
	ctx context.Context,
	cfg Config,
	provider KeyProvider,
	dbPath string,
) ([]byte, error) {
	dbExists, err := databasePathState(dbPath)
	if err != nil {
		return nil, err
	}

	key, err := provider.LoadKey(
		ctx,
		cfg.DatabaseID,
	)

	switch {
	case err == nil:
		validated, validationErr :=
			validateLoadedKey(key)
		clearBytes(key)

		if validationErr != nil {
			return nil, validationErr
		}

		return validated, nil

	case errors.Is(
		err,
		ErrOutboxKeyUnavailable,
	):
		// Continue to create-or-fail logic.

	default:
		return nil, fmt.Errorf(
			"outbox: load key: %w",
			err,
		)
	}

	if dbExists {
		return nil, fmt.Errorf(
			"%w: %w: database path exists but key is unavailable",
			ErrOutboxInconsistentInit,
			ErrOutboxKeyUnavailable,
		)
	}

	// A reset that was started and not finished must not be undone by a
	// start. Creating a key here would give the old identity a fresh
	// key, and the user would see a working, empty queue instead of the
	// reset they were told to run.
	if PendingReset(cfg.DataDir) {
		return nil, fmt.Errorf(
			"%w: run telecli outbox reset to finish it",
			ErrOutboxResetPending,
		)
	}

	key, err = provider.CreateKey(
		ctx,
		cfg.DatabaseID,
	)
	if err == nil {
		validated, validationErr :=
			validateLoadedKey(key)
		clearBytes(key)

		if validationErr != nil {
			return nil, fmt.Errorf(
				"%w: provider returned malformed created key: %v",
				ErrOutboxKeyCreationFailed,
				validationErr,
			)
		}

		return validated, nil
	}

	if !errors.Is(
		err,
		ErrOutboxKeyExists,
	) {
		return nil, fmt.Errorf(
			"outbox: create key: %w",
			err,
		)
	}

	// Another cooperating process created the key first. Load its key
	// instead of generating a replacement.
	key, err = provider.LoadKey(
		ctx,
		cfg.DatabaseID,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"outbox: reload key after create race: %w",
			err,
		)
	}

	validated, validationErr :=
		validateLoadedKey(key)
	clearBytes(key)

	if validationErr != nil {
		return nil, validationErr
	}

	return validated, nil
}

// databasePathState determines whether initialization has created the
// SQLite database path.
//
// It deliberately treats a zero-byte regular file as existing. Such a
// file may represent interrupted initialization and must never cause
// Open to create a replacement key.
func databasePathState(
	path string,
) (bool, error) {
	info, err := os.Lstat(path)

	switch {
	case err == nil:
		if info.Mode()&os.ModeSymlink != 0 {
			return true, fmt.Errorf(
				"%w: database path is a symbolic link",
				ErrOutboxInconsistentInit,
			)
		}

		if !info.Mode().IsRegular() {
			return true, fmt.Errorf(
				"%w: database path is not a regular file",
				ErrOutboxInconsistentInit,
			)
		}

		return true, nil

	case errors.Is(
		err,
		os.ErrNotExist,
	):
		return false, nil

	default:
		return false, fmt.Errorf(
			"outbox: inspect database path: %w",
			err,
		)
	}
}

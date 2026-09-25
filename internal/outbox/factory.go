package outbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
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
}

// Outbox is the assembled durable outbox.
type Outbox struct {
	Store      Store
	Dispatcher *Dispatcher
	Cipher     PayloadCipher

	closer io.Closer
}

// Close releases the underlying SQLite handle.
//
// Close is idempotent.
func (o *Outbox) Close() error {
	if o == nil || o.closer == nil {
		return nil
	}

	closer := o.closer
	o.closer = nil

	return closer.Close()
}

// Open assembles a durable outbox.
//
// Sequence:
//
//  1. Validate Config and Deps.
//  2. Prepare DataDir and verify its type, ownership, and permissions.
//  3. Inspect the database path without following symbolic links.
//  4. Resolve the data-encryption key.
//  5. Build AEADCipher and open the SQLite Store.
//  6. Build Dispatcher when Deps.Sender is non-nil.
//
// Fail-closed behavior:
//
//   - existing database path plus missing key returns both
//     ErrOutboxInconsistentInit and ErrOutboxKeyUnavailable;
//   - a zero-byte database file is still considered an existing,
//     partially initialized database;
//   - a non-regular database path is rejected;
//   - key-provider failures never fall back to plaintext or memory;
//   - insecure, foreign-owned, or symlinked DataDir values are
//     rejected.
func Open(
	ctx context.Context,
	cfg Config,
	deps Deps,
) (*Outbox, error) {
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
		Store:  store,
		Cipher: payloadCipher,
		closer: closer,
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
//   - regular database path present plus key present reuses the key;
//   - any database path present plus key absent fails closed;
//   - a non-regular database path is inconsistent initialization.
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

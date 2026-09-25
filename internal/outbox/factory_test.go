package outbox

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// factoryKeyProvider is a controllable KeyProvider fixture.
//
// Unlike fakeKeyProvider from key_provider_test.go, this fixture allows
// tests to script LoadKey and CreateKey results independently and to
// assert call counts.
type factoryKeyProvider struct {
	keys map[string][]byte

	loadErr   error
	createErr error
	createKey []byte

	loadCalls   int
	createCalls int
}

func newFactoryKeyProvider() *factoryKeyProvider {
	return &factoryKeyProvider{
		keys: make(
			map[string][]byte,
		),
	}
}

func (p *factoryKeyProvider) LoadKey(
	ctx context.Context,
	databaseID string,
) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if err := validateDatabaseID(
		databaseID,
	); err != nil {
		return nil, err
	}

	p.loadCalls++

	if p.loadErr != nil {
		return nil, p.loadErr
	}

	key, ok := p.keys[databaseID]
	if !ok {
		return nil, ErrOutboxKeyUnavailable
	}

	return append(
		[]byte(nil),
		key...,
	), nil
}

func (p *factoryKeyProvider) CreateKey(
	ctx context.Context,
	databaseID string,
) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if err := validateDatabaseID(
		databaseID,
	); err != nil {
		return nil, err
	}

	p.createCalls++

	if p.createErr != nil {
		return nil, p.createErr
	}

	if _, exists := p.keys[databaseID]; exists {
		return nil, ErrOutboxKeyExists
	}

	key := p.createKey
	if key == nil {
		key = make(
			[]byte,
			outboxDataEncryptionKeySize,
		)

		for index := range key {
			key[index] = byte(
				index + 1,
			)
		}
	}

	p.keys[databaseID] = append(
		[]byte(nil),
		key...,
	)

	return append(
		[]byte(nil),
		key...,
	), nil
}

var _ KeyProvider = (*factoryKeyProvider)(nil)

func newFactoryConfig(
	t *testing.T,
) Config {
	t.Helper()

	dir := t.TempDir()

	// t.TempDir() may return a directory that is world-readable on
	// some platforms; prepareDataDir rejects such directories by
	// design. Normalize the fixture to the production permission.
	if err := os.Chmod(
		dir,
		0o700,
	); err != nil {
		t.Fatalf(
			"chmod data dir: %v",
			err,
		)
	}

	return Config{
		DataDir:    dir,
		DatabaseID: "primary",
		InstanceID: "test-instance",
	}
}

type factorySender struct{}

func (factorySender) SendMessage(
	_ context.Context,
	_ int64,
	_ string,
) (SentMessage, error) {
	return SentMessage{}, errors.New(
		"factory sender: not used",
	)
}

var _ Sender = factorySender{}

// ---- Config validation ----

func TestOpenRejectsEmptyDataDir(
	t *testing.T,
) {
	cfg := Config{
		DatabaseID: "primary",
	}

	_, err := Open(
		context.Background(),
		cfg,
		Deps{
			KeyProvider: newFactoryKeyProvider(),
		},
	)

	if !errors.Is(
		err,
		ErrOutboxInvalidConfig,
	) {
		t.Fatalf(
			"error = %v, want ErrOutboxInvalidConfig",
			err,
		)
	}
}

func TestOpenRejectsEmptyDatabaseID(
	t *testing.T,
) {
	cfg := Config{
		DataDir: t.TempDir(),
	}

	_, err := Open(
		context.Background(),
		cfg,
		Deps{
			KeyProvider: newFactoryKeyProvider(),
		},
	)

	if !errors.Is(
		err,
		ErrOutboxInvalidConfig,
	) {
		t.Fatalf(
			"error = %v, want ErrOutboxInvalidConfig",
			err,
		)
	}
}

func TestOpenRejectsWhitespaceDatabaseID(
	t *testing.T,
) {
	cfg := Config{
		DataDir:    t.TempDir(),
		DatabaseID: "  \t\n ",
	}

	_, err := Open(
		context.Background(),
		cfg,
		Deps{
			KeyProvider: newFactoryKeyProvider(),
		},
	)

	if !errors.Is(
		err,
		ErrOutboxInvalidConfig,
	) {
		t.Fatalf(
			"error = %v, want ErrOutboxInvalidConfig",
			err,
		)
	}
}

func TestOpenRejectsNilKeyProvider(
	t *testing.T,
) {
	cfg := newFactoryConfig(t)

	_, err := Open(
		context.Background(),
		cfg,
		Deps{},
	)

	if !errors.Is(
		err,
		ErrOutboxInvalidConfig,
	) {
		t.Fatalf(
			"error = %v, want ErrOutboxInvalidConfig",
			err,
		)
	}
}

func TestOpenRejectsCanceledContext(
	t *testing.T,
) {
	cfg := newFactoryConfig(t)

	ctx, cancel := context.WithCancel(
		context.Background(),
	)
	cancel()

	_, err := Open(
		ctx,
		cfg,
		Deps{
			KeyProvider: newFactoryKeyProvider(),
		},
	)

	if !errors.Is(
		err,
		context.Canceled,
	) {
		t.Fatalf(
			"error = %v, want context.Canceled",
			err,
		)
	}
}

// ---- New outbox ----

func TestOpenCreatesNewOutboxAndKey(
	t *testing.T,
) {
	cfg := newFactoryConfig(t)
	provider := newFactoryKeyProvider()

	box, err := Open(
		context.Background(),
		cfg,
		Deps{
			KeyProvider: provider,
		},
	)
	if err != nil {
		t.Fatalf(
			"Open: %v",
			err,
		)
	}
	defer func() {
		_ = box.Close()
	}()

	if box.Store == nil {
		t.Fatal("Store is nil")
	}
	if box.Cipher == nil {
		t.Fatal("Cipher is nil")
	}
	if box.Dispatcher != nil {
		t.Fatal(
			"Dispatcher should be nil when Sender is nil",
		)
	}

	if provider.createCalls != 1 {
		t.Fatalf(
			"CreateKey calls = %d, want 1",
			provider.createCalls,
		)
	}

	key, err := provider.LoadKey(
		context.Background(),
		cfg.DatabaseID,
	)
	if err != nil {
		t.Fatalf(
			"LoadKey: %v",
			err,
		)
	}
	if len(key) !=
		outboxDataEncryptionKeySize {
		t.Fatalf(
			"key length = %d, want %d",
			len(key),
			outboxDataEncryptionKeySize,
		)
	}

	dbPath := filepath.Join(
		cfg.DataDir,
		outboxDatabaseFileName,
	)

	exists, err := databasePathState(
		dbPath,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Fatalf(
			"database file not created at %s",
			dbPath,
		)
	}
}

// ---- Reopen ----

func TestOpenReopensExistingOutbox(
	t *testing.T,
) {
	cfg := newFactoryConfig(t)
	provider := newFactoryKeyProvider()

	deps := Deps{
		KeyProvider: provider,
	}

	first, err := Open(
		context.Background(),
		cfg,
		deps,
	)
	if err != nil {
		t.Fatalf(
			"first Open: %v",
			err,
		)
	}

	entry := queuedEntry(
		"op-1",
		42,
		"hello",
	)

	if err := first.Store.Enqueue(
		context.Background(),
		entry,
	); err != nil {
		_ = first.Close()
		t.Fatalf(
			"Enqueue: %v",
			err,
		)
	}

	if err := first.Close(); err != nil {
		t.Fatalf(
			"first Close: %v",
			err,
		)
	}

	second, err := Open(
		context.Background(),
		cfg,
		deps,
	)
	if err != nil {
		t.Fatalf(
			"second Open: %v",
			err,
		)
	}
	defer func() {
		_ = second.Close()
	}()

	got, err := second.Store.Get(
		context.Background(),
		entry.ID,
	)
	if err != nil {
		t.Fatalf(
			"Get after reopen: %v",
			err,
		)
	}

	if got.Text != "hello" {
		t.Fatalf(
			"Text = %q, want hello",
			got.Text,
		)
	}

	if got.State != StateQueued {
		t.Fatalf(
			"State = %s, want queued",
			got.State,
		)
	}

	if provider.createCalls != 1 {
		t.Fatalf(
			"CreateKey calls = %d, want 1",
			provider.createCalls,
		)
	}
}

// ---- Incomplete initialization ----

func TestOpenRejectsExistingDatabaseWithoutKey(
	t *testing.T,
) {
	cfg := newFactoryConfig(t)
	provider := newFactoryKeyProvider()

	first, err := Open(
		context.Background(),
		cfg,
		Deps{
			KeyProvider: provider,
		},
	)
	if err != nil {
		t.Fatalf(
			"first Open: %v",
			err,
		)
	}

	if err := first.Close(); err != nil {
		t.Fatalf(
			"Close: %v",
			err,
		)
	}

	delete(
		provider.keys,
		cfg.DatabaseID,
	)

	_, err = Open(
		context.Background(),
		cfg,
		Deps{
			KeyProvider: provider,
		},
	)

	if !errors.Is(
		err,
		ErrOutboxInconsistentInit,
	) {
		t.Fatalf(
			"error = %v, want ErrOutboxInconsistentInit",
			err,
		)
	}

	if !errors.Is(
		err,
		ErrOutboxKeyUnavailable,
	) {
		t.Fatalf(
			"error = %v, want ErrOutboxKeyUnavailable",
			err,
		)
	}

	if _, exists := provider.keys[cfg.DatabaseID]; exists {
		t.Fatal(
			"replacement key was created",
		)
	}

	if provider.createCalls != 1 {
		t.Fatalf(
			"CreateKey calls = %d, want 1",
			provider.createCalls,
		)
	}
}

func TestOpenRejectsEmptyDatabaseWithoutKey(
	t *testing.T,
) {
	cfg := newFactoryConfig(t)
	provider := newFactoryKeyProvider()

	dbPath := filepath.Join(
		cfg.DataDir,
		outboxDatabaseFileName,
	)

	if err := os.WriteFile(
		dbPath,
		nil,
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	_, err := Open(
		context.Background(),
		cfg,
		Deps{
			KeyProvider: provider,
		},
	)

	if !errors.Is(
		err,
		ErrOutboxInconsistentInit,
	) {
		t.Fatalf(
			"error = %v, want ErrOutboxInconsistentInit",
			err,
		)
	}

	if !errors.Is(
		err,
		ErrOutboxKeyUnavailable,
	) {
		t.Fatalf(
			"error = %v, want ErrOutboxKeyUnavailable",
			err,
		)
	}

	if provider.createCalls != 0 {
		t.Fatalf(
			"CreateKey calls = %d, want 0",
			provider.createCalls,
		)
	}
}

func TestOpenRejectsDatabasePathThatIsDirectory(
	t *testing.T,
) {
	cfg := newFactoryConfig(t)

	dbPath := filepath.Join(
		cfg.DataDir,
		outboxDatabaseFileName,
	)

	if err := os.Mkdir(
		dbPath,
		0o700,
	); err != nil {
		t.Fatal(err)
	}

	_, err := Open(
		context.Background(),
		cfg,
		Deps{
			KeyProvider: newFactoryKeyProvider(),
		},
	)

	if !errors.Is(
		err,
		ErrOutboxInconsistentInit,
	) {
		t.Fatalf(
			"error = %v, want ErrOutboxInconsistentInit",
			err,
		)
	}
}

func TestOpenRejectsSymlinkDatabasePath(
	t *testing.T,
) {
	cfg := newFactoryConfig(t)

	target := filepath.Join(
		cfg.DataDir,
		"target.db",
	)

	if err := os.WriteFile(
		target,
		nil,
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(
		cfg.DataDir,
		outboxDatabaseFileName,
	)

	if err := os.Symlink(
		target,
		dbPath,
	); err != nil {
		t.Fatal(err)
	}

	_, err := Open(
		context.Background(),
		cfg,
		Deps{
			KeyProvider: newFactoryKeyProvider(),
		},
	)

	if !errors.Is(
		err,
		ErrOutboxInconsistentInit,
	) {
		t.Fatalf(
			"error = %v, want ErrOutboxInconsistentInit",
			err,
		)
	}
}

// ---- Create race ----

func TestOpenLoadsWinnerKeyOnCreateRace(
	t *testing.T,
) {
	cfg := newFactoryConfig(t)
	provider := newFactoryKeyProvider()

	winner := make(
		[]byte,
		outboxDataEncryptionKeySize,
	)
	for index := range winner {
		winner[index] = 0x41
	}

	provider.createErr =
		ErrOutboxKeyExists
	provider.loadErr =
		ErrOutboxKeyUnavailable

	// Simulate the winner becoming visible when the second LoadKey
	// executes.
	racing := &raceFactoryKeyProvider{
		base:       provider,
		databaseID: cfg.DatabaseID,
		winner:     winner,
	}

	box, err := Open(
		context.Background(),
		cfg,
		Deps{
			KeyProvider: racing,
		},
	)
	if err != nil {
		t.Fatalf(
			"Open: %v",
			err,
		)
	}
	defer func() {
		_ = box.Close()
	}()

	if box.Store == nil {
		t.Fatal("Store is nil")
	}
}

type raceFactoryKeyProvider struct {
	base       *factoryKeyProvider
	databaseID string
	winner     []byte
	loads      int
}

func (p *raceFactoryKeyProvider) LoadKey(
	ctx context.Context,
	databaseID string,
) ([]byte, error) {
	p.loads++

	if p.loads == 1 {
		return nil, ErrOutboxKeyUnavailable
	}

	return append(
		[]byte(nil),
		p.winner...,
	), nil
}

func (p *raceFactoryKeyProvider) CreateKey(
	ctx context.Context,
	databaseID string,
) ([]byte, error) {
	return nil, ErrOutboxKeyExists
}

var _ KeyProvider = (*raceFactoryKeyProvider)(nil)

// ---- Provider failures ----

func TestOpenPropagatesUnsupportedKeyProvider(
	t *testing.T,
) {
	cfg := newFactoryConfig(t)
	provider := newFactoryKeyProvider()
	provider.loadErr =
		ErrOutboxKeyProviderUnsupported

	_, err := Open(
		context.Background(),
		cfg,
		Deps{
			KeyProvider: provider,
		},
	)

	if !errors.Is(
		err,
		ErrOutboxKeyProviderUnsupported,
	) {
		t.Fatalf(
			"error = %v, want ErrOutboxKeyProviderUnsupported",
			err,
		)
	}
}

func TestOpenRejectsMalformedCreatedKey(
	t *testing.T,
) {
	cfg := newFactoryConfig(t)
	provider := newFactoryKeyProvider()
	provider.createKey = []byte(
		"too short",
	)

	_, err := Open(
		context.Background(),
		cfg,
		Deps{
			KeyProvider: provider,
		},
	)

	if !errors.Is(
		err,
		ErrOutboxKeyCreationFailed,
	) {
		t.Fatalf(
			"error = %v, want ErrOutboxKeyCreationFailed",
			err,
		)
	}
}

// ---- Data directory ----

func TestOpenRejectsInsecureDataDir(
	t *testing.T,
) {
	base := t.TempDir()
	dir := filepath.Join(
		base,
		"outbox",
	)

	if err := os.Mkdir(
		dir,
		0o755,
	); err != nil {
		t.Fatal(err)
	}

	if err := os.Chmod(
		dir,
		0o755,
	); err != nil {
		t.Fatal(err)
	}

	_, err := Open(
		context.Background(),
		Config{
			DataDir:    dir,
			DatabaseID: "primary",
		},
		Deps{
			KeyProvider: newFactoryKeyProvider(),
		},
	)

	if !errors.Is(
		err,
		ErrOutboxDataDirInsecure,
	) {
		t.Fatalf(
			"error = %v, want ErrOutboxDataDirInsecure",
			err,
		)
	}
}

func TestOpenCreatesDataDirWithSecurePermissions(
	t *testing.T,
) {
	base := t.TempDir()
	dir := filepath.Join(
		base,
		"outbox",
	)

	box, err := Open(
		context.Background(),
		Config{
			DataDir:    dir,
			DatabaseID: "primary",
		},
		Deps{
			KeyProvider: newFactoryKeyProvider(),
		},
	)
	if err != nil {
		t.Fatalf(
			"Open: %v",
			err,
		)
	}
	defer func() {
		_ = box.Close()
	}()

	info, err := os.Lstat(dir)
	if err != nil {
		t.Fatal(err)
	}

	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf(
			"permissions = %o",
			info.Mode().Perm(),
		)
	}
}

func TestOpenRejectsSymlinkDataDir(
	t *testing.T,
) {
	base := t.TempDir()

	target := filepath.Join(
		base,
		"target",
	)
	if err := os.Mkdir(
		target,
		0o700,
	); err != nil {
		t.Fatal(err)
	}

	link := filepath.Join(
		base,
		"outbox-link",
	)
	if err := os.Symlink(
		target,
		link,
	); err != nil {
		t.Fatal(err)
	}

	_, err := Open(
		context.Background(),
		Config{
			DataDir:    link,
			DatabaseID: "primary",
		},
		Deps{
			KeyProvider: newFactoryKeyProvider(),
		},
	)

	if !errors.Is(
		err,
		ErrOutboxDataDirInvalid,
	) {
		t.Fatalf(
			"error = %v, want ErrOutboxDataDirInvalid",
			err,
		)
	}
}

func TestOpenRejectsDataDirPathThatIsFile(
	t *testing.T,
) {
	base := t.TempDir()
	path := filepath.Join(
		base,
		"outbox",
	)

	if err := os.WriteFile(
		path,
		[]byte("not a directory"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	_, err := Open(
		context.Background(),
		Config{
			DataDir:    path,
			DatabaseID: "primary",
		},
		Deps{
			KeyProvider: newFactoryKeyProvider(),
		},
	)

	if !errors.Is(
		err,
		ErrOutboxDataDirInvalid,
	) {
		t.Fatalf(
			"error = %v, want ErrOutboxDataDirInvalid",
			err,
		)
	}
}

// ---- Dispatcher wiring ----

func TestOpenBuildsDispatcherWhenSenderProvided(
	t *testing.T,
) {
	cfg := newFactoryConfig(t)

	box, err := Open(
		context.Background(),
		cfg,
		Deps{
			KeyProvider: newFactoryKeyProvider(),
			Sender:      factorySender{},
		},
	)
	if err != nil {
		t.Fatalf(
			"Open: %v",
			err,
		)
	}
	defer func() {
		_ = box.Close()
	}()

	if box.Dispatcher == nil {
		t.Fatal("Dispatcher is nil")
	}
}

func TestOpenExplicitDispatcherInstanceIDTakesPrecedence(
	t *testing.T,
) {
	cfg := newFactoryConfig(t)
	cfg.InstanceID = "factory-instance"
	cfg.Dispatcher = DefaultDispatcherConfig(
		"explicit-dispatcher",
	)

	box, err := Open(
		context.Background(),
		cfg,
		Deps{
			KeyProvider: newFactoryKeyProvider(),
			Sender:      factorySender{},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = box.Close()
	}()

	if box.Dispatcher == nil {
		t.Fatal("Dispatcher is nil")
	}

	if box.Dispatcher.cfg.InstanceID !=
		"explicit-dispatcher" {
		t.Fatalf(
			"InstanceID = %q, want explicit-dispatcher",
			box.Dispatcher.cfg.InstanceID,
		)
	}
}

func TestOpenUsesFactoryInstanceIDAsDispatcherFallback(
	t *testing.T,
) {
	cfg := newFactoryConfig(t)
	cfg.InstanceID = "factory-instance"
	cfg.Dispatcher.InstanceID = ""

	box, err := Open(
		context.Background(),
		cfg,
		Deps{
			KeyProvider: newFactoryKeyProvider(),
			Sender:      factorySender{},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = box.Close()
	}()

	if box.Dispatcher == nil {
		t.Fatal("Dispatcher is nil")
	}

	if box.Dispatcher.cfg.InstanceID !=
		"factory-instance" {
		t.Fatalf(
			"InstanceID = %q, want factory-instance",
			box.Dispatcher.cfg.InstanceID,
		)
	}
}

func TestOpenDefaultsDispatcherInstanceID(
	t *testing.T,
) {
	cfg := newFactoryConfig(t)
	cfg.InstanceID = ""
	cfg.Dispatcher.InstanceID = ""

	box, err := Open(
		context.Background(),
		cfg,
		Deps{
			KeyProvider: newFactoryKeyProvider(),
			Sender:      factorySender{},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = box.Close()
	}()

	if box.Dispatcher.cfg.InstanceID !=
		"dispatcher" {
		t.Fatalf(
			"InstanceID = %q, want dispatcher",
			box.Dispatcher.cfg.InstanceID,
		)
	}
}

// ---- Close ----

func TestOutboxCloseIsIdempotent(
	t *testing.T,
) {
	cfg := newFactoryConfig(t)

	box, err := Open(
		context.Background(),
		cfg,
		Deps{
			KeyProvider: newFactoryKeyProvider(),
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	if err := box.Close(); err != nil {
		t.Fatalf(
			"first Close: %v",
			err,
		)
	}

	if err := box.Close(); err != nil {
		t.Fatalf(
			"second Close: %v",
			err,
		)
	}
}

func TestOutboxCloseNilReceiver(
	t *testing.T,
) {
	var box *Outbox

	if err := box.Close(); err != nil {
		t.Fatalf(
			"nil Close error = %v",
			err,
		)
	}
}

// ---- End-to-end ----

func TestOpenEnqueueAndReopenPreservesText(
	t *testing.T,
) {
	cfg := newFactoryConfig(t)
	provider := newFactoryKeyProvider()

	deps := Deps{
		KeyProvider: provider,
	}

	box, err := Open(
		context.Background(),
		cfg,
		deps,
	)
	if err != nil {
		t.Fatal(err)
	}

	plaintext := "привет мир 🌍"

	entry := queuedEntry(
		"op-1",
		42,
		plaintext,
	)

	if err := box.Store.Enqueue(
		context.Background(),
		entry,
	); err != nil {
		_ = box.Close()
		t.Fatal(err)
	}

	dbPath := filepath.Join(
		cfg.DataDir,
		outboxDatabaseFileName,
	)

	raw, err := os.ReadFile(dbPath)
	if err != nil {
		_ = box.Close()
		t.Fatal(err)
	}

	if bytes.Contains(
		raw,
		[]byte(plaintext),
	) {
		_ = box.Close()
		t.Fatal(
			"database contains plaintext message body",
		)
	}

	walRaw, walErr := os.ReadFile(
		dbPath + "-wal",
	)
	if walErr == nil &&
		bytes.Contains(
			walRaw,
			[]byte(plaintext),
		) {
		_ = box.Close()
		t.Fatal(
			"WAL contains plaintext message body",
		)
	}

	if err := box.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(
		context.Background(),
		cfg,
		deps,
	)
	if err != nil {
		t.Fatalf(
			"reopen: %v",
			err,
		)
	}
	defer func() {
		_ = reopened.Close()
	}()

	got, err := reopened.Store.Get(
		context.Background(),
		entry.ID,
	)
	if err != nil {
		t.Fatal(err)
	}

	if got.Text != plaintext {
		t.Fatalf(
			"Text = %q, want %q",
			got.Text,
			plaintext,
		)
	}
}

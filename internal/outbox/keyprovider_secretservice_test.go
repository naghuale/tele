package outbox

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
)

// cloneStringMap avoids depending on maps.Clone.
func cloneStringMap(source map[string]string) map[string]string {
	target := make(map[string]string, len(source))
	for key, value := range source {
		target[key] = value
	}
	return target
}

// fakeSecretServiceClient is a scripted secretServiceClient.
type fakeSecretServiceClient struct {
	lookupValue  []byte
	lookupResult secretServiceResult

	storeResult  secretServiceResult
	deleteResult secretServiceResult

	storedValue []byte
	storedAttrs map[string]string

	lookupCalls int
	storeCalls  int
	deleteCalls int
}

func (c *fakeSecretServiceClient) Lookup(
	ctx context.Context,
	_ map[string]string,
) ([]byte, secretServiceResult) {
	if err := ctx.Err(); err != nil {
		return nil, secretServiceUnavailable
	}
	c.lookupCalls++
	return append([]byte(nil), c.lookupValue...), c.lookupResult
}

func (c *fakeSecretServiceClient) Store(
	ctx context.Context,
	attributes map[string]string,
	_ string,
	value []byte,
) secretServiceResult {
	if err := ctx.Err(); err != nil {
		return secretServiceUnavailable
	}
	c.storeCalls++
	c.storedAttrs = cloneStringMap(attributes)
	c.storedValue = append([]byte(nil), value...)
	return c.storeResult
}

func (c *fakeSecretServiceClient) Delete(
	ctx context.Context,
	_ map[string]string,
) secretServiceResult {
	if err := ctx.Err(); err != nil {
		return secretServiceUnavailable
	}
	c.deleteCalls++
	return c.deleteResult
}

type fakeLockFactory struct {
	calls int
	err   error
}

func (f *fakeLockFactory) Acquire(
	_ context.Context,
	_ string,
) (fileLock, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return noopLock{}, nil
}

type noopLock struct{}

func (noopLock) Release() error { return nil }

type countedReader struct {
	value byte
}

func (r countedReader) Read(buffer []byte) (int, error) {
	for i := range buffer {
		buffer[i] = r.value
	}
	return len(buffer), nil
}

type failingLinuxReader struct {
	err error
}

func (r failingLinuxReader) Read(_ []byte) (int, error) {
	return 0, r.err
}

// ---- LoadKey ----

func TestLinuxKeyProviderLoadKeySuccess(t *testing.T) {
	expected := bytes.Repeat([]byte{0x42}, outboxDataEncryptionKeySize)
	client := &fakeSecretServiceClient{
		lookupValue:  expected,
		lookupResult: secretServiceSuccess,
	}
	provider := newLinuxKeyProvider(
		client, countedReader{0x11}, &fakeLockFactory{},
	)

	got, err := provider.LoadKey(context.Background(), "database-1")
	if err != nil {
		t.Fatalf("LoadKey: %v", err)
	}
	if !bytes.Equal(got, expected) {
		t.Fatal("loaded key differs")
	}
	if client.lookupCalls != 1 {
		t.Fatalf("lookup calls = %d, want 1", client.lookupCalls)
	}
}

func TestLinuxKeyProviderLoadKeyMissing(t *testing.T) {
	client := &fakeSecretServiceClient{lookupResult: secretServiceNotFound}
	provider := newLinuxKeyProvider(client, countedReader{}, &fakeLockFactory{})

	_, err := provider.LoadKey(context.Background(), "database-1")
	if !errors.Is(err, ErrOutboxKeyUnavailable) {
		t.Fatalf("err = %v, want ErrOutboxKeyUnavailable", err)
	}
}

// A locked keyring is not a missing key, and the two send the user to
// opposite places.
func TestLinuxKeyProviderLoadKeyUnavailableIsAccessDenied(t *testing.T) {
	client := &fakeSecretServiceClient{lookupResult: secretServiceUnavailable}
	provider := newLinuxKeyProvider(client, countedReader{}, &fakeLockFactory{})

	_, err := provider.LoadKey(context.Background(), "database-1")
	if !errors.Is(err, ErrOutboxKeyAccessDenied) {
		t.Fatalf("err = %v, want ErrOutboxKeyAccessDenied", err)
	}
	if errors.Is(err, ErrOutboxKeyUnavailable) {
		t.Fatal("a locked keyring must not be reported as a missing key")
	}
}

func TestLinuxKeyProviderLoadKeyUnknownResultIsAccessDenied(t *testing.T) {
	client := &fakeSecretServiceClient{lookupResult: secretServiceResult(99)}
	provider := newLinuxKeyProvider(client, countedReader{}, &fakeLockFactory{})

	_, err := provider.LoadKey(context.Background(), "database-1")
	if err == nil {
		t.Fatal("an unknown result must be an error")
	}
	if errors.Is(err, ErrOutboxKeyUnavailable) {
		t.Fatal("an unknown failure must never be read as a missing key")
	}
}

// A missing key is the only condition that may be reported as missing.
// Anything else risks an offered reset throwing away a working queue.
func TestLinuxKeyProviderMissingKeyComesOnlyFromNotFound(t *testing.T) {
	results := []secretServiceResult{
		secretServiceUnavailable,
		secretServiceResult(99),
	}

	for _, result := range results {
		client := &fakeSecretServiceClient{lookupResult: result}
		provider := newLinuxKeyProvider(
			client, countedReader{}, &fakeLockFactory{},
		)

		_, err := provider.LoadKey(context.Background(), "database-1")
		if err == nil {
			t.Fatalf("result %d: expected an error", result)
		}
		if errors.Is(err, ErrOutboxKeyUnavailable) {
			t.Fatalf(
				"result %d produced ErrOutboxKeyUnavailable; only "+
					"secretServiceNotFound may",
				result,
			)
		}
	}
}

func TestLinuxKeyProviderLoadKeyMalformed(t *testing.T) {
	client := &fakeSecretServiceClient{
		lookupValue:  []byte("too short"),
		lookupResult: secretServiceSuccess,
	}
	provider := newLinuxKeyProvider(client, countedReader{}, &fakeLockFactory{})

	_, err := provider.LoadKey(context.Background(), "database-1")
	if !errors.Is(err, ErrOutboxKeyUnavailable) {
		t.Fatalf("err = %v, want ErrOutboxKeyUnavailable", err)
	}
}

// ---- CreateKey ----

func TestLinuxKeyProviderCreateKeySuccess(t *testing.T) {
	client := &fakeSecretServiceClient{
		lookupResult: secretServiceNotFound,
		storeResult:  secretServiceSuccess,
	}
	locks := &fakeLockFactory{}
	provider := newLinuxKeyProvider(client, countedReader{0x7A}, locks)

	key, err := provider.CreateKey(context.Background(), "database-1")
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}
	if len(key) != outboxDataEncryptionKeySize {
		t.Fatalf("key length = %d", len(key))
	}
	if !bytes.Equal(key, bytes.Repeat([]byte{0x7A}, outboxDataEncryptionKeySize)) {
		t.Fatal("unexpected key")
	}
	if locks.calls != 1 {
		t.Fatalf("lock calls = %d, want 1", locks.calls)
	}
	if client.storeCalls != 1 {
		t.Fatalf("store calls = %d, want 1", client.storeCalls)
	}
	if client.storedAttrs["service"] != keychainOutboxService {
		t.Fatalf("stored service = %q", client.storedAttrs["service"])
	}
	if client.storedAttrs["account"] != "database-1" {
		t.Fatalf("stored account = %q", client.storedAttrs["account"])
	}
}

func TestLinuxKeyProviderCreateKeyExistingUnderLock(t *testing.T) {
	client := &fakeSecretServiceClient{
		lookupResult: secretServiceSuccess,
		lookupValue:  bytes.Repeat([]byte{0x01}, outboxDataEncryptionKeySize),
	}
	provider := newLinuxKeyProvider(client, countedReader{0x7A}, &fakeLockFactory{})

	_, err := provider.CreateKey(context.Background(), "database-1")
	if !errors.Is(err, ErrOutboxKeyExists) {
		t.Fatalf("err = %v, want ErrOutboxKeyExists", err)
	}
	if client.storeCalls != 0 {
		t.Fatalf("store calls = %d, want 0", client.storeCalls)
	}
}

func TestLinuxKeyProviderCreateKeyStoreFailure(t *testing.T) {
	client := &fakeSecretServiceClient{
		lookupResult: secretServiceNotFound,
		storeResult:  secretServiceFailure,
	}
	provider := newLinuxKeyProvider(client, countedReader{0x7A}, &fakeLockFactory{})

	_, err := provider.CreateKey(context.Background(), "database-1")
	if !errors.Is(err, ErrOutboxKeyCreationFailed) {
		t.Fatalf("err = %v, want ErrOutboxKeyCreationFailed", err)
	}
}

func TestLinuxKeyProviderCreateKeyUnavailable(t *testing.T) {
	client := &fakeSecretServiceClient{
		lookupResult: secretServiceUnavailable,
	}
	provider := newLinuxKeyProvider(client, countedReader{0x7A}, &fakeLockFactory{})

	_, err := provider.CreateKey(context.Background(), "database-1")
	if !errors.Is(err, ErrOutboxKeyCreationFailed) {
		t.Fatalf("err = %v, want ErrOutboxKeyCreationFailed", err)
	}
	if client.storeCalls != 0 {
		t.Fatalf("store calls = %d, want 0", client.storeCalls)
	}
}

func TestLinuxKeyProviderCreateKeyRandomFailure(t *testing.T) {
	randomErr := errors.New("random unavailable")
	client := &fakeSecretServiceClient{
		lookupResult: secretServiceNotFound,
		storeResult:  secretServiceSuccess,
	}
	provider := newLinuxKeyProvider(
		client, failingLinuxReader{err: randomErr}, &fakeLockFactory{},
	)

	_, err := provider.CreateKey(context.Background(), "database-1")
	if !errors.Is(err, ErrOutboxKeyCreationFailed) {
		t.Fatalf("err = %v, want ErrOutboxKeyCreationFailed", err)
	}
	if client.storeCalls != 0 {
		t.Fatalf("store calls = %d, want 0", client.storeCalls)
	}
}

func TestLinuxKeyProviderCreateKeyLockFailure(t *testing.T) {
	lockErr := errors.New("lock dir missing")
	client := &fakeSecretServiceClient{lookupResult: secretServiceNotFound}
	provider := newLinuxKeyProvider(
		client, countedReader{0x7A}, &fakeLockFactory{err: lockErr},
	)

	_, err := provider.CreateKey(context.Background(), "database-1")
	if !errors.Is(err, ErrOutboxKeyCreationFailed) {
		t.Fatalf("err = %v, want ErrOutboxKeyCreationFailed", err)
	}
	if client.storeCalls != 0 {
		t.Fatalf("store calls = %d, want 0", client.storeCalls)
	}
}

// ---- Validation, cancellation, nil guards ----

func TestLinuxKeyProviderValidationBeforeTransport(t *testing.T) {
	client := &fakeSecretServiceClient{}
	provider := newLinuxKeyProvider(client, countedReader{}, &fakeLockFactory{})

	if _, err := provider.LoadKey(context.Background(), " "); !errors.Is(
		err, ErrOutboxKeyInvalidDatabaseID,
	) {
		t.Fatalf("LoadKey err = %v", err)
	}
	if _, err := provider.CreateKey(context.Background(), " "); !errors.Is(
		err, ErrOutboxKeyInvalidDatabaseID,
	) {
		t.Fatalf("CreateKey err = %v", err)
	}
	if client.lookupCalls != 0 || client.storeCalls != 0 {
		t.Fatal("transport called for invalid database ID")
	}
}

func TestLinuxKeyProviderCanceledContext(t *testing.T) {
	client := &fakeSecretServiceClient{}
	locks := &fakeLockFactory{}
	provider := newLinuxKeyProvider(client, countedReader{}, locks)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := provider.LoadKey(ctx, "database-1"); !errors.Is(
		err, context.Canceled,
	) {
		t.Fatalf("LoadKey err = %v", err)
	}
	if _, err := provider.CreateKey(ctx, "database-1"); !errors.Is(
		err, context.Canceled,
	) {
		t.Fatalf("CreateKey err = %v", err)
	}
	if client.lookupCalls != 0 || client.storeCalls != 0 {
		t.Fatal("transport called for canceled context")
	}
	if locks.calls != 0 {
		t.Fatal("lock acquired for canceled context")
	}
}

func TestLinuxKeyProviderNilClient(t *testing.T) {
	provider := newLinuxKeyProvider(nil, countedReader{}, &fakeLockFactory{})

	if _, err := provider.LoadKey(context.Background(), "database-1"); !errors.Is(
		err, ErrOutboxKeyProviderUnsupported,
	) {
		t.Fatalf("LoadKey err = %v", err)
	}
	if _, err := provider.CreateKey(context.Background(), "database-1"); !errors.Is(
		err, ErrOutboxKeyProviderUnsupported,
	) {
		t.Fatalf("CreateKey err = %v", err)
	}
}

func TestLinuxKeyProviderNilRandom(t *testing.T) {
	client := &fakeSecretServiceClient{lookupResult: secretServiceNotFound}
	provider := newLinuxKeyProvider(client, nil, &fakeLockFactory{})

	_, err := provider.CreateKey(context.Background(), "database-1")
	if !errors.Is(err, ErrOutboxKeyCreationFailed) {
		t.Fatalf("err = %v, want ErrOutboxKeyCreationFailed", err)
	}
}

func TestLinuxKeyProviderNilLockFactory(t *testing.T) {
	client := &fakeSecretServiceClient{lookupResult: secretServiceNotFound}
	provider := newLinuxKeyProvider(client, countedReader{0x7A}, nil)

	_, err := provider.CreateKey(context.Background(), "database-1")
	if !errors.Is(err, ErrOutboxKeyCreationFailed) {
		t.Fatalf("err = %v, want ErrOutboxKeyCreationFailed", err)
	}
}

// ---- Interface assertions ----

func TestLinuxKeyProviderImplementsInterface(t *testing.T) {
	var _ KeyProvider = (*linuxKeyProvider)(nil)
	var _ secretServiceClient = (*fakeSecretServiceClient)(nil)
	var _ io.Reader = countedReader{}
}

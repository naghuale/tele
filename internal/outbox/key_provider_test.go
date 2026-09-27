package outbox

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"testing"
)

// fakeKeyProvider is an in-memory KeyProvider used by unit tests.
//
// It is not a production implementation: keys are kept in a map and
// disappear with the process.
type fakeKeyProvider struct {
	keys map[string][]byte
}

func newFakeKeyProvider() *fakeKeyProvider {
	return &fakeKeyProvider{keys: make(map[string][]byte)}
}

func (p *fakeKeyProvider) LoadKey(
	ctx context.Context,
	databaseID string,
) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateDatabaseID(databaseID); err != nil {
		return nil, err
	}
	key, ok := p.keys[databaseID]
	if !ok {
		return nil, ErrOutboxKeyUnavailable
	}
	return validateLoadedKey(key)
}

func (p *fakeKeyProvider) CreateKey(
	ctx context.Context,
	databaseID string,
) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateDatabaseID(databaseID); err != nil {
		return nil, err
	}
	if _, ok := p.keys[databaseID]; ok {
		return nil, ErrOutboxKeyExists
	}
	key := make([]byte, outboxDataEncryptionKeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf(
			"%w: %v", ErrOutboxKeyCreationFailed, err,
		)
	}
	p.keys[databaseID] = append([]byte(nil), key...)
	return append([]byte(nil), key...), nil
}

// Compile-time assertion.
var _ KeyProvider = (*fakeKeyProvider)(nil)

// ---- Fake provider contract ----

func TestFakeKeyProviderCreateThenLoad(t *testing.T) {
	p := newFakeKeyProvider()

	created, err := p.CreateKey(context.Background(), "primary")
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}
	if len(created) != outboxDataEncryptionKeySize {
		t.Fatalf("key length = %d, want %d",
			len(created), outboxDataEncryptionKeySize)
	}

	loaded, err := p.LoadKey(context.Background(), "primary")
	if err != nil {
		t.Fatalf("LoadKey: %v", err)
	}
	if !bytes.Equal(created, loaded) {
		t.Fatal("loaded key differs from created key")
	}
}

func TestFakeKeyProviderCreateExistingReturnsErrOutboxKeyExists(
	t *testing.T,
) {
	p := newFakeKeyProvider()

	if _, err := p.CreateKey(context.Background(), "primary"); err != nil {
		t.Fatal(err)
	}

	_, err := p.CreateKey(context.Background(), "primary")
	if !errors.Is(err, ErrOutboxKeyExists) {
		t.Fatalf("err = %v, want ErrOutboxKeyExists", err)
	}
}

func TestFakeKeyProviderLoadMissingReturnsErrOutboxKeyUnavailable(
	t *testing.T,
) {
	p := newFakeKeyProvider()

	_, err := p.LoadKey(context.Background(), "primary")
	if !errors.Is(err, ErrOutboxKeyUnavailable) {
		t.Fatalf("err = %v, want ErrOutboxKeyUnavailable", err)
	}
}

func TestFakeKeyProviderReturnsCopy(t *testing.T) {
	p := newFakeKeyProvider()

	created, err := p.CreateKey(context.Background(), "primary")
	if err != nil {
		t.Fatal(err)
	}
	for i := range created {
		created[i] = 0
	}

	loaded, err := p.LoadKey(context.Background(), "primary")
	if err != nil {
		t.Fatal(err)
	}
	allZero := true
	for _, b := range loaded {
		if b != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		t.Fatal("fake provider exposed its internal key buffer")
	}
}

// ---- validateDatabaseID / validateLoadedKey ----

func TestKeyProviderRejectsEmptyDatabaseID(t *testing.T) {
	p := newFakeKeyProvider()

	for _, id := range []string{"", " ", "\t", "\n", "  \t\n "} {
		if _, err := p.CreateKey(context.Background(), id); !errors.Is(
			err, ErrOutboxKeyInvalidDatabaseID,
		) {
			t.Fatalf("CreateKey(%q): err = %v, want ErrOutboxKeyInvalidDatabaseID",
				id, err)
		}
		if _, err := p.LoadKey(context.Background(), id); !errors.Is(
			err, ErrOutboxKeyInvalidDatabaseID,
		) {
			t.Fatalf("LoadKey(%q): err = %v, want ErrOutboxKeyInvalidDatabaseID",
				id, err)
		}
	}
}

func TestKeyProviderRejectsCancelledContext(t *testing.T) {
	p := newFakeKeyProvider()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := p.CreateKey(ctx, "primary"); !errors.Is(
		err, context.Canceled,
	) {
		t.Fatalf("CreateKey: err = %v, want context.Canceled", err)
	}
	if _, err := p.LoadKey(ctx, "primary"); !errors.Is(
		err, context.Canceled,
	) {
		t.Fatalf("LoadKey: err = %v, want context.Canceled", err)
	}
}

func TestValidateLoadedKeyAcceptsExactSize(t *testing.T) {
	key := make([]byte, outboxDataEncryptionKeySize)
	got, err := validateLoadedKey(key)
	if err != nil {
		t.Fatalf("validateLoadedKey: %v", err)
	}
	if len(got) != outboxDataEncryptionKeySize {
		t.Fatalf("len = %d, want %d",
			len(got), outboxDataEncryptionKeySize)
	}
}

func TestValidateLoadedKeyReturnsDefensiveCopy(t *testing.T) {
	key := make([]byte, outboxDataEncryptionKeySize)
	for i := range key {
		key[i] = byte(i)
	}

	got, err := validateLoadedKey(key)
	if err != nil {
		t.Fatal(err)
	}
	for i := range got {
		got[i] = 0xFF
	}
	if key[0] == 0xFF {
		t.Fatal("validateLoadedKey exposed its input buffer")
	}
}

func TestValidateLoadedKeyRejectsWrongLength(t *testing.T) {
	for _, size := range []int{0, 1, 16, 31, 33, 64} {
		_, err := validateLoadedKey(make([]byte, size))
		if !errors.Is(err, ErrOutboxKeyMalformed) {
			t.Fatalf("size=%d: err = %v, want ErrOutboxKeyMalformed",
				size, err)
		}
		if errors.Is(err, ErrOutboxKeyUnavailable) {
			t.Fatalf(
				"size=%d: a malformed key must not satisfy "+
					"ErrOutboxKeyUnavailable: it still exists",
				size,
			)
		}
	}
}

// ---- Sentinels are errors.Is-stable ----

func TestKeyProviderSentinelsAreDistinct(t *testing.T) {
	sentinels := []error{
		ErrOutboxKeyUnavailable,
		ErrOutboxKeyProviderUnsupported,
		ErrOutboxKeyInvalidDatabaseID,
		ErrOutboxKeyExists,
		ErrOutboxKeyCreationFailed,
	}
	for i, a := range sentinels {
		if a == nil {
			t.Fatalf("sentinel %d is nil", i)
		}
		for j, b := range sentinels {
			if i == j {
				continue
			}
			if errors.Is(a, b) {
				t.Fatalf("sentinel %d matches sentinel %d", i, j)
			}
		}
	}
}

// ---- Platform stub is fail-closed with validation first ----

func TestNewPlatformKeyProviderReturnsProvider(t *testing.T) {
	p := NewPlatformKeyProvider()
	if p == nil {
		t.Fatal("NewPlatformKeyProvider returned nil")
	}
}

// Every platform stub must validate databaseID before returning
// ErrOutboxKeyProviderUnsupported. That keeps the validation contract
// shared with real providers and prevents an empty id from being
// silently treated as "unsupported platform".
func TestPlatformStubValidatesDatabaseIDFirst(t *testing.T) {
	p := NewPlatformKeyProvider()

	for _, id := range []string{"", " ", "\t", "\n", "  \t\n "} {
		if _, err := p.LoadKey(
			context.Background(), id,
		); !errors.Is(err, ErrOutboxKeyInvalidDatabaseID) {
			t.Fatalf("LoadKey(%q): err = %v, want ErrOutboxKeyInvalidDatabaseID",
				id, err)
		}
		if _, err := p.CreateKey(
			context.Background(), id,
		); !errors.Is(err, ErrOutboxKeyInvalidDatabaseID) {
			t.Fatalf("CreateKey(%q): err = %v, want ErrOutboxKeyInvalidDatabaseID",
				id, err)
		}
	}
}

// On a valid databaseID the C3a stub is fail-closed: either the
// provider is unsupported on this platform, or (in future C3b) it
// returns a key. Both outcomes must be non-nil errors or a valid key;
// never a silent success with a malformed key.
func TestPlatformStubDoesNotReturnMalformedKey(t *testing.T) {
	p := NewPlatformKeyProvider()

	key, err := p.LoadKey(context.Background(), "primary")
	if err != nil {
		// Any of the "the key cannot be produced" sentinels is
		// acceptable; what must never happen is a key coming back
		// malformed.
		if !errors.Is(err, ErrOutboxKeyProviderUnsupported) &&
			!errors.Is(err, ErrOutboxKeyUnavailable) &&
			!errors.Is(err, ErrOutboxKeyAccessDenied) {
			t.Fatalf("unexpected error: %v", err)
		}
		return
	}
	if len(key) != outboxDataEncryptionKeySize {
		t.Fatalf("loaded key length = %d, want %d",
			len(key), outboxDataEncryptionKeySize)
	}
}

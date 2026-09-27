package outbox

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

var (
	// ErrOutboxKeyUnavailable is returned when the platform key
	// provider reports that no key exists for an existing database.
	//
	// It is deliberately exclusive: it means the item is absent, nothing
	// else. Only this condition may be treated as recoverable by starting
	// a new queue, because only here is there no key to lose. A key that
	// exists but cannot be used is ErrOutboxKeyMalformed, and a provider
	// that will not answer is ErrOutboxKeyAccessDenied.
	//
	// It remains fail-closed: a new key is never created as a fallback.
	ErrOutboxKeyUnavailable = errors.New(
		"outbox: key unavailable",
	)

	// ErrOutboxKeyMalformed is returned when a key exists but is not a
	// usable one, for example of the wrong length.
	//
	// It is distinct from ErrOutboxKeyUnavailable on purpose. The key is
	// still in the keychain, so the queue is not unrecoverable: a
	// restored backup may fix it. Reporting it as a missing key would
	// tell the user their key is gone and, once a reset command exists,
	// would offer to orphan a queue whose key is still present.
	ErrOutboxKeyMalformed = errors.New(
		"outbox: key malformed",
	)

	// ErrOutboxKeyAccessDenied is returned when the platform key
	// provider holds the key but refused to hand it over: the Keychain
	// is locked, or the user denied access.
	//
	// It is deliberately distinct from ErrOutboxKeyUnavailable so the
	// user can be told to unlock or grant access, instead of being told
	// the key is gone. The native bridge already separates a missing item
	// from an unavailable one; collapsing them here would throw that away
	// and force callers to match on message text.
	ErrOutboxKeyAccessDenied = errors.New(
		"outbox: key access denied",
	)

	// ErrOutboxKeyProviderUnsupported is returned when the current
	// platform does not have a supported key provider. On such
	// platforms the durable outbox remains unavailable.
	ErrOutboxKeyProviderUnsupported = errors.New(
		"outbox: key provider not supported on this platform",
	)

	// ErrOutboxKeyInvalidDatabaseID is returned for an empty or
	// whitespace-only database identity.
	ErrOutboxKeyInvalidDatabaseID = errors.New(
		"outbox: invalid database id",
	)

	// ErrOutboxKeyExists is returned by CreateKey when a key already
	// exists for the given database identity. It guards against
	// accidentally re-keying an existing encrypted store.
	ErrOutboxKeyExists = errors.New(
		"outbox: key already exists",
	)

	// ErrOutboxKeyCreationFailed is returned when generating or
	// storing a new key fails.
	ErrOutboxKeyCreationFailed = errors.New(
		"outbox: key creation failed",
	)
)

// outboxDataEncryptionKeySize is the expected length of a
// data-encryption key, matching XChaCha20-Poly1305.
const outboxDataEncryptionKeySize = 32

// KeyProvider loads and creates per-database data-encryption keys
// outside the outbox database.
//
// The contract is intentionally split: LoadKey and CreateKey have
// different failure semantics, and a single LoadOrCreate method cannot
// reliably distinguish a brand new database from an existing database
// whose key has been lost.
//
// Contract:
//
//   - Opening an existing database must call LoadKey. If the key is
//     missing, LoadKey returns ErrOutboxKeyUnavailable and the caller
//     must not fall back to CreateKey.
//   - CreateKey may only be called while initializing a new database
//     identity. If a key already exists, CreateKey returns
//     ErrOutboxKeyExists without overwriting it.
//   - Implementations must not store the key beside the database and
//     must not include it in logs.
type KeyProvider interface {
	LoadKey(
		ctx context.Context,
		databaseID string,
	) ([]byte, error)

	CreateKey(
		ctx context.Context,
		databaseID string,
	) ([]byte, error)
}

// validateDatabaseID rejects empty and whitespace-only identities.
//
// The database identity is an application-chosen opaque string. It is
// not a credential and must not contain secrets.
func validateDatabaseID(id string) error {
	if strings.TrimSpace(id) == "" {
		return ErrOutboxKeyInvalidDatabaseID
	}
	return nil
}

// validateLoadedKey enforces the exact key length expected by
// AEADCipher and returns a defensive copy.
//
// A malformed stored key is fail-closed: the caller must not proceed
// with a truncated or oversized key, and must not create a replacement.
//
// The error is ErrOutboxKeyMalformed and not ErrOutboxKeyUnavailable,
// because the key exists. Only a caller that can tell the two apart can
// avoid telling a user with a recoverable key that it is gone.
func validateLoadedKey(key []byte) ([]byte, error) {
	if len(key) != outboxDataEncryptionKeySize {
		return nil, fmt.Errorf(
			"%w: invalid key length %d, want %d",
			ErrOutboxKeyMalformed,
			len(key),
			outboxDataEncryptionKeySize,
		)
	}
	return append([]byte(nil), key...), nil
}

package outbox

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

var (
	// ErrOutboxKeyUnavailable is returned when a key is missing for
	// an existing database, when the platform key provider is locked
	// or otherwise unavailable, or when a stored key is malformed.
	//
	// The caller must treat this as a fail-closed condition: a new
	// key is never created as a fallback.
	ErrOutboxKeyUnavailable = errors.New(
		"outbox: key unavailable",
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
func validateLoadedKey(key []byte) ([]byte, error) {
	if len(key) != outboxDataEncryptionKeySize {
		return nil, fmt.Errorf(
			"%w: invalid key length %d, want %d",
			ErrOutboxKeyUnavailable,
			len(key),
			outboxDataEncryptionKeySize,
		)
	}
	return append([]byte(nil), key...), nil
}

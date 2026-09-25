package outbox

import (
	"context"
	"fmt"
	"io"
)

// secretServiceResult is a transport-neutral result returned by the
// Secret Service bridge. It hides D-Bus error names from callers.
type secretServiceResult uint8

const (
	secretServiceSuccess secretServiceResult = iota
	secretServiceNotFound
	secretServiceUnavailable
	secretServiceFailure
)

// secretServiceClient is the narrow Secret Service transport used by
// the Linux provider.
//
// Every method accepts context.Context. Implementations must honor it:
// a D-Bus call must use a context-aware variant so a stuck call can be
// cancelled.
//
// Store must not overwrite an existing item. The provider relies on
// the file lock plus a preceding Lookup to detect the existing item;
// Store itself reports only success, unavailable, or failure.
type secretServiceClient interface {
	Lookup(
		ctx context.Context,
		attributes map[string]string,
	) ([]byte, secretServiceResult)

	Store(
		ctx context.Context,
		attributes map[string]string,
		label string,
		value []byte,
	) secretServiceResult

	Delete(
		ctx context.Context,
		attributes map[string]string,
	) secretServiceResult
}

func secretServiceAttrs(
	databaseID string,
) map[string]string {
	return map[string]string{
		"service": keychainOutboxService,
		"account": databaseID,
	}
}

// linuxKeyProvider stores one data-encryption key per database
// identity in the user's Secret Service collection.
//
// The Secret Service API does not expose an atomic create-if-absent
// result for matching attributes. telecli serializes CreateKey across
// cooperating telecli processes on the same machine with a
// per-database advisory file lock.
//
// While holding the lock, CreateKey performs Lookup followed by Store
// with replace=false. A second cooperating creator observes the item
// created by the first and returns ErrOutboxKeyExists. The stored key
// is never replaced by a cooperating process.
type linuxKeyProvider struct {
	client secretServiceClient
	random io.Reader
	locks  fileLockFactory
}

func newLinuxKeyProvider(
	client secretServiceClient,
	random io.Reader,
	locks fileLockFactory,
) *linuxKeyProvider {
	return &linuxKeyProvider{
		client: client,
		random: random,
		locks:  locks,
	}
}

// LoadKey implements KeyProvider.
func (p *linuxKeyProvider) LoadKey(
	ctx context.Context,
	databaseID string,
) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateDatabaseID(databaseID); err != nil {
		return nil, err
	}
	if p == nil || p.client == nil {
		return nil, ErrOutboxKeyProviderUnsupported
	}

	value, result := p.client.Lookup(
		ctx,
		secretServiceAttrs(databaseID),
	)

	switch result {
	case secretServiceSuccess:
		if err := ctx.Err(); err != nil {
			clearBytes(value)
			return nil, err
		}
		validated, err := validateLoadedKey(value)
		clearBytes(value)
		if err != nil {
			return nil, err
		}
		return validated, nil

	case secretServiceNotFound, secretServiceUnavailable:
		clearBytes(value)
		return nil, ErrOutboxKeyUnavailable

	default:
		clearBytes(value)
		return nil, fmt.Errorf(
			"%w: secret service lookup failed",
			ErrOutboxKeyUnavailable,
		)
	}
}

// CreateKey implements KeyProvider.
func (p *linuxKeyProvider) CreateKey(
	ctx context.Context,
	databaseID string,
) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateDatabaseID(databaseID); err != nil {
		return nil, err
	}
	if p == nil || p.client == nil {
		return nil, ErrOutboxKeyProviderUnsupported
	}
	if p.random == nil {
		return nil, fmt.Errorf(
			"%w: random source unavailable",
			ErrOutboxKeyCreationFailed,
		)
	}
	if p.locks == nil {
		return nil, fmt.Errorf(
			"%w: lock factory unavailable",
			ErrOutboxKeyCreationFailed,
		)
	}

	lock, err := p.locks.Acquire(ctx, databaseID)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf(
			"%w: acquire key lock: %v",
			ErrOutboxKeyCreationFailed,
			err,
		)
	}
	defer func() { _ = lock.Release() }()

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	attributes := secretServiceAttrs(databaseID)

	existing, result := p.client.Lookup(ctx, attributes)

	switch result {
	case secretServiceSuccess:
		clearBytes(existing)
		return nil, ErrOutboxKeyExists

	case secretServiceNotFound:
		clearBytes(existing)

	case secretServiceUnavailable:
		clearBytes(existing)
		return nil, fmt.Errorf(
			"%w: secret service unavailable",
			ErrOutboxKeyCreationFailed,
		)

	default:
		clearBytes(existing)
		return nil, fmt.Errorf(
			"%w: secret service lookup failed",
			ErrOutboxKeyCreationFailed,
		)
	}

	key := make([]byte, outboxDataEncryptionKeySize)
	defer clearBytes(key)

	if _, err := io.ReadFull(p.random, key); err != nil {
		return nil, fmt.Errorf(
			"%w: generate data-encryption key: %v",
			ErrOutboxKeyCreationFailed,
			err,
		)
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	storeResult := p.client.Store(
		ctx,
		attributes,
		keychainOutboxService+":"+databaseID,
		key,
	)

	if err := ctx.Err(); err != nil {
		if storeResult == secretServiceSuccess {
			return nil, fmt.Errorf(
				"%w: caller canceled after secret service write",
				ErrOutboxKeyCreationFailed,
			)
		}
		return nil, err
	}

	switch storeResult {
	case secretServiceSuccess:
		return append([]byte(nil), key...), nil

	case secretServiceUnavailable:
		return nil, fmt.Errorf(
			"%w: secret service unavailable",
			ErrOutboxKeyCreationFailed,
		)

	default:
		return nil, fmt.Errorf(
			"%w: secret service rejected key creation",
			ErrOutboxKeyCreationFailed,
		)
	}
}

// Compile-time assertion.
var _ KeyProvider = (*linuxKeyProvider)(nil)

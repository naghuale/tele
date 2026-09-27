package outbox

import (
	"context"
	"fmt"
	"io"
)

const keychainOutboxService = "telecli-outbox"

// secItemResult is a transport-neutral result returned by the native
// SecItem bridge.
//
// The provider does not expose raw OSStatus values to callers.
type secItemResult uint8

const (
	secItemSuccess secItemResult = iota
	secItemNotFound
	secItemDuplicate
	secItemUnavailable
	secItemFailure
)

// secItemClient is the narrow native Keychain transport used by the
// macOS provider.
//
// AddPassword must implement atomic create-if-absent. It must return
// secItemDuplicate when an item with the same generic-password primary
// key already exists.
type secItemClient interface {
	CopyPassword(
		service string,
		account string,
	) ([]byte, secItemResult)

	AddPassword(
		service string,
		account string,
		value []byte,
	) secItemResult
}

// darwinKeyProvider stores one data-encryption key per database
// identity in macOS Keychain.
//
// The generic-password primary key is:
//
//	service = "telecli-outbox"
//	account = databaseID
//
// SecItemAdd provides atomic create-if-absent semantics for that
// primary key.
type darwinKeyProvider struct {
	client secItemClient
	random io.Reader
}

func newDarwinKeyProvider(
	client secItemClient,
	random io.Reader,
) *darwinKeyProvider {
	return &darwinKeyProvider{
		client: client,
		random: random,
	}
}

// LoadKey implements KeyProvider.
func (p *darwinKeyProvider) LoadKey(
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

	if p == nil || p.client == nil {
		return nil, ErrOutboxKeyProviderUnsupported
	}

	key, result := p.client.CopyPassword(
		keychainOutboxService,
		databaseID,
	)

	switch result {
	case secItemSuccess:
		if err := ctx.Err(); err != nil {
			clearBytes(key)
			return nil, err
		}

		validated, err := validateLoadedKey(key)
		clearBytes(key)
		if err != nil {
			return nil, err
		}

		return validated, nil

	case secItemNotFound:
		// The item is absent: a missing key. Distinct from a refusal
		// below, because the user must be told different things.
		clearBytes(key)
		return nil, ErrOutboxKeyUnavailable

	case secItemUnavailable:
		// The item may well exist, but the Keychain is locked or
		// access was denied. Reporting this as a missing key would send
		// the user looking for a key that is still there.
		clearBytes(key)
		return nil, ErrOutboxKeyAccessDenied

	default:
		clearBytes(key)
		return nil, fmt.Errorf(
			"%w: keychain read failed",
			ErrOutboxKeyAccessDenied,
		)
	}
}

// CreateKey implements KeyProvider.
//
// SecItemAdd performs the create-if-absent operation atomically. A
// concurrent creator receives secItemDuplicate and the existing key is
// not replaced.
func (p *darwinKeyProvider) CreateKey(
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

	if p == nil || p.client == nil {
		return nil, ErrOutboxKeyProviderUnsupported
	}

	if p.random == nil {
		return nil, fmt.Errorf(
			"%w: random source unavailable",
			ErrOutboxKeyCreationFailed,
		)
	}

	key := make(
		[]byte,
		outboxDataEncryptionKeySize,
	)
	defer clearBytes(key)

	if _, err := io.ReadFull(
		p.random,
		key,
	); err != nil {
		return nil, fmt.Errorf(
			"%w: generate data-encryption key: %v",
			ErrOutboxKeyCreationFailed,
			err,
		)
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	result := p.client.AddPassword(
		keychainOutboxService,
		databaseID,
		key,
	)

	if err := ctx.Err(); err != nil {
		// AddPassword may already have completed before cancellation
		// became visible. Do not claim that no item was created.
		if result == secItemSuccess {
			return nil, fmt.Errorf(
				"%w: caller canceled after keychain write",
				ErrOutboxKeyCreationFailed,
			)
		}

		return nil, err
	}

	switch result {
	case secItemSuccess:
		return append(
			[]byte(nil),
			key...,
		), nil

	case secItemDuplicate:
		return nil, ErrOutboxKeyExists

	case secItemUnavailable:
		return nil, fmt.Errorf(
			"%w: keychain unavailable",
			ErrOutboxKeyCreationFailed,
		)

	default:
		return nil, fmt.Errorf(
			"%w: keychain write failed",
			ErrOutboxKeyCreationFailed,
		)
	}
}

// clearBytes overwrites a byte slice before it becomes unreachable.
//
// This is a best-effort cleanup for temporary key buffers. Callers
// must not use value after this function returns.
func clearBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

// Compile-time assertion.
var _ KeyProvider = (*darwinKeyProvider)(nil)

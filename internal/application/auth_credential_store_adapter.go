package application

import (
	"context"
	"errors"
	"fmt"

	"telecli/internal/authstore"
)

// keychainTelegramCredentialStore adapts an authstore.Store to the
// application credential contract.
//
// The adapter is the only place where backend sentinels become
// application errors, so the raw Keychain status, the item attributes and
// the serialized envelope never travel upward.
type keychainTelegramCredentialStore struct {
	store authstore.Store
}

// NewTelegramCredentialStore returns the production credential store for
// the current platform.
//
// On a platform without a store the returned implementation fails
// closed: a configured profile becomes a startup error instead of a
// silent mock-only run.
func NewTelegramCredentialStore() TelegramCredentialStore {
	return keychainTelegramCredentialStore{
		store: authstore.NewPlatformStore(),
	}
}

// Compile-time assertion: the adapter satisfies the application contract.
var _ TelegramCredentialStore = keychainTelegramCredentialStore{}

// LoadTelegramCredentials returns the profile stored under profile.
func (s keychainTelegramCredentialStore) LoadTelegramCredentials(
	ctx context.Context,
	profile string,
) (TelegramProfileCredentials, error) {
	credentials, err := s.store.Load(ctx, profile)
	if err != nil {
		return TelegramProfileCredentials{}, projectCredentialError(
			"load",
			err,
		)
	}

	return TelegramProfileCredentials{
		APIHash: credentials.APIHash,
		Phone:   credentials.Phone,
	}, nil
}

// projectCredentialError maps a backend sentinel to the application
// error surface.
//
// The backend message is dropped rather than wrapped: a platform error
// can quote item attributes, and the application contract promises that
// no Keychain detail reaches the user.
func projectCredentialError(operation string, err error) error {
	switch {
	case errors.Is(err, authstore.ErrProfileUnavailable):
		return fmt.Errorf(
			"%w: %s profile",
			ErrTelegramCredentialProfileUnavailable,
			operation,
		)

	case errors.Is(err, authstore.ErrStoreUnavailable):
		return fmt.Errorf(
			"%w: %s profile",
			ErrTelegramCredentialStoreUnavailable,
			operation,
		)

	case errors.Is(err, authstore.ErrInvalidProfile):
		return fmt.Errorf(
			"%w: %s profile: profile is invalid",
			ErrTelegramCredentialsInvalid,
			operation,
		)

	case errors.Is(err, authstore.ErrProfileExists):
		return fmt.Errorf(
			"%w: %s profile: profile already exists",
			ErrTelegramCredentialsInvalid,
			operation,
		)

	default:
		return fmt.Errorf(
			"%w: %s profile",
			ErrTelegramCredentialProfileUnavailable,
			operation,
		)
	}
}

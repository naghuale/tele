//go:build !darwin || !cgo

package authstore

import (
	"context"
	"errors"
)

// unsupportedStore reports that the platform has no usable credential
// store.
//
// A configured profile must fail closed on such a platform, so every
// operation returns ErrStoreUnavailable. There is deliberately no
// plaintext file fallback: a credential written to disk would silently
// downgrade the security model.
type unsupportedStore struct{}

// NewPlatformStore returns the store for the current build.
func NewPlatformStore() Store {
	return unsupportedStore{}
}

// Compile-time assertion: unsupportedStore implements Store.
var _ Store = unsupportedStore{}

// Load always reports the store as unavailable.
func (unsupportedStore) Load(
	context.Context,
	string,
) (Profile, error) {
	return Profile{}, unavailable("load credential profile")
}

// Create always reports the store as unavailable.
func (unsupportedStore) Create(
	context.Context,
	string,
	Profile,
) error {
	return unavailable("create credential profile")
}

// Replace always reports the store as unavailable.
func (unsupportedStore) Replace(
	context.Context,
	string,
	Profile,
) error {
	return unavailable("replace credential profile")
}

// Delete always reports the store as unavailable.
func (unsupportedStore) Delete(
	context.Context,
	string,
) error {
	return unavailable("delete credential profile")
}

func unavailable(operation string) error {
	return errors.Join(
		ErrStoreUnavailable,
		errors.New(operation+": this platform has no credential store"),
	)
}

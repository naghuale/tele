//go:build !darwin && !linux

package outbox

import "context"

// unsupportedPlatformKeyProvider is the fail-closed stub for platforms
// without a supported key source.
type unsupportedPlatformKeyProvider struct{}

// NewPlatformKeyProvider returns a fail-closed stub.
func NewPlatformKeyProvider() KeyProvider {
	return unsupportedPlatformKeyProvider{}
}

func (unsupportedPlatformKeyProvider) LoadKey(
	ctx context.Context,
	databaseID string,
) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateDatabaseID(databaseID); err != nil {
		return nil, err
	}
	return nil, ErrOutboxKeyProviderUnsupported
}

func (unsupportedPlatformKeyProvider) CreateKey(
	ctx context.Context,
	databaseID string,
) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateDatabaseID(databaseID); err != nil {
		return nil, err
	}
	return nil, ErrOutboxKeyProviderUnsupported
}

// Compile-time assertion.
var _ KeyProvider = unsupportedPlatformKeyProvider{}

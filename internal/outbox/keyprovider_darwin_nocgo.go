//go:build darwin && !cgo

package outbox

import "context"

// unsupportedDarwinKeyProvider keeps durable outbox activation disabled
// in macOS builds without cgo.
type unsupportedDarwinKeyProvider struct{}

// NewPlatformKeyProvider returns a fail-closed provider.
func NewPlatformKeyProvider() KeyProvider {
	return unsupportedDarwinKeyProvider{}
}

func (unsupportedDarwinKeyProvider) LoadKey(
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

func (unsupportedDarwinKeyProvider) CreateKey(
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

var _ KeyProvider = unsupportedDarwinKeyProvider{}

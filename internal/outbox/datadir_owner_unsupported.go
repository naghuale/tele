//go:build !linux && !darwin

package outbox

import "os"

// validateDataDirOwner is a no-op on platforms where this package does
// not expose Unix ownership metadata.
//
// Production activation remains gated by NewPlatformKeyProvider.
func validateDataDirOwner(
	_ os.FileInfo,
) error {
	return nil
}

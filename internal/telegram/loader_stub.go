//go:build !cgo || (!darwin && !linux)

package telegram

// LoadNative reports that native TDLib integration is unavailable in
// builds without CGO or on unsupported operating systems.
func LoadNative(string) (Native, error) {
	return nil, ErrNativeUnavailable
}

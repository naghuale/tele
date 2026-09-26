//go:build !cgo || (!darwin && !linux)

package telegram

// LoadedNative reports the candidate that produced a native runtime.
//
// Builds without cgo have no loader, so the type exists only to keep the
// public surface identical across build tags.
type LoadedNative struct {
	Native Native
	Source NativeLibrarySource
	Path   string
}

// LoadNative reports that native TDLib integration is unavailable in
// builds without CGO or on unsupported operating systems.
func LoadNative(string) (Native, error) {
	return nil, ErrNativeUnavailable
}

// LoadNativeWithSource reports that no native runtime can be loaded.
func LoadNativeWithSource(string) (LoadedNative, error) {
	return LoadedNative{}, ErrNativeUnavailable
}

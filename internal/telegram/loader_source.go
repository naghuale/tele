package telegram

// NativeLibraryCandidate is one place the loader may look for TDLib.
//
// It lives outside any build tag so the loader, the loader stub and the
// configuration wizard describe the same value.
type NativeLibraryCandidate struct {
	// Path is the library path. An empty value means the platform
	// loader default.
	Path string
	// Source records which rule produced the candidate.
	Source NativeLibrarySource
	// Explicit marks a path the operator asked for. An explicit
	// candidate that fails is fatal: the loader must not silently
	// substitute a different library.
	Explicit bool
}

// NativeLibrarySource names where a TDLib library came from.
//
// It lives outside any build tag so the loader stub and the native
// loader describe the same values.
//
// The source is reported by doctor so an operator can tell a packaged
// runtime from a development checkout without printing a local path.
type NativeLibrarySource string

const (
	// NativeLibrarySourceEnvironment is TELECLI_TDLIB_LIBRARY.
	NativeLibrarySourceEnvironment NativeLibrarySource = "environment"
	// NativeLibrarySourceConfigured is tdlib.library_path.
	NativeLibrarySourceConfigured NativeLibrarySource = "configured"
	// NativeLibrarySourcePackaged is the library shipped next to the
	// executable, in ../lib.
	NativeLibrarySourcePackaged NativeLibrarySource = "packaged"
	// NativeLibrarySourceDevelopment is the repository checkout path.
	NativeLibrarySourceDevelopment NativeLibrarySource = "development"
	// NativeLibrarySourcePlatform is the dynamic loader default.
	NativeLibrarySourcePlatform NativeLibrarySource = "platform"
)

package telegram

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// The packaged library policy lives outside any build tag on purpose.
//
// The runtime loader, the configuration wizard and the tests must agree on
// where a shipped library is, and a build without cgo still has to be able
// to answer that question. Keeping the computation here means there is one
// implementation rather than one per build.

// PackagedNativeLibraryCandidate returns the library shipped next to the
// executable.
//
// It is the single source of truth for the packaged location. The path is
// derived from the executable, never from the working directory, so a
// relocated package keeps working and a symlinked binary still finds the
// library beside the real executable.
//
// Computing the path needs neither cgo nor a native loader, so a build
// without cgo can still answer this. An error means the executable path
// could not be resolved; a zero candidate with a nil error means there is
// no packaged library to offer.
func PackagedNativeLibraryCandidate() (NativeLibraryCandidate, error) {
	executable, err := executablePath()
	if err != nil {
		return NativeLibraryCandidate{}, err
	}

	path := packagedTDLibPath(executable)
	if strings.TrimSpace(path) == "" {
		return NativeLibraryCandidate{}, nil
	}

	return NativeLibraryCandidate{
		Path:   path,
		Source: NativeLibrarySourcePackaged,
	}, nil
}

// packagedTDLibPath returns the library shipped next to the executable.
func packagedTDLibPath(executablePath string) string {
	if executablePath == "" || !filepath.IsAbs(executablePath) {
		return ""
	}

	return filepath.Clean(
		filepath.Join(
			filepath.Dir(executablePath),
			"..",
			"lib",
			defaultLibraryName(),
		),
	)
}

// executablePath returns the absolute, symlink-resolved executable path.
func executablePath() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executable path: %w", err)
	}

	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolve executable symlinks: %w", err)
	}

	absolute, err := filepath.Abs(resolved)
	if err != nil {
		return "", fmt.Errorf("resolve executable absolute path: %w", err)
	}

	return absolute, nil
}

func defaultLibraryName() string {
	if runtime.GOOS == "darwin" {
		return "libtdjson.dylib"
	}

	return "libtdjson.so"
}

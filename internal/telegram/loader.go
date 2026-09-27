//go:build cgo && (darwin || linux)

package telegram

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"

	"telecli/internal/telegram/tdjson"
)

const tdlibLibraryEnvironment = "TELECLI_TDLIB_LIBRARY"

// LoadedNative is a native runtime together with the candidate that
// produced it.
type LoadedNative struct {
	Native Native
	Source NativeLibrarySource
	Path   string
}

// LoadNative loads the TDLib JSON runtime.
//
// Discovery order:
//
//  1. TELECLI_TDLIB_LIBRARY
//  2. explicit configuration path
//  3. packaged path next to the executable
//  4. repository development path (telecli_dev builds only)
//  5. platform locations (absolute paths on macOS, loader default on Linux)
//
// No candidate is resolved from the working directory in a release
// build, so starting telecli from an untrusted directory cannot load a
// library planted there.
//
// An explicit candidate, whether from the environment or from the
// configuration, is the only candidate. A path the operator chose is
// never silently replaced by another library.
func LoadNative(configuredPath string) (Native, error) {
	loaded, err := LoadNativeWithSource(configuredPath)
	if err != nil {
		return nil, err
	}

	return loaded.Native, nil
}

// LoadNativeWithSource loads TDLib and reports which candidate was used.
func LoadNativeWithSource(configuredPath string) (LoadedNative, error) {
	executable, err := executablePath()
	if err != nil {
		// A missing executable path only removes the packaged
		// candidate; the remaining candidates stay usable.
		executable = ""
	}

	candidates := libraryCandidates(configuredPath, executable)

	var openErrors []error

	for _, candidate := range candidates {
		native, openErr := tdjson.Open(candidate.Path)
		if openErr == nil {
			return LoadedNative{
				Native: native,
				Source: candidate.Source,
				Path:   candidate.Path,
			}, nil
		}

		openErrors = append(
			openErrors,
			fmt.Errorf(
				"%s: %w",
				displayCandidatePath(candidate),
				openErr,
			),
		)

		if candidate.Explicit {
			// The operator named this library. Continuing would
			// run against something they did not ask for.
			return LoadedNative{}, errors.Join(
				append([]error{ErrNativeUnavailable}, openErrors...)...,
			)
		}
	}

	return LoadedNative{}, errors.Join(
		append([]error{ErrNativeUnavailable}, openErrors...)...,
	)
}

// libraryCandidates builds the ordered, de-duplicated candidate list.
//
// The environment and the configured path are handled structurally: when
// either is present it becomes the only candidate, so the fail-closed
// behaviour cannot be lost by a later edit to the ordering.
func libraryCandidates(
	configuredPath string,
	executablePath string,
) []NativeLibraryCandidate {
	if environmentPath := strings.TrimSpace(
		os.Getenv(tdlibLibraryEnvironment),
	); environmentPath != "" {
		return []NativeLibraryCandidate{
			{
				Path:     environmentPath,
				Source:   NativeLibrarySourceEnvironment,
				Explicit: true,
			},
		}
	}

	if configured := strings.TrimSpace(configuredPath); configured != "" {
		return []NativeLibraryCandidate{
			{
				Path:     configured,
				Source:   NativeLibrarySourceConfigured,
				Explicit: true,
			},
		}
	}

	var candidates []NativeLibraryCandidate

	// packagedTDLibPath is the same computation the exported
	// PackagedNativeLibraryCandidate uses, so there is one packaged
	// policy. The executable path stays a parameter here because it is
	// the seam the tests drive.
	if packaged := packagedTDLibPath(executablePath); packaged != "" {
		candidates = append(candidates, NativeLibraryCandidate{
			Path:   packaged,
			Source: NativeLibrarySourcePackaged,
		})
	}

	if developmentLibrarySearch {
		candidates = append(candidates, NativeLibraryCandidate{
			Path:   DevelopmentLibraryPath(),
			Source: NativeLibrarySourceDevelopment,
		})
	}

	for _, path := range platformLibraryPaths() {
		// An empty path asks the dynamic loader to use its own default
		// search.
		candidates = append(candidates, NativeLibraryCandidate{
			Path:   path,
			Source: NativeLibrarySourcePlatform,
		})
	}

	return uniqueLibraryCandidates(candidates)
}

// packagedTDLibPath returns the library shipped next to the executable.
//
// displayCandidatePath renders a candidate for an error message.
//
// The platform default has no path of its own, so it is shown by name.
func displayCandidatePath(candidate NativeLibraryCandidate) string {
	if candidate.Path != "" {
		return candidate.Path
	}

	return defaultLibraryName()
}

// uniqueLibraryCandidates removes duplicates while keeping the first
// occurrence, so the highest-priority source for a path is preserved.
func uniqueLibraryCandidates(
	candidates []NativeLibraryCandidate,
) []NativeLibraryCandidate {
	result := make([]NativeLibraryCandidate, 0, len(candidates))
	seen := make(map[string]struct{}, len(candidates))

	for _, candidate := range candidates {
		key := string(candidate.Source) + "\x00" + candidate.Path
		if _, exists := seen[key]; exists {
			continue
		}

		seen[key] = struct{}{}
		result = append(result, candidate)
	}

	return result
}

// platformLibraryPaths returns the platform candidates.
//
// On macOS a bare leaf name makes dlopen fall back to the working
// directory, so only absolute system locations are offered there. On
// Linux the dynamic linker never searches the working directory for a
// leaf name, so the loader default is kept and signalled by an empty
// path.
func platformLibraryPaths() []string {
	if runtime.GOOS == "darwin" {
		return []string{
			"/opt/homebrew/lib/libtdjson.dylib",
			"/usr/local/lib/libtdjson.dylib",
		}
	}

	return []string{""}
}

//go:build cgo && (darwin || linux)

package telegram

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"telecli/internal/telegram/tdjson"
)

const tdlibLibraryEnvironment = "TELECLI_TDLIB_LIBRARY"

// LoadNative loads the TDLib JSON runtime.
//
// Discovery order:
//  1. TELECLI_TDLIB_LIBRARY
//  2. explicit configuration path
//  3. repository development path
//  4. platform loader default
func LoadNative(configuredPath string) (Native, error) {
	candidates := libraryCandidates(configuredPath)

	var openErrors []error

	for _, candidate := range candidates {
		native, err := tdjson.Open(candidate)
		if err == nil {
			return native, nil
		}

		displayPath := candidate
		if displayPath == "" {
			displayPath = defaultLibraryName()
		}

		openErrors = append(
			openErrors,
			fmt.Errorf("%s: %w", displayPath, err),
		)
	}

	return nil, errors.Join(
		append([]error{ErrNativeUnavailable}, openErrors...)...,
	)
}

func libraryCandidates(configuredPath string) []string {
	var candidates []string

	if environmentPath := os.Getenv(tdlibLibraryEnvironment); environmentPath != "" {
		candidates = append(candidates, environmentPath)
	}

	if configuredPath != "" {
		candidates = append(candidates, configuredPath)
	}

	candidates = append(
		candidates,
		filepath.Join("third_party", "tdlib", "lib", defaultLibraryName()),
	)

	// Empty path tells tdjson.Open to use the platform loader default.
	candidates = append(candidates, "")

	return uniqueStrings(candidates)
}

func defaultLibraryName() string {
	if runtime.GOOS == "darwin" {
		return "libtdjson.dylib"
	}
	return "libtdjson.so"
}

func uniqueStrings(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{})

	for _, value := range values {
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}

	return result
}

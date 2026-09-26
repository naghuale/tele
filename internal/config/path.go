package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// configPathEnvironment overrides the default configuration path.
//
// It is intended for tests, CI and users who keep the configuration
// outside the per-user config directory.
const configPathEnvironment = "TELECLI_CONFIG"

// PathSource describes where a resolved configuration path came from.
type PathSource string

const (
	// PathSourceExplicit is a path passed on the command line.
	PathSourceExplicit PathSource = "explicit"
	// PathSourceEnvironment is a path taken from TELECLI_CONFIG.
	PathSourceEnvironment PathSource = "environment"
	// PathSourceDefault is the per-user configuration file.
	PathSourceDefault PathSource = "default"
	// PathSourceNone means no configuration file exists and the
	// built-in defaults apply.
	PathSourceNone PathSource = "none"
)

var (
	// ErrConfigNotFound is returned when a configuration path was
	// requested explicitly but the file does not exist.
	ErrConfigNotFound = errors.New("config: file not found")
	// ErrConfigDirUnavailable is returned when the per-user config
	// directory cannot be determined.
	ErrConfigDirUnavailable = errors.New(
		"config: user config directory unavailable",
	)
)

// ResolvedPath is the outcome of configuration path resolution.
type ResolvedPath struct {
	// Path is the configuration file to read. It is empty when Source
	// is PathSourceNone.
	Path string
	// Source records which rule produced Path.
	Source PathSource
}

// Found reports whether a configuration file was selected.
//
// The zero value counts as not found so a failed resolution can never be
// mistaken for a usable path.
func (r ResolvedPath) Found() bool {
	switch r.Source {
	case PathSourceExplicit, PathSourceEnvironment, PathSourceDefault:
		return true
	default:
		return false
	}
}

// DefaultPath returns the per-user configuration file path.
//
// It uses os.UserConfigDir so macOS and Linux share one contract, and
// never hardcodes ~/.config.
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf(
			"%w: %w",
			ErrConfigDirUnavailable,
			err,
		)
	}

	return filepath.Join(dir, "telecli", "config.toml"), nil
}

// ResolvePath selects the configuration file to read.
//
// Precedence:
//
//  1. an explicitly passed path;
//  2. the TELECLI_CONFIG environment variable;
//  3. the per-user configuration file;
//  4. the built-in defaults when no file exists.
//
// A path that was requested explicitly or through the environment must
// exist. Silently falling back to defaults would hide a typo in a path
// the user deliberately provided, so a missing file is an error.
//
// A missing default file is not an error: it is the first run before the
// setup flow. A default file that exists but cannot be parsed is an
// error, raised later by Load.
//
// The returned error may name the configuration path. It never contains
// the file contents.
func ResolvePath(explicitPath string) (ResolvedPath, error) {
	if explicitPath != "" {
		if err := requireFile(explicitPath); err != nil {
			return ResolvedPath{}, err
		}

		return ResolvedPath{
			Path:   explicitPath,
			Source: PathSourceExplicit,
		}, nil
	}

	if envPath := os.Getenv(configPathEnvironment); envPath != "" {
		if err := requireFile(envPath); err != nil {
			return ResolvedPath{}, err
		}

		return ResolvedPath{
			Path:   envPath,
			Source: PathSourceEnvironment,
		}, nil
	}

	defaultPath, err := DefaultPath()
	if err != nil {
		// Without a config directory the user simply has no
		// configuration file. Defaults apply.
		return ResolvedPath{Source: PathSourceNone}, nil
	}

	if _, statErr := os.Stat(defaultPath); statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			return ResolvedPath{Source: PathSourceNone}, nil
		}

		return ResolvedPath{}, fmt.Errorf(
			"config: stat %s: %w",
			defaultPath,
			statErr,
		)
	}

	return ResolvedPath{
		Path:   defaultPath,
		Source: PathSourceDefault,
	}, nil
}

func requireFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: %s", ErrConfigNotFound, path)
		}

		return fmt.Errorf("config: stat %s: %w", path, err)
	}

	if info.IsDir() {
		return fmt.Errorf(
			"%w: %s is a directory",
			ErrConfigNotFound,
			path,
		)
	}

	return nil
}

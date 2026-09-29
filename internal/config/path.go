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

// configDirEnvironment is the XDG variable that replaces ~/.config.
//
// It is read on macOS as well as on Linux: the owner decided on
// 29.09.2026 that the settings of telecli live in ~/.config, and the
// tools around it keep theirs there too.
const configDirEnvironment = "XDG_CONFIG_HOME"

// The per-user names of the configuration file.
const (
	// appDirectoryName is the directory telecli owns in every
	// per-user location.
	appDirectoryName = "telecli"

	// configFileName is the name of the settings file inside it.
	configFileName = "config.toml"
)

// PathSource describes where a resolved configuration path came from.
type PathSource string

const (
	// PathSourceExplicit is a path passed on the command line.
	PathSourceExplicit PathSource = "explicit"
	// PathSourceEnvironment is a path taken from TELECLI_CONFIG.
	PathSourceEnvironment PathSource = "environment"
	// PathSourceDefault is the per-user configuration file.
	PathSourceDefault PathSource = "default"
	// PathSourceLegacy is the file this build used before 29.09.2026,
	// kept working because the move to the new path has not finished.
	PathSourceLegacy PathSource = "legacy"
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
	// ErrConfigAmbiguous is returned when a configuration file is in
	// the new place and in the old one at the same time.
	ErrConfigAmbiguous = errors.New(
		"config: the settings file is in two places",
	)
)

// ResolvedPath is the outcome of configuration path resolution.
type ResolvedPath struct {
	// Path is the configuration file to read. It is empty when Source
	// is PathSourceNone.
	Path string
	// Source records which rule produced Path.
	Source PathSource
	// Notice is the one line a run prints about the settings file: it
	// was moved to the new place, or the move was refused and why.
	// It is empty when nothing about the file changed.
	Notice string
}

// Found reports whether a configuration file was selected.
//
// The zero value counts as not found so a failed resolution can never be
// mistaken for a usable path.
func (r ResolvedPath) Found() bool {
	switch r.Source {
	case PathSourceExplicit,
		PathSourceEnvironment,
		PathSourceDefault,
		PathSourceLegacy:
		return true
	default:
		return false
	}
}

// DefaultPath returns the per-user configuration file path.
//
// The owner decided on 29.09.2026 that it is
// $XDG_CONFIG_HOME/telecli/config.toml, and ~/.config/telecli/
// config.toml when that variable is not set, on macOS as well as on
// Linux. os.UserConfigDir would put it under ~/Library/Application
// Support on macOS, and a path with a space in a hidden folder is hard
// to find and to type.
//
// A relative $XDG_CONFIG_HOME is ignored, as the XDG specification says
// it must be: a relative directory would resolve from whatever directory
// telecli happens to start in, so the same machine would read two
// different files depending on the working directory.
func DefaultPath() (string, error) {
	if dir := os.Getenv(configDirEnvironment); filepath.IsAbs(dir) {
		return filepath.Join(
			dir,
			appDirectoryName,
			configFileName,
		), nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrConfigDirUnavailable, err)
	}
	if !filepath.IsAbs(home) {
		return "", fmt.Errorf(
			"%w: %s is not absolute",
			ErrConfigDirUnavailable,
			home,
		)
	}

	return filepath.Join(
		home,
		".config",
		appDirectoryName,
		configFileName,
	), nil
}

// LegacyPath returns the path this build used before 29.09.2026, or ""
// when the platform has no separate old path.
//
// On macOS it is ~/Library/Application Support/telecli/config.toml. On
// Linux os.UserConfigDir is the new place already, so there is no old
// one and no file to move: LegacyPath is empty rather than the new path
// a second time.
func LegacyPath() string {
	dir, err := os.UserConfigDir()
	if err != nil || !filepath.IsAbs(dir) {
		return ""
	}

	legacy := filepath.Join(dir, appDirectoryName, configFileName)

	current, err := DefaultPath()
	if err != nil || current == legacy {
		return ""
	}

	return legacy
}

// AmbiguityError reports the pair of settings files when the new one and
// the old one both exist, and nil when there is no such pair.
//
// telecli cannot choose between them: the file a user edited by hand and
// the file a build wrote may disagree, and silently preferring either one
// is how a machine ends up with settings nobody chose.
func AmbiguityError(currentPath string) error {
	legacy := LegacyPath()
	if currentPath == "" || legacy == "" || legacy == currentPath {
		return nil
	}

	currentExists, err := fileExists(currentPath)
	if err != nil {
		return err
	}
	if !currentExists {
		return nil
	}

	legacyExists, err := fileExists(legacy)
	if err != nil {
		return err
	}
	if !legacyExists {
		return nil
	}

	return fmt.Errorf(
		"%w: %s and %s; keep the one you want and remove the other",
		ErrConfigAmbiguous,
		currentPath,
		legacy,
	)
}

// WriteTarget returns the path a command that creates or changes the
// configuration must use: an explicit path, then TELECLI_CONFIG, then
// the new default path.
//
// The old path is never a target. Writing there would recreate the layout
// the new place replaces, and the move would start over on the next run.
func WriteTarget(explicitPath string) (string, error) {
	if explicitPath != "" {
		return explicitPath, nil
	}

	if envPath := os.Getenv(configPathEnvironment); envPath != "" {
		return envPath, nil
	}

	return DefaultPath()
}

// ResolvePath selects the configuration file to read, and moves the old
// one out of the way when it is still there.
//
// Precedence:
//
//  1. an explicitly passed path;
//  2. the TELECLI_CONFIG environment variable;
//  3. the per-user configuration file in ~/.config/telecli;
//  4. the file this build used before 29.09.2026, which is read while it
//     is still there and carries a Notice about the move;
//  5. the built-in defaults when no file exists.
//
// A path that was requested explicitly or through the environment must
// exist. Silently falling back to defaults would hide a typo in a path
// the user deliberately provided, so a missing file is an error.
//
// A file in the new place and in the old one at the same time is an
// error, not a choice: the run stops and the message names both paths.
//
// A missing default file is not an error: it is the first run before the
// setup flow. A default file that exists but cannot be parsed is an
// error, raised later by Load.
//
// The returned error may name the configuration path. It never contains
// the file contents.
func ResolvePath(explicitPath string) (ResolvedPath, error) {
	return resolvePath(explicitPath, MigrationOps{})
}

// resolvePath is the testable form: the move of the old file is an input
// rather than the file system, so a test can fail one of its steps.
func resolvePath(
	explicitPath string,
	ops MigrationOps,
) (ResolvedPath, error) {
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

	currentPath, err := DefaultPath()
	if err != nil {
		// Without a config directory the user simply has no
		// configuration file. Defaults apply.
		return ResolvedPath{Source: PathSourceNone}, nil
	}

	if err := AmbiguityError(currentPath); err != nil {
		return ResolvedPath{}, err
	}

	currentExists, err := fileExists(currentPath)
	if err != nil {
		return ResolvedPath{}, err
	}
	if currentExists {
		return ResolvedPath{
			Path:   currentPath,
			Source: PathSourceDefault,
		}, nil
	}

	legacyPath := LegacyPath()
	legacyExists := false
	if legacyPath != "" {
		if legacyExists, err = fileExists(legacyPath); err != nil {
			return ResolvedPath{}, err
		}
	}
	if !legacyExists {
		return ResolvedPath{Source: PathSourceNone}, nil
	}

	// The old file is still the one the user configured, so it is read
	// either way. Whether telecli ends up reading it or the copy is the
	// only question the move answers, and a move that failed must not
	// cost the user their settings.
	moved := migrateSettingsFile(legacyPath, currentPath, ops)
	if moved.Err != nil {
		return ResolvedPath{
			Path:   legacyPath,
			Source: PathSourceLegacy,
			Notice: migrationNotice(moved),
		}, nil
	}

	return ResolvedPath{
		Path:   currentPath,
		Source: PathSourceDefault,
		Notice: migrationNotice(moved),
	}, nil
}

// migrationNotice is the one line a run prints about the move of the
// settings file: what went where, or why it did not happen.
func migrationNotice(moved Migration) string {
	if moved.Err != nil {
		return fmt.Sprintf(
			"telecli could not move the settings file from %s to %s: "+
				"%v. The old file is in place and unchanged.",
			moved.From,
			moved.To,
			moved.Err,
		)
	}

	return fmt.Sprintf(
		"telecli moved the settings file from %s to %s. "+
			"The old file is now %s.",
		moved.From,
		moved.To,
		moved.Backup,
	)
}

// fileExists reports whether path is a file that is there. A missing
// file is not an error: it is what a first run looks like.
func fileExists(path string) (bool, error) {
	if path == "" {
		return false, nil
	}

	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}

		return false, fmt.Errorf("config: stat %s: %w", path, err)
	}

	return true, nil
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

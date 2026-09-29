package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// File modes for generated configuration.
const (
	// ConfigFileMode keeps the configuration readable by its owner
	// only. The file holds no secrets, but the API ID and the data
	// layout are still private.
	ConfigFileMode os.FileMode = 0o600
	// ConfigDirMode keeps the configuration directory owner-only.
	ConfigDirMode os.FileMode = 0o700
)

var (
	// ErrConfigWrite reports a failed configuration write.
	ErrConfigWrite = errors.New("config: write")
	// ErrSecretInConfig reports a refused write because a secret value
	// was about to be persisted.
	ErrSecretInConfig = errors.New("config: refusing to write a secret")
)

// WriteFile writes cfg to path atomically.
//
// The file is written to a temporary sibling with mode 0600, flushed,
// and then renamed over the target, so a reader never observes a partial
// file and a failure leaves any previous configuration intact.
func WriteFile(path string, cfg Config) error {
	encoded, err := Encode(cfg)
	if err != nil {
		return err
	}

	return WriteRaw(path, encoded)
}

// WriteRaw writes an already rendered configuration to path atomically.
//
// It is the same write WriteFile does, without the rendering, for the
// commands that change one value in a file a person wrote: the bytes
// around that value are theirs, and only the value may change.
func WriteRaw(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, ConfigDirMode); err != nil {
		return fmt.Errorf("%w: create %s: %w", ErrConfigWrite, dir, err)
	}

	temp, err := os.CreateTemp(dir, ".config-*.toml")
	if err != nil {
		return fmt.Errorf("%w: create temporary file: %w", ErrConfigWrite, err)
	}

	tempName := temp.Name()

	cleanup := func() {
		_ = temp.Close()
		_ = os.Remove(tempName)
	}

	// CreateTemp already uses 0600, but the mode is set explicitly so
	// the guarantee does not depend on that implementation detail.
	if err := temp.Chmod(ConfigFileMode); err != nil {
		cleanup()
		return fmt.Errorf("%w: chmod temporary file: %w", ErrConfigWrite, err)
	}

	if _, err := temp.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("%w: write temporary file: %w", ErrConfigWrite, err)
	}

	if err := temp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("%w: sync temporary file: %w", ErrConfigWrite, err)
	}

	if err := temp.Close(); err != nil {
		_ = os.Remove(tempName)
		return fmt.Errorf("%w: close temporary file: %w", ErrConfigWrite, err)
	}

	if err := os.Rename(tempName, path); err != nil {
		_ = os.Remove(tempName)
		return fmt.Errorf("%w: rename into place: %w", ErrConfigWrite, err)
	}

	return nil
}

// Encode renders cfg as TOML.
//
// The struct has no field for a credential, so a secret cannot reach the
// file even by accident. The check below is a second, explicit guard
// against a future field that would reintroduce one.
func Encode(cfg Config) ([]byte, error) {
	if err := rejectSecrets(cfg); err != nil {
		return nil, err
	}

	var builder strings.Builder
	encoder := toml.NewEncoder(&builder)

	if err := encoder.Encode(cfg); err != nil {
		return nil, fmt.Errorf("%w: encode: %w", ErrConfigWrite, err)
	}

	encoded := []byte(builder.String())

	if err := rejectSecrets(encoded); err != nil {
		return nil, err
	}

	return encoded, nil
}

// rejectSecrets fails when a rendered configuration contains a secret
// field name.
func rejectSecrets[T any](value T) error {
	var rendered string

	switch typed := any(value).(type) {
	case Config:
		rendered = fmt.Sprintf("%+v", typed)
	case []byte:
		rendered = string(typed)
	default:
		rendered = fmt.Sprintf("%v", typed)
	}

	lowered := strings.ToLower(rendered)
	for _, forbidden := range []string{
		"api_hash",
		"apihash",
		"phone",
		"password",
		"database_encryption_key",
	} {
		if strings.Contains(lowered, forbidden) {
			return fmt.Errorf(
				"%w: %q must never be persisted",
				ErrSecretInConfig,
				forbidden,
			)
		}
	}

	return nil
}

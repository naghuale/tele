package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// TDLib holds runtime library discovery and lifecycle timeouts.
type TDLib struct {
	LibraryPath       string `toml:"library_path"`
	DatabaseDir       string `toml:"database_dir"`
	FilesDir          string `toml:"files_dir"`
	ReceiveTimeoutMS  int    `toml:"receive_timeout_ms"`
	ShutdownTimeoutMS int    `toml:"shutdown_timeout_ms"`
}

type MessageDeliveryConfig struct {
	Mode       MessageSendMode `toml:"mode"`
	DataDir    string          `toml:"data_dir"`
	DatabaseID string          `toml:"database_id"`
	InstanceID string          `toml:"instance_id"`
}

// AuthConfig holds the non-secret part of the Telegram application
// configuration.
//
// Secrets are never stored in the config file: APIHash and Phone are
// resolved from a credential store under CredentialProfile. Only the
// non-secret API ID and the profile selector live here.
type AuthConfig struct {
	APIID             int    `toml:"api_id"`
	CredentialProfile string `toml:"credential_profile"`
}

// Configured reports whether the config selects a Telegram application
// at all. A zero value means the user never ran the setup flow.
func (a AuthConfig) Configured() bool {
	return a.APIID != 0 || a.CredentialProfile != ""
}

// Config is the validated telecli configuration.
type Config struct {
	LogLevel        string                `toml:"log_level"`
	DataDir         string                `toml:"data_dir"`
	TDLib           TDLib                 `toml:"tdlib"`
	Auth            AuthConfig            `toml:"auth"`
	MessageDelivery MessageDeliveryConfig `toml:"message_delivery"`

	// Warnings holds non-fatal notes produced while loading, such as a
	// retired send mode that was normalised rather than rejected. It is
	// never persisted: the file stays as the user wrote it, and the note
	// is repeated by telecli doctor on every run.
	Warnings []string `toml:"-"`
}

// ErrDataDirUnavailable reports that no data directory is configured
// and none can be derived from the home directory.
var ErrDataDirUnavailable = errors.New(
	"data_dir is not set and no absolute home directory is available; " +
		"set data_dir to an absolute path",
)

// Default returns the built-in defaults.
//
// Without an absolute home directory DataDir and the TDLib directories
// stay empty, and Validate rejects the result until data_dir is set.
func Default() Config {
	dataDir := defaultDataDir()

	return Config{
		LogLevel: "info",
		DataDir:  dataDir,
		TDLib: TDLib{
			DatabaseDir:       dataSubdir(dataDir, "tdlib", "database"),
			FilesDir:          dataSubdir(dataDir, "tdlib", "files"),
			ReceiveTimeoutMS:  100,
			ShutdownTimeoutMS: 5000,
		},
		MessageDelivery: MessageDeliveryConfig{
			Mode: DefaultMessageSendMode,
		},
	}
}

// Validate checks the configuration.
//
// TDLib.LibraryPath is optional: an empty value means runtime discovery.
// TDLib.DatabaseDir and TDLib.FilesDir are populated by Default but are
// not yet opened on disk in PR-04.
func (c Config) Validate() error {
	if c.LogLevel == "" {
		return fmt.Errorf("log_level must not be empty")
	}
	if c.DataDir == "" {
		return ErrDataDirUnavailable
	}
	// A relative directory would resolve from wherever telecli starts,
	// placing the session and the outbox in an arbitrary directory.
	for _, dir := range []struct {
		key      string
		path     string
		required bool
	}{
		{"data_dir", c.DataDir, true},
		{"tdlib.database_dir", c.TDLib.DatabaseDir, true},
		{"tdlib.files_dir", c.TDLib.FilesDir, true},
		{"message_delivery.data_dir", c.MessageDelivery.DataDir, false},
	} {
		if dir.path == "" {
			if dir.required {
				return fmt.Errorf("%s must not be empty", dir.key)
			}
			continue
		}
		if !filepath.IsAbs(dir.path) {
			return fmt.Errorf("%s must be an absolute path", dir.key)
		}
	}
	if err := c.MessageDelivery.Mode.Validate(); err != nil {
		return fmt.Errorf("message_delivery.mode: %w", err)
	}
	if c.TDLib.ReceiveTimeoutMS <= 0 {
		return fmt.Errorf("tdlib.receive_timeout_ms must be > 0")
	}
	if c.TDLib.ShutdownTimeoutMS <= 0 {
		return fmt.Errorf("tdlib.shutdown_timeout_ms must be > 0")
	}
	return nil
}

// defaultDataDir returns the per-user data directory, or "" when no
// absolute home directory is available. It never falls back to a
// relative path.
func defaultDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(home) {
		return ""
	}
	return filepath.Join(home, ".local", "share", "telecli")
}

// dataSubdir joins elem under dataDir, or returns "" for an unset
// dataDir so no relative path is ever produced.
func dataSubdir(dataDir string, elem ...string) string {
	if dataDir == "" {
		return ""
	}
	return filepath.Join(append([]string{dataDir}, elem...)...)
}

// deriveUnsetDirectories fills TDLib directories that are still unset
// from DataDir. It matters when the defaults had no home directory and
// the file supplies only data_dir.
func (c *Config) deriveUnsetDirectories() {
	if c.TDLib.DatabaseDir == "" {
		c.TDLib.DatabaseDir = dataSubdir(c.DataDir, "tdlib", "database")
	}
	if c.TDLib.FilesDir == "" {
		c.TDLib.FilesDir = dataSubdir(c.DataDir, "tdlib", "files")
	}
}

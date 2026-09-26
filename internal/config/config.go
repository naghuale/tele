package config

import (
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
}

// Default returns validated defaults.
func Default() Config {
	dataDir := defaultDataDir()

	return Config{
		LogLevel: "info",
		DataDir:  dataDir,
		TDLib: TDLib{
			DatabaseDir:       filepath.Join(dataDir, "tdlib", "database"),
			FilesDir:          filepath.Join(dataDir, "tdlib", "files"),
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
		return fmt.Errorf("data_dir must not be empty")
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

func defaultDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".telecli"
	}
	return filepath.Join(home, ".local", "share", "telecli")
}

package config

import (
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
)

// Load returns a validated configuration.
//
// An empty path yields the built-in defaults. A non-empty path must
// exist and must parse; unknown keys and invalid values are errors.
//
// Load keeps the historical single-path contract used by --config. Code
// that wants the documented precedence should resolve the path first and
// call LoadResolved.
func Load(path string) (Config, error) {
	if path == "" {
		cfg := Default()
		if err := cfg.Validate(); err != nil {
			return Config{}, err
		}

		return cfg, nil
	}

	return loadFile(path)
}

// LoadResolved returns a validated configuration for an already resolved
// path.
//
// PathSourceNone means no configuration file exists, so the built-in
// defaults apply. Every other source requires a readable file.
func LoadResolved(resolved ResolvedPath) (Config, error) {
	if !resolved.Found() {
		cfg := Default()
		if err := cfg.Validate(); err != nil {
			return Config{}, err
		}

		return cfg, nil
	}

	return loadFile(resolved.Path)
}

func loadFile(path string) (Config, error) {
	cfg := Default()

	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}

	md, err := toml.Decode(string(data), &cfg)
	if err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}

	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		return Config{}, fmt.Errorf("unknown config keys: %v", undecoded)
	}

	mode, legacy, err := ParseMessageSendMode(string(cfg.MessageDelivery.Mode))
	if err != nil {
		return Config{}, fmt.Errorf("parse message delivery mode: %w", err)
	}
	if legacy {
		cfg.Warnings = append(
			cfg.Warnings,
			`message_delivery.mode = "direct" is no longer supported; `+
				"durable delivery is used instead",
		)
	}
	cfg.MessageDelivery.Mode = mode

	cfg.deriveUnsetDirectories()

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

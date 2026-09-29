package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
)

// tuiSection is the one section where an unknown key is a note rather
// than an error.
//
// The [tui] section holds the appearance, and a build learns a new
// setting after a user has already written one: a person who added
// nerd_font before this build knew it would otherwise lose every command,
// including the one that would tell them why. The name of the setting is
// in the note, so a typo is still visible, and the rest of the file is
// read as it is written. Every other section stays strict: there an
// unknown key is a misspelling of a setting that exists.
const tuiSection = "tui"

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

// Parse returns a validated configuration from a document in memory.
//
// It is how a command that has just rendered a document knows whether the
// document is one telecli would accept, before the document is written
// over a file a person keeps by hand.
func Parse(data []byte) (Config, error) {
	cfg := Default()

	md, err := toml.Decode(string(data), &cfg)
	if err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}

	for _, key := range md.Undecoded() {
		if len(key) > 0 && key[0] == tuiSection {
			cfg.Warnings = append(
				cfg.Warnings,
				fmt.Sprintf(
					"unknown [%s] setting %q is ignored by this "+
						"build; the rest of the file is read",
					tuiSection,
					strings.Join(key, "."),
				),
			)

			continue
		}

		return Config{}, fmt.Errorf("unknown config keys: %v", md.Undecoded())
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

func loadFile(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}

	return Parse(data)
}

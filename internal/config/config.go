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

// TUIConfig holds the interface settings.
//
// The three values are plain strings on purpose: the vocabulary of themes,
// colour modes and width rules belongs to internal/tui and
// internal/tui/termwidth, and this package is a leaf that must not import
// them. The values are therefore not validated here but by those packages,
// which report an unknown name as a configuration error with the valid
// names in it. An absent value is the default, because a configuration
// written before these settings existed has none.
type TUIConfig struct {
	// Theme is the name of a built-in theme.
	Theme string `toml:"theme"`

	// Color is "auto", "always" or "never".
	Color string `toml:"color"`

	// Width is "auto", "grapheme" or "codepoint".
	//
	// It is how the interface counts the width of text, which the two
	// rules of internal/tui/termwidth disagree about for a hand, a flag
	// and a family. Left empty it is "auto", which measures the terminal
	// before the first frame.
	Width string `toml:"width"`

	// Clock is "auto", "12h" or "24h": how the hour of a moment is written
	// on the screen.
	//
	// It is a machine's own answer and not this program's: a user ten hours
	// east of Greenwich reads "10:06" and not "04:06", and the system
	// already knows which of the two spellings they read fastest. Left
	// empty it is "auto", which asks the system — and falls back to
	// twenty-four hours on a machine that cannot be asked.
	Clock string `toml:"clock"`

	// NerdFont says whether the terminal is drawn with a Nerd Font, and
	// so whether the block of a message of this user may be rounded.
	//
	// It is a flag and not a word because there is nothing to choose
	// between: the glyphs are either there or they are not, and a
	// terminal does not report which font it has been given. It is off
	// by default because a terminal without one draws them as empty
	// squares, and a message with empty squares at both ends is worse
	// than a message with square ones.
	NerdFont bool `toml:"nerd_font"`

	// UnreadCounter is what the number in the header of the chat list
	// counts: "chats", "messages" or "off".
	//
	// The default is "chats" because that is what the counter in the same
	// place in Telegram counts, and because the sum of the messages of a
	// list is decided by one channel however small the badges on the rows
	// are. A configuration written before this setting existed has none of
	// it and gets the default, which is the same answer.
	UnreadCounter string `toml:"unread_counter"`
}

// The interface defaults, spelled out here so that Default is a complete
// configuration and `telecli configure` writes a file that says what it
// decided.
//
// The theme name is the one the specification lists first and draws its
// example in. It is duplicated from the theme package because that
// package must stay a leaf above this one; TestInterfaceDefaultsMatchThe
// ThemePackage in internal/application fails if the two ever disagree.
const (
	// DefaultTUITheme is the theme used when none is configured.
	DefaultTUITheme = "catppuccin-mocha"

	// DefaultTUIColorMode follows what the terminal reports.
	DefaultTUIColorMode = "auto"

	// DefaultTUIWidthMode measures the terminal and counts text the way
	// it draws it.
	//
	// It is spelled out here so that Default is a complete configuration
	// and `telecli configure` writes a file that says what it decided.
	// The word belongs to internal/tui/termwidth, which parses it; it is
	// duplicated for the same reason the theme name is, and
	// TestInterfaceDefaultsMatchTheWidthPackage in
	// internal/application fails if the two ever disagree.
	DefaultTUIWidthMode = "auto"

	// DefaultTUIClock asks the machine how it writes the hour.
	//
	// It is spelled out here so that Default is a complete configuration
	// and `telecli configure` writes a file that says what it decided. The
	// word belongs to internal/tui, which parses it; it is duplicated for
	// the same reason the theme name and the width rule are, and
	// TestInterfaceDefaultsMatchTheClockPackage in internal/application
	// fails if the two ever disagree.
	DefaultTUIClock = "auto"

	// DefaultTUINerdFont draws the block of a message of this user with
	// square ends.
	//
	// It is the zero value, so it is named for the same reason the other
	// three are: a configuration written by `telecli configure` says
	// what it decided, and what it decides about a font it cannot see is
	// that it does not assume one.
	DefaultTUINerdFont = false

	// DefaultTUIUnreadCounter counts the chats that have something unread
	// in them, and leaves out the silenced ones, which is what the same
	// header says in Telegram.
	//
	// It is spelled out here for the reason the theme name and the width
	// rule are: the word belongs to internal/tui, which parses it, and it
	// is duplicated for the same reason theirs is.
	DefaultTUIUnreadCounter = "chats"
)

// MessageDeliveryConfig holds the durable outbox settings.
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
	TUI             TUIConfig             `toml:"tui"`

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
		TUI: TUIConfig{
			Theme:         DefaultTUITheme,
			Color:         string(DefaultTUIColorMode),
			Width:         DefaultTUIWidthMode,
			Clock:         DefaultTUIClock,
			NerdFont:      DefaultTUINerdFont,
			UnreadCounter: DefaultTUIUnreadCounter,
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

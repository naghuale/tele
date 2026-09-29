package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// One setting of the file, and how `telecli configure set` and
// `telecli configure get` reach it.
//
// The table is here, next to the struct it names, so a setting cannot be
// added to the configuration without either being settable or the table
// naming why it is not.

// settingKind is what a value of a setting has to be.
type settingKind uint8

const (
	// settingString is any one-line text.
	settingString settingKind = iota
	// settingWholeNumber is a whole number, written without quotes.
	settingWholeNumber
	// settingAbsolutePath is a path that must not depend on the
	// working directory.
	settingAbsolutePath
	// settingSendMode is a delivery mode this build supports.
	settingSendMode
)

// setting is one settable value: where it is in the document, what it
// accepts, and how it is read and written.
type setting struct {
	// key is what a person types: tui.theme.
	key string
	// section is the table the value is in. An empty section is a
	// value before the first table.
	section string
	// name is the name in the document: theme.
	name  string
	kind  settingKind
	read  func(Config) string
	write func(*Config, string)
}

// canonical refuses a value this setting cannot hold and returns it in
// the spelling the file will hold, so what is written and what is read
// back are one value.
//
// The check happens before anything is written, so a refused value
// leaves the file exactly as it was.
func (s setting) canonical(value string) (string, error) {
	if value == "" {
		return "", fmt.Errorf("%s must not be empty", s.key)
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf(
				"%s must be one line of text: a control character "+
					"cannot be written into TOML",
				s.key,
			)
		}
	}

	switch s.kind {
	case settingWholeNumber:
		number, err := strconv.Atoi(value)
		if err != nil {
			return "", fmt.Errorf("%s must be a whole number", s.key)
		}

		return strconv.Itoa(number), nil

	case settingAbsolutePath:
		if !filepath.IsAbs(value) {
			return "", fmt.Errorf(
				"%s must be an absolute path, not %q",
				s.key,
				value,
			)
		}

	case settingSendMode:
		if _, _, err := ParseMessageSendMode(value); err != nil {
			return "", fmt.Errorf("%s: %w", s.key, err)
		}

	case settingString:
	}

	return value, nil
}

// render returns the TOML text of a canonical value.
func (s setting) render(value string) string {
	if s.kind == settingWholeNumber {
		return value
	}

	return strconv.Quote(value)
}

// settings are the values a person can set one at a time.
var settings = []setting{
	{
		key:   "log_level",
		name:  "log_level",
		read:  func(c Config) string { return c.LogLevel },
		write: func(c *Config, v string) { c.LogLevel = v },
		kind:  settingString,
	},
	{
		key:   "data_dir",
		name:  "data_dir",
		read:  func(c Config) string { return c.DataDir },
		write: func(c *Config, v string) { c.DataDir = v },
		kind:  settingAbsolutePath,
	},
	{
		key:     "tdlib.library_path",
		section: "tdlib",
		name:    "library_path",
		read:    func(c Config) string { return c.TDLib.LibraryPath },
		write:   func(c *Config, v string) { c.TDLib.LibraryPath = v },
		kind:    settingAbsolutePath,
	},
	{
		key:     "tdlib.database_dir",
		section: "tdlib",
		name:    "database_dir",
		read:    func(c Config) string { return c.TDLib.DatabaseDir },
		write:   func(c *Config, v string) { c.TDLib.DatabaseDir = v },
		kind:    settingAbsolutePath,
	},
	{
		key:     "tdlib.files_dir",
		section: "tdlib",
		name:    "files_dir",
		read:    func(c Config) string { return c.TDLib.FilesDir },
		write:   func(c *Config, v string) { c.TDLib.FilesDir = v },
		kind:    settingAbsolutePath,
	},
	{
		key:     "tdlib.receive_timeout_ms",
		section: "tdlib",
		name:    "receive_timeout_ms",
		read:    func(c Config) string { return strconv.Itoa(c.TDLib.ReceiveTimeoutMS) },
		write: func(c *Config, v string) {
			c.TDLib.ReceiveTimeoutMS, _ = strconv.Atoi(v)
		},
		kind: settingWholeNumber,
	},
	{
		key:     "tdlib.shutdown_timeout_ms",
		section: "tdlib",
		name:    "shutdown_timeout_ms",
		read:    func(c Config) string { return strconv.Itoa(c.TDLib.ShutdownTimeoutMS) },
		write: func(c *Config, v string) {
			c.TDLib.ShutdownTimeoutMS, _ = strconv.Atoi(v)
		},
		kind: settingWholeNumber,
	},
	{
		key:     "auth.api_id",
		section: "auth",
		name:    "api_id",
		read:    func(c Config) string { return strconv.Itoa(c.Auth.APIID) },
		write:   func(c *Config, v string) { c.Auth.APIID, _ = strconv.Atoi(v) },
		kind:    settingWholeNumber,
	},
	{
		key:     "auth.credential_profile",
		section: "auth",
		name:    "credential_profile",
		read:    func(c Config) string { return c.Auth.CredentialProfile },
		write:   func(c *Config, v string) { c.Auth.CredentialProfile = v },
		kind:    settingString,
	},
	{
		key:     "message_delivery.mode",
		section: "message_delivery",
		name:    "mode",
		read:    func(c Config) string { return string(c.MessageDelivery.Mode) },
		write:   func(c *Config, v string) { c.MessageDelivery.Mode = MessageSendMode(v) },
		kind:    settingSendMode,
	},
	{
		key:     "message_delivery.data_dir",
		section: "message_delivery",
		name:    "data_dir",
		read:    func(c Config) string { return c.MessageDelivery.DataDir },
		write:   func(c *Config, v string) { c.MessageDelivery.DataDir = v },
		kind:    settingAbsolutePath,
	},
	{
		key:     "message_delivery.database_id",
		section: "message_delivery",
		name:    "database_id",
		read:    func(c Config) string { return c.MessageDelivery.DatabaseID },
		write:   func(c *Config, v string) { c.MessageDelivery.DatabaseID = v },
		kind:    settingString,
	},
	{
		key:     "message_delivery.instance_id",
		section: "message_delivery",
		name:    "instance_id",
		read:    func(c Config) string { return c.MessageDelivery.InstanceID },
		write:   func(c *Config, v string) { c.MessageDelivery.InstanceID = v },
		kind:    settingString,
	},
	{
		key:     "tui.theme",
		section: tuiSection,
		name:    "theme",
		read:    func(c Config) string { return c.TUI.Theme },
		write:   func(c *Config, v string) { c.TUI.Theme = v },
		kind:    settingString,
	},
	{
		key:     "tui.color",
		section: tuiSection,
		name:    "color",
		read:    func(c Config) string { return c.TUI.Color },
		write:   func(c *Config, v string) { c.TUI.Color = v },
		kind:    settingString,
	},
	{
		key:     "tui.width",
		section: tuiSection,
		name:    "width",
		read:    func(c Config) string { return c.TUI.Width },
		write:   func(c *Config, v string) { c.TUI.Width = v },
		kind:    settingString,
	},
}

// SettingKeys returns the keys a person can set and read, in the order
// they are listed in a refusal.
func SettingKeys() []string {
	keys := make([]string, 0, len(settings))
	for _, s := range settings {
		keys = append(keys, s.key)
	}

	return keys
}

// ErrUnknownSetting reports a key that is not in the table above.
var ErrUnknownSetting = errors.New("config: unknown setting")

func findSetting(key string) (setting, error) {
	name := strings.TrimSpace(key)

	for _, s := range settings {
		if s.key == name {
			return s, nil
		}
	}

	return setting{}, fmt.Errorf(
		"%w: %q; the settings are: %s",
		ErrUnknownSetting,
		name,
		strings.Join(SettingKeys(), ", "),
	)
}

// SettingValue returns one setting as telecli resolved it, so `get`
// answers with what a start of the program will use and not with what the
// file happens to spell.
func SettingValue(cfg Config, key string) (string, error) {
	s, err := findSetting(key)
	if err != nil {
		return "", err
	}

	return s.read(cfg), nil
}

// SetValue changes one setting in the file at path.
//
// The file is not rendered again: only the line of that value changes and
// every other byte is left as the person who wrote the file left it. A
// file that has no such section gets one, and a file that does not exist
// is created with the value in it.
//
// An unknown key, a value the key cannot hold and a document that would
// not load are all refused before anything is written.
func SetValue(path, key, value string) error {
	s, err := findSetting(key)
	if err != nil {
		return err
	}

	canonical, err := s.canonical(value)
	if err != nil {
		return err
	}

	rendered := s.render(canonical)

	document, err := readDocument(path)
	if err != nil {
		return err
	}

	edited, err := setDocumentValue(document, s, rendered)
	if err != nil {
		return err
	}

	// The whole document is checked, not only the value: a file that
	// telecli cannot load is a problem a person has to hear about
	// before a command makes it worse, and never after.
	checked, err := Parse([]byte(edited))
	if err != nil {
		return fmt.Errorf(
			"%w: %s would not load with that value: %w",
			ErrConfigWrite,
			path,
			err,
		)
	}

	// And the value is read back out of the document that is about to
	// be written, so a file that does not hold what was asked for never
	// replaces one that does.
	if read := s.read(checked); read != canonical {
		return fmt.Errorf(
			"%w: %s would read back as %q, not %q",
			ErrConfigWrite,
			key,
			read,
			canonical,
		)
	}

	return WriteRaw(path, []byte(edited))
}

// readDocument returns the file at path, or an empty document when there
// is no file yet. A file is created by the command that changes it, so a
// missing one is the state before the first `set`, not a failure.
func readDocument(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}

		return "", fmt.Errorf("%w: read %s: %w", ErrConfigWrite, path, err)
	}

	return string(data), nil
}

// setDocumentValue returns document with the one value changed.
func setDocumentValue(
	document string,
	s setting,
	rendered string,
) (string, error) {
	lines := strings.Split(document, "\n")
	assignment := s.name + " = " + rendered

	from, to, found := sectionSpan(lines, s.section)

	if !found {
		return strings.Join(
			appendSection(lines, s.section, assignment),
			"\n",
		), nil
	}

	if index := keyLine(lines, s.name, from+1, to); index >= 0 {
		lines[index] = assignment

		return strings.Join(lines, "\n"), nil
	}

	return strings.Join(
		insertLine(lines, insertAt(lines, from, to), assignment),
		"\n",
	), nil
}

// sectionSpan returns the line range a table owns: its header is at from
// and its body ends before to. Values before the first table belong to
// the empty section, which is always there.
//
// found is false when the table itself is not in the document.
func sectionSpan(lines []string, section string) (from, to int, found bool) {
	if section == "" {
		for i, line := range lines {
			if _, isHeader := tableHeader(line); isHeader {
				return -1, i, true
			}
		}

		return -1, len(lines), true
	}

	from, to, found = 0, len(lines), false

	current := ""
	for i, line := range lines {
		name, isHeader := tableHeader(line)
		if !isHeader {
			continue
		}

		if current == section {
			to = i
		}
		current = name
		if name == section {
			from, found = i, true
		}
	}

	return from, to, found
}

// tableHeader returns the name in a `[name]` line.
func tableHeader(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "[") {
		return "", false
	}

	name := strings.TrimSuffix(
		strings.TrimPrefix(trimmed, "["),
		"]",
	)

	return strings.TrimSpace(name), true
}

// keyLine returns the line in [from, to) that sets name, or -1.
func keyLine(lines []string, name string, from, to int) int {
	for i := from; i < to; i++ {
		if key, ok := assignmentKey(lines[i]); ok && key == name {
			return i
		}
	}

	return -1
}

// assignmentKey returns the bare key of a `key = value` line.
//
// A comment, a line without `=` and a quoted or dotted key are not one:
// the first two are not a setting, and the last is a spelling this
// command does not touch.
func assignmentKey(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return "", false
	}

	name, _, found := strings.Cut(trimmed, "=")
	if !found {
		return "", false
	}

	name = strings.TrimSpace(name)
	if !bareKey(name) {
		return "", false
	}

	return name, true
}

// bareKey reports whether name is a key written without quotes.
func bareKey(name string) bool {
	if name == "" {
		return false
	}

	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9', r == '_', r == '-':
		default:
			return false
		}
	}

	return true
}

// insertAt is where a new line goes inside a table: after the last line
// that says something, so the blank lines a person left at the end of a
// table stay at the end of it.
func insertAt(lines []string, from, to int) int {
	at := to
	for at > from+1 && strings.TrimSpace(lines[at-1]) == "" {
		at--
	}

	return at
}

func insertLine(lines []string, at int, line string) []string {
	lines = append(lines, "")
	copy(lines[at+1:], lines[at:])
	lines[at] = line

	return lines
}

// appendSection adds a table with one value in it at the end of the
// document, keeping whatever the document ended with.
func appendSection(lines []string, section, assignment string) []string {
	// A document that is nothing but the empty line a file ends with is
	// a new file, and its first line should be the table rather than a
	// blank one.
	if len(lines) == 1 && strings.TrimSpace(lines[0]) == "" {
		lines = nil
	}

	if len(lines) > 0 && lines[len(lines)-1] != "" {
		// The document has no newline at its end, so one is needed
		// before anything can follow it.
		lines = append(lines, "")
	}

	return append(lines, "["+section+"]", assignment, "")
}

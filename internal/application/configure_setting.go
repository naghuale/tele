package application

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"telecli/internal/config"
	"telecli/internal/tui/termwidth"
	"telecli/internal/tui/theme"
)

// `telecli configure set` and `telecli configure get` exist because the
// settings file is read by a person as well as by the program. A person
// who wants to change the theme should not have to know the whole shape
// of the file to keep the rest of it, and should not be told to write
// the file out with a shell redirection: `printf ... > config.toml`
// replaces every setting in it, including the TDLib directories, the
// Telegram login and the message queue.
//
// So the command changes one value in place and refuses a key it does
// not know, and the help of every other command points here instead of
// at a text editor.

// runConfigureSet implements `telecli configure set <key> <value>`.
func runConfigureSet(args []string, env Environment) int {
	fs := flag.NewFlagSet("configure set", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)

	configPath := fs.String(
		"config",
		"",
		"path to the configuration file",
	)

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}

		return 2
	}

	rest := fs.Args()
	if len(rest) != 2 {
		fmt.Fprintf(
			env.Stderr,
			"configure error: `configure set` needs a key and a "+
				"value, for example: telecli configure set "+
				"tui.theme gruvbox\n",
		)
		fmt.Fprintf(
			env.Stderr,
			"The settings are: %s\n",
			strings.Join(config.SettingKeys(), ", "),
		)

		return 2
	}

	path, resolved, err := configEditTarget(*configPath)
	if err != nil {
		fmt.Fprintf(env.Stderr, "configure error: %v\n", err)
		return 1
	}

	// The vocabulary of the interface settings belongs to the packages
	// above this one, so it is checked here, before the file is opened
	// for writing: a theme this build does not know is a value the next
	// start of the TUI would refuse, and a person must hear about it
	// from the command that set it.
	if err := checkSettingValue(rest[0], rest[1]); err != nil {
		fmt.Fprintf(env.Stderr, "configure error: %v\n", err)
		return 1
	}

	if err := config.SetValue(path, rest[0], rest[1]); err != nil {
		fmt.Fprintf(env.Stderr, "configure error: %v\n", err)
		return 1
	}

	out := env.Stdout
	writeConfigNotice(out, resolved)
	fmt.Fprintf(out, "%s = %s\n", rest[0], rest[1])
	writeStatusLine(out, "File", path)
	fmt.Fprintf(out, "\n")

	return 0
}

// runConfigureGet implements `telecli configure get <key>`.
//
// It answers with the value a start of telecli will use, and says where
// that value came from, because a value written in the file and a value
// left at its default look the same in the output otherwise.
func runConfigureGet(args []string, env Environment) int {
	fs := flag.NewFlagSet("configure get", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)

	configPath := fs.String(
		"config",
		"",
		"path to the configuration file",
	)

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}

		return 2
	}

	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintf(
			env.Stderr,
			"configure error: `configure get` needs one key, for "+
				"example: telecli configure get tui.theme\n",
		)
		fmt.Fprintf(
			env.Stderr,
			"The settings are: %s\n",
			strings.Join(config.SettingKeys(), ", "),
		)

		return 2
	}

	cfg, resolved, err := loadConfigForStatus(*configPath)
	if err != nil {
		fmt.Fprintf(env.Stderr, "configure error: %v\n", err)
		return 1
	}

	value, err := config.SettingValue(cfg, rest[0])
	if err != nil {
		fmt.Fprintf(env.Stderr, "configure error: %v\n", err)
		return 1
	}

	out := env.Stdout
	writeConfigNotice(out, resolved)
	fmt.Fprintf(out, "%s = %s\n", rest[0], value)
	writeStatusLine(out, "Source", valueSource(*configPath, resolved))
	fmt.Fprintf(out, "\n")

	return 0
}

// valueSource says where a read value came from.
//
// A configuration file holds only the settings a person changed, so a
// value that comes from a file may still be the default for a setting
// the file does not mention. The line says so rather than pretending
// every value was written down.
func valueSource(
	explicitPath string,
	resolved config.ResolvedPath,
) string {
	if !resolved.Found() {
		createPath, err := config.WriteTarget(explicitPath)
		if err != nil {
			return "the built-in default"
		}

		return "the built-in default; create the file at " + createPath
	}

	return resolved.Path +
		" (the built-in default for what it does not set)"
}

// settingVocabularies are the values a setting can hold whose vocabulary
// belongs to a package above internal/config.
//
// The names of themes, colour modes and width rules live in
// internal/tui/theme and internal/tui/termwidth, which are leaves above
// the configuration, so this package checks them. Everything else is
// checked where it is stored: a path has to be absolute, a whole number
// has to parse, and a send mode has to be one this build sends with.
var settingVocabularies = map[string]func(string) error{
	"tui.theme": func(value string) error {
		_, err := theme.ThemeFor(value)

		return err
	},
	"tui.color": func(value string) error {
		_, err := theme.ParseColorMode(value)

		return err
	},
	"tui.width": func(value string) error {
		_, err := termwidth.ParseMode(value)

		return err
	},
}

// checkSettingValue refuses a value this build cannot use.
func checkSettingValue(key, value string) error {
	check, known := settingVocabularies[key]
	if !known {
		return nil
	}

	return check(value)
}

// configEditTarget is the file `configure set` writes, and the
// resolution behind it.
//
// The file telecli reads is the file that gets the change, so a setting
// never lands somewhere the program does not look: while the old
// settings file is still in use because the move keeps failing, it stays
// the target. With no file at all the change creates one, in the place
// this build keeps settings.
func configEditTarget(
	explicitPath string,
) (string, config.ResolvedPath, error) {
	resolved, err := config.ResolvePath(explicitPath)
	if err != nil {
		// A path that was asked for by name and does not exist is
		// created rather than refused: setting a value is one of the
		// ways a configuration file comes into being.
		if !errors.Is(err, config.ErrConfigNotFound) {
			return "", config.ResolvedPath{}, err
		}

		path, targetErr := config.WriteTarget(explicitPath)
		if targetErr != nil {
			return "", config.ResolvedPath{}, targetErr
		}

		return path, config.ResolvedPath{
			Source: config.PathSourceNone,
		}, nil
	}

	if resolved.Found() {
		return resolved.Path, resolved, nil
	}

	path, err := config.WriteTarget(explicitPath)
	if err != nil {
		return "", config.ResolvedPath{}, err
	}

	return path, resolved, nil
}

// writeConfigNotice prints the one line a run has about the settings
// file: it was moved to the new place, or the move was refused and why.
//
// It is one line and it is never silent, because a file that changed
// place behind a person's back is a file they will look for in the old
// directory and not find.
func writeConfigNotice(w io.Writer, resolved config.ResolvedPath) {
	if resolved.Notice == "" {
		return
	}

	fmt.Fprintf(w, "%s\n", resolved.Notice)
}

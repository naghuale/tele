package application

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"telecli/internal/authstore"
	"telecli/internal/config"
	"telecli/internal/secretinput"
)

// runConfigure implements `telecli configure` and its subcommands.
func runConfigure(args []string, env Environment) int {
	if len(args) > 0 {
		switch args[0] {
		case "status":
			return runConfigureStatus(args[1:], env)
		case "reset":
			return runConfigureReset(args[1:], env)
		case "set":
			return runConfigureSet(args[1:], env)
		case "get":
			return runConfigureGet(args[1:], env)
		}
	}

	fs := flag.NewFlagSet("configure", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)

	var (
		configPath = fs.String(
			"config",
			"",
			"path to the configuration file",
		)
		mode = fs.String(
			"mode",
			"",
			"delivery mode: durable is the only supported value",
		)
		profile = fs.String(
			"profile",
			"",
			"credential profile name",
		)
		force = fs.Bool(
			"force",
			false,
			"replace an existing credential profile without asking",
		)
	)

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}

		return 2
	}

	request := ConfigureRequest{
		Environ: os.Environ(),
		Prompter: &terminalPrompter{
			out:     env.Stdout,
			scanner: newLineScanner(env.Stdin),
		},
		Secrets: secretinput.NewReader(),
		Probe:   newNativeTDLibProbe(),
		Options: ConfigureOptions{
			ConfigPath: *configPath,
			Mode:       *mode,
			Profile:    *profile,
			Force:      *force,
		},
	}

	if env.NewTelegramCredentialStore != nil {
		request.Credentials = configureWriterFrom(
			env.telegramCredentialStore(),
		)
	} else {
		request.Credentials = configureWriterFrom(
			NewTelegramCredentialStore(),
		)
	}

	ctx := context.Background()

	result, err := RunConfigure(ctx, request)
	if err != nil {
		fmt.Fprintf(env.Stderr, "configure error: %v\n", err)
		return 1
	}

	writeConfigureReport(env.Stdout, result)

	return 0
}

// runConfigureStatus implements `telecli configure status`.
//
// It reports states only. Credential values, the API ID, the phone and
// any Keychain detail are never printed.
func runConfigureStatus(args []string, env Environment) int {
	fs := flag.NewFlagSet("configure status", flag.ContinueOnError)
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

	cfg, resolved, err := loadConfigForStatus(*configPath)
	if err != nil {
		fmt.Fprintf(env.Stderr, "configure error: %v\n", err)
		return 1
	}

	out := env.Stdout

	fmt.Fprintf(out, "Configuration\n")
	writeStatusLine(out, "File", statusFile(resolved))
	writeStatusLine(out, "Source", statusSource(resolved))
	if resolved.Notice != "" {
		writeStatusLine(out, "Notice", resolved.Notice)
	}
	if !resolved.Found() {
		createPath, err := config.WriteTarget(*configPath)
		if err != nil {
			fmt.Fprintf(env.Stderr, "configure error: %v\n", err)
			return 1
		}
		writeStatusLine(out, "Create at", createPath)
	}
	writeStatusLine(out, "Delivery mode", string(cfg.MessageDelivery.Mode))
	writeStatusLine(out, "Data directory", "configured")

	fmt.Fprintf(out, "\nTDLib\n")
	if cfg.TDLib.LibraryPath == "" {
		writeStatusLine(out, "Library", "not configured")
	} else {
		writeStatusLine(out, "Library", "configured")
	}

	fmt.Fprintf(out, "\nTelegram\n")
	if cfg.Auth.APIID > 0 {
		writeStatusLine(out, "API ID", "configured")
	} else {
		writeStatusLine(out, "API ID", "not configured")
	}

	if cfg.Auth.CredentialProfile == "" {
		writeStatusLine(out, "Credential profile", "not configured")
	} else {
		writeStatusLine(
			out,
			"Credential profile",
			cfg.Auth.CredentialProfile,
		)
	}

	resolver := NewTelegramCredentialResolver(env.telegramCredentialStore())
	resolvedAuth, resolveErr := resolver.ResolveTelegramCredentials(
		context.Background(),
		cfg.Auth,
	)

	switch resolvedAuth.Availability {
	case AuthAvailabilityReady:
		writeStatusLine(out, "API hash", "configured")
		writeStatusLine(out, "Phone", "configured")
		writeStatusLine(
			out,
			"Credential source",
			resolvedAuth.Source.String(),
		)

	case AuthAvailabilityInvalid:
		writeStatusLine(out, "API hash", "unresolved")
		writeStatusLine(out, "Phone", "unresolved")
		if resolvedAuth.Reason != "" {
			writeStatusLine(out, "Reason", resolvedAuth.Reason)
		}

	default:
		writeStatusLine(out, "API hash", "not configured")
		writeStatusLine(out, "Phone", "not configured")
		writeStatusLine(out, "Credential source", "none")
	}

	fmt.Fprintf(out, "\n")
	writeStatusLine(out, "Ready", readyWord(resolvedAuth, resolveErr))

	return 0
}

// statusLabelWidth keeps the status output aligned without trailing
// whitespace on empty values.
const statusLabelWidth = 18

// statusFile is the file line: where the settings were read from, or
// that there are none and the built-in defaults apply.
func statusFile(resolved config.ResolvedPath) string {
	if !resolved.Found() {
		return "none, the built-in defaults apply"
	}

	return resolved.Path
}

// statusSource is the source line: which rule picked the file, so a
// person looking for it knows whether to look in ~/.config, in the
// variable they exported, or in the argument they passed.
func statusSource(resolved config.ResolvedPath) string {
	switch resolved.Source {
	case config.PathSourceExplicit:
		return "explicit, --config"
	case config.PathSourceEnvironment:
		return "environment, TELECLI_CONFIG"
	case config.PathSourceDefault:
		return "the default file"
	case config.PathSourceLegacy:
		return "the file this build used before 29.09.2026"
	default:
		return "none"
	}
}

func writeStatusLine(w io.Writer, label, value string) {
	if value == "" {
		value = "-"
	}

	fmt.Fprintf(w, "  %-*s %s\n", statusLabelWidth, label, value)
}

func readyWord(
	resolved ResolvedTelegramCredentials,
	err error,
) string {
	if err != nil || !resolved.Ready() {
		return "no"
	}

	return "yes"
}

// resetConfirmation is the word a person has to type before
// `telecli configure reset` removes anything.
//
// It is a whole word and not a y because the command removes the
// settings file and the Telegram login in one step. The owner ran it to
// reset one line, said y after a question he had not read closely, and
// lost the file: every other setting in it went with the one line he
// meant to change. A word cannot be typed by a hand that is already
// moving.
const resetConfirmation = "delete"

// resetDeclined is printed when the confirmation is not the word.
const resetDeclined = "Nothing was removed."

// runConfigureReset implements `telecli configure reset`.
//
// It removes the configuration file and the credential profile, and it
// says which of the two it is about to remove before it removes
// anything. The durable outbox database and its Keychain key are left
// untouched, because deleting the key would make an existing database
// permanently unreadable.
func runConfigureReset(args []string, env Environment) int {
	fs := flag.NewFlagSet("configure reset", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)

	var (
		configPath = fs.String(
			"config",
			"",
			"path to the configuration file",
		)
		assumeYes = fs.Bool(
			"yes",
			false,
			"remove without asking, for an unattended run",
		)
	)

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}

		return 2
	}

	cfg, resolved, err := loadConfigForStatus(*configPath)
	if err != nil {
		fmt.Fprintf(env.Stderr, "configure error: %v\n", err)
		return 1
	}

	out := env.Stdout
	writeResetSummary(out, resolved.Path, cfg.Auth.CredentialProfile)

	if !*assumeYes {
		confirmed, err := askResetConfirmation(out, env.Stdin)
		if err != nil {
			fmt.Fprintf(env.Stderr, "configure error: %v\n", err)
			return 1
		}

		if !confirmed {
			fmt.Fprintf(out, "%s\n", resetDeclined)
			return 0
		}
	}

	if cfg.Auth.CredentialProfile != "" {
		err := env.credentialProfileDeleter()(
			context.Background(),
			cfg.Auth.CredentialProfile,
		)
		if err != nil &&
			!errors.Is(err, authstore.ErrProfileUnavailable) {
			fmt.Fprintf(
				env.Stderr,
				"configure error: remove credential profile: %v\n",
				err,
			)
			return 1
		}
	}

	if resolved.Found() {
		if err := os.Remove(resolved.Path); err != nil &&
			!errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(
				env.Stderr,
				"configure error: remove configuration: %v\n",
				err,
			)
			return 1
		}
	}

	fmt.Fprintf(out, "Configuration removed.\n")
	fmt.Fprintf(
		out,
		"The durable outbox database and its key were left in place.\n",
	)

	return 0
}

// writeResetSummary names what the command is about to remove.
//
// A question that says only "are you sure" is a question about something
// the person has to remember, and the answer they are thinking about is
// usually one line of a file they have open. The paths and the profile
// name are here so the answer can be about this run instead.
func writeResetSummary(
	w io.Writer,
	configPath string,
	profile string,
) {
	fmt.Fprintf(w, "This removes:\n")

	if configPath == "" {
		fmt.Fprintf(
			w,
			"  the settings file    none: there is no configuration file\n",
		)
	} else {
		fmt.Fprintf(w, "  the settings file    %s\n", configPath)
	}

	if profile == "" {
		fmt.Fprintf(
			w,
			"  the Telegram login   none: no credential profile is set\n",
		)
	} else {
		fmt.Fprintf(w, "  the Telegram login   %s\n", profile)
	}

	fmt.Fprintf(
		w,
		"  the message queue and its key are left in place.\n\n",
	)
}

// askResetConfirmation prints the question and reports whether the word
// was typed.
//
// A line that is not the word, and an input that ends before an answer,
// are both a refusal: nothing is removed and the summary stays where the
// person can read it and run the command again.
func askResetConfirmation(out io.Writer, in io.Reader) (bool, error) {
	if _, err := io.WriteString(
		out,
		`Type "`+resetConfirmation+`" to remove them: `,
	); err != nil {
		return false, err
	}

	scanner := newLineScanner(in)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return false, err
		}

		return false, nil
	}

	answer := strings.TrimSpace(scanner.Text())

	return strings.EqualFold(answer, resetConfirmation), nil
}

// loadConfigForStatus loads the configuration for a read-only command.
//
// A missing file is not an error here: status must be able to report an
// unconfigured machine.
func loadConfigForStatus(
	explicitPath string,
) (config.Config, config.ResolvedPath, error) {
	resolved, err := config.ResolvePath(explicitPath)
	if err != nil {
		// status is a diagnostic and must work on an unconfigured
		// machine, so a missing file is reported as unconfigured
		// rather than as a failure. A present but broken file is
		// still an error.
		if errors.Is(err, config.ErrConfigNotFound) {
			return config.Default(), config.ResolvedPath{
				Source: config.PathSourceNone,
			}, nil
		}

		return config.Config{}, config.ResolvedPath{}, err
	}

	if !resolved.Found() {
		return config.Default(), config.ResolvedPath{
			Source: config.PathSourceNone,
		}, nil
	}

	cfg, err := config.Load(resolved.Path)
	if err != nil {
		return config.Config{}, resolved, err
	}

	return cfg, resolved, nil
}

// configureWriterFrom returns the credential writer used by the command.
//
// The command line path always uses the platform store: setup is the
// moment a profile is created, so it cannot depend on a store that the
// application itself would refuse to build.
func configureWriterFrom(
	TelegramCredentialStore,
) ConfigureCredentialWriter {
	return &platformConfigureWriter{}
}

// platformConfigureWriter writes profiles through the platform store.
type platformConfigureWriter struct{}

func (*platformConfigureWriter) Create(
	ctx context.Context,
	profile string,
	credentials authstore.Profile,
) error {
	return authstore.NewPlatformStore().Create(ctx, profile, credentials)
}

func (*platformConfigureWriter) Replace(
	ctx context.Context,
	profile string,
	credentials authstore.Profile,
) error {
	return authstore.NewPlatformStore().Replace(ctx, profile, credentials)
}

func (*platformConfigureWriter) Load(
	ctx context.Context,
	profile string,
) (authstore.Profile, error) {
	return authstore.NewPlatformStore().Load(ctx, profile)
}

func (*platformConfigureWriter) Delete(
	ctx context.Context,
	profile string,
) error {
	return authstore.NewPlatformStore().Delete(ctx, profile)
}

// terminalPrompter reads answers from a terminal or a pipe.
type terminalPrompter struct {
	out     io.Writer
	scanner *bufio.Scanner
}

func newLineScanner(in io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	return scanner
}

// Ask reads one visible answer.
func (p *terminalPrompter) Ask(prompt string) (string, error) {
	if _, err := io.WriteString(p.out, prompt); err != nil {
		return "", err
	}

	if !p.scanner.Scan() {
		if err := p.scanner.Err(); err != nil {
			return "", err
		}

		return "", errors.New("configure: input ended")
	}

	return p.scanner.Text(), nil
}

// AskSecret reads one hidden answer.
func (p *terminalPrompter) AskSecret(prompt string) ([]byte, error) {
	return secretinput.NewReader().ReadSecret(prompt)
}

// Confirm asks a yes/no question and treats anything but y/yes as no.
func (p *terminalPrompter) Confirm(prompt string) (bool, error) {
	answer, err := p.Ask(prompt)
	if err != nil {
		return false, err
	}

	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

// Compile-time assertion: terminalPrompter satisfies the flow contract.
var _ ConfigurePrompter = (*terminalPrompter)(nil)

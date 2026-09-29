package application

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"telecli/internal/authstore"
	"telecli/internal/buildinfo"
	"telecli/internal/config"
	"telecli/internal/outbox"
	"telecli/internal/telegram"
	"telecli/internal/telemetry/recorder"
	"telecli/internal/tui"
	"telecli/internal/tui/termwidth"
	"telecli/internal/tui/theme"
)

// TDLibReporter reports TDLib runtime status for the doctor command.
type TDLibReporter func(
	ctx context.Context,
	cfg config.Config,
	stdout io.Writer,
) int

// RuntimeFactory builds a concrete *telegram.Runtime from
// configuration.
//
// It returns *telegram.Runtime, not the narrow TelegramLifecycle
// interface: production authorization requires the concrete type, and
// returning the interface would force an unsafe type assertion inside
// runTUI. App still stores the runtime through the narrow
// TelegramLifecycle interface, so lifecycle callers are unaffected.
type RuntimeFactory func(
	cfg config.Config,
	rec recorder.ComponentRecorder,
) (*telegram.Runtime, error)

// AuthRunResult is the outcome of an authorization step.
type AuthRunResult struct {
	Source    tui.ChatSource
	Submitter tui.ComposerSubmitter

	// AccountKey is the stable account identifier used for delivery status
	// reads.
	AccountKey string

	// MessageStatuses is nil in direct delivery mode and non-nil when the
	// runtime can report durable delivery status metadata.
	MessageStatuses tui.MessageStatusSource

	// PendingMessages is nil in direct delivery mode and non-nil when the
	// runtime has a queue to read. It is the only path by which the text of
	// an outgoing message reaches the interface.
	PendingMessages tui.PendingMessageSource

	// StatusSummaries reads the connection, the queue counters and the
	// presence of the open chat, which is what the status line shows. It
	// is nil in direct delivery mode, which has no queue to count, and then
	// the interface draws no status line.
	StatusSummaries tui.StatusSummarySource

	// MessageCanceller cancels a queued message by the version the screen
	// read. It is nil when the program has no queue to cancel in.
	MessageCanceller tui.MessageCanceller

	// PresenceOpener tells Telegram which chat the user is looking at.
	//
	// TDLib counts the online members of a chat only while it is open, so
	// without it a group header says nothing about its members.
	PresenceOpener tui.ChatPresenceOpener

	// SendingPaused is non-nil when the durable outbox could not be
	// opened. The TUI still starts: only sending is paused, and the
	// submitter refuses rather than sending by another route.
	SendingPaused *SendingPausedError

	Close func(context.Context) error
}

// Environment is the injection point for tests.
type Environment struct {
	Stdout io.Writer
	Stderr io.Writer

	// Stdin supplies answers for interactive setup. A nil value uses
	// os.Stdin, which is what the real CLI does; tests inject a buffer.
	Stdin io.Reader

	RunTUI              func(tui.ChatSource) error
	RunTUIWithSubmitter func(context.Context, tui.Dependencies) error

	// OutboxHealthRecorder is the optional durable outbox telemetry backend.
	// A nil value keeps sampling structurally enabled with a no-op backend.
	OutboxHealthRecorder recorder.OutboxHealthRecorder

	ReportTDLib        TDLibReporter
	NewTelegramRuntime RuntimeFactory

	// NewTelegramCredentialStore builds the platform credential store.
	// A nil value uses the production store. Tests inject a fake so no
	// real Keychain item is read or written.
	NewTelegramCredentialStore func() TelegramCredentialStore

	// DeleteCredentialProfile removes one Telegram credential profile.
	//
	// A nil value uses the platform store. Tests inject a fake so that
	// `telecli configure reset` can be exercised without deleting a
	// real Keychain item on the machine that runs the suite.
	DeleteCredentialProfile func(context.Context, string) error

	// ProbeOutbox is the message-queue probe used by telecli doctor.
	//
	// A nil value uses the production probe, which opens the real
	// platform key provider and can therefore block on a user prompt.
	// Tests inject a fake: the suite must never touch a real Keychain,
	// and on a machine without one the production probe hangs until the
	// test binary is killed.
	ProbeOutbox OutboxProbe

	// NewOutboxKeyProvider builds the key provider used by
	// telecli outbox reset.
	//
	// A nil value uses the platform provider. Tests inject a fake so no
	// real Keychain item is read, created or left behind.
	NewOutboxKeyProvider func() outbox.KeyProvider

	// OutboxResetOps overrides individual steps of
	// telecli outbox reset. A nil field keeps the production step.
	//
	// Tests use it to fail one step, which is the only way to prove
	// that a repeated run after a crash finishes the same reset.
	OutboxResetOps OutboxResetOperations
}

// telegramCredentialStore returns the credential store for this run.
func (e Environment) telegramCredentialStore() TelegramCredentialStore {
	if e.NewTelegramCredentialStore != nil {
		return e.NewTelegramCredentialStore()
	}

	return NewTelegramCredentialStore()
}

// credentialProfileDeleter returns the function that removes one
// credential profile for this run.
func (e Environment) credentialProfileDeleter() func(
	context.Context,
	string,
) error {
	if e.DeleteCredentialProfile != nil {
		return e.DeleteCredentialProfile
	}

	store := authstore.NewPlatformStore()

	return store.Delete
}

var errMissingTUIRunner = errors.New("TUI runner is not configured")

// TelegramLifecycle is the narrow lifecycle contract used by App.
type TelegramLifecycle interface {
	Start(context.Context) error
	Close(context.Context) error
}

// App holds the composition root state.
type App struct {
	cfg                 config.Config
	recorder            recorder.ComponentRecorder
	telegram            TelegramLifecycle
	runAuth             func(ctx context.Context) (AuthRunResult, error)
	runTUI              func(tui.ChatSource) error
	runTUIWithSubmitter func(context.Context, tui.Dependencies) error

	// diagnostics receives the reasons that must not reach the screen,
	// such as why the message queue could not be opened. It is a field
	// rather than os.Stderr so tests can read it.
	diagnostics io.Writer

	// theme and colorProfile are the interface theme resolved by the
	// composition root and the profile it was built for.
	theme        theme.Theme
	colorProfile theme.Profile

	// widthMode is the rule the interface counts the width of text in.
	// The zero value measures the terminal before the first frame.
	widthMode termwidth.Mode
}

// WithInterface returns a copy of the app that draws with the given theme
// and colour profile.
//
// The composition root resolves both, because what the terminal can show
// is not a view's business and every command that starts an interface has
// to resolve it the same way. An app built without this draws with the
// default theme and no colour, which is legible everywhere.
func (a *App) WithInterface(
	resolved theme.Theme,
	profile theme.Profile,
) *App {
	if a == nil {
		return nil
	}

	copied := *a
	copied.theme = resolved
	copied.colorProfile = profile

	return &copied
}

// WithWidthMode returns a copy of the app that counts the width of text in
// the given rule.
//
// The rule is resolved by the composition root for the same reason the
// theme is: it is a property of the machine, the program measures the
// terminal when the rule says auto, and telecli doctor reports the rule
// the TUI will be drawn with. An app built without it measures the
// terminal, which is what the zero value of the setting says.
func (a *App) WithWidthMode(mode termwidth.Mode) *App {
	if a == nil {
		return nil
	}

	copied := *a
	copied.widthMode = mode

	return &copied
}

// WithDiagnostics returns a copy of the app that writes operational
// reasons to w.
//
// Only reasons that are unsafe or unhelpful on screen go here: the TUI
// shows user-facing text, and this stream carries the cause.
func (a *App) WithDiagnostics(w io.Writer) *App {
	if a == nil {
		return nil
	}
	copied := *a
	if w == nil {
		w = os.Stderr
	}
	copied.diagnostics = w
	return &copied
}

// interfaceTheme returns the theme to draw with, or the default one.
//
// An app built without WithInterface still has to draw something, and the
// default theme is what a user sees until they configure another.
func (a *App) interfaceTheme() theme.Theme {
	if a == nil || a.theme.Name == "" {
		return theme.DefaultTheme()
	}

	return a.theme
}

// diagnosticsWriter returns the configured stream or os.Stderr.
func (a *App) diagnosticsWriter() io.Writer {
	if a == nil || a.diagnostics == nil {
		return os.Stderr
	}
	return a.diagnostics
}

// New is the PR-02-compatible constructor without Telegram lifecycle.
func New(
	cfg config.Config,
	rec recorder.ComponentRecorder,
	runTUI func(tui.ChatSource) error,
) *App {
	return &App{cfg: cfg, recorder: rec, runTUI: runTUI}
}

// NewWithTelegram wires the Telegram lifecycle.
func NewWithTelegram(
	cfg config.Config,
	rec recorder.ComponentRecorder,
	telegramLifecycle TelegramLifecycle,
	runTUI func(tui.ChatSource) error,
) *App {
	return &App{
		cfg:      cfg,
		recorder: rec,
		telegram: telegramLifecycle,
		runTUI:   runTUI,
	}
}

// NewWithAuth wires the Telegram lifecycle and an authorization step.
func NewWithAuth(
	cfg config.Config,
	rec recorder.ComponentRecorder,
	telegramLifecycle TelegramLifecycle,
	runAuth func(ctx context.Context) (AuthRunResult, error),
	runTUI func(tui.ChatSource) error,
) *App {
	return &App{
		cfg:      cfg,
		recorder: rec,
		telegram: telegramLifecycle,
		runAuth:  runAuth,
		runTUI:   runTUI,
	}
}

func NewWithAuthAndSubmitter(
	cfg config.Config,
	rec recorder.ComponentRecorder,
	telegramLifecycle TelegramLifecycle,
	runAuth func(ctx context.Context) (AuthRunResult, error),
	runTUI func(tui.ChatSource) error,
	runTUIWithSubmitter func(context.Context, tui.Dependencies) error,
) *App {
	return &App{
		cfg:                 cfg,
		recorder:            rec,
		telegram:            telegramLifecycle,
		runAuth:             runAuth,
		runTUI:              runTUI,
		runTUIWithSubmitter: runTUIWithSubmitter,
	}
}

// RunTUI runs the application lifecycle.
func (a *App) RunTUI(ctx context.Context) error {
	if a.runTUI == nil {
		return errMissingTUIRunner
	}

	if a.telegram != nil {
		if err := a.telegram.Start(ctx); err != nil {
			return fmt.Errorf("start Telegram runtime: %w", err)
		}
	}

	var authResult AuthRunResult
	var authErr error
	if a.runAuth != nil {
		authResult, authErr = a.runAuth(ctx)
	}

	var runErr error
	if authErr == nil {
		if authResult.Submitter != nil {
			if a.runTUIWithSubmitter == nil {
				runErr = errMissingTUIRunner
			} else {
				// The paused state travels to the TUI as the send-error
				// text, so the reason is on screen without the raw cause.
				var sendError error
				if authResult.SendingPaused != nil {
					sendError = authResult.SendingPaused
					// The cause goes to the diagnostic stream only. It
					// can name files and keychain services, and it must
					// never carry message text.
					fmt.Fprintf(
						a.diagnosticsWriter(),
						"message queue unavailable (%s): %v\n",
						authResult.SendingPaused.Reason(),
						authResult.SendingPaused.Cause(),
					)
				}
				runErr = a.runTUIWithSubmitter(
					ctx,
					tui.Dependencies{
						Source:           authResult.Source,
						MessageSubmitter: authResult.Submitter,
						AccountKey:       authResult.AccountKey,
						MessageStatuses:  authResult.MessageStatuses,
						PendingMessages:  authResult.PendingMessages,
						StatusSummaries:  authResult.StatusSummaries,
						PresenceOpener:   authResult.PresenceOpener,
						MessageCanceller: authResult.MessageCanceller,
						SendError:        sendError,
						// The causes the screen must not show go here:
						// why a message could not be queued and why the
						// chat list could not be read.
						Diagnostics:  a.diagnosticsWriter(),
						Theme:        a.interfaceTheme(),
						ColorProfile: a.colorProfile,
						WidthMode:    a.widthMode,
					},
				)
			}
		} else {
			runErr = a.runTUI(authResult.Source)
		}
	}

	shutdownTimeout := time.Duration(a.cfg.TDLib.ShutdownTimeoutMS) * time.Millisecond
	if shutdownTimeout <= 0 {
		shutdownTimeout = 5 * time.Second
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	var closeErr error
	switch {
	case authResult.Close != nil:
		closeErr = authResult.Close(shutdownCtx)
	case a.telegram != nil:
		closeErr = a.telegram.Close(shutdownCtx)
	}

	return errors.Join(authErr, runErr, closeErr)
}

// Main is the CLI entry point.
func Main(args []string, env Environment) int {
	if env.Stdout == nil {
		env.Stdout = os.Stdout
	}
	if env.Stdin == nil {
		env.Stdin = os.Stdin
	}
	if env.Stderr == nil {
		env.Stderr = os.Stderr
	}
	if env.RunTUI == nil {
		env.RunTUI = func(tui.ChatSource) error { return errMissingTUIRunner }
	}
	if env.RunTUIWithSubmitter == nil {
		env.RunTUIWithSubmitter = func(
			_ context.Context,
			deps tui.Dependencies,
		) error {
			return env.RunTUI(deps.Source)
		}
	}
	if env.ReportTDLib == nil {
		env.ReportTDLib = reportTDLib
	}
	if env.NewTelegramRuntime == nil {
		env.NewTelegramRuntime = newTelegramLifecycle
	}

	if len(args) < 2 {
		printHelp(env.Stdout)
		return 0
	}

	switch args[1] {
	case "--help", "-h", "help":
		printHelp(env.Stdout)
		return 0
	case "version":
		fmt.Fprintln(env.Stdout, buildinfo.Get().String())
		return 0
	case "doctor":
		return runDoctor(args[2:], env)
	case "configure":
		return runConfigure(args[2:], env)
	case "outbox":
		return runOutbox(args[2:], env)
	case "tui":
		return runTUI(args[2:], env)
	default:
		fmt.Fprintf(env.Stderr, "unknown command: %s\n\n", args[1])
		printHelp(env.Stderr)
		return 2
	}
}

func printHelp(w io.Writer) {
	fmt.Fprint(w, `telecli - terminal Telegram client

Usage:
  telecli --help
  telecli version
  telecli configure [--config path] [--mode durable]
  telecli configure status
  telecli configure get <key>
  telecli configure set <key> <value>
  telecli configure reset [--config path] [--yes]
  telecli outbox reset [--config path] [--yes]
  telecli doctor [--config path] [--no-color]
  telecli tui    [--config path] [--no-color]

The settings live in ~/.config/telecli/config.toml, or in
`+"`$XDG_CONFIG_HOME/telecli/config.toml`"+` when that variable is set,
and are read from --config, then TELECLI_CONFIG, then there, then the
built-in defaults.

Change one setting with

  telecli configure set tui.theme gruvbox

It edits that one line and leaves the rest of the file as you wrote it.
`)
}

// loadCommandConfig resolves and loads the configuration for a
// subcommand.
//
// Precedence is --config, then TELECLI_CONFIG, then the per-user config
// file, then the built-in defaults. An explicit or environment path that
// does not exist is an error rather than a silent fallback.
//
// The resolution is returned with the configuration, because the place
// the file was read from is part of what a run has to report: a file
// that is still in the old place is a file the person has to move, and
// the one line about it is the whole report.
func loadCommandConfig(
	explicitPath string,
) (config.Config, config.ResolvedPath, error) {
	resolved, err := config.ResolvePath(explicitPath)
	if err != nil {
		return config.Config{}, config.ResolvedPath{}, err
	}

	cfg, err := config.LoadResolved(resolved)
	if err != nil {
		return config.Config{}, resolved, err
	}

	return cfg, resolved, nil
}

// writeAuthStatus prints the authentication readiness of the current
// configuration.
//
// It reports the state and the source name only. Credential values, the
// profile contents and variable values are never printed; variable names
// are not needed here because the state already says what is wrong.
func writeAuthStatus(
	w io.Writer,
	resolved ResolvedTelegramCredentials,
	resolveErr error,
) {
	switch resolved.Availability {
	case AuthAvailabilityReady:
		fmt.Fprintf(w, "Auth configuration: ready\n")
		fmt.Fprintf(w, "Auth source: %s\n", resolved.Source)

	case AuthAvailabilityAbsent:
		fmt.Fprintf(w, "Auth configuration: absent\n")
		fmt.Fprintf(w, "Mode: mock-only\n")

	case AuthAvailabilityInvalid:
		fmt.Fprintf(w, "Auth configuration: invalid\n")
		if resolved.Reason != "" {
			fmt.Fprintf(w, "Reason: %s\n", resolved.Reason)
		} else if resolveErr != nil {
			fmt.Fprintf(w, "Reason: %s\n", resolveErr)
		}

	default:
		fmt.Fprintf(w, "Auth configuration: unknown\n")
	}
}

func runDoctor(args []string, env Environment) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	cfgPath := fs.String("config", "", "path to config file")
	noColor := fs.Bool("no-color", false, "report without colour")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}

		return 2
	}

	cfg, settings, err := loadCommandConfig(*cfgPath)
	if err != nil {
		fmt.Fprintf(env.Stderr, "config error: %v\n", err)
		return 1
	}

	// The interface settings are resolved here and not only in runTUI, so
	// a mistyped theme is reported by the command a user runs when
	// something looks wrong, instead of at the next start of the TUI.
	interfaceTheme, profile, err := resolveInterfaceTheme(cfg, *noColor)
	if err != nil {
		fmt.Fprintf(env.Stderr, "config error: %v\n", err)
		return 1
	}

	widthMode, err := resolveInterfaceWidth(cfg)
	if err != nil {
		fmt.Fprintf(env.Stderr, "config error: %v\n", err)
		return 1
	}

	fmt.Fprintln(env.Stdout, "telecli doctor")
	fmt.Fprintln(env.Stdout, buildinfo.Get().String())
	fmt.Fprintf(env.Stdout, "Go: %s %s/%s\n",
		runtime.Version(), runtime.GOOS, runtime.GOARCH)
	fmt.Fprintf(env.Stdout, "Config: OK (data_dir=%s, log_level=%s)\n",
		cfg.DataDir, cfg.LogLevel)
	writeConfigWarnings(env.Stdout, doctorWarnings(cfg, settings))
	writeInterfaceStatus(env.Stdout, interfaceTheme, profile)
	writeWidthStatus(env.Stdout, widthMode)
	reportOutboxStatus(
		env.Stdout,
		context.Background(),
		cfg,
		env.ProbeOutbox,
	)

	resolver := NewTelegramCredentialResolver(env.telegramCredentialStore())
	resolved, resolveErr := resolver.ResolveTelegramCredentials(
		context.Background(),
		cfg.Auth,
	)
	writeAuthStatus(env.Stdout, resolved, resolveErr)

	fmt.Fprintln(env.Stdout, "TDLib interface: modern JSON C API")

	return env.ReportTDLib(context.Background(), cfg, env.Stdout)
}

// runTUI implements the CLI "tui" command.
//
// Credentials policy:
//
//   - no TELECLI_TDLIB_* variables set: mock-only TUI, the native
//     runtime is never created;
//   - partial set: configuration error, exit 1;
//   - complete set: production authorization; runtime and
//     authorization errors also exit 1 without falling back to mock.
func runTUI(args []string, env Environment) int {
	fs := flag.NewFlagSet("tui", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	cfgPath := fs.String("config", "", "path to config file")
	noColor := fs.Bool(
		"no-color",
		false,
		"draw the interface without colour, whatever the terminal can show",
	)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}

		return 2
	}

	cfg, settings, err := loadCommandConfig(*cfgPath)
	if err != nil {
		fmt.Fprintf(env.Stderr, "config error: %v\n", err)
		return 1
	}

	// The line about the settings file goes to the diagnostics rather
	// than to the terminal: it is a note about the machine, and the
	// interface owns the screen from here.
	writeConfigNotice(env.Stderr, settings)

	interfaceTheme, colorProfile, err := resolveInterfaceTheme(cfg, *noColor)
	if err != nil {
		fmt.Fprintf(env.Stderr, "config error: %v\n", err)
		return 1
	}

	widthMode, err := resolveInterfaceWidth(cfg)
	if err != nil {
		fmt.Fprintf(env.Stderr, "config error: %v\n", err)
		return 1
	}

	rec := recorder.NewNoop()

	// Tri-state credential gate. Only a complete absence of
	// authentication configuration may use the mock-only UI; a partial
	// or broken configuration fails closed.
	resolver := NewTelegramCredentialResolver(env.telegramCredentialStore())
	resolved, err := resolver.ResolveTelegramCredentials(
		context.Background(),
		cfg.Auth,
	)
	if err != nil {
		fmt.Fprintf(env.Stderr, "credentials error: %v\n", err)
		return 1
	}

	switch resolved.Availability {
	case AuthAvailabilityAbsent:
		fmt.Fprintln(env.Stderr,
			"warning: Telegram credentials are not set; "+
				"starting mock-only TUI")

		app := New(cfg, rec, env.RunTUI)
		if err := app.RunTUI(context.Background()); err != nil {
			fmt.Fprintf(env.Stderr, "app error: %v\n", err)
			return 1
		}
		return 0

	case AuthAvailabilityReady:
		// Continue below with a complete credential set.

	case AuthAvailabilityInvalid:
		fmt.Fprintf(env.Stderr,
			"credentials error: %s\n",
			resolved.Reason,
		)
		return 1

	default:
		fmt.Fprintln(env.Stderr,
			"credentials error: unknown availability state")
		return 1
	}

	params, err := TdlibParametersFromEnv(cfg, resolved.Credentials)
	if err != nil {
		fmt.Fprintf(env.Stderr, "credentials error: %v\n", err)
		return 1
	}

	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)

	// A shutdown signal cancels ctx instead of killing the process, so
	// the session and the outbox still close gracefully below.
	stopSignals := notifyShutdownSignals(ctx, cancel)
	defer stopSignals()

	rt, err := env.NewTelegramRuntime(cfg, rec)
	if err != nil {
		fmt.Fprintf(env.Stderr, "Telegram runtime error: %v\n", err)
		return 1
	}

	openDelivery := func(
		openCtx context.Context,
		deliveryCfg MessageDeliveryRuntimeConfig,
		deliveryDeps MessageDeliveryRuntimeDeps,
	) (MessageDeliveryRuntime, error) {
		return OpenMessageDeliveryRuntime(
			openCtx,
			deliveryCfg,
			deliveryDeps,
		)
	}

	runAuth := func(authCtx context.Context) (AuthRunResult, error) {
		session, err := telegram.Authorize(
			authCtx,
			rt,
			params,
			TUIAuthProvider{Phone: resolved.Credentials.Phone},
		)
		if err != nil {
			return AuthRunResult{}, fmt.Errorf(
				"authorize Telegram session: %w",
				err,
			)
		}

		healthRecorder := env.OutboxHealthRecorder
		if healthRecorder == nil {
			healthRecorder = recorder.NewNoopOutboxHealth()
		}
		tuiHealthRecorder, err := NewMessageDeliveryHealthRecorder(
			healthRecorder,
		)
		if err != nil {
			return AuthRunResult{}, fmt.Errorf(
				"create message delivery health recorder: %w",
				err,
			)
		}

		// Which user this client is, so that a chat with oneself is not
		// drawn with a presence. A refusal is a line in the log and not a
		// reason to refuse to start: the interface shows no presence for
		// Saved Messages instead of showing the user's own.
		ownUserID, err := resolveOwnUserID(authCtx, session)
		if err != nil {
			fmt.Fprintf(
				env.Stderr,
				"telegram own user unavailable: %v\n",
				err,
			)
		}

		return prepareDeliveryAuthResult(
			authCtx,
			cfg,
			session,
			cancel,
			openDelivery,
			deliveryHealthSampling{Recorder: tuiHealthRecorder},
			ownUserID,
		)
	}

	app := NewWithAuthAndSubmitter(
		cfg,
		rec,
		rt,
		runAuth,
		env.RunTUI,
		env.RunTUIWithSubmitter,
	).WithInterface(interfaceTheme, colorProfile).WithWidthMode(widthMode)
	appErr := app.RunTUI(ctx)
	cause := context.Cause(ctx)

	var signalErr *shutdownSignalError
	if errors.As(cause, &signalErr) {
		// Cancellation errors are the expected result of the signal;
		// only a failed graceful close is worth reporting.
		if closeErr := withoutCancellation(appErr); closeErr != nil {
			fmt.Fprintf(env.Stderr, "app error: %v\n", closeErr)
		}
		fmt.Fprintf(env.Stderr, "telecli: %v\n", signalErr)
		return signalErr.exitCode()
	}
	if cause != nil && !errors.Is(cause, context.Canceled) {
		appErr = errors.Join(appErr, cause)
	}
	if appErr == nil {
		return 0
	}
	if cause == nil && errors.Is(appErr, context.Canceled) {
		return 0
	}
	fmt.Fprintf(env.Stderr, "app error: %v\n", appErr)
	return 1
}

// withoutCancellation drops context cancellation from a joined error, so
// only real failures remain.
func withoutCancellation(err error) error {
	if err == nil {
		return nil
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		var kept []error
		for _, part := range joined.Unwrap() {
			if part = withoutCancellation(part); part != nil {
				kept = append(kept, part)
			}
		}
		return errors.Join(kept...)
	}
	if errors.Is(err, context.Canceled) ||
		errors.Is(err, tea.ErrProgramKilled) ||
		errors.Is(err, errShutdownSignal) {
		return nil
	}
	return err
}

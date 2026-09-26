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

	"telecli/internal/buildinfo"
	"telecli/internal/config"
	"telecli/internal/telegram"
	"telecli/internal/telemetry/recorder"
	"telecli/internal/tui"
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
}

// telegramCredentialStore returns the credential store for this run.
func (e Environment) telegramCredentialStore() TelegramCredentialStore {
	if e.NewTelegramCredentialStore != nil {
		return e.NewTelegramCredentialStore()
	}

	return NewTelegramCredentialStore()
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
				runErr = a.runTUIWithSubmitter(
					ctx,
					tui.Dependencies{
						Source:           authResult.Source,
						MessageSubmitter: authResult.Submitter,
						AccountKey:       authResult.AccountKey,
						MessageStatuses:  authResult.MessageStatuses,
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
  telecli configure [--config path] [--mode direct|durable]
  telecli configure status
  telecli configure reset
  telecli doctor [--config path]
  telecli tui    [--config path]

Configuration is read from --config, then TELECLI_CONFIG, then
`+"`$XDG_CONFIG_HOME/telecli/config.toml`"+` (or the platform equivalent),
then built-in defaults.
`)
}

// loadCommandConfig resolves and loads the configuration for a
// subcommand.
//
// Precedence is --config, then TELECLI_CONFIG, then the per-user config
// file, then the built-in defaults. An explicit or environment path that
// does not exist is an error rather than a silent fallback.
func loadCommandConfig(explicitPath string) (config.Config, error) {
	resolved, err := config.ResolvePath(explicitPath)
	if err != nil {
		return config.Config{}, err
	}

	return config.LoadResolved(resolved)
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
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg, err := loadCommandConfig(*cfgPath)
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
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg, err := loadCommandConfig(*cfgPath)
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
			TUIAuthProvider{},
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

		return prepareDeliveryAuthResult(
			authCtx,
			cfg,
			session,
			cancel,
			openDelivery,
			deliveryHealthSampling{Recorder: tuiHealthRecorder},
		)
	}

	app := NewWithAuthAndSubmitter(
		cfg,
		rec,
		rt,
		runAuth,
		env.RunTUI,
		env.RunTUIWithSubmitter,
	)
	appErr := app.RunTUI(ctx)
	cause := context.Cause(ctx)
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

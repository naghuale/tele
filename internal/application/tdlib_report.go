package application

import (
	"context"
	"fmt"
	"io"
	"time"

	"telecli/internal/config"
	"telecli/internal/telegram"
	"telecli/internal/telemetry/recorder"
)

// reportTDLib inspects the runtime and prints diagnostics.
func reportTDLib(
	ctx context.Context,
	cfg config.Config,
	stdout io.Writer,
) int {
	loaded, err := telegram.LoadNativeWithSource(cfg.TDLib.LibraryPath)
	if err != nil {
		fmt.Fprintln(stdout, "TDLib runtime: unavailable")
		fmt.Fprintf(stdout, "Reason: %v\n", err)
		return 1
	}

	rt, err := newRuntimeFromNative(
		cfg,
		loaded.Native,
		recorder.NewNoop(),
	)
	if err != nil {
		_ = loaded.Native.Close()
		fmt.Fprintln(stdout, "TDLib runtime: unavailable")
		fmt.Fprintf(stdout, "Reason: %v\n", err)
		return 1
	}

	defer func() {
		closeContext, cancel := context.WithTimeout(
			context.Background(),
			time.Duration(cfg.TDLib.ShutdownTimeoutMS)*time.Millisecond,
		)
		defer cancel()

		_ = rt.Close(closeContext)
	}()

	info, err := rt.Inspect(ctx, telegram.RuntimeCompatibility())
	if err != nil {
		fmt.Fprintln(stdout, "TDLib runtime: unavailable")
		fmt.Fprintf(stdout, "Reason: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, "TDLib runtime: available")

	// The source tells a packaged runtime apart from a development
	// checkout. Only the source name is printed: the library path is a
	// local absolute path and must never reach the output or a log.
	fmt.Fprintf(stdout, "TDLib source: %s\n", loaded.Source)

	fmt.Fprintf(stdout, "TDLib version: %s\n", info.Version)
	fmt.Fprintf(stdout, "TDLib commit: %s\n", info.Commit)
	fmt.Fprintf(stdout, "TDLib compatibility: %s\n", info.Mode)

	if info.Reason != "" {
		fmt.Fprintf(stdout, "Reason: %s\n", info.Reason)
	}

	if info.Mode == telegram.CompatibilityRejected {
		return 1
	}
	return 0
}

// newRuntimeFromNative builds a runtime around an already opened native
// library.
func newRuntimeFromNative(
	cfg config.Config,
	native telegram.Native,
	rec recorder.ComponentRecorder,
) (*telegram.Runtime, error) {
	rt, err := telegram.NewRuntime(telegramRuntimeConfig(cfg), native, rec)
	if err != nil {
		_ = native.Close()
		return nil, err
	}

	return rt, nil
}

// telegramRuntimeConfig is the binding's configuration for this program.
//
// It is one function because the journal of the library is named here and
// read from the same place by `telecli doctor`, and because the verbosity
// of that journal is a setting of the user, not a constant of the binding:
// a request must reach TDLib with the journal already named, or the
// library prints its first lines to the terminal the interface owns.
func telegramRuntimeConfig(cfg config.Config) telegram.Config {
	runtimeConfig := telegram.DefaultConfig()
	runtimeConfig.ReceiveTimeout =
		time.Duration(cfg.TDLib.ReceiveTimeoutMS) * time.Millisecond
	runtimeConfig.ShutdownTimeout =
		time.Duration(cfg.TDLib.ShutdownTimeoutMS) * time.Millisecond
	runtimeConfig.LogFilePath = TDLibLogPath(cfg)
	runtimeConfig.LogVerbosity = cfg.TDLib.LogVerbosity

	return runtimeConfig
}

// newTelegramLifecycle constructs the concrete runtime used by both
// doctor and the production TUI.
//
// It returns *telegram.Runtime, not the TelegramLifecycle interface,
// so callers that need concrete methods (Inspect, and later the
// runtime passed to telegram.Authorize) do not rely on type
// assertions.
//
// The signature matches RuntimeFactory, so the source of the library is
// deliberately not threaded through here: it is a diagnostic, and only
// doctor needs it.
func newTelegramLifecycle(
	cfg config.Config,
	rec recorder.ComponentRecorder,
) (*telegram.Runtime, error) {
	native, err := telegram.LoadNative(cfg.TDLib.LibraryPath)
	if err != nil {
		return nil, fmt.Errorf("load native TDLib: %w", err)
	}

	rt, err := newRuntimeFromNative(cfg, native, rec)
	if err != nil {
		return nil, err
	}

	return rt, nil
}

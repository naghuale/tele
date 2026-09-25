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
	rt, err := newTelegramLifecycle(cfg, recorder.NewNoop())
	if err != nil {
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

// newTelegramLifecycle constructs the concrete runtime used by both
// doctor and the production TUI.
//
// It returns *telegram.Runtime, not the TelegramLifecycle interface,
// so callers that need concrete methods (Inspect, and later the
// runtime passed to telegram.Authorize) do not rely on type
// assertions.
func newTelegramLifecycle(
	cfg config.Config,
	rec recorder.ComponentRecorder,
) (*telegram.Runtime, error) {
	native, err := telegram.LoadNative(cfg.TDLib.LibraryPath)
	if err != nil {
		return nil, fmt.Errorf("load native TDLib: %w", err)
	}

	runtimeConfig := telegram.DefaultConfig()
	runtimeConfig.ReceiveTimeout =
		time.Duration(cfg.TDLib.ReceiveTimeoutMS) * time.Millisecond
	runtimeConfig.ShutdownTimeout =
		time.Duration(cfg.TDLib.ShutdownTimeoutMS) * time.Millisecond

	rt, err := telegram.NewRuntime(runtimeConfig, native, rec)
	if err != nil {
		_ = native.Close()
		return nil, err
	}

	return rt, nil
}

package application

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"telecli/internal/config"
	"telecli/internal/telegram"
	"telecli/internal/telemetry/recorder"
)

var (
	// ErrConfigureInput reports a rejected setup answer.
	ErrConfigureInput = errors.New("configure: invalid input")
	// ErrConfigureTDLib reports that no usable TDLib runtime was found.
	ErrConfigureTDLib = errors.New("configure: TDLib runtime")
	// ErrConfigureRollback reports that setup could not restore the
	// previous state and needs manual attention.
	ErrConfigureRollback = errors.New(
		"configure: rollback required",
	)
)

// TDLibProbe inspects a candidate TDLib library.
//
// It is an interface so the setup flow can be tested without loading a
// real dylib.
type TDLibProbe interface {
	Probe(ctx context.Context, path string) (TDLibProbeResult, error)
}

// TDLibProbeResult describes a probed TDLib runtime.
type TDLibProbeResult struct {
	Path          string
	Version       string
	Commit        string
	Compatibility telegram.CompatibilityMode
}

// TDLibLibraryCandidates returns the paths configure probes, in order.
//
// The order reuses the production discovery order and adds the Homebrew
// locations, which the loader does not search implicitly. A discovered
// library is written to tdlib.library_path, so the runtime still loads it
// through the explicit configuration path.
func TDLibLibraryCandidates(
	configuredPath string,
	environ []string,
) []string {
	var candidates []string

	fromEnv := envValue(environ, "TELECLI_TDLIB_LIBRARY")
	if fromEnv != "" {
		candidates = append(candidates, fromEnv)
	}

	if configuredPath != "" {
		candidates = append(candidates, configuredPath)
	}

	candidates = append(
		candidates,
		thirdPartyTDLibPath,
		"/opt/homebrew/lib/libtdjson.dylib",
		"/usr/local/lib/libtdjson.dylib",
		"/opt/homebrew/lib/libtdjson.so",
		"/usr/local/lib/libtdjson.so",
	)

	return uniqueNonEmpty(candidates)
}

const thirdPartyTDLibPath = "third_party/tdlib/lib/libtdjson.dylib"

// nativeTDLibProbe loads a real TDLib and inspects it.
type nativeTDLibProbe struct{}

// newNativeTDLibProbe returns the production probe.
func newNativeTDLibProbe() TDLibProbe {
	return nativeTDLibProbe{}
}

// Probe loads the library at path and reports its compatibility.
func (nativeTDLibProbe) Probe(
	ctx context.Context,
	path string,
) (TDLibProbeResult, error) {
	if _, err := os.Stat(path); err != nil {
		return TDLibProbeResult{}, fmt.Errorf(
			"%w: %s is not readable",
			ErrConfigureTDLib,
			path,
		)
	}

	// A scratch directory keeps the probe from touching the configured
	// database or files directories.
	scratch, err := os.MkdirTemp("", "telecli-tdlib-probe-")
	if err != nil {
		return TDLibProbeResult{}, fmt.Errorf(
			"%w: create probe directory",
			ErrConfigureTDLib,
		)
	}
	defer func() {
		_ = os.RemoveAll(scratch)
	}()

	probeCfg := config.Default()
	probeCfg.TDLib.DatabaseDir = scratch + "/database"
	probeCfg.TDLib.FilesDir = scratch + "/files"
	probeCfg.TDLib.LibraryPath = path

	previous, had := os.LookupEnv("TELECLI_TDLIB_LIBRARY")
	if err := os.Setenv("TELECLI_TDLIB_LIBRARY", path); err != nil {
		return TDLibProbeResult{}, fmt.Errorf(
			"%w: set library path",
			ErrConfigureTDLib,
		)
	}
	defer func() {
		if had {
			_ = os.Setenv("TELECLI_TDLIB_LIBRARY", previous)
			return
		}
		_ = os.Unsetenv("TELECLI_TDLIB_LIBRARY")
	}()

	rt, err := newTelegramLifecycle(probeCfg, recorder.NewNoop())
	if err != nil {
		return TDLibProbeResult{}, fmt.Errorf(
			"%w: %s: %s",
			ErrConfigureTDLib,
			path,
			safeProbeReason(err),
		)
	}
	defer func() {
		closeContext, cancel := context.WithTimeout(
			context.Background(),
			time.Duration(probeCfg.TDLib.ShutdownTimeoutMS)*time.Millisecond,
		)
		defer cancel()

		_ = rt.Close(closeContext)
	}()

	// The runtime must be started before it can answer anything.
	if err := rt.Start(ctx); err != nil {
		return TDLibProbeResult{}, fmt.Errorf(
			"%w: %s: %s",
			ErrConfigureTDLib,
			path,
			safeProbeReason(err),
		)
	}

	// TDLib defaults to verbosity 5, so the probe lowers it before it
	// asks anything. Setup has no credentials to leak, but leaving a
	// chatty library running for the length of a probe is noise the
	// operator did not ask for.
	if err := rt.ConfigureSafeLogging(); err != nil {
		return TDLibProbeResult{}, fmt.Errorf(
			"%w: %s: %s",
			ErrConfigureTDLib,
			path,
			safeProbeReason(err),
		)
	}

	info, err := rt.Inspect(ctx, telegram.RuntimeCompatibility())
	if err != nil {
		return TDLibProbeResult{}, fmt.Errorf(
			"%w: %s: %s",
			ErrConfigureTDLib,
			path,
			safeProbeReason(err),
		)
	}

	return TDLibProbeResult{
		Path:          path,
		Version:       info.Version,
		Commit:        info.Commit,
		Compatibility: info.Mode,
	}, nil
}

// safeProbeReason keeps a probe error free of anything sensitive while
// still naming the underlying stage.
func safeProbeReason(err error) string {
	if err == nil {
		return ""
	}

	message := err.Error()
	if index := strings.Index(message, ": "); index >= 0 {
		message = message[:index]
	}

	return message
}

// validateProbe enforces the TDLib requirements for a release artifact.
func validateProbe(result TDLibProbeResult) error {
	if result.Version == "" {
		return fmt.Errorf(
			"%w: library reports no version",
			ErrConfigureTDLib,
		)
	}

	expected := telegram.RuntimeCompatibility()

	if expected.Commit != "" && result.Commit != expected.Commit {
		return fmt.Errorf(
			"%w: commit mismatch: got %s, want %s",
			ErrConfigureTDLib,
			result.Commit,
			expected.Commit,
		)
	}

	if result.Compatibility != telegram.CompatibilityVerified {
		return fmt.Errorf(
			"%w: compatibility %s",
			ErrConfigureTDLib,
			result.Compatibility,
		)
	}

	return nil
}

// parseAPIID validates a Telegram API ID answer.
func parseAPIID(answer string) (int, error) {
	trimmed := strings.TrimSpace(answer)
	if trimmed == "" {
		return 0, fmt.Errorf(
			"%w: API ID must not be empty",
			ErrConfigureInput,
		)
	}

	apiID, err := strconv.Atoi(trimmed)
	if err != nil {
		return 0, fmt.Errorf(
			"%w: API ID must be a number",
			ErrConfigureInput,
		)
	}

	if apiID <= 0 {
		return 0, fmt.Errorf(
			"%w: API ID must be positive",
			ErrConfigureInput,
		)
	}

	return apiID, nil
}

// parseNonEmpty validates that an answer carries a value.
func parseNonEmpty(answer, field string) (string, error) {
	trimmed := strings.TrimSpace(answer)
	if trimmed == "" {
		return "", fmt.Errorf(
			"%w: %s must not be empty",
			ErrConfigureInput,
			field,
		)
	}

	return trimmed, nil
}

func envValue(environ []string, name string) string {
	prefix := name + "="
	for _, entry := range environ {
		if strings.HasPrefix(entry, prefix) {
			return strings.TrimPrefix(entry, prefix)
		}
	}

	return ""
}

func uniqueNonEmpty(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))

	for _, value := range values {
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}

	return result
}

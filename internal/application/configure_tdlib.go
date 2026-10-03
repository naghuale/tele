package application

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	Source        telegram.NativeLibrarySource
	Persist       bool
	Version       string
	Commit        string
	Compatibility telegram.CompatibilityMode
}

// TDLibLibraryCandidate is a location configure may probe.
type TDLibLibraryCandidate struct {
	Path   string
	Source telegram.NativeLibrarySource
	// Persist reports whether a successful probe of this candidate may
	// be written to tdlib.library_path.
	//
	// A packaged library must never be persisted: the path is absolute
	// and tied to one directory, so recording it would break the package
	// as soon as it is moved, and an explicit configured path would then
	// outrank the packaged discovery.
	Persist bool
}

// TDLibLibraryCandidates returns the locations configure probes, in order.
//
// Explicit override semantics are preserved: an operator named path stays
// the only candidate, so a packaged fallback can never quietly replace
// what was asked for. The packaged location itself comes from the
// production loader, so the wizard and the runtime cannot drift apart.
//
// The order is not identical to the runtime order, and does not claim to
// be: configure additionally probes Homebrew and /usr/local, which the
// loader does not search implicitly, and it can ask for a manual path.
func TDLibLibraryCandidates(
	configuredPath string,
	environ []string,
) []TDLibLibraryCandidate {
	var candidates []TDLibLibraryCandidate

	fromEnv := envValue(environ, "TELECLI_TDLIB_LIBRARY")
	if fromEnv != "" {
		// An explicit override is the only candidate, exactly as in the
		// runtime loader. Continuing to the packaged library here would
		// silently replace what the operator asked for.
		return []TDLibLibraryCandidate{
			{
				Path:   fromEnv,
				Source: telegram.NativeLibrarySourceEnvironment,
			},
		}
	}

	if configuredPath != "" {
		candidates = append(candidates, TDLibLibraryCandidate{
			Path:    configuredPath,
			Source:  telegram.NativeLibrarySourceConfigured,
			Persist: true,
		})

		// An operator named path is the only candidate, so a failure is
		// fatal for the wizard exactly as it is for the loader.
		return uniqueTDLibCandidates(candidates)
	}

	if packaged, err := telegram.PackagedNativeLibraryCandidate(); err == nil {
		if packaged.Path != "" {
			candidates = append(candidates, TDLibLibraryCandidate{
				Path:   packaged.Path,
				Source: packaged.Source,
			})
		}
	}

	// The checkout is only searched in a telecli_dev build, and is
	// remembered as an absolute path: a relative one would be resolved
	// from whatever directory telecli later starts in.
	if telegram.DevelopmentLibrarySearchEnabled() {
		if path, err := filepath.Abs(
			telegram.DevelopmentLibraryPath(),
		); err == nil {
			candidates = append(candidates, TDLibLibraryCandidate{
				Path:    path,
				Source:  telegram.NativeLibrarySourceDevelopment,
				Persist: true,
			})
		}
	}

	candidates = append(
		candidates,
		// The Homebrew and /usr/local locations hold a system-wide
		// installation at a stable absolute path, so they are reported
		// as the platform source and may be remembered. That is the
		// opposite of a packaged library, whose path belongs to one
		// directory and must never be written down.
		TDLibLibraryCandidate{
			Path:    "/opt/homebrew/lib/libtdjson.dylib",
			Source:  telegram.NativeLibrarySourcePlatform,
			Persist: true,
		},
		TDLibLibraryCandidate{
			Path:    "/usr/local/lib/libtdjson.dylib",
			Source:  telegram.NativeLibrarySourcePlatform,
			Persist: true,
		},
		TDLibLibraryCandidate{
			Path:    "/opt/homebrew/lib/libtdjson.so",
			Source:  telegram.NativeLibrarySourcePlatform,
			Persist: true,
		},
		TDLibLibraryCandidate{
			Path:    "/usr/local/lib/libtdjson.so",
			Source:  telegram.NativeLibrarySourcePlatform,
			Persist: true,
		},
	)

	return uniqueTDLibCandidates(candidates)
}

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
	// database or files directories, or the configured journal: the probe
	// starts a real runtime, and a runtime writes the library's journal
	// where it was told to.
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
	probeCfg.DataDir = scratch
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

// tdlibCandidatesAreExplicit reports whether the operator named the
// library.
//
// An explicit choice is the only candidate, exactly as in the runtime
// loader, so a failure must not be softened by offering another path.
func tdlibCandidatesAreExplicit(
	candidates []TDLibLibraryCandidate,
) bool {
	if len(candidates) == 0 {
		return false
	}

	switch candidates[0].Source {
	case telegram.NativeLibrarySourceEnvironment,
		telegram.NativeLibrarySourceConfigured:
		return true
	default:
		return false
	}
}

func uniqueTDLibCandidates(
	values []TDLibLibraryCandidate,
) []TDLibLibraryCandidate {
	seen := make(map[string]struct{}, len(values))
	result := make([]TDLibLibraryCandidate, 0, len(values))

	for _, value := range values {
		if value.Path == "" {
			continue
		}

		if _, exists := seen[value.Path]; exists {
			continue
		}

		seen[value.Path] = struct{}{}
		result = append(result, value)
	}

	return result
}

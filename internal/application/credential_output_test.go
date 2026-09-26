package application

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"telecli/internal/config"
	"telecli/internal/telegram"
	"telecli/internal/telemetry/recorder"
	"telecli/internal/tui"
)

// outputProbeNative is a telegram.Native whose Execute always fails, so
// production startup stops at the safe-logging step.
type outputProbeNative struct {
	executeCalls atomic.Int64
	sendCalls    atomic.Int64
}

func (n *outputProbeNative) CreateClientID() (int, error) { return 1, nil }
func (n *outputProbeNative) Send(int, []byte) error {
	n.sendCalls.Add(1)
	return nil
}
func (n *outputProbeNative) Receive(time.Duration) ([]byte, error) {
	return nil, nil
}
func (n *outputProbeNative) Execute([]byte) ([]byte, error) {
	n.executeCalls.Add(1)
	return nil, errors.New("native execute failed")
}
func (n *outputProbeNative) Close() error { return nil }

func newProbeRuntime(
	t *testing.T,
	native telegram.Native,
) *telegram.Runtime {
	t.Helper()
	cfg := telegram.DefaultConfig()
	cfg.ReceiveTimeout = time.Millisecond
	runtime, err := telegram.NewRuntime(
		cfg,
		native,
		recorder.NewNoop(),
	)
	if err != nil {
		t.Fatal(err)
	}
	// The runtime is intentionally not started here: the application
	// owns the lifecycle and must start it itself.
	t.Cleanup(func() {
		_ = runtime.Close(context.Background())
	})
	return runtime
}

// TestProductionAuthorizationDoesNotLogCredentials captures everything the
// application writes to stdout and stderr during a production startup that
// has credentials in the environment, and requires that no credential value
// appears in the captured output.
//
// The secrets are intentionally never included in failure messages.
func TestProductionAuthorizationDoesNotLogCredentials(t *testing.T) {
	const secretHash = "h7-test-secret-api-hash"
	const secretPhone = "+15550001234"

	t.Setenv("TELECLI_TDLIB_API_ID", "123456")
	t.Setenv("TELECLI_TDLIB_API_HASH", secretHash)
	t.Setenv("TELECLI_TDLIB_PHONE", secretPhone)

	native := &outputProbeNative{}
	runtime := newProbeRuntime(t, native)

	var stdout, stderr bytes.Buffer

	tuiCalls := 0
	env := Environment{
		Stdout: &stdout,
		Stderr: &stderr,
		RunTUI: func(tui.ChatSource) error {
			tuiCalls++
			return nil
		},
		ReportTDLib: func(
			_ context.Context,
			_ config.Config,
			_ io.Writer,
		) int {
			return 1
		},
		NewTelegramRuntime: func(
			_ config.Config,
			_ recorder.ComponentRecorder,
		) (*telegram.Runtime, error) {
			return runtime, nil
		},
	}

	code := Main([]string{"telecli", "tui"}, env)

	captured := stdout.String() + stderr.String()

	if code == 0 {
		t.Fatal("startup must fail when the log configuration fails")
	}
	if tuiCalls != 0 {
		t.Fatalf("TUI was started %d times after a failed startup", tuiCalls)
	}
	if native.sendCalls.Load() != 0 {
		t.Fatalf(
			"Send() calls = %d, want 0",
			native.sendCalls.Load(),
		)
	}
	if native.executeCalls.Load() != 1 {
		t.Fatalf(
			"Execute() calls = %d, want 1",
			native.executeCalls.Load(),
		)
	}

	for _, secret := range []string{secretHash, secretPhone} {
		if strings.Contains(captured, secret) {
			t.Fatal("captured output contains a credential value")
		}
	}
}

// ---- config discovery wiring ----

// writeDiscoveredConfig writes a config file and points TELECLI_CONFIG at
// it, exercising the documented discovery order for doctor and tui.
func writeDiscoveredConfig(t *testing.T, logLevel string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "discovered.toml")
	body := "log_level = \"" + logLevel + "\"\n" +
		"[auth]\napi_id = 424242\ncredential_profile = \"discovered\"\n"

	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("TELECLI_CONFIG", path)

	return path
}

func TestDoctorUsesDiscoveredConfig(t *testing.T) {
	clearCredentials(t)
	writeDiscoveredConfig(t, "debug")

	var stdout, stderr bytes.Buffer
	tuiCalls := 0
	env := newEnv(&stdout, &stderr, &tuiCalls)
	env.ReportTDLib = func(
		_ context.Context,
		cfg config.Config,
		w io.Writer,
	) int {
		if cfg.LogLevel != "debug" {
			t.Errorf("doctor saw log_level = %q, want debug", cfg.LogLevel)
		}
		if cfg.Auth.APIID != 424242 {
			t.Errorf("doctor saw api_id = %d, want 424242", cfg.Auth.APIID)
		}
		if cfg.Auth.CredentialProfile != "discovered" {
			t.Errorf(
				"doctor saw profile = %q, want discovered",
				cfg.Auth.CredentialProfile,
			)
		}
		fmt.Fprintln(w, "TDLib runtime: available")
		return 0
	}

	if code := Main([]string{"telecli", "doctor"}, env); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "log_level=debug") {
		t.Fatalf("doctor output = %q", stdout.String())
	}
}

// TestTUIUsesDiscoveredConfig proves the tui subcommand really reads the
// discovered configuration.
//
// A discovered config with an invalid value must abort startup. If tui
// ignored discovery it would silently fall back to the built-in defaults
// and exit 0, so the exit code discriminates the two behaviours.
func TestTUIUsesDiscoveredConfig(t *testing.T) {
	clearCredentials(t)

	path := filepath.Join(t.TempDir(), "discovered.toml")
	if err := os.WriteFile(path, []byte("log_level = \"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TELECLI_CONFIG", path)

	var stdout, stderr bytes.Buffer
	tuiCalls := 0
	env := newEnv(&stdout, &stderr, &tuiCalls)

	if code := Main([]string{"telecli", "tui"}, env); code != 1 {
		t.Fatalf(
			"code = %d, want 1: tui must reject the discovered config",
			code,
		)
	}
	if !strings.Contains(stderr.String(), "config error") {
		t.Fatalf("stderr = %q, want a config error", stderr.String())
	}
	if tuiCalls != 0 {
		t.Fatalf("RunTUI was called %d times", tuiCalls)
	}
}

// TestTUIUsesValidDiscoveredConfig is the companion: a valid discovered
// config must be accepted, so the previous test cannot pass because of an
// unrelated failure.
func TestTUIUsesValidDiscoveredConfig(t *testing.T) {
	clearCredentials(t)
	path := filepath.Join(t.TempDir(), "discovered.toml")
	body := "log_level = \"info\"\n[auth]\napi_id = 99\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TELECLI_CONFIG", path)

	var stdout, stderr bytes.Buffer
	tuiCalls := 0
	env := newEnv(&stdout, &stderr, &tuiCalls)

	if code := Main([]string{"telecli", "tui"}, env); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, stderr.String())
	}
	if tuiCalls != 1 {
		t.Fatalf("RunTUI was called %d times, want 1", tuiCalls)
	}
}

func TestExplicitConfigOverridesEnvironment(t *testing.T) {
	clearCredentials(t)
	writeDiscoveredConfig(t, "debug")

	explicit := filepath.Join(t.TempDir(), "explicit.toml")
	body := "log_level = \"error\"\n[auth]\napi_id = 7\n"
	if err := os.WriteFile(explicit, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	tuiCalls := 0
	env := newEnv(&stdout, &stderr, &tuiCalls)
	env.ReportTDLib = func(
		_ context.Context,
		cfg config.Config,
		w io.Writer,
	) int {
		if cfg.LogLevel != "error" {
			t.Errorf("log_level = %q, want error", cfg.LogLevel)
		}
		if cfg.Auth.APIID != 7 {
			t.Errorf("api_id = %d, want 7", cfg.Auth.APIID)
		}
		fmt.Fprintln(w, "TDLib runtime: available")
		return 0
	}

	args := []string{"telecli", "doctor", "--config", explicit}
	if code := Main(args, env); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, stderr.String())
	}
}

func TestEnvironmentConfigOverridesDefault(t *testing.T) {
	clearCredentials(t)
	writeDiscoveredConfig(t, "error")

	var stdout, stderr bytes.Buffer
	tuiCalls := 0
	env := newEnv(&stdout, &stderr, &tuiCalls)
	env.ReportTDLib = func(
		_ context.Context,
		cfg config.Config,
		w io.Writer,
	) int {
		if cfg.LogLevel != "error" {
			t.Errorf("log_level = %q, want error", cfg.LogLevel)
		}
		fmt.Fprintln(w, "TDLib runtime: available")
		return 0
	}

	if code := Main([]string{"telecli", "doctor"}, env); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, stderr.String())
	}
}

func TestMissingExplicitConfigIsAnError(t *testing.T) {
	clearCredentials(t)
	t.Setenv("TELECLI_CONFIG", "")

	missing := filepath.Join(t.TempDir(), "absent.toml")

	var stdout, stderr bytes.Buffer
	tuiCalls := 0
	env := newEnv(&stdout, &stderr, &tuiCalls)

	args := []string{"telecli", "doctor", "--config", missing}
	if code := Main(args, env); code != 1 {
		t.Fatalf("code = %d, want 1 for a missing explicit config", code)
	}
	if !strings.Contains(stderr.String(), "config error") {
		t.Fatalf("stderr = %q, want a config error", stderr.String())
	}
}

func TestMissingEnvironmentConfigIsAnError(t *testing.T) {
	clearCredentials(t)
	t.Setenv("TELECLI_CONFIG", filepath.Join(t.TempDir(), "absent.toml"))

	var stdout, stderr bytes.Buffer
	tuiCalls := 0
	env := newEnv(&stdout, &stderr, &tuiCalls)

	if code := Main([]string{"telecli", "doctor"}, env); code != 1 {
		t.Fatalf("code = %d, want 1 for a missing TELECLI_CONFIG file", code)
	}
}

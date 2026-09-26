package application

import (
	"bytes"
	"context"
	"errors"
	"io"
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

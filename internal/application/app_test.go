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
	"testing"

	"telecli/internal/config"
	"telecli/internal/outbox"
	"telecli/internal/telegram"
	"telecli/internal/telemetry/recorder"
	"telecli/internal/tui"
)

// newEnv builds an Environment that never touches real TDLib and never
// starts a real TUI. Tests that use it must clear TELECLI_TDLIB_* env
// variables (t.Setenv) so the credentials branch is not taken.
func newEnv(stdout, stderr *bytes.Buffer, tuiCalls *int) Environment {
	return Environment{
		Stdout: stdout,
		Stderr: stderr,
		RunTUI: func(tui.ChatSource) error {
			*tuiCalls++
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
			return nil, errors.New("TDLib unavailable in tests")
		},
		// The production probe opens the real platform key provider,
		// which blocks on a runner with no Keychain until the test
		// binary is killed. Tests must never touch it.
		ProbeOutbox: func(
			context.Context,
			outbox.Config,
			outbox.Deps,
		) (io.Closer, error) {
			return h17NoopCloser{}, nil
		},
	}
}

// clearCredentials ensures the credentials branch is not taken by
// default in tests.
func clearCredentials(t *testing.T) {
	t.Helper()
	t.Setenv("TELECLI_TDLIB_API_ID", "")
	t.Setenv("TELECLI_TDLIB_API_HASH", "")
	t.Setenv("TELECLI_TDLIB_PHONE", "")
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ---- CLI tests ----

func TestNoArgsPrintsHelpExit0(t *testing.T) {
	clearCredentials(t)
	var out, errb bytes.Buffer
	calls := 0
	code := Main([]string{"telecli"}, newEnv(&out, &errb, &calls))
	if code != 0 {
		t.Fatalf("code=%d", code)
	}
	if !strings.Contains(out.String(), "Usage:") {
		t.Fatalf("help not printed: %q", out.String())
	}
}

func TestHelpExit0(t *testing.T) {
	clearCredentials(t)
	var out, errb bytes.Buffer
	calls := 0
	if code := Main([]string{"telecli", "--help"}, newEnv(&out, &errb, &calls)); code != 0 {
		t.Fatalf("code=%d", code)
	}
}

func TestVersionExit0(t *testing.T) {
	clearCredentials(t)
	var out, errb bytes.Buffer
	calls := 0
	if code := Main([]string{"telecli", "version"}, newEnv(&out, &errb, &calls)); code != 0 {
		t.Fatalf("code=%d", code)
	}
	if !strings.Contains(out.String(), "telecli ") {
		t.Fatalf("no version line: %q", out.String())
	}
}

func TestUnknownCommandExit2(t *testing.T) {
	clearCredentials(t)
	var out, errb bytes.Buffer
	calls := 0
	if code := Main([]string{"telecli", "nope"}, newEnv(&out, &errb, &calls)); code != 2 {
		t.Fatalf("code=%d", code)
	}
	if !strings.Contains(errb.String(), "unknown command") {
		t.Fatalf("stderr=%q", errb.String())
	}
}

func TestDoctorInvalidConfigExit1(t *testing.T) {
	clearCredentials(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "c.toml")
	if err := os.WriteFile(p, []byte("log_levle=\"x\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	calls := 0
	code := Main([]string{"telecli", "doctor", "--config", p}, newEnv(&out, &errb, &calls))
	if code != 1 {
		t.Fatalf("code=%d stderr=%s", code, errb.String())
	}
}

func TestTUIInvalidConfigDoesNotRun(t *testing.T) {
	clearCredentials(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "c.toml")
	if err := os.WriteFile(p, []byte("log_level=\"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	calls := 0
	code := Main([]string{"telecli", "tui", "--config", p}, newEnv(&out, &errb, &calls))
	if code != 1 {
		t.Fatalf("code=%d", code)
	}
	if calls != 0 {
		t.Fatalf("RunTUI was called %d times", calls)
	}
}

func TestTUIValidConfigRunsOnce(t *testing.T) {
	clearCredentials(t)
	var out, errb bytes.Buffer
	calls := 0
	code := Main([]string{"telecli", "tui"}, newEnv(&out, &errb, &calls))
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, errb.String())
	}
	if calls != 1 {
		t.Fatalf("RunTUI calls=%d", calls)
	}
}

func TestTUIMissingRunnerExit1(t *testing.T) {
	clearCredentials(t)
	var out, errb bytes.Buffer
	code := Main(
		[]string{"telecli", "tui"},
		Environment{
			Stdout: &out,
			Stderr: &errb,
		},
	)
	if code != 1 {
		t.Fatalf("code=%d, want 1; stderr=%q", code, errb.String())
	}
	if !strings.Contains(errb.String(), "not configured") {
		t.Fatalf("stderr=%q", errb.String())
	}
}

// ---- Credentials policy ----

func TestTUINoCredentialsRunsMockWithoutCreatingRuntime(t *testing.T) {
	clearCredentials(t)

	var stdout, stderr bytes.Buffer

	runtimeCalls := 0
	tuiCalls := 0

	env := Environment{
		Stdout: &stdout,
		Stderr: &stderr,

		RunTUI: func(source tui.ChatSource) error {
			tuiCalls++
			if source != nil {
				t.Fatalf("source = %T, want nil", source)
			}
			return nil
		},

		NewTelegramRuntime: func(
			config.Config,
			recorder.ComponentRecorder,
		) (*telegram.Runtime, error) {
			runtimeCalls++
			return nil, errors.New("must not be called")
		},
	}

	code := Main([]string{"telecli", "tui"}, env)

	if code != 0 {
		t.Fatalf("code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if runtimeCalls != 0 {
		t.Fatalf("runtime calls = %d, want 0", runtimeCalls)
	}
	if tuiCalls != 1 {
		t.Fatalf("TUI calls = %d, want 1", tuiCalls)
	}
	if !strings.Contains(stderr.String(), "credentials are not set") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestTUIPartialCredentialsFailWithoutStartingTUI(t *testing.T) {
	t.Setenv("TELECLI_TDLIB_API_ID", "12345")
	t.Setenv("TELECLI_TDLIB_API_HASH", "")
	t.Setenv("TELECLI_TDLIB_PHONE", "")

	var stdout, stderr bytes.Buffer

	runtimeCalls := 0
	tuiCalls := 0

	env := Environment{
		Stdout: &stdout,
		Stderr: &stderr,

		RunTUI: func(tui.ChatSource) error {
			tuiCalls++
			return nil
		},

		NewTelegramRuntime: func(
			config.Config,
			recorder.ComponentRecorder,
		) (*telegram.Runtime, error) {
			runtimeCalls++
			return nil, errors.New("must not be called")
		},
	}

	code := Main([]string{"telecli", "tui"}, env)

	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if runtimeCalls != 0 {
		t.Fatalf("runtime calls = %d, want 0", runtimeCalls)
	}
	if tuiCalls != 0 {
		t.Fatalf("TUI calls = %d, want 0", tuiCalls)
	}
	if !strings.Contains(stderr.String(), "credentials error") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestTUIRuntimeFailureWithCredentialsDoesNotRunMock(t *testing.T) {
	t.Setenv("TELECLI_TDLIB_API_ID", "12345")
	t.Setenv("TELECLI_TDLIB_API_HASH", "test-hash")
	t.Setenv("TELECLI_TDLIB_PHONE", "+15551234567")

	var stdout, stderr bytes.Buffer

	tuiCalls := 0
	runtimeErr := errors.New("native runtime unavailable")

	env := Environment{
		Stdout: &stdout,
		Stderr: &stderr,

		RunTUI: func(tui.ChatSource) error {
			tuiCalls++
			return nil
		},

		NewTelegramRuntime: func(
			config.Config,
			recorder.ComponentRecorder,
		) (*telegram.Runtime, error) {
			return nil, runtimeErr
		},
	}

	code := Main([]string{"telecli", "tui"}, env)

	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if tuiCalls != 0 {
		t.Fatalf("TUI calls = %d, want 0", tuiCalls)
	}
	if !strings.Contains(stderr.String(), "runtime error") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

// ---- Doctor seam tests ----

func TestDoctorWithoutTDLibExit1(t *testing.T) {
	clearCredentials(t)
	var stdout, stderr bytes.Buffer

	env := Environment{
		Stdout: &stdout,
		Stderr: &stderr,
		RunTUI: func(tui.ChatSource) error { return nil },
		ReportTDLib: func(
			_ context.Context,
			_ config.Config,
			output io.Writer,
		) int {
			fmt.Fprintln(output, "TDLib runtime: unavailable")
			return 1
		},
		ProbeOutbox: func(
			context.Context,
			outbox.Config,
			outbox.Deps,
		) (io.Closer, error) {
			return h17NoopCloser{}, nil
		},
	}

	code := Main([]string{"telecli", "doctor"}, env)
	if code != 1 {
		t.Fatalf("code = %d, want 1; stdout=%q stderr=%q",
			code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "TDLib runtime: unavailable") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestDoctorReportsVerifiedTDLib(t *testing.T) {
	clearCredentials(t)
	var stdout, stderr bytes.Buffer

	env := Environment{
		Stdout: &stdout,
		Stderr: &stderr,
		RunTUI: func(tui.ChatSource) error { return nil },
		ReportTDLib: func(
			_ context.Context,
			_ config.Config,
			output io.Writer,
		) int {
			fmt.Fprintln(output, "TDLib runtime: available")
			fmt.Fprintln(output, "TDLib version: 1.8.0")
			fmt.Fprintln(output, "TDLib compatibility: verified")
			return 0
		},
		ProbeOutbox: func(
			context.Context,
			outbox.Config,
			outbox.Deps,
		) (io.Closer, error) {
			return h17NoopCloser{}, nil
		},
	}

	code := Main([]string{"telecli", "doctor"}, env)
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "TDLib compatibility: verified") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

// ---- Lifecycle tests ----

type fakeLifecycle struct {
	startCalls int
	closeCalls int
	startErr   error
	closeErr   error
	order      *[]string
}

func (f *fakeLifecycle) Start(ctx context.Context) error {
	f.startCalls++
	if f.order != nil {
		*f.order = append(*f.order, "start")
	}
	return f.startErr
}

func (f *fakeLifecycle) Close(ctx context.Context) error {
	f.closeCalls++
	if f.order != nil {
		*f.order = append(*f.order, "close")
	}
	return f.closeErr
}

func TestRunTUIStartsTelegramBeforeUI(t *testing.T) {
	var order []string
	tg := &fakeLifecycle{order: &order}
	app := NewWithTelegram(
		config.Default(),
		recorder.NewNoop(),
		tg,
		func(tui.ChatSource) error {
			order = append(order, "tui")
			return nil
		},
	)

	if err := app.RunTUI(context.Background()); err != nil {
		t.Fatalf("RunTUI: %v", err)
	}
	want := []string{"start", "tui", "close"}
	if !equalStringSlices(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
}

func TestRunTUIDoesNotOpenUIWhenStartFails(t *testing.T) {
	tg := &fakeLifecycle{startErr: errors.New("boom")}
	app := NewWithTelegram(
		config.Default(),
		recorder.NewNoop(),
		tg,
		func(tui.ChatSource) error {
			t.Fatal("RunTUI must not be called when Start fails")
			return nil
		},
	)

	if err := app.RunTUI(context.Background()); err == nil {
		t.Fatal("expected error")
	}
	if tg.startCalls != 1 {
		t.Fatalf("startCalls = %d, want 1", tg.startCalls)
	}
	if tg.closeCalls != 0 {
		t.Fatalf("closeCalls = %d, want 0", tg.closeCalls)
	}
}

func TestRunTUIReturnsUIError(t *testing.T) {
	tg := &fakeLifecycle{}
	uiErr := errors.New("ui failed")
	app := NewWithTelegram(
		config.Default(),
		recorder.NewNoop(),
		tg,
		func(tui.ChatSource) error { return uiErr },
	)

	err := app.RunTUI(context.Background())
	if !errors.Is(err, uiErr) {
		t.Fatalf("err = %v, want %v", err, uiErr)
	}
	if tg.closeCalls != 1 {
		t.Fatalf("closeCalls = %d, want 1", tg.closeCalls)
	}
}

func TestRunTUIReturnsCloseError(t *testing.T) {
	closeErr := errors.New("close failed")
	tg := &fakeLifecycle{closeErr: closeErr}
	app := NewWithTelegram(
		config.Default(),
		recorder.NewNoop(),
		tg,
		func(tui.ChatSource) error { return nil },
	)

	err := app.RunTUI(context.Background())
	if !errors.Is(err, closeErr) {
		t.Fatalf("err = %v, want %v", err, closeErr)
	}
}

func TestRunTUIJoinsUIAndCloseErrors(t *testing.T) {
	uiErr := errors.New("ui")
	closeErr := errors.New("close")
	tg := &fakeLifecycle{closeErr: closeErr}
	app := NewWithTelegram(
		config.Default(),
		recorder.NewNoop(),
		tg,
		func(tui.ChatSource) error { return uiErr },
	)

	err := app.RunTUI(context.Background())
	if !errors.Is(err, uiErr) {
		t.Fatalf("err missing UI error: %v", err)
	}
	if !errors.Is(err, closeErr) {
		t.Fatalf("err missing close error: %v", err)
	}
}

// ---- Auth lifecycle tests ----

func TestRunTUICallsAuthBetweenStartAndTUI(t *testing.T) {
	var order []string
	tg := &fakeLifecycle{order: &order}

	app := NewWithAuth(
		config.Default(),
		recorder.NewNoop(),
		tg,
		func(ctx context.Context) (AuthRunResult, error) {
			order = append(order, "auth")
			return AuthRunResult{}, nil
		},
		func(tui.ChatSource) error {
			order = append(order, "tui")
			return nil
		},
	)

	if err := app.RunTUI(context.Background()); err != nil {
		t.Fatalf("RunTUI: %v", err)
	}
	want := []string{"start", "auth", "tui", "close"}
	if !equalStringSlices(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
}

func TestRunTUIPassesSourceFromAuthToTUI(t *testing.T) {
	tg := &fakeLifecycle{}

	type stubSource struct{ tui.ChatSource }
	sentinel := &stubSource{}

	var received tui.ChatSource
	app := NewWithAuth(
		config.Default(),
		recorder.NewNoop(),
		tg,
		func(ctx context.Context) (AuthRunResult, error) {
			return AuthRunResult{Source: sentinel}, nil
		},
		func(src tui.ChatSource) error {
			received = src
			return nil
		},
	)

	if err := app.RunTUI(context.Background()); err != nil {
		t.Fatalf("RunTUI: %v", err)
	}
	if received != sentinel {
		t.Fatalf("received source = %p, want %p", received, sentinel)
	}
}

func TestRunTUIAuthFailureSkipsTUI(t *testing.T) {
	authErr := errors.New("auth failed")
	tg := &fakeLifecycle{}
	tuiCalls := 0

	app := NewWithAuth(
		config.Default(),
		recorder.NewNoop(),
		tg,
		func(ctx context.Context) (AuthRunResult, error) {
			return AuthRunResult{}, authErr
		},
		func(tui.ChatSource) error {
			tuiCalls++
			return nil
		},
	)

	err := app.RunTUI(context.Background())
	if !errors.Is(err, authErr) {
		t.Fatalf("err = %v, want auth error", err)
	}
	if tuiCalls != 0 {
		t.Fatalf("TUI was called %d times, want 0", tuiCalls)
	}
	if tg.startCalls != 1 {
		t.Fatalf("startCalls = %d, want 1", tg.startCalls)
	}
	if tg.closeCalls != 1 {
		t.Fatalf("closeCalls = %d, want 1", tg.closeCalls)
	}
}

func TestRunTUIJoinsAuthAndCloseErrors(t *testing.T) {
	authErr := errors.New("auth")
	closeErr := errors.New("close")

	tg := &fakeLifecycle{closeErr: closeErr}
	tuiCalls := 0

	app := NewWithAuth(
		config.Default(),
		recorder.NewNoop(),
		tg,
		func(context.Context) (AuthRunResult, error) {
			return AuthRunResult{}, authErr
		},
		func(tui.ChatSource) error {
			tuiCalls++
			return errors.New("must not be returned")
		},
	)

	err := app.RunTUI(context.Background())

	if !errors.Is(err, authErr) {
		t.Fatalf("error missing auth error: %v", err)
	}
	if !errors.Is(err, closeErr) {
		t.Fatalf("error missing close error: %v", err)
	}
	if tuiCalls != 0 {
		t.Fatalf("TUI calls = %d, want 0", tuiCalls)
	}
}

func TestRunTUIWithoutAuthRunsTUIDirectly(t *testing.T) {
	var order []string
	tg := &fakeLifecycle{order: &order}

	app := NewWithTelegram(
		config.Default(),
		recorder.NewNoop(),
		tg,
		func(tui.ChatSource) error {
			order = append(order, "tui")
			return nil
		},
	)

	if err := app.RunTUI(context.Background()); err != nil {
		t.Fatalf("RunTUI: %v", err)
	}
	want := []string{"start", "tui", "close"}
	if !equalStringSlices(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
}

func TestRunTUIAuthCanceledSkipsTUIPreservesSentinel(t *testing.T) {
	tg := &fakeLifecycle{}
	tuiCalls := 0

	app := NewWithAuth(
		config.Default(),
		recorder.NewNoop(),
		tg,
		func(context.Context) (AuthRunResult, error) {
			return AuthRunResult{}, tui.ErrAuthCanceled
		},
		func(tui.ChatSource) error {
			tuiCalls++
			return nil
		},
	)

	err := app.RunTUI(context.Background())

	if !errors.Is(err, tui.ErrAuthCanceled) {
		t.Fatalf("error = %v, want tui.ErrAuthCanceled", err)
	}
	if tuiCalls != 0 {
		t.Fatalf("TUI calls = %d, want 0", tuiCalls)
	}
	if tg.startCalls != 1 {
		t.Fatalf("start calls = %d, want 1", tg.startCalls)
	}
	if tg.closeCalls != 1 {
		t.Fatalf("close calls = %d, want 1", tg.closeCalls)
	}
}

// ---- Session close ownership ----

func TestRunTUIUsesAuthCloserInsteadOfRuntimeClose(t *testing.T) {
	tg := &fakeLifecycle{}
	authCloseCalls := 0

	app := NewWithAuth(
		config.Default(),
		recorder.NewNoop(),
		tg,
		func(context.Context) (AuthRunResult, error) {
			return AuthRunResult{
				Close: func(context.Context) error {
					authCloseCalls++
					return nil
				},
			}, nil
		},
		func(tui.ChatSource) error { return nil },
	)

	if err := app.RunTUI(context.Background()); err != nil {
		t.Fatalf("RunTUI() error = %v", err)
	}
	if authCloseCalls != 1 {
		t.Fatalf("auth close calls = %d, want 1", authCloseCalls)
	}
	if tg.closeCalls != 0 {
		t.Fatalf("runtime close calls = %d, want 0", tg.closeCalls)
	}
}

func TestRunTUIPropagatesAuthCloserError(t *testing.T) {
	closeErr := errors.New("session close failed")
	tg := &fakeLifecycle{}

	app := NewWithAuth(
		config.Default(),
		recorder.NewNoop(),
		tg,
		func(context.Context) (AuthRunResult, error) {
			return AuthRunResult{
				Close: func(context.Context) error {
					return closeErr
				},
			}, nil
		},
		func(tui.ChatSource) error { return nil },
	)

	err := app.RunTUI(context.Background())
	if !errors.Is(err, closeErr) {
		t.Fatalf("err = %v, want %v", err, closeErr)
	}
	if tg.closeCalls != 0 {
		t.Fatalf("runtime close calls = %d, want 0", tg.closeCalls)
	}
}

func TestRunTUIFallsBackToRuntimeCloseWithoutAuthCloser(t *testing.T) {
	tg := &fakeLifecycle{}

	app := NewWithAuth(
		config.Default(),
		recorder.NewNoop(),
		tg,
		func(context.Context) (AuthRunResult, error) {
			return AuthRunResult{}, nil
		},
		func(tui.ChatSource) error { return nil },
	)

	if err := app.RunTUI(context.Background()); err != nil {
		t.Fatalf("RunTUI() error = %v", err)
	}
	if tg.closeCalls != 1 {
		t.Fatalf("runtime close calls = %d, want 1", tg.closeCalls)
	}
}

func TestRunTUIUsesAuthCloserWhenTUIFails(t *testing.T) {
	tg := &fakeLifecycle{}
	authCloseCalls := 0
	uiErr := errors.New("ui")

	app := NewWithAuth(
		config.Default(),
		recorder.NewNoop(),
		tg,
		func(context.Context) (AuthRunResult, error) {
			return AuthRunResult{
				Close: func(context.Context) error {
					authCloseCalls++
					return nil
				},
			}, nil
		},
		func(tui.ChatSource) error { return uiErr },
	)

	err := app.RunTUI(context.Background())
	if !errors.Is(err, uiErr) {
		t.Fatalf("err = %v, want %v", err, uiErr)
	}
	if authCloseCalls != 1 {
		t.Fatalf("auth close calls = %d, want 1", authCloseCalls)
	}
	if tg.closeCalls != 0 {
		t.Fatalf("runtime close calls = %d, want 0", tg.closeCalls)
	}
}

func TestRunTUIUsesAuthCloserOnAuthError(t *testing.T) {
	tg := &fakeLifecycle{}
	authCloseCalls := 0
	authErr := errors.New("auth")

	app := NewWithAuth(
		config.Default(),
		recorder.NewNoop(),
		tg,
		func(context.Context) (AuthRunResult, error) {
			return AuthRunResult{
				Close: func(context.Context) error {
					authCloseCalls++
					return nil
				},
			}, authErr
		},
		func(tui.ChatSource) error {
			t.Fatal("TUI must not be called when auth fails")
			return nil
		},
	)

	err := app.RunTUI(context.Background())
	if !errors.Is(err, authErr) {
		t.Fatalf("err = %v, want %v", err, authErr)
	}
	if authCloseCalls != 1 {
		t.Fatalf("auth close calls = %d, want 1", authCloseCalls)
	}
	if tg.closeCalls != 0 {
		t.Fatalf("runtime close calls = %d, want 0", tg.closeCalls)
	}
}

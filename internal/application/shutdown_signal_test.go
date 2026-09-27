package application

import (
	"context"
	"errors"
	"os"
	"syscall"
	"testing"
	"time"

	"telecli/internal/config"
	"telecli/internal/tui"
)

// noopComposerSubmitter satisfies tui.ComposerSubmitter for lifecycle
// tests that never submit.
type noopComposerSubmitter struct{}

func (noopComposerSubmitter) SubmitMessage(
	context.Context,
	int64,
	string,
) (tui.Submission, error) {
	return tui.Submission{}, nil
}

func TestWatchShutdownSignalsRecordsSignalAsCause(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	signals := make(chan os.Signal, 1)
	done := watchShutdownSignals(ctx, cancel, signals)

	signals <- syscall.SIGTERM
	<-done

	cause := context.Cause(ctx)
	if !errors.Is(cause, errShutdownSignal) {
		t.Fatalf("cause = %v, want errShutdownSignal", cause)
	}
	var signalErr *shutdownSignalError
	if !errors.As(cause, &signalErr) {
		t.Fatalf("cause = %T, want *shutdownSignalError", cause)
	}
	if got := signalErr.exitCode(); got != 128+int(syscall.SIGTERM) {
		t.Fatalf("exit code = %d, want %d", got, 128+int(syscall.SIGTERM))
	}
}

func TestWatchShutdownSignalsStopsWithContext(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	done := watchShutdownSignals(ctx, cancel, make(chan os.Signal))
	cancel(nil)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("watcher did not stop with its context")
	}
}

// TestRunTUIClosesSessionAfterShutdownSignal pins that a signal which
// cancels the application context still runs the graceful close on a
// fresh shutdown context, so TDLib and the outbox are finalized.
func TestRunTUIClosesSessionAfterShutdownSignal(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)

	var closeCtxErr error
	closed := false
	app := NewWithAuthAndSubmitter(
		config.Default(),
		nil,
		nil,
		func(context.Context) (AuthRunResult, error) {
			return AuthRunResult{
				Submitter: noopComposerSubmitter{},
				Close: func(shutdownCtx context.Context) error {
					closed = true
					closeCtxErr = shutdownCtx.Err()
					return nil
				},
			}, nil
		},
		func(tui.ChatSource) error { return nil },
		func(runCtx context.Context, _ tui.Dependencies) error {
			cancel(&shutdownSignalError{signal: syscall.SIGHUP})
			<-runCtx.Done()
			return runCtx.Err()
		},
	)

	_ = app.RunTUI(ctx)

	if !closed {
		t.Fatal("session was not closed after a shutdown signal")
	}
	if closeCtxErr != nil {
		t.Fatalf("close ran on a cancelled context: %v", closeCtxErr)
	}
}

func TestWithoutCancellationKeepsOnlyRealFailures(t *testing.T) {
	closeErr := errors.New("close Telegram session: timeout")
	err := errors.Join(
		context.Canceled,
		&shutdownSignalError{signal: syscall.SIGTERM},
		errors.Join(context.Canceled, closeErr),
	)

	got := withoutCancellation(err)
	if !errors.Is(got, closeErr) {
		t.Fatalf("got %v, want the close failure kept", got)
	}
	if errors.Is(got, context.Canceled) {
		t.Fatalf("got %v, want cancellation dropped", got)
	}
	if withoutCancellation(errors.Join(context.Canceled)) != nil {
		t.Fatal("pure cancellation must reduce to nil")
	}
}

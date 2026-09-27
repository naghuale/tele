package application

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

// errShutdownSignal marks an application run stopped by an operating
// system signal.
var errShutdownSignal = errors.New("shutdown signal received")

// shutdownSignals are the signals that stop telecli gracefully.
//
// Without a handler SIGTERM and SIGHUP (a closed terminal) kill the
// process before TDLib is closed and before an in-flight outbox send is
// finalized, which leaves that entry uncertain after restart.
var shutdownSignals = []os.Signal{
	syscall.SIGINT,
	syscall.SIGTERM,
	syscall.SIGHUP,
}

// shutdownSignalError is the cancellation cause recorded for a signal.
type shutdownSignalError struct {
	signal os.Signal
}

func (e *shutdownSignalError) Error() string {
	return fmt.Sprintf("%s: %s", errShutdownSignal, e.signal)
}

func (e *shutdownSignalError) Unwrap() error { return errShutdownSignal }

// exitCode follows the shell convention of 128 plus the signal number.
func (e *shutdownSignalError) exitCode() int {
	if sig, ok := e.signal.(syscall.Signal); ok {
		return 128 + int(sig)
	}
	return 1
}

// notifyShutdownSignals cancels ctx with a shutdownSignalError when a
// shutdown signal arrives. The returned stop function restores default
// signal handling and waits for the watcher to exit.
func notifyShutdownSignals(
	ctx context.Context,
	cancel context.CancelCauseFunc,
) (stop func()) {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, shutdownSignals...)
	done := watchShutdownSignals(ctx, cancel, signals)
	return func() {
		signal.Stop(signals)
		cancel(nil)
		<-done
	}
}

// watchShutdownSignals turns the first received signal into a
// cancellation cause. It returns once a signal arrived or ctx is done.
func watchShutdownSignals(
	ctx context.Context,
	cancel context.CancelCauseFunc,
	signals <-chan os.Signal,
) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		select {
		case sig := <-signals:
			cancel(&shutdownSignalError{signal: sig})
		case <-ctx.Done():
		}
	}()
	return done
}

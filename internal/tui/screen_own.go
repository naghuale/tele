package tui

import (
	"io"
	"os"
	"os/signal"

	tea "github.com/charmbracelet/bubbletea"
)

// The auto-wrap mode of the terminal, DECAWM, and what it is for.
//
// The interface draws a frame row by row, and it fits every row to the width
// of the window with a rule of counting it measured the terminal with. A
// terminal that draws a row wider than the program measured it — a row with
// an emoji in it, on a terminal that follows wcwidth and not the emoji rules
// — wraps that row onto the row below it, and every row under the wrap moves
// down one row. A frame is drawn from the top of the screen downwards, so
// from the first row that moved the screen is showing something else: the
// chat list with its search line twice, the preview of one chat under the
// name of another, a bubble a row out of place and a row of a wide character
// running past the right edge (the owner, 01.10).
//
// Measuring well is the other half of the answer and it is in
// internal/tui/termwidth. This is the half that holds when the measurement is
// wrong, when the terminal could not be asked, or when a cluster nobody
// probed is one column out: with the mode off, what does not fit the row is
// clipped at the last column of the window. A clipped row is a row that is
// missing its last character, and it cannot move a single row of anything
// else.
//
// The mode belongs to the terminal and not to this program: it is given back
// before the run returns, so the shell a user comes back to wraps its own
// output the way it always did.
const (
	autoWrapOff = "\x1b[?7l"
	autoWrapOn  = "\x1b[?7h"
)

// owningTheScreen runs a program that owns the whole screen with the
// auto-wrap mode turned off, and puts the mode back however the run ended.
//
// The modes are written through the same writer the frames go to, so a mode
// cannot land in the middle of a frame: the output of the program is one
// object with one lock behind it, and a copy of a message written from a key
// press at the wrong moment would show the rest of an escape sequence as text
// in the middle of a conversation (clipboard.go).
//
// The restore is a defer and not a line after the run, because the two ways
// of not reaching it are the two that matter: a program that stops with an
// error, and a program that panics. Both leave a terminal that cannot wrap,
// which is a terminal the user has to reset with reset before typing
// anything. The signal that ends the process without unwinding the stack is
// covered by watchFatalSignals.
func owningTheScreen(
	out io.Writer,
	run func() (tea.Model, error),
) (tea.Model, error) {
	writeScreenMode(out, autoWrapOff)

	_, stop := watchFatalSignals(out, fatalSignals, raiseAfterDefault)
	defer stop()
	defer writeScreenMode(out, autoWrapOn)

	return run()
}

// writeScreenMode writes one mode of the terminal and says nothing about it.
//
// A terminal that is not there is a file, and a file is written the same way
// a terminal is: the bytes go out and nothing reads them. The error is
// dropped on purpose. A terminal that has gone away mid-run is not a reason
// to stop drawing the interface the user is looking at, and the restore that
// follows fails the same way and just as harmlessly.
func writeScreenMode(out io.Writer, mode string) {
	if out == nil {
		return
	}

	_, _ = io.WriteString(out, mode)
}

// watchFatalSignals watches for the signals that end the process without
// unwinding the stack, and gives the terminal back before letting them
// through.
//
// SIGINT and SIGTERM are not here: the program and the application both stop
// on them in an orderly way, the run returns, and owningTheScreen gives the
// terminal back on its way out. SIGQUIT is, because the Go runtime answers it
// by dumping every goroutine and exiting, and a deferred write never runs on
// the way out of that.
//
// The channel is returned so that a test can send the signal itself rather
// than raise one at the test binary, and raise is what lets the signal do
// what it was going to do: the handler is taken back first, so re-raising
// reaches the default disposition instead of this watcher.
func watchFatalSignals(
	out io.Writer,
	fatal []os.Signal,
	raise func(os.Signal),
) (chan os.Signal, func()) {
	if len(fatal) == 0 {
		return nil, func() {}
	}

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, fatal...)

	done := make(chan struct{})
	go func() {
		select {
		case received := <-signals:
			writeScreenMode(out, autoWrapOn)
			raise(received)
		case <-done:
		}
	}()

	return signals, func() {
		signal.Stop(signals)
		close(done)
	}
}

// raiseAfterDefault puts the default disposition of a signal back and sends
// it to this process again, so that a signal the program was handling ends
// the program the way it was always going to.
func raiseAfterDefault(sig os.Signal) {
	if sig == nil {
		return
	}

	signal.Reset(sig)

	process, err := os.FindProcess(os.Getpid())
	if err != nil {
		return
	}

	_ = process.Signal(sig)
}

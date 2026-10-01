//go:build unix

package tui

import (
	"os"
	"syscall"
)

// fatalSignals are the signals that end the process without unwinding the
// stack, so that a deferred write to the terminal never runs.
//
// There is one of them. SIGINT and SIGTERM stop the program in an orderly
// way — Bubble Tea turns the first into a quit and the application cancels
// on the second — and both come back out of program.Run, which is where the
// terminal is given back. SIGQUIT is answered by the Go runtime with a dump
// of every goroutine and an exit, and nothing runs on the way out of that.
var fatalSignals = []os.Signal{syscall.SIGQUIT}

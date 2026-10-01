//go:build !unix

package tui

import "os"

// fatalSignals is empty on a platform with no signal that ends the process
// without unwinding the stack.
//
// Windows answers Ctrl+C and Ctrl+Break through the console control handler,
// which returns into the program, so a run that was stopped that way comes
// back out of program.Run and the terminal is given back there. A watcher for
// a signal nothing raises is a goroutine waiting for the rest of the program.
var fatalSignals []os.Signal

//go:build !unix

package termwidth

import (
	"os"
	"time"
)

// newFileInput reports that this platform cannot ask a file whether it has
// something in it.
//
// There is no measurement on a platform without a readiness check for a
// terminal, and the rule the interface falls back to is the one a terminal
// nobody could ask is drawn with. Returning an error rather than a reader
// that blocks is the point: a program that waits for a terminal that has
// nothing to say does not draw its first frame at all.
func newFileInput(file *os.File) (Input, error) {
	return nil, ErrNoInputTerminal
}

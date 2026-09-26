// Package secretinput reads secrets from a terminal without echoing them.
//
// It exists so the interactive setup flow never has to know how a
// terminal is put into no-echo mode, and so a secret is never passed as
// a command line argument where it could reach the shell history or the
// process list.
package secretinput

import "errors"

// ErrUnsupported reports that the platform has no no-echo reader.
var ErrUnsupported = errors.New(
	"secretinput: no hidden input is available on this platform",
)

// ErrEmpty reports that the user submitted an empty secret.
var ErrEmpty = errors.New("secretinput: value is empty")

// Reader reads one secret.
//
// The returned slice is owned by the caller. A production
// implementation zeroes its own copy before returning.
type Reader interface {
	ReadSecret(prompt string) ([]byte, error)
}

//go:build unix

package termwidth

import (
	"errors"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// newFileInput returns an Input that reads a terminal without blocking the
// program while it waits for it.
//
// The file is read through the operating system's own readiness check
// rather than through a read with a deadline: a deadline on a terminal is
// not portable, and a read that blocks is a read that puts the program
// behind the terminal until somebody types. Nothing is read here that the
// caller did not ask for, so what a user pressed while the measurement was
// running is still in the terminal when the program starts reading it.
func newFileInput(file *os.File) (Input, error) {
	if file == nil {
		return nil, ErrNoInputTerminal
	}

	return fileInput{file: file}, nil
}

// fileInput waits for a file to have something in it.
type fileInput struct {
	file *os.File
}

// Wait reports whether the file has something to read before the timeout.
//
// It polls rather than blocks, and it is a poll and not a thread: the
// measurement must not be holding a read on the terminal the program is
// about to read from itself. A terminal that has nothing to say makes the
// poll time out, and a program that gives up on a measurement is quicker
// than a program that gives up on a screen.
func (i fileInput) Wait(timeout time.Duration) (bool, error) {
	if timeout <= 0 {
		return false, nil
	}

	milliseconds := int(timeout.Milliseconds())
	if milliseconds < 1 {
		milliseconds = 1
	}

	descriptors := []unix.PollFd{{
		Fd:     int32(i.file.Fd()), //nolint:gosec // a terminal descriptor is small.
		Events: unix.POLLIN,
	}}

	ready, err := unix.Poll(descriptors, milliseconds)
	if err != nil {
		// A signal in the middle of the wait is not a terminal that has
		// stopped answering: the budget is what decides that, and the
		// caller is already counting it down.
		if errors.Is(err, unix.EINTR) {
			return false, nil
		}

		return false, err
	}

	return ready > 0 && descriptors[0].Revents != 0, nil
}

// Read reads what the file has. It is called only after Wait said there is
// something, so it does not block.
func (i fileInput) Read(buffer []byte) (int, error) {
	return i.file.Read(buffer)
}

package tui

import (
	"encoding/base64"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/charmbracelet/x/term"
)

// Copying is OSC 52: the terminal puts the text in the clipboard itself.
//
// It is the copy that works over ssh and in a browser tab, where a program
// cannot reach a clipboard of its own. The text goes to the terminal and
// nowhere else - not to a log, not to a file, not through a command somebody
// would have to trust with a message (§19).
//
// The sequence is written by hand rather than through a library because it is
// one escape sequence with a base64 payload, and the writer is the one the
// program builds: a test substitutes a buffer and reads what would have gone
// to the terminal.

// terminalOutput is where the program's frames and its escape sequences go.
//
// It is one object for both on purpose. Bubble Tea writes a frame from its
// own goroutine, and a copy written from a key press at the same moment
// would land in the middle of that frame's escape sequence - a terminal
// shows the rest of the sequence as text, in the middle of a conversation.
// A mutex is the whole of the answer, and it has to be the *same* lock for
// both writers or it is not an answer at all.
//
// A terminal that is not a terminal gets no copy: OSC 52 is a request to a
// program on the other end of the connection, and a pipe or a file has
// nobody to ask. The interface says the copy did not happen.
type terminalOutput struct {
	mu    sync.Mutex
	out   io.Writer
	file  *os.File
	isTTY bool
}

// newTerminalOutput wraps a writer for the frames and the copies of the
// program.
func newTerminalOutput(out io.Writer, isTTY bool) *terminalOutput {
	file, _ := out.(*os.File)

	return &terminalOutput{out: out, file: file, isTTY: isTTY}
}

// programOutput returns the output of the running program.
func programOutput() *terminalOutput {
	file := os.Stdout

	return newTerminalOutput(file, term.IsTerminal(file.Fd()))
}

// Write serialises a write with the frames of the program.
func (t *terminalOutput) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.out.Write(p)
}

// Read, Close and Fd make terminalOutput a term.File, and that is not a
// detail: Bubble Tea v1.3.10 takes its output as a terminal only when it
// implements term.File, and without that it sends neither the first
// WindowSizeMsg nor a resize. A program whose layout is never told how big
// the terminal is draws it at zero size, and a wrapped writer would have
// done that silently.
//
// The three delegate to the file the output wraps. A test buffer is not a
// file and reports an invalid descriptor, which is the honest answer: there
// is no terminal there to ask.
func (t *terminalOutput) Read(p []byte) (int, error) {
	if t == nil || t.file == nil {
		return 0, os.ErrInvalid
	}

	// No lock: this is the input side, which only Bubble Tea reads, and it
	// never carries a frame.
	return t.file.Read(p)
}

func (t *terminalOutput) Close() error {
	if t == nil || t.file == nil {
		return os.ErrInvalid
	}

	return t.file.Close()
}

func (t *terminalOutput) Fd() uintptr {
	if t == nil || t.file == nil {
		return notATerminalFD
	}

	return t.file.Fd()
}

// notATerminalFD is what an output that is not a file reports as its
// descriptor. A terminal check on it is false on every platform, which is
// what a buffer is.
const notATerminalFD = ^uintptr(0)

var _ term.File = (*terminalOutput)(nil)

// terminal reports whether there is a terminal to ask.
func (t *terminalOutput) terminal() bool {
	return t != nil && t.isTTY
}

// copyText asks the terminal to put text in the clipboard.
//
// The payload is base64, so a message with any character in it - quotes,
// newlines, a phone number pasted from a stranger - travels without
// breaking the sequence.
func (t *terminalOutput) copyText(text string) error {
	if t == nil || t.out == nil {
		return errNoTerminal
	}
	if !t.terminal() {
		return errNoTerminal
	}

	_, err := t.Write([]byte(osc52Sequence(text)))

	return err
}

// errNoTerminal says there is nowhere to put a copied message.
var errNoTerminal = errCopyNoTerminal{}

type errCopyNoTerminal struct{}

func (errCopyNoTerminal) Error() string { return "no terminal to copy into" }

// osc52Sequence returns the escape sequence that puts text in the clipboard
// of the terminal.
func osc52Sequence(text string) string {
	return "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\x07"
}

// osc52Prefix is what every OSC 52 sequence starts with, for a test that
// checks the shape of what was written without decoding it.
const osc52Prefix = "\x1b]52;c;"

// isOSC52 reports whether written is an OSC 52 copy sequence.
func isOSC52(written string) bool {
	return strings.HasPrefix(written, osc52Prefix)
}

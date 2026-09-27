package tui

import (
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/charmbracelet/x/term"
)

// The program's output has to stay a terminal to the program.
//
// Bubble Tea v1.3.10 takes its output as a TTY only when it implements
// term.File (tty_unix.go), and a wrapped writer that does not is a program
// that is never told how big the terminal is: no first WindowSizeMsg, no
// resize, and a layout drawn at zero size. A test that only checks that a
// copy reaches a writer would not have found that.
func TestTheProgramOutputIsATerminalToTheProgram(t *testing.T) {
	output := programOutput()

	file, isFile := any(output).(term.File)
	if !isFile {
		t.Fatalf(
			"the program's output is %T, which is not a term.File: the program would never get a size",
			output,
		)
	}
	if got, want := file.Fd(), os.Stdout.Fd(); got != want {
		t.Fatalf("the output's descriptor = %d, want the program's %d", got, want)
	}
}

// A test buffer is not a terminal, and it says so rather than answering a
// terminal check with somebody else's descriptor.
func TestAnOutputThatIsNotAFileIsNotATerminal(t *testing.T) {
	output := newTerminalOutput(&recordingWriter{}, false)

	if output.Fd() != notATerminalFD {
		t.Fatalf("the descriptor of a buffer is %d, want an invalid one", output.Fd())
	}
	if term.IsTerminal(output.Fd()) {
		t.Fatal("a buffer answered a terminal check")
	}
	if _, err := output.Read(make([]byte, 1)); err == nil {
		t.Fatal("a buffer answered a read")
	}
	if err := output.Close(); err == nil {
		t.Fatal("a buffer answered a close")
	}
}

// The lock is the reason the writer exists, and it is around the write: two
// writes to the terminal at once interleave, and a frame with an escape
// sequence in the middle of it is text on the screen.
//
// The buffer below is synchronized on its own, so what the test reads is
// whole writes in whatever order they arrived - and a writer without the
// lock would be a data race here, which is what -race is for.
func TestTheOutputSerialisesItsWrites(t *testing.T) {
	terminal := &lockedBuffer{}
	output := newTerminalOutput(terminal, true)

	var done sync.WaitGroup
	for _, text := range []string{"a frame", osc52Sequence("текст")} {
		done.Add(1)
		go func() {
			defer done.Done()
			_, _ = output.Write([]byte(text))
		}()
	}
	done.Wait()

	written := terminal.String()
	for _, want := range []string{"a frame", osc52Sequence("текст")} {
		if !strings.Contains(written, want) {
			t.Fatalf("the terminal got %q, want %q in it", written, want)
		}
	}
}

// lockedBuffer is a writer that is safe to read while it is being written.
type lockedBuffer struct {
	mu      sync.Mutex
	written strings.Builder
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.written.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.written.String()
}

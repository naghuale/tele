package tui

import (
	"os"
	"testing"
	"time"

	"github.com/charmbracelet/x/term"

	"telecli/internal/tui/termwidth"
)

// The program measures the terminal before the model is built, and the
// measurement is what the model is drawn with: a model built before the
// terminal was asked has no way to know how it draws, and counting columns
// the way every other program does is the bug this measurement is here for.

func TestAConfiguredRuleIsNotMeasured(t *testing.T) {
	for _, mode := range []termwidth.Mode{
		termwidth.ModeGrapheme,
		termwidth.ModeCodepoint,
	} {
		measured := measureTerminalWidths(mode)

		if measured.Usable() {
			t.Errorf(
				"measureTerminalWidths(%v) has %d answers, want none: the "+
					"rule was asked for by name",
				mode, measured.Answered(),
			)
		}
	}
}

// A test runs with its output in a file, and that is the case the
// measurement has to survive: a program whose output is a file has no
// terminal to ask, and it draws its first frame rather than waiting for an
// answer that is not coming.
func TestAMeasurementWithoutATerminalFallsBackToTheRule(t *testing.T) {
	if term.IsTerminal(os.Stdout.Fd()) {
		t.Skip("the output of this test is a terminal")
	}

	started := time.Now()
	measured := measureTerminalWidths(termwidth.ModeAuto)
	elapsed := time.Since(started)

	if measured.Usable() {
		t.Fatalf("the measurement has %d answers, want none", measured.Answered())
	}
	if elapsed > time.Second {
		t.Fatalf("the measurement took %v with no terminal to ask", elapsed)
	}

	model, choice := termwidth.Select(termwidth.ModeAuto, measured)
	if choice.Mode != termwidth.ModeCodepoint {
		t.Fatalf("the screen is drawn in %v, want the codepoint rule", model.Mode())
	}
}

// A key pressed while the measurement was in the air is a key press to
// this program, and the program is about to start reading for it. Nothing
// is dropped and nothing is asked of a terminal that is not there.
func TestTheProgramReadsWhatTheMeasurementRead(t *testing.T) {
	t.Run("nothing was read", func(t *testing.T) {
		if input := inputWithPending(&termwidth.Measurement{}); input != nil {
			t.Errorf("the input is %v, want the terminal itself", input)
		}
		if input := inputWithPending(nil); input != nil {
			t.Errorf("the input is %v, want the terminal itself", input)
		}
	})

	t.Run("a key press was read", func(t *testing.T) {
		measured := &termwidth.Measurement{Pending: []byte("q")}

		input := inputWithPending(measured)
		if input == nil {
			t.Fatal("the program was given the terminal and lost the key press")
		}

		// The program has to be able to put the terminal into raw mode
		// and take it out again, so the reader it is given is the
		// terminal with the bytes in front of it and not a pipe.
		if _, ok := input.(term.File); !ok {
			t.Fatalf("the input is %T, want something with a file descriptor", input)
		}

		buffer := make([]byte, 4)

		read, err := input.Read(buffer)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if read != 1 || buffer[0] != 'q' {
			t.Fatalf("read %d bytes %q, want the key press", read, buffer[:read])
		}
	})
}

// The measurement reads the terminal by polling it rather than by taking
// a read on it, so it is finished with the terminal when it returns and
// the program is the only thing left reading it. A measurement that kept a
// goroutine waiting for a keystroke would take turns with the program for
// the bytes of one, and a key press is taken by the wrong one.

package tui

import (
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"telecli/internal/tui/termwidth"
)

// The auto-wrap mode of the terminal is turned off while the interface owns
// the screen.
//
// The owner's screen of 01.10 says what happens without it: a row the
// program measured a column narrower than the terminal draws it reaches the
// last column, the terminal wraps it onto the row below, and everything
// under the wrap moves down one row. That is the chat list with "/ search"
// twice, the "Big Geek" preview and its 49 badge twice, a bubble row
// shifted, and a cell of an old background at the left edge of a row. The
// measuring is the other half of the answer (termwidth), and this is the
// half that holds when the measuring is wrong: with the mode off, a row one
// column too wide is clipped at the last column of the window, and a
// clipped row cannot move anything.

// The terminal is told before the first frame and given back after the
// last one, through the writer the program's own frames go to.
func TestTheScreenIsTakenWithAutoWrapOffAndGivenBack(t *testing.T) {
	out := &lockedBuffer{}

	_, err := owningTheScreen(out, func() (tea.Model, error) {
		_, err := io.WriteString(out, "frame\n")

		return nil, err
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	written := out.String()
	if !strings.HasPrefix(written, autoWrapOff) {
		t.Errorf(
			"the run began with %q, want the auto-wrap mode turned off first",
			written,
		)
	}
	if !strings.HasSuffix(written, autoWrapOn) {
		t.Errorf(
			"the run ended with %q, want the auto-wrap mode given back last",
			written,
		)
	}

	frame := strings.Index(written, "frame")
	off, on := strings.Index(written, autoWrapOff), strings.LastIndex(written, autoWrapOn)
	if off > frame || on < frame {
		t.Errorf(
			"the mode was turned off at %d and given back at %d, the frame at %d",
			off, on, frame,
		)
	}
}

// A run that ends badly still gives the terminal back. A program that dies
// with the mode off leaves a terminal that cannot wrap its own shell
// output, which is a terminal a user has to reset by hand.
func TestTheScreenIsGivenBackWhenTheRunFails(t *testing.T) {
	out := &lockedBuffer{}
	want := errors.New("the program stopped")

	if _, err := owningTheScreen(out, func() (tea.Model, error) {
		return nil, want
	}); !errors.Is(err, want) {
		t.Fatalf("the error of the run is %v, want %v", err, want)
	}

	if !strings.HasSuffix(out.String(), autoWrapOn) {
		t.Errorf("a failed run ended with %q, want the mode given back", out.String())
	}
}

// And a panic, which is the one end of a run whose result nobody reads.
func TestTheScreenIsGivenBackWhenTheRunPanics(t *testing.T) {
	out := &lockedBuffer{}

	panicked := func() (panicked bool) {
		defer func() {
			if recover() != nil {
				panicked = true
			}
		}()

		_, _ = owningTheScreen(out, func() (tea.Model, error) {
			panic("the view drew a row nobody could measure")
		})

		return false
	}()

	if !panicked {
		t.Error("the panic did not come out of the run, so nothing was tested")
	}
	if !strings.HasSuffix(out.String(), autoWrapOn) {
		t.Errorf(
			"a panicking run ended with %q, want the mode given back",
			out.String(),
		)
	}
}

// A signal that ends the process without unwinding the stack gets the
// terminal back as well, which is the one exit a deferred write does not
// reach.
func TestTheScreenIsGivenBackWhenTheProcessIsSignalled(t *testing.T) {
	out := &lockedBuffer{}
	raised := make(chan os.Signal, 1)

	signals, stop := watchFatalSignals(out, fatalSignals, func(sig os.Signal) {
		raised <- sig
	})
	defer stop()

	signals <- os.Interrupt

	select {
	case <-raised:
	case <-time.After(2 * time.Second):
		t.Fatal("the signal was not raised again")
	}

	if !strings.HasSuffix(out.String(), autoWrapOn) {
		t.Errorf(
			"a signalled run ended with %q, want the mode given back",
			out.String(),
		)
	}
}

// And what that buys on a screen: a row wider than the window is clipped at
// its last column, and the row below it stays exactly where it is.
//
// This is the owner's screen, one row too wide: a wrapping terminal takes
// the row below it too, and every row under that, and a row that moved is
// the row the reader was looking at.
func TestARowWiderThanTheScreenDoesNotMoveTheRowBelowIt(t *testing.T) {
	const (
		width  = 40
		height = 8
	)

	widths := termwidth.Unmeasured(termwidth.ModeGrapheme)
	frameRows := rowsOfTheFrameWithARowOverTheEdge(width)
	frame := frameWithARowOverTheEdge(width)

	clipping := newScreenEmulator(width, height, widths)
	clipping.Write([]byte(autoWrapOff + frame)) //nolint:errcheck

	wrapping := newScreenEmulator(width, height, widths)
	wrapping.Write([]byte(autoWrapOn + frame)) //nolint:errcheck

	// The two are not the same screen, which is the point: with the mode on,
	// a row one column too wide takes the next row with it.
	moved := 0
	for row := 1; row < height; row++ {
		if clipping.rowText(row) != wrapping.rowText(row) {
			moved++
		}
	}
	if moved == 0 {
		t.Fatal("a wrapping terminal did not move a row, so nothing was tested")
	}

	// With the mode off, every row the frame drew is the row the terminal
	// holds, including the row after the one that was too wide.
	for row, want := range frameRows {
		got := clipping.rowText(row)
		if row == overWideRow {
			// The row itself is clipped: the last character of it is drawn
			// over the one before it, because there is no column for it. It
			// is a row a character short, which is what a column of air at
			// the edge of a pane costs, and it is not a row that moves.
			if got == want {
				t.Error("the over-wide row was drawn whole, so it was not clipped")
			}
			if !strings.HasPrefix(got, "⛩") {
				t.Errorf("the over-wide row reads %q, want its own text kept", got)
			}

			continue
		}

		if got != want {
			t.Errorf("row %d reads %q, want %q", row+1, got, want)
		}
	}
}

// overWideRow is which row of that frame is one column too wide.
const overWideRow = 1

// rowsOfTheFrameWithARowOverTheEdge is what that frame says, one entry per
// row, as the terminal is expected to hold it.
func rowsOfTheFrameWithARowOverTheEdge(width int) []string {
	// The wide row is a pagoda, two flags and letters: a cluster a
	// terminal can draw two columns wide, followed by letters whose width
	// no rule disputes, so that what the row is clipped by is visible in
	// the text of it.
	over := "⛩" + "🇨🇳🇨🇳" + strings.Repeat("B", width-8) + "BCDE"

	return []string{
		strings.Repeat("A", width),
		over,
		strings.Repeat("C", width),
		strings.Repeat("D", width),
		strings.Repeat("E", width),
	}
}

// frameWithARowOverTheEdge is a frame of five rows whose second row is one
// column wider than the window, which is what a row measured with the code
// point rule is on a terminal that draws each of those clusters two columns
// wide.
func frameWithARowOverTheEdge(width int) string {
	rows := rowsOfTheFrameWithARowOverTheEdge(width)

	return "\x1b[H" + strings.Join(rows, "\r\n") + "\r\n"
}

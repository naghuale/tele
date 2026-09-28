package termwidth

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// answeringTerminal is a terminal that answers a cursor position request
// the way a terminal that draws the probes in the widths the test says does.
//
// It is the whole measurement in one value: what the program writes is
// parsed, and what comes back is the answer a terminal with those widths
// would give. A test that wanted a real terminal for this would need a
// terminal emulator, and a terminal emulator is not what is being proved
// here — what is being proved is that the program asks, reads, and gets on
// with it.
type answeringTerminal struct {
	// widths is what a probe takes in the columns of this terminal.
	widths map[string]int

	// before is written into every answer ahead of it, as though a key had
	// been pressed while the question was in the air.
	before string

	// silent is a terminal that never answers at all.
	silent bool

	written bytes.Buffer
	queued  bytes.Buffer
	// waitFor is how long the last Wait was asked to wait for, which is
	// what a terminal with nothing to say is asked.
	waited time.Duration
}

// Write is the request going out. A request for the cursor position is
// answered with one.
func (t *answeringTerminal) Write(p []byte) (int, error) {
	return t.written.Write(p)
}

// Read is what comes back.
func (t *answeringTerminal) Read(p []byte) (int, error) {
	if t.queued.Len() == 0 {
		return 0, io.EOF
	}

	return t.queued.Read(p)
}

// Wait reports whether there is anything to read, and waits for the time it
// was given when there is not: a terminal that has nothing to say makes the
// program wait exactly as long as the program is willing to wait.
//
// A terminal that answers does not wait to be asked twice: by the time the
// program asks whether there is anything, the answer is on its way.
func (t *answeringTerminal) Wait(timeout time.Duration) (bool, error) {
	t.waited = timeout

	if t.queued.Len() > 0 {
		return true, nil
	}
	if t.silent {
		time.Sleep(timeout)

		return false, nil
	}

	t.answer()

	return t.queued.Len() > 0, nil
}

// answer answers the question that was asked last.
func (t *answeringTerminal) answer() {
	written := t.written.String()
	index := strings.LastIndex(written, cursorPositionRequest)
	if index < 0 {
		return
	}

	probe := written[:index]
	probe = probe[strings.LastIndex(probe, "\r")+1:]

	width, known := t.widths[probe]
	if !known {
		return
	}

	t.queued.WriteString(t.before)
	t.queued.WriteString("\x1b[1;")
	t.queued.WriteString(itoa(width + 1))
	t.queued.WriteString("R")
}

// Terminal returns the terminal as the measurement sees it.
func (t *answeringTerminal) Terminal() Terminal {
	return Terminal{In: t, Out: t}
}

// itoa writes a small positive number the way a terminal would.
func itoa(value int) string {
	if value == 0 {
		return "0"
	}

	var digits []byte
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}

	return string(digits)
}

// The measurement reads what the terminal answered and believes it.
func TestMeasureReadsTheWidthsTheTerminalGave(t *testing.T) {
	terminal := &answeringTerminal{widths: appleTerminalAnswers}

	measured := Measure(terminal.Terminal(), time.Second)

	if measured.TimedOut {
		t.Fatal("the measurement gave up on a terminal that answered")
	}
	if measured.Answered() != len(Probes) {
		t.Fatalf("answered about %d probes, want %d", measured.Answered(), len(Probes))
	}
	for _, probe := range Probes {
		if measured.Widths[probe] != appleTerminalAnswers[probe] {
			t.Errorf(
				"%q was measured as %d columns, want %d",
				probe, measured.Widths[probe], appleTerminalAnswers[probe],
			)
		}
	}
	if len(measured.Pending) != 0 {
		t.Errorf("the measurement kept %q, want nothing", measured.Pending)
	}
}

// Every probe is written at the start of the line and the line is erased
// before the next one, so a question about column widths leaves nothing on
// the screen of a user.
func TestMeasureLeavesTheLineItWroteOnEmpty(t *testing.T) {
	terminal := &answeringTerminal{widths: appleTerminalAnswers}

	Measure(terminal.Terminal(), time.Second)

	written := terminal.written.String()
	if strings.Count(written, cursorPositionRequest) != len(Probes) {
		t.Errorf(
			"%d requests were written, want one per probe (%d)",
			strings.Count(written, cursorPositionRequest), len(Probes),
		)
	}
	if got := strings.Count(written, "\r\x1b[2K"); got != len(Probes) {
		t.Errorf("the line was erased %d times, want %d", got, len(Probes))
	}
	if !strings.HasSuffix(written, "\r\x1b[2K") {
		t.Errorf("the measurement ended with %q, want the line erased", written)
	}
	if !strings.HasPrefix(written, "\r") {
		t.Errorf("the first probe was not written at the start of the line: %q", written)
	}
}

// A key pressed in the window between the program starting and the terminal
// answering is a key press the user made to this program. The measurement
// is the only thing that read it, and the program is about to start reading
// for it: dropping it would eat a key press for a reason nobody can see.
func TestMeasureKeepsTheBytesThatWereNotAnswers(t *testing.T) {
	terminal := &answeringTerminal{
		widths: appleTerminalAnswers,
		before: "the user typed this",
	}

	measured := Measure(terminal.Terminal(), time.Second)

	// The key press arrived with every answer, and every one of them was
	// kept: the program reads this terminal next, and each of those bytes
	// is a key press the user made.
	want := strings.Repeat(terminal.before, len(Probes))
	if got := string(measured.Pending); got != want {
		t.Fatalf("pending = %q, want %q", got, want)
	}
	if measured.Answered() != len(Probes) {
		t.Fatalf(
			"answered about %d probes, want %d: a key press is not a bad answer",
			measured.Answered(), len(Probes),
		)
	}
}

// A terminal that never answers is a terminal that is not a terminal. The
// measurement gives up inside its budget, says so, and the program draws
// its first frame with the rule it would have used anyway.
func TestMeasureGivesUpOnATerminalThatSaysNothing(t *testing.T) {
	terminal := &answeringTerminal{silent: true}

	const budget = 40 * time.Millisecond

	started := time.Now()
	measured := Measure(terminal.Terminal(), budget)
	elapsed := time.Since(started)

	if !measured.TimedOut {
		t.Fatal("the measurement did not say that it gave up")
	}
	if measured.Usable() {
		t.Fatalf("the measurement has %d answers, want none", measured.Answered())
	}
	if elapsed > 4*budget {
		t.Fatalf("the measurement took %v, want at most about %v", elapsed, 4*budget)
	}
	if terminal.waited > budget {
		t.Fatalf(
			"the terminal was asked to wait %v, want at most the budget %v",
			terminal.waited, budget,
		)
	}
}

// The budget is the whole measurement and not the budget per probe: a
// terminal that stops answering in the middle has gone quiet, and the rest
// of the budget is better spent on the first frame.
func TestTheBudgetIsTheWholeMeasurement(t *testing.T) {
	terminal := &answeringTerminal{
		widths: appleTerminalAnswers,
		silent: true,
	}

	const budget = 40 * time.Millisecond

	started := time.Now()
	Measure(terminal.Terminal(), budget)
	elapsed := time.Since(started)

	if elapsed > 4*budget {
		t.Fatalf(
			"the measurement took %v for %d probes, want at most about %v",
			elapsed, len(Probes), 4*budget,
		)
	}
}

// The terminal is put back the way it was found, whatever happened in
// between: a program that leaves a terminal in raw mode is a program the
// user cannot type in afterwards.
func TestMeasurePutsTheTerminalBack(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		terminal *answeringTerminal
	}{
		{name: "a terminal that answered", terminal: &answeringTerminal{
			widths: appleTerminalAnswers,
		}},
		{name: "a terminal that did not", terminal: &answeringTerminal{
			silent: true,
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var (
				entered bool
				left    bool
			)

			terminal := testCase.terminal.Terminal()
			terminal.Raw = func() (func(), error) {
				entered = true

				return func() { left = true }, nil
			}

			Measure(terminal, 20*time.Millisecond)

			if !entered {
				t.Error("the terminal was never put into raw mode")
			}
			if !left {
				t.Error("the terminal was not put back")
			}
		})
	}
}

// A terminal that cannot be put into raw mode is not measured at all: a
// question asked on a terminal that is buffering its input is a question
// that is not answered until the user presses a key.
func TestATerminalThatCannotBePutIntoRawModeIsNotMeasured(t *testing.T) {
	fake := &answeringTerminal{widths: appleTerminalAnswers}
	terminal := fake.Terminal()
	terminal.Raw = func() (func(), error) {
		return nil, errors.New("not a terminal")
	}

	measured := Measure(terminal, time.Second)

	if measured.Usable() {
		t.Fatalf("the measurement has %d answers, want none", measured.Answered())
	}
	if fake.written.Len() != 0 {
		t.Errorf("something was written to a terminal that cannot answer: %q", fake.written.String())
	}
}

// A program whose output is a file has no terminal to ask, and says so
// rather than writing a cursor position request into the file.
func TestAMeasurementWithoutATerminalIsEmpty(t *testing.T) {
	var out bytes.Buffer

	measured := Measure(Terminal{Out: &out}, time.Second)

	if measured.Usable() || measured.TimedOut {
		t.Fatalf("the measurement is %+v, want nothing at all", measured)
	}
	if out.Len() != 0 {
		t.Errorf("%q was written with no terminal to read it", out.String())
	}
}

func TestNewTerminalWithoutFiles(t *testing.T) {
	if _, err := NewTerminal(nil, nil); !errors.Is(err, ErrNoInputTerminal) {
		t.Fatalf("NewTerminal(nil, nil) = %v, want ErrNoInputTerminal", err)
	}
}

// ---- the answers, one at a time ----

func TestCutCPR(t *testing.T) {
	cases := []struct {
		name    string
		buffer  string
		column  int
		head    string
		rest    string
		decided bool
	}{
		{
			name:    "an answer on its own",
			buffer:  "\x1b[1;5R",
			column:  5,
			rest:    "",
			decided: true,
		},
		{
			name:    "a key press before the answer is kept for the program",
			buffer:  "a\x1b[1;5R",
			column:  5,
			head:    "a",
			decided: true,
		},
		{
			name:    "an answer of two digits",
			buffer:  "\x1b[12;120R",
			column:  120,
			rest:    "",
			decided: true,
		},
		{
			name:    "what follows the answer is kept",
			buffer:  "\x1b[1;5Rxyz",
			column:  5,
			rest:    "xyz",
			decided: true,
		},
		{
			name:    "an answer that is not all here yet",
			buffer:  "\x1b[1;5",
			decided: false,
		},
		{
			name:    "a lone escape is not an answer",
			buffer:  "\x1b",
			decided: false,
		},
		{
			name:    "an arrow key is not an answer",
			buffer:  "\x1b[A",
			decided: false,
		},
		{
			name:    "nothing that looks like one",
			buffer:  "hello",
			decided: false,
		},
		{
			name:    "an escape sequence that is not a cursor report",
			buffer:  "\x1b[38;2;1;2;3m",
			decided: false,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			column, head, tail, ok := cutCPR([]byte(testCase.buffer))

			if ok != testCase.decided {
				t.Fatalf(
					"cutCPR(%q) decided = %v, want %v",
					testCase.buffer, ok, testCase.decided,
				)
			}
			if !ok {
				return
			}
			if column != testCase.column {
				t.Errorf("cutCPR(%q) column = %d, want %d", testCase.buffer, column, testCase.column)
			}
			want := testCase.head + testCase.rest
			if got := string(head) + string(tail); got != want {
				t.Errorf("cutCPR(%q) left %q, want %q", testCase.buffer, got, want)
			}
		})
	}
}

// A terminal that answers in two pieces is answering, and a width made out
// of half an answer is a width of nothing.
func TestAnAnswerInTwoPiecesIsOneAnswer(t *testing.T) {
	terminal := &partialTerminal{parts: []string{"\x1b[1;", "5R"}}

	reader := newCPRReader(terminal, time.Now().Add(time.Second))

	column, ok := reader.readColumn()
	if !ok {
		t.Fatal("an answer in two pieces was not an answer")
	}
	if column != 5 {
		t.Fatalf("the column is %d, want 5", column)
	}
	if pending := reader.pending(); len(pending) != 0 {
		t.Errorf("the reader kept %q, want nothing", pending)
	}
}

// partialTerminal hands out its bytes one read at a time, which is what a
// terminal does when the answer and something else arrive together.
type partialTerminal struct {
	parts []string
	read  int
}

func (p *partialTerminal) Wait(time.Duration) (bool, error) { return true, nil }

func (p *partialTerminal) Read(buffer []byte) (int, error) {
	if p.read >= len(p.parts) {
		return 0, io.EOF
	}

	read := copy(buffer, p.parts[p.read])
	p.read++

	return read, nil
}

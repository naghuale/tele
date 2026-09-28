package termwidth

import (
	"errors"
	"io"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"
)

// MeasureBudget is how long the whole measurement may take.
//
// One hundred and fifty milliseconds is a fraction of what a user notices
// and several times what a terminal needs to answer a question it has
// already been asked a thousand times. It is the whole measurement and not
// the whole budget per probe: a terminal that answers the first probe and
// then stops has gone quiet, and the rest of the budget would be spent
// waiting for it to start again.
const MeasureBudget = 150 * time.Millisecond

// ErrNoInputTerminal reports that there is no terminal to measure.
//
// It is not a failure of the program: telecli draws a screen without one
// when its output is a file, and the rule that is assumed for a terminal
// that cannot be asked is the one it draws with.
var ErrNoInputTerminal = errors.New("termwidth: no terminal to measure")

// cursorPositionRequest is the standard question a terminal answers with
// the row and the column the cursor is in: ESC [ 6 n.
//
// It is a question and not a command, so a terminal that is asked one
// answers one and draws nothing. The answer is ESC [ row ; column R.
const cursorPositionRequest = "\x1b[6n"

// cursorPositionAnswer opens the answer of the request above.
const cursorPositionAnswer = 'R'

// Input is the reading end of a terminal, as the measurement sees it.
//
// It is an interface and not an *os.File for two reasons. A test has to be
// able to state the bytes a terminal answered with, because no terminal
// answers the same way. And a file read blocks until there is something to
// read, so the measurement needs to ask whether there is before it reads —
// a terminal that has gone quiet must not hold the program before its first
// frame, and a user must not be able to make the program wait by not
// typing.
type Input interface {
	// Wait reports whether the terminal has something to read, giving up
	// after timeout.
	Wait(timeout time.Duration) (bool, error)

	// Read reads what is there. It is called only after Wait said there is
	// something, and returns what the terminal has.
	Read(buffer []byte) (int, error)
}

// Terminal is the terminal a measurement talks to.
type Terminal struct {
	// In is where the answers arrive.
	In Input

	// Out is where the requests go.
	Out io.Writer

	// Raw puts the terminal into raw mode and returns the function that
	// puts it back.
	//
	// It is needed because a terminal in its ordinary mode buffers what it
	// is sent and answers nothing until a key is pressed: the answer to
	// the question is itself an input, and it is queued behind the line
	// discipline.
	//
	// A nil Raw means the terminal needs no mode change, which is what a
	// test and a pipe are.
	Raw func() (restore func(), err error)
}

// NewTerminal returns the terminal a program is running in, and whether
// there is one.
//
// Both ends have to be terminals. A question asked on a file is a question
// nobody answers, and the fallback rule is written for that case already.
func NewTerminal(in, out *os.File) (Terminal, error) {
	if in == nil || out == nil {
		return Terminal{}, ErrNoInputTerminal
	}
	if !term.IsTerminal(in.Fd()) || !term.IsTerminal(out.Fd()) {
		return Terminal{}, ErrNoInputTerminal
	}

	reader, err := newFileInput(in)
	if err != nil {
		return Terminal{}, err
	}

	return Terminal{
		In:  reader,
		Out: out,
		Raw: func() (func(), error) {
			state, err := term.MakeRaw(in.Fd())
			if err != nil {
				return nil, err
			}

			return func() { _ = term.Restore(in.Fd(), state) }, nil
		},
	}, nil
}

// Measure asks the terminal how many columns it gives each probe.
//
// It is the only place the program talks to the terminal before the first
// frame, and it is over in a fraction of a second. Each probe is written
// at the start of the line, the cursor position is requested, and the
// answer says how far the cursor moved: a terminal that put the hand in
// two columns leaves the cursor one column further along than a terminal
// that put it in one.
//
// Three things are true of it whatever the terminal does. The line it
// wrote on is erased before it returns, so a probe is never left on the
// screen. Bytes that are not an answer are given back rather than eaten,
// because a key pressed in the window between the program starting and the
// question being answered is a key press, and the program is about to
// start reading for it. And the terminal is put back the way it was found,
// whatever happened in between: a program that leaves a terminal in raw
// mode is a program the user cannot type in afterwards.
func Measure(terminal Terminal, budget time.Duration) Measurement {
	if terminal.In == nil || terminal.Out == nil {
		return Measurement{}
	}

	if terminal.Raw != nil {
		restore, err := terminal.Raw()
		if err != nil {
			return Measurement{}
		}
		if restore != nil {
			defer restore()
		}
	}

	deadline := time.Now().Add(budget)
	reader := newCPRReader(terminal.In, deadline)

	var result Measurement

	for _, probe := range Probes {
		width, ok := reader.ask(terminal.Out, probe)
		if !ok {
			break
		}

		if result.Widths == nil {
			result.Widths = map[string]int{}
		}
		result.Widths[probe] = width
	}

	// Whatever was read and was not an answer goes back to the caller,
	// whatever came of the measurement: a key pressed in the window
	// between the program starting and the question being answered is a
	// key press, and the program is about to start reading for it.
	result.Pending = reader.pending()
	result.TimedOut = len(result.Widths) == 0 && reader.expired()

	return result
}

// cprReader asks a terminal where the cursor is and reads the answer out of
// what comes back.
type cprReader struct {
	in       Input
	deadline time.Time

	// buffer is what has been read and not yet accounted for: the bytes
	// before the answer being waited for, and a partial answer.
	buffer []byte

	// read is what was read before an answer and is not ours. It is handed
	// back to the caller rather than dropped: a key pressed while the
	// question was in the air is a key press to this program, and the
	// program is about to start reading for it.
	read []byte
}

// newCPRReader returns a reader that gives up at the deadline.
func newCPRReader(in Input, deadline time.Time) *cprReader {
	return &cprReader{in: in, deadline: deadline}
}

// ask writes the probe, asks where the cursor is, and returns what the
// terminal answered.
//
// It reports false when the terminal did not answer within the budget or
// at all, which is a measurement with nothing in it rather than a failure:
// the caller falls back to the rule it would have used anyway.
func (r *cprReader) ask(out io.Writer, probe string) (int, bool) {
	if _, err := io.WriteString(out, "\r"+probe+cursorPositionRequest); err != nil {
		return 0, false
	}

	// The line the probe was written on goes before the next question, so
	// the terminal never has two probes on it and nothing is left behind.
	defer func() { _, _ = io.WriteString(out, "\r\x1b[2K") }()

	column, ok := r.readColumn()
	if !ok {
		return 0, false
	}

	// The answer counts columns from one, and the probe was written at
	// column zero: the cursor is one past the last column it drew.
	return column - 1, true
}

// readColumn waits for the answer to the request and returns the column it
// named.
func (r *cprReader) readColumn() (int, bool) {
	for {
		if column, head, tail, ok := cutCPR(r.buffer); ok {
			r.read = append(r.read, head...)
			r.buffer = tail

			return column, true
		}

		wait := time.Until(r.deadline)
		if wait <= 0 {
			return 0, false
		}

		ready, err := r.in.Wait(wait)
		if err != nil || !ready {
			return 0, false
		}

		buffer := make([]byte, readChunk)

		read, err := r.in.Read(buffer)
		if read > 0 {
			r.buffer = append(r.buffer, buffer[:read]...)

			// An answer that arrived in the same read as the end of it is
			// an answer, and a read that ends in an error is still a read.
			if column, head, tail, ok := cutCPR(r.buffer); ok {
				r.read = append(r.read, head...)
				r.buffer = tail

				return column, true
			}
		}
		if err != nil {
			return 0, false
		}
	}
}

// pending returns the bytes that were read and were not the answer to a
// request of ours.
//
// They are a key press, or the beginning of a mouse report the program
// has not read yet, and they are handed back rather than dropped: the
// program reads this terminal next, and a byte this measurement took and
// threw away is a key press the user pressed.
func (r *cprReader) pending() []byte {
	out := append(r.read, r.buffer...)
	r.read, r.buffer = nil, nil

	return out
}

// expired reports whether the budget is gone.
func (r *cprReader) expired() bool {
	return !time.Now().Before(r.deadline)
}

// readChunk is how much of a terminal is read at a time.
//
// It is one read per answer: the answer to a cursor position request is a
// dozen bytes, and a program that is measuring a terminal has no business
// sitting on a large buffer of what a user typed.
const readChunk = 256

// cutCPR returns the column of the first cursor position answer in buffer,
// the bytes that came before it, and the bytes that follow it.
//
// The answer is ESC [ row ; column R, and everything before it is not ours:
// it is handed to the caller, which will give it back to the program as
// input. An answer that has not all arrived is waited for: a terminal that
// answers in two pieces is answering, and reading half of one would make a
// width out of a fragment. An escape sequence that is not the start of an
// answer is not waited for — a user who pressed Esc in the window between
// the program starting and the question being answered is waiting for the
// program, not for a terminal to finish a sentence.
func cutCPR(buffer []byte) (int, []byte, []byte, bool) {
	for index, value := range buffer {
		if value != 0x1b {
			continue
		}

		column, size, ok := parseCPR(buffer[index:])
		if ok {
			return column, buffer[:index], buffer[index+size:], true
		}

		if couldBeCPR(buffer[index:]) {
			return 0, nil, nil, false
		}
	}

	return 0, nil, nil, false
}

// couldBeCPR reports whether these bytes are the beginning of a cursor
// position answer that has not all arrived.
//
// It is deliberately unsure: every prefix of the answer is one, including
// a single ESC, and the difference between "not here yet" and "not this" is
// settled by the next read or by the budget running out.
func couldBeCPR(answer []byte) bool {
	if len(answer) == 0 {
		return false
	}
	if answer[0] != 0x1b {
		return false
	}
	if len(answer) == 1 {
		return true
	}
	if answer[1] != '[' {
		return false
	}

	index := 2
	for digits := 0; index < len(answer) && isDigit(answer[index]); index++ {
		digits++
	}

	switch {
	case index == 2:
		// A number has to start here.
		return false
	case index == len(answer):
		return true
	case answer[index] != ';':
		return false
	}

	index++

	columnStart := index
	for index < len(answer) && isDigit(answer[index]) {
		index++
	}

	switch {
	case index == columnStart:
		return false
	case index == len(answer):
		return true
	default:
		return answer[index] == cursorPositionAnswer
	}
}

// isDigit reports whether a byte is one of the ten a cursor position answer
// is written in.
func isDigit(value byte) bool {
	return value >= '0' && value <= '9'
}

// parseCPR returns the column of a cursor position answer and how many
// bytes it is.
func parseCPR(answer []byte) (int, int, bool) {
	rest, ok := strings.CutPrefix(string(answer), "\x1b[")
	if !ok {
		return 0, 0, false
	}

	_, after, ok := parseCPRNumber(rest)
	if !ok {
		return 0, 0, false
	}

	columnText, ok := strings.CutPrefix(after, ";")
	if !ok {
		return 0, 0, false
	}

	column, after, ok := parseCPRNumber(columnText)
	if !ok {
		return 0, 0, false
	}

	terminated, ok := strings.CutPrefix(after, string(cursorPositionAnswer))
	if !ok {
		return 0, 0, false
	}

	size := len(answer) - len(terminated)

	return column, size, true
}

// parseCPRNumber reads the digits at the start of value and the rest of it.
//
// The row is read and thrown away: a question about the width does not need
// to know where it was asked, and a terminal that answered with a row that
// makes no sense has still answered.
func parseCPRNumber(value string) (int, string, bool) {
	digits := 0
	for digits < len(value) && isDigit(value[digits]) {
		digits++
	}
	if digits == 0 {
		return 0, value, false
	}

	number := 0
	for _, digit := range value[:digits] {
		number = number*10 + int(digit-'0')
	}

	return number, value[digits:], true
}

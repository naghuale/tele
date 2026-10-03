package tui

import (
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rivo/uniseg"

	"telecli/internal/tui/termwidth"
)

// This file is a terminal, in memory.
//
// It is a terminal and not a recording of what the program wrote because
// the question this file answers is what a user would see. The program
// writes escape sequences; a terminal applies them; the two agree only if
// somebody applies the first to the second and looks at the result. Every
// assertion in screen_repaint_test.go is that comparison, and a test that
// compared the program's output with its own view would pass on a program
// that draws one thing and believes it drew another.
//
// It is a small emulator rather than a library because the sequences the
// standard Bubble Tea renderer writes are a short list: a cursor home, a
// cursor position, a carriage return, a line feed, an erase-line-right, an
// erase-screen-below, an erase-screen, the modes of the alternate screen
// and the cursor, and the SGR runs of Lip Gloss. Everything else a frame
// can contain is a wide character, and a wide character is two cells and a
// deferred wrap at the right edge — which is the whole of what makes a
// row of exactly the width of the window a question rather than an
// answer.
//
// The widths are counted with the same model the program was measured
// with, because the emulator stands for the terminal the program measured
// and not for a terminal nobody asked.

// emulatorState is where the parser is inside a sequence.
type emulatorState uint8

const (
	// emuText is outside any sequence, which is where text goes.
	emuText emulatorState = iota

	// emuEscape is the byte after ESC, where the sequence has not said
	// what kind it is yet.
	emuEscape

	// emuCSI is inside ESC [ … final.
	emuCSI

	// emuString is inside a sequence that carries text of its own: OSC,
	// DCS, APC, PM and SOS, all of which end at BEL or at ESC \.
	emuString

	// emuStringEscape is the ESC of an ESC \ terminator.
	emuStringEscape
)

// screenEmulator is a terminal of a given size that applies what it is
// given and holds the cells it is left with.
type screenEmulator struct {
	widths termwidth.WidthModel

	mu    sync.Mutex
	width int
	cells [][]string
	taken [][]bool
	row   int
	col   int

	// pending is a run of text that has not been drawn yet, and wrap is
	// the flag a terminal sets when the cell at the right edge has been
	// written: the cursor is still in it, and the next character is the
	// one that moves to the row below.
	pending []rune
	wrap    bool

	// autowrap is DECAWM, which is on in a terminal that was found as it
	// was: what is written past the last column of a row is wrapped onto
	// the row below. The interface turns it off, because a row it measured
	// a column too narrow is a row that reaches the last column, and a row
	// that reaches the last column is a row that moves every row under it.
	autowrap bool

	// tap is where a copy of everything written goes, for a test that has
	// to read the bytes rather than the cells.
	tap io.Writer

	// advanceOneCellPerEmoji is the macOS Terminal's pagoda: drawn two cells
	// wide, cursor advanced one (the owner, 01.10). The glyph takes the two
	// cells it is drawn in and the cursor goes one, so whatever is written
	// after it lands inside its own picture unless the program says where
	// the next cell is.
	advanceOneCellPerEmoji bool

	// painted is how many cells have been written since the terminal was
	// made, a wide character counting for the two of them it takes. A
	// screen drawn again from the top paints every cell of it, and a
	// screen that is only patched paints the cells that changed: the
	// difference between the two is the whole of repaint.go.
	painted int

	// lastWrite is how many cells the write the program made last gave the
	// terminal, which is a whole window for a repaint and the rows that
	// changed for a patch.
	lastWrite int

	savedRow, savedCol int

	state   emulatorState
	params  []byte
	private byte

	// changed is closed and replaced by every write, so that a test
	// waiting for the screen to catch up with the program waits for the
	// write and not for a length of time it guessed at. The renderer
	// draws on a clock of its own, so "the frame is on the screen" is
	// the one thing a test cannot know from the outside.
	changed chan struct{}

	// paintedFrames is how many times the program has written to the
	// terminal.
	paintedFrames int
}

// newScreenEmulator returns a terminal of the given size that draws with
// the given rule.
func newScreenEmulator(width, height int, widths termwidth.WidthModel) *screenEmulator {
	if height < 1 {
		height = 1
	}

	emulator := &screenEmulator{
		widths:   widths,
		width:    width,
		cells:    make([][]string, height),
		taken:    make([][]bool, height),
		changed:  make(chan struct{}),
		autowrap: true,
	}
	for row := range emulator.cells {
		emulator.cells[row] = make([]string, width)
		emulator.taken[row] = make([]bool, width)
	}

	return emulator
}

// Write applies what the program wrote to the terminal.
//
// It is the io.Writer the program is given instead of a terminal. A test
// knows a frame is on the screen by the cells it holds and not by the
// count of the calls: the renderer writes nothing when the frame did not
// change, so a key that changes nothing on the screen draws a frame the
// terminal is never told about — and it is the frame it already holds.
func (e *screenEmulator) Write(p []byte) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.tap != nil {
		_, _ = e.tap.Write(p)
	}

	painted := e.painted
	e.feed(string(p))
	e.lastWrite = e.painted - painted
	e.paintedFrames++

	// Whoever is waiting for the screen to catch up with the program is
	// waiting on this, and on nothing else.
	close(e.changed)
	e.changed = make(chan struct{})

	return len(p), nil
}

// writes reports how many times the program has written to the terminal.
//
// A test that wants to know a frame was *written* rather than merely
// produced compares this across the frame: the renderer skips the write
// altogether when the frame did not change, so a frame of the same bytes
// as the one before it is drawn without the terminal being told, and only
// a repaint gets through.
func (e *screenEmulator) writes() int {
	e.mu.Lock()
	defer e.mu.Unlock()

	return e.paintedFrames
}

// waitForAChange blocks until the program has written to the terminal, or
// until every is over, or until within is over, whichever comes first.
//
// It is how a test waits for a frame, and it is not a wait for a length of
// time that happens to be longer than the clock of the renderer: the
// screen is looked at again after every write and after every moment, and
// the wait ends when the screen is the frame. The moment is there for the
// frame a key draws is already on the screen — a key that changes nothing
// draws a frame the renderer never writes, and a wait that ended only on a
// write would sit on it until something unrelated drew one.
func (e *screenEmulator) waitForAChange(within, every time.Duration) {
	e.mu.Lock()
	changed := e.changed
	e.mu.Unlock()

	wait := min(within, every)
	if wait <= 0 {
		return
	}

	timer := time.NewTimer(wait)
	defer timer.Stop()

	select {
	case <-changed:
	case <-timer.C:
	}
}

// rows returns every row of the screen as the terminal has left it.
func (e *screenEmulator) rows() []string {
	e.mu.Lock()
	defer e.mu.Unlock()

	rows := make([]string, len(e.cells))
	for row := range e.cells {
		rows[row] = e.rowText(row)
	}

	return rows
}

// paintedCells reports how many cells have been written to the terminal.
func (e *screenEmulator) paintedCells() int {
	e.mu.Lock()
	defer e.mu.Unlock()

	return e.painted
}

// cellsOfTheLastWrite reports how many cells the write the program made last
// gave the terminal.
//
// It is paintedCells for one write and not for the terminal, and the two are
// not the same question. A repaint is one write over every cell of the window
// and a patch is one write over the rows that changed, so the write is what
// tells them apart: the cells of several writes added together are a whole
// screen of nothing in particular, and a test that adds them up cannot say
// which of them were the repaint.
func (e *screenEmulator) cellsOfTheLastWrite() int {
	e.mu.Lock()
	defer e.mu.Unlock()

	return e.lastWrite
}

// rowText is one row of the screen, with the right-hand cell of a wide
// character in the one before it and not on its own, and a space for every
// cell nothing was ever written to.
//
// The caller holds the lock.
func (e *screenEmulator) rowText(row int) string {
	var out strings.Builder
	for col, cell := range e.cells[row] {
		if e.taken[row][col] {
			continue
		}
		if cell == "" {
			out.WriteRune(' ')

			continue
		}
		out.WriteString(cell)
	}

	return out.String()
}

// feed applies a whole run of bytes to the parser.
func (e *screenEmulator) feed(data string) {
	for _, r := range data {
		switch e.state {
		case emuText:
			e.text(r)

		case emuEscape:
			e.escape(r)

		case emuCSI:
			e.csi(r)

		case emuString:
			e.stringSequence(r)

		case emuStringEscape:
			e.state = emuString
			if r == '\\' {
				e.state = emuText
			}
		}
	}

	e.draw()
}

// text handles one byte of ordinary text.
func (e *screenEmulator) text(r rune) {
	switch {
	case r == escapeByte:
		e.draw()
		e.state = emuEscape

	case r == '\r':
		e.draw()
		e.col = 0
		e.wrap = false

	case r == '\n':
		e.draw()
		e.index()
		// A line feed is not a carriage return: the column stays where
		// the last `\r` left it, which is why the renderer skips a
		// line with a bare newline.

	case r == '\b':
		e.draw()
		if e.col > 0 {
			e.col--
		}
		e.wrap = false

	case r == '\t':
		e.draw()
		e.col = minInt(((e.col/8)+1)*8, e.width-1)
		e.wrap = false

	case r < 0x20 || r == delByte:
		// A control nobody acts on, and the bell above all: a terminal
		// rings and carries on.

	default:
		e.pending = append(e.pending, r)
	}
}

// draw puts the text that has been collected on the screen.
//
// The whole run is drawn at once, at the moment a control character ends
// it, because a run of text is one run of grapheme clusters: a hand with
// a skin tone modifier behind it is one cell-pair, and drawing it half at
// a time would put two characters where a terminal puts one.
func (e *screenEmulator) draw() {
	for len(e.pending) > 0 {
		cluster, rest, _, _ := uniseg.FirstGraphemeClusterInString(
			string(e.pending), -1,
		)
		if cluster == "" {
			e.pending = nil

			return
		}
		e.pending = []rune(rest)
		e.put(cluster)
	}
}

// put writes one grapheme cluster at the cursor.
//
// The cell it lands on stops being the right-hand half of whatever wide
// character used to own it. A terminal that kept the flag would go on
// hiding the cell that is now there, and a row the program drew in full
// would come back a character short: the chat list of §4.2 has a flag in
// every preview, so a row that moves up under a row of a narrower one lands
// a narrow glyph on the flag's second half often enough to be a name in
// this file.
//
// With the auto-wrap mode off a cluster that does not fit the rest of the
// row is written into the last columns the row has instead, and the cursor
// stays there: the row is clipped at the edge of the window and the row
// below is left exactly where it was.
func (e *screenEmulator) put(cluster string) {
	width := maxInt(e.widths.StringWidth(cluster), 1)
	advance := e.advanceOf(cluster, width)

	if e.wrap {
		e.index()
	}
	if e.col+width > e.width {
		if !e.autowrap {
			e.col = maxInt(e.width-width, 0)
			e.wrap = false
		} else {
			e.index()
		}
	}

	e.cells[e.row][e.col] = cluster
	e.taken[e.row][e.col] = false
	for offset := 1; offset < width && e.col+offset < e.width; offset++ {
		e.cells[e.row][e.col+offset] = ""
		e.taken[e.row][e.col+offset] = true
	}
	e.painted += width

	e.col += advance
	if e.col >= e.width {
		e.col = e.width - 1
		e.wrap = e.autowrap
	}
}

// advanceOf returns how far the cursor goes after a cluster of the given
// width, which is not always the width of it.
//
// The two are the same in every terminal that follows the emoji rules and
// not in the macOS Terminal, where a symbol with text presentation by
// default — a pagoda, a pagoda with a selector behind it — is drawn from
// the emoji font in two cells and leaves the cursor one further along. The
// glyph occupies what it occupies either way, so the cells it hides are the
// cells of it.
func (e *screenEmulator) advanceOf(cluster string, width int) int {
	if e.advanceOneCellPerEmoji && width > 1 && termwidth.EmojiLike(cluster) {
		return width - 1
	}

	return width
}

// index moves the cursor down a row, scrolling the screen when it is on
// the last one.
func (e *screenEmulator) index() {
	e.row++
	e.wrap = false
	if e.row < len(e.cells) {
		return
	}

	e.row = len(e.cells) - 1
	for row := range e.cells {
		copy(e.cells[row], e.cells[row+1])
		copy(e.taken[row], e.taken[row+1])
	}
	for col := range e.cells[e.row] {
		e.cells[e.row][col] = ""
		e.taken[e.row][col] = false
	}
}

// escape handles the byte after ESC.
func (e *screenEmulator) escape(r rune) {
	switch r {
	case '[':
		e.state = emuCSI
		e.params = e.params[:0]
		e.private = 0

		// A control character ends a sequence of its own: ESC ESC is Alt
		// and the second ESC starts the next one.
	case ']':
		e.state = emuString

	case 'P', 'X', '^', '_':
		e.state = emuString

	case '(', ')', '*', '+', '-', '.', '/', '#', '%', ' ':
		// A character set designation or a DEC line-size, which is one
		// more byte and changes nothing about the cells.
		e.state = emuText

	case '7':
		e.savedRow, e.savedCol = e.row, e.col
		e.state = emuText

	case '8':
		e.row, e.col = e.savedRow, e.savedCol
		e.wrap = false
		e.state = emuText

	case 'D':
		e.index()
		e.state = emuText

	case 'E':
		e.index()
		e.col = 0
		e.state = emuText

	case 'M':
		e.row = maxInt(e.row-1, 0)
		e.state = emuText

	case 'c':
		e.reset()
		e.state = emuText

	default:
		e.state = emuText
	}
}

// csi handles one byte of a control sequence.
func (e *screenEmulator) csi(r rune) {
	switch {
	case r >= 0x30 && r <= 0x3f:
		if r == '?' || r == '<' || r == '=' || r == '>' {
			if len(e.params) == 0 {
				e.private = byte(r)
			}
		}
		e.params = append(e.params, byte(r))

	case r >= 0x20 && r <= 0x2f:
		// An intermediate byte: nothing the renderer writes has one.

	case r >= 0x40 && r <= 0x7e:
		e.control(r)

	default:
		e.state = emuText
	}
}

// control applies a finished control sequence.
func (e *screenEmulator) control(final rune) {
	e.state = emuText
	e.wrap = false

	switch final {
	case 'A':
		e.row = maxInt(e.row-e.at(1, 1), 0)
	case 'B':
		e.row = minInt(e.row+e.at(1, 1), len(e.cells)-1)
	case 'C':
		e.col = minInt(e.col+e.at(1, 1), e.width-1)
	case 'D':
		e.col = maxInt(e.col-e.at(1, 1), 0)
	case 'E':
		e.row = minInt(e.row+e.at(1, 1), len(e.cells)-1)
		e.col = 0
	case 'F':
		e.row = maxInt(e.row-e.at(1, 1), 0)
		e.col = 0
	case 'G', '`':
		e.col = clampInt(e.at(1, 1)-1, 0, e.width-1)
	case 'd':
		e.row = clampInt(e.at(1, 1)-1, 0, len(e.cells)-1)
	case 'H', 'f':
		e.row = clampInt(e.at(1, 1)-1, 0, len(e.cells)-1)
		e.col = clampInt(e.at(2, 1)-1, 0, e.width-1)
	case 'J':
		e.eraseDisplay(e.at(1, 0))
	case 'K':
		e.eraseLine(e.at(1, 0))
	case 's':
		e.savedRow, e.savedCol = e.row, e.col
	case 'u':
		e.row, e.col = e.savedRow, e.savedCol
	case 'h', 'l':
		if e.private == '?' && e.modeNumber() == autoWrapMode {
			e.autowrap = final == 'h'
		}
	}

	// m (SGR), h and l (modes), r (scroll region), n (reports), c (device
	// attributes) and the private modes of the alternate screen and the
	// cursor change what a cell looks like and not which cell it is, so
	// this terminal keeps the text and drops everything else.
	_ = e.private
}

// at returns the nth parameter of the sequence, or fallback where the
// parameter was left out — which every terminal reads as its own default.
//
// The parameters are the bytes between the ESC [ and the final character,
// one field per semicolon, each of which may carry a private marker in
// front of it: ESC [ 35 G is the thirty-fifth column and ESC [ ? 7 l is the
// mode of the auto-wrap. Reading the fields as they were written is what
// tells the two apart, and it is what puts a two digit column where it
// belongs: read one byte at a time, the thirty-fifth column is the third.
func (e *screenEmulator) at(index int, fallback int) int {
	fields := strings.Split(string(e.params), ";")
	if index > len(fields) {
		return fallback
	}

	value, err := strconv.Atoi(strings.TrimLeft(fields[index-1], "?<=>"))
	if err != nil || value == 0 {
		return fallback
	}

	return value
}

// autoWrapMode is the number of DECAWM, the mode that says whether what is
// written past the last column of a row is wrapped onto the row below.
const autoWrapMode = 7

// modeNumber returns the number of the private mode a finished sequence
// sets, or 0 where it sets none.
//
// It is read out of the parameters as they were written, private marker and
// all, because a mode sequence names one mode: ESC [ ? 7 l is the mode of the
// auto-wrap and nothing else.
func (e *screenEmulator) modeNumber() int {
	fields := strings.Split(string(e.params), ";")

	number, err := strconv.Atoi(strings.TrimLeft(fields[0], "?<=>"))
	if err != nil {
		return 0
	}

	return number
}

// eraseDisplay clears the cells of the screen the mode names: from the
// cursor to the end of it, from the beginning of it to the cursor, or all
// of it.
func (e *screenEmulator) eraseDisplay(mode int) {
	switch mode {
	case 1:
		for row := 0; row <= e.row; row++ {
			last := e.width - 1
			if row == e.row {
				last = e.col
			}
			e.clear(row, 0, last)
		}

	case 2:
		for row := range e.cells {
			e.clear(row, 0, e.width-1)
		}

	default:
		e.clear(e.row, e.col, e.width-1)
		for row := e.row + 1; row < len(e.cells); row++ {
			e.clear(row, 0, e.width-1)
		}
	}
}

// eraseLine clears the cells of the row the mode names.
func (e *screenEmulator) eraseLine(mode int) {
	switch mode {
	case 1:
		e.clear(e.row, 0, e.col)
	case 2:
		e.clear(e.row, 0, e.width-1)
	default:
		e.clear(e.row, e.col, e.width-1)
	}
}

// clear empties the cells of one row between two columns.
func (e *screenEmulator) clear(row, from, to int) {
	if row < 0 || row >= len(e.cells) {
		return
	}
	for col := maxInt(from, 0); col <= to && col < e.width; col++ {
		e.cells[row][col] = ""
		e.taken[row][col] = false
	}
}

// stringSequence drops the text of an OSC, DCS, APC, PM or SOS sequence.
// Nothing the program writes into one is ever shown.
func (e *screenEmulator) stringSequence(r rune) {
	switch r {
	case 0x07:
		e.state = emuText
	case escapeByte:
		e.state = emuStringEscape
	}
}

// reset puts the terminal back the way it was found.
func (e *screenEmulator) reset() {
	for row := range e.cells {
		e.clear(row, 0, e.width-1)
	}
	e.row, e.col = 0, 0
	e.wrap = false
}

func clampInt(value, low, high int) int {
	if value < low {
		return low
	}
	if value > high {
		return high
	}

	return value
}

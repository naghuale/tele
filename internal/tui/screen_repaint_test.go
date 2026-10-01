package tui

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"telecli/internal/tui/termwidth"
	"telecli/internal/tui/theme"
)

// This file runs the real program against a terminal in memory and asks
// the one question the interface cannot ask itself: does the screen a user
// is looking at say what the program believes it drew?
//
// The complaint this answers is a list of chats whose rows came out twice
// — the name of a chat, then its own name again, and the preview of the
// chat below it hanging under a name that is not its own. The model was
// not at fault: its frames are exactly the height of the window and every
// row of them is exactly the width, in both of the rules of
// internal/tui/termwidth. What was at fault is that Bubble Tea repaints
// only the rows that changed since the last frame, and a row it decided
// to leave alone is a row it did not write. When the terminal and the
// program disagree about what is where, the rows the program believes it
// skipped are the rows that keep what was there before, and the screen is
// a frame with pieces of the one before it in it.
//
// So the test is the whole way round: it starts tea.NewProgram with the
// same options the composition root uses, gives it a terminal that keeps
// its cells, and after every key compares what the terminal holds with
// what the program drew.

// The keys of the walk in §8.1 and §8.3, as a terminal writes them.
const (
	keyDown  = "\x1b[B"
	keyUp    = "\x1b[A"
	keyEnter = "\r"
	keyEsc   = "\x1b"
	keyTab   = "\t"
)

// repaintSize is the size the screen is walked at: a wide screen, so
// both panes are drawn and the chat list has rows to double.
const (
	repaintWidth  = 120
	repaintHeight = 30
)

// repaintChats is the list the screen is drawn over.
//
// Every name carries a character the two width rules count differently and
// one they count the same: a hand with the emoji selector behind it is one
// column to the codepoint rule and two to the grapheme rule, a flag is two
// to both, and a name that holds them is a row the terminal has to place
// one cell at a time. A list of plain names would not exercise the edge
// where a row is exactly the width of the window.
func repaintChats(count int) []Chat {
	chats := make([]Chat, 0, count)
	for index := range count {
		number := index + 1
		chats = append(chats, Chat{
			ID:      int64(number),
			Title:   fmt.Sprintf("Chat %03d ✌️ about 中文", number),
			Preview: fmt.Sprintf("preview %03d 🇨🇳 прошивка вышла", number),
			Unread:  number % 7,
			Time:    "12:" + fmt.Sprintf("%02d", number%60),
		})
	}

	return chats
}

// frameLog is the newest frame the program has drawn and the update it
// was drawn from.
//
// The renderer of Bubble Tea writes a frame on a clock of its own rather
// than in the middle of a key press, so a test that looked at the terminal
// the moment a key was sent would be looking at the frame before it — the
// one that says "Chats" where the program had already drawn the chat it
// opened. A test that waited out a length of time instead would pass on a
// fast machine and fail on a slow one, which is the same test twice.
//
// So both sides of the comparison are numbered. Every update of the program
// draws exactly one frame, so the number of the frame says which update it
// belongs to, and a wait is for the cells of the terminal to be the cells
// of the frame that belongs to the key that was pressed. The clock of the
// renderer is then a thing the test is patient about rather than a thing it
// guesses at.
type frameLog struct {
	mu   sync.Mutex
	gen  int
	view string
}

// drawnFrom returns the newest frame drawn from an update later than gen,
// and whether the program has drawn one yet.
func (l *frameLog) drawnFrom(gen int) (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.gen <= gen {
		return "", false
	}

	return l.view, true
}

// newest returns the update the newest frame was drawn from.
func (l *frameLog) newest() int {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.gen
}

// recordedModel is the model of the program under test with a log on it.
//
// It is the same model, value for value: the wrapper only remembers what
// was drawn and counts the updates, so the screen the terminal ends up with
// is compared with the view of the state that produced it rather than with
// a second model a test kept in step by hand.
type recordedModel struct {
	Model
	log *frameLog
	gen int
}

func (m recordedModel) View() string {
	view := m.Model.View()

	m.log.mu.Lock()
	defer m.log.mu.Unlock()

	m.log.gen = m.gen
	m.log.view = view

	return view
}

func (m recordedModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.Model.Update(msg)

	return recordedModel{Model: next.(Model), log: m.log, gen: m.gen + 1}, cmd
}

// screenHarness is a program, a terminal and a way of waiting for one to
// catch up with the other.
type screenHarness struct {
	emulator *screenEmulator
	log      *frameLog
	program  *tea.Program
	input    *io.PipeWriter
	done     chan struct{}
	width    int
	height   int

	// held is the frame the terminal was last seen to hold, and it is what
	// a key that draws a frame of the very same bytes is settled by: the
	// renderer skips the write when nothing changed, so there is no write
	// to wait for, and the screen is the frame already.
	held string
}

// startProgram starts a program with the options the composition root
// uses, writing to a terminal in memory and reading keys from a pipe.
func startProgram(
	t *testing.T,
	model Model,
	widths termwidth.WidthModel,
	width, height int,
) *screenHarness {
	t.Helper()

	emulator := newScreenEmulator(width, height, widths)
	log := &frameLog{}
	reader, writer := io.Pipe()
	if debugRaw != nil {
		emulator = newScreenEmulator(width, height, widths)
		emulator.tap = debugRaw
	}

	program := tea.NewProgram(
		recordedModel{Model: model, log: log},
		tea.WithContext(context.Background()),
		tea.WithInput(reader),
		tea.WithOutput(emulator),
		tea.WithAltScreen(),
		tea.WithoutSignalHandler(),
		tea.WithoutCatchPanics(),
	)

	harness := &screenHarness{
		emulator: emulator,
		log:      log,
		program:  program,
		input:    writer,
		done:     make(chan struct{}),
		width:    width,
		height:   height,
	}

	go func() {
		defer close(harness.done)
		defer reader.Close() //nolint:errcheck
		defer writer.Close() //nolint:errcheck

		_, _ = program.Run()
	}()

	t.Cleanup(harness.stop)

	return harness
}

// stop ends the program and waits for it.
func (h *screenHarness) stop() {
	h.program.Quit()

	select {
	case <-h.done:
	case <-time.After(5 * time.Second):
	}
}

// key presses a key the way a terminal delivers it, waits for the frame it
// produced to reach the terminal, and returns that frame.
//
// The wait is the frame and not the time: the number the program has
// updated to is read before the key is written, and the wait ends when the
// cells of the terminal are the cells of the frame drawn from an update
// later than that number. A key that changes nothing on the screen answers
// with a frame the renderer never writes, and the wait ends at once
// because the terminal is already holding it — which is right, and is the
// one thing a wait for a write could not manage.
func (h *screenHarness) key(t *testing.T, label, keys string) string {
	t.Helper()

	before := h.log.newest()
	if _, err := io.WriteString(h.input, keys); err != nil {
		t.Fatalf("%s: send %q: %v", label, keys, err)
	}

	return h.awaitTheFrame(t, label, before)
}

// send puts a message into the program the way the resize handler does.
func (h *screenHarness) send(t *testing.T, msg tea.Msg) string {
	t.Helper()

	before := h.log.newest()
	h.program.Send(msg)

	return h.awaitTheFrame(t, "the size of the screen", before)
}

// repaint asks the program for the whole screen to be drawn again at the
// size the window already is, which is the message a change of chat asks
// for (repaint.go), and waits until the terminal has been written the
// frame it produced.
//
// It is a wait for a write rather than for a frame, because the frame is
// the one the terminal is already holding: a repaint of a screen that did
// not change is the same bytes again, and the only thing that tells it
// apart from the silence is that the cells were written once more.
func (h *screenHarness) repaint(t *testing.T, label string) string {
	t.Helper()

	before, written := h.log.newest(), h.emulator.writes()
	h.program.Send(tea.WindowSizeMsg{Width: h.width, Height: h.height})

	return h.awaitTheWrittenFrame(t, label, before, written)
}

// frameDeadline is how long the terminal is given to catch up with a frame
// the program has drawn. The renderer draws on a clock of a sixtieth of a
// second, so this is two hundred of them: a wait that is over sooner than
// this fails on a machine with a slow clock, which is the machine CI is.
const frameDeadline = 2 * time.Second

// framePoll is how often the screen is looked at again when the program
// has not written to the terminal. It is not how long the wait is: the wait
// ends when the screen is the frame, and this is how often that is asked.
const framePoll = time.Millisecond

// awaitTheFrame waits until the terminal holds the frame the program drew
// after the update it was reading the number of, and returns it.
//
// Two things settle it, and both of them are statements about the screen
// rather than about the time: either the cells of the terminal are the
// cells of the frame, or the frame is the very bytes the terminal was seen
// to hold a moment ago and the renderer had nothing to write. Anything else
// is the program ahead of the terminal, which is the state a test of this
// is waiting out.
func (h *screenHarness) awaitTheFrame(t *testing.T, label string, after int) string {
	t.Helper()

	deadline := wallClock().Add(frameDeadline)
	for {
		view, drawn := h.log.drawnFrom(after)
		switch {
		case drawn && (view == h.held || len(rowsThatDisagree(h.emulator.rows(), view)) == 0):
			h.held = view

			return view

		case wallClock().After(deadline):
			h.failOnTheRowsThatDisagree(t, label, after)
		}

		h.emulator.waitForAChange(time.Until(deadline), framePoll)
	}
}

// awaitTheWrittenFrame waits until the terminal has been written the frame
// the program drew after the update it was reading the number of, and
// returns it.
//
// It is awaitTheFrame plus the write: the frame has to have reached the
// terminal, not only to have been produced. A test that counts the cells a
// repaint wrote needs that difference, because a frame the renderer skips
// is a frame that costs the terminal nothing.
func (h *screenHarness) awaitTheWrittenFrame(
	t *testing.T,
	label string,
	after, written int,
) string {
	t.Helper()

	deadline := wallClock().Add(frameDeadline)
	for {
		view, drawn := h.log.drawnFrom(after)
		switch {
		case drawn &&
			h.emulator.writes() > written &&
			len(rowsThatDisagree(h.emulator.rows(), view)) == 0:
			h.held = view

			return view

		case wallClock().After(deadline):
			h.failOnTheRowsThatDisagree(t, label, after)
		}

		h.emulator.waitForAChange(time.Until(deadline), framePoll)
	}
}

// awaitAScreenPainted waits until the terminal has been given a whole
// screen of cells since from, and returns how many cells it was given.
//
// One whole screen is every cell of the window. A repaint writes all of
// them; a patch writes the rows that changed, which for one row of the
// chat list is three of thirty. So this is the other half of the same
// condition awaitTheFrame waits on: the screen is the frame *and* the
// terminal was given the cells of it.
//
// Counting cells is what tells the two apart where a frame of the very
// same bytes would not: the renderer skips a write when nothing changed,
// and only a repaint gets through.
func (h *screenHarness) wholeScreen() int {
	return h.width * h.height
}

func (h *screenHarness) awaitAScreenPainted(t *testing.T, label string, from int) int {
	t.Helper()

	whole := h.wholeScreen()
	deadline := wallClock().Add(frameDeadline)
	for {
		painted := h.emulator.paintedCells() - from
		if painted >= whole {
			return painted
		}

		if wallClock().After(deadline) {
			t.Fatalf(
				"%s: the terminal was given %d cells in %s, want a whole "+
					"screen (%d): the rows the program left alone are the "+
					"ones a terminal and a program can disagree about",
				label, painted, frameDeadline, whole,
			)
		}

		h.emulator.waitForAChange(time.Until(deadline), framePoll)
	}
}

// failOnTheRowsThatDisagree says what the terminal was showing and what
// the program had drawn, a few rows of it.
func (h *screenHarness) failOnTheRowsThatDisagree(
	t *testing.T,
	label string,
	after int,
) {
	t.Helper()

	view, drawn := h.log.drawnFrom(after)
	if !drawn {
		t.Fatalf("%s: the program drew no frame in %s", label, frameDeadline)
	}

	rows := rowsThatDisagree(h.emulator.rows(), view)
	for _, row := range rows[:minInt(len(rows), 8)] {
		t.Logf(
			"row %d\n  terminal: %q\n  program:  %q",
			row.row+1, row.have, row.want,
		)
	}

	t.Fatalf(
		"%s: the terminal was not the frame of the program in %s, "+
			"and %d of its %d rows do not agree",
		label, frameDeadline, len(rows), len(h.emulator.rows()),
	)
}

// rowMismatch is one row where the terminal and the program differ.
type rowMismatch struct {
	row  int
	have string
	want string
}

// rowsThatDisagree compares the screen with a frame of the program, row by
// row.
//
// The rows are compared on their text and not on their escape sequences:
// what a user is looking at is the words and where they are, and a colour
// is a claim about a cell rather than about the row.
//
// The right-hand spaces are dropped from both sides because they are not
// what is drawn, they are what a row of a fixed width is padded with, and
// comparing them would turn a difference in how a row is padded into a
// disagreement about the row.
func rowsThatDisagree(screen []string, view string) []rowMismatch {
	lines := strings.Split(view, "\n")
	if len(lines) != len(screen) {
		return []rowMismatch{{
			row:  minInt(len(screen), len(lines)),
			have: strconv.Itoa(len(screen)) + " rows on the terminal",
			want: strconv.Itoa(len(lines)) + " rows in the frame",
		}}
	}

	var mismatches []rowMismatch
	for row, line := range lines {
		have := strings.TrimRight(screen[row], " ")
		want := strings.TrimRight(plain(line), " ")
		if have == want {
			continue
		}

		mismatches = append(mismatches, rowMismatch{row: row, have: have, want: want})
	}

	return mismatches
}

// assertScreenIsTheFrame compares the terminal with the frame of the
// program, row by row.
func assertScreenIsTheFrame(t *testing.T, emulator *screenEmulator, view string) {
	t.Helper()

	for _, row := range rowsThatDisagree(emulator.rows(), view) {
		t.Errorf(
			"row %d of the screen is not the row the program drew\n"+
				"  terminal: %q\n  program:  %q",
			row.row+1, row.have, row.want,
		)
	}
}

// repaintModel is the screen the walk happens on: a source-less model
// with a list of two hundred chats in it, so that no command ever runs
// and the only messages the program sees are the keys and the size.
func repaintModel(t *testing.T, mode termwidth.Mode) Model {
	t.Helper()

	model, err := NewModelWithDependencies(context.Background(), Dependencies{
		MessageSubmitter: &programSubmitter{},
		Theme:            theme.DefaultTheme().ForProfile(theme.ProfileTrueColor),
		ColorProfile:     theme.ProfileTrueColor,
		WidthMode:        mode,
	})
	if err != nil {
		t.Fatalf("NewModelWithDependencies: %v", err)
	}

	model.chats = repaintChats(repaintChatCount)
	model.chatsState = loadStateLoaded
	model.selectedChat = 0

	return model
}

// repaintChatCount is how many chats the list holds: more than fit on the
// screen many times over, so that walking the selection scrolls the list
// rather than walking to its end.
const repaintChatCount = 200

// repaintKeyNames names every key of the walk, so that a failure says
// which key of it the screen went wrong on. Two thirds of the walk is
// the same key twice over, and a report that says only "the screen" is a
// report about a bug nobody can find again.
var repaintKeyNames = func() []string {
	names := make([]string, 0, 68)
	for range 40 {
		names = append(names, "down")
	}
	for range 15 {
		names = append(names, "up")
	}
	names = append(names, "enter", "esc", "tab")
	for range 10 {
		names = append(names, "down")
	}

	return names
}()

// The walk the owner made when the rows doubled: down the list, back up
// it, into a chat, out of it, and down the list again, with the focus
// moved in between so that the focus rule moves too.
func repaintWalk() []string {
	keys := make([]string, 0, 68)
	for range 40 {
		keys = append(keys, keyDown)
	}
	for range 15 {
		keys = append(keys, keyUp)
	}

	keys = append(keys, keyEnter, keyEsc, keyTab)
	for range 10 {
		keys = append(keys, keyDown)
	}

	return keys
}

// TestTheProgramDrawsWhatItThinksItDraws walks the chat list and the
// conversation with a real program in front of a real terminal, and
// compares the two after every key.
//
// It is the whole claim of the change: what the terminal is showing is
// what the program drew. Everything here is a consequence — a list of two
// hundred chats whose names are two columns wide, an alternate screen,
// and a renderer that repaints only what changed.
func TestTheProgramDrawsWhatItThinksItDraws(t *testing.T) {
	for name, mode := range map[string]termwidth.Mode{
		"grapheme":  termwidth.ModeGrapheme,
		"codepoint": termwidth.ModeCodepoint,
	} {
		t.Run(name, func(t *testing.T) {
			widths := termwidth.Unmeasured(mode)
			harness := startProgram(
				t,
				repaintModel(t, mode),
				widths,
				repaintWidth,
				repaintHeight,
			)

			view := harness.send(t, tea.WindowSizeMsg{
				Width:  repaintWidth,
				Height: repaintHeight,
			})
			assertScreenIsTheFrame(t, harness.emulator, view)

			for step, keys := range repaintWalk() {
				view := harness.key(
					t,
					fmt.Sprintf("key %d (%s)", step+1, repaintKeyNames[step]),
					keys,
				)
				assertScreenIsTheFrame(t, harness.emulator, view)
			}
		})
	}
}

// The rows of the screen are the width of the window, and that is what
// makes the right edge of a row the place where a frame is either right
// or a line out: a row of exactly the width of the window is a row the
// terminal has to decide about.
//
// The test above says the two agree; this one says the frames are the
// shape the terminal can be asked about, so that a test which passes is a
// test about a screen and not about a screen that happens to be narrow.
func TestEveryFrameIsTheSizeOfTheWindow(t *testing.T) {
	mode := termwidth.ModeGrapheme
	widths := termwidth.Unmeasured(mode)
	harness := startProgram(
		t, repaintModel(t, mode), widths, repaintWidth, repaintHeight,
	)
	harness.send(t, tea.WindowSizeMsg{Width: repaintWidth, Height: repaintHeight})

	for step, keys := range repaintWalk() {
		view := harness.key(t, repaintKeyNames[step], keys)

		lines := strings.Split(view, "\n")
		if len(lines) != repaintHeight {
			t.Fatalf("the frame is %d rows, want %d", len(lines), repaintHeight)
		}
		for row, line := range lines {
			if got := widths.StringWidth(line); got != repaintWidth {
				t.Errorf(
					"row %d is %d columns, want %d: %q",
					row+1, got, repaintWidth, line,
				)
			}
		}
	}
}

// debugRaw is where the terminal of a harness is tapped, when a test has to
// read the bytes rather than the cells.
var debugRaw io.Writer

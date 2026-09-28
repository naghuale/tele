package tui

import (
	"context"
	"fmt"
	"io"
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

// frameLog is what the program drew, and when.
//
// The renderer writes a frame on a clock rather than in the middle of a
// key press, so a test that compared the terminal with the view the moment
// the key was sent would be comparing it with a frame that has not been
// drawn yet. The log is what both sides of the comparison are timed
// against: the view the program produced and the write that put it on the
// screen.
type frameLog struct {
	mu     sync.Mutex
	frames []string
}

func (l *frameLog) add(view string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.frames = append(l.frames, view)
}

func (l *frameLog) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()

	return len(l.frames)
}

func (l *frameLog) last() string {
	l.mu.Lock()
	defer l.mu.Unlock()

	if len(l.frames) == 0 {
		return ""
	}

	return l.frames[len(l.frames)-1]
}

// recordedModel is the model of the program under test with a log on it.
//
// It is the same model, value for value: the wrapper only remembers what
// was drawn, so the screen the terminal ends up with is compared with the
// view of the state that produced it rather than with a second model a
// test kept in step by hand.
type recordedModel struct {
	Model
	log *frameLog
}

func (m recordedModel) View() string {
	view := m.Model.View()
	m.log.add(view)

	return view
}

func (m recordedModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.Model.Update(msg)

	return recordedModel{Model: next.(Model), log: m.log}, cmd
}

// screenHarness is a program, a terminal and a way of waiting for one to
// catch up with the other.
type screenHarness struct {
	emulator *screenEmulator
	log      *frameLog
	program  *tea.Program
	input    *io.PipeWriter
	done     chan struct{}
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

// key presses a key the way a terminal delivers it, waits for the frame
// it produced to reach the terminal, and returns the view of the program.
//
// The renderer draws on a clock rather than in the middle of a key press,
// so the wait is for the terminal to go quiet rather than for a write: a
// key that changes nothing on the screen is a key the program answers with
// a frame the renderer never writes, and a test that waited for the write
// would sit on it until it timed out. The second half of the wait is what
// keeps that from being a race: a frame is a frame only after the
// renderer has had a whole frame's worth of clock to write it.
func (h *screenHarness) key(t *testing.T, label, keys string) string {
	t.Helper()

	drawn := h.log.count()
	if _, err := io.WriteString(h.input, keys); err != nil {
		t.Fatalf("%s: send %q: %v", label, keys, err)
	}

	waitFor(t, label+": a frame", func() bool {
		return h.log.count() > drawn
	})
	h.waitForTheFrame(t, label)

	return h.log.last()
}

// send puts a message into the program the way the resize handler does.
func (h *screenHarness) send(t *testing.T, msg tea.Msg) {
	t.Helper()

	drawn := h.log.count()
	h.program.Send(msg)
	waitFor(t, "the size of the screen: a frame", func() bool {
		return h.log.count() > drawn
	})
	h.waitForTheFrame(t, "the size of the screen")
}

// screenQuiet is how long the program has to leave the terminal alone
// before the frame is taken to be the one on the screen. It is several
// frames of the renderer's clock, so that a frame which is on its way is
// waited for rather than raced.
const screenQuiet = 60 * time.Millisecond

// waitForTheFrame waits until the frame the program has produced has been
// written to the terminal, or until there is nothing to write.
//
// The two halves are the same statement from either side: the frame has to
// have been given a whole frame's worth of the renderer's clock to be
// written, and the terminal has to have been left alone for that long
// since. A key that changes nothing on the screen produces a frame the
// renderer never writes, and a wait that asked for a write would sit on
// it until it timed out.
func (h *screenHarness) waitForTheFrame(t *testing.T, label string) {
	t.Helper()

	produced := time.Now()
	waitFor(t, label+": the frame on the screen", func() bool {
		return time.Since(produced) >= screenQuiet &&
			h.emulator.idleFor(screenQuiet)
	})
}

// waitFor waits for a condition, and says what it was waiting for when it
// never came.
func waitFor(t *testing.T, what string, done func() bool) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("the program produced no %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// assertScreenIsTheFrame compares the terminal with the frame of the
// program, row by row.
//
// The rows are compared on their text and not on their escape sequences:
// what a user is looking at is the words and where they are, and a colour
// is a claim about a cell rather than about the row.
//
// The right-hand spaces are dropped from both sides because they are not
// what is drawn, they are what a row of a fixed width is padded with, and
// comparing them would turn a difference in how a row is padded into a
// failure about the row.
func assertScreenIsTheFrame(
	t *testing.T,
	emulator *screenEmulator,
	view string,
	width, height int,
) {
	t.Helper()

	lines := strings.Split(view, "\n")
	if len(lines) != height {
		t.Fatalf(
			"the frame is %d rows and the window is %d: %s",
			len(lines), height, strings.Join(lines, "\n"),
		)
	}

	for row := range height {
		want := strings.TrimRight(plain(lines[row]), " ")
		got := strings.TrimRight(emulator.line(row), " ")

		if got == want {
			continue
		}

		t.Errorf(
			"row %d of the screen is not the row the program drew\n"+
				"  terminal: %q\n  program:  %q",
			row+1, got, want,
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

			harness.send(t, tea.WindowSizeMsg{
				Width:  repaintWidth,
				Height: repaintHeight,
			})
			assertScreenIsTheFrame(
				t, harness.emulator, harness.log.last(), repaintWidth, repaintHeight,
			)

			for step, keys := range repaintWalk() {
				view := harness.key(
					t,
					fmt.Sprintf("key %d (%s)", step+1, repaintKeyNames[step]),
					keys,
				)
				assertScreenIsTheFrame(
					t, harness.emulator, view, repaintWidth, repaintHeight,
				)
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

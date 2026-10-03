package tui

import (
	"fmt"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"telecli/internal/tui/termwidth"
)

// This file is the claim that the screen is drawn again whole, and the two
// ways of being wrong about it.
//
// The rows of the chat list came out doubled on a real account while the
// user walked the list and came back out of a chat. The frames were right:
// the height of the window, every row of them the width, in both of the
// rules of internal/tui/termwidth. What was wrong was that Bubble Tea
// repaints the rows that changed and leaves the rest, and a row it leaves
// alone is a row it did not write.
//
// So the answer is a full repaint at the three moments where the answer to
// "what is on the screen" moves: the selection moves to another chat, a
// chat is opened, and a chat is left. The first half of this file says
// the program asks for it, and the second half says the ask reaches the
// terminal.

// repaintCommands runs the commands a key produced and returns the window
// sizes among them, which is how a Bubble Tea v1 program asks for the
// whole screen to be drawn again (see repaint.go).
func repaintCommands(cmd tea.Cmd) []tea.WindowSizeMsg {
	if cmd == nil {
		return nil
	}

	switch msg := cmd().(type) {
	case tea.WindowSizeMsg:
		return []tea.WindowSizeMsg{msg}

	case tea.BatchMsg:
		var sizes []tea.WindowSizeMsg
		for _, batched := range msg {
			sizes = append(sizes, repaintCommands(batched)...)
		}

		return sizes

	default:
		return nil
	}
}

// assertRepainted fails unless a key asked for the whole screen at the
// size the model has.
func assertRepainted(t *testing.T, m Model, cmd tea.Cmd, label string) {
	t.Helper()

	sizes := repaintCommands(cmd)
	if len(sizes) == 0 {
		t.Fatalf("%s did not ask for the screen to be drawn again", label)
	}
	for _, size := range sizes {
		if size.Width != m.width || size.Height != m.height {
			t.Fatalf(
				"%s asked for a %dx%d screen, want %dx%d",
				label, size.Width, size.Height, m.width, m.height,
			)
		}
	}
}

// assertNotRepainted fails when a key asked for the screen to be drawn
// again over nothing.
func assertNotRepainted(t *testing.T, cmd tea.Cmd, label string) {
	t.Helper()

	if sizes := repaintCommands(cmd); len(sizes) != 0 {
		t.Fatalf("%s asked for a repaint over nothing: %v", label, sizes)
	}
}

// Moving the selection to another chat is the moment the list and the
// conversation beside it both change what they hold, and it is the moment
// a row the program believes it left alone has the most chance of being a
// row that is not what the program thinks.
func TestMovingTheSelectionDrawsTheScreenAgain(t *testing.T) {
	m := sizedModel(t, 120, 30)

	_, cmd := updateModel(t, m, press(tea.KeyDown))
	assertRepainted(t, m, cmd, "↓ in the chat list")

	// A key that does not move the selection changes nothing, and asking
	// for a whole screen for nothing is the cost this must not pay.
	_, again := updateModel(t, m, pressRunes("g"))
	assertNotRepainted(t, again, "g on the chat that is already selected")
}

func TestOpeningAChatDrawsTheScreenAgain(t *testing.T) {
	m := sizedModel(t, 120, 30)

	_, cmd := updateModel(t, m, press(tea.KeyEnter))
	assertRepainted(t, m, cmd, "Enter in the chat list")
}

func TestLeavingAChatDrawsTheScreenAgain(t *testing.T) {
	m := openedModel(t, 60, 30)

	// A narrow screen has one pane, so the first Esc is the composer
	// handing the keys to the timeline and the second is the timeline
	// handing them to the list — which is the one that leaves the
	// conversation.
	m, _ = updateModel(t, m, press(tea.KeyEsc))
	if m.screen != ScreenConversation {
		t.Fatalf("screen = %v, want the conversation", m.screen)
	}

	m, cmd := updateModel(t, m, press(tea.KeyEsc))
	if m.screen != ScreenChats {
		t.Fatalf("screen = %v, want the chat list", m.screen)
	}
	assertRepainted(t, m, cmd, "Esc out of the conversation")
}

// The ask is one of the words of the model and not a library call the
// views happen to make: a screen the model has never been sized for has
// nothing to redraw, and a size of zero is one the renderer would use to
// throw the whole frame away.
func TestAModelWithoutASizeHasNothingToRepaint(t *testing.T) {
	assertNotRepainted(t, repaintCmd(NewModel()), "a model with no size")
}

// The ask has to reach the terminal and not stop at the model. A program
// that asked for a repaint and painted a handful of rows is a program that
// asked and was not heard, and a screen that is not the frame is exactly
// what this change is about.
//
// One whole screen is every cell of the window. A repaint writes all of
// them; a patch writes the rows that changed, which for one row of the
// chat list is three of thirty.
func TestAChangeOfChatIsFollowedByAWholeScreenOfCells(t *testing.T) {
	t.Run("the selection moves to another chat", func(t *testing.T) {
		harness := sizedScreen(t, repaintWidth, repaintHeight)

		for step, keys := range []string{keyDown, keyDown, keyUp, keyDown} {
			before := harness.emulator.paintedCells()
			harness.key(t, fmt.Sprintf("key %d", step+1), keys)
			harness.awaitAScreenPainted(t, fmt.Sprintf("key %d", step+1), before)
		}
	})

	t.Run("a chat is opened", func(t *testing.T) {
		harness := sizedScreen(t, repaintWidth, repaintHeight)

		before := harness.emulator.paintedCells()
		harness.key(t, "enter", keyEnter)
		harness.awaitAScreenPainted(t, "enter", before)
	})

	// A conversation is left only on a single-pane screen: a screen with
	// two panes puts the chat list next to the conversation rather than
	// behind it, and there is nothing for Esc to go back to (§8.5).
	t.Run("a conversation is left", func(t *testing.T) {
		harness := sizedScreen(t, 60, 30)
		harness.key(t, "enter", keyEnter)
		harness.key(t, "esc to the timeline", keyEsc)

		before := harness.emulator.paintedCells()
		harness.key(t, "esc out of the conversation", keyEsc)
		harness.awaitAScreenPainted(t, "esc out of the conversation", before)
	})
}

// sizedScreen is a program of the given size with a list of chats in it and
// a terminal in memory in front of it, sized and drawn.
func sizedScreen(t *testing.T, width, height int) *screenHarness {
	t.Helper()

	harness := startProgram(
		t,
		repaintModel(t, termwidth.ModeGrapheme),
		termwidth.Unmeasured(termwidth.ModeGrapheme),
		width, height,
	)
	harness.send(t, tea.WindowSizeMsg{Width: width, Height: height})

	return harness
}

// The half of the chain above that is the renderer's: a repaint is a
// WindowSizeMsg of the size the window already is, and it is written to
// the terminal in full rather than in the rows that changed.
//
// It is a test of its own so that a failure of the one above says which
// half is at fault: the program asked and the terminal was not given
// anything, or the program never asked.
//
// The claim is in the wait and nowhere else: the terminal has to be given
// one write over every cell of the window, and the test fails there with
// the number of cells when it is not (screen_repaint_test.go,
// awaitTheWrittenFrame). The cells used to be counted by the test at the
// moment the screen settled, and that moment is not the repaint's on a slow
// machine: it is the first write after the ask, and that write is sometimes
// a patch of somebody else's frame (CI, 03.10: 960 cells of 3600).
func TestARepaintIsWrittenOverEveryCellOfTheWindow(t *testing.T) {
	harness := sizedScreen(t, repaintWidth, repaintHeight)

	// The repaint is of a screen that really did change: a repaint of a
	// screen that did not is the one case a renderer satisfies without
	// writing anything, and the one case that says nothing about a chat
	// changing.
	harness.key(t, "down", keyDown)
	harness.repaint(t, "the repaint after ↓")
}

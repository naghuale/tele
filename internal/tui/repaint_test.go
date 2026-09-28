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
func TestTheTerminalIsDrawnOverAgainWhenTheChatChanges(t *testing.T) {
	widths := termwidth.Unmeasured(termwidth.ModeGrapheme)
	harness := startProgram(
		t, repaintModel(t, termwidth.ModeGrapheme), widths,
		repaintWidth, repaintHeight,
	)
	harness.send(t, tea.WindowSizeMsg{
		Width:  repaintWidth,
		Height: repaintHeight,
	})

	screen := repaintWidth * repaintHeight

	// Walking the list, opening a chat and moving the focus afterwards:
	// the three moments repaint.go names, and the keys around them that
	// name no chat at all. The keys of the timeline move the cursor inside
	// a conversation, which changes no row of the list.
	steps := []struct {
		label string
		keys  string
	}{
		{"down", keyDown},
		{"down", keyDown},
		{"down", keyDown},
		{"up", keyUp},
		{"enter", keyEnter},
		{"esc", keyEsc},
		{"tab", keyTab},
	}

	for step, keys := range steps {
		before := harness.emulator.paintedCells()
		harness.key(t, fmt.Sprintf("key %d (%s)", step+1, keys.label), keys.keys)
		drawn := harness.emulator.paintedCells() - before

		if !repaintNeeded(keys.keys) {
			continue
		}
		if drawn < screen {
			t.Fatalf(
				"key %d (%s) painted %d cells, want a whole screen (%d): "+
					"the rows the program left alone are the ones a "+
					"terminal and a program can disagree about",
				step+1, keys.label, drawn, screen,
			)
		}
	}
}

// repaintNeeded says which keys are the ones the screen has to be drawn
// again for: the selection moving to another chat, and the chat being
// opened. The keys of the timeline move no chat and say nothing about
// which one is selected.
func repaintNeeded(keys string) bool {
	switch keys {
	case keyDown, keyEnter:
		return true
	}

	return false
}

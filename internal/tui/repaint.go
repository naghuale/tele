package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// This file is the full repaint.
//
// Bubble Tea v1 repaints the rows of a frame that changed since the last
// one and leaves the rest of the screen alone. That is the right thing to
// do between two frames of a list whose cursor moved, and it is a
// promise about the screen: a row it leaves alone is a row it did not
// write, and the row on the terminal is whatever was there before.
//
// A promise the terminal does not keep is a screen that is not the frame.
// The rows of the chat list came out doubled on a real account — the name
// of a chat, then its own name again, and the preview of the chat below
// it hanging under a name that is not its own — and the model was not at
// fault: its frames are the height of the window and every row of them is
// the width, in both of the rules of internal/tui/termwidth. The frames
// and the terminal were handed the same bytes and came out with different
// screens, and a program in front of a terminal in memory drew the same
// frame over the same walk without a single row out of place.
//
// So the frame is not the thing that can be made safer, and the answer is
// to stop trusting the record of the last frame: whenever the selection
// moves to another chat, or a chat is opened, or one is left, the whole
// screen is drawn again.
//
// A window size message is how a Bubble Tea v1 program asks for that.
// The renderer drops what it remembers of the last frame on one, so the
// next frame writes every row of it, and it moves the cursor rather than
// erasing anything — unlike a clear, which blanks the screen first and
// leaves a flicker on every key press. The size it carries is the size
// the program already has, and the model answers it by laying the screen
// out again at that size, which is a no-op: the point is the side effect
// in the renderer, not anything the model draws differently.
//
// It costs a full frame on a key press, which is the cheapest moment
// there is: a key press is a frame the user asked for, and the frame
// that replaces it is the same one.

// repaintCmd asks the program to draw the whole screen again.
func repaintCmd(m Model) tea.Cmd {
	if m.width < 1 || m.height < 1 {
		// A screen the model has not been sized for has nothing to
		// repaint, and a size of zero is one the renderer would use to
		// throw the whole frame away.
		return nil
	}

	return func() tea.Msg {
		return tea.WindowSizeMsg{Width: m.width, Height: m.height}
	}
}

// withRepaint adds the full repaint to a command the model returns.
func withRepaint(m Model, cmd tea.Cmd) tea.Cmd {
	return tea.Batch(cmd, repaintCmd(m))
}

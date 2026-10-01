package tui

import (
	"context"
	"io"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"telecli/internal/tui/termwidth"
)

// newProgram creates a Bubble Tea program with the options shared by
// the main TUI and authorization prompts.
func newProgram(model Model) *tea.Program {
	return tea.NewProgram(
		model,
		tea.WithAltScreen(),
	)
}

func newProgramWithContext(
	ctx context.Context,
	model Model,
	extra ...tea.ProgramOption,
) *tea.Program {
	options := []tea.ProgramOption{tea.WithAltScreen()}
	if ctx != nil {
		options = append(options, tea.WithContext(ctx))
	}
	options = append(options, extra...)

	return tea.NewProgram(model, options...)
}

// Run starts the TUI in mock-only mode.
//
// It is equivalent to RunWithSource(nil).
func Run() error {
	return RunWithSource(nil)
}

// RunWithSource starts the TUI and loads chats and history from source.
//
// A nil source keeps the deterministic mock-only behavior.
func RunWithSource(source ChatSource) error {
	model := NewModel()
	if source != nil {
		model = NewModelWithSource(source)
	}

	// The mock-only program draws the same screen as the one with
	// Telegram behind it, so it counts columns the same way and has the
	// same problem when it does not.
	model.widths = measuredWidths()

	program := newProgram(model)

	_, err := owningTheScreen(os.Stdout, program.Run)
	return err
}

// RunWithDependencies starts the program with the dependencies the
// composition root resolved.
//
// The output is built here and given to two things: the program writes its
// frames through it, and the model copies through it. One writer and one
// lock around it, because a copy written from a key press while Bubble Tea
// is painting a frame lands inside that frame's escape sequence, and a
// terminal shows the rest of a sequence as text in the middle of a
// conversation.
func RunWithDependencies(
	ctx context.Context,
	deps Dependencies,
) error {
	output := programOutput()

	if deps.WidthMeasured == nil {
		measured := measureTerminalWidths(deps.WidthMode)
		deps.WidthMeasured = &measured
	}

	model, err := NewModelWithDependencies(ctx, deps)
	if err != nil {
		return err
	}
	model.clipboard = output

	options := []tea.ProgramOption{tea.WithOutput(output)}
	if input := inputWithPending(deps.WidthMeasured); input != nil {
		options = append(options, tea.WithInput(input))
	}

	program := newProgramWithContext(ctx, model, options...)

	// The modes of the terminal go through the same output the frames go
	// through: one writer, one lock, so a mode cannot land in the middle of
	// a frame.
	_, err = owningTheScreen(output, program.Run)
	return err
}

// inputWithPending returns the program's input when the measurement read
// something that was not an answer, and nothing when it did not.
//
// A key pressed in the window between the program starting and the terminal
// answering is a key press the user made to this program, and the
// measurement is the only thing that read it. Handing it back is the whole
// reason the measurement reports what it read instead of only what it
// understood: a byte eaten by a question about column widths is a key
// press the user will press again, and the second one is the one that
// works.
func inputWithPending(measured *termwidth.Measurement) io.Reader {
	if measured == nil || len(measured.Pending) == 0 {
		return nil
	}

	return &pendingInput{File: os.Stdin, pending: measured.Pending}
}

// pendingInput is the terminal with the bytes the measurement read in front
// of it.
//
// It embeds the file rather than wrapping a reader because the program has
// to be able to put the terminal into raw mode and take it out again: an
// input that is not a terminal is read line by line, and a user typing
// into telecli would have to press Enter after every letter.
type pendingInput struct {
	*os.File

	pending []byte
}

// Read gives the bytes the measurement read before the terminal anything
// else.
func (i *pendingInput) Read(buffer []byte) (int, error) {
	if len(i.pending) == 0 || len(buffer) == 0 {
		return i.File.Read(buffer)
	}

	read := copy(buffer, i.pending)
	i.pending = i.pending[read:]

	return read, nil
}

// measureTerminalWidths asks the terminal how it draws, and returns what
// it said.
//
// It runs before the program exists, and it is over in a fraction of a
// second: it asks the standard question about the cursor position once per
// doubtful character, erases the line it wrote on, and puts the terminal
// back the way it found it. A terminal that is not there, or that does not
// answer, gives an empty measurement and the rule of a terminal nobody
// could ask.
//
// A configured rule is not measured: it was asked for by name, and the
// measurement would only be a second opinion about it.
func measureTerminalWidths(mode termwidth.Mode) termwidth.Measurement {
	if mode != termwidth.ModeAuto {
		return termwidth.Measurement{}
	}

	terminal, err := termwidth.NewTerminal(os.Stdin, os.Stdout)
	if err != nil {
		return termwidth.Measurement{}
	}

	return termwidth.Measure(terminal, termwidth.MeasureBudget)
}

// measuredWidths is the model a program without a resolved configuration
// draws with: the rule this terminal was measured into.
func measuredWidths() termwidth.WidthModel {
	measured := measureTerminalWidths(termwidth.ModeAuto)
	model, _ := termwidth.Select(termwidth.ModeAuto, measured)

	return model
}

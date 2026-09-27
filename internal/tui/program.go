package tui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"
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

	program := newProgram(model)

	_, err := program.Run()
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

	model, err := NewModelWithDependencies(ctx, deps)
	if err != nil {
		return err
	}
	model.clipboard = output

	program := newProgramWithContext(ctx, model, tea.WithOutput(output))

	_, err = program.Run()
	return err
}

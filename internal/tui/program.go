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
) *tea.Program {
	options := []tea.ProgramOption{tea.WithAltScreen()}
	if ctx != nil {
		options = append(options, tea.WithContext(ctx))
	}
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

func RunWithDependencies(
	ctx context.Context,
	deps Dependencies,
) error {
	model, err := NewModelWithDependencies(ctx, deps)
	if err != nil {
		return err
	}
	program := newProgramWithContext(ctx, model)
	_, err = program.Run()
	return err
}

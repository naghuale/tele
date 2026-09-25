package tui

import (
	"errors"
	"fmt"
	"strings"
)

// ErrAuthCanceled is returned by RunAuth when the user cancels the
// prompt via Esc or Ctrl+C.
//
// It is distinct from ("", nil), which would be ambiguous with an
// empty submission.
var ErrAuthCanceled = errors.New("tui: authorization canceled")

func (m Model) authDisplayValue() string {
	if m.authPrompt != AuthPromptPassword {
		return string(m.composer)
	}
	return strings.Repeat("•", len(m.composer))
}

func (m Model) viewAuth() string {
	width := minInt(maxInt(m.width, minWidth), 60)

	var b strings.Builder
	b.WriteString("telecli — Sign in\n")
	b.WriteString(strings.Repeat("-", width))
	b.WriteString("\n")

	title := m.authPrompt.Title()
	if title == "" {
		title = "Input"
	}

	b.WriteString(title)
	b.WriteString(": ")
	b.WriteString(m.authDisplayValue())
	b.WriteString("_\n")

	b.WriteString(strings.Repeat("-", width))
	b.WriteString("\n")
	b.WriteString("Enter submit  Esc/Ctrl+C cancel\n")
	return b.String()
}

// RunAuth launches a terminal prompt for a single auth input.
//
// Returns ErrAuthCanceled when the user cancels. Returns ("", nil) only
// for an explicit empty submission.
func RunAuth(prompt AuthPromptKind) (string, error) {
	model := NewModel()
	model.screen = ScreenAuth
	model.authPrompt = prompt

	program := newProgram(model)
	finalModel, err := program.Run()
	if err != nil {
		return "", err
	}
	m, ok := finalModel.(Model)
	if !ok {
		return "", fmt.Errorf("tui: unexpected final model %T", finalModel)
	}
	if m.authCanceled {
		return "", ErrAuthCanceled
	}

	value := string(m.composer)
	m.composer = nil

	return value, nil
}

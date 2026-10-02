package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestIsUp(t *testing.T) {
	if !isUp(press(tea.KeyUp)) {
		t.Fatal("KeyUp must be up")
	}
	if !isUp(pressRunes("k")) {
		t.Fatal("k must be up")
	}
	if isUp(pressRunes("j")) {
		t.Fatal("j must not be up")
	}
	if isUp(press(tea.KeyDown)) {
		t.Fatal("KeyDown must not be up")
	}
}

func TestIsDown(t *testing.T) {
	if !isDown(press(tea.KeyDown)) {
		t.Fatal("KeyDown must be down")
	}
	if !isDown(pressRunes("j")) {
		t.Fatal("j must be down")
	}
	if isDown(pressRunes("k")) {
		t.Fatal("k must not be down")
	}
}

func TestIsFirst(t *testing.T) {
	if !isFirst(press(tea.KeyHome)) {
		t.Fatal("KeyHome must be first")
	}
	if !isFirst(pressRunes("g")) {
		t.Fatal("g must be first")
	}
	if isFirst(pressRunes("G")) {
		t.Fatal("G must not be first")
	}
}

func TestIsLast(t *testing.T) {
	if !isLast(press(tea.KeyEnd)) {
		t.Fatal("KeyEnd must be last")
	}
	if !isLast(pressRunes("G")) {
		t.Fatal("G must be last")
	}
	if isLast(pressRunes("g")) {
		t.Fatal("g must not be last")
	}
}

func TestIsTabAndShiftTab(t *testing.T) {
	if !isTab(press(tea.KeyTab)) {
		t.Fatal("KeyTab must be tab")
	}
	if isTab(press(tea.KeyShiftTab)) {
		t.Fatal("KeyShiftTab must not be tab")
	}
	if !isShiftTab(press(tea.KeyShiftTab)) {
		t.Fatal("KeyShiftTab must be shift-tab")
	}
	if isShiftTab(press(tea.KeyTab)) {
		t.Fatal("KeyTab must not be shift-tab")
	}
}

// A terminal that has no name for Shift+Tab sends the sequence `ESC [ Z`,
// and Bubble Tea passes on whatever it did not recognise as runes. Both
// forms walk the focus backwards, or the key works on one terminal and does
// nothing on the next.
func TestShiftTabIsTheKeyAndTheSequence(t *testing.T) {
	sequence := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(shiftTabSequence)}

	if !isShiftTab(sequence) {
		t.Fatalf("%q must be shift-tab", shiftTabSequence)
	}
	if isTab(sequence) {
		t.Fatalf("%q must not be tab", shiftTabSequence)
	}
	if isShiftTab(pressRunes("q")) {
		t.Fatal("a letter must not be shift-tab")
	}
}

func TestIsClearComposer(t *testing.T) {
	if !isClearComposer(press(tea.KeyCtrlU)) {
		t.Fatal("KeyCtrlU must be clear")
	}
	if isClearComposer(press(tea.KeyCtrlC)) {
		t.Fatal("KeyCtrlC must not be clear")
	}
}

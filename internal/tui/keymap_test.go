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

func TestIsClearComposer(t *testing.T) {
	if !isClearComposer(press(tea.KeyCtrlU)) {
		t.Fatal("KeyCtrlU must be clear")
	}
	if isClearComposer(press(tea.KeyCtrlC)) {
		t.Fatal("KeyCtrlC must not be clear")
	}
}

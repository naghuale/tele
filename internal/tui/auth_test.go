package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestAuthPromptTitles(t *testing.T) {
	cases := map[AuthPromptKind]string{
		AuthPromptNone:     "",
		AuthPromptPhone:    "Phone number",
		AuthPromptCode:     "Code",
		AuthPromptPassword: "Password",
	}
	for k, want := range cases {
		if got := k.Title(); got != want {
			t.Fatalf("Title() = %q, want %q", got, want)
		}
	}
}

func TestScreenAuthValid(t *testing.T) {
	if !ScreenAuth.Valid() {
		t.Fatal("ScreenAuth must be valid")
	}
	if ScreenAuth.String() != "auth" {
		t.Fatalf("ScreenAuth.String() = %q", ScreenAuth.String())
	}
}

func TestAuthViewShowsPrompt(t *testing.T) {
	m := NewModel()
	m.screen = ScreenAuth
	m.authPrompt = AuthPromptPhone
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	v := m.View()
	if !strings.Contains(v, "Sign in") {
		t.Fatalf("missing title: %s", v)
	}
	if !strings.Contains(v, "Phone number") {
		t.Fatalf("missing prompt: %s", v)
	}
}

func TestAuthViewShowsComposerInput(t *testing.T) {
	m := NewModel()
	m.screen = ScreenAuth
	m.authPrompt = AuthPromptCode
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m, _ = updateModel(t, m, pressRunes("1234"))

	if !strings.Contains(m.View(), "1234") {
		t.Fatalf("composer not rendered: %s", m.View())
	}
}

func TestAuthPasswordIsMasked(t *testing.T) {
	m := NewModel()
	m.screen = ScreenAuth
	m.authPrompt = AuthPromptPassword
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m, _ = updateModel(t, m, pressRunes("secret"))

	view := m.View()
	if strings.Contains(view, "secret") {
		t.Fatalf("password exposed in view: %q", view)
	}
	if !strings.Contains(view, "••••••") {
		t.Fatalf("masked password missing: %q", view)
	}
}

func TestAuthPhoneAndCodeAreNotMasked(t *testing.T) {
	for _, prompt := range []AuthPromptKind{AuthPromptPhone, AuthPromptCode} {
		m := NewModel()
		m.screen = ScreenAuth
		m.authPrompt = prompt
		m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
		m, _ = updateModel(t, m, pressRunes("+15551234"))

		if !strings.Contains(m.View(), "+15551234") {
			t.Fatalf("prompt %v: value should be visible: %s", prompt, m.View())
		}
	}
}

func TestAuthInputAcceptsUnicode(t *testing.T) {
	m := NewModel()
	m.screen = ScreenAuth
	m.authPrompt = AuthPromptPassword
	m, _ = updateModel(t, m, pressRunes("пароль"))
	if m.Composer() != "пароль" {
		t.Fatalf("composer = %q", m.Composer())
	}
}

func TestAuthBackspaceRemovesOneRune(t *testing.T) {
	m := NewModel()
	m.screen = ScreenAuth
	m.authPrompt = AuthPromptCode
	m, _ = updateModel(t, m, pressRunes("аб"))
	m, _ = updateModel(t, m, press(tea.KeyBackspace))
	if m.Composer() != "а" {
		t.Fatalf("composer = %q", m.Composer())
	}
}

func TestAuthCtrlUClearsComposer(t *testing.T) {
	m := NewModel()
	m.screen = ScreenAuth
	m.authPrompt = AuthPromptCode
	m, _ = updateModel(t, m, pressRunes("1234"))
	m, _ = updateModel(t, m, press(tea.KeyCtrlU))
	if m.Composer() != "" {
		t.Fatalf("composer = %q", m.Composer())
	}
}

func TestAuthEnterQuitsPreservingInput(t *testing.T) {
	m := NewModel()
	m.screen = ScreenAuth
	m.authPrompt = AuthPromptCode
	m, _ = updateModel(t, m, pressRunes("1234"))

	updated, cmd := m.Update(press(tea.KeyEnter))
	mm := updated.(Model)
	if !mm.quitting {
		t.Fatal("Enter must quit the auth prompt")
	}
	if cmd == nil {
		t.Fatal("Enter must return a quit command")
	}
	if mm.authCanceled {
		t.Fatal("Enter must not mark canceled")
	}
	if mm.Composer() != "1234" {
		t.Fatalf("composer = %q, want preserved", mm.Composer())
	}
}

func TestAuthEscQuitsMarkingCanceled(t *testing.T) {
	m := NewModel()
	m.screen = ScreenAuth
	m.authPrompt = AuthPromptCode
	m, _ = updateModel(t, m, pressRunes("1234"))

	updated, cmd := m.Update(press(tea.KeyEsc))
	mm := updated.(Model)
	if !mm.quitting {
		t.Fatal("Esc must quit the auth prompt")
	}
	if cmd == nil {
		t.Fatal("Esc must return a quit command")
	}
	if !mm.authCanceled {
		t.Fatal("Esc must mark canceled")
	}
	if mm.Composer() != "" {
		t.Fatalf("composer = %q, want empty on cancel", mm.Composer())
	}
}

func TestAuthCtrlCQuitsMarkingCanceled(t *testing.T) {
	m := NewModel()
	m.screen = ScreenAuth
	m.authPrompt = AuthPromptCode
	m, _ = updateModel(t, m, pressRunes("1234"))

	updated, cmd := m.Update(press(tea.KeyCtrlC))
	mm := updated.(Model)
	if !mm.quitting {
		t.Fatal("Ctrl+C must quit the auth prompt")
	}
	if cmd == nil {
		t.Fatal("Ctrl+C must return a quit command")
	}
	if !mm.authCanceled {
		t.Fatal("Ctrl+C must mark canceled")
	}
	if mm.Composer() != "" {
		t.Fatalf("composer = %q, want empty on cancel", mm.Composer())
	}
}

// AuthPromptNone must not accidentally mask code/phone.
func TestAuthDisplayValueNoneIsIdentity(t *testing.T) {
	m := NewModel()
	m.screen = ScreenAuth
	m.authPrompt = AuthPromptNone

	m, _ = updateModel(
		t,
		m,
		pressRunes("hello"),
	)

	if got := m.authDisplayValue(); got != "hello" {
		t.Fatalf(
			"authDisplayValue = %q, want %q",
			got,
			"hello",
		)
	}
}

// Guard: ErрорAuthCanceled must not be confused with a regular error.
func TestErrAuthCanceledIsSentinel(t *testing.T) {
	wrapped := fmt.Errorf(
		"prompt failed: %w",
		ErrAuthCanceled,
	)

	if !errors.Is(wrapped, ErrAuthCanceled) {
		t.Fatal(
			"wrapped cancellation must match ErrAuthCanceled",
		)
	}

	otherErr := errors.New("another error")
	if errors.Is(ErrAuthCanceled, otherErr) {
		t.Fatal(
			"ErrAuthCanceled must not match unrelated errors",
		)
	}
}
func TestAuthPromptKindValid(t *testing.T) {
	valid := []AuthPromptKind{
		AuthPromptNone,
		AuthPromptPhone,
		AuthPromptCode,
		AuthPromptPassword,
	}

	for _, prompt := range valid {
		if !prompt.Valid() {
			t.Fatalf(
				"%v must be valid",
				prompt,
			)
		}
	}

	if AuthPromptKind(255).Valid() {
		t.Fatal(
			"unknown AuthPromptKind must be invalid",
		)
	}
}

func TestAuthPromptKindString(t *testing.T) {
	tests := []struct {
		prompt AuthPromptKind
		want   string
	}{
		{
			prompt: AuthPromptNone,
			want:   "none",
		},
		{
			prompt: AuthPromptPhone,
			want:   "phone",
		},
		{
			prompt: AuthPromptCode,
			want:   "code",
		},
		{
			prompt: AuthPromptPassword,
			want:   "password",
		},
		{
			prompt: AuthPromptKind(255),
			want:   "unknown",
		},
	}

	for _, test := range tests {
		if got := test.prompt.String(); got != test.want {
			t.Fatalf(
				"AuthPromptKind(%d).String() = %q, want %q",
				test.prompt,
				got,
				test.want,
			)
		}
	}
}

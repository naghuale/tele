package application

import (
	"context"
	"strings"
	"testing"

	"telecli/internal/config"
	"telecli/internal/tui"
)

// The Nerd Font setting is a decision a user makes, not a fact a program
// can find out: a terminal does not report the font it has been given.
// So the two things the composition root can do about it are to hand it to
// the TUI as it was written, and to report it in telecli doctor, which is
// the only place a user can check that what they wrote is what the program
// will do.

// A program built without the setting draws square corners, which is what a
// terminal without the font needs and what every screen of §14 asks for: an
// interface that does not depend on a font a user may not have.
func TestANerdFontIsOffUnlessItWasAskedFor(t *testing.T) {
	if config.Default().TUI.NerdFont != config.DefaultTUINerdFont {
		t.Errorf(
			"the default is %v, want %v",
			config.Default().TUI.NerdFont, config.DefaultTUINerdFont,
		)
	}
	if config.DefaultTUINerdFont {
		t.Error("the default draws halves a terminal without the font would show as squares")
	}

	var captured tui.Dependencies

	app := NewWithAuthAndSubmitter(
		config.Default(),
		nil,
		nil,
		func(context.Context) (AuthRunResult, error) {
			return AuthRunResult{Submitter: noopComposerSubmitter{}}, nil
		},
		func(tui.ChatSource) error { return nil },
		func(_ context.Context, deps tui.Dependencies) error {
			captured = deps
			return nil
		},
	)

	if err := app.RunTUI(context.Background()); err != nil {
		t.Fatalf("RunTUI: %v", err)
	}

	if captured.NerdFont {
		t.Error("the TUI was told to round the ends of a message without being asked")
	}
}

// The setting has to reach the TUI, or resolving it in the composition
// root would be a ceremony with no effect and a user who set it would see
// exactly what they saw before.
func TestTheNerdFontSettingReachesTheTUIDependencies(t *testing.T) {
	var captured tui.Dependencies

	app := NewWithAuthAndSubmitter(
		config.Default(),
		nil,
		nil,
		func(context.Context) (AuthRunResult, error) {
			return AuthRunResult{Submitter: noopComposerSubmitter{}}, nil
		},
		func(tui.ChatSource) error { return nil },
		func(_ context.Context, deps tui.Dependencies) error {
			captured = deps
			return nil
		},
	).WithNerdFont(true)

	if err := app.RunTUI(context.Background()); err != nil {
		t.Fatalf("RunTUI: %v", err)
	}

	if !captured.NerdFont {
		t.Error("the TUI was not told to round the ends of a message")
	}
}

// The doctor is a report of state, and this one is a report of a decision
// the user made rather than of a fact about the machine. It has to say
// which way the decision went, and it has to say what a terminal without
// the font draws, because "the setting is on" and "the setting is on and
// the font is there" are two different screens.
func TestDoctorReportsTheNerdFontSetting(t *testing.T) {
	cases := []struct {
		name   string
		nerd   bool
		want   string
		refuse string
	}{
		{
			name:   "off",
			nerd:   false,
			want:   "Interface font: plain",
			refuse: "set tui.nerd_font = true to round the ends of a message",
		},
		{
			name:   "on",
			nerd:   true,
			want:   "Interface font: Nerd Font",
			refuse: "a terminal without the font draws them as empty squares",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			path := writeWidthConfig(t, "")
			setNerdFont(t, path, testCase.nerd)

			stdout, stderr, code := runCommand(t, "doctor", "--config", path)
			if code != 0 {
				t.Fatalf("exit code = %d, want 0\n%s%s", code, stdout, stderr)
			}

			if !strings.Contains(stdout, testCase.want) {
				t.Errorf("doctor does not report %q:\n%s", testCase.want, stdout)
			}
			if !strings.Contains(stdout, testCase.refuse) {
				t.Errorf(
					"doctor does not say what a terminal without the font draws (%q):\n%s",
					testCase.refuse, stdout,
				)
			}
		})
	}
}

// setNerdFont rewrites the [tui] section of a written configuration, so a
// test can ask for the setting without a second kind of configuration
// file.
func setNerdFont(t *testing.T, path string, enabled bool) {
	t.Helper()

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("config.Load(%s): %v", path, err)
	}
	cfg.TUI.NerdFont = enabled

	if err := config.WriteFile(path, cfg); err != nil {
		t.Fatalf("config.WriteFile(%s): %v", path, err)
	}
}

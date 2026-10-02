package application

import (
	"context"
	"strings"
	"testing"

	"telecli/internal/config"
	"telecli/internal/tui"
)

// The defaults are written in two places, because internal/config is a leaf
// that must not import the clock package. They have to agree, or a default
// configuration would be drawn in a format nobody named.
func TestInterfaceDefaultsMatchTheClockPackage(t *testing.T) {
	defaults := config.Default()

	mode, err := tui.ParseClockMode(defaults.TUI.Clock)
	if err != nil {
		t.Fatalf("the default clock %q is not a value: %v", defaults.TUI.Clock, err)
	}
	if mode != tui.ClockAuto {
		t.Errorf("default clock = %q, want auto", defaults.TUI.Clock)
	}
}

func TestResolveInterfaceClock(t *testing.T) {
	cases := []struct {
		value string
		want  tui.ClockFormat
	}{
		// A configured format is the answer whatever the machine says, and
		// the machine is not asked at all here — that is what makes this
		// table the same on a Mac set to a twelve-hour clock and in CI.
		{value: "12h", want: tui.ClockFormat12h},
		{value: "24h", want: tui.ClockFormat24h},
		// An empty word is the default, because a configuration written
		// before the setting existed has none.
		{value: "", want: resolveSystemClock(tui.ClockAuto)},
		{value: "auto", want: resolveSystemClock(tui.ClockAuto)},
	}

	for _, testCase := range cases {
		cfg := config.Default()
		cfg.TUI.Clock = testCase.value

		got, err := resolveInterfaceClock(cfg)
		if err != nil {
			t.Errorf("resolveInterfaceClock(%q): %v", testCase.value, err)
			continue
		}
		if got != testCase.want {
			t.Errorf(
				"resolveInterfaceClock(%q) = %v, want %v",
				testCase.value, got, testCase.want,
			)
		}
	}
}

// A word that is not one of the three is a configuration error with the
// three in it, and it is reported by the command the user runs rather than
// at the next start of the TUI.
func TestAnUnknownClockIsAConfigurationError(t *testing.T) {
	cfg := config.Default()
	cfg.TUI.Clock = "half past"

	_, err := resolveInterfaceClock(cfg)
	if err == nil {
		t.Fatal("a clock of \"half past\" was accepted")
	}

	for _, want := range []string{"auto", "12h", "24h", "half past"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error %q does not name %q", err, want)
		}
	}
}

func TestTheDoctorRejectsAnUnknownClock(t *testing.T) {
	path := writeClockConfig(t, "half past")

	stdout, stderr, code := runCommand(t, "doctor", "--config", path)
	if code == 0 {
		t.Fatalf("doctor accepted a clock of \"half past\":\n%s", stdout)
	}
	if !strings.Contains(stderr, "24h") {
		t.Errorf("doctor does not say what the valid clocks are: %q", stderr)
	}
}

// The doctor is a report of state, so it says which clock the screen is drawn
// in and whether the format was configured or is the machine's own. It does
// not read the machine's preferences: the doctor prints lines and leaves, and
// a reading of a machine is a question for the command that draws in it.
func TestDoctorReportsTheClock(t *testing.T) {
	cases := []struct {
		name  string
		clock string
		want  string
	}{
		{
			name:  "a twelve-hour clock that was configured",
			clock: "12h",
			want:  "Interface clock: 12h (configured)",
		},
		{
			name:  "a twenty-four-hour clock that was configured",
			clock: "24h",
			want:  "Interface clock: 24h (configured)",
		},
		{
			name:  "no clock at all",
			clock: "",
			want:  "Interface clock: auto (system)",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			path := writeClockConfig(t, testCase.clock)

			stdout, stderr, code := runCommand(t, "doctor", "--config", path)
			if code != 0 {
				t.Fatalf("exit code = %d, want 0\n%s%s", code, stdout, stderr)
			}

			if !strings.Contains(stdout, testCase.want) {
				t.Errorf("doctor does not report %q:\n%s", testCase.want, stdout)
			}
		})
	}
}

// A configuration that left the choice to the machine says so, and says when
// the machine is asked. A user who reads "system" and wants to know what the
// system actually says has to be able to find out from the same screen.
func TestDoctorSaysThatAutoIsReadAtTheTUI(t *testing.T) {
	path := writeClockConfig(t, "")

	stdout, _, code := runCommand(t, "doctor", "--config", path)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", code, stdout)
	}

	if !strings.Contains(stdout, "read when the TUI starts") {
		t.Errorf("doctor does not say that auto is read at the TUI:\n%s", stdout)
	}
}

// The format has to reach the TUI, or resolving it here would be a ceremony
// with no effect: a screen drawn in a clock the configuration did not ask for
// is the owner's report of 29.09.2026 all over again.
func TestTheClockReachesTheTUIDependencies(t *testing.T) {
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
	).WithClock(tui.ClockFormat12h)

	if err := app.RunTUI(context.Background()); err != nil {
		t.Fatalf("RunTUI: %v", err)
	}

	if captured.Clock != tui.ClockFormat12h {
		t.Errorf("the TUI was given clock %v, want 12h", captured.Clock)
	}
}

// An app nobody told a clock for draws twenty-four hours: a machine whose own
// setting could not be read is a machine that is drawn this way, and an app
// that drew a twelve-hour clock by accident would be one of those machines
// with a different accident.
func TestAnAppWithoutAClockDrawsTwentyFourHours(t *testing.T) {
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

	if captured.Clock != tui.ClockFormat24h {
		t.Errorf("the TUI was given clock %v, want 24h", captured.Clock)
	}
}

// `telecli configure status` reports the interface settings as they are
// written, and refuses a word the program would refuse — a status that prints
// "auto" for a clock set to "half past" is a report of a setting the program
// cannot use.
func TestConfigureStatusReportsTheInterfaceSettings(t *testing.T) {
	path := writeClockConfig(t, "12h")

	stdout, stderr, code := runCommand(t, "configure", "status", "--config", path)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\n%s%s", code, stdout, stderr)
	}

	for _, want := range []string{
		"Interface",
		"Theme",
		"Width",
		"Clock",
		"Nerd Font",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("configure status does not report %q:\n%s", want, stdout)
		}
	}
	if !strings.Contains(stdout, "12h") {
		t.Errorf("configure status does not report the configured clock:\n%s", stdout)
	}
}

func TestConfigureStatusRejectsAnUnknownClock(t *testing.T) {
	path := writeClockConfig(t, "half past")

	stdout, stderr, code := runCommand(t, "configure", "status", "--config", path)
	if code == 0 {
		t.Fatalf("configure status accepted a clock of \"half past\":\n%s", stdout)
	}
	if !strings.Contains(stderr, "24h") {
		t.Errorf("configure status does not name the valid clocks: %q", stderr)
	}
}

// writeClockConfig writes a configuration file with a clock in it, and points
// TELECLI_CONFIG at it.
func writeClockConfig(t *testing.T, clock string) string {
	t.Helper()

	path := writeWidthConfig(t, "auto")

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("read the configuration back: %v", err)
	}
	cfg.TUI.Clock = clock

	if err := config.WriteFile(path, cfg); err != nil {
		t.Fatalf("write config: %v", err)
	}

	t.Setenv("TELECLI_CONFIG", path)

	return path
}

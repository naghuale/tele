package application

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"telecli/internal/config"
	"telecli/internal/tui"
)

// The setting that says what the number in the header of the chat list
// counts, resolved where every other interface setting is resolved and
// reported where every other one is reported.
//
// It is a word in a file like the theme, the width rule and the clock, and
// it is answered the same way: a word that is not one of the three is a
// configuration error with the three in it, because the setting was written
// to change what the header says, and a header that keeps saying the old
// thing is the failure the setting was written against (#46).

func TestTheUnreadCounterResolvesToWhatTheConfigurationAsksFor(t *testing.T) {
	for value, want := range map[string]tui.UnreadCounterMode{
		"":         tui.UnreadCounterChats,
		"chats":    tui.UnreadCounterChats,
		"messages": tui.UnreadCounterMessages,
		"off":      tui.UnreadCounterOff,
	} {
		cfg := config.Default()
		cfg.TUI.UnreadCounter = value

		got, err := resolveInterfaceUnreadCounter(cfg)
		if err != nil {
			t.Fatalf("resolveInterfaceUnreadCounter(%q): %v", value, err)
		}
		if got != want {
			t.Fatalf("resolveInterfaceUnreadCounter(%q) = %v, want %v", value, got, want)
		}
	}
}

func TestAMistypedUnreadCounterIsAConfigurationError(t *testing.T) {
	cfg := config.Default()
	cfg.TUI.UnreadCounter = "chats, please"

	_, err := resolveInterfaceUnreadCounter(cfg)
	if err == nil {
		t.Fatal("an unknown word must be reported, not ignored")
	}
	for _, word := range []string{"chats", "messages", "off"} {
		if !strings.Contains(err.Error(), word) {
			t.Fatalf("the error %q does not name %q", err, word)
		}
	}
}

// The default of the configuration and the default of the interface are one
// setting and not two: a program started on a configuration that says
// nothing has to draw what a program started on the written-out default
// draws.
func TestTheConfiguredDefaultCountsChats(t *testing.T) {
	if config.Default().TUI.UnreadCounter != config.DefaultTUIUnreadCounter {
		t.Fatalf(
			"the default of the configuration = %q, want %q",
			config.Default().TUI.UnreadCounter,
			config.DefaultTUIUnreadCounter,
		)
	}

	mode, err := tui.ParseUnreadCounterMode(config.DefaultTUIUnreadCounter)
	if err != nil {
		t.Fatalf("ParseUnreadCounterMode(%q): %v", config.DefaultTUIUnreadCounter, err)
	}
	if mode != tui.UnreadCounterChats {
		t.Fatalf(
			"the default resolves to %v, want the chats of the list",
			mode,
		)
	}
}

// What the setting says is in telecli doctor, because a reader who changed a
// word in a file has one question — did it reach the program — and the
// doctor is the one command that answers it without drawing a frame.
func TestTheDoctorReportsTheUnreadCounter(t *testing.T) {
	for mode, want := range map[tui.UnreadCounterMode]string{
		tui.UnreadCounterChats:    "Interface unread counter: chats",
		tui.UnreadCounterMessages: "Interface unread counter: messages",
		tui.UnreadCounterOff:      "Interface unread counter: off",
	} {
		var out bytes.Buffer
		writeUnreadCounterStatus(&out, mode)

		if !strings.Contains(out.String(), want) {
			t.Fatalf("the doctor says %q, want %q", out.String(), want)
		}
	}
}

// The setting has to reach the TUI, or resolving it in the composition root
// would be a ceremony with no effect and a reader who wrote the word would
// go on reading the header they wrote it to change. It is proved on the
// dependencies the TUI is built with, which is where the other three
// interface settings are proved too.
func TestTheUnreadCounterReachesTheTUIDependencies(t *testing.T) {
	for _, mode := range []tui.UnreadCounterMode{
		tui.UnreadCounterChats,
		tui.UnreadCounterMessages,
		tui.UnreadCounterOff,
	} {
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
		).WithUnreadCounter(mode)

		if err := app.RunTUI(context.Background()); err != nil {
			t.Fatalf("RunTUI: %v", err)
		}
		if captured.UnreadCounter != mode {
			t.Fatalf(
				"the TUI was given %v, want %v",
				captured.UnreadCounter, mode,
			)
		}
	}
}

// And an app built without the setting counts chats: the zero value is the
// default of the setting, so a program nobody configured draws what a
// program started on the written-out default draws.
func TestAnAppBuiltWithoutTheCounterCountsChats(t *testing.T) {
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
	if captured.UnreadCounter != tui.UnreadCounterChats {
		t.Fatalf(
			"the TUI was given %v without being asked, want the chats of the list",
			captured.UnreadCounter,
		)
	}

	var nilApp *App
	if nilApp.WithUnreadCounter(tui.UnreadCounterOff) != nil {
		t.Fatal("WithUnreadCounter on a nil app must stay nil")
	}
}

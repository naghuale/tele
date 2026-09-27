package application

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"telecli/internal/config"
	"telecli/internal/telegram"
	"telecli/internal/tui"
)

// The status line and the diagnostic stream are wired by the composition
// root, and nothing else can wire them: the model asks for them and the
// application is the only place that knows where they come from.

// summarySourceStub is a status summary source the composition root can
// pass on, and a test can recognize.
type summarySourceStub struct{}

func (summarySourceStub) ReadStatusSummary(
	context.Context,
	int64,
) (tui.StatusSummary, error) {
	return tui.StatusSummary{Connection: tui.ConnectionReady}, nil
}

func TestTheStatusSummarySourceReachesTheTUIDependencies(t *testing.T) {
	summary := summarySourceStub{}
	var captured tui.Dependencies

	app := NewWithAuthAndSubmitter(
		config.Default(),
		nil,
		nil,
		func(context.Context) (AuthRunResult, error) {
			return AuthRunResult{
				Submitter:       noopComposerSubmitter{},
				StatusSummaries: summary,
			}, nil
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

	if captured.StatusSummaries == nil {
		t.Fatal("the status summary source did not reach the interface")
	}
	if _, ok := captured.StatusSummaries.(summarySourceStub); !ok {
		t.Fatalf(
			"status summary source = %T, want the one the auth step built",
			captured.StatusSummaries,
		)
	}
}

// The causes the screen must not show need somewhere to go, and the
// diagnostic stream is where they were already going for the paused queue.
func TestTheDiagnosticStreamReachesTheTUIDependencies(t *testing.T) {
	log := &bytes.Buffer{}
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
	).WithDiagnostics(log)

	if err := app.RunTUI(context.Background()); err != nil {
		t.Fatalf("RunTUI: %v", err)
	}

	if captured.Diagnostics == nil {
		t.Fatal("the diagnostic stream did not reach the interface")
	}
	if _, err := captured.Diagnostics.Write([]byte("cause\n")); err != nil {
		t.Fatalf("the interface could not write a cause: %v", err)
	}
	if !strings.Contains(log.String(), "cause") {
		t.Fatalf("diagnostics = %q, want the cause", log.String())
	}
}

// The session is the thing that owns the store the status line reads, and
// the wiring asks it for one. A session that stopped returning it would
// fail to compile here rather than leave the status line silent at run
// time.
var _ interface{ LiveState() *telegram.LiveState } = (*telegram.AuthorizedSession)(nil)

// A session with no store yet answers for itself, so the first poll of a
// program that is still starting says "unknown" instead of failing.
func TestAStatusSummarySourceWithoutAStoreReads(t *testing.T) {
	source, err := NewLiveStatusSummarySource(
		(*telegram.LiveState)(nil),
		fixedHealthSource{health: MessageDeliveryHealth{
			State:  MessageDeliveryHealthRunning,
			Queued: 1,
		}},
		0,
	)
	if err != nil {
		t.Fatalf("NewLiveStatusSummarySource: %v", err)
	}

	summary, err := source.ReadStatusSummary(context.Background(), 0)
	if err != nil {
		t.Fatalf("ReadStatusSummary: %v", err)
	}
	if summary.Connection != tui.ConnectionUnknown {
		t.Fatalf("connection = %q, want unknown", summary.Connection)
	}
}

// The presence opener has to reach the interface: without it a group header
// says nothing about its members, because TDLib only counts a chat that has
// been opened.
func TestThePresenceOpenerReachesTheTUIDependencies(t *testing.T) {
	opener := &TelegramChatPresenceOpener{}
	var captured tui.Dependencies

	app := NewWithAuthAndSubmitter(
		config.Default(),
		nil,
		nil,
		func(context.Context) (AuthRunResult, error) {
			return AuthRunResult{
				Submitter:      noopComposerSubmitter{},
				PresenceOpener: opener,
			}, nil
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

	if captured.PresenceOpener == nil {
		t.Fatal("the presence opener did not reach the interface")
	}
	if captured.PresenceOpener != opener {
		t.Fatalf(
			"presence opener = %T, want the one the auth step built",
			captured.PresenceOpener,
		)
	}
}

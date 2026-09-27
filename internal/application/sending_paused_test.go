package application

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"telecli/internal/config"
	"telecli/internal/outbox"
)

// ---- Reason classification by sentinel, never by text ----

// Each case is matched by errors.Is. A message that merely looks like a
// case must not be treated as one, and a wrapped sentinel must still be
// found.
func TestClassifySendingPausedCoversEveryCase(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
		want SendingPausedReason
	}{
		{
			name: "keychain locked or access denied",
			err:  outbox.ErrOutboxKeyAccessDenied,
			want: SendingPausedKeychainLocked,
		},
		{
			name: "key missing for an existing queue",
			err:  outbox.ErrOutboxKeyUnavailable,
			want: SendingPausedKeyMissing,
		},
		{
			name: "no secure key storage",
			err:  outbox.ErrOutboxKeyProviderUnsupported,
			want: SendingPausedNoKeyStorage,
		},
		{
			name: "data folder reachable by others",
			err:  outbox.ErrOutboxDataDirInsecure,
			want: SendingPausedDataDirInsecure,
		},
		{
			name: "anything else",
			err:  errors.New("disk on fire"),
			want: SendingPausedOther,
		},
		{
			name: "no error",
			err:  nil,
			want: SendingPausedOther,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := classifySendingPaused(c.err); got != c.want {
				t.Fatalf("classifySendingPaused(%v) = %v, want %v",
					c.err, got, c.want)
			}
		})
	}
}

func TestClassifySendingPausedSeesThroughWrapping(t *testing.T) {
	t.Parallel()

	wrapped := fmt.Errorf(
		"open durable message delivery runtime: %w",
		outbox.ErrOutboxKeyAccessDenied,
	)
	if got := classifySendingPaused(wrapped); got != SendingPausedKeychainLocked {
		t.Fatalf("classifySendingPaused(wrapped) = %v, want keychain locked", got)
	}
}

// A string that merely resembles a known cause must not be classified as
// one: that is the whole reason the mapping is sentinel-based. Each case
// is probed, because a classification that accidentally reads like the
// right text still passes the happy-path test.
func TestClassifySendingPausedIgnoresLookalikeText(t *testing.T) {
	t.Parallel()

	lookalikes := map[string]string{
		"key unavailable":        "outbox: key unavailable",
		"key access denied":      "outbox: key access denied",
		"provider unsupported":   "outbox: key provider not supported on this platform",
		"data dir insecure":      "outbox: data directory is accessible to other users",
		"a whole sentence about": "the keychain is locked so access denied the read",
	}

	for name, text := range lookalikes {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := classifySendingPaused(errors.New(text)); got != SendingPausedOther {
				t.Fatalf(
					"classifySendingPaused(%q) = %v, want other: matching on text "+
						"would send the user to the wrong remedy",
					text, got,
				)
			}
		})
	}
}

// ---- The user-facing text ----

// The four parts come from docs/TUI_SPEC.md decision 3, and the cause
// must not appear in any of them.
func TestSendingPausedErrorTextMatchesTheSpecification(t *testing.T) {
	t.Parallel()

	for _, reason := range []SendingPausedReason{
		SendingPausedKeychainLocked,
		SendingPausedKeyMissing,
		SendingPausedNoKeyStorage,
		SendingPausedDataDirInsecure,
		SendingPausedOther,
	} {
		err := NewSendingPausedError(reason)
		text := err.Error()

		for _, want := range []string{
			"Sending paused",
			"Sending is paused. Your message was not sent and is still here.",
			"telecli could not open its secure message queue",
			reason.Hint(),
			"Details: " + sendingPausedDetailsURL,
		} {
			if !strings.Contains(text, want) {
				t.Fatalf("reason %v text is missing %q:\n%s", reason, want, text)
			}
		}
	}
}

func TestSendingPausedErrorKeepsTheCauseOutOfTheText(t *testing.T) {
	t.Parallel()

	cause := fmt.Errorf(
		"%w: /Users/someone/Library/Keychains/login.keychain-db",
		outbox.ErrOutboxKeyAccessDenied,
	)
	err := &SendingPausedError{
		reason: SendingPausedKeychainLocked,
		cause:  cause,
	}

	if !strings.Contains(err.Error(), "Sending paused") {
		t.Fatalf("text lost the headline:\n%s", err.Error())
	}
	if strings.Contains(err.Error(), "login.keychain-db") {
		t.Fatalf("text leaks the cause:\n%s", err.Error())
	}
	if !errors.Is(err.Cause(), cause) {
		t.Fatalf("Cause() = %v, want %v", err.Cause(), cause)
	}
}

// The hint must match the one in docs/help/sending-paused.md, or the
// screen and the help page would disagree.
func TestSendingPausedHintsMatchTheHelpPage(t *testing.T) {
	t.Parallel()

	cases := map[SendingPausedReason]string{
		SendingPausedKeychainLocked: "Unlock your Keychain or allow telecli access, then restart telecli.",
		SendingPausedKeyMissing:     "The key for your message queue is missing. Run telecli doctor for details.",
		SendingPausedNoKeyStorage:   "This system has no secure key storage, so messages cannot be queued safely.",
		SendingPausedDataDirInsecure: "The telecli data folder is accessible to other users. " +
			"Run telecli doctor to fix it.",
		SendingPausedOther: "Run telecli doctor to see what went wrong.",
	}

	for reason, want := range cases {
		got := strings.Join(strings.Fields(reason.Hint()), " ")
		if got != want {
			t.Fatalf("reason %v hint = %q, want %q", reason, got, want)
		}
	}
}

// ---- The refusing submitter ----

// A paused composer must refuse, and must not reach any sender.
func TestUnavailableSubmitterAlwaysRefuses(t *testing.T) {
	t.Parallel()

	submitter := NewUnavailableComposerSubmitter(SendingPausedKeyMissing)

	_, err := submitter.SubmitMessage(
		context.Background(), "account", 42, "hello",
	)
	if err == nil {
		t.Fatal("SubmitMessage() error = nil, want a refusal")
	}

	var paused *SendingPausedError
	if !errors.As(err, &paused) {
		t.Fatalf("error = %T, want *SendingPausedError", err)
	}
	if paused.Reason() != SendingPausedKeyMissing {
		t.Fatalf("reason = %v, want key missing", paused.Reason())
	}
	if strings.Contains(err.Error(), "hello") {
		t.Fatal("the refusal must not echo the message text")
	}
}

// ---- telecli doctor ----

func TestReportOutboxStatusPrintsOK(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	cfg := h17Config(t)
	reportOutboxStatus(&out, context.Background(), cfg, h17Probe(nil))

	text := out.String()
	if !strings.Contains(text, "Message queue: OK") {
		t.Fatalf("doctor did not report a healthy queue:\n%s", text)
	}
	if !strings.Contains(text, cfg.DataDir) {
		t.Fatalf("doctor did not print the data folder %q:\n%s", cfg.DataDir, text)
	}
}

// For every case doctor prints a comprehensible line, the data folder and
// the full cause, which is the only place the cause appears.
func TestReportOutboxStatusPrintsEveryCase(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
		want string
	}{
		{"locked", outbox.ErrOutboxKeyAccessDenied, "keychain locked or access denied"},
		{"missing", outbox.ErrOutboxKeyUnavailable, "queue key missing"},
		{"no storage", outbox.ErrOutboxKeyProviderUnsupported, "no secure key storage"},
		{"insecure dir", outbox.ErrOutboxDataDirInsecure, "data folder accessible to other users"},
		{"other", errors.New("disk on fire"), "other"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			var out bytes.Buffer
			cfg := h17Config(t)
			reportOutboxStatus(
				&out, context.Background(), cfg, h17Probe(c.err),
			)

			text := out.String()
			for _, want := range []string{
				"Message queue: unavailable",
				c.want,
				cfg.DataDir,
			} {
				if !strings.Contains(text, want) {
					t.Fatalf("doctor output is missing %q:\n%s", want, text)
				}
			}
		})
	}
}

// A legacy direct mode must warn without failing, every run.
func TestDoctorWarnsAboutLegacyDirectMode(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	writeConfigWarnings(&out, []string{
		`message_delivery.mode = "direct" is no longer supported; ` +
			"durable delivery is used instead",
	})

	text := out.String()
	if !strings.Contains(text, "warning:") {
		t.Fatalf("no warning printed:\n%s", text)
	}
	if !strings.Contains(text, "direct") {
		t.Fatalf("warning does not name the retired mode:\n%s", text)
	}
}

func TestWriteConfigWarningsIsQuietWhenThereIsNothingToSay(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	writeConfigWarnings(&out, nil)
	if out.Len() != 0 {
		t.Fatalf("unexpected output:\n%s", out.String())
	}
}

// h17Probe returns an outboxProbe that fails with err, or succeeds with
// a closer that discards everything when err is nil.
func h17Probe(err error) outboxProbe {
	return func(
		context.Context,
		outbox.Config,
		outbox.Deps,
	) (io.Closer, error) {
		if err != nil {
			return nil, err
		}
		return h17NoopCloser{}, nil
	}
}

// h17NoopCloser stands in for an opened outbox.
type h17NoopCloser struct{}

func (h17NoopCloser) Close() error { return nil }

// h17Config returns a config with a known data directory.
func h17Config(t *testing.T) config.Config {
	t.Helper()
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.MessageDelivery.DataDir = cfg.DataDir
	return cfg
}

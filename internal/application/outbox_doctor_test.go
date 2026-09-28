package application

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"telecli/internal/config"
	"telecli/internal/outbox"
)

// The doctor line about a message queue says numbers and states, and
// never what a message says. A queue holds the user's own words, and a
// diagnostic that pastes into an issue must not carry them (#19, #41).

// countingCloser is an opened queue that can be counted.
type countingCloser struct {
	snapshot outbox.OperationalSnapshot
	err      error
	closed   bool
}

func (c *countingCloser) Close() error {
	c.closed = true
	return nil
}

func (c *countingCloser) ReadOperationalSnapshot(
	context.Context,
) (outbox.OperationalSnapshot, error) {
	if c.err != nil {
		return outbox.OperationalSnapshot{}, c.err
	}
	return c.snapshot, nil
}

// queueConfigWithAFile is a config whose data folder holds the file
// doctor looks for before it opens anything.
func queueConfigWithAFile(t *testing.T) config.Config {
	t.Helper()

	dataDir := filepath.Join(t.TempDir(), "outbox")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatalf("create data dir: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(dataDir, "outbox.db"), []byte("not a real database"), 0o600,
	); err != nil {
		t.Fatalf("write database file: %v", err)
	}

	cfg := config.Config{}
	cfg.MessageDelivery.DataDir = dataDir
	return cfg
}

// Every state the queue can hold is counted, in the order a message walks
// them, so a reader can tell "TDLib took it" from "Telegram sent it".
func TestDoctorReportsTheChainByNumbersAndStates(t *testing.T) {
	t.Parallel()

	opened := &countingCloser{snapshot: outbox.OperationalSnapshot{
		Queued:          1,
		Dispatching:     2,
		Accepted:        3,
		Sent:            4,
		FailedRetryable: 5,
		FailedPermanent: 6,
		Uncertain:       7,
		Canceled:        8,
	}}

	var out bytes.Buffer
	reportOutboxStatus(
		&out, context.Background(), queueConfigWithAFile(t),
		func(context.Context, outbox.Config, outbox.Deps) (io.Closer, error) {
			return opened, nil
		},
	)

	text := out.String()
	for _, want := range []string{
		"Message queue: OK",
		"1 queued",
		"2 sending",
		"3 on their way",
		"4 sent",
		"5 retrying",
		"6 failed",
		"7 uncertain",
		"8 canceled",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf(
				"doctor did not report %q:\n%s", want, text,
			)
		}
	}
	if !opened.closed {
		t.Fatal("the queue was not closed after it was counted")
	}
}

// A queue that cannot be counted is reported as unreadable, and never as
// empty: a line of zeroes would say there is nothing in the queue, and a
// queue that could not be read is not an empty one.
func TestDoctorSaysAQueueItCannotCountIsUnreadable(t *testing.T) {
	t.Parallel()

	opened := &countingCloser{err: errors.New("database is locked")}

	var out bytes.Buffer
	reportOutboxStatus(
		&out, context.Background(), queueConfigWithAFile(t),
		func(context.Context, outbox.Config, outbox.Deps) (io.Closer, error) {
			return opened, nil
		},
	)

	text := out.String()
	if !strings.Contains(text, "could not be read") {
		t.Fatalf("an unreadable queue is not reported as such:\n%s", text)
	}
	if strings.Contains(text, "0 queued") {
		t.Fatalf("an unreadable queue is reported as an empty one:\n%s", text)
	}
	// The queue itself is still fine as far as the user is concerned: it
	// opened, and only the counts are missing.
	if !strings.Contains(text, "Message queue: OK") {
		t.Fatalf("an uncountable queue is reported as broken:\n%s", text)
	}
}

// A queue that opens but cannot be counted at all is still a queue that
// works. Nothing is invented for it, and the queue is closed.
func TestDoctorSaysNothingAboutAQueueItCannotCountAtAll(t *testing.T) {
	t.Parallel()

	opened := h17NoopCloser{}

	var out bytes.Buffer
	reportOutboxStatus(
		&out, context.Background(), queueConfigWithAFile(t),
		func(context.Context, outbox.Config, outbox.Deps) (io.Closer, error) {
			return opened, nil
		},
	)

	text := out.String()
	if !strings.Contains(text, "Message queue: OK") {
		t.Fatalf("doctor did not report the queue:\n%s", text)
	}
	if strings.Contains(text, "Messages:") {
		t.Fatalf(
			"doctor invented a count for a queue it could not read:\n%s",
			text,
		)
	}
}

// The chain line is a diagnostic for somebody pasting it into a bug
// report, so it carries no message text and no account key. The numbers
// it does carry are the ones a reader needs to tell the states apart.
func TestTheChainLineCarriesNoMessageText(t *testing.T) {
	t.Parallel()

	opened := &countingCloser{snapshot: outbox.OperationalSnapshot{
		Queued: 1, Accepted: 1, Sent: 1,
	}}

	var out bytes.Buffer
	reportOutboxStatus(
		&out, context.Background(), queueConfigWithAFile(t),
		func(context.Context, outbox.Config, outbox.Deps) (io.Closer, error) {
			return opened, nil
		},
	)

	text := out.String()
	for _, forbidden := range []string{"@", "http", "Bearer"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf(
				"the chain line carries %q, which has no business in a "+
					"diagnostic:\n%s", forbidden, text,
			)
		}
	}
}

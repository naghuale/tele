package application

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"telecli/internal/config"
	"telecli/internal/outbox"
	"telecli/internal/tui"
)

// Cancel of §13: one translation between the optimistic version the screen
// read and the refusal the store gives back.
//
// The translation is the whole of this file, and it is worth testing on its
// own: the interface has two sentences for two failures, and a store refusal
// that arrives as itself would be a cause on the screen.

func TestCancelNamesTheEntryAndTheVersion(t *testing.T) {
	submitter := &recordingMessageSubmitter{}
	canceller, err := NewOutboxPendingMessageCanceller(submitter)
	if err != nil {
		t.Fatalf("NewOutboxPendingMessageCanceller: %v", err)
	}

	if err := canceller.CancelMessage(context.Background(), "entry-1", 7); err != nil {
		t.Fatalf("CancelMessage: %v", err)
	}

	if submitter.canceled != 1 {
		t.Fatalf("cancels = %d, want 1", submitter.canceled)
	}
	if submitter.canceledID != outbox.ID("entry-1") {
		t.Fatalf("canceled id = %q, want entry-1", submitter.canceledID)
	}
	if submitter.canceledVersion != 7 {
		t.Fatalf("canceled version = %d, want 7", submitter.canceledVersion)
	}
}

// A record that moved is the one failure with its own sentence on the
// screen, and the store says it in four different ways depending on how far
// it moved: a version conflict, a transition the domain forbids, a cancel of
// a record Telegram already accepted, and a record that is not there at all.
func TestARefusalThatMeansTheRecordMovedIsTranslated(t *testing.T) {
	for name, cause := range map[string]error{
		"version conflict":   outbox.ErrVersionConflict,
		"invalid transition": outbox.ErrInvalidTransition,
		"already accepted":   outbox.ErrCancelAfterAccepted,
		"not found":          outbox.ErrNotFound,
	} {
		t.Run(name, func(t *testing.T) {
			canceller, err := NewOutboxPendingMessageCanceller(
				&recordingMessageSubmitter{cancelErr: cause},
			)
			if err != nil {
				t.Fatalf("NewOutboxPendingMessageCanceller: %v", err)
			}

			err = canceller.CancelMessage(context.Background(), "entry-1", 7)
			if !errors.Is(err, tui.ErrMessageStateChanged) {
				t.Fatalf("error = %v, want ErrMessageStateChanged", err)
			}
			if !errors.Is(err, cause) {
				t.Fatalf("error = %v, want the store's own cause for the log", err)
			}
		})
	}
}

// Every other refusal is a queue that would not do it, and the screen says
// the message is still there instead of guessing why.
func TestAnyOtherRefusalIsACancelThatDidNotHappen(t *testing.T) {
	canceller, err := NewOutboxPendingMessageCanceller(
		&recordingMessageSubmitter{
			cancelErr: errors.New("outbox: lease held"),
		},
	)
	if err != nil {
		t.Fatalf("NewOutboxPendingMessageCanceller: %v", err)
	}

	err = canceller.CancelMessage(context.Background(), "entry-1", 7)
	if !errors.Is(err, tui.ErrMessageCancelUnavailable) {
		t.Fatalf("error = %v, want ErrMessageCancelUnavailable", err)
	}
	if errors.Is(err, tui.ErrMessageStateChanged) {
		t.Fatal("a queue that would not cancel was reported as a moved record")
	}
}

func TestTheCancellerNeedsASubmitter(t *testing.T) {
	if _, err := NewOutboxPendingMessageCanceller(nil); err == nil {
		t.Fatal("a canceller without a submitter must be refused")
	}
}

// A program with no durable queue has nothing to cancel, and the interface
// says so rather than failing the call it was handed.
func TestTheCancellerWithoutASubmitterSaysItCannot(t *testing.T) {
	var canceller *OutboxPendingMessageCanceller

	if err := canceller.CancelMessage(context.Background(), "entry-1", 7); !errors.Is(
		err,
		tui.ErrMessageCancelUnavailable,
	) {
		t.Fatalf("error = %v, want ErrMessageCancelUnavailable", err)
	}
}

// The cause travels with the translation: a store refusal can name a path
// and a keychain service, and the log is where that belongs.
func TestTheCancellerKeepsTheStoreCause(t *testing.T) {
	canceller, err := NewOutboxPendingMessageCanceller(
		&recordingMessageSubmitter{
			cancelErr: fmt.Errorf(
				"cancel: %w: /Users/me/Library/outbox.db",
				outbox.ErrVersionConflict,
			),
		},
	)
	if err != nil {
		t.Fatalf("NewOutboxPendingMessageCanceller: %v", err)
	}

	err = canceller.CancelMessage(context.Background(), "entry-1", 7)
	if !errors.Is(err, tui.ErrMessageStateChanged) {
		t.Fatalf("error = %v, want ErrMessageStateChanged", err)
	}
	if !strings.Contains(err.Error(), "/Users/me/Library/outbox.db") {
		t.Fatalf("error = %v, want the store's own cause for the log", err)
	}
}

// A cancel of nothing is not a cancel that happened: the interface has to
// be able to say so, and an entry without an identifier is a bug that must
// not reach the store as a request.
func TestACancelWithoutAnEntryIsRefused(t *testing.T) {
	submitter := &recordingMessageSubmitter{}
	canceller, err := NewOutboxPendingMessageCanceller(submitter)
	if err != nil {
		t.Fatalf("NewOutboxPendingMessageCanceller: %v", err)
	}

	if err := canceller.CancelMessage(context.Background(), "", 7); err == nil {
		t.Fatal("a cancel without an entry id was accepted")
	}
	if submitter.canceled != 0 {
		t.Fatalf("the store was asked %d times", submitter.canceled)
	}
}

// The canceller has to reach the interface: a menu whose cancel cannot be
// performed is a menu that lies about what the queue can do.
func TestTheCancellerReachesTheTUIDependencies(t *testing.T) {
	canceller, err := NewOutboxPendingMessageCanceller(&recordingMessageSubmitter{})
	if err != nil {
		t.Fatalf("NewOutboxPendingMessageCanceller: %v", err)
	}

	var captured tui.Dependencies
	terminal := &bytes.Buffer{}
	app := NewWithAuthAndSubmitter(
		config.Default(),
		nil,
		nil,
		func(context.Context) (AuthRunResult, error) {
			return AuthRunResult{
				Submitter:        noopComposerSubmitter{},
				MessageCanceller: canceller,
			}, nil
		},
		func(tui.ChatSource) error { return nil },
		func(_ context.Context, deps tui.Dependencies) error {
			captured = deps
			return nil
		},
	).WithTerminal(terminal)

	if err := app.RunTUI(context.Background()); err != nil {
		t.Fatalf("RunTUI: %v", err)
	}
	if captured.MessageCanceller == nil {
		t.Fatal("the canceller did not reach the interface")
	}
	if captured.Clipboard != terminal {
		t.Fatal("the terminal stream did not reach the interface")
	}
}

// recordingMessageSubmitter records the cancels it was asked for.
type recordingMessageSubmitter struct {
	canceled        int
	canceledID      outbox.ID
	canceledVersion uint64
	cancelErr       error
}

func (s *recordingMessageSubmitter) QueueMessage(
	context.Context,
	int64,
	string,
) (outbox.Entry, error) {
	return outbox.Entry{}, errors.New("must not be called")
}

func (s *recordingMessageSubmitter) CancelMessage(
	_ context.Context,
	id outbox.ID,
	expectedVersion uint64,
) (outbox.Entry, error) {
	s.canceled++
	s.canceledID = id
	s.canceledVersion = expectedVersion
	if s.cancelErr != nil {
		return outbox.Entry{}, s.cancelErr
	}

	return outbox.Entry{ID: id}, nil
}

var _ MessageSubmitter = (*recordingMessageSubmitter)(nil)

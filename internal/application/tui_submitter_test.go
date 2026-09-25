package application

import (
	"context"
	"errors"
	"testing"

	"telecli/internal/outbox"
	"telecli/internal/tui"
)

type h5bTUIDelegate struct {
	accountKey string
	chatID     int64
	text       string
	result     MessageSubmission
	err        error
}

func (d *h5bTUIDelegate) QueueMessage(
	_ context.Context,
	chatID int64,
	text string,
) (outbox.Entry, error) {
	d.chatID = chatID
	d.text = text
	return outbox.Entry{}, d.err
}

func (d *h5bTUIDelegate) CancelMessage(
	context.Context,
	outbox.ID,
	uint64,
) (outbox.Entry, error) {
	return outbox.Entry{}, nil
}

func (d *h5bTUIDelegate) SubmitMessage(
	_ context.Context,
	accountKey string,
	chatID int64,
	text string,
) (MessageSubmission, error) {
	d.accountKey = accountKey
	d.chatID = chatID
	d.text = text
	return d.result, d.err
}

var _ MessageSubmitter = (*h5bTUIDelegate)(nil)
var _ tui.ComposerSubmitter = (*TUISubmitter)(nil)

func TestNewTUISubmitterRejectsNilDelegate(t *testing.T) {
	t.Parallel()

	if _, err := NewTUISubmitter(nil, "account-1"); err == nil {
		t.Fatal("NewTUISubmitter() error = nil, want non-nil")
	}
}

func TestTUISubmitterForwardsAccountAndMapsSubmission(t *testing.T) {
	t.Parallel()

	delegate := &h5bTUIDelegate{
		result: MessageSubmission{ID: "entry-1", State: MessageDeliveryQueued},
	}
	submitter, err := NewTUISubmitter(delegate, "account-1")
	if err != nil {
		t.Fatalf("NewTUISubmitter() error = %v", err)
	}

	got, err := submitter.SubmitMessage(
		context.Background(),
		42,
		"hello",
	)
	if err != nil {
		t.Fatalf("SubmitMessage() error = %v", err)
	}
	if delegate.accountKey != "account-1" || delegate.chatID != 42 || delegate.text != "hello" {
		t.Fatalf("forwarded args = (%q, %d, %q)", delegate.accountKey, delegate.chatID, delegate.text)
	}
	want := tui.Submission{ID: "entry-1", State: tui.SubmissionQueued}
	if got != want {
		t.Fatalf("SubmitMessage() = %#v, want %#v", got, want)
	}
}

func TestTUISubmitterPropagatesDeliveryError(t *testing.T) {
	t.Parallel()

	deliveryErr := errors.New("delivery unavailable")
	delegate := &h5bTUIDelegate{err: deliveryErr}
	submitter, err := NewTUISubmitter(delegate, "account-1")
	if err != nil {
		t.Fatalf("NewTUISubmitter() error = %v", err)
	}
	_, err = submitter.SubmitMessage(context.Background(), 42, "hello")
	if !errors.Is(err, deliveryErr) {
		t.Fatalf("SubmitMessage() error = %v, want %v", err, deliveryErr)
	}
}

package tui

import (
	"context"
	"reflect"
	"testing"
)

func TestMessageDeliveryStatesAreDistinctAndKnown(t *testing.T) {
	t.Parallel()

	states := []MessageDeliveryState{
		MessageDeliveryQueued,
		MessageDeliverySending,
		MessageDeliveryRetrying,
		MessageDeliveryFailed,
		MessageDeliveryUncertain,
		MessageDeliverySent,
		MessageDeliveryCanceled,
	}

	seen := make(map[MessageDeliveryState]struct{}, len(states))
	for _, state := range states {
		if state == "" {
			t.Fatal("message delivery state must not be empty")
		}
		if !state.IsKnown() {
			t.Fatalf("IsKnown() = false for supported state %q", state)
		}
		if _, exists := seen[state]; exists {
			t.Fatalf("duplicate message delivery state %q", state)
		}
		seen[state] = struct{}{}
	}
}

func TestMessageDeliveryStateIsKnownRejectsUnknownState(t *testing.T) {
	t.Parallel()

	if MessageDeliveryState("").IsKnown() {
		t.Fatal("IsKnown() = true for empty state")
	}
	if MessageDeliveryState("unknown").IsKnown() {
		t.Fatal("IsKnown() = true for unknown state")
	}
}

func TestMessageDeliveryStateTerminalClassification(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		state    MessageDeliveryState
		terminal bool
	}{
		{name: "queued", state: MessageDeliveryQueued, terminal: false},
		{name: "sending", state: MessageDeliverySending, terminal: false},
		{name: "retrying", state: MessageDeliveryRetrying, terminal: false},
		{name: "failed", state: MessageDeliveryFailed, terminal: true},
		// Uncertain is not terminal: §6.2 keeps it open until the user
		// decides, and the interface is that decision. A state the interface
		// calls terminal is a state it stops watching.
		{name: "uncertain", state: MessageDeliveryUncertain, terminal: false},
		{name: "sent", state: MessageDeliverySent, terminal: true},
		{name: "canceled", state: MessageDeliveryCanceled, terminal: true},
		{name: "unknown", state: MessageDeliveryState("unknown"), terminal: false},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := test.state.IsTerminal(); got != test.terminal {
				t.Fatalf("IsTerminal() = %v, want %v", got, test.terminal)
			}
		})
	}
}

func TestMessageStatusContainsExpectedMetadataOnly(t *testing.T) {
	t.Parallel()

	statusType := reflect.TypeOf(MessageStatus{})

	wantFields := []string{
		"EntryID",
		"AccountKey",
		"ChatID",
		"State",
		"Attempt",
		"NextAttemptAt",
		"UpdatedAt",
	}

	gotFields := make([]string, 0, statusType.NumField())
	for index := 0; index < statusType.NumField(); index++ {
		gotFields = append(gotFields, statusType.Field(index).Name)
	}

	if !reflect.DeepEqual(gotFields, wantFields) {
		t.Fatalf("MessageStatus fields = %#v, want %#v", gotFields, wantFields)
	}
}

func TestMessageStatusDoesNotExposePayloadFields(t *testing.T) {
	t.Parallel()

	statusType := reflect.TypeOf(MessageStatus{})

	for _, forbidden := range []string{
		"Text",
		"Payload",
		"EncryptedText",
		"Ciphertext",
		"Nonce",
		"Error",
		"ErrorMessage",
		"ProviderMessage",
		"TDLibError",
		"LeaseOwner",
	} {
		if _, exists := statusType.FieldByName(forbidden); exists {
			t.Fatalf("MessageStatus contains forbidden field %q", forbidden)
		}
	}
}

type h6cStubStatusSource struct {
	statuses []MessageStatus
	err      error
}

func (s *h6cStubStatusSource) ListMessageStatuses(
	context.Context,
	string,
	int64,
) ([]MessageStatus, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.statuses, nil
}

var _ MessageStatusSource = (*h6cStubStatusSource)(nil)

func TestMessageStatusSourceAcceptsNilForDirectMode(t *testing.T) {
	t.Parallel()

	model, err := NewModelWithDependencies(
		context.Background(),
		Dependencies{MessageSubmitter: &h5aComposerSubmitter{}},
	)
	if err != nil {
		t.Fatalf("NewModelWithDependencies() error = %v", err)
	}
	if model.messageStatuses != nil {
		t.Fatalf("message statuses = %#v, want nil", model.messageStatuses)
	}
}

func TestMessageStatusSourceIsRetainedWhenProvided(t *testing.T) {
	t.Parallel()

	source := &h6cStubStatusSource{}
	model, err := NewModelWithDependencies(
		context.Background(),
		Dependencies{
			MessageSubmitter: &h5aComposerSubmitter{},
			MessageStatuses:  source,
		},
	)
	if err != nil {
		t.Fatalf("NewModelWithDependencies() error = %v", err)
	}
	if model.messageStatuses != source {
		t.Fatal("model did not retain provided message status source")
	}
}

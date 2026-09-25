package application

import (
	"errors"
	"reflect"
	"testing"
)

func TestMessageDeliveryUnavailableIsSentinel(t *testing.T) {
	t.Parallel()

	wrapped := errors.Join(
		errors.New("runtime stopped"),
		ErrMessageDeliveryUnavailable,
	)
	if !errors.Is(wrapped, ErrMessageDeliveryUnavailable) {
		t.Fatal("errors.Is() = false, want true")
	}
}

func TestMessageStatusUnavailableIsSentinel(t *testing.T) {
	t.Parallel()

	wrapped := errors.Join(
		errors.New("status lookup failed"),
		ErrMessageStatusUnavailable,
	)
	if !errors.Is(wrapped, ErrMessageStatusUnavailable) {
		t.Fatal("errors.Is() = false, want true")
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
		"ErrorMessage",
		"ProviderMessage",
	} {
		if _, exists := statusType.FieldByName(forbidden); exists {
			t.Fatalf("MessageStatus contains forbidden field %q", forbidden)
		}
	}
}

func TestMessageDeliveryStatesAreDistinct(t *testing.T) {
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
		if _, exists := seen[state]; exists {
			t.Fatalf("duplicate message delivery state %q", state)
		}
		seen[state] = struct{}{}
	}
}

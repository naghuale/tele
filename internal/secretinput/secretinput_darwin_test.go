//go:build darwin && cgo

package secretinput

import (
	"errors"
	"testing"
)

// TestCopySecretReturnsTheSecret pins that the reader hands back the
// value that was typed. It used to zero its own return value, so every
// secret arrived as NUL bytes and Telegram rejected the stored API hash
// with API_ID_INVALID.
func TestCopySecretReturnsTheSecret(t *testing.T) {
	const secret = "0123456789abcdef0123456789abcdef"

	got, err := copySecretFromString(secret)
	if err != nil {
		t.Fatalf("copySecret: %v", err)
	}
	if string(got) != secret {
		t.Fatalf("copySecret returned %q, want the typed secret", got)
	}
}

func TestCopySecretRejectsEmpty(t *testing.T) {
	if _, err := copySecretFromString(""); !errors.Is(err, ErrEmpty) {
		t.Fatalf("error = %v, want ErrEmpty", err)
	}
}

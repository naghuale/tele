//go:build outbox_integration

package outbox

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

// TestOpenIntegrationPlatform exercises the platform KeyProvider
// together with AEADCipher and the SQLite store.
//
// It verifies creation, enqueue, close, reopen, and decryption. Missing
// key fail-closed behavior is covered by factory unit tests using a
// controllable provider.
//
// Platform-specific provider integration tests are responsible for
// deleting their test key items.
//
// Run with:
//
//	go test -tags outbox_integration ./internal/outbox/...
func TestOpenIntegrationPlatform(
	t *testing.T,
) {
	provider := NewPlatformKeyProvider()

	databaseID := fmt.Sprintf(
		"telecli-integration-%d",
		time.Now().UnixNano(),
	)

	cfg := Config{
		DataDir:    t.TempDir(),
		DatabaseID: databaseID,
		InstanceID: "integration",
	}

	deps := Deps{
		KeyProvider: provider,
	}

	box, err := Open(
		context.Background(),
		cfg,
		deps,
	)
	if err != nil {
		if errors.Is(
			err,
			ErrOutboxKeyProviderUnsupported,
		) || errors.Is(
			err,
			ErrOutboxKeyUnavailable,
		) || errors.Is(
			err,
			ErrOutboxKeyCreationFailed,
		) {
			t.Skipf(
				"platform key provider unavailable: %v",
				err,
			)
		}

		t.Fatalf(
			"Open: %v",
			err,
		)
	}

	entry := queuedEntry(
		"op-1",
		42,
		"hello",
	)

	if err := box.Store.Enqueue(
		context.Background(),
		entry,
	); err != nil {
		_ = box.Close()
		t.Fatalf(
			"Enqueue: %v",
			err,
		)
	}

	if err := box.Close(); err != nil {
		t.Fatalf(
			"Close: %v",
			err,
		)
	}

	reopened, err := Open(
		context.Background(),
		cfg,
		deps,
	)
	if err != nil {
		t.Fatalf(
			"reopen: %v",
			err,
		)
	}
	defer func() {
		_ = reopened.Close()
	}()

	got, err := reopened.Store.Get(
		context.Background(),
		entry.ID,
	)
	if err != nil {
		t.Fatal(err)
	}

	if got.Text != "hello" {
		t.Fatalf(
			"Text = %q, want hello",
			got.Text,
		)
	}

	if got.State != StateQueued {
		t.Fatalf(
			"State = %s, want queued",
			got.State,
		)
	}
}

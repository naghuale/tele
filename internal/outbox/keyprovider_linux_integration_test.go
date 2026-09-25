//go:build linux && secretservice_integration

package outbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

// TestLinuxKeyProviderIntegration exercises the real D-Bus Secret
// Service. It requires a running user session bus and the
// org.freedesktop.secrets name.
//
//	go test -tags secretservice_integration ./internal/outbox/...
func TestLinuxKeyProviderIntegration(t *testing.T) {
	provider := NewPlatformKeyProvider()

	databaseID := fmt.Sprintf(
		"telecli-test-%d", time.Now().UnixNano(),
	)

	t.Cleanup(func() {
		client := newDBusSecretServiceClient()
		_ = client.Delete(
			context.Background(),
			secretServiceAttrs(databaseID),
		)
	})

	created, err := provider.CreateKey(
		context.Background(), databaseID,
	)
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	loaded, err := provider.LoadKey(
		context.Background(), databaseID,
	)
	if err != nil {
		t.Fatalf("LoadKey: %v", err)
	}
	if !bytes.Equal(created, loaded) {
		t.Fatal("loaded key differs from created key")
	}

	_, err = provider.CreateKey(
		context.Background(), databaseID,
	)
	if !errors.Is(err, ErrOutboxKeyExists) {
		t.Fatalf("duplicate err = %v, want ErrOutboxKeyExists", err)
	}

	loadedAgain, err := provider.LoadKey(
		context.Background(), databaseID,
	)
	if err != nil {
		t.Fatalf("second LoadKey: %v", err)
	}
	if !bytes.Equal(created, loadedAgain) {
		t.Fatal("duplicate create changed stored key")
	}
}

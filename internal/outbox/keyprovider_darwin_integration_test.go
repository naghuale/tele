//go:build darwin && cgo && keychain_integration

package outbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestDarwinKeyProviderIntegration(
	t *testing.T,
) {
	databaseID := fmt.Sprintf(
		"telecli-test-%d",
		time.Now().UnixNano(),
	)

	client := nativeSecItemClient{}

	t.Cleanup(func() {
		result := client.deletePassword(
			keychainOutboxService,
			databaseID,
		)

		if result != secItemSuccess &&
			result != secItemNotFound {
			t.Errorf(
				"cleanup result = %d",
				result,
			)
		}
	})

	provider := NewPlatformKeyProvider()

	created, err := provider.CreateKey(
		context.Background(),
		databaseID,
	)
	if err != nil {
		t.Fatalf(
			"CreateKey: %v",
			err,
		)
	}

	loaded, err := provider.LoadKey(
		context.Background(),
		databaseID,
	)
	if err != nil {
		t.Fatalf(
			"LoadKey: %v",
			err,
		)
	}

	if !bytes.Equal(
		created,
		loaded,
	) {
		t.Fatal(
			"loaded key differs from created key",
		)
	}

	_, err = provider.CreateKey(
		context.Background(),
		databaseID,
	)
	if !errors.Is(
		err,
		ErrOutboxKeyExists,
	) {
		t.Fatalf(
			"duplicate error = %v, want ErrOutboxKeyExists",
			err,
		)
	}

	loadedAgain, err := provider.LoadKey(
		context.Background(),
		databaseID,
	)
	if err != nil {
		t.Fatalf(
			"second LoadKey: %v",
			err,
		)
	}

	if !bytes.Equal(
		created,
		loadedAgain,
	) {
		t.Fatal(
			"duplicate create changed stored key",
		)
	}
}

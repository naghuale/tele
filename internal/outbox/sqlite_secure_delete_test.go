package outbox

import (
	"context"
	"testing"
)

// TestSQLiteStoreEnablesSecureDelete pins that purged rows, which hold
// encrypted message text, are overwritten rather than left in free pages.
func TestSQLiteStoreEnablesSecureDelete(t *testing.T) {
	store, _ := sqliteFactory(t)

	var enabled int
	if err := store.(*sqliteStore).db.QueryRowContext(
		context.Background(),
		"PRAGMA secure_delete",
	).Scan(&enabled); err != nil {
		t.Fatalf("read secure_delete: %v", err)
	}
	if enabled != 1 {
		t.Fatalf("secure_delete = %d, want 1", enabled)
	}
}

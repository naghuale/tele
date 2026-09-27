package outbox

import "testing"

// TestMemoryStoreContract runs the backend-agnostic contract against the
// in-memory store, so both backends are held to the same behaviour.
func TestMemoryStoreContract(t *testing.T) {
	runStoreContract(t, func(*testing.T) Store {
		return NewMemoryStore()
	})
}

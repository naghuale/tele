package outbox

import (
	"context"
	"fmt"
)

// ReadOperationalSnapshot counts persisted entries per state.
//
// Only entry state is inspected: message bodies, account keys, chat IDs and
// error history are never read, and the count is taken under the store lock
// so the result is a consistent observation.
func (s *MemoryStore) ReadOperationalSnapshot(
	ctx context.Context,
) (OperationalSnapshot, error) {
	if s == nil {
		return OperationalSnapshot{}, fmt.Errorf(
			"outbox operational: nil memory store",
		)
	}
	if err := ctx.Err(); err != nil {
		return OperationalSnapshot{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return OperationalSnapshot{}, err
	}

	var snapshot OperationalSnapshot
	for id, entry := range s.entries {
		if err := ctx.Err(); err != nil {
			return OperationalSnapshot{}, err
		}
		if err := addOperationalCount(&snapshot, entry.State, 1); err != nil {
			return OperationalSnapshot{}, fmt.Errorf(
				"outbox operational: entry %q: %w",
				id,
				err,
			)
		}
	}

	return snapshot, nil
}

var _ OperationalSnapshotReader = (*MemoryStore)(nil)

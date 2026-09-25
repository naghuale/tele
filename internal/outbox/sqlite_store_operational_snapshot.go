package outbox

import (
	"context"
	"fmt"
)

// ReadOperationalSnapshot counts persisted entries per state.
//
// The aggregate selects the state column and a count only: message bodies,
// account keys, chat IDs, lease ownership, error history and database
// identity are never read, and payload decryption is never invoked. The
// existing outbox_ready_idx leads with the state column, so the grouping
// reuses it and no additional index or migration is required.
func (s *sqliteStore) ReadOperationalSnapshot(
	ctx context.Context,
) (OperationalSnapshot, error) {
	if s == nil || s.db == nil {
		return OperationalSnapshot{}, fmt.Errorf(
			"outbox operational: nil sqlite store",
		)
	}
	if err := ctx.Err(); err != nil {
		return OperationalSnapshot{}, err
	}

	rows, err := s.db.QueryContext(ctx, `
SELECT state, COUNT(*)
FROM outbox_entries
GROUP BY state
`)
	if err != nil {
		return OperationalSnapshot{}, fmt.Errorf(
			"outbox operational query: %w",
			err,
		)
	}
	defer rows.Close()

	var snapshot OperationalSnapshot
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return OperationalSnapshot{}, err
		}
		var (
			state string
			count int64
		)
		if err := rows.Scan(&state, &count); err != nil {
			return OperationalSnapshot{}, fmt.Errorf(
				"outbox operational scan: %w",
				err,
			)
		}
		if err := addOperationalCount(
			&snapshot,
			State(state),
			count,
		); err != nil {
			return OperationalSnapshot{}, fmt.Errorf(
				"outbox operational: %w",
				err,
			)
		}
	}
	if err := rows.Err(); err != nil {
		return OperationalSnapshot{}, fmt.Errorf(
			"outbox operational rows: %w",
			err,
		)
	}

	return snapshot, nil
}

var _ OperationalSnapshotReader = (*sqliteStore)(nil)

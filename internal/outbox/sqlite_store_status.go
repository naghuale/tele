package outbox

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strconv"
	"time"
)

func (s *sqliteStore) ListEntryStatuses(
	ctx context.Context,
	query ListEntryStatusesQuery,
) ([]EntryStatus, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("outbox status: nil sqlite store")
	}
	normalized, err := normalizeListEntryStatusesQuery(query)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(ctx, `
SELECT id, account_key, chat_id, state, telegram_message_id,
       attempt_count, next_attempt_ns, updated_at_ns, version
FROM outbox_entries
WHERE account_key = ?
  AND chat_id = ?
ORDER BY updated_at_ns DESC, id ASC
LIMIT ?
`, normalized.AccountKey, normalized.ChatID, normalized.Limit)
	if err != nil {
		return nil, fmt.Errorf("outbox status query: %w", err)
	}
	defer rows.Close()

	result := make([]EntryStatus, 0)
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		status, err := scanEntryStatus(rows)
		if err != nil {
			return nil, err
		}
		if err := validateEntryStatus(status); err != nil {
			// One record that cannot be reported is reported here and the
			// read answers with the rest. Failing the read meant a chat
			// whose delivery states never changed, with the reason in
			// neither the screen nor the log — the owner read a message
			// stuck on Queued for as long as the program ran and had
			// nothing to read.
			//
			// The entry identifier and the reason are what go in. No
			// message text is read here: the status projection is
			// payload-free for exactly this reason.
			s.log().Warn(
				"outbox status record not reported",
				slog.String("entry_id", status.ID),
				slog.String("chat_id", strconv.FormatInt(status.ChatID, 10)),
				slog.String("state", string(status.State)),
				slog.String("reason", SafeReason(err)),
			)
			continue
		}
		result = append(result, status)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("outbox status rows: %w", err)
	}
	return result, nil
}

func scanEntryStatus(row rowScanner) (EntryStatus, error) {
	var (
		id          string
		accountKey  string
		chatID      int64
		state       string
		messageID   sql.NullInt64
		attempt     int64
		nextAttempt sql.NullInt64
		updatedNS   int64
		version     int64
	)
	if err := row.Scan(
		&id,
		&accountKey,
		&chatID,
		&state,
		&messageID,
		&attempt,
		&nextAttempt,
		&updatedNS,
		&version,
	); err != nil {
		return EntryStatus{}, err
	}
	if attempt < 0 {
		return EntryStatus{}, fmt.Errorf(
			"%w: persisted status %s has negative attempt",
			ErrInvalidEntry,
			id,
		)
	}
	if version < 0 {
		return EntryStatus{}, fmt.Errorf(
			"%w: persisted status %s has negative version",
			ErrInvalidEntry,
			id,
		)
	}
	updatedAt := time.Time{}
	if updatedNS != 0 {
		updatedAt = time.Unix(0, updatedNS).UTC()
	}
	return EntryStatus{
		ID:                id,
		AccountKey:        accountKey,
		ChatID:            chatID,
		State:             State(state),
		TelegramMessageID: messageID.Int64,
		Attempt:           int(attempt),
		NextAttemptAt:     timeFromNull(nextAttempt),
		UpdatedAt:         updatedAt,
		Version:           uint64(version),
	}, nil
}

var _ EntryStatusReader = (*sqliteStore)(nil)

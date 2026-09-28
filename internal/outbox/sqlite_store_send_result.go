package outbox

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// ListAwaitingSendResult implements SendResultStore.
//
// The state column and the temporary message id are the lookup, and
// outbox_send_result_idx leads with both, so the scan touches only the
// entries that are actually waiting. The text is never read: the
// projection names entries and identifiers, and a consumer that could
// read what somebody wrote would be a second reader of the payload.
func (s *sqliteStore) ListAwaitingSendResult(
	ctx context.Context,
	accountKey string,
	limit int,
) ([]AwaitingSendResult, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf(
			"outbox send result: nil sqlite store",
		)
	}
	account, err := normalizeAccountKey(accountKey)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// A non-positive limit is "everything", which a single-account queue
	// is; the store therefore applies the cap in Go rather than
	// substituting a number the caller never asked for.
	rows, err := s.db.QueryContext(ctx, `
SELECT id, account_key, chat_id, telegram_message_id, accepted_at_ns,
       version
FROM outbox_entries
WHERE state = ? AND account_key = ?
ORDER BY accepted_at_ns ASC, id ASC
`, string(StateAccepted), account)
	if err != nil {
		return nil, fmt.Errorf(
			"outbox send result query: %w", err,
		)
	}
	defer rows.Close()

	result := make([]AwaitingSendResult, 0)
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		accepted, err := scanAwaitingSendResult(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, accepted)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("outbox send result rows: %w", err)
	}

	sortAwaiting(result)
	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

// ApplySendResult implements SendResultStore.
//
// The lookup and the write are one transaction: a result that arrives
// while another writer moves the entry is applied to the entry as it is
// at that moment, and the transition itself refuses anything that is no
// longer accepted. A message that is already sent is not sent twice, and
// one that was canceled before its result arrived is not resurrected.
func (s *sqliteStore) ApplySendResult(
	ctx context.Context,
	accountKey string,
	result SendResult,
	now time.Time,
) (Entry, error) {
	if s == nil || s.db == nil {
		return Entry{}, fmt.Errorf(
			"outbox send result: nil sqlite store",
		)
	}
	account, err := normalizeAccountKey(accountKey)
	if err != nil {
		return Entry{}, err
	}
	if err := validateSendResult(result); err != nil {
		return Entry{}, err
	}
	if err := ctx.Err(); err != nil {
		return Entry{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Entry{}, fmt.Errorf("outbox: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	row := tx.QueryRowContext(ctx, `
SELECT id, account_key, chat_id, encrypted_text, state, attempt_count,
       next_attempt_ns, telegram_message_id, last_error_code,
       last_error_message, created_at_ns, updated_at_ns, accepted_at_ns,
       sent_at_ns, lease_owner, lease_until_ns, version
FROM outbox_entries
WHERE state = ? AND account_key = ? AND telegram_message_id = ?
ORDER BY accepted_at_ns ASC, id ASC
LIMIT 1
`, string(StateAccepted), account, result.OldMessageID)

	entry, err := s.scanEntry(ctx, row)
	if err != nil {
		if sql.ErrNoRows == err || err == sql.ErrNoRows {
			return Entry{}, ErrNoAcceptedEntry
		}
		return Entry{}, err
	}

	out, err := applySendResultTo(entry, result, now)
	if err != nil {
		return Entry{}, fmt.Errorf(
			"outbox send result: entry %q: %w", entry.ID, err,
		)
	}

	if err := s.updateTx(ctx, tx, entry.Version, out); err != nil {
		return Entry{}, err
	}
	if err := tx.Commit(); err != nil {
		return Entry{}, fmt.Errorf("outbox: commit send result: %w", err)
	}
	return out, nil
}

func scanAwaitingSendResult(row rowScanner) (AwaitingSendResult, error) {
	var (
		id                string
		accountKey        string
		chatID            int64
		telegramMessageID int64
		acceptedAtNS      int64
		version           int64
	)

	if err := row.Scan(
		&id,
		&accountKey,
		&chatID,
		&telegramMessageID,
		&acceptedAtNS,
		&version,
	); err != nil {
		return AwaitingSendResult{}, err
	}
	if version < 0 {
		return AwaitingSendResult{}, fmt.Errorf(
			"%w: persisted entry %s has negative version",
			ErrInvalidEntry, id,
		)
	}

	return normalizeAccepted(AwaitingSendResult{
		ID:                ID(id),
		AccountKey:        accountKey,
		ChatID:            chatID,
		TelegramMessageID: telegramMessageID,
		AcceptedAt:        time.Unix(0, acceptedAtNS).UTC(),
		Version:           uint64(version),
	})
}

var _ SendResultStore = (*sqliteStore)(nil)

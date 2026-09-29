package outbox

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
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

// ListUnsettledAccepted implements UnsettledAcceptedStore.
func (s *sqliteStore) ListUnsettledAccepted(
	ctx context.Context,
	accountKey string,
	limit int,
) ([]UnsettledAccepted, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf(
			"outbox unsettled accepted: nil sqlite store",
		)
	}
	account, err := normalizeAccountKey(accountKey)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// The text is decrypted here and nowhere else on this path: the
	// settlement compares it with what the chat holds, and the
	// comparison is the last thing in the program that sees it.
	rows, err := s.db.QueryContext(ctx, `
SELECT id, account_key, chat_id, encrypted_text, accepted_at_ns, version
FROM outbox_entries
WHERE state = ? AND account_key = ?
ORDER BY accepted_at_ns ASC, id ASC
`, string(StateAccepted), account)
	if err != nil {
		return nil, fmt.Errorf(
			"outbox unsettled accepted query: %w", err,
		)
	}
	defer func() { _ = rows.Close() }()

	var entries []UnsettledAccepted
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		var (
			record UnsettledAccepted
			// accepted_at_ns is nullable in the schema and scanning a
			// NULL into an int64 fails the whole read. A row that is
			// accepted and has no accepted time is a row this build
			// cannot reason about, and the honest thing is to say which
			// row it is rather than to hand back a scan error that names
			// no column and no record.
			acceptedAt  sql.NullInt64
			accountText string
			idText      string
			ciphertext  []byte
		)
		if err := rows.Scan(
			&idText,
			&accountText,
			&record.ChatID,
			&ciphertext,
			&acceptedAt,
			&record.Version,
		); err != nil {
			return nil, fmt.Errorf(
				"outbox unsettled accepted scan: %w", err,
			)
		}

		record.ID = ID(idText)
		record.AccountKey = accountText
		if !acceptedAt.Valid {
			return nil, fmt.Errorf(
				"%w: %s is %s without an accepted time",
				ErrInvalidEntry, idText, StateAccepted,
			)
		}
		record.AcceptedAt = time.Unix(0, acceptedAt.Int64).UTC()

		plaintext, err := s.cipher.DecryptMessage(
			ctx, record.ID, record.AccountKey, record.ChatID, ciphertext,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"outbox: decrypt message %s: %w", record.ID, err,
			)
		}
		record.Text = string(plaintext)

		normalized, err := normalizeUnsettled(record)
		if err != nil {
			return nil, err
		}
		entries = append(entries, normalized)

		if limit > 0 && len(entries) >= limit {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf(
			"outbox unsettled accepted rows: %w", err,
		)
	}

	return entries, nil
}

// ListSettledUncertain implements UnsettledAcceptedStore.
func (s *sqliteStore) ListSettledUncertain(
	ctx context.Context,
	accountKey string,
	reason string,
	limit int,
) ([]UnsettledAccepted, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf(
			"outbox settled uncertain: nil sqlite store",
		)
	}
	account, err := normalizeAccountKey(accountKey)
	if err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(reason)
	if trimmed == "" {
		return nil, fmt.Errorf(
			"%w: the reason a settlement wrote is required to recognise "+
				"its own records", ErrInvalidEntry,
		)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(ctx, `
SELECT id, account_key, chat_id, encrypted_text, accepted_at_ns, version,
       telegram_message_id
FROM outbox_entries
WHERE state = ? AND account_key = ? AND last_error_message = ?
  AND telegram_message_id IS NOT NULL AND telegram_message_id != 0
ORDER BY accepted_at_ns ASC, id ASC
`, string(StateUncertain), account, trimmed)
	if err != nil {
		return nil, fmt.Errorf(
			"outbox settled uncertain query: %w", err,
		)
	}
	defer func() { _ = rows.Close() }()

	var entries []UnsettledAccepted
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		var (
			record        UnsettledAccepted
			idText        string
			account       string
			ciphertext    []byte
			acceptedAt    sql.NullInt64
			telegramIDRaw sql.NullInt64
		)
		if err := rows.Scan(
			&idText,
			&account,
			&record.ChatID,
			&ciphertext,
			&acceptedAt,
			&record.Version,
			&telegramIDRaw,
		); err != nil {
			return nil, fmt.Errorf(
				"outbox settled uncertain scan: %w", err,
			)
		}

		record.ID = ID(idText)
		record.AccountKey = account
		if !acceptedAt.Valid {
			return nil, fmt.Errorf(
				"%w: %s is uncertain without an accepted time, so it was "+
					"never accepted and is not a record to look at again",
				ErrInvalidEntry, idText,
			)
		}
		record.AcceptedAt = time.Unix(0, acceptedAt.Int64).UTC()
		record.TelegramMessageID = telegramIDRaw.Int64

		plaintext, err := s.cipher.DecryptMessage(
			ctx, record.ID, record.AccountKey, record.ChatID, ciphertext,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"outbox: decrypt message %s: %w", record.ID, err,
			)
		}
		record.Text = string(plaintext)

		normalized, err := normalizeSettledRecord(record)
		if err != nil {
			return nil, err
		}
		entries = append(entries, normalized)

		if limit > 0 && len(entries) >= limit {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("outbox settled uncertain rows: %w", err)
	}

	return entries, nil
}

var _ UnsettledAcceptedStore = (*sqliteStore)(nil)

package outbox

import (
	"context"
	"fmt"
	"time"
)

// ListAwaitingSendResult implements SendResultStore.
func (s *MemoryStore) ListAwaitingSendResult(
	ctx context.Context,
	accountKey string,
	limit int,
) ([]AwaitingSendResult, error) {
	if s == nil {
		return nil, fmt.Errorf("outbox send result: nil memory store")
	}
	account, err := normalizeAccountKey(accountKey)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	result := make([]AwaitingSendResult, 0, len(s.entries))
	for _, entry := range s.entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if entry.State != StateAccepted || entry.AccountKey != account {
			continue
		}
		accepted, err := acceptedFromEntry(entry)
		if err != nil {
			return nil, fmt.Errorf(
				"outbox send result: entry %q: %w", entry.ID, err,
			)
		}
		result = append(result, accepted)
	}

	sortAwaiting(result)
	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

// ApplySendResult implements SendResultStore.
func (s *MemoryStore) ApplySendResult(
	ctx context.Context,
	accountKey string,
	result SendResult,
	now time.Time,
) (Entry, error) {
	if s == nil {
		return Entry{}, fmt.Errorf(
			"outbox send result: nil memory store",
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

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return Entry{}, err
	}

	for _, entry := range s.entries {
		if entry.State != StateAccepted ||
			entry.AccountKey != account ||
			entry.TelegramMessageID != result.OldMessageID {
			continue
		}
		if err := ctx.Err(); err != nil {
			return Entry{}, err
		}

		out, err := applySendResultTo(entry, result, now)
		if err != nil {
			return Entry{}, fmt.Errorf(
				"outbox send result: entry %q: %w", entry.ID, err,
			)
		}
		s.entries[entry.ID] = out

		return out, nil
	}

	return Entry{}, ErrNoAcceptedEntry
}

// applySendResultTo is the transition a send result makes on an entry.
//
// It is shared by both stores so the two cannot disagree about what a
// confirmation does to a record: the temporary identifier is replaced
// with the final one, or the entry ends as a permanent failure carrying
// Telegram's own error code.
func applySendResultTo(
	entry Entry,
	result SendResult,
	now time.Time,
) (Entry, error) {
	if result.Failed {
		return entry.MarkSendFailed(
			result.ErrorCode,
			result.ErrorReason,
			now,
		)
	}
	return entry.Sent(result.MessageID, now)
}

var _ SendResultStore = (*MemoryStore)(nil)

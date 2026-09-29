package outbox

import (
	"context"
	"fmt"
	"strings"
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

// ListUnsettledAccepted implements UnsettledAcceptedStore.
func (s *MemoryStore) ListUnsettledAccepted(
	ctx context.Context,
	accountKey string,
	limit int,
) ([]UnsettledAccepted, error) {
	if s == nil {
		return nil, fmt.Errorf("outbox unsettled accepted: nil memory store")
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

	result := make([]UnsettledAccepted, 0, len(s.entries))
	for _, entry := range s.entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if entry.State != StateAccepted || entry.AccountKey != account {
			continue
		}
		unsettled, err := normalizeSettledRecord(UnsettledAccepted{
			ID:                entry.ID,
			AccountKey:        entry.AccountKey,
			ChatID:            entry.ChatID,
			Text:              entry.Text,
			AcceptedAt:        entry.AcceptedAt,
			TelegramMessageID: entry.TelegramMessageID,
			Version:           entry.Version,
		})
		if err != nil {
			return nil, fmt.Errorf(
				"outbox unsettled accepted: entry %q: %w",
				entry.ID, err,
			)
		}
		result = append(result, unsettled)
	}

	sortUnsettled(result)
	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

// ListSettledUncertain implements UnsettledAcceptedStore.
func (s *MemoryStore) ListSettledUncertain(
	ctx context.Context,
	accountKey string,
	reason string,
	limit int,
) ([]UnsettledAccepted, error) {
	if s == nil {
		return nil, fmt.Errorf("outbox settled uncertain: nil memory store")
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

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	var result []UnsettledAccepted
	for _, entry := range s.entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if entry.State != StateUncertain ||
			entry.AccountKey != account ||
			entry.LastErrorMessage != trimmed {
			continue
		}
		settled, err := normalizeSettledRecord(UnsettledAccepted{
			ID:                entry.ID,
			AccountKey:        entry.AccountKey,
			ChatID:            entry.ChatID,
			Text:              entry.Text,
			AcceptedAt:        entry.AcceptedAt,
			TelegramMessageID: entry.TelegramMessageID,
			Version:           entry.Version,
		})
		if err != nil {
			// A record with no message id was never accepted, so it was
			// never this queue's to re-check; it is not an error, and
			// refusing the whole read over it would strand the records
			// that are.
			continue
		}
		result = append(result, settled)
	}

	sortUnsettled(result)
	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

var _ UnsettledAcceptedStore = (*MemoryStore)(nil)

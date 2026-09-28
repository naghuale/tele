package outbox

import (
	"context"
	"fmt"
	"sort"
)

func (s *MemoryStore) ListEntryStatuses(
	ctx context.Context,
	query ListEntryStatusesQuery,
) ([]EntryStatus, error) {
	if s == nil {
		return nil, fmt.Errorf("outbox status: nil memory store")
	}
	normalized, err := normalizeListEntryStatusesQuery(query)
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

	result := make([]EntryStatus, 0, len(s.entries))
	for _, entry := range s.entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if entry.AccountKey != normalized.AccountKey || entry.ChatID != normalized.ChatID {
			continue
		}
		status := EntryStatus{
			ID:                string(entry.ID),
			AccountKey:        entry.AccountKey,
			ChatID:            entry.ChatID,
			State:             entry.State,
			TelegramMessageID: entry.TelegramMessageID,
			Attempt:           entry.AttemptCount,
			NextAttemptAt:     entry.NextAttempt,
			UpdatedAt:         entry.UpdatedAt,
			Version:           entry.Version,
		}
		if err := validateEntryStatus(status); err != nil {
			return nil, fmt.Errorf("validate outbox status %q: %w", status.ID, err)
		}
		result = append(result, status)
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].UpdatedAt.Equal(result[j].UpdatedAt) {
			return result[i].ID < result[j].ID
		}
		return result[i].UpdatedAt.After(result[j].UpdatedAt)
	})
	if len(result) > normalized.Limit {
		result = result[:normalized.Limit]
	}
	return result, nil
}

var _ EntryStatusReader = (*MemoryStore)(nil)

package outbox

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	DefaultEntryStatusLimit = 100
	MaxEntryStatusLimit     = 500
)

type EntryStatus struct {
	ID            string
	AccountKey    string
	ChatID        int64
	State         State
	Attempt       int
	NextAttemptAt time.Time
	UpdatedAt     time.Time
	Version       uint64
}

type ListEntryStatusesQuery struct {
	AccountKey string
	ChatID     int64
	Limit      int
}

type EntryStatusReader interface {
	ListEntryStatuses(
		ctx context.Context,
		query ListEntryStatusesQuery,
	) ([]EntryStatus, error)
}

func normalizeListEntryStatusesQuery(
	query ListEntryStatusesQuery,
) (ListEntryStatusesQuery, error) {
	if strings.TrimSpace(query.AccountKey) == "" {
		return ListEntryStatusesQuery{},
			errors.New("outbox status account key is required")
	}
	if query.ChatID == 0 {
		return ListEntryStatusesQuery{},
			errors.New("outbox status chat ID must not be zero")
	}
	if query.Limit < 0 {
		return ListEntryStatusesQuery{},
			errors.New("outbox status limit must not be negative")
	}
	if query.Limit == 0 {
		query.Limit = DefaultEntryStatusLimit
	}
	if query.Limit > MaxEntryStatusLimit {
		return ListEntryStatusesQuery{}, fmt.Errorf(
			"outbox status limit %d exceeds maximum %d",
			query.Limit,
			MaxEntryStatusLimit,
		)
	}
	return query, nil
}

func validateEntryStatus(status EntryStatus) error {
	if strings.TrimSpace(status.ID) == "" {
		return fmt.Errorf("%w: empty id", ErrInvalidEntry)
	}
	if strings.TrimSpace(status.AccountKey) == "" {
		return fmt.Errorf("%w: empty account key", ErrInvalidEntry)
	}
	if status.ChatID == 0 {
		return fmt.Errorf("%w: zero chat id", ErrInvalidEntry)
	}
	if !status.State.Valid() {
		return fmt.Errorf("%w: unknown state %q", ErrInvalidEntry, status.State)
	}
	if status.Attempt < 0 {
		return fmt.Errorf("%w: negative attempt", ErrInvalidEntry)
	}
	if status.UpdatedAt.IsZero() {
		return fmt.Errorf("%w: zero updated time", ErrInvalidEntry)
	}
	if status.State == StateFailedRetryable {
		if status.NextAttemptAt.IsZero() {
			return fmt.Errorf("%w: retryable without next attempt", ErrInvalidEntry)
		}
	} else if !status.NextAttemptAt.IsZero() {
		return fmt.Errorf("%w: next attempt outside retryable state", ErrInvalidEntry)
	}
	return nil
}

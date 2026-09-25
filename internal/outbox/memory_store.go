package outbox

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// MemoryStore is the in-memory implementation of Store.
//
// It exists so PR-08B can ship and be tested without committing to a
// durable backend. It is not intended for production use: entries are
// lost on process exit, which is the opposite of what a durable outbox
// is for.
type MemoryStore struct {
	mu      sync.Mutex
	entries map[ID]Entry
}

// NewMemoryStore returns an empty in-memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		entries: make(map[ID]Entry),
	}
}

// Enqueue implements Store.
func (s *MemoryStore) Enqueue(ctx context.Context, entry Entry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if entry.State != StateQueued {
		return fmt.Errorf(
			"%w: enqueue requires queued state, got %s",
			ErrInvalidEntry, entry.State,
		)
	}
	if err := entry.Validate(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return err
	}

	if _, exists := s.entries[entry.ID]; exists {
		return ErrDuplicateID
	}
	s.entries[entry.ID] = entry
	return nil
}

// Get implements Store.
func (s *MemoryStore) Get(ctx context.Context, id ID) (Entry, error) {
	if err := ctx.Err(); err != nil {
		return Entry{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return Entry{}, err
	}

	entry, exists := s.entries[id]
	if !exists {
		return Entry{}, ErrNotFound
	}
	return entry, nil
}

// ListReady implements Store.
func (s *MemoryStore) ListReady(
	ctx context.Context,
	now time.Time,
	limit int,
) ([]Entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		return nil, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	var ready []Entry
	for _, entry := range s.entries {
		if entry.IsReadyAt(now) {
			ready = append(ready, entry)
		}
	}
	sort.Slice(ready, func(i, j int) bool {
		if ready[i].CreatedAt.Equal(ready[j].CreatedAt) {
			return ready[i].ID < ready[j].ID
		}
		return ready[i].CreatedAt.Before(ready[j].CreatedAt)
	})
	if len(ready) > limit {
		ready = ready[:limit]
	}
	return ready, nil
}

// ListAll implements Store.
func (s *MemoryStore) ListAll(ctx context.Context) ([]Entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	out := make([]Entry, 0, len(s.entries))
	for _, entry := range s.entries {
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

// Claim implements Store.
func (s *MemoryStore) Claim(
	ctx context.Context,
	id ID,
	expectedVersion uint64,
	owner string,
	leaseUntil time.Time,
	now time.Time,
) (Entry, error) {
	if err := ctx.Err(); err != nil {
		return Entry{}, err
	}
	if strings.TrimSpace(owner) == "" {
		return Entry{}, fmt.Errorf(
			"%w: empty lease owner", ErrInvalidEntry,
		)
	}
	if !leaseUntil.After(now) {
		return Entry{}, fmt.Errorf(
			"%w: lease must expire after claim time", ErrInvalidEntry,
		)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return Entry{}, err
	}

	entry, err := s.loadLocked(id, expectedVersion)
	if err != nil {
		return Entry{}, err
	}

	if entry.State == StateDispatching &&
		entry.LeaseOwner != "" &&
		entry.LeaseOwner != owner &&
		entry.LeaseUntil.After(now) {
		return Entry{}, ErrLeaseHeld
	}

	claimed, err := entry.Claim(owner, leaseUntil, now)
	if err != nil {
		return Entry{}, err
	}
	s.entries[id] = claimed
	return claimed, nil
}

// MarkAccepted implements Store.
func (s *MemoryStore) MarkAccepted(
	ctx context.Context,
	id ID,
	expectedVersion uint64,
	messageID int64,
	now time.Time,
) (Entry, error) {
	return s.mutate(ctx, id, expectedVersion, func(e Entry) (Entry, error) {
		return e.Accept(messageID, now)
	})
}

// MarkRetryable implements Store.
func (s *MemoryStore) MarkRetryable(
	ctx context.Context,
	id ID,
	expectedVersion uint64,
	nextAttempt time.Time,
	code int,
	message string,
	now time.Time,
) (Entry, error) {
	return s.mutate(ctx, id, expectedVersion, func(e Entry) (Entry, error) {
		return e.MarkRetryable(nextAttempt, code, message, now)
	})
}

// MarkPermanentFailure implements Store.
func (s *MemoryStore) MarkPermanentFailure(
	ctx context.Context,
	id ID,
	expectedVersion uint64,
	code int,
	message string,
	now time.Time,
) (Entry, error) {
	return s.mutate(ctx, id, expectedVersion, func(e Entry) (Entry, error) {
		return e.MarkPermanentFailure(code, message, now)
	})
}

// MarkUncertain implements Store.
func (s *MemoryStore) MarkUncertain(
	ctx context.Context,
	id ID,
	expectedVersion uint64,
	reason string,
	now time.Time,
) (Entry, error) {
	return s.mutate(ctx, id, expectedVersion, func(e Entry) (Entry, error) {
		return e.MarkUncertain(reason, now)
	})
}

// Cancel implements Store.
func (s *MemoryStore) Cancel(
	ctx context.Context,
	id ID,
	expectedVersion uint64,
	now time.Time,
) (Entry, error) {
	return s.mutate(ctx, id, expectedVersion, func(e Entry) (Entry, error) {
		return e.Cancel(now)
	})
}

// RecoverInterrupted implements Store.
func (s *MemoryStore) RecoverInterrupted(
	ctx context.Context,
	now time.Time,
) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return 0, err
	}

	recovered := 0
	for id, entry := range s.entries {
		if entry.State != StateDispatching {
			continue
		}
		if entry.LeaseUntil.After(now) {
			continue
		}
		out, err := entry.MarkUncertain(
			"dispatch lease expired during recovery",
			now,
		)
		if err != nil {
			return recovered, err
		}
		s.entries[id] = out
		recovered++
	}
	return recovered, nil
}

// loadLocked returns the entry for id and verifies expectedVersion.
// The caller must hold s.mu.
func (s *MemoryStore) loadLocked(id ID, expectedVersion uint64) (Entry, error) {
	entry, exists := s.entries[id]
	if !exists {
		return Entry{}, ErrNotFound
	}
	if entry.Version != expectedVersion {
		return Entry{}, ErrVersionConflict
	}
	return entry, nil
}

// mutate applies fn under the store lock and persists the result.
func (s *MemoryStore) mutate(
	ctx context.Context,
	id ID,
	expectedVersion uint64,
	fn func(Entry) (Entry, error),
) (Entry, error) {
	if err := ctx.Err(); err != nil {
		return Entry{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return Entry{}, err
	}

	entry, err := s.loadLocked(id, expectedVersion)
	if err != nil {
		return Entry{}, err
	}
	out, err := fn(entry)
	if err != nil {
		return Entry{}, err
	}
	s.entries[id] = out
	return out, nil
}

// Compile-time assertion.
var _ Store = (*MemoryStore)(nil)

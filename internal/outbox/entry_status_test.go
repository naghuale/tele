package outbox

import (
	"testing"
	"time"
)

func TestNormalizeListEntryStatusesQueryUsesDefaultLimit(t *testing.T) {
	t.Parallel()

	got, err := normalizeListEntryStatusesQuery(ListEntryStatusesQuery{
		AccountKey: "account-1",
		ChatID:     42,
	})
	if err != nil {
		t.Fatalf("normalizeListEntryStatusesQuery() error = %v", err)
	}
	if got.Limit != DefaultEntryStatusLimit {
		t.Fatalf("limit = %d, want %d", got.Limit, DefaultEntryStatusLimit)
	}
}

func TestNormalizeListEntryStatusesQueryRejectsInvalidValues(t *testing.T) {
	t.Parallel()

	for _, query := range []ListEntryStatusesQuery{
		{AccountKey: " ", ChatID: 42},
		{AccountKey: "account-1", ChatID: 0},
		{AccountKey: "account-1", ChatID: 42, Limit: -1},
		{AccountKey: "account-1", ChatID: 42, Limit: MaxEntryStatusLimit + 1},
	} {
		if _, err := normalizeListEntryStatusesQuery(query); err == nil {
			t.Fatalf("normalizeListEntryStatusesQuery(%+v) error = nil, want non-nil", query)
		}
	}
}

func TestValidateEntryStatusRejectsInvalidMetadata(t *testing.T) {
	t.Parallel()

	valid := EntryStatus{
		ID:         "entry-1",
		AccountKey: "account-1",
		ChatID:     42,
		State:      StateQueued,
		UpdatedAt:  time.Unix(1700000000, 0).UTC(),
	}
	if err := validateEntryStatus(valid); err != nil {
		t.Fatalf("validateEntryStatus() error = %v", err)
	}

	invalid := []EntryStatus{
		{AccountKey: "account-1", ChatID: 42, State: StateQueued, UpdatedAt: valid.UpdatedAt},
		{ID: "entry-1", ChatID: 42, State: StateQueued, UpdatedAt: valid.UpdatedAt},
		{ID: "entry-1", AccountKey: "account-1", State: StateQueued, UpdatedAt: valid.UpdatedAt},
		{ID: "entry-1", AccountKey: "account-1", ChatID: 42, State: State("future"), UpdatedAt: valid.UpdatedAt},
		{ID: "entry-1", AccountKey: "account-1", ChatID: 42, State: StateQueued, Attempt: -1, UpdatedAt: valid.UpdatedAt},
		{ID: "entry-1", AccountKey: "account-1", ChatID: 42, State: StateQueued},
		{ID: "entry-1", AccountKey: "account-1", ChatID: 42, State: StateQueued, UpdatedAt: valid.UpdatedAt, NextAttemptAt: valid.UpdatedAt},
		{ID: "entry-1", AccountKey: "account-1", ChatID: 42, State: StateFailedRetryable, UpdatedAt: valid.UpdatedAt},
	}
	for _, status := range invalid {
		if err := validateEntryStatus(status); err == nil {
			t.Fatalf("validateEntryStatus(%+v) error = nil, want non-nil", status)
		}
	}
}

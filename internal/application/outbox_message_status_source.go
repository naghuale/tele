package application

import (
	"context"
	"errors"
	"fmt"

	"telecli/internal/outbox"
)

// OutboxMessageStatusSource exposes durable outbox delivery status metadata
// to the presentation layer without reading stored message payloads.
type OutboxMessageStatusSource struct {
	reader outbox.EntryStatusReader
	limit  int
}

func NewOutboxMessageStatusSource(
	reader outbox.EntryStatusReader,
	limit int,
) (*OutboxMessageStatusSource, error) {
	if reader == nil {
		return nil, errors.New(
			"outbox message status source: entry status reader is required",
		)
	}
	if limit < 0 {
		return nil, errors.New(
			"outbox message status source: limit must not be negative",
		)
	}
	return &OutboxMessageStatusSource{reader: reader, limit: limit}, nil
}

func (s *OutboxMessageStatusSource) ListMessageStatuses(
	ctx context.Context,
	accountKey string,
	chatID int64,
) ([]MessageStatus, error) {
	if s == nil || s.reader == nil {
		return nil, errors.New(
			"outbox message status source: entry status reader is required",
		)
	}
	if ctx == nil {
		return nil, errors.New(
			"outbox message status source: context is required",
		)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	entries, err := s.reader.ListEntryStatuses(
		ctx,
		outbox.ListEntryStatusesQuery{
			AccountKey: accountKey,
			ChatID:     chatID,
			Limit:      s.limit,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("list outbox entry statuses: %w", err)
	}

	statuses := make([]MessageStatus, 0, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		status, err := projectEntryStatus(entry)
		if err != nil {
			return nil, fmt.Errorf("list outbox entry statuses: %w", err)
		}
		statuses = append(statuses, status)
	}

	return statuses, nil
}

var _ MessageStatusSource = (*OutboxMessageStatusSource)(nil)

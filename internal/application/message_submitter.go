package application

import (
	"context"
	"errors"
	"strings"

	"telecli/internal/outbox"
)

var (
	// ErrSubmitterNilStore is returned when a nil Store is passed to
	// the constructor.
	ErrSubmitterNilStore = errors.New("submitter: nil store")

	// ErrSubmitterNilIDGen is returned when a nil ID generator is
	// passed to the constructor.
	ErrSubmitterNilIDGen = errors.New("submitter: nil id generator")

	// ErrSubmitterEmptyAccount is returned when the account key is
	// empty or whitespace-only.
	ErrSubmitterEmptyAccount = errors.New("submitter: empty account key")

	// ErrSubmitterInvalidChatID is returned when chatID is zero.
	// Negative chat IDs are accepted: TDLib treats chat_id as int53.
	ErrSubmitterInvalidChatID = errors.New("submitter: invalid chat id")

	// ErrSubmitterEmptyText is returned when text is empty or
	// whitespace-only.
	ErrSubmitterEmptyText = errors.New("submitter: empty text")
)

// MessageSubmitter is the application-facing contract for queuing
// outgoing messages.
//
// The interface deliberately does not expose retry. Manual retry of
// failed_permanent or uncertain entries requires an explicit UX policy
// because it may create a duplicate already accepted by Telegram.
// That policy is added in a later PR.
//
// QueueMessage persists the message in StateQueued and returns the
// stored Entry. It does not wait for dispatch: the notifier seam wakes
// the dispatcher out of band.
type MessageSubmitter interface {
	QueueMessage(
		ctx context.Context,
		chatID int64,
		text string,
	) (outbox.Entry, error)

	CancelMessage(
		ctx context.Context,
		id outbox.ID,
		expectedVersion uint64,
	) (outbox.Entry, error)
}

// OutboxIDGenerator returns a new unique outbox ID.
//
// It is a function type so tests can inject deterministic values and
// simulate generator failures.
type OutboxIDGenerator func() (outbox.ID, error)

// DispatchNotifier is notified after a successful enqueue. It must not
// block. A production dispatcher implementation may use a buffered
// channel of size 1 so that several enqueues coalesce into one
// wake-up.
type DispatchNotifier interface {
	Notify()
}

// noopNotifier ignores notifications. It is used when the caller
// passes a nil notifier.
type noopNotifier struct{}

func (noopNotifier) Notify() {}

// OutboxMessageSubmitter implements MessageSubmitter over an
// outbox.Store.
//
// It is the single application-level entry point for queueing outgoing
// messages. It does not connect to production composition in this
// revision: the store is expected to be a development MemoryStore
// until ADR-0002 selects a durable backend.
type OutboxMessageSubmitter struct {
	store      outbox.Store
	clock      outbox.Clock
	newID      OutboxIDGenerator
	notifier   DispatchNotifier
	accountKey string
}

// NewOutboxMessageSubmitter wires a submitter.
//
// store and newID are required. clock and notifier may be nil; they
// are substituted with SystemClock and a no-op notifier respectively.
// accountKey must be non-blank.
func NewOutboxMessageSubmitter(
	store outbox.Store,
	clock outbox.Clock,
	newID OutboxIDGenerator,
	notifier DispatchNotifier,
	accountKey string,
) (*OutboxMessageSubmitter, error) {
	if store == nil {
		return nil, ErrSubmitterNilStore
	}
	if newID == nil {
		return nil, ErrSubmitterNilIDGen
	}
	if strings.TrimSpace(accountKey) == "" {
		return nil, ErrSubmitterEmptyAccount
	}
	if clock == nil {
		clock = outbox.SystemClock{}
	}
	if notifier == nil {
		notifier = noopNotifier{}
	}

	return &OutboxMessageSubmitter{
		store:      store,
		clock:      clock,
		newID:      newID,
		notifier:   notifier,
		accountKey: accountKey,
	}, nil
}

// QueueMessage persists a queued entry for the given chat.
//
// Validation uses strings.TrimSpace to detect blank text, but the
// stored payload is the caller's text verbatim: leading and trailing
// whitespace inside a meaningful message are preserved.
//
// The notifier is called only after Store.Enqueue succeeds. Failed
// enqueues never wake the dispatcher.
func (s *OutboxMessageSubmitter) QueueMessage(
	ctx context.Context,
	chatID int64,
	text string,
) (outbox.Entry, error) {
	if err := ctx.Err(); err != nil {
		return outbox.Entry{}, err
	}
	if chatID == 0 {
		return outbox.Entry{}, ErrSubmitterInvalidChatID
	}
	if strings.TrimSpace(text) == "" {
		return outbox.Entry{}, ErrSubmitterEmptyText
	}

	id, err := s.newID()
	if err != nil {
		return outbox.Entry{}, err
	}

	now := s.clock.Now()

	entry := outbox.Entry{
		ID:         id,
		AccountKey: s.accountKey,
		ChatID:     chatID,
		Text:       text,
		State:      outbox.StateQueued,
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	if err := s.store.Enqueue(ctx, entry); err != nil {
		return outbox.Entry{}, err
	}

	s.notifier.Notify()

	return entry, nil
}

// CancelMessage cancels a queued, retryable, or uncertain entry.
//
// expectedVersion comes from the Entry the caller most recently read.
// Cancel is rejected for dispatching, accepted, permanently failed,
// and already canceled entries.
func (s *OutboxMessageSubmitter) CancelMessage(
	ctx context.Context,
	id outbox.ID,
	expectedVersion uint64,
) (outbox.Entry, error) {
	if err := ctx.Err(); err != nil {
		return outbox.Entry{}, err
	}

	return s.store.Cancel(ctx, id, expectedVersion, s.clock.Now())
}

// Compile-time assertion.
var _ MessageSubmitter = (*OutboxMessageSubmitter)(nil)

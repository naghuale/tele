package outbox

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// This file is the part of a store a send-result consumer needs.
//
// TDLib answers sendMessage with a *temporary* message identifier and
// replaces it with the final one only when Telegram confirms the send,
// in updateMessageSendSucceeded. An entry in StateAccepted therefore
// holds an identifier nothing else in the program can name: the history
// will come back with a different one, and a second send of the same
// text has a different one again. The only thing that links the two is
// that the confirmation carries the temporary identifier as
// old_message_id.
//
// SendResultStore is that link. It is a separate capability on purpose:
// a store that cannot correlate a send result is still a perfectly good
// queue, and forcing the capability on the transactional Store contract
// would make every backend carry a lookup the dispatcher never makes.

// AwaitingSendResult is an entry TDLib has accepted and Telegram has not
// answered about yet.
//
// It is payload-free: it names the entry, the chat and the temporary
// message identifier, and never the text, so a diagnostic or a log line
// built from it says numbers and nothing somebody wrote.
type AwaitingSendResult struct {
	// ID is the queue entry waiting for the send result.
	ID ID

	// AccountKey scopes the entry to one authenticated account.
	AccountKey string

	// ChatID is the chat the message was sent to.
	ChatID int64

	// TelegramMessageID is the temporary identifier sendMessage
	// returned. It is what updateMessageSendSucceeded and
	// updateMessageSendFailed carry back as old_message_id, and it is
	// never a message anybody else can name.
	TelegramMessageID int64

	// AcceptedAt is when TDLib took the message, which is the same
	// instant every TDLib error is measured from.
	AcceptedAt time.Time

	// Version is the version the store held this entry at, and it is
	// what a send result is applied against.
	Version uint64
}

// SendResult is what Telegram said about a message TDLib was sending.
type SendResult struct {
	// OldMessageID is the temporary identifier from the sendMessage
	// response, as it arrives in old_message_id.
	OldMessageID int64

	// MessageID is the final identifier Telegram assigned. It is zero
	// for a failure, where there is no message.
	MessageID int64

	// Failed reports that Telegram refused the message.
	Failed bool

	// ErrorCode is TDLib's own code for the refusal, or zero.
	ErrorCode int

	// ErrorReason is a short description that is free of message text,
	// for the persisted last error.
	ErrorReason string
}

// SendResultStore is the part of a durable store that a send-result
// consumer reads and writes.
type SendResultStore interface {
	// ListAwaitingSendResult returns the entries of an account that
	// TDLib has accepted and Telegram has not answered about, oldest
	// acceptance first.
	//
	// limit caps the result set. A non-positive limit returns
	// everything, which is what a queue of a single account is.
	ListAwaitingSendResult(
		ctx context.Context,
		accountKey string,
		limit int,
	) ([]AwaitingSendResult, error)

	// ApplySendResult records what Telegram said about a message this
	// queue sent.
	//
	// The entry is found by the temporary identifier in result, and the
	// transition is refused unless that entry is still accepted: a
	// result for a message that is no longer being sent is a result
	// about nothing, and applying it would move a record nobody is
	// waiting on. Applying the same result twice is not an error and
	// changes nothing, because the second application finds the entry
	// in a state the transition does not allow and returns the entry
	// as it is.
	ApplySendResult(
		ctx context.Context,
		accountKey string,
		result SendResult,
		now time.Time,
	) (Entry, error)
}

// ErrNoAcceptedEntry is returned by ApplySendResult when no entry is
// waiting for a send result with that temporary identifier.
//
// It is a normal answer rather than a failure: the result may belong to
// a message another client sent, to one this queue already resolved, or
// to one the retention window has already removed.
var ErrNoAcceptedEntry = fmt.Errorf(
	"%w: no accepted entry awaits this send result",
	ErrInvalidTransition,
)

// normalizeAccountKey rejects an account key the store cannot scope a
// send result by.
func normalizeAccountKey(accountKey string) (string, error) {
	trimmed := strings.TrimSpace(accountKey)
	if trimmed == "" {
		return "", errors.New(
			"outbox send result: account key is required",
		)
	}
	return trimmed, nil
}

// normalizeAccepted reports a malformed AwaitingSendResult.
//
// An entry with no identifier could not be matched to any send result,
// and one with no id could not be written back at all: both mean the row
// is not what the caller thinks it is.
func normalizeAccepted(
	entry AwaitingSendResult,
) (AwaitingSendResult, error) {
	if entry.ID == "" {
		return AwaitingSendResult{}, fmt.Errorf(
			"%w: empty id", ErrInvalidEntry,
		)
	}
	if strings.TrimSpace(entry.AccountKey) == "" {
		return AwaitingSendResult{}, fmt.Errorf(
			"%w: empty account key", ErrInvalidEntry,
		)
	}
	if entry.ChatID == 0 {
		return AwaitingSendResult{}, fmt.Errorf(
			"%w: zero chat id", ErrInvalidEntry,
		)
	}
	if entry.TelegramMessageID == 0 {
		return AwaitingSendResult{}, fmt.Errorf(
			"%w: %s has no temporary message id",
			ErrInvalidEntry, entry.ID,
		)
	}
	if entry.AcceptedAt.IsZero() {
		return AwaitingSendResult{}, fmt.Errorf(
			"%w: %s has no accepted time",
			ErrInvalidEntry, entry.ID,
		)
	}
	return entry, nil
}

// acceptedFromEntry is the payload-free projection of an accepted entry.
func acceptedFromEntry(entry Entry) (AwaitingSendResult, error) {
	return normalizeAccepted(AwaitingSendResult{
		ID:                entry.ID,
		AccountKey:        entry.AccountKey,
		ChatID:            entry.ChatID,
		TelegramMessageID: entry.TelegramMessageID,
		AcceptedAt:        entry.AcceptedAt,
		Version:           entry.Version,
	})
}

// sortAwaiting orders entries oldest acceptance first, so the consumer
// resolves the message that has been in the queue longest before a newer
// one.
func sortAwaiting(entries []AwaitingSendResult) {
	sort.SliceStable(entries, func(i, j int) bool {
		if !entries[i].AcceptedAt.Equal(entries[j].AcceptedAt) {
			return entries[i].AcceptedAt.Before(entries[j].AcceptedAt)
		}
		return entries[i].ID < entries[j].ID
	})
}

// validateSendResult checks what a consumer is about to apply.
//
// A result with neither a message id nor a failure flag says nothing
// about anything, and one that is both a failure and a message id is a
// contradiction this program cannot resolve in the transport's favour.
func validateSendResult(result SendResult) error {
	if result.OldMessageID == 0 {
		return fmt.Errorf(
			"%w: send result without old message id", ErrInvalidEntry,
		)
	}
	if result.Failed {
		if result.MessageID != 0 {
			return fmt.Errorf(
				"%w: failed send result carries a message id",
				ErrInvalidEntry,
			)
		}
		return nil
	}
	if result.MessageID == 0 {
		return fmt.Errorf(
			"%w: successful send result without message id",
			ErrInvalidEntry,
		)
	}
	return nil
}

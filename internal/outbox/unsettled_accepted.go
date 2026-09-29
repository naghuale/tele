package outbox

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// This file is what a later run uses on records an earlier one left
// behind.
//
// TDLib delivers a send result exactly once, to the process that was
// running when the message went out. A process that is closed — or
// crashes, or is killed — between TDLib taking a message and Telegram
// answering about it leaves an accepted record holding a temporary
// identifier that nothing will ever confirm: a new TDLib client will not
// send the update again, because to it the message is simply a message
// that is already in the chat.
//
// Such a record must not be left as it is. It is drawn as "on its way
// out", and after enough time that is a lie about Telegram rather than a
// fact about the queue. So the next run settles it: it looks the message
// up in the chat and either finds it, in which case the record becomes
// sent with the identifier the history will come back with, or admits it
// cannot find it, in which case the record becomes uncertain and says so.

// UnsettledAccepted is an accepted record this process did not send and
// has no confirmation for.
//
// It carries the text, unlike AwaitingSendResult, and that is the whole
// reason it is a separate projection: finding the message in a chat
// means comparing what the user wrote with what Telegram holds, and there
// is no way to do that without the text. The text is read here and
// matched here; it is never logged, never counted and never leaves this
// process in a diagnostic.
type UnsettledAccepted struct {
	// ID is the queue entry to settle.
	ID ID

	// AccountKey scopes the entry to one authenticated account.
	AccountKey string

	// ChatID is the chat the message was sent to, and the only chat
	// worth looking in.
	ChatID int64

	// Text is what the user wrote, as the record holds it.
	Text string

	// AcceptedAt is when TDLib took the message, and it is the anchor
	// for the time window a match has to fall in: Telegram dates the
	// message it now holds at the moment TDLib took it.
	AcceptedAt time.Time

	// TelegramMessageID is the temporary identifier TDLib handed out.
	//
	// It names nothing outside this queue, so it is not what a match is
	// made on — the text and the time are, because those are what the
	// chat can be asked about. It is here because a record that was
	// marked uncertain by an earlier settlement has to be recognisable as
	// one that was accepted, and a record with no identifier was never
	// accepted.
	TelegramMessageID int64

	// Version is the version the store held this entry at, and it is
	// what the settlement is written against.
	Version uint64

	// PreviouslyUncertain says the record is one a settlement already
	// marked uncertain, rather than one still waiting for its first
	// result.
	//
	// The two are looked at for different reasons. A record that is
	// accepted is waiting for an answer that is not coming, and looking
	// is the only thing that settles it. A record a settlement marked
	// uncertain has already been looked for and not found, so it is here
	// to be given a second look by a lookup that may have been wrong —
	// and it is the one record this queue promised to try again.
	PreviouslyUncertain bool
}

// UnsettledAcceptedStore is the part of a durable store that restart
// settlement reads.
//
// MarkSent and MarkUncertain are already on Store, so this capability is
// one method: the read that has to be told apart from the payload-free
// one, because it is the only read on this path that carries a message.
type UnsettledAcceptedStore interface {
	// ListUnsettledAccepted returns the entries of an account that are
	// accepted, oldest acceptance first.
	//
	// limit caps the result set. A non-positive limit returns
	// everything, which is what a queue of a single account is.
	ListUnsettledAccepted(
		ctx context.Context,
		accountKey string,
		limit int,
	) ([]UnsettledAccepted, error)

	// ListSettledUncertain returns the entries of an account that a
	// settlement marked uncertain with exactly this reason, oldest
	// acceptance first.
	//
	// The reason is the whole of the selection. An uncertain record is
	// either one a settlement could not find in its chat or one whose
	// lease was lost mid-send, and the two are not the same question: the
	// first is this queue's to ask again, the second is the user's to
	// answer. Matching on the sentence the settlement wrote is what keeps
	// the second out, and a settlement that cannot recognise its own
	// records cannot be corrected by a later, better lookup.
	//
	// Only records holding a message identifier are returned. An accepted
	// record always holds one, and a record without one was never
	// accepted and so was never this queue's to re-check.
	ListSettledUncertain(
		ctx context.Context,
		accountKey string,
		reason string,
		limit int,
	) ([]UnsettledAccepted, error)
}

// normalizeSettled reports a malformed re-check record.
func normalizeSettled(
	entry UnsettledAccepted,
) (UnsettledAccepted, error) {
	normalized, err := normalizeUnsettled(entry)
	if err != nil {
		return UnsettledAccepted{}, err
	}
	if normalized.TelegramMessageID == 0 {
		return UnsettledAccepted{}, fmt.Errorf(
			"%w: %s has no message id, so it was never accepted",
			ErrInvalidEntry, normalized.ID,
		)
	}

	normalized.PreviouslyUncertain = true

	return normalized, nil
}

// normalizeUnsettled reports a malformed UnsettledAccepted.
//
// A record with no chat could not be looked up in anything, and one with
// no accepted time has no window to look in: both mean the row is not
// what the caller thinks it is, and settling it would either match
// nothing or match something at random.
func normalizeUnsettled(
	entry UnsettledAccepted,
) (UnsettledAccepted, error) {
	if entry.ID == "" {
		return UnsettledAccepted{}, fmt.Errorf(
			"%w: empty id", ErrInvalidEntry,
		)
	}
	if strings.TrimSpace(entry.AccountKey) == "" {
		return UnsettledAccepted{}, fmt.Errorf(
			"%w: empty account key", ErrInvalidEntry,
		)
	}
	if entry.ChatID == 0 {
		return UnsettledAccepted{}, fmt.Errorf(
			"%w: zero chat id", ErrInvalidEntry,
		)
	}
	if entry.AcceptedAt.IsZero() {
		return UnsettledAccepted{}, fmt.Errorf(
			"%w: %s has no accepted time",
			ErrInvalidEntry, entry.ID,
		)
	}
	return entry, nil
}

// normalizeSettledRecord is what both stores hand to the re-check: a
// record a settlement marked uncertain, read back with its text.
func normalizeSettledRecord(
	record UnsettledAccepted,
) (UnsettledAccepted, error) {
	return normalizeSettled(record)
}

// sortUnsettled orders records oldest acceptance first, so the message
// that has been waiting longest is settled first and the shortest window
// into the chat's history is spent on it.
func sortUnsettled(entries []UnsettledAccepted) {
	sort.SliceStable(entries, func(i, j int) bool {
		if !entries[i].AcceptedAt.Equal(entries[j].AcceptedAt) {
			return entries[i].AcceptedAt.Before(entries[j].AcceptedAt)
		}
		return entries[i].ID < entries[j].ID
	})
}

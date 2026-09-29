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

	// Version is the version the store held this entry at, and it is
	// what the settlement is written against.
	Version uint64
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

// unsettledFromEntry projects an accepted entry for settlement.
func unsettledFromEntry(entry Entry) (UnsettledAccepted, error) {
	return normalizeUnsettled(UnsettledAccepted{
		ID:         entry.ID,
		AccountKey: entry.AccountKey,
		ChatID:     entry.ChatID,
		Text:       entry.Text,
		AcceptedAt: entry.AcceptedAt,
		Version:    entry.Version,
	})
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

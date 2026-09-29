package application

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"telecli/internal/outbox"
	"telecli/internal/telegram"
)

// What a later run does with the records an earlier one left accepted.
//
// TDLib delivers a send result once, to the process that was running
// when the message went out. Nothing re-delivers it: a new client sees a
// message that is simply in the chat, and the temporary identifier the
// record holds is meaningless to it. So a record left accepted by a
// process that is gone has no future event at all, and the only two
// honest states it can reach are sent — the message turned up in the
// chat — and uncertain, which says the queue cannot say.
//
// The alternative, which is what this replaces, is to leave it accepted
// and let the interface draw "on its way out" for as long as the program
// runs. After a day that is not a slow send; it is a claim about Telegram
// that nothing in the program can support.

// TelegramHistoryReader is the part of a session the settlement reads.
//
// It is separate from TelegramSender on purpose: sending and looking a
// message up are different capabilities, and a settlement that could
// only be built where a sender exists would never run in the one place it
// is needed — a program that is starting up to find out what it left
// behind.
type TelegramHistoryReader interface {
	GetChatHistory(
		ctx context.Context,
		chatID telegram.ChatID,
		fromMessageID telegram.MessageID,
		limit int,
	) (telegram.HistoryPage, error)
}

// settlementWindow is how far from the moment TDLib took the message a
// matching message may be dated.
//
// Telegram dates a message at the moment it was sent, and the record
// holds the moment TDLib took it, so the two are seconds apart. The
// window is generous because being wrong in one direction is much worse
// than the other: too narrow and a message that really did go out is
// called uncertain, which asks the user to look; too wide and two
// identical messages sent close together could be crossed, which would
// tell the user the wrong one went out. Five minutes is far wider than
// any plausible send latency and far narrower than "the same text again
// an hour later".
const settlementWindow = 5 * time.Minute

// settlementHistoryLimit is how many messages of a chat are read to
// settle its records.
//
// One page is what TDLib serves in a single request, and the records
// being settled were all sent by this queue, so they are the newest
// messages of the chat unless the user has sent a great deal since. A
// record that is not in the newest hundred is not going to be found by
// reading more: it would be settled as uncertain, which is the honest
// answer for a message that old.
const settlementHistoryLimit = 100

// restartSettler settles the records a previous process left accepted.
type restartSettler struct {
	store   outbox.UnsettledAcceptedStore
	entries outbox.Store
	history TelegramHistoryReader

	accountKey string
	clock      outbox.Clock
	logger     *slog.Logger

	// now is overridable so a test can date the settlement.
	now func() time.Time
}

// settlementCounts is what one settlement did, in numbers.
type settlementCounts struct {
	// considered is how many accepted records were looked at.
	considered int

	// sent is how many were found in their chat and marked sent.
	sent int

	// uncertain is how many could not be found and were marked
	// uncertain.
	uncertain int

	// unreadable is how many records could not be settled at all,
	// because the chat could not be read. These stay accepted: a
	// history that could not be fetched is not evidence that the
	// message is missing, and turning a read failure into "uncertain"
	// would ask the user about a message that is simply unread right
	// now.
	unreadable int
}

// Settle resolves every accepted record this process did not send.
//
// It runs once, at startup, before the dispatcher takes anything new. A
// record it cannot settle because the chat could not be read is left
// accepted and counted, and the next run tries again — the alternative,
// guessing, would be worse than the state it is trying to improve.
func (s *restartSettler) Settle(ctx context.Context) (settlementCounts, error) {
	var counts settlementCounts

	if s == nil || s.store == nil || s.entries == nil {
		return counts, errors.New(
			"restart settlement: store is required",
		)
	}

	unsettled, err := s.store.ListUnsettledAccepted(ctx, s.accountKey, 0)
	if err != nil {
		return counts, fmt.Errorf(
			"restart settlement: list accepted records: %w", err,
		)
	}
	if len(unsettled) == 0 {
		return counts, nil
	}
	counts.considered = len(unsettled)

	if s.history == nil {
		// Without a session there is no chat to look in, and a record
		// that cannot be looked up cannot honestly be called missing.
		counts.unreadable = len(unsettled)
		return counts, nil
	}

	// One read of a chat settles every record waiting in it.
	byChat := make(map[int64][]outbox.UnsettledAccepted, len(unsettled))
	for _, record := range unsettled {
		byChat[record.ChatID] = append(byChat[record.ChatID], record)
	}

	for chatID, records := range byChat {
		if err := ctx.Err(); err != nil {
			return counts, err
		}
		if err := s.settleChat(ctx, chatID, records, &counts); err != nil {
			return counts, err
		}
	}

	return counts, nil
}

// settleChat settles the records waiting in one chat.
func (s *restartSettler) settleChat(
	ctx context.Context,
	chatID int64,
	records []outbox.UnsettledAccepted,
	counts *settlementCounts,
) error {
	page, err := s.history.GetChatHistory(
		ctx, telegram.ChatID(chatID), 0, settlementHistoryLimit,
	)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// A chat that cannot be read leaves its records as they are.
		// The reason is safe: it is a transport error that has already
		// been through the program's own reason filter elsewhere.
		s.log().Warn(
			"restart settlement could not read a chat",
			slog.Int64("chat_id", chatID),
			slog.Int("records", len(records)),
			slog.String("error", outbox.SafeReason(err)),
		)
		counts.unreadable += len(records)
		return nil
	}

	candidates := settlementCandidates(page, records)

	for _, record := range records {
		if err := ctx.Err(); err != nil {
			return err
		}

		index := bestCandidate(candidates, record)
		if index < 0 {
			if err := s.markUncertain(ctx, record); err != nil {
				return err
			}
			counts.uncertain++
			s.log().Info(
				"accepted record not found in its chat; marked uncertain",
				slog.String("entry_id", string(record.ID)),
				slog.Int64("chat_id", chatID),
			)
			continue
		}

		messageID := candidates[index].messageID
		candidates[index].used = true

		if err := s.markSent(ctx, record, messageID); err != nil {
			return err
		}
		counts.sent++
		s.log().Info(
			"accepted record found in its chat; marked sent",
			slog.String("entry_id", string(record.ID)),
			slog.Int64("chat_id", chatID),
			slog.Int64("message_id", candidates[index].messageID),
		)
	}

	return nil
}

// settlementCandidate is one message of a chat that could match a
// record, reduced to what the match needs.
type settlementCandidate struct {
	messageID int64
	at        time.Time
	digest    [sha256.Size]byte
	used      bool
}

// settlementCandidates reduces a history page to the messages that could
// match one of these records.
//
// The text is compared as a digest and discarded, so the plaintext of
// somebody's message is not held in a second variable for the length of
// the settlement. Two messages of the same text have the same digest,
// which is why the match also has to be inside a time window and why
// each message is used at most once.
func settlementCandidates(
	page telegram.HistoryPage,
	records []outbox.UnsettledAccepted,
) []settlementCandidate {
	// Only the digests of the records are built here, and only for
	// records that are actually waiting: a busy chat has messages this
	// settlement has no interest in.
	wanted := make(map[[sha256.Size]byte]struct{}, len(records))
	for _, record := range records {
		wanted[textDigest(record.Text)] = struct{}{}
	}

	var candidates []settlementCandidate
	for _, message := range page.Messages {
		if !message.Outgoing {
			continue
		}
		digest := textDigest(message.Text)
		if _, interesting := wanted[digest]; !interesting {
			continue
		}
		if message.ID == 0 || message.Timestamp.IsZero() {
			continue
		}
		candidates = append(candidates, settlementCandidate{
			messageID: int64(message.ID),
			at:        message.Timestamp,
			digest:    digest,
		})
	}
	return candidates
}

// textDigest is the digest of a message body.
func textDigest(text string) [sha256.Size]byte {
	return sha256.Sum256([]byte(text))
}

// bestCandidate returns the index of the unused message that matches a
// record best, or -1 when none does.
//
// "Best" is the one closest in time. A user who sends the same words
// twice in a row has two records and two messages, and matching them by
// closeness keeps each record with its own message instead of letting
// the first record take the first message and leaving the second to be
// called missing.
func bestCandidate(
	candidates []settlementCandidate,
	record outbox.UnsettledAccepted,
) int {
	best := -1
	bestDistance := time.Duration(0)

	digest := textDigest(record.Text)
	for i, candidate := range candidates {
		if candidate.used || candidate.digest != digest {
			continue
		}
		distance := candidate.at.Sub(record.AcceptedAt)
		if distance < 0 {
			distance = -distance
		}
		if distance > settlementWindow {
			continue
		}
		if best < 0 || distance < bestDistance {
			best = i
			bestDistance = distance
		}
	}

	return best
}

// markSent records that the message is in the chat, with the identifier
// the history will come back with.
func (s *restartSettler) markSent(
	ctx context.Context,
	record outbox.UnsettledAccepted,
	messageID int64,
) error {
	_, err := s.entries.MarkSent(
		ctx, record.ID, record.Version, messageID, s.stamp(),
	)
	if err != nil {
		if errors.Is(err, outbox.ErrVersionConflict) {
			// Somebody else moved the record on, which is the outcome
			// this wanted anyway.
			return nil
		}
		return fmt.Errorf(
			"restart settlement: mark %s sent: %w", record.ID, err,
		)
	}
	return nil
}

// markUncertain records that the queue cannot say what became of the
// message.
func (s *restartSettler) markUncertain(
	ctx context.Context,
	record outbox.UnsettledAccepted,
) error {
	_, err := s.entries.MarkUncertain(
		ctx,
		record.ID,
		record.Version,
		"no confirmation from the run that sent it; not found in the chat",
		s.stamp(),
	)
	if err != nil {
		if errors.Is(err, outbox.ErrVersionConflict) {
			return nil
		}
		return fmt.Errorf(
			"restart settlement: mark %s uncertain: %w", record.ID, err,
		)
	}
	return nil
}

func (s *restartSettler) stamp() time.Time {
	if s.now != nil {
		return s.now()
	}
	if s.clock != nil {
		return s.clock.Now()
	}
	return time.Now()
}

func (s *restartSettler) log() *slog.Logger {
	if s == nil || s.logger == nil {
		return slog.Default()
	}
	return s.logger
}

// settlementSummary is one line about a settlement, in numbers.
func settlementSummary(counts settlementCounts) string {
	return fmt.Sprintf(
		"settled %d: %d sent, %d uncertain, %d unreadable",
		counts.considered, counts.sent, counts.uncertain,
		counts.unreadable,
	)
}

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

// settlementUncertainReason is what a settlement writes when it cannot
// find a message in its chat.
//
// It is a constant because it is also how a later settlement recognises
// its own records: the same sentence in last_error_message is what says
// "a lookup already tried this and did not find it, and the lookup may
// have been wrong". An uncertain record whose reason is something else —
// a lease lost mid-send — is the user's question and is left alone.
const settlementUncertainReason = "no confirmation from the run that sent it; not found in the chat"

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

// settlementHistoryLimit is how many messages one request asks for.
//
// It is the maximum TDLib serves in one call, so a page is as deep as a
// page can be and asking for more is asking for nothing.
const settlementHistoryLimit = 100

// settlementPageBudget is how many pages one chat may be read across.
//
// The records being settled were all sent by this queue, so they are near
// the end of the chat — but "near" is not "in the first page", and the
// owner's account proved it: thirteen records from the day before, every
// one of them in the chat, and none of them found, because one page of the
// newest hundred messages did not reach back that far.
//
// So the settlement pages backwards until it has found every record it
// can or the chat runs out. The budget is what stops a busy chat from
// being read to its beginning on every start: a record that is older than
// this many messages is a record this queue sent long enough ago that
// asking again is not worth the traffic, and it is settled as uncertain —
// which is what a user is asked to look at.
const settlementPageBudget = 10

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
		return counts, settlementErrorf(stepListAwaiting, err)
	}

	// And the records an earlier settlement of ours could not find.
	//
	// A settlement is allowed to be wrong once, and it was: on the
	// owner's account it marked thirteen records uncertain because the
	// lookup did not reach far enough, and "uncertain" asks the user to go
	// and look in Telegram by hand — which is the work the program is
	// being paid to do. The records it wrote that sentence to are looked
	// at again, and become sent when the message turns up. A record that
	// is uncertain for any other reason is not ours to answer.
	previous, err := s.store.ListSettledUncertain(
		ctx, s.accountKey, settlementUncertainReason, 0,
	)
	if err != nil {
		return counts, settlementErrorf(stepListSettled, err)
	}
	unsettled = append(unsettled, previous...)

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
	candidates, readErr := s.readCandidates(ctx, chatID, records)
	if readErr != nil {
		if ctx.Err() != nil {
			// The program is stopping, not the settlement failing. There
			// is nobody left to read the log and the records are exactly
			// where they were.
			return nil
		}
		// A chat that cannot be read leaves its records as they are.
		// The reason is safe: it is a transport error that has already
		// been through the program's own reason filter elsewhere.
		s.log().Warn(
			"restart settlement could not read a chat",
			slog.Int64("chat_id", chatID),
			slog.Int("records", len(records)),
			slog.String("step", stepReadHistory),
			slog.String("kind", errorKind(readErr)),
		)
		counts.unreadable += len(records)
		return nil
	}

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

// readCandidates reads as much of a chat as it takes to account for the
// records waiting in it.
//
// It pages backwards from the newest message, because a record this queue
// sent is near the end of the chat but is not necessarily in the first
// page. It stops as soon as every record has a candidate, as soon as the
// chat runs out, or when the page budget is spent — and it stops the
// moment the candidates cover the records, so the common case of a queue
// that was closed a moment ago costs exactly one request.
func (s *restartSettler) readCandidates(
	ctx context.Context,
	chatID int64,
	records []outbox.UnsettledAccepted,
) ([]settlementCandidate, error) {
	var (
		candidates []settlementCandidate
		unfound    = len(records)
		from       telegram.MessageID
	)

	for page := 0; page < settlementPageBudget; page++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		history, err := s.history.GetChatHistory(
			ctx, telegram.ChatID(chatID), from, settlementHistoryLimit,
		)
		if err != nil {
			return nil, err
		}
		if len(history.Messages) == 0 {
			return candidates, nil
		}

		candidates = append(
			candidates, settlementCandidates(history, records)...,
		)
		unfound -= coveredRecords(candidates, records)
		if unfound <= 0 {
			return candidates, nil
		}

		// The end of a chat is an empty page and nothing else.
		//
		// It used to also be a page shorter than the request, which is the
		// same len(messages) == limit heuristic telegram.HistoryPage had
		// and had already been taken out of, in the very comment that
		// describes what a real account answers: TDLib serves the first
		// request for a chat's history with what it has under its hand,
		// and on the owner's account that was one message, for both
		// chats, for a limit of a hundred. So the read took one page, and
		// one message, as the whole conversation — and settled all
		// thirteen records, sent the day before and every one of them in
		// the chat, as uncertain with sameText=0, which is not a failed
		// match but a match that was never attempted.
		//
		// NextFrom is the boundary to ask for next, and it is zero only
		// for a page that was empty. A boundary that does not move would
		// ask for the same page again and be answered with the same
		// page, so that is the other end of the chat. What bounds a chat
		// that has no end is the page budget.
		if history.NextFrom == 0 || history.NextFrom == from {
			return candidates, nil
		}
		from = history.NextFrom
	}

	return candidates, nil
}

// coveredRecords is how many of the records now have at least one
// candidate to be matched against.
func coveredRecords(
	candidates []settlementCandidate,
	records []outbox.UnsettledAccepted,
) int {
	wanted := make(map[[sha256.Size]byte]struct{}, len(records))
	for _, record := range records {
		wanted[textDigest(record.Text)] = struct{}{}
	}

	covered := make(map[[sha256.Size]byte]struct{}, len(wanted))
	for _, candidate := range candidates {
		if _, interesting := wanted[candidate.digest]; interesting {
			covered[candidate.digest] = struct{}{}
		}
	}

	return len(covered)
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
		return &settlementFailure{
			step: stepMarkSent,
			kind: errorKind(err),
			err:  err,
		}
	}
	return nil
}

// markUncertain records that the queue cannot say what became of the
// message.
//
// A record that is already uncertain is left alone, and that is the whole
// fix for a settlement that failed on every start. The record already
// says "I do not know"; writing that sentence again is not a state change,
// and the state machine refuses it — `uncertain -> uncertain` is not a
// transition. So the second and third runs of a lookup that still cannot
// find the message were failing on the write, and failing on the write
// meant the whole settlement reported a failure, which meant the logs
// said nothing about the reads that had already worked.
//
// The record is counted either way: it is uncertain, and the user is the
// one who has to look.
func (s *restartSettler) markUncertain(
	ctx context.Context,
	record outbox.UnsettledAccepted,
) error {
	if record.PreviouslyUncertain {
		return nil
	}

	_, err := s.entries.MarkUncertain(
		ctx, record.ID, record.Version,
		settlementUncertainReason, s.stamp(),
	)
	if err != nil {
		if errors.Is(err, outbox.ErrVersionConflict) {
			return nil
		}
		return &settlementFailure{
			step: stepMarkUncertain,
			kind: errorKind(err),
			err:  err,
		}
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

// log is the logger the settlement reports through. A nil logger
// discards rather than reaching for the default, which writes to the
// terminal the interface owns.
func (s *restartSettler) log() *slog.Logger {
	if s == nil || s.logger == nil {
		return discardLogger()
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

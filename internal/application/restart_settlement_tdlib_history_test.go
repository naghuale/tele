package application

import (
	"context"
	"fmt"
	"testing"
	"time"

	"telecli/internal/outbox"
	"telecli/internal/telegram"
)

// TDLib answers the FIRST request for a chat's history with what it has
// under its hand, and what it has under its hand is often one message.
//
// The owner's account answered both chats' first request with got=1 for a
// limit of 100, and the read took "less than I asked for" as the end of
// the chat. So it read one message, matched nothing, and settled all
// thirteen records — sent the day before, every one of them in the chat —
// as uncertain with sameText=0, which is not a failed match but a match
// that was never attempted.
//
// A chat ends at an empty page. That is not a new rule: it is what
// telegram.HistoryPage says about HasMore, in the place where the
// len(messages) == limit heuristic was already taken out of for the feed.
// The settlement had its own copy of the version that was removed.
func TestASettlementReadsPastTDLibsShortFirstPage(t *testing.T) {
	t.Parallel()

	path := newSettlementPath(t)
	at := time.Now().Add(-26 * time.Hour).Truncate(time.Second)
	const text = "вчерашнее сообщение"

	accepted := path.leaveAccepted(text, at)

	// The record's message sits on the third page, which is what a chat
	// that has been written to for years looks like: a day-old message is
	// two hundred messages back, and nothing about the first page says so.
	history := tdlibLikeHistory{pages: map[telegram.MessageID]telegram.HistoryPage{
		// from = 0: the newest message, and the only one TDLib had.
		0: page(1,
			telegram.Message{
				ID:        900,
				ChatID:    telegram.ChatID(settlementChat),
				Outgoing:  true,
				Timestamp: at.Add(24 * time.Hour),
				Text:      "сегодняшнее сообщение",
			},
		),
		// from = 900: a full page.
		900: page(settlementHistoryLimit,
			filler(800, 899, at, 20*time.Hour, "сегодняшнее")...,
		),
		// from = 800: another full page, and the record is the newest
		// message of it — which is where a record this queue sent a day ago
		// lands once the chat has a few hundred messages after it.
		800: page(settlementHistoryLimit,
			append(
				[]telegram.Message{{
					ID:        4242,
					ChatID:    telegram.ChatID(settlementChat),
					Outgoing:  true,
					Timestamp: at.Add(2 * time.Second),
					Text:      text,
				}},
				filler(700, 798, at, 5*time.Hour, "позавчерашнее")...,
			)...,
		),
	},
	}
	// The boundary of the last page points into the page before it, so a
	// read that keeps going finds an empty page and stops there rather
	// than on a page that happened to be short.
	history.pages[600] = page(settlementHistoryLimit,
		filler(500, 599, at, 30*time.Hour, "позавчерашнее")...,
	)

	counts, err := path.settler(&history).Settle(context.Background())
	if err != nil {
		t.Fatalf("settle: %v", err)
	}

	if len(history.asked) < 3 {
		t.Fatalf(
			"the read asked for %d pages and stopped, having not reached "+
				"the third: %v. A page shorter than the request is not "+
				"the end of a chat.",
			len(history.asked), history.asked,
		)
	}
	if counts.considered != 1 || counts.sent != 1 || counts.uncertain != 0 {
		t.Fatalf(
			"counts = %+v, want one record sent. The owner's own numbers "+
				"for this were 0 sent and 13 uncertain with sameText=0.",
			counts,
		)
	}
	if got := path.state(text); got != outbox.StateSent {
		t.Fatalf(
			"state = %q, want sent: the message was in the chat, on the "+
				"third page", got,
		)
	}

	entry, err := path.store.Store.Get(context.Background(), accepted.ID)
	if err != nil {
		t.Fatalf("get entry: %v", err)
	}
	if entry.TelegramMessageID != 4242 {
		t.Fatalf(
			"message id = %d, want 4242: the id the third page reported",
			entry.TelegramMessageID,
		)
	}
}

// tdlibLikeHistory answers by boundary the way TDLib does, which is the
// only thing about it that matters: a request is answered with the
// messages older than its boundary, and a boundary that is not in the map
// is an empty page.
//
// A fake that returns one fixed page however it is asked cannot see this
// bug at all, which is why the ones in the other file of this package
// answer from a map keyed by chat rather than by boundary.
type tdlibLikeHistory struct {
	pages map[telegram.MessageID]telegram.HistoryPage
	asked []telegram.MessageID
}

func (h *tdlibLikeHistory) GetChatHistory(
	_ context.Context,
	_ telegram.ChatID,
	from telegram.MessageID,
	limit int,
) (telegram.HistoryPage, error) {
	h.asked = append(h.asked, from)

	answer, ok := h.pages[from]
	if !ok {
		return telegram.HistoryPage{}, nil
	}
	// TDLib serves the limit it was given, and the boundary it answers
	// with is the oldest message of the answer.
	if limit > 0 && len(answer.Messages) > limit {
		answer.Messages = answer.Messages[:limit]
	}
	if len(answer.Messages) == 0 {
		return telegram.HistoryPage{}, nil
	}
	answer.HasMore = true
	answer.NextFrom = answer.Messages[len(answer.Messages)-1].ID

	return answer, nil
}

// page is a history page holding count messages, with the boundary the
// answer reports left for the fake to fill in.
func page(count int, messages ...telegram.Message) telegram.HistoryPage {
	return telegram.HistoryPage{Messages: messages[:count:count]}
}

// filler is count messages of text, dated at base+offset and numbered
// downwards from last, so a page can be built without writing out a
// hundred literals of somebody's chat.
func filler(
	first, last telegram.MessageID,
	base time.Time,
	offset time.Duration,
	text string,
) []telegram.Message {
	messages := make([]telegram.Message, 0, last-first+1)
	for id := last; id >= first; id-- {
		messages = append(messages, telegram.Message{
			ID:        id,
			ChatID:    telegram.ChatID(settlementChat),
			Outgoing:  true,
			Timestamp: base.Add(offset + time.Duration(id)*time.Second),
			Text:      fmt.Sprintf("%s %d", text, id),
		})
	}

	return messages
}

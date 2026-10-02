package telegram

import (
	"context"
	"testing"
	"time"
)

// chatSummaryResult is the answer of one GetChat call, as the tests of
// this file wait for it.
type chatSummaryResult struct {
	summary ChatSummary
	err     error
}

// The wire form of the two fields the interface reads the clock and the
// unread line from, as TDLib sends them: `date` and
// `last_read_inbox_message_id` are both plain integers in the JSON, and a
// build that reads either of them as a string or writes it as one turns a
// chat into an unreadable answer.
//
// The numbers below are the shape of a chat a person has been talking in:
// a message sent at 21:21 UTC and read up to message 199, with two more
// messages after it. 1700000000 is 14 November 2023 22:13:20 UTC, which is
// the 14th of November at 08:13 in Vladivostok — the ten hours east of
// Greenwich that made the whole of this a bug.
func TestGetChatReadsTheInstantAndTheReadPointerInTheFormTDLibSendsThem(t *testing.T) {
	const sentAt = 1700000000

	session, sender, _, client := newSessionWithFakes(t)

	resultCh := make(chan chatSummaryResult, 1)

	go func() {
		summary, err := session.GetChat(context.Background(), 42)
		resultCh <- chatSummaryResult{summary: summary, err: err}
	}()

	extra := waitForRequestTypeN(t, sender, "getChat", 1)
	feedResponse(t, client, map[string]any{
		"@type":                      "chat",
		"id":                         42,
		"title":                      "Alice",
		"unread_count":               2,
		"last_read_inbox_message_id": 199,
		"last_message": map[string]any{
			"@type": "message",
			"id":    201,
			"date":  sentAt,
			"content": map[string]any{
				"@type": "messageText",
				"text": map[string]any{
					"@type": "formattedText",
					"text":  "the newest one",
				},
			},
		},
	}, extra)

	got := waitForChatSummary(t, resultCh)

	if want := MessageID(199); got.LastReadInboxMessageID != want {
		t.Errorf(
			"LastReadInboxMessageID = %d, want %d",
			got.LastReadInboxMessageID, want,
		)
	}

	// The instant, in whatever zone the machine is in. `Equal` compares
	// the moment and not the zone on purpose: the instant is what Telegram
	// sent, and where it is read is the interface's decision, made once,
	// from the zone of the machine.
	want := time.Unix(sentAt, 0)
	if !got.LastMessageTime.Equal(want) {
		t.Errorf("LastMessageTime = %v, want %v", got.LastMessageTime, want)
	}
	if got.LastMessageTime.Location() == time.UTC {
		t.Error(
			"LastMessageTime is stamped in UTC: every time on the screen " +
				"would be UTC's, which is what the owner reported on " +
				"29.09.2026",
		)
	}
}

// A chat with nothing read in it sends a zero, and a chat TDLib did not
// mention the field on sends nothing at all. Both are a chat with no line
// between what has been read and what has not, and both have to be the zero
// rather than an error: the feed asks for the line of every chat it opens.
func TestAChatWithoutAReadPointerIsZero(t *testing.T) {
	for name, feed := range map[string]map[string]any{
		"the field is zero": {
			"@type":                      "chat",
			"id":                         42,
			"title":                      "Alice",
			"unread_count":               0,
			"last_read_inbox_message_id": 0,
			"last_message":               nil,
		},
		"the field is absent": {
			"@type":        "chat",
			"id":           42,
			"title":        "Alice",
			"unread_count": 0,
			"last_message": nil,
		},
	} {
		t.Run(name, func(t *testing.T) {
			session, sender, _, client := newSessionWithFakes(t)

			resultCh := make(chan chatSummaryResult, 1)

			go func() {
				summary, err := session.GetChat(context.Background(), 42)
				resultCh <- chatSummaryResult{summary: summary, err: err}
			}()

			extra := waitForRequestTypeN(t, sender, "getChat", 1)
			feedResponse(t, client, feed, extra)

			if got := waitForChatSummary(t, resultCh); got.LastReadInboxMessageID != 0 {
				t.Errorf(
					"LastReadInboxMessageID = %d, want 0",
					got.LastReadInboxMessageID,
				)
			}
		})
	}
}

// waitForChatSummary returns the answer of a GetChat call, and fails the
// test rather than returning a zero summary if the call did not come back.
func waitForChatSummary(
	t *testing.T,
	resultCh <-chan chatSummaryResult,
) ChatSummary {
	t.Helper()

	select {
	case r := <-resultCh:
		if r.err != nil {
			t.Fatalf("GetChat: %v", r.err)
		}

		return r.summary
	case <-time.After(2 * time.Second):
		t.Fatal("GetChat did not return")

		return ChatSummary{}
	}
}

package application

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"telecli/internal/telegram"
	"telecli/internal/tui"
)

// The viewer is the one place that turns "these messages are on the screen"
// into a query, and the window crosses over as it is: the source of the read
// belongs to the session, which owns the names TDLib knows.

// recordedViewerSession is a session that records the reads it was asked for
// and answers with a recorded one.
type recordedViewerSession struct {
	reads  []recordedRead
	answer error
}

type recordedRead struct {
	chatID telegram.ChatID
	ids    []telegram.MessageID
}

func (s *recordedViewerSession) ViewMessages(
	_ context.Context,
	chatID telegram.ChatID,
	messageIDs []telegram.MessageID,
) error {
	s.reads = append(s.reads, recordedRead{
		chatID: chatID,
		ids:    append([]telegram.MessageID(nil), messageIDs...),
	})

	return s.answer
}

// lastRead is the read the last call asked for.
func (s *recordedViewerSession) lastRead(t *testing.T) recordedRead {
	t.Helper()

	if len(s.reads) == 0 {
		t.Fatal("no read reached the session")
	}

	return s.reads[len(s.reads)-1]
}

// recordedWindow is the bytes of the message_ids the session was asked with.
func recordedWindow(t *testing.T, read recordedRead) []any {
	t.Helper()

	raw, err := json.Marshal(map[string]any{
		"@type":       "viewMessages",
		"chat_id":     int64(read.chatID),
		"message_ids": read.ids,
	})
	if err != nil {
		t.Fatalf("marshal the window: %v", err)
	}

	var request struct {
		MessageIDs []any `json:"message_ids"`
	}
	if err := json.Unmarshal(raw, &request); err != nil {
		t.Fatalf("decode the window: %v", err)
	}

	return request.MessageIDs
}

// The window crosses into the session whole and in order, for every kind of
// chat: nothing here decides how a read is to be described, and a decision
// taken here is a name TDLib may not know.
func TestTheWindowCrossesToTheSessionForEveryKindOfChat(t *testing.T) {
	for _, kind := range []tui.ChatKind{
		tui.ChatKindPrivate,
		tui.ChatKindGroup,
		tui.ChatKindChannel,
	} {
		t.Run(kind.String(), func(t *testing.T) {
			session := &recordedViewerSession{}
			viewer := &TelegramMessageViewer{session: session}

			if err := viewer.ViewMessages(
				context.Background(), 7, []int64{34, 39, 40},
			); err != nil {
				t.Fatalf("ViewMessages: %v", err)
			}

			read := session.lastRead(t)
			if read.chatID != 7 {
				t.Fatalf("chat = %d, want 7", read.chatID)
			}
			window := recordedWindow(t, read)
			if len(window) != 3 {
				t.Fatalf("message_ids = %v, want the whole window", window)
			}
			for index, want := range []float64{34, 39, 40} {
				if window[index] != want {
					t.Fatalf(
						"message_ids = %v, want %v in order",
						window, []float64{34, 39, 40},
					)
				}
			}
		})
	}
}

// A viewer without a session reads nothing. The alternative is a nil
// dereference on the first Enter, and a program without Telegram has no
// chat to read.
func TestTheViewerWithoutASessionDoesNothing(t *testing.T) {
	viewer := &TelegramMessageViewer{}

	if err := viewer.ViewMessages(
		context.Background(), 42, []int64{7},
	); err != nil {
		t.Fatalf("ViewMessages: %v", err)
	}
}

// A session that refuses is reported, not swallowed: the interface logs what
// it asked and what it got, and something has to hand it the cause.
func TestTheViewerReturnsTheSessionError(t *testing.T) {
	session := &recordedViewerSession{
		answer: errors.New("viewMessages: TDLib response: code=400"),
	}
	viewer := &TelegramMessageViewer{session: session}

	err := viewer.ViewMessages(context.Background(), 42, []int64{7})
	if err == nil {
		t.Fatal("a refused read returned nothing")
	}
}

// The identifiers are the ones TDLib knows the messages by. A message of the
// queue has none yet — it carries a temporary identifier until Telegram
// confirms the send — and a read that carried it would mark nothing while
// looking like a read that did.
func TestOnlyTheIdentifiersOfDeliveredMessagesAreRead(t *testing.T) {
	session := &recordedViewerSession{}
	viewer := &TelegramMessageViewer{session: session}

	if err := viewer.ViewMessages(
		context.Background(), 7, []int64{40},
	); err != nil {
		t.Fatalf("ViewMessages: %v", err)
	}

	read := session.lastRead(t)
	if len(read.ids) != 1 || read.ids[0] != 40 {
		t.Fatalf("ids = %v, want the identifier of the delivered message", read.ids)
	}
}

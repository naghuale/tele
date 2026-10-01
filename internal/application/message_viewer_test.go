package application

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"telecli/internal/telegram"
	"telecli/internal/tui"
)

// The viewer is the only place that turns "these messages are on the screen"
// into a query. Two things in it decide whether TDLib takes the read at all,
// and both are the owner's channel of 89 unread: the kind of the chat, which
// decides the source, and the identifiers, which have to be the ones TDLib
// knows the messages by.

// recordedViewerSession is a session that records the reads it was asked for
// and answers with a recorded one.
type recordedViewerSession struct {
	reads  []recordedRead
	answer error
}

type recordedRead struct {
	chatID telegram.ChatID
	ids    []telegram.MessageID
	source telegram.MessageSource
}

func (s *recordedViewerSession) ViewMessages(
	_ context.Context,
	chatID telegram.ChatID,
	messageIDs []telegram.MessageID,
	source telegram.MessageSource,
) error {
	s.reads = append(s.reads, recordedRead{
		chatID: chatID,
		ids:    append([]telegram.MessageID(nil), messageIDs...),
		source: source,
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

// recordedRequest is the bytes of the viewMessages the session would send for
// a read, which is what a check of a stuck counter reads.
func recordedRequest(t *testing.T, read recordedRead) map[string]any {
	t.Helper()

	raw, err := json.Marshal(struct {
		Type       string                 `json:"@type"`
		ChatID     int64                  `json:"chat_id"`
		MessageIDs []telegram.MessageID   `json:"message_ids"`
		Source     telegram.MessageSource `json:"source"`
		ForceRead  bool                   `json:"force_read"`
	}{
		Type:       "viewMessages",
		ChatID:     int64(read.chatID),
		MessageIDs: read.ids,
		Source:     read.source,
		ForceRead:  true,
	})
	if err != nil {
		t.Fatalf("marshal viewMessages: %v", err)
	}

	var request map[string]any
	if err := json.Unmarshal(raw, &request); err != nil {
		t.Fatalf("decode viewMessages: %v", err)
	}

	return request
}

// The kind of the chat decides the source, and the three kinds are three
// sources. A channel read with the source of a private chat is a read TDLib
// refuses, which is how a count of 89 stayed at 89.
func TestTheKindOfTheChatDecidesTheSourceOfTheRead(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		kind       tui.ChatKind
		wantSource string
	}{
		{name: "private", kind: tui.ChatKindPrivate, wantSource: "messageSourceChat"},
		{name: "group", kind: tui.ChatKindGroup, wantSource: "messageSourceHistory"},
		{name: "channel", kind: tui.ChatKindChannel, wantSource: "messageSourceHistory"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			session := &recordedViewerSession{}
			viewer := &TelegramMessageViewer{session: session}

			if err := viewer.ViewMessages(
				context.Background(), 7, testCase.kind, []int64{34, 40},
			); err != nil {
				t.Fatalf("ViewMessages: %v", err)
			}

			request := recordedRequest(t, session.lastRead(t))
			source, ok := request["source"].(map[string]any)
			if !ok {
				t.Fatalf("request = %v, want a source", request)
			}
			if source["@type"] != testCase.wantSource {
				t.Fatalf(
					"source = %v, want %s", source, testCase.wantSource,
				)
			}

			// force_read is what reads the page of older messages above the
			// newest one; without it only the end of the chat is read.
			if request["force_read"] != true {
				t.Fatalf("force_read = %v, want true", request["force_read"])
			}
			// The window is the window, in order, with the identifiers TDLib
			// knows the messages by.
			if got := request["message_ids"]; len(got.([]any)) != 2 {
				t.Fatalf("message_ids = %v, want two", got)
			}
			ids := request["message_ids"].([]any)
			if ids[0].(float64) != 34 || ids[1].(float64) != 40 {
				t.Fatalf("message_ids = %v, want 34 and 40", ids)
			}

			// A read of a chat with many people names where it stopped: the
			// newest identifier of the window, which is the number a stuck
			// counter is read by.
			if testCase.kind.Grouped() {
				if id, ok := source["message_id"].(float64); !ok || id != 40 {
					t.Fatalf(
						"source = %v, want the newest identifier of the window",
						source,
					)
				}
			}
		})
	}
}

// A window is read up to its newest identifier, so the source names the
// newest one and not the first: a read that named the oldest of the window
// would leave everything above it unread.
func TestTheSourceNamesTheNewestMessageOfTheWindow(t *testing.T) {
	session := &recordedViewerSession{}
	viewer := &TelegramMessageViewer{session: session}

	if err := viewer.ViewMessages(
		context.Background(), 7, tui.ChatKindChannel, []int64{34, 35, 36, 37, 38, 39, 40},
	); err != nil {
		t.Fatalf("ViewMessages: %v", err)
	}

	source := recordedRequest(t, session.lastRead(t))["source"].(map[string]any)
	if id, ok := source["message_id"].(float64); !ok || id != 40 {
		t.Fatalf("source = %v, want the newest of the window", source)
	}
}

// A viewer without a session reads nothing. The alternative is a nil
// dereference on the first Enter, and a program without Telegram has no
// chat to read.
func TestTheViewerWithoutASessionDoesNothing(t *testing.T) {
	viewer := &TelegramMessageViewer{}

	if err := viewer.ViewMessages(
		context.Background(), 42, tui.ChatKindChannel, []int64{7},
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

	err := viewer.ViewMessages(
		context.Background(), 42, tui.ChatKindChannel, []int64{7},
	)
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
		context.Background(), 7, tui.ChatKindPrivate, []int64{40},
	); err != nil {
		t.Fatalf("ViewMessages: %v", err)
	}

	read := session.lastRead(t)
	if len(read.ids) != 1 || read.ids[0] != 40 {
		t.Fatalf("ids = %v, want the identifier of the delivered message", read.ids)
	}
}

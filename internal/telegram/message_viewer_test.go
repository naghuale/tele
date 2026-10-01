package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// TDLib does not know what is on the screen, so the session is told which
// messages have been read and asks TDLib to move the read pointer there.
//
// The three kinds of chat are three recorded requests, because which source
// a read carries decides whether TDLib takes it at all, and a source that is
// refused leaves the counter exactly where it was and says nothing about
// itself.

// viewMessagesSourceJSON reads the source of a recorded request as plain
// JSON, so a test states the bytes TDLib is asked with rather than the Go
// value they were built from.
func viewMessagesSourceJSON(t *testing.T, raw []byte) map[string]any {
	t.Helper()

	var decoded struct {
		Source map[string]any `json:"source"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode request: %v", err)
	}

	return decoded.Source
}

// A private chat is read in itself: that is the source an official client
// sends for a chat with one other person, and it is what makes TDLib tell
// the phone and the other devices of the account.
func TestAPrivateChatIsReadInTheChatItself(t *testing.T) {
	source := viewMessagesSourceJSON(
		t,
		recordedViewMessages(t, ChatKindPrivate, 991),
	)

	if source["@type"] != "messageSourceChat" {
		t.Fatalf("source = %v, want messageSourceChat", source)
	}
	// A source that names no message carries no message field: the private
	// chat is the whole of where the read happened.
	if _, named := source["message_id"]; named {
		t.Fatalf("source = %v, want no message_id on a chat source", source)
	}
}

// A group and a channel are read in a window of their history, and the
// window is named by the newest message of it. This is the source TDLib's
// own clients send for a channel, and it is the one the owner's channel with
// 89 unread was missing: messageSourceChat is a chat the messages do not
// belong to, and a read that names it does not move a channel's pointer.
func TestAGroupAndAChannelAreReadInAWindowOfTheirHistory(t *testing.T) {
	for _, kind := range []ChatKind{ChatKindGroup, ChatKindSupergroup} {
		t.Run(string(kind), func(t *testing.T) {
			source := viewMessagesSourceJSON(
				t,
				recordedViewMessages(t, kind, 991),
			)

			if source["@type"] != "messageSourceHistory" {
				t.Fatalf("source = %v, want messageSourceHistory", source)
			}
			// ttl 0 is this device: the read happened here and is not one
			// another session left to expire.
			if ttl, ok := source["ttl"].(float64); !ok || ttl != 0 {
				t.Fatalf("ttl = %v, want 0", source["ttl"])
			}
			if id, ok := source["message_id"].(float64); !ok || id != 991 {
				t.Fatalf("message_id = %v, want the newest read message", source["message_id"])
			}
		})
	}
}

// recordedViewMessages is the bytes of a request for a read of the newest
// message 991, built the way the interface builds one.
func recordedViewMessages(t *testing.T, kind ChatKind, newest MessageID) []byte {
	t.Helper()

	raw, err := json.Marshal(viewMessagesRequest{
		Type:       "viewMessages",
		ChatID:     42,
		MessageIDs: []tdInt{tdInt(newest)},
		Source:     MessageSourceFor(kind, newest),
		ForceRead:  true,
	})
	if err != nil {
		t.Fatalf("marshal viewMessages: %v", err)
	}

	return raw
}

func TestViewMessagesSendsTheWindowWithForcedRead(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	resultCh := make(chan error, 1)
	go func() {
		resultCh <- session.ViewMessages(
			context.Background(),
			42,
			[]MessageID{7, 8, 9},
			MessageSourceFor(ChatKindPrivate, 9),
		)
	}()

	request := waitForRequestN(t, sender, "viewMessages", 1)

	var decoded struct {
		Type       string  `json:"@type"`
		ChatID     int64   `json:"chat_id"`
		MessageIDs []tdInt `json:"message_ids"`
		Source     struct {
			Type string `json:"@type"`
		} `json:"source"`
		ForceRead bool   `json:"force_read"`
		Extra     string `json:"@extra"`
	}
	if err := json.Unmarshal(request, &decoded); err != nil {
		t.Fatalf("decode request: %v", err)
	}

	if decoded.Type != "viewMessages" {
		t.Fatalf("@type = %q, want viewMessages", decoded.Type)
	}
	if decoded.ChatID != 42 {
		t.Fatalf("chat_id = %d, want 42", decoded.ChatID)
	}
	if len(decoded.MessageIDs) != 3 ||
		decoded.MessageIDs[0] != 7 ||
		decoded.MessageIDs[1] != 8 ||
		decoded.MessageIDs[2] != 9 {
		t.Fatalf("message_ids = %v, want the window in order", decoded.MessageIDs)
	}
	// Without force_read TDLib only marks a message read at the end of the
	// chat, which is not the page of older messages above the newest one.
	if !decoded.ForceRead {
		t.Fatal("force_read = false, want true")
	}
	if decoded.Source.Type != "messageSourceChat" {
		t.Fatalf("source = %q, want the source of the kind", decoded.Source.Type)
	}
	if decoded.Extra == "" {
		t.Fatal("@extra is empty")
	}

	feedResponse(t, client, map[string]any{"@type": "ok"}, decoded.Extra)

	select {
	case err := <-resultCh:
		if err != nil {
			t.Fatalf("ViewMessages: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ViewMessages did not return")
	}
}

// The identifiers travel as numbers, not as the JSON strings TDLib writes
// for its 64-bit integers: a request that quoted them would ask TDLib for a
// string where the scheme has a number (see tdint.go). An identifier is
// also the one TDLib knows the message by, which is what makes the read
// reach the account rather than a local row.
func TestViewMessagesWritesTheServerIdentifiersAsNumbers(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	resultCh := make(chan error, 1)
	go func() {
		resultCh <- session.ViewMessages(
			context.Background(),
			42,
			[]MessageID{7, 9007199254740993},
			MessageSourceFor(ChatKindSupergroup, 9007199254740993),
		)
	}()

	request := waitForRequestN(t, sender, "viewMessages", 1)

	var decoded struct {
		MessageIDs []json.RawMessage `json:"message_ids"`
		Source     struct {
			MessageID json.RawMessage `json:"message_id"`
		} `json:"source"`
		Extra string `json:"@extra"`
	}
	if err := json.Unmarshal(request, &decoded); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if len(decoded.MessageIDs) != 2 {
		t.Fatalf("message_ids = %v, want two", decoded.MessageIDs)
	}
	if string(decoded.MessageIDs[0]) != "7" {
		t.Fatalf("message_ids[0] = %s, want the bare number 7", decoded.MessageIDs[0])
	}
	if string(decoded.MessageIDs[1]) != "9007199254740993" {
		t.Fatalf(
			"message_ids[1] = %s, want the bare number 9007199254740993",
			decoded.MessageIDs[1],
		)
	}
	if string(decoded.Source.MessageID) != "9007199254740993" {
		t.Fatalf(
			"source.message_id = %s, want the bare number 9007199254740993",
			decoded.Source.MessageID,
		)
	}

	feedResponse(t, client, map[string]any{"@type": "ok"}, decoded.Extra)
	<-resultCh
}

// A request that cannot say anything is not sent: a zero chat is no chat,
// an empty list of identifiers is a read of nothing, and a read with no
// source is a read TDLib cannot attribute to a place.
func TestViewMessagesRejectsARequestThatCouldNotBeSent(t *testing.T) {
	session, sender, _, _ := newSessionWithFakes(t)
	source := MessageSourceFor(ChatKindPrivate, 1)

	err := session.ViewMessages(context.Background(), 0, []MessageID{1}, source)
	if !errors.Is(err, ErrInvalidChatID) {
		t.Fatalf("zero chat id error = %v, want ErrInvalidChatID", err)
	}
	if err := session.ViewMessages(
		context.Background(), 42, nil, source,
	); !errors.Is(err, ErrNoMessagesToView) {
		t.Fatalf("no identifiers error = %v, want ErrNoMessagesToView", err)
	}
	if err := session.ViewMessages(
		context.Background(), 42, []MessageID{0}, source,
	); !errors.Is(err, ErrNoMessagesToView) {
		t.Fatalf("zero identifier error = %v, want ErrNoMessagesToView", err)
	}
	if err := session.ViewMessages(
		context.Background(), 42, []MessageID{1}, nil,
	); !errors.Is(err, ErrMessageViewResponse) {
		t.Fatalf("no source error = %v, want ErrMessageViewResponse", err)
	}
	if sender.count() != 0 {
		t.Fatalf("sent requests = %d, want 0", sender.count())
	}
}

// A refusal reaches the caller as a TDLib error: the interface logs it and
// draws nothing, and the messages stay unread rather than being reported as
// read. This is the recorded answer a channel gives when it has not been
// opened, and the caller is what tells the log about it.
func TestViewMessagesReturnsTheTDLibError(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	resultCh := make(chan error, 1)
	go func() {
		resultCh <- session.ViewMessages(
			context.Background(),
			42,
			[]MessageID{7},
			MessageSourceFor(ChatKindSupergroup, 7),
		)
	}()

	request := waitForRequestN(t, sender, "viewMessages", 1)
	var decoded struct {
		Extra string `json:"@extra"`
	}
	if err := json.Unmarshal(request, &decoded); err != nil {
		t.Fatalf("decode request: %v", err)
	}

	feedResponse(t, client, map[string]any{
		"@type":   "error",
		"code":    400,
		"message": "Chat not found",
	}, decoded.Extra)

	select {
	case err := <-resultCh:
		var tdlibErr *TDLibError
		if !errors.As(err, &tdlibErr) {
			t.Fatalf("error = %v, want a TDLib error", err)
		}
		if tdlibErr.Code != 400 {
			t.Fatalf("code = %d, want 400", tdlibErr.Code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ViewMessages did not return")
	}
}

// The schema says viewMessages returns ok, so anything else is a protocol
// surprise and is reported rather than read as a read.
func TestViewMessagesRejectsAnUnexpectedAnswer(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	resultCh := make(chan error, 1)
	go func() {
		resultCh <- session.ViewMessages(
			context.Background(),
			42,
			[]MessageID{7},
			MessageSourceFor(ChatKindPrivate, 7),
		)
	}()

	request := waitForRequestN(t, sender, "viewMessages", 1)
	var decoded struct {
		Extra string `json:"@extra"`
	}
	if err := json.Unmarshal(request, &decoded); err != nil {
		t.Fatalf("decode request: %v", err)
	}

	feedResponse(t, client, map[string]any{
		"@type":   "updateChatReadInbox",
		"chat_id": int64(42),
	}, decoded.Extra)

	select {
	case err := <-resultCh:
		if !errors.Is(err, ErrMessageViewResponse) {
			t.Fatalf("error = %v, want ErrMessageViewResponse", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ViewMessages did not return")
	}
}

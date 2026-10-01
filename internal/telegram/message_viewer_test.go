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
// The source is the same for every kind of chat — the history of the chat
// that is open — and the tests below are what keep it that name and not one
// this build invented. TestEveryTDLibClassTheCodeNamesIsInThePinnedSchema
// is the guard; these are the requests it guards.

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

// recordedViewMessages is the bytes of a request for a read of the newest
// message 991, built the way the interface builds one.
func recordedViewMessages(t *testing.T, newest MessageID) []byte {
	t.Helper()

	raw, err := json.Marshal(viewMessagesRequest{
		Type:       "viewMessages",
		ChatID:     42,
		MessageIDs: []tdInt{tdInt(newest)},
		Source:     viewMessagesSource,
		ForceRead:  true,
	})
	if err != nil {
		t.Fatalf("marshal viewMessages: %v", err)
	}

	return raw
}

// The read is of the history of the chat that is open, and that is one
// source for every kind of chat: there is nothing in the window that says
// whether the chat is a channel, a group or a chat with one other person.
func TestAReadIsOfTheHistoryOfTheChatThatIsOpen(t *testing.T) {
	source := viewMessagesSourceJSON(t, recordedViewMessages(t, 991))

	if source["@type"] != "messageSourceChatHistory" {
		t.Fatalf("source = %v, want messageSourceChatHistory", source)
	}
	// The schema gives the class no field, and a field written into it is a
	// field TDLib ignores.
	if len(source) != 1 {
		t.Fatalf("source = %v, want only its type", source)
	}
}

func TestViewMessagesSendsTheWindowWithForcedRead(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	resultCh := make(chan error, 1)
	go func() {
		resultCh <- session.ViewMessages(
			context.Background(), 42, []MessageID{7, 8, 9},
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
	if decoded.Source.Type != "messageSourceChatHistory" {
		t.Fatalf("source = %q, want messageSourceChatHistory", decoded.Source.Type)
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
// string where the scheme has a number (see tdint.go). An identifier is also
// the one TDLib knows the message by, which is what makes the read reach the
// account rather than a local row.
func TestViewMessagesWritesTheServerIdentifiersAsNumbers(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	resultCh := make(chan error, 1)
	go func() {
		resultCh <- session.ViewMessages(
			context.Background(), 42, []MessageID{7, 9007199254740993},
		)
	}()

	request := waitForRequestN(t, sender, "viewMessages", 1)

	var decoded struct {
		MessageIDs []json.RawMessage `json:"message_ids"`
		Extra      string            `json:"@extra"`
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

	feedResponse(t, client, map[string]any{"@type": "ok"}, decoded.Extra)
	<-resultCh
}

// A request that cannot say anything is not sent: a zero chat is no chat
// and an empty list of identifiers is a read of nothing.
func TestViewMessagesRejectsARequestThatCouldNotBeSent(t *testing.T) {
	session, sender, _, _ := newSessionWithFakes(t)

	err := session.ViewMessages(context.Background(), 0, []MessageID{1})
	if !errors.Is(err, ErrInvalidChatID) {
		t.Fatalf("zero chat id error = %v, want ErrInvalidChatID", err)
	}
	if err := session.ViewMessages(
		context.Background(), 42, nil,
	); !errors.Is(err, ErrNoMessagesToView) {
		t.Fatalf("no identifiers error = %v, want ErrNoMessagesToView", err)
	}
	if err := session.ViewMessages(
		context.Background(), 42, []MessageID{0},
	); !errors.Is(err, ErrNoMessagesToView) {
		t.Fatalf("zero identifier error = %v, want ErrNoMessagesToView", err)
	}
	if sender.count() != 0 {
		t.Fatalf("sent requests = %d, want 0", sender.count())
	}
}

// A refusal reaches the caller as a TDLib error: the interface logs it and
// draws nothing, and the messages stay unread rather than being reported as
// read.
func TestViewMessagesReturnsTheTDLibError(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	resultCh := make(chan error, 1)
	go func() {
		resultCh <- session.ViewMessages(context.Background(), 42, []MessageID{7})
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
		resultCh <- session.ViewMessages(context.Background(), 42, []MessageID{7})
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

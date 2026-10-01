package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// waitForRequestTypeN returns the @extra of the N-th request whose
// @type equals reqType.
func waitForRequestTypeN(
	t *testing.T,
	sender *fakeSender,
	reqType string,
	n int,
) string {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		sender.mu.Lock()
		var extras []string
		for _, request := range sender.requests {
			var envelope struct {
				Type  string `json:"@type"`
				Extra string `json:"@extra"`
			}
			if err := json.Unmarshal(request, &envelope); err != nil {
				sender.mu.Unlock()
				t.Fatalf("decode request: %v", err)
			}
			if envelope.Type == reqType {
				extras = append(extras, envelope.Extra)
			}
		}
		sender.mu.Unlock()

		if len(extras) >= n {
			return extras[n-1]
		}
		time.Sleep(time.Millisecond)
	}

	t.Fatalf("no request #%d with @type=%s", n, reqType)
	return ""
}

// withExtra serializes payload with @extra added.
func withExtra(t *testing.T, payload map[string]any, extra string) RawMessage {
	t.Helper()
	payload["@extra"] = extra
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return RawMessage(raw)
}

func feedResponse(t *testing.T, client *Client, payload map[string]any, extra string) {
	t.Helper()
	client.updates <- Update{
		ClientID: client.id,
		Raw:      withExtra(t, payload, extra),
	}
}

// ---- GetChats ----

func TestGetChatsRejectsInvalidLimit(t *testing.T) {
	session, _, _, _ := newSessionWithFakes(t)

	for _, limit := range []int{-1, 0, 1001, 5000} {
		_, err := session.GetChats(context.Background(), limit)
		if !errors.Is(err, ErrInvalidChatLimit) {
			t.Fatalf("limit=%d: err = %v, want ErrInvalidChatLimit", limit, err)
		}
	}
}

func TestGetChatsHappyPath(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	type result struct {
		snapshot ChatListSnapshot
		err      error
	}
	resultCh := make(chan result, 1)

	go func() {
		snapshot, err := session.GetChats(context.Background(), 50)
		resultCh <- result{snapshot: snapshot, err: err}
	}()

	extra := waitForRequestTypeN(t, sender, "getChats", 1)
	feedResponse(t, client, map[string]any{
		"@type":    "chats",
		"chat_ids": []int64{1, 2},
	}, extra)

	extra = waitForRequestTypeN(t, sender, "getChat", 1)
	feedResponse(t, client, map[string]any{
		"@type":        "chat",
		"id":           1,
		"title":        "Alice",
		"unread_count": 2,
		"last_message": nil,
	}, extra)

	extra = waitForRequestTypeN(t, sender, "getChat", 2)
	feedResponse(t, client, map[string]any{
		"@type":        "chat",
		"id":           2,
		"title":        "Bob",
		"unread_count": 0,
		"last_message": nil,
	}, extra)

	select {
	case r := <-resultCh:
		if r.err != nil {
			t.Fatalf("GetChats: %v", r.err)
		}
		if len(r.snapshot.Chats) != 2 {
			t.Fatalf("len(chats) = %d, want 2", len(r.snapshot.Chats))
		}
		if r.snapshot.Chats[0].ID != 1 || r.snapshot.Chats[0].Title != "Alice" {
			t.Fatalf("chats[0] = %+v", r.snapshot.Chats[0])
		}
		if r.snapshot.Chats[0].UnreadCount != 2 {
			t.Fatalf("chats[0].UnreadCount = %d, want 2", r.snapshot.Chats[0].UnreadCount)
		}
		if r.snapshot.Chats[1].ID != 2 || r.snapshot.Chats[1].Title != "Bob" {
			t.Fatalf("chats[1] = %+v", r.snapshot.Chats[1])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("GetChats did not return")
	}
}

func TestGetChatsEmptyList(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	type result struct {
		snapshot ChatListSnapshot
		err      error
	}
	resultCh := make(chan result, 1)

	go func() {
		snapshot, err := session.GetChats(context.Background(), 50)
		resultCh <- result{snapshot: snapshot, err: err}
	}()

	extra := waitForRequestTypeN(t, sender, "getChats", 1)
	feedResponse(t, client, map[string]any{
		"@type":    "chats",
		"chat_ids": []int64{},
	}, extra)

	select {
	case r := <-resultCh:
		if r.err != nil {
			t.Fatalf("GetChats: %v", r.err)
		}
		if len(r.snapshot.Chats) != 0 {
			t.Fatalf("len(chats) = %d, want 0", len(r.snapshot.Chats))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("GetChats did not return")
	}
}

func TestGetChatsPropagatesTDLibError(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	type result struct {
		err error
	}
	resultCh := make(chan result, 1)

	go func() {
		_, err := session.GetChats(context.Background(), 50)
		resultCh <- result{err: err}
	}()

	extra := waitForRequestTypeN(t, sender, "getChats", 1)
	feedResponse(t, client, map[string]any{
		"@type":   "error",
		"code":    400,
		"message": "bad",
	}, extra)

	select {
	case r := <-resultCh:
		if !errors.Is(r.err, ErrTDLibResponse) {
			t.Fatalf("err = %v, want ErrTDLibResponse", r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("GetChats did not return")
	}
}

func TestGetChatsRejectsWrongResponseType(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	type result struct {
		err error
	}
	resultCh := make(chan result, 1)

	go func() {
		_, err := session.GetChats(context.Background(), 50)
		resultCh <- result{err: err}
	}()

	extra := waitForRequestTypeN(t, sender, "getChats", 1)
	feedResponse(t, client, map[string]any{
		"@type": "notChats",
	}, extra)

	select {
	case r := <-resultCh:
		if !errors.Is(r.err, ErrUnexpectedChatResponse) {
			t.Fatalf("err = %v, want ErrUnexpectedChatResponse", r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("GetChats did not return")
	}
}

// ---- GetChat ----

func TestGetChatRejectsZeroChatID(t *testing.T) {
	session, _, _, _ := newSessionWithFakes(t)

	_, err := session.GetChat(context.Background(), 0)
	if !errors.Is(err, ErrInvalidChatID) {
		t.Fatalf("error = %v, want ErrInvalidChatID", err)
	}
}

func TestGetChatAllowsNegativeChatID(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	type result struct {
		summary ChatSummary
		err     error
	}
	resultCh := make(chan result, 1)

	go func() {
		summary, err := session.GetChat(context.Background(), -1001)
		resultCh <- result{summary: summary, err: err}
	}()

	extra := waitForRequestTypeN(t, sender, "getChat", 1)

	sender.mu.Lock()
	request := append(RawMessage(nil), sender.requests[0]...)
	sender.mu.Unlock()

	var decoded struct {
		ChatID int64 `json:"chat_id"`
	}
	if err := json.Unmarshal(request, &decoded); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if decoded.ChatID != -1001 {
		t.Fatalf("chat_id = %d, want -1001", decoded.ChatID)
	}

	feedResponse(t, client, map[string]any{
		"@type":        "chat",
		"id":           int64(-1001),
		"title":        "Channel",
		"unread_count": 0,
		"last_message": nil,
	}, extra)

	select {
	case r := <-resultCh:
		if r.err != nil {
			t.Fatalf("GetChat() error = %v", r.err)
		}
		if r.summary.ID != -1001 {
			t.Fatalf("ID = %d, want -1001", r.summary.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("GetChat did not return")
	}
}

func TestGetChatExtractsTextContent(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	type result struct {
		summary ChatSummary
		err     error
	}
	resultCh := make(chan result, 1)

	go func() {
		summary, err := session.GetChat(context.Background(), 42)
		resultCh <- result{summary: summary, err: err}
	}()

	extra := waitForRequestTypeN(t, sender, "getChat", 1)
	feedResponse(t, client, map[string]any{
		"@type":        "chat",
		"id":           42,
		"title":        "Alice",
		"unread_count": 1,
		"last_message": map[string]any{
			"@type": "message",
			"id":    99,
			"content": map[string]any{
				"@type": "messageText",
				"text": map[string]any{
					"@type": "formattedText",
					"text":  "hello world",
				},
			},
		},
	}, extra)

	select {
	case r := <-resultCh:
		if r.err != nil {
			t.Fatalf("GetChat: %v", r.err)
		}
		if r.summary.ID != 42 {
			t.Fatalf("ID = %d, want 42", r.summary.ID)
		}
		if r.summary.LastMessageID != 99 {
			t.Fatalf("LastMessageID = %d, want 99", r.summary.LastMessageID)
		}
		if r.summary.LastMessageText != "hello world" {
			t.Fatalf("LastMessageText = %q, want hello world", r.summary.LastMessageText)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("GetChat did not return")
	}
}

// A chat list says what the last message of a chat was, in the words the
// feed draws it in.
//
// The label is the same one the feed of the open chat uses, so a sticker is
// "[sticker 😀]" in both places and a message this build has no words for
// is named by its own type in both (#17). The old preview for an unknown
// content was the same "[unsupported message]" in both, which is what the
// owner read in the list and could not act on (01.10).
func TestGetChatPreviewSaysWhatTheLastMessageCarries(t *testing.T) {
	for _, preview := range []struct {
		name    string
		content map[string]any
		want    string
	}{
		{
			name: "a sticker",
			content: map[string]any{
				"@type":   "messageSticker",
				"sticker": map[string]any{"emoji": "😀"},
			},
			want: "[sticker 😀]",
		},
		{
			name: "a file with a caption",
			content: map[string]any{
				"@type":    "messageDocument",
				"document": map[string]any{"file_name": "счёт.pdf"},
				"caption":  map[string]any{"text": "February"},
			},
			want: "[file счёт.pdf] February",
		},
		{
			name:    "a service message",
			content: map[string]any{"@type": "messageChatJoinByRequest"},
			want:    "joined the chat",
		},
		{
			name:    "a content this build has no words for",
			content: map[string]any{"@type": "messageUnsupported"},
			want:    "[messageUnsupported]",
		},
	} {
		t.Run(preview.name, func(t *testing.T) {
			last := map[string]any{
				"@type":   "message",
				"id":      5,
				"content": preview.content,
			}
			raw, err := json.Marshal(last)
			if err != nil {
				t.Fatalf("marshal the last message: %v", err)
			}

			_, text, _ := parseLastMessage(raw)
			if text != preview.want {
				t.Errorf("the preview reads %q, want %q", text, preview.want)
			}
		})
	}
}

// A chat whose last message is a picture says so, and the word for a
// picture is the word the feed draws.
func TestGetChatPreviewOfAPhoto(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	type result struct {
		summary ChatSummary
		err     error
	}
	resultCh := make(chan result, 1)

	go func() {
		summary, err := session.GetChat(context.Background(), 7)
		resultCh <- result{summary: summary, err: err}
	}()

	extra := waitForRequestTypeN(t, sender, "getChat", 1)
	feedResponse(t, client, map[string]any{
		"@type":        "chat",
		"id":           7,
		"title":        "Bob",
		"unread_count": 0,
		"last_message": map[string]any{
			"@type": "message",
			"id":    5,
			"content": map[string]any{
				"@type": "messagePhoto",
			},
		},
	}, extra)

	select {
	case r := <-resultCh:
		if r.err != nil {
			t.Fatalf("GetChat: %v", r.err)
		}
		if r.summary.LastMessageID != 5 {
			t.Fatalf("LastMessageID = %d, want 5", r.summary.LastMessageID)
		}
		if r.summary.LastMessageText != "[photo]" {
			t.Fatalf("LastMessageText = %q, want [photo]", r.summary.LastMessageText)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("GetChat did not return")
	}
}

func TestGetChatNullLastMessage(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	type result struct {
		summary ChatSummary
		err     error
	}
	resultCh := make(chan result, 1)

	go func() {
		summary, err := session.GetChat(context.Background(), 3)
		resultCh <- result{summary: summary, err: err}
	}()

	extra := waitForRequestTypeN(t, sender, "getChat", 1)
	feedResponse(t, client, map[string]any{
		"@type":        "chat",
		"id":           3,
		"title":        "Empty",
		"unread_count": 0,
		"last_message": nil,
	}, extra)

	select {
	case r := <-resultCh:
		if r.err != nil {
			t.Fatalf("GetChat: %v", r.err)
		}
		if r.summary.LastMessageID != 0 {
			t.Fatalf("LastMessageID = %d, want 0", r.summary.LastMessageID)
		}
		if r.summary.LastMessageText != "" {
			t.Fatalf("LastMessageText = %q, want empty", r.summary.LastMessageText)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("GetChat did not return")
	}
}

func TestGetChatWrongResponseType(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	type result struct {
		err error
	}
	resultCh := make(chan result, 1)

	go func() {
		_, err := session.GetChat(context.Background(), 1)
		resultCh <- result{err: err}
	}()

	extra := waitForRequestTypeN(t, sender, "getChat", 1)
	feedResponse(t, client, map[string]any{
		"@type": "notChat",
	}, extra)

	select {
	case r := <-resultCh:
		if !errors.Is(r.err, ErrUnexpectedChatResponse) {
			t.Fatalf("err = %v, want ErrUnexpectedChatResponse", r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("GetChat did not return")
	}
}

func TestGetChatRejectsMismatchedResponseID(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	resultCh := make(chan error, 1)

	go func() {
		_, err := session.GetChat(context.Background(), 42)
		resultCh <- err
	}()

	extra := waitForRequestTypeN(t, sender, "getChat", 1)
	feedResponse(t, client, map[string]any{
		"@type":        "chat",
		"id":           int64(99),
		"title":        "Wrong chat",
		"unread_count": 0,
		"last_message": nil,
	}, extra)

	select {
	case err := <-resultCh:
		if !errors.Is(err, ErrUnexpectedChatResponse) {
			t.Fatalf("error = %v, want ErrUnexpectedChatResponse", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("GetChat did not return")
	}
}

// ---- parseLastMessage unit coverage ----

func TestParseLastMessageNil(t *testing.T) {
	id, text, at := parseLastMessage(nil)
	if id != 0 || text != "" || !at.IsZero() {
		t.Fatalf("got (%d, %q, %v), want (0, \"\", zero)", id, text, at)
	}

	id, text, at = parseLastMessage(json.RawMessage(`null`))
	if id != 0 || text != "" || !at.IsZero() {
		t.Fatalf("got (%d, %q, %v), want (0, \"\", zero)", id, text, at)
	}
}

// A message of a chat list keeps its place in the row even where its words
// could not be read.
//
// The identifier of a last message is what a read of that chat is placed by,
// and a content this build cannot decode must not cost the row its number.
func TestParseLastMessageOfAnUnreadableContentKeepsTheIdentifier(t *testing.T) {
	raw := json.RawMessage(
		`{"@type":"message","id":9,"date":1700000000,"content":{"@type":42}}`,
	)
	id, text, at := parseLastMessage(raw)
	if id != 9 || text != "" || at.IsZero() {
		t.Fatalf("got (%d, %q, %v), want (9, \"\", a moment)", id, text, at)
	}
}

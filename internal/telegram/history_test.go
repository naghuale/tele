package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// waitForRequestN returns the raw bytes of the N-th request whose @type
// equals reqType.
func waitForRequestN(
	t *testing.T,
	sender *fakeSender,
	reqType string,
	n int,
) RawMessage {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		sender.mu.Lock()
		var found []RawMessage
		for _, request := range sender.requests {
			var envelope struct {
				Type string `json:"@type"`
			}
			if err := json.Unmarshal(request, &envelope); err != nil {
				sender.mu.Unlock()
				t.Fatalf("decode request: %v", err)
			}
			if envelope.Type == reqType {
				found = append(found, append(RawMessage(nil), request...))
			}
		}
		sender.mu.Unlock()

		if len(found) >= n {
			return found[n-1]
		}
		time.Sleep(time.Millisecond)
	}

	t.Fatalf("no request #%d with @type=%s", n, reqType)
	return nil
}

// decodeExtra reads @extra from a serialized request and fails on any
// decode error.
func decodeExtra(t *testing.T, request RawMessage) string {
	t.Helper()
	var decoded struct {
		Extra string `json:"@extra"`
	}
	if err := json.Unmarshal(request, &decoded); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if decoded.Extra == "" {
		t.Fatal("request has no @extra")
	}
	return decoded.Extra
}

// ---- Validation ----

func TestGetChatHistoryRejectsZeroChatID(t *testing.T) {
	session, _, _, _ := newSessionWithFakes(t)

	_, err := session.GetChatHistory(context.Background(), 0, 0, 50)
	if !errors.Is(err, ErrInvalidChatID) {
		t.Fatalf("error = %v, want ErrInvalidChatID", err)
	}
}

func TestGetChatHistoryRejectsInvalidLimit(t *testing.T) {
	session, _, _, _ := newSessionWithFakes(t)

	for _, limit := range []int{-1, 0, 101, 500} {
		_, err := session.GetChatHistory(context.Background(), 1, 0, limit)
		if !errors.Is(err, ErrInvalidHistoryLimit) {
			t.Fatalf(
				"limit=%d: error = %v, want ErrInvalidHistoryLimit",
				limit, err,
			)
		}
	}
}

// ---- Request shape ----

func TestGetChatHistoryRequestShape(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	resultCh := make(chan error, 1)
	go func() {
		_, err := session.GetChatHistory(context.Background(), 42, 7, 50)
		resultCh <- err
	}()

	request := waitForRequestN(t, sender, "getChatHistory", 1)

	var decoded struct {
		Type          string `json:"@type"`
		ChatID        int64  `json:"chat_id"`
		FromMessageID int64  `json:"from_message_id"`
		Offset        int    `json:"offset"`
		Limit         int    `json:"limit"`
		OnlyLocal     bool   `json:"only_local"`
		Extra         string `json:"@extra"`
	}
	if err := json.Unmarshal(request, &decoded); err != nil {
		t.Fatalf("decode request: %v", err)
	}

	if decoded.Type != "getChatHistory" {
		t.Fatalf("@type = %q", decoded.Type)
	}
	if decoded.ChatID != 42 {
		t.Fatalf("chat_id = %d, want 42", decoded.ChatID)
	}
	if decoded.FromMessageID != 7 {
		t.Fatalf("from_message_id = %d, want 7", decoded.FromMessageID)
	}
	if decoded.Offset != 0 {
		t.Fatalf("offset = %d, want 0", decoded.Offset)
	}
	if decoded.Limit != 50 {
		t.Fatalf("limit = %d, want 50", decoded.Limit)
	}
	if decoded.OnlyLocal {
		t.Fatal("only_local = true, want false")
	}
	if decoded.Extra == "" {
		t.Fatal("@extra missing")
	}

	feedResponse(t, client, map[string]any{
		"@type":    "messages",
		"messages": []any{},
	}, decoded.Extra)

	select {
	case err := <-resultCh:
		if err != nil {
			t.Fatalf("GetChatHistory: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("GetChatHistory did not return")
	}
}

func TestGetChatHistoryAllowsNegativeChatID(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	type result struct {
		page HistoryPage
		err  error
	}
	resultCh := make(chan result, 1)
	go func() {
		page, err := session.GetChatHistory(context.Background(), -1001, 0, 10)
		resultCh <- result{page: page, err: err}
	}()

	request := waitForRequestN(t, sender, "getChatHistory", 1)

	var decoded struct {
		ChatID int64  `json:"chat_id"`
		Extra  string `json:"@extra"`
	}
	if err := json.Unmarshal(request, &decoded); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if decoded.ChatID != -1001 {
		t.Fatalf("chat_id = %d, want -1001", decoded.ChatID)
	}

	feedResponse(t, client, map[string]any{
		"@type":    "messages",
		"messages": []any{},
	}, decoded.Extra)

	select {
	case r := <-resultCh:
		if r.err != nil {
			t.Fatalf("GetChatHistory: %v", r.err)
		}
		if len(r.page.Messages) != 0 {
			t.Fatalf("len(messages) = %d, want 0", len(r.page.Messages))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("GetChatHistory did not return")
	}
}

// ---- Boundary semantics ----

// fromMessageID is an inclusive boundary: the response may include a
// message whose ID equals the requested from_message_id. Callers are
// expected to de-duplicate by Message.ID when concatenating pages.
func TestGetChatHistoryUsesInclusiveFromBoundary(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	type result struct {
		page HistoryPage
		err  error
	}
	resultCh := make(chan result, 1)

	go func() {
		page, err := session.GetChatHistory(context.Background(), 42, 199, 10)
		resultCh <- result{page: page, err: err}
	}()

	request := waitForRequestN(t, sender, "getChatHistory", 1)

	var decoded struct {
		FromMessageID int64  `json:"from_message_id"`
		Offset        int    `json:"offset"`
		Extra         string `json:"@extra"`
	}
	if err := json.Unmarshal(request, &decoded); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if decoded.FromMessageID != 199 {
		t.Fatalf("from_message_id = %d, want 199", decoded.FromMessageID)
	}
	if decoded.Offset != 0 {
		t.Fatalf("offset = %d, want 0", decoded.Offset)
	}

	feedResponse(t, client, map[string]any{
		"@type": "messages",
		"messages": []any{
			map[string]any{
				"@type":   "message",
				"id":      int64(199),
				"chat_id": int64(42),
				"date":    int64(1),
				"content": map[string]any{
					"@type": "messageText",
					"text": map[string]any{
						"@type": "formattedText",
						"text":  "boundary",
					},
				},
			},
		},
	}, decoded.Extra)

	select {
	case r := <-resultCh:
		if r.err != nil {
			t.Fatalf("GetChatHistory() error = %v", r.err)
		}
		if len(r.page.Messages) != 1 {
			t.Fatalf("message count = %d, want 1", len(r.page.Messages))
		}
		if r.page.Messages[0].ID != 199 {
			t.Fatalf("message ID = %d, want 199", r.page.Messages[0].ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("GetChatHistory did not return")
	}
}

// ---- Happy path ----

func TestGetChatHistoryHappyPath(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	type result struct {
		page HistoryPage
		err  error
	}
	resultCh := make(chan result, 1)
	go func() {
		page, err := session.GetChatHistory(context.Background(), 42, 0, 50)
		resultCh <- result{page: page, err: err}
	}()

	request := waitForRequestN(t, sender, "getChatHistory", 1)
	extra := decodeExtra(t, request)

	feedResponse(t, client, map[string]any{
		"@type": "messages",
		"messages": []any{
			map[string]any{
				"@type":       "message",
				"id":          int64(200),
				"chat_id":     int64(42),
				"is_outgoing": true,
				"date":        int64(1700000000),
				"content": map[string]any{
					"@type": "messageText",
					"text": map[string]any{
						"@type": "formattedText",
						"text":  "newest",
					},
				},
			},
			map[string]any{
				"@type":       "message",
				"id":          int64(199),
				"chat_id":     int64(42),
				"is_outgoing": false,
				"date":        int64(1699999900),
				"content": map[string]any{
					"@type": "messageText",
					"text": map[string]any{
						"@type": "formattedText",
						"text":  "older",
					},
				},
			},
		},
	}, extra)

	select {
	case r := <-resultCh:
		if r.err != nil {
			t.Fatalf("GetChatHistory: %v", r.err)
		}
		if len(r.page.Messages) != 2 {
			t.Fatalf("len(messages) = %d, want 2", len(r.page.Messages))
		}
		first := r.page.Messages[0]
		if first.ID != 200 {
			t.Fatalf("messages[0].ID = %d, want 200", first.ID)
		}
		if first.ChatID != 42 {
			t.Fatalf("messages[0].ChatID = %d, want 42", first.ChatID)
		}
		if !first.Outgoing {
			t.Fatal("messages[0].Outgoing = false, want true")
		}
		// The instant, and not a moment written in some zone: TDLib sends a
		// count of seconds and the zone is the reader's. A projection that
		// stamped it as UTC wrote 21:21 on a screen of a user ten hours
		// east, which is what the owner reported on 29.09.2026.
		if got := first.Timestamp; !got.Equal(time.Unix(1700000000, 0)) {
			t.Fatalf("messages[0].Timestamp = %v", got)
		}
		if first.Text != "newest" {
			t.Fatalf("messages[0].Text = %q, want newest", first.Text)
		}
		if r.page.Messages[1].Text != "older" {
			t.Fatalf("messages[1].Text = %q, want older", r.page.Messages[1].Text)
		}
		if r.page.NextFrom != 199 {
			t.Fatalf("NextFrom = %d, want 199", r.page.NextFrom)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("GetChatHistory did not return")
	}
}

func TestGetChatHistoryEmptyPage(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	type result struct {
		page HistoryPage
		err  error
	}
	resultCh := make(chan result, 1)
	go func() {
		page, err := session.GetChatHistory(context.Background(), 1, 0, 10)
		resultCh <- result{page: page, err: err}
	}()

	request := waitForRequestN(t, sender, "getChatHistory", 1)
	extra := decodeExtra(t, request)

	feedResponse(t, client, map[string]any{
		"@type":    "messages",
		"messages": []any{},
	}, extra)

	select {
	case r := <-resultCh:
		if r.err != nil {
			t.Fatalf("GetChatHistory: %v", r.err)
		}
		if len(r.page.Messages) != 0 {
			t.Fatalf("len(messages) = %d, want 0", len(r.page.Messages))
		}
		if r.page.NextFrom != 0 {
			t.Fatalf("NextFrom = %d, want 0", r.page.NextFrom)
		}
		if r.page.HasMore {
			t.Fatal("HasMore = true, want false")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("GetChatHistory did not return")
	}
}

// ---- HasMore ----

// A page that came back with something in it says there may be more, and a
// full page is only one way of coming back with something.
func TestGetChatHistoryHasMoreWhenTheAnswerWasNotEmpty(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	type result struct {
		page HistoryPage
		err  error
	}
	resultCh := make(chan result, 1)
	go func() {
		page, err := session.GetChatHistory(context.Background(), 5, 0, 2)
		resultCh <- result{page: page, err: err}
	}()

	request := waitForRequestN(t, sender, "getChatHistory", 1)
	extra := decodeExtra(t, request)

	feedResponse(t, client, map[string]any{
		"@type": "messages",
		"messages": []any{
			map[string]any{
				"@type": "message", "id": int64(2), "chat_id": int64(5),
				"date": int64(1), "content": map[string]any{"@type": "messageText",
					"text": map[string]any{"@type": "formattedText", "text": "a"}},
			},
			map[string]any{
				"@type": "message", "id": int64(1), "chat_id": int64(5),
				"date": int64(1), "content": map[string]any{"@type": "messageText",
					"text": map[string]any{"@type": "formattedText", "text": "b"}},
			},
		},
	}, extra)

	select {
	case r := <-resultCh:
		if r.err != nil {
			t.Fatalf("GetChatHistory: %v", r.err)
		}
		if !r.page.HasMore {
			t.Fatal("HasMore = false, want true for a page with two in it")
		}
		if r.page.NextFrom != 1 {
			t.Fatalf("NextFrom = %d, want 1", r.page.NextFrom)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("GetChatHistory did not return")
	}
}

// ---- What a message carries ----

// A message that carries a file has no text of its own: the interface says
// what the message is in its own words through the Media field, and a
// placeholder in the text would be drawn twice. A kind this build has no
// words for is called a message, because the name TDLib gives it is not a
// word on a row a user reads to find out what arrived (#50) — it used to be
// named by nothing at all, and the feed said "[unsupported message]".
func TestGetChatHistoryMedia(t *testing.T) {
	cases := []struct {
		contentType string
		wantMedia   string
	}{
		{"messagePhoto", "photo"},
		{"messageVideo", "video"},
		{"messageDocument", "file"},
		{"messageVoiceNote", "voice note"},
		{"messageFuture", "message"},
		{"messageRichMessage", "message"},
	}

	for _, tc := range cases {
		t.Run(tc.contentType, func(t *testing.T) {
			session, sender, _, client := newSessionWithFakes(t)

			type result struct {
				page HistoryPage
				err  error
			}
			resultCh := make(chan result, 1)
			go func() {
				page, err := session.GetChatHistory(context.Background(), 9, 0, 1)
				resultCh <- result{page: page, err: err}
			}()

			request := waitForRequestN(t, sender, "getChatHistory", 1)
			extra := decodeExtra(t, request)

			feedResponse(t, client, map[string]any{
				"@type": "messages",
				"messages": []any{
					map[string]any{
						"@type":   "message",
						"id":      int64(100),
						"chat_id": int64(9),
						"date":    int64(1),
						"content": map[string]any{"@type": tc.contentType},
					},
				},
			}, extra)

			select {
			case r := <-resultCh:
				if r.err != nil {
					t.Fatalf("GetChatHistory: %v", r.err)
				}
				if len(r.page.Messages) != 1 {
					t.Fatalf("len(messages) = %d, want 1", len(r.page.Messages))
				}
				if r.page.Messages[0].Media != tc.wantMedia {
					t.Fatalf("Media = %q, want %q",
						r.page.Messages[0].Media, tc.wantMedia)
				}
				if r.page.Messages[0].Text != "" {
					t.Fatalf("Text = %q, want a message with no text of its own",
						r.page.Messages[0].Text)
				}
				if r.page.Messages[0].ID != 100 {
					t.Fatalf("ID = %d, want 100", r.page.Messages[0].ID)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("GetChatHistory did not return")
			}
		})
	}
}

// ---- Error paths ----

func TestGetChatHistoryWrongResponseType(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	resultCh := make(chan error, 1)
	go func() {
		_, err := session.GetChatHistory(context.Background(), 1, 0, 10)
		resultCh <- err
	}()

	request := waitForRequestN(t, sender, "getChatHistory", 1)
	extra := decodeExtra(t, request)

	feedResponse(t, client, map[string]any{
		"@type": "notMessages",
	}, extra)

	select {
	case err := <-resultCh:
		if !errors.Is(err, ErrUnexpectedHistoryResponse) {
			t.Fatalf("error = %v, want ErrUnexpectedHistoryResponse", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("GetChatHistory did not return")
	}
}

func TestGetChatHistoryMismatchedChatID(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	resultCh := make(chan error, 1)
	go func() {
		_, err := session.GetChatHistory(context.Background(), 42, 0, 10)
		resultCh <- err
	}()

	request := waitForRequestN(t, sender, "getChatHistory", 1)
	extra := decodeExtra(t, request)

	feedResponse(t, client, map[string]any{
		"@type": "messages",
		"messages": []any{
			map[string]any{
				"@type":   "message",
				"id":      int64(1),
				"chat_id": int64(99), // wrong
				"date":    int64(1),
				"content": map[string]any{"@type": "messageText",
					"text": map[string]any{"@type": "formattedText", "text": "x"}},
			},
		},
	}, extra)

	select {
	case err := <-resultCh:
		if !errors.Is(err, ErrUnexpectedHistoryResponse) {
			t.Fatalf("error = %v, want ErrUnexpectedHistoryResponse", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("GetChatHistory did not return")
	}
}

func TestGetChatHistoryRejectsNonMessageEntry(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	resultCh := make(chan error, 1)
	go func() {
		_, err := session.GetChatHistory(context.Background(), 42, 0, 10)
		resultCh <- err
	}()

	request := waitForRequestN(t, sender, "getChatHistory", 1)
	extra := decodeExtra(t, request)

	feedResponse(t, client, map[string]any{
		"@type": "messages",
		"messages": []any{
			map[string]any{"@type": "notMessage"},
		},
	}, extra)

	select {
	case err := <-resultCh:
		if !errors.Is(err, ErrUnexpectedHistoryResponse) {
			t.Fatalf("error = %v, want ErrUnexpectedHistoryResponse", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("GetChatHistory did not return")
	}
}

func TestGetChatHistoryPropagatesTDLibError(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	resultCh := make(chan error, 1)
	go func() {
		_, err := session.GetChatHistory(context.Background(), 1, 0, 10)
		resultCh <- err
	}()

	request := waitForRequestN(t, sender, "getChatHistory", 1)
	extra := decodeExtra(t, request)

	feedResponse(t, client, map[string]any{
		"@type":   "error",
		"code":    400,
		"message": "bad",
	}, extra)

	select {
	case err := <-resultCh:
		if !errors.Is(err, ErrTDLibResponse) {
			t.Fatalf("error = %v, want ErrTDLibResponse", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("GetChatHistory did not return")
	}
}

package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"
)

// ---- Validation ----

func TestSendTextMessageRejectsZeroChatID(t *testing.T) {
	session, sender, _, _ := newSessionWithFakes(t)

	_, err := session.SendTextMessage(context.Background(), 0, "hello")
	if !errors.Is(err, ErrInvalidChatID) {
		t.Fatalf("error = %v, want ErrInvalidChatID", err)
	}
	if sender.count() != 0 {
		t.Fatalf("sent requests = %d, want 0", sender.count())
	}
}

func TestSendTextMessageRejectsEmptyText(t *testing.T) {
	for _, text := range []string{
		"",
		" ",
		"\t",
		"\n",
		"  \t\n ",
	} {
		t.Run(fmt.Sprintf("%q", text), func(t *testing.T) {
			session, sender, _, _ := newSessionWithFakes(t)

			_, err := session.SendTextMessage(
				context.Background(),
				1,
				text,
			)
			if !errors.Is(err, ErrEmptyMessageText) {
				t.Fatalf("error = %v, want ErrEmptyMessageText", err)
			}
			if sender.count() != 0 {
				t.Fatalf("sent requests = %d, want 0", sender.count())
			}
		})
	}
}

// ---- Request shape ----

func TestSendTextMessageRequestShape(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	resultCh := make(chan error, 1)
	go func() {
		_, err := session.SendTextMessage(context.Background(), 42, "hello")
		resultCh <- err
	}()

	request := waitForRequestN(t, sender, "sendMessage", 1)

	var decoded struct {
		Type   string `json:"@type"`
		ChatID int64  `json:"chat_id"`
		Extra  string `json:"@extra"`

		InputMessageContent struct {
			Type string `json:"@type"`
			Text struct {
				Type     string `json:"@type"`
				Text     string `json:"text"`
				Entities []any  `json:"entities"`
			} `json:"text"`
			ClearDraft bool `json:"clear_draft"`
		} `json:"input_message_content"`
	}
	if err := json.Unmarshal(request, &decoded); err != nil {
		t.Fatalf("decode request: %v", err)
	}

	if decoded.Type != "sendMessage" {
		t.Fatalf("@type = %q, want sendMessage", decoded.Type)
	}
	if decoded.ChatID != 42 {
		t.Fatalf("chat_id = %d, want 42", decoded.ChatID)
	}
	if decoded.Extra == "" {
		t.Fatal("@extra is empty")
	}

	content := decoded.InputMessageContent
	if content.Type != "inputMessageText" {
		t.Fatalf("content @type = %q, want inputMessageText", content.Type)
	}
	if content.Text.Type != "formattedText" {
		t.Fatalf("text @type = %q, want formattedText", content.Text.Type)
	}
	if content.Text.Text != "hello" {
		t.Fatalf("text = %q, want hello", content.Text.Text)
	}
	if content.Text.Entities == nil {
		t.Fatal("entities must be present (possibly empty)")
	}
	if len(content.Text.Entities) != 0 {
		t.Fatalf("entities = %v, want empty", content.Text.Entities)
	}
	if !content.ClearDraft {
		t.Fatal("clear_draft = false, want true")
	}

	// Prove that optional fields are present with a JSON null, not
	// silently omitted. A pointer field cannot distinguish those two
	// cases, so inspect raw JSON.
	var rawObject map[string]json.RawMessage
	if err := json.Unmarshal(request, &rawObject); err != nil {
		t.Fatalf("decode raw request: %v", err)
	}

	for _, field := range []string{
		"topic_id",
		"reply_to",
		"options",
		"reply_markup",
	} {
		value, exists := rawObject[field]
		if !exists {
			t.Fatalf("field %q is missing", field)
		}
		if string(value) != "null" {
			t.Fatalf("field %q = %s, want null", field, value)
		}
	}

	var contentObject map[string]json.RawMessage
	if err := json.Unmarshal(
		rawObject["input_message_content"],
		&contentObject,
	); err != nil {
		t.Fatalf("decode input_message_content: %v", err)
	}

	linkPreview, exists := contentObject["link_preview_options"]
	if !exists {
		t.Fatal("link_preview_options is missing")
	}
	if string(linkPreview) != "null" {
		t.Fatalf("link_preview_options = %s, want null", linkPreview)
	}

	feedResponse(t, client, map[string]any{
		"@type":   "message",
		"id":      int64(1),
		"chat_id": int64(42),
	}, decoded.Extra)

	select {
	case err := <-resultCh:
		if err != nil {
			t.Fatalf("SendTextMessage: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SendTextMessage did not return")
	}
}

func TestSendTextMessagePreservesOriginalWhitespace(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	resultCh := make(chan error, 1)
	go func() {
		_, err := session.SendTextMessage(context.Background(), 7, "  hello  ")
		resultCh <- err
	}()

	request := waitForRequestN(t, sender, "sendMessage", 1)

	var decoded struct {
		Extra               string `json:"@extra"`
		InputMessageContent struct {
			Text struct {
				Text string `json:"text"`
			} `json:"text"`
		} `json:"input_message_content"`
	}
	if err := json.Unmarshal(request, &decoded); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if got := decoded.InputMessageContent.Text.Text; got != "  hello  " {
		t.Fatalf("text = %q, want %q", got, "  hello  ")
	}

	feedResponse(t, client, map[string]any{
		"@type":   "message",
		"id":      int64(1),
		"chat_id": int64(7),
	}, decoded.Extra)

	select {
	case err := <-resultCh:
		if err != nil {
			t.Fatalf("SendTextMessage: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SendTextMessage did not return")
	}
}

// ---- Happy paths ----

func TestSendTextMessageAllowsNegativeChatID(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	type result struct {
		message Message
		err     error
	}
	resultCh := make(chan result, 1)
	go func() {
		message, err := session.SendTextMessage(context.Background(), -1001, "hi")
		resultCh <- result{message: message, err: err}
	}()

	request := waitForRequestN(t, sender, "sendMessage", 1)

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
		"@type":   "message",
		"id":      int64(1),
		"chat_id": int64(-1001),
	}, decoded.Extra)

	select {
	case r := <-resultCh:
		if r.err != nil {
			t.Fatalf("SendTextMessage: %v", r.err)
		}
		if r.message.ChatID != -1001 {
			t.Fatalf("message.ChatID = %d, want -1001", r.message.ChatID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SendTextMessage did not return")
	}
}

func TestSendTextMessageHappyPath(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	type result struct {
		message Message
		err     error
	}
	resultCh := make(chan result, 1)
	go func() {
		message, err := session.SendTextMessage(context.Background(), 42, "hello")
		resultCh <- result{message: message, err: err}
	}()

	request := waitForRequestN(t, sender, "sendMessage", 1)

	var decoded struct {
		Extra string `json:"@extra"`
	}
	if err := json.Unmarshal(request, &decoded); err != nil {
		t.Fatalf("decode request: %v", err)
	}

	feedResponse(t, client, map[string]any{
		"@type":       "message",
		"id":          int64(9001),
		"chat_id":     int64(42),
		"is_outgoing": true,
		"date":        int64(1700000000),
		"content": map[string]any{
			"@type": "messageText",
			"text": map[string]any{
				"@type": "formattedText",
				"text":  "hello",
			},
		},
	}, decoded.Extra)

	select {
	case r := <-resultCh:
		if r.err != nil {
			t.Fatalf("SendTextMessage: %v", r.err)
		}
		if r.message.ID != 9001 {
			t.Fatalf("message.ID = %d, want 9001", r.message.ID)
		}
		if r.message.ChatID != 42 {
			t.Fatalf("message.ChatID = %d, want 42", r.message.ChatID)
		}
		if !r.message.Outgoing {
			t.Fatal("message.Outgoing = false, want true")
		}
		// The instant and not a moment in a chosen zone: TDLib sends seconds, and
		// who reads them in which zone is the interface's business.
		if got := r.message.Timestamp; !got.Equal(time.Unix(1700000000, 0)) {
			t.Fatalf("message.Timestamp = %v", got)
		}
		if r.message.Text != "hello" {
			t.Fatalf("message.Text = %q, want hello", r.message.Text)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SendTextMessage did not return")
	}
}

func TestSendTextMessagePlaceholderContent(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	type result struct {
		message Message
		err     error
	}
	resultCh := make(chan result, 1)
	go func() {
		message, err := session.SendTextMessage(context.Background(), 42, "caption")
		resultCh <- result{message: message, err: err}
	}()

	request := waitForRequestN(t, sender, "sendMessage", 1)

	var decoded struct {
		Extra string `json:"@extra"`
	}
	if err := json.Unmarshal(request, &decoded); err != nil {
		t.Fatalf("decode request: %v", err)
	}

	feedResponse(t, client, map[string]any{
		"@type":       "message",
		"id":          int64(7),
		"chat_id":     int64(42),
		"is_outgoing": true,
		"date":        int64(1),
		"content":     map[string]any{"@type": "messagePhoto"},
	}, decoded.Extra)

	select {
	case r := <-resultCh:
		if r.err != nil {
			t.Fatalf("SendTextMessage: %v", r.err)
		}
		if r.message.ID != 7 {
			t.Fatalf("message.ID = %d, want 7", r.message.ID)
		}
		if r.message.Media != "photo" {
			t.Fatalf("message.Media = %q, want photo", r.message.Media)
		}
		if r.message.Text != "" {
			t.Fatalf("message.Text = %q, want a message with no text of its own",
				r.message.Text)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SendTextMessage did not return")
	}
}

// ---- Error paths ----

func TestSendTextMessagePropagatesTDLibError(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	resultCh := make(chan error, 1)
	go func() {
		_, err := session.SendTextMessage(context.Background(), 42, "hello")
		resultCh <- err
	}()

	request := waitForRequestN(t, sender, "sendMessage", 1)

	var decoded struct {
		Extra string `json:"@extra"`
	}
	if err := json.Unmarshal(request, &decoded); err != nil {
		t.Fatalf("decode request: %v", err)
	}

	feedResponse(t, client, map[string]any{
		"@type":   "error",
		"code":    400,
		"message": "bad",
	}, decoded.Extra)

	select {
	case err := <-resultCh:
		if !errors.Is(err, ErrTDLibResponse) {
			t.Fatalf("error = %v, want ErrTDLibResponse", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SendTextMessage did not return")
	}
}

func TestSendTextMessageRejectsWrongResponseType(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	resultCh := make(chan error, 1)
	go func() {
		_, err := session.SendTextMessage(context.Background(), 42, "hello")
		resultCh <- err
	}()

	request := waitForRequestN(t, sender, "sendMessage", 1)

	var decoded struct {
		Extra string `json:"@extra"`
	}
	if err := json.Unmarshal(request, &decoded); err != nil {
		t.Fatalf("decode request: %v", err)
	}

	feedResponse(t, client, map[string]any{
		"@type": "notMessage",
	}, decoded.Extra)

	select {
	case err := <-resultCh:
		if !errors.Is(err, ErrUnexpectedSendResponse) {
			t.Fatalf("error = %v, want ErrUnexpectedSendResponse", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SendTextMessage did not return")
	}
}

func TestSendTextMessageRejectsMismatchedChatID(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	resultCh := make(chan error, 1)
	go func() {
		_, err := session.SendTextMessage(context.Background(), 42, "hello")
		resultCh <- err
	}()

	request := waitForRequestN(t, sender, "sendMessage", 1)

	var decoded struct {
		Extra string `json:"@extra"`
	}
	if err := json.Unmarshal(request, &decoded); err != nil {
		t.Fatalf("decode request: %v", err)
	}

	feedResponse(t, client, map[string]any{
		"@type":   "message",
		"id":      int64(1),
		"chat_id": int64(99), // wrong
	}, decoded.Extra)

	select {
	case err := <-resultCh:
		if !errors.Is(err, ErrUnexpectedSendResponse) {
			t.Fatalf("error = %v, want ErrUnexpectedSendResponse", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SendTextMessage did not return")
	}
}

func TestSendTextMessageRejectsZeroMessageID(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	resultCh := make(chan error, 1)
	go func() {
		_, err := session.SendTextMessage(context.Background(), 42, "hello")
		resultCh <- err
	}()

	request := waitForRequestN(t, sender, "sendMessage", 1)

	var decoded struct {
		Extra string `json:"@extra"`
	}
	if err := json.Unmarshal(request, &decoded); err != nil {
		t.Fatalf("decode request: %v", err)
	}

	feedResponse(t, client, map[string]any{
		"@type":   "message",
		"id":      int64(0),
		"chat_id": int64(42),
	}, decoded.Extra)

	select {
	case err := <-resultCh:
		if !errors.Is(err, ErrUnexpectedSendResponse) {
			t.Fatalf("error = %v, want ErrUnexpectedSendResponse", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SendTextMessage did not return")
	}
}

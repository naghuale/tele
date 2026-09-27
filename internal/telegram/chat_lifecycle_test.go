package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// TDLib only counts the online members of a chat that has been opened, so
// the session is told which chat the user is looking at.

func TestOpenChatSendsTheRequestForTheChat(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	resultCh := make(chan error, 1)
	go func() {
		resultCh <- session.OpenChat(context.Background(), 42)
	}()

	request := waitForRequestN(t, sender, "openChat", 1)

	var decoded struct {
		Type   string `json:"@type"`
		ChatID int64  `json:"chat_id"`
		Extra  string `json:"@extra"`
	}
	if err := json.Unmarshal(request, &decoded); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if decoded.Type != "openChat" {
		t.Fatalf("@type = %q, want openChat", decoded.Type)
	}
	if decoded.ChatID != 42 {
		t.Fatalf("chat_id = %d, want 42", decoded.ChatID)
	}
	if decoded.Extra == "" {
		t.Fatal("@extra is empty")
	}

	feedResponse(t, client, map[string]any{"@type": "ok"}, decoded.Extra)

	select {
	case err := <-resultCh:
		if err != nil {
			t.Fatalf("OpenChat: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OpenChat did not return")
	}
}

func TestCloseChatSendsTheRequestForTheChat(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	resultCh := make(chan error, 1)
	go func() {
		resultCh <- session.CloseChat(context.Background(), 42)
	}()

	request := waitForRequestN(t, sender, "closeChat", 1)

	var decoded struct {
		ChatID int64  `json:"chat_id"`
		Extra  string `json:"@extra"`
	}
	if err := json.Unmarshal(request, &decoded); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if decoded.ChatID != 42 {
		t.Fatalf("chat_id = %d, want 42", decoded.ChatID)
	}

	feedResponse(t, client, map[string]any{"@type": "ok"}, decoded.Extra)

	select {
	case err := <-resultCh:
		if err != nil {
			t.Fatalf("CloseChat: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("CloseChat did not return")
	}
}

func TestTheChatLifecycleRejectsAZeroChatID(t *testing.T) {
	session, sender, _, _ := newSessionWithFakes(t)

	if err := session.OpenChat(context.Background(), 0); !errors.Is(err, ErrInvalidChatID) {
		t.Fatalf("OpenChat error = %v, want ErrInvalidChatID", err)
	}
	if err := session.CloseChat(context.Background(), 0); !errors.Is(err, ErrInvalidChatID) {
		t.Fatalf("CloseChat error = %v, want ErrInvalidChatID", err)
	}
	if sender.count() != 0 {
		t.Fatalf("sent requests = %d, want 0", sender.count())
	}
}

// A refusal comes back as a TDLib error, and it reaches the caller as one:
// the interface logs it and draws nothing, so a caller that wants to say
// something has a code to work with rather than a string.
func TestOpenChatReturnsTheTDLibError(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	resultCh := make(chan error, 1)
	go func() {
		resultCh <- session.OpenChat(context.Background(), 42)
	}()

	request := waitForRequestN(t, sender, "openChat", 1)
	var decoded struct {
		Extra string `json:"@extra"`
	}
	if err := json.Unmarshal(request, &decoded); err != nil {
		t.Fatalf("decode request: %v", err)
	}

	feedResponse(t, client, map[string]any{
		"@type":   "error",
		"code":    400,
		"message": "CHAT_INVALID",
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
		t.Fatal("OpenChat did not return")
	}
}

// The schema says both calls return ok, so anything else is a protocol
// surprise and is reported rather than read as a success.
func TestTheChatLifecycleRejectsAnUnexpectedAnswer(t *testing.T) {
	for _, method := range []string{"openChat", "closeChat"} {
		t.Run(method, func(t *testing.T) {
			session, sender, _, client := newSessionWithFakes(t)

			resultCh := make(chan error, 1)
			go func() {
				switch method {
				case "openChat":
					resultCh <- session.OpenChat(context.Background(), 42)
				default:
					resultCh <- session.CloseChat(context.Background(), 42)
				}
			}()

			request := waitForRequestN(t, sender, method, 1)
			var decoded struct {
				Extra string `json:"@extra"`
			}
			if err := json.Unmarshal(request, &decoded); err != nil {
				t.Fatalf("decode request: %v", err)
			}

			feedResponse(t, client, map[string]any{
				"@type": "chat",
				"id":    int64(42),
			}, decoded.Extra)

			select {
			case err := <-resultCh:
				if !errors.Is(err, ErrChatLifecycleResponse) {
					t.Fatalf("error = %v, want ErrChatLifecycleResponse", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("the call did not return")
			}
		})
	}
}

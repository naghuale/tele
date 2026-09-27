package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// Saved Messages is a chat with oneself, and nothing in a user update says
// which user that is. getMe is the only answer, and only the identifier
// comes back out of it.

func TestGetMeUserIDSendsGetMeAndReturnsTheID(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	resultCh := make(chan int64, 1)
	errCh := make(chan error, 1)
	go func() {
		id, err := session.GetMeUserID(context.Background())
		resultCh <- id
		errCh <- err
	}()

	request := waitForRequestN(t, sender, "getMe", 1)

	var decoded struct {
		Type  string `json:"@type"`
		Extra string `json:"@extra"`
	}
	if err := json.Unmarshal(request, &decoded); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if decoded.Type != "getMe" {
		t.Fatalf("@type = %q, want getMe", decoded.Type)
	}

	feedResponse(t, client, map[string]any{
		"@type":        "user",
		"id":           int64(4242),
		"phone_number": "+15550100",
	}, decoded.Extra)

	select {
	case id := <-resultCh:
		if err := <-errCh; err != nil {
			t.Fatalf("GetMeUserID: %v", err)
		}
		if id != 4242 {
			t.Fatalf("id = %d, want 4242", id)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("GetMeUserID did not return")
	}
}

// getMe is answered with an error, and the error is what the caller has to
// work with: a missing own id is a presence that cannot be told apart from
// a contact's, not a reason to refuse to start.
func TestGetMeUserIDReturnsTheTDLibError(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	errCh := make(chan error, 1)
	go func() {
		_, err := session.GetMeUserID(context.Background())
		errCh <- err
	}()

	request := waitForRequestN(t, sender, "getMe", 1)
	var decoded struct {
		Extra string `json:"@extra"`
	}
	if err := json.Unmarshal(request, &decoded); err != nil {
		t.Fatalf("decode request: %v", err)
	}

	feedResponse(t, client, map[string]any{
		"@type":   "error",
		"code":    401,
		"message": "Unauthorized",
	}, decoded.Extra)

	select {
	case err := <-errCh:
		var tdlibErr *TDLibError
		if !errors.As(err, &tdlibErr) {
			t.Fatalf("error = %v, want a TDLib error", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("GetMeUserID did not return")
	}
}

// A user object with no id is not an answer to a question about who the
// user is, and zero would be a real identifier to everything that reads it.
func TestGetMeUserIDRejectsAZeroID(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	errCh := make(chan error, 1)
	go func() {
		_, err := session.GetMeUserID(context.Background())
		errCh <- err
	}()

	request := waitForRequestN(t, sender, "getMe", 1)
	var decoded struct {
		Extra string `json:"@extra"`
	}
	if err := json.Unmarshal(request, &decoded); err != nil {
		t.Fatalf("decode request: %v", err)
	}

	feedResponse(t, client, map[string]any{
		"@type": "user",
		"id":    int64(0),
	}, decoded.Extra)

	select {
	case err := <-errCh:
		if !errors.Is(err, ErrChatLifecycleResponse) {
			t.Fatalf("error = %v, want ErrChatLifecycleResponse", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("GetMeUserID did not return")
	}
}

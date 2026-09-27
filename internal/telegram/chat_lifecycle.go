package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// TDLib sends the online member count of a chat only for a chat that has
// been opened, and it forgets a chat that stays open forever. The interface
// therefore says which chat the user is looking at, and the session asks
// TDLib to open exactly that one.
//
// The calls are best effort from the interface's point of view: a chat that
// cannot be opened still shows its messages, and a presence that does not
// arrive is a presence the header does not draw. What an openChat fails on
// is logged by the caller, never shown.

// ErrChatLifecycleResponse is returned when TDLib answers openChat or
// closeChat with something other than ok.
//
// The schema says both return ok (:13220, :13223), so any other answer is a
// protocol surprise rather than a routine refusal, and it is reported
// instead of being read as a success.
var ErrChatLifecycleResponse = errors.New(
	"telegram chat lifecycle: unexpected response",
)

type chatLifecycleRequest struct {
	Type   string `json:"@type"`
	ChatID int64  `json:"chat_id"`
}

// OpenChat tells TDLib that the user is looking at a chat.
//
// Without it TDLib does not send updateChatOnlineMemberCount for a group
// (:10613), and a group header would show nothing about its members for as
// long as the program runs.
func (s *AuthorizedSession) OpenChat(ctx context.Context, chatID ChatID) error {
	return s.chatLifecycle(ctx, "openChat", chatID)
}

// CloseChat tells TDLib that the user has left a chat.
//
// It is the other half of OpenChat: a chat left open keeps being counted
// and updated for somebody who is not looking at it.
func (s *AuthorizedSession) CloseChat(ctx context.Context, chatID ChatID) error {
	return s.chatLifecycle(ctx, "closeChat", chatID)
}

// chatLifecycle sends openChat or closeChat and checks the answer.
func (s *AuthorizedSession) chatLifecycle(
	ctx context.Context,
	method string,
	chatID ChatID,
) error {
	if chatID == 0 {
		return fmt.Errorf("%w: %d", ErrInvalidChatID, chatID)
	}

	request, err := json.Marshal(chatLifecycleRequest{
		Type:   method,
		ChatID: int64(chatID),
	})
	if err != nil {
		return fmt.Errorf("marshal %s: %w", method, err)
	}

	raw, err := s.Query(ctx, RawMessage(request))
	if err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}

	var response struct {
		Type string `json:"@type"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return fmt.Errorf("decode %s response: %w", method, err)
	}
	if response.Type != "ok" {
		return fmt.Errorf(
			"%w: %s returned @type=%q",
			ErrChatLifecycleResponse,
			method,
			response.Type,
		)
	}

	return nil
}

// GetMeUserID returns the identifier of the user this client is authorized
// as.
//
// It is the only way to tell a chat with oneself from a chat with a
// contact, because TDLib sends the current user as an ordinary user: there
// is nothing in updateUser that says "this one is you". The interface needs
// the difference for Saved Messages, where the status in the chat is the
// user's own and drawing it would put "Online" over a chat with oneself.
//
// Only the identifier leaves this function. A user object carries a name, a
// phone number, a username and a profile photo, and none of them is needed
// to recognise oneself, so none of them is kept.
func (s *AuthorizedSession) GetMeUserID(ctx context.Context) (int64, error) {
	if s == nil {
		return 0, ErrSessionNilClient
	}

	raw, err := s.Query(ctx, RawMessage(`{"@type":"getMe"}`))
	if err != nil {
		return 0, fmt.Errorf("getMe: %w", err)
	}

	var response struct {
		Type string `json:"@type"`
		ID   int64  `json:"id"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return 0, fmt.Errorf("decode getMe response: %w", err)
	}
	if response.Type != "user" {
		return 0, fmt.Errorf(
			"%w: getMe returned @type=%q",
			ErrChatLifecycleResponse,
			response.Type,
		)
	}
	if response.ID == 0 {
		return 0, fmt.Errorf(
			"%w: getMe returned a zero user id",
			ErrChatLifecycleResponse,
		)
	}

	return response.ID, nil
}

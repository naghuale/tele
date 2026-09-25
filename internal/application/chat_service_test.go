package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"telecli/internal/telegram"
)

type fakeTelegramChats struct {
	snapshot    telegram.ChatListSnapshot
	snapshotErr error

	history    telegram.HistoryPage
	historyErr error

	sentMessage telegram.Message
	sendErr     error

	gotListLimit   int
	gotHistoryArgs struct {
		chatID int64
		from   int64
		limit  int
	}
	gotSendArgs struct {
		chatID int64
		text   string
	}
}

func (f *fakeTelegramChats) GetChats(
	ctx context.Context,
	limit int,
) (telegram.ChatListSnapshot, error) {
	f.gotListLimit = limit
	return f.snapshot, f.snapshotErr
}

func (f *fakeTelegramChats) GetChatHistory(
	ctx context.Context,
	chatID telegram.ChatID,
	fromMessageID telegram.MessageID,
	limit int,
) (telegram.HistoryPage, error) {
	f.gotHistoryArgs.chatID = int64(chatID)
	f.gotHistoryArgs.from = int64(fromMessageID)
	f.gotHistoryArgs.limit = limit
	return f.history, f.historyErr
}

func (f *fakeTelegramChats) SendTextMessage(
	ctx context.Context,
	chatID telegram.ChatID,
	text string,
) (telegram.Message, error) {
	f.gotSendArgs.chatID = int64(chatID)
	f.gotSendArgs.text = text
	return f.sentMessage, f.sendErr
}

func TestTelegramChatServiceListChats(t *testing.T) {
	fake := &fakeTelegramChats{
		snapshot: telegram.ChatListSnapshot{
			Chats: []telegram.ChatSummary{
				{ID: 1, Title: "Alice", UnreadCount: 2, LastMessageText: "hi"},
				{ID: 2, Title: "Bob"},
			},
		},
	}
	svc := NewTelegramChatService(fake)

	chats, err := svc.ListChats(context.Background())
	if err != nil {
		t.Fatalf("ListChats: %v", err)
	}
	if fake.gotListLimit != defaultChatListLimit {
		t.Fatalf("list limit = %d, want %d", fake.gotListLimit, defaultChatListLimit)
	}
	if len(chats) != 2 {
		t.Fatalf("len(chats) = %d, want 2", len(chats))
	}
	if chats[0].ID != 1 || chats[0].Title != "Alice" || chats[0].Unread != 2 {
		t.Fatalf("chats[0] = %+v", chats[0])
	}
	if chats[0].Preview != "hi" {
		t.Fatalf("chats[0].Preview = %q", chats[0].Preview)
	}
}

func TestTelegramChatServiceListChatsError(t *testing.T) {
	fake := &fakeTelegramChats{snapshotErr: errors.New("boom")}
	svc := NewTelegramChatService(fake)

	_, err := svc.ListChats(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestTelegramChatServiceNilSource(t *testing.T) {
	svc := NewTelegramChatService(nil)
	if _, err := svc.ListChats(context.Background()); err == nil {
		t.Fatal("expected error for nil source")
	}
	if _, err := svc.LoadHistory(context.Background(), 1, 0, 50); err == nil {
		t.Fatal("expected error for nil source")
	}
	if _, err := svc.SendMessage(context.Background(), 1, "hi"); err == nil {
		t.Fatal("expected error for nil source")
	}
}

func TestTelegramChatServiceLoadHistory(t *testing.T) {
	ts := time.Unix(1700000000, 0).UTC()
	wantTime := ts.Format("15:04")

	fake := &fakeTelegramChats{
		history: telegram.HistoryPage{
			Messages: []telegram.Message{
				{ID: 200, ChatID: 42, Outgoing: true, Text: "hi", Timestamp: ts},
				{ID: 199, ChatID: 42, Text: "[photo]", Timestamp: ts},
			},
			NextFrom: 199,
			HasMore:  true,
		},
	}
	svc := NewTelegramChatService(fake)

	page, err := svc.LoadHistory(context.Background(), 42, 0, 50)
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	if fake.gotHistoryArgs.chatID != 42 {
		t.Fatalf("chatID = %d, want 42", fake.gotHistoryArgs.chatID)
	}
	if fake.gotHistoryArgs.from != 0 {
		t.Fatalf("from message ID = %d, want 0", fake.gotHistoryArgs.from)
	}
	if fake.gotHistoryArgs.limit != 50 {
		t.Fatalf("limit = %d, want 50", fake.gotHistoryArgs.limit)
	}
	if len(page.Messages) != 2 {
		t.Fatalf("len(messages) = %d, want 2", len(page.Messages))
	}
	if page.Messages[0].ID != 200 || page.Messages[0].Text != "hi" {
		t.Fatalf("messages[0] = %+v", page.Messages[0])
	}
	if page.Messages[1].Text != "[photo]" {
		t.Fatalf("messages[1].Text = %q", page.Messages[1].Text)
	}
	if page.NextFrom != 199 || !page.HasMore {
		t.Fatalf("page = %+v", page)
	}
	if page.Messages[0].Time != wantTime {
		t.Fatalf("message time = %q, want %q",
			page.Messages[0].Time, wantTime)
	}
}

func TestTelegramChatServiceLoadHistoryClampsLimit(t *testing.T) {
	fake := &fakeTelegramChats{}
	svc := NewTelegramChatService(fake)

	for _, limit := range []int{-1, 0, 101, 5000} {
		_, err := svc.LoadHistory(context.Background(), 1, 0, limit)
		if err != nil {
			t.Fatalf("limit=%d: %v", limit, err)
		}
		if fake.gotHistoryArgs.limit != defaultHistoryLimit {
			t.Fatalf("limit=%d: passed %d, want default %d",
				limit, fake.gotHistoryArgs.limit, defaultHistoryLimit)
		}
	}
}

func TestTelegramChatServiceLoadHistoryError(t *testing.T) {
	fake := &fakeTelegramChats{historyErr: errors.New("boom")}
	svc := NewTelegramChatService(fake)

	_, err := svc.LoadHistory(context.Background(), 1, 0, 50)
	if err == nil {
		t.Fatal("expected error")
	}
}

// ---- SendMessage ----

func TestTelegramChatServiceSendMessage(t *testing.T) {
	ts := time.Unix(1700000000, 0).UTC()
	wantTime := ts.Format("15:04")

	fake := &fakeTelegramChats{
		sentMessage: telegram.Message{
			ID:        9001,
			ChatID:    42,
			Outgoing:  true,
			Text:      "hello",
			Timestamp: ts,
		},
	}
	svc := NewTelegramChatService(fake)

	msg, err := svc.SendMessage(context.Background(), 42, "hello")
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if fake.gotSendArgs.chatID != 42 {
		t.Fatalf("chatID = %d, want 42", fake.gotSendArgs.chatID)
	}
	if fake.gotSendArgs.text != "hello" {
		t.Fatalf("text = %q, want hello", fake.gotSendArgs.text)
	}
	if msg.ID != 9001 {
		t.Fatalf("message.ID = %d, want 9001", msg.ID)
	}
	if !msg.Outgoing {
		t.Fatal("message.Outgoing = false, want true")
	}
	if msg.Text != "hello" {
		t.Fatalf("message.Text = %q, want hello", msg.Text)
	}
	if msg.Time != wantTime {
		t.Fatalf("message.Time = %q, want %q", msg.Time, wantTime)
	}
}

func TestTelegramChatServiceSendMessageError(t *testing.T) {
	sendErr := errors.New("boom")
	fake := &fakeTelegramChats{sendErr: sendErr}
	svc := NewTelegramChatService(fake)

	_, err := svc.SendMessage(context.Background(), 1, "hi")
	if !errors.Is(err, sendErr) {
		t.Fatalf("error = %v, want wrapped send error", err)
	}
}

func TestTelegramChatServiceSendMessagePreservesWhitespace(t *testing.T) {
	fake := &fakeTelegramChats{}
	svc := NewTelegramChatService(fake)

	_, err := svc.SendMessage(context.Background(), 1, "  hello  ")
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if fake.gotSendArgs.text != "  hello  " {
		t.Fatalf("text = %q, want %q", fake.gotSendArgs.text, "  hello  ")
	}
}

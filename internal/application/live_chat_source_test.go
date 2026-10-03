package application

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"

	"telecli/internal/telegram"
	"telecli/internal/tui"
)

// This file is the live chat list from the other side of the boundary: the
// recorded TDLib updates of a conversation that goes on while the interface
// is watching it, read through the adapter the composition root builds.
//
// The payloads are the ones a real account sends, in the shape the pinned
// schema gives them (TDLib 1.8.67):
//
//	updateNewChat chat:chat = Update;
//	updateChatLastMessage chat_id last_message positions = Update;
//	updateChatPosition chat_id position = Update;
//	updateChatReadInbox chat_id last_read_inbox_message_id unread_count = Update;
//	updateNewMessage message = Update;
//
// positions is a bare JSON array of chatPosition, and the list is its `list`
// field. Nothing here is an account of anybody: the names are the ones this
// file writes, and they are only ever answers of a stub reader.

// recordedChat builds an updateNewChat for one chat of the fixture.
func recordedChat(
	id int,
	title string,
	order, unread int,
	pinned bool,
	kind string,
) string {
	return `{"@type":"updateNewChat","chat":{"@type":"chat","id":` + strconv.Itoa(id) +
		`,"title":"` + title + `","unread_count":` + strconv.Itoa(unread) +
		`,"last_message":{"@type":"message","id":` + strconv.Itoa(1000+id) +
		`,"chat_id":` + strconv.Itoa(id) + `,"date":17000000` + strconv.Itoa(id%10) +
		`,"content":{"@type":"messageText","text":{"@type":"formattedText",` +
		`"text":"the last message of ` + title + `"}}},` +
		`"type":{"@type":"` + kind + `"},` +
		`"positions":[{"@type":"chatPosition","list":{"@type":"chatListMain"},` +
		`"order":"` + strconv.Itoa(order) + `","is_pinned":` + boolText(pinned) +
		`,"source":null}]}}`
}

// recordedLastMessage is an updateChatLastMessage for a text message.
//
// It carries the whole position vector, as TDLib sends it: that is how a
// chat that received a message also moves to the top of the list, and it is
// why this is the update that moves a row.
func recordedLastMessage(
	chatID, messageID, date, order int,
	text string,
) string {
	return `{"@type":"updateChatLastMessage","chat_id":` + strconv.Itoa(chatID) +
		`,"last_message":{"@type":"message","id":` + strconv.Itoa(messageID) +
		`,"chat_id":` + strconv.Itoa(chatID) + `,"date":` + strconv.Itoa(date) +
		`,"sender_id":{"@type":"messageSenderUser","user_id":77},` +
		`"content":{"@type":"messageText","text":{"@type":"formattedText",` +
		`"text":"` + text + `"}}},` +
		`"positions":[{"@type":"chatPosition","list":{"@type":"chatListMain"},` +
		`"order":"` + strconv.Itoa(order) + `","is_pinned":false,"source":null}]}`
}

func recordedReadInbox(chatID, unread int) string {
	return recordedReadInboxTo(chatID, unread, 9000+chatID)
}

// recordedReadInboxTo is updateChatReadInbox with the pointer it carries:
// last_read_inbox_message_id is how far the read reached and unread_count is
// what is left of it (td_api.tl:10521), and TDLib sends both in one update.
func recordedReadInboxTo(chatID, unread, readTo int) string {
	return `{"@type":"updateChatReadInbox","chat_id":` + strconv.Itoa(chatID) +
		`,"last_read_inbox_message_id":` + strconv.Itoa(readTo) +
		`,"unread_count":` + strconv.Itoa(unread) + `}`
}

func recordedNewMessage(chatID, messageID, date int, text string) string {
	return `{"@type":"updateNewMessage","message":{"@type":"message","id":` +
		strconv.Itoa(messageID) + `,"chat_id":` + strconv.Itoa(chatID) +
		`,"date":` + strconv.Itoa(date) +
		`,"sender_id":{"@type":"messageSenderUser","user_id":77},` +
		`"content":{"@type":"messageText","text":{"@type":"formattedText",` +
		`"text":"` + text + `"}}}}`
}

// recordedPosition is an updateChatPosition for the main list.
func recordedPosition(chatID, order int, pinned bool) string {
	return `{"@type":"updateChatPosition","chat_id":` + strconv.Itoa(chatID) +
		`,"position":{"@type":"chatPosition","list":{"@type":"chatListMain"},` +
		`"order":"` + strconv.Itoa(order) + `","is_pinned":` + boolText(pinned) +
		`,"source":null}}`
}

func boolText(value bool) string {
	if value {
		return "true"
	}

	return "false"
}

// recordedAccount is a live store fed with the recorded updates of a chat
// list that goes on while the interface watches it.
func recordedAccount(t *testing.T) *telegram.LiveState {
	t.Helper()

	store := telegram.NewLiveState()
	applyRecorded(t, store, recordedChat(1, "Anna Example", 300, 0, false, "chatTypePrivate"))
	applyRecorded(t, store, recordedChat(2, "Release Room", 200, 4, false, "chatTypeSupergroup"))
	applyRecorded(t, store, recordedChat(3, "Xiaomi News", 100, 12, false, "chatTypeSupergroup"))

	return store
}

func applyRecorded(t *testing.T, store *telegram.LiveState, raw string) {
	t.Helper()

	if _, err := store.ApplyUpdate(telegram.RawMessage(raw)); err != nil {
		t.Fatalf("apply %s: %v", raw, err)
	}
}

// The list the adapter hands the interface is the list TDLib keeps: pinned
// chats first, then by the order of their positions.
func TestTheLiveListIsInTheOrderTelegramKeeps(t *testing.T) {
	store := recordedAccount(t)
	applyRecorded(t, store, recordedPosition(3, 100, true))

	live, err := NewTelegramLiveUpdates(t.Context(), store, nil)
	if err != nil {
		t.Fatalf("NewTelegramLiveUpdates: %v", err)
	}

	chats := live.Chats()
	if len(chats) != 3 {
		t.Fatalf("Chats() = %d chats, want 3", len(chats))
	}
	want := []int64{3, 1, 2}
	for index, chat := range chats {
		if chat.ID != want[index] {
			t.Fatalf(
				"chat %d of the list is %d, want %d", index, chat.ID, want[index],
			)
		}
	}

	first := chats[0]
	if first.Title != "Xiaomi News" {
		t.Fatalf("title = %q, want the pinned chat", first.Title)
	}
	if first.Unread != 12 {
		t.Fatalf("unread = %d, want 12", first.Unread)
	}
	if first.Kind != tui.ChatKindGroup {
		t.Fatalf(
			"kind = %v, want a group: a supergroup has more than one person in it",
			first.Kind,
		)
	}
	if first.Preview != "the last message of Xiaomi News" {
		t.Fatalf("preview = %q, want the words of the last message", first.Preview)
	}
	if first.At.IsZero() {
		t.Fatal("the row has no moment for its last message")
	}
}

// A message that arrives in the middle of the list puts its chat at the top
// of it, with the words of that message and the moment it was sent.
func TestAMessageMovesItsChatToTheTopOfTheList(t *testing.T) {
	store := recordedAccount(t)

	live, err := NewTelegramLiveUpdates(t.Context(), store, nil)
	if err != nil {
		t.Fatalf("NewTelegramLiveUpdates: %v", err)
	}
	if got := live.Chats()[0].ID; got != 1 {
		t.Fatalf("chat %d is first before the message, want 1", got)
	}

	applyRecorded(t, store, recordedLastMessage(3, 3003, 1700000900, 900, "the firmware is out"))

	first := live.Chats()[0]
	if first.ID != 3 {
		t.Fatalf("chat %d is first after the message, want 3", first.ID)
	}
	if first.Preview != "the firmware is out" {
		t.Fatalf("preview = %q, want the message that arrived", first.Preview)
	}
}

// A chat read on another device loses its count at once: the row is not
// read again, it is told.
func TestTheCountFallsWhenTheChatIsReadElsewhere(t *testing.T) {
	store := recordedAccount(t)

	live, err := NewTelegramLiveUpdates(t.Context(), store, nil)
	if err != nil {
		t.Fatalf("NewTelegramLiveUpdates: %v", err)
	}
	applyRecorded(t, store, recordedReadInbox(3, 0))

	for _, chat := range live.Chats() {
		if chat.ID == 3 && chat.Unread != 0 {
			t.Fatalf("unread = %d after the chat was read, want 0", chat.Unread)
		}
	}
}

// The read pointer crosses the boundary with the count, because the two are
// the two numbers of one update and the interface draws the line over the
// unread messages of the open chat from the pointer alone.
//
// A row that arrived with the count and without the pointer left the owner
// looking at a chat with no circle and a line over messages that had been
// read (real account, 03.10, #77) — so this is asked of the update that
// Telegram really sends, with both of its numbers.
func TestTheReadPointerOfTheRowFollowsTelegram(t *testing.T) {
	store := recordedAccount(t)

	live, err := NewTelegramLiveUpdates(t.Context(), store, nil)
	if err != nil {
		t.Fatalf("NewTelegramLiveUpdates: %v", err)
	}
	applyRecorded(t, store, recordedReadInboxTo(3, 0, 3003))

	for _, chat := range live.Chats() {
		if chat.ID != 3 {
			continue
		}
		if chat.LastReadInboxMessageID != 3003 {
			t.Fatalf(
				"LastReadInboxMessageID = %d, want the 3003 Telegram sent",
				chat.LastReadInboxMessageID,
			)
		}
		if chat.Unread != 0 {
			t.Fatalf("Unread = %d, want the 0 Telegram sent", chat.Unread)
		}
	}
}

// A chat that left the main list is not in the list the interface draws.
func TestAChatThatLeftTheMainListIsNotInTheList(t *testing.T) {
	store := recordedAccount(t)

	live, err := NewTelegramLiveUpdates(t.Context(), store, nil)
	if err != nil {
		t.Fatalf("NewTelegramLiveUpdates: %v", err)
	}
	applyRecorded(t, store, `{"@type":"updateChatPosition","chat_id":3,`+
		`"position":{"@type":"chatPosition","list":{"@type":"chatListMain"},`+
		`"order":"0","is_pinned":false,"source":null}}`)

	for _, chat := range live.Chats() {
		if chat.ID == 3 {
			t.Fatal("a chat that left the main list is still in the list")
		}
	}
}

// The events of a chat reach the interface as events of that chat, and the
// message is signed with the name of whoever sent it.
//
// The name is read from the reader rather than from the store: the live
// state keeps no names on purpose (#41), and this test is where that shows —
// the store answered with an identifier and the adapter asked for a word.
//
// It is asked for in the background and comes back as one more event of that
// chat, so what the message carries on the way out is the placeholder; the
// name is checked where it arrives, in live_sender_names_test.go, and here it
// is only checked that it arrives at all and that it was read once.
func TestTheEventsOfAChatReachTheInterface(t *testing.T) {
	store := recordedAccount(t)
	names := &stubNameReader{user: "Marta Ivanova", chat: "Xiaomi News"}

	live, err := NewTelegramLiveUpdates(t.Context(), store, names)
	if err != nil {
		t.Fatalf("NewTelegramLiveUpdates: %v", err)
	}
	applyRecorded(t, store, recordedNewMessage(2, 2002, 1700000800, "the tag is pushed"))

	events := live.MessageEvents(2, 0)
	if len(events.Events) != 1 {
		t.Fatalf("the chat produced %d events, want 1", len(events.Events))
	}
	if events.Resync {
		t.Fatal("the first read of the window is a resync")
	}

	added := events.Events[0]
	if added.Kind != tui.LiveMessageAdded {
		t.Fatalf("event kind = %v, want an added message", added.Kind)
	}
	if added.Message.Text != "the tag is pushed" {
		t.Fatalf("text = %q, want the message that arrived", added.Message.Text)
	}
	if added.Message.AuthorID != 77 {
		t.Fatalf("author id = %d, want 77", added.Message.AuthorID)
	}

	readUntilNamed(t, live, 2, events.Cursor)
	if names.reads() != 1 {
		t.Fatalf(
			"the name was read %d times, want 1: a second read is a round trip",
			names.reads(),
		)
	}
}

// A window that has moved past what the consumer holds is reported, and the
// consumer reloads the first page of the chat rather than losing a message.
func TestAResyncIsReportedToTheConsumer(t *testing.T) {
	store := recordedAccount(t)
	live, err := NewTelegramLiveUpdates(t.Context(), store, nil)
	if err != nil {
		t.Fatalf("NewTelegramLiveUpdates: %v", err)
	}

	// More events than the window holds, so the cursor the consumer holds
	// is older than the oldest event left in it.
	for id := range 300 {
		applyRecorded(
			t, store, recordedNewMessage(2, 4000+id, 1700000000+id, "a message"),
		)
	}

	events := live.MessageEvents(2, 1)
	if !events.Resync {
		t.Fatalf("a cursor older than the window was answered with %d events", len(events.Events))
	}
	if events.Cursor == 0 {
		t.Fatal("a resync gave no cursor to continue from")
	}
}

// A failed send is the durable queue's business and not the interface's: the
// store records it and this adapter does not hand it over, so a row cannot
// be drawn from a state the outbox does not know about.
func TestAFailedSendIsNotAnEventOfTheInterface(t *testing.T) {
	store := recordedAccount(t)
	live, err := NewTelegramLiveUpdates(t.Context(), store, nil)
	if err != nil {
		t.Fatalf("NewTelegramLiveUpdates: %v", err)
	}

	applyRecorded(t, store, `{"@type":"updateMessageSendFailed","message":`+
		`{"@type":"message","id":5000,"chat_id":2,"date":1700000000,`+
		`"is_outgoing":true,"content":{"@type":"messageText",`+
		`"text":{"@type":"formattedText","text":"never sent"}}},`+
		`"old_message_id":4999,"error":{"@type":"error","code":400,`+
		`"message":"Chat not found"}}`)

	if events := live.MessageEvents(2, 0); len(events.Events) != 0 {
		t.Fatalf("a failed send became %d interface events", len(events.Events))
	}
}

// The list is asked of TDLib before the interface draws anything, and a
// refusal is reported rather than swallowed: a store nobody asked for a list
// in is a chat list that will not move.
func TestLoadAsksTDLibForTheList(t *testing.T) {
	loader := &stubChatListLoader{pages: 2}
	live, err := NewTelegramLiveUpdates(t.Context(), telegram.NewLiveState(), nil)
	if err != nil {
		t.Fatalf("NewTelegramLiveUpdates: %v", err)
	}

	if err := live.Load(context.Background(), loader, nil); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loader.calls != 2 {
		t.Fatalf("loadChats was asked %d times, want 2", loader.calls)
	}
	if loader.limit != defaultChatListLimit {
		t.Fatalf("limit = %d, want %d", loader.limit, defaultChatListLimit)
	}
}

func TestLoadSaysWhyTheListIsNotLive(t *testing.T) {
	loader := &stubChatListLoader{err: errors.New("TDLib is closing")}
	live, err := NewTelegramLiveUpdates(t.Context(), telegram.NewLiveState(), nil)
	if err != nil {
		t.Fatalf("NewTelegramLiveUpdates: %v", err)
	}

	report := &strings.Builder{}
	if err := live.Load(context.Background(), loader, report); err == nil {
		t.Fatal("a refusal of the chat list was swallowed")
	}
	if !strings.Contains(report.String(), "will not update by itself") {
		t.Fatalf("the diagnostics say %q", report.String())
	}
}

func TestLoadNeedsAStoreAndALoader(t *testing.T) {
	if _, err := NewTelegramLiveUpdates(t.Context(), nil, nil); err == nil {
		t.Fatal("an adapter with nothing to read was built")
	}

	live, err := NewTelegramLiveUpdates(t.Context(), telegram.NewLiveState(), nil)
	if err != nil {
		t.Fatalf("NewTelegramLiveUpdates: %v", err)
	}
	if err := live.Load(context.Background(), nil, nil); err == nil {
		t.Fatal("the chat list was asked of nothing")
	}
}

// A program that has authorized a session hands the interface a live chat
// list: the store of that session, filled by the question the adapter asks
// TDLib before the program draws anything.
//
// The owner read a program that drew a chat list which did not move, and
// nothing said so. This is the control for that: the dependency is checked
// where it is built rather than where it is used, because a program that
// forgot to wire it still starts.
func TestTheProgramIsStartedWithTheLiveChatListOfItsSession(t *testing.T) {
	store := telegram.NewLiveState()
	loader := &stubChatListLoader{pages: 1}
	session := &stubLiveSession{state: store, loader: loader}

	updates := liveChatUpdatesFor(context.Background(), session, nil)
	if updates == nil {
		t.Fatal("the program was started with no live chat list")
	}
	if !updates.Available() {
		t.Fatal("the live chat list says it cannot be read")
	}
	if loader.calls != 1 {
		t.Fatalf("loadChats was asked %d times, want 1", loader.calls)
	}
}

// A session with no store is a program whose chat list cannot move, and the
// interface is told so in its status line rather than being handed nothing
// at all.
func TestAProgramWithNoStoreHasNoLiveChatList(t *testing.T) {
	if updates := liveChatUpdatesFor(
		context.Background(), &stubLiveSession{}, nil,
	); updates != nil {
		t.Fatalf("a program with no store was given %T", updates)
	}
}

// stubLiveSession is the part of a session the live chat list needs.
type stubLiveSession struct {
	state  *telegram.LiveState
	loader *stubChatListLoader
}

func (s *stubLiveSession) LiveState() *telegram.LiveState { return s.state }

func (s *stubLiveSession) GetUserName(
	_ context.Context,
	_ int64,
) (string, error) {
	return "Marta Ivanova", nil
}

func (s *stubLiveSession) GetChat(
	_ context.Context,
	_ telegram.ChatID,
) (telegram.ChatSummary, error) {
	return telegram.ChatSummary{}, nil
}

func (s *stubLiveSession) LoadChats(
	_ context.Context,
	limit int,
) (bool, error) {
	return s.loader.LoadChats(context.Background(), limit)
}

// stubChatListLoader answers loadChats with a number of pages of the list.
type stubChatListLoader struct {
	pages int
	calls int
	limit int
	err   error
}

func (l *stubChatListLoader) LoadChats(
	_ context.Context,
	limit int,
) (bool, error) {
	l.calls++
	l.limit = limit

	if l.err != nil {
		return false, l.err
	}
	if l.calls >= l.pages {
		return true, nil
	}

	return false, nil
}

// stubNameReader answers with the names of the fixture and counts the reads:
// a name is read once and kept, not asked for on every message.
type stubNameReader struct {
	mu    sync.Mutex
	user  string
	chat  string
	count int
}

func (r *stubNameReader) GetUserName(_ context.Context, _ int64) (string, error) {
	return r.remember(r.user), nil
}

func (r *stubNameReader) GetChat(
	_ context.Context,
	_ telegram.ChatID,
) (telegram.ChatSummary, error) {
	return telegram.ChatSummary{Title: r.remember(r.chat)}, nil
}

// remember counts the read and gives the name of the fixture back.
func (r *stubNameReader) remember(name string) string {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.count++

	return name
}

func (r *stubNameReader) reads() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.count
}

var (
	_ LiveChatLoader     = (*stubChatListLoader)(nil)
	_ tui.ChatLiveSource = (*TelegramLiveUpdates)(nil)
)

// recordedNotificationSettings is an updateChatNotificationSettings for one
// chat: the mute is the chat's own, with seconds left on it.
func recordedNotificationSettings(chatID, muteFor int) string {
	return `{"@type":"updateChatNotificationSettings","chat_id":` +
		strconv.Itoa(chatID) +
		`,"notification_settings":{"@type":"chatNotificationSettings",` +
		`"use_default_mute_for":false,"mute_for":` + strconv.Itoa(muteFor) + `}}`
}

// The pin and the mute cross the boundary with the rest of the row: the pin
// says why a chat is at the top of the list, and the mute is what keeps it
// out of the number in the header. Both come from the recorded updates of
// the account, so a row that arrived without them would be a row about the
// moment the program started rather than about the chat (#46).
func TestTheLiveListBringsThePinAndTheMuteOfAChat(t *testing.T) {
	store := recordedAccount(t)
	applyRecorded(t, store, recordedPosition(1, 300, true))
	applyRecorded(t, store, recordedNotificationSettings(3, 3600))

	live, err := NewTelegramLiveUpdates(t.Context(), store, nil)
	if err != nil {
		t.Fatalf("NewTelegramLiveUpdates: %v", err)
	}

	chats := live.Chats()
	if len(chats) != 3 {
		t.Fatalf("Chats() = %d chats, want 3", len(chats))
	}

	for _, chat := range chats {
		switch chat.ID {
		case 1:
			if !chat.Pinned {
				t.Error("the chat the position marked as pinned is not marked")
			}
			if chat.Muted {
				t.Error("a chat nobody silenced is marked as silenced")
			}
		case 3:
			if !chat.Muted {
				t.Error("the silenced chat is not marked as silenced")
			}
		case 2:
			if chat.Pinned || chat.Muted {
				t.Errorf(
					"chat 2 arrived as pinned=%v muted=%v, want neither",
					chat.Pinned, chat.Muted,
				)
			}
		}
	}
}

// Lifting the mute is the same update with the time back at zero, and the
// chat comes back out of the number without the program knowing anything
// about it: the header counts what the state says, and the state is what
// Telegram keeps sending.
func TestUnsilencingAChatIsTheSameUpdateWithNoTimeLeft(t *testing.T) {
	store := recordedAccount(t)
	applyRecorded(t, store, recordedNotificationSettings(3, 3600))
	applyRecorded(t, store, recordedNotificationSettings(3, 0))

	live, err := NewTelegramLiveUpdates(t.Context(), store, nil)
	if err != nil {
		t.Fatalf("NewTelegramLiveUpdates: %v", err)
	}

	for _, chat := range live.Chats() {
		if chat.ID == 3 && chat.Muted {
			t.Fatal("the chat is still marked as silenced after its mute ran out")
		}
	}
}

package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// The owner's channel of 89 unread stayed at 89. Two things had to be true
// for that not to be a mystery, and neither of them is visible on a screen:
// the chat has to be open before its messages are read, and the request has
// to name the source TDLib accepts for that kind of chat.
//
// The answers below are recorded TDLib payloads — the shapes of a real
// session, including the fields this build does not model — and the requests
// are the bytes the session really sent, read back off the transport.

const recordedChannelID = int64(-1001234567890)

// recordedChannelChat is the recorded answer of getChat for a broadcast
// channel: a supergroup with is_channel, 89 unread, and a last message.
const recordedChannelChat = `{"@type":"chat","id":-1001234567890,"title":"Release Notes",` +
	`"unread_count":89,"last_message":{"@type":"message","id":991,"chat_id":-1001234567890,` +
	`"is_outgoing":false,"date":1759000000,"content":{"@type":"messageText",` +
	`"text":{"@type":"formattedText","text":"build 12 is green"}}},"type":` +
	`{"@type":"chatTypeSupergroup","is_channel":true}}`

// recordedChannelHistory is the recorded answer of getChatHistory for it. The
// identifiers in it are the ones TDLib knows the messages by, and they are
// what a read has to carry: a local identifier, or the temporary one a send
// carries before Telegram has confirmed it, marks nothing at all.
const recordedChannelHistory = `{"@type":"messages","total_count":89,"messages":[` +
	`{"@type":"message","id":990,"chat_id":-1001234567890,"is_outgoing":false,` +
	`"date":1758999940,"content":{"@type":"messageText",` +
	`"text":{"@type":"formattedText","text":"and 13 too"}}},` +
	`{"@type":"message","id":991,"chat_id":-1001234567890,"is_outgoing":false,` +
	`"date":1759000000,"content":{"@type":"messageText",` +
	`"text":{"@type":"formattedText","text":"build 12 is green"}}}]}`

// recordedNewChatChannel is the recorded answer that puts the channel into
// the main list with the 89 it had before anything was read.
const recordedNewChatChannel = `{"@type":"updateNewChat","chat":{"@type":"chat",` +
	`"id":-1001234567890,"title":"Release Notes","unread_count":89,"last_message":null,` +
	`"type":{"@type":"chatTypeSupergroup","is_channel":true},"positions":[` +
	`{"@type":"chatPosition","list":{"@type":"chatListMain"},"order":"700",` +
	`"is_pinned":false,"source":null}]}}`

// recordedReadInbox is what TDLib sends once it has taken the read of a
// channel: the identifier the pointer reached and the count that is left. The
// count of the list is the count in here.
const recordedReadInbox = `{"@type":"updateChatReadInbox","chat_id":-1001234567890,` +
	`"last_read_inbox_message_id":991,"unread_count":0}`

// recordedChatNotFound is the recorded answer TDLib gives a chat it has not
// loaded, which is what a broadcast chat is before openChat.
const recordedChatNotFound = `{"@type":"error","code":400,"message":"Chat not found"}`

// recordedAnswer reads a recorded payload as the map a fake answer carries.
func recordedAnswer(t *testing.T, raw string) map[string]any {
	t.Helper()

	var answer map[string]any
	if err := json.Unmarshal([]byte(raw), &answer); err != nil {
		t.Fatalf("decode recorded answer: %v", err)
	}

	return answer
}

// askRecorded sends one call, waits for its request to reach the transport,
// answers it with a recorded payload, and returns what the call reported.
func askRecorded(
	t *testing.T,
	session *AuthorizedSession,
	sender *fakeSender,
	client *Client,
	requestType string,
	answer map[string]any,
	call func() error,
) error {
	t.Helper()

	answered := make(chan error, 1)
	go func() {
		answered <- call()
	}()

	extra := decodeExtra(t, waitForRequestN(t, sender, requestType, 1))
	feedResponse(t, client, answer, extra)

	select {
	case err := <-answered:
		return err
	case <-time.After(2 * time.Second):
		t.Fatalf("%s did not return", requestType)

		return nil
	}
}

// The read of a channel, end to end, over recorded answers: the chat is
// opened, the page of history gives the identifiers the window is made of,
// the window is read with the source a channel is read with, and the
// recorded updateChatReadInbox takes the list from 89 to 0.
func TestTheRecordedRoundTripOfAChannelRead(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	live := session.LiveState()
	if _, err := live.ApplyUpdate(RawMessage(recordedNewChatChannel)); err != nil {
		t.Fatalf("apply updateNewChat: %v", err)
	}
	if got := live.ChatList()[0].UnreadCount; got != 89 {
		t.Fatalf("UnreadCount = %d, want the recorded 89", got)
	}

	// The chat is opened first: TDLib loads a broadcast chat with openChat,
	// and a read that arrives before it is refused.
	if err := askRecorded(
		t, session, sender, client, "openChat",
		map[string]any{"@type": "ok"},
		func() error { return session.OpenChat(context.Background(), ChatID(recordedChannelID)) },
	); err != nil {
		t.Fatalf("OpenChat: %v", err)
	}

	// The window is made of the identifiers the page of history carries.
	history := askRecordedHistory(t, session, sender, client)
	if len(history.Messages) != 2 {
		t.Fatalf("history messages = %d, want 2", len(history.Messages))
	}
	if history.Messages[0].ID != 990 || history.Messages[1].ID != 991 {
		t.Fatalf(
			"history ids = %d, %d, want the recorded 990 and 991",
			history.Messages[0].ID, history.Messages[1].ID,
		)
	}

	window := []MessageID{
		MessageID(history.Messages[0].ID),
		MessageID(history.Messages[1].ID),
	}
	err := askRecorded(
		t, session, sender, client, "viewMessages",
		map[string]any{"@type": "ok"},
		func() error {
			return session.ViewMessages(
				context.Background(),
				ChatID(recordedChannelID),
				window,
				MessageSourceFor(ChatKindSupergroup, window[len(window)-1]),
			)
		},
	)
	if err != nil {
		t.Fatalf("ViewMessages: %v", err)
	}

	var asked struct {
		Type       string  `json:"@type"`
		ChatID     int64   `json:"chat_id"`
		MessageIDs []tdInt `json:"message_ids"`
		Source     struct {
			Type      string `json:"@type"`
			TTL       int    `json:"ttl"`
			MessageID tdInt  `json:"message_id"`
		} `json:"source"`
		ForceRead bool `json:"force_read"`
	}
	request := waitForRequestN(t, sender, "viewMessages", 1)
	if err := json.Unmarshal(request, &asked); err != nil {
		t.Fatalf("decode viewMessages: %v", err)
	}

	if asked.ChatID != recordedChannelID {
		t.Fatalf("chat_id = %d, want the channel", asked.ChatID)
	}
	if len(asked.MessageIDs) != 2 ||
		asked.MessageIDs[0] != 990 || asked.MessageIDs[1] != 991 {
		t.Fatalf("message_ids = %v, want the identifiers of the window", asked.MessageIDs)
	}
	if !asked.ForceRead {
		t.Fatal("force_read = false, want true")
	}
	// messageSourceChat is a chat the messages do not belong to, and a read
	// that names it does not move a channel's pointer.
	if asked.Source.Type != "messageSourceHistory" {
		t.Fatalf("source = %q, want messageSourceHistory", asked.Source.Type)
	}
	if asked.Source.TTL != 0 || asked.Source.MessageID != 991 {
		t.Fatalf(
			"source = %+v, want ttl 0 and the newest message of the window",
			asked.Source,
		)
	}

	changed, err := live.ApplyUpdate(RawMessage(recordedReadInbox))
	if err != nil {
		t.Fatalf("apply updateChatReadInbox: %v", err)
	}
	if !changed {
		t.Fatal("the recorded read must change the list")
	}
	if got := live.ChatList()[0].UnreadCount; got != 0 {
		t.Fatalf("UnreadCount = %d, want 0", got)
	}
}

// askRecordedHistory reads one page of a chat's history over the recorded
// answer of a channel.
func askRecordedHistory(
	t *testing.T,
	session *AuthorizedSession,
	sender *fakeSender,
	client *Client,
) HistoryPage {
	t.Helper()

	var (
		page HistoryPage
		err  error
	)
	askRecorded(
		t, session, sender, client, "getChatHistory",
		recordedAnswer(t, recordedChannelHistory),
		func() error {
			page, err = session.GetChatHistory(
				context.Background(), ChatID(recordedChannelID), 0, 50,
			)

			return nil
		},
	)
	if err != nil {
		t.Fatalf("GetChatHistory: %v", err)
	}

	return page
}

// The recorded chat is what the row of the list is drawn from: 89, a
// supergroup and a channel, which is the kind that decides the source of the
// read and whether the badge is drawn in the accent.
func TestTheRecordedChannelChatCarriesTheCountAndTheKind(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	var (
		chat ChatSummary
		err  error
	)
	askRecorded(
		t, session, sender, client, "getChat",
		recordedAnswer(t, recordedChannelChat),
		func() error {
			chat, err = session.GetChat(context.Background(), ChatID(recordedChannelID))

			return nil
		},
	)
	if err != nil {
		t.Fatalf("GetChat: %v", err)
	}

	if chat.UnreadCount != 89 {
		t.Fatalf("UnreadCount = %d, want the recorded 89", chat.UnreadCount)
	}
	if chat.Kind != ChatKindSupergroup {
		t.Fatalf("Kind = %q, want a supergroup", chat.Kind)
	}
	if !chat.IsChannel {
		t.Fatal("IsChannel = false, want the recorded channel")
	}
}

// A chat TDLib has not loaded answers a read with the recorded refusal
// rather than with nothing, and the caller is what says so: the interface
// logs it and keeps showing the messages. This is the answer a channel gives
// when the open was skipped, and it is the whole of what the owner saw.
func TestARefusedReadOfAnUnopenedChatIsReportedAndReadsNothing(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	live := session.LiveState()
	if _, err := live.ApplyUpdate(RawMessage(recordedNewChatChannel)); err != nil {
		t.Fatalf("apply updateNewChat: %v", err)
	}

	err := askRecorded(
		t, session, sender, client, "viewMessages",
		recordedAnswer(t, recordedChatNotFound),
		func() error {
			return session.ViewMessages(
				context.Background(),
				ChatID(recordedChannelID),
				[]MessageID{991},
				MessageSourceFor(ChatKindSupergroup, 991),
			)
		},
	)

	var tdlibErr *TDLibError
	if !errors.As(err, &tdlibErr) {
		t.Fatalf("error = %v, want a TDLib error", err)
	}
	if tdlibErr.Code != 400 {
		t.Fatalf("code = %d, want the recorded 400", tdlibErr.Code)
	}
	// Nothing was read, so the list still says what it said.
	if got := live.ChatList()[0].UnreadCount; got != 89 {
		t.Fatalf("UnreadCount = %d, want the count to be untouched", got)
	}
}

// A chat with one other person is read in itself, and the recorded chat is
// the one that says so: a private chat's source carries no message, because
// the chat is the whole of where the read happened.
func TestTheRecordedRoundTripOfAPrivateChatRead(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	err := askRecorded(
		t, session, sender, client, "viewMessages",
		map[string]any{"@type": "ok"},
		func() error {
			return session.ViewMessages(
				context.Background(),
				ChatID(42),
				[]MessageID{990, 991},
				MessageSourceFor(ChatKindPrivate, 991),
			)
		},
	)
	if err != nil {
		t.Fatalf("ViewMessages: %v", err)
	}

	var asked struct {
		Source map[string]any `json:"source"`
	}
	request := waitForRequestN(t, sender, "viewMessages", 1)
	if err := json.Unmarshal(request, &asked); err != nil {
		t.Fatalf("decode viewMessages: %v", err)
	}

	if asked.Source["@type"] != "messageSourceChat" {
		t.Fatalf("source = %v, want messageSourceChat", asked.Source)
	}
	if _, named := asked.Source["message_id"]; named {
		t.Fatalf("source = %v, want no message_id on a chat source", asked.Source)
	}
}

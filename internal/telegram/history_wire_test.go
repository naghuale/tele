package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The numbers of TDLib as it writes them, not as a test wishes them.
//
// TDLib's JSON interface quotes a 64-bit integer and writes a 53-bit one
// as a number, and message.media_album_id is one of the 64-bit ones: a real
// account answers a history request with "media_album_id":"0" on every
// message, and a field declared as a plain int64 failed the decode of the
// whole page. The interface then said "Failed to load history" in every
// chat, and no test noticed, because every fixture in this package was
// written with a number where TDLib writes a string (#53).
//
// Every fixture below is the object TDLib writes, spelled out, so that a
// change in what it writes is a change in a fixture rather than a change
// nobody can see.

// feedRawJSON delivers a response exactly as it is written here, with the
// @extra of the request spliced into it.
//
// body is the object TDLib answers with. The helper adds the one field
// telecli owns, so that a fixture is the answer and not the bookkeeping
// around it.
func feedRawJSON(t *testing.T, client *Client, body, extra string) {
	t.Helper()

	if !strings.HasPrefix(body, "{") {
		t.Fatalf("body must be a JSON object, got %q", body)
	}
	payload := strings.Replace(
		body,
		"{",
		`{"@extra":`+strconv.Quote(extra)+`,`,
		1,
	)

	client.updates <- Update{ClientID: client.id, Raw: RawMessage(payload)}
}

// historyPage runs one getChatHistory request and returns the page or the
// error it came back with.
func historyPage(
	t *testing.T,
	session *AuthorizedSession,
	sender *fakeSender,
	client *Client,
	chatID ChatID,
	limit int,
	body string,
) (HistoryPage, error) {
	t.Helper()

	type result struct {
		page HistoryPage
		err  error
	}
	resultCh := make(chan result, 1)
	go func() {
		page, err := session.GetChatHistory(
			context.Background(), chatID, 0, limit,
		)
		resultCh <- result{page: page, err: err}
	}()

	extra := decodeExtra(t, waitForRequestN(t, sender, "getChatHistory", 1))
	feedRawJSON(t, client, body, extra)

	select {
	case r := <-resultCh:
		return r.page, r.err
	case <-time.After(3 * time.Second):
		t.Fatal("GetChatHistory did not return")
		return HistoryPage{}, nil
	}
}

// textMessage is one messageText message as TDLib writes it, with the
// numbers spelled as the case asks for.
func textMessage(id, chatID, date, albumID, text string) string {
	return `{"@type":"message","id":` + id +
		`,"chat_id":` + chatID +
		`,"is_outgoing":false,"date":` + date +
		`,"media_album_id":` + albumID +
		`,"sender_id":{"@type":"messageSenderUser","user_id":"5"},` +
		`"content":{"@type":"messageText","text":{"@type":"formattedText",` +
		`"text":"` + text + `"}}}`
}

// A page of a real chat, with the album id as the string TDLib writes and
// "0" for the messages that are not part of an album.
func TestGetChatHistoryReadsTheNumbersTDLibWritesAsStrings(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	page, err := historyPage(
		t, session, sender, client, 42, 3,
		`{"@type":"messages","total_count":3,"messages":[`+
			textMessage(`"3"`, `"42"`, `"1700000000"`, `"0"`, "newest")+`,`+
			textMessage(
				`"2"`, `"42"`, `"1699999999"`,
				`"1234567890123456789"`, "in an album",
			)+`,`+
			textMessage(`1`, `42`, `1699999998`, `0`, "the oldest")+
			`]}`,
	)
	if err != nil {
		t.Fatalf("GetChatHistory: %v", err)
	}

	if len(page.Messages) != 3 {
		t.Fatalf("len(messages) = %d, want 3", len(page.Messages))
	}
	for index, want := range []MessageID{3, 2, 1} {
		if page.Messages[index].ID != want {
			t.Fatalf("messages[%d].ID = %d, want %d",
				index, page.Messages[index].ID, want)
		}
		if page.Messages[index].ChatID != 42 {
			t.Fatalf("messages[%d].ChatID = %d, want 42",
				index, page.Messages[index].ChatID)
		}
	}

	// "0" is not an album: a message that is not part of one has no album
	// to be grouped with, and the timeline groups runs of messages that
	// share a non-zero id.
	if got := page.Messages[0].MediaAlbumID; got != 0 {
		t.Fatalf("messages[0].MediaAlbumID = %d, want 0", got)
	}
	if got := page.Messages[2].MediaAlbumID; got != 0 {
		t.Fatalf("messages[2].MediaAlbumID = %d, want 0", got)
	}
	if got := page.Messages[1].MediaAlbumID; got != 1234567890123456789 {
		t.Fatalf("messages[1].MediaAlbumID = %d, want 1234567890123456789", got)
	}

	if got, want := page.Messages[0].Sender.ID, int64(5); got != want {
		t.Fatalf("messages[0].Sender.ID = %d, want %d", got, want)
	}
	if page.NextFrom != 1 {
		t.Fatalf("NextFrom = %d, want 1", page.NextFrom)
	}
}

// Two messages of one album, as a channel sends them: the two parts share
// the album id and the feed draws them as one entry.
func TestGetChatHistoryGroupsAnAlbumSentAsStrings(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	page, err := historyPage(
		t, session, sender, client, 42, 2,
		`{"@type":"messages","messages":[`+
			`{"@type":"message","id":"2","chat_id":"42","date":"1700000000",`+
			`"media_album_id":"9007199254740993",`+
			`"content":{"@type":"messagePhoto","caption":{"@type":"formattedText",`+
			`"text":"the new wallpaper"}}},`+
			`{"@type":"message","id":"1","chat_id":"42","date":"1700000000",`+
			`"media_album_id":"9007199254740993",`+
			`"content":{"@type":"messagePhoto"}}`+
			`]}`,
	)
	if err != nil {
		t.Fatalf("GetChatHistory: %v", err)
	}
	if len(page.Messages) != 2 {
		t.Fatalf("len(messages) = %d, want 2", len(page.Messages))
	}
	if page.Messages[0].Media != "photo" || page.Messages[1].Media != "photo" {
		t.Fatalf("Media = %q and %q, want photo for both",
			page.Messages[0].Media, page.Messages[1].Media)
	}
	if got, want := page.Messages[0].Caption, "the new wallpaper"; got != want {
		t.Fatalf("Caption = %q, want %q", got, want)
	}
	if page.Messages[0].MediaAlbumID != page.Messages[1].MediaAlbumID {
		t.Fatalf("album ids = %d and %d, want the same album",
			page.Messages[0].MediaAlbumID, page.Messages[1].MediaAlbumID)
	}
	if page.Messages[0].MediaAlbumID == 0 {
		t.Fatal("album id = 0, want the album the two parts share")
	}
}

// One message this build cannot read is one message that is not shown. The
// rest of the page is the fifty messages the user came to read, and losing
// it to one of them is the bug of #53 in a second shape.
func TestGetChatHistoryLeavesOutAMessageItCannotRead(t *testing.T) {
	for _, broken := range []string{
		`{"@type":"message","id":"forty","chat_id":"42","date":"1",` +
			`"content":{"@type":"messageText","text":{"@type":"formattedText",` +
			`"text":"x"}}}`,
		`{"@type":"message","id":"2","chat_id":"42","date":"1",` +
			`"media_album_id":{"nested":true},` +
			`"content":{"@type":"messageText","text":{"@type":"formattedText",` +
			`"text":"x"}}}`,
		`"a string where a message belongs"`,
		`null`,
	} {
		t.Run(broken, func(t *testing.T) {
			session, sender, _, client := newSessionWithFakes(t)

			page, err := historyPage(
				t, session, sender, client, 42, 3,
				`{"@type":"messages","messages":[`+
					textMessage(`"3"`, `"42"`, `"1700000000"`, `"0"`, "newest")+
					`,`+broken+`,`+
					textMessage(`"1"`, `"42"`, `"1700000000"`, `"0"`, "oldest")+
					`]}`,
			)
			if err != nil {
				t.Fatalf("GetChatHistory: %v", err)
			}
			if len(page.Messages) != 2 {
				t.Fatalf("len(messages) = %d, want the 2 that can be read",
					len(page.Messages))
			}
			if page.Messages[0].ID != 3 || page.Messages[1].ID != 1 {
				t.Fatalf("ids = %d and %d, want 3 and 1",
					page.Messages[0].ID, page.Messages[1].ID)
			}
		})
	}
}

// A full page of which one message cannot be read is still a full page:
// HasMore is about the answer, not about what this build could show of it.
func TestGetChatHistoryReportsMoreWhenTheAnswerWasFull(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	page, err := historyPage(
		t, session, sender, client, 42, 2,
		`{"@type":"messages","messages":[`+
			textMessage(`"2"`, `"42"`, `"1700000000"`, `"0"`, "newest")+`,`+
			`{"@type":"message","id":"forty","chat_id":"42","date":"1"}`+
			`]}`,
	)
	if err != nil {
		t.Fatalf("GetChatHistory: %v", err)
	}
	if !page.HasMore {
		t.Fatal("HasMore = false, want true for an answer of the full page")
	}
}

// The two surprises a page can hold that are not about a number: an entry
// that is not a message, and a message of another chat. Both are the answer
// to a different question, and the interface has to be able to say so.
func TestGetChatHistoryStillReportsAnUnexpectedAnswer(t *testing.T) {
	for name, body := range map[string]string{
		"an entry that is not a message": `{"@type":"messages","messages":[` +
			`{"@type":"notMessage","id":"1"}]}`,
		"a message of another chat": `{"@type":"messages","messages":[` +
			textMessage(`"1"`, `"99"`, `"1"`, `"0"`, "x") + `]}`,
	} {
		t.Run(name, func(t *testing.T) {
			session, sender, _, client := newSessionWithFakes(t)

			_, err := historyPage(
				t, session, sender, client, 42, 1, body,
			)
			if !errors.Is(err, ErrUnexpectedHistoryResponse) {
				t.Fatalf(
					"error = %v, want ErrUnexpectedHistoryResponse", err,
				)
			}
		})
	}
}

// The chat list reads the same numbers, in the same two shapes.
func TestGetChatsReadsChatIDsAsStrings(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	type result struct {
		snapshot ChatListSnapshot
		err      error
	}
	resultCh := make(chan result, 1)
	go func() {
		snapshot, err := session.GetChats(context.Background(), 2)
		resultCh <- result{snapshot: snapshot, err: err}
	}()

	extra := decodeExtra(t, waitForRequestN(t, sender, "getChats", 1))
	feedRawJSON(
		t, client,
		`{"@type":"chats","total_count":2,"chat_ids":["42","43"]}`,
		extra,
	)

	// Each id of the list is asked about on its own, and both answers
	// come back in the same shape TDLib writes them.
	for id := range 2 {
		chatExtra := decodeExtra(t, waitForRequestN(t, sender, "getChat", id+1))
		feedRawJSON(t, client, `{"@type":"chat","id":"`+
			strconv.Itoa(42+id)+`","title":"Chat `+strconv.Itoa(42+id)+`",`+
			`"unread_count":0,"last_message":null,`+
			`"type":{"@type":"chatTypePrivate","user_id":"`+
			strconv.Itoa(100+id)+`"}}`, chatExtra)
	}

	select {
	case r := <-resultCh:
		if r.err != nil {
			t.Fatalf("GetChats: %v", r.err)
		}
		if len(r.snapshot.Chats) != 2 {
			t.Fatalf("chats = %d, want 2", len(r.snapshot.Chats))
		}
		if r.snapshot.Chats[0].ID != 42 || r.snapshot.Chats[1].ID != 43 {
			t.Fatalf("ids = %d and %d, want 42 and 43",
				r.snapshot.Chats[0].ID, r.snapshot.Chats[1].ID)
		}
		if r.snapshot.Chats[1].PeerUserID != 101 {
			t.Fatalf("PeerUserID = %d, want 101", r.snapshot.Chats[1].PeerUserID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("GetChats did not return")
	}
}

// A chat whose last message was sent to the user carries the same numbers
// as the message itself.
func TestGetChatReadsALastMessageSentAsStrings(t *testing.T) {
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

	extra := decodeExtra(t, waitForRequestN(t, sender, "getChat", 1))
	feedRawJSON(t, client, `{"@type":"chat","id":"42","title":"Release Room",`+
		`"unread_count":3,"last_message":{"@type":"message","id":"77",`+
		`"chat_id":"42","date":"1700000000","media_album_id":"0",`+
		`"content":{"@type":"messageText","text":{"@type":"formattedText",`+
		`"text":"the tag is pushed"}}},"type":{"@type":"chatTypeGroup"}}`, extra)

	select {
	case r := <-resultCh:
		if r.err != nil {
			t.Fatalf("GetChat: %v", r.err)
		}
		if r.summary.ID != 42 {
			t.Fatalf("ID = %d, want 42", r.summary.ID)
		}
		if r.summary.LastMessageID != 77 {
			t.Fatalf("LastMessageID = %d, want 77", r.summary.LastMessageID)
		}
		if r.summary.LastMessageText != "the tag is pushed" {
			t.Fatalf("LastMessageText = %q", r.summary.LastMessageText)
		}
		if got := r.summary.LastMessageTime.UTC().Unix(); got != 1700000000 {
			t.Fatalf("LastMessageTime = %d, want 1700000000", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("GetChat did not return")
	}
}

// A name is read by the id of a user, and the id arrives in the same two
// shapes as every other id.
func TestGetUserNameReadsAnIDSentAsAString(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	type result struct {
		name string
		err  error
	}
	resultCh := make(chan result, 1)
	go func() {
		name, err := session.GetUserName(context.Background(), 7)
		resultCh <- result{name: name, err: err}
	}()

	extra := decodeExtra(t, waitForRequestN(t, sender, "getUser", 1))
	feedRawJSON(t, client, `{"@type":"user","id":"7",`+
		`"first_name":"Anna","last_name":"Example","username":"anna"}`, extra)

	select {
	case r := <-resultCh:
		if r.err != nil {
			t.Fatalf("GetUserName: %v", r.err)
		}
		if r.name != "Anna Example" {
			t.Fatalf("name = %q, want %q", r.name, "Anna Example")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("GetUserName did not return")
	}
}

// A user with no name at all is labelled by the id, which is a number TDLib
// may write as a string.
func TestGetUserNameFallsBackToAnIDSentAsAString(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	type result struct {
		name string
		err  error
	}
	resultCh := make(chan result, 1)
	go func() {
		name, err := session.GetUserName(context.Background(), 7)
		resultCh <- result{name: name, err: err}
	}()

	extra := decodeExtra(t, waitForRequestN(t, sender, "getUser", 1))
	feedRawJSON(t, client,
		`{"@type":"user","id":"7","first_name":"","last_name":"","username":""}`,
		extra,
	)

	select {
	case r := <-resultCh:
		if r.err != nil {
			t.Fatalf("GetUserName: %v", r.err)
		}
		if r.name != "user 7" {
			t.Fatalf("name = %q, want %q", r.name, "user 7")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("GetUserName did not return")
	}
}

// The answer to a send carries the message, and the message carries the
// same numbers as every other message.
func TestSendTextMessageReadsTheAnswerSentAsStrings(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	type result struct {
		message Message
		err     error
	}
	resultCh := make(chan result, 1)
	go func() {
		message, err := session.SendTextMessage(
			context.Background(), 42, "hello",
		)
		resultCh <- result{message: message, err: err}
	}()

	extra := decodeExtra(t, waitForRequestN(t, sender, "sendMessage", 1))
	feedRawJSON(t, client, `{"@type":"message","id":"900","chat_id":"42",`+
		`"is_outgoing":true,"date":"1700000000","media_album_id":"0",`+
		`"content":{"@type":"messageText","text":{"@type":"formattedText",`+
		`"text":"hello"}}}`, extra)

	select {
	case r := <-resultCh:
		if r.err != nil {
			t.Fatalf("SendTextMessage: %v", r.err)
		}
		if r.message.ID != 900 {
			t.Fatalf("ID = %d, want 900", r.message.ID)
		}
		if r.message.ChatID != 42 {
			t.Fatalf("ChatID = %d, want 42", r.message.ChatID)
		}
		if r.message.Text != "hello" {
			t.Fatalf("Text = %q, want hello", r.message.Text)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("SendTextMessage did not return")
	}
}

// The live path reads the same message object, out of an update rather than
// out of a response.
func TestParseLiveMessageReadsTheNumbersWrittenAsStrings(t *testing.T) {
	message, err := parseLiveMessage(json.RawMessage(
		`{"@type":"message","id":"77","chat_id":"42",` +
			`"is_outgoing":false,"date":"1700000000",` +
			`"content":{"@type":"messageText","text":{"@type":"formattedText",` +
			`"text":"a new message"}}}`,
	))
	if err != nil {
		t.Fatalf("parseLiveMessage: %v", err)
	}
	if message.ID != 77 {
		t.Fatalf("ID = %d, want 77", message.ID)
	}
	if message.ChatID != 42 {
		t.Fatalf("ChatID = %d, want 42", message.ChatID)
	}
	if message.Text != "a new message" {
		t.Fatalf("Text = %q", message.Text)
	}
	if got := message.Timestamp.UTC().Unix(); got != 1700000000 {
		t.Fatalf("Timestamp = %d, want 1700000000", got)
	}
}

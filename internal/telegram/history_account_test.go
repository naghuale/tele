package telegram

import (
	"context"
	"strconv"
	"testing"
	"time"
)

// This file is a real account, written down.
//
// The pages below are the answers a real account gives to a real walk of
// a real chat, in the order it gives them: the first request comes back
// with whatever the local database has, which on a database that has just
// been opened is one or two messages however many were asked for; the
// request after that comes back with the page that was asked for; and the
// last one comes back with nothing at all, which is the only answer that
// means the beginning of the chat.
//
// Reading HasMore out of the size of a page instead is what made every
// chat of a real account open with the last two messages of it and never
// ask again (#57), so the rule is checked here against the sequence and
// not against one page.

// historyStep is one request of that walk and the answer it got.
type historyStep struct {
	from    MessageID
	limit   int
	body    string
	hasMore bool
	next    MessageID
	read    []MessageID
}

// walkAChat runs the steps against one session and checks the page each
// of them produced.
func walkAChat(t *testing.T, steps []historyStep) {
	t.Helper()

	session, sender, _, client := newSessionWithFakes(t)

	for index, step := range steps {
		page, err := historyPageFrom(
			t, session, sender, client, 42, index+1, step.from, step.limit,
			step.body,
		)
		if err != nil {
			t.Fatalf("step %d: GetChatHistory: %v", index+1, err)
		}

		if page.HasMore != step.hasMore {
			t.Fatalf(
				"step %d: HasMore = %t, want %t for a page of %s",
				index+1, page.HasMore, step.hasMore, step.body,
			)
		}
		if page.NextFrom != step.next {
			t.Fatalf(
				"step %d: NextFrom = %d, want %d", index+1, page.NextFrom, step.next,
			)
		}
		if len(page.Messages) != len(step.read) {
			t.Fatalf(
				"step %d: %d messages, want %d", index+1, len(page.Messages), len(step.read),
			)
		}
		for position, want := range step.read {
			if page.Messages[position].ID != want {
				t.Fatalf(
					"step %d: messages[%d].ID = %d, want %d",
					index+1, position, page.Messages[position].ID, want,
				)
			}
		}
	}
}

// historyPageFrom runs one getChatHistory request from a boundary and
// returns the page or the error it came back with.
//
// It is historyPage for a request that continues a walk rather than one
// that starts it, and the walk is the thing the rule of HasMore is about.
func historyPageFrom(
	t *testing.T,
	session *AuthorizedSession,
	sender *fakeSender,
	client *Client,
	chatID ChatID,
	request int,
	from MessageID,
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
		page, err := session.GetChatHistory(context.Background(), chatID, from, limit)
		resultCh <- result{page: page, err: err}
	}()

	extra := decodeExtra(
		t, waitForRequestN(t, sender, "getChatHistory", request),
	)
	feedRawJSON(t, client, body, extra)

	select {
	case r := <-resultCh:
		return r.page, r.err
	case <-time.After(3 * time.Second):
		t.Fatal("GetChatHistory did not return")
		return HistoryPage{}, nil
	}
}

// The walk of a chat whose history is years of it, on an account that has
// just been started: two messages, then a full page, then the beginning.
func TestHasMoreFollowsTheAnswersOfARealAccount(t *testing.T) {
	walkAChat(t, []historyStep{
		{
			from:    0,
			limit:   50,
			body:    `{"@type":"messages","total_count":4171,"messages":[` + textMessage(`"4021"`, `"42"`, `"1700000000"`, `"0"`, "and I pushed the tag") + `,` + textMessage(`"4020"`, `"42"`, `"1699999999"`, `"0"`, "the notes are in the changelog") + `]}`,
			hasMore: true,
			next:    4020,
			read:    []MessageID{4021, 4020},
		},
		{
			from:  4020,
			limit: 50,
			body: `{"@type":"messages","total_count":4171,"messages":[` +
				textMessage(`"4020"`, `"42"`, `"1699999999"`, `"0"`, "the notes are in the changelog") +
				`,` + textMessage(`"4019"`, `"42"`, `"1699999000"`, `"0"`, "yesterday") +
				`,` + textMessage(`"4018"`, `"42"`, `"1699998000"`, `"0"`, "the day before") +
				`]}`,
			hasMore: true,
			next:    4018,
			read:    []MessageID{4020, 4019, 4018},
		},
		{
			from:    1,
			limit:   50,
			body:    `{"@type":"messages","total_count":4171,"messages":[]}`,
			hasMore: false,
			next:    0,
		},
	})
}

// A chat of one message is the other end of it: the first page is the
// whole history and the next request is empty, and the two answers
// together are what says the chat has nothing above its only message.
func TestHasMoreOfAChatWithOneMessage(t *testing.T) {
	walkAChat(t, []historyStep{
		{
			from:    0,
			limit:   50,
			body:    `{"@type":"messages","total_count":1,"messages":[` + textMessage(`"7"`, `"42"`, `"1700000000"`, `"0"`, "hi") + `]}`,
			hasMore: true,
			next:    7,
			read:    []MessageID{7},
		},
		{
			from:    7,
			limit:   50,
			body:    `{"@type":"messages","total_count":1,"messages":[` + textMessage(`"7"`, `"42"`, `"1700000000"`, `"0"`, "hi") + `]}`,
			hasMore: true,
			next:    7,
			read:    []MessageID{7},
		},
		{
			from:    7,
			limit:   50,
			body:    `{"@type":"messages","total_count":1,"messages":[]}`,
			hasMore: false,
		},
	})
}

// A page with nothing but entries this build cannot read still moves the
// boundary to the oldest of them. The alternative is a page asked for
// twice and a user who scrolls up and nothing happens: the identifier is
// the one field a message cannot be read without, so the boundary does
// not wait for the rest.
func TestTheBoundaryCrossesAnEntryThatCouldNotBeRead(t *testing.T) {
	walkAChat(t, []historyStep{
		{
			from:  0,
			limit: 50,
			body: `{"@type":"messages","total_count":2,"messages":[` +
				`{"@type":"message","id":"2","chat_id":"42","date":"1700000000",` +
				`"media_album_id":{"nested":true},` +
				`"content":{"@type":"messageText","text":` +
				`{"@type":"formattedText","text":"the album id is an object"}}}` +
				`,` + `{"@type":"message","id":"1","chat_id":"42","date":"1",` +
				`"media_album_id":{"nested":true},"content":{"@type":"messageText",` +
				`"text":{"@type":"formattedText","text":"and so is this one"}}}` +
				`]}`,
			hasMore: true,
			next:    1,
		},
	})
}

// The count of the entries this build could not read is the number that
// says a message of this user is missing, and the boundary moves past them
// so that a page is never asked for twice.
func TestAPageThatCouldNotBeReadWholeStillMovesTheBoundary(t *testing.T) {
	walkAChat(t, []historyStep{
		{
			from:  0,
			limit: 50,
			body: `{"@type":"messages","total_count":3,"messages":[` +
				textMessage(`"3"`, `"42"`, `"1700000000"`, `"0"`, "mine") + `,` +
				`{"@type":"message","id":"two","chat_id":"42",` +
				`"date":"1699999999","content":{"@type":"messageText","text":` +
				`{"@type":"formattedText","text":"a number TDLib never sent"}}}` +
				`,` + `{"@type":"message","id":"forty","chat_id":"42",` +
				`"date":"1","content":{"@type":"messageText","text":` +
				`{"@type":"formattedText","text":"unreadable"}}}` +
				`,` + textMessage(`"1"`, `"42"`, `"1699999998"`, `"0"`, "older") +
				`]}`,
			hasMore: true,
			next:    1,
			read:    []MessageID{3, 1},
		},
	})
}

// The unreadable count is the number that reaches the interface as a
// number, so it has to be the number of the entries that were left out and
// not the number of the messages that came through.
func TestThePageCountsTheEntriesItCouldNotRead(t *testing.T) {
	session, sender, _, client := newSessionWithFakes(t)

	page, err := historyPage(
		t, session, sender, client, 42, 50,
		`{"@type":"messages","total_count":5,"messages":[`+
			textMessage(`"5"`, `"42"`, `"1700000000"`, `"0"`, "readable")+
			`,`+`{"@type":"message","id":"four","chat_id":"42","date":"1"}`+
			`,`+`null`+
			`,`+textMessage(`"3"`, `"42"`, `"1700000000"`, `"0"`, "readable too")+
			`]}`,
	)
	if err != nil {
		t.Fatalf("GetChatHistory: %v", err)
	}

	if page.Unreadable != 2 {
		t.Fatalf("Unreadable = %d, want 2", page.Unreadable)
	}
	if len(page.Messages) != 2 {
		t.Fatalf("messages = %d, want the 2 that could be read", len(page.Messages))
	}
	if !page.HasMore {
		t.Fatal("HasMore = false, want true: the page was not empty")
	}
}

// The messages of this user are the ones that went missing: TDLib writes
// every kind of message of a person with fields this build has no words
// for, and the fields it adds to a message of this user are the ones
// nobody tested.
//
// Every kind below is an outgoing message as TDLib writes it: a text, a
// photograph, a message that is still on its way out, one somebody
// forwarded, and one that is a reply to another. They are all readable,
// which is the whole claim: a message of this user that this build cannot
// read is a message the user cannot see they sent.
func TestEveryKindOfOutgoingMessageIsReadable(t *testing.T) {
	cases := map[string]struct {
		body    string
		wantID  MessageID
		wantOut bool
		text    string
		media   string
	}{
		"a text": {
			body: `{"@type":"message","id":"900","chat_id":"42",` +
				`"is_outgoing":true,"date":"1700000000","media_album_id":"0",` +
				`"sender_id":{"@type":"messageSenderUser","user_id":"5"},` +
				`"content":{"@type":"messageText","text":{"@type":"formattedText",` +
				`"text":"shipped"}}}`,
			wantID: 900, wantOut: true, text: "shipped",
		},
		"a photograph": {
			body: `{"@type":"message","id":"901","chat_id":"42",` +
				`"is_outgoing":true,"date":"1700000001","media_album_id":"0",` +
				`"sender_id":{"@type":"messageSenderUser","user_id":"5"},` +
				`"content":{"@type":"messagePhoto","photo":{"@type":"photo",` +
				`"id":1,"width":90,"height":90},"caption":{"@type":"formattedText",` +
				`"text":"the new wallpaper"}}}`,
			wantID: 901, wantOut: true, text: "", media: "photo",
		},
		"one that is still on its way out": {
			body: `{"@type":"message","id":"902","chat_id":"42",` +
				`"is_outgoing":true,"date":"1700000002","media_album_id":"0",` +
				`"sender_id":{"@type":"messageSenderUser","user_id":"5"},` +
				`"sending_state":{"@type":"messageSendingStatePending"},` +
				`"content":{"@type":"messageText","text":{"@type":"formattedText",` +
				`"text":"still going"}}}`,
			wantID: 902, wantOut: true, text: "still going",
		},
		"one somebody forwarded": {
			body: `{"@type":"message","id":"903","chat_id":"42",` +
				`"is_outgoing":true,"date":"1700000003","media_album_id":"0",` +
				`"sender_id":{"@type":"messageSenderUser","user_id":"5"},` +
				`"forward_info":{"@type":"messageForwardInfo",` +
				`"origin":{"@type":"messageOriginUser","sender_user_id":7,` +
				`"date":1690000000}},"content":{"@type":"messageText",` +
				`"text":{"@type":"formattedText","text":"worth a read"}}}`,
			wantID: 903, wantOut: true, text: "worth a read",
		},
		"a reply to another message": {
			body: `{"@type":"message","id":"904","chat_id":"42",` +
				`"is_outgoing":true,"date":"1700000004","media_album_id":"0",` +
				`"sender_id":{"@type":"messageSenderUser","user_id":"5"},` +
				`"reply_to":{"@type":"messageReplyToMessage","message_id":800},` +
				`"content":{"@type":"messageText","text":{"@type":"formattedText",` +
				`"text":"agreed"}}}`,
			wantID: 904, wantOut: true, text: "agreed",
		},
	}

	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			session, sender, _, client := newSessionWithFakes(t)

			page, err := historyPage(t, session, sender, client, 42, 1, pageOf(want.body))
			if err != nil {
				t.Fatalf("GetChatHistory: %v", err)
			}

			if len(page.Messages) != 1 {
				t.Fatalf(
					"messages = %d, want 1: %s is not readable",
					len(page.Messages), name,
				)
			}
			message := page.Messages[0]
			if message.ID != want.wantID {
				t.Fatalf("ID = %d, want %d", message.ID, want.wantID)
			}
			if message.Outgoing != want.wantOut {
				t.Fatalf("Outgoing = %t, want %t", message.Outgoing, want.wantOut)
			}
			if message.Text != want.text {
				t.Fatalf("Text = %q, want %q", message.Text, want.text)
			}
			if message.Media != want.media {
				t.Fatalf("Media = %q, want %q", message.Media, want.media)
			}
			if page.Unreadable != 0 {
				t.Fatalf("Unreadable = %d, want 0", page.Unreadable)
			}
		})
	}
}

// pageOf puts one message into an answer, with the total count a real
// answer carries.
func pageOf(message string) string {
	return `{"@type":"messages","total_count":` +
		strconv.Itoa(countMessages(message)) + `,"messages":[` + message + `]}`
}

// countMessages is the number of messages in a one-message answer, which
// is one unless the answer is empty.
func countMessages(message string) int {
	if message == "" {
		return 0
	}

	return 1
}

package application

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"telecli/internal/telegram"
	"telecli/internal/tui"
	"telecli/internal/tui/theme"
)

// A channel whose history TDLib has not brought down from the server yet
// (#27, the owner, 02.10, build review-tele4).
//
// The row of the chat list said `[photo] Целых 200+ грамм…` — Telegram held
// the conversation — and the conversation under it said "No messages yet",
// and nothing could be read there. Closing the program and opening it again
// found the whole conversation in the same chat: in between, TDLib had
// downloaded what the first request started it downloading. An empty first
// page was read as an empty chat, and an empty chat is the one thing this
// program says out loud: "Write the first message below."
//
// So the newest page of a chat that answers empty while Telegram reports a
// last message in that chat is asked again, with a pause between the asks
// and a count on them. The answers below are recorded ones, read by the
// decoder a live answer is read by, and the interface is driven over the
// real adapter rather than over a source built to answer what the model
// wants to hear.

// notLoadedChatID is the channel the owner opened.
const notLoadedChatID = 1001

// The recorded first answer: a page with nothing in it, which is what TDLib
// says about a chat it has not loaded the history of yet.
const recordedEmptyNewestPage = `{"@type":"messages","total_count":0,"messages":[]}`

// The recorded second answer: the last two messages of the same channel,
// numbers written the way TDLib writes them.
var recordedLoadedNewestPage = `{"@type":"messages","total_count":2,"messages":[` +
	recordedChannelMessage(`"512"`, `"1700000500"`, "Целых 200+ грамм") + `,` +
	recordedChannelMessage(`"511"`, `"1700000400"`, "Сегодня на кухне") + `]}`

// recordedChannelMessage is one message of that channel as TDLib writes it.
func recordedChannelMessage(id, date, text string) string {
	chatID := strconv.FormatInt(notLoadedChatID, 10)

	return `{"@type":"message","id":` + id +
		`,"chat_id":` + chatID +
		`,"is_outgoing":false,"date":` + date +
		`,"media_album_id":"0",` +
		`"sender_id":{"@type":"messageSenderChat","chat_id":` + chatID +
		`},"content":{"@type":"messageText","text":{"@type":"formattedText",` +
		`"text":"` + text + `"}}}`
}

// A page TDLib did send, and every entry of it is a message this build
// cannot read: the identifiers are words where TDLib writes numbers. It is
// not an empty answer, and asking again for it is how a page that is already
// there would be waited for.
var recordedUnreadableNewestPage = `{"@type":"messages","total_count":2,` +
	`"messages":[{"@type":"message","id":"words","chat_id":` +
	strconv.FormatInt(notLoadedChatID, 10) +
	`,"date":"1700000500"}]}`

// notLoadedChats is a Telegram that answers from a recorded sequence: one
// answer per request, and the last one again for every request past it.
//
// The answers are the JSON of the wire and they are read by
// telegram.DecodeHistoryPage, which is what reads a live answer. A fixture
// that built a page by hand would prove the adapter against a shape nobody
// sends.
type notLoadedChats struct {
	chat telegram.ChatSummary

	// answers is the recorded sequence, and the last of them answers every
	// request after the sequence is over: a chat TDLib never loads is a
	// chat that answers the same empty page for ever.
	answers []string

	// failure, when set, is what every request after the first answers
	// with — the repeat going wrong rather than the first one.
	failure error

	requests []notLoadedRequest
}

// notLoadedRequest is one request as the adapter made it.
type notLoadedRequest struct {
	from  int64
	limit int
}

func (f *notLoadedChats) GetChats(
	context.Context, int,
) (telegram.ChatListSnapshot, error) {
	return telegram.ChatListSnapshot{Chats: []telegram.ChatSummary{f.chat}}, nil
}

func (f *notLoadedChats) GetChatHistory(
	_ context.Context,
	chatID telegram.ChatID,
	from telegram.MessageID,
	limit int,
) (telegram.HistoryPage, error) {
	f.requests = append(f.requests, notLoadedRequest{
		from:  int64(from),
		limit: limit,
	})

	if f.failure != nil && len(f.requests) > 1 {
		return telegram.HistoryPage{}, f.failure
	}

	answer := f.answers[len(f.answers)-1]
	if index := len(f.requests) - 1; index < len(f.answers) {
		answer = f.answers[index]
	}

	return telegram.DecodeHistoryPage(telegram.RawMessage(answer), chatID)
}

func (f *notLoadedChats) SendTextMessage(
	context.Context, telegram.ChatID, string,
) (telegram.Message, error) {
	return telegram.Message{}, nil
}

// GetChat and GetUserName are the names of the same Telegram; a channel is
// named by itself, and the last message it reports is what says that this
// chat is not empty.
func (f *notLoadedChats) GetChat(
	context.Context, telegram.ChatID,
) (telegram.ChatSummary, error) {
	return f.chat, nil
}

func (f *notLoadedChats) GetUserName(context.Context, int64) (string, error) {
	return "Nebrito", nil
}

// asks is how many times the newest page of the chat was asked for.
func (f *notLoadedChats) asks() int {
	return len(f.requests)
}

// notLoadedService is the adapter over a recorded Telegram, with the pause
// between the repeats at a millisecond: these tests are about the repeat and
// not about the wait for it, and six seconds of a real pause in a test is a
// test nobody runs.
func notLoadedService(chats *notLoadedChats) *TelegramChatService {
	service := NewTelegramChatService(chats)
	service.historyRetryWait = time.Millisecond

	return service
}

// The owner's screen: a conversation whose history came with the second ask.
func TestAChannelTDLibHasNotLoadedShowsItsMessages(t *testing.T) {
	chats := &notLoadedChats{
		chat:    notLoadedChannel(),
		answers: []string{recordedEmptyNewestPage, recordedLoadedNewestPage},
	}

	screen := conversationOf(t, notLoadedService(chats))

	if strings.Contains(screen, "No messages yet") {
		t.Fatalf(
			"the chat list says the channel has messages and the "+
				"conversation says it has none:\n%s", screen,
		)
	}
	if !strings.Contains(screen, "Целых 200+ грамм") {
		t.Fatalf("the conversation does not show the newest message:\n%s", screen)
	}
	if !strings.Contains(screen, "Сегодня на кухне") {
		t.Fatalf("the conversation shows one message of the page:\n%s", screen)
	}
}

// The repeat asks for the newest page again and not for the boundary of the
// page that came back empty: the empty page has no boundary, and asking from
// the oldest message of a page that was never read is asking for the same
// empty page with more words in the request.
func TestTheNewestPageOfAChatWithALastMessageIsAskedAgain(t *testing.T) {
	chats := &notLoadedChats{
		chat:    notLoadedChannel(),
		answers: []string{recordedEmptyNewestPage, recordedLoadedNewestPage},
	}

	page, err := notLoadedService(chats).LoadHistory(
		context.Background(), notLoadedChatID, 0, defaultHistoryLimit,
	)
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}

	if len(page.Messages) != 2 {
		t.Fatalf("messages = %d, want the 2 of the second answer", len(page.Messages))
	}
	if chats.asks() != 2 {
		t.Fatalf("asks = %d, want 2: the first answer and one repeat", chats.asks())
	}
	for index, request := range chats.requests {
		if request.from != 0 {
			t.Fatalf(
				"ask %d went from message %d, want the newest page",
				index+1, request.from,
			)
		}
		if request.limit != defaultHistoryLimit {
			t.Fatalf(
				"ask %d asked for %d messages, want %d: the repeat is the "+
					"same question asked again",
				index+1, request.limit, defaultHistoryLimit,
			)
		}
	}
}

// A chat nobody has written in has no last message, and an empty first page
// of it is the end of its history rather than a history that has not arrived.
// One ask and the screen says the chat is empty, which is what it is.
func TestAChatWithNothingInItIsAskedOnceAndSaysItIsEmpty(t *testing.T) {
	chats := &notLoadedChats{
		chat:    telegram.ChatSummary{ID: notLoadedChatID, Title: "Новый чат"},
		answers: []string{recordedEmptyNewestPage},
	}

	screen := conversationOf(t, notLoadedService(chats))

	if chats.asks() != 1 {
		t.Fatalf("asks = %d, want 1: an empty chat is not asked twice", chats.asks())
	}
	if !strings.Contains(screen, "No messages yet") {
		t.Fatalf("an empty chat does not say it is empty:\n%s", screen)
	}
}

// The repeats are counted. A wait that never ends is the same defect with a
// spinner on it, and the end of them has to say what it is: the history is
// not loaded yet, which is not the same as there being nothing to read.
func TestTheRepeatsOfAnEmptyNewestPageAreBounded(t *testing.T) {
	chats := &notLoadedChats{
		chat:    notLoadedChannel(),
		answers: []string{recordedEmptyNewestPage},
	}

	_, err := notLoadedService(chats).LoadHistory(
		context.Background(), notLoadedChatID, 0, defaultHistoryLimit,
	)
	if !errors.Is(err, ErrHistoryNotLoaded) {
		t.Fatalf("LoadHistory error = %v, want ErrHistoryNotLoaded", err)
	}

	if want := historyNotLoadedRetries + 1; chats.asks() != want {
		t.Fatalf("asks = %d, want %d: the page and its repeats", chats.asks(), want)
	}
}

// What the interface says when the repeats are over: that the history did
// not load, and not that the chat is empty.
func TestAConversationWhoseHistoryDidNotLoadSaysSo(t *testing.T) {
	chats := &notLoadedChats{
		chat:    notLoadedChannel(),
		answers: []string{recordedEmptyNewestPage},
	}

	screen := conversationOf(t, notLoadedService(chats))

	if strings.Contains(screen, "No messages yet") {
		t.Fatalf(
			"a chat whose own last message is on the screen is called "+
				"empty:\n%s", screen,
		)
	}
	if !strings.Contains(screen, "Failed to load history") {
		t.Fatalf("the conversation does not say the history did not load:\n%s", screen)
	}
}

// The wait is a wait and not a poll: it is counted, and the counting is what
// keeps a conversation that will never load from being asked for ever.
func TestTheWaitForAHistoryThatHasNotLoadedIsBounded(t *testing.T) {
	longest := time.Duration(historyNotLoadedRetries) * historyNotLoadedRetryWaitMax
	if longest > 10*time.Second {
		t.Fatalf(
			"the repeats of an empty page can take %s, want no more than 10s: "+
				"a user who opened a chat is waiting, not watching",
			longest,
		)
	}
}

// A page whose entries this build cannot read is a page TDLib did send. It
// is not a history that has not arrived, and asking again for it would be
// waiting five times for a page that is already here.
func TestAPageOfEntriesThisBuildCannotReadIsNotAskedAgain(t *testing.T) {
	chats := &notLoadedChats{
		chat:    notLoadedChannel(),
		answers: []string{recordedUnreadableNewestPage},
	}

	page, err := notLoadedService(chats).LoadHistory(
		context.Background(), notLoadedChatID, 0, defaultHistoryLimit,
	)
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}

	if chats.asks() != 1 {
		t.Fatalf("asks = %d, want 1: TDLib sent a page", chats.asks())
	}
	if page.Unreadable != 1 {
		t.Fatalf("Unreadable = %d, want 1", page.Unreadable)
	}
	if !page.HasMore {
		t.Fatal("HasMore = false, want true: the page was not empty")
	}
}

// A repeat that fails is a failure, and not a history that has not arrived:
// the two are told apart, because one of them is worth asking again at once
// and the other is not.
func TestAFailureOfTheRepeatIsNotAHistoryThatHasNotLoaded(t *testing.T) {
	chats := &notLoadedChats{
		chat:    notLoadedChannel(),
		answers: []string{recordedEmptyNewestPage},
		failure: errors.New("ERROR 400: Chat not found"),
	}

	_, err := notLoadedService(chats).LoadHistory(
		context.Background(), notLoadedChatID, 0, defaultHistoryLimit,
	)
	if err == nil {
		t.Fatal("LoadHistory returned no error for a failing repeat")
	}
	if errors.Is(err, ErrHistoryNotLoaded) {
		t.Fatalf("LoadHistory error = %v, want the failure of the repeat", err)
	}
	if chats.asks() != 2 {
		t.Fatalf("asks = %d, want 2: the first failure ended the repeats", chats.asks())
	}
}

// notLoadedChannel is the channel the owner opened, with the last message
// the chat list drew its row from.
func notLoadedChannel() telegram.ChatSummary {
	return telegram.ChatSummary{
		ID:              notLoadedChatID,
		Title:           "Nebrito",
		Kind:            telegram.ChatKindSupergroup,
		IsChannel:       true,
		LastMessageID:   512,
		LastMessageText: "Целых 200+ грамм",
		LastMessageTime: time.Unix(1700000500, 0).UTC(),
	}
}

// notLoadedSubmitter is a composer submitter that never submits: these tests
// are about what the conversation shows when it opens, and a queue behind it
// would be a second thing under test.
type notLoadedSubmitter struct{}

func (notLoadedSubmitter) SubmitMessage(
	context.Context, int64, string,
) (tui.Submission, error) {
	return tui.Submission{}, errors.New("not used by this test")
}

// conversationOf opens the one chat of a source the way the program does,
// and returns the screen the owner was left looking at.
func conversationOf(t *testing.T, source tui.ChatSource) string {
	t.Helper()

	model, opening := openingChat(t, source)

	return applyStatusMsgs(t, model, runStatusCmds(t, opening)).View()
}

// While the repeats are being asked, the feed says that it is loading. That
// is the whole difference between a conversation that is waiting for a
// history and a conversation that is empty, and it is the sentence a user
// reads for the six seconds the waiting takes.
func TestAConversationWaitingForItsHistorySaysItIsLoading(t *testing.T) {
	chats := &notLoadedChats{
		chat:    notLoadedChannel(),
		answers: []string{recordedEmptyNewestPage},
	}

	model, _ := openingChat(t, notLoadedService(chats))
	screen := model.View()

	if !strings.Contains(screen, "Loading history") {
		t.Fatalf("the conversation does not say the history is on its way:\n%s", screen)
	}
	if strings.Contains(screen, "No messages yet") {
		t.Fatalf(
			"a conversation whose history is on its way says it is empty:\n%s",
			screen,
		)
	}
}

// openingChat drives the model as far as the opening of the chat, and hands
// back what the opening asked for without running it: the screen of that
// instant is the screen of a chat whose history has not answered yet.
//
// The terminal is measured first, then the chat list is loaded, then the
// chat is opened: the history of a chat is asked for on the way in, so
// nothing here writes a state a user could not reach by opening a chat.
func openingChat(t *testing.T, source tui.ChatSource) (tui.Model, tea.Cmd) {
	t.Helper()

	model, err := tui.NewModelWithDependencies(context.Background(), tui.Dependencies{
		Source:           source,
		MessageSubmitter: notLoadedSubmitter{},
		Theme:            theme.DefaultTheme().ForProfile(theme.ProfileNoColor),
		ColorProfile:     theme.ProfileNoColor,
	})
	if err != nil {
		t.Fatalf("model: %v", err)
	}

	model = settleNotLoaded(t, model, tea.WindowSizeMsg{Width: 100, Height: 30})
	model = applyStatusMsgs(t, model, runStatusCmds(t, model.Init()))

	return updateNotLoaded(t, model, tea.KeyMsg{Type: tea.KeyEnter})
}

// settleNotLoaded gives the model a message and runs everything the model
// answers with, so that the screen is the one a user is left with rather
// than the one a frame was drawn from.
func settleNotLoaded(t *testing.T, model tui.Model, msg tea.Msg) tui.Model {
	t.Helper()

	updated, cmd := updateNotLoaded(t, model, msg)

	return applyStatusMsgs(t, updated, runStatusCmds(t, cmd))
}

// updateNotLoaded gives the model a message and returns the model it became
// and what it asked for.
func updateNotLoaded(
	t *testing.T,
	model tui.Model,
	msg tea.Msg,
) (tui.Model, tea.Cmd) {
	t.Helper()

	updated, cmd := model.Update(msg)
	typed, ok := updated.(tui.Model)
	if !ok {
		t.Fatalf("Update returned %T, want tui.Model", updated)
	}

	return typed, cmd
}

package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// This file is the conversation a user opens into.
//
// TDLib answers the first getChatHistory of a chat with whatever the local
// database holds, and on a database that has just been opened that is one
// or two messages however many were asked for. The interface took the
// first page for the whole history, so a chat a user has been writing in
// for years opened as its last two messages — and their own messages were
// not even in the two (#57).
//
// The fix is to ask again from the oldest message of the page, and again,
// until there are enough messages in the feed to fill it. Three things
// bound that: an empty page is the end of the history, a source that says
// so is believed, and the number of requests and of messages is capped so
// that a channel with a hundred thousand messages in it is not read into
// memory to draw one screen of it.

// fillSource answers history requests from a script of pages and counts
// them, which is what the bound on the repeat is about.
type fillSource struct {
	pages []HistoryPage

	calls int
	seen  []int64
}

func (s *fillSource) ListChats(context.Context) ([]Chat, error) {
	return []Chat{{ID: 7, Title: "A"}}, nil
}

func (s *fillSource) LoadHistory(
	_ context.Context,
	_ int64,
	fromMessageID int64,
	_ int,
) (HistoryPage, error) {
	s.calls++
	s.seen = append(s.seen, fromMessageID)

	if s.calls > len(s.pages) {
		return HistoryPage{}, nil
	}

	return s.pages[s.calls-1], nil
}

func (s *fillSource) SendMessage(context.Context, int64, string) (Message, error) {
	return Message{}, nil
}

// pageOfMessages is a page of the given messages, newest first, and it
// says there is more above it: the pages of a walk of a real account are
// all but the last one.
func pageOfMessages(ids []int64, hasMore bool) HistoryPage {
	messages := make([]Message, 0, len(ids))
	for _, id := range ids {
		messages = append(messages, Message{ID: id, Text: "message"})
	}

	var next int64
	if len(ids) > 0 {
		next = ids[len(ids)-1]
	}

	return HistoryPage{Messages: messages, NextFrom: next, HasMore: hasMore}
}

// idsOf is the identifiers of the messages of a conversation, oldest
// first, which is the order the model keeps them in.
func idsOf(messages []Message) []int64 {
	ids := make([]int64, 0, len(messages))
	for _, message := range messages {
		ids = append(ids, message.ID)
	}

	return ids
}

// openAChatWith drives a model into a conversation the way Enter does and
// runs the load commands it asks for until it stops asking, which is
// exactly what the program does with the commands a key returns.
func openAChatWith(t *testing.T, source ChatSource) Model {
	t.Helper()

	m := NewModelWithSource(source)
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	m, _ = updateModel(t, m, chatsLoadedMsg{chats: []Chat{{ID: 7, Title: "A"}}})

	m, cmd := updateModel(t, m, press(tea.KeyEnter))
	if cmd == nil {
		t.Fatal("Enter did not start a history load")
	}

	return runLoads(t, m, cmd)
}

// runLoads runs the commands a key returned and applies what they answer,
// until the model asks for nothing more.
//
// It is the loop the program runs: a key returns commands, they answer
// with messages, and the messages answer with commands. Every key here
// also asks for a repaint (repaint.go), so the loop has to carry a batch
// rather than one message, and it has to keep every command a message
// returned rather than only the last one.
func runLoads(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()

	for depth := 0; cmd != nil; depth++ {
		if depth > maxHistoryFillRequests+2 {
			t.Fatal("the model keeps asking for pages")
		}

		asked := make([]tea.Cmd, 0, 2)
		for _, msg := range messagesOf(t, cmd) {
			var next tea.Cmd
			m, next = updateModel(t, m, msg)
			if next != nil {
				asked = append(asked, next)
			}
		}

		cmd = tea.Batch(asked...)
	}

	return m
}

// messagesOf runs a command and returns what it delivers, a batch being
// several messages rather than one.
func messagesOf(t *testing.T, cmd tea.Cmd) []tea.Msg {
	t.Helper()

	if cmd == nil {
		return nil
	}

	switch msg := cmd().(type) {
	case nil:
		return nil

	case tea.BatchMsg:
		var messages []tea.Msg
		for _, batched := range msg {
			messages = append(messages, messagesOf(t, batched)...)
		}

		return messages

	default:
		return []tea.Msg{msg}
	}
}

// A source that answers one message at a time is the case the whole
// change is about: the conversation opens on the first message and the
// feed fills as the pages arrive, within the bound on the requests.
func TestAChatThatAnswersOneMessageAtATimeStillFillsTheFeed(t *testing.T) {
	pages := []HistoryPage{pageOfMessages([]int64{60}, true)}
	for id := int64(59); id > 40; id-- {
		pages = append(pages, pageOfMessages([]int64{id}, true))
	}

	source := &fillSource{pages: pages}
	m := openAChatWith(t, source)

	got := idsOf(m.selected().Messages)
	if len(got) < 2 {
		t.Fatalf("messages = %v, want the feed filled", got)
	}
	if got[len(got)-1] != 60 {
		t.Fatalf("messages = %v, want the newest last", got)
	}
	if source.calls > maxHistoryFillRequests {
		t.Fatalf(
			"%d requests for one conversation, want at most %d",
			source.calls, maxHistoryFillRequests,
		)
	}

	// Every request after the first continues from the oldest message of
	// the one before it, which is the only boundary a history has.
	if source.seen[0] != 0 {
		t.Fatalf("the first request was from %d, want 0", source.seen[0])
	}
	for index := 2; index < len(source.seen); index++ {
		if source.seen[index] >= source.seen[index-1] {
			t.Fatalf(
				"request %d was from %d, want below the %d of the one before",
				index+1, source.seen[index], source.seen[index-1],
			)
		}
	}
}

// A page that fills the feed stops the repeat: a channel that answers with
// fifty messages is not asked for a second page just because the bound
// allows one.
func TestAPageThatFillsTheFeedStopsTheRepeat(t *testing.T) {
	full := make([]int64, 0, 50)
	for id := int64(100); id > 50; id-- {
		full = append(full, id)
	}
	source := &fillSource{pages: []HistoryPage{pageOfMessages(full, true)}}
	m := openAChatWith(t, source)

	if source.calls != 1 {
		t.Fatalf("%d requests, want 1: the page was a full one", source.calls)
	}
	if got := len(m.selected().Messages); got != 50 {
		t.Fatalf("messages = %d, want the 50 of the page", got)
	}
}

// A page of thirty messages fills a tall feed and not a short one, and
// both are the whole of what came back: the feed is not padded with
// messages that were never sent.
func TestAFeedIsFullOfTheMessagesThatWereThere(t *testing.T) {
	ids := make([]int64, 0, 30)
	for id := int64(30); id > 0; id-- {
		ids = append(ids, id)
	}
	source := &fillSource{pages: []HistoryPage{pageOfMessages(ids, false)}}
	m := openAChatWith(t, source)

	if got := idsOf(m.selected().Messages); len(got) != 30 {
		t.Fatalf("messages = %v, want the 30 that were there", got)
	}
	// The page arrives newest first, as TDLib answers it, and the
	// conversation is read oldest first (§8.3).
	for index, id := range idsOf(m.selected().Messages) {
		if id != int64(index+1) {
			t.Fatalf("messages[%d] = %d, want %d, oldest first",
				index, id, index+1)
		}
	}
}

// An empty page is the end of the history, and the conversation opens on
// the empty state of §17 rather than on a loop of requests.
func TestAChatWithNoHistoryStopsAtTheFirstEmptyPage(t *testing.T) {
	source := &fillSource{pages: []HistoryPage{{Messages: nil, HasMore: false}}}
	m := openAChatWith(t, source)

	if source.calls != 1 {
		t.Fatalf("%d requests, want 1", source.calls)
	}
	if m.historyState != loadStateEmpty {
		t.Fatalf("historyState = %s, want empty", m.historyState)
	}

	m.width, m.height = 120, 30
	if view := m.View(); !strings.Contains(view, noMessagesHint) {
		t.Fatalf("the conversation does not say what to do:\n%s", view)
	}
}

// The bound is a bound and not a hope: a source that says there is always
// more costs a fixed number of requests and no more.
func TestASourceThatNeverSaysNoCostsAFixedNumberOfRequests(t *testing.T) {
	pages := make([]HistoryPage, 0, 2*maxHistoryFillRequests)
	for offset := range 2 * maxHistoryFillRequests {
		pages = append(pages, pageOfMessages(
			[]int64{int64(100 - offset)}, true,
		))
	}
	source := &fillSource{pages: pages}
	openAChatWith(t, source)

	if source.calls != maxHistoryFillRequests {
		t.Fatalf(
			"%d requests, want the %d the bound allows",
			source.calls, maxHistoryFillRequests,
		)
	}
}

// The number of messages the repeat may bring in is bounded as well, so
// that a source answering at the full page size costs a screenful rather
// than five of them.
func TestTheRepeatIsBoundedByMessagesAsWellAsByRequests(t *testing.T) {
	full := make([]int64, 0, 50)
	for id := int64(500); id > 450; id-- {
		full = append(full, id)
	}

	pages := make([]HistoryPage, 0, maxHistoryFillRequests+1)
	for range maxHistoryFillRequests + 1 {
		pages = append(pages, pageOfMessages(full, true))
	}
	source := &fillSource{pages: pages}
	m := openAChatWith(t, source)

	brought := len(m.selected().Messages)
	if brought > maxHistoryFillMessages {
		t.Fatalf(
			"%d messages in one conversation, want at most %d",
			brought, maxHistoryFillMessages,
		)
	}
}

// ↑ at the top edge asks for the page above the oldest message it has, and
// it goes on asking while the feed is short of a screenful: the page is
// asked for to have messages in the conversation, and half a screen of
// them is not a conversation however the page came to be asked for.
func TestScrollingUpAtTheTopFillsTheFeedTheSameWay(t *testing.T) {
	pages := make([]HistoryPage, 0, 2*maxHistoryFillRequests)
	for offset := range 2 * maxHistoryFillRequests {
		pages = append(pages, pageOfMessages(
			[]int64{int64(100 - offset)}, true,
		))
	}
	source := &fillSource{pages: pages}
	m := openAChatWith(t, source)
	before := source.calls

	m.focus = FocusHistory
	m = selectOldestMessage(t, m)
	_, cmd := updateModel(t, m, press(tea.KeyUp))
	if cmd == nil {
		t.Fatal("↑ on the oldest loaded message must ask for the page above it")
	}
	m = runLoads(t, m, cmd)

	if source.calls == before {
		t.Fatal("↑ asked for nothing")
	}
	if got := len(m.selected().Messages); got <= 1 {
		t.Fatalf("messages = %d, want the feed filled by the pages above", got)
	}
	if source.calls-before > maxHistoryFillRequests {
		t.Fatalf(
			"↑ cost %d requests, want at most %d",
			source.calls-before, maxHistoryFillRequests,
		)
	}
}

// A page the source could not read is a message of this user that is not
// on the screen, and the only place that can say so is the log: the screen
// of a user reading somebody's conversation has no business carrying a
// sentence about what the program failed to decode (§11.3, §19).
func TestAHistoryPageSaysHowManyEntriesItCouldNotRead(t *testing.T) {
	diagnostics := &strings.Builder{}
	m := NewModelWithSource(&fillSource{
		pages: []HistoryPage{{
			Messages:   []Message{{ID: 1, Text: "readable"}},
			HasMore:    false,
			Unreadable: 3,
		}},
	})
	m.diagnostics = diagnostics
	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	m, _ = updateModel(t, m, chatsLoadedMsg{chats: []Chat{{ID: 7, Title: "A"}}})
	m, cmd := updateModel(t, m, press(tea.KeyEnter))
	m = runLoads(t, m, cmd)

	view := m.View()
	if strings.Contains(plain(view), "3") && strings.Contains(view, "unreadable") {
		t.Fatalf("the count of unreadable entries is on the screen:\n%s", view)
	}
	if !strings.Contains(diagnostics.String(), "3 entries") {
		t.Fatalf(
			"the log says %q, want the number of the entries that were left out",
			diagnostics.String(),
		)
	}
}

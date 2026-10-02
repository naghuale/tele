package application

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"telecli/internal/outbox"
	"telecli/internal/telegram"
	"telecli/internal/tui"
	"telecli/internal/tui/theme"
)

// The program path, driven the way `telecli tui` drives it.
//
// Everything below the terminal is the real thing: a real durable queue, the
// real dispatcher and the real send-result reconciler in their own
// goroutines, the real delivery sources the composition root hands the
// interface, and the real model with the real update loop. Only TDLib is
// replaced, by a sender whose answer the test holds so the confirmation can
// be put at a chosen moment, and by a recorded history.
//
// What is NOT done here is calling the handlers. The owner reported a
// screen that stayed on `Queued` while the log said `sent`, and every test
// of this chain so far had called a handler directly — which proves the
// handler and says nothing about whether anything ever called it. So the
// model is pumped here: commands are executed and their messages fed back
// until it is quiet, and the only messages from outside are the ones a
// user would type.

// programRuntime is the whole delivery stack over one temporary queue: the
// real durable runtime, which owns the store, the dispatcher and the
// reconciler, and the sources it hands the interface.
type programRuntime struct {
	runtime *DurableOutboxRuntime
	live    *telegram.LiveState
	sender  *heldSender
	data    string
	ids     *programIDs
}

// entry is one record of the queue, which is what the owner reads in the
// log: a state and an identifier.
func (r *programRuntime) entry(id string) (outbox.Entry, bool) {
	entries, err := r.runtime.opened.Store.ListAll(context.Background())
	if err != nil {
		return outbox.Entry{}, false
	}
	for _, entry := range entries {
		if string(entry.ID) == id {
			return entry, true
		}
	}
	return outbox.Entry{}, false
}

func newProgramRuntime(t *testing.T) *programRuntime {
	t.Helper()

	dataDir := filepath.Join(t.TempDir(), "outbox")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatalf("create data dir: %v", err)
	}
	if err := os.Chmod(dataDir, 0o700); err != nil {
		t.Fatalf("chmod data dir: %v", err)
	}

	live := telegram.NewLiveState()
	sender := &heldSender{}
	ids := &programIDs{}

	runtime, err := OpenDurableOutboxRuntime(
		context.Background(),
		DurableOutboxRuntimeConfig{
			Outbox: outbox.Config{
				DataDir:    dataDir,
				DatabaseID: "program-path",
				InstanceID: "program-path",
				Dispatcher: outbox.DefaultDispatcherConfig("program-path"),
			},
		},
		DurableOutboxRuntimeDeps{
			// The test key provider, once: the runtime creates the
			// database, so a second provider would not have its key.
			KeyProvider:   newH7dKeyProvider(),
			Session:       sender,
			IDGenerator:   ids.next,
			AccountKey:    programAccountKey,
			MessageEvents: live,
		},
	)
	if err != nil {
		t.Fatalf("open the durable runtime: %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })

	return &programRuntime{
		runtime: runtime,
		live:    live,
		sender:  sender,
		data:    dataDir,
		ids:     ids,
	}
}

const programAccountKey = "program-path-account"

// programChats is a chat list of two conversations.
type programChats struct {
	mu sync.Mutex

	// history is what each chat holds on the server, by chat id. It is
	// what the owner's account has: the message is on Telegram even
	// though the queue has stopped listing the record.
	history map[int64][]tui.Message
}

func (c *programChats) ListChats(
	context.Context,
) ([]tui.Chat, error) {
	return []tui.Chat{
		{ID: 7, Title: "Избранное"},
		{ID: 8, Title: "Другой чат"},
	}, nil
}

func (c *programChats) LoadHistory(
	_ context.Context,
	chatID int64,
	_ int64,
	_ int,
) (tui.HistoryPage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	held := c.history[chatID]

	// Newest first, the way TDLib answers.
	answer := make([]tui.Message, 0, len(held))
	for index := len(held) - 1; index >= 0; index-- {
		answer = append(answer, held[index])
	}

	return tui.HistoryPage{
		Messages: answer,
		NextFrom: programBoundary(answer),
		HasMore:  false,
	}, nil
}

// SendMessage is never reached: the composer is handed the queue's own
// submitter, which is what the durable path does.
func (c *programChats) SendMessage(
	context.Context, int64, string,
) (tui.Message, error) {
	return tui.Message{}, nil
}

// addOnServer records a message as being in a chat, which is what Telegram
// has of it from the moment it is confirmed.
func (c *programChats) addOnServer(chatID int64, message tui.Message) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, held := range c.history[chatID] {
		if held.ID == message.ID {
			return
		}
	}
	c.history[chatID] = append(c.history[chatID], message)
}

func programBoundary(page []tui.Message) int64 {
	if len(page) == 0 {
		return 0
	}
	return page[len(page)-1].ID
}

// programModel is the interface's model, driven outside a terminal.
//
// The inbox is the part that matters. A command's answer belongs to the
// program whenever it arrives, not to the window that happened to be open
// when the command was started: the delivery tick is two seconds out, and
// a harness that dropped what arrived after a short wait would never see a
// poll happen at all — and would then report that the screen does not
// follow the queue, which is the defect this file exists to catch.
type programModel struct {
	model tui.Model
	inbox chan tea.Msg
}

func newProgramModel(model tui.Model) *programModel {
	return &programModel{model: model, inbox: make(chan tea.Msg, 256)}
}

// send types a message and presses Enter, exactly as a user does.
func (p *programModel) send(t *testing.T, text string) {
	t.Helper()

	for _, letter := range text {
		p.feed(t, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{letter}})
	}
	p.feed(t, tea.KeyMsg{Type: tea.KeyEnter})
}

// feed gives the model one key and settles whatever it answers with.
func (p *programModel) feed(t *testing.T, key tea.KeyMsg) {
	t.Helper()

	p.settle(t, quickWindow, key)
}

// quickWindow is long enough for a store read and nothing else.
const quickWindow = 400 * time.Millisecond

// settle feeds the model the given messages and everything they answer
// with, giving the commands a short window each.
//
// It is bounded on purpose. The delivery poll arms a tick for two seconds
// later, so a loop that waited for the inbox to go quiet would follow the
// poll for ever: there is always another tick coming. A key is settled by
// answering what it asked for, and the poll is left running in the
// background for whoever is waiting on it.
func (p *programModel) settle(
	t *testing.T,
	window time.Duration,
	msgs ...tea.Msg,
) {
	t.Helper()

	for _, msg := range msgs {
		p.feedOne(t, msg)
	}

	for round := 0; round < maxSettleRounds; round++ {
		next := p.take(window)
		if len(next) == 0 {
			return
		}
		for _, msg := range next {
			p.feedOne(t, msg)
		}
	}
}

// maxSettleRounds bounds one settle, so a model that answers with an
// endless stream of reads fails rather than hangs.
const maxSettleRounds = 8

// take waits up to the window for what the commands have answered, and
// takes everything waiting.
func (p *programModel) take(window time.Duration) []tea.Msg {
	if len(p.inbox) == 0 {
		select {
		case msg := <-p.inbox:
			return []tea.Msg{msg}
		case <-time.After(window):
			return nil
		}
	}

	var taken []tea.Msg
	for {
		select {
		case msg := <-p.inbox:
			taken = append(taken, msg)
			continue
		default:
		}
		break
	}

	return taken
}

// drain is settle without the wait: it takes what has already arrived and
// answers it, and returns.
//
// It is how a test waits for something the poll does: the tick fires two
// seconds after it was armed, so the test sleeps, drains, and looks again
// rather than holding the harness open for it.
func (p *programModel) drain(t *testing.T) {
	t.Helper()

	for round := 0; round < maxSettleRounds; round++ {
		next := p.take(0)
		if len(next) == 0 {
			return
		}
		for _, msg := range next {
			p.feedOne(t, msg)
		}
	}
}

// feedOne gives the model one message and runs what it answers with.
func (p *programModel) feedOne(t *testing.T, msg tea.Msg) {
	t.Helper()

	updated, cmd := p.model.Update(msg)
	model, ok := updated.(tui.Model)
	if !ok {
		t.Fatalf("Update returned %T, want tui.Model", updated)
	}
	p.model = model
	programCommands(t, p, cmd)
}

// awaitScreen waits for the screen to say something, with no key pressed in
// between, and reports what the queue said if it never does.
func (p *programModel) awaitScreen(
	t *testing.T,
	path *programRuntime,
	want string,
	within time.Duration,
) {
	t.Helper()

	deadline := time.Now().Add(within)
	for {
		p.drain(t)
		if strings.Contains(p.screen(), want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf(
				"the screen does not say %q within %s, with no key "+
					"pressed:\n%s\n\nqueue:\n%s",
				want, within, p.screen(), describeProgramQueue(t, path),
			)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// programCommands runs the commands a message produced and files what they
// answer with in the inbox.
func programCommands(
	t *testing.T,
	program *programModel,
	cmd tea.Cmd,
) {
	t.Helper()

	if cmd == nil {
		return
	}

	var spawn func(tea.Cmd)
	spawn = func(inner tea.Cmd) {
		if inner == nil {
			return
		}
		go func() {
			answer := inner()
			if answer == nil {
				return
			}
			if batch, isBatch := answer.(tea.BatchMsg); isBatch {
				for _, one := range batch {
					spawn(one)
				}
				return
			}
			select {
			case program.inbox <- answer:
			default:
			}
		}()
	}
	spawn(cmd)
}

// programRuntime is the whole delivery stack over one temporary queue: the
// real durable runtime, which owns the store, the dispatcher and the
// reconciler, and the sources it hands the interface.
func (p *programModel) screen() string { return p.model.View() }

// The interface follows a message to Sent without a key being pressed, and
// keeps it when the user looks at another chat and comes back.
// programAt is a moment of the fixed day the fixtures of this file are on,
// in the fixed zone, because a screen that says when a message was sent
// reads that moment in the zone of its model and this model reads one zone.
func programAt(clock string) time.Time {
	hour, minute := 0, 0
	for index, letter := range clock {
		if letter == ':' {
			continue
		}
		if index < 2 {
			hour = hour*10 + int(letter-'0')
			continue
		}
		minute = minute*10 + int(letter-'0')
	}

	return time.Date(2026, 3, 14, hour, minute, 0, 0, time.UTC)
}

func TestTheInterfaceFollowsAMessageToSentAndKeepsItAcrossAChatSwitch(
	t *testing.T,
) {
	const (
		chatID    = int64(7)
		temporary = int64(-1000000042)
		finalID   = int64(437583347712)
		text      = "проверяю сборку"
	)

	path := newProgramRuntime(t)

	chats := &programChats{history: map[int64][]tui.Message{
		chatID: {{ID: 1, Author: "Anna", Text: "привет", At: programAt("12:00")}},
		8:      {{ID: 2, Author: "Boris", Text: "пока", At: programAt("12:01")}},
	}}

	// The adapter the composition root builds between the queue's
	// submitter and the composer's: the interface speaks the queue's
	// submission and the queue speaks in chat-first arguments.
	submitter, err := NewTUISubmitter(
		path.runtime.Submitter(), programAccountKey,
	)
	if err != nil {
		t.Fatalf("build the submitter the app builds: %v", err)
	}

	model, err := tui.NewModelWithDependencies(
		context.Background(),
		tui.Dependencies{
			Source:           chats,
			MessageSubmitter: submitter,
			AccountKey:       programAccountKey,
			MessageStatuses:  newTUIMessageStatusSourceAdapter(path.runtime.StatusSource()),
			PendingMessages:  newTUIPendingMessageSourceAdapter(path.runtime.PendingMessages()),
			Theme:            theme.DefaultTheme().ForProfile(theme.ProfileNoColor),
			ColorProfile:     theme.ProfileNoColor,
		},
	)
	if err != nil {
		t.Fatalf("build the model: %v", err)
	}

	program := newProgramModel(model)
	// Init is where the program starts: the chat list load and the first
	// poll are asked for there, and a test that skips it is testing a
	// program that never started.
	program.settle(t, quickWindow, tea.WindowSizeMsg{Width: 100, Height: 24})
	programCommands(t, program, model.Init())
	program.settle(t, quickWindow)

	// Open the first conversation and let its history land.
	program.feed(t, tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(program.screen(), "привет") {
		t.Fatalf(
			"the conversation did not open on its history:\n%s",
			program.screen(),
		)
	}

	// The message goes out. The sender is held so the confirmation can be
	// placed three hundred milliseconds later, which is the gap the owner
	// reported.
	entered, release := path.sender.hold(temporary)
	program.send(t, text)

	<-entered
	close(release)

	// Accepted first: TDLib has the message, Telegram has not confirmed.
	waitForProgramState(t, path, "e0", outbox.StateAccepted, 5*time.Second)
	program.settle(t, quickWindow)
	if screen := program.screen(); !strings.Contains(screen, "◐ sending") &&
		!strings.Contains(screen, "● queued") {
		t.Fatalf(
			"the message is on its way and the screen does not say so:\n%s",
			screen,
		)
	}

	time.Sleep(300 * time.Millisecond)

	// And now Telegram confirms it. No key is pressed between here and the
	// screen being read.
	if _, err := path.live.ApplyUpdate(telegram.RawMessage(
		recordedConfirmation(finalID, temporary, chatID, text),
	)); err != nil {
		t.Fatalf("deliver the confirmation: %v", err)
	}
	waitForProgramState(t, path, "e0", outbox.StateSent, 5*time.Second)

	// The screen follows the queue, and the only thing that moved the queue
	// was the reconciler. Nothing here presses a key: the wait is the poll,
	// which is the two seconds the program waits between reads and the
	// thing that was never armed.
	program.awaitScreen(t, path, "✓ sent", 10*time.Second)

	// Telegram holds the message, which is what a later history page will
	// bring back.
	chats.addOnServer(chatID, tui.Message{
		ID: finalID, Outgoing: true, Text: text, At: programAt("12:05"),
	})

	// The user looks at another chat and comes back.
	program.feed(t, tea.KeyMsg{Type: tea.KeyEsc})
	program.settle(t, quickWindow)
	program.feed(t, tea.KeyMsg{Type: tea.KeyDown})
	program.feed(t, tea.KeyMsg{Type: tea.KeyEnter})
	program.settle(t, quickWindow)
	program.feed(t, tea.KeyMsg{Type: tea.KeyEsc})
	program.settle(t, quickWindow)
	program.feed(t, tea.KeyMsg{Type: tea.KeyUp})
	program.feed(t, tea.KeyMsg{Type: tea.KeyEnter})
	program.settle(t, quickWindow)

	screen := program.screen()
	if strings.Count(screen, text) != 1 {
		t.Fatalf(
			"the message is in the feed %d times after a chat switch, "+
				"want once:\n%s", strings.Count(screen, text), screen,
		)
	}
	ids := program.model.ConversationMessageIDs()
	found := 0
	for _, id := range ids {
		if id == finalID {
			found++
		}
	}
	if found != 1 {
		t.Fatalf(
			"the message is in the conversation under its final id %d "+
				"%d times, want once; the conversation holds %v:\n%s",
			finalID, found, ids, screen,
		)
	}
}

// waitForProgramState waits for a record to reach a state.
func waitForProgramState(
	t *testing.T,
	path *programRuntime,
	id string,
	want outbox.State,
	within time.Duration,
) {
	t.Helper()

	deadline := time.Now().Add(within)
	for {
		entry, found := path.entry(id)
		if found && entry.State == want {
			return
		}
		if time.Now().After(deadline) {
			got := "absent"
			if found {
				got = string(entry.State)
			}
			t.Fatalf("record %s is %s, want %s", id, got, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// describeProgramQueue is what the owner reads in the log, for a failure
// message: numbers and states, and the text of the record.
func describeProgramQueue(t *testing.T, path *programRuntime) string {
	t.Helper()

	entry, found := path.entry("e0")
	if !found {
		return "the record is not in the queue"
	}
	return fmt.Sprintf(
		"state=%s message_id=%d", entry.State, entry.TelegramMessageID,
	)
}

// recordedConfirmation is what TDLib sends when a message is on its way:
// the message with its final identifier, and the temporary one it replaces.
func recordedConfirmation(
	final, temporary, chat int64,
	text string,
) string {
	return fmt.Sprintf(`{
  "@type": "updateMessageSendSucceeded",
  "message": {
    "@type": "message",
    "id": %d,
    "sender_id": {"@type": "messageSenderUser", "user_id": 7},
    "chat_id": %d,
    "is_outgoing": true,
    "date": %d,
    "sending_state": {"@type": "messageSendingStateSent"},
    "content": {
      "@type": "messageText",
      "text": {"@type": "formattedText", "text": %q, "entities": []}
    }
  },
  "old_message_id": %d
}`, final, chat, 1789500300, text, temporary)
}

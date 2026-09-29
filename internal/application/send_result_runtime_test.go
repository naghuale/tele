package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"telecli/internal/outbox"
	"telecli/internal/telegram"
)

// The send path, end to end, over the wiring the program actually uses.
//
// The reconciler is not driven here on its own. A reconciler handed a
// window of events it asked for proves the reconciler, and it is exactly
// how a defect in the chain around it survives a green suite: the
// hand-built window answers the question the reconciler asks, so it can
// never show that the real window does not answer it.
//
// So this file opens a real durable queue, runs the real dispatcher in
// its own goroutine, runs the real reconciler in its own goroutine, and
// feeds a real LiveState with recorded TDLib payloads through the same
// entry point the session pump uses. The only thing standing in for
// Telegram is the sender, which is held on the wire so that the test can
// say exactly when the confirmation lands relative to the moment the
// queue records the acceptance — the three orderings that a real client
// produces and no hand-built window can produce at all.

// recordedSendAnswer is the message object TDLib answers sendMessage
// with, carrying the temporary identifier and a pending sending state.
//
// The identifier is the temporary one. TDLib hands out temporary
// identifiers that no history page will ever contain, which is why the
// confirmation can only ever be matched on the one that comes back as
// old_message_id.
const recordedSendAnswerTemplate = `{
  "@type": "message",
  "id": %d,
  "sender_id": {"@type": "messageSenderUser", "user_id": 7},
  "chat_id": %d,
  "is_outgoing": true,
  "date": 1759100000,
  "sending_state": {"@type": "messageSendingStatePending"},
  "content": {
    "@type": "messageText",
    "text": {"@type": "formattedText", "text": "проверяю сборку", "entities": []}
  }
}`

// recordedSendSucceededTemplate is the update Telegram sends when the
// message is on its way: the final message, and the temporary identifier
// it replaces.
const recordedSendSucceededTemplate = `{
  "@type": "updateMessageSendSucceeded",
  "message": {
    "@type": "message",
    "id": %d,
    "sender_id": {"@type": "messageSenderUser", "user_id": 7},
    "chat_id": %d,
    "is_outgoing": true,
    "date": 1759100000,
    "sending_state": {"@type": "messageSendingStateSent"},
    "content": {
      "@type": "messageText",
      "text": {"@type": "formattedText", "text": "проверяю сборку", "entities": []}
    }
  },
  "old_message_id": %d
}`

// sendPath is a real durable runtime driven by a real dispatcher and a
// real reconciler, with Telegram replaced by a sender the test can hold.
type sendPath struct {
	t      *testing.T
	store  *outbox.Outbox
	live   *telegram.LiveState
	queue  *OutboxMessageSubmitter
	sender *heldSender
	data   string
	cancel context.CancelFunc
}

func newSendPath(t *testing.T) *sendPath {
	t.Helper()

	return newSendPathWithLogger(t, nil)
}

// newSendPathWithLogger is newSendPath with the logger the production
// wiring hands the reconciler.
//
// A nil logger is the same as newSendPath's: the reconciler then discards,
// which is the default for a component that was not given one. Passing a
// real one is what lets a test watch where the reasons go.
func newSendPathWithLogger(
	t *testing.T,
	logger *slog.Logger,
) *sendPath {
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

	opened, err := outbox.Open(
		context.Background(),
		outbox.Config{
			DataDir:    dataDir,
			DatabaseID: "send-path",
			InstanceID: "send-path",
			Dispatcher: outbox.DefaultDispatcherConfig("send-path"),
		},
		outbox.Deps{
			// The platform key provider is never reached: this suite
			// must not read or write a Keychain item, and the data
			// folder is under t.TempDir() so it can never be the queue
			// on the machine running it.
			KeyProvider: newH7dKeyProvider(),
			Sender:      NewTelegramOutboxSender(sender),
			Clock:       outbox.SystemClock{},
		},
	)
	if err != nil {
		t.Fatalf("open outbox: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	// The real dispatcher, in its own goroutine, as the runtime starts it.
	dispatcherDone := make(chan struct{})
	go func() {
		defer close(dispatcherDone)
		_ = opened.Dispatcher.Run(ctx)
	}()

	var (
		idMu sync.Mutex
		idN  int
	)
	queue, err := NewOutboxMessageSubmitter(
		opened.Store,
		outbox.SystemClock{},
		func() (outbox.ID, error) {
			idMu.Lock()
			defer idMu.Unlock()
			id := fmt.Sprintf("e%d", idN)
			idN++
			return outbox.ID(id), nil
		},
		opened.Dispatcher,
		sendPathAccountKey,
	)
	if err != nil {
		t.Fatalf("submitter: %v", err)
	}

	path := &sendPath{
		t:      t,
		store:  opened,
		live:   live,
		queue:  queue,
		sender: sender,
		data:   dataDir,
		cancel: cancel,
	}

	// the real reconciler, in its own goroutine, as the runtime starts it
	results, ok := opened.Store.(outbox.SendResultStore)
	if !ok {
		t.Fatal("the durable store does not support send results")
	}
	reconciler := &sendResultReconciler{
		store:      results,
		events:     live,
		accountKey: sendPathAccountKey,
		clock:      outbox.SystemClock{},
		logger:     logger,
		counters:   &sendResultCounters{},
		sink:       newSendResultCounterFile(dataDir),
	}
	reconcilerDone := make(chan struct{})
	go func() {
		defer close(reconcilerDone)
		_ = reconciler.Run(ctx)
	}()

	// The teardown is one cleanup, registered last so it runs first: the
	// goroutines are stopped and the queue closed before t.TempDir()
	// removes the folder underneath them. A test that leaves a goroutine
	// writing into a directory that is being deleted fails on the run
	// where the two happen to overlap, and not before.
	t.Cleanup(func() {
		cancel()
		<-reconcilerDone
		<-dispatcherDone
		_ = opened.Close()
	})

	return path
}

const sendPathAccountKey = "send-path-account"

// heldSender answers sendMessage with a temporary identifier and lets
// the test hold the answer.
//
// Holding the answer is the whole point: it is what puts the entry in
// dispatching while the test delivers the confirmation, which is the
// ordering a real client produces whenever Telegram is quick.
type heldSender struct {
	mu      sync.Mutex
	tempID  int64
	entered chan struct{}
	release chan struct{}
}

// hold arms the sender for one send and returns the channel that is
// closed when the request is in flight and the one that releases it.
func (s *heldSender) hold(temporary int64) (chan struct{}, chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.tempID = temporary
	s.entered = make(chan struct{})
	s.release = make(chan struct{})
	return s.entered, s.release
}

func (s *heldSender) SendTextMessage(
	ctx context.Context,
	chatID telegram.ChatID,
	_ string,
) (telegram.Message, error) {
	s.mu.Lock()
	entered, release := s.entered, s.release
	temporary := s.tempID
	s.mu.Unlock()

	if entered == nil {
		return telegram.Message{}, errors.New("held sender: not armed")
	}
	close(entered)

	select {
	case <-release:
	case <-ctx.Done():
		return telegram.Message{}, ctx.Err()
	}

	// The answer TDLib gives: the temporary identifier, and a sending
	// state that is still pending. This is the object the dispatcher
	// records the acceptance from.
	var answer struct {
		Type     string `json:"@type"`
		ID       int64  `json:"id"`
		ChatID   int64  `json:"chat_id"`
		Outgoing bool   `json:"is_outgoing"`
	}
	payload := fmt.Sprintf(
		recordedSendAnswerTemplate, temporary, int64(chatID),
	)
	if err := jsonUnmarshal([]byte(payload), &answer); err != nil {
		return telegram.Message{}, err
	}

	return telegram.Message{
		ID:        telegram.MessageID(answer.ID),
		ChatID:    chatID,
		Outgoing:  answer.Outgoing,
		Timestamp: time.Unix(1759100000, 0).UTC(),
		Text:      "проверяю сборку",
	}, nil
}

// sendPathEntry is the state of one queued record.
func (p *sendPath) entry(id string) (outbox.Entry, bool) {
	p.t.Helper()

	entries, err := p.store.Store.(interface {
		ListAll(context.Context) ([]outbox.Entry, error)
	}).ListAll(context.Background())
	if err != nil {
		p.t.Fatalf("list entries: %v", err)
	}
	for _, entry := range entries {
		if string(entry.ID) == id {
			return entry, true
		}
	}
	return outbox.Entry{}, false
}

// waitForState waits for a record to reach one state, and says which
// state it is in when it does not.
func (p *sendPath) waitForState(
	id string,
	want outbox.State,
	within time.Duration,
) (outbox.Entry, error) {
	p.t.Helper()

	deadline := time.Now().Add(within)
	for {
		entry, found := p.entry(id)
		if found && entry.State == want {
			return entry, nil
		}
		if time.Now().After(deadline) {
			if !found {
				return outbox.Entry{}, fmt.Errorf(
					"entry %s never appeared", id,
				)
			}
			return entry, fmt.Errorf(
				"entry %s is %s with message id %d, want %s",
				id, entry.State, entry.TelegramMessageID, want,
			)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// deliverConfirmation feeds one recorded updateMessageSendSucceeded into
// the real live store, through the entry point the session pump uses.
func (p *sendPath) deliverConfirmation(
	final, temporary, chat int64,
) {
	p.t.Helper()

	raw := fmt.Sprintf(
		recordedSendSucceededTemplate, final, chat, temporary,
	)
	if _, err := p.live.ApplyUpdate(telegram.RawMessage(raw)); err != nil {
		p.t.Fatalf("deliver confirmation: %v", err)
	}
}

// The confirmation arrives before the queue records the acceptance.
//
// This is the ordering the earlier suite could not produce, because a
// hand-built window answers whenever it is asked and so cannot be made
// to arrive early. Here the entry is held in dispatching by the sender,
// the confirmation is delivered into the real window, and only then is
// the answer released.
func TestTheConfirmationThatArrivesBeforeTheQueueAcceptsIsStillMatched(
	t *testing.T,
) {
	const (
		chat      = int64(7)
		temporary = int64(-1000000042)
		final     = int64(501)
	)

	path := newSendPath(t)

	entered, release := path.sender.hold(temporary)
	if _, err := path.queue.QueueMessage(
		context.Background(), chat, "проверяю сборку",
	); err != nil {
		t.Fatalf("queue message: %v", err)
	}

	<-entered

	if _, found := path.entry("e0"); !found {
		t.Fatal("the record is not in the queue")
	}
	// The record is held in flight, so no acceptance has been recorded
	// and there is no temporary identifier in the queue to match on.
	if entry, _ := path.entry("e0"); entry.TelegramMessageID != 0 {
		t.Fatalf(
			"the queue already holds message id %d: the ordering "+
				"this test is about did not happen",
			entry.TelegramMessageID,
		)
	}

	// Telegram confirms while the request is still in flight.
	path.deliverConfirmation(final, temporary, chat)
	close(release)

	entry, err := path.waitForState(
		"e0", outbox.StateSent, 10*time.Second,
	)
	if err != nil {
		t.Fatalf("early confirmation was lost: %v", err)
	}
	if entry.TelegramMessageID != final {
		t.Fatalf(
			"entry holds message id %d, want the final id %d",
			entry.TelegramMessageID, final,
		)
	}
	if entry.SentAt.IsZero() {
		t.Fatal("the record is sent without a sent time")
	}
}

// The confirmation arrives while the queue is recording the acceptance.
//
// The two writes are genuinely concurrent here, so this is run a number
// of times: the point is that neither order loses the confirmation, and a
// chain that only works when the confirmation happens to be late is a
// chain that will lose it in the field.
func TestTheConfirmationThatRacesTheAcceptanceIsStillMatched(
	t *testing.T,
) {
	const rounds = 12

	for round := 0; round < rounds; round++ {
		t.Run(fmt.Sprintf("round %d", round), func(t *testing.T) {
			const chat = int64(11)
			temporary := int64(-2000000000 - round)
			final := int64(700 + round)

			path := newSendPath(t)

			entered, release := path.sender.hold(temporary)
			if _, err := path.queue.QueueMessage(
				context.Background(), chat, "проверяю сборку",
			); err != nil {
				t.Fatalf("queue message: %v", err)
			}
			<-entered

			// Release the answer and deliver the confirmation from
			// another goroutine at the same moment, so the two race.
			delivered := make(chan struct{})
			go func() {
				defer close(delivered)
				path.deliverConfirmation(final, temporary, chat)
			}()
			close(release)
			<-delivered

			entry, err := path.waitForState(
				"e0", outbox.StateSent, 10*time.Second,
			)
			if err != nil {
				t.Fatalf("raced confirmation was lost: %v", err)
			}
			if entry.TelegramMessageID != final {
				t.Fatalf(
					"entry holds message id %d, want %d",
					entry.TelegramMessageID, final,
				)
			}
		})
	}
}

// Several messages in a row, each confirmed before its own acceptance.
//
// One message working and the next one not is what a user reports as
// "sometimes it says sent", and a single-message test cannot see it: the
// window is shared, so the second message is matched against a window the
// first one already wrote to.
func TestSeveralMessagesInARowAreAllConfirmed(
	t *testing.T,
) {
	const (
		chat  = int64(13)
		count = 6
	)

	path := newSendPath(t)

	for i := 0; i < count; i++ {
		temporary := int64(-3000000000 - i)
		final := int64(900 + i)
		id := fmt.Sprintf("e%d", i)

		entered, release := path.sender.hold(temporary)
		if _, err := path.queue.QueueMessage(
			context.Background(), chat, "проверяю сборку",
		); err != nil {
			t.Fatalf("queue message %d: %v", i, err)
		}
		<-entered

		path.deliverConfirmation(final, temporary, chat)
		close(release)

		if _, err := path.waitForState(
			id, outbox.StateSent, 10*time.Second,
		); err != nil {
			t.Fatalf("message %d was lost: %v", i, err)
		}
	}

	for i := 0; i < count; i++ {
		entry, found := path.entry(fmt.Sprintf("e%d", i))
		if !found {
			t.Fatalf("message %d vanished from the queue", i)
		}
		if entry.State != outbox.StateSent {
			t.Fatalf("message %d is %s", i, entry.State)
		}
		if entry.TelegramMessageID != int64(900+i) {
			t.Fatalf(
				"message %d holds id %d, want %d",
				i, entry.TelegramMessageID, 900+i,
			)
		}
	}
}

// The counters the owner reads are the counters the reconciler keeps.
//
// Without this, the line doctor prints is a number nobody can believe:
// the reconciler could be counting its own passes and the test would
// still be green.
func TestTheReconcilerCountsWhatItSawAndWhatItMatched(
	t *testing.T,
) {
	const (
		chat      = int64(17)
		temporary = int64(-4000000042)
		final     = int64(1201)
	)

	path := newSendPath(t)

	entered, release := path.sender.hold(temporary)
	if _, err := path.queue.QueueMessage(
		context.Background(), chat, "проверяю сборку",
	); err != nil {
		t.Fatalf("queue message: %v", err)
	}
	<-entered
	path.deliverConfirmation(final, temporary, chat)
	close(release)

	if _, err := path.waitForState(
		"e0", outbox.StateSent, 10*time.Second,
	); err != nil {
		t.Fatalf("confirmation was lost: %v", err)
	}

	// The file is written on the way out, which the cleanup does. Read
	// what the reconciler recorded for this run.
	path.cancel()
	waitForFile(t, filepath.Join(path.data, sendResultCounterFileName))

	report, ok := readSendResultCounters(path.data)
	if !ok {
		t.Fatal("the run recorded no counters for doctor to read")
	}
	if report.Seen < 1 {
		t.Fatalf("seen = %d: the confirmation was not counted", report.Seen)
	}
	if report.Matched < 1 {
		t.Fatalf("matched = %d: the match was not counted", report.Matched)
	}
	if report.NoEntry != 0 {
		t.Fatalf(
			"noEntry = %d: a matched confirmation was also counted as "+
				"naming no record", report.NoEntry,
		)
	}
	if report.WindowGap != 0 {
		t.Fatalf(
			"windowGap = %d: the window answered every pass",
			report.WindowGap,
		)
	}
}

// waitForFile waits for the counters to be written.
//
// The reconciler writes on the way out and the throttle lets a forced
// write through, so this is a short bound rather than a fixed sleep.
func waitForFile(t *testing.T, path string) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the counters were never written to %s", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// jsonUnmarshal is json.Unmarshal, named so the recorded payloads above
// read as fixtures rather than as parsing code.
func jsonUnmarshal(data []byte, into any) error {
	return json.Unmarshal(data, into)
}

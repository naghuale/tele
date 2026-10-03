package application

import (
	"context"
	"sync"
	"testing"
	"time"

	"telecli/internal/telegram"
	"telecli/internal/tui"
)

// This file is the name of whoever sent a message asked in the background
// rather than in the loop of the interface.
//
// The defect it proves against is the loop asking TDLib for a name and
// standing there until TDLib answered: the screen neither drew nor answered
// a key while one name was on its way (FINDING-T1, auditor, main fdadd25).
// So the source of names below answers in seconds and in silence, and every
// test here is about what the interface does while that is happening.
//
// Nothing here is an account of anybody: the names are the ones this file
// writes, and they are only ever answers of a stub reader.

// liveNameAnswer is how long the name source of these tests takes to answer.
//
// It is the shape of the check the task asks for — a source that answers in
// seconds rather than at once — at a size a gate can afford. The manual
// check of the same thing is five seconds, and nothing in the program knows
// the difference: the read is asked in the background and is waited for by
// nobody.
const liveNameAnswer = 900 * time.Millisecond

// liveLoopBudget is how long the read of the events of a chat may take
// before it is a read that waited for something.
//
// The work is a copy of a small map and a goroutine to start; a quarter of a
// second is a thousand times that, and a tenth of liveNameAnswer, which is
// what makes the two answers of this file differ.
const liveLoopBudget = 250 * time.Millisecond

// liveNameWait is how long a test waits for a name to arrive or for the
// loop to be woken by one: generous next to the tenth of a second the
// program takes to draw, and short enough that a test waiting for something
// that never happens fails instead of hanging.
const liveNameWait = 5 * time.Second

// The loop of the interface must not wait for TDLib, and the name of a
// sender is TDLib.
//
// The message below is signed with a placeholder while its sender's name is
// being read, and the read of the events returns at once: a chat that waits
// for a name is a chat that has stopped drawing and stopped answering keys.
func TestTheInterfaceDoesNotWaitForTheNameOfASender(t *testing.T) {
	store := recordedAccount(t)
	names := &slowNameReader{after: liveNameAnswer, name: "Marta Ivanova"}

	live, err := NewTelegramLiveUpdates(t.Context(), store, names)
	if err != nil {
		t.Fatalf("NewTelegramLiveUpdates: %v", err)
	}
	applyRecorded(t, store, recordedNewMessage(2, 2002, 1700000800, "the tag is pushed"))

	started := time.Now()
	events := live.MessageEvents(2, 0)
	waited := time.Since(started)

	if waited > liveLoopBudget {
		t.Fatalf(
			"reading the events of a chat took %s, want under %s: the loop of "+
				"the interface waited for a name",
			waited, liveLoopBudget,
		)
	}
	if len(events.Events) != 1 {
		t.Fatalf("the chat produced %d events, want 1", len(events.Events))
	}
	if got := events.Events[0].Message.Author; got != unknownAuthor {
		t.Fatalf(
			"author = %q while the name of the sender is on its way, want the "+
				"placeholder %q", got, unknownAuthor,
		)
	}

	// The name that arrives is given to the interface rather than waited
	// for, so the placeholder above this message is not what stays there.
	named := readUntilNamed(t, live, 2, events.Cursor)
	if named.Message.Text != "the tag is pushed" {
		t.Fatalf("text = %q, want the message that arrived", named.Message.Text)
	}
	if named.Message.Author != "Marta Ivanova" {
		t.Fatalf(
			"author = %q once the name arrived, want it above the message",
			named.Message.Author,
		)
	}
}

// The answer is one more event of that chat rather than a question the
// interface has to ask again: the same row, drawn with the name above it.
//
// It is the event of a replaced message because that is what it is — the
// message TDLib has now told the adapter everything about. The interface
// puts it where the message stands, so a name that arrives neither moves the
// message nor counts as a new one, and a name that is delivered once is not
// delivered twice.
func TestTheNameOfASenderArrivesAsAnotherEventOfItsChat(t *testing.T) {
	store := recordedAccount(t)
	names := &slowNameReader{after: liveNameAnswer, name: "Marta Ivanova"}

	live, err := NewTelegramLiveUpdates(t.Context(), store, names)
	if err != nil {
		t.Fatalf("NewTelegramLiveUpdates: %v", err)
	}
	applyRecorded(t, store, recordedNewMessage(2, 2002, 1700000800, "the tag is pushed"))

	events := live.MessageEvents(2, 0)
	named := readUntilNamed(t, live, 2, events.Cursor)

	if named.Kind != tui.LiveMessageReplaced {
		t.Fatalf(
			"the name arrived as a %v, want the message drawn again with it",
			named.Kind,
		)
	}
	if named.OldID != 2002 {
		t.Fatalf(
			"the event names message %d as the one to draw again, want 2002",
			named.OldID,
		)
	}

	// A name is delivered once. A read that found nothing new has nothing
	// to add, and the cursor it answered with is the one to ask with next.
	if again := live.MessageEvents(2, events.Cursor); len(again.Events) != 0 {
		t.Fatalf(
			"the same name was delivered again as %d events", len(again.Events),
		)
	}
}

// The store says nothing when a name arrives: no update, no change of the
// chat list, no new message. Without a signal of its own the placeholder
// above the message would stay until Telegram happened to send the next
// update of anything, which is not what a name that has arrived means.
func TestANameThatArrivedWakesTheLoopWithoutAnUpdate(t *testing.T) {
	store := recordedAccount(t)
	names := &slowNameReader{after: liveNameAnswer, name: "Marta Ivanova"}

	live, err := NewTelegramLiveUpdates(t.Context(), store, names)
	if err != nil {
		t.Fatalf("NewTelegramLiveUpdates: %v", err)
	}
	applyRecorded(t, store, recordedNewMessage(2, 2002, 1700000800, "the tag is pushed"))

	live.MessageEvents(2, 0)

	// Everything the store has said, and the name that came with it, is
	// taken first: what wakes the wait below can only be a name that
	// arrives after the wait was armed.
	drainChanged(store)
	drainNames(live)

	woken := make(chan struct{})
	go func() {
		live.WaitForChange(t.Context())
		close(woken)
	}()

	select {
	case <-woken:
	case <-time.After(liveNameWait):
		t.Fatal("nothing woke the loop: a name arrived and said so nowhere")
	}
}

// One read of one name answers for every message that sender sent, however
// many of them arrive while it is on its way — and a message of the same
// person that arrives afterwards is signed with the name that was read.
func TestTheNameOfASenderIsReadOnceHoweverManyMessagesItSigns(t *testing.T) {
	store := recordedAccount(t)
	names := &slowNameReader{after: liveNameAnswer, name: "Marta Ivanova"}

	live, err := NewTelegramLiveUpdates(t.Context(), store, names)
	if err != nil {
		t.Fatalf("NewTelegramLiveUpdates: %v", err)
	}
	for _, id := range []int{2001, 2002, 2003} {
		applyRecorded(t, store, recordedNewMessage(2, id, 1700000800, "one of three"))
	}

	events := live.MessageEvents(2, 0)
	if len(events.Events) != 3 {
		t.Fatalf("the chat produced %d events, want 3", len(events.Events))
	}

	readUntilNamed(t, live, 2, events.Cursor)
	if reads := names.reads(); reads != 1 {
		t.Fatalf(
			"the name was read %d times, want 1: three messages of one sender "+
				"are one question", reads,
		)
	}

	// A message that arrives after the name was read is signed with it at
	// once, and asks nothing: the name is in memory.
	applyRecorded(t, store, recordedNewMessage(2, 2004, 1700000800, "the fourth"))
	after := live.MessageEvents(2, events.Cursor)
	if len(after.Events) != 1 {
		t.Fatalf("the fourth message produced %d events, want 1", len(after.Events))
	}
	if got := after.Events[0].Message.Author; got != "Marta Ivanova" {
		t.Fatalf("author = %q, want the name that was already read", got)
	}
	if reads := names.reads(); reads != 1 {
		t.Fatalf("the name was read %d times, want 1", reads)
	}
}

// Leaving the program ends the reads still on their way.
//
// A read that nobody calls off outlives the program that asked it: the
// goroutine waits for a TDLib that is being closed, and a name that arrives
// after the program has left is a name nobody will ever see.
func TestLeavingTheProgramEndsTheReadOfAName(t *testing.T) {
	store := recordedAccount(t)
	names := &slowNameReader{after: liveNameAnswer, name: "Marta Ivanova"}

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	live, err := NewTelegramLiveUpdates(ctx, store, names)
	if err != nil {
		t.Fatalf("NewTelegramLiveUpdates: %v", err)
	}
	applyRecorded(t, store, recordedNewMessage(2, 2002, 1700000800, "the tag is pushed"))

	if elapsed := timeSinceLiveEvents(live, 2); elapsed > liveLoopBudget {
		t.Fatalf("reading the events of a chat took %s", elapsed)
	}

	waitFor(t, "the read of the name to be on its way", names.onItsWay())
	cancel()

	waitFor(t, "the read of the name to be called off", names.calledOff())
	waitFor(t, "the read of the name to be over", names.finished())
	if _, known := live.cache.lookup(userNameKey(77)); known {
		t.Fatal("a name that arrived after the program left was kept")
	}
}

// A message that is gone must not come back as the answer to a question
// about its sender: the placeholder of a deleted message is not a row the
// interface is waiting to draw again.
func TestANameDoesNotBringBackAMessageThatWasDeleted(t *testing.T) {
	store := recordedAccount(t)
	names := &slowNameReader{after: liveNameAnswer, name: "Marta Ivanova"}

	live, err := NewTelegramLiveUpdates(t.Context(), store, names)
	if err != nil {
		t.Fatalf("NewTelegramLiveUpdates: %v", err)
	}
	applyRecorded(t, store, recordedNewMessage(2, 2002, 1700000800, "the tag is pushed"))
	applyRecorded(t, store, `{"@type":"updateDeleteMessages","chat_id":2,`+
		`"message_ids":[2002],"is_permanent":true}`)

	events := live.MessageEvents(2, 0)
	deleted := false

	for _, event := range events.Events {
		if event.Kind == tui.LiveMessageDeleted {
			deleted = true
		}
	}
	if !deleted {
		t.Fatalf("the deletion produced %d events, want the deletion", len(events.Events))
	}

	// The name arrives afterwards, and there is nothing left to draw it on.
	waitFor(t, "the name to be read", names.finished())
	if again := live.MessageEvents(2, events.Cursor); len(again.Events) != 0 {
		t.Fatalf(
			"the name brought a deleted message back as %d events",
			len(again.Events),
		)
	}
}

// The memory of the messages waiting for a name is a memory and not a store:
// it is over its limit rather than grown, and what it held keeps its
// placeholder until its chat is read again.
func TestTheMemoryOfTheMessagesWaitingForANameIsBounded(t *testing.T) {
	live, err := NewTelegramLiveUpdates(t.Context(), telegram.NewLiveState(), nil)
	if err != nil {
		t.Fatalf("NewTelegramLiveUpdates: %v", err)
	}

	for id := range nameCacheLimit + 10 {
		message := telegram.Message{
			ID:     telegram.MessageID(2000 + id),
			Sender: telegram.MessageSender{Kind: telegram.MessageSenderUser, ID: int64(id)},
		}

		live.rememberUnnamed(2, int64(message.ID), userNameKey(message.Sender.ID), tui.Message{
			ID:       int64(message.ID),
			AuthorID: message.Sender.ID,
			Author:   unknownAuthor,
		})
	}

	if len(live.unnamed) > nameCacheLimit {
		t.Fatalf(
			"the memory of the messages waiting for a name holds %d, want at "+
				"most %d", len(live.unnamed), nameCacheLimit,
		)
	}
}

// ---- the source of names these tests drive ----

// slowNameReader answers with a name after a delay, or when the read is
// called off, and says which of the two happened.
//
// A reader that answers at once cannot show what the interface does while a
// name is on its way, which is the whole of this file.
type slowNameReader struct {
	after time.Duration
	name  string

	mu       sync.Mutex
	count    int
	itsWayAt chan struct{}
	offAt    chan struct{}
	over     chan struct{}
}

func (r *slowNameReader) GetUserName(ctx context.Context, _ int64) (string, error) {
	return r.read(ctx)
}

func (r *slowNameReader) GetChat(
	ctx context.Context,
	_ telegram.ChatID,
) (telegram.ChatSummary, error) {
	name, err := r.read(ctx)
	if err != nil {
		return telegram.ChatSummary{}, err
	}

	return telegram.ChatSummary{Title: name}, nil
}

// signals returns the three signals of this reader, made if no read has been
// asked of it yet.
func (r *slowNameReader) signals() (itsWay, calledOff, over chan struct{}) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.itsWayAt == nil {
		r.itsWayAt = make(chan struct{}, 1)
	}
	if r.offAt == nil {
		r.offAt = make(chan struct{}, 1)
	}
	if r.over == nil {
		r.over = make(chan struct{}, 1)
	}

	return r.itsWayAt, r.offAt, r.over
}

// read is the one read both questions of a session share.
//
// The three signals are sent and not closed, because a session may be asked
// more than once and a read that is called off is not the last read there
// will be.
func (r *slowNameReader) read(ctx context.Context) (string, error) {
	itsWay, calledOff, over := r.signals()

	r.mu.Lock()
	r.count++
	r.mu.Unlock()

	say(itsWay)

	var failure error

	select {
	case <-time.After(r.after):
	case <-ctx.Done():
		say(calledOff)
		failure = ctx.Err()
	}

	say(over)

	if failure != nil {
		return "", failure
	}

	return r.name, nil
}

// reads is how many questions this reader has been asked.
func (r *slowNameReader) reads() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.count
}

// onItsWay returns the signal that a read of a name has been started.
func (r *slowNameReader) onItsWay() <-chan struct{} {
	itsWay, _, _ := r.signals()

	return itsWay
}

// calledOff returns the signal that a read of a name was called off rather
// than answered.
func (r *slowNameReader) calledOff() <-chan struct{} {
	_, calledOff, _ := r.signals()

	return calledOff
}

// finished returns the signal that a read of a name is over.
func (r *slowNameReader) finished() <-chan struct{} {
	_, _, over := r.signals()

	return over
}

// say says one thing without waiting, the way the store says a change.
func say(to chan<- struct{}) {
	select {
	case to <- struct{}{}:
	default:
	}
}

// ---- what the tests above read the program through ----

// readUntilNamed reads the events of a chat until one of them carries the
// name of a sender, and fails with what it saw when none does.
//
// It is the consumer the interface is: it reads, it waits, and it reads
// again. The only thing it does not do is ask for a name, which is the whole
// of the change.
func readUntilNamed(
	t *testing.T,
	live *TelegramLiveUpdates,
	chatID int64,
	cursor uint64,
) tui.LiveMessageEvent {
	t.Helper()

	deadline := time.Now().Add(liveNameWait)
	for {
		for _, event := range live.MessageEvents(chatID, cursor).Events {
			if event.Message.Author != unknownAuthor {
				return event
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the name of the sender never arrived within %s", liveNameWait)
		}

		time.Sleep(5 * time.Millisecond)
	}
}

// timeSinceLiveEvents returns how long the read of the events of a chat
// took, which is the measurement the loop tests are about.
func timeSinceLiveEvents(
	live *TelegramLiveUpdates,
	chatID int64,
) time.Duration {
	started := time.Now()
	live.MessageEvents(chatID, 0)

	return time.Since(started)
}

// waitFor waits for a signal, and fails with what it was waiting for when it
// does not come.
//
// The bound is the one of liveNameWait and not of liveNameAnswer: what is
// waited for is a name that has been asked for, and not the answer to a
// question this test made.
func waitFor(t *testing.T, what string, signal <-chan struct{}) {
	t.Helper()

	select {
	case <-signal:
	case <-time.After(liveNameWait):
		t.Fatalf("the program never came to: %s", what)
	}
}

// drainNames takes every name that has arrived so far, so that a wait armed
// after it can only be woken by one that comes later.
func drainNames(live *TelegramLiveUpdates) {
	for {
		select {
		case <-live.senders.changes():
		default:
			return
		}
	}
}

var _ TelegramSenderNames = (*slowNameReader)(nil)

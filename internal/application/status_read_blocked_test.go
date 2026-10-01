package application

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"telecli/internal/outbox"
	"telecli/internal/tui"
	"telecli/internal/tui/theme"
)

// One record the settlement could not find, and every other message in the
// chat.
//
// This is the owner's account. The startup settlement moves an accepted
// record to uncertain and keeps the identifier TDLib gave it; the status
// reader rejected a record shaped like that, so the read of the WHOLE chat
// failed, the interface kept the failure in a field nobody read, and the
// screen showed a message as Queued while the queue said sent — for every
// message, for ever, because the read never once succeeded.
//
// Nothing here writes a state by hand. The record that blocks the read is
// put into uncertain by the settlement's own write, and the record that
// must still reach the screen is sent through MarkAccepted and MarkSent,
// which is the chain the reconciler performs.

// statusReadSentID is the queue entry the watched message was sent as. The
// submitter hands the interface the same identifier, so the status the
// queue reports lands on the row the submission drew.
const statusReadSentID = "sent-entry"

const statusReadAccountKey = "status-read-account"

// statusReadFixture is a real durable queue, the real status reader over
// it, and the interface's model wired to both — with the diagnostic stream
// captured, because a read that fails is only useful if it says so.
type statusReadFixture struct {
	log *strings.Builder
	m   tui.Model
}

// oneSettledRecordAndOneSentMessage builds the state the owner has.
func oneSettledRecordAndOneSentMessage(t *testing.T) *statusReadFixture {
	t.Helper()

	dataDir := filepath.Join(t.TempDir(), "outbox")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatalf("create data dir: %v", err)
	}

	opened, err := outbox.Open(
		context.Background(),
		outbox.Config{
			DataDir:    dataDir,
			DatabaseID: "status-read",
			InstanceID: "status-read",
			Dispatcher: outbox.DefaultDispatcherConfig("status-read"),
		},
		outbox.Deps{
			KeyProvider: newH7dKeyProvider(),
			Clock:       outbox.SystemClock{},
		},
	)
	if err != nil {
		t.Fatalf("open outbox: %v", err)
	}
	t.Cleanup(func() { _ = opened.Close() })

	store := opened.Store
	base := time.Unix(1789500000, 0).UTC()

	// The record the settlement will not find: accepted, then the lookup
	// comes back empty, so it is marked uncertain and keeps the temporary
	// identifier it was accepted with.
	settled := statusReadAccepted(t, store, "settled-entry", base)
	if _, err := store.MarkUncertain(
		context.Background(), settled.ID, settled.Version,
		settlementUncertainReason, base.Add(time.Minute),
	); err != nil {
		t.Fatalf("the settlement's own write: %v", err)
	}

	// A message the queue has sent, through the two transitions the
	// reconciler makes.
	sent := statusReadAccepted(t, store, statusReadSentID, base)
	confirmed, err := store.MarkSent(
		context.Background(), sent.ID, sent.Version,
		437583347712, base.Add(time.Second),
	)
	if err != nil {
		t.Fatalf("mark sent: %v", err)
	}
	if confirmed.State != outbox.StateSent {
		t.Fatalf("state = %q, want sent", confirmed.State)
	}

	statusReader, ok := store.(outbox.EntryStatusReader)
	if !ok {
		t.Fatal("the store cannot read statuses")
	}
	statusSource, err := NewOutboxMessageStatusSource(statusReader, 0)
	if err != nil {
		t.Fatalf("status source: %v", err)
	}
	pendingSource, err := NewOutboxPendingMessageSource(store, 0)
	if err != nil {
		t.Fatalf("pending source: %v", err)
	}

	log := &strings.Builder{}
	model, err := tui.NewModelWithDependencies(
		context.Background(),
		tui.Dependencies{
			Source:           &statusReadChats{},
			MessageSubmitter: &statusReadSubmitter{},
			AccountKey:       statusReadAccountKey,
			MessageStatuses:  newTUIMessageStatusSourceAdapter(statusSource),
			PendingMessages:  newTUIPendingMessageSourceAdapter(pendingSource),
			// The interface's own diagnostic stream. While `telecli tui`
			// runs this is the log file, and a read that fails has to reach
			// it rather than a field.
			Diagnostics:  log,
			Theme:        theme.DefaultTheme().ForProfile(theme.ProfileNoColor),
			ColorProfile: theme.ProfileNoColor,
		},
	)
	if err != nil {
		t.Fatalf("model: %v", err)
	}

	// The program starts: the chat list is loaded from here, the way the
	// model loads it in the running program.
	m := runStatusCmds(t, model.Init())
	model = applyStatusMsgs(t, model, m)

	return &statusReadFixture{log: log, m: model}
}

func statusReadAccepted(
	t *testing.T,
	store outbox.Store,
	id string,
	at time.Time,
) outbox.Entry {
	t.Helper()

	entry := outbox.Entry{
		ID: outbox.ID(id), AccountKey: statusReadAccountKey, ChatID: 7,
		Text: "сообщение", State: outbox.StateQueued,
		CreatedAt: at, UpdatedAt: at,
	}
	if err := store.Enqueue(context.Background(), entry); err != nil {
		t.Fatalf("enqueue %s: %v", id, err)
	}
	claimed, err := store.Claim(
		context.Background(), entry.ID, 0, "status-read",
		at.Add(time.Minute), at,
	)
	if err != nil {
		t.Fatalf("claim %s: %v", id, err)
	}
	accepted, err := store.MarkAccepted(
		context.Background(), claimed.ID, claimed.Version,
		-1000000042, at,
	)
	if err != nil {
		t.Fatalf("mark accepted %s: %v", id, err)
	}

	return accepted
}

// statusReadChats is one chat.
type statusReadChats struct{}

func (statusReadChats) ListChats(context.Context) ([]tui.Chat, error) {
	return []tui.Chat{{ID: 7, Title: "Избранное"}}, nil
}

func (statusReadChats) LoadHistory(
	context.Context, int64, int64, int,
) (tui.HistoryPage, error) {
	return tui.HistoryPage{}, nil
}

func (statusReadChats) SendMessage(
	context.Context, int64, string,
) (tui.Message, error) {
	return tui.Message{}, nil
}

type statusReadSubmitter struct{}

func (statusReadSubmitter) SubmitMessage(
	context.Context, int64, string,
) (tui.Submission, error) {
	return tui.Submission{ID: statusReadSentID, State: tui.SubmissionQueued}, nil
}

// A record the settlement could not find must not stop the interface from
// learning that another message went out.
func TestOneSettledRecordDoesNotStopTheInterfaceReadingTheOthers(
	t *testing.T,
) {
	fixture := oneSettledRecordAndOneSentMessage(t)
	m := fixture.m

	// The terminal is measured before anything is drawn, so the size
	// comes first: at 0x0 there is no conversation to open.
	m = fixture.key(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})

	// Open the conversation. The read the program asks for on the way is
	// the read that matters: the record that blocks it is already there.
	m = fixture.key(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	// Send. The submission puts a Queued row on the screen, which is
	// honest at that instant.
	// Opening a conversation with Enter already put the cursor in the
	// composer, which is where a user who opened a chat is about to write.
	m = fixture.key(t, m, tea.KeyMsg{
		Type: tea.KeyRunes, Runes: []rune("привет"),
	})
	m = fixture.key(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	// The submission's own read ran with the key press above, and it is
	// that read which carries the confirmation. Whether a Queued row was
	// ever drawn is not this test's business: the queue confirmed the
	// message in the same quarter of a second, and the screen that matters
	// is the one after.
	screen := m.View()
	if strings.Contains(screen, "● queued") {
		t.Fatalf(
			"the queue sent the message and the screen still says "+
				"queued:\n%s\n\nthe interface wrote:\n%s",
			screen, fixture.log.String(),
		)
	}
	if !strings.Contains(screen, "✓ sent") {
		t.Fatalf("the screen does not say sent:\n%s", screen)
	}
}

// A read that fails is written to the log rather than kept in a field.
//
// The owner read three starts of a failing settlement from the log and
// nothing at all about a failing status read, because the failure went
// into a field the screen never showed and nothing ever wrote out.
func TestAFailedStatusReadIsWrittenToTheLog(t *testing.T) {
	t.Parallel()

	// A status source that fails, which is what a read of a queue with one
	// unreadable record used to be.
	log := &strings.Builder{}
	model, err := tui.NewModelWithDependencies(
		context.Background(),
		tui.Dependencies{
			Source:           &statusReadChats{},
			MessageSubmitter: &statusReadSubmitter{},
			AccountKey:       statusReadAccountKey,
			MessageStatuses:  newTUIMessageStatusSourceAdapter(failingStatuses{}),
			PendingMessages:  newTUIPendingMessageSourceAdapter(failingPending{}),
			Diagnostics:      log,
			Theme:            theme.DefaultTheme().ForProfile(theme.ProfileNoColor),
			ColorProfile:     theme.ProfileNoColor,
		},
	)
	if err != nil {
		t.Fatalf("model: %v", err)
	}

	fixture := &statusReadFixture{log: log, m: model}
	model = applyStatusMsgs(t, model, runStatusCmds(t, model.Init()))
	fixture.m = model

	fixture.key(t, fixture.m, tea.KeyMsg{Type: tea.KeyEnter})

	written := log.String()
	if !strings.Contains(written, "status") {
		t.Fatalf(
			"a status read that failed wrote nothing to the log, so "+
				"nothing on the machine could say why the screen was "+
				"stuck:\n%s", written,
		)
	}
	if strings.Contains(written, "привет") {
		t.Fatalf("the log carried the text of a message:\n%s", written)
	}
}

// failingStatuses is a queue that cannot be read, which is what a chat
// with one unreadable record used to be.
type failingStatuses struct{}

func (failingStatuses) ListMessageStatuses(
	context.Context, string, int64,
) ([]MessageStatus, error) {
	return nil, errStatusRead
}

type failingPending struct{}

func (failingPending) ListPendingMessages(
	context.Context, string, int64,
) ([]PendingMessage, error) {
	return nil, errStatusRead
}

// errStatusRead names no message and carries no text of one.
var errStatusRead = errors.New("outbox status: unreadable record")

// applyStatusMsgs feeds a model a list of messages and runs what they
// answer with.
func applyStatusMsgs(
	t *testing.T,
	model tui.Model,
	msgs []tea.Msg,
) tui.Model {
	t.Helper()

	for round := 0; round < 8 && len(msgs) > 0; round++ {
		next := make([]tea.Msg, 0, len(msgs))
		for _, msg := range msgs {
			updated, cmd := model.Update(msg)
			typed, ok := updated.(tui.Model)
			if !ok {
				t.Fatalf("Update returned %T, want tui.Model", updated)
			}
			model = typed
			next = append(next, runStatusCmds(t, cmd)...)
		}
		msgs = next
	}

	return model
}

// key gives the model a message and settles what it answers with.
func (f *statusReadFixture) key(
	t *testing.T,
	model tui.Model,
	msg tea.Msg,
) tui.Model {
	t.Helper()

	updated, cmd := model.Update(msg)
	typed, ok := updated.(tui.Model)
	if !ok {
		t.Fatalf("Update returned %T, want tui.Model", updated)
	}

	return applyStatusMsgs(t, typed, runStatusCmds(t, cmd))
}

// runStatusCmds runs a command and returns what it answered with, a batch
// expanded and a tick dropped: a tick is a timer two seconds out, and these
// tests are about the read rather than the wait for it.
func runStatusCmds(t *testing.T, cmd tea.Cmd) []tea.Msg {
	t.Helper()

	if cmd == nil {
		return nil
	}

	answers := make(chan tea.Msg, 1)
	go func() { answers <- cmd() }()

	var answer tea.Msg
	select {
	case answer = <-answers:
	case <-time.After(200 * time.Millisecond):
		return nil
	}
	if answer == nil {
		return nil
	}
	if batch, isBatch := answer.(tea.BatchMsg); isBatch {
		var flattened []tea.Msg
		for _, inner := range batch {
			flattened = append(flattened, runStatusCmds(t, inner)...)
		}
		return flattened
	}

	return []tea.Msg{answer}
}

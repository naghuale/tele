package tui

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type h6c2bStatusSource struct {
	listMessageStatuses func(
		context.Context,
		string,
		int64,
	) ([]MessageStatus, error)
}

func (s *h6c2bStatusSource) ListMessageStatuses(
	ctx context.Context,
	accountKey string,
	chatID int64,
) ([]MessageStatus, error) {
	if s.listMessageStatuses == nil {
		return nil, errors.New("unexpected ListMessageStatuses call")
	}
	return s.listMessageStatuses(ctx, accountKey, chatID)
}

var _ MessageStatusSource = (*h6c2bStatusSource)(nil)

func newPollingTestModel(
	source MessageStatusSource,
) Model {
	return Model{
		ctx:             context.Background(),
		accountKey:      "account-1",
		messageStatuses: source,
		quitting:        false,
	}
}

func h6c2bLoadedMsg(
	model Model,
	statuses []MessageStatus,
) messageStatusesLoadedMsg {
	return messageStatusesLoadedMsg{
		generation: model.messageStatusGeneration,
		read:       model.statusReadSeq,
		accountKey: model.messageStatusAccountKey,
		chatID:     model.messageStatusChatID,
		statuses:   statuses,
	}
}

func h6c2bFailedMsg(
	model Model,
	err error,
) messageStatusesFailedMsg {
	return messageStatusesFailedMsg{
		generation: model.messageStatusGeneration,
		read:       model.statusReadSeq,
		accountKey: model.messageStatusAccountKey,
		chatID:     model.messageStatusChatID,
		err:        err,
	}
}

func TestMessageStatusPollingDisabledWithoutSource(t *testing.T) {
	t.Parallel()

	model := newPollingTestModel(nil)
	model.messageStatusAccountKey = "account-1"
	model.messageStatusChatID = 42

	if cmd := model.loadMessageStatuses(); cmd != nil {
		t.Fatal("loadMessageStatuses() != nil without source")
	}
	if model.messageStatusLoading {
		t.Fatal("messageStatusLoading = true without source")
	}
}

func TestMessageStatusPollingDisabledWithoutAccount(t *testing.T) {
	t.Parallel()

	model := newPollingTestModel(&h6c2bStatusSource{
		listMessageStatuses: func(
			context.Context,
			string,
			int64,
		) ([]MessageStatus, error) {
			return nil, errors.New("unexpected read")
		},
	})
	model.messageStatusChatID = 42

	if cmd := model.loadMessageStatuses(); cmd != nil {
		t.Fatal("loadMessageStatuses() != nil without account key")
	}
	if model.messageStatusLoading {
		t.Fatal("messageStatusLoading = true without account key")
	}
}

func TestMessageStatusPollingDisabledWithoutActiveChat(t *testing.T) {
	t.Parallel()

	model := newPollingTestModel(&h6c2bStatusSource{
		listMessageStatuses: func(
			context.Context,
			string,
			int64,
		) ([]MessageStatus, error) {
			return nil, errors.New("unexpected read")
		},
	})
	model.messageStatusAccountKey = "account-1"

	if cmd := model.loadMessageStatuses(); cmd != nil {
		t.Fatal("loadMessageStatuses() != nil without active chat")
	}
	if model.messageStatusLoading {
		t.Fatal("messageStatusLoading = true without active chat")
	}
}

func TestMessageStatusPollingDisabledWhileQuitting(t *testing.T) {
	t.Parallel()

	model := newPollingTestModel(&h6c2bStatusSource{
		listMessageStatuses: func(
			context.Context,
			string,
			int64,
		) ([]MessageStatus, error) {
			return nil, errors.New("unexpected read")
		},
	})
	model.messageStatusAccountKey = "account-1"
	model.messageStatusChatID = 42
	model.quitting = true

	if cmd := model.loadMessageStatuses(); cmd != nil {
		t.Fatal("loadMessageStatuses() != nil while quitting")
	}
	if model.messageStatusLoading {
		t.Fatal("messageStatusLoading = true while quitting")
	}
}

func TestMessageStatusPollingStartsWithSource(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	source := &h6c2bStatusSource{
		listMessageStatuses: func(
			context.Context,
			string,
			int64,
		) ([]MessageStatus, error) {
			calls.Add(1)
			return []MessageStatus{
				{EntryID: "entry-1", State: MessageDeliveryQueued},
			}, nil
		},
	}
	model := newPollingTestModel(source)

	cmd := model.setMessageStatusTarget("account-1", 42)
	if cmd == nil {
		t.Fatal("setMessageStatusTarget() = nil, want status command")
	}
	if !model.messageStatusLoading {
		t.Fatal("messageStatusLoading = false before command runs")
	}

	polling := feedStatusResponse(t, model, cmd)
	if calls.Load() != 1 {
		t.Fatalf("source calls = %d, want 1", calls.Load())
	}
	if polling.messageStatusLoading {
		t.Fatal("messageStatusLoading = true after response")
	}
	if len(polling.deliveryStatuses) != 1 {
		t.Fatalf("deliveryStatuses = %#v, want one entry", polling.deliveryStatuses)
	}
}

func TestMessageStatusPollingUsesActiveAccountAndChat(t *testing.T) {
	t.Parallel()

	var gotAccountKey string
	var gotChatID int64
	source := &h6c2bStatusSource{
		listMessageStatuses: func(
			_ context.Context,
			accountKey string,
			chatID int64,
		) ([]MessageStatus, error) {
			gotAccountKey = accountKey
			gotChatID = chatID
			return nil, nil
		},
	}
	model := newPollingTestModel(source)
	model.messageStatusAccountKey = "account-7"
	model.messageStatusChatID = 42

	cmd := model.loadMessageStatuses()
	if cmd == nil {
		t.Fatal("loadMessageStatuses() = nil")
	}
	_ = cmd()

	if gotAccountKey != "account-7" {
		t.Fatalf("accountKey = %q, want account-7", gotAccountKey)
	}
	if gotChatID != 42 {
		t.Fatalf("chatID = %d, want 42", gotChatID)
	}
}

func TestMessageStatusPollingPreventsOverlappingRequests(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	release := make(chan struct{})
	source := &h6c2bStatusSource{
		listMessageStatuses: func(
			context.Context,
			string,
			int64,
		) ([]MessageStatus, error) {
			close(started)
			<-release
			return nil, nil
		},
	}
	model := newPollingTestModel(source)
	model.messageStatusAccountKey = "account-1"
	model.messageStatusChatID = 42

	first := model.loadMessageStatuses()
	if first == nil {
		t.Fatal("first loadMessageStatuses() = nil")
	}
	if second := model.loadMessageStatuses(); second != nil {
		t.Fatal("second loadMessageStatuses() != nil while loading")
	}

	done := make(chan struct{})
	go func() {
		_ = first()
		close(done)
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for status read to start")
	}

	close(release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for status read to finish")
	}
}

func TestMessageStatusPollingPreservesSourceOrder(t *testing.T) {
	t.Parallel()

	source := &h6c2bStatusSource{
		listMessageStatuses: func(
			context.Context,
			string,
			int64,
		) ([]MessageStatus, error) {
			return []MessageStatus{
				{EntryID: "entry-newer", State: MessageDeliverySending},
				{EntryID: "entry-middle", State: MessageDeliveryRetrying},
				{EntryID: "entry-older", State: MessageDeliverySent},
			}, nil
		},
	}
	model := newPollingTestModel(source)
	cmd := model.setMessageStatusTarget("account-1", 42)
	if cmd == nil {
		t.Fatal("setMessageStatusTarget() = nil")
	}

	polling := feedStatusResponse(t, model, cmd)

	want := []string{"entry-newer", "entry-middle", "entry-older"}
	if len(polling.deliveryStatuses) != len(want) {
		t.Fatalf("deliveryStatuses = %#v, want %d entries", polling.deliveryStatuses, len(want))
	}
	for index, entryID := range want {
		if polling.deliveryStatuses[index].EntryID != entryID {
			t.Fatalf(
				"deliveryStatuses[%d] = %q, want %q",
				index,
				polling.deliveryStatuses[index].EntryID,
				entryID,
			)
		}
	}
}

func TestMessageStatusPollingStoresDefensiveCopy(t *testing.T) {
	t.Parallel()

	model := newPollingTestModel(&h6c2bStatusSource{})
	model.messageStatusAccountKey = "account-1"
	model.messageStatusChatID = 42
	model.messageStatusGeneration = 3

	statuses := []MessageStatus{
		{EntryID: "entry-1", State: MessageDeliveryQueued},
	}
	msg := h6c2bLoadedMsg(model, statuses)

	updated, _ := model.handleMessageStatusesLoaded(msg)
	statuses[0].EntryID = "mutated"

	if updated.deliveryStatuses[0].EntryID != "entry-1" {
		t.Fatalf(
			"deliveryStatuses[0] = %q, want entry-1",
			updated.deliveryStatuses[0].EntryID,
		)
	}
}

func TestMessageStatusPollingIgnoresStaleGeneration(t *testing.T) {
	t.Parallel()

	model := newPollingTestModel(&h6c2bStatusSource{})
	model.messageStatusAccountKey = "account-1"
	model.messageStatusChatID = 42
	model.messageStatusGeneration = 5
	model.messageStatusLoading = true
	model.messageStatusErr = errors.New("current error")
	model.deliveryStatuses = []MessageStatus{
		{EntryID: "current", State: MessageDeliverySent},
	}

	msg := h6c2bLoadedMsg(model, nil)
	msg.generation = 4
	msg.statuses = []MessageStatus{
		{EntryID: "stale", State: MessageDeliveryFailed},
	}

	updated, cmd := model.handleMessageStatusesLoaded(msg)
	if cmd != nil {
		t.Fatal("stale response scheduled a poll")
	}
	if !updated.messageStatusLoading {
		t.Fatal("stale response cleared loading")
	}
	if updated.messageStatusErr == nil {
		t.Fatal("stale response cleared the current error")
	}
	if len(updated.deliveryStatuses) != 1 ||
		updated.deliveryStatuses[0].EntryID != "current" {
		t.Fatalf("deliveryStatuses = %#v, want current snapshot", updated.deliveryStatuses)
	}
}

func TestMessageStatusPollingIgnoresPreviousAccountResponse(t *testing.T) {
	t.Parallel()

	model := newPollingTestModel(&h6c2bStatusSource{})
	model.messageStatusAccountKey = "account-2"
	model.messageStatusChatID = 42
	model.messageStatusLoading = true

	msg := h6c2bLoadedMsg(model, nil)
	msg.accountKey = "account-1"
	msg.statuses = []MessageStatus{
		{EntryID: "stale", State: MessageDeliveryFailed},
	}

	updated, cmd := model.handleMessageStatusesLoaded(msg)
	if cmd != nil {
		t.Fatal("stale response scheduled a poll")
	}
	if !updated.messageStatusLoading {
		t.Fatal("stale response cleared loading")
	}
	if len(updated.deliveryStatuses) != 0 {
		t.Fatalf("deliveryStatuses = %#v, want empty", updated.deliveryStatuses)
	}
}

func TestMessageStatusPollingIgnoresPreviousChatResponse(t *testing.T) {
	t.Parallel()

	model := newPollingTestModel(&h6c2bStatusSource{})
	model.messageStatusAccountKey = "account-1"
	model.messageStatusChatID = 42
	model.messageStatusLoading = true

	msg := h6c2bLoadedMsg(model, nil)
	msg.chatID = 7
	msg.statuses = []MessageStatus{
		{EntryID: "stale", State: MessageDeliveryFailed},
	}

	updated, cmd := model.handleMessageStatusesLoaded(msg)
	if cmd != nil {
		t.Fatal("stale response scheduled a poll")
	}
	if !updated.messageStatusLoading {
		t.Fatal("stale response cleared loading")
	}
	if len(updated.deliveryStatuses) != 0 {
		t.Fatalf("deliveryStatuses = %#v, want empty", updated.deliveryStatuses)
	}
}

func TestStaleStatusResponseDoesNotClearCurrentLoadingState(t *testing.T) {
	t.Parallel()

	model := newPollingTestModel(&h6c2bStatusSource{})
	model.messageStatusAccountKey = "account-1"
	model.messageStatusChatID = 42
	model.messageStatusGeneration = 2
	model.messageStatusLoading = true

	stale := h6c2bLoadedMsg(model, nil)
	stale.generation = 1

	updated, _ := model.handleMessageStatusesLoaded(stale)
	if !updated.messageStatusLoading {
		t.Fatal("stale success cleared loading")
	}

	staleFailure := h6c2bFailedMsg(model, errors.New("stale failure"))
	staleFailure.generation = 1
	updated, _ = model.handleMessageStatusesFailed(staleFailure)
	if !updated.messageStatusLoading {
		t.Fatal("stale failure cleared loading")
	}
	if updated.messageStatusErr != nil {
		t.Fatalf("messageStatusErr = %v, want nil", updated.messageStatusErr)
	}
}

func TestStaleStatusFailureDoesNotReplaceCurrentError(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("current failure")
	model := newPollingTestModel(&h6c2bStatusSource{})
	model.messageStatusAccountKey = "account-1"
	model.messageStatusChatID = 42
	model.messageStatusGeneration = 4
	model.messageStatusErr = sentinel

	stale := h6c2bFailedMsg(model, errors.New("stale failure"))
	stale.generation = 3

	updated, cmd := model.handleMessageStatusesFailed(stale)
	if cmd != nil {
		t.Fatal("stale failure scheduled a poll")
	}
	if !errors.Is(updated.messageStatusErr, sentinel) {
		t.Fatalf("messageStatusErr = %v, want %v", updated.messageStatusErr, sentinel)
	}
}

func TestMessageStatusPollingKeepsSnapshotOnFailure(t *testing.T) {
	t.Parallel()

	model := newPollingTestModel(&h6c2bStatusSource{})
	model.messageStatusAccountKey = "account-1"
	model.messageStatusChatID = 42
	model.messageStatusGeneration = 1
	model.deliveryStatuses = []MessageStatus{
		{EntryID: "entry-1", State: MessageDeliverySending},
	}

	updated, _ := model.handleMessageStatusesFailed(
		h6c2bFailedMsg(model, errors.New("read failed")),
	)
	if len(updated.deliveryStatuses) != 1 ||
		updated.deliveryStatuses[0].EntryID != "entry-1" {
		t.Fatalf(
			"deliveryStatuses = %#v, want preserved snapshot",
			updated.deliveryStatuses,
		)
	}
	if updated.messageStatusLoading {
		t.Fatal("messageStatusLoading = true after failure")
	}
}

func TestMessageStatusPollingPreservesComposerOnFailure(t *testing.T) {
	t.Parallel()

	model := newPollingTestModel(&h6c2bStatusSource{})
	model.messageStatusAccountKey = "account-1"
	model.messageStatusChatID = 42
	model.messageStatusGeneration = 1
	model.composer = []rune("draft text")

	updated, _ := model.handleMessageStatusesFailed(
		h6c2bFailedMsg(model, errors.New("read failed")),
	)
	if string(updated.composer) != "draft text" {
		t.Fatalf("composer = %q, want draft text", string(updated.composer))
	}
}

func TestMessageStatusPollingDoesNotSubmitOnFailure(t *testing.T) {
	t.Parallel()

	submitter := &h5aComposerSubmitter{}
	model := newPollingTestModel(&h6c2bStatusSource{})
	model.submitter = submitter
	model.messageStatusAccountKey = "account-1"
	model.messageStatusChatID = 42
	model.messageStatusGeneration = 1
	model.composer = []rune("draft text")

	model.handleMessageStatusesFailed(
		h6c2bFailedMsg(model, errors.New("read failed")),
	)

	if submitter.calls.Load() != 0 {
		t.Fatalf("submitter calls = %d, want 0", submitter.calls.Load())
	}
}

func TestMessageStatusPollingPreservesErrorSentinel(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("status read failed")
	model := newPollingTestModel(&h6c2bStatusSource{})
	model.messageStatusAccountKey = "account-1"
	model.messageStatusChatID = 42
	model.messageStatusGeneration = 1

	updated, _ := model.handleMessageStatusesFailed(
		h6c2bFailedMsg(model, sentinel),
	)
	if !errors.Is(updated.messageStatusErr, sentinel) {
		t.Fatalf("messageStatusErr = %v, want %v", updated.messageStatusErr, sentinel)
	}
}

func TestMessageStatusPollingIgnoresCanceledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	source := &h6c2bStatusSource{
		listMessageStatuses: func(
			context.Context,
			string,
			int64,
		) ([]MessageStatus, error) {
			return nil, errors.New("unexpected read")
		},
	}
	model := newPollingTestModel(source)
	model.ctx = ctx
	model.messageStatusAccountKey = "account-1"
	model.messageStatusChatID = 42
	model.messageStatusGeneration = 1
	model.deliveryStatuses = []MessageStatus{
		{EntryID: "entry-1", State: MessageDeliverySent},
	}

	cancel()

	cmd := model.loadMessageStatuses()
	if cmd == nil {
		t.Fatal("loadMessageStatuses() = nil")
	}
	msg, ok := cmd().(messageStatusesFailedMsg)
	if !ok {
		t.Fatalf("message type = %T, want messageStatusesFailedMsg", msg)
	}
	if !errors.Is(msg.err, context.Canceled) {
		t.Fatalf("message error = %v, want context.Canceled", msg.err)
	}

	updated, next := model.handleMessageStatusesFailed(msg)
	if next != nil {
		t.Fatal("canceled read scheduled a poll")
	}
	if updated.messageStatusErr != nil {
		t.Fatalf("messageStatusErr = %v, want nil", updated.messageStatusErr)
	}
	if updated.messageStatusLoading {
		t.Fatal("messageStatusLoading = true after canceled read")
	}
	if len(updated.deliveryStatuses) != 1 {
		t.Fatalf(
			"deliveryStatuses = %#v, want preserved snapshot",
			updated.deliveryStatuses,
		)
	}
}

func TestMessageStatusPollingStopsWhenContextCanceled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	source := &h6c2bStatusSource{
		listMessageStatuses: func(
			context.Context,
			string,
			int64,
		) ([]MessageStatus, error) {
			return nil, errors.New("unexpected read")
		},
	}
	model := newPollingTestModel(source)
	model.ctx = ctx
	model.messageStatusAccountKey = "account-1"
	model.messageStatusChatID = 42

	cmd := model.loadMessageStatuses()
	if cmd == nil {
		t.Fatal("loadMessageStatuses() = nil")
	}
	updated, next := model.handleMessageStatusesFailed(cmd().(messageStatusesFailedMsg))
	if next != nil {
		t.Fatal("canceled lifecycle context scheduled a poll")
	}
	if updated.messageStatusErr != nil {
		t.Fatalf("messageStatusErr = %v, want nil", updated.messageStatusErr)
	}
}

// The cadence belongs to the tick, and to nothing else.
//
// A response scheduling the next read as well means two loops: one driven
// by the timer and one driven by whichever read answered last, and a
// failing source would then be asked twice as often as a working one. It
// also means a read that delivers nothing - because nothing changed - can
// never hand the loop back, and the poll would stop for good.
func TestTheTickIsTheOnlyThingThatSchedulesTheNextPoll(t *testing.T) {
	t.Parallel()

	model := newPollingTestModel(&h6c2bStatusSource{})
	model.messageStatusAccountKey = "account-1"
	model.messageStatusChatID = 42
	model.messageStatusGeneration = 7

	if _, cmd := model.handleMessageStatusesLoaded(
		h6c2bLoadedMsg(model, nil),
	); cmd != nil {
		t.Fatal("a response scheduled a poll of its own")
	}
	if _, cmd := model.handleMessageStatusesFailed(
		h6c2bFailedMsg(model, errors.New("read failed")),
	); cmd != nil {
		t.Fatal("a failure scheduled a poll of its own")
	}

	updated, cmd := model.handleMessageStatusPollTick(
		messageStatusPollTickMsg{},
	)
	if cmd == nil {
		t.Fatal("the tick did not start the next read")
	}
	if !updated.messageStatusLoading {
		t.Fatal("the tick did not start a read")
	}
}

func TestMessageStatusPollingDoesNotScheduleAfterQuit(t *testing.T) {
	t.Parallel()

	model := newPollingTestModel(&h6c2bStatusSource{})
	model.messageStatusAccountKey = "account-1"
	model.messageStatusChatID = 42
	model.messageStatusGeneration = 7
	model.quitting = true
	model.messageStatusLoading = true

	if _, cmd := model.handleMessageStatusesLoaded(
		h6c2bLoadedMsg(model, nil),
	); cmd != nil {
		t.Fatal("success after quit scheduled a poll")
	}

	model.messageStatusLoading = true
	if _, cmd := model.handleMessageStatusesFailed(
		h6c2bFailedMsg(model, errors.New("read failed")),
	); cmd != nil {
		t.Fatal("failure after quit scheduled a poll")
	}
}

// A tick belongs to the generation it was armed for. It must not read the
// target that generation pointed at — the model has left it — but it must
// still hand the loop back, because dropping it is what ended the delivery
// poll for the rest of the session: the chat the user opened started a new
// generation, the only tick on its way belonged to the old one, and from
// then on nothing was ever read again, so a message stayed on Queued for
// as long as the program ran.
func TestMessageStatusPollingOldTickKeepsTheLoopAliveForTheCurrentTarget(t *testing.T) {
	t.Parallel()

	var reads atomic.Int32
	source := &h6c2bStatusSource{
		listMessageStatuses: func(
			context.Context,
			string,
			int64,
		) ([]MessageStatus, error) {
			reads.Add(1)
			return nil, nil
		},
	}
	model := newPollingTestModel(source)
	model.messageStatusAccountKey = "account-1"
	model.messageStatusChatID = 42
	model.messageStatusGeneration = 9
	// A read of the generation the model has left is in flight. The
	// response is discarded by its generation, so its loading flag has to
	// be cleared or the loop would wait for a read that ends in nothing.
	model.messageStatusLoading = true

	updated, cmd := model.handleMessageStatusPollTick(
		messageStatusPollTickMsg{},
	)
	if cmd == nil {
		t.Fatal("the old tick ended the poll loop")
	}
	if !updated.messageStatusLoading {
		t.Fatal("the old tick did not start a read of the current target")
	}
	_ = firstReadOf(t, cmd)
	if reads.Load() != 1 {
		t.Fatalf("reads = %d, want 1 of the current target", reads.Load())
	}
}

func TestMessageStatusPollingCurrentTickStartsRequest(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	source := &h6c2bStatusSource{
		listMessageStatuses: func(
			context.Context,
			string,
			int64,
		) ([]MessageStatus, error) {
			calls.Add(1)
			return nil, nil
		},
	}
	model := newPollingTestModel(source)
	model.messageStatusAccountKey = "account-1"
	model.messageStatusChatID = 42
	model.messageStatusGeneration = 9

	updated, cmd := model.handleMessageStatusPollTick(
		messageStatusPollTickMsg{},
	)
	if cmd == nil {
		t.Fatal("current tick did not start a request")
	}
	if !updated.messageStatusLoading {
		t.Fatal("current tick did not set loading")
	}
	_ = firstReadOf(t, cmd)
	if calls.Load() != 1 {
		t.Fatalf("source calls = %d, want 1", calls.Load())
	}
}

// A read that takes longer than the interval is not waited for: the next
// tick reads again, and the slow answer is then the older one. Applying it
// would put a snapshot back on the screen that the newer read has already
// replaced, so it is dropped by its read number instead.
func TestASlowReadIsDiscardedAfterANewerOne(t *testing.T) {
	t.Parallel()

	model := newPollingTestModel(&h6c2bStatusSource{})
	model.messageStatusAccountKey = "account-1"
	model.messageStatusChatID = 42
	model.messageStatusGeneration = 2

	slow := messageStatusesLoadedMsg{
		generation: 2,
		read:       1,
		accountKey: "account-1",
		chatID:     42,
		statuses: []MessageStatus{
			{EntryID: "entry-slow", State: MessageDeliveryQueued},
		},
	}

	// The first read is in flight when the tick arrives, and the tick
	// starts the second one: the read number of the answer below is the
	// one the tick has already replaced.
	model.messageStatusLoading = true
	model.statusReadSeq = 1
	updated, _ := model.handleMessageStatusPollTick(
		messageStatusPollTickMsg{},
	)
	if !updated.messageStatusLoading {
		t.Fatal("the tick did not start a read while one was in flight")
	}

	updated, _ = updated.handleMessageStatusesLoaded(slow)
	if len(updated.deliveryStatuses) != 0 {
		t.Fatalf(
			"deliveryStatuses = %#v, want the older read discarded",
			updated.deliveryStatuses,
		)
	}
}

func TestMessageStatusPollingReplacesSnapshot(t *testing.T) {
	t.Parallel()

	model := newPollingTestModel(&h6c2bStatusSource{})
	model.messageStatusAccountKey = "account-1"
	model.messageStatusChatID = 42
	model.messageStatusGeneration = 1
	model.deliveryStatuses = []MessageStatus{
		{EntryID: "entry-old", State: MessageDeliveryQueued},
	}

	updated, _ := model.handleMessageStatusesLoaded(h6c2bLoadedMsg(
		model,
		[]MessageStatus{{EntryID: "entry-new", State: MessageDeliverySent}},
	))
	if len(updated.deliveryStatuses) != 1 ||
		updated.deliveryStatuses[0].EntryID != "entry-new" {
		t.Fatalf(
			"deliveryStatuses = %#v, want replaced snapshot",
			updated.deliveryStatuses,
		)
	}
}

func TestMessageStatusPollingDoesNotAppendDuplicateEntries(t *testing.T) {
	t.Parallel()

	model := newPollingTestModel(&h6c2bStatusSource{})
	model.messageStatusAccountKey = "account-1"
	model.messageStatusChatID = 42
	model.messageStatusGeneration = 1

	snapshot := []MessageStatus{
		{EntryID: "entry-1", State: MessageDeliveryQueued},
	}

	model, _ = model.handleMessageStatusesLoaded(h6c2bLoadedMsg(model, snapshot))
	model, _ = model.handleMessageStatusesLoaded(h6c2bLoadedMsg(model, snapshot))

	if len(model.deliveryStatuses) != 1 {
		t.Fatalf(
			"deliveryStatuses = %#v, want one entry",
			model.deliveryStatuses,
		)
	}
}

func TestMessageStatusPollingAcceptsEmptySnapshot(t *testing.T) {
	t.Parallel()

	model := newPollingTestModel(&h6c2bStatusSource{})
	model.messageStatusAccountKey = "account-1"
	model.messageStatusChatID = 42
	model.messageStatusGeneration = 1
	model.deliveryStatuses = []MessageStatus{
		{EntryID: "entry-1", State: MessageDeliveryQueued},
	}

	updated, _ := model.handleMessageStatusesLoaded(h6c2bLoadedMsg(model, nil))
	if updated.deliveryStatuses == nil {
		t.Fatal("deliveryStatuses = nil, want non-nil empty snapshot")
	}
	if len(updated.deliveryStatuses) != 0 {
		t.Fatalf("deliveryStatuses = %#v, want empty", updated.deliveryStatuses)
	}
}

func TestMessageStatusPollingClearsPreviousChatSnapshot(t *testing.T) {
	t.Parallel()

	source := &h6c2bStatusSource{
		listMessageStatuses: func(
			context.Context,
			string,
			int64,
		) ([]MessageStatus, error) {
			return []MessageStatus{
				{EntryID: "entry-2", State: MessageDeliveryQueued},
			}, nil
		},
	}
	model := newPollingTestModel(source)
	model.messageStatusAccountKey = "account-1"
	model.messageStatusChatID = 42
	model.messageStatusGeneration = 1
	model.deliveryStatuses = []MessageStatus{
		{EntryID: "entry-1", State: MessageDeliverySent},
	}
	model.messageStatusErr = errors.New("previous failure")

	if cmd := model.setMessageStatusTarget("account-1", 42); cmd != nil {
		t.Fatal("same target restarted polling")
	}

	cmd := model.setMessageStatusTarget("account-1", 7)
	if model.messageStatusGeneration != 2 {
		t.Fatalf(
			"messageStatusGeneration = %d, want 2",
			model.messageStatusGeneration,
		)
	}
	if len(model.deliveryStatuses) != 0 {
		t.Fatalf(
			"deliveryStatuses = %#v, want cleared snapshot",
			model.deliveryStatuses,
		)
	}
	if model.messageStatusErr != nil {
		t.Fatalf("messageStatusErr = %v, want nil", model.messageStatusErr)
	}
	if cmd == nil {
		t.Fatal("new target did not start a request")
	}

	polling := feedStatusResponse(t, model, cmd)
	if len(polling.deliveryStatuses) != 1 ||
		polling.deliveryStatuses[0].EntryID != "entry-2" {
		t.Fatalf(
			"deliveryStatuses = %#v, want entry-2 only",
			polling.deliveryStatuses,
		)
	}
}

func TestMessageStatusPollingQuitInvalidatesInFlightResponse(t *testing.T) {
	t.Parallel()

	model := newPollingTestModel(&h6c2bStatusSource{})
	model.messageStatusAccountKey = "account-1"
	model.messageStatusChatID = 42
	model.messageStatusGeneration = 3
	model.messageStatusLoading = true

	inFlight := h6c2bLoadedMsg(model, []MessageStatus{
		{EntryID: "entry-1", State: MessageDeliverySent},
	})

	model.quitting = true
	model.invalidateMessageStatusPolling()

	updated, cmd := model.handleMessageStatusesLoaded(inFlight)
	if cmd != nil {
		t.Fatal("in-flight response after quit scheduled a poll")
	}
	if len(updated.deliveryStatuses) != 0 {
		t.Fatalf(
			"deliveryStatuses = %#v, want empty",
			updated.deliveryStatuses,
		)
	}
	if cmd := updated.loadMessageStatuses(); cmd != nil {
		t.Fatal("polling continued after quit")
	}
}

func TestMessageStatusPollingStartsOnConversationEntry(t *testing.T) {
	t.Parallel()

	source := &h6c2bStatusSource{
		listMessageStatuses: func(
			context.Context,
			string,
			int64,
		) ([]MessageStatus, error) {
			return nil, nil
		},
	}
	model := newPollingTestModel(source)
	model.chats = []Chat{{ID: 42}}
	model.chatsState = loadStateLoaded

	updated, cmd := model.updateChatsKey(press(tea.KeyEnter))
	if cmd == nil {
		t.Fatal("opening a conversation did not start status polling")
	}

	polling, ok := updated.(Model)
	if !ok {
		t.Fatalf("model type = %T, want Model", updated)
	}
	if polling.messageStatusChatID != 42 {
		t.Fatalf(
			"messageStatusChatID = %d, want 42",
			polling.messageStatusChatID,
		)
	}
	if !polling.messageStatusLoading {
		t.Fatal("messageStatusLoading = false after opening a chat")
	}
}

func TestMessageStatusPollingStopsOnConversationExit(t *testing.T) {
	t.Parallel()

	model := newPollingTestModel(&h6c2bStatusSource{})
	model.screen = ScreenConversation
	// Leaving the conversation is Esc from the timeline; the composer
	// hands the keys there first.
	model.focus = FocusHistory
	model.messageStatusAccountKey = "account-1"
	model.messageStatusChatID = 42
	model.messageStatusGeneration = 5
	model.messageStatusLoading = true

	updated, cmd := model.updateConversationKey(press(tea.KeyEsc))
	if cmd != nil {
		t.Fatal("leaving a conversation started a command")
	}

	polling, ok := updated.(Model)
	if !ok {
		t.Fatalf("model type = %T, want Model", updated)
	}
	if polling.messageStatusChatID != 0 {
		t.Fatalf(
			"messageStatusChatID = %d, want 0",
			polling.messageStatusChatID,
		)
	}
	if polling.messageStatusGeneration <= 5 {
		t.Fatalf(
			"messageStatusGeneration = %d, want greater than 5",
			polling.messageStatusGeneration,
		)
	}
	if next := polling.loadMessageStatuses(); next != nil {
		t.Fatal("polling continued after leaving a conversation")
	}
}

// firstReadOf runs the first read of a poll batch and returns its message.
//
// A tick returns one command per source plus the next tick, and the tick
// command sleeps for the interval, so a test must not run the whole batch:
// this runs the read that comes first and leaves the timer alone.
func firstReadOf(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()

	if cmd == nil {
		t.Fatal("no command")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) < 2 {
		t.Fatalf("the poll is not a batch of reads and a tick: %T", cmd())
	}
	if len(batch) < 2 {
		t.Fatal("the poll batch has no next tick")
	}

	return batch[0]()
}

// feedStatusResponse gives the model the answer a status read produced.
//
// Opening a target answers with two things now — the read, and the tick
// that has to start alongside it, because that tick is the only thing that
// makes the read the first of many — so the answer is inside a batch. A
// test that hands the whole batch to Update is handing it something the
// model does not understand, and it would conclude that the source was
// never read.
func feedStatusResponse(
	t *testing.T,
	model Model,
	cmd tea.Cmd,
) Model {
	t.Helper()

	for _, msg := range flattenBatch(t, cmd) {
		updated, _ := model.Update(msg)
		typed, ok := updated.(Model)
		if !ok {
			t.Fatalf("Update returned %T, want Model", updated)
		}
		model = typed
	}

	return model
}

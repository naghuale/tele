package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"telecli/internal/config"
	"telecli/internal/outbox"
	"telecli/internal/telegram"
)

type h5bSession struct {
	sendCalls  atomic.Int32
	closeCalls atomic.Int32
	order      *[]string
	closeErr   error
	live       *telegram.LiveState
	openErr    error
	closeChats atomic.Int32
	viewed     atomic.Int32
}

func (s *h5bSession) GetChats(
	context.Context,
	int,
) (telegram.ChatListSnapshot, error) {
	return telegram.ChatListSnapshot{}, nil
}

func (s *h5bSession) GetChatHistory(
	context.Context,
	telegram.ChatID,
	telegram.MessageID,
	int,
) (telegram.HistoryPage, error) {
	return telegram.HistoryPage{}, nil
}

func (s *h5bSession) SendTextMessage(
	context.Context,
	telegram.ChatID,
	string,
) (telegram.Message, error) {
	s.sendCalls.Add(1)
	return telegram.Message{}, nil
}

func (s *h5bSession) OpenChat(context.Context, telegram.ChatID) error {
	return s.openErr
}

func (s *h5bSession) CloseChat(context.Context, telegram.ChatID) error {
	s.closeChats.Add(1)
	return nil
}

func (s *h5bSession) ViewMessages(
	context.Context,
	telegram.ChatID,
	[]telegram.MessageID,
	telegram.MessageSource,
) error {
	s.viewed.Add(1)
	return nil
}

func (s *h5bSession) LiveState() *telegram.LiveState {
	return s.live
}

func (s *h5bSession) Close(context.Context) error {
	s.closeCalls.Add(1)
	if s.order != nil {
		*s.order = append(*s.order, "session")
	}
	return s.closeErr
}

var _ deliverySession = (*h5bSession)(nil)

type h5bComposerStub struct {
	calls atomic.Int32
}

func (s *h5bComposerStub) SubmitMessage(
	context.Context,
	string,
	int64,
	string,
) (MessageSubmission, error) {
	s.calls.Add(1)
	return MessageSubmission{ID: "entry-1", State: MessageDeliveryQueued}, nil
}

type h5bRuntime struct {
	submitter    ComposerMessageSubmitter
	statusSource MessageStatusSource
	healthSource MessageDeliveryHealthSource
	done         chan struct{}
	err          error
	closeErr     error
	closeOnce    sync.Once
	order        *[]string

	// cancelSubmitter is the durable submitter the action sheet cancels
	// through. It is nil in a runtime that has no queue.
	cancelSubmitter MessageSubmitter
}

func (r *h5bRuntime) Submitter() ComposerMessageSubmitter {
	return r.submitter
}

func (r *h5bRuntime) StatusSource() MessageStatusSource {
	return r.statusSource
}

// PendingMessages reports that this runtime has no queue to read. It is the
// direct-mode answer, and it is what a lifecycle test wants: a screen with
// no pending messages is the state these tests are about.
func (r *h5bRuntime) PendingMessages() PendingMessageSource {
	return nil
}

func (r *h5bRuntime) CancelSubmitter() MessageSubmitter {
	return r.cancelSubmitter
}
func (r *h5bRuntime) HealthSource() MessageDeliveryHealthSource {
	return r.healthSource
}

func (r *h5bRuntime) Done() <-chan struct{} {
	return r.done
}

func (r *h5bRuntime) Err() error {
	return r.err
}

func (r *h5bRuntime) stop() {
	r.closeOnce.Do(func() {
		if r.done != nil {
			close(r.done)
		}
	})
}

func (r *h5bRuntime) Close() error {
	r.closeOnce.Do(func() {
		if r.order != nil {
			*r.order = append(*r.order, "delivery")
		}
		if r.done != nil {
			close(r.done)
		}
	})
	return r.closeErr
}

func h5bConfig(t *testing.T) config.Config {
	t.Helper()
	cfg := config.Default()
	cfg.MessageDelivery.Mode = config.MessageSendModeDirect
	cfg.MessageDelivery.DataDir = t.TempDir()
	cfg.MessageDelivery.DatabaseID = "account-1"
	cfg.MessageDelivery.InstanceID = "instance-1"
	cfg.TDLib.ShutdownTimeoutMS = 1000
	return cfg
}

func TestRunApplicationWiresDirectSubmitterInDirectMode(t *testing.T) {
	t.Parallel()

	cfg := h5bConfig(t)
	session := &h5bSession{}
	submitter := &h5bComposerStub{}
	runtime := &h5bRuntime{submitter: submitter}
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)

	var gotCfg MessageDeliveryRuntimeConfig
	var gotDeps MessageDeliveryRuntimeDeps
	result, err := prepareDeliveryAuthResult(
		ctx,
		cfg,
		session,
		cancel,
		func(
			_ context.Context,
			deliveryCfg MessageDeliveryRuntimeConfig,
			deliveryDeps MessageDeliveryRuntimeDeps,
		) (MessageDeliveryRuntime, error) {
			gotCfg = deliveryCfg
			gotDeps = deliveryDeps
			return runtime, nil
		},
		deliveryHealthSampling{},
		0,
	)
	if err != nil {
		t.Fatalf("prepareDeliveryAuthResult() error = %v", err)
	}
	if result.Submitter == nil {
		t.Fatal("AuthRunResult.Submitter = nil")
	}
	if gotCfg.Mode != config.MessageSendModeDirect {
		t.Fatalf("mode = %q, want direct", gotCfg.Mode)
	}
	if gotDeps.Durable.KeyProvider != nil {
		t.Fatal("direct mode supplied durable key provider")
	}
	if result.MessageStatuses != nil {
		t.Fatal("direct mode supplied a message status source")
	}
	if err := result.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestRunApplicationWiresDurableSubmitterInDurableMode(t *testing.T) {
	t.Parallel()

	cfg := h5bConfig(t)
	cfg.MessageDelivery.Mode = config.MessageSendModeDurable
	session := &h5bSession{}
	statusSource := &h6c2aStubStatusSource{
		listMessageStatuses: func(
			context.Context,
			string,
			int64,
		) ([]MessageStatus, error) {
			return nil, nil
		},
	}
	runtime := &h5bRuntime{
		submitter:    &h5bComposerStub{},
		statusSource: statusSource,
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)

	var gotDeps MessageDeliveryRuntimeDeps
	result, err := prepareDeliveryAuthResult(
		ctx,
		cfg,
		session,
		cancel,
		func(
			_ context.Context,
			_ MessageDeliveryRuntimeConfig,
			deps MessageDeliveryRuntimeDeps,
		) (MessageDeliveryRuntime, error) {
			gotDeps = deps
			return runtime, nil
		},
		deliveryHealthSampling{},
		0,
	)
	if err != nil {
		t.Fatalf("prepareDeliveryAuthResult() error = %v", err)
	}
	if gotDeps.Durable.Session != session {
		t.Fatal("durable session was not forwarded")
	}
	if gotDeps.Durable.KeyProvider == nil {
		t.Fatal("durable key provider is nil")
	}
	if gotDeps.Durable.IDGenerator == nil {
		t.Fatal("durable ID generator is nil")
	}
	if result.Submitter == nil {
		t.Fatal("AuthRunResult.Submitter = nil")
	}
	if result.MessageStatuses == nil {
		t.Fatal("AuthRunResult.MessageStatuses = nil in durable mode")
	}
	statuses, err := result.MessageStatuses.ListMessageStatuses(
		context.Background(),
		"account-1",
		42,
	)
	if err != nil {
		t.Fatalf("ListMessageStatuses() error = %v", err)
	}
	if statuses == nil {
		t.Fatal("ListMessageStatuses() = nil, want non-nil empty slice")
	}
	if err := result.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

// A durable outbox that cannot be opened must not stop the program and
// must not fall back to a direct send. The TUI starts, the submitter
// refuses, and the draft is left for the user.
func TestRunApplicationStartsWithSendingPausedAfterDurableOpenFails(
	t *testing.T,
) {
	t.Parallel()

	cfg := h5bConfig(t)
	cfg.MessageDelivery.Mode = config.MessageSendModeDurable
	session := &h5bSession{}
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	sentinel := fmt.Errorf(
		"%w: no keychain",
		outbox.ErrOutboxKeyAccessDenied,
	)

	result, err := prepareDeliveryAuthResult(
		ctx,
		cfg,
		session,
		cancel,
		func(
			context.Context,
			MessageDeliveryRuntimeConfig,
			MessageDeliveryRuntimeDeps,
		) (MessageDeliveryRuntime, error) {
			return nil, sentinel
		},
		deliveryHealthSampling{},
		0,
	)
	if err != nil {
		t.Fatalf("startup must not fail because the outbox is closed: %v", err)
	}

	// No direct send: the whole point of the outbox is that a message
	// which cannot be queued is not sent by another route.
	if session.sendCalls.Load() != 0 {
		t.Fatalf("direct send calls = %d, want 0", session.sendCalls.Load())
	}

	// The TUI must be usable, so the session stays open.
	if result.Source == nil {
		t.Fatal("Source is nil, want a working TUI source")
	}
	if result.Submitter == nil {
		t.Fatal("Submitter is nil, want a submitter that refuses")
	}
	if result.MessageStatuses != nil {
		t.Fatal("MessageStatuses is non-nil, want nil with no outbox to read")
	}
	if result.SendingPaused == nil {
		t.Fatal("SendingPaused is nil, want the paused state")
	}
	if got := result.SendingPaused.Reason(); got != SendingPausedKeychainLocked {
		t.Fatalf("reason = %v, want keychain locked", got)
	}
	if session.closeCalls.Load() != 0 {
		t.Fatalf("session close calls = %d, want 0 while the TUI runs",
			session.closeCalls.Load())
	}

	// Submitting must fail with the user-facing text and no cause detail.
	if _, err := result.Submitter.SubmitMessage(
		context.Background(), 42, "hello",
	); err == nil {
		t.Fatal("SubmitMessage() error = nil, want the paused error")
	} else {
		text := err.Error()
		for _, want := range []string{
			"Sending paused",
			"Your message was not sent and is still here",
			"Unlock your Keychain",
			"Details: ",
		} {
			if !strings.Contains(text, want) {
				t.Fatalf("paused text %q does not contain %q", text, want)
			}
		}
		for _, forbidden := range []string{"no keychain", "ErrOutboxKey"} {
			if strings.Contains(text, forbidden) {
				t.Fatalf("paused text leaks the cause %q: %s", forbidden, text)
			}
		}
	}
}

// The composer draft survives a refused submission, and nothing is
// queued.
func TestRunApplicationPausedSubmitterPreservesDraft(t *testing.T) {
	t.Parallel()

	cfg := h5bConfig(t)
	cfg.MessageDelivery.Mode = config.MessageSendModeDurable
	session := &h5bSession{}
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)

	result, err := prepareDeliveryAuthResult(
		ctx,
		cfg,
		session,
		cancel,
		func(
			context.Context,
			MessageDeliveryRuntimeConfig,
			MessageDeliveryRuntimeDeps,
		) (MessageDeliveryRuntime, error) {
			return nil, errors.New("outbox unavailable")
		},
		deliveryHealthSampling{},
		0,
	)
	if err != nil {
		t.Fatalf("startup must not fail: %v", err)
	}
	if result.SendingPaused == nil {
		t.Fatal("SendingPaused is nil, want the paused state")
	}
	if got := result.SendingPaused.Reason(); got != SendingPausedOther {
		t.Fatalf("reason = %v, want other", got)
	}
	if session.sendCalls.Load() != 0 {
		t.Fatalf("direct send calls = %d, want 0", session.sendCalls.Load())
	}
}

func TestRunApplicationReturnsDispatcherFailure(t *testing.T) {
	t.Parallel()

	cfg := h5bConfig(t)
	cfg.MessageDelivery.Mode = config.MessageSendModeDurable
	session := &h5bSession{}
	dispatcherErr := errors.New("dispatcher failed")
	done := make(chan struct{})
	runtime := &h5bRuntime{
		submitter: &h5bComposerStub{},
		done:      done,
		err:       dispatcherErr,
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)

	result, err := prepareDeliveryAuthResult(
		ctx,
		cfg,
		session,
		cancel,
		func(
			context.Context,
			MessageDeliveryRuntimeConfig,
			MessageDeliveryRuntimeDeps,
		) (MessageDeliveryRuntime, error) {
			return runtime, nil
		},
		deliveryHealthSampling{},
		0,
	)
	if err != nil {
		t.Fatalf("prepareDeliveryAuthResult() error = %v", err)
	}
	runtime.stop()
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("dispatcher failure did not cancel application context")
	}
	cause := context.Cause(ctx)
	if !errors.Is(cause, dispatcherErr) {
		t.Fatalf("cause = %v, want dispatcher error", cause)
	}
	if err := result.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestRunApplicationClosesDeliveryBeforeSession(t *testing.T) {
	t.Parallel()

	cfg := h5bConfig(t)
	var order []string
	session := &h5bSession{order: &order}
	runtime := &h5bRuntime{
		submitter: &h5bComposerStub{},
		order:     &order,
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	result, err := prepareDeliveryAuthResult(
		ctx,
		cfg,
		session,
		cancel,
		func(
			context.Context,
			MessageDeliveryRuntimeConfig,
			MessageDeliveryRuntimeDeps,
		) (MessageDeliveryRuntime, error) {
			return runtime, nil
		},
		deliveryHealthSampling{},
		0,
	)
	if err != nil {
		t.Fatalf("prepareDeliveryAuthResult() error = %v", err)
	}
	if err := result.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if len(order) != 2 || order[0] != "delivery" || order[1] != "session" {
		t.Fatalf("close order = %v, want [delivery session]", order)
	}
}

func TestRunApplicationDoesNotCancelForNormalClose(t *testing.T) {
	t.Parallel()

	cfg := h5bConfig(t)
	session := &h5bSession{}
	done := make(chan struct{})
	runtime := &h5bRuntime{
		submitter: &h5bComposerStub{},
		done:      done,
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	result, err := prepareDeliveryAuthResult(
		ctx,
		cfg,
		session,
		cancel,
		func(
			context.Context,
			MessageDeliveryRuntimeConfig,
			MessageDeliveryRuntimeDeps,
		) (MessageDeliveryRuntime, error) {
			return runtime, nil
		},
		deliveryHealthSampling{},
		0,
	)
	if err != nil {
		t.Fatalf("prepareDeliveryAuthResult() error = %v", err)
	}
	if err := result.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	select {
	case <-ctx.Done():
		t.Fatalf("context canceled after normal close: %v", context.Cause(ctx))
	case <-time.After(50 * time.Millisecond):
	}
}

// The sheet of §13 can only cancel a message if the result carries a way to
// do it, and a runtime without a durable submitter has nothing to cancel.
func TestTheAuthResultCarriesACancellerOnlyWithAQueue(t *testing.T) {
	t.Parallel()

	queued := &recordingMessageSubmitter{}
	durable := &h5bRuntime{
		submitter:       &h5bComposerStub{},
		cancelSubmitter: queued,
	}
	direct := &h5bRuntime{submitter: &h5bComposerStub{}}

	withQueue, err := prepareDeliveryAuthResult(
		context.Background(),
		h5bConfig(t),
		&h5bSession{},
		func(error) {},
		func(
			context.Context,
			MessageDeliveryRuntimeConfig,
			MessageDeliveryRuntimeDeps,
		) (MessageDeliveryRuntime, error) {
			return durable, nil
		},
		deliveryHealthSampling{},
		0,
	)
	if err != nil {
		t.Fatalf("prepareDeliveryAuthResult() error = %v", err)
	}
	if withQueue.MessageCanceller == nil {
		t.Fatal("a runtime with a queue carries no canceller")
	}
	if err := withQueue.MessageCanceller.CancelMessage(
		context.Background(),
		"entry-1",
		3,
	); err != nil {
		t.Fatalf("CancelMessage: %v", err)
	}
	if queued.canceled != 1 || queued.canceledVersion != 3 {
		t.Fatalf("cancel = %d at version %d, want one at version 3",
			queued.canceled,
			queued.canceledVersion,
		)
	}
	if err := withQueue.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	withoutQueue, err := prepareDeliveryAuthResult(
		context.Background(),
		h5bConfig(t),
		&h5bSession{},
		func(error) {},
		func(
			context.Context,
			MessageDeliveryRuntimeConfig,
			MessageDeliveryRuntimeDeps,
		) (MessageDeliveryRuntime, error) {
			return direct, nil
		},
		deliveryHealthSampling{},
		0,
	)
	if err != nil {
		t.Fatalf("prepareDeliveryAuthResult() error = %v", err)
	}
	if withoutQueue.MessageCanceller != nil {
		t.Fatal("a runtime with no queue carries a canceller")
	}
	if err := withoutQueue.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

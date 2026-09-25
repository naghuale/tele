package application

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"telecli/internal/config"
	"telecli/internal/outbox"
)

type h4RecordingComposerSubmitter struct {
	calls atomic.Int64
}

func (s *h4RecordingComposerSubmitter) SubmitMessage(
	context.Context,
	string,
	int64,
	string,
) (MessageSubmission, error) {
	s.calls.Add(1)
	return MessageSubmission{
		ID:    "direct-message",
		State: MessageDeliverySent,
	}, nil
}

type h4StubRuntime struct {
	submitter    ComposerMessageSubmitter
	statusSource MessageStatusSource
	done         <-chan struct{}
	err          error
	closeErr     error
}

func (r *h4StubRuntime) Submitter() ComposerMessageSubmitter {
	return r.submitter
}

func (r *h4StubRuntime) StatusSource() MessageStatusSource {
	return r.statusSource
}

func (r *h4StubRuntime) Done() <-chan struct{} {
	return r.done
}

func (r *h4StubRuntime) Err() error {
	return r.err
}

func (r *h4StubRuntime) Close() error {
	return r.closeErr
}

func h4FactoryWithRuntime(
	runtime MessageDeliveryRuntime,
	calls *atomic.Int64,
) messageDeliveryRuntimeFactory {
	return messageDeliveryRuntimeFactory{
		openDurable: func(
			context.Context,
			DurableOutboxRuntimeConfig,
			DurableOutboxRuntimeDeps,
		) (MessageDeliveryRuntime, error) {
			if calls != nil {
				calls.Add(1)
			}
			return runtime, nil
		},
	}
}

func TestOpenMessageDeliveryRuntimeSelectsDirectMode(t *testing.T) {
	t.Parallel()

	submitter := &h4RecordingComposerSubmitter{}
	var durableCalls atomic.Int64
	runtime, err := openMessageDeliveryRuntime(
		context.Background(),
		MessageDeliveryRuntimeConfig{Mode: config.MessageSendModeDirect},
		MessageDeliveryRuntimeDeps{DirectSubmitter: submitter},
		h4FactoryWithRuntime(nil, &durableCalls),
	)
	if err != nil {
		t.Fatalf("openMessageDeliveryRuntime() error = %v", err)
	}
	direct, ok := runtime.(*DirectMessageDeliveryRuntime)
	if !ok {
		t.Fatalf("runtime type = %T, want *DirectMessageDeliveryRuntime", runtime)
	}
	if direct.Submitter() != submitter {
		t.Fatal("direct runtime did not retain submitter")
	}
	if durableCalls.Load() != 0 {
		t.Fatalf("durable factory calls = %d, want 0", durableCalls.Load())
	}
}

func TestOpenMessageDeliveryRuntimeSelectsDurableMode(t *testing.T) {
	t.Parallel()

	submitter := &h4RecordingComposerSubmitter{}
	stub := &h4StubRuntime{submitter: submitter}
	var durableCalls atomic.Int64
	runtime, err := openMessageDeliveryRuntime(
		context.Background(),
		MessageDeliveryRuntimeConfig{Mode: config.MessageSendModeDurable},
		MessageDeliveryRuntimeDeps{},
		h4FactoryWithRuntime(stub, &durableCalls),
	)
	if err != nil {
		t.Fatalf("openMessageDeliveryRuntime() error = %v", err)
	}
	if runtime != stub {
		t.Fatalf("runtime = %#v, want factory runtime", runtime)
	}
	if durableCalls.Load() != 1 {
		t.Fatalf("durable factory calls = %d, want 1", durableCalls.Load())
	}
}

func TestOpenMessageDeliveryRuntimeDirectModeHasNoStatusSource(t *testing.T) {
	t.Parallel()

	runtime, err := openMessageDeliveryRuntime(
		context.Background(),
		MessageDeliveryRuntimeConfig{Mode: config.MessageSendModeDirect},
		MessageDeliveryRuntimeDeps{
			DirectSubmitter: &h4RecordingComposerSubmitter{},
		},
		h4FactoryWithRuntime(nil, nil),
	)
	if err != nil {
		t.Fatalf("openMessageDeliveryRuntime() error = %v", err)
	}
	if source := runtime.StatusSource(); source != nil {
		t.Fatalf("StatusSource() = %#v, want nil", source)
	}
}

func TestOpenMessageDeliveryRuntimeExposesDurableStatusSource(t *testing.T) {
	t.Parallel()

	reader := &h6bFakeEntryStatusReader{}
	source, err := NewOutboxMessageStatusSource(reader, 0)
	if err != nil {
		t.Fatalf("NewOutboxMessageStatusSource() error = %v", err)
	}
	stub := &h4StubRuntime{
		submitter:    &h4RecordingComposerSubmitter{},
		statusSource: source,
	}

	runtime, err := openMessageDeliveryRuntime(
		context.Background(),
		MessageDeliveryRuntimeConfig{Mode: config.MessageSendModeDurable},
		MessageDeliveryRuntimeDeps{},
		h4FactoryWithRuntime(stub, nil),
	)
	if err != nil {
		t.Fatalf("openMessageDeliveryRuntime() error = %v", err)
	}
	if runtime.StatusSource() != source {
		t.Fatal("StatusSource() did not return durable status source")
	}
}

func TestOpenMessageDeliveryRuntimeRejectsUnknownMode(t *testing.T) {
	t.Parallel()

	_, err := openMessageDeliveryRuntime(
		context.Background(),
		MessageDeliveryRuntimeConfig{Mode: config.MessageSendMode("automatic")},
		MessageDeliveryRuntimeDeps{},
		productionMessageDeliveryRuntimeFactory(),
	)
	if err == nil {
		t.Fatal("openMessageDeliveryRuntime() error = nil, want non-nil")
	}
}

func TestOpenMessageDeliveryRuntimePropagatesCanceledContext(t *testing.T) {
	t.Parallel()

	var durableCalls atomic.Int64
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := openMessageDeliveryRuntime(
		ctx,
		MessageDeliveryRuntimeConfig{Mode: config.MessageSendModeDirect},
		MessageDeliveryRuntimeDeps{DirectSubmitter: &h4RecordingComposerSubmitter{}},
		h4FactoryWithRuntime(nil, &durableCalls),
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if durableCalls.Load() != 0 {
		t.Fatalf("durable factory calls = %d, want 0", durableCalls.Load())
	}
}

func TestOpenMessageDeliveryRuntimeRejectsZeroMode(t *testing.T) {
	t.Parallel()

	_, err := openMessageDeliveryRuntime(
		context.Background(),
		MessageDeliveryRuntimeConfig{},
		MessageDeliveryRuntimeDeps{DirectSubmitter: &h4RecordingComposerSubmitter{}},
		productionMessageDeliveryRuntimeFactory(),
	)
	if err == nil {
		t.Fatal("openMessageDeliveryRuntime() error = nil, want non-nil")
	}
}

func TestDirectMessageDeliveryRuntimeIsPassive(t *testing.T) {
	t.Parallel()

	submitter := &h4RecordingComposerSubmitter{}
	runtime, err := NewDirectMessageDeliveryRuntime(submitter)
	if err != nil {
		t.Fatalf("NewDirectMessageDeliveryRuntime() error = %v", err)
	}
	if runtime.Submitter() != submitter {
		t.Fatal("Submitter() did not return configured submitter")
	}
	if runtime.Done() != nil {
		t.Fatal("Done() != nil")
	}
	if runtime.Err() != nil {
		t.Fatal("Err() != nil")
	}
	if err := runtime.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestOpenMessageDeliveryRuntimeDoesNotOpenDurableRuntimeInDirectMode(t *testing.T) {
	t.Parallel()

	var durableCalls atomic.Int64
	_, err := openMessageDeliveryRuntime(
		context.Background(),
		MessageDeliveryRuntimeConfig{Mode: config.MessageSendModeDirect},
		MessageDeliveryRuntimeDeps{DirectSubmitter: &h4RecordingComposerSubmitter{}},
		h4FactoryWithRuntime(nil, &durableCalls),
	)
	if err != nil {
		t.Fatalf("openMessageDeliveryRuntime() error = %v", err)
	}
	if durableCalls.Load() != 0 {
		t.Fatalf("durable factory calls = %d, want 0", durableCalls.Load())
	}
}

func TestOpenMessageDeliveryRuntimeRejectsNilDirectSubmitter(t *testing.T) {
	t.Parallel()

	_, err := openMessageDeliveryRuntime(
		context.Background(),
		MessageDeliveryRuntimeConfig{Mode: config.MessageSendModeDirect},
		MessageDeliveryRuntimeDeps{},
		productionMessageDeliveryRuntimeFactory(),
	)
	if err == nil {
		t.Fatal("openMessageDeliveryRuntime() error = nil, want non-nil")
	}
}

func TestOpenMessageDeliveryRuntimeDoesNotRequireDirectSubmitterInDurableMode(t *testing.T) {
	t.Parallel()

	stub := &h4StubRuntime{}
	var durableCalls atomic.Int64
	_, err := openMessageDeliveryRuntime(
		context.Background(),
		MessageDeliveryRuntimeConfig{Mode: config.MessageSendModeDurable},
		MessageDeliveryRuntimeDeps{},
		h4FactoryWithRuntime(stub, &durableCalls),
	)
	if err != nil {
		t.Fatalf("openMessageDeliveryRuntime() error = %v", err)
	}
	if durableCalls.Load() != 1 {
		t.Fatalf("durable factory calls = %d, want 1", durableCalls.Load())
	}
}

func TestOpenMessageDeliveryRuntimeDoesNotUseDirectSubmitterInDurableMode(t *testing.T) {
	t.Parallel()

	direct := &h4RecordingComposerSubmitter{}
	stub := &h4StubRuntime{}
	var durableCalls atomic.Int64
	_, err := openMessageDeliveryRuntime(
		context.Background(),
		MessageDeliveryRuntimeConfig{Mode: config.MessageSendModeDurable},
		MessageDeliveryRuntimeDeps{DirectSubmitter: direct},
		h4FactoryWithRuntime(stub, &durableCalls),
	)
	if err != nil {
		t.Fatalf("openMessageDeliveryRuntime() error = %v", err)
	}
	if direct.calls.Load() != 0 {
		t.Fatalf("direct calls = %d, want 0", direct.calls.Load())
	}
}

func TestOpenMessageDeliveryRuntimeDoesNotFallbackAfterKeyProviderFailure(t *testing.T) {
	t.Parallel()

	sentinel := outbox.ErrOutboxKeyUnavailable
	direct := &h4RecordingComposerSubmitter{}
	factory := messageDeliveryRuntimeFactory{
		openDurable: func(
			context.Context,
			DurableOutboxRuntimeConfig,
			DurableOutboxRuntimeDeps,
		) (MessageDeliveryRuntime, error) {
			return nil, sentinel
		},
	}
	runtime, err := openMessageDeliveryRuntime(
		context.Background(),
		MessageDeliveryRuntimeConfig{Mode: config.MessageSendModeDurable},
		MessageDeliveryRuntimeDeps{DirectSubmitter: direct},
		factory,
	)
	if runtime != nil {
		t.Fatalf("runtime = %#v, want nil", runtime)
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("error = %v, want wrapped sentinel", err)
	}
	if direct.calls.Load() != 0 {
		t.Fatalf("direct calls = %d, want 0", direct.calls.Load())
	}
}

func TestOpenMessageDeliveryRuntimeDoesNotFallbackAfterStoreFailure(t *testing.T) {
	t.Parallel()

	sentinel := outbox.ErrOutboxDataDirInvalid
	direct := &h4RecordingComposerSubmitter{}
	factory := messageDeliveryRuntimeFactory{
		openDurable: func(
			context.Context,
			DurableOutboxRuntimeConfig,
			DurableOutboxRuntimeDeps,
		) (MessageDeliveryRuntime, error) {
			return nil, sentinel
		},
	}
	runtime, err := openMessageDeliveryRuntime(
		context.Background(),
		MessageDeliveryRuntimeConfig{Mode: config.MessageSendModeDurable},
		MessageDeliveryRuntimeDeps{DirectSubmitter: direct},
		factory,
	)
	if runtime != nil {
		t.Fatalf("runtime = %#v, want nil", runtime)
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("error = %v, want wrapped sentinel", err)
	}
	if direct.calls.Load() != 0 {
		t.Fatalf("direct calls = %d, want 0", direct.calls.Load())
	}
}

func TestOpenMessageDeliveryRuntimePreservesDurableErrorSentinel(t *testing.T) {
	t.Parallel()

	sentinel := outbox.ErrOutboxInconsistentInit
	factory := messageDeliveryRuntimeFactory{
		openDurable: func(
			context.Context,
			DurableOutboxRuntimeConfig,
			DurableOutboxRuntimeDeps,
		) (MessageDeliveryRuntime, error) {
			return nil, sentinel
		},
	}
	_, err := openMessageDeliveryRuntime(
		context.Background(),
		MessageDeliveryRuntimeConfig{Mode: config.MessageSendModeDurable},
		MessageDeliveryRuntimeDeps{},
		factory,
	)
	if !errors.Is(err, sentinel) {
		t.Fatalf("error = %v, want wrapped sentinel", err)
	}
}

func TestOpenMessageDeliveryRuntimeRejectsNilDurableRuntime(t *testing.T) {
	t.Parallel()

	factory := h4FactoryWithRuntime(nil, nil)
	_, err := openMessageDeliveryRuntime(
		context.Background(),
		MessageDeliveryRuntimeConfig{Mode: config.MessageSendModeDurable},
		MessageDeliveryRuntimeDeps{},
		factory,
	)
	if err == nil {
		t.Fatal("openMessageDeliveryRuntime() error = nil, want non-nil")
	}
}

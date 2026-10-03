package telegram

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"telecli/internal/telemetry/recorder"
)

type receiveResult struct {
	raw []byte
	err error
}

type fakeNative struct {
	nextID         atomic.Int64
	receive        chan receiveResult
	sendCalls      atomic.Int64
	closeCalls     atomic.Int64
	executeMu      sync.Mutex
	executeResults map[string][]byte
	closed         atomic.Bool
}

func newFakeNative() *fakeNative {
	f := &fakeNative{receive: make(chan receiveResult, 16), executeResults: make(map[string][]byte)}
	f.nextID.Store(100)
	return f
}
func (f *fakeNative) CreateClientID() (int, error) { return int(f.nextID.Add(1)), nil }
func (f *fakeNative) Send(int, []byte) error       { f.sendCalls.Add(1); return nil }
func (f *fakeNative) Receive(timeout time.Duration) ([]byte, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case result := <-f.receive:
		return result.raw, result.err
	case <-timer.C:
		return nil, nil
	}
}
func (f *fakeNative) Execute(request []byte) ([]byte, error) {
	f.executeMu.Lock()
	defer f.executeMu.Unlock()
	if result, ok := f.executeResults[string(request)]; ok {
		return append([]byte(nil), result...), nil
	}
	// Runtime.Start names the library's journal and lowers its verbosity
	// before anything else; TDLib answers ok to both.
	switch requestType(request) {
	case "setLogStream", "setLogVerbosityLevel":
		return []byte(`{"@type":"ok"}`), nil
	}
	return nil, nil
}
func (f *fakeNative) Close() error { f.closeCalls.Add(1); f.closed.Store(true); return nil }

func newTestRuntime(t *testing.T, native Native) *Runtime {
	t.Helper()
	cfg := DefaultConfig()
	cfg.ReceiveTimeout = time.Millisecond
	runtime, err := NewRuntime(cfg, native, recorder.NewNoop())
	if err != nil {
		t.Fatal(err)
	}
	return runtime
}

func TestRuntimeStartsSingleReceiveLoop(t *testing.T) {
	r := newTestRuntime(t, newFakeNative())
	if err := r.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := r.Start(context.Background()); !errors.Is(err, ErrAlreadyStarted) {
		t.Fatalf("second Start error = %v", err)
	}
	if err := r.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestReceiveLoopRoutesAndSequencesMessages(t *testing.T) {
	native := newFakeNative()
	r := newTestRuntime(t, native)
	if err := r.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	client, err := r.NewClient()
	if err != nil {
		t.Fatal(err)
	}
	native.receive <- receiveResult{raw: []byte(`{"@client_id":101,"@type":"a"}`)}
	native.receive <- receiveResult{raw: []byte(`{"@client_id":101,"@type":"b"}`)}
	first := <-client.Updates()
	second := <-client.Updates()
	if first.Sequence != 1 || second.Sequence != 2 {
		t.Fatalf("sequences = %d,%d", first.Sequence, second.Sequence)
	}
	if string(first.Raw) == string(second.Raw) {
		t.Fatal("messages unexpectedly identical")
	}
	if err := r.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestReceiveLoopCopiesRawPayload(t *testing.T) {
	native := newFakeNative()
	r := newTestRuntime(t, native)
	if err := r.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	client, err := r.NewClient()
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"@client_id":101,"@type":"x"}`)
	native.receive <- receiveResult{raw: raw}
	update := <-client.Updates()
	raw[0] = 'X'
	if update.Raw[0] != '{' {
		t.Fatal("update payload aliases native buffer")
	}
	_ = r.Close(context.Background())
}

func TestCloseIsIdempotentAndStopsLoop(t *testing.T) {
	native := newFakeNative()
	r := newTestRuntime(t, native)
	if err := r.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	client, err := r.NewClient()
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if native.closeCalls.Load() != 1 {
		t.Fatalf("Close calls = %d", native.closeCalls.Load())
	}
	if _, ok := <-client.Updates(); ok {
		t.Fatal("updates channel remains open")
	}
}

func TestCloseBeforeStart(t *testing.T) {
	native := newFakeNative()
	r := newTestRuntime(t, native)
	if err := r.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r.State() != LifecycleClosed {
		t.Fatalf("state = %s", r.State())
	}
}

func TestInvalidConfigRejected(t *testing.T) {
	_, err := NewRuntime(Config{}, newFakeNative(), recorder.NewNoop())
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("error = %v", err)
	}
}

func TestConcurrentCloseDoesNotPanic(t *testing.T) {
	native := newFakeNative()
	r := newTestRuntime(t, native)
	if err := r.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = r.Close(context.Background()) }()
	}
	wg.Wait()
	if native.closeCalls.Load() != 1 {
		t.Fatalf("Close calls = %d", native.closeCalls.Load())
	}
}

func TestStartAfterCloseReturnsClosed(t *testing.T) {
	runtime := newTestRuntime(t, newFakeNative())
	if err := runtime.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Start(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("Start() error = %v, want ErrClosed", err)
	}
}

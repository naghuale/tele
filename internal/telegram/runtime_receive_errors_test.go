package telegram

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// failingReceiveNative fails every Receive immediately, the way a broken
// native bridge would.
type failingReceiveNative struct {
	*fakeNative
	calls atomic.Int64
}

func (n *failingReceiveNative) Receive(time.Duration) ([]byte, error) {
	n.calls.Add(1)
	return nil, errors.New("receive failed")
}

// TestReceiveLoopBacksOffOnPersistentErrors pins that a failing Receive
// does not turn the loop into a busy spin.
func TestReceiveLoopBacksOffOnPersistentErrors(t *testing.T) {
	native := &failingReceiveNative{fakeNative: newFakeNative()}
	r := newTestRuntime(t, native)
	r.receiveErrorLimit = 1 << 30
	if err := r.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if err := r.Close(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Without backoff the loop calls Receive hundreds of thousands of
	// times in 200ms; with a 10ms initial delay doubling up to 1s it is a
	// handful.
	if calls := native.calls.Load(); calls > 20 {
		t.Fatalf("Receive calls in 200ms = %d, want a backed-off loop", calls)
	}
}

// TestReceiveLoopFailsAfterConsecutiveErrors pins that a runtime whose
// native bridge keeps failing reaches the failed state, closes its client
// channels so owners stop waiting, and rejects new requests.
func TestReceiveLoopFailsAfterConsecutiveErrors(t *testing.T) {
	native := &failingReceiveNative{fakeNative: newFakeNative()}
	r := newTestRuntime(t, native)
	r.receiveErrorBackoffMin = time.Microsecond
	r.receiveErrorBackoffMax = time.Microsecond
	r.receiveErrorLimit = 3
	if err := r.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	client, err := r.NewClient()
	if err != nil {
		t.Fatal(err)
	}

	deadline := time.After(2 * time.Second)
	for open := true; open; {
		select {
		case _, open = <-client.Updates():
		case <-deadline:
			t.Fatal("client updates were not closed after repeated receive errors")
		}
	}

	if state := r.State(); state != LifecycleFailed {
		t.Fatalf("state = %s, want failed", state)
	}
	if got := native.calls.Load(); got != 3 {
		t.Fatalf("Receive calls = %d, want 3", got)
	}
	if err := r.Send(client.ID(), RawMessage(`{"@type":"close"}`)); err == nil {
		t.Fatal("Send succeeded on a failed runtime")
	}
	if err := r.Close(context.Background()); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	if !native.closed.Load() {
		t.Fatal("native library was not closed")
	}
	if state := r.State(); state != LifecycleClosed {
		t.Fatalf("state after Close = %s, want closed", state)
	}
}

// flakyReceiveNative fails a fixed number of times, then delivers
// nothing.
type flakyReceiveNative struct {
	*fakeNative
	failures atomic.Int64
}

func (n *flakyReceiveNative) Receive(timeout time.Duration) ([]byte, error) {
	if n.failures.Add(-1) >= 0 {
		return nil, errors.New("transient")
	}
	return n.fakeNative.Receive(timeout)
}

// TestReceiveLoopResetsErrorCountAfterSuccess pins that only consecutive
// errors count toward the failure limit.
func TestReceiveLoopResetsErrorCountAfterSuccess(t *testing.T) {
	native := &flakyReceiveNative{fakeNative: newFakeNative()}
	native.failures.Store(2)
	r := newTestRuntime(t, native)
	r.receiveErrorBackoffMin = time.Microsecond
	r.receiveErrorBackoffMax = time.Microsecond
	r.receiveErrorLimit = 3
	if err := r.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	time.Sleep(50 * time.Millisecond)
	native.failures.Store(2)
	time.Sleep(50 * time.Millisecond)

	if state := r.State(); state != LifecycleRunning {
		t.Fatalf("state = %s, want running", state)
	}
	if err := r.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

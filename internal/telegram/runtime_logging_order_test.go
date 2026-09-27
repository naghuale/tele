package telegram

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// orderNative records whether TDLib was asked to receive before its log
// verbosity was lowered. td_receive itself writes log lines at the
// default verbosity, so any receive before that is a leak window.
type orderNative struct {
	*recordingNative
	mu       sync.Mutex
	received bool
	lowered  bool
	early    bool
}

func (n *orderNative) Execute(request []byte) ([]byte, error) {
	response, err := n.recordingNative.Execute(request)
	if err == nil && requestType(request) == "setLogVerbosityLevel" {
		n.mu.Lock()
		n.lowered = true
		n.mu.Unlock()
	}
	return response, err
}

func (n *orderNative) Receive(timeout time.Duration) ([]byte, error) {
	n.mu.Lock()
	if !n.lowered {
		n.early = true
	}
	n.received = true
	n.mu.Unlock()
	return n.recordingNative.Receive(timeout)
}

// TestRuntimeStartLowersVerbosityBeforeFirstReceive pins that the receive
// loop never calls td_receive while TDLib still logs at its default
// verbosity.
func TestRuntimeStartLowersVerbosityBeforeFirstReceive(t *testing.T) {
	native := &orderNative{recordingNative: newRecordingNative()}
	r := newTestRuntime(t, native)

	if err := r.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(2 * time.Second)
	for {
		native.mu.Lock()
		received := native.received
		native.mu.Unlock()
		if received {
			break
		}
		select {
		case <-deadline:
			t.Fatal("receive loop never ran")
		case <-time.After(time.Millisecond):
		}
	}
	if err := r.Close(context.Background()); err != nil {
		t.Fatal(err)
	}

	native.mu.Lock()
	defer native.mu.Unlock()
	if native.early {
		t.Fatal("td_receive ran before the log verbosity was lowered")
	}
}

// TestRuntimeStartFailsClosedWhenVerbosityIsRejected pins that a runtime
// whose verbosity cannot be lowered never starts receiving.
func TestRuntimeStartFailsClosedWhenVerbosityIsRejected(t *testing.T) {
	inner := newRecordingNative()
	inner.executeResponse = []byte(`{"@type":"error","code":400,"message":"rejected"}`)
	native := &orderNative{recordingNative: inner}
	r := newTestRuntime(t, native)

	if err := r.Start(context.Background()); !errors.Is(err, ErrTDLibLogConfiguration) {
		t.Fatalf("Start error = %v, want ErrTDLibLogConfiguration", err)
	}
	time.Sleep(20 * time.Millisecond)

	native.mu.Lock()
	received := native.received
	native.mu.Unlock()
	if received {
		t.Fatal("receive loop started although logging could not be secured")
	}
	if state := r.State(); state == LifecycleRunning {
		t.Fatal("runtime is running although logging could not be secured")
	}
	if err := r.Close(context.Background()); err != nil {
		t.Fatalf("Close error = %v", err)
	}
}

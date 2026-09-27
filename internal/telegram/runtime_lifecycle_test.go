package telegram

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"telecli/internal/telemetry/recorder"
)

// slowReceiveNative blocks every Receive for the whole timeout, the way
// td_receive does when TDLib has nothing to deliver.
type slowReceiveNative struct {
	*fakeNative
	entered chan struct{}
	once    sync.Once
}

func (n *slowReceiveNative) Receive(timeout time.Duration) ([]byte, error) {
	n.once.Do(func() { close(n.entered) })
	time.Sleep(timeout)
	return nil, nil
}

// TestCloseCompletesAfterCallerTimeout pins that a caller deadline bounds
// only the wait. The close sequence keeps running, the native library is
// still closed, and a later Close reports the real outcome instead of the
// first caller's deadline.
func TestCloseCompletesAfterCallerTimeout(t *testing.T) {
	native := &slowReceiveNative{
		fakeNative: newFakeNative(),
		entered:    make(chan struct{}),
	}
	cfg := DefaultConfig()
	cfg.ReceiveTimeout = 100 * time.Millisecond
	r, err := NewRuntime(cfg, native, recorder.NewNoop())
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The loop is inside a blocking Receive, so the close sequence
	// cannot finish before the short caller deadline.
	<-native.entered

	short, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if err := r.Close(short); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("first Close error = %v, want DeadlineExceeded", err)
	}

	wait, cancelWait := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelWait()
	if err := r.Close(wait); err != nil {
		t.Fatalf("second Close error = %v, want nil", err)
	}
	if !native.closed.Load() {
		t.Fatal("native library was never closed")
	}
	if got := native.closeCalls.Load(); got != 1 {
		t.Fatalf("native Close calls = %d, want 1", got)
	}
	if state := r.State(); state != LifecycleClosed {
		t.Fatalf("state = %s, want closed", state)
	}
}

// TestConcurrentStartAndCloseBeforeStartDoNotPanic pins that Start and
// Close racing from the created state never close the done channel
// twice and never leave a receive loop running on a closed library.
func TestConcurrentStartAndCloseBeforeStartDoNotPanic(t *testing.T) {
	for i := 0; i < 200; i++ {
		native := newFakeNative()
		r := newTestRuntime(t, native)

		var wg sync.WaitGroup
		start := make(chan struct{})
		var startErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			startErr = r.Start(context.Background())
		}()
		go func() {
			defer wg.Done()
			<-start
			_ = r.Close(context.Background())
		}()
		close(start)
		wg.Wait()

		if startErr != nil && !errors.Is(startErr, ErrClosed) {
			t.Fatalf("Start error = %v, want nil or ErrClosed", startErr)
		}
		if err := r.Close(context.Background()); err != nil {
			t.Fatalf("Close error = %v", err)
		}
		if state := r.State(); state != LifecycleClosed {
			t.Fatalf("state = %s, want closed", state)
		}
		if got := native.closeCalls.Load(); got != 1 {
			t.Fatalf("native Close calls = %d, want 1", got)
		}
	}
}

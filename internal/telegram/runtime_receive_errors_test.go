package telegram

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// errReceiveFailed is what the natives of this file hand to the loop.
var errReceiveFailed = errors.New("receive failed")

// failingReceiveNative fails every Receive immediately, the way a broken
// native bridge would.
type failingReceiveNative struct {
	*fakeNative
	calls atomic.Int64
}

func (n *failingReceiveNative) Receive(time.Duration) ([]byte, error) {
	n.calls.Add(1)

	return nil, errReceiveFailed
}

// gatedFailingReceiveNative fails the Receives the test has released and
// parks in the ones it has not.
//
// The gate is what makes a test that counts receive failures
// deterministic. A loop that fails a microsecond after Start can reach its
// failure limit before the test has made its first call, and then the test
// fails on something it never meant to check: it wanted to see what a
// failed runtime does to a client, and it saw NewClient refuse one. A
// parked Receive cannot consume the limit, so the order the test writes
// down is the order that happens.
type gatedFailingReceiveNative struct {
	*fakeNative
	calls   atomic.Int64
	gate    chan struct{}
	entered chan struct{}
	once    sync.Once
}

func newGatedFailingReceiveNative() *gatedFailingReceiveNative {
	return &gatedFailingReceiveNative{
		fakeNative: newFakeNative(),
		gate:       make(chan struct{}),
		entered:    make(chan struct{}, 1),
	}
}

func (n *gatedFailingReceiveNative) Receive(time.Duration) ([]byte, error) {
	n.calls.Add(1)

	select {
	case n.entered <- struct{}{}:
	default:
	}

	<-n.gate

	return nil, errReceiveFailed
}

// releaseErrors lets every Receive from now on fail without waiting. It
// is safe to call twice, so a test can release and still have a cleanup
// that does.
func (n *gatedFailingReceiveNative) releaseErrors() { n.once.Do(func() { close(n.gate) }) }

// waitUntilEntered reports when the loop is inside Receive, waiting for a
// failure the test has not released.
func (n *gatedFailingReceiveNative) waitUntilEntered(t *testing.T) {
	t.Helper()

	select {
	case <-n.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the receive loop did not call Receive within 5s")
	}
}

// TestTheFailingReceiveLoopWaitsForTheTest pins the property the failure
// test below is built on: a native that holds its failures keeps the loop
// parked in Receive, so a client created after Start is never refused
// because of errors the test has not handed out yet.
//
// It is the mutation the flake was: releasing the failures before
// NewClient is what failed on CI, and it fails here every time.
func TestTheFailingReceiveLoopWaitsForTheTest(t *testing.T) {
	native := newGatedFailingReceiveNative()
	r := newTestRuntime(t, native)
	r.receiveErrorBackoffMin = time.Microsecond
	r.receiveErrorBackoffMax = time.Microsecond
	r.receiveErrorLimit = 3
	if err := r.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		native.releaseErrors()
		_ = r.Close(context.Background())
	})

	native.waitUntilEntered(t)

	client, err := r.NewClient()
	if err != nil {
		t.Fatalf("NewClient while the loop holds its failures: %v", err)
	}
	if state := r.State(); state != LifecycleRunning {
		t.Fatalf("state = %s, want running while the loop is parked", state)
	}
	if calls := native.calls.Load(); calls != 1 {
		t.Fatalf("Receive calls = %d, want the single parked call", calls)
	}
	select {
	case _, open := <-client.Updates():
		t.Fatalf("an update arrived before any error: open = %t", open)
	default:
	}

	native.releaseErrors()

	waitForClosedUpdates(t, client)
	if calls := native.calls.Load(); calls != 3 {
		t.Fatalf("Receive calls after release = %d, want 3", calls)
	}
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
	native := newGatedFailingReceiveNative()
	r := newTestRuntime(t, native)
	r.receiveErrorBackoffMin = time.Microsecond
	r.receiveErrorBackoffMax = time.Microsecond
	r.receiveErrorLimit = 3
	if err := r.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	// The client exists before the loop is allowed to fail: what this
	// test is about is what failure does to a client that has one, and
	// the loop must not reach its limit on its own while the test is
	// still getting there.
	native.waitUntilEntered(t)
	client, err := r.NewClient()
	if err != nil {
		t.Fatal(err)
	}
	native.releaseErrors()

	waitForClosedUpdates(t, client)

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

// waitForClosedUpdates waits for the loop to close the client's update
// channel, which is the signal that it is done.
func waitForClosedUpdates(t *testing.T, client *Client) {
	t.Helper()

	deadline := time.After(5 * time.Second)
	for open := true; open; {
		select {
		case _, open = <-client.Updates():
		case <-deadline:
			t.Fatal("client updates were not closed after repeated receive errors")
		}
	}
}

// burstReceiveNative fails the number of times the test arms it with, then
// succeeds, and reports the success that ends a burst.
//
// A burst is over when one successful Receive follows the failures, and
// that is the only thing a test has to wait for: sleeping and hoping
// whether the loop got there first is what made this file flaky.
type burstReceiveNative struct {
	*fakeNative
	failures  atomic.Int64
	errors    atomic.Int64
	burstDone chan struct{}
}

func newBurstReceiveNative() *burstReceiveNative {
	return &burstReceiveNative{
		fakeNative: newFakeNative(),
		burstDone:  make(chan struct{}, 1),
	}
}

func (n *burstReceiveNative) Receive(timeout time.Duration) ([]byte, error) {
	if n.failures.Add(-1) >= 0 {
		n.errors.Add(1)

		return nil, errReceiveFailed
	}

	select {
	case n.burstDone <- struct{}{}:
	default:
	}

	return n.fakeNative.Receive(timeout)
}

// armFailures hands the native the next failures it will report.
func (n *burstReceiveNative) armFailures(count int64) { n.failures.Store(count) }

// waitForBurst waits for the end of one burst of armed failures. It
// reports whether the burst ended, so a caller can tell a runtime that
// never got past it from one that is idle.
func (n *burstReceiveNative) waitForBurst(t *testing.T) {
	t.Helper()

	select {
	case <-n.burstDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the receive loop did not get through the burst of failures in 5s")
	}
}

// TestReceiveLoopResetsErrorCountAfterSuccess pins that only consecutive
// errors count toward the failure limit: two bursts of two failures with a
// success in between never fail a runtime whose limit is three.
func TestReceiveLoopResetsErrorCountAfterSuccess(t *testing.T) {
	native := newBurstReceiveNative()
	native.armFailures(2)
	r := newTestRuntime(t, native)
	r.receiveErrorBackoffMin = time.Microsecond
	r.receiveErrorBackoffMax = time.Microsecond
	r.receiveErrorLimit = 3
	if err := r.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close(context.Background()) })

	native.waitForBurst(t)
	if state := r.State(); state != LifecycleRunning {
		t.Fatalf("state after the first burst = %s, want running", state)
	}

	native.armFailures(2)
	native.waitForBurst(t)

	if state := r.State(); state != LifecycleRunning {
		t.Fatalf("state after the second burst = %s, want running", state)
	}
	// A counter that did not reset would have failed the runtime on the
	// fourth error, and the second burst would never have ended: this is
	// the assertion that says which of the two happened.
	if got := native.errors.Load(); got != 4 {
		t.Fatalf("receive errors = %d, want 4 in two bursts", got)
	}
}

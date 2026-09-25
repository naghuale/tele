package recorder

import (
	"testing"
	"time"
	"unsafe"
)

func TestNoopHasZeroSize(t *testing.T) {
	if got := unsafe.Sizeof(Noop{}); got != 0 {
		t.Fatalf(
			"unsafe.Sizeof(Noop{}) = %d, want 0",
			got,
		)
	}
}

func TestNewNoopReturnsComponentRecorder(t *testing.T) {
	var componentRecorder ComponentRecorder = NewNoop()

	if componentRecorder == nil {
		t.Fatal("NewNoop() returned nil")
	}
}

func TestNoopSetStateZeroAllocs(t *testing.T) {
	r := Noop{}

	allocs := testing.AllocsPerRun(1000, func() {
		r.SetState(StateRunning)
	})

	if allocs != 0 {
		t.Fatalf(
			"SetState allocs = %v, want 0",
			allocs,
		)
	}
}

func TestNoopSetBlockedReasonZeroAllocs(t *testing.T) {
	r := Noop{}

	allocs := testing.AllocsPerRun(1000, func() {
		r.SetBlockedReason(BlockedNetwork)
	})

	if allocs != 0 {
		t.Fatalf(
			"SetBlockedReason allocs = %v, want 0",
			allocs,
		)
	}
}

func TestNoopTouchZeroAllocs(t *testing.T) {
	r := Noop{}

	allocs := testing.AllocsPerRun(1000, func() {
		r.Touch()
	})

	if allocs != 0 {
		t.Fatalf(
			"Touch allocs = %v, want 0",
			allocs,
		)
	}
}

func TestNoopAddReceivedZeroAllocs(t *testing.T) {
	r := Noop{}

	allocs := testing.AllocsPerRun(1000, func() {
		r.AddReceived(1, 64)
	})

	if allocs != 0 {
		t.Fatalf(
			"AddReceived allocs = %v, want 0",
			allocs,
		)
	}
}

func TestNoopAddSentZeroAllocs(t *testing.T) {
	r := Noop{}

	allocs := testing.AllocsPerRun(1000, func() {
		r.AddSent(1, 64)
	})

	if allocs != 0 {
		t.Fatalf(
			"AddSent allocs = %v, want 0",
			allocs,
		)
	}
}

func TestNoopAddDroppedZeroAllocs(t *testing.T) {
	r := Noop{}

	allocs := testing.AllocsPerRun(1000, func() {
		r.AddDropped(1, DropQueueFull)
	})

	if allocs != 0 {
		t.Fatalf(
			"AddDropped allocs = %v, want 0",
			allocs,
		)
	}
}

func TestNoopAddCoalescedZeroAllocs(t *testing.T) {
	r := Noop{}

	allocs := testing.AllocsPerRun(1000, func() {
		r.AddCoalesced(1)
	})

	if allocs != 0 {
		t.Fatalf(
			"AddCoalesced allocs = %v, want 0",
			allocs,
		)
	}
}

func TestNoopSetDepthZeroAllocs(t *testing.T) {
	r := Noop{}

	allocs := testing.AllocsPerRun(1000, func() {
		r.SetDepth(1)
	})

	if allocs != 0 {
		t.Fatalf(
			"SetDepth allocs = %v, want 0",
			allocs,
		)
	}
}

func TestNoopSetCapacityZeroAllocs(t *testing.T) {
	r := Noop{}

	allocs := testing.AllocsPerRun(1000, func() {
		r.SetCapacity(1)
	})

	if allocs != 0 {
		t.Fatalf(
			"SetCapacity allocs = %v, want 0",
			allocs,
		)
	}
}

func TestNoopSetOldestAgeZeroAllocs(t *testing.T) {
	r := Noop{}

	allocs := testing.AllocsPerRun(1000, func() {
		r.SetOldestAge(time.Second)
	})

	if allocs != 0 {
		t.Fatalf(
			"SetOldestAge allocs = %v, want 0",
			allocs,
		)
	}
}

func TestNoopAddNetworkReceivedZeroAllocs(t *testing.T) {
	r := Noop{}

	allocs := testing.AllocsPerRun(1000, func() {
		r.AddNetworkReceived(1)
	})

	if allocs != 0 {
		t.Fatalf(
			"AddNetworkReceived allocs = %v, want 0",
			allocs,
		)
	}
}

func TestNoopAddNetworkSentZeroAllocs(t *testing.T) {
	r := Noop{}

	allocs := testing.AllocsPerRun(1000, func() {
		r.AddNetworkSent(1)
	})

	if allocs != 0 {
		t.Fatalf(
			"AddNetworkSent allocs = %v, want 0",
			allocs,
		)
	}
}

func TestNoopAddPayloadReceivedZeroAllocs(t *testing.T) {
	r := Noop{}

	allocs := testing.AllocsPerRun(1000, func() {
		r.AddPayloadReceived(1)
	})

	if allocs != 0 {
		t.Fatalf(
			"AddPayloadReceived allocs = %v, want 0",
			allocs,
		)
	}
}

func TestNoopAddPayloadSentZeroAllocs(t *testing.T) {
	r := Noop{}

	allocs := testing.AllocsPerRun(1000, func() {
		r.AddPayloadSent(1)
	})

	if allocs != 0 {
		t.Fatalf(
			"AddPayloadSent allocs = %v, want 0",
			allocs,
		)
	}
}

func TestNoopAddLocalReadZeroAllocs(t *testing.T) {
	r := Noop{}

	allocs := testing.AllocsPerRun(1000, func() {
		r.AddLocalRead(1)
	})

	if allocs != 0 {
		t.Fatalf(
			"AddLocalRead allocs = %v, want 0",
			allocs,
		)
	}
}

func TestNoopAddLocalWrittenZeroAllocs(t *testing.T) {
	r := Noop{}

	allocs := testing.AllocsPerRun(1000, func() {
		r.AddLocalWritten(1)
	})

	if allocs != 0 {
		t.Fatalf(
			"AddLocalWritten allocs = %v, want 0",
			allocs,
		)
	}
}

func TestNoopOperationStartedZeroAllocs(t *testing.T) {
	r := Noop{}

	allocs := testing.AllocsPerRun(1000, func() {
		r.OperationStarted()
	})

	if allocs != 0 {
		t.Fatalf(
			"OperationStarted allocs = %v, want 0",
			allocs,
		)
	}
}

func TestNoopOperationCompletedZeroAllocs(t *testing.T) {
	r := Noop{}

	allocs := testing.AllocsPerRun(1000, func() {
		r.OperationCompleted(time.Second)
	})

	if allocs != 0 {
		t.Fatalf(
			"OperationCompleted allocs = %v, want 0",
			allocs,
		)
	}
}

func TestNoopOperationFailedZeroAllocs(t *testing.T) {
	r := Noop{}

	allocs := testing.AllocsPerRun(1000, func() {
		r.OperationFailed(time.Second, ErrorNetwork, 0)
	})

	if allocs != 0 {
		t.Fatalf(
			"OperationFailed allocs = %v, want 0",
			allocs,
		)
	}
}

func TestNoopObserveZeroAllocs(t *testing.T) {
	r := Noop{}

	allocs := testing.AllocsPerRun(1000, func() {
		r.Observe(OperationSend, time.Second)
	})

	if allocs != 0 {
		t.Fatalf(
			"Observe allocs = %v, want 0",
			allocs,
		)
	}
}

func TestNoopRecordErrorZeroAllocs(t *testing.T) {
	r := Noop{}

	allocs := testing.AllocsPerRun(1000, func() {
		r.RecordError(ErrorGeneric, 1)
	})

	if allocs != 0 {
		t.Fatalf(
			"RecordError allocs = %v, want 0",
			allocs,
		)
	}
}

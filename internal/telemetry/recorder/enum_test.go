package recorder

import "testing"

func TestComponentStateEnum(t *testing.T) {
	valid := []ComponentState{
		StateStarting, StateRunning, StateQuiet,
		StateDegraded, StateBlocked, StateFailed, StateStopped,
	}
	for _, v := range valid {
		if !v.Valid() {
			t.Fatalf("%q should be valid", v)
		}
		if v.String() != string(v) {
			t.Fatalf("String mismatch for %q", v)
		}
	}
	if ComponentState("").Valid() {
		t.Fatal("empty ComponentState must be invalid")
	}
	if ComponentState("bogus").Valid() {
		t.Fatal("unknown ComponentState must be invalid")
	}
}

func TestBlockedReasonEnum(t *testing.T) {
	valid := []BlockedReason{BlockedUnknown, BlockedIO, BlockedNetwork}
	for _, v := range valid {
		if !v.Valid() {
			t.Fatalf("%q should be valid", v)
		}
		if v.String() != string(v) {
			t.Fatalf("String mismatch for %q", v)
		}
	}
	if BlockedReason("").Valid() {
		t.Fatal("empty BlockedReason must be invalid")
	}
	if BlockedReason("bogus").Valid() {
		t.Fatal("unknown BlockedReason must be invalid")
	}
}

func TestDropReasonEnum(t *testing.T) {
	valid := []DropReason{DropQueueFull, DropExpired, DropSuperseded}
	for _, v := range valid {
		if !v.Valid() {
			t.Fatalf("%q should be valid", v)
		}
		if v.String() != string(v) {
			t.Fatalf("String mismatch for %q", v)
		}
	}
	if DropReason("").Valid() {
		t.Fatal("empty DropReason must be invalid")
	}
	if DropReason("bogus").Valid() {
		t.Fatal("unknown DropReason must be invalid")
	}
}

func TestErrorKindEnum(t *testing.T) {
	valid := []ErrorKind{ErrorGeneric, ErrorIO, ErrorNetwork, ErrorTDLib}
	for _, v := range valid {
		if !v.Valid() {
			t.Fatalf("%q should be valid", v)
		}
		if v.String() != string(v) {
			t.Fatalf("String mismatch for %q", v)
		}
	}
	if ErrorKind("").Valid() {
		t.Fatal("empty ErrorKind must be invalid")
	}
	if ErrorKind("bogus").Valid() {
		t.Fatal("unknown ErrorKind must be invalid")
	}
}

func TestOperationNameEnum(t *testing.T) {
	valid := []OperationName{OperationReceive, OperationSend, OperationStore}
	for _, v := range valid {
		if !v.Valid() {
			t.Fatalf("%q should be valid", v)
		}
		if v.String() != string(v) {
			t.Fatalf("String mismatch for %q", v)
		}
	}
	if OperationName("").Valid() {
		t.Fatal("empty OperationName must be invalid")
	}
	if OperationName("bogus").Valid() {
		t.Fatal("unknown OperationName must be invalid")
	}
}

func TestComponentKindEnum(t *testing.T) {
	valid := []ComponentKind{
		KindWorker, KindQueue, KindIO, KindStore, KindAggregate, KindEventLoop,
	}
	for _, v := range valid {
		if !v.Valid() {
			t.Fatalf("%q should be valid", v)
		}
		if v.String() != string(v) {
			t.Fatalf("String mismatch for %q", v)
		}
	}
	if ComponentKind("").Valid() {
		t.Fatal("empty ComponentKind must be invalid")
	}
	if ComponentKind("bogus").Valid() {
		t.Fatal("unknown ComponentKind must be invalid")
	}
}

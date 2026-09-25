package outbox

import (
	"errors"
	"reflect"
	"testing"
)

func TestAddOperationalCountCoversAllStates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		state State
		want  func(OperationalSnapshot) int64
	}{
		{
			name:  "queued",
			state: StateQueued,
			want:  func(s OperationalSnapshot) int64 { return s.Queued },
		},
		{
			name:  "dispatching",
			state: StateDispatching,
			want:  func(s OperationalSnapshot) int64 { return s.Dispatching },
		},
		{
			name:  "accepted",
			state: StateAccepted,
			want:  func(s OperationalSnapshot) int64 { return s.Accepted },
		},
		{
			name:  "failed retryable",
			state: StateFailedRetryable,
			want:  func(s OperationalSnapshot) int64 { return s.FailedRetryable },
		},
		{
			name:  "failed permanent",
			state: StateFailedPermanent,
			want:  func(s OperationalSnapshot) int64 { return s.FailedPermanent },
		},
		{
			name:  "uncertain",
			state: StateUncertain,
			want:  func(s OperationalSnapshot) int64 { return s.Uncertain },
		},
		{
			name:  "canceled",
			state: StateCanceled,
			want:  func(s OperationalSnapshot) int64 { return s.Canceled },
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			snapshot := OperationalSnapshot{}
			if err := addOperationalCount(&snapshot, test.state, 3); err != nil {
				t.Fatalf("addOperationalCount() error = %v", err)
			}
			if got := test.want(snapshot); got != 3 {
				t.Fatalf("counter = %d, want 3", got)
			}
		})
	}
}

func TestAddOperationalCountAccumulatesRepeatedStates(t *testing.T) {
	t.Parallel()

	snapshot := OperationalSnapshot{}
	for i := 0; i < 3; i++ {
		if err := addOperationalCount(&snapshot, StateQueued, 2); err != nil {
			t.Fatalf("addOperationalCount() error = %v", err)
		}
	}
	if snapshot.Queued != 6 {
		t.Fatalf("Queued = %d, want 6", snapshot.Queued)
	}
}

func TestAddOperationalCountRejectsUnknownState(t *testing.T) {
	t.Parallel()

	snapshot := OperationalSnapshot{}
	err := addOperationalCount(&snapshot, State("future"), 1)
	if !errors.Is(err, ErrInvalidEntry) {
		t.Fatalf("error = %v, want ErrInvalidEntry", err)
	}
	if snapshot != (OperationalSnapshot{}) {
		t.Fatalf("snapshot = %#v, want zero", snapshot)
	}
}

func TestAddOperationalCountRejectsEmptyState(t *testing.T) {
	t.Parallel()

	snapshot := OperationalSnapshot{}
	if err := addOperationalCount(&snapshot, State(""), 1); err == nil {
		t.Fatal("addOperationalCount() error = nil, want non-nil")
	}
}

func TestAddOperationalCountRejectsNegativeCount(t *testing.T) {
	t.Parallel()

	snapshot := OperationalSnapshot{}
	if err := addOperationalCount(&snapshot, StateQueued, -1); err == nil {
		t.Fatal("addOperationalCount() error = nil, want non-nil")
	}
	if snapshot.Queued != 0 {
		t.Fatalf("Queued = %d, want 0", snapshot.Queued)
	}
}

func TestAddOperationalCountRejectsNilSnapshot(t *testing.T) {
	t.Parallel()

	if err := addOperationalCount(nil, StateQueued, 1); err == nil {
		t.Fatal("addOperationalCount() error = nil, want non-nil")
	}
}

func TestOperationalSnapshotDoesNotExposeIdentifiersOrPayload(t *testing.T) {
	t.Parallel()

	snapshotType := reflect.TypeOf(OperationalSnapshot{})

	for _, forbidden := range []string{
		"ID",
		"EntryID",
		"AccountKey",
		"ChatID",
		"DatabaseID",
		"DatabasePath",
		"Path",
		"Text",
		"Payload",
		"EncryptedText",
		"Ciphertext",
		"Nonce",
		"Error",
		"ErrorMessage",
		"ProviderMessage",
		"ProviderCode",
		"LeaseOwner",
		"LeaseUntil",
		"NextAttempt",
		"UpdatedAt",
		"CreatedAt",
		"Version",
		"CaptureAt",
	} {
		if _, exists := snapshotType.FieldByName(forbidden); exists {
			t.Fatalf("OperationalSnapshot contains field %q", forbidden)
		}
	}
}

func TestOperationalSnapshotContainsOnlyStateCounters(t *testing.T) {
	t.Parallel()

	snapshotType := reflect.TypeOf(OperationalSnapshot{})

	want := []string{
		"Queued",
		"Dispatching",
		"Accepted",
		"FailedRetryable",
		"FailedPermanent",
		"Uncertain",
		"Canceled",
	}

	got := make([]string, 0, snapshotType.NumField())
	for index := 0; index < snapshotType.NumField(); index++ {
		field := snapshotType.Field(index)
		if field.Type.Kind() != reflect.Int64 {
			t.Fatalf("field %q is %s, want int64", field.Name, field.Type)
		}
		got = append(got, field.Name)
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fields = %#v, want %#v", got, want)
	}
}

func TestOperationalSnapshotReaderIsSeparateFromStore(t *testing.T) {
	t.Parallel()

	storeType := reflect.TypeOf((*Store)(nil)).Elem()
	for index := 0; index < storeType.NumMethod(); index++ {
		if storeType.Method(index).Name == "ReadOperationalSnapshot" {
			t.Fatal("Store must not declare ReadOperationalSnapshot")
		}
	}

	readerType := reflect.TypeOf((*OperationalSnapshotReader)(nil)).Elem()
	if readerType.NumMethod() != 1 ||
		readerType.Method(0).Name != "ReadOperationalSnapshot" {
		t.Fatalf("OperationalSnapshotReader methods = %#v", readerType)
	}
}

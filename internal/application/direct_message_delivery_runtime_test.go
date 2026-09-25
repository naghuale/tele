package application

import "testing"

func TestNewDirectMessageDeliveryRuntimeRejectsNilSubmitter(t *testing.T) {
	t.Parallel()

	runtime, err := NewDirectMessageDeliveryRuntime(nil)
	if err == nil {
		t.Fatal("NewDirectMessageDeliveryRuntime() error = nil, want non-nil")
	}
	if runtime != nil {
		t.Fatalf("NewDirectMessageDeliveryRuntime() = %#v, want nil", runtime)
	}
}

func TestDirectMessageDeliveryRuntimeReturnsConfiguredSubmitter(t *testing.T) {
	t.Parallel()

	submitter := &h4RecordingComposerSubmitter{}
	runtime, err := NewDirectMessageDeliveryRuntime(submitter)
	if err != nil {
		t.Fatalf("NewDirectMessageDeliveryRuntime() error = %v", err)
	}
	if runtime.Submitter() != submitter {
		t.Fatal("Submitter() did not return configured submitter")
	}
}

func TestDirectMessageDeliveryRuntimeDoneIsNil(t *testing.T) {
	t.Parallel()

	runtime, err := NewDirectMessageDeliveryRuntime(&h4RecordingComposerSubmitter{})
	if err != nil {
		t.Fatalf("NewDirectMessageDeliveryRuntime() error = %v", err)
	}
	if runtime.Done() != nil {
		t.Fatal("Done() != nil")
	}
}

func TestDirectMessageDeliveryRuntimeErrIsNil(t *testing.T) {
	t.Parallel()

	runtime, err := NewDirectMessageDeliveryRuntime(&h4RecordingComposerSubmitter{})
	if err != nil {
		t.Fatalf("NewDirectMessageDeliveryRuntime() error = %v", err)
	}
	if runtime.Err() != nil {
		t.Fatalf("Err() = %v, want nil", runtime.Err())
	}
}

func TestDirectMessageDeliveryRuntimeCloseIsIdempotent(t *testing.T) {
	t.Parallel()

	runtime, err := NewDirectMessageDeliveryRuntime(&h4RecordingComposerSubmitter{})
	if err != nil {
		t.Fatalf("NewDirectMessageDeliveryRuntime() error = %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := runtime.Close(); err != nil {
			t.Fatalf("Close() error = %v, want nil", err)
		}
	}
}

func TestDirectMessageDeliveryRuntimeNilReceiver(t *testing.T) {
	t.Parallel()

	var runtime *DirectMessageDeliveryRuntime
	if runtime.Submitter() != nil {
		t.Fatal("Submitter() != nil")
	}
	if runtime.Done() != nil {
		t.Fatal("Done() != nil")
	}
	if runtime.Err() != nil {
		t.Fatal("Err() != nil")
	}
	if err := runtime.Close(); err != nil {
		t.Fatalf("Close() error = %v, want nil", err)
	}
}

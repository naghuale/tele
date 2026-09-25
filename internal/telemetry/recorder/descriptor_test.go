package recorder

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func validDescriptor() Descriptor {
	return Descriptor{
		ID:           ComponentEventQueue,
		Name:         "Event queue",
		Kind:         KindQueue,
		Capabilities: CapState | CapEvents | CapQueue,
		QuietPolicy: QuietPolicy{
			Enabled:   true,
			Threshold: time.Minute,
		},
		Inputs: []ComponentID{
			ComponentTDLibReceive,
		},
		Outputs: []ComponentID{
			ComponentEventDispatcher,
		},
	}
}

func TestDescriptorAcceptsZeroCapabilitiesForWorker(t *testing.T) {
	descriptor := Descriptor{
		ID:           "worker.passive",
		Name:         "Passive worker",
		Kind:         KindWorker,
		Capabilities: 0,
	}

	if err := descriptor.Validate(); err != nil {
		t.Fatalf(
			"Descriptor.Validate() error = %v",
			err,
		)
	}
}

func TestDescriptorRejectsUnknownCapabilityBits(t *testing.T) {
	descriptor := validDescriptor()
	descriptor.Capabilities |= Capability(1) << 63

	err := descriptor.Validate()
	if err == nil {
		t.Fatal(
			"Descriptor.Validate() error = nil, " +
				"want unknown capability error",
		)
	}

	if !strings.Contains(
		err.Error(),
		"unknown capability bits",
	) {
		t.Fatalf(
			"Descriptor.Validate() error = %q, "+
				"want unknown capability error",
			err,
		)
	}
}

func TestQuietPolicyEnabledRequiresPositiveThreshold(t *testing.T) {
	tests := []struct {
		name      string
		threshold time.Duration
	}{
		{
			name:      "zero",
			threshold: 0,
		},
		{
			name:      "negative",
			threshold: -time.Second,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			descriptor := validDescriptor()
			descriptor.QuietPolicy = QuietPolicy{
				Enabled:   true,
				Threshold: test.threshold,
			}

			err := descriptor.Validate()
			if err == nil {
				t.Fatal(
					"Descriptor.Validate() error = nil, " +
						"want threshold error",
				)
			}

			if !strings.Contains(
				err.Error(),
				"positive threshold",
			) {
				t.Fatalf(
					"Descriptor.Validate() error = %q, "+
						"want positive threshold error",
					err,
				)
			}
		})
	}
}

func TestQuietPolicyDisabledIgnoresThreshold(t *testing.T) {
	tests := []time.Duration{
		0,
		time.Second,
		-time.Second,
	}

	for _, threshold := range tests {
		descriptor := validDescriptor()
		descriptor.QuietPolicy = QuietPolicy{
			Enabled:   false,
			Threshold: threshold,
		}

		if err := descriptor.Validate(); err != nil {
			t.Fatalf(
				"Descriptor.Validate() with disabled "+
					"threshold %s error = %v",
				threshold,
				err,
			)
		}
	}
}

func TestDescriptorRejectsInputSelfReference(t *testing.T) {
	descriptor := validDescriptor()
	descriptor.Inputs = []ComponentID{
		descriptor.ID,
	}

	err := descriptor.Validate()
	if err == nil {
		t.Fatal(
			"Descriptor.Validate() error = nil, " +
				"want input self-reference error",
		)
	}

	if !strings.Contains(err.Error(), "self-reference") {
		t.Fatalf(
			"Descriptor.Validate() error = %q, "+
				"want self-reference error",
			err,
		)
	}
}

func TestDescriptorRejectsOutputSelfReference(t *testing.T) {
	descriptor := validDescriptor()
	descriptor.Outputs = []ComponentID{
		descriptor.ID,
	}

	err := descriptor.Validate()
	if err == nil {
		t.Fatal(
			"Descriptor.Validate() error = nil, " +
				"want output self-reference error",
		)
	}

	if !strings.Contains(err.Error(), "self-reference") {
		t.Fatalf(
			"Descriptor.Validate() error = %q, "+
				"want self-reference error",
			err,
		)
	}
}

func TestDescriptorRejectsDuplicateInputs(t *testing.T) {
	descriptor := validDescriptor()
	descriptor.Inputs = []ComponentID{
		"worker.source",
		"worker.source",
	}

	err := descriptor.Validate()
	if err == nil {
		t.Fatal(
			"Descriptor.Validate() error = nil, " +
				"want duplicate input error",
		)
	}

	if !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf(
			"Descriptor.Validate() error = %q, "+
				"want duplicate error",
			err,
		)
	}
}

func TestDescriptorRejectsDuplicateOutputs(t *testing.T) {
	descriptor := validDescriptor()
	descriptor.Outputs = []ComponentID{
		"worker.destination",
		"worker.destination",
	}

	err := descriptor.Validate()
	if err == nil {
		t.Fatal(
			"Descriptor.Validate() error = nil, " +
				"want duplicate output error",
		)
	}

	if !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf(
			"Descriptor.Validate() error = %q, "+
				"want duplicate error",
			err,
		)
	}
}

func TestDescriptorValidateDoesNotMutateSlices(t *testing.T) {
	inputs := []ComponentID{
		"worker.z",
		"worker.a",
	}
	outputs := []ComponentID{
		"worker.y",
		"worker.b",
	}

	originalInputs := append(
		[]ComponentID(nil),
		inputs...,
	)
	originalOutputs := append(
		[]ComponentID(nil),
		outputs...,
	)

	descriptor := Descriptor{
		ID:           "worker.main",
		Name:         "Main worker",
		Kind:         KindWorker,
		Capabilities: CapState,
		Inputs:       inputs,
		Outputs:      outputs,
	}

	if err := descriptor.Validate(); err != nil {
		t.Fatalf(
			"Descriptor.Validate() error = %v",
			err,
		)
	}

	if !reflect.DeepEqual(inputs, originalInputs) {
		t.Fatalf(
			"Inputs mutated: got %v, want %v",
			inputs,
			originalInputs,
		)
	}

	if !reflect.DeepEqual(outputs, originalOutputs) {
		t.Fatalf(
			"Outputs mutated: got %v, want %v",
			outputs,
			originalOutputs,
		)
	}
}

package recorder

import (
	"strings"
	"testing"
)

func TestDescriptorKindCapabilityRulesPositive(t *testing.T) {
	tests := []struct {
		name string
		kind ComponentKind
		caps Capability
	}{
		{
			name: "queue with CapQueue",
			kind: KindQueue,
			caps: CapQueue,
		},
		{
			name: "event loop without CapQueue",
			kind: KindEventLoop,
			caps: CapEvents,
		},
		{
			name: "aggregate with CapOperations",
			kind: KindAggregate,
			caps: CapOperations,
		},
		{
			name: "store without CapNetworkBytes",
			kind: KindStore,
			caps: CapLocalBytes,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			descriptor := Descriptor{
				ID:           "component.test",
				Name:         "Test component",
				Kind:         test.kind,
				Capabilities: test.caps,
			}

			if err := descriptor.Validate(); err != nil {
				t.Fatalf(
					"Descriptor.Validate() error = %v",
					err,
				)
			}
		})
	}
}

func TestDescriptorKindCapabilityRulesNegative(t *testing.T) {
	tests := []struct {
		name    string
		kind    ComponentKind
		caps    Capability
		wantErr string
	}{
		{
			name:    "queue without CapQueue",
			kind:    KindQueue,
			caps:    CapEvents,
			wantErr: "requires CapQueue",
		},
		{
			name:    "event loop with CapQueue",
			kind:    KindEventLoop,
			caps:    CapEvents | CapQueue,
			wantErr: "forbids CapQueue",
		},
		{
			name:    "aggregate without CapOperations",
			kind:    KindAggregate,
			caps:    CapEvents,
			wantErr: "requires CapOperations",
		},
		{
			name:    "store with CapNetworkBytes",
			kind:    KindStore,
			caps:    CapLocalBytes | CapNetworkBytes,
			wantErr: "forbids CapNetworkBytes",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			descriptor := Descriptor{
				ID:           "component.test",
				Name:         "Test component",
				Kind:         test.kind,
				Capabilities: test.caps,
			}

			err := descriptor.Validate()
			if err == nil {
				t.Fatal(
					"Descriptor.Validate() error = nil",
				)
			}

			if !strings.Contains(
				err.Error(),
				test.wantErr,
			) {
				t.Fatalf(
					"Descriptor.Validate() error = %q, "+
						"want substring %q",
					err,
					test.wantErr,
				)
			}
		})
	}
}

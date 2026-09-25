package recorder

import "testing"

func TestCapabilityHas(t *testing.T) {
	capabilities := CapState | CapEvents

	if !capabilities.Has(CapState) {
		t.Fatal("CapState must be present")
	}

	if !capabilities.Has(CapEvents) {
		t.Fatal("CapEvents must be present")
	}

	if !capabilities.Has(CapState | CapEvents) {
		t.Fatal("combined requirement must be satisfied")
	}

	if capabilities.Has(CapQueue) {
		t.Fatal("CapQueue must not be present")
	}

	if capabilities.Has(CapState | CapQueue) {
		t.Fatal("all bits in a combined requirement must be present")
	}
}

func TestCapabilityHasZeroMask(t *testing.T) {
	if Capability(0).Has(CapEvents) {
		t.Fatal(
			"zero capability set satisfies non-zero requirement",
		)
	}

	if !Capability(0).Has(0) {
		t.Fatal(
			"zero capability set must satisfy empty requirement",
		)
	}

	if !(CapEvents | CapErrors).Has(0) {
		t.Fatal(
			"non-zero capability set must satisfy empty requirement",
		)
	}
}

func TestAllCapabilitiesClosedSet(t *testing.T) {
	expected :=
		CapState |
			CapEvents |
			CapOperations |
			CapErrors |
			CapLatency |
			CapQueue |
			CapNetworkBytes |
			CapPayloadBytes |
			CapLocalBytes |
			CapCache |
			CapInFlight

	if allCapabilities != expected {
		t.Fatalf(
			"allCapabilities = %#x, want %#x",
			allCapabilities,
			expected,
		)
	}
}

package recorder

import (
	"errors"
	"fmt"
	"time"
)

// Capability is a bitmask of recorder features.
type Capability uint64

const (
	CapState Capability = 1 << iota
	CapEvents
	CapOperations
	CapErrors
	CapLatency
	CapQueue
	CapNetworkBytes
	CapPayloadBytes
	CapLocalBytes
	CapCache
	CapInFlight
)

// allCapabilities is the canonical closed set of capability bits.
const allCapabilities Capability = CapState |
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

// Has reports whether all bits in required are set in c.
//
// An empty requirement is satisfied by every capability set.
func (c Capability) Has(required Capability) bool {
	return c&required == required
}

// QuietPolicy controls when a component is considered quiet.
//
// Threshold is interpreted only when Enabled is true.
type QuietPolicy struct {
	Enabled   bool
	Threshold time.Duration
}

// Descriptor describes a recorder component.
type Descriptor struct {
	ID           ComponentID
	Name         string
	Kind         ComponentKind
	Capabilities Capability
	QuietPolicy  QuietPolicy

	Inputs  []ComponentID
	Outputs []ComponentID
}

// Validate returns an error if the descriptor is not usable.
//
// Validate does not mutate Inputs or Outputs.
func (d Descriptor) Validate() error {
	if !ValidComponentID(d.ID) {
		return fmt.Errorf(
			"recorder: invalid ComponentID %q",
			d.ID,
		)
	}

	if d.Name == "" {
		return errors.New(
			"recorder: empty Name",
		)
	}

	if !d.Kind.Valid() {
		return fmt.Errorf(
			"recorder: invalid ComponentKind %q",
			d.Kind,
		)
	}

	if d.Capabilities&^allCapabilities != 0 {
		return fmt.Errorf(
			"recorder: unknown capability bits %#x",
			uint64(d.Capabilities&^allCapabilities),
		)
	}

	if d.QuietPolicy.Enabled &&
		d.QuietPolicy.Threshold <= 0 {
		return errors.New(
			"recorder: enabled quiet policy requires positive threshold",
		)
	}

	if err := validateEdges(
		d.ID,
		d.Inputs,
		"Inputs",
	); err != nil {
		return err
	}

	if err := validateEdges(
		d.ID,
		d.Outputs,
		"Outputs",
	); err != nil {
		return err
	}

	switch d.Kind {
	case KindQueue:
		if !d.Capabilities.Has(CapQueue) {
			return errors.New(
				"recorder: KindQueue requires CapQueue",
			)
		}

	case KindEventLoop:
		if d.Capabilities.Has(CapQueue) {
			return errors.New(
				"recorder: KindEventLoop forbids CapQueue",
			)
		}

	case KindAggregate:
		if !d.Capabilities.Has(CapOperations) {
			return errors.New(
				"recorder: KindAggregate requires CapOperations",
			)
		}

	case KindStore:
		if d.Capabilities.Has(CapNetworkBytes) {
			return errors.New(
				"recorder: KindStore forbids CapNetworkBytes",
			)
		}
	}

	return nil
}

func validateEdges(
	self ComponentID,
	edges []ComponentID,
	field string,
) error {
	seen := make(
		map[ComponentID]struct{},
		len(edges),
	)

	for _, id := range edges {
		if !ValidComponentID(id) {
			return fmt.Errorf(
				"recorder: invalid ComponentID %q in %s",
				id,
				field,
			)
		}

		if id == self {
			return fmt.Errorf(
				"recorder: self-reference in %s: %q",
				field,
				id,
			)
		}

		if _, exists := seen[id]; exists {
			return fmt.Errorf(
				"recorder: duplicate in %s: %q",
				field,
				id,
			)
		}

		seen[id] = struct{}{}
	}

	return nil
}

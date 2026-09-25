// Package recorder defines telemetry recorder contracts used across
// telecli components.
//
// This package depends on the standard library only. Any violation is
// enforced repository-wide by internal/archdeps TestArchImports.
package recorder

import "regexp"

// ComponentID is a stable identifier for a recorder component.
type ComponentID string

// Reserved ComponentIDs. Semantic meaning is fixed for each identifier.
const (
	// ComponentTDLibReceive is the TDLib receive loop.
	ComponentTDLibReceive ComponentID = "tdlib.receive"
	// ComponentEventQueue is the bounded control/event queue.
	ComponentEventQueue ComponentID = "event.queue"
	// ComponentEventDispatcher is the event dispatcher.
	ComponentEventDispatcher ComponentID = "event.dispatcher"
)

var componentIDPattern = regexp.MustCompile(`^[a-z0-9._-]+$`)

// ValidComponentID reports whether id matches [a-z0-9._-]+.
func ValidComponentID(id ComponentID) bool {
	return componentIDPattern.MatchString(string(id))
}

// ComponentKind classifies a component by its architectural role.
type ComponentKind string

const (
	KindWorker    ComponentKind = "worker"
	KindQueue     ComponentKind = "queue"
	KindIO        ComponentKind = "io"
	KindStore     ComponentKind = "store"
	KindAggregate ComponentKind = "aggregate"
	KindEventLoop ComponentKind = "event_loop"
)

// Valid reports whether k is a known ComponentKind.
func (k ComponentKind) Valid() bool {
	switch k {
	case KindWorker, KindQueue, KindIO, KindStore, KindAggregate, KindEventLoop:
		return true
	}
	return false
}

// String returns the canonical string representation.
func (k ComponentKind) String() string { return string(k) }

// ComponentState is the lifecycle state of a component.
type ComponentState string

const (
	StateStarting ComponentState = "starting"
	StateRunning  ComponentState = "running"
	StateQuiet    ComponentState = "quiet"
	StateDegraded ComponentState = "degraded"
	StateBlocked  ComponentState = "blocked"
	StateFailed   ComponentState = "failed"
	StateStopped  ComponentState = "stopped"
)

// Valid reports whether s is a canonical state. Empty is invalid.
func (s ComponentState) Valid() bool {
	switch s {
	case StateStarting, StateRunning, StateQuiet,
		StateDegraded, StateBlocked, StateFailed, StateStopped:
		return true
	}
	return false
}

// String returns the canonical string representation.
func (s ComponentState) String() string { return string(s) }

// BlockedReason classifies why a component is in StateBlocked.
type BlockedReason string

const (
	BlockedUnknown BlockedReason = "unknown"
	BlockedIO      BlockedReason = "io"
	BlockedNetwork BlockedReason = "network"
)

// Valid reports whether r is a canonical blocked reason. Empty is invalid.
func (r BlockedReason) Valid() bool {
	switch r {
	case BlockedUnknown, BlockedIO, BlockedNetwork:
		return true
	}
	return false
}

// String returns the canonical string representation.
func (r BlockedReason) String() string { return string(r) }

// StateRecorder records component lifecycle state.
type StateRecorder interface {
	SetState(ComponentState)
	SetBlockedReason(BlockedReason)
	Touch()
}

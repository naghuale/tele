package recorder

import "time"

// DropReason classifies why events were dropped.
type DropReason string

const (
	DropQueueFull  DropReason = "queue_full"
	DropExpired    DropReason = "expired"
	DropSuperseded DropReason = "superseded"
)

// Valid reports whether r is a canonical drop reason. Empty is invalid.
func (r DropReason) Valid() bool {
	switch r {
	case DropQueueFull, DropExpired, DropSuperseded:
		return true
	}
	return false
}

// String returns the canonical string representation.
func (r DropReason) String() string { return string(r) }

// EventRecorder records discrete event counters.
type EventRecorder interface {
	AddReceived(count, payloadBytes uint64)
	AddSent(count, payloadBytes uint64)
	AddDropped(count uint64, reason DropReason)
	AddCoalesced(count uint64)
}

// QueueRecorder records queue observations.
type QueueRecorder interface {
	SetDepth(depth int64)
	SetCapacity(capacity int64)
	SetOldestAge(age time.Duration)
}

// IORecorder records byte counters.
type IORecorder interface {
	AddNetworkReceived(bytes uint64)
	AddNetworkSent(bytes uint64)
	AddPayloadReceived(bytes uint64)
	AddPayloadSent(bytes uint64)
	AddLocalRead(bytes uint64)
	AddLocalWritten(bytes uint64)
}

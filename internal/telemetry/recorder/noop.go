package recorder

import "time"

// Noop is a zero-size, zero-allocation ComponentRecorder.
//
// No mutexes, no goroutines, no logging, no validation, no per-call
// allocations.
type Noop struct{}

// Compile-time assertion: Noop implements ComponentRecorder.
var _ ComponentRecorder = Noop{}

// NewNoop returns a zero-allocation no-op ComponentRecorder.
func NewNoop() ComponentRecorder { return Noop{} }

// NewNoopOutboxHealth returns a zero-allocation no-op outbox health
// recorder.
//
// NewNoop exposes the narrower ComponentRecorder interface, so callers that
// record durable outbox health use this constructor.
func NewNoopOutboxHealth() OutboxHealthRecorder { return Noop{} }

func (Noop) SetState(ComponentState)        {}
func (Noop) SetBlockedReason(BlockedReason) {}
func (Noop) Touch()                         {}

func (Noop) AddReceived(uint64, uint64)    {}
func (Noop) AddSent(uint64, uint64)        {}
func (Noop) AddDropped(uint64, DropReason) {}
func (Noop) AddCoalesced(uint64)           {}

func (Noop) SetDepth(int64)             {}
func (Noop) SetCapacity(int64)          {}
func (Noop) SetOldestAge(time.Duration) {}

func (Noop) AddNetworkReceived(uint64) {}
func (Noop) AddNetworkSent(uint64)     {}
func (Noop) AddPayloadReceived(uint64) {}
func (Noop) AddPayloadSent(uint64)     {}
func (Noop) AddLocalRead(uint64)       {}
func (Noop) AddLocalWritten(uint64)    {}

func (Noop) OperationStarted()                             {}
func (Noop) OperationCompleted(time.Duration)              {}
func (Noop) OperationFailed(time.Duration, ErrorKind, int) {}
func (Noop) Observe(OperationName, time.Duration)          {}

func (Noop) RecordError(ErrorKind, int) {}

// SetOutboxHealth discards a durable outbox observation.
func (Noop) SetOutboxHealth(OutboxHealth) {}

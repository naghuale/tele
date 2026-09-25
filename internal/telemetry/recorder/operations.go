package recorder

import "time"

// OperationName identifies a logical operation.
type OperationName string

const (
	OperationReceive OperationName = "receive"
	OperationSend    OperationName = "send"
	OperationStore   OperationName = "store"
)

// Valid reports whether n is a canonical operation name. Empty is invalid.
func (n OperationName) Valid() bool {
	switch n {
	case OperationReceive, OperationSend, OperationStore:
		return true
	}
	return false
}

// String returns the canonical string representation.
func (n OperationName) String() string { return string(n) }

// OperationRecorder records operation lifecycle and durations.
type OperationRecorder interface {
	OperationStarted()
	OperationCompleted(duration time.Duration)
	OperationFailed(duration time.Duration, kind ErrorKind, code int)
	Observe(name OperationName, duration time.Duration)
}

// ComponentRecorder is the composite contract implemented by telemetry
// backends and accepted by application components.
type ComponentRecorder interface {
	StateRecorder
	EventRecorder
	QueueRecorder
	IORecorder
	OperationRecorder
	ErrorRecorder
}

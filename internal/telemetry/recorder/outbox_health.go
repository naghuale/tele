package recorder

// OutboxHealthState mirrors the durable message delivery lifecycle for
// telemetry.
//
// The enum is intentionally low cardinality: telemetry must not carry entry,
// account, chat, database or instance identifiers.
type OutboxHealthState string

const (
	OutboxHealthStarting OutboxHealthState = "starting"
	OutboxHealthRunning  OutboxHealthState = "running"
	OutboxHealthStopping OutboxHealthState = "stopping"
	OutboxHealthStopped  OutboxHealthState = "stopped"
	OutboxHealthFailed   OutboxHealthState = "failed"
)

// Valid reports whether s is a canonical lifecycle state. Empty is invalid.
func (s OutboxHealthState) Valid() bool {
	switch s {
	case OutboxHealthStarting,
		OutboxHealthRunning,
		OutboxHealthStopping,
		OutboxHealthStopped,
		OutboxHealthFailed:
		return true
	}
	return false
}

// String returns the canonical string representation.
func (s OutboxHealthState) String() string { return string(s) }

// OutboxHealth is one payload-free operational observation of the durable
// outbox.
//
// It contains aggregate state counters only. Entry identifiers, account keys,
// chat IDs, database identity, payload data, lease ownership, provider errors
// and runtime error text are never part of this value.
type OutboxHealth struct {
	State OutboxHealthState

	Queued          int64
	Dispatching     int64
	Accepted        int64
	FailedRetryable int64
	FailedPermanent int64
	Uncertain       int64
	Canceled        int64
}

// Validate reports whether the observation satisfies the telemetry schema.
func (h OutboxHealth) Validate() error {
	if !h.State.Valid() {
		return ErrInvalidOutboxHealth
	}
	for _, count := range h.counts() {
		if count.value < 0 {
			return ErrInvalidOutboxHealth
		}
	}
	return nil
}

type outboxHealthCount struct {
	name  string
	value int64
}

func (h OutboxHealth) counts() []outboxHealthCount {
	return []outboxHealthCount{
		{name: "queued", value: h.Queued},
		{name: "dispatching", value: h.Dispatching},
		{name: "accepted", value: h.Accepted},
		{name: "failed_retryable", value: h.FailedRetryable},
		{name: "failed_permanent", value: h.FailedPermanent},
		{name: "uncertain", value: h.Uncertain},
		{name: "canceled", value: h.Canceled},
	}
}

// OutboxHealthRecorder records aggregate durable outbox observations.
//
// The call is best effort by design: telemetry is an observability path, and
// a recording failure must never change delivery behavior.
type OutboxHealthRecorder interface {
	SetOutboxHealth(health OutboxHealth)
}

// Compile-time assertion: OutboxHealthRecorder accepts the no-op recorder.
var _ OutboxHealthRecorder = Noop{}

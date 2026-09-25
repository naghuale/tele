package recorder

import "errors"

// ErrInvalidOutboxHealth reports an observation that does not satisfy the
// outbox telemetry schema: an unknown lifecycle state or a negative counter.
var ErrInvalidOutboxHealth = errors.New(
	"telemetry: invalid outbox health observation",
)

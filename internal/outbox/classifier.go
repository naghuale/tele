package outbox

import (
	"context"
	"errors"
	"fmt"
)

// Outcome classifies the result of a single dispatch attempt.
//
// The classifier is deliberately conservative: unknown errors are
// never classified as retryable, and any error that could have
// occurred after the sender saw the request is classified as
// uncertain.
type Outcome uint8

const (
	// OutcomeAccepted: the sender returned a message object with a
	// non-zero ID and a chat ID matching the request.
	OutcomeAccepted Outcome = iota

	// OutcomeRetryable: the send did not reach the transport, or the
	// transport returned a transient error. The entry can be retried
	// after a backoff delay.
	OutcomeRetryable

	// OutcomePermanent: the send reached the transport and was
	// rejected with a terminal error. Automatic retries stop.
	OutcomePermanent

	// OutcomeUncertain: the dispatch started but the outcome could
	// not be proven. Automatic retries stop; only an explicit user
	// decision can resolve the entry.
	OutcomeUncertain
)

// String returns a stable lowercase name for diagnostics.
func (o Outcome) String() string {
	switch o {
	case OutcomeAccepted:
		return "accepted"
	case OutcomeRetryable:
		return "retryable"
	case OutcomePermanent:
		return "permanent"
	case OutcomeUncertain:
		return "uncertain"
	default:
		return "unknown"
	}
}

// SendError is a transport-neutral structured send failure.
//
// Application adapters translate transport-specific errors into this
// type before returning them to the dispatcher. The outbox package
// never imports a transport package.
//
// Error() intentionally does not include Message: the message field
// is application-provided and may carry arbitrary text. Callers that
// need a safe, persisted description use SafeReason.
type SendError struct {
	// Code is a transport-specific error code, or 0 when the
	// transport did not provide one.
	Code int

	// Message is a short, transport-provided description. It is not
	// used in logs or in the persisted LastErrorMessage.
	Message string

	// Retryable marks a transient failure that may be retried after
	// the backoff delay.
	Retryable bool

	// Permanent marks a terminal failure for this entry.
	Permanent bool
}

// Error implements error.
func (e *SendError) Error() string {
	if e == nil {
		return "outbox send error"
	}
	if e.Code != 0 {
		return fmt.Sprintf("outbox send error: code=%d", e.Code)
	}
	return "outbox send error"
}

// SendErrorDetails extracts a *SendError from err.
func SendErrorDetails(err error) (*SendError, bool) {
	var sendErr *SendError
	if errors.As(err, &sendErr) {
		return sendErr, true
	}
	return nil, false
}

// Classify classifies a dispatch result.
//
// The classifier receives the error returned by the Sender and a flag
// indicating whether the send attempt was started. In the current
// dispatcher the flag is always true: pre-send errors are handled by
// the dispatcher before SendMessage is invoked and never reach
// Classify.
//
// Classification rules (initial conservative policy):
//
//   - nil error → accepted.
//   - error when started == false → retryable.
//   - context.Canceled or context.DeadlineExceeded after start →
//     uncertain.
//   - *SendError with Permanent → permanent.
//   - *SendError with Retryable → retryable.
//   - *SendError with a known code → code-based mapping.
//   - *SendError with an unknown code → uncertain.
//   - Any other error after start → uncertain.
//
// The classifier never guesses: unknown shapes default to the safer
// outcome.
func Classify(err error, started bool) Outcome {
	if err == nil {
		return OutcomeAccepted
	}

	if !started {
		return OutcomeRetryable
	}

	if errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) {
		return OutcomeUncertain
	}

	sendErr, ok := SendErrorDetails(err)
	if !ok {
		return OutcomeUncertain
	}

	switch {
	case sendErr.Permanent:
		return OutcomePermanent
	case sendErr.Retryable:
		return OutcomeRetryable
	default:
		return classifySendCode(sendErr.Code)
	}
}

// classifySendCode maps a transport error code to an outcome.
//
// Only codes explicitly listed here are considered permanent or
// retryable; everything else is uncertain.
func classifySendCode(code int) Outcome {
	switch code {
	case 400, 401, 403, 404:
		return OutcomePermanent

	case 420, 429:
		return OutcomeRetryable

	case 500, 502, 503, 504:
		return OutcomeRetryable

	default:
		return OutcomeUncertain
	}
}

// SafeReason returns a short, transport-neutral description of err
// that is safe to persist in Entry.LastErrorMessage and to write to
// logs.
//
// It never includes err.Error() verbatim: an implementation-defined
// error string may contain the message body. For unrecognized errors
// only the concrete type name is used.
func SafeReason(err error) string {
	if err == nil {
		return "unknown send failure"
	}

	if sendErr, ok := SendErrorDetails(err); ok {
		switch {
		case sendErr.Code != 0:
			return fmt.Sprintf("send error code=%d", sendErr.Code)
		case sendErr.Permanent:
			return "permanent send failure"
		case sendErr.Retryable:
			return "retryable send failure"
		default:
			return "structured send failure"
		}
	}

	switch {
	case errors.Is(err, context.Canceled):
		return "send context canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "send deadline exceeded"
	default:
		return fmt.Sprintf("send failure type=%T", err)
	}
}

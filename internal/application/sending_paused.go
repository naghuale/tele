package application

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"telecli/internal/outbox"
)

// SendingPausedReason is why the durable outbox could not be opened.
//
// The cases mirror docs/TUI_SPEC.md decision 3 and
// docs/help/sending-paused.md. They are distinguished by sentinel errors
// with errors.Is, never by message text, so a wrapped or reworded error
// still classifies.
type SendingPausedReason uint8

const (
	// SendingPausedOther is any cause that does not match a known case.
	SendingPausedOther SendingPausedReason = iota

	// SendingPausedKeychainLocked is a Keychain that is locked, or an
	// access request the user denied.
	SendingPausedKeychainLocked

	// SendingPausedKeyMissing is a queue on disk whose key is gone. The
	// same case covers a reset that was started and not finished: the
	// key of the current identity is missing either way, and
	// telecli outbox reset is the remedy for both.
	SendingPausedKeyMissing

	// SendingPausedNoKeyStorage is a platform with no secure key
	// provider, such as headless Linux without Secret Service.
	SendingPausedNoKeyStorage

	// SendingPausedDataDirInsecure is a data folder other users can
	// reach.
	SendingPausedDataDirInsecure
)

// sendingPausedDetailsURL is where the user reads the full explanation.
//
// The wording is fixed by docs/TUI_SPEC.md decision 3 and must stay in
// step with docs/help/sending-paused.md.
const sendingPausedDetailsURL = "https://github.com/naghuale/tele/blob/main/docs/help/sending-paused.md"

// sendingPausedHeadline is the status line shown while sending is paused.
const sendingPausedHeadline = "Sending paused"

// sendingPausedExplanation is shown under the composer. It says what
// happened and why nothing was lost.
const sendingPausedExplanation = "Sending is paused. Your message was not sent and is still here.\n" +
	"telecli could not open its secure message queue, which keeps unsent\n" +
	"messages safe if the app closes."

// Hint returns the one-line reason hint for this case.
//
// The text is the same as the hint in docs/help/sending-paused.md.
func (r SendingPausedReason) Hint() string {
	switch r {
	case SendingPausedKeychainLocked:
		return "Unlock your Keychain or allow telecli access, " +
			"then restart telecli."
	case SendingPausedKeyMissing:
		return "The key for your message queue is missing. " +
			"Run telecli outbox reset to start a new queue."
	case SendingPausedNoKeyStorage:
		return "This system has no secure key storage, " +
			"so messages cannot be queued safely."
	case SendingPausedDataDirInsecure:
		return "The telecli data folder is accessible to other users. " +
			"Run telecli doctor to fix it."
	default:
		return "Run telecli doctor to see what went wrong."
	}
}

// String names the case for telecli doctor and the log.
func (r SendingPausedReason) String() string {
	switch r {
	case SendingPausedKeychainLocked:
		return "keychain locked or access denied"
	case SendingPausedKeyMissing:
		return "queue key missing"
	case SendingPausedNoKeyStorage:
		return "no secure key storage"
	case SendingPausedDataDirInsecure:
		return "data folder accessible to other users"
	default:
		return "other"
	}
}

// classifySendingPaused maps an outbox open failure to a case.
//
// Order matters only where a wrapped error could match more than one
// sentinel; the sentinels are distinct, so a single pass is enough.
func classifySendingPaused(err error) SendingPausedReason {
	switch {
	case err == nil:
		return SendingPausedOther
	case errors.Is(err, outbox.ErrOutboxKeyAccessDenied):
		return SendingPausedKeychainLocked
	case errors.Is(err, outbox.ErrOutboxKeyUnavailable):
		return SendingPausedKeyMissing
	case errors.Is(err, outbox.ErrOutboxKeyProviderUnsupported):
		return SendingPausedNoKeyStorage
	case errors.Is(err, outbox.ErrOutboxDataDirInsecure):
		return SendingPausedDataDirInsecure
	case errors.Is(err, outbox.ErrOutboxResetPending):
		// A reset that was started and not finished. The key of the
		// current identity really is missing, and the same command is
		// what finishes the reset, so it is the same case with the same
		// remedy.
		return SendingPausedKeyMissing
	default:
		return SendingPausedOther
	}
}

// SendingPausedError is what the composer shows while the durable outbox
// is unavailable.
//
// Its message is the user-facing text and nothing else. The underlying
// failure is carried in Cause, never in Error: the cause can name files
// and keychain services, and the specification puts it in the log and in
// telecli doctor only.
type SendingPausedError struct {
	reason SendingPausedReason
	cause  error
}

// NewSendingPausedError returns the user-facing error for a reason.
func NewSendingPausedError(reason SendingPausedReason) *SendingPausedError {
	return &SendingPausedError{reason: reason}
}

// Cause returns the underlying failure for logging and for doctor.
//
// It is never rendered: Error deliberately omits it.
func (e *SendingPausedError) Cause() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// Reason returns the classified case.
func (e *SendingPausedError) Reason() SendingPausedReason {
	if e == nil {
		return SendingPausedOther
	}
	return e.reason
}

// Error implements error with the texts from decision 3.
func (e *SendingPausedError) Error() string {
	if e == nil {
		return sendingPausedHeadline
	}
	return strings.Join([]string{
		sendingPausedHeadline,
		sendingPausedExplanation,
		e.reason.Hint(),
		"Details: " + sendingPausedDetailsURL,
	}, "\n")
}

// UnavailableComposerSubmitter refuses every message while the durable
// outbox is unavailable.
//
// There is deliberately no second delivery path. A message that cannot
// be queued must not be sent directly instead: that is exactly the loss
// the outbox exists to prevent, and it would happen silently.
type UnavailableComposerSubmitter struct {
	reason SendingPausedReason
}

// NewUnavailableComposerSubmitter returns a submitter that refuses
// everything with the user-facing text for reason.
func NewUnavailableComposerSubmitter(
	reason SendingPausedReason,
) *UnavailableComposerSubmitter {
	return &UnavailableComposerSubmitter{reason: reason}
}

// SubmitMessage always fails. The draft is left to the composer.
func (s *UnavailableComposerSubmitter) SubmitMessage(
	context.Context,
	string,
	int64,
	string,
) (MessageSubmission, error) {
	reason := SendingPausedOther
	if s != nil {
		reason = s.reason
	}
	return MessageSubmission{}, NewSendingPausedError(reason)
}

var _ ComposerMessageSubmitter = (*UnavailableComposerSubmitter)(nil)

// DescribeSendingPaused returns the telecli doctor line for a failure.
//
// The full error goes here and to the log, never to the screen. The data
// directory is included because every remedy is a path operation.
func DescribeSendingPaused(err error, dataDir string) string {
	reason := classifySendingPaused(err)

	var b strings.Builder
	fmt.Fprintf(
		&b,
		"Message queue: unavailable (%s)\n",
		reason,
	)
	fmt.Fprintf(&b, "  %s\n", reason.Hint())
	if strings.TrimSpace(dataDir) != "" {
		fmt.Fprintf(&b, "  Data folder: %s\n", dataDir)
	}
	if err != nil {
		fmt.Fprintf(&b, "  Cause: %v\n", err)
	}
	return b.String()
}

package outbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"
)

// Sender is the narrow transport contract used by the dispatcher.
//
// It is satisfied by an application-level adapter over the Telegram
// client in PR-08E. The dispatcher never sees transport-specific
// types: the adapter translates transport errors into *SendError
// before returning them.
type Sender interface {
	SendMessage(
		ctx context.Context,
		chatID int64,
		text string,
	) (SentMessage, error)
}

// SentMessage is the result of one successful SendMessage call.
//
// ChatID must equal the requested chat ID. The dispatcher marks the
// entry as uncertain when they differ.
type SentMessage struct {
	ID     int64
	ChatID int64
}

// DispatcherConfig tunes the dispatcher.
type DispatcherConfig struct {
	// InstanceID identifies this dispatcher instance. It becomes the
	// lease owner in StateDispatching.
	InstanceID string

	// LeaseDuration bounds a single dispatch attempt.
	LeaseDuration time.Duration

	// RequestTimeout bounds one SendMessage call.
	RequestTimeout time.Duration

	// FinalizeTimeout bounds the Store mutation that records an
	// outcome after SendMessage has started.
	//
	// Finalization uses context.WithoutCancel(ctx) with this timeout
	// so that a canceled caller context cannot prevent the outcome
	// from being persisted. A canceled finalization would leave the
	// entry in dispatching and force recovery to convert it to
	// uncertain even though the outcome was already known.
	FinalizeTimeout time.Duration

	// PollInterval is how long the dispatcher waits between scans
	// when no entry is ready.
	PollInterval time.Duration

	// BatchSize caps how many ready entries are considered per scan.
	BatchSize int

	// Backoff computes retry delays.
	Backoff Backoff

	// MaxAttempts caps the number of Sender.SendMessage calls per
	// entry.
	//
	// When claiming would create an attempt above this limit, the
	// dispatcher marks the entry as failed_permanent without calling
	// Sender.
	MaxAttempts int

	// MaxConsecutiveScanErrors caps how many scans in a row may fail
	// before Run gives up.
	//
	// Only failures before a send attempt (listing and claiming) are
	// retried: they have no side effect, and a short SQLite lock must
	// not stop delivery. Failures after a send started still end Run.
	MaxConsecutiveScanErrors int

	// Retention is how long accepted and canceled entries are kept
	// before Run deletes them. Zero selects the default; a negative
	// value disables purging.
	Retention time.Duration

	// PurgeInterval is how often Run purges while it keeps running.
	PurgeInterval time.Duration

	// Logger receives structured diagnostics. Message text is never
	// logged; error strings are passed through SafeReason. When nil,
	// the dispatcher discards: it never falls back to slog.Default(),
	// whose default destination is the terminal an interface may own.
	Logger *slog.Logger
}

// DefaultDispatcherConfig returns the initial production policy.
func DefaultDispatcherConfig(
	instanceID string,
) DispatcherConfig {
	return DispatcherConfig{
		InstanceID:      instanceID,
		LeaseDuration:   2 * time.Minute,
		RequestTimeout:  60 * time.Second,
		FinalizeTimeout: 5 * time.Second,
		PollInterval:    5 * time.Second,
		BatchSize:       16,
		Backoff:         DefaultBackoff(),
		MaxAttempts:     5,

		MaxConsecutiveScanErrors: 10,

		Retention:     7 * 24 * time.Hour,
		PurgeInterval: time.Hour,
	}
}

// Dispatcher moves ready entries through the send pipeline.
//
// A Dispatcher instance must have at most one active Run or ScanOnce
// call. Multiple Dispatcher instances coordinate through Store.Claim.
type Dispatcher struct {
	store  Store
	sender Sender
	clock  Clock
	cfg    DispatcherConfig

	mu      sync.Mutex
	stopped bool

	// wake coalesces enqueue notifications into at most one pending
	// wake-up of an idle Run.
	wake chan struct{}
}

// NewDispatcher wires a dispatcher.
func NewDispatcher(
	store Store,
	sender Sender,
	clock Clock,
	cfg DispatcherConfig,
) *Dispatcher {
	if clock == nil {
		clock = SystemClock{}
	}
	if cfg.InstanceID == "" {
		cfg.InstanceID = "dispatcher"
	}
	if cfg.LeaseDuration <= 0 {
		cfg.LeaseDuration = 2 * time.Minute
	}
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = 60 * time.Second
	}
	if cfg.FinalizeTimeout <= 0 {
		cfg.FinalizeTimeout = 5 * time.Second
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 5 * time.Second
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 16
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 5
	}
	if cfg.MaxConsecutiveScanErrors <= 0 {
		cfg.MaxConsecutiveScanErrors = 10
	}
	if cfg.Retention == 0 {
		cfg.Retention = 7 * 24 * time.Hour
	}
	if cfg.PurgeInterval <= 0 {
		cfg.PurgeInterval = time.Hour
	}
	if cfg.Logger == nil {
		// A dispatcher with no logger says nothing. The default would
		// write to the terminal, and a terminal running the interface is
		// not a place a log line may go.
		cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	return &Dispatcher{
		store:  store,
		sender: sender,
		clock:  clock,
		cfg:    cfg,
		wake:   make(chan struct{}, 1),
	}
}

// Notify wakes an idle Run so a newly queued entry is sent at once
// instead of after the poll interval. It never blocks; notifications
// that arrive while one is pending coalesce.
func (d *Dispatcher) Notify() {
	select {
	case d.wake <- struct{}{}:
	default:
	}
}

// Run drives the dispatcher until ctx is done.
//
// The first action is recovery: dispatching entries with an expired
// lease become uncertain. Then the dispatcher loops, listing ready
// entries and dispatching them one by one.
//
// Cancellation of the Run context is a graceful shutdown: Run returns
// nil. Any other error, including context.DeadlineExceeded produced
// by an internal bounded operation such as detached finalization, is
// a real dispatcher error and is returned to the caller. Hiding an
// internal deadline would leave the entry in dispatching and force
// recovery to convert it to uncertain even though the outcome was
// already known.
func (d *Dispatcher) Run(
	ctx context.Context,
) error {
	if d.store == nil {
		return errors.New(
			"outbox dispatcher: nil store",
		)
	}
	if d.sender == nil {
		return errors.New(
			"outbox dispatcher: nil sender",
		)
	}

	// A context canceled before Run starts is a graceful shutdown.
	// Do not enter recovery with an already canceled context.
	if ctx.Err() != nil {
		return nil
	}

	if _, err := d.RecoverInterrupted(ctx); err != nil {
		// Cancellation of the Run context during recovery is also a
		// graceful shutdown. Errors originating inside recovery are
		// returned when the Run context remains active.
		if ctx.Err() != nil {
			return nil
		}

		return fmt.Errorf(
			"outbox dispatcher: recover: %w",
			err,
		)
	}

	scanErrors := 0
	var lastPurge time.Time

	for {
		if ctx.Err() != nil {
			return nil
		}

		if now := d.clock.Now(); lastPurge.IsZero() ||
			now.Sub(lastPurge) >= d.cfg.PurgeInterval {
			d.purgeFinished(ctx, now)
			lastPurge = now
		}

		processed, err := d.scanOnce(ctx)
		if err != nil {
			// Cancellation of the Run context is a graceful
			// shutdown. Cancellation or deadline from an internal
			// operation is a dispatcher error and must not be
			// hidden.
			if ctx.Err() != nil {
				return nil
			}

			var scanErr *scanError
			if !errors.As(err, &scanErr) {
				return err
			}

			scanErrors++
			if scanErrors >= d.cfg.MaxConsecutiveScanErrors {
				return fmt.Errorf(
					"outbox dispatcher: %d consecutive scan failures: %w",
					scanErrors,
					err,
				)
			}

			d.cfg.Logger.Warn(
				"outbox scan failed; retrying",
				slog.Int("consecutive_failures", scanErrors),
				slog.String("error", SafeReason(err)),
			)

			select {
			case <-ctx.Done():
				return nil
			case <-d.clock.After(d.scanRetryDelay(scanErrors)):
			}
			continue
		}
		scanErrors = 0

		if processed {
			continue
		}

		select {
		case <-ctx.Done():
			return nil

		case <-d.wake:

		case <-d.clock.After(
			d.cfg.PollInterval,
		):
		}
	}
}

// Stop marks the dispatcher as stopped. Further scans become no-ops.
func (d *Dispatcher) Stop() {
	d.mu.Lock()
	d.stopped = true
	d.mu.Unlock()
}

// RecoverInterrupted converts expired dispatching entries to
// uncertain. It is called once at Run startup and can be invoked
// directly from tests.
func (d *Dispatcher) RecoverInterrupted(
	ctx context.Context,
) (int, error) {
	if d.store == nil {
		return 0, errors.New(
			"outbox dispatcher: nil store",
		)
	}

	return d.store.RecoverInterrupted(
		ctx,
		d.clock.Now(),
	)
}

// ScanOnce runs a single dispatch cycle.
//
// It returns true when at least one entry was processed.
func (d *Dispatcher) ScanOnce(
	ctx context.Context,
) (bool, error) {
	if d.store == nil {
		return false, errors.New(
			"outbox dispatcher: nil store",
		)
	}
	if d.sender == nil {
		return false, errors.New(
			"outbox dispatcher: nil sender",
		)
	}

	return d.scanOnce(ctx)
}

func (d *Dispatcher) scanOnce(
	ctx context.Context,
) (bool, error) {
	d.mu.Lock()
	stopped := d.stopped
	d.mu.Unlock()

	if stopped {
		return false, nil
	}

	now := d.clock.Now()

	ready, err := d.store.ListReady(
		ctx,
		now,
		d.cfg.BatchSize,
	)
	if err != nil {
		return false, &scanError{err: fmt.Errorf(
			"list ready: %w",
			err,
		)}
	}
	if len(ready) == 0 {
		return false, nil
	}

	processedAny := false

	for _, entry := range ready {
		if ctx.Err() != nil {
			return processedAny, nil
		}

		if err := d.dispatch(
			ctx,
			entry,
		); err != nil {
			return processedAny, err
		}

		processedAny = true
	}

	return processedAny, nil
}

// dispatch runs one full attempt for a single ready entry.
func (d *Dispatcher) dispatch(
	ctx context.Context,
	entry Entry,
) error {
	now := d.clock.Now()
	leaseUntil := now.Add(
		d.cfg.LeaseDuration,
	)

	claimed, err := d.store.Claim(
		ctx,
		entry.ID,
		entry.Version,
		d.cfg.InstanceID,
		leaseUntil,
		now,
	)
	if err != nil {
		if errors.Is(
			err,
			ErrVersionConflict,
		) || errors.Is(
			err,
			ErrLeaseHeld,
		) {
			return nil
		}

		return &scanError{err: fmt.Errorf(
			"claim %s: %w",
			entry.ID,
			err,
		)}
	}

	// The claimed entry has already transitioned to dispatching.
	// When the claim creates an attempt above MaxAttempts, record a
	// permanent failure without invoking Sender.
	if claimed.AttemptCount >
		d.cfg.MaxAttempts {
		finalizeCtx, cancelFinalize :=
			context.WithTimeout(
				context.WithoutCancel(ctx),
				d.cfg.FinalizeTimeout,
			)
		defer cancelFinalize()

		return d.markPermanent(
			finalizeCtx,
			claimed,
			&SendError{
				Permanent: true,
			},
		)
	}

	requestCtx, cancelRequest :=
		context.WithTimeout(
			ctx,
			d.cfg.RequestTimeout,
		)
	defer cancelRequest()

	sentMessage, sendErr :=
		d.sender.SendMessage(
			requestCtx,
			claimed.ChatID,
			claimed.Text,
		)

	// From this point the send attempt has started. Pre-send errors
	// were handled before Claim and never reach Classify.
	outcome := Classify(
		sendErr,
		true,
	)

	d.logDispatch(
		claimed,
		outcome,
		sendErr,
	)

	// Finalize with a detached context. Even if the caller context
	// was canceled during or after SendMessage, the outcome must be
	// persisted.
	finalizeCtx, cancelFinalize :=
		context.WithTimeout(
			context.WithoutCancel(ctx),
			d.cfg.FinalizeTimeout,
		)
	defer cancelFinalize()

	switch outcome {
	case OutcomeAccepted:
		return d.handleAccepted(
			finalizeCtx,
			claimed,
			sentMessage,
		)

	case OutcomeRetryable:
		return d.markRetryable(
			finalizeCtx,
			claimed,
			sendErr,
		)

	case OutcomePermanent:
		return d.markPermanent(
			finalizeCtx,
			claimed,
			sendErr,
		)

	case OutcomeUncertain:
		return d.markUncertain(
			finalizeCtx,
			claimed,
			SafeReason(sendErr),
		)

	default:
		return d.markUncertain(
			finalizeCtx,
			claimed,
			"unknown dispatch outcome",
		)
	}
}

func (d *Dispatcher) handleAccepted(
	ctx context.Context,
	entry Entry,
	sent SentMessage,
) error {
	if sent.ID == 0 {
		return d.markUncertain(
			ctx,
			entry,
			"sender returned zero message id",
		)
	}

	if sent.ChatID != entry.ChatID {
		return d.markUncertain(
			ctx,
			entry,
			"sender returned mismatched chat id",
		)
	}

	return d.markAccepted(
		ctx,
		entry,
		sent.ID,
	)
}

func (d *Dispatcher) markAccepted(
	ctx context.Context,
	entry Entry,
	messageID int64,
) error {
	_, err := d.store.MarkAccepted(
		ctx,
		entry.ID,
		entry.Version,
		messageID,
		d.clock.Now(),
	)
	if errors.Is(err, ErrVersionConflict) {
		d.logLostFinalization(entry, OutcomeAccepted)
		return nil
	}
	if err != nil {
		return fmt.Errorf(
			"mark accepted %s: %w",
			entry.ID,
			err,
		)
	}

	return nil
}

func (d *Dispatcher) markRetryable(
	ctx context.Context,
	entry Entry,
	sendErr error,
) error {
	delay, ok := d.cfg.Backoff.NextDelay(
		entry.AttemptCount,
	)
	if !ok {
		return d.markPermanent(
			ctx,
			entry,
			sendErr,
		)
	}

	code := 0

	if details, ok := SendErrorDetails(
		sendErr,
	); ok {
		code = details.Code
	}

	now := d.clock.Now()

	_, err := d.store.MarkRetryable(
		ctx,
		entry.ID,
		entry.Version,
		now.Add(delay),
		code,
		SafeReason(sendErr),
		now,
	)
	if errors.Is(err, ErrVersionConflict) {
		d.logLostFinalization(entry, OutcomeRetryable)
		return nil
	}
	if err != nil {
		return fmt.Errorf(
			"mark retryable %s: %w",
			entry.ID,
			err,
		)
	}

	return nil
}

func (d *Dispatcher) markPermanent(
	ctx context.Context,
	entry Entry,
	sendErr error,
) error {
	code := 0

	if details, ok := SendErrorDetails(
		sendErr,
	); ok {
		code = details.Code
	}

	_, err := d.store.MarkPermanentFailure(
		ctx,
		entry.ID,
		entry.Version,
		code,
		SafeReason(sendErr),
		d.clock.Now(),
	)
	if errors.Is(err, ErrVersionConflict) {
		d.logLostFinalization(entry, OutcomePermanent)
		return nil
	}
	if err != nil {
		return fmt.Errorf(
			"mark permanent %s: %w",
			entry.ID,
			err,
		)
	}

	return nil
}

func (d *Dispatcher) markUncertain(
	ctx context.Context,
	entry Entry,
	reason string,
) error {
	_, err := d.store.MarkUncertain(
		ctx,
		entry.ID,
		entry.Version,
		reason,
		d.clock.Now(),
	)
	if errors.Is(err, ErrVersionConflict) {
		d.logLostFinalization(entry, OutcomeUncertain)
		return nil
	}
	if err != nil {
		return fmt.Errorf(
			"mark uncertain %s: %w",
			entry.ID,
			err,
		)
	}

	return nil
}

// purgeFinished deletes delivered and canceled entries past the
// retention period. It is housekeeping: a failure is logged and never
// stops delivery.
func (d *Dispatcher) purgeFinished(ctx context.Context, now time.Time) {
	if d.cfg.Retention < 0 {
		return
	}
	purged, err := d.store.PurgeFinished(ctx, now.Add(-d.cfg.Retention))
	if err != nil {
		if ctx.Err() == nil {
			d.cfg.Logger.Warn(
				"outbox purge failed",
				slog.String("error", SafeReason(err)),
			)
		}
		return
	}
	if purged > 0 {
		d.cfg.Logger.Info("outbox purge", slog.Int("purged", purged))
	}
}

// scanError marks a failure before any send attempt. It has no side
// effect, so Run may retry it.
type scanError struct {
	err error
}

func (e *scanError) Error() string { return e.err.Error() }
func (e *scanError) Unwrap() error { return e.err }

// scanRetryDelay doubles the poll interval per consecutive failure, up
// to one minute.
func (d *Dispatcher) scanRetryDelay(failures int) time.Duration {
	delay := d.cfg.PollInterval
	for i := 1; i < failures && delay < time.Minute; i++ {
		delay *= 2
	}
	return min(delay, time.Minute)
}

// logLostFinalization records an outcome that could not be persisted
// because another writer changed the entry first. The outcome is known
// here and nowhere else, so it must not vanish silently.
func (d *Dispatcher) logLostFinalization(entry Entry, outcome Outcome) {
	if d.cfg.Logger == nil {
		return
	}
	d.cfg.Logger.Warn(
		"outbox finalize lost to a concurrent update",
		slog.String("entry_id", string(entry.ID)),
		slog.Int("attempt", entry.AttemptCount),
		slog.String("outcome", outcome.String()),
	)
}

func (d *Dispatcher) logDispatch(
	entry Entry,
	outcome Outcome,
	err error,
) {
	if d.cfg.Logger == nil {
		return
	}

	attributes := []any{
		slog.String(
			"entry_id",
			string(entry.ID),
		),
		slog.Int64(
			"chat_id",
			entry.ChatID,
		),
		slog.Int(
			"attempt",
			entry.AttemptCount,
		),
		slog.String(
			"outcome",
			outcome.String(),
		),
	}

	if err != nil {
		attributes = append(
			attributes,
			slog.String(
				"error",
				SafeReason(err),
			),
		)
	}

	d.cfg.Logger.Info(
		"outbox dispatch",
		attributes...,
	)
}

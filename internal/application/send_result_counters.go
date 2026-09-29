package application

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// What the send-result reconciler saw, in numbers.
//
// The owner of a queue that says a message is "on its way out" for an
// hour needs to be able to answer one question without attaching a
// debugger to a running program: did the reconciler read Telegram's
// confirmation at all, and if it did, why did it not act on it. These
// counters are the answer, and `telecli doctor` prints them.
//
// They are counters and nothing else. No message text, no identifiers,
// no chat names: a diagnostic that carried any of those would be a copy
// of somebody's conversation in a file on disk (§19).
//
// What they count is the reconciliation of records that are *waiting*.
// A pass with nothing awaiting does not read a chat's window at all — there
// is nothing to match a confirmation against — so a run that sent nothing
// reports nothing, and a run whose records were all resolved reports what
// it saw while they were. That is the right scope for the question the
// numbers answer, which is what happened to a message that is stuck on
// "on its way out": there is always a record waiting for that one.
type sendResultCounters struct {
	// seen counts the send results read out of the live store's window.
	seen atomic.Uint64

	// matched counts the results that moved an entry to sent or to a
	// permanent failure.
	matched atomic.Uint64

	// noEntry counts the results that named a temporary identifier this
	// queue holds no accepted entry for, and that have been sitting in
	// the window unmatched for longer than unmatchedGrace. The grace is
	// what keeps a record the dispatcher has not written yet out of this
	// count.
	noEntry atomic.Uint64

	// windowGap counts the passes that could not read a chat's window
	// at all, because the window had moved past the beginning of it. This
	// is the one failure that cannot be diagnosed from the queue, and it
	// is the reason a confirmation can be delivered and never seen.
	windowGap atomic.Uint64

	// passes counts the reconcile passes, so a reader can tell an idle
	// reconciler from a busy one.
	passes atomic.Uint64
}

// sendResultCountReport is one reading of the counters, as doctor prints
// it.
type sendResultCountReport struct {
	StartedAt time.Time
	UpdatedAt time.Time
	Seen      uint64
	Matched   uint64
	NoEntry   uint64
	WindowGap uint64
	Passes    uint64
}

// sendResultCounterFileName is the file the counters are written to, in
// the data folder next to the queue itself.
//
// The data folder is the one place doctor already looks and the one place
// the user is already told about, so a counter that lives anywhere else
// would be a counter nobody finds. It is a separate file rather than a
// row in the queue because a count of diagnostics is not a message: it
// must not make the queue's schema move, and it must not be something a
// migration has to carry.
const sendResultCounterFileName = "send-results.json"

// sendResultCounterFile writes the counters for a later doctor run.
//
// A counter is written when it changes and no more often than
// sendResultCounterInterval, and once more when the runtime closes. The
// throttle matters because the reconciler passes every second for as long
// as the program runs, and a file rewritten every second for a week is
// its own kind of damage.
type sendResultCounterFile struct {
	path string

	mu     sync.Mutex
	last   sendResultCountReport
	lastAt time.Time

	// now is the clock the throttle reads. It is the runtime's clock, so
	// a test drives it rather than sleeping.
	now func() time.Time
}

// sendResultCounterInterval is the shortest time between two writes.
const sendResultCounterInterval = 5 * time.Second

// newSendResultCounterFile returns a writer for the data folder.
func newSendResultCounterFile(dataDir string) *sendResultCounterFile {
	return &sendResultCounterFile{
		path: filepath.Join(dataDir, sendResultCounterFileName),
		now:  time.Now,
	}
}

// write records the current counters, if they moved and the throttle
// allows it.
//
// A write that fails is not worth failing a reconcile pass over: the
// queue is the product and the counter is a note about it. The error is
// returned for the caller to log, and the next pass tries again.
func (f *sendResultCounterFile) write(
	counters *sendResultCounters,
	force bool,
) error {
	if f == nil || counters == nil {
		return nil
	}

	report := counters.report()

	f.mu.Lock()
	defer f.mu.Unlock()

	if !force {
		if report == f.last {
			return nil
		}
		if !f.lastAt.IsZero() &&
			f.now().Sub(f.lastAt) < sendResultCounterInterval {
			return nil
		}
	}
	f.last = report
	f.lastAt = f.now()

	return f.writeNow(report)
}

// writeNow replaces the file atomically.
//
// The write goes to a temporary name in the same folder and is renamed
// over the old one, so a doctor that reads the file while the program
// is running reads a whole file rather than half of one.
func (f *sendResultCounterFile) writeNow(
	report sendResultCountReport,
) error {
	encoded, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("encode send result counters: %w", err)
	}

	temp := f.path + ".tmp"
	if err := os.WriteFile(temp, encoded, 0o600); err != nil {
		return fmt.Errorf("write send result counters: %w", err)
	}
	if err := os.Rename(temp, f.path); err != nil {
		_ = os.Remove(temp)
		return fmt.Errorf("replace send result counters: %w", err)
	}
	return nil
}

// report reads the counters.
func (c *sendResultCounters) report() sendResultCountReport {
	return sendResultCountReport{
		Seen:      c.seen.Load(),
		Matched:   c.matched.Load(),
		NoEntry:   c.noEntry.Load(),
		WindowGap: c.windowGap.Load(),
		Passes:    c.passes.Load(),
	}
}

// readSendResultCounters reads what the last run recorded.
//
// A missing file is not an error: it means no run of the program with
// this queue has written one yet, which is what a queue that has never
// been sent from looks like. Doctor says so in words rather than
// printing zeroes, because zeroes would say the reconciler saw nothing
// when the truth is that it has never run.
func readSendResultCounters(
	dataDir string,
) (sendResultCountReport, bool) {
	raw, err := os.ReadFile(
		filepath.Join(dataDir, sendResultCounterFileName),
	)
	if err != nil {
		return sendResultCountReport{}, false
	}

	var report sendResultCountReport
	if err := json.Unmarshal(raw, &report); err != nil {
		return sendResultCountReport{}, false
	}
	return report, true
}

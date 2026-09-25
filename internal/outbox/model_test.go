package outbox

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func validEntry() Entry {
	now := time.Unix(1700000000, 0).UTC()
	return Entry{
		ID:         "op-1",
		AccountKey: "primary",
		ChatID:     42,
		Text:       "hello",
		State:      StateQueued,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
}

func validDispatchingEntry() Entry {
	e := validEntry()
	e.State = StateDispatching
	e.AttemptCount = 1
	e.LeaseOwner = "dispatcher-1"
	e.LeaseUntil = time.Unix(1700009999, 0).UTC()
	return e
}

// ---- State ----

func TestStateValid(t *testing.T) {
	valid := []State{
		StateQueued, StateDispatching, StateAccepted,
		StateFailedRetryable, StateFailedPermanent,
		StateUncertain, StateCanceled,
	}
	for _, s := range valid {
		if !s.Valid() {
			t.Fatalf("%q should be valid", s)
		}
		if s.String() != string(s) {
			t.Fatalf("String mismatch for %q", s)
		}
	}
	if State("bogus").Valid() {
		t.Fatal("unknown State must be invalid")
	}
}

func TestStateTerminal(t *testing.T) {
	terminal := []State{
		StateAccepted,
		StateFailedPermanent,
		StateUncertain,
		StateCanceled,
	}
	for _, state := range terminal {
		if !state.Terminal() {
			t.Fatalf("%q should be terminal for automatic processing", state)
		}
	}

	nonTerminal := []State{
		StateQueued,
		StateDispatching,
		StateFailedRetryable,
	}
	for _, state := range nonTerminal {
		if state.Terminal() {
			t.Fatalf("%q should not be terminal", state)
		}
	}
}

// ---- Validate ----

func TestEntryValidatePositive(t *testing.T) {
	if err := validEntry().Validate(); err != nil {
		t.Fatalf("valid entry rejected: %v", err)
	}
	if err := validDispatchingEntry().Validate(); err != nil {
		t.Fatalf("valid dispatching entry rejected: %v", err)
	}
}

func TestEntryValidateRejectsEmptyID(t *testing.T) {
	e := validEntry()
	e.ID = ""
	if err := e.Validate(); !errors.Is(err, ErrInvalidEntry) {
		t.Fatalf("err = %v, want ErrInvalidEntry", err)
	}
}

func TestEntryValidateRejectsEmptyAccountKey(t *testing.T) {
	for _, key := range []string{"", " ", "\t", "\n"} {
		e := validEntry()
		e.AccountKey = key
		if err := e.Validate(); !errors.Is(err, ErrInvalidEntry) {
			t.Fatalf("key=%q: err = %v, want ErrInvalidEntry", key, err)
		}
	}
}

func TestEntryValidateRejectsZeroChatID(t *testing.T) {
	e := validEntry()
	e.ChatID = 0
	if err := e.Validate(); !errors.Is(err, ErrInvalidEntry) {
		t.Fatalf("err = %v, want ErrInvalidEntry", err)
	}
}

func TestEntryValidateRejectsBlankText(t *testing.T) {
	for _, text := range []string{"", " ", "\t", "\n", "  \t\n "} {
		e := validEntry()
		e.Text = text
		if err := e.Validate(); !errors.Is(err, ErrInvalidEntry) {
			t.Fatalf("text=%q: err = %v, want ErrInvalidEntry", text, err)
		}
	}
}

func TestEntryValidateRejectsUnknownState(t *testing.T) {
	e := validEntry()
	e.State = "future"
	if err := e.Validate(); !errors.Is(err, ErrInvalidEntry) {
		t.Fatalf("err = %v, want ErrInvalidEntry", err)
	}
}

func TestEntryValidateRejectsNegativeAttempts(t *testing.T) {
	e := validEntry()
	e.AttemptCount = -1
	if err := e.Validate(); !errors.Is(err, ErrInvalidEntry) {
		t.Fatalf("err = %v, want ErrInvalidEntry", err)
	}
}

func TestEntryValidateRejectsZeroTimestamps(t *testing.T) {
	e := validEntry()
	e.CreatedAt = time.Time{}
	if err := e.Validate(); !errors.Is(err, ErrInvalidEntry) {
		t.Fatalf("zero CreatedAt: err = %v, want ErrInvalidEntry", err)
	}

	e = validEntry()
	e.UpdatedAt = time.Time{}
	if err := e.Validate(); !errors.Is(err, ErrInvalidEntry) {
		t.Fatalf("zero UpdatedAt: err = %v, want ErrInvalidEntry", err)
	}
}

func TestEntryValidateRejectsNextAttemptOutsideRetryable(t *testing.T) {
	for _, s := range []State{
		StateQueued, StateDispatching, StateAccepted,
		StateFailedPermanent, StateUncertain, StateCanceled,
	} {
		e := validEntry()
		e.State = s
		e.NextAttempt = time.Unix(1700000100, 0).UTC()

		if s == StateDispatching {
			e.LeaseOwner = "dispatcher-1"
			e.LeaseUntil = time.Unix(1700009999, 0).UTC()
		}
		if s == StateAccepted {
			e.TelegramMessageID = 1
			e.AcceptedAt = time.Unix(1700000000, 0).UTC()
		}

		if err := e.Validate(); !errors.Is(err, ErrInvalidEntry) {
			t.Fatalf("state=%s: err = %v, want ErrInvalidEntry", s, err)
		}
	}
}

func TestEntryValidateRejectsRetryableWithoutNextAttempt(t *testing.T) {
	e := validEntry()
	e.State = StateFailedRetryable

	if err := e.Validate(); !errors.Is(err, ErrInvalidEntry) {
		t.Fatalf("err = %v, want ErrInvalidEntry", err)
	}
}

func TestEntryValidateAcceptsRetryableWithNextAttempt(t *testing.T) {
	e := validEntry()
	e.State = StateFailedRetryable
	e.NextAttempt = time.Unix(1700000100, 0).UTC()

	if err := e.Validate(); err != nil {
		t.Fatalf("retryable entry rejected: %v", err)
	}
}

func TestEntryValidateRejectsLeaseOutsideDispatching(t *testing.T) {
	for _, s := range []State{
		StateQueued, StateAccepted, StateFailedRetryable,
		StateFailedPermanent, StateUncertain, StateCanceled,
	} {
		e := validEntry()
		e.State = s
		e.LeaseOwner = "dispatcher-1"
		e.LeaseUntil = time.Unix(1700009999, 0).UTC()
		if s == StateFailedRetryable {
			e.NextAttempt = time.Unix(1700000100, 0).UTC()
		}
		if s == StateAccepted {
			e.TelegramMessageID = 1
			e.AcceptedAt = time.Unix(1700000000, 0).UTC()
		}
		if err := e.Validate(); !errors.Is(err, ErrInvalidEntry) {
			t.Fatalf("state=%s: err = %v, want ErrInvalidEntry", s, err)
		}
	}
}

func TestEntryValidateRejectsDispatchingWithoutLease(t *testing.T) {
	e := validEntry()
	e.State = StateDispatching
	if err := e.Validate(); !errors.Is(err, ErrInvalidEntry) {
		t.Fatalf("no owner: err = %v, want ErrInvalidEntry", err)
	}

	e = validEntry()
	e.State = StateDispatching
	e.LeaseOwner = "dispatcher-1"
	if err := e.Validate(); !errors.Is(err, ErrInvalidEntry) {
		t.Fatalf("no deadline: err = %v, want ErrInvalidEntry", err)
	}
}

func TestEntryValidateRejectsAcceptedWithoutMessageID(t *testing.T) {
	e := validEntry()
	e.State = StateAccepted
	e.AcceptedAt = time.Unix(1700000000, 0).UTC()
	if err := e.Validate(); !errors.Is(err, ErrInvalidEntry) {
		t.Fatalf("err = %v, want ErrInvalidEntry", err)
	}
}

func TestEntryValidateRejectsAcceptedWithoutAcceptedAt(t *testing.T) {
	e := validEntry()
	e.State = StateAccepted
	e.TelegramMessageID = 9001
	if err := e.Validate(); !errors.Is(err, ErrInvalidEntry) {
		t.Fatalf("err = %v, want ErrInvalidEntry", err)
	}
}

func TestEntryValidateRejectsMessageIDOutsideAccepted(t *testing.T) {
	e := validEntry()
	e.TelegramMessageID = 9001
	if err := e.Validate(); !errors.Is(err, ErrInvalidEntry) {
		t.Fatalf("err = %v, want ErrInvalidEntry", err)
	}
}

func TestEntryValidateRejectsAcceptedAtOutsideAccepted(t *testing.T) {
	e := validEntry()
	e.AcceptedAt = time.Unix(1700000000, 0).UTC()
	if err := e.Validate(); !errors.Is(err, ErrInvalidEntry) {
		t.Fatalf("err = %v, want ErrInvalidEntry", err)
	}
}

// ---- CanTransition ----

func TestCanTransitionAllowed(t *testing.T) {
	type testCase struct{ from, to State }
	allowed := []testCase{
		{StateQueued, StateDispatching},
		{StateQueued, StateCanceled},
		{StateDispatching, StateAccepted},
		{StateDispatching, StateFailedRetryable},
		{StateDispatching, StateFailedPermanent},
		{StateDispatching, StateUncertain},
		{StateFailedRetryable, StateDispatching},
		{StateFailedRetryable, StateQueued},
		{StateFailedRetryable, StateCanceled},
		{StateUncertain, StateCanceled},
	}
	for _, c := range allowed {
		e := validEntry()
		e.State = c.from
		if !e.CanTransition(c.to) {
			t.Fatalf("%s -> %s should be allowed", c.from, c.to)
		}
	}
}

func TestCanTransitionForbidden(t *testing.T) {
	type testCase struct{ from, to State }
	forbidden := []testCase{
		{StateQueued, StateAccepted},
		{StateQueued, StateFailedPermanent},
		{StateQueued, StateUncertain},
		{StateDispatching, StateQueued},
		{StateAccepted, StateQueued},
		{StateAccepted, StateDispatching},
		{StateFailedPermanent, StateQueued},
		{StateCanceled, StateQueued},
		{StateUncertain, StateQueued},
		{StateUncertain, StateDispatching},
	}
	for _, c := range forbidden {
		e := validEntry()
		e.State = c.from
		if e.CanTransition(c.to) {
			t.Fatalf("%s -> %s should be forbidden", c.from, c.to)
		}
	}
}

// ---- Claim ----

func TestClaimSetsLeaseAndIncrementsAttempt(t *testing.T) {
	entry := validEntry()
	now := time.Unix(1700000100, 0).UTC()
	leaseUntil := now.Add(time.Minute)

	claimed, err := entry.Claim("dispatcher-1", leaseUntil, now)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}

	if claimed.State != StateDispatching {
		t.Fatalf("state = %s, want dispatching", claimed.State)
	}
	if claimed.LeaseOwner != "dispatcher-1" {
		t.Fatalf("lease owner = %q", claimed.LeaseOwner)
	}
	if !claimed.LeaseUntil.Equal(leaseUntil) {
		t.Fatalf("lease until = %v, want %v", claimed.LeaseUntil, leaseUntil)
	}
	if claimed.AttemptCount != entry.AttemptCount+1 {
		t.Fatalf("attempt count = %d, want %d",
			claimed.AttemptCount, entry.AttemptCount+1)
	}
	if claimed.Version != entry.Version+1 {
		t.Fatalf("version = %d, want %d",
			claimed.Version, entry.Version+1)
	}
	if !claimed.UpdatedAt.Equal(now) {
		t.Fatalf("UpdatedAt = %v, want %v", claimed.UpdatedAt, now)
	}
}

// A failed_retryable entry may be claimed directly, without going back
// through queued.
func TestClaimRetryableEntry(t *testing.T) {
	entry := validEntry()
	entry.State = StateFailedRetryable
	entry.NextAttempt = time.Unix(1700000050, 0).UTC()

	now := time.Unix(1700000100, 0).UTC()

	claimed, err := entry.Claim("dispatcher-1", now.Add(time.Minute), now)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if claimed.State != StateDispatching {
		t.Fatalf("state = %s, want dispatching", claimed.State)
	}
	if claimed.AttemptCount != 1 {
		t.Fatalf("attempt count = %d, want 1", claimed.AttemptCount)
	}
}

// Claiming a retryable entry must clear NextAttempt: the field is
// meaningful only in StateFailedRetryable.
func TestClaimRetryableClearsNextAttempt(t *testing.T) {
	entry := validDispatchingEntry()

	now := time.Unix(1700000200, 0).UTC()

	retryable, err := entry.MarkRetryable(
		now.Add(time.Minute), 500, "temporary", now,
	)
	if err != nil {
		t.Fatalf("MarkRetryable: %v", err)
	}
	if retryable.NextAttempt.IsZero() {
		t.Fatal("MarkRetryable must set NextAttempt")
	}

	claimed, err := retryable.Claim(
		"dispatcher-2",
		now.Add(2*time.Minute),
		now.Add(time.Minute),
	)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if !claimed.NextAttempt.IsZero() {
		t.Fatalf("NextAttempt = %v, want zero", claimed.NextAttempt)
	}
}

func TestClaimRejectsEmptyOwner(t *testing.T) {
	entry := validEntry()
	now := time.Unix(1700000100, 0).UTC()

	for _, owner := range []string{"", " ", "\t", "\n"} {
		if _, err := entry.Claim(owner, now.Add(time.Minute), now); !errors.Is(err, ErrInvalidEntry) {
			t.Fatalf("owner=%q: err = %v, want ErrInvalidEntry", owner, err)
		}
	}
}

func TestClaimRejectsExpiredLease(t *testing.T) {
	entry := validEntry()
	now := time.Unix(1700000100, 0).UTC()

	if _, err := entry.Claim("d1", now, now); !errors.Is(err, ErrInvalidEntry) {
		t.Fatalf("same-time lease: err = %v, want ErrInvalidEntry", err)
	}
	if _, err := entry.Claim("d1", now.Add(-time.Second), now); !errors.Is(err, ErrInvalidEntry) {
		t.Fatalf("past lease: err = %v, want ErrInvalidEntry", err)
	}
}

func TestClaimRejectsNonClaimable(t *testing.T) {
	now := time.Unix(1700000100, 0).UTC()
	lease := now.Add(time.Minute)

	accepted := validEntry()
	accepted.State = StateAccepted
	accepted.TelegramMessageID = 1
	accepted.AcceptedAt = now
	if _, err := accepted.Claim("d2", lease, now); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("accepted: err = %v, want ErrInvalidTransition", err)
	}

	canceled := validEntry()
	canceled.State = StateCanceled
	if _, err := canceled.Claim("d2", lease, now); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("canceled: err = %v, want ErrInvalidTransition", err)
	}

	uncertain := validEntry()
	uncertain.State = StateUncertain
	if _, err := uncertain.Claim("d2", lease, now); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("uncertain: err = %v, want ErrInvalidTransition", err)
	}
}

// ---- Accept ----

func TestAcceptSetsMessageIDAndClearsLease(t *testing.T) {
	entry := validDispatchingEntry()
	now := time.Unix(1700000200, 0).UTC()

	accepted, err := entry.Accept(9001, now)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}

	if accepted.State != StateAccepted {
		t.Fatalf("state = %s, want accepted", accepted.State)
	}
	if accepted.TelegramMessageID != 9001 {
		t.Fatalf("message id = %d, want 9001", accepted.TelegramMessageID)
	}
	if !accepted.AcceptedAt.Equal(now) {
		t.Fatalf("accepted at = %v, want %v", accepted.AcceptedAt, now)
	}
	if accepted.LeaseOwner != "" || !accepted.LeaseUntil.IsZero() {
		t.Fatalf("lease not cleared: %+v", accepted)
	}
	if !accepted.NextAttempt.IsZero() {
		t.Fatalf("NextAttempt = %v, want zero", accepted.NextAttempt)
	}
	if accepted.Version != entry.Version+1 {
		t.Fatalf("version = %d, want %d",
			accepted.Version, entry.Version+1)
	}
}

func TestAcceptRejectsZeroMessageID(t *testing.T) {
	entry := validDispatchingEntry()
	now := time.Unix(1700000200, 0).UTC()

	if _, err := entry.Accept(0, now); !errors.Is(err, ErrInvalidEntry) {
		t.Fatalf("err = %v, want ErrInvalidEntry", err)
	}
}

func TestAcceptRejectsNonDispatching(t *testing.T) {
	now := time.Unix(1700000200, 0).UTC()

	queued := validEntry()
	if _, err := queued.Accept(9001, now); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("queued: err = %v, want ErrInvalidTransition", err)
	}

	accepted := validEntry()
	accepted.State = StateAccepted
	accepted.TelegramMessageID = 1
	accepted.AcceptedAt = now
	if _, err := accepted.Accept(9001, now); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("accepted: err = %v, want ErrInvalidTransition", err)
	}
}

// ---- MarkRetryable ----

func TestMarkRetryableSetsNextAttemptAndClearsLease(t *testing.T) {
	entry := validDispatchingEntry()
	now := time.Unix(1700000200, 0).UTC()
	next := now.Add(5 * time.Second)

	retry, err := entry.MarkRetryable(next, 500, "temporary", now)
	if err != nil {
		t.Fatalf("MarkRetryable: %v", err)
	}

	if retry.State != StateFailedRetryable {
		t.Fatalf("state = %s", retry.State)
	}
	if !retry.NextAttempt.Equal(next) {
		t.Fatalf("next attempt = %v, want %v", retry.NextAttempt, next)
	}
	if retry.LastErrorCode != 500 || retry.LastErrorMessage != "temporary" {
		t.Fatalf("last error = (%d, %q)",
			retry.LastErrorCode, retry.LastErrorMessage)
	}
	if retry.LeaseOwner != "" || !retry.LeaseUntil.IsZero() {
		t.Fatalf("lease not cleared: %+v", retry)
	}
	if retry.Version != entry.Version+1 {
		t.Fatalf("version = %d, want %d",
			retry.Version, entry.Version+1)
	}
}

func TestMarkRetryableRejectsPastNextAttempt(t *testing.T) {
	entry := validDispatchingEntry()
	now := time.Unix(1700000200, 0).UTC()

	if _, err := entry.MarkRetryable(now.Add(-time.Second), 500, "x", now); !errors.Is(err, ErrInvalidEntry) {
		t.Fatalf("err = %v, want ErrInvalidEntry", err)
	}
}

func TestMarkRetryableRejectsNonDispatching(t *testing.T) {
	now := time.Unix(1700000200, 0).UTC()
	queued := validEntry()

	if _, err := queued.MarkRetryable(now.Add(time.Second), 500, "x", now); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("err = %v, want ErrInvalidTransition", err)
	}
}

// ---- MarkPermanentFailure ----

func TestMarkPermanentFailureSetsStateAndClearsLease(t *testing.T) {
	entry := validDispatchingEntry()
	now := time.Unix(1700000200, 0).UTC()

	perm, err := entry.MarkPermanentFailure(400, "chat not found", now)
	if err != nil {
		t.Fatalf("MarkPermanentFailure: %v", err)
	}

	if perm.State != StateFailedPermanent {
		t.Fatalf("state = %s", perm.State)
	}
	if perm.LastErrorCode != 400 || perm.LastErrorMessage != "chat not found" {
		t.Fatalf("last error = (%d, %q)",
			perm.LastErrorCode, perm.LastErrorMessage)
	}
	if perm.LeaseOwner != "" || !perm.LeaseUntil.IsZero() {
		t.Fatalf("lease not cleared: %+v", perm)
	}
	if !perm.NextAttempt.IsZero() {
		t.Fatalf("next attempt should be zero: %v", perm.NextAttempt)
	}
}

func TestMarkPermanentFailureRejectsNonDispatching(t *testing.T) {
	now := time.Unix(1700000200, 0).UTC()
	queued := validEntry()

	if _, err := queued.MarkPermanentFailure(400, "x", now); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("err = %v, want ErrInvalidTransition", err)
	}
}

// ---- MarkUncertain ----

func TestMarkUncertainClearsLease(t *testing.T) {
	entry := validDispatchingEntry()
	now := time.Unix(1700000300, 0).UTC()

	uncertain, err := entry.MarkUncertain("process exit during dispatch", now)
	if err != nil {
		t.Fatalf("MarkUncertain: %v", err)
	}

	if uncertain.State != StateUncertain {
		t.Fatalf("state = %s, want uncertain", uncertain.State)
	}
	if uncertain.LeaseOwner != "" || !uncertain.LeaseUntil.IsZero() {
		t.Fatalf("lease not cleared: %+v", uncertain)
	}
	if !uncertain.NextAttempt.IsZero() {
		t.Fatalf("NextAttempt = %v, want zero", uncertain.NextAttempt)
	}
	if uncertain.LastErrorMessage != "process exit during dispatch" {
		t.Fatalf("reason = %q", uncertain.LastErrorMessage)
	}
	if uncertain.Version != entry.Version+1 {
		t.Fatalf("version = %d, want %d",
			uncertain.Version, entry.Version+1)
	}
}

func TestMarkUncertainRejectsBlankReason(t *testing.T) {
	entry := validDispatchingEntry()
	now := time.Unix(1700000300, 0).UTC()

	for _, reason := range []string{"", " ", "\t", "\n", "  \t\n "} {
		if _, err := entry.MarkUncertain(reason, now); !errors.Is(err, ErrInvalidEntry) {
			t.Fatalf("reason=%q: err = %v, want ErrInvalidEntry", reason, err)
		}
	}
}

func TestMarkUncertainRejectsNonDispatching(t *testing.T) {
	now := time.Unix(1700000300, 0).UTC()
	queued := validEntry()

	if _, err := queued.MarkUncertain("x", now); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("err = %v, want ErrInvalidTransition", err)
	}
}

// ---- Cancel ----

func TestCancelFromQueued(t *testing.T) {
	entry := validEntry()
	now := time.Unix(1700000400, 0).UTC()

	canceled, err := entry.Cancel(now)
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if canceled.State != StateCanceled {
		t.Fatalf("state = %s", canceled.State)
	}
	if !canceled.UpdatedAt.Equal(now) {
		t.Fatalf("UpdatedAt = %v, want %v", canceled.UpdatedAt, now)
	}
}

func TestCancelFromRetryable(t *testing.T) {
	entry := validDispatchingEntry()
	now := time.Unix(1700000400, 0).UTC()

	retry, err := entry.MarkRetryable(now.Add(time.Second), 500, "x", now)
	if err != nil {
		t.Fatalf("MarkRetryable: %v", err)
	}
	canceled, err := retry.Cancel(now.Add(time.Second))
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if canceled.State != StateCanceled {
		t.Fatalf("state = %s", canceled.State)
	}
	if !canceled.NextAttempt.IsZero() {
		t.Fatalf("NextAttempt = %v, want zero", canceled.NextAttempt)
	}
}

func TestCancelFromUncertain(t *testing.T) {
	entry := validDispatchingEntry()
	now := time.Unix(1700000400, 0).UTC()

	uncertain, err := entry.MarkUncertain("result unknown", now)
	if err != nil {
		t.Fatalf("MarkUncertain: %v", err)
	}
	canceled, err := uncertain.Cancel(now.Add(time.Second))
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if canceled.State != StateCanceled {
		t.Fatalf("state = %s", canceled.State)
	}
}

func TestCancelRejectsAccepted(t *testing.T) {
	entry := validDispatchingEntry()
	now := time.Unix(1700000400, 0).UTC()

	accepted, err := entry.Accept(9001, now)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if _, err := accepted.Cancel(now.Add(time.Second)); !errors.Is(err, ErrCancelAfterAccepted) {
		t.Fatalf("err = %v, want ErrCancelAfterAccepted", err)
	}
}

func TestCancelRejectsDispatching(t *testing.T) {
	entry := validDispatchingEntry()
	now := time.Unix(1700000400, 0).UTC()

	if _, err := entry.Cancel(now); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("err = %v, want ErrInvalidTransition", err)
	}
}

// ---- transition helper ----

// The internal transition helper is used only for simple transitions
// such as queued -> canceled. It clears the lease when leaving
// dispatching and clears NextAttempt when leaving failed_retryable.
func TestTransitionToCanceledClearsLeaseAndIncrementsVersion(t *testing.T) {
	entry := validEntry()

	canceled, err := entry.transition(StateCanceled)
	if err != nil {
		t.Fatalf("transition: %v", err)
	}

	if canceled.State != StateCanceled {
		t.Fatalf("state = %s, want canceled", canceled.State)
	}
	if canceled.Version != entry.Version+1 {
		t.Fatalf("version = %d, want %d",
			canceled.Version, entry.Version+1)
	}
}

func TestTransitionFromRetryableClearsNextAttempt(t *testing.T) {
	entry := validDispatchingEntry()
	now := time.Unix(1700000200, 0).UTC()

	retry, err := entry.MarkRetryable(now.Add(time.Minute), 500, "x", now)
	if err != nil {
		t.Fatalf("MarkRetryable: %v", err)
	}

	queued, err := retry.transition(StateQueued)
	if err != nil {
		t.Fatalf("transition: %v", err)
	}
	if !queued.NextAttempt.IsZero() {
		t.Fatalf("NextAttempt = %v, want zero", queued.NextAttempt)
	}
}

func TestTransitionUnknownStateRejected(t *testing.T) {
	e := validEntry()
	if _, err := e.transition("future"); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("err = %v, want ErrInvalidTransition", err)
	}
}

// ---- IsReadyAt ----

func TestIsReadyAt(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()

	queued := validEntry()
	if !queued.IsReadyAt(now) {
		t.Fatal("queued entry must be ready")
	}

	retryable := validEntry()
	retryable.State = StateFailedRetryable
	retryable.NextAttempt = now.Add(time.Minute)
	if retryable.IsReadyAt(now) {
		t.Fatal("retryable entry must not be ready before NextAttempt")
	}
	if !retryable.IsReadyAt(now.Add(time.Minute)) {
		t.Fatal("retryable entry must be ready at NextAttempt")
	}

	dispatching := validDispatchingEntry()
	if dispatching.IsReadyAt(now) {
		t.Fatal("dispatching entry must not be ready")
	}

	for _, s := range []State{
		StateAccepted, StateFailedPermanent, StateUncertain, StateCanceled,
	} {
		e := validEntry()
		e.State = s
		if s == StateAccepted {
			e.TelegramMessageID = 1
			e.AcceptedAt = now
		}
		if e.IsReadyAt(now) {
			t.Fatalf("state %s must not be ready for dispatch", s)
		}
	}
}

// ---- Credential regression guard ----

func TestEntryHasNoCredentialFields(t *testing.T) {
	entryType := reflect.TypeOf(Entry{})

	forbidden := []string{
		"api",
		"hash",
		"phone",
		"password",
		"authcode",
		"session",
		"encryptionkey",
		"databasekey",
	}

	for index := 0; index < entryType.NumField(); index++ {
		field := entryType.Field(index)

		normalized := strings.ToLower(
			strings.ReplaceAll(field.Name, "_", ""),
		)

		for _, token := range forbidden {
			if strings.Contains(normalized, token) {
				t.Fatalf(
					"Entry field %q contains forbidden credential token %q",
					field.Name, token,
				)
			}
		}
	}
}

package outbox

import (
	"context"
	"errors"
	"testing"
	"time"
)

// storeFactory returns a fresh empty Store for a test.
type storeFactory func(t *testing.T) Store

// runStoreContract executes the backend-agnostic contract suite.
//
// A future durable backend in PR-08C only needs to call this helper
// with its own factory; the assertions below are the contract.
//
// The suite never constructs non-queued entries directly: every
// intermediate state is produced through the public Store API, so a
// durable backend with the same state machine will pass without
// special-casing.
func runStoreContract(t *testing.T, factory storeFactory) {
	t.Helper()

	t.Run("EnqueueGetRoundTrip", func(t *testing.T) {
		store := factory(t)
		entry := queuedEntry("op-1", 42, "hello")

		if err := store.Enqueue(context.Background(), entry); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}

		got, err := store.Get(context.Background(), entry.ID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.ID != entry.ID || got.Text != entry.Text {
			t.Fatalf("round-trip mismatch: %+v", got)
		}
		if got.State != StateQueued {
			t.Fatalf("state = %s, want queued", got.State)
		}
	})

	t.Run("EnqueueRejectsNonQueued", func(t *testing.T) {
		store := factory(t)
		entry := queuedEntry("op-1", 42, "hello")
		entry.State = StateDispatching
		entry.LeaseOwner = "d1"
		entry.LeaseUntil = time.Unix(1700009999, 0).UTC()

		if err := store.Enqueue(context.Background(), entry); !errors.Is(err, ErrInvalidEntry) {
			t.Fatalf("err = %v, want ErrInvalidEntry", err)
		}
	})

	t.Run("EnqueueRejectsDuplicateID", func(t *testing.T) {
		store := factory(t)
		entry := queuedEntry("op-1", 42, "hello")

		if err := store.Enqueue(context.Background(), entry); err != nil {
			t.Fatalf("first Enqueue: %v", err)
		}
		if err := store.Enqueue(context.Background(), entry); !errors.Is(err, ErrDuplicateID) {
			t.Fatalf("err = %v, want ErrDuplicateID", err)
		}
	})

	t.Run("GetNotFound", func(t *testing.T) {
		store := factory(t)
		if _, err := store.Get(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("ListReadyOrdersByCreatedAt", func(t *testing.T) {
		store := factory(t)
		t0 := time.Unix(1700000000, 0).UTC()

		first := queuedEntryAt("op-1", 42, "first", t0)
		second := queuedEntryAt("op-2", 43, "second", t0.Add(time.Second))

		if err := store.Enqueue(context.Background(), second); err != nil {
			t.Fatal(err)
		}
		if err := store.Enqueue(context.Background(), first); err != nil {
			t.Fatal(err)
		}

		ready, err := store.ListReady(context.Background(), t0.Add(time.Hour), 10)
		if err != nil {
			t.Fatalf("ListReady: %v", err)
		}
		if len(ready) != 2 {
			t.Fatalf("len = %d, want 2", len(ready))
		}
		if ready[0].ID != "op-1" || ready[1].ID != "op-2" {
			t.Fatalf("order = %s, %s", ready[0].ID, ready[1].ID)
		}
	})

	t.Run("ListReadyHonoursLimit", func(t *testing.T) {
		store := factory(t)
		t0 := time.Unix(1700000000, 0).UTC()

		for i := 0; i < 5; i++ {
			if err := store.Enqueue(
				context.Background(),
				queuedEntryAt(ID(rune('a'+i)), int64(42+i), "x",
					t0.Add(time.Duration(i)*time.Second)),
			); err != nil {
				t.Fatal(err)
			}
		}

		ready, err := store.ListReady(context.Background(), t0.Add(time.Hour), 2)
		if err != nil {
			t.Fatalf("ListReady: %v", err)
		}
		if len(ready) != 2 {
			t.Fatalf("len = %d, want 2", len(ready))
		}
	})

	t.Run("ListReadyKeepsPerChatOrder", func(t *testing.T) {
		store := factory(t)
		ctx := context.Background()
		t0 := time.Unix(1700000000, 0).UTC()

		head := queuedEntryAt("op-head", 42, "first", t0)
		next := queuedEntryAt("op-next", 42, "second", t0.Add(time.Second))
		otherChat := queuedEntryAt("op-other-chat", 43, "x", t0.Add(2*time.Second))
		otherAccount := queuedEntryAt("op-other-account", 42, "x", t0.Add(3*time.Second))
		otherAccount.AccountKey = "secondary"
		for _, entry := range []Entry{head, next, otherChat, otherAccount} {
			if err := store.Enqueue(ctx, entry); err != nil {
				t.Fatal(err)
			}
		}

		readyIDs := func(now time.Time) []ID {
			t.Helper()
			ready, err := store.ListReady(ctx, now, 10)
			if err != nil {
				t.Fatalf("ListReady: %v", err)
			}
			ids := make([]ID, 0, len(ready))
			for _, entry := range ready {
				ids = append(ids, entry.ID)
			}
			return ids
		}
		assertReady := func(now time.Time, want ...ID) {
			t.Helper()
			got := readyIDs(now)
			if len(got) != len(want) {
				t.Fatalf("ready = %v, want %v", got, want)
			}
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("ready = %v, want %v", got, want)
				}
			}
		}

		now := t0.Add(time.Minute)

		// A queued head blocks later entries in its chat only.
		assertReady(now, "op-head", "op-other-chat", "op-other-account")

		// An in-flight head still blocks.
		claimed, err := store.Claim(ctx, head.ID, 0, "d1", now.Add(time.Minute), now)
		if err != nil {
			t.Fatal(err)
		}
		assertReady(now, "op-other-chat", "op-other-account")

		// A head waiting for its retry blocks until it is terminal, so
		// the second message can never overtake the first.
		retrying, err := store.MarkRetryable(
			ctx, head.ID, claimed.Version, now.Add(time.Hour), 0, "retry", now,
		)
		if err != nil {
			t.Fatal(err)
		}
		assertReady(now, "op-other-chat", "op-other-account")

		reclaimed, err := store.Claim(
			ctx, head.ID, retrying.Version, "d1",
			now.Add(2*time.Hour), now.Add(time.Hour),
		)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.MarkPermanentFailure(
			ctx, head.ID, reclaimed.Version, 400, "rejected", now.Add(time.Hour),
		); err != nil {
			t.Fatal(err)
		}

		// A terminal head no longer blocks its chat.
		assertReady(now.Add(time.Hour), "op-next", "op-other-chat", "op-other-account")
	})

	t.Run("ListReadySkipsNonReady", func(t *testing.T) {
		store := factory(t)
		t0 := time.Unix(1700000000, 0).UTC()

		queued := queuedEntryAt("op-queued", 1, "x", t0)
		if err := store.Enqueue(context.Background(), queued); err != nil {
			t.Fatal(err)
		}

		// Build a retryable entry via public Store transitions.
		seed := queuedEntryAt("op-retry", 2, "y", t0)
		if err := store.Enqueue(context.Background(), seed); err != nil {
			t.Fatal(err)
		}
		claimed, err := store.Claim(
			context.Background(), seed.ID, seed.Version,
			"dispatcher-1", t0.Add(time.Minute), t0,
		)
		if err != nil {
			t.Fatalf("Claim: %v", err)
		}
		retryable, err := store.MarkRetryable(
			context.Background(), claimed.ID, claimed.Version,
			t0.Add(time.Minute), 500, "temporary failure", t0,
		)
		if err != nil {
			t.Fatalf("MarkRetryable: %v", err)
		}
		if retryable.State != StateFailedRetryable {
			t.Fatalf("seed state = %s, want failed_retryable", retryable.State)
		}

		ready, err := store.ListReady(context.Background(), t0, 10)
		if err != nil {
			t.Fatalf("ListReady: %v", err)
		}
		if len(ready) != 1 || ready[0].ID != "op-queued" {
			t.Fatalf("ready at t0 = %+v", ready)
		}

		ready, err = store.ListReady(context.Background(), t0.Add(time.Minute), 10)
		if err != nil {
			t.Fatalf("ListReady: %v", err)
		}
		if len(ready) != 2 {
			t.Fatalf("len at t0+1m = %d, want 2", len(ready))
		}
	})

	t.Run("ClaimIncrementsVersionAndAttempt", func(t *testing.T) {
		store := factory(t)
		entry := queuedEntry("op-1", 42, "hello")
		if err := store.Enqueue(context.Background(), entry); err != nil {
			t.Fatal(err)
		}

		t1 := time.Unix(1700000100, 0).UTC()
		lease := t1.Add(time.Minute)

		claimed, err := store.Claim(
			context.Background(), entry.ID, entry.Version,
			"dispatcher-1", lease, t1,
		)
		if err != nil {
			t.Fatalf("Claim: %v", err)
		}
		if claimed.State != StateDispatching {
			t.Fatalf("state = %s", claimed.State)
		}
		if claimed.AttemptCount != 1 {
			t.Fatalf("attempt = %d, want 1", claimed.AttemptCount)
		}
		if claimed.LeaseOwner != "dispatcher-1" {
			t.Fatalf("owner = %q", claimed.LeaseOwner)
		}
		if !claimed.LeaseUntil.Equal(lease) {
			t.Fatalf("lease until = %v, want %v", claimed.LeaseUntil, lease)
		}
	})

	t.Run("ClaimRejectsVersionConflict", func(t *testing.T) {
		store := factory(t)
		entry := queuedEntry("op-1", 42, "hello")
		if err := store.Enqueue(context.Background(), entry); err != nil {
			t.Fatal(err)
		}

		t1 := time.Unix(1700000100, 0).UTC()
		lease := t1.Add(time.Minute)

		if _, err := store.Claim(
			context.Background(), entry.ID, entry.Version+99,
			"d1", lease, t1,
		); !errors.Is(err, ErrVersionConflict) {
			t.Fatalf("err = %v, want ErrVersionConflict", err)
		}
	})

	t.Run("ClaimRejectsActiveForeignLease", func(t *testing.T) {
		store := factory(t)
		entry := queuedEntry("op-1", 42, "hello")
		if err := store.Enqueue(context.Background(), entry); err != nil {
			t.Fatal(err)
		}

		t1 := time.Unix(1700000100, 0).UTC()
		lease := t1.Add(time.Minute)

		claimed, err := store.Claim(
			context.Background(), entry.ID, entry.Version,
			"d1", lease, t1,
		)
		if err != nil {
			t.Fatalf("first Claim: %v", err)
		}

		if _, err := store.Claim(
			context.Background(), claimed.ID, claimed.Version,
			"d2", t1.Add(2*time.Minute), t1,
		); !errors.Is(err, ErrLeaseHeld) {
			t.Fatalf("err = %v, want ErrLeaseHeld", err)
		}
	})

	t.Run("ClaimReadyRetryable", func(t *testing.T) {
		store := factory(t)
		entry := queuedEntry("op-1", 42, "hello")
		if err := store.Enqueue(context.Background(), entry); err != nil {
			t.Fatal(err)
		}

		t1 := time.Unix(1700000100, 0).UTC()

		claimed, err := store.Claim(
			context.Background(), entry.ID, entry.Version,
			"d1", t1.Add(time.Minute), t1,
		)
		if err != nil {
			t.Fatalf("first Claim: %v", err)
		}

		retryable, err := store.MarkRetryable(
			context.Background(), claimed.ID, claimed.Version,
			t1.Add(time.Second), 500, "temporary", t1,
		)
		if err != nil {
			t.Fatalf("MarkRetryable: %v", err)
		}

		claimedAgain, err := store.Claim(
			context.Background(), retryable.ID, retryable.Version,
			"d2", t1.Add(2*time.Minute), t1.Add(time.Second),
		)
		if err != nil {
			t.Fatalf("Claim retryable: %v", err)
		}
		if claimedAgain.State != StateDispatching {
			t.Fatalf("state = %s, want dispatching", claimedAgain.State)
		}
		if claimedAgain.AttemptCount != 2 {
			t.Fatalf("attempt count = %d, want 2", claimedAgain.AttemptCount)
		}
	})

	t.Run("MarkAcceptedPersistsMessageID", func(t *testing.T) {
		store := factory(t)
		entry := queuedEntry("op-1", 42, "hello")
		if err := store.Enqueue(context.Background(), entry); err != nil {
			t.Fatal(err)
		}

		t1 := time.Unix(1700000100, 0).UTC()
		claimed, err := store.Claim(
			context.Background(), entry.ID, entry.Version,
			"d1", t1.Add(time.Minute), t1,
		)
		if err != nil {
			t.Fatalf("Claim: %v", err)
		}

		t2 := time.Unix(1700000200, 0).UTC()
		accepted, err := store.MarkAccepted(
			context.Background(), claimed.ID, claimed.Version,
			9001, t2,
		)
		if err != nil {
			t.Fatalf("MarkAccepted: %v", err)
		}
		if accepted.State != StateAccepted {
			t.Fatalf("state = %s", accepted.State)
		}
		if accepted.TelegramMessageID != 9001 {
			t.Fatalf("message id = %d", accepted.TelegramMessageID)
		}
		if !accepted.AcceptedAt.Equal(t2) {
			t.Fatalf("accepted at = %v", accepted.AcceptedAt)
		}

		got, err := store.Get(context.Background(), claimed.ID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.State != StateAccepted || got.TelegramMessageID != 9001 {
			t.Fatalf("persisted entry = %+v", got)
		}
	})

	t.Run("MarkRetryableSetsNextAttempt", func(t *testing.T) {
		store := factory(t)
		entry := queuedEntry("op-1", 42, "hello")
		if err := store.Enqueue(context.Background(), entry); err != nil {
			t.Fatal(err)
		}

		t1 := time.Unix(1700000100, 0).UTC()
		claimed, err := store.Claim(
			context.Background(), entry.ID, entry.Version,
			"d1", t1.Add(time.Minute), t1,
		)
		if err != nil {
			t.Fatalf("Claim: %v", err)
		}

		t2 := time.Unix(1700000200, 0).UTC()
		next := t2.Add(5 * time.Second)
		retry, err := store.MarkRetryable(
			context.Background(), claimed.ID, claimed.Version,
			next, 500, "temporary failure", t2,
		)
		if err != nil {
			t.Fatalf("MarkRetryable: %v", err)
		}
		if retry.State != StateFailedRetryable {
			t.Fatalf("state = %s", retry.State)
		}
		if !retry.NextAttempt.Equal(next) {
			t.Fatalf("next attempt = %v, want %v", retry.NextAttempt, next)
		}
		if retry.LastErrorCode != 500 {
			t.Fatalf("code = %d", retry.LastErrorCode)
		}
	})

	t.Run("MarkPermanentFailureTerminal", func(t *testing.T) {
		store := factory(t)
		entry := queuedEntry("op-1", 42, "hello")
		if err := store.Enqueue(context.Background(), entry); err != nil {
			t.Fatal(err)
		}

		t1 := time.Unix(1700000100, 0).UTC()
		claimed, err := store.Claim(
			context.Background(), entry.ID, entry.Version,
			"d1", t1.Add(time.Minute), t1,
		)
		if err != nil {
			t.Fatalf("Claim: %v", err)
		}

		perm, err := store.MarkPermanentFailure(
			context.Background(), claimed.ID, claimed.Version,
			400, "chat not found", t1.Add(time.Second),
		)
		if err != nil {
			t.Fatalf("MarkPermanentFailure: %v", err)
		}
		if perm.State != StateFailedPermanent {
			t.Fatalf("state = %s", perm.State)
		}
	})

	t.Run("MarkUncertainTerminalAndNotReady", func(t *testing.T) {
		store := factory(t)
		entry := queuedEntry("op-1", 42, "hello")
		if err := store.Enqueue(context.Background(), entry); err != nil {
			t.Fatal(err)
		}

		t1 := time.Unix(1700000100, 0).UTC()
		claimed, err := store.Claim(
			context.Background(), entry.ID, entry.Version,
			"d1", t1.Add(time.Minute), t1,
		)
		if err != nil {
			t.Fatalf("Claim: %v", err)
		}

		uncertain, err := store.MarkUncertain(
			context.Background(), claimed.ID, claimed.Version,
			"process exit during dispatch", t1.Add(time.Second),
		)
		if err != nil {
			t.Fatalf("MarkUncertain: %v", err)
		}
		if uncertain.State != StateUncertain {
			t.Fatalf("state = %s", uncertain.State)
		}

		ready, err := store.ListReady(context.Background(), t1.Add(time.Hour), 10)
		if err != nil {
			t.Fatalf("ListReady: %v", err)
		}
		if len(ready) != 0 {
			t.Fatalf("uncertain entry leaked into ready list: %+v", ready)
		}
	})

	t.Run("CancelQueued", func(t *testing.T) {
		store := factory(t)
		entry := queuedEntry("op-1", 42, "hello")
		if err := store.Enqueue(context.Background(), entry); err != nil {
			t.Fatal(err)
		}

		canceled, err := store.Cancel(
			context.Background(), entry.ID, entry.Version,
			time.Unix(1700000200, 0).UTC(),
		)
		if err != nil {
			t.Fatalf("Cancel: %v", err)
		}
		if canceled.State != StateCanceled {
			t.Fatalf("state = %s", canceled.State)
		}
	})

	t.Run("CancelDispatchingRejected", func(t *testing.T) {
		store := factory(t)
		entry := queuedEntry("op-1", 42, "hello")
		if err := store.Enqueue(context.Background(), entry); err != nil {
			t.Fatal(err)
		}

		t1 := time.Unix(1700000100, 0).UTC()
		claimed, err := store.Claim(
			context.Background(), entry.ID, entry.Version,
			"d1", t1.Add(time.Minute), t1,
		)
		if err != nil {
			t.Fatalf("Claim: %v", err)
		}

		if _, err := store.Cancel(
			context.Background(), claimed.ID, claimed.Version,
			t1.Add(time.Second),
		); !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("err = %v, want ErrInvalidTransition", err)
		}
	})

	t.Run("CancelAcceptedRejected", func(t *testing.T) {
		store := factory(t)
		entry := queuedEntry("op-1", 42, "hello")
		if err := store.Enqueue(context.Background(), entry); err != nil {
			t.Fatal(err)
		}

		t1 := time.Unix(1700000100, 0).UTC()
		claimed, err := store.Claim(
			context.Background(), entry.ID, entry.Version,
			"d1", t1.Add(time.Minute), t1,
		)
		if err != nil {
			t.Fatalf("Claim: %v", err)
		}

		accepted, err := store.MarkAccepted(
			context.Background(), claimed.ID, claimed.Version,
			9001, t1.Add(time.Second),
		)
		if err != nil {
			t.Fatalf("MarkAccepted: %v", err)
		}

		if _, err := store.Cancel(
			context.Background(), accepted.ID, accepted.Version,
			t1.Add(2*time.Second),
		); !errors.Is(err, ErrCancelAfterAccepted) {
			t.Fatalf("err = %v, want ErrCancelAfterAccepted", err)
		}
	})

	t.Run("CancelRetryable", func(t *testing.T) {
		store := factory(t)
		entry := queuedEntry("op-1", 42, "hello")
		if err := store.Enqueue(context.Background(), entry); err != nil {
			t.Fatal(err)
		}

		t1 := time.Unix(1700000100, 0).UTC()
		claimed, err := store.Claim(
			context.Background(), entry.ID, entry.Version,
			"d1", t1.Add(time.Minute), t1,
		)
		if err != nil {
			t.Fatalf("Claim: %v", err)
		}
		retry, err := store.MarkRetryable(
			context.Background(), claimed.ID, claimed.Version,
			t1.Add(time.Second), 500, "temporary", t1.Add(time.Second),
		)
		if err != nil {
			t.Fatalf("MarkRetryable: %v", err)
		}
		canceled, err := store.Cancel(
			context.Background(), retry.ID, retry.Version,
			t1.Add(2*time.Second),
		)
		if err != nil {
			t.Fatalf("Cancel: %v", err)
		}
		if canceled.State != StateCanceled {
			t.Fatalf("state = %s", canceled.State)
		}
	})

	t.Run("CancelUncertain", func(t *testing.T) {
		store := factory(t)
		entry := queuedEntry("op-1", 42, "hello")
		if err := store.Enqueue(context.Background(), entry); err != nil {
			t.Fatal(err)
		}

		t1 := time.Unix(1700000100, 0).UTC()
		claimed, err := store.Claim(
			context.Background(), entry.ID, entry.Version,
			"d1", t1.Add(time.Minute), t1,
		)
		if err != nil {
			t.Fatalf("Claim: %v", err)
		}

		uncertain, err := store.MarkUncertain(
			context.Background(), claimed.ID, claimed.Version,
			"result unknown", t1.Add(time.Second),
		)
		if err != nil {
			t.Fatalf("MarkUncertain: %v", err)
		}

		canceled, err := store.Cancel(
			context.Background(), uncertain.ID, uncertain.Version,
			t1.Add(2*time.Second),
		)
		if err != nil {
			t.Fatalf("Cancel uncertain: %v", err)
		}
		if canceled.State != StateCanceled {
			t.Fatalf("state = %s, want canceled", canceled.State)
		}
	})

	t.Run("RecoverInterruptedConvertsExpiredLease", func(t *testing.T) {
		store := factory(t)
		entry := queuedEntry("op-1", 42, "hello")
		if err := store.Enqueue(context.Background(), entry); err != nil {
			t.Fatal(err)
		}

		t1 := time.Unix(1700000100, 0).UTC()
		claimed, err := store.Claim(
			context.Background(), entry.ID, entry.Version,
			"d1", t1.Add(time.Minute), t1,
		)
		if err != nil {
			t.Fatalf("Claim: %v", err)
		}

		recovered, err := store.RecoverInterrupted(
			context.Background(),
			t1.Add(10*time.Minute),
		)
		if err != nil {
			t.Fatalf("RecoverInterrupted: %v", err)
		}
		if recovered != 1 {
			t.Fatalf("recovered = %d, want 1", recovered)
		}

		got, err := store.Get(context.Background(), claimed.ID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.State != StateUncertain {
			t.Fatalf("state = %s, want uncertain", got.State)
		}

		ready, err := store.ListReady(context.Background(), t1.Add(time.Hour), 10)
		if err != nil {
			t.Fatalf("ListReady: %v", err)
		}
		if len(ready) != 0 {
			t.Fatalf("recovered entry returned to ready set: %+v", ready)
		}
	})

	t.Run("RecoverInterruptedLeavesActiveLease", func(t *testing.T) {
		store := factory(t)
		entry := queuedEntry("op-1", 42, "hello")
		if err := store.Enqueue(context.Background(), entry); err != nil {
			t.Fatal(err)
		}

		t1 := time.Unix(1700000100, 0).UTC()
		claimed, err := store.Claim(
			context.Background(), entry.ID, entry.Version,
			"d1", t1.Add(time.Minute), t1,
		)
		if err != nil {
			t.Fatalf("Claim: %v", err)
		}

		recovered, err := store.RecoverInterrupted(
			context.Background(),
			t1.Add(time.Second),
		)
		if err != nil {
			t.Fatalf("RecoverInterrupted: %v", err)
		}
		if recovered != 0 {
			t.Fatalf("recovered = %d, want 0", recovered)
		}

		got, err := store.Get(context.Background(), claimed.ID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.State != StateDispatching {
			t.Fatalf("state = %s, want dispatching", got.State)
		}
	})

	t.Run("ConcurrentClaimOnlyOneWins", func(t *testing.T) {
		store := factory(t)
		entry := queuedEntry("op-1", 42, "hello")
		if err := store.Enqueue(context.Background(), entry); err != nil {
			t.Fatal(err)
		}

		t1 := time.Unix(1700000100, 0).UTC()
		lease := t1.Add(time.Minute)

		type outcome struct {
			owner string
			err   error
		}
		results := make(chan outcome, 2)

		claim := func(owner string) {
			_, err := store.Claim(
				context.Background(), entry.ID, entry.Version,
				owner, lease, t1,
			)
			results <- outcome{owner: owner, err: err}
		}

		go claim("d1")
		go claim("d2")

		var succeeded int
		for i := 0; i < 2; i++ {
			r := <-results
			switch {
			case r.err == nil:
				succeeded++
			case errors.Is(r.err, ErrLeaseHeld),
				errors.Is(r.err, ErrVersionConflict):
				// Expected loser.
			default:
				t.Fatalf("owner %s: unexpected error %v", r.owner, r.err)
			}
		}
		if succeeded != 1 {
			t.Fatalf("succeeded = %d, want 1", succeeded)
		}
	})

	t.Run("VersionConflictOnStaleMutation", func(t *testing.T) {
		store := factory(t)
		entry := queuedEntry("op-1", 42, "hello")
		if err := store.Enqueue(context.Background(), entry); err != nil {
			t.Fatal(err)
		}

		t1 := time.Unix(1700000100, 0).UTC()
		claimed, err := store.Claim(
			context.Background(), entry.ID, entry.Version,
			"d1", t1.Add(time.Minute), t1,
		)
		if err != nil {
			t.Fatalf("Claim: %v", err)
		}

		if _, err := store.MarkAccepted(
			context.Background(), claimed.ID, claimed.Version+99,
			9001, t1.Add(time.Second),
		); !errors.Is(err, ErrVersionConflict) {
			t.Fatalf("err = %v, want ErrVersionConflict", err)
		}
	})
}

// queuedEntry builds a valid queued entry at a fixed timestamp.
func queuedEntry(id ID, chatID int64, text string) Entry {
	return queuedEntryAt(id, chatID, text, time.Unix(1700000000, 0).UTC())
}

func queuedEntryAt(id ID, chatID int64, text string, at time.Time) Entry {
	return Entry{
		ID:         id,
		AccountKey: "primary",
		ChatID:     chatID,
		Text:       text,
		State:      StateQueued,
		CreatedAt:  at,
		UpdatedAt:  at,
	}
}

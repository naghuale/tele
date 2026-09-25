package tui

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func h6c3Status(
	entryID string,
	state MessageDeliveryState,
) MessageStatus {
	return MessageStatus{
		EntryID:    entryID,
		AccountKey: "account-1",
		ChatID:     42,
		State:      state,
		Attempt:    1,
		UpdatedAt:  time.Unix(1700000000, 0).UTC(),
	}
}

func h6c3Model(
	statuses []MessageStatus,
) Model {
	model := newPollingTestModel(&h6c2bStatusSource{})
	model.deliveryStatuses = statuses
	return model
}

func h6c3Render(
	model Model,
) string {
	return model.viewMessageStatusesAt(time.Date(2026, 3, 4, 12, 0, 0, 0, time.Local))
}

func TestPresentMessageDeliveryStateProjectsAllStates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		state       MessageDeliveryState
		label       string
		marker      string
		wantWarning string
	}{
		{
			name:   "queued",
			state:  MessageDeliveryQueued,
			label:  "В очереди",
			marker: "○",
		},
		{
			name:   "sending",
			state:  MessageDeliverySending,
			label:  "Отправляется",
			marker: "●",
		},
		{
			name:   "retrying",
			state:  MessageDeliveryRetrying,
			label:  "Повторная попытка",
			marker: "↻",
		},
		{
			name:   "failed",
			state:  MessageDeliveryFailed,
			label:  "Не отправлено",
			marker: "×",
		},
		{
			name:        "uncertain",
			state:       MessageDeliveryUncertain,
			label:       "Результат неизвестен",
			marker:      "?",
			wantWarning: "Повторная отправка может создать дубликат.",
		},
		{
			name:   "sent",
			state:  MessageDeliverySent,
			label:  "Отправлено",
			marker: "✓",
		},
		{
			name:   "canceled",
			state:  MessageDeliveryCanceled,
			label:  "Отменено",
			marker: "−",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := presentMessageDeliveryState(test.state)
			if err != nil {
				t.Fatalf("presentMessageDeliveryState() error = %v", err)
			}
			if got.Label != test.label {
				t.Fatalf("label = %q, want %q", got.Label, test.label)
			}
			if got.Marker != test.marker {
				t.Fatalf("marker = %q, want %q", got.Marker, test.marker)
			}
			if got.Warning != test.wantWarning {
				t.Fatalf("warning = %q, want %q", got.Warning, test.wantWarning)
			}
		})
	}
}

func TestPresentMessageDeliveryStateRejectsUnknownState(t *testing.T) {
	t.Parallel()

	got, err := presentMessageDeliveryState(
		MessageDeliveryState("unknown"),
	)
	if err == nil {
		t.Fatal("presentMessageDeliveryState() error = nil, want non-nil")
	}
	if got != (messageStatusPresentation{}) {
		t.Fatalf(
			"presentMessageDeliveryState() = %#v, want zero presentation",
			got,
		)
	}
}

func TestMessageStatusViewDisabledWithoutSource(t *testing.T) {
	t.Parallel()

	model := h6c3Model([]MessageStatus{
		h6c3Status("entry-1", MessageDeliveryQueued),
	})
	model.messageStatuses = nil

	if view := h6c3Render(model); view != "" {
		t.Fatalf("viewMessageStatuses() = %q, want empty", view)
	}
}

func TestMessageStatusViewHiddenOutsideChat(t *testing.T) {
	t.Parallel()

	model := h6c3Model([]MessageStatus{
		h6c3Status("entry-1", MessageDeliveryQueued),
	})
	model.screen = ScreenChats
	model.width = 80
	model.height = 24
	model.chats = []Chat{{ID: 42, Title: "Peer"}}
	model.chatsState = loadStateLoaded

	view := model.View()
	if strings.Contains(view, messageStatusHeaderText) {
		t.Fatalf("view = %q, delivery block must stay inside the chat", view)
	}
}

func TestMessageStatusViewHiddenForStableEmptySnapshot(t *testing.T) {
	t.Parallel()

	model := h6c3Model(nil)
	model.messageStatusLoading = false
	model.messageStatusErr = nil

	if view := h6c3Render(model); view != "" {
		t.Fatalf("viewMessageStatuses() = %q, want hidden", view)
	}
}

func TestMessageStatusViewRendersInitialLoading(t *testing.T) {
	t.Parallel()

	model := h6c3Model(nil)
	model.messageStatusLoading = true

	view := h6c3Render(model)
	if !strings.Contains(view, messageStatusHeaderText) {
		t.Fatalf("view = %q, want header", view)
	}
	if !strings.Contains(view, messageStatusLoadingSuffix) {
		t.Fatalf("view = %q, want loading suffix", view)
	}
	if strings.Count(view, "\n") != 0 {
		t.Fatalf("view = %q, want a single line", view)
	}
}

func TestMessageStatusViewPreservesSnapshotOrder(t *testing.T) {
	t.Parallel()

	view := h6c3Render(h6c3Model([]MessageStatus{
		h6c3Status("entry-1", MessageDeliverySent),
		h6c3Status("entry-2", MessageDeliveryQueued),
		h6c3Status("entry-3", MessageDeliveryRetrying),
	}))

	lines := strings.Split(view, "\n")
	want := []string{"Отправлено", "В очереди", "Повторная попытка"}
	if len(lines) != 1+len(want) {
		t.Fatalf("lines = %#v, want %d", lines, 1+len(want))
	}
	if !strings.HasPrefix(lines[0], messageStatusHeaderText) {
		t.Fatalf("first line = %q, want header", lines[0])
	}
	for index, label := range want {
		if !strings.Contains(lines[index+1], label) {
			t.Fatalf("line %d = %q, want %q", index+1, lines[index+1], label)
		}
	}
}

func TestMessageStatusViewKeepsSnapshotWhileRefreshing(t *testing.T) {
	t.Parallel()

	model := h6c3Model([]MessageStatus{
		h6c3Status("entry-1", MessageDeliverySending),
	})
	model.messageStatusLoading = true

	view := h6c3Render(model)
	if !strings.Contains(view, "Отправляется") {
		t.Fatalf("view = %q, snapshot must stay visible", view)
	}
	if !strings.Contains(view, messageStatusLoadingSuffix) {
		t.Fatalf("view = %q, want loading marker in header", view)
	}
}

func TestMessageStatusViewHandlesNilSnapshot(t *testing.T) {
	t.Parallel()

	model := h6c3Model(nil)
	model.deliveryStatuses = nil

	if view := h6c3Render(model); view != "" {
		t.Fatalf("viewMessageStatuses() = %q, want hidden", view)
	}
}

func TestMessageStatusViewHandlesEmptySnapshot(t *testing.T) {
	t.Parallel()

	model := h6c3Model([]MessageStatus{})

	if view := h6c3Render(model); view != "" {
		t.Fatalf("viewMessageStatuses() = %q, want hidden", view)
	}
}

func TestMessageStatusViewRendersQueued(t *testing.T) {
	t.Parallel()

	view := h6c3Render(h6c3Model([]MessageStatus{
		h6c3Status("entry-1", MessageDeliveryQueued),
	}))

	if !strings.Contains(view, "○ В очереди") {
		t.Fatalf("view = %q, want queued line", view)
	}
	if strings.Contains(view, "✓") {
		t.Fatalf("view = %q, queued must not use the sent marker", view)
	}
}

func TestMessageStatusViewRendersSending(t *testing.T) {
	t.Parallel()

	view := h6c3Render(h6c3Model([]MessageStatus{
		h6c3Status("entry-1", MessageDeliverySending),
	}))

	if !strings.Contains(view, "● Отправляется") {
		t.Fatalf("view = %q, want sending line", view)
	}
	if strings.Contains(strings.ToLower(view), "повторить") {
		t.Fatalf("view = %q, sending must not offer a retry", view)
	}
}

func TestMessageStatusViewRendersRetrying(t *testing.T) {
	t.Parallel()

	view := h6c3Render(h6c3Model([]MessageStatus{
		h6c3Status("entry-1", MessageDeliveryRetrying),
	}))

	if !strings.Contains(view, "↻ Повторная попытка") {
		t.Fatalf("view = %q, want retrying line", view)
	}
}

func TestMessageStatusViewRendersFailed(t *testing.T) {
	t.Parallel()

	view := h6c3Render(h6c3Model([]MessageStatus{
		h6c3Status("entry-1", MessageDeliveryFailed),
	}))

	if !strings.Contains(view, "× Не отправлено") {
		t.Fatalf("view = %q, want failed line", view)
	}
	if strings.Contains(strings.ToLower(view), "повторить") {
		t.Fatalf("view = %q, failed must not offer a retry", view)
	}
}

func TestMessageStatusViewRendersUncertain(t *testing.T) {
	t.Parallel()

	view := h6c3Render(h6c3Model([]MessageStatus{
		h6c3Status("entry-1", MessageDeliveryUncertain),
	}))

	if !strings.Contains(view, "? Результат неизвестен") {
		t.Fatalf("view = %q, want uncertain line", view)
	}
	if strings.Contains(view, "× Не отправлено") {
		t.Fatalf("view = %q, uncertain must not render as failed", view)
	}
}

func TestMessageStatusViewRendersSent(t *testing.T) {
	t.Parallel()

	view := h6c3Render(h6c3Model([]MessageStatus{
		h6c3Status("entry-1", MessageDeliverySent),
	}))

	if !strings.Contains(view, "✓ Отправлено") {
		t.Fatalf("view = %q, want sent line", view)
	}
}

func TestMessageStatusViewRendersCanceled(t *testing.T) {
	t.Parallel()

	view := h6c3Render(h6c3Model([]MessageStatus{
		h6c3Status("entry-1", MessageDeliveryCanceled),
	}))

	if !strings.Contains(view, "− Отменено") {
		t.Fatalf("view = %q, want canceled line", view)
	}
	if strings.Contains(view, "×") {
		t.Fatalf("view = %q, canceled must not look like a failure", view)
	}
}

func TestMessageStatusViewWarnsAboutDuplicateRiskForUncertain(t *testing.T) {
	t.Parallel()

	view := h6c3Render(h6c3Model([]MessageStatus{
		h6c3Status("entry-1", MessageDeliveryUncertain),
	}))

	lines := strings.Split(view, "\n")
	if len(lines) < 3 {
		t.Fatalf("lines = %#v, want a warning line", lines)
	}
	want := "  Повторная отправка может создать дубликат."
	if lines[2] != want {
		t.Fatalf("warning line = %q, want %q", lines[2], want)
	}
}

func TestMessageStatusViewDoesNotOfferRetryForUncertain(t *testing.T) {
	t.Parallel()

	view := h6c3Render(h6c3Model([]MessageStatus{
		h6c3Status("entry-1", MessageDeliveryUncertain),
	}))

	lowered := strings.ToLower(view)
	for _, forbidden := range []string{
		"повторить",
		"retry",
		"отправить снова",
		"send again",
		"requeue",
	} {
		if strings.Contains(lowered, forbidden) {
			t.Fatalf("view = %q, must not offer %q", view, forbidden)
		}
	}
}

func TestMessageStatusViewRendersPositiveAttempt(t *testing.T) {
	t.Parallel()

	status := h6c3Status("entry-1", MessageDeliveryRetrying)
	status.Attempt = 2

	view := h6c3Render(h6c3Model([]MessageStatus{status}))
	if !strings.Contains(view, "попытка 2") {
		t.Fatalf("view = %q, want attempt counter", view)
	}
}

func TestMessageStatusViewHidesZeroAttempt(t *testing.T) {
	t.Parallel()

	status := h6c3Status("entry-1", MessageDeliveryRetrying)
	status.Attempt = 0

	view := h6c3Render(h6c3Model([]MessageStatus{status}))
	lines := strings.Split(view, "\n")
	want := "  ↻ Повторная попытка"
	if lines[1] != want {
		t.Fatalf("retrying line = %q, want %q", lines[1], want)
	}
}

func TestMessageStatusViewRendersFutureNextAttempt(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 4, 12, 0, 0, 0, time.Local)
	status := h6c3Status("entry-1", MessageDeliveryRetrying)
	status.NextAttemptAt = now.Add(14 * time.Second)

	view := h6c3Model([]MessageStatus{status}).viewMessageStatusesAt(now)
	if !strings.Contains(view, "через 14с") {
		t.Fatalf("view = %q, want future next attempt", view)
	}
}

func TestMessageStatusViewHidesPastNextAttempt(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 4, 12, 0, 0, 0, time.Local)
	status := h6c3Status("entry-1", MessageDeliveryRetrying)
	status.NextAttemptAt = now.Add(-3 * time.Second)

	view := h6c3Model([]MessageStatus{status}).viewMessageStatusesAt(now)
	if strings.Contains(view, "через") {
		t.Fatalf("view = %q, past attempts must not be rendered", view)
	}
	if strings.Contains(view, "-3") {
		t.Fatalf("view = %q, negative duration leaked", view)
	}
}

func TestMessageStatusViewHidesNextAttemptForNonRetryingState(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 4, 12, 0, 0, 0, time.Local)
	status := h6c3Status("entry-1", MessageDeliverySending)
	status.NextAttemptAt = now.Add(time.Minute)

	view := h6c3Model([]MessageStatus{status}).viewMessageStatusesAt(now)
	if strings.Contains(view, "через") {
		t.Fatalf("view = %q, next attempt is retrying only", view)
	}
}

func TestMessageStatusViewUsesSafePollingError(t *testing.T) {
	t.Parallel()

	model := h6c3Model(nil)
	model.messageStatusErr = errors.New("status read failed")

	view := h6c3Render(model)
	if !strings.Contains(view, messageStatusRefreshErrorText) {
		t.Fatalf("view = %q, want safe error text", view)
	}
	if !strings.Contains(view, messageStatusNoticeMarker+" ") {
		t.Fatalf("view = %q, want notice marker", view)
	}
}

func TestMessageStatusViewDoesNotRenderRawPollingError(t *testing.T) {
	t.Parallel()

	const secret = "sqlite: /var/tele/outbox.db: provider internal detail"

	model := h6c3Model([]MessageStatus{
		h6c3Status("entry-1", MessageDeliveryQueued),
	})
	model.messageStatusErr = errors.New(secret)

	view := h6c3Render(model)
	for _, forbidden := range []string{
		secret,
		"outbox.db",
		"status read failed",
	} {
		if strings.Contains(view, forbidden) {
			t.Fatalf("view = %q, must not render %q", view, forbidden)
		}
	}
}

func TestMessageStatusViewKeepsSnapshotOnPollingError(t *testing.T) {
	t.Parallel()

	model := h6c3Model([]MessageStatus{
		h6c3Status("entry-1", MessageDeliveryQueued),
		h6c3Status("entry-2", MessageDeliverySent),
	})
	model.messageStatusErr = errors.New("status read failed")

	view := h6c3Render(model)
	if !strings.Contains(view, "В очереди") {
		t.Fatalf("view = %q, queued entry must stay visible", view)
	}
	if !strings.Contains(view, "Отправлено") {
		t.Fatalf("view = %q, sent entry must stay visible", view)
	}
	if !strings.Contains(view, messageStatusRefreshErrorText) {
		t.Fatalf("view = %q, want safe error text", view)
	}
}

func TestMessageStatusViewRendersUnknownStateSafely(t *testing.T) {
	t.Parallel()

	view := h6c3Render(h6c3Model([]MessageStatus{
		h6c3Status("entry-1", MessageDeliveryState("future")),
		h6c3Status("entry-2", MessageDeliverySent),
	}))

	if !strings.Contains(view, messageStatusUnknownText) {
		t.Fatalf("view = %q, want unknown state text", view)
	}
	if strings.Contains(view, "future") {
		t.Fatalf("view = %q, raw state must not be rendered", view)
	}
	if strings.Contains(view, "× Не отправлено") {
		t.Fatalf("view = %q, unknown state must not render as failed", view)
	}
	if !strings.Contains(view, "Отправлено") {
		t.Fatalf("view = %q, known entries must still render", view)
	}
}

func TestMessageStatusViewDoesNotCallStatusSource(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	source := &h6c2bStatusSource{
		listMessageStatuses: func(
			context.Context,
			string,
			int64,
		) ([]MessageStatus, error) {
			calls.Add(1)
			return nil, nil
		},
	}
	model := newPollingTestModel(source)
	model.deliveryStatuses = []MessageStatus{
		h6c3Status("entry-1", MessageDeliveryQueued),
	}
	model.screen = ScreenConversation
	model.width = 80
	model.height = 24
	model.chats = []Chat{{ID: 42, Title: "Peer"}}

	_ = model.View()
	_ = model.viewMessageStatuses()

	if calls.Load() != 0 {
		t.Fatalf("status source calls = %d, want 0", calls.Load())
	}
}

func TestMessageStatusViewDoesNotCallSubmitter(t *testing.T) {
	t.Parallel()

	submitter := &h5aComposerSubmitter{}
	model := h6c3Model([]MessageStatus{
		h6c3Status("entry-1", MessageDeliveryQueued),
	})
	model.submitter = submitter
	model.screen = ScreenConversation
	model.width = 80
	model.height = 24
	model.chats = []Chat{{ID: 42, Title: "Peer"}}

	view := model.View()
	if !strings.Contains(view, "○ В очереди") {
		t.Fatalf("view = %q, want delivery block above the composer", view)
	}
	if submitter.calls.Load() != 0 {
		t.Fatalf("submitter calls = %d, want 0", submitter.calls.Load())
	}
}

func TestMessageStatusViewDoesNotMutateSnapshot(t *testing.T) {
	t.Parallel()

	statuses := []MessageStatus{
		h6c3Status("entry-1", MessageDeliverySent),
		h6c3Status("entry-2", MessageDeliveryRetrying),
	}
	model := h6c3Model(statuses)
	model.messageStatusGeneration = 4
	model.messageStatusLoading = true
	model.messageStatusErr = errors.New("status read failed")

	first := h6c3Render(model)
	second := h6c3Render(model)

	if first != second {
		t.Fatalf("repeated rendering differs:\n%q\n%q", first, second)
	}
	if len(model.deliveryStatuses) != 2 {
		t.Fatalf(
			"deliveryStatuses = %#v, want unchanged snapshot",
			model.deliveryStatuses,
		)
	}
	if model.messageStatusGeneration != 4 {
		t.Fatalf(
			"messageStatusGeneration = %d, want 4",
			model.messageStatusGeneration,
		)
	}
	if !model.messageStatusLoading {
		t.Fatal("messageStatusLoading = false after rendering")
	}
	if model.messageStatusErr == nil {
		t.Fatal("messageStatusErr = nil after rendering")
	}
}

func TestMessageStatusViewDoesNotExposePayload(t *testing.T) {
	t.Parallel()

	const (
		entryID    = "entry-secret-1"
		accountKey = "account-secret-1"
	)

	status := h6c3Status(entryID, MessageDeliveryQueued)
	status.AccountKey = accountKey

	view := h6c3Render(h6c3Model([]MessageStatus{status}))
	for _, secret := range []string{entryID, accountKey, "Попытка", "Text"} {
		if strings.Contains(view, secret) {
			t.Fatalf("view = %q, must not expose %q", view, secret)
		}
	}
}

func TestMessageStatusViewBoundsBlockHeight(t *testing.T) {
	t.Parallel()

	statuses := make([]MessageStatus, 0, 20)
	for i := 0; i < 20; i++ {
		statuses = append(
			statuses,
			h6c3Status("entry", MessageDeliverySending),
		)
	}

	model := h6c3Model(statuses)
	model.height = 12

	view := h6c3Render(model)
	limit := messageStatusLineLimit(model.height)
	if got := len(strings.Split(view, "\n")); got > limit {
		t.Fatalf("lines = %d, want at most %d", got, limit)
	}
}

func TestMessageStatusViewPlacesBlockAboveComposer(t *testing.T) {
	t.Parallel()

	model := h6c3Model([]MessageStatus{
		h6c3Status("entry-1", MessageDeliveryQueued),
	})
	model.screen = ScreenConversation
	model.width = 80
	model.height = 24
	model.chats = []Chat{{ID: 42, Title: "Peer"}}
	model.focus = FocusComposer

	view := model.View()
	delivery := strings.Index(view, messageStatusHeaderText)
	composer := strings.Index(view, "> ")
	if delivery < 0 || composer < 0 {
		t.Fatalf("view = %q, want delivery block and composer", view)
	}
	if delivery > composer {
		t.Fatalf("view = %q, delivery block must be above the composer", view)
	}
}

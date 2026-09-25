package tui

import (
	"errors"
	"strings"
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

func TestPresentMessageDeliveryStateProjectsAllStates(t *testing.T) {
	t.Parallel()

	uncertainWarning := "Результат отправки неизвестен. " +
		"Повторная отправка может создать дубликат."

	tests := []struct {
		name    string
		state   MessageDeliveryState
		want    messageStatusPresentation
		warning string
	}{
		{
			name:  "queued",
			state: MessageDeliveryQueued,
			want:  messageStatusPresentation{Label: "В очереди"},
		},
		{
			name:  "sending",
			state: MessageDeliverySending,
			want:  messageStatusPresentation{Label: "Отправляется"},
		},
		{
			name:  "retrying",
			state: MessageDeliveryRetrying,
			want:  messageStatusPresentation{Label: "Повторная попытка"},
		},
		{
			name:  "failed",
			state: MessageDeliveryFailed,
			want:  messageStatusPresentation{Label: "Не отправлено"},
		},
		{
			name:  "uncertain",
			state: MessageDeliveryUncertain,
			want: messageStatusPresentation{
				Label:   "Результат неизвестен",
				Warning: uncertainWarning,
			},
			warning: uncertainWarning,
		},
		{
			name:  "sent",
			state: MessageDeliverySent,
			want:  messageStatusPresentation{Label: "Отправлено"},
		},
		{
			name:  "canceled",
			state: MessageDeliveryCanceled,
			want:  messageStatusPresentation{Label: "Отменено"},
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
			if got != test.want {
				t.Fatalf(
					"presentMessageDeliveryState() = %#v, want %#v",
					got,
					test.want,
				)
			}
			if got.Label == "" {
				t.Fatal("presentation label is empty")
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

	if view := model.viewMessageStatuses(); view != "" {
		t.Fatalf("viewMessageStatuses() = %q, want empty", view)
	}
}

func TestMessageStatusViewRendersQueued(t *testing.T) {
	t.Parallel()

	view := h6c3Model([]MessageStatus{
		h6c3Status("entry-1", MessageDeliveryQueued),
	}).viewMessageStatuses()

	if !strings.Contains(view, "В очереди") {
		t.Fatalf("view = %q, want queued label", view)
	}
	if strings.Contains(view, "Отправлено") {
		t.Fatalf("view = %q, queued must not render as sent", view)
	}
}

func TestMessageStatusViewRendersSending(t *testing.T) {
	t.Parallel()

	view := h6c3Model([]MessageStatus{
		h6c3Status("entry-1", MessageDeliverySending),
	}).viewMessageStatuses()

	if !strings.Contains(view, "Отправляется") {
		t.Fatalf("view = %q, want sending label", view)
	}
	if strings.Contains(strings.ToLower(view), "повторить") {
		t.Fatalf("view = %q, sending must not offer a retry", view)
	}
}

func TestMessageStatusViewRendersRetrying(t *testing.T) {
	t.Parallel()

	next := time.Date(2026, 3, 4, 5, 6, 7, 0, time.Local)
	status := h6c3Status("entry-1", MessageDeliveryRetrying)
	status.NextAttemptAt = next

	view := h6c3Model([]MessageStatus{status}).viewMessageStatuses()

	if !strings.Contains(view, "Повторная попытка") {
		t.Fatalf("view = %q, want retrying label", view)
	}
	want := next.Local().Format(messageStatusTimeLayout)
	if !strings.Contains(view, want) {
		t.Fatalf("view = %q, want next attempt %q", view, want)
	}
}

func TestMessageStatusViewHidesNextAttemptForNonRetryingState(t *testing.T) {
	t.Parallel()

	status := h6c3Status("entry-1", MessageDeliverySending)
	status.NextAttemptAt = time.Date(2026, 3, 4, 5, 6, 7, 0, time.Local)

	view := h6c3Model([]MessageStatus{status}).viewMessageStatuses()

	if strings.Contains(view, "следующая попытка") {
		t.Fatalf("view = %q, next attempt must be retrying only", view)
	}
}

func TestMessageStatusViewRendersFailed(t *testing.T) {
	t.Parallel()

	view := h6c3Model([]MessageStatus{
		h6c3Status("entry-1", MessageDeliveryFailed),
	}).viewMessageStatuses()

	if !strings.Contains(view, "Не отправлено") {
		t.Fatalf("view = %q, want failed label", view)
	}
	if strings.Contains(strings.ToLower(view), "повторить") {
		t.Fatalf("view = %q, failed must not offer a retry", view)
	}
}

func TestMessageStatusViewRendersUncertain(t *testing.T) {
	t.Parallel()

	view := h6c3Model([]MessageStatus{
		h6c3Status("entry-1", MessageDeliveryUncertain),
	}).viewMessageStatuses()

	if !strings.Contains(view, "Результат неизвестен") {
		t.Fatalf("view = %q, want uncertain label", view)
	}
	if strings.Contains(view, "Не отправлено") {
		t.Fatalf("view = %q, uncertain must not render as failed", view)
	}
}

func TestMessageStatusViewWarnsAboutDuplicateRiskForUncertain(t *testing.T) {
	t.Parallel()

	view := h6c3Model([]MessageStatus{
		h6c3Status("entry-1", MessageDeliveryUncertain),
	}).viewMessageStatuses()

	want := "Результат отправки неизвестен. " +
		"Повторная отправка может создать дубликат."
	if !strings.Contains(view, want) {
		t.Fatalf("view = %q, want duplicate risk warning", view)
	}
}

func TestMessageStatusViewDoesNotOfferRetryForUncertain(t *testing.T) {
	t.Parallel()

	view := h6c3Model([]MessageStatus{
		h6c3Status("entry-1", MessageDeliveryUncertain),
	}).viewMessageStatuses()

	lowered := strings.ToLower(view)
	for _, forbidden := range []string{
		"повторить",
		"retry",
		"отправить снова",
		"send again",
	} {
		if strings.Contains(lowered, forbidden) {
			t.Fatalf("view = %q, must not offer %q", view, forbidden)
		}
	}
}

func TestMessageStatusViewRendersSent(t *testing.T) {
	t.Parallel()

	view := h6c3Model([]MessageStatus{
		h6c3Status("entry-1", MessageDeliverySent),
	}).viewMessageStatuses()

	if !strings.Contains(view, "Отправлено") {
		t.Fatalf("view = %q, want sent label", view)
	}
	if strings.Contains(view, "В очереди") {
		t.Fatalf("view = %q, sent must not render as queued", view)
	}
}

func TestMessageStatusViewRendersCanceled(t *testing.T) {
	t.Parallel()

	view := h6c3Model([]MessageStatus{
		h6c3Status("entry-1", MessageDeliveryCanceled),
	}).viewMessageStatuses()

	if !strings.Contains(view, "Отменено") {
		t.Fatalf("view = %q, want canceled label", view)
	}
}

func TestMessageStatusViewPreservesSnapshotOrder(t *testing.T) {
	t.Parallel()

	view := h6c3Model([]MessageStatus{
		h6c3Status("entry-1", MessageDeliverySent),
		h6c3Status("entry-2", MessageDeliveryQueued),
		h6c3Status("entry-3", MessageDeliveryRetrying),
	}).viewMessageStatuses()

	lines := strings.Split(view, "\n")
	want := []string{"Отправлено", "В очереди", "Повторная попытка"}
	if len(lines) != len(want) {
		t.Fatalf("lines = %#v, want %d", lines, len(want))
	}
	for index, label := range want {
		if !strings.Contains(lines[index], label) {
			t.Fatalf("line %d = %q, want %q", index, lines[index], label)
		}
	}
}

func TestMessageStatusViewRendersNonNilEmptySnapshot(t *testing.T) {
	t.Parallel()

	view := h6c3Model([]MessageStatus{}).viewMessageStatuses()

	if view == "" {
		t.Fatal("viewMessageStatuses() = empty, want neutral empty state")
	}
	if view != messageStatusEmptyText {
		t.Fatalf("view = %q, want %q", view, messageStatusEmptyText)
	}
}

func TestMessageStatusViewRendersLoadingState(t *testing.T) {
	t.Parallel()

	model := h6c3Model(nil)
	model.messageStatusLoading = true

	view := model.viewMessageStatuses()

	if !strings.Contains(view, messageStatusLoadingText) {
		t.Fatalf("view = %q, want loading text", view)
	}
}

func TestMessageStatusViewKeepsSnapshotWhileLoading(t *testing.T) {
	t.Parallel()

	model := h6c3Model([]MessageStatus{
		h6c3Status("entry-1", MessageDeliverySending),
	})
	model.messageStatusLoading = true

	view := model.viewMessageStatuses()

	if !strings.Contains(view, "Отправляется") {
		t.Fatalf("view = %q, snapshot must stay visible while loading", view)
	}
	if !strings.Contains(view, messageStatusLoadingText) {
		t.Fatalf("view = %q, want loading text", view)
	}
}

func TestMessageStatusViewUsesSafePollingError(t *testing.T) {
	t.Parallel()

	model := h6c3Model([]MessageStatus{
		h6c3Status("entry-1", MessageDeliveryQueued),
	})
	model.messageStatusErr = errors.New("status read failed")

	view := model.viewMessageStatuses()

	if !strings.Contains(view, messageStatusErrorText) {
		t.Fatalf("view = %q, want safe polling error text", view)
	}
}

func TestMessageStatusViewDoesNotRenderRawPollingError(t *testing.T) {
	t.Parallel()

	const secret = "sqlite: /var/tele/outbox.db: provider internal detail"

	model := h6c3Model([]MessageStatus{
		h6c3Status("entry-1", MessageDeliveryQueued),
	})
	model.messageStatusErr = errors.New(secret)

	view := model.viewMessageStatuses()

	if strings.Contains(view, secret) {
		t.Fatalf("view = %q, raw polling error must not be rendered", view)
	}
	if strings.Contains(view, "outbox.db") {
		t.Fatalf("view = %q, internal details must not be rendered", view)
	}
}

func TestMessageStatusViewDoesNotSubmit(t *testing.T) {
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

	block := model.viewMessageStatuses()
	view := model.View()

	if block == "" || view == "" {
		t.Fatal("conversation view did not render the status block")
	}
	if !strings.Contains(view, "В очереди") {
		t.Fatalf("view = %q, want status block in conversation", view)
	}
	if submitter.calls.Load() != 0 {
		t.Fatalf("submitter calls = %d, want 0", submitter.calls.Load())
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

	view := h6c3Model([]MessageStatus{status}).viewMessageStatuses()

	for _, secret := range []string{entryID, accountKey, "Text", "Payload"} {
		if strings.Contains(view, secret) {
			t.Fatalf("view = %q, must not expose %q", view, secret)
		}
	}
}

func TestMessageStatusViewRendersUnknownStateExplicitly(t *testing.T) {
	t.Parallel()

	view := h6c3Model([]MessageStatus{
		h6c3Status("entry-1", MessageDeliveryState("future")),
	}).viewMessageStatuses()

	if !strings.Contains(view, messageStatusUnknownText) {
		t.Fatalf("view = %q, want unknown state text", view)
	}
	if strings.Contains(view, "Не отправлено") {
		t.Fatalf("view = %q, unknown state must not render as failed", view)
	}
}

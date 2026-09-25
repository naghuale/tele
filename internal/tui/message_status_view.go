package tui

import (
	"fmt"
	"strings"
	"time"
)

const (
	// messageStatusHeaderText titles the delivery block of the open chat.
	messageStatusHeaderText = "Delivery"

	// messageStatusLoadingSuffix marks a refresh without hiding the snapshot.
	messageStatusLoadingSuffix = "Обновление…"

	// messageStatusRefreshErrorText is the only user visible form of a status
	// read failure. The raw error stays in model state for errors.Is.
	messageStatusRefreshErrorText = "Не удалось обновить состояния сообщений."

	// messageStatusUnknownText replaces an unexpected state. The raw value is
	// never shown to the user.
	messageStatusUnknownText = "Неизвестное состояние"

	// messageStatusNoticeMarker prefixes warnings and failures.
	messageStatusNoticeMarker = "!"
)

// messageStatusPresentation is the user facing form of one delivery state.
//
// Marker keeps states distinguishable without color. Warning carries an
// explicit caution the user must read, for example the duplicate risk of an
// uncertain result.
type messageStatusPresentation struct {
	Label   string
	Marker  string
	Warning string
}

// presentMessageDeliveryState maps a delivery state onto its presentation.
//
// The switch is exhaustive on purpose: an unknown state is reported as an
// error instead of being displayed as a known state.
func presentMessageDeliveryState(
	state MessageDeliveryState,
) (messageStatusPresentation, error) {
	switch state {
	case MessageDeliveryQueued:
		return messageStatusPresentation{
			Label:  "В очереди",
			Marker: "○",
		}, nil

	case MessageDeliverySending:
		return messageStatusPresentation{
			Label:  "Отправляется",
			Marker: "●",
		}, nil

	case MessageDeliveryRetrying:
		return messageStatusPresentation{
			Label:  "Повторная попытка",
			Marker: "↻",
		}, nil

	case MessageDeliveryFailed:
		return messageStatusPresentation{
			Label:  "Не отправлено",
			Marker: "×",
		}, nil

	case MessageDeliveryUncertain:
		return messageStatusPresentation{
			Label:   "Результат неизвестен",
			Marker:  "?",
			Warning: "Повторная отправка может создать дубликат.",
		}, nil

	case MessageDeliverySent:
		return messageStatusPresentation{
			Label:  "Отправлено",
			Marker: "✓",
		}, nil

	case MessageDeliveryCanceled:
		return messageStatusPresentation{
			Label:  "Отменено",
			Marker: "−",
		}, nil

	default:
		return messageStatusPresentation{}, fmt.Errorf(
			"unsupported message delivery state %q",
			state,
		)
	}
}

// viewMessageStatuses renders the delivery block of the open chat.
//
// It is a pure read of the current snapshot: it never queries the status
// source, never polls, never submits and never mutates model state.
func (m Model) viewMessageStatuses() string {
	return m.viewMessageStatusesAt(time.Now())
}

// viewMessageStatusesAt renders the block for an explicit instant so that
// relative retry timings are deterministic in tests.
func (m Model) viewMessageStatusesAt(now time.Time) string {
	// A nil source is direct delivery mode: the block is absent, and the
	// absence of a durable status is not reported as a problem.
	if m.messageStatuses == nil {
		return ""
	}

	header := messageStatusHeaderText
	if m.messageStatusLoading {
		header += " · " + messageStatusLoadingSuffix
	}
	lines := []string{header}

	if m.messageStatusErr != nil {
		lines = append(
			lines,
			messageStatusNoticeMarker+" "+messageStatusRefreshErrorText,
		)
	}
	lines = append(lines, m.messageStatusEntryLines(now)...)

	// A stable empty snapshot produces no block at all: an empty delivery
	// section is visual noise. The first load still shows its neutral
	// progress line.
	if len(lines) == 1 && !m.messageStatusLoading {
		return ""
	}

	return strings.Join(lines, "\n")
}

// messageStatusEntryLines renders one line per status in reader order, plus a
// warning line where a state requires attention.
//
// Entries are never re-sorted. The count is bounded so the block cannot push
// the composer out of the chat view; an unknown terminal height renders the
// whole snapshot. One line of the budget is reserved for the block header.
func (m Model) messageStatusEntryLines(now time.Time) []string {
	entryLimit := 0
	if limit := messageStatusLineLimit(m.height); limit > 0 {
		entryLimit = maxInt(limit-1, 1)
	}
	lines := make([]string, 0, len(m.deliveryStatuses))

	for _, status := range m.deliveryStatuses {
		presentation, err := presentMessageDeliveryState(status.State)
		if err != nil {
			lines = append(
				lines,
				messageStatusNoticeMarker+" "+messageStatusUnknownText,
			)
		} else {
			line := "  " + presentation.Marker + " " + presentation.Label
			if details := formatMessageStatusDetails(status, now); details != "" {
				line += " · " + details
			}
			lines = append(lines, line)
			if presentation.Warning != "" {
				lines = append(lines, "  "+presentation.Warning)
			}
		}

		if entryLimit > 0 && len(lines) >= entryLimit {
			break
		}
	}

	return lines
}

// formatMessageStatusDetails renders attempt and next attempt metadata.
//
// Retry scheduling is shown for a retrying entry only, a zero attempt count is
// hidden, and a next attempt in the past is not rendered as a negative
// duration.
func formatMessageStatusDetails(
	status MessageStatus,
	now time.Time,
) string {
	if status.State != MessageDeliveryRetrying {
		return ""
	}

	parts := make([]string, 0, 2)
	if status.Attempt > 0 {
		parts = append(parts, fmt.Sprintf("попытка %d", status.Attempt))
	}
	if !status.NextAttemptAt.IsZero() && status.NextAttemptAt.After(now) {
		parts = append(
			parts,
			"через "+formatMessageStatusWait(
				status.NextAttemptAt.Sub(now),
			),
		)
	}

	return strings.Join(parts, " · ")
}

// formatMessageStatusWait formats a wait rounded to whole seconds.
func formatMessageStatusWait(wait time.Duration) string {
	seconds := int(wait.Round(time.Second).Seconds())
	if seconds < 1 {
		seconds = 1
	}
	if seconds < 60 {
		return fmt.Sprintf("%dс", seconds)
	}
	return fmt.Sprintf("%dм %02dс", seconds/60, seconds%60)
}

// messageStatusLineLimit caps the block at roughly 30% of the chat view
// height. A non-positive height means the model is not laid out yet.
func messageStatusLineLimit(height int) int {
	if height <= 0 {
		return 0
	}
	limit := height / 3
	if limit < 1 {
		limit = 1
	}
	return limit
}

package tui

import (
	"fmt"
	"strings"
)

const (
	messageStatusEmptyText   = "Нет сообщений с активной доставкой."
	messageStatusLoadingText = "Обновление состояний доставки..."
	messageStatusErrorText   = "Не удалось обновить состояния сообщений."
	messageStatusUnknownText = "Неизвестное состояние доставки"

	messageStatusTimeLayout = "2006-01-02 15:04:05"
)

// messageStatusPresentation is the user facing text of one delivery state.
//
// Warning carries an explicit caution that the user must act on, for example
// the duplicate risk of an uncertain result.
type messageStatusPresentation struct {
	Label   string
	Warning string
}

// presentMessageDeliveryState maps a delivery state onto its presentation.
//
// The switch is exhaustive on purpose: an unknown state is reported as an
// error instead of being displayed as a delivery failure.
func presentMessageDeliveryState(
	state MessageDeliveryState,
) (messageStatusPresentation, error) {
	switch state {
	case MessageDeliveryQueued:
		return messageStatusPresentation{Label: "В очереди"}, nil

	case MessageDeliverySending:
		return messageStatusPresentation{Label: "Отправляется"}, nil

	case MessageDeliveryRetrying:
		return messageStatusPresentation{Label: "Повторная попытка"}, nil

	case MessageDeliveryFailed:
		return messageStatusPresentation{Label: "Не отправлено"}, nil

	case MessageDeliveryUncertain:
		return messageStatusPresentation{
			Label: "Результат неизвестен",
			Warning: "Результат отправки неизвестен. " +
				"Повторная отправка может создать дубликат.",
		}, nil

	case MessageDeliverySent:
		return messageStatusPresentation{Label: "Отправлено"}, nil

	case MessageDeliveryCanceled:
		return messageStatusPresentation{Label: "Отменено"}, nil

	default:
		return messageStatusPresentation{}, fmt.Errorf(
			"unsupported message delivery state %q",
			state,
		)
	}
}

// viewMessageStatuses renders the current durable delivery snapshot.
//
// It reads state only: it never queries the status source, never polls and
// never submits. A nil source means direct delivery mode, where the durable
// status section is not shown at all.
func (m Model) viewMessageStatuses() string {
	if m.messageStatuses == nil {
		return ""
	}

	lines := make([]string, 0, len(m.deliveryStatuses)+2)
	if m.messageStatusLoading {
		lines = append(lines, messageStatusLoadingText)
	}
	// The polling error is never rendered verbatim: it may carry internal
	// details. A fixed phrase is shown instead, and the last successful
	// snapshot stays visible.
	if m.messageStatusErr != nil {
		lines = append(lines, messageStatusErrorText)
	}
	if len(m.deliveryStatuses) == 0 {
		if len(lines) == 0 {
			lines = append(lines, messageStatusEmptyText)
		}
		return strings.Join(lines, "\n")
	}

	// Reader order is preserved: statuses are never re-sorted here.
	for _, status := range m.deliveryStatuses {
		lines = append(lines, formatMessageStatus(status))
	}

	return strings.Join(lines, "\n")
}

// formatMessageStatus renders one status line.
//
// Only the delivery state, its warning and the scheduled next attempt are
// shown. Entry identifiers, account keys, message text and provider details
// stay out of the view.
func formatMessageStatus(status MessageStatus) string {
	presentation, err := presentMessageDeliveryState(status.State)
	if err != nil {
		return messageStatusUnknownText
	}

	line := "  " + presentation.Label
	if presentation.Warning != "" {
		line += " — " + presentation.Warning
	}
	if status.State == MessageDeliveryRetrying &&
		!status.NextAttemptAt.IsZero() {
		line += fmt.Sprintf(
			" (следующая попытка %s)",
			status.NextAttemptAt.Local().Format(messageStatusTimeLayout),
		)
	}
	return line
}

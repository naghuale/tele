package tui

import (
	"context"
	"errors"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// messageStatusPollInterval is the delay between two durable status reads.
const messageStatusPollInterval = 2 * time.Second

type messageStatusPollTickMsg struct {
	generation uint64
}

type messageStatusesLoadedMsg struct {
	generation uint64
	accountKey string
	chatID     int64
	statuses   []MessageStatus

	// pending is the outgoing messages of the chat with their text, and it
	// is nil when there is no pending source. It travels with the statuses
	// because the two are read together and drawn together: a read that
	// delivered one without the other would draw a message with the state
	// of another.
	pending []PendingMessage
}

type messageStatusesFailedMsg struct {
	generation uint64
	accountKey string
	chatID     int64
	err        error
}

// setMessageStatusTarget points polling at an account and chat.
//
// Selecting the same target twice is a no-op, so a repeated selection does not
// burn a generation. A zero chatID means that no conversation is active, which
// disables polling while still invalidating in-flight responses.
func (m *Model) setMessageStatusTarget(
	accountKey string,
	chatID int64,
) tea.Cmd {
	if m == nil {
		return nil
	}
	if m.messageStatusAccountKey == accountKey &&
		m.messageStatusChatID == chatID {
		return nil
	}
	return m.resetMessageStatusPolling(accountKey, chatID)
}

// resetMessageStatusPolling starts a new generation for a new target.
//
// The previous snapshot is dropped: statuses of another chat must never be
// rendered as if they belonged to the active one.
func (m *Model) resetMessageStatusPolling(
	accountKey string,
	chatID int64,
) tea.Cmd {
	if m == nil {
		return nil
	}

	m.messageStatusGeneration++
	m.messageStatusAccountKey = accountKey
	m.messageStatusChatID = chatID
	m.messageStatusLoading = false
	m.messageStatusErr = nil
	m.deliveryStatuses = []MessageStatus{}

	// A new chat has a new set of pending messages, and a message this
	// session queued for another chat is not on this screen.
	m.pending = nil

	return m.loadMessageStatuses()
}

// invalidateMessageStatusPolling stops polling and discards in-flight
// responses by advancing the generation.
func (m *Model) invalidateMessageStatusPolling() {
	if m == nil {
		return
	}
	m.messageStatusGeneration++
	m.messageStatusLoading = false
	m.messageStatusAccountKey = ""
	m.messageStatusChatID = 0
}

// loadMessageStatuses starts at most one request for the active generation.
//
// A nil command means that polling is disabled: direct delivery mode has no
// status source, and an inactive or shutting-down model must not read
// anything.
func (m *Model) loadMessageStatuses() tea.Cmd {
	if m == nil {
		return nil
	}
	if m.quitting ||
		(m.messageStatuses == nil && m.pendingMessages == nil) ||
		m.messageStatusLoading ||
		m.messageStatusAccountKey == "" ||
		m.messageStatusChatID == 0 {
		return nil
	}

	m.messageStatusLoading = true

	generation := m.messageStatusGeneration
	accountKey := m.messageStatusAccountKey
	chatID := m.messageStatusChatID
	statuses := m.messageStatuses
	pending := m.pendingMessages
	ctx := m.ctx

	return func() tea.Msg {
		if err := ctx.Err(); err != nil {
			return messageStatusesFailedMsg{
				generation: generation,
				accountKey: accountKey,
				chatID:     chatID,
				err:        err,
			}
		}

		var (
			readStatuses []MessageStatus
			readPending  []PendingMessage
		)

		if statuses != nil {
			read, err := statuses.ListMessageStatuses(ctx, accountKey, chatID)
			if err != nil {
				return messageStatusesFailedMsg{
					generation: generation,
					accountKey: accountKey,
					chatID:     chatID,
					err:        err,
				}
			}
			readStatuses = cloneMessageStatuses(read)
		}

		if pending != nil {
			read, err := pending.ListPendingMessages(ctx, accountKey, chatID)
			if err != nil {
				return messageStatusesFailedMsg{
					generation: generation,
					accountKey: accountKey,
					chatID:     chatID,
					err:        err,
				}
			}
			readPending = clonePendingMessages(read)
		}

		return messageStatusesLoadedMsg{
			generation: generation,
			accountKey: accountKey,
			chatID:     chatID,
			statuses:   readStatuses,
			pending:    readPending,
		}
	}
}

// clonePendingMessages copies a snapshot so the model never aliases a
// slice a source owns.
func clonePendingMessages(messages []PendingMessage) []PendingMessage {
	if len(messages) == 0 {
		return nil
	}

	return append([]PendingMessage{}, messages...)
}

// isCurrentMessageStatusResponse reports whether a response still belongs to
// the active generation and target.
//
// A stale response must change nothing at all, including the loading flag:
// clearing it would let a second request overlap the current one.
func (m *Model) isCurrentMessageStatusResponse(
	generation uint64,
	accountKey string,
	chatID int64,
) bool {
	if m == nil {
		return false
	}
	return generation == m.messageStatusGeneration &&
		accountKey == m.messageStatusAccountKey &&
		chatID == m.messageStatusChatID
}

func (m Model) handleMessageStatusesLoaded(
	msg messageStatusesLoadedMsg,
) (Model, tea.Cmd) {
	if !m.isCurrentMessageStatusResponse(
		msg.generation,
		msg.accountKey,
		msg.chatID,
	) {
		return m, nil
	}

	m.messageStatusLoading = false
	m.messageStatusErr = nil
	m.deliveryStatuses = cloneMessageStatuses(msg.statuses)
	m.mergePendingMessages(msg.pending, m.deliveryStatuses)

	if m.quitting {
		return m, nil
	}
	return m, scheduleMessageStatusPoll(m.messageStatusGeneration)
}

func (m Model) handleMessageStatusesFailed(
	msg messageStatusesFailedMsg,
) (Model, tea.Cmd) {
	if !m.isCurrentMessageStatusResponse(
		msg.generation,
		msg.accountKey,
		msg.chatID,
	) {
		return m, nil
	}

	m.messageStatusLoading = false

	// A canceled read is a control-flow event, not a user facing failure.
	if errors.Is(msg.err, context.Canceled) {
		m.messageStatusErr = nil
		return m, nil
	}

	m.messageStatusErr = msg.err

	if m.quitting {
		return m, nil
	}
	return m, scheduleMessageStatusPoll(m.messageStatusGeneration)
}

func (m Model) handleMessageStatusPollTick(
	msg messageStatusPollTickMsg,
) (Model, tea.Cmd) {
	if m.quitting || msg.generation != m.messageStatusGeneration {
		return m, nil
	}
	return m, m.loadMessageStatuses()
}

func scheduleMessageStatusPoll(
	generation uint64,
) tea.Cmd {
	return tea.Tick(
		messageStatusPollInterval,
		func(time.Time) tea.Msg {
			return messageStatusPollTickMsg{generation: generation}
		},
	)
}

// cloneMessageStatuses copies a snapshot so the model never aliases a slice
// owned by a source or a Bubble Tea message.
func cloneMessageStatuses(
	statuses []MessageStatus,
) []MessageStatus {
	if len(statuses) == 0 {
		return []MessageStatus{}
	}
	result := make([]MessageStatus, len(statuses))
	copy(result, statuses)
	return result
}

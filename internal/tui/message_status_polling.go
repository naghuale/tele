package tui

import (
	"context"
	"errors"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// messageStatusPollInterval is the delay between two durable status reads.
const messageStatusPollInterval = 2 * time.Second

// messageStatusPollTickMsg is the tick of the delivery poll.
//
// It carries nothing about which target it was armed for: a tick is a
// timer, and the only question it answers is whether the loop is still
// running. The reads it starts are asked for the target the model holds
// now, which is the one thing a tick from a target the model has left must
// not do.
type messageStatusPollTickMsg struct{}

type messageStatusesLoadedMsg struct {
	generation uint64

	// read is the number of the read that produced this message, so a slow
	// read that answers after a newer one is discarded instead of
	// overwriting it.
	read       uint64
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
	read       uint64
	accountKey string
	chatID     int64
	err        error
}

// statusSummaryLoadedMsg is delivered by loadStatusSummary.
//
// A read that changed nothing delivers nothing at all: a poll that wakes
// the program every two seconds to redraw the same screen is a program
// that burns power and flickers, and the read is cheap enough to be worth
// nothing when it learned nothing.
type statusSummaryLoadedMsg struct {
	generation uint64

	// read is the number of the read that produced this message, so a slow
	// read that answers after a newer one is discarded instead of
	// overwriting it.
	read    uint64
	summary StatusSummary
}

type statusSummaryFailedMsg struct {
	generation uint64
	read       uint64
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

// loadStatusSummary starts one read of the status line's data.
//
// It is independent of the chat: the status line is under the conversation
// title, and it is also in the header of the chat list on a narrow screen
// where no conversation is open at all. A summary read that is waiting for
// an open chat would leave the program saying nothing exactly when the
// user is least able to act.
func (m *Model) loadStatusSummary() tea.Cmd {
	if m == nil || m.quitting || m.statusSummaries == nil || m.summaryLoading {
		return nil
	}

	m.summaryLoading = true
	m.summaryReadSeq++

	generation := m.messageStatusGeneration
	read := m.summaryReadSeq
	known := m.summary
	source := m.statusSummaries
	chatID := m.messageStatusChatID
	ctx := m.ctx

	return func() tea.Msg {
		if err := ctx.Err(); err != nil {
			return statusSummaryFailedMsg{generation: generation, read: read, err: err}
		}

		summary, err := source.ReadStatusSummary(ctx, chatID)
		if err != nil {
			return statusSummaryFailedMsg{generation: generation, read: read, err: err}
		}
		if summary == known {
			// Nothing changed, so nothing is delivered. The model keeps
			// loading=true until the next tick, which is where the next
			// read is started from.
			return nil
		}

		return statusSummaryLoadedMsg{
			generation: generation,
			read:       read,
			summary:    summary,
		}
	}
}

// isCurrentSummaryRead reports whether a summary response is the newest
// one and belongs to the active generation.
//
// A read that was slow can answer after the read that replaced it, and a
// stale summary would put an old connection state back on the screen.
func (m *Model) isCurrentSummaryRead(msgRead, generation uint64) bool {
	if m == nil {
		return false
	}

	return generation == m.messageStatusGeneration && msgRead == m.summaryReadSeq
}

func (m Model) handleStatusSummaryLoaded(
	msg statusSummaryLoadedMsg,
) (Model, tea.Cmd) {
	if m.quitting || !m.isCurrentSummaryRead(msg.read, msg.generation) {
		return m, nil
	}

	m.summaryLoading = false
	m.summaryErr = nil
	m.summary = msg.summary

	return m, m.armPresenceDeadline()
}

// armPresenceDeadline schedules the repaint that ends an online presence.
//
// An online status carries the moment it runs out and TDLib sends nothing
// at that moment, so the word has to change without an update. One message
// at the moment of the change is the whole of it; the view still reads the
// clock, because a message that stored the expired state would be a second
// copy of a fact the clock already has.
func (m Model) armPresenceDeadline() tea.Cmd {
	expires := m.summary.Presence.ExpiresAt
	if m.summary.Presence.Kind != PresenceUser || expires.IsZero() {
		return nil
	}

	remaining := expires.Sub(m.clock()())
	if remaining <= 0 {
		return nil
	}

	return tea.Tick(remaining, func(time.Time) tea.Msg {
		return presenceExpiredMsg{expires: expires}
	})
}

// handlePresenceExpired repaints the screen.
//
// The data is not changed: the presence still says when it expires and the
// view reads the clock, so what changed is only what the user reads.
func (m Model) handlePresenceExpired(msg presenceExpiredMsg) (Model, tea.Cmd) {
	return m, nil
}

// handleStatusSummaryFailed keeps the last summary.
//
// The counts of a queue that could not be read are dropped rather than
// kept: they would be the counts of a moment ago, and a user who is told
// "2 queued" cannot tell that from a live one. The connection survives,
// because a client that was connected has not stopped being connected.
func (m Model) handleStatusSummaryFailed(
	msg statusSummaryFailedMsg,
) (Model, tea.Cmd) {
	if m.quitting || !m.isCurrentSummaryRead(msg.read, msg.generation) {
		return m, nil
	}

	m.summaryLoading = false

	if errors.Is(msg.err, context.Canceled) {
		return m, nil
	}

	m.reportDiagnostic("status summary unavailable: %v\n", msg.err)
	m.summaryErr = msg.err
	if m.summary.Queue.Known {
		m.summary.Queue = QueueSummary{}
	}

	return m, nil
}

// reportDiagnostic writes a cause to the diagnostic stream.
//
// The stream is a log, not a screen: §11.3 and §19 keep the transport
// error text, the file paths and the secrets out of the interface and
// leave them here, where somebody asked for them.
func (m Model) reportDiagnostic(format string, args ...any) {
	if m.diagnostics == nil {
		return
	}

	fmt.Fprintf(m.diagnostics, format, args...)
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
	m.statusReadSeq++

	generation := m.messageStatusGeneration
	read := m.statusReadSeq
	accountKey := m.messageStatusAccountKey
	chatID := m.messageStatusChatID
	statuses := m.messageStatuses
	pending := m.pendingMessages
	knownStatuses := m.deliveryStatuses
	knownPending := m.pendingSnapshot
	ctx := m.ctx

	return func() tea.Msg {
		if err := ctx.Err(); err != nil {
			return messageStatusesFailedMsg{
				generation: generation,
				read:       read,
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
			listed, err := statuses.ListMessageStatuses(ctx, accountKey, chatID)
			if err != nil {
				return messageStatusesFailedMsg{
					generation: generation,
					read:       read,
					accountKey: accountKey,
					chatID:     chatID,
					err:        err,
				}
			}
			readStatuses = cloneMessageStatuses(listed)
		}

		if pending != nil {
			listed, err := pending.ListPendingMessages(ctx, accountKey, chatID)
			if err != nil {
				return messageStatusesFailedMsg{
					generation: generation,
					read:       read,
					accountKey: accountKey,
					chatID:     chatID,
					err:        err,
				}
			}
			readPending = clonePendingMessages(listed)
		}

		// Nothing changed, so nothing is delivered. An idle program that
		// redraws itself every two seconds is a program that a user with
		// a battery is paying for, and a repaint that changes no pixels is
		// a flicker a user can see.
		if equalMessageStatuses(readStatuses, knownStatuses) &&
			equalPendingMessages(readPending, knownPending) {
			return nil
		}

		return messageStatusesLoadedMsg{
			generation: generation,
			read:       read,
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

// messageResponseTarget is what a response carries to say which read and
// which target it belongs to.
type messageResponseTarget struct {
	generation uint64
	read       uint64
	accountKey string
	chatID     int64
}

func (m messageStatusesLoadedMsg) messageResponseTarget() messageResponseTarget {
	return messageResponseTarget{
		generation: m.generation,
		read:       m.read,
		accountKey: m.accountKey,
		chatID:     m.chatID,
	}
}

func (m messageStatusesFailedMsg) messageResponseTarget() messageResponseTarget {
	return messageResponseTarget{
		generation: m.generation,
		read:       m.read,
		accountKey: m.accountKey,
		chatID:     m.chatID,
	}
}

// isCurrentMessageStatusResponse reports whether a response still belongs to
// the active generation and target.
//
// A stale response must change nothing at all, including the loading flag:
// clearing it would let a second request overlap the current one.
func (m *Model) isCurrentMessageStatusResponse(
	msg messageResponseTarget,
) bool {
	if m == nil {
		return false
	}
	return msg.generation == m.messageStatusGeneration &&
		msg.read == m.statusReadSeq &&
		msg.accountKey == m.messageStatusAccountKey &&
		msg.chatID == m.messageStatusChatID
}

func (m Model) handleMessageStatusesLoaded(
	msg messageStatusesLoadedMsg,
) (Model, tea.Cmd) {
	if !m.isCurrentMessageStatusResponse(msg.messageResponseTarget()) {
		return m, nil
	}

	m.messageStatusLoading = false
	m.messageStatusErr = nil
	m.deliveryStatuses = cloneMessageStatuses(msg.statuses)
	m.pendingSnapshot = clonePendingMessages(msg.pending)
	m.mergePendingMessages(msg.pending, m.deliveryStatuses)

	// A message Telegram has confirmed is no longer pending: it is a
	// message of the conversation now, and leaving it drawn as one that is
	// still going out is what made it disappear from the feed the moment
	// the user looked at another chat. It is merged rather than dropped so
	// that it stays in the timeline, under its final identifier, which is
	// the one the history comes back with.
	if delivered := m.deliverSentMessages(); delivered {
		return m, withRepaint(m, nil)
	}

	// The next read is scheduled by the tick, not by this response: the
	// cadence belongs to the loop and not to whichever read answered last.
	return m, nil
}

// deliverSentMessages moves the messages this session queued that the
// queue has confirmed into the history of their chat.
//
// The text comes from the pending message the model already holds and the
// identifier from the status the queue reports, so nothing is fetched and
// no message is read twice: the merge replaces by identifier, so a history
// page that later brings the same message lands on the row that is
// already there.
//
// It reports whether the timeline changed.
func (m *Model) deliverSentMessages() bool {
	if m.source == nil || m.selectedChat < 0 ||
		m.selectedChat >= len(m.chats) {
		return false
	}

	delivered := m.pendingSentByID()
	if len(delivered) == 0 {
		return false
	}

	chat := &m.chats[m.selectedChat]
	atNewest := m.timelineFollowsNewest()
	for _, message := range delivered {
		chat.Messages = appendMessageByID(chat.Messages, message)
	}

	kept := make([]PendingMessage, 0, len(m.pending))
	for _, message := range m.pending {
		if _, stillPending := delivered[message.EntryID]; stillPending {
			continue
		}
		kept = append(kept, message)
	}
	m.pending = kept
	m.pendingSnapshot = clonePendingMessages(kept)

	if atNewest {
		*m = m.scrollToNewest()
	}
	*m = m.normalizeTimeline()
	return true
}

// pendingSentByID returns the messages the queue has confirmed, keyed by
// the queue entry they were sent as.
//
// A message is delivered only when the queue reports a final identifier
// for it: without one the row would go into the history under nothing, and
// a message the user wrote would be drawn twice with no way to tell them
// apart.
func (m Model) pendingSentByID() map[string]Message {
	confirmed := make(map[string]Message)
	for _, message := range m.pending {
		if message.State != MessageDeliverySent || message.MessageID == 0 {
			continue
		}
		confirmed[message.EntryID] = Message{
			ID:       message.MessageID,
			Outgoing: true,
			Text:     message.Text,
			Time:     formatMessageTime(message.CreatedAt),
		}
	}
	if len(confirmed) == 0 {
		return nil
	}
	return confirmed
}

func (m Model) handleMessageStatusesFailed(
	msg messageStatusesFailedMsg,
) (Model, tea.Cmd) {
	if !m.isCurrentMessageStatusResponse(msg.messageResponseTarget()) {
		return m, nil
	}

	m.messageStatusLoading = false

	// A canceled read is a control-flow event, not a user facing failure.
	if errors.Is(msg.err, context.Canceled) {
		m.messageStatusErr = nil
		return m, nil
	}

	m.messageStatusErr = msg.err

	return m, nil
}

// handleMessageStatusPollTick is the one loop of the program that reads
// delivery state, and the status summary is read on it.
//
// One loop, one cadence, one place that decides when a read happens. Two
// timers would let the status line and the delivery states be read a
// second apart, and the screen would show a queue count that does not match
// the states under the messages it counts.
//
// A tick from a generation the model has left still schedules the next
// one, for the generation that is current now. Dropping it is what made a
// message sit on `Queued` for the rest of the session: opening a chat
// starts a generation, the tick that was already on its way belongs to the
// old one, and the loop ended there — a user who sent a message after
// opening their first chat never saw it leave the queue.
func (m Model) handleMessageStatusPollTick(
	messageStatusPollTickMsg,
) (Model, tea.Cmd) {
	if m.quitting {
		return m, nil
	}

	// The read a stale tick would have started belongs to a target the
	// model has left, so it is not started. The loading flags are cleared
	// anyway: the response that owned them is discarded by its
	// generation, and leaving them set would block the next read until the
	// tick after that one.
	m.messageStatusLoading = false
	m.summaryLoading = false

	return m, m.pollDeliverySources()
}

// deliveryPolling reports whether there is anything to poll.
//
// A model with no source at all must not keep a timer alive: a program
// that wakes every two seconds to read nothing is a program that cannot
// be reasoned about and cannot be put to sleep.
func (m Model) deliveryPolling() bool {
	if m.quitting {
		return false
	}
	if m.statusSummaries != nil {
		return true
	}

	return (m.messageStatuses != nil || m.pendingMessages != nil) &&
		m.messageStatusAccountKey != ""
}

// pollDeliverySources reads every source once and schedules the next tick.
func (m *Model) pollDeliverySources() tea.Cmd {
	if !m.deliveryPolling() {
		return nil
	}

	cmds := make([]tea.Cmd, 0, 3)
	if cmd := m.loadStatusSummary(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if cmd := m.loadMessageStatuses(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	cmds = append(cmds, scheduleMessageStatusPoll())

	return tea.Batch(cmds...)
}

func scheduleMessageStatusPoll() tea.Cmd {
	return tea.Tick(
		messageStatusPollInterval,
		func(time.Time) tea.Msg { return messageStatusPollTickMsg{} },
	)
}

// equalMessageStatuses reports whether two snapshots are the same.
//
// A field-by-field comparison rather than a string or a reflect.DeepEqual:
// the snapshot is a flat value and the loop runs every two seconds, so the
// cost of a read should not include a walk of the type.
func equalMessageStatuses(left, right []MessageStatus) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}

	return true
}

// equalPendingMessages reports whether two pending lists are the same.
//
// The entries hold a time and a state, and both are comparable values, so
// the same field-by-field comparison is enough.
func equalPendingMessages(left, right []PendingMessage) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}

	return true
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

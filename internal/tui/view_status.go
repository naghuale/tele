package tui

import (
	"strconv"

	"github.com/charmbracelet/lipgloss"
)

// This file draws the status block: the one or two lines §4.3 puts under
// the title of a conversation, and §3.3 puts in the header of the chat
// list when the screen is too narrow to show both panes.
//
// The block is a list of parts joined with the separator of §3.3, in the
// order §11.1 ranks them: a queue telecli cannot write to first, then the
// connection, then recovery, then the counts. The ranking is a ranking of
// what a user must know, not of how bad it is: a user whose queue cannot
// be opened does not need to know that four messages are queued, and a user
// whose client is off the network does not need a counter to act.
//
// A part that is not known is not drawn. An unknown connection is not
// "offline" and an unreadable queue is not "empty", and the interface has
// no word for either that would be honest.

// statusBlock returns the lines of the status block, or none when there is
// nothing to say.
//
// The block is at most two lines, and one on a short screen (§3.4 hides
// the second line): a status that grows into the conversation costs the
// messages it is meant to explain.
func (m Model) statusBlock(layout Layout, width int) []string {
	lines := m.statusBlockLines(layout, width)
	if len(lines) == 0 {
		return nil
	}

	style := m.statusStyle()
	for index, line := range lines {
		lines[index] = style.Render(line)
	}

	return lines
}

// statusBlockLines returns the text of the status block, one entry per row.
//
// The text is fitted and capped here and the style is applied by
// statusBlock, in that order: a line is measured by what it says, so the
// width is the width of the words rather than of the escape sequences that
// colour them.
func (m Model) statusBlockLines(layout Layout, width int) []string {
	parts := m.statusParts()
	if len(parts) == 0 || width < 1 {
		return nil
	}

	// A tall screen has two lines and a short one has a single line (§3.4
	// hides the second). A third line is the conversation, and the
	// conversation is what the user is here for.
	limit := maxStatusLines
	if layout.Short() {
		limit = 1
	}

	// What does not fit is dropped from the end, in the order §11.1 ranks
	// the parts, rather than cut. A status line cut in the middle of a
	// sentence says less than the sentence above it: a user reading
	// "Waiting for network · Recovering" is left wondering about the rest
	// of a word, while one reading "Waiting for network" has everything
	// the line is for.
	lines := wrapCells(joinWithSeparator(parts), width)
	for len(lines) > limit && len(parts) > 1 {
		parts = parts[:len(parts)-1]
		lines = wrapCells(joinWithSeparator(parts), width)
	}
	if len(lines) > limit {
		lines = lines[:limit]
	}
	for index, line := range lines {
		lines[index] = fitCells(line, width)
	}

	return lines
}

// maxStatusLines is how many lines the status block may take (§4.3).
const maxStatusLines = 2

// statusSeparator is what holds the parts of the status block together.
//
// It is the middle dot of §3.3, the same one the hint bar uses, so a user
// reads one way of saying "and also" on this screen.
const statusSeparator = " · "

// statusParts returns the parts of the status block in priority order.
func (m Model) statusParts() []string {
	// A notice is what an action just did, and it is the thing the user is
	// looking for: they pressed a key and the screen owes them an answer
	// before it owes them anything else.
	if m.notice != "" {
		return []string{m.notice}
	}

	// §11.1 puts the key and outbox failure above everything else, and
	// decision 3 names the word. The counts of a queue nothing can be
	// queued into would be a lie about that queue, so they go.
	if m.pausedErr != nil {
		return []string{sendingPausedHeadline}
	}

	var parts []string
	// The presence comes first because it is the only part of the line
	// that is about a person rather than about the program, and a user
	// reading a header wants to know who is there before they want to know
	// how the queue is doing. It is also the part that is dropped last
	// when the line does not fit: the queue can be read again in two
	// seconds, and a person cannot.
	if presence := presenceText(
		m.summary.Presence,
		m.clock()(),
		m.timeZone(),
	); presence != "" {
		parts = append(parts, presence)
	}

	if connection := connectionStatusText(m.summary.Connection); connection != "" {
		parts = append(parts, connection)
	}

	queue := m.summary.Queue
	if !queue.Known {
		return parts
	}

	if queue.Recovering {
		parts = append(parts, recoveringMessagesText)
	}
	if queue.Queued > 0 {
		parts = append(parts, strconv.Itoa(queue.Queued)+" queued")
	}
	if queue.Retrying > 0 {
		parts = append(parts, strconv.Itoa(queue.Retrying)+" retrying")
	}

	return parts
}

// connectionStatusText is what the interface calls a connection state.
//
// The wording is §11.2's: a state is a sentence a user can read without a
// legend, and the two states that are not yet a connection say what is
// being waited for. An unknown state has no word, because there is nothing
// true to say about it.
func connectionStatusText(state ConnectionState) string {
	switch state {
	case ConnectionReady:
		return "Connected"
	case ConnectionConnecting:
		return "Connecting…"
	case ConnectionUpdating:
		return "Updating…"
	case ConnectionWaitingForNetwork:
		return "Waiting for network"
	case ConnectionConnectingToProxy:
		return "Connecting to proxy…"
	default:
		return ""
	}
}

// The words of the status block that are not a state name.
const (
	// sendingPausedHeadline is the status line while sending is paused.
	// The wording is fixed by decision 3 and must stay in step with
	// docs/help/sending-paused.md.
	sendingPausedHeadline = "Sending paused"

	// recoveringMessagesText is said while the delivery runtime is still
	// starting and the entries of a previous run are being recovered. It
	// says that something is happening instead of leaving an empty queue
	// unexplained.
	recoveringMessagesText = "Recovering interrupted messages…"
)

// statusStyle is the style of the whole block.
//
// One style per block rather than one per part: the parts are separated by
// a dot, and a line whose parts are in five colours reads as five messages
// instead of one. The colour is chosen by the part that ranks highest, so
// what a user notices first is what §11.1 says matters first, and the word
// carries the meaning in every profile whatever the colour does (§14).
func (m Model) statusStyle() lipgloss.Style {
	switch {
	case m.notice != "":
		return m.styles().text(m.tokens().StatusActive)
	case m.pausedErr != nil:
		return m.styles().text(m.tokens().StatusError)
	case presenceText(m.summary.Presence, m.clock()(), m.timeZone()) != "":
		// A presence line is a status about somebody, and the status
		// vocabulary of the theme says a status is a colour that repeats
		// the word rather than one that carries it.
		return m.styles().text(m.tokens().StatusSuccess)
	case m.summary.Connection != ConnectionReady &&
		m.summary.Connection != ConnectionUnknown:
		return m.styles().text(m.tokens().StatusWarning)
	case m.summary.Queue.Recovering:
		return m.styles().text(m.tokens().StatusActive)
	default:
		return m.styles().dimmed(m.tokens().SecondaryText)
	}
}

// joinWithSeparator joins the parts of the status block.
func joinWithSeparator(parts []string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	}

	text := parts[0]
	for _, part := range parts[1:] {
		text += statusSeparator + part
	}

	return text
}

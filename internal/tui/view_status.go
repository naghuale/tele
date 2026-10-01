package tui

import (
	"strconv"

	"github.com/charmbracelet/lipgloss"

	"telecli/internal/tui/theme"
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

// statusPart is one part of the status line: the words, and the colour they
// are drawn in.
//
// A part carries a colour of its own because of the one part that has to
// have one. The presence is the only part of the line that is about a
// person rather than about the program, and the mockup of the owner (30.09)
// draws its word in green and the rest of the line in the dim step of the
// text ramp: a reader sees "online" before they read anything else, and a
// line in one colour cannot say that. Everything else on the line is the
// program's own state and stays in the block's own colour, so the line
// reads as one line.
type statusPart struct {
	text  string
	style lipgloss.Style
}

// statusBlock returns the lines of the status block drawn, or none when
// there is nothing to say.
//
// The block is at most two lines, and one on a short screen (§3.4 hides the
// second line): a status that grows into the conversation costs the
// messages it is meant to explain.
//
// The origin is the column the first cell of the block is in: the block is
// drawn in the conversation header and in the header of the chat list, and
// the two are not in the same column of the screen. A cursor position of an
// emoji in it is absolute, and an absolute position counted from the first
// column of a pane that is not the first pane puts the letters after the
// emoji a pane to the left (columns.go).
func (m Model) statusBlock(layout Layout, width, origin int) []string {
	lines, parts := m.statusLines(layout, width)
	if len(lines) == 0 {
		return nil
	}

	// A line that fits is drawn part by part, so the presence keeps its own
	// green. A line that has to be wrapped is drawn in the block's own
	// colour for the whole of it: a wrapped line has no columns of its own
	// to keep a part in, and a colour that moves to the next row in the
	// middle of a sentence is a colour the reader has to work out rather
	// than one that tells them something.
	if len(lines) == 1 {
		return []string{m.styledStatusLine(parts, width, origin)}
	}

	style := m.statusStyle()
	for index, line := range lines {
		lines[index] = style.Render(line)
	}

	return lines
}

// statusBlockLines returns the text of the status block, one entry per row.
//
// It is the same block without the colours on it, which is what the budget
// of the timeline measures: a row is a row whatever colour it is, and both
// come from statusLines so that the two cannot disagree about how many rows
// the status is.
func (m Model) statusBlockLines(layout Layout, width int) []string {
	lines, _ := m.statusLines(layout, width)

	return lines
}

// statusLines returns the rows of the status block as text, and the parts
// of the line that fit into them.
func (m Model) statusLines(layout Layout, width int) ([]string, []statusPart) {
	parts := m.statusParts()
	if len(parts) == 0 || width < 1 {
		return nil, nil
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
	lines := m.widths.Wrap(joinWithSeparator(statusPartText(parts)), width, ellipsis)
	for len(lines) > limit && len(parts) > 1 {
		parts = parts[:len(parts)-1]
		lines = m.widths.Wrap(joinWithSeparator(statusPartText(parts)), width, ellipsis)
	}
	if len(lines) > limit {
		lines = lines[:limit]
	}
	for index, line := range lines {
		lines[index] = m.widths.Fit(line, width, ellipsis)
	}

	return lines, parts
}

// styledStatusLine draws one row of the status block: every part in its own
// colour and the separator of §3.3 between the parts in the block's own.
func (m Model) styledStatusLine(parts []statusPart, width, origin int) string {
	// The separator and the parts that have no colour of their own are in
	// the dim step of the text ramp, and not in the colour of the part that
	// ranks highest: a line whose separators are green is a line in green
	// with a word of it in a different green.
	own := m.styles().dimmed(m.tokens().SecondaryText)
	row := m.painterIn(origin, theme.Color{}).add(parts[0].style, parts[0].text)
	for _, part := range parts[1:] {
		row = row.add(own, statusSeparator).add(part.style, part.text)
	}

	return row.pad(width - m.widths.StringWidth(joinWithSeparator(statusPartText(parts)))).
		String()
}

// statusPartText returns what the parts say, as one string per part, which
// is how the width of the line is asked about: a line is measured by what
// it says, so the width is the width of the words and not of the escape
// sequences that colour them.
func statusPartText(parts []statusPart) []string {
	texts := make([]string, 0, len(parts))
	for _, part := range parts {
		texts = append(texts, part.text)
	}

	return texts
}

// maxStatusLines is how many lines the status block may take (§4.3).
const maxStatusLines = 2

// statusSeparator is what holds the parts of the status block together.
//
// It is the middle dot of §3.3, the same one the hint bar uses, so a user
// reads one way of saying "and also" on this screen.
const statusSeparator = " · "

// statusParts returns the parts of the status block in priority order, each
// with the colour it is drawn in.
func (m Model) statusParts() []statusPart {
	styles := m.styles()
	quiet := styles.dimmed(m.tokens().SecondaryText)
	var parts []statusPart

	// A notice is what an action just did, and it is the thing the user is
	// looking for: they pressed a key and the screen owes them an answer
	// before it owes them anything else.
	if m.notice != "" {
		return []statusPart{{
			text:  m.notice,
			style: styles.text(m.tokens().StatusActive),
		}}
	}

	// §11.1 puts the key and outbox failure above everything else, and
	// decision 3 names the word. It is a part of the line and not the whole
	// of it: the parts under it are still true while nothing can be sent,
	// and a line that said only "Sending paused" took the presence of a
	// person and the state of a connection off the screen because a
	// composer could not write (the owner, 30.09).
	if m.pausedErr != nil {
		parts = append(parts, statusPart{
			text:  sendingPausedHeadline,
			style: styles.text(m.tokens().StatusError),
		})
	}

	// The presence comes first because it is the only part of the line
	// that is about a person rather than about the program, and a user
	// reading a header wants to know who is there before they want to know
	// how the queue is doing. It is also the part that is dropped last
	// when the line does not fit: the queue can be read again in two
	// seconds, and a person cannot.
	//
	// It is in green, as the mockup of the owner draws it: a presence is a
	// fact about somebody, and the status vocabulary of the theme says a
	// status is a colour that repeats the word rather than one that
	// carries it.
	if presence := presenceText(
		m.summary.Presence,
		m.clock()(),
		m.timeZone(),
	); presence != "" {
		parts = append(parts, statusPart{
			text:  presence,
			style: styles.text(m.tokens().StatusSuccess),
		})
	}

	if connection := connectionStatusText(m.summary.Connection); connection != "" {
		style := quiet
		// A connection that is not ready is the one part of the line the
		// user can do something about, and the theme has a colour that
		// says "waited for" rather than "broken".
		if m.summary.Connection != ConnectionReady {
			style = styles.text(m.tokens().StatusWarning)
		}

		parts = append(parts, statusPart{text: connection, style: style})
	}

	queue := m.summary.Queue
	if !queue.Known {
		return parts
	}

	// The counts of a queue nothing can be queued into would be a lie about
	// that queue, so they go while sending is paused (decision 3). The
	// presence and the connection stay: they are not counts.
	if m.pausedErr != nil {
		return parts
	}

	if queue.Recovering {
		parts = append(parts, statusPart{
			text:  recoveringMessagesText,
			style: styles.text(m.tokens().StatusActive),
		})
	}
	if queue.Queued > 0 {
		parts = append(parts, statusPart{
			text:  strconv.Itoa(queue.Queued) + " queued",
			style: quiet,
		})
	}
	if queue.Retrying > 0 {
		parts = append(parts, statusPart{
			text:  strconv.Itoa(queue.Retrying) + " retrying",
			style: quiet,
		})
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
		return "connected"
	case ConnectionConnecting:
		return "connecting…"
	case ConnectionUpdating:
		return "updating…"
	case ConnectionWaitingForNetwork:
		return "waiting for network"
	case ConnectionConnectingToProxy:
		return "connecting to proxy…"
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

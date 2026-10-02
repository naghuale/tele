package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"telecli/internal/tui/theme"
)

// The status line: what the interface says about the connection and about
// the delivery queue, in the place §4.3 puts it.
//
// The parts are independent on purpose. A queue that cannot be read is not
// a reason to stop saying whether the client is connected, and a client
// that is reconnecting is not a reason to stop counting what is queued.

func TestTheStatusLineSaysConnectedWithTheQueueCounts(t *testing.T) {
	for _, profile := range profiles() {
		t.Run(profileName(profile), func(t *testing.T) {
			model := modelWithSummary(t, profile, 100, 24, StatusSummary{
				Connection: ConnectionReady,
				Queue:      QueueSummary{Known: true, Queued: 2, Retrying: 1},
			})

			line := statusLineOf(t, model)
			if line != "connected · 2 queued · 1 retrying" {
				t.Fatalf("status line = %q", line)
			}
			if !strings.Contains(plain(model.View()), line) {
				t.Fatal("the status line is not on the screen")
			}
		})
	}
}

func TestTheStatusLineNamesEveryConnectionState(t *testing.T) {
	for state, want := range map[ConnectionState]string{
		ConnectionReady:             "connected",
		ConnectionConnecting:        "connecting…",
		ConnectionUpdating:          "updating…",
		ConnectionWaitingForNetwork: "waiting for network",
		ConnectionConnectingToProxy: "connecting to proxy…",
	} {
		t.Run(want, func(t *testing.T) {
			model := modelWithSummary(t, theme.ProfileNoColor, 100, 24, StatusSummary{
				Connection: state,
				Queue:      QueueSummary{Known: true},
			})

			if got := statusLineOf(t, model); got != want {
				t.Fatalf("status line = %q, want %q", got, want)
			}
		})
	}
}

// A client that has not said anything is not a client that is offline and
// not a client that is online, so the line says nothing about it. A
// status line that guesses is a status line that lies.
func TestAnUnknownConnectionIsNotNamed(t *testing.T) {
	withCounts := modelWithSummary(t, theme.ProfileNoColor, 100, 24, StatusSummary{
		Connection: ConnectionUnknown,
		Queue:      QueueSummary{Known: true, Queued: 3},
	})
	if got := statusLineOf(t, withCounts); got != "3 queued" {
		t.Fatalf("status line = %q, want the queue part alone", got)
	}

	empty := modelWithSummary(t, theme.ProfileNoColor, 100, 24, StatusSummary{
		Connection: ConnectionUnknown,
		Queue:      QueueSummary{Known: true},
	})
	if got := statusLinesOf(t, empty); len(got) != 0 {
		t.Fatalf("status lines = %q, want none", got)
	}
}

// The counts are for a queue that has something in it. "0 queued" on every
// screen is noise that teaches a user to stop reading the line.
func TestAnEmptyQueueIsNotCounted(t *testing.T) {
	model := modelWithSummary(t, theme.ProfileNoColor, 100, 24, StatusSummary{
		Connection: ConnectionReady,
		Queue:      QueueSummary{Known: true},
	})

	if got := statusLineOf(t, model); got != "connected" {
		t.Fatalf("status line = %q, want connected alone", got)
	}
}

func TestRecoveringInterruptedMessagesIsSaid(t *testing.T) {
	recovering := modelWithSummary(t, theme.ProfileNoColor, 100, 24, StatusSummary{
		Connection: ConnectionReady,
		Queue:      QueueSummary{Known: true, Recovering: true, Queued: 4},
	})
	if got := statusLineOf(t, recovering); got != "connected · Recovering interrupted messages… · 4 queued" {
		t.Fatalf("status line = %q", got)
	}

	// §11.1 ranks the connection above recovery: a user who is not
	// connected is told that first, and recovery is still said after it.
	offline := modelWithSummary(t, theme.ProfileNoColor, 100, 24, StatusSummary{
		Connection: ConnectionWaitingForNetwork,
		Queue:      QueueSummary{Known: true, Recovering: true},
	})
	if got := statusLineOf(t, offline); got != "waiting for network · Recovering interrupted messages…" {
		t.Fatalf("status line = %q", got)
	}
}

// §11.1 puts a key or queue failure above everything else, and decision 3
// names the word: the queue counts of a queue nothing can be queued into
// are a lie about a queue.
func TestSendingPausedComesFirst(t *testing.T) {
	model := modelWithSummary(t, theme.ProfileNoColor, 100, 24, StatusSummary{
		Connection: ConnectionReady,
		Queue:      QueueSummary{Known: true, Queued: 7, Retrying: 2},
	})
	model = pausedModel(t, model, theme.ProfileNoColor)

	lines := statusLinesOf(t, model)
	if len(lines) == 0 {
		t.Fatal("a paused composer says so in the status line")
	}
	if lines[0] != "Sending paused" {
		t.Fatalf("status line = %q, want the paused word first", lines[0])
	}
	for _, line := range lines {
		if strings.Contains(line, "queued") {
			t.Fatalf("status line %q counts a queue that cannot be used", line)
		}
	}
}

func TestAQueueThatCannotBeReadIsNotAnEmptyQueue(t *testing.T) {
	summary := StatusSummary{
		Connection: ConnectionReady,
		Queue:      QueueSummary{Known: true, Queued: 5, Retrying: 1},
	}
	source := &summarySource{summary: summary}
	model := modelWithSummarySource(t, theme.ProfileNoColor, 100, 24, source, summary)
	model, _ = updateModel(t, model, model.statusRefresh(t, source))

	source.err = errors.New("the outbox store is closed")
	model, _ = updateModel(t, model, model.statusRefresh(t, source))

	// The read failed, so the counts are gone rather than stale, and the
	// connection is still said: §11.1 ranks them apart and §4.3 says an
	// error of one part must not take the others with it.
	if got := statusLineOf(t, model); got != "connected" {
		t.Fatalf("status line = %q, want the connection alone", got)
	}
}

// §11.3: the status block is where a TDLib error message, a phone number
// and a message body must never be. The fixture strings below are the
// ones the interface is not allowed to show.
func TestTheStatusLineCarriesNoSecretAndNoMessageText(t *testing.T) {
	forbidden := []string{
		"+1 555 0100 secret phone",
		"api_hash 0123456789abcdef",
		"ERROR: the phone number is invalid",
		"текст сообщения пользователя",
	}

	source := &summarySource{}
	model := modelWithSummarySource(t, theme.ProfileNoColor, 100, 24, source, StatusSummary{
		Connection: ConnectionReady,
		Queue:      QueueSummary{Known: true, Queued: 1},
	})
	source.err = errors.New(
		"read status: ERROR 400: " + forbidden[0] + " " + forbidden[1] +
			" " + forbidden[2] + " " + forbidden[3],
	)
	model, _ = updateModel(t, model, model.statusRefresh(t, source))

	view := plain(model.View())
	for _, secret := range forbidden {
		if strings.Contains(view, secret) {
			t.Fatalf("the screen carries %q", secret)
		}
	}
}

// §4.3: no more than two lines, and §3.4: one of them goes on a short
// screen. A status block that grows into the conversation is a status
// block that costs the messages.
func TestTheStatusBlockIsTwoLinesAtMost(t *testing.T) {
	const width = 46

	model := modelWithSummary(t, theme.ProfileNoColor, width, 24, StatusSummary{
		Connection: ConnectionWaitingForNetwork,
		Queue:      QueueSummary{Known: true, Recovering: true, Queued: 12, Retrying: 11},
	})

	lines := statusLinesOf(t, model)
	if len(lines) != 2 {
		t.Fatalf("status lines = %d (%q), want two", len(lines), lines)
	}
	for _, line := range lines {
		if model.widths.StringWidth(line) > width {
			t.Fatalf("status line %q is %d columns wide, want at most %d", line, model.widths.StringWidth(line), width)
		}
	}
}

// isWholeStatusLine reports whether a status line is one of the joins of
// the parts, which is the only shape a line may have.
func isWholeStatusLine(line string) bool {
	for _, want := range []string{
		"waiting for network",
		"waiting for network · Recovering interrupted messages…",
		"Waiting for network · Recovering interrupted messages… · 12 queued",
		"Waiting for network · Recovering interrupted messages… · 12 queued · 11 retrying",
	} {
		if line == want {
			return true
		}
	}

	return false
}

func TestAShortScreenKeepsOneStatusLine(t *testing.T) {
	tall := modelWithSummary(t, theme.ProfileNoColor, 46, 24, StatusSummary{
		Connection: ConnectionWaitingForNetwork,
		Queue:      QueueSummary{Known: true, Recovering: true, Queued: 12, Retrying: 11},
	})
	short := modelWithSummary(t, theme.ProfileNoColor, 46, 12, StatusSummary{
		Connection: ConnectionWaitingForNetwork,
		Queue:      QueueSummary{Known: true, Recovering: true, Queued: 12, Retrying: 11},
	})

	if len(statusLinesOf(t, tall)) < 2 {
		t.Fatal("a tall screen shows the whole status block")
	}
	shortLines := statusLinesOf(t, short)
	if len(shortLines) != 1 {
		t.Fatalf("status lines on a short screen = %d, want 1", len(shortLines))
	}
	// What survives is a whole part or several of them joined, never half a
	// sentence: a cut status says less than a shorter one.
	if !isWholeStatusLine(shortLines[0]) {
		t.Fatalf(
			"the short status line %q is cut in the middle of a part",
			shortLines[0],
		)
	}
}

// §4.3 puts the status under the title of the conversation, so a user
// reads it as part of the header and not as the last line of the
// conversation.
func TestTheStatusIsUnderTheConversationTitle(t *testing.T) {
	model := modelWithSummary(t, theme.ProfileNoColor, 100, 24, StatusSummary{
		Connection: ConnectionReady,
		Queue:      QueueSummary{Known: true, Queued: 1},
	})

	lines := viewLines(plain(model.View()))
	title := indexOfLineWith(lines, "A")
	status := indexOfLineWith(lines, "connected · 1 queued")
	if title < 0 || status < 0 {
		t.Fatalf("the conversation title or the status line is missing: %q", lines)
	}

	// The status is on the row under the title, with nothing between them
	// (the mockup of the owner, 30.09: the name of the chat and then what
	// the program is doing in it), and the rule that says the timeline has
	// the keys closes the header under the two of them.
	if status != title+1 {
		t.Fatalf("the status is on line %d and the title on %d", status, title)
	}
}

// §3.3: on a narrow screen there is no conversation beside the list, so
// the list header carries the status. On a wide screen the list header
// keeps the unread count and the status belongs to the conversation.
func TestTheNarrowChatListHeaderCarriesTheStatus(t *testing.T) {
	summary := StatusSummary{
		Connection: ConnectionReady,
		Queue:      QueueSummary{Known: true, Queued: 2},
	}

	narrow := modelWithSummary(t, theme.ProfileNoColor, 60, 24, summary)
	if got := statusLineOf(t, narrow); got != "connected · 2 queued" {
		t.Fatalf("status line = %q", got)
	}
	narrowHeader := chatListHeaderLines(t, narrow)
	if !strings.Contains(strings.Join(narrowHeader, " "), "connected · 2 queued") {
		t.Fatalf("the narrow chat list header = %q", narrowHeader)
	}

	wide := modelWithSummary(t, theme.ProfileNoColor, 120, 24, summary)
	if got := chatListHeaderLines(t, wide); !strings.Contains(strings.Join(got, " "), "unread") {
		t.Fatalf("the wide chat list header = %q, want the unread count", got)
	}
}

// An empty conversation pane is still the place the status belongs: it is
// the pane the user is looking at when no chat is open, and on a two-pane
// screen it is the only place the status is drawn.
func TestTheStatusIsSaidBesideAnUnopenedConversation(t *testing.T) {
	for width, profile := range map[int]theme.Profile{
		60:  theme.ProfileNoColor,
		120: theme.ProfileTrueColor,
	} {
		model := modelWithSummary(t, profile, width, 24, StatusSummary{
			Connection: ConnectionConnecting,
			Queue:      QueueSummary{Known: true, Queued: 2},
		})
		// Nothing is opened: the chat list is the screen, and the
		// conversation pane is waiting for a choice.
		model, _ = updateModel(t, model, press(tea.KeyEsc))

		view := plain(model.View())
		if !strings.Contains(view, "connecting… · 2 queued") {
			t.Fatalf(
				"the empty pane at %d columns does not carry the status: %q",
				width,
				viewLines(view),
			)
		}
	}
}

// Without a source there is nothing to say, and a line that says nothing
// must not take a row of the conversation.
func TestNoStatusSourceMeansNoStatusLine(t *testing.T) {
	model := conversationWithPending(t, theme.ProfileNoColor, 100, 24, nil)
	if got := statusLinesOf(t, model); len(got) != 0 {
		t.Fatalf("status lines = %q, want none", got)
	}
}

// ---- helpers ----

func profiles() []theme.Profile {
	return []theme.Profile{theme.ProfileNoColor, theme.ProfileTrueColor}
}

func profileName(profile theme.Profile) string {
	if profile == theme.ProfileTrueColor {
		return "true color"
	}

	return "no color"
}

// summarySource answers with whatever a test puts in it, and records which
// chat it was asked about so that a test can check that the presence of a
// closed chat is not read.
type summarySource struct {
	summary   StatusSummary
	err       error
	calls     int
	requested []int64
}

func (s *summarySource) ReadStatusSummary(
	_ context.Context,
	chatID int64,
) (StatusSummary, error) {
	s.calls++
	s.requested = append(s.requested, chatID)

	if s.err != nil {
		return StatusSummary{}, s.err
	}
	if chatID == 0 {
		summary := s.summary
		summary.Presence = Presence{}

		return summary, nil
	}

	return s.summary, nil
}

// modelWithSummarySource is a conversation open with a status source
// wired, before any read has been delivered.
func modelWithSummarySource(
	t *testing.T,
	profile theme.Profile,
	width int,
	height int,
	source StatusSummarySource,
	summary StatusSummary,
) Model {
	t.Helper()

	return modelWithSummaryAndPaused(t, profile, width, height, source, summary, nil)
}

// modelWithSummaryAndPaused is the same model with the composition root's
// paused state, which is the only way a user ever sees it: telecli starts
// the interface with sending already paused rather than letting them find
// out.
func modelWithSummaryAndPaused(
	t *testing.T,
	profile theme.Profile,
	width int,
	height int,
	source StatusSummarySource,
	summary StatusSummary,
	sendError error,
) Model {
	t.Helper()

	model, err := NewModelWithDependencies(context.Background(), Dependencies{
		Source:           &fakeChatSource{},
		MessageSubmitter: &recordingSubmitter{},
		AccountKey:       "account-1",
		StatusSummaries:  source,
		LiveUpdates:      newFakeLiveSource(),
		SendError:        sendError,
		Theme:            theme.DefaultTheme().ForProfile(profile),
		ColorProfile:     profile,
	})
	if err != nil {
		t.Fatalf("NewModelWithDependencies: %v", err)
	}

	// The model reads the clock and the zone through two fields that are
	// the machine's clock and the machine's zone until something pins
	// them, so everything that goes through this builder starts pinned.
	// A status line says when somebody was last there and when a message
	// will be tried again, and both are moments: a test that lets them be
	// the machine's is a test that changes its answer at 16:00 UTC.
	model = withClock(model, testClock, time.UTC)

	model, _ = updateModel(t, model, tea.WindowSizeMsg{Width: width, Height: height})
	model, _ = updateModel(t, model, chatsLoadedMsg{chats: []Chat{{ID: 7, Title: "A"}}})
	model, _ = updateModel(t, model, press(tea.KeyEnter))

	return model
}

// pausedModel is a model with a delivery that cannot work, as decision 3
// words it.
func pausedModel(
	t *testing.T,
	model Model,
	profile theme.Profile,
) Model {
	t.Helper()

	paused, err := NewModelWithDependencies(context.Background(), Dependencies{
		Source:           &fakeChatSource{},
		MessageSubmitter: &recordingSubmitter{},
		AccountKey:       "account-1",
		LiveUpdates:      newFakeLiveSource(),
		SendError:        errSendingPausedFixture,
		Theme:            theme.DefaultTheme().ForProfile(profile),
		ColorProfile:     profile,
	})
	if err != nil {
		t.Fatalf("NewModelWithDependencies: %v", err)
	}

	paused, _ = updateModel(t, paused, tea.WindowSizeMsg{
		Width:  model.width,
		Height: model.height,
	})
	paused, _ = updateModel(t, paused, chatsLoadedMsg{chats: model.chats})
	paused, _ = updateModel(t, paused, press(tea.KeyEnter))

	return paused
}

// modelWithSummary is a conversation with one status read delivered.
func modelWithSummary(
	t *testing.T,
	profile theme.Profile,
	width int,
	height int,
	summary StatusSummary,
) Model {
	t.Helper()

	source := &summarySource{summary: summary}
	model := modelWithSummarySource(t, profile, width, height, source, summary)
	model, _ = updateModel(t, model, model.statusRefresh(t, source))

	return model
}

// statusRefresh delivers one status summary read.
func (m Model) statusRefresh(t *testing.T, source StatusSummarySource) tea.Msg {
	t.Helper()

	msg := statusSummaryLoadedMsg{generation: m.messageStatusGeneration}
	summary, err := source.ReadStatusSummary(
		context.Background(),
		m.messageStatusChatID,
	)
	if err != nil {
		return statusSummaryFailedMsg{generation: m.messageStatusGeneration, err: err}
	}
	msg.summary = summary

	return msg
}

// statusLinesOf returns the text of the status block, one entry per row,
// without the padding the region adds around it.
func statusLinesOf(t *testing.T, model Model) []string {
	t.Helper()

	layout := LayoutFor(model.width, model.height)

	lines := model.statusBlockLines(layout, layout.ChatContentWidth())
	for index, line := range lines {
		lines[index] = strings.TrimRight(line, " ")
	}

	return lines
}

// chatListHeaderLines returns the first two lines of the chat list, which
// is its header (§4.1).
func chatListHeaderLines(t *testing.T, model Model) []string {
	t.Helper()

	layout := LayoutFor(model.width, model.height)
	width := layout.SidebarContentWidth()
	if !layout.TwoPane() {
		width = layout.FullContentWidth()
	}
	lines := model.chatListLines(layout, width, layout.Height)

	if len(lines) < chatListHeaderRows {
		return lines
	}

	// The header is the title, the rule that says this pane has the keys
	// and the second line under them. The rule is not one of them: it says
	// where the focus is and not what the list is about.
	return lines[:chatListHeaderRows]
}

// chatListHeaderRows is how many rows the header of the chat list takes
// before the first chat: the title, the rule and the second line.
const chatListHeaderRows = 3

func statusLineOf(t *testing.T, model Model) string {
	t.Helper()

	lines := statusLinesOf(t, model)
	if len(lines) == 0 {
		return ""
	}

	return lines[0]
}

func indexOfLineWith(lines []string, text string) int {
	for index, line := range lines {
		if strings.Contains(line, text) {
			return index
		}
	}

	return -1
}

// §18: a client that is off the network does not fail the queue. The
// entries keep their states, the counts keep being counted, and what the
// status line says about the connection is a separate sentence from what it
// says about the queue.
func TestAReconnectingClientKeepsTheQueueStates(t *testing.T) {
	at := time.Date(2026, 9, 28, 14, 35, 0, 0, time.UTC)
	source := &pendingSource{messages: []PendingMessage{{
		EntryID:       "entry-1",
		ChatID:        7,
		Text:          "текст сообщения",
		State:         MessageDeliveryRetrying,
		NextAttemptAt: at,
		CreatedAt:     at,
	}}}

	summary := &summarySource{summary: StatusSummary{
		Connection: ConnectionWaitingForNetwork,
		Queue:      QueueSummary{Known: true, Queued: 2, Retrying: 1},
	}}
	model := modelWithSummarySource(t, theme.ProfileNoColor, 100, 24, summary, summary.summary)
	model, _ = updateModel(t, model, model.statusRefresh(t, summary))
	model, _ = updateModel(t, model, deliveryRefresh(t, model, source))

	view := plain(model.View())
	if !strings.Contains(view, "текст сообщения") {
		t.Fatal("the message is not on the screen")
	}
	if !strings.Contains(view, "retrying at 14:35") {
		t.Fatalf("the message lost its delivery state: %q", viewLines(view))
	}
	if got := statusLineOf(t, model); got != "waiting for network · 2 queued · 1 retrying" {
		t.Fatalf("status line = %q", got)
	}
}

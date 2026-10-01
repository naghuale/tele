package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// This file is the composer of a chat Telegram refuses: the line that stands
// where the field was, the keys that do nothing there, and the read that
// brings the field back when the rights change.

// stubChatAccess answers with what a test says it answers with.
type stubChatAccess struct {
	access ChatAccess
	err    error
	calls  int
	lastID int64
}

func (s *stubChatAccess) ReadChatAccess(
	_ context.Context,
	chatID int64,
) (ChatAccess, error) {
	s.calls++
	s.lastID = chatID

	if s.err != nil {
		return ChatAccess{}, s.err
	}

	return s.access, nil
}

// readOnlyModel is a conversation in a chat this account cannot write in,
// reached the way a user reaches it: the chat is opened and the read lands.
func readOnlyModel(
	t *testing.T,
	submitter ComposerSubmitter,
	blocked ChatBlocked,
) Model {
	t.Helper()

	m, err := NewModelWithDependencies(context.Background(), Dependencies{
		Source:           &fakeChatSource{},
		MessageSubmitter: submitter,
		ChatAccess:       &stubChatAccess{access: ChatAccess{Blocked: blocked}},
	})
	if err != nil {
		t.Fatalf("NewModelWithDependencies: %v", err)
	}

	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 24})
	m, _ = updateModel(t, m, chatsLoadedMsg{
		chats: []Chat{{ID: 7, Title: "Release Notes", Kind: ChatKindChannel}},
	})
	m, _ = updateModel(t, m, press(tea.KeyEnter))
	m, _ = updateModel(t, m, historyLoadedMsg{
		chatID:    7,
		operation: m.historyOperation,
		page:      HistoryPage{},
	})
	m, _ = updateModel(t, m, chatAccessLoadedMsg{
		chatID: 7,
		access: ChatAccess{Blocked: blocked},
	})

	return m
}

// TestAChatThatCannotBeWrittenInHasNoField is the whole of it: the region
// under the messages is one line, and it says which chat it is.
func TestAChatThatCannotBeWrittenInHasNoField(t *testing.T) {
	m := readOnlyModel(t, &recordingSubmitter{}, ChatBlockedChannel)

	view := m.View()
	if !strings.Contains(view, readOnlyChannelLine) {
		t.Fatalf(
			"the screen does not say %q:\n%s", readOnlyChannelLine, view,
		)
	}
	if strings.Contains(view, composerPlaceholder) {
		t.Fatalf("the screen still draws the composer field:\n%s", view)
	}
	if strings.Contains(view, cursorBar) {
		t.Fatalf(
			"the screen draws a cursor in a place that ignores keys:\n%s", view,
		)
	}
}

// TestAChatThatCannotBeWrittenInSaysWhy walks the reasons, so that a
// read-only line is never the same two words for five different facts.
func TestAChatThatCannotBeWrittenInSaysWhy(t *testing.T) {
	cases := map[ChatBlocked]string{
		ChatBlockedChannel:   readOnlyChannelLine,
		ChatBlockedGroup:     "sending is off for you",
		ChatBlockedPeer:      "this account is no longer reachable",
		ChatBlockedNotMember: "you are not a member",
		ChatBlockedBanned:    "you are banned",
	}

	for blocked, words := range cases {
		t.Run(string(blocked), func(t *testing.T) {
			view := readOnlyModel(
				t, &recordingSubmitter{}, blocked,
			).View()
			if !strings.Contains(view, words) {
				t.Errorf(
					"the screen does not say %q:\n%s", words, view,
				)
			}
		})
	}
}

// TestAChatThatCannotBeWrittenInSendsNothing is the defect of 01.10: two
// messages went into a channel that refuses them and both of them stayed on
// the screen with `! failed` under them.
func TestAChatThatCannotBeWrittenInSendsNothing(t *testing.T) {
	submitter := &recordingSubmitter{}
	m := readOnlyModel(t, submitter, ChatBlockedChannel)

	// A draft typed before the rights were read is still a draft, and
	// pressing Enter on it starts nothing.
	m, _ = updateModel(t, m, pressRunes("hello"))
	_, cmd := updateModel(t, m, press(tea.KeyEnter))

	for _, msg := range runBatch(t, cmd) {
		if _, isSubmission := msg.(composerSubmissionMsg); isSubmission {
			t.Fatal("Enter produced a submission in a chat that refuses it")
		}
		if _, isSend := msg.(messageSentMsg); isSend {
			t.Fatal("Enter produced a send in a chat that refuses it")
		}
	}
	if submitter.calls != 0 {
		t.Fatalf(
			"the submitter was called %d times, want none: Telegram refuses "+
				"this send and the message would sit in the feed as failed",
			submitter.calls,
		)
	}
	if m.sendState == sendStateSending {
		t.Fatal("a send is in flight in a chat that cannot be written in")
	}
}

// TestAChatThatCannotBeWrittenInIsNotTypedIn covers the rest of the keys: a
// composer that refuses Enter but takes letters is a place a message can be
// lost in.
func TestAChatThatCannotBeWrittenInIsNotTypedIn(t *testing.T) {
	m := readOnlyModel(t, &recordingSubmitter{}, ChatBlockedChannel)

	m, _ = updateModel(t, m, pressRunes("a longer text"))
	m, _ = updateModel(t, m, press(tea.KeyBackspace))
	m, _ = updateModel(t, m, press(tea.KeyTab))

	if draft := string(m.Composer()); draft != "" {
		t.Fatalf("the draft is %q, want it empty", draft)
	}
}

// TestTheKeysOfAChatThatCannotBeWrittenInAreTheKeysOfTheMessages is §4.6
// asking for the keys of the focus: a bar that promises "Enter send" under a
// line is the same defect as the field would have been.
func TestTheKeysOfAChatThatCannotBeWrittenInAreTheKeysOfTheMessages(t *testing.T) {
	m := readOnlyModel(t, &recordingSubmitter{}, ChatBlockedChannel)
	m.focus = FocusComposer

	hint := m.hintText(LayoutFor(100, 24))
	if strings.Contains(hint, "Enter send") {
		t.Fatalf("the hint promises a send: %q", hint)
	}
	if hint != m.timelineHint(LayoutFor(100, 24)) {
		t.Fatalf(
			"the hint is %q, want the keys of the timeline", hint,
		)
	}
}

// TestAComposerThatCannotBeTypedInIsNotAFocusTarget is §5: exactly one
// region is focused, and it is a region with keys in it.
func TestAComposerThatCannotBeTypedInIsNotAFocusTarget(t *testing.T) {
	m := readOnlyModel(t, &recordingSubmitter{}, ChatBlockedChannel)

	regions := m.visibleFocusRegions()
	for _, region := range regions {
		if region == FocusComposer {
			t.Fatalf(
				"Tab visits the composer of a chat that cannot be written "+
					"in: %v", regions,
			)
		}
	}

	// Enter from the timeline is the other way in, and it does nothing.
	m.focus = FocusHistory
	m, _ = updateModel(t, m, press(tea.KeyEnter))
	if m.focus != FocusHistory {
		t.Fatalf("focus = %v, want the timeline", m.focus)
	}
}

// TestAFieldComesBackWhenTheRightsAreRestored is the promise the other way
// round: somebody is made an administrator of the channel, and the field is
// there within a poll cycle with no restart.
func TestAFieldComesBackWhenTheRightsAreRestored(t *testing.T) {
	submitter := &recordingSubmitter{}
	m := readOnlyModel(t, submitter, ChatBlockedChannel)

	if strings.Contains(m.View(), composerPlaceholder) {
		t.Fatalf("the field is already there before the rights changed:\n%s",
			m.View())
	}

	m, _ = updateModel(t, m, chatAccessLoadedMsg{
		chatID: 7,
		access: ChatAccess{CanSend: true},
	})

	view := m.View()
	if !strings.Contains(view, composerPlaceholder) {
		t.Fatalf("the field did not come back:\n%s", view)
	}

	m.focus = FocusComposer
	m, _ = updateModel(t, m, pressRunes("now it goes"))
	_, cmd := updateModel(t, m, press(tea.KeyEnter))
	runBatch(t, cmd)

	if submitter.calls != 1 {
		t.Fatalf(
			"the submitter was called %d times, want one: the field is back",
			submitter.calls,
		)
	}
}

// TestTheDraftOfAChatThatRefusedItIsNotThrownAway: the text is not drawn
// while the chat cannot be written in, and it is still there when it can.
func TestTheDraftOfAChatThatRefusedItIsNotThrownAway(t *testing.T) {
	m := readOnlyModel(t, &recordingSubmitter{}, ChatBlockedChannel)
	m.composer = []rune(snapshotDraft)
	m.composerCursor = len(m.composer)

	m, _ = updateModel(t, m, chatAccessLoadedMsg{
		chatID: 7,
		access: ChatAccess{CanSend: true},
	})

	if draft := string(m.Composer()); draft != snapshotDraft {
		t.Fatalf("the draft is %q, want %q", draft, snapshotDraft)
	}
}

// TestAReadThatFailedLeavesTheComposerAlone: a query that timed out is a
// fact about the network, and a line about the network in the place where
// the chat is would be a wrong claim about the chat.
func TestAReadThatFailedLeavesTheComposerAlone(t *testing.T) {
	m := readOnlyModel(t, &recordingSubmitter{}, ChatBlockedChannel)

	m, _ = updateModel(t, m, chatAccessFailedMsg{
		chatID: 7,
		err:    errors.New("getChat: context deadline exceeded"),
	})

	if m.chatAccess.Blocked != ChatBlockedChannel {
		t.Fatal("a failed read changed what is known about the chat")
	}
}

// TestAReadThatFailedFirstLeavesTheFieldAlone is the same rule for a chat
// that has never been read: nothing is known, and nothing is drawn.
func TestAReadThatFailedFirstLeavesTheFieldAlone(t *testing.T) {
	m := modelWithChatAccess(t, &stubChatAccess{
		err: errors.New("getChat: context deadline exceeded"),
	})

	var opened tea.Cmd
	m, opened = updateModel(t, m, press(tea.KeyEnter))

	failure, reported := chatAccessFailedMsg{}, false
	for _, msg := range runBatch(t, opened) {
		if got, isFailed := msg.(chatAccessFailedMsg); isFailed {
			failure, reported = got, true
		}
	}
	if !reported {
		t.Fatal("a source that could not answer produced no report at all")
	}

	m, _ = updateModel(t, m, failure)

	if m.chatAccessKnown {
		t.Fatal("a read that failed decided the chat")
	}
	if !strings.Contains(m.View(), composerPlaceholder) {
		t.Fatalf("the composer is gone after a read that failed:\n%s", m.View())
	}
}

// TestAnAnswerAboutAnotherChatIsDropped: the user walked into the next chat
// while the read was on its way.
func TestAnAnswerAboutAnotherChatIsDropped(t *testing.T) {
	m := readOnlyModel(t, &recordingSubmitter{}, ChatBlockedChannel)

	m, _ = updateModel(t, m, chatAccessLoadedMsg{
		chatID: 99,
		access: ChatAccess{CanSend: true},
	})

	if m.canWrite() {
		t.Fatal("an answer about another chat was applied to this one")
	}
}

// TestAChangeOfRightsArrivesOnThePollWithoutARestart is the whole of the
// live half: a channel this account does not post in, a promotion while the
// program is running, and the field on the screen within a poll cycle.
func TestAChangeOfRightsArrivesOnThePollWithoutARestart(t *testing.T) {
	source := &stubChatAccess{access: ChatAccess{Blocked: ChatBlockedChannel}}
	m := modelWithChatAccess(t, source)

	var opened tea.Cmd
	m, opened = updateModel(t, m, press(tea.KeyEnter))

	loaded := theRightsAnswer(t, runBatch(t, opened))
	if loaded.chatID != 7 {
		t.Fatalf("the read answered about chat %d, want 7", loaded.chatID)
	}
	if source.lastID != 7 {
		t.Fatalf("the read asked about chat %d, want 7", source.lastID)
	}

	m, _ = updateModel(t, m, loaded)
	if !strings.Contains(m.View(), readOnlyChannelLine) {
		t.Fatalf("the channel has a field:\n%s", m.View())
	}

	// Somebody was made an administrator of the channel, the next read says
	// so, and the field is there.
	source.access = ChatAccess{CanSend: true}
	m, _ = updateModel(t, m, theRightsAnswer(t, runCmds(t, pollReads(t, m))))

	if !strings.Contains(m.View(), composerPlaceholder) {
		t.Fatalf("the field did not come back on the poll:\n%s", m.View())
	}
}

// TestTheRightsAreReadForTheChatThatIsOpen walks the rest of the read: the
// poll asks again, a read that changed nothing delivers nothing, and a
// conversation that was left is not read for.
func TestTheRightsAreReadForTheChatThatIsOpen(t *testing.T) {
	source := &stubChatAccess{access: ChatAccess{CanSend: true}}
	m := modelWithChatAccess(t, source)

	var opened tea.Cmd
	m, opened = updateModel(t, m, press(tea.KeyEnter))
	m, _ = updateModel(t, m, theRightsAnswer(t, runBatch(t, opened)))

	if source.calls != 1 {
		t.Fatalf("the read was asked %d times on opening, want one", source.calls)
	}

	reads := pollReads(t, m)
	if len(reads) != 1 {
		t.Fatalf("the poll has %d reads, want one", len(reads))
	}
	if msgs := runCmds(t, reads); len(msgs) != 0 {
		t.Fatalf(
			"a read that changed nothing delivered %d messages, want none: "+
				"a poll that redraws the same screen burns a battery",
			len(msgs),
		)
	}
	if source.calls != 2 {
		t.Fatalf(
			"the read was asked %d times, want two: the poll asks again",
			source.calls,
		)
	}

	m, _ = m.leaveConversation()
	runCmds(t, pollReads(t, m))
	if source.calls != 2 {
		t.Fatalf(
			"the read was asked %d times, want no read outside a conversation",
			source.calls,
		)
	}
}

// modelWithChatAccess is a program with a list of one channel and a source
// of the rights of the chat that is opened.
func modelWithChatAccess(t *testing.T, source ChatAccessSource) Model {
	t.Helper()

	m, err := NewModelWithDependencies(context.Background(), Dependencies{
		Source:           &fakeChatSource{},
		MessageSubmitter: &recordingSubmitter{},
		ChatAccess:       source,
	})
	if err != nil {
		t.Fatalf("NewModelWithDependencies: %v", err)
	}

	m, _ = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 24})
	m, _ = updateModel(t, m, chatsLoadedMsg{
		chats: []Chat{{ID: 7, Title: "Release Notes", Kind: ChatKindChannel}},
	})

	return m
}

// pollReads returns the read commands of a poll without the tick that goes
// with them.
//
// The poll builds its reads first and its next tick last, and the tick is a
// two-second timer: a test that ran it would wait for a timer to learn
// nothing about the read beside it.
func pollReads(t *testing.T, m Model) []tea.Cmd {
	t.Helper()

	poll := m.pollDeliverySources()
	if poll == nil {
		return nil
	}

	answer := poll()
	batch, isBatch := answer.(tea.BatchMsg)
	if !isBatch {
		return []tea.Cmd{poll}
	}
	if len(batch) == 0 {
		return nil
	}

	return batch[:len(batch)-1]
}

// runBatch runs a command and every command inside it, and returns the
// messages they produced.
//
// A read of a source is a command: the source is asked when the command
// runs, not when it is built, so a test that wants to see what a read asked
// has to run it. A batch runs to nothing but its own list, so every command
// inside it is run exactly once here.
func runBatch(t *testing.T, cmd tea.Cmd) []tea.Msg {
	t.Helper()

	if cmd == nil {
		return nil
	}

	answer := cmd()
	if answer == nil {
		return nil
	}

	batch, isBatch := answer.(tea.BatchMsg)
	if !isBatch {
		return []tea.Msg{answer}
	}

	var out []tea.Msg
	for _, one := range batch {
		out = append(out, runBatch(t, one)...)
	}

	return out
}

func runCmds(t *testing.T, cmds []tea.Cmd) []tea.Msg {
	t.Helper()

	var out []tea.Msg
	for _, one := range cmds {
		if msg := one(); msg != nil {
			out = append(out, msg)
		}
	}

	return out
}

// theRightsAnswer returns the one message of a read of the rights of a chat.
func theRightsAnswer(t *testing.T, msgs []tea.Msg) chatAccessLoadedMsg {
	t.Helper()

	for _, msg := range msgs {
		if loaded, isAccess := msg.(chatAccessLoadedMsg); isAccess {
			return loaded
		}
	}

	t.Fatal("no read of the rights of a chat among the messages")

	return chatAccessLoadedMsg{}
}

// TestAProgramWithNoRightsSourceDrawsTheComposer proves the default: an
// interface that has nobody to ask keeps the field it always had, because a
// composer that is missing in every chat is a program that is broken rather
// than one that is careful.
func TestAProgramWithNoRightsSourceDrawsTheComposer(t *testing.T) {
	m := modelWithChatAccess(t, nil)

	var opened tea.Cmd
	m, opened = updateModel(t, m, press(tea.KeyEnter))
	runBatch(t, opened)

	if !strings.Contains(m.View(), composerPlaceholder) {
		t.Fatalf("the composer is missing with no source of rights:\n%s",
			m.View())
	}
}

// TestTheLinesOfTheReasons pins the words, so that a change to one of them
// is a change somebody decided to make.
func TestTheLinesOfTheReasons(t *testing.T) {
	cases := map[ChatBlocked]string{
		ChatBlockedNone:    "",
		ChatBlockedChannel: "Read-only channel",
		ChatBlockedGroup:   "You cannot write in this chat: sending is off for you",
		ChatBlockedPeer: "You cannot write in this chat: " +
			"this account is no longer reachable",
		ChatBlockedNotMember: "You cannot write in this chat: you are not a member",
		ChatBlockedBanned:    "You cannot write in this chat: you are banned",
	}

	for blocked, line := range cases {
		if got := blocked.line(); got != line {
			t.Errorf("line of %q = %q, want %q", blocked, got, line)
		}
	}
}

// TestAReasonThisBuildDoesNotKnowIsNotDrawnAsOneThisBuildDoes is the
// honest direction of an unknown reason: the composer goes away rather than
// a sentence that is not true appearing in its place.
func TestAReasonThisBuildDoesNotKnowIsNotDrawnAsOneThisBuildDoes(t *testing.T) {
	unknown := ChatBlocked("somethingNewInTelegram")

	if line := unknown.line(); strings.Contains(line, "off for you") {
		t.Fatalf("an unknown reason was drawn as a known one: %q", line)
	}
	if unknown.line() == "" {
		t.Fatal("an unknown reason draws no line at all")
	}
}

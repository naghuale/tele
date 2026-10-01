package tui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"
)

// This file is what the composer draws instead of a field when Telegram has
// said the chat cannot be written in, and the one read that asks.
//
// The rule of §4.5 is that a conversation is written in the composer. The
// rule of this file is narrower and it wins: a field in a chat Telegram will
// refuse is a promise the program cannot keep. A channel this account is only
// subscribed to took two messages, and both of them sat in the feed with
// `! failed` under them, which is what the report of 01.10 was. The line
// replaces the field in that chat, Enter sends nothing, and no key edits a
// draft there.
//
// There is a third state and it is the default: a chat nobody has asked
// about. The composer is drawn as it always is, because an interface that
// refuses to write in a chat on no evidence is a program that has stopped
// working rather than one that is careful.

// ChatAccess is whether this account can write in the open chat.
//
// The zero value is a chat that can be written in, so a program with no
// source of rights behaves exactly as it did before the question was asked.
type ChatAccess struct {
	// CanSend is whether a message typed in this chat would be accepted.
	CanSend bool

	// Blocked is what stops it, and empty when nothing does.
	Blocked ChatBlocked
}

// CanWrite reports whether the composer may be typed in.
func (a ChatAccess) CanWrite() bool {
	return a.Blocked == ChatBlockedNone
}

// ChatBlocked is what stops a chat from being written in, in the words this
// package draws.
//
// The names are the interface's own rather than TDLib's, the way
// ConnectionState's are: a value that travels to a screen reads the way the
// screen draws it.
type ChatBlocked string

const (
	// ChatBlockedNone is a chat that can be written in.
	ChatBlockedNone ChatBlocked = ""

	// ChatBlockedChannel is a channel this account does not post in.
	ChatBlockedChannel ChatBlocked = "channel"

	// ChatBlockedGroup is a group or a supergroup where this account may
	// not send.
	ChatBlockedGroup ChatBlocked = "group"

	// ChatBlockedPeer is a chat with a person whose account is gone.
	ChatBlockedPeer ChatBlocked = "peer"

	// ChatBlockedNotMember is a chat this account is not in.
	ChatBlockedNotMember ChatBlocked = "notMember"

	// ChatBlockedBanned is a chat this account was banned from.
	ChatBlockedBanned ChatBlocked = "banned"
)

// The words of §4.5 with the field taken out of them.
//
// A read-only channel is named on its own: it is the case a person meets
// first, it is not a failure of theirs, and the line says what the chat is
// rather than what is wrong with them. Every other reason is one sentence
// with the reason in it, because "you cannot write here" without saying why
// is a sentence a user has to go and ask somebody.
const (
	readOnlyChannelLine = "Read-only channel"
	noWritingLinePrefix = "You cannot write in this chat: "
)

// blockedLine returns the line that stands where the field would be.
func (b ChatBlocked) line() string {
	switch b {
	case ChatBlockedChannel:
		return readOnlyChannelLine
	case ChatBlockedNone:
		return ""
	}

	return noWritingLinePrefix + b.words()
}

// words is the reason as it is read inside the sentence.
func (b ChatBlocked) words() string {
	switch b {
	case ChatBlockedGroup:
		return "sending is off for you"
	case ChatBlockedPeer:
		return "this account is no longer reachable"
	case ChatBlockedNotMember:
		return "you are not a member"
	case ChatBlockedBanned:
		return "you are banned"
	case ChatBlockedNone:
		return ""
	}

	// A reason this build does not have a sentence for is not drawn as one
	// of the reasons it has: a wrong sentence about a real reason is worse
	// than the general one.
	return noWritingLinePrefix
}

// ChatAccessSource reads whether this account can write in a chat.
//
// It is a source and not a field of the chat because the rights are not
// known when the chat list is read: a channel is decided by this account's
// own member status in it, and that is a question about the account, asked
// for the chat that is open.
//
// A nil source means the interface knows nothing about the rights of any
// chat, which is the truth about a program that has no Telegram to ask: it
// draws the composer as it always did.
type ChatAccessSource interface {
	// ReadChatAccess reads the rights of one chat.
	//
	// A read that could not be answered is an error and not an answer, so
	// that the composer keeps what it knew rather than being told a chat
	// cannot be written in by a read that failed.
	ReadChatAccess(ctx context.Context, chatID int64) (ChatAccess, error)
}

// chatAccessLoadedMsg is delivered by loadChatAccess.
type chatAccessLoadedMsg struct {
	chatID int64
	access ChatAccess
}

// chatAccessFailedMsg is delivered by loadChatAccess for a read that could
// not be answered.
//
// The access of the open chat is kept: a read that failed says nothing about
// the chat, and a line that appeared because a query timed out would be a
// line about the network in the place where the chat is.
type chatAccessFailedMsg struct {
	chatID int64
	err    error
}

// loadChatAccess starts one read of the rights of the open chat.
//
// It runs on the poll the program already has, next to the status line and
// the delivery states: a change of rights is a change of state like any
// other, and a second timer for it would be a second cadence for a screen
// that is drawn as one.
func (m *Model) loadChatAccess() tea.Cmd {
	if m == nil || m.quitting || m.chatAccessSource == nil ||
		m.chatAccessLoading || m.chatAccessChatID == 0 {
		return nil
	}

	m.chatAccessLoading = true

	chatID := m.chatAccessChatID
	known := m.chatAccess
	source := m.chatAccessSource
	ctx := m.ctx

	return func() tea.Msg {
		if err := ctx.Err(); err != nil {
			return chatAccessFailedMsg{chatID: chatID, err: err}
		}

		access, err := source.ReadChatAccess(ctx, chatID)
		if err != nil {
			return chatAccessFailedMsg{chatID: chatID, err: err}
		}
		if access == known {
			// Nothing changed, so nothing is delivered. A read that
			// repainted the screen every two seconds for the same answer
			// would burn a battery and flicker a screen.
			return nil
		}

		return chatAccessLoadedMsg{chatID: chatID, access: access}
	}
}

// setChatAccessTarget points the read at the chat that is open.
//
// The rights of a chat are not the rights of the next one, so the answer is
// dropped with the target: a personal chat that can be written in must not
// leave a field over the channel that follows it.
//
// Pointing at the chat that is already the target changes nothing, so
// leaving a conversation and coming back to it does not turn a known answer
// into a question mark and then into a line that flickers.
func (m *Model) setChatAccessTarget(chatID int64) {
	if m == nil || m.chatAccessChatID == chatID {
		return
	}

	m.chatAccessChatID = chatID
	m.chatAccessLoading = false
	m.chatAccess = ChatAccess{}
	m.chatAccessKnown = false
}

// canWrite reports whether the composer of the open chat may be typed in.
//
// It is the whole of the rule and it is one line: a chat nobody has asked
// about is a chat this account can write in, because a field drawn from
// nothing is a field nobody refused.
func (m Model) canWrite() bool {
	if !m.chatAccessKnown {
		return true
	}

	return m.chatAccess.CanWrite()
}

// handleChatAccessLoaded puts the rights of a chat on the model.
//
// The draft is not touched: text typed while the chat could be written in
// stays where it is, and §8.5 keeps the draft when the composer is left.
// The focus moves, though, because a focus on a region where every key does
// nothing is the one thing §5 does not allow — and the line that replaced
// the field is a sentence about the chat, not a place to type.
func (m Model) handleChatAccessLoaded(
	msg chatAccessLoadedMsg,
) (Model, tea.Cmd) {
	if m.quitting || msg.chatID != m.chatAccessChatID {
		return m, nil
	}

	m.chatAccessLoading = false
	m.chatAccess = msg.access
	m.chatAccessKnown = true

	if !msg.access.CanWrite() && m.focus == FocusComposer {
		m.focus = FocusHistory
	}

	return m, nil
}

// handleChatAccessFailed keeps the rights the model already had.
func (m Model) handleChatAccessFailed(
	msg chatAccessFailedMsg,
) (Model, tea.Cmd) {
	if m.quitting || msg.chatID != m.chatAccessChatID {
		return m, nil
	}

	m.chatAccessLoading = false

	m.reportDiagnostic("chat access unavailable: %v\n", msg.err)

	return m, nil
}

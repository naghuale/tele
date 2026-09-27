package tui

import (
	"context"
	"errors"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// The keys and the results of the action sheet.
//
// Every action that can go wrong does so away from the keys: a cancel is a
// query to the durable queue, and a user walking a menu must not wait for
// one. What comes back is a message, and what the screen says about it is
// a fixed sentence.

// isOpenActions is the key that opens the sheet on the message under the
// cursor (§8.3, §13).
func isOpenActions(msg tea.KeyMsg) bool {
	return msg.Type == tea.KeyRunes && len(msg.Runes) == 1 && msg.Runes[0] == 'a'
}

// openActionSheet opens the sheet over the message under the cursor.
//
// A message with no actions yet has no sheet: an empty menu is a menu that
// cannot be closed with the keys in it.
func (m *Model) openActionSheet() {
	pending, ok := m.selectedPending()
	if !ok {
		history, isHistory := m.selectedHistory()
		if !isHistory {
			return
		}
		*m = withActionSheet(*m, actionSheetFor(MessageDeliverySent, true))
		m.actionSheet.text = history.Text

		return
	}

	sheet := actionSheetFor(pending.State, false)
	sheet.entryID = pending.EntryID
	sheet.version = pending.Version
	sheet.text = pending.Text
	*m = withActionSheet(*m, sheet)
}

func withActionSheet(model Model, sheet actionSheet) Model {
	model.actionSheet = sheet

	return model
}

// closeActionSheet closes the sheet without deciding anything.
func (m *Model) closeActionSheet() {
	m.actionSheet = actionSheet{}
	m.modal = confirmModal{}
}

// updatePopupKey handles a key while the sheet or the question is open.
//
// The question comes first in the Esc hierarchy of §8.5: a modal is above
// everything, and Esc in a question has to mean "I did not answer that"
// before it can mean anything else.
func (m Model) updatePopupKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.modal.open {
		return m.updateModalKey(msg)
	}

	return m.updateActionSheetKey(msg)
}

func (m Model) updateActionSheetKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case msg.Type == tea.KeyEsc:
		// §13: in uncertain, Esc is the safe default, and the safe default
		// is keeping the record. A user who opened a menu and closed it
		// without reading has not decided anything.
		m.closeActionSheet()

		return m, nil

	case isUp(msg):
		m.actionSheet.cursor = maxInt(m.actionSheet.cursor-1, 0)

		return m, nil

	case isDown(msg):
		m.actionSheet.cursor = minInt(
			m.actionSheet.cursor+1,
			maxInt(len(m.actionSheet.items)-1, 0),
		)

		return m, nil

	case msg.Type == tea.KeyEnter:
		return m.runAction()
	}

	return m, nil
}

func (m Model) updateModalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case msg.Type == tea.KeyEsc:
		// Cancel: the question was not answered, and nothing about the
		// record changes.
		m.closeActionSheet()

		return m, nil

	case isUp(msg):
		m.modal.cursor = (m.modal.cursor + len(modalItems) - 1) % len(modalItems)

		return m, nil

	case isDown(msg):
		m.modal.cursor = (m.modal.cursor + 1) % len(modalItems)

		return m, nil

	case msg.Type == tea.KeyEnter:
		switch m.modal.cursor {
		case modalCreateCopyItem:
			m.draftFromActionSheet()

			return m, nil

		case modalKeepUncertain:
			m.closeActionSheet()

			return m, nil

		default:
			m.closeActionSheet()

			return m, nil
		}
	}

	return m, nil
}

// modalItems are the items of the question of §12.3, in order.
var modalItems = []string{modalCreateCopy, modalKeepText, modalCancelText}

// runAction performs the item under the cursor.
func (m Model) runAction() (tea.Model, tea.Cmd) {
	item, ok := m.actionSheet.selected()
	if !ok {
		return m, nil
	}

	switch item.id {
	case actionClose, actionKeepUncertain:
		m.closeActionSheet()

		return m, nil

	case actionCopyText:
		return m.copySelectedText()

	case actionCreateNewMessage:
		m.draftFromActionSheet()

		return m, nil

	case actionCreateNewMessageConfirmed:
		// §12.3: the one action in this interface that can put a duplicate
		// into a conversation asks first, and the answer it starts on is
		// the one that changes nothing.
		m.modal = confirmModal{open: true, cursor: modalKeepUncertain}

		return m, nil

	case actionCancelMessage:
		return m.cancelSelectedMessage()
	}

	return m, nil
}

// draftFromActionSheet puts the text of the message into the composer.
//
// It is a draft and nothing else: the user sends it. A program that queued
// the text on a menu item would send a message nobody confirmed, which is
// the one thing this action exists to let a person decide.
func (m *Model) draftFromActionSheet() {
	text := m.actionSheet.text
	m.closeActionSheet()
	m.composer = []rune(text)
	m.composerCursor = len(m.composer)
	m.focus = FocusComposer
	m.composerPlaceholderLit = false
}

// cancelSelectedMessage asks the queue to cancel the message.
func (m Model) cancelSelectedMessage() (tea.Model, tea.Cmd) {
	if m.canceller == nil {
		return m, nil
	}

	entryID := m.actionSheet.entryID
	version := m.actionSheet.version
	canceller := m.canceller
	diagnostics := m.diagnostics
	ctx := m.ctx
	if ctx == nil {
		ctx = context.Background()
	}

	m.closeActionSheet()

	return m, func() tea.Msg {
		err := canceller.CancelMessage(ctx, entryID, version)
		if err == nil {
			return nil
		}
		if diagnostics != nil {
			fmt.Fprintf(
				diagnostics,
				"cancel message %s: %v\n",
				entryID,
				err,
			)
		}

		return messageCancelFailedMsg{entryID: entryID, err: err}
	}
}

// handleMessageCancelFailed applies the answer of a cancel that did not
// happen.
//
// Two sentences and one of them is about the record. A record the queue
// moved on is read again, because the screen is showing a state the user
// did not act on and a stale record on the screen is a claim about the
// queue that nothing checked.
func (m Model) handleMessageCancelFailed(
	msg messageCancelFailedMsg,
) (Model, tea.Cmd) {
	if msg.err == nil {
		return m, m.loadMessageStatuses()
	}

	if errors.Is(msg.err, ErrMessageStateChanged) {
		m.setNotice(noticeStateMoved)
		m.dropPendingMessage(msg.entryID)

		return m, m.loadMessageStatuses()
	}

	m.setNotice(noticeNotCanceled)

	return m, m.loadMessageStatuses()
}

// copySelectedText puts the text of the message in the clipboard of the
// terminal.
//
// OSC 52 is the copy that works over ssh and in a browser tab, and the
// text goes to the terminal and nowhere else: never to a log, never to a
// file, and never through a command a user has to trust with a message.
func (m Model) copySelectedText() (tea.Model, tea.Cmd) {
	text := m.actionSheet.text
	m.closeActionSheet()

	if err := m.clipboard.copyText(text); err != nil {
		m.reportDiagnostic("copy message text: %v\n", err)

		return m.withNoticeCleared(noticeCopyFailed)
	}

	return m.withNoticeCleared(noticeCopied)
}

// setNotice puts a sentence in the status line and takes it away after the
// notice is old enough to have been read.
func (m *Model) setNotice(text string) {
	m.notice = text
	m.noticeGeneration++
	m.noticeDeadline = m.clock()().Add(noticeTTL)
}

// withNoticeCleared is setNotice for a value receiver: the model comes back
// with the notice and the message that takes it away.
func (m Model) withNoticeCleared(text string) (Model, tea.Cmd) {
	m.setNotice(text)
	generation := m.noticeGeneration

	return m, tea.Tick(noticeTTL, func(time.Time) tea.Msg {
		return noticeExpiredMsg{generation: generation}
	})
}

// noticeExpiredMsg takes a notice away once it has been read.
type noticeExpiredMsg struct {
	generation uint64
}

// handleNoticeExpired takes a notice away, unless a newer one replaced it
// while this timer was running.
func (m Model) handleNoticeExpired(msg noticeExpiredMsg) (Model, tea.Cmd) {
	if msg.generation != m.noticeGeneration {
		return m, nil
	}

	m.notice = ""

	return m, nil
}

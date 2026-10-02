package tui

import (
	"context"
	"errors"
	"time"
)

// This file is the action sheet of §13: the menu a user opens on a message
// and the actions it can perform.
//
// The items are the specification. A queued message can be cancelled
// because the queue still holds it; a sending one cannot, because the
// dispatcher already has it; a failed one can be written again, because the
// queue gave up on it; an uncertain one asks a question first, because
// writing a copy of a message that may already have been delivered is the
// one action in this interface that can put two of the same thing in a
// conversation. There is no Retry anywhere: a retry is what the queue does
// on its own schedule, and a menu item for it would be a second dispatcher
// with a user behind it.

// ErrMessageStateChanged says that a record moved before the action that
// acted on it could happen.
//
// The queue cancels with the version the screen read, and refuses when the
// record is no longer at it. That is the right answer and it is not a
// reason to say "canceled": the user would watch a message they are sure
// they cancelled leave the screen, and it would come back.
var ErrMessageStateChanged = errors.New("message state changed")

// ErrMessageCancelUnavailable says that the queue could not cancel a
// message at all.
var ErrMessageCancelUnavailable = errors.New("message cancel is unavailable")

// MessageCanceller cancels a queued message by the version the screen read.
//
// The version is not decoration: the queue refuses a cancel of a record
// that has moved on, and an interface that passed a version of zero would
// either be refused every time or cancel whatever the record became.
type MessageCanceller interface {
	CancelMessage(
		ctx context.Context,
		entryID string,
		version uint64,
	) error
}

// actionID is one action of the sheet.
type actionID uint8

const (
	// actionCancelMessage cancels a queued, retrying or uncertain record.
	actionCancelMessage actionID = iota

	// actionCopyText puts the text of the message in the clipboard.
	actionCopyText

	// actionCreateNewMessage puts the text of a failed message in the
	// composer as a new draft.
	actionCreateNewMessage

	// actionCreateNewMessageConfirmed is the same thing behind the
	// question of §12.3.
	actionCreateNewMessageConfirmed

	// actionKeepUncertain closes the sheet and changes nothing.
	actionKeepUncertain

	// actionClose closes the sheet and changes nothing.
	actionClose
)

// actionItem is one line of the sheet.
type actionItem struct {
	id    actionID
	label string
}

// actionSheet is the open menu of §13.
//
// It is a value on the model and not a screen of its own: a popup is drawn
// over the conversation, and a screen would be a second thing the Esc
// hierarchy has to know about.
type actionSheet struct {
	open   bool
	items  []actionItem
	cursor int

	// entryID, version and text are what the actions act on, taken from
	// the message under the cursor when the sheet opened.
	//
	// They are read once on purpose: a sheet over a message that the queue
	// has moved on since would act on a state the user did not choose, and
	// the version is what lets the queue refuse it.
	entryID string
	version uint64
	text    string
}

// The words of §13.
const (
	labelCancelMessage     = "Cancel message"
	labelCopyText          = "Copy text"
	labelCreateNewMessage  = "Create new message"
	labelCreateANewMessage = "Create a new message"
	labelKeepAsUncertain   = "Keep as uncertain"
	labelCancelRecord      = "Cancel record"
	labelClose             = "Close"
)

// actionSheetFor returns the sheet of a message in a state.
//
// history says the message is one Telegram already has: its delivery is
// finished, so there is nothing to cancel and nothing to decide, and the
// only thing left to do with it is to take its text.
func actionSheetFor(
	state MessageDeliveryState,
	history bool,
) actionSheet {
	if history {
		return actionSheet{
			open:  true,
			items: []actionItem{{actionCopyText, labelCopyText}, {actionClose, labelClose}},
		}
	}

	items := []actionItem{{actionCopyText, labelCopyText}, {actionClose, labelClose}}

	switch state {
	case MessageDeliveryQueued, MessageDeliveryRetrying:
		// A cancel first: it is the action a user came here for, and the
		// one that is gone the moment the queue takes the message.
		items = append([]actionItem{
			{actionCancelMessage, labelCancelMessage},
		}, items...)

	case MessageDeliveryFailed:
		// The queue gave up, so the only way to send this text is to write
		// it again. Nothing is sent here: it becomes a draft.
		items = append([]actionItem{
			{actionCreateNewMessage, labelCreateNewMessage},
		}, items...)

	case MessageDeliveryUncertain:
		// Five items, and the first one asks. A user who came to an
		// uncertain message is looking at a message that may already be
		// there, and every action here has to be reachable without a
		// thought and one of them has to be a thought.
		items = []actionItem{
			{actionCreateNewMessageConfirmed, labelCreateANewMessage},
			{actionCopyText, labelCopyText},
			{actionKeepUncertain, labelKeepAsUncertain},
			{actionCancelMessage, labelCancelRecord},
			{actionClose, labelClose},
		}

	case MessageDeliverySending, MessageDeliveryCanceled, MessageDeliverySent:
		// Sending is on its way and cannot be called back; a canceled and a
		// sent message are decided already.
	}

	return actionSheet{open: true, items: items}
}

// selected returns the item under the cursor.
func (s actionSheet) selected() (actionItem, bool) {
	if !s.open || s.cursor < 0 || s.cursor >= len(s.items) {
		return actionItem{}, false
	}

	return s.items[s.cursor], true
}

// The words of §12.3, the one question in the interface.
const (
	modalTitle       = "Send a new copy?"
	modalExplanation = "The previous delivery is uncertain and may already have succeeded.\n" +
		"Sending again can create a duplicate."
	modalCreateCopy = "Create new copy"
	modalKeepText   = "Keep uncertain"
	modalCancelText = "Cancel"
)

// The words of the question about a draft, which the way out asks before
// it takes one away.
//
// It is the same shape as §12.3 and it is a question for the same reason:
// the thing on the screen cannot be put back. The title says what is being
// asked about, the second line says what is lost, and the answer the
// question opens on is the one that changes nothing.
const (
	quitModalTitle       = "Quit with a draft?"
	quitModalExplanation = "The text in the composer was not sent.\n" +
		"Quitting now leaves it behind."
	quitModalQuitText = "Quit and lose it"
	quitModalStayText = "Stay and write"
)

// The items of the question of §12.3, in the order they are drawn.
const (
	modalCreateCopyItem = iota
	modalKeepUncertain
	modalCancelItem
)

// The items of the question about a draft, in the order they are drawn. The
// question opens on the second of them: quitting is the answer the key was
// pressed for, and leaving is the answer that changes nothing.
const (
	quitModalQuitItem = iota
	quitModalStayItem
)

// modalKind is which question of the interface is open.
type modalKind uint8

const (
	// modalKindUncertain is the question of §12.3: may a copy of an
	// uncertain delivery be sent.
	modalKindUncertain modalKind = iota

	// modalKindQuit is the question the way out asks about a draft in the
	// composer.
	modalKindQuit
)

// modalQuestion is the text and the answers of one question.
//
// The question is a value rather than two sets of constants with a branch
// at every place that draws or answers one, so a new question is a new
// value here and every place that reads a question reads it the same way.
type modalQuestion struct {
	title       string
	explanation string
	items       []string
}

// theModalQuestion is the question of §12.3.
var theModalQuestion = modalQuestion{
	title:       modalTitle,
	explanation: modalExplanation,
	items:       []string{modalCreateCopy, modalKeepText, modalCancelText},
}

// theQuitQuestion is the question about a draft on the way out.
var theQuitQuestion = modalQuestion{
	title:       quitModalTitle,
	explanation: quitModalExplanation,
	items:       []string{quitModalQuitText, quitModalStayText},
}

// confirmModal is the question of §12.3, or the question about a draft.
type confirmModal struct {
	open   bool
	cursor int
	kind   modalKind
}

// question returns the question the modal is asking.
func (c confirmModal) question() modalQuestion {
	if c.kind == modalKindQuit {
		return theQuitQuestion
	}

	return theModalQuestion
}

// noticeTTL is how long a notice stays in the status line.
//
// Long enough to be read, short enough that it is not a permanent addition
// to a line the user is looking at for a connection. It is one message that
// takes it away, not a repaint loop (§6.3).
const noticeTTL = 3 * time.Second

// The notices of §13 and of the actions, in the words the specification
// uses.
const (
	noticeCopied      = "Copied"
	noticeCopyFailed  = "Could not copy. The terminal refused the request."
	noticeStateMoved  = "This message changed state. Nothing was canceled."
	noticeNotCanceled = "This message was not canceled."
)

// The notices of the keys that do not do what a user expected them to
// (the owner, 02.10).
//
// They are the same kind of sentence as the notices of the actions: what
// the key did, or did not do, and what to press instead. They are short
// enough to be read in the three seconds they stay on the status line, and
// they are not in the hint bar, which says both of these before either key
// is pressed.
const (
	// noticeQuitKey answers Esc in the chat list.
	noticeQuitKey = "q to quit"

	// noticeOpenAChatFirst answers Tab in a chat list with nothing in it.
	noticeOpenAChatFirst = "Open a chat first"
)

// messageCancelFailedMsg is delivered when a cancel did not happen.
//
// It is one message and no text: the cause can name a file and a keychain
// service, and the screen says what happened in words that are true while
// the cause goes to the diagnostic stream.
type messageCancelFailedMsg struct {
	entryID string
	err     error
}

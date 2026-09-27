package tui

import (
	"context"
	"errors"
	"io"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"telecli/internal/tui/theme"
)

type SubmissionState string

const (
	SubmissionQueued SubmissionState = "queued"
	SubmissionSent   SubmissionState = "sent"
)

type Submission struct {
	ID    string
	State SubmissionState
}

type ComposerSubmitter interface {
	SubmitMessage(
		ctx context.Context,
		chatID int64,
		text string,
	) (Submission, error)
}

type Dependencies struct {
	Source           ChatSource
	MessageSubmitter ComposerSubmitter

	// AccountKey is the stable account identifier used for durable delivery
	// status reads. Status polling stays disabled while it is empty.
	AccountKey string

	// PendingMessages is optional.
	//
	// It lists the outgoing messages that are not in the history yet, with
	// their text, so that the timeline can show them. A nil source is
	// direct delivery mode: every message is in the history as soon as it
	// is sent and nothing is pending.
	PendingMessages PendingMessageSource

	// StatusSummaries is optional.
	//
	// It reads the connection state and the queue counters the status line
	// shows, on the same poll cycle as the delivery statuses. A nil source
	// draws no status line.
	StatusSummaries StatusSummarySource

	// MessageStatuses is optional.
	//
	// A nil source is the direct delivery mode and disables durable status
	// polling; a non-nil source enables it.
	MessageStatuses MessageStatusSource

	// SendError, when non-nil, is shown in the send-error slot from the
	// start instead of only after a failed attempt.
	//
	// It is how a composer whose delivery is paused states the reason
	// before the user has tried to send anything. The message is
	// user-facing text: the composition root resolves the reason to a
	// safe string.
	SendError error

	// MessageCanceller is optional.
	//
	// It cancels a queued message by the version the screen read. A nil
	// canceller means the program does not own the queue: the sheet still
	// opens and the items that need it are the ones it cannot perform.
	MessageCanceller MessageCanceller

	// Clipboard receives the OSC 52 copy sequence.
	//
	// It is the program's terminal output and nothing else: the text of a
	// message goes to the terminal and to no log and to no file (§19). A
	// nil writer means there is nowhere to send a copy, and the interface
	// says the copy did not happen rather than claiming it did.
	Clipboard io.Writer

	// PresenceOpener is optional.
	//
	// It is told which chat the user is looking at, because TDLib only
	// sends the online member count of a chat that has been opened
	// (td_api.tl:10613). A nil opener draws no presence and opens
	// nothing.
	PresenceOpener ChatPresenceOpener

	// Diagnostics, when non-nil, receives the causes the screen must not
	// show: why a message could not be queued, why the chat list could not
	// be read.
	//
	// The interface shows a fixed sentence for those (§12.1) because a
	// TDLib error message can carry a phone number, a file path or a
	// message body. The cause belongs in a log somebody asked for. A nil
	// writer discards the causes.
	Diagnostics io.Writer

	// Theme is the interface theme, and ColorProfile the profile it was
	// built for.
	//
	// The composition root resolves both, because deciding what the
	// terminal can show is not a view's business and the resolution has to
	// be the same one for every command. An empty name selects the
	// default theme, so a caller that does not care about the interface
	// can leave both out.
	//
	// The views do not read them yet: the screens are rewritten in
	// PR-10A.2, and until then the theme travels with the model so that
	// step has one place to read it from.
	Theme        theme.Theme
	ColorProfile theme.Profile
}

type composerSubmissionMsg struct {
	chatID     int64
	operation  uint64
	submission Submission
	err        error
}

func NewModelWithDependencies(
	ctx context.Context,
	deps Dependencies,
) (Model, error) {
	if deps.MessageSubmitter == nil {
		return Model{}, errors.New("message submitter is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	model := NewModel()
	if deps.Source != nil {
		model = NewModelWithSource(deps.Source)
	}
	model.ctx = ctx
	model.submitter = deps.MessageSubmitter
	model.accountKey = strings.TrimSpace(deps.AccountKey)
	model.messageStatuses = deps.MessageStatuses
	model.pendingMessages = deps.PendingMessages
	model.statusSummaries = deps.StatusSummaries
	model.canceller = deps.MessageCanceller
	model.clipboard = deps.Clipboard
	model.presenceOpener = deps.PresenceOpener
	model.diagnostics = deps.Diagnostics
	model.theme = deps.Theme
	model.colorProfile = deps.ColorProfile
	// The renderer is built here, where the profile is known, rather than
	// per frame: it is the one object that decides how a colour is
	// printed, and a view that built its own would be a view deciding
	// what the terminal can show.
	model.rendererForProfile = newRenderer(deps.ColorProfile)
	if deps.SendError != nil {
		// Sending is already known to be impossible. Showing it now
		// means the user is not invited to press Enter to find out, and
		// keeping it on the model means the reason is still there in the
		// next chat.
		model.pausedErr = deps.SendError
		model.sendState = sendStateError
		model.sendErr = deps.SendError
	}
	return model, nil
}

func NewModelWithSourceAndSubmitter(
	ctx context.Context,
	source ChatSource,
	submitter ComposerSubmitter,
) (Model, error) {
	return NewModelWithDependencies(ctx, Dependencies{
		Source:           source,
		MessageSubmitter: submitter,
	})
}

func submitComposerCmd(
	ctx context.Context,
	submitter ComposerSubmitter,
	chatID int64,
	text string,
	operation uint64,
) tea.Cmd {
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		submission, err := submitter.SubmitMessage(ctx, chatID, text)
		return composerSubmissionMsg{
			chatID:     chatID,
			operation:  operation,
			submission: submission,
			err:        err,
		}
	}
}

package tui

import (
	"context"
	"errors"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
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

	// MessageStatuses is optional.
	//
	// A nil source is the direct delivery mode and disables durable status
	// polling; a non-nil source enables it.
	MessageStatuses MessageStatusSource
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

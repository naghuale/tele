package tui

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
)

type h5aComposerSubmitter struct {
	calls      atomic.Int64
	chatID     int64
	text       string
	submission Submission
	err        error
}

func (s *h5aComposerSubmitter) SubmitMessage(
	_ context.Context,
	chatID int64,
	text string,
) (Submission, error) {
	s.calls.Add(1)
	s.chatID = chatID
	s.text = text
	return s.submission, s.err
}

type h5aChatSource struct {
	sendCalls atomic.Int64
}

func (s *h5aChatSource) ListChats(context.Context) ([]Chat, error) {
	return nil, nil
}

func (s *h5aChatSource) LoadHistory(
	context.Context,
	int64,
	int64,
	int,
) (HistoryPage, error) {
	return HistoryPage{}, nil
}

func (s *h5aChatSource) SendMessage(
	context.Context,
	int64,
	string,
) (Message, error) {
	s.sendCalls.Add(1)
	return Message{}, errors.New("legacy SendMessage must not be called")
}

func h5aModel(
	source ChatSource,
	submitter ComposerSubmitter,
) Model {
	model, err := NewModelWithDependencies(
		context.Background(),
		Dependencies{
			Source:           source,
			MessageSubmitter: submitter,
		},
	)
	if err != nil {
		panic(err)
	}
	model.chats = []Chat{{ID: 42, Title: "Test"}}
	model.screen = ScreenConversation
	model.focus = FocusComposer
	return model
}

func TestNewModelWithDependenciesRejectsNilSubmitter(t *testing.T) {
	t.Parallel()

	_, err := NewModelWithDependencies(
		context.Background(),
		Dependencies{Source: &h5aChatSource{}},
	)
	if err == nil {
		t.Fatal("NewModelWithDependencies() error = nil, want non-nil")
	}
}

func TestComposerUsesInjectedMessageSubmitter(t *testing.T) {
	t.Parallel()

	source := &h5aChatSource{}
	submitter := &h5aComposerSubmitter{
		submission: Submission{ID: "entry-1", State: SubmissionQueued},
	}
	model := h5aModel(source, submitter)
	model.composer = []rune("hello")

	submitted, cmd := model.handleComposerEnter()
	model = submitted.(Model)
	if cmd == nil {
		t.Fatal("handleComposerEnter() cmd = nil")
	}
	msg := cmd()
	updated, _ := model.Update(msg)
	model = updated.(Model)

	if submitter.calls.Load() != 1 {
		t.Fatalf("submitter calls = %d, want 1", submitter.calls.Load())
	}
	if source.sendCalls.Load() != 0 {
		t.Fatalf("legacy source calls = %d, want 0", source.sendCalls.Load())
	}
	if submitter.chatID != 42 || submitter.text != "hello" {
		t.Fatalf("submitted args = (%d, %q), want (42, hello)", submitter.chatID, submitter.text)
	}
	if model.Composer() != "" {
		t.Fatalf("composer = %q, want empty", model.Composer())
	}
	got, ok := model.LastSubmission()
	if !ok || got != submitter.submission {
		t.Fatalf("last submission = %#v, %v, want %#v, true", got, ok, submitter.submission)
	}
}

func TestComposerAcceptsSentSubmission(t *testing.T) {
	t.Parallel()

	submitter := &h5aComposerSubmitter{
		submission: Submission{ID: "9001", State: SubmissionSent},
	}
	model := h5aModel(&h5aChatSource{}, submitter)
	model.composer = []rune("hello")
	submitted, cmd := model.handleComposerEnter()
	model = submitted.(Model)
	updated, _ := model.Update(cmd())
	model = updated.(Model)
	got, ok := model.LastSubmission()
	if !ok || got.State != SubmissionSent {
		t.Fatalf("last submission = %#v, %v, want sent", got, ok)
	}
}

func TestComposerKeepsTextAfterSubmissionFailure(t *testing.T) {
	t.Parallel()

	submitErr := errors.New("delivery unavailable")
	submitter := &h5aComposerSubmitter{err: submitErr}
	model := h5aModel(&h5aChatSource{}, submitter)
	model.composer = []rune("secret message payload")
	submitted, cmd := model.handleComposerEnter()
	model = submitted.(Model)
	updated, _ := model.Update(cmd())
	model = updated.(Model)
	if model.Composer() != "secret message payload" {
		t.Fatalf("composer = %q, want preserved", model.Composer())
	}
	if !errors.Is(model.sendErr, submitErr) {
		t.Fatalf("sendErr = %v, want %v", model.sendErr, submitErr)
	}
}

func TestComposerDoesNotRetryAfterSubmissionFailure(t *testing.T) {
	t.Parallel()

	submitter := &h5aComposerSubmitter{err: errors.New("failed")}
	model := h5aModel(&h5aChatSource{}, submitter)
	model.composer = []rune("hello")
	submitted, cmd := model.handleComposerEnter()
	model = submitted.(Model)
	updated, _ := model.Update(cmd())
	model = updated.(Model)
	if submitter.calls.Load() != 1 {
		t.Fatalf("submitter calls = %d, want 1", submitter.calls.Load())
	}
	if model.sendState != sendStateError {
		t.Fatalf("sendState = %v, want error", model.sendState)
	}
}

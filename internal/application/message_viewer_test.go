package application

import (
	"context"
	"errors"
	"testing"

	"telecli/internal/telegram"
)

// The viewer is the only place that turns "these messages are on the screen"
// into a query, and it passes the window through unchanged.

// recordingViewerSession is a session that records the windows it was asked
// to read.
type recordingViewerSession struct {
	chatID   telegram.ChatID
	windows  [][]telegram.MessageID
	err      error
	askedFor int
}

func (s *recordingViewerSession) ViewMessages(
	_ context.Context,
	chatID telegram.ChatID,
	messageIDs []telegram.MessageID,
) error {
	s.chatID = chatID
	s.windows = append(s.windows, append([]telegram.MessageID(nil), messageIDs...))
	s.askedFor++

	return s.err
}

func TestTheViewerAsksTheSessionAboutTheWindow(t *testing.T) {
	session := &recordingViewerSession{}
	viewer := &TelegramMessageViewer{session: session}

	if err := viewer.ViewMessages(context.Background(), 42, []int64{7, 8, 9}); err != nil {
		t.Fatalf("ViewMessages: %v", err)
	}

	if session.chatID != 42 {
		t.Fatalf("chat id = %d, want 42", session.chatID)
	}
	if len(session.windows) != 1 {
		t.Fatalf("windows = %v, want one", session.windows)
	}
	got := session.windows[0]
	if len(got) != 3 || got[0] != 7 || got[1] != 8 || got[2] != 9 {
		t.Fatalf("window = %v, want the identifiers of the screen", got)
	}
}

// A viewer without a session reads nothing. The alternative is a nil
// dereference on the first Enter, and a program without Telegram has no
// chat to read.
func TestTheViewerWithoutASessionDoesNothing(t *testing.T) {
	viewer := &TelegramMessageViewer{}

	if err := viewer.ViewMessages(context.Background(), 42, []int64{7}); err != nil {
		t.Fatalf("ViewMessages: %v", err)
	}
}

// A session that refuses is reported, not swallowed: the interface logs the
// cause and draws nothing, and something has to hand it the cause.
func TestTheViewerReturnsTheSessionError(t *testing.T) {
	session := &recordingViewerSession{err: errors.New("viewMessages: ERROR 400")}
	viewer := &TelegramMessageViewer{session: session}

	if err := viewer.ViewMessages(context.Background(), 42, []int64{7}); err == nil {
		t.Fatal("a refused read returned nothing")
	}
}

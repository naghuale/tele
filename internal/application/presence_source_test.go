package application

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"telecli/internal/telegram"
	"telecli/internal/tui"
)

// The presence of the other side of a chat, from the Telegram store to the
// header of the conversation.
//
// The mapping is where two facts meet: the store knows a status of a user,
// and the header draws a word. Neither owns the time zone, and neither
// decides whether an online status has run out - the view does that against
// the clock, because a status is a promise with a deadline and the deadline
// is the only thing that ends it.

var presenceMoment = time.Date(2026, 9, 28, 15, 0, 0, 0, time.UTC)

func presenceSourceFor(
	t *testing.T,
	presence telegram.Presence,
	ownUserID int64,
) *LiveStatusSummarySource {
	t.Helper()

	source, err := NewLiveStatusSummarySource(
		fixedConnectionState{presence: presence},
		fixedHealthSource{health: MessageDeliveryHealth{
			State: MessageDeliveryHealthRunning,
		}},
		ownUserID,
	)
	if err != nil {
		t.Fatalf("NewLiveStatusSummarySource: %v", err)
	}

	return source
}

func TestAnOnlineUserReachesTheHeaderAsADeadline(t *testing.T) {
	source := presenceSourceFor(t, telegram.Presence{
		Kind:   telegram.PresenceOneUser,
		UserID: 700,
		Status: telegram.UserStatus{
			Kind:    telegram.UserStatusOnline,
			Expires: presenceMoment.Add(time.Hour),
		},
	}, 0)

	summary, err := source.ReadStatusSummary(context.Background(), 42)
	if err != nil {
		t.Fatalf("ReadStatusSummary: %v", err)
	}
	if summary.Presence.Kind != tui.PresenceUser {
		t.Fatalf("presence kind = %v, want a user", summary.Presence.Kind)
	}
	if !summary.Presence.ExpiresAt.Equal(presenceMoment.Add(time.Hour)) {
		t.Fatalf("expires at = %s, want the deadline Telegram gave", summary.Presence.ExpiresAt)
	}
	// The word is the view's: nothing here decides that the deadline is
	// still in the future.
	if summary.Presence.LastSeenAt.IsZero() != true {
		t.Fatal("an online status was given a last-seen time")
	}
}

func TestAnOfflineUserReachesTheHeaderAsALastSeenTime(t *testing.T) {
	source := presenceSourceFor(t, telegram.Presence{
		Kind:   telegram.PresenceOneUser,
		UserID: 700,
		Status: telegram.UserStatus{
			Kind:      telegram.UserStatusOffline,
			WasOnline: presenceMoment.Add(-time.Hour),
		},
	}, 0)

	summary, err := source.ReadStatusSummary(context.Background(), 42)
	if err != nil {
		t.Fatalf("ReadStatusSummary: %v", err)
	}
	if !summary.Presence.LastSeenAt.Equal(presenceMoment.Add(-time.Hour)) {
		t.Fatalf("last seen at = %s, want the time Telegram gave", summary.Presence.LastSeenAt)
	}
}

func TestACoarseStatusReachesTheHeaderAsARange(t *testing.T) {
	for kind, want := range map[telegram.UserStatusKind]tui.PresenceRecency{
		telegram.UserStatusRecently:  tui.RecencyRecently,
		telegram.UserStatusLastWeek:  tui.RecencyLastWeek,
		telegram.UserStatusLastMonth: tui.RecencyLastMonth,
	} {
		source := presenceSourceFor(t, telegram.Presence{
			Kind:   telegram.PresenceOneUser,
			UserID: 700,
			Status: telegram.UserStatus{Kind: kind},
		}, 0)

		summary, err := source.ReadStatusSummary(context.Background(), 42)
		if err != nil {
			t.Fatalf("%v: ReadStatusSummary: %v", kind, err)
		}
		if summary.Presence.Recency != want {
			t.Fatalf("%v: recency = %v, want %v", kind, summary.Presence.Recency, want)
		}
	}
}

// TDLib sends the current user as an ordinary user, so the only way to
// tell a chat with oneself from a chat with a contact is to ask who this
// client is. The presence of Saved Messages is the user's own status, and
// drawing it would put "Online" over a conversation with nobody.
func TestAChatWithOneselfIsMarkedAsSuch(t *testing.T) {
	presence := telegram.Presence{
		Kind:   telegram.PresenceOneUser,
		UserID: 700,
		Status: telegram.UserStatus{
			Kind:    telegram.UserStatusOnline,
			Expires: presenceMoment.Add(time.Hour),
		},
	}

	ourselves := presenceSourceFor(t, presence, 700)
	summary, err := ourselves.ReadStatusSummary(context.Background(), 42)
	if err != nil {
		t.Fatalf("ReadStatusSummary: %v", err)
	}
	if !summary.Presence.Self {
		t.Fatal("a chat with oneself is not marked as one")
	}

	// A contact with the same status is not ourselves, and the identifier
	// that did not resolve leaves nobody marked.
	contact := presenceSourceFor(t, presence, 0)
	summary, err = contact.ReadStatusSummary(context.Background(), 42)
	if err != nil {
		t.Fatalf("ReadStatusSummary: %v", err)
	}
	if summary.Presence.Self {
		t.Fatal("a contact was marked as ourselves")
	}
}

func TestAGroupReachesTheHeaderAsACount(t *testing.T) {
	source := presenceSourceFor(t, telegram.Presence{
		Kind:              telegram.PresenceGroup,
		OnlineMemberCount: 4,
	}, 0)

	summary, err := source.ReadStatusSummary(context.Background(), 42)
	if err != nil {
		t.Fatalf("ReadStatusSummary: %v", err)
	}
	if summary.Presence.Kind != tui.PresenceGroup {
		t.Fatalf("presence kind = %v, want a group", summary.Presence.Kind)
	}
	if summary.Presence.OnlineMembers != 4 {
		t.Fatalf("online members = %d, want 4", summary.Presence.OnlineMembers)
	}
}

// A presence that the store does not have, and a chat that is not open, are
// both nothing. The presence of a chat the user has left would be a fact
// about a person on a screen that is not about them.
func TestAPresenceOfNothingIsNothing(t *testing.T) {
	source := presenceSourceFor(t, telegram.Presence{}, 0)

	for _, chatID := range []int64{0, 42} {
		summary, err := source.ReadStatusSummary(context.Background(), chatID)
		if err != nil {
			t.Fatalf("chat %d: ReadStatusSummary: %v", chatID, err)
		}
		if summary.Presence.Kind != tui.PresenceNone {
			t.Fatalf("chat %d: presence kind = %v, want none", chatID, summary.Presence.Kind)
		}
	}
}

// A store that cannot be asked for a presence at all - a program built
// without one - draws no presence rather than a wrong one.
func TestASourceWithoutAStoreHasNoPresence(t *testing.T) {
	source, err := NewLiveStatusSummarySource(
		nil,
		fixedHealthSource{health: MessageDeliveryHealth{
			State: MessageDeliveryHealthRunning,
		}},
		4242,
	)
	if err != nil {
		t.Fatalf("NewLiveStatusSummarySource: %v", err)
	}

	summary, err := source.ReadStatusSummary(context.Background(), 42)
	if err != nil {
		t.Fatalf("ReadStatusSummary: %v", err)
	}
	if summary.Presence.Kind != tui.PresenceNone {
		t.Fatalf("presence kind = %v, want none", summary.Presence.Kind)
	}
}

// A presence is a fact about a person, and §19 keeps it out of logs and
// reports. The summary that carries it holds no times, and the value on its
// way to a log says only what kind of presence it is.
func TestTheSummaryCarriesNoPresenceTimesInItsPrinting(t *testing.T) {
	source := presenceSourceFor(t, telegram.Presence{
		Kind:   telegram.PresenceOneUser,
		UserID: 700,
		Status: telegram.UserStatus{
			Kind:      telegram.UserStatusOffline,
			WasOnline: presenceMoment.Add(-time.Hour),
		},
	}, 0)

	summary, err := source.ReadStatusSummary(context.Background(), 42)
	if err != nil {
		t.Fatalf("ReadStatusSummary: %v", err)
	}
	if !strings.Contains(summary.Presence.String(), "presence") {
		t.Fatalf("presence String() = %q", summary.Presence.String())
	}
	if strings.Contains(summary.Presence.String(), "14:00") {
		t.Fatal("the presence printed a time")
	}
}

// ---- Open and close ----

// The opener is the only place that turns "the user is looking at this
// chat" into a query, and it passes the chat through unchanged.
func TestTheOpenerAsksTheSessionAboutTheChat(t *testing.T) {
	session := &recordingLifecycle{}
	opener := &TelegramChatPresenceOpener{session: session}

	if err := opener.OpenChat(context.Background(), 42); err != nil {
		t.Fatalf("OpenChat: %v", err)
	}
	if err := opener.CloseChat(context.Background(), 42); err != nil {
		t.Fatalf("CloseChat: %v", err)
	}

	if len(session.opened) != 1 || session.opened[0] != 42 {
		t.Fatalf("opened = %v, want the chat", session.opened)
	}
	if len(session.closed) != 1 || session.closed[0] != 42 {
		t.Fatalf("closed = %v, want the chat", session.closed)
	}
}

// An opener without a session opens nothing. The alternative is a nil
// dereference on the first Enter, and a program without Telegram has no
// chat to open.
func TestTheOpenerWithoutASessionDoesNothing(t *testing.T) {
	opener := &TelegramChatPresenceOpener{}

	if err := opener.OpenChat(context.Background(), 42); err != nil {
		t.Fatalf("OpenChat: %v", err)
	}
	if err := opener.CloseChat(context.Background(), 42); err != nil {
		t.Fatalf("CloseChat: %v", err)
	}
}

// A session that refuses is reported, not swallowed: the interface logs the
// cause and draws nothing, and something has to hand it the cause.
func TestTheOpenerReturnsTheSessionError(t *testing.T) {
	session := &recordingLifecycle{err: errors.New("openChat: ERROR 400")}
	opener := &TelegramChatPresenceOpener{session: session}

	if err := opener.OpenChat(context.Background(), 42); err == nil {
		t.Fatal("a refused open returned nothing")
	}
}

// ---- The own identifier ----

// Which user this client is, is asked once with a bounded wait: a slow
// answer must not hold the interface behind a presence line.
func TestTheOwnUserIDIsAskedWithABoundedWait(t *testing.T) {
	session := &ownUserStub{id: 700}

	id, err := resolveOwnUserID(context.Background(), session)
	if err != nil {
		t.Fatalf("resolveOwnUserID: %v", err)
	}
	if id != 700 {
		t.Fatalf("id = %d, want 700", id)
	}
	if session.calls != 1 {
		t.Fatalf("getMe calls = %d, want 1", session.calls)
	}
}

// A session that cannot answer leaves the identifier unknown, and a refusal
// is a line in the log rather than a program that will not start: the
// interface then shows no presence in Saved Messages, which is the smaller
// mistake next to showing the user's own Online there.
func TestAnUnansweredOwnUserIDIsUnknown(t *testing.T) {
	session := &ownUserStub{err: errors.New("getMe: ERROR 401")}

	id, err := resolveOwnUserID(context.Background(), session)
	if err == nil {
		t.Fatal("a refused getMe returned no error")
	}
	if id != 0 {
		t.Fatalf("id = %d, want 0", id)
	}
}

func TestNoSessionMeansNoOwnUserID(t *testing.T) {
	id, err := resolveOwnUserID(context.Background(), nil)
	if err != nil {
		t.Fatalf("resolveOwnUserID: %v", err)
	}
	if id != 0 {
		t.Fatalf("id = %d, want 0", id)
	}
}

// recordingLifecycle is a session that records the chat lifecycle calls.
type recordingLifecycle struct {
	opened []telegram.ChatID
	closed []telegram.ChatID
	err    error
}

func (s *recordingLifecycle) OpenChat(
	_ context.Context,
	chatID telegram.ChatID,
) error {
	s.opened = append(s.opened, chatID)

	return s.err
}

func (s *recordingLifecycle) CloseChat(
	_ context.Context,
	chatID telegram.ChatID,
) error {
	s.closed = append(s.closed, chatID)

	return s.err
}

// ownUserStub answers getMe with an identifier or a refusal.
type ownUserStub struct {
	id    int64
	err   error
	calls int
}

func (s *ownUserStub) GetMeUserID(context.Context) (int64, error) {
	s.calls++
	if s.err != nil {
		return 0, s.err
	}

	return s.id, nil
}

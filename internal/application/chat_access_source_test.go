package application

import (
	"context"
	"errors"
	"testing"

	"telecli/internal/telegram"
	"telecli/internal/tui"
)

// This file is the translation between the rights of a chat as the binding
// names them and the line the interface draws, and the one rule that decides
// who is asked: a read of the store, and a question to TDLib only for a chat
// the store has never heard of.

// stubAccessReader answers a read of the rights of a chat.
type stubAccessReader struct {
	access telegram.ChatAccess
	err    error
	calls  int
	lastID telegram.ChatID
}

func (r *stubAccessReader) GetChatAccess(
	_ context.Context,
	chatID telegram.ChatID,
) (telegram.ChatAccess, error) {
	r.calls++
	r.lastID = chatID

	return r.access, r.err
}

// stubAccessStore is the live store's half of the question.
type stubAccessStore struct {
	access telegram.ChatAccess
	known  bool
	calls  int
}

func (s *stubAccessStore) ChatAccess(
	telegram.ChatID,
) (telegram.ChatAccess, bool) {
	s.calls++

	return s.access, s.known
}

func TestTheChatAccessSourceNeedsAReader(t *testing.T) {
	if _, err := NewTelegramChatAccessSource(nil, nil); err == nil {
		t.Fatal("a source that can read nothing was built")
	}
}

func TestTheChatAccessSourceAsksTDLibForAChatTheStoreDoesNotKnow(t *testing.T) {
	reader := &stubAccessReader{access: telegram.ChatAccess{CanSend: true}}
	store := &stubAccessStore{}
	source, err := NewTelegramChatAccessSource(reader, store)
	if err != nil {
		t.Fatalf("NewTelegramChatAccessSource: %v", err)
	}

	access, err := source.ReadChatAccess(context.Background(), 42)
	if err != nil {
		t.Fatalf("ReadChatAccess: %v", err)
	}
	if !access.CanWrite() {
		t.Fatalf("access = %+v, want a composer", access)
	}
	if reader.calls != 1 || reader.lastID != 42 {
		t.Fatalf("the read asked %d times about %d, want once about 42",
			reader.calls, reader.lastID)
	}
}

func TestTheChatAccessSourcePrefersTheStore(t *testing.T) {
	reader := &stubAccessReader{access: telegram.ChatAccess{CanSend: true}}
	store := &stubAccessStore{
		access: telegram.ChatAccess{Reason: telegram.ChatAccessChannelReadOnly},
		known:  true,
	}
	source, err := NewTelegramChatAccessSource(reader, store)
	if err != nil {
		t.Fatalf("NewTelegramChatAccessSource: %v", err)
	}

	access, err := source.ReadChatAccess(context.Background(), 42)
	if err != nil {
		t.Fatalf("ReadChatAccess: %v", err)
	}
	if access.Blocked != tui.ChatBlockedChannel {
		t.Fatalf("blocked = %q, want the channel of the store", access.Blocked)
	}
	if reader.calls != 0 {
		t.Fatalf(
			"TDLib was asked %d times although the store knew the answer",
			reader.calls,
		)
	}
}

// TestTheStoreIsAskedAgainAfterTheRead covers the race the two halves are
// here for: a promotion that arrived while the read was on its way is already
// applied, and it is the newer of the two answers.
func TestTheStoreIsAskedAgainAfterTheRead(t *testing.T) {
	reader := &stubAccessReader{access: telegram.ChatAccess{CanSend: true}}
	store := &stubAccessStore{}
	source, err := NewTelegramChatAccessSource(reader, store)
	if err != nil {
		t.Fatalf("NewTelegramChatAccessSource: %v", err)
	}

	if _, err := source.ReadChatAccess(context.Background(), 42); err != nil {
		t.Fatalf("ReadChatAccess: %v", err)
	}
	// Once before the read, to see whether the store knows the chat, and
	// once after it, to see whether it knows more than the read returned.
	if store.calls != 2 {
		t.Fatalf("the store was asked %d times around the read, want two",
			store.calls)
	}

	// What the read put into the store, and what an update added to it.
	store.access = telegram.ChatAccess{Reason: telegram.ChatAccessPeerUnreachable}
	store.known = true

	access, err := source.ReadChatAccess(context.Background(), 42)
	if err != nil {
		t.Fatalf("ReadChatAccess: %v", err)
	}
	if access.Blocked != tui.ChatBlockedPeer {
		t.Fatalf(
			"blocked = %q, want what the store holds: it is newer than the "+
				"answer that came back",
			access.Blocked,
		)
	}
}

func TestTheChatAccessSourceRefusesAChatThatIsNotOpen(t *testing.T) {
	reader := &stubAccessReader{access: telegram.ChatAccess{CanSend: true}}
	source, err := NewTelegramChatAccessSource(reader, nil)
	if err != nil {
		t.Fatalf("NewTelegramChatAccessSource: %v", err)
	}

	if _, err := source.ReadChatAccess(
		context.Background(), 0,
	); !errors.Is(err, telegram.ErrInvalidChatID) {
		t.Fatalf("err = %v, want ErrInvalidChatID", err)
	}
	if reader.calls != 0 {
		t.Fatal("a read of no chat asked TDLib about something")
	}
}

func TestAReadThatFailedIsAnErrorAndNotAnAnswer(t *testing.T) {
	reader := &stubAccessReader{
		err: errors.New("getChat: context deadline exceeded"),
	}
	source, err := NewTelegramChatAccessSource(reader, nil)
	if err != nil {
		t.Fatalf("NewTelegramChatAccessSource: %v", err)
	}

	access, err := source.ReadChatAccess(context.Background(), 42)
	if err == nil {
		t.Fatal("a failed read was reported as an answer")
	}
	if access.Blocked != tui.ChatBlockedNone {
		t.Fatalf("access = %+v, want no claim at all", access)
	}
}

// TestEveryReasonOfTheBindingHasWords pins the table: a reason of the binding
// with no sentence in the interface is a read-only line with a wrong sentence
// on it, and the binding growing a reason is a change somebody has to see.
func TestEveryReasonOfTheBindingHasWords(t *testing.T) {
	cases := map[telegram.ChatAccessReason]tui.ChatBlocked{
		telegram.ChatAccessNone:            tui.ChatBlockedNone,
		telegram.ChatAccessChannelReadOnly: tui.ChatBlockedChannel,
		telegram.ChatAccessGroupRestricted: tui.ChatBlockedGroup,
		telegram.ChatAccessPeerUnreachable: tui.ChatBlockedPeer,
		telegram.ChatAccessNotAMember:      tui.ChatBlockedNotMember,
		telegram.ChatAccessBanned:          tui.ChatBlockedBanned,
	}

	for reason, blocked := range cases {
		access := projectChatAccess(telegram.ChatAccess{Reason: reason})
		if access.Blocked != blocked {
			t.Errorf(
				"reason %q is drawn as %q, want %q", reason, access.Blocked,
				blocked,
			)
		}
		if blocked != tui.ChatBlockedNone && access.CanWrite() {
			t.Errorf("reason %q still draws a field", reason)
		}
	}
}

// TestAReasonTheInterfaceHasNoWordsForTakesTheComposerAway is the direction
// an unknown reason is handled in: the composer goes rather than a sentence
// that is not true appearing in its place.
func TestAReasonTheInterfaceHasNoWordsForTakesTheComposerAway(t *testing.T) {
	access := projectChatAccess(
		telegram.ChatAccess{Reason: telegram.ChatAccessReason("somethingNew")},
	)
	if access.CanWrite() {
		t.Fatalf("access = %+v, want no composer", access)
	}
}

// TestTheChatAccessSourceIsAReadOfTheLiveStore drives the source over the
// real store, so that the wiring between the two halves of the question is
// proved and not only the halves apart.
func TestTheChatAccessSourceIsAReadOfTheLiveStore(t *testing.T) {
	store := telegram.NewLiveState()
	store.ChatAccess(1) // A nil-safe call on a real store is not a claim.
	reader := &stubAccessReader{
		access: telegram.ChatAccess{Reason: telegram.ChatAccessChannelReadOnly},
	}
	source, err := NewTelegramChatAccessSource(reader, store)
	if err != nil {
		t.Fatalf("NewTelegramChatAccessSource: %v", err)
	}

	access, err := source.ReadChatAccess(context.Background(), 42)
	if err != nil {
		t.Fatalf("ReadChatAccess: %v", err)
	}
	if access.Blocked != tui.ChatBlockedChannel {
		t.Fatalf("blocked = %q, want the channel of the reader", access.Blocked)
	}
}

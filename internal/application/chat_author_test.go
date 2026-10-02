package application

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"telecli/internal/telegram"
	"telecli/internal/tui"
)

// The names of people are read from a local TDLib at the moment a message
// is about to be drawn, kept in the adapter's memory, and never written
// anywhere. The tests here are the proof of the second half as much as of
// the first: the live store holds none of them (#41), the cache is bounded,
// and a name never reaches an error a user reads.

// fakeTelegramNames is a TelegramSenderNames that answers from a table and
// counts what it was asked, so that a test can tell a cache hit from a
// second read.
type fakeTelegramNames struct {
	users map[int64]string
	chats map[int64]string

	userErr  error
	chatErr  error
	userRead int
	chatRead int
}

func (f *fakeTelegramNames) GetUserName(
	_ context.Context,
	userID int64,
) (string, error) {
	f.userRead++

	if f.userErr != nil {
		return "", f.userErr
	}

	name, ok := f.users[userID]
	if !ok {
		return "", errors.New("telegram: user not found")
	}

	return name, nil
}

func (f *fakeTelegramNames) GetChat(
	_ context.Context,
	chatID telegram.ChatID,
) (telegram.ChatSummary, error) {
	f.chatRead++

	if f.chatErr != nil {
		return telegram.ChatSummary{}, f.chatErr
	}

	return telegram.ChatSummary{
		ID:    chatID,
		Title: f.chats[int64(chatID)],
		Kind:  telegram.ChatKindGroup,
	}, nil
}

// namedChats is a source that also answers for names.
type namedChats struct {
	*fakeTelegramChats
	names *fakeTelegramNames
}

func (n namedChats) GetUserName(
	ctx context.Context,
	userID int64,
) (string, error) {
	return n.names.GetUserName(ctx, userID)
}

func (n namedChats) GetChat(
	ctx context.Context,
	chatID telegram.ChatID,
) (telegram.ChatSummary, error) {
	return n.names.GetChat(ctx, chatID)
}

// groupSource is a source whose one chat is a group, which is the case the
// names are for.
func groupSource(names *fakeTelegramNames, messages ...telegram.Message) namedChats {
	return namedChats{
		fakeTelegramChats: &fakeTelegramChats{
			history: telegram.HistoryPage{Messages: messages},
		},
		names: names,
	}
}

func historyOf(t *testing.T, service *TelegramChatService, messages ...telegram.Message) []tui.Message {
	t.Helper()

	fake, ok := service.chats.(namedChats)
	if !ok {
		fake = namedChats{fakeTelegramChats: service.chats.(*fakeTelegramChats)}
	}
	fake.fakeTelegramChats.history = telegram.HistoryPage{Messages: messages}

	page, err := service.LoadHistory(context.Background(), 9, 0, len(messages))
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}

	return page.Messages
}

// A message of this user is signed "You", and the interface draws no name
// above it: the block is on the right, which already says whose it is.
func TestAnOutgoingMessageIsSignedYou(t *testing.T) {
	names := &fakeTelegramNames{users: map[int64]string{}}
	service := NewTelegramChatService(groupSource(names, telegram.Message{
		ID: 1, Outgoing: true, Text: "hi", Timestamp: time.Unix(1, 0),
	}))

	got := historyOf(t, service, telegram.Message{
		ID: 1, Outgoing: true, Text: "hi", Timestamp: time.Unix(1, 0),
	})
	if got[0].Author != "You" {
		t.Fatalf("Author = %q, want %q", got[0].Author, "You")
	}
	if names.userRead != 0 {
		t.Fatalf("an outgoing message asked for %d names, want none", names.userRead)
	}
}

// A personal chat has exactly one other person in it, and every message in
// it is from that person or from this user: the name of the chat is the
// name of the sender, and no name is read for it.
func TestAPersonalChatIsNamedByTheChat(t *testing.T) {
	fake := &fakeTelegramChats{}
	service := NewTelegramChatService(fake)
	service.names = &chatOnlyNames{title: "Anna"}

	got := historyOf(t, service, telegram.Message{
		ID: 1, Text: "hi", Sender: telegram.MessageSender{
			Kind: telegram.MessageSenderUser, ID: 5,
		},
	})

	if got[0].Author != "Anna" {
		t.Fatalf("Author = %q, want the name of the chat", got[0].Author)
	}
}

// chatOnlyNames answers for a chat and not for a person, which is what a
// personal chat needs: it never asks.
type chatOnlyNames struct {
	title string
	reads int
}

func (c *chatOnlyNames) GetUserName(
	_ context.Context,
	_ int64,
) (string, error) {
	return "", errors.New("no user names here")
}

func (c *chatOnlyNames) GetChat(
	_ context.Context,
	chatID telegram.ChatID,
) (telegram.ChatSummary, error) {
	c.reads++

	return telegram.ChatSummary{
		ID:    chatID,
		Title: c.title,
		Kind:  telegram.ChatKindPrivate,
	}, nil
}

// A channel's messages are all from the channel, and the channel is named
// by its own title whatever the sender field says.
func TestAChannelIsNamedByItsTitle(t *testing.T) {
	names := &fakeTelegramNames{users: map[int64]string{}}
	source := groupSource(names, telegram.Message{ID: 1, Text: "news"})
	service := NewTelegramChatService(source)
	service.names = &chatOnlyNames{title: "Xiaomi News"}

	got := historyOf(t, service, telegram.Message{
		ID: 1, Text: "news", Sender: telegram.MessageSender{
			Kind: telegram.MessageSenderUser, ID: 900,
		},
	})

	if got[0].Author != "Xiaomi News" {
		t.Fatalf("Author = %q, want the title of the channel", got[0].Author)
	}
	if names.userRead != 0 {
		t.Fatalf("a channel asked for %d user names, want none", names.userRead)
	}
}

// A group names the person who sent each message, and two people get two
// different names.
func TestAGroupNamesThePersonWhoSentTheMessage(t *testing.T) {
	names := &fakeTelegramNames{
		users: map[int64]string{21: "Marta", 34: "Boris"},
		chats: map[int64]string{},
	}
	source := groupSource(names)
	service := NewTelegramChatService(source)

	got := historyOf(t, service,
		telegram.Message{ID: 1, Text: "one", Sender: telegram.MessageSender{
			Kind: telegram.MessageSenderUser, ID: 21,
		}},
		telegram.Message{ID: 2, Text: "two", Sender: telegram.MessageSender{
			Kind: telegram.MessageSenderUser, ID: 34,
		}},
	)

	if got[0].Author != "Marta" || got[1].Author != "Boris" {
		t.Fatalf("authors = %q and %q, want Marta and Boris", got[0].Author, got[1].Author)
	}
	if got[0].AuthorID != 21 || got[1].AuthorID != 34 {
		t.Fatalf(
			"author identifiers = %d and %d, want 21 and 34",
			got[0].AuthorID, got[1].AuthorID,
		)
	}
}

// A message sent on behalf of a chat is signed with the title of that
// chat: an anonymous administrator of a group is not a person, and the
// group is what the message is from as far as a reader is concerned.
func TestAMessageSentByAChatIsNamedByThatChat(t *testing.T) {
	names := &fakeTelegramNames{
		users: map[int64]string{},
		chats: map[int64]string{77: "Release Room"},
	}
	service := NewTelegramChatService(groupSource(names))

	got := historyOf(t, service, telegram.Message{
		ID: 1, Text: "posted", Sender: telegram.MessageSender{
			Kind: telegram.MessageSenderChat, ID: 77,
		},
	})

	if got[0].Author != "Release Room" {
		t.Fatalf("Author = %q, want the title of the sending chat", got[0].Author)
	}
}

// A sender that cannot be named says so. A message with nothing above it
// is a message a user cannot tell from a message that failed to load, and
// the name of the group is a worse answer than an honest one: it would put
// a name on a message that nobody sent on behalf of the group.
func TestASenderThatCannotBeNamedIsUnknown(t *testing.T) {
	for name, names := range map[string]*fakeTelegramNames{
		"the read fails":   {users: map[int64]string{}, userErr: errors.New("no")},
		"the user is gone": {users: map[int64]string{}},
		"there is no source": {
			users: map[int64]string{},
		},
	} {
		t.Run(name, func(t *testing.T) {
			source := groupSource(names)
			service := NewTelegramChatService(source)
			if name != "there is no source" {
				service.names = names
			} else {
				service.names = nil
			}

			got := historyOf(t, service, telegram.Message{
				ID: 1, Text: "hi", Sender: telegram.MessageSender{
					Kind: telegram.MessageSenderUser, ID: 21,
				},
			})
			if got[0].Author != unknownAuthor {
				t.Fatalf("Author = %q, want %q", got[0].Author, unknownAuthor)
			}
		})
	}
}

// The same name is read once. A conversation scrolls, a poll redraws the
// screen, and a name that is asked for again every frame is a local query
// per message per frame.
func TestANameIsReadOnceAndKept(t *testing.T) {
	names := &fakeTelegramNames{users: map[int64]string{21: "Marta"}, chats: map[int64]string{}}
	source := groupSource(names)
	service := NewTelegramChatService(source)

	messages := []telegram.Message{
		{ID: 1, Text: "one", Sender: telegram.MessageSender{Kind: telegram.MessageSenderUser, ID: 21}},
		{ID: 2, Text: "two", Sender: telegram.MessageSender{Kind: telegram.MessageSenderUser, ID: 21}},
	}
	historyOf(t, service, messages...)
	reads := names.userRead

	historyOf(t, service, messages...)
	if names.userRead != reads {
		t.Fatalf("the name was read %d times, want %d", names.userRead, reads)
	}
	if reads != 1 {
		t.Fatalf("the name was read %d times, want 1", reads)
	}
}

// The cache is bounded. A conversation with a thousand people in it must
// not leave a thousand names in the memory of a program that is only
// drawing a screen.
func TestTheNameCacheDoesNotGrowPastItsLimit(t *testing.T) {
	cache := newNameCache(8)
	for index := range 64 {
		cache.remember(userNameKey(int64(index)), "a name")
	}

	if got := cache.size(); got > 8 {
		t.Fatalf("the cache holds %d names, want at most 8", got)
	}
	if got := cache.size(); got == 0 {
		t.Fatal("the cache forgot everything")
	}
}

// A user and a chat live in the same number space, and 42 is a person in
// one message and a channel in the next. One key for both would put a
// channel's title above a person's messages.
func TestTheCacheKeepsUsersAndChatsApart(t *testing.T) {
	cache := newNameCache(8)
	cache.remember(userNameKey(42), "Marta")
	cache.remember(chatNameKey(42), "Release Room")

	user, ok := cache.lookup(userNameKey(42))
	if !ok || user != "Marta" {
		t.Fatalf("the user is %q, want Marta", user)
	}

	chat, ok := cache.lookup(chatNameKey(42))
	if !ok || chat != "Release Room" {
		t.Fatalf("the chat is %q, want Release Room", chat)
	}
}

// A name is read from a local TDLib and kept in the adapter's memory; it
// reaches no store, no log and no error. The error of a failed read is
// the TDLib one, and it names the query rather than the person.
func TestANameNeverReachesAnErrorOrADiagnostic(t *testing.T) {
	cause := errors.New("getUser 500: internal server error at /Users/somebody")
	names := &fakeTelegramNames{
		users:   map[int64]string{},
		userErr: cause,
		chats:   map[int64]string{},
	}
	service := NewTelegramChatService(groupSource(names))

	got := historyOf(t, service, telegram.Message{
		ID: 1, Text: "hi", Sender: telegram.MessageSender{
			Kind: telegram.MessageSenderUser, ID: 21,
		},
	})
	if got[0].Author != unknownAuthor {
		t.Fatalf("Author = %q, want %q", got[0].Author, unknownAuthor)
	}

	// The page itself is a successful read: a sender that could not be
	// named does not fail the history it is in.
	page, err := service.LoadHistory(context.Background(), 9, 0, 1)
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	if strings.Contains(errString(page), "somebody") {
		t.Fatalf("the page carries the cause: %v", page)
	}
}

// errString is every string in a history page, for a test that asks what
// would be on the screen.
func errString(page tui.HistoryPage) string {
	var out strings.Builder
	for _, message := range page.Messages {
		out.WriteString(message.Text)
		out.WriteString(message.Author)
		out.WriteString(message.Caption)
	}

	return out.String()
}

// A picture is one message with a word for what it carries and the caption
// under it, and an album is a run of them that share an identifier.
func TestAMediaMessageCarriesItsWordAndItsCaption(t *testing.T) {
	names := &fakeTelegramNames{users: map[int64]string{21: "Marta"}, chats: map[int64]string{}}
	service := NewTelegramChatService(groupSource(names))

	got := historyOf(t, service,
		telegram.Message{ID: 1, Media: "photo", Caption: "at the bridge"},
		telegram.Message{ID: 2, Media: "photo", MediaAlbumID: 5},
		telegram.Message{ID: 3, Media: "photo", MediaAlbumID: 5},
	)

	if got[0].Media != "photo" || got[0].Caption != "at the bridge" {
		t.Fatalf("media = %q caption = %q, want photo and the caption",
			got[0].Media, got[0].Caption)
	}
	if got[1].AlbumID != 5 || got[2].AlbumID != 5 {
		t.Fatalf("album identifiers = %d and %d, want 5", got[1].AlbumID, got[2].AlbumID)
	}
	if got[0].AlbumID != 0 {
		t.Fatalf("a message that is not in an album has the identifier %d", got[0].AlbumID)
	}
}

// What a message carries reaches the screen whole: the word, whatever the
// payload said about it, and the caption under it, and the phrase of a
// service message in the field of its own.
//
// The three words of a label are one bracket on the screen, so the fields
// have to travel together: a sticker whose emoji was left on the way is
// "[sticker]", which is a label about a sticker nobody can see (#17).
func TestTheWordsOfALabelReachTheScreenTogether(t *testing.T) {
	names := &fakeTelegramNames{users: map[int64]string{21: "Marta"}, chats: map[int64]string{}}
	service := NewTelegramChatService(groupSource(names))

	got := historyOf(t, service,
		telegram.Message{
			ID: 1, Media: "sticker", MediaDetail: " 😀",
			Caption: "at the bridge",
		},
		telegram.Message{ID: 2, Service: "joined the chat by a link"},
		telegram.Message{ID: 3, Media: "message"},
	)

	if got[0].Media != "sticker" || got[0].MediaDetail != " 😀" {
		t.Errorf("the sticker carries %q and %q, want the word and its emoji",
			got[0].Media, got[0].MediaDetail)
	}
	if got[0].Caption != "at the bridge" {
		t.Errorf("the caption is %q, want the words under the sticker",
			got[0].Caption)
	}
	if got[1].Service != "joined the chat by a link" {
		t.Errorf("the service phrase is %q, want what happened in the chat",
			got[1].Service)
	}
	// A kind the adapter has no words for travels as the word it was named
	// with on the screen, and the screen draws that word — the name of the
	// TDLib class never travels (#50).
	if got[2].Media != "message" {
		t.Errorf("an unnamed content travels as %q, want the word of a "+
			"message with no kind named", got[2].Media)
	}
}

// A row says what kind of chat it is, because that is what decides whether
// a message names its author and whether the unread badge is in the
// accent.
func TestAChatRowKnowsWhatKindOfChatItIs(t *testing.T) {
	fake := &fakeTelegramChats{
		snapshot: telegram.ChatListSnapshot{
			Chats: []telegram.ChatSummary{
				{ID: 1, Title: "Anna", Kind: telegram.ChatKindPrivate},
				{ID: 2, Title: "Room", Kind: telegram.ChatKindGroup},
				{ID: 3, Title: "News", Kind: telegram.ChatKindSupergroup, IsChannel: true},
			},
		},
	}
	chats, err := NewTelegramChatService(fake).
		ListChats(context.Background())
	if err != nil {
		t.Fatalf("ListChats: %v", err)
	}

	want := []tui.ChatKind{tui.ChatKindPrivate, tui.ChatKindGroup, tui.ChatKindChannel}
	for index, kind := range want {
		if chats[index].Kind != kind {
			t.Fatalf("chat %d is %v, want %v", index, chats[index].Kind, kind)
		}
	}
}

// The chat with oneself is called "Saved Messages" by Telegram and by the
// phone, and "Избранное" by the person using it, and a user who is looking
// for it types the word they know. Both words find the row, and the row says
// "Saved Messages" whatever it was called on the wire: TDLib sends the name
// of the user for that chat, and a list whose row says the account holder's
// own name is a list where somebody has to recognise themselves by their own
// name to find their own notes (the owner, 29.09.2026).
func TestTheOwnChatIsFoundByEitherOfItsNames(t *testing.T) {
	// The name TDLib sends for the chat with oneself: the user.
	fake := &fakeTelegramChats{
		snapshot: telegram.ChatListSnapshot{
			Chats: []telegram.ChatSummary{
				{ID: 1, Title: "Anna", Kind: telegram.ChatKindPrivate, PeerUserID: 5},
				{ID: 2, Title: "Andrey Babenko", Kind: telegram.ChatKindPrivate, PeerUserID: 77},
			},
		},
	}
	service := NewTelegramChatServiceFor(fake, 77)

	chats, err := service.ListChats(context.Background())
	if err != nil {
		t.Fatalf("ListChats: %v", err)
	}
	if chats[1].Title != ownChatTitle {
		t.Fatalf(
			"the own chat is called %q, want %q",
			chats[1].Title, ownChatTitle,
		)
	}
	for _, want := range []string{ownChatTitle, ownChatAlias} {
		if !slices.Contains(chats[1].Aliases, want) {
			t.Errorf(
				"the own chat is not found by %q: aliases %q",
				want, chats[1].Aliases,
			)
		}
	}
	if len(chats[0].Aliases) != 0 {
		t.Fatalf("a chat with somebody else has the aliases %q", chats[0].Aliases)
	}
	if chats[0].Title != "Anna" {
		t.Fatalf("a chat with somebody else was renamed: %q", chats[0].Title)
	}
}

// A chat with somebody who happens to share the identifier is not the own
// chat, and one whose own identifier could not be resolved is not either:
// calling a contact "Избранное" is worse than not finding the chat by
// that word.
func TestOnlyTheChatWithThisUserCarriesTheAlias(t *testing.T) {
	for name, own := range map[string]int64{
		"the identifier is not known": 0,
		"the identifier is another":   78,
	} {
		t.Run(name, func(t *testing.T) {
			fake := &fakeTelegramChats{
				snapshot: telegram.ChatListSnapshot{
					Chats: []telegram.ChatSummary{{
						ID: 2, Title: "Saved Messages",
						Kind: telegram.ChatKindPrivate, PeerUserID: 77,
					}},
				},
			}
			chats, err := NewTelegramChatServiceFor(fake, own).
				ListChats(context.Background())
			if err != nil {
				t.Fatalf("ListChats: %v", err)
			}
			if len(chats[0].Aliases) != 0 {
				t.Fatalf("the chat carries the aliases %q", chats[0].Aliases)
			}
		})
	}
}

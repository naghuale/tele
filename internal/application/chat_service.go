package application

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"telecli/internal/telegram"
	"telecli/internal/tui"
)

const (
	defaultChatListLimit = 50
	defaultHistoryLimit  = 50

	// chatServiceTimeout bounds a single chat service call. Without it,
	// a TDLib query that never produces a response would keep the
	// tea.Cmd goroutine alive until the session is closed.
	chatServiceTimeout = 60 * time.Second

	// authorNameTimeout bounds the read of a sender's name.
	//
	// The name is decoration around a message the user can already read,
	// and a local TDLib that cannot answer in a second is not going to
	// answer at all. The message is drawn as "Unknown" rather than held
	// back for it.
	authorNameTimeout = 2 * time.Second

	// unknownAuthor is what a message is signed with when the name of its
	// sender could not be read.
	//
	// It is a word and not an empty row: a message with nothing above it
	// is a message a user cannot tell from a message that failed to load.
	unknownAuthor = "Unknown"

	// ownChatAliases are the other names the chat with oneself is found
	// by in the search of §9.
	//
	// Telegram calls it "Saved Messages" and the person using it calls it
	// "Избранное", and a user who is looking for it types the word they
	// know. The alias never reaches the screen: it is a way of finding the
	// row and not a second name for it.
	ownChatAlias = "Избранное"

	// ownChatTitle is the name the chat with oneself is drawn under, in the
	// header of the conversation and in the row of the list.
	//
	// Telegram calls it "Saved Messages" and the person using it calls it
	// "Избранное", and both are the same row: the name is what the screen
	// says and the alias is what a search finds it by, so the two words are
	// one on the screen and two in the search. It used to be drawn under
	// the name of the user, which is a name everybody knows and nobody
	// looks for.
	ownChatTitle = "Saved Messages"

	// historyNotLoadedRetries is how many times the newest page of a chat is
	// asked again while it comes back empty and Telegram says the chat has a
	// last message (#27).
	//
	// Five is a bound and not a patience: the owner saw a channel whose
	// whole conversation was replaced by "No messages yet", and a wait that
	// never ends is the same defect with a spinner on it. Five repeats with
	// the pause below are a little over six seconds of waiting, which is
	// what TDLib needs to bring a channel's history down from the server on
	// a connection that is worth having.
	historyNotLoadedRetries = 5

	// historyNotLoadedRetryWait is the pause before the first repeat of a
	// page that came back empty, and every repeat doubles it up to
	// historyNotLoadedRetryWaitMax.
	//
	// The pause is what gives TDLib the time to answer at all: the first
	// request is the one that starts the download, and asking again
	// immediately asks about a download that has not begun. Doubling is
	// what a wait of unknown length looks like — far enough apart to be
	// worth waiting, close enough that a user who opened a chat reads a
	// conversation and not a countdown.
	historyNotLoadedRetryWait    = 300 * time.Millisecond
	historyNotLoadedRetryWaitMax = 2 * time.Second

	// nameCacheLimit is how many names the adapter keeps.
	//
	// It is a bound and not a policy: a conversation with a thousand
	// people in it would otherwise keep a thousand names for as long as
	// the program runs, and the names are read from a local TDLib rather
	// than sent anywhere, so nothing is saved by keeping all of them. The
	// limit is far above the number of people in any chat list a user
	// reads, so a limit that is reached is a conversation nobody scrolls.
	nameCacheLimit = 512
)

// TelegramChats is the narrow dependency used by TelegramChatService.
//
// It is satisfied by *telegram.AuthorizedSession. The interface exists
// so unit tests can drive the adapter without a real session.
type TelegramChats interface {
	GetChats(ctx context.Context, limit int) (telegram.ChatListSnapshot, error)
	GetChatHistory(
		ctx context.Context,
		chatID telegram.ChatID,
		fromMessageID telegram.MessageID,
		limit int,
	) (telegram.HistoryPage, error)
	SendTextMessage(
		ctx context.Context,
		chatID telegram.ChatID,
		text string,
	) (telegram.Message, error)
}

// TelegramSenderNames reads the name of a person and the title of a chat.
//
// The two are separate capabilities from TelegramChats because the live
// store deliberately does not keep either of them (#41): a chat title is
// part of the chat list, and a person's name is not kept anywhere at all.
// A session that cannot answer says so and the message is drawn as
// "Unknown".
type TelegramSenderNames interface {
	GetUserName(ctx context.Context, userID int64) (string, error)
	GetChat(ctx context.Context, chatID telegram.ChatID) (telegram.ChatSummary, error)
}

// ErrHistoryNotLoaded says the newest page of a chat came back empty as
// many times as it was asked, while Telegram reports a last message in that
// chat (#27).
//
// It is not an error of the wire and not a failure of the program: it is
// TDLib holding a conversation it has not brought down from the server yet.
// The interface has to say something about it, and the one thing it must
// not say is that the chat is empty — the chat list was drawn from the same
// last message a moment earlier, and the owner read both on one screen.
//
// It is a named error rather than a string because the difference matters to
// whoever asks again: this conversation is not empty, and a wait will find
// it where an empty page says there is nothing to find.
var ErrHistoryNotLoaded = errors.New("telegram: history is not loaded yet")

// TelegramChatService adapts a TelegramChats source to tui.ChatSource.
type TelegramChatService struct {
	chats        TelegramChats
	names        TelegramSenderNames
	listLimit    int
	historyLimit int

	// historyRetryWait is the pause before the newest page of a chat is
	// asked again while it comes back empty, and it is a field rather than
	// the constant so that a test can wait for the repeat instead of for
	// the wait.
	historyRetryWait time.Duration

	// ownUserID is the user this client is, and is what tells the chat
	// with oneself from a chat with a contact. It is zero when it could
	// not be resolved, and then the own chat has no alias and the
	// interface searches it by its title alone — the smaller mistake
	// next to calling somebody's contact "Избранное".
	ownUserID int64

	// names read once and kept for as long as the program runs. It is a
	// field rather than a package so that two adapters in one process do
	// not share it, and it is not a store: nothing here is written to
	// disk, to a log, to a doctor report or to an error message.
	names2 *nameCache
}

// NewTelegramChatService builds a chat service over the given source.
//
// The source is asked for names when it can answer: a session answers
// both questions, and a source that cannot is drawn with "Unknown" above
// its messages rather than not at all.
func NewTelegramChatService(chats TelegramChats) *TelegramChatService {
	service := &TelegramChatService{
		chats:            chats,
		listLimit:        defaultChatListLimit,
		historyLimit:     defaultHistoryLimit,
		historyRetryWait: historyNotLoadedRetryWait,
		names2:           newNameCache(nameCacheLimit),
	}
	if names, ok := chats.(TelegramSenderNames); ok {
		service.names = names
	}

	return service
}

// NewTelegramChatServiceFor builds a chat service that knows which user
// this client is, so that the chat with oneself can be found by its other
// name.
func NewTelegramChatServiceFor(
	chats TelegramChats,
	ownUserID int64,
) *TelegramChatService {
	service := NewTelegramChatService(chats)
	service.ownUserID = ownUserID

	return service
}

// ListChats implements tui.ChatSource.
func (s *TelegramChatService) ListChats(ctx context.Context) ([]tui.Chat, error) {
	if s == nil || s.chats == nil {
		return nil, errors.New("chat service: nil source")
	}

	ctx, cancel := context.WithTimeout(ctx, chatServiceTimeout)
	defer cancel()

	snapshot, err := s.chats.GetChats(ctx, s.listLimit)
	if err != nil {
		return nil, fmt.Errorf("list chats: %w", err)
	}

	out := make([]tui.Chat, 0, len(snapshot.Chats))
	for _, c := range snapshot.Chats {
		out = append(out, s.chatOf(c))
	}

	return out, nil
}

// chatOf projects one chat summary into the row the interface draws.
func (s *TelegramChatService) chatOf(summary telegram.ChatSummary) tui.Chat {
	chat := tui.Chat{
		ID:                     int64(summary.ID),
		Title:                  summary.Title,
		Unread:                 summary.UnreadCount,
		Preview:                summary.LastMessageText,
		At:                     summary.LastMessageTime,
		LastReadInboxMessageID: int64(summary.LastReadInboxMessageID),
		Kind:                   tuiChatKind(summary),
	}

	if s.isOwnChat(summary) {
		// The chat with oneself is called "Saved Messages" by Telegram and by
		// the phone, and by this program too. TDLib sends the name of the
		// user for it, and a list whose third row says the account holder's
		// own name is a list where the person reading it has to recognise
		// themselves by their own name to find their own notes.
		chat.Title = ownChatTitle
		chat.Aliases = []string{ownChatAlias, ownChatTitle}
	}

	return chat
}

// tuiChatKind maps what Telegram says a chat is onto what the interface
// needs to know: whether a message names its author, and whether the
// unread badge of the row is in the accent or in the muted step.
func tuiChatKind(summary telegram.ChatSummary) tui.ChatKind {
	if summary.Kind.Grouped() {
		if summary.IsChannel {
			return tui.ChatKindChannel
		}

		return tui.ChatKindGroup
	}

	return tui.ChatKindPrivate
}

// isOwnChat reports whether a chat is the one with oneself.
//
// Telegram sends the current user as an ordinary user, so the only thing
// that tells the own chat from a chat with a contact is the identifier of
// the person on the other side. Zero means "not known" and then no chat
// is the own chat, which costs the alias and nothing else.
func (s *TelegramChatService) isOwnChat(summary telegram.ChatSummary) bool {
	if s == nil || s.ownUserID == 0 {
		return false
	}

	return summary.Kind == telegram.ChatKindPrivate && summary.PeerUserID == s.ownUserID
}

// LoadHistory implements tui.ChatSource.
//
// The TDLib-level limit is clamped to (0, 100]; when the caller passes
// a non-positive or out-of-range value, the service substitutes its
// default history limit.
//
// The newest page of a chat that answers empty while Telegram reports a
// last message in that chat is asked for again, a bounded number of times
// and with a pause between the asks. A chat with no last message is empty
// and is said to be, and a page that ends without an answer is reported as
// ErrHistoryNotLoaded rather than as a chat with nothing in it (#27).
func (s *TelegramChatService) LoadHistory(
	ctx context.Context,
	chatID int64,
	fromMessageID int64,
	limit int,
) (tui.HistoryPage, error) {
	if s == nil || s.chats == nil {
		return tui.HistoryPage{}, errors.New("chat service: nil source")
	}
	if limit <= 0 || limit > 100 {
		limit = s.historyLimit
	}

	ctx, cancel := context.WithTimeout(ctx, chatServiceTimeout)
	defer cancel()

	page, err := s.chats.GetChatHistory(
		ctx,
		telegram.ChatID(chatID),
		telegram.MessageID(fromMessageID),
		limit,
	)
	if err != nil {
		return tui.HistoryPage{}, fmt.Errorf("load history: %w", err)
	}

	// The chat is asked about once per page rather than once per message:
	// a personal chat and a channel are named by the chat itself, and
	// asking TDLib about the same chat fifty times to learn the same
	// thing fifty times is fifty round trips for one row.
	//
	// It is also what tells an empty newest page from an empty chat: the
	// chat carries a last message, and TDLib does not put one in a chat
	// that has nothing in it.
	chat := telegram.ChatSummary{ID: telegram.ChatID(chatID), Kind: telegram.ChatKindPrivate}
	if summary, err := s.chatOfID(ctx, chatID); err == nil {
		chat = summary
	}

	// An empty newest page of a chat Telegram says has a last message is
	// not the end of the conversation — it is a chat whose history TDLib
	// has not brought down from the server yet (#27). The owner opened a
	// channel that was in no local database and the whole of it read
	// "No messages yet" until the program was closed and opened again, at
	// which point the same chat was there: the messages had arrived in
	// between. A page that is empty because the chat is empty gets no
	// second question, so a chat a person has never written in says what
	// it is straight away.
	if fromMessageID == 0 && chat.LastMessageID != 0 && pageWithoutMessages(page) {
		page, err = s.awaitHistory(ctx, telegram.ChatID(chatID), limit)
		if err != nil {
			return tui.HistoryPage{}, err
		}
	}

	out := make([]tui.Message, 0, len(page.Messages))
	for _, m := range page.Messages {
		out = append(out, s.messageOf(ctx, chat, m))
	}

	return tui.HistoryPage{
		Messages:   out,
		NextFrom:   int64(page.NextFrom),
		HasMore:    page.HasMore,
		Unreadable: page.Unreadable,
	}, nil
}

// awaitHistory asks TDLib for the newest page of a chat again until it has
// something to say, or until the repeats are used up.
//
// The pause before every repeat is what makes the question worth asking: the
// request that comes back empty is the request that started the download, so
// asking again at once would ask about a download that has not begun. It is
// a wait and not a poll — the repeats are counted, and the count is the end
// of them.
//
// Everything that ends without a page is an error rather than an empty chat.
// Returning the empty page would say "this conversation has nothing in it"
// about a chat whose own last message is on the screen, and that sentence is
// the defect this answers.
func (s *TelegramChatService) awaitHistory(
	ctx context.Context,
	chatID telegram.ChatID,
	limit int,
) (telegram.HistoryPage, error) {
	wait := s.historyRetryWait
	if wait <= 0 {
		wait = historyNotLoadedRetryWait
	}

	for range historyNotLoadedRetries {
		if !sleepContext(ctx, wait) {
			return telegram.HistoryPage{}, fmt.Errorf(
				"load history: %w", ctx.Err(),
			)
		}

		page, err := s.chats.GetChatHistory(
			ctx,
			chatID,
			0,
			limit,
		)
		if err != nil {
			return telegram.HistoryPage{}, fmt.Errorf("load history: %w", err)
		}

		if !pageWithoutMessages(page) {
			return page, nil
		}

		wait = min(2*wait, historyNotLoadedRetryWaitMax)
	}

	return telegram.HistoryPage{}, fmt.Errorf(
		"%w: chat %d answered with an empty page %d times and Telegram"+
			" reports a last message in it",
		ErrHistoryNotLoaded,
		chatID,
		historyNotLoadedRetries+1,
	)
}

// pageWithoutMessages reports that an answer of getChatHistory held nothing
// to show: no message this build could read and no entry it could not.
//
// An answer whose entries were all unreadable is a page TDLib did send, and
// it is not the case this function is about. Counting it as empty would
// make a chat of messages of kinds this build has no words for wait five
// times for a page that is already there, and then be reported as a history
// that is not loaded.
func pageWithoutMessages(page telegram.HistoryPage) bool {
	return len(page.Messages) == 0 && page.Unreadable == 0
}

// sleepContext waits for d or until ctx is done, and reports whether the
// whole of it elapsed.
func sleepContext(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// chatOfID asks what kind of chat a chat is, and says what it does not
// know rather than pretending to know.
//
// A private chat is the smallest of the three claims the interface makes
// from a chat's type, so a chat it could not read about is drawn as a
// personal one: a message is not named and the row is in the accent.
func (s *TelegramChatService) chatOfID(
	ctx context.Context,
	chatID int64,
) (telegram.ChatSummary, error) {
	if s == nil || s.names == nil {
		return telegram.ChatSummary{
			ID:   telegram.ChatID(chatID),
			Kind: telegram.ChatKindPrivate,
		}, nil
	}

	return s.names.GetChat(ctx, telegram.ChatID(chatID))
}

// messageOf projects one message, resolving the name of whoever sent it.
func (s *TelegramChatService) messageOf(
	ctx context.Context,
	chat telegram.ChatSummary,
	message telegram.Message,
) tui.Message {
	return tui.Message{
		ID:       int64(message.ID),
		Outgoing: message.Outgoing,
		Text:     message.Text,

		// The moment and nothing else: the screen writes the time of day in
		// the format and the zone of the machine, and it places the row in
		// the conversation by the same moment. Spelling it out here is what
		// put 21:21 where Telegram says 07:21 for a reader ten hours east
		// of Greenwich (the owner, 29.09.2026).
		At:       message.Timestamp,
		Author:   s.authorOf(ctx, chat, message),
		AuthorID: message.Sender.ID,
		Media:    message.Media,

		// What the message carries is three fields and not one: the word,
		// whatever else the payload says about it, and the caption under
		// it. The screen puts the three in one pair of brackets and the
		// plural of an album of them where the word goes; see
		// internal/tui/view_conversation.go.
		MediaDetail: message.MediaDetail,
		Caption:     message.Caption,
		Service:     message.Service,
		AlbumID:     int64(message.MediaAlbumID),
	}
}

// authorOf returns the name to write above a message.
//
//   - a message of this user is signed "You", and the interface draws no
//     name above it at all: the block is on the right, which already says
//     whose it is;
//   - a personal chat and a channel are named by the chat itself, because
//     a channel's messages are all from the channel and a personal chat
//     has exactly one other person in it;
//   - a group names the person who sent it, and a sender this build
//     cannot name is "Unknown" rather than the name of the group.
//
// Every name is a local read answered by the adapter's memory, and none
// of them reaches a log, a report or an error message.
func (s *TelegramChatService) authorOf(
	ctx context.Context,
	chat telegram.ChatSummary,
	message telegram.Message,
) string {
	if message.Outgoing {
		return "You"
	}

	if !chat.Kind.Grouped() || chat.IsChannel {
		if chat.Title != "" {
			return chat.Title
		}
		if title := s.chatTitleOf(ctx, int64(chat.ID)); title != "" {
			return title
		}

		return unknownAuthor
	}

	switch message.Sender.Kind {
	case telegram.MessageSenderUser:
		return s.userNameOf(ctx, message.Sender.ID)

	case telegram.MessageSenderChat:
		if title := s.chatTitleOf(ctx, message.Sender.ID); title != "" {
			return title
		}

		return unknownAuthor

	default:
		return unknownAuthor
	}
}

// chatTitleOf returns the title of a chat, from memory where it has been
// read and from TDLib otherwise.
func (s *TelegramChatService) chatTitleOf(ctx context.Context, chatID int64) string {
	if chatID == 0 || s == nil {
		return ""
	}

	key := chatNameKey(chatID)
	if name, ok := s.names2.lookup(key); ok {
		return name
	}

	if s.names == nil {
		return ""
	}

	nameCtx, cancel := context.WithTimeout(ctx, authorNameTimeout)
	defer cancel()

	summary, err := s.names.GetChat(nameCtx, telegram.ChatID(chatID))
	if err != nil || summary.Title == "" {
		return ""
	}

	s.names2.remember(key, summary.Title)

	return summary.Title
}

// userNameOf returns the name of a person, from memory where it has been
// read and from TDLib otherwise.
func (s *TelegramChatService) userNameOf(ctx context.Context, userID int64) string {
	if userID == 0 || s == nil {
		return unknownAuthor
	}

	key := userNameKey(userID)
	if name, ok := s.names2.lookup(key); ok {
		return name
	}

	if s.names == nil {
		return unknownAuthor
	}

	nameCtx, cancel := context.WithTimeout(ctx, authorNameTimeout)
	defer cancel()

	name, err := s.names.GetUserName(nameCtx, userID)
	if err != nil || name == "" {
		return unknownAuthor
	}

	s.names2.remember(key, name)

	return name
}

// SendMessage implements tui.ChatSource.
//
// The TDLib-level validation of the chat ID and message text is
// performed by SendTextMessage. This adapter bounds the call duration
// and maps the Telegram message projection into the TUI projection.
func (s *TelegramChatService) SendMessage(
	ctx context.Context,
	chatID int64,
	text string,
) (tui.Message, error) {
	if s == nil || s.chats == nil {
		return tui.Message{}, errors.New("chat service: nil source")
	}

	ctx, cancel := context.WithTimeout(ctx, chatServiceTimeout)
	defer cancel()

	message, err := s.chats.SendTextMessage(
		ctx,
		telegram.ChatID(chatID),
		text,
	)
	if err != nil {
		return tui.Message{}, fmt.Errorf("send message: %w", err)
	}

	chat := telegram.ChatSummary{ID: telegram.ChatID(chatID), Kind: telegram.ChatKindPrivate}
	if summary, err := s.chatOfID(ctx, chatID); err == nil {
		chat = summary
	}

	return s.messageOf(ctx, chat, message), nil
}

// Compile-time assertion.
var _ tui.ChatSource = (*TelegramChatService)(nil)

// nameCache is the memory the adapter keeps the names it has read.
//
// It is a map and a queue rather than a store: a name is read from a local
// TDLib the first time a message from that sender is drawn, kept until
// the program stops, and never written anywhere. The live store holds no
// names by design (#41), and a cache that outlived the process would be a
// store by another name.
type nameCache struct {
	mu    sync.Mutex
	names map[string]string
	order []string
	limit int
}

// newNameCache returns a cache that holds at most limit names.
func newNameCache(limit int) *nameCache {
	if limit < 1 {
		limit = nameCacheLimit
	}

	return &nameCache{
		names: make(map[string]string),
		limit: limit,
	}
}

// lookup returns a name the cache holds.
func (c *nameCache) lookup(key string) (string, bool) {
	if c == nil {
		return "", false
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	name, ok := c.names[key]

	return name, ok
}

// remember keeps a name, and forgets the oldest half of the cache when it
// is over its limit.
//
// Half rather than one, because a chat list of ten people read back and
// forth would otherwise evict somebody the user is reading and ask TDLib
// for the same name again on the next frame. Which half is the oldest is
// the insertion order, so the names that are still in a conversation being
// read survive a limit that is reached.
func (c *nameCache) remember(key, name string) {
	if c == nil || name == "" {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if _, known := c.names[key]; !known {
		c.order = append(c.order, key)
	}

	c.names[key] = name

	if len(c.order) <= c.limit {
		return
	}

	drop := len(c.order) / 2
	for _, stale := range c.order[:drop] {
		delete(c.names, stale)
	}
	c.order = append([]string(nil), c.order[drop:]...)
}

// size returns how many names the cache holds.
func (c *nameCache) size() int {
	if c == nil {
		return 0
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	return len(c.names)
}

// The keys of the cache. A user and a chat are keyed apart, because their
// identifiers live in the same number space and 42 is a person in one
// message and a channel in the next.
const (
	userNameKeyPrefix = "user:"
	chatNameKeyPrefix = "chat:"
)

func userNameKey(userID int64) string {
	return userNameKeyPrefix + strconv.FormatInt(userID, 10)
}

func chatNameKey(chatID int64) string {
	return chatNameKeyPrefix + strconv.FormatInt(chatID, 10)
}

package application

import (
	"context"
	"errors"
	"fmt"
	"io"

	"telecli/internal/livewatch"
	"telecli/internal/telegram"
	"telecli/internal/tui"
)

// The live chat list reaches the interface through this adapter and nothing
// else.
//
// internal/tui may not import internal/telegram (internal/archdeps), and the
// reason is not a rule about tidiness: a TDLib type on a screen would put
// the schema of a wire format into the half of the program that draws
// words, and a change of the schema would then be a change of the
// interface. So the store is read here, in the composition root's layer, and
// what crosses the line is the four questions tui.ChatLiveSource asks.
//
// The adapter is also where the list is started. A live store is filled by
// the updates TDLib sends for a chat list that has been asked for
// (loadChats, ADR-0003 §3): without that question the store is empty, the
// interface reads an empty list, and the list on the screen is the one that
// was loaded at startup. LoadChats is asked once, here, before the program
// starts drawing.

// TelegramLiveUpdates is the live Telegram state as the interface reads it.
type TelegramLiveUpdates struct {
	store    LiveChatState
	names    TelegramSenderNames
	chatList int
	cache    *nameCache
}

// LiveChatState is the part of the live store the interface is drawn from.
//
// It is a narrow interface rather than *telegram.LiveState so that the
// adapter can be proved against recorded TDLib payloads without a session,
// and so that a field added to the store cannot silently change what the
// interface is told.
type LiveChatState interface {
	// Changed is the coalesced signal that something changed. It is never
	// closed: a program that has stopped reading it is a program that has
	// stopped, and the wait ends with its context.
	Changed() <-chan struct{}

	// ChatList returns the main chat list in Telegram's own order.
	ChatList() []telegram.LiveChat

	// MessageEventsSince returns the message events of a chat after a
	// cursor, the cursor to continue from, and whether the window could
	// not answer.
	MessageEventsSince(
		chatID telegram.ChatID,
		seq uint64,
	) (events []telegram.MessageEvent, next uint64, resync bool)
}

// NewTelegramLiveUpdates builds the adapter over a live store.
//
// The store may not be nil: an adapter with nothing to read would report a
// chat list that is not there, and the interface would draw a program with
// no chats in it as if that were the truth. A program with no store does not
// build this at all, and says so in the status line instead.
//
// names is optional and is the reader of the one thing the store
// deliberately does not keep: the name of whoever sent a message (#41). A
// message in a group is signed with it, so an adapter without a reader
// draws "Unknown" above the messages of a live conversation — which is the
// truth about a name nobody could read, and not about the live list.
func NewTelegramLiveUpdates(
	store LiveChatState,
	names TelegramSenderNames,
) (*TelegramLiveUpdates, error) {
	if store == nil {
		return nil, errors.New("live updates: store is required")
	}

	return &TelegramLiveUpdates{
		store:    store,
		names:    names,
		chatList: defaultChatListLimit,
		cache:    newNameCache(nameCacheLimit),
	}, nil
}

// liveChatListLoads is how many times loadChats is asked before the start of
// the list is given up on.
//
// TDLib answers "ok" while more of the list remains and an error once the
// whole of it is known, so one call is a page of the list rather than the
// list. Twenty pages of fifty is a thousand chats, which is more than any
// account has; the bound is here so that a client that never says "the list
// is complete" cannot hold the start of the program in a loop of round
// trips.
const liveChatListLoads = 20

// Load asks TDLib for the chat list, which is what fills the store.
//
// It is asked before the interface draws anything, because until TDLib has
// sent the updates of the list the store is empty and there is nothing live
// to read: a chat list that moves needs the list to be there first.
//
// A refusal is reported rather than swallowed, and the interface is not told
// about it: the cause can name a file and a path, and the status line says
// what a user can do about it (R reloads the list) rather than why it
// failed. What arrived before the refusal stays in the store, and the rest
// of the list arrives with the updates that follow it.
func (u *TelegramLiveUpdates) Load(
	ctx context.Context,
	loader LiveChatLoader,
	report io.Writer,
) error {
	if u == nil || loader == nil {
		return errors.New("live updates: no loader to ask for the chat list")
	}

	for range liveChatListLoads {
		if err := ctx.Err(); err != nil {
			return err
		}

		complete, err := loader.LoadChats(ctx, u.chatList)
		if err != nil {
			if report != nil {
				fmt.Fprintf(
					report,
					"telegram live chat list unavailable, the chat list will"+
						" not update by itself: %v\n", err,
				)
			}

			return fmt.Errorf("load chats into the live state: %w", err)
		}
		if complete {
			return nil
		}
	}

	return nil
}

// LiveChatLoader asks TDLib to send the updates of the main chat list.
//
// It is the part of the session the start of the list needs, named here so
// that the question is asked in one place and can be answered by a fake.
type LiveChatLoader interface {
	LoadChats(ctx context.Context, limit int) (complete bool, err error)
}

// Available implements tui.ChatLiveSource.
func (u *TelegramLiveUpdates) Available() bool {
	return u != nil && u.store != nil
}

// WaitForChange implements tui.ChatLiveSource.
//
// It blocks on the store's coalesced signal and on the program's context,
// and nothing else: the read of the state happens in the interface's own
// goroutine when the loop delivers the message, so the pump that writes the
// store is never waited on here.
//
// What woke it is written to the diagnostic of the live list when one is
// named, because a wait that never returns is the first step a report of a
// list that does not move has to rule out (#47).
func (u *TelegramLiveUpdates) WaitForChange(ctx context.Context) {
	if !u.Available() {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}

	select {
	case <-u.store.Changed():
		livewatch.WaitReturned("change")
	case <-ctx.Done():
		livewatch.WaitReturned("context")
	}
}

// Chats implements tui.ChatLiveSource.
func (u *TelegramLiveUpdates) Chats() []tui.LiveChat {
	if !u.Available() {
		return nil
	}

	list := u.store.ChatList()
	chats := make([]tui.LiveChat, 0, len(list))
	for _, chat := range list {
		chats = append(chats, u.chatOf(chat))
	}

	return chats
}

// chatOf projects one chat of the live store into the row the interface
// draws.
//
// The preview is the last message of the chat composed the way the row of a
// loaded list composes it, and the moment is the moment of that message: a
// row whose preview is the newest message and whose time is the oldest is a
// row about two different conversations.
//
// The pin and the mute cross the line with the rest, because they are the
// two answers a row of a chat list gives about the chat itself: the pin says
// why the chat is where it is, and the mute is what keeps it out of the
// number in the header. Neither is read from the name or guessed from the
// kind — a channel is not silenced because it is a channel.
func (u *TelegramLiveUpdates) chatOf(chat telegram.LiveChat) tui.LiveChat {
	row := tui.LiveChat{
		ID:     int64(chat.ID),
		Title:  chat.Title,
		Unread: chat.UnreadCount,
		Pinned: chat.Pinned,
		Muted:  chat.Muted,
		Kind:   liveChatKind(chat),
	}

	if chat.LastMessage == nil {
		return row
	}

	row.Preview = chat.LastMessage.PreviewLine()
	row.At = chat.LastMessage.Timestamp

	return row
}

// liveChatKind maps what the store knows about a chat onto what the badge
// of a row needs: whether the chat has more than one person in it.
//
// The store knows that a supergroup is a supergroup and not whether it is a
// channel, so a channel that arrives with a live update is drawn as a group
// until a loaded list says otherwise. That is the smaller of the two
// mistakes: the badge of the row is a colour, and the message that arrived
// is on the screen either way. A row the model already holds keeps the kind
// it was given, which is the one TDLib was asked about.
func liveChatKind(chat telegram.LiveChat) tui.ChatKind {
	if chat.Grouped {
		return tui.ChatKindGroup
	}

	return tui.ChatKindPrivate
}

// MessageEvents implements tui.ChatLiveSource.
func (u *TelegramLiveUpdates) MessageEvents(
	chatID int64,
	cursor uint64,
) tui.LiveMessageEvents {
	if !u.Available() || chatID == 0 {
		return tui.LiveMessageEvents{}
	}

	events, next, resync := u.store.MessageEventsSince(
		telegram.ChatID(chatID), cursor,
	)

	out := tui.LiveMessageEvents{
		Cursor: next,
		Resync: resync,
		Events: make([]tui.LiveMessageEvent, 0, len(events)),
	}
	for _, event := range events {
		switch changed := event.(type) {
		case telegram.MessageAdded:
			out.Events = append(out.Events, tui.LiveMessageEvent{
				Kind:    tui.LiveMessageAdded,
				Message: u.messageOf(changed.Message),
			})

		case telegram.MessageReplaced:
			out.Events = append(out.Events, tui.LiveMessageEvent{
				Kind:    tui.LiveMessageReplaced,
				OldID:   int64(changed.OldID),
				Message: u.messageOf(changed.Message),
			})

		case telegram.MessagesDeleted:
			ids := make([]int64, 0, len(changed.IDs))
			for _, id := range changed.IDs {
				ids = append(ids, int64(id))
			}
			out.Events = append(out.Events, tui.LiveMessageEvent{
				Kind: tui.LiveMessageDeleted,
				IDs:  ids,
			})

		default:
			// MessageFailed is the durable queue's business and not the
			// interface's: a send that failed is a record in the outbox,
			// and the row under the message says so from that record
			// (ADR-0003, out of scope).
		}
	}

	return out
}

// messageOf projects one message of the live store into a message the
// interface draws, and signs it with the name of whoever sent it.
func (u *TelegramLiveUpdates) messageOf(message telegram.Message) tui.Message {
	return tui.Message{
		ID:          int64(message.ID),
		Outgoing:    message.Outgoing,
		Text:        message.Text,
		At:          message.Timestamp,
		Author:      u.authorOf(message),
		AuthorID:    message.Sender.ID,
		Media:       message.Media,
		MediaDetail: message.MediaDetail,
		Caption:     message.Caption,
		Service:     message.Service,
		AlbumID:     int64(message.MediaAlbumID),
	}
}

// authorOf returns the name to write above a message of the live store.
//
// It is the same rule the chat service follows for a page of history: a
// message of this user is signed "You", a person is signed with the name
// TDLib gives for them and a channel with its own, and a sender this build
// cannot name is "Unknown" rather than the name of the chat.
//
// Every name is read from a local TDLib and kept in this adapter's memory
// for as long as the program runs. None of them reaches the store, a log
// line, a report or an error message (#41).
func (u *TelegramLiveUpdates) authorOf(message telegram.Message) string {
	if message.Outgoing {
		return "You"
	}
	if u == nil || u.names == nil {
		return unknownAuthor
	}

	switch message.Sender.Kind {
	case telegram.MessageSenderUser:
		return u.nameOf(userNameKey(message.Sender.ID), func() string {
			name, err := u.names.GetUserName(context.Background(), message.Sender.ID)
			if err != nil {
				return ""
			}

			return name
		})

	case telegram.MessageSenderChat:
		return u.nameOf(chatNameKey(message.Sender.ID), func() string {
			summary, err := u.names.GetChat(
				context.Background(), telegram.ChatID(message.Sender.ID),
			)
			if err != nil {
				return ""
			}

			return summary.Title
		})

	default:
		return unknownAuthor
	}
}

// nameOf returns a name from the cache, reading it from TDLib when it is
// not there.
//
// A read that fails leaves the cache alone and answers "Unknown": the name
// above a message is decoration around a message the user can already read,
// and a chat that waits for it is a chat that has stopped drawing.
func (u *TelegramLiveUpdates) nameOf(key string, read func() string) string {
	if name, ok := u.cache.lookup(key); ok {
		return name
	}

	name := read()
	if name == "" {
		return unknownAuthor
	}

	u.cache.remember(key, name)

	return name
}

var _ tui.ChatLiveSource = (*TelegramLiveUpdates)(nil)

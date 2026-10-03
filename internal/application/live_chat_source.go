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
//
// The name of whoever sent a message is asked here too, and it is asked in
// the background (live_sender_names.go): the interface reads the events of a
// chat from the loop of the program, and a loop that stands in TDLib's door
// until TDLib answers is a screen that neither draws nor takes keys. A
// message whose sender has no name yet goes out with the placeholder above
// it, and the name is handed over as one more event of that chat.

// TelegramLiveUpdates is the live Telegram state as the interface reads it.
type TelegramLiveUpdates struct {
	store    LiveChatState
	names    TelegramSenderNames
	chatList int
	cache    *nameCache
	senders  *liveSenderNames

	// unnamed are the messages that went to the interface with a placeholder
	// above them, and the key of the name that is being asked for. The
	// answer is handed over as one more event of that chat, which is how a
	// name replaces the placeholder without the interface having asked for
	// it again.
	//
	// It is a memory and not a store: an entry is dropped as soon as the
	// name arrives or the message is gone, the whole of it is emptied when
	// it grows past the names the cache holds, and nothing here is ever
	// written to disk, to a log or to a report.
	//
	// Only the loop of the interface touches it, in the one call it makes
	// per redraw, so it needs no lock of its own; the names it points at do
	// (nameCache).
	unnamed map[unnamedKey]unnamedMessage
}

// unnamedKey is which message of which chat is waiting for a name.
type unnamedKey struct {
	chat    int64
	message int64
}

// unnamedMessage is the message the interface already holds and the key of
// the name that is on its way for it.
type unnamedMessage struct {
	sender  string
	message tui.Message
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
// ctx is the program's context, and it is what every read of a sender's name
// is made under (live_sender_names.go): the interface asks for names and
// never waits for one, so the reads of them are the ones that have to be
// called off when the program leaves.
//
// names is optional and is the reader of the one thing the store
// deliberately does not keep: the name of whoever sent a message (#41). A
// message in a group is signed with it, so an adapter without a reader
// draws "Unknown" above the messages of a live conversation — which is the
// truth about a name nobody could read, and not about the live list.
func NewTelegramLiveUpdates(
	ctx context.Context,
	store LiveChatState,
	names TelegramSenderNames,
) (*TelegramLiveUpdates, error) {
	if store == nil {
		return nil, errors.New("live updates: store is required")
	}

	cache := newNameCache(nameCacheLimit)

	return &TelegramLiveUpdates{
		store:    store,
		names:    names,
		chatList: defaultChatListLimit,
		cache:    cache,
		senders:  newLiveSenderNames(ctx, names, cache),
		unnamed:  make(map[unnamedKey]unnamedMessage),
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
// and on one more thing: a name of a sender that has arrived. A message is
// drawn with a placeholder above it while its name is being read, and the
// answer is delivered to the interface as another event of that chat — so
// without this the placeholder above it would stay until Telegram happened
// to send the next update of anything. A reader with no names has no such
// signal, and a nil one is a case of this wait that never fires.
//
// The read of the state itself happens in the interface's own goroutine when
// the loop delivers the message, so the pump that writes the store is never
// waited on here — and neither is TDLib, which is what this wait was for.
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
	case <-u.senders.changes():
		livewatch.WaitReturned("name")
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
//
// It is called from the loop of the interface, and it waits for nothing: the
// events come from memory, and the name of a sender whose name is not known
// yet is asked for in the background and handed over with a later read of
// this chat (withNames). What is left on the screen while a name is on its
// way is the placeholder, not a screen that has stopped drawing.
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
				Message: u.messageOf(chatID, changed.Message),
			})

		case telegram.MessageReplaced:
			out.Events = append(out.Events, tui.LiveMessageEvent{
				Kind:    tui.LiveMessageReplaced,
				OldID:   int64(changed.OldID),
				Message: u.messageOf(chatID, changed.Message),
			})

		case telegram.MessagesDeleted:
			ids := make([]int64, 0, len(changed.IDs))
			for _, id := range changed.IDs {
				// A message that is gone must not come back as the
				// answer to a question about its sender.
				delete(u.unnamed, unnamedKey{chat: chatID, message: int64(id)})

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

	return u.withNames(chatID, out)
}

// withNames hands the interface the names that have arrived since it last
// read this chat, as one more event per message they belong to.
//
// The event is the one a replaced message already is: the same row, drawn
// again with the name above it. The interface puts it where the message
// stands, so a name that arrives replaces the placeholder in place and the
// message itself neither moves nor counts as a new one.
//
// An entry is dropped as it is handed over, so a name is delivered once. An
// entry whose name has not arrived is kept, and the message it belongs to
// keeps the placeholder above it until it does — the interface is not asked
// anything in the meantime, because it is the one that is not waiting.
func (u *TelegramLiveUpdates) withNames(
	chatID int64,
	out tui.LiveMessageEvents,
) tui.LiveMessageEvents {
	for key, waiting := range u.unnamed {
		if key.chat != chatID {
			continue
		}

		name, known := u.cache.lookup(waiting.sender)
		if !known {
			continue
		}

		delete(u.unnamed, key)

		waiting.message.Author = name
		out.Events = append(out.Events, tui.LiveMessageEvent{
			Kind:    tui.LiveMessageReplaced,
			OldID:   key.message,
			Message: waiting.message,
		})
	}

	return out
}

// rememberUnnamed keeps a message whose sender has no name yet, so that the
// name can replace the placeholder above it when it arrives.
//
// What is kept is the message the interface already holds and the key of the
// name that is on its way for it — the key, and not a name, because the same
// sender writes many messages and one read of one name is what answers for
// all of them (live_sender_names.go).
//
// The memory is emptied rather than grown when it is over its limit. What it
// held keeps its placeholder until its chat is read again, which is the
// smaller of the two mistakes: a program that has been running for days must
// not hold every message whose name never came, and a name that is late is
// decoration over a message a reader can read either way.
func (u *TelegramLiveUpdates) rememberUnnamed(
	chatID, messageID int64,
	sender string,
	message tui.Message,
) {
	if sender == "" {
		return
	}

	key := unnamedKey{chat: chatID, message: messageID}

	if message.Author != unknownAuthor {
		// The name was known when this message went to the interface, so
		// there is nothing left to answer for it: it is either the name
		// from the cache, or a sender this build cannot name at all.
		delete(u.unnamed, key)

		return
	}

	if _, waiting := u.unnamed[key]; waiting {
		return
	}

	if len(u.unnamed) >= nameCacheLimit {
		clear(u.unnamed)
	}

	u.unnamed[key] = unnamedMessage{sender: sender, message: message}
}

// messageOf projects one message of the live store into the message the
// interface draws, and asks for the name of whoever sent it when there is
// none yet.
func (u *TelegramLiveUpdates) messageOf(
	chatID int64,
	message telegram.Message,
) tui.Message {
	author, sender := u.authorOf(message)

	projected := tui.Message{
		ID:          int64(message.ID),
		Outgoing:    message.Outgoing,
		Text:        message.Text,
		At:          message.Timestamp,
		Author:      author,
		AuthorID:    message.Sender.ID,
		Media:       message.Media,
		MediaDetail: message.MediaDetail,
		Caption:     message.Caption,
		Service:     message.Service,
		AlbumID:     int64(message.MediaAlbumID),
	}
	u.rememberUnnamed(chatID, projected.ID, sender, projected)

	return projected
}

// authorOf returns the name to write above a message of the live store, and
// the key of the name it is read from.
//
// It is the same rule the chat service follows for a page of history: a
// message of this user is signed "You", a person is signed with the name
// TDLib gives for them and a channel with its own, and a sender this build
// cannot name is "Unknown" rather than the name of the chat.
//
// A name that is not in the adapter's memory is asked for in the background
// and is not waited for: the message goes to the interface with the
// placeholder above it, and the answer comes back as another event of that
// chat (withNames). The key is empty where there is nothing to ask for — a
// message of this user, a sender this build cannot name, a session that
// cannot answer — and nothing is remembered for it.
//
// Every name is read from a local TDLib and kept in this adapter's memory
// for as long as the program runs. None of them reaches the store, a log
// line, a report or an error message (#41).
func (u *TelegramLiveUpdates) authorOf(message telegram.Message) (string, string) {
	if message.Outgoing {
		return "You", ""
	}
	if u == nil || u.names == nil {
		return unknownAuthor, ""
	}

	switch message.Sender.Kind {
	case telegram.MessageSenderUser:
		sender := userNameKey(message.Sender.ID)
		if name, known := u.cache.lookup(sender); known {
			return name, sender
		}

		u.senders.askUserName(message.Sender.ID)

		return unknownAuthor, sender

	case telegram.MessageSenderChat:
		sender := chatNameKey(message.Sender.ID)
		if name, known := u.cache.lookup(sender); known {
			return name, sender
		}

		u.senders.askChatName(telegram.ChatID(message.Sender.ID))

		return unknownAuthor, sender

	default:
		return unknownAuthor, ""
	}
}

var _ tui.ChatLiveSource = (*TelegramLiveUpdates)(nil)

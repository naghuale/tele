# ADR-0003: Live updates through a coalescing state store

## Status

Accepted.

## Context

The TUI shows a snapshot. The chat list is fetched once through
`getChats` plus one `getChat` per ID, and a chat's history is fetched
when the chat is opened. Nothing that happens afterwards reaches the
screen: incoming messages, new chats, reordering, unread counters, and
the final ID of a message the user sent.

TDLib pushes all of this as updates. Today they are lost on purpose:

- `Runtime` delivers updates to each logical client with backpressure
  (`internal/telegram/runtime.go`, blocking send), so nothing is lost
  between TDLib and the session.
- `AuthorizedSession.run` routes query responses and authorization
  states, and forwards everything else to `s.updates` (buffer 256) with a
  non-blocking send that drops on overflow
  (`internal/telegram/session.go`). No component reads `s.updates`.

The pump cannot simply block on a slow consumer: query responses travel
through the same pump, so a stalled TUI would stall every
`getChatHistory` and `sendMessage` and could deadlock a consumer that
waits for a query while holding up the updates it has not read.

TDLib itself guarantees that a client receives every update in order.
Any gap is therefore created inside telecli, at a buffer we own.

## Decision

The session pump applies updates synchronously to an in-memory state
store. Consumers are told *that* the state changed, not *what* changed,
and read the state themselves.

### 1. The pump is the only writer

`AuthorizedSession.run` hands every non-query, non-authorization update
to a `LiveState` store owned by the session. Applying an update is
in-memory work only (JSON decode, map and slice operations, no I/O, no
channel sends that can block), so the pump never waits on a consumer.

The lossy `s.updates` channel is removed. `AuthorizedSession.Updates()`
goes with it; the store is the only application-facing view of updates.

### 2. Change notification is coalesced and cannot be lost

The store owns `changed chan struct{}` with capacity 1. After applying
an update that changed state, the pump does a non-blocking send. A full
channel already means "something changed since you last looked", so a
dropped signal loses nothing. A consumer waits on the channel, then
reads a snapshot.

Memory is bounded by the amount of state, not by the update rate.

### 3. Chat list: the store is the source of truth

The main chat list is built from updates, as TDLib documents:

- `updateNewChat` adds a chat (title, positions, last message, unread
  count);
- `updateChatTitle`, `updateChatLastMessage`, `updateChatReadInbox`
  update fields;
- `updateChatPosition` for `chatListMain` sets or removes the chat's
  `order`; a chat with order 0 or no main-list position is not listed.
  `updateChatLastMessage` and `updateChatDraftMessage` also carry
  positions and are applied the same way.

Order is TDLib's `position.order` (int64 carried as a JSON string),
descending, ties broken by chat ID descending. `ChatList()` returns a
copy sorted by that order.

`loadChats(chatListMain, limit)` asks TDLib to send the updates for the
next part of the list. It returns `ok` while more chats remain and a 404
error when the whole list is known; 404 is not a failure.

`getChats` and the `GetChat` fan-out are removed from the chat list
path once the store-backed list works.

### 4. Messages: a bounded event window per chat with a sequence

For messages the store keeps events, not a copy of the history:

- `updateNewMessage` → `MessageAdded` (incoming and outgoing; an
  outgoing message first appears with a temporary ID);
- `updateMessageSendSucceeded` → `MessageReplaced{OldID, Message}`;
- `updateMessageSendFailed` → `MessageFailed{OldID, Message, Error}`;
- `updateDeleteMessages` with `is_permanent = true` →
  `MessagesDeleted{IDs}`; non-permanent deletions only leave TDLib's
  cache and are ignored.

Each chat has a monotonic sequence number and keeps the last 256
events. A consumer calls `MessageEventsSince(chatID, seq)` and gets
either the events after `seq` or `Resync`, when `seq` is older than the
window. On `Resync` the consumer reloads the first history page, exactly
like opening the chat, and continues from the current sequence.

Overflow therefore degrades into a reload, never into silent loss.

### 5. The TUI stays independent of Telegram

`internal/tui` still imports nothing from `internal/telegram`. The
application adapter maps store snapshots and events to TUI types. The
TUI gets a `tea.Cmd` that waits for the next change notification and
returns a message, re-issued after each change, so the Bubble Tea loop
stays the only owner of `Model`.

The selection in the chat list follows the chat ID, not the index,
because the list is reordered under the cursor.

### 6. Temporary message IDs

`sendMessage` returns a message with a temporary ID. The TUI keeps it
until `MessageReplaced` arrives with that `OldID`, then replaces it in
place. `MessageAdded` for an ID the TUI already holds (the same outgoing
message seen through both the send response and `updateNewMessage`) is
a no-op, which the existing dedup by ID already provides.

## Out of scope

- Chat lists other than the main one (archive, folders).
- Message edits, reactions, typing, online status, mentions.
- Feeding `updateMessageSendSucceeded`/`Failed` into the durable
  outbox's delivery state. The store exposes the events; wiring them
  into the outbox is a separate decision.
- Persisting the store. It is rebuilt from TDLib's local database on
  every start.

## Alternatives considered

- **Unbounded queue between pump and consumer.** Lossless, but memory
  grows without limit while the TUI is busy or suspended, and every
  consumer must replay every event.
- **Bounded queue with drop and a resync marker for everything.** Works,
  but the chat list would be reloaded through `getChats` after each
  overflow, which is the snapshot path we are trying to leave. For the
  chat list, applying state directly is simpler and exact.
- **Blocking send to the consumer.** Rejected: query responses share the
  pump, so a slow consumer would stall queries (see Context).

## Consequences

- One goroutine writes the store, many read it: the store needs a
  `sync.RWMutex`, and readers get copies.
- Unit tests can drive the store with raw TDLib JSON fixtures and no
  native library.
- Behaviour that must be confirmed against the pinned TDLib 1.8.67 in
  the manual integration step: `loadChats` returns 404 at the end of the
  list; `updateNewMessage` is sent for outgoing messages with the
  temporary ID; `updateMessageSendSucceeded` carries `old_message_id`.

## Implementation plan

Steps 3 and 4 wait until the PR-10A interface from docs/TUI_SPEC.md has
landed, so the screens are not rewritten twice (TUI_SPEC decision 2).
Steps 1 and 2 are independent of it.

Each step is its own issue and PR, in this order:

1. **Store and pump wiring (chat list).** `LiveState` in
   `internal/telegram`, chat-list updates, `changed` channel,
   `LoadChats`, removal of `s.updates`. Unit tests with JSON fixtures.
   The query tests that read foreign-`@extra` responses from
   `session.Updates()` (`internal/telegram/query_test.go`) move to the
   store: such a response must still not be swallowed as a query reply.
2. **Message events.** Per-chat sequence, 256-event window,
   `MessageEventsSince` with `Resync`. Unit tests.
3. **Live chat list in the TUI.** Adapter, change-wait command, chat
   list rendered from the store, selection kept by chat ID.
4. **Live messages in the open chat.** Apply events, replace temporary
   IDs, reload on `Resync`.
5. **Manual real-account integration** for the behaviours listed under
   Consequences, and PROJECT_FACTS.md updated.

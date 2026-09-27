# telecli project facts

## Repository
- Repository: local
- Default branch: main
- Go module: telecli
- Minimum Go version: 1.27
- License: TBD

## Supported platforms
- Development platform: macOS arm64
- Supported runtime platforms:
  - macOS arm64 (verified in PR-04, PR-05, PR-07)
  - macOS amd64
  - Linux amd64 (unit tests in CI; native TDLib not yet verified)
  - Linux arm64 (cross-compiled in development; not yet in CI)
- Required build feature: CGO (for TDLib)
- Builds without CGO: supported, but TDLib runtime is unavailable
- CI: .github/workflows/ci.yml runs gofmt, go vet, race tests, a
  telecli_dev test build and a cgo-free build on macOS and Linux, and
  golangci-lint v2 (.golangci.yml) built with the module Go version
- Windows: unsupported in PR-04 / PR-05 / PR-07

## CLI and TUI
- CLI framework: standard library flag
- TUI framework: github.com/charmbracelet/bubbletea v1.3.10
- Styling library: github.com/charmbracelet/lipgloss v1.1.0, through a
  private renderer built for the resolved colour profile; the theme
  package itself imports no Lip Gloss style and changes no global state
  (github.com/muesli/termenv is a direct dependency for the same
  reason: Lip Gloss reports its profile as a termenv value)
- Terminal cell measurement: github.com/charmbracelet/x/ansi v0.10.1
  (direct since PR-10A.2), for StringWidth and Truncate: every width in
  the layout is counted in terminal columns, and a rune count overflows
  the screen on the first non-ASCII chat name
- Configuration library: github.com/BurntSushi/toml
- Configuration format: TOML
- Logging library: standard library log/slog (outbox dispatcher)

## TDLib
- Interface: modern JSON C API
- Binding: internal dynamic cgo wrapper (internal/telegram/tdjson)
- Pinned repository: https://github.com/tdlib/td
- Pinned commit: ea97bcdd3a15523c58ddfe772b4547187cf5bbeb
- Expected TDLib version: 1.8.67
- Verified TDLib version: 1.8.67
- Verified TDLib commit: ea97bcdd3a15523c58ddfe772b4547187cf5bbeb
- Build method: source CMake, shared tdjson target
- Native entry points:
  - td_create_client_id
  - td_send
  - td_receive
  - td_execute
- Runtime library discovery:
  1. TELECLI_TDLIB_LIBRARY environment variable
  2. tdlib.library_path from config
  3. packaged path: ../lib/libtdjson.* next to the executable
  4. repository development path, only in builds with
     `-tags telecli_dev`:
     - third_party/tdlib/lib/libtdjson.dylib (macOS)
     - third_party/tdlib/lib/libtdjson.so (Linux)
  5. platform locations:
     - macOS: /opt/homebrew/lib, /usr/local/lib (absolute only; a bare
       leaf name would make dlopen fall back to the working directory)
     - Linux: system loader default
  - a release build never resolves a candidate from the working
    directory
- Runtime version source: synchronous getOption("version")
- Runtime commit source: synchronous getOption("commit_hash")
- Compatibility manifest: internal/telegram/manifest.go
- Compatibility status: verified on macOS arm64 against the pinned commit above
- Receive ownership: exactly one process-wide td_receive loop per Runtime
- Receive errors: backoff from 10ms doubling to 1s; after 32
  consecutive errors the runtime becomes failed, closes client channels,
  and rejects Send with ErrRuntimeFailed; Close still releases the
  native handle
- Client routing: by @client_id envelope field
- Initial client activation: Authorize sends getAuthorizationState
  immediately after NewClient. In the verified TDLib 1.8.67 runtime,
  a freshly created logical client produced no initial authorization
  object before its first request. getAuthorizationState is an
  offline method permitted before initialization.
- Verified behaviors (macOS arm64):
  - td_create_client_id
  - td_send
  - td_receive
  - td_execute
  - getOption("version") == 1.8.67
  - getOption("commit_hash") == ea97bcdd3a15523c58ddfe772b4547187cf5bbeb
  - graceful close to authorizationStateClosed
  - pinned runtime emitted authorizationStateClosing before
    authorizationStateClosed (first and second runs of the automated
    close integration)
  - database initialization after prior graceful close
  - real-account authorization through phone, code, and 2FA password
  - sendMessage accepted by TDLib and returned a message object
  - returned message had a non-zero ID, matching chat ID, outgoing
    flag, matching text, and non-zero timestamp
  - authorized session closed successfully after the send operation
- Unsupported in PR-04 / PR-05 / PR-07:
  - authorization states beyond phone, code, password (email,
    registration, other-device confirmation, premium purchase)
  - live chat-list updates (loadChats, updateNewChat,
    updateChatTitle, updateChatLastMessage, updateChatPosition,
    updateChatReadInbox)
  - message history pagination beyond the first page
  - final delivery to recipient confirmation
  - delivery-state tracking through updateMessageSendSucceeded and
    updateMessageSendFailed
  - logout flow beyond close
  - Linux runtime close verification

## Authorization lifecycle
- Coordinator: internal/telegram/auth_coordinator.go
  - single owner of Client.Updates() and Client.Errors() during auth
  - terminal states: Ready, Closed, Closing, LoggingOut
  - unsupported states abort with ErrUnsupportedAuthState
  - empty provider input aborts with ErrInvalidAuthParameters
  - runtime errors propagated via errors.Is
- Parser: internal/telegram/auth.go
  - accepts updateAuthorizationState envelope and direct
    authorizationState* response from getAuthorizationState
  - unknown authorizationState* → ErrUnsupportedAuthState
  - unrelated update → (AuthStateUnknown, nil)
  - malformed JSON → parse error
- Auth session: internal/telegram/session.go
  - Authorize sends getAuthorizationState immediately after NewClient
    to activate a dormant logical client
  - single owner of Client.Updates() and Client.Errors() after Ready
  - active pump goroutine started by Authorize before it returns
  - non-query, non-authorization updates are applied to LiveState
    (internal/telegram/live_state.go), the only application-facing
    view of updates; applying is in-memory only, so the pump never
    waits on a reader
  - the lossy session update channel and Updates() are removed; nothing
    TDLib sends is dropped on the way out
  - updates that arrived during authorization are held on AuthResult
    and applied to LiveState before the pump's first update, so a chat
    first seen before Ready is not lost; only the update types the
    store consumes are held, which bounds the slice by the chat list.
    Message events are applied but never held, since the TUI starts from
    the first history page (ADR-0003 step 2)
  - runtime errors still forwarded to a session-facing Errors() channel
  - graceful close observes authorizationStateClosed as the required
    terminal state
  - close request is sent before the pump is cancelled
- Graceful close sequence (verified on macOS arm64):
  1. AuthorizedSession.Close sends {"@type":"close"}
  2. TDLib emits authorizationStateClosing
  3. TDLib emits authorizationStateClosed
  4. session cancels the pump and waits for pumpDone
  5. Runtime.Close stops the process-wide receive loop on an
     independent shutdown context
  6. the native handle is released; libtdjson stays loaded because
     TDLib keeps process-wide threads and cannot be safely unloaded
  7. same database directory initializes successfully in a second
     runtime
- Close sentinels:
  - ErrSessionCloseTimeout: authorizationStateClosed not observed
    within the internal shutdown timeout
  - ErrSessionUpdatesClosed: update channel closed before
    authorizationStateClosed was seen
  - ErrSessionNilClient: nil session or nil client
  - ErrSessionNilSender: nil sender during Close
- Caller context semantics:
  - the caller context bounds the wait, not the close sequence
  - a short caller context does not poison the internal sequence
  - the internal close runs on a lifecycle context derived from
    runtime.shutdown_timeout
- Auth failure cleanup:
  - Authorize closes the newly created TDLib client if authorization
    fails, is cancelled, or hits an unsupported state
  - close is issued on a fresh context so a canceled caller context
    cannot prevent the runtime from shutting down

## Chat and message lifecycle
- Correlated queries: internal/telegram/query.go
  - @extra-based request/response correlation
  - pendingQuery registry guarded by queryMu
  - responses with telecli-owned @extra never reach LiveState
  - foreign @extra (string, object, or non-string) is treated as a
    normal application update
  - late responses to cancelled queries are consumed silently
  - TDLib error objects decode into *TDLibError with
    ErrTDLibResponse preserved for errors.Is
- Chat projection: internal/telegram/chats.go
  - GetChats fetches an ordered snapshot and materializes each chat
    via GetChat
  - ChatID is int53; only zero is rejected locally
  - getChats limit is a telecli policy (maxChatListLimit = 1000) and
    performs one GetChat per returned id
  - GetChat validates response @type and id match the request
  - messageText previews extracted; other content types use a
    placeholder such as [photo] or [unsupported message]; the
    message ID is always preserved
  - still the snapshot path the TUI uses; the live store below is not
    wired into the chat list yet (ADR-0003 step 3)
- Live chat-list store: internal/telegram/live_state.go (ADR-0003 step 1)
  - LiveState is the pump's single-writer store; readers take copies
  - Changed() is a capacity-1 channel signalled non-blockingly after an
    update that changed state, so signals coalesce and cannot be lost
  - ChatList() returns the main list ordered by (order, chat ID)
    descending; a chat with no chatListMain position or order 0 is
    excluded
  - applied updates: updateNewChat, updateChatTitle, updateChatPosition,
    updateChatLastMessage, updateChatDraftMessage (positions only),
    updateChatReadInbox
  - the message updates below are routed by the same apply and never
    move the list: the two projections are independent, and a chat
    update spends no message sequence
  - position order is a JSON string int64; a value that does not parse
    reports ErrLiveStateOrder and leaves the state unchanged
  - an update for an unknown chat creates no record; other update types
    are ignored without an error or a signal
  - LoadChats(ctx, limit) returns complete=true on TDLib error 404 and
    an error for anything else; limit validated as in GetChats
- Live message events: internal/telegram/live_messages.go
  (ADR-0003 step 2)
  - a closed set of four events with an unexported marker method, so a
    consumer's type switch is exhaustive: MessageAdded{Message},
    MessageReplaced{OldID, Message},
    MessageFailed{OldID, Message, Error}, MessagesDeleted{IDs}
  - updateNewMessage → MessageAdded for incoming and outgoing alike; the
    chat comes from message.chat_id, since the update has no chat_id of
    its own
  - updateMessageSendSucceeded → MessageReplaced, carrying the temporary
    ID the consumer already holds
  - updateMessageSendFailed → MessageFailed; Error is MessageError with
    a code and TDLib's own description, and neither Error() nor Reason()
    includes that description, so a log line cannot get the message body
    or a transport string
  - updateDeleteMessages with is_permanent = true → MessagesDeleted; a
    non-permanent deletion only drops TDLib's cache and is ignored, as is
    a deletion with no ids
  - one window of the last 256 events per chat, with a monotonic
    sequence; the window is created by the first event, so a chat that
    never receives a message costs nothing, and the allocation stops
    growing once the window is full
  - MessageEventsSince(chatID, seq) returns the events after seq as
    copies, the cursor to continue from, and a resync flag: a cursor
    older than the window or ahead of the store cannot be answered and
    the consumer reloads the first history page. A chat with no events
    reports next = 0 and no resync
  - a message event signals the same capacity-1 channel as a chat-list
    change; a chat-list consumer re-reads a list that did not move
  - message updates are deliberately not in liveStateUpdateTypes, so
    they are not held during authorization: the TUI starts from the first
    history page and that page closes the gap
  - a message the store cannot parse is reported as an error and leaves
    the window untouched, as a malformed order does for the chat list
- History projection: internal/telegram/history.go
  - GetChatHistory returns newest-first pages
  - chatID == 0 rejected; limit validated in (0, 100]
  - fromMessageID == 0 requests the latest page
  - a non-zero fromMessageID is an inclusive boundary because
    offset is 0; callers must de-duplicate the boundary Message.ID
  - TDLib may return fewer messages than requested; HasMore is a
    heuristic
- Send projection: internal/telegram/send.go
  - SendTextMessage sends one plain-text message through sendMessage
  - chatID == 0 rejected; whitespace-only text rejected
  - topic_id, reply_to, options, reply_markup, link_preview_options
    are serialized as explicit null; entities is an empty array
  - clear_draft is true
  - response must be a message with matching chat_id and non-zero id
  - delivery-state tracking (message.sending_state,
    updateMessageSendSucceeded/Failed) is deferred

## Application and TUI
- Application lifecycle: internal/application/app.go
  - New: mock-only path
  - NewWithTelegram: runtime + TUI
  - NewWithAuth: runtime → optional auth → TUI → close
  - AuthRunResult carries tui.ChatSource and an optional
    session-owned Close; when non-nil, it replaces Runtime.Close
  - errors.Join combines auth, TUI, and close errors
- Chat service adapter: internal/application/chat_service.go
  - implements tui.ChatSource over a TelegramChats interface
  - per-call timeout chatServiceTimeout = 60s
  - clamps history limit to the TDLib range and substitutes a
    default when out of range
  - maps telegram.Message → tui.Message; preserves Outgoing, Text,
    and formats Timestamp as HH:MM
- TUI ChatSource: internal/tui/chat_source.go
  - ListChats, LoadHistory, SendMessage
  - async tea.Cmd-based loading with loadState
    idle/loading/loaded/empty/error
  - stale history and send responses are filtered by chat ID and by
    a monotonically increasing sendOperation
- Layout and focus: internal/tui (PR-10A.2, docs/TUI_SPEC.md §1, §3,
  §4, §5, §10)
  - layout.go: every number that decides a shape, and nothing else
    decides one. Width classes are wide >= 100, medium 72-99 and
    narrow below; the chat list pane is a fixed 24 columns when wide
    and 16 when medium, never a share of the terminal, and the two
    panes are separated by one column of space and no line
  - height rules: below 20 rows the previews and the extra status
    lines go, below 10 the hint bar is not drawn, below 6 the
    conversation is the composer alone, and below 40x5 the screen says
    it is too small instead of drawing something that does not fit
  - Wide and Medium always have two panes: before a chat is chosen the
    right one carries the empty state of §17 ("Select a chat", and the
    reason when the list is loading or has failed), and the list keeps
    the width of §3.3 either way. Narrow has one region
  - one focus at a time: the focus is a bar in the first column of a
    region, drawn with BorderLeft and the theme's FocusBar, and an
    unfocused region reserves the same column with MarginLeft, so
    moving the focus changes no cell to its right. The bar is a glyph
    and not a colour, so it is drawn under every profile: the no-
    colour profile clears the tokens of the theme on the way in and
    termenv prints neither colour nor attributes there. The selected
    chat and the selected message carry a chevron of their own
    (theme.SelectionMark, §4.1/§5.2) and keep it while the focus is
    elsewhere; it is not the focus bar, because a list that marked both
    with `▌` read as a double line. Styles: styles.go
  - Esc hierarchy (§8.5): composer to timeline, timeline to the chat
    list, and then it stops. On a two-pane screen the third step
    focuses the list beside the conversation instead of throwing the
    conversation away. A draft is never discarded
  - `q` leaves the program from the chat list and is a letter in the
    composer; Esc in the list does nothing. Ctrl+C quits everywhere
  - Enter in the list opens the chat and focuses the composer, and the
    conversation follows the selection while it is beside the list
  - widths are terminal columns (truncateCells, fitCells, wrapCells),
    never runes, and a line is padded rather than left ragged so a
    surface covers its whole pane
- Timeline: internal/tui/timeline.go, internal/tui/view_conversation.go
  (PR-10A.2b, §4.4, §8.3, divergence 1)
  - Chat.Messages is chronological, oldest first. TDLib answers a page
    newest first, so the page is reversed where it arrives; the source
    is untouched. A first page is stored as it comes, a page of older
    messages is prepended with dedup by Message.ID, and a sent message
    is appended
  - the cursor (selectedMsg) and the scroll anchor (timelineTop) are
    separate: the anchor is the message on the first row and survives a
    page arriving above it and a resize (§10.5). A page that adds N
    messages moves both by N, so the message that was on screen stays
    on screen
  - keys (§8.3): j/k walk, PgUp/PgDn move a screenful, G and End go to
    the newest message, Enter and i hand the keys to the composer, and
    g does nothing: it is the chat list's key
  - older pages load on ↑ at the oldest loaded message. Everything #12
    does with the request is unchanged: inclusive boundary, dedup by ID,
    a page that adds nothing exhausts the history, one request at a
    time, an error keeps what is loaded and is retried by the next ↑,
    and a stale response is dropped by historyOperation
  - a message is its author line with the time at the right edge and its
    text under it, wrapped to the width of the region. An outgoing
    message is indented two columns and named in the accent, one marker
    marks the selected message, the header is sticky, and the progress
    or the failure of an older page sits at the top of the timeline
  - opening a chat and sending a message both put the cursor on the
    newest message; a message that arrives while a reader is scrolled
    up does not move them
- Composer: internal/tui/composer_text.go, composer_layout.go,
  view_composer.go, model.go (PR-10A.3, §4.5, §7, §8.4)
  - the draft is a run of runes and composerCursor is an index into it.
    Editing goes over grapheme clusters (github.com/rivo/uniseg, a direct
    dependency since PR-10A.3), so a ZWJ emoji or a letter with a
    combining accent is one thing to move over and one thing to delete
  - keys (§8.4): arrows and Home/End and Ctrl+A/Ctrl+E move the cursor,
    up and down move between the rows of the draft keeping the column,
    Backspace and Delete remove a cluster, Ctrl+U clears to the cursor,
    Ctrl+W takes the word before it, Ctrl+K takes the rest of the line,
    Tab and Shift+Tab move the focus and Esc leaves without losing the
    draft
  - Enter sends and Alt+Enter starts a line. Shift+Enter is not offered
    because Bubble Tea v1 cannot tell it from Enter in most terminals
    (divergence 3); the hint bar names Alt+Enter on every width
  - a bracketed paste goes in whole, newlines and all, and is never a
    send
  - height (§4.5): one row for a draft that fits, more as it grows, four
    at most and never more than 30% of the screen. The window follows
    the cursor, so the row being written is always on screen
  - a draft longer than the field is hard-wrapped for the screen and
    never written into: what reaches SubmitMessage is the text as typed,
    spaces and newlines included. Emptiness is a separate test
  - the draft is cleared only after a successful enqueue, and a failure
    keeps the text and the cursor
  - editing forgets an ordinary send error and never forgets a paused
    one: nothing was attempted, the reason still holds, and a user who
    cannot send is told so on every keystroke
  - Enter on a blank draft sends nothing, makes no error, and lights the
    placeholder for 700ms through one message. Where the hint bar is
    hidden the placeholder says "Write a message… (Enter to send)"
  - the cursor is drawn: reverse video on the character it is at, a bar
    at the end of a row. A terminal with no colour prints no attributes
    either, and the bar is all that is left there
- Composer (pre-10A.3 behaviour, kept in model.go)
  - Enter starts a send only with non-nil source, non-empty
    TrimSpace(text), no send in flight, and a selected chat
  - composer is frozen while sendState == sendStateSending
  - Ctrl+U clears draft and error in sendStateError; no-op in
    sendStateSending
  - on success composer is cleared and the message is prepended with
    dedup by Message.ID
  - on error composer is preserved and sendState becomes
    sendStateError
- Auth TUI: internal/tui/screen_auth.go
  - ScreenAuth with phone/code/password prompts
  - password masked as •
  - ErrAuthCanceled sentinel for Esc/Ctrl+C
  - 2FA password is read only from environment for manual
    integration; production path must use a masked prompt or OS
    keyring

## Storage
- Embedded database: SQLite via modernc.org/sqlite (durable outbox,
  ADR-0002)
- Payload encryption: XChaCha20-Poly1305 per outbox message text
- Schema migration: TBD
- Outbox retention: accepted and canceled entries are purged 7 days
  after their last update (DispatcherConfig.Retention; negative
  disables); uncertain and permanently failed entries are kept for the
  user; SQLite runs with secure_delete so purged rows are overwritten
- Data directory: ~/.local/share/telecli by default; `telecli configure`
  moves it to os.UserConfigDir()/telecli. Every stored directory must
  be absolute; without an absolute home directory there is no default
  and startup fails with ErrDataDirUnavailable until data_dir is set
- Outbox reset (internal/outbox/reset.go, internal/application/outbox_reset.go):
  - `telecli outbox reset` starts a new empty queue when the key of the
    old one is provably absent (provider answered "no record"); a
    malformed key, a locked or denied key store and a platform without
    key storage all refuse and change nothing
  - the summary before the confirmation is read from a scratch copy of
    the database, so the queue is never opened or modified and the
    message bodies, chat ids and phone numbers are never read at all:
    only plaintext scheduling metadata is counted
  - confirmation is the word `reset` (case-insensitive), not `y` or
    Enter; `--yes` is for an unattended run, and without either a
    terminal the command does nothing
  - the old queue is never deleted: it is renamed to
    `outbox.db.orphaned-<UTC time>` with its write-ahead log, and a
    `.txt` note beside it records the old `database_id` and how to put
    the queue back if the old key returns
  - a new `database_id` gets the new key; the old one is never reused,
    so a key restored from a backup cannot end up on the empty queue
  - steps run in the order record, move aside, create key, write
    configuration, clear record, and the record (`outbox.reset` in the
    data folder) makes the order recoverable: a repeated run finishes
    the same reset. While it exists, `Open` refuses to create a key
    (ErrOutboxResetPending), so no start can quietly invent a queue
  - a session holds the queue through the run lock
    (`outbox.AcquireRunLock`, `Deps.Exclusive`), so the command refuses
    while another telecli window has the queue open. The lock lives in
    the shared lock directory, not in the data folder
- TDLib database directory: ~/.local/share/telecli/tdlib/database
- TDLib files directory: ~/.local/share/telecli/tdlib/files
- Config file: os.UserConfigDir()/telecli/config.toml, overridden by
  --config or TELECLI_CONFIG
- Interface configuration: [tui] theme and [tui] color
  - theme: a built-in theme name, default catppuccin-mocha; an unknown
    name is a configuration error that lists the names there are
  - color: "auto" (default), "always" or "never"; anything else is a
    configuration error with the valid values in it
  - the vocabulary lives in internal/tui/theme, so the values are stored
    as plain strings here and validated there
- Cache directory: TBD

## Secrets
- Keyring backend:
  - outbox keys: macOS Keychain Services; Linux Secret Service
  - Telegram credential profiles: macOS Keychain Services only
    (internal/authstore, darwin + cgo); other platforms have no
    profile store and must use the environment
- Headless fallback: none; without a key provider the durable outbox
  fails closed and never creates a replacement key
- Send mode (internal/config/message_send_mode.go):
  - `durable` is the only mode; it is also the default, because direct
    send lost messages when the process exited between Enter and TDLib's
    answer
  - a configured `direct` is a retired value: it loads as durable and
    adds a `Config.Warnings` note that `telecli doctor` prints on every
    run; the file itself is not rewritten
  - an unknown value is a configuration error
  - `telecli configure` asks no mode question and refuses `--mode
    direct` with an explanation
- Sending paused (internal/application/sending_paused.go):
  - a durable outbox that cannot be opened does not fail startup; the
    TUI starts and only sending is paused
  - the submitter refuses, so nothing is queued and nothing is sent by
    another route; the draft and composer focus survive
  - the screen shows `Sending paused`, the explanation, the reason hint
    and the `Details:` link; the cause never appears there
  - the cause is matched to one of five cases by `errors.Is` on outbox
    sentinels: `ErrOutboxKeyAccessDenied` (locked or denied),
    `ErrOutboxKeyUnavailable` (key missing),
    `ErrOutboxKeyProviderUnsupported` (no secure storage),
    `ErrOutboxDataDirInsecure` (folder reachable by others),
    `ErrOutboxResetPending` (a reset that was started and not finished,
    reported as the missing-key case with the same remedy), otherwise
    other
  - `ErrOutboxKeyAccessDenied` was added because the Keychain bridge
    already separated a missing item from a refusal and the provider
    collapsed them, which would have forced string matching
  - `telecli doctor` probes the queue and prints the case, the data
    folder and the full cause
  - exact layout of the status and composer blocks is PR-10A.4; here the
    existing send-error slot is reused
- Outgoing messages in the timeline (internal/tui/pending_message.go,
  internal/tui/pending_message_model.go,
  internal/application/pending_message_source.go, PR-10A.4a, §4.4, §6)
  - a message the history does not have yet is drawn in the timeline as an
    outgoing message with the state of its delivery under the text; there
    is no separate delivery block between the timeline and the composer
  - `tui.PendingMessage` is the one type that carries message text outside
    the history, and it goes to `View()` and nowhere else: `String()` and
    `GoString()` print the entry and the state only, so `%v`, `%+v` and
    `%#v` of a value on its way to a log cannot carry it
  - `PendingMessageSource` is a separate source from
    `MessageStatusSource` on purpose. The status list stays payload-free for
    #21's privacy tests; the timeline asks a second source that has the
    text, and both are read on the same poll tick so the text and the state
    of a message never come from two reads that disagree
  - `OutboxPendingMessageSource` depends on a narrow `entryLister`
    (ListAll) and not on the whole store: a source that could enqueue or
    claim could send a message from a read the screen asked for
  - accepted entries are not pending: Telegram has the message, and the
    entry holds the temporary identifier of the sendMessage response, not
    the one the history returns (ADR-0003 §6)
  - a message queued in this session appears at once, from the draft and
    the entry the queue returned, and stays with `✓ Sent` until the
    history brings it back; re-entering a chat clears it
  - the states use the theme's status vocabulary (`theme.StatusState`),
    so a state is a symbol and a word and the word carries the meaning in
    every profile. Uncertain adds "Message may already have been sent"
    and never borrows the wording of a failure; a retry names the
    absolute time of the next attempt, never a countdown
  - `MessageDeliveryState.IsTerminal()` no longer counts `uncertain` as
    terminal: §6.2 keeps it open until the user decides, and the outbox
    keeps its own narrower notion for the dispatcher, which must never
    send the same entry twice
- Status line (internal/tui/view_status.go, internal/tui/status_summary.go,
  internal/application/status_summary_source.go, PR-10A.4b, §4.1, §4.3, §11,
  §12, §17, §18)
  - the status block is the parts of §11.1 joined with `·` and drawn under
    the title of a conversation, in the header of the chat list on a narrow
    screen (§3.3), and in the conversation pane while no chat is open. Two
    lines at most, one on a short screen (§3.4)
  - what does not fit is dropped from the end of the parts, in the order
    §11.1 ranks them, and never cut in the middle of a part: a cut sentence
    says less than a shorter one
  - an unknown connection is not drawn at all and a queue that could not be
    read is not drawn as empty. The interface has no word for either, and a
    word it makes up would be a claim nothing checked
  - a failed queue read keeps the connection and drops the counts, and the
    cause goes to the diagnostic stream. The summary carries counters and a
    state and nothing else, so it is safe in a log
  - `tui.StatusSummarySource` is read on the delivery poll, not on a
    subscription (ADR-0003 step 3). A read that changed nothing delivers
    no message at all, so an idle program does not redraw itself every two
    seconds
  - the tick is the only thing that schedules the next read. A response
    scheduling one too would make the cadence depend on which read
    answered, and a read that delivered nothing could never hand the loop
    back
  - each read is numbered, so a read slower than the interval is discarded
    when it answers after a newer one instead of putting an old snapshot
    back on the screen
  - `telegram.LiveState` keeps the connection state
    (`updateConnectionState`, td_api.tl:10974) and holds it during
    authorization like the chat list: TDLib announces it during the login
    wait and does not announce it again. A constructor the pinned schema
    does not have is applied as unknown rather than kept, so a stale "ready"
    is never read as the present
  - the chat list waits ten seconds and then says so with the key that ends
    the wait (§18): `R` repeats the load, and the hint bar names it only
    while a retry can do something
  - a failed queueing says `Message was not queued` and `Your text is still
    in the composer` (§12.1), on a raised background behind those two
    sentences and not across the pane (§24). The cause goes to the
    diagnostic stream and the draft text goes nowhere at all
  - `tui.Dependencies.Diagnostics` is that stream. It is the same one the
    paused queue writes its cause to
- Secret logging policy: never log secrets
- TDLib credentials source (internal/application/auth_credentials.go):
  exactly one source per run, never mixed
  1. environment, when all three are set:
     - TELECLI_TDLIB_API_ID
     - TELECLI_TDLIB_API_HASH
     - TELECLI_TDLIB_PHONE
     Only some of them set is a configuration error; it never falls
     through to a profile.
  2. otherwise a credential profile: [auth] api_id in the config file
     (not a secret) plus the API hash and phone stored in the Keychain
     under [auth] credential_profile; created by `telecli configure`
  3. neither configured: mock-only TUI
- 2FA password source: masked TUI prompt in production;
  TELECLI_TDLIB_PASSWORD is read only by the manual integration tests

## Commands
- telecli --help
- telecli version
- telecli doctor
- telecli configure
- telecli configure status
- telecli configure reset
- telecli outbox reset [--config path] [--yes]
- telecli tui

## Architecture
- Architecture policy: internal/archdeps/policy.go, enforced by
  TestArchImports over every Go file regardless of build tags
- Recorder contract: internal/telemetry/recorder
- Recorder dependency rule: standard library only
- TDLib binding: internal/telegram/tdjson
  (dynamic cgo loader over modern JSON C API; no third-party Go
  binding)
- Authorization coordinator: internal/telegram/auth_coordinator.go
- Authorized session: internal/telegram/session.go
- Query correlation: internal/telegram/query.go
- Chat projection: internal/telegram/chats.go
- History projection: internal/telegram/history.go
- Send projection: internal/telegram/send.go
- Application lifecycle: internal/application/app.go
- Chat service adapter: internal/application/chat_service.go
- Interface theme: internal/tui/theme (PR-10A.1, docs/TUI_SPEC.md §2)
  - a leaf package above internal/config: Palette, Tokens, Theme,
    ThemeMode and Gradients, with the tokens computed from a palette by
    one function, TokensFor, so a user palette needs no component change
  - three built-in dark themes, and only three on purpose (§2.6): the
    values are the official Catppuccin Mocha, Tokyo Night Storm and
    Gruvbox Dark palettes, with the upstream role named in a comment
    beside each block
  - contrast: PrimaryText and SecondaryText are at or above WCAG AA
    4.5:1 on AppBackground, SidebarBackground, ChatBackground and
    ComposerBackground in every built-in theme, and the two dimmer tiers
    get dimmer in order. A colour that is not set has no luminance, so a
    ratio with one is 0 and a test cannot be flattered by it
  - colour profiles: True Color, ANSI-256, ANSI-16 and no colour. The
    terminal is measured through Lip Gloss and the decision is a pure
    function of that measurement, the environment, --no-color and
    [tui] color, so a test states them instead of inheriting the machine
  - the precedence follows no-color.org, which asks a user-level
    configuration file and a per-instance command line to override the
    NO_COLOR environment variable:
    1. [tui] color = "never" and --no-color win over everything, a
       command-line argument being the most specific thing a user can say
    2. TERM=dumb still means no colour: a terminal that renders escape
       sequences as garbage is not a terminal
    3. [tui] color = "always" is stronger than NO_COLOR; it uses what the
       terminal reported, and assumes 256 colours when nothing could be
       measured or Lip Gloss reported none, which is a pipe, `script`,
       tmux with an unusual TERM, or the terminal of some IDE
    4. auto follows NO_COLOR and then the measurement, believes a Lip
       Gloss that reports no colour, and assumes 16 colours for a
       terminal it could not identify
  - an indexed terminal gets the color library's own reduction to the 256
    palette and a gradient of at most three stops; a 16-colour terminal
    gets basic colours by role, so the terminal renders them with the
    palette the user configured, and its backgrounds stay unset
  - nothing depends on colour: the focus marker, the reverse-and-bold
    selection and the symbol-plus-words of every status are theme roles,
    not view helpers
  - the views do not read the theme yet (PR-10A.2). It reaches
    tui.Model through tui.Dependencies, and telecli doctor reports the
    theme and the profile that were resolved

## Current roadmap status
- PR-01: recorder foundation, accepted
- PR-02: CLI and TUI shell, accepted
- PR-03: interactive mock TUI navigation, accepted
- PR-04: TDLib runtime lifecycle, accepted on macOS arm64
- PR-05: authorization foundation and session lifecycle, accepted
  - auth vocabulary, parser, request builders: accepted
  - ScreenAuth, password masking, cancellation: accepted
  - authorization coordinator: accepted
  - application wiring foundation: accepted
  - AuthorizedSession (pump + graceful close): accepted
  - automated graceful-close integration: passed on macOS arm64
  - database reinitialization after prior close: passed on macOS
    arm64
  - real-account login: verified on macOS arm64
- PR-06: correlated queries and chat projection
  - PR-06A correlated TDLib queries: accepted
  - PR-06B chat list projection: accepted
  - PR-06C chat history queries: accepted
  - PR-06D.1 ChatSource interface and telegram adapter: accepted
  - PR-06D.2 application source plumbing and close ownership:
    accepted
  - history pagination UI: accepted
  - live chat-list updates: accepted (ADR-0003 step 1)
  - live message events: accepted (ADR-0003 step 2)
  - lossless update/resync policy: pending
- PR-07: text message sending
  - Telegram `sendMessage` wire layer: accepted
  - application and TUI send boundary: accepted
  - composer send state machine: accepted
  - manual real-account send integration: passed on macOS arm64
  - `sendMessage` response object: verified against TDLib 1.8.67
  - final delivery to recipient: not verified
  - delivery-state update tracking: deferred
- PR-10A: interface rewrite per docs/TUI_SPEC.md
  - PR-10A.1 semantic theme engine with three dark presets: accepted
  - PR-10A.2 layout and focus: accepted (borderless two-pane layout,
    one focus, Esc hierarchy, focus-aware hint bar, drawn with the
    tokens of the theme)
  - PR-10A.2b chronological timeline and conversation keys: accepted
  - PR-10A.3 multi-line composer with a cursor and readline keys:
    accepted
  - PR-10A.4a outgoing messages in the timeline with their delivery
    state: accepted
  - PR-10A.4b status line, connection state, empty states §17, loading
    §18, enqueue error §12.1: accepted
  - 10A.5 action sheet, 10A.6 chat search, 10A.7 snapshots: pending
- First usable TUI checkpoint: PR-02
- First TDLib lifecycle checkpoint: PR-05
  - initialization before user authorization: verified on macOS
    arm64
  - graceful close: verified on macOS arm64
  - real-account login: verified on macOS arm64
- First message-send checkpoint: PR-07
  - `sendMessage` accepted by TDLib and returned a message object:
    verified on macOS arm64
  - final delivery to recipient: not verified
  - delivery-state update tracking: deferred

## Integration gates
- Automated graceful close:
  - file: internal/telegram/session_close_integration_test.go
  - build tag: tdlib_integration
  - requires: TELECLI_TDLIB_INTEGRATION=1, TELECLI_TDLIB_LIBRARY,
    TELECLI_TDLIB_API_ID, TELECLI_TDLIB_API_HASH
  - does not require a user account or phone number
  - passed on macOS arm64
- Manual account authorization:
  - file: internal/telegram/session_account_manual_test.go
  - build tag: tdlib_account_integration
  - requires: TELECLI_TDLIB_INTEGRATION=1, TELECLI_TDLIB_LIBRARY,
    TELECLI_TDLIB_API_ID, TELECLI_TDLIB_API_HASH,
    TELECLI_TDLIB_PHONE, optional TELECLI_TDLIB_PASSWORD
  - passed on macOS arm64
- Manual message send:
  - file: internal/telegram/send_account_manual_test.go
  - build tag: tdlib_send_integration
  - requires: TELECLI_TDLIB_INTEGRATION=1,
    TELECLI_TDLIB_SEND_CONFIRM=1, TELECLI_TDLIB_LIBRARY,
    TELECLI_TDLIB_API_ID, TELECLI_TDLIB_API_HASH,
    TELECLI_TDLIB_PHONE, TELECLI_TDLIB_SEND_CHAT_ID,
    TELECLI_TDLIB_SEND_TEXT, optional TELECLI_TDLIB_PASSWORD
  - produces an external side effect: writes one message into a real
    chat
  - diagnostics before send log only the page size and whether the
    target chat ID appears in the first GetChats page; absence is not
    a failure and the send is always attempted
  - passed on macOS arm64
- Production authorization activation: wired. No credentials →
  mock-only, partial environment or incomplete profile → configuration
  error, complete environment or profile → production authorization

## Open ADRs
- ADR-0001: TDLib modern JSON C API through an internal dynamic cgo
  loader (accepted)
- ADR-0002: durable outbox storage and at-rest privacy (accepted)
- ADR-0003: live updates through a coalescing state store (accepted)
- TUI specification: docs/TUI_SPEC.md (source of truth for PR-10A and
  later interface work; its "Решения" section overrides the body)

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
- Terminal cell measurement: internal/tui/termwidth, over
  github.com/charmbracelet/x/ansi v0.10.1 (the grapheme rule and the
  escape sequence decoding) and github.com/mattn/go-runewidth v0.0.16
  (East Asian Width for the code point rule). Every width in the layout
  is counted in terminal columns, never in runes: "привет" is six
  columns wide and an emoji is two, and a rune count overflows the
  screen on the first non-ASCII chat name. The rule is the one the
  terminal was measured for, not a fixed one
- Terminal readiness: golang.org/x/sys/unix v0.48.0 Poll, unix only, so
  the width measurement waits for the terminal without holding a read
  on it. A platform without it is not measured and is drawn with the
  codepoint rule
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
- TDLib numbers: internal/telegram/tdint.go
  - TDLib writes a 64-bit integer as a JSON string and a 53-bit one as
    a JSON number, and which one a field is depends on the field:
    message.chat_id is int53 and arrives as 42, while
    message.media_album_id is int64 and arrives as "0" (a real account,
    #53)
  - tdInt is the one way telecli reads either shape. Every decoded
    int64 of the message, chat, user and send paths is a tdInt, and
    chatPosition.order, the field that had to be read both ways
    before, is read by it too
  - a value that is neither a number nor a string of digits is a
    decode error; a null or absent field is zero
  - a request of ours is written as a number, so the type is safe in
    both directions
- History projection: internal/telegram/history.go
  - GetChatHistory returns newest-first pages
  - chatID == 0 rejected; limit validated in (0, 100]
  - fromMessageID == 0 requests the latest page
  - a non-zero fromMessageID is an inclusive boundary because
    offset is 0; callers must de-duplicate the boundary Message.ID
  - a history ends at an empty answer and nowhere else. HasMore is
    whether the answer was not empty (#57). It used to be
    len(messages) == limit, and TDLib answers the first request of a
    chat with what the local database has — one or two messages on a
    fresh account, however many were asked for — so every chat opened
    as its last two messages and was never asked again
  - NextFrom is the oldest message of the answer, including one this
    build could not read: a boundary that skipped it would ask for the
    same page again
  - the entries of a page are read one at a time, out of their own raw
    objects: one message that cannot be read is left out of the page
    and the rest of it is shown, rather than one unreadable field
    failing the fifty messages around it (#53). An entry that is not
    a message, and a message of another chat, are still
    ErrUnexpectedHistoryResponse: they are the answer to a different
    question
  - the number of entries a page could not read travels with it as
    HistoryPage.Unreadable and reaches the diagnostic stream as a
    number, never as text: a message of this user that is missing is
    missing silently otherwise
  - Message.MediaAlbumID is int64 and 0 is not an album; the timeline
    groups runs of messages that share a non-zero id
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
  - names: the sender of a message is resolved when the message is
    drawn. An outgoing message is "You"; a personal chat and a channel
    are named by the chat; a group names the person who sent it, read
    with getUser for a user sender and getChat for a chat sender. A
    sender that cannot be read is "Unknown" and does not fail the page
  - the names are read from the local TDLib at authorNameTimeout and
    kept in a bounded in-memory map (nameCacheLimit = 512, users and
    chats keyed apart). They are not in LiveState, not in a log, not in
    a doctor report and not in an error message: the privacy claim of
    #41 is unchanged, and this file is what keeps it true
  - ownUserID is passed in by the composition root, and a chat whose
    private peer is that user is the chat with oneself: it is found by
    the name the user types ("Избранное") as well as by the title
    Telegram gives it. An ownUserID of zero means the chat is not
    recognised, which costs the alias and nothing else
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
    narrow below; the chat list pane is 28% of a wide screen kept
    between 28 and 40 columns, a fixed 16 when medium, and absent
    when narrow, and the two panes are separated by one column of
    space and no line. A share alone is not right either way: a list
    that does not grow cuts a name in half while the conversation
    beside it has empty columns, and one that grows without a bound
    takes the columns a message needs
  - height rules: below 20 rows the previews and the extra status
    lines go, below 10 the hint bar is not drawn, below 6 the
    conversation is the composer alone, and below 40x5 the screen says
    it is too small instead of drawing something that does not fit
  - Wide and Medium always have two panes: before a chat is chosen the
    right one carries the empty state of §17 ("Select a chat", and the
    reason when the list is loading or has failed), and the list keeps
    the width of §3.3 either way. Narrow has one region
  - one focus at a time: the focus of a pane is one accent rule
    (`━`, the focusRuleGlyph) under the heading of that pane, and the
    unfocused pane has a blank line of the same height in the same
    place, so moving the focus moves a rule and nothing else. A pane
    is the unit rather than a region inside it: the timeline and the
    composer are two regions of one conversation. A rule is a
    character and not a colour, so it is drawn under every profile;
    the no-colour profile clears the tokens of the theme on the way
    in and termenv prints neither colour nor attributes there. No
    row of the chat list and no row of the timeline carries a focus
    bar at all (`▌`, `┃`, `│`): a line of focus down the side of a
    list is a column of the list, and a user reads `▌ Alice` as a
    name that begins with a block. The selected chat is the
    Selected background across both of its rows with its name in the
    accent and no marker; the profile with no colour has no
    background to show and puts `›` in a column of its own
    (theme.SelectionMark, §4.1) and asks for bold, which that profile
    prints as nothing. The search above the chat list is a region of
    its own while it is open (FocusSearch, §5/§9) and is stacked
    above the rows, so the list gives up the column rather than there
    being two of them. Styles: styles.go
  - Esc hierarchy (§8.5): search to composer, composer to timeline,
    timeline to the chat list, and then it stops. On a two-pane screen
    the third step focuses the list beside the conversation instead of
    throwing the conversation away. A draft is never discarded
  - `q` leaves the program from the chat list and is a letter in the
    composer and in the chat search; Esc in the list does nothing.
    Ctrl+C quits everywhere
  - Enter in the list opens the chat and focuses the composer, and the
    conversation follows the selection while it is beside the list
  - widths are terminal columns (the model's WidthModel), never runes,
    and a line is padded rather than left ragged so a surface covers its
    whole pane. One width model per model, read by every view: the
    composer, the search, the timeline, the list, the status line and
    the action sheet, and a test in the package fails if a view reaches
    a width function of a library on its own
  - Lip Gloss is told the colours of a region and not its width. It
    measures with the grapheme rule and pads the lines of a block to the
    widest of them, and a row of the chat list with a hand in it is a
    column wider that way than it is on a terminal that counts code
    points — so the regions are rendered a line at a time and the two
    panes are joined by hand, with the model's width
  - the screens of §21 are kept as golden files under
    internal/tui/testdata/snapshots, one per screen, with the plain text
    and the escape sequences, and the width rule a screen was drawn in
    is in the header of its file. Two of them are the same screen with
    the names the two rules count differently, drawn in each rule, and
    every line of both is a whole number of columns in the rule that
    screen was drawn in
- Screen text: internal/tui/screen_text.go (§19, #53)
  - every string that came from Telegram is cleaned once, on its way
    into the model, and not in each view: safeChats, safeMessages and
    safeMessage are asked about every string of tui.Chat and
    tui.Message, and the model calls them where a projection lands
    (chatsLoadedMsg, the first history page, an older page, the answer
    to a send). A view that cleans what it draws is a view that has to
    remember to, and the field nobody remembered would be the one that
    moves the cursor
  - what goes: every C0 control that is not spacing, DEL, every C1 control
    that is not NEL, whole escape sequences with what they carry (CSI,
    OSC, DCS, APC, PM, SOS, in the seven-bit and the eight-bit form),
    and the bidi controls U+202A–U+202E and U+2066–U+2069. They go
    without a replacement: a backspace erased a character on the
    terminal, and the character it erased is not the one a reader wants
    back
  - what stays as spacing: a line feed, a carriage return, a vertical
    tab, a form feed, NEL (U+0085) and U+2028/U+2029 are line breaks of
    the text a sender wrote, and dropping them would glue two words
    into one. A run of them that stands next to each other is one
    break, so a Windows line ending is one line and not two, and so is
    an empty line a sender wrote
  - a tab is a space, because a tab is not a column in a cell of a
    fixed width
  - what stays: letters, punctuation, emoji with their ZWJ sequences,
    variation selectors and flags, and the combining marks of every
    script. Nothing that makes a character a character is removed
  - screenBody is for the feed, where a line break in a message is a
    line break on the screen; screenLine is for a chat name, a
    preview, a conversation title and the name above a message, where
    a line break is a space
  - a sequence that is cut short eats what is left of the text rather
    than putting half of it on the screen, and a text with nothing to
    clean is returned as it is, without an allocation
  - a source keeps its own list: the cleaned one is a copy
  - the draft of the composer and the text of a pending message are
    not part of this: they are what the user of this machine typed,
    not what Telegram sent
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
  - a page is asked for until the feed has as many messages in it as it
    has rows, and that is the whole of the fill (#57). The first page
    went in the screen as it arrived, which on a fresh account is one or
    two messages, and it is the number of rows the feed has and not the
    height of the window, so the fill does not chase a resize
  - the fill is bounded three ways: at most maxHistoryFillRequests (5)
    requests, at most maxHistoryFillMessages (100) messages brought in
    beyond the first page, and it stops at a page that says there is
    no more, one that adds nothing, or one that errors. A channel with
    a hundred thousand messages in it is not read into memory to draw
    one screen of it
  - ↑ at the top edge runs the same fill afterwards, so scrolling back
    into a chat that opened on half a screen is the same walk as
    opening it
  - a chat re-entered from the chat list is asked about again rather
    than read from the cache: the cache is the twenty most recent
    messages and what the user is doing with ↑ is asking for the rest
  - a change of chat — the selection moving, a chat opening, a
    conversation closing — asks for a full redraw of the same size
    (internal/tui/repaint.go, tea.WindowSizeMsg of the size the
    window already is). Bubbletea's renderer paints the line of every
    line of every frame, so a line whose bytes change without its
    width changing is a line the terminal was never told about, and a
    chat list that scrolls by a pixel is exactly that (#57)
  - a test against a terminal in memory synchronises on the frame and
    not on a length of time: every update draws exactly one frame, the
    frame is marked with the number of the update it was drawn from,
    and the wait ends when the cells of the terminal are the cells of
    the frame drawn after the key — or when the frame is the very bytes
    the terminal already holds, which is what a key that changes
    nothing draws and the renderer does not write. There is no sleep
    standing in for a write, and a wait that is over says which rows
    did not agree
  - the feed is bottom-anchored: fewer messages than the feed has rows
    for means the empty rows are above them, so the newest message sits
    on the row directly above the composer
  - a page arrives from TDLib newest first and the model reverses it
    where it lands, so the screen is oldest at the top and the newest
    message directly above the composer (divergence 1, closed by #50).
    A golden that handed the model an already chronological page would
    be a golden of a program nobody runs
  - a message from the other side is its author and its time on one row
    ("author · time") and its text under it, wrapped to the width of the
    region. The author is coloured out of six theme roles picked by the
    hash of the sender identifier, so the same person is the same colour
    in every message and a group has two names in two colours; a
    personal chat has one other person in it and one colour
  - a message of this user is a block on the right on the composer's
    own surface, inset one column, at most 70% of the feed, with the
    time and the state of the send under the text at the block's right
    edge in the colour of the state itself ("✓ Sent 14:30", "● Queued
    14:30", "↻ Retrying at 14:35") and no author line: the block being
    on the right already says whose it is
  - a blank row separates two messages, and it is the first thing to go
    on a screen shorter than 20 rows
  - a run of consecutive messages from one sender that share a
    media_album_id is one entry: "[3 photos]", with the caption of the
    first part that has one. A message that carries a file says what it
    carries in brackets, "[photo]", whether or not it has a caption, and
    a message with neither a file nor words draws no empty row
  - one marker marks the selected message, the header is sticky, and the
    progress or the failure of an older page sits at the top of the
    timeline
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
- Chat search: internal/tui/chat_search.go, view_chat_search.go,
  view_chats.go, model.go (PR-10A.6, §4.1, §5, §8.2, §8.5, §9, §17)
  - `/` in the chat list opens a line above the list and puts the focus in
    it. It is a focus region of its own (FocusSearch, §5), not a mode of
    the list: it is a region stacked above the rows, so the list gives up
    the accent column while the keys are in it and the screen still has
    one accent column (§5.2)
  - while the focus is in the search, letters are the query: q does not
    quit, j/k do not walk the list, g does not jump, R does not reload.
    The arrow keys are the way to walk the results. Ctrl+C still quits
    everywhere
  - Tab walks the search and the list (they are two regions while the
    search is open), Esc closes the search from either of them, and Esc
    puts the cursor back on the chat it was on before (§8.5, §9)
  - the filter is over the loaded chats and asks TDLib nothing: a chat
    Telegram has not loaded cannot be found by a list that does not have
    it. Search over messages is PR-10D
  - matching is Unicode simple case folding rune by rune, the folding of
    strings.EqualFold (foldIndexOf, foldRuneEqual): "dev" finds "Dev",
    "ПРИВЕТ" finds "Привет", the Kelvin sign folds into k. It does not
    fold ё into е and it does not normalise anything else, so a search
    never rewrites what somebody typed. An empty query does not filter
  - the results keep the order of the whole list, and the first of them is
    selected on every keystroke (§9). selectedChat is an index into the
    whole list, not into the results, so a filter renumbers nothing the
    model knows
  - ↑/↓, and the list's j/k after Tab, walk the results and stop at their
    ends; g and G go to the first and the last result. While the search is
    open the conversation beside the list does NOT follow the selection:
    Enter is the key that opens a chat, and following the cursor would
    open a chat for every result the user arrows past
  - Enter opens the selected result and closes the search; a query that
    found nothing opens nothing and keeps the search. Nothing is opened
    out of an empty result
  - edits are the readline ones of §8.4 that work backwards from the end
    of the query: Backspace (per cluster), Ctrl+U, Ctrl+W. The cursor is
    at the end of the query and does not move into it, so Ctrl+K could
    only ever do nothing and is not offered
  - the matched fragment is drawn in the Focus token in bold, in runs
    split on grapheme cluster boundaries, and never inside one: an emoji
    or a letter with a combining accent is drawn whole or not at all. The
    title is fitted to the width of the row first, so a highlight is never
    in a part of a title that the pane cut. Under NO_COLOR there is no
    highlight and the narrowed list is the meaning (§2.7)
  - the line is one row at every width and shows a window of the query
    that ends at the cursor, so a query longer than a 14-column pane
    shows what is being typed. Nothing is ever cut in the middle of a
    character
  - the header of the list is "/ Search · N unread" (§4.1), and the hint
    bar of the list names "/ search". On a narrow screen the status block
    keeps the second line of the header (§3.3) and the search is named in
    the hint bar. The hint bar of the search is "Enter open · Esc
    cancel" and does not name q, which is a letter in it
  - "No chats found" (§17) is what a query that matched nothing leaves:
    a calm sentence, and chatsState and loadErr are untouched so it
    cannot be mistaken for a list that failed
  - a resize that hides the chat list (wide to narrow with a conversation
    open) closes the search: a field that is not on the screen is not one
    anybody can type into
- Screen snapshots: internal/tui/snapshot_test.go and
  internal/tui/testdata/snapshots/ (PR-10A.7, docs/TUI_SPEC.md §20, §21)
  - one golden file per screen, named after the test that owns it, and one
    test per screen. §21 reserves the names of the eighteen it lists, and
    the search, the status line with the presence and the paused composer
    have theirs beside them. A file no test claims, or a screen no file
    holds, fails: a golden nobody checks drifts and is then believed
  - every file holds the screen twice: the text without escape sequences,
    which is what a reviewer reads, and the same screen as one %q per
    line, so a change of a colour is a diff like any other. With only the
    first half a theme could be replaced and no test would notice
  - a difference fails the test and prints both sides of every line that
    differs. Only `go test ./internal/tui -run Snapshot -update` rewrites
    a file, and it rewrites the ones whose test ran
  - everything on a snapshot is synthetic: the chats, the messages, the
    times, the entry ids and the account key are written in the test file
    and nowhere else (§19). A golden is committed, travels to CI and
    outlives the machine it was made on, so nothing of a real account may
    reach one
  - determinism: a fixed moment, a fixed zone (time.FixedZone), one of the
    three layouts of §10.1, and a theme and a profile the test names. The
    renderer is built for a profile and never probes the terminal, and a
    test sets TERM, COLORTERM, NO_COLOR, CLICOLOR and CLICOLOR_FORCE to
    values that disagree and compares the bytes with the golden
  - the invariants of §20 are checked over every snapshot in one pass: no
    box drawing glyph and no line made of frame characters, focus bars in
    exactly one column, and no line wider than the screen it is drawn for
  - one of them is a chat list whose names and previews carry what a
    terminal acts on (TestSnapshotUntrustedNames, #53). The golden holds
    four ordinary rows of four ordinary chats — "Anna Example" out of a
    carriage return — which is the claim: the words are still there,
    apart from each other, and the characters are not. The invariant
    pass covers it like any other
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
- Interface configuration: [tui] theme, [tui] color and [tui] width
  - theme: a built-in theme name, default catppuccin-mocha; an unknown
    name is a configuration error that lists the names there are
  - color: "auto" (default), "always" or "never"; anything else is a
    configuration error with the valid values in it
  - width: "auto" (default), "grapheme" or "codepoint"; anything else
    is a configuration error with the valid values in it, resolved by
    the composition root like the theme and reported by telecli doctor
  - the vocabulary lives in internal/tui/theme and
    internal/tui/termwidth, so the values are stored as plain strings
    here and validated there
- Width rule: internal/tui/termwidth
  - the two rules disagree about a grapheme cluster made of several code
    points: "✌️" is two columns to a grapheme counter and one to a
    terminal that follows wcwidth, which is what the macOS Terminal and
    iTerm2 do
  - grapheme: the width of a cluster as the Unicode emoji rules say it
    is drawn; codepoint: the widths of its code points added up, with
    U+FE0F, U+FE0E, ZWJ, a skin tone and whatever follows a ZWJ at
    nothing, and a flag at the width the terminal gave it
  - auto measures the terminal before the first frame, with the standard
    cursor position request (ESC[6n) over six probes, inside a total
    budget of 150 ms, erasing the line it wrote on and restoring the
    terminal. Answers that agree with the grapheme rule everywhere give
    grapheme; anything else gives codepoint; no answer gives codepoint
  - bytes read that were not an answer are handed back to the program as
    input rather than dropped, so a key pressed during the measurement
    is not eaten
  - telecli doctor does not measure: it reports the rule and whether it
    was measured, configured or the default, and says that auto is
    measured when the TUI starts
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
- Peer presence in the conversation header (PR-10A.4c, §3.1, §4.3:
  `Online · Connected · 2 queued`)
  - `internal/telegram/live_state_presence.go` keeps the chat type from
    `updateNewChat`, the status of a user from `updateUser` and
    `updateUserStatus`, and the online member count from
    `updateChatOnlineMemberCount`, and answers `ChatPresence(chatID)`. A
    private or a secret chat answers with the status of its user, a group
    with its count
  - the store keeps an identifier, a status and a bot flag and nothing
    else of a user: no name, no phone number, no usernames, not even an
    empty field shaped like one. `userRecord` has three fields, and the
    privacy test prints the whole store and looks for the fixture's name,
    phone, username and e-mail; a second test feeds the dump a chat title
    the store does keep, and a third fails if the dump stops reading every
    field of the struct
  - `updateUser` and `updateUserStatus` are **not** held during
    authorization. TDLib has no user data cached before the client is
    authorized, so there is nothing to hold; and holding `updateUser`
    would hold a name and a phone number
  - a presence constructor the pinned schema does not have is ignored
    rather than guessed, and the last known status stays: it is what
    Telegram said last
  - `AuthorizedSession.OpenChat`, `CloseChat` and `GetMeUserID` are the
    session's side. `updateChatOnlineMemberCount` arrives only for a chat
    that has been opened, so the interface says which chat it is looking
    at
  - the own identifier comes from one `getMe` with a five second bound, and
    it is what tells a chat with oneself from a chat with a contact:
    TDLib sends the current user as an ordinary user. A refusal is a line
    in the log, and the interface then shows no presence in Saved Messages
  - `internal/tui/presence.go` and `view_presence.go` draw it. An online
    status carries the deadline Telegram gave and the view decides from
    the model's clock whether it has run out, because TDLib sends nothing
    at that moment; one message at the moment the word changes repaints the
    screen and is not a repaint loop
  - times are drawn in the model's own zone, and a test pins both the
    moment and the zone
  - a test pins the moment in one place, `testClock`, and reads the
    machine's clock in exactly one other, `wallClock`, and only for a
    deadline or an elapsed measure. A test that builds what it asserts on
    out of the machine's clock is a test that fails on the day the data it
    built runs out, which is what `TestPresencePrintsWithoutTheStatus` did
    at 2026-09-28 15:00 UTC;
    `TestNoTestOfThisPackageBuildsItsDataFromTheWallClock` greps the test
    files of the package and names the file that read the clock for data
  - the presence is the first part of the status line and the last one
    dropped when the line does not fit: the queue can be read again in two
    seconds, and a person cannot
  - a presence prints as a kind and never as a status, in every verb a
    value takes on its way to a log. A presence is a fact about somebody
    who did not ask to be followed, and a log file is read by people who
    are not in the conversation. The screen is the other matter: it says
    when somebody was last there, in the reader's own words and zone, and
    what it must not print is the store's own year and day
- The action sheet (internal/tui/action_sheet.go, `action_sheet_view.go`,
  `action_sheet_keys.go`, `clipboard.go`, PR-10A.5, §5, §6.2, §8.3, §8.5,
  §12.3, §13, §14, §24)
  - `a` in the timeline opens the sheet over the message under the cursor,
    and the hint bar names it. The items are the table of §13 exactly, and
    there is no Retry anywhere: a retry is what the queue does on its own
    schedule
  - the sheet is a value on the model and not a screen, and it is the first
    level of the Esc hierarchy of §8.5. Esc closes it, and in an uncertain
    message closing it is how the user keeps the record
  - the cursor walks the pending messages as well as the history, so a
    queued message has a menu. The pending block is only drawn with the
    history scrolled to its end, and that is where the window goes
  - the entry, the version and the text are read when the sheet opens: a
    sheet over a record the queue has moved on would act on a state the
    user did not choose, and the version is what lets the queue refuse it
  - `Cancel message` and `Cancel record` go to
    `OutboxPendingMessageCanceller` with the version the screen read. A
    refusal that means the record moved (a version conflict, a forbidden
    transition, a cancel after acceptance, a record that is not there) says
    `This message changed state. Nothing was canceled.` and drops the
    record; anything else says `This message was not canceled.` The cause
    keeps its own text and goes to the diagnostic stream
  - `Create new message` of a failed message puts the text in the composer
    and nothing else: a program that re-queued a message on a menu item
    would send a message nobody confirmed
  - `Create a new message` of an uncertain message asks first (§12.3), the
    question starts on `Keep uncertain`, and `Esc` is `Cancel`
  - `Copy text` writes OSC 52 to the program's terminal and nowhere else.
    The notice says `Copied`, and with no terminal to write to it says the
    copy did not happen rather than claiming one
  - the program's output is one mutex-guarded writer (`terminalOutput`)
    given to Bubble Tea (`tea.WithOutput`) and to the model at the same
    time, because a copy written while a frame is painted lands inside
    that frame's escape sequence. It is also a `term.File`: Bubble Tea
    v1.3.10 takes its output as a terminal only through that interface, and
    a writer that is not one leaves the program without a size and without
    a resize
  - an action that answers says so in the status line, in a notice that one
    message takes away after three seconds. It is one message, not a repaint
    loop (§6.3)
  - a popup is a raised background, one accent line, a shadow column and an
    indent, and no frame. The rows are cut by column and not by byte, so a
    cut through a styled row cannot print half an escape sequence
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
- Text width: internal/tui/termwidth, a leaf package beside
  internal/tui/theme: WidthModel counts columns, Truncate and
  TruncateLeft cut at cluster boundaries in either rule, Wrap breaks
  prose on words, and Measure asks the terminal how it draws. It is the
  only place that answers "how wide is this", and internal/tui reaches
  it through Model.widths
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
  - the muted tier is a value the preset names (Palette.Muted) and not
    one worked out at run time: it is the first step of the walk from
    the dim step towards the text ramp that clears 4.5:1 on both
    surfaces a preview and a timestamp are drawn on, the walk lives in
    the test, and a golden file cannot be right on two machines when a
    colour comes out of floating-point arithmetic
  - contrast: PrimaryText and SecondaryText are at or above WCAG AA
    4.5:1 on AppBackground, SidebarBackground, ChatBackground and
    ComposerBackground in every built-in theme; MutedText, which draws
    the chat previews and the times, is held to the same bar on
    SidebarBackground and ChatBackground; and the tiers get dimmer in
    order. MutedText is computed rather than named: the dim step of the
    palette lifted towards the text ramp until it clears the bar on
    every surface it is drawn on, because no single step of any of the
    three palettes is both dim enough to be the tier below secondary and
    readable, and a palette written by a user would have the same
    trouble. A colour that is not set has no luminance, so a ratio with
    one is 0 and a test cannot be flattered by it
  - Selected is a background, not a foreground: it is the surface of the
    selected row of the list, and the name on that row is in Focus, so a
    selected chat is a row and never a marker. Every run of a selected row
    carries that surface and the gaps between the runs are written with
    it, because a run without it ends with SGR 0 and takes the surface of
    the row down with it: what is left is a one-column highlight that
    reads as a cursor. The unread badge is the one exception, and it keeps
    the background of the badge on every row: a count drawn in the colour
    of the list on the background of the selection is a number nobody can
    read
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
  - every colour of a preset names all three of its values: the hex a
    24-bit terminal shows, the entry of the 256-colour palette an indexed
    one shows, and the index of the basic palette a 16-colour one shows
    (`Complete(hex, indexed, basic)`; `Color.Print` is what a renderer is
    given). Only the true-colour profile uses the hex
  - they are named and not derived because the derivation is float work:
    two entries of a ramp can be the same distance from a value, and
    arm64 and amd64 round that tie differently, which is not a question
    about the interface but one a golden file has to have a single answer
    to. `TestEveryTokenNamesItsIndexedAndBasicEntry` says no built-in
    token may be left to the arithmetic, and a 16-colour terminal's
    surfaces are left unset (§2.7) so they have no basic index to name
  - a gradient is cut to at most three stops on an indexed terminal, and a
    16-colour terminal renders the basic colours with the palette the user
    configured
  - nothing depends on colour: the rule under a focused heading, the
    `›` of a selected row where there is no background to show, and the
    symbol-plus-words of every status are theme roles, not view helpers
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
- PR-10A: interface rewrite per docs/TUI_SPEC.md, accepted
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
  - PR-10A.4c peer presence in the conversation header (#41),
    open/close of the open chat: accepted
  - PR-10A.5 action sheet and the uncertain decision, cancel of a queued
    message, copy through OSC 52: accepted
  - PR-10A.6 chat search over the loaded chat list: accepted
  - PR-10A.7 golden screen snapshots of every screen, text and ANSI, with
    the §20 invariants over all of them: accepted
  - left out of PR-10A, found by the snapshots and not fixed there: with
    the cursor on an outgoing message that is not in the history yet,
    timelineLines draws the history from an index that is past the end of
    it (internal/tui/view_conversation.go), so the conversation above the
    message disappears; and the popup overlay keeps the head of the row it
    covers instead of its tail (overlayRow, TruncateLeft takes the
    number of columns to remove), so a menu is drawn twice as far to the
    right as it is wide. Both need a fix of their own
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

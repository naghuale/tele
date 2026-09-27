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
- Styling library: none yet
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
  - best-effort forwarding into session-facing Updates()/Errors()
    channels; drops do not block the process-wide receive loop
  - channel-drop policy is deferred to PR-08
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
  - responses with telecli-owned @extra never reach Updates()
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
  - snapshot-only: live chat-list updates are not yet handled
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
- Composer: internal/tui/model.go
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
- Data directory: ~/.local/share/telecli
- TDLib database directory: ~/.local/share/telecli/tdlib/database
- TDLib files directory: ~/.local/share/telecli/tdlib/files
- Config file: os.UserConfigDir()/telecli/config.toml, overridden by
  --config or TELECLI_CONFIG
- Cache directory: TBD

## Secrets
- Keyring backend:
  - outbox keys: macOS Keychain Services; Linux Secret Service
  - Telegram credential profiles: macOS Keychain Services only
    (internal/authstore, darwin + cgo); other platforms have no
    profile store and must use the environment
- Headless fallback: none; without a key provider the durable outbox
  fails closed and never creates a replacement key
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
  - history pagination UI: pending
  - live chat-list updates: pending
  - lossless update/resync policy: pending
- PR-07: text message sending
  - Telegram `sendMessage` wire layer: accepted
  - application and TUI send boundary: accepted
  - composer send state machine: accepted
  - manual real-account send integration: passed on macOS arm64
  - `sendMessage` response object: verified against TDLib 1.8.67
  - final delivery to recipient: not verified
  - delivery-state update tracking: deferred
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

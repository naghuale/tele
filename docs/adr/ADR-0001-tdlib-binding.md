# ADR-0001: TDLib modern JSON C API through an internal dynamic cgo loader

## Status

Accepted.

Verified on macOS arm64 against TDLib 1.8.67 at commit
`ea97bcdd3a15523c58ddfe772b4547187cf5bbeb`.

## Context

telecli requires a pinned and auditable TDLib integration for macOS and
Linux without delegating lifecycle ownership to a third-party Go
wrapper.

TDLib exposes a modern JSON C API through the following entry points:

- `td_create_client_id`
- `td_send`
- `td_receive`
- `td_execute`

The receive function is process-wide. It must not be called
concurrently, and received objects must be processed in receive order.

## Decision

Use an internal dynamic cgo loader in:

`internal/telegram/tdjson`

The loader uses `dlopen` and `dlsym`. It never calls `dlclose`: TDLib keeps
process-wide threads alive after its clients close, so unloading it is unsafe.

Supported platforms:

- macOS arm64 (verified)
- macOS amd64
- Linux amd64
- Linux arm64

Native TDLib support requires CGO.

telecli owns exactly one process-wide receive loop and routes incoming
JSON objects by their `@client_id` field.

The runtime version and commit are obtained synchronously through:

- `getOption("version")`
- `getOption("commit_hash")`

The runtime verified by PR-04 and PR-05 is:

- Version: `1.8.67`
- Commit: `ea97bcdd3a15523c58ddfe772b4547187cf5bbeb`

## Consequences

- Logical clients must never call `td_receive` directly.
- The process-wide receive loop must stop before the native loader close
  path is invoked.
- Native library lookup supports:
  - an explicit environment variable
  - configuration
  - a repository development path
  - the platform loader default
- Builds without CGO retain the pure-Go contracts and return a typed
  unavailable-runtime error.
- Linux runtime integration must be verified in Linux CI.
- In the verified integration harness, a freshly created logical client
  did not emit an initial authorization update before its first request.
- The integration harness sends `getAuthorizationState` as the first
  request.
- `getAuthorizationState` is an offline method, is permitted before
  initialization, and returns the current authorization state as a
  direct response.
- The integration parser in
  `internal/telegram/session_close_integration_test.go` recognizes:
  - the direct response format of `getAuthorizationState`
  - the update format of `updateAuthorizationState`

## Graceful close

Real-runtime client close through `close` and
`authorizationStateClosed` is verified on macOS arm64 for TDLib 1.8.67
at commit `ea97bcdd3a15523c58ddfe772b4547187cf5bbeb`.

The real-runtime integration harness verified this sequence:

1. the harness sends `{"@type":"close"}` to the logical client
2. TDLib emits `authorizationStateClosing`
3. TDLib emits `authorizationStateClosed`
4. the harness calls `Runtime.Close`, which invokes the native loader
   close path
5. the same database directory initializes successfully in a second
   runtime

The integration harness observed `authorizationStateClosing` before
`authorizationStateClosed` in both runs against the same database
directory.

`AuthorizedSession.Close` implements the same lifecycle and is covered
by pure-Go unit tests and race tests.

End-to-end execution of `AuthorizedSession.Close` against an authorized
real account remains pending the manual `tdlib_account_integration`
gate.

Linux verification remains a separate CI gate.

## Out of scope

- Telegram authorization against a real user account
  - tracked by the manual `tdlib_account_integration` gate
- chat and message loading
- sending user messages
- cross-process TDLib database ownership
- confirmed behavior on Linux before CI verification
- lossless delivery policy for application-facing Telegram updates

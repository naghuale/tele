#!/usr/bin/env bash
set -euo pipefail

readonly EXPECTED_BRANCH="${EXPECTED_BRANCH:-release/telecli-v0.1-bootstrap}"
readonly RELEASE_VERSION="${RELEASE_VERSION:-v0.1.0-rc1}"
readonly BUILDINFO_VERSION_VAR="${BUILDINFO_VERSION_VAR:-Version}"
readonly BUILDINFO_COMMIT_VAR="${BUILDINFO_COMMIT_VAR:-Commit}"
readonly BUILDINFO_BUILT_VAR="${BUILDINFO_BUILT_VAR:-Date}"
readonly DIST_DIR="${DIST_DIR:-dist}"
readonly BINARY_NAME="${BINARY_NAME:-telecli}"

log() {
  printf '\n===== %s =====\n' "$1"
}

fail() {
  printf 'FAIL: %s\n' "$1" >&2
  exit 1
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || fail "required command is missing: $1"
}

require_clean_repository() {
  local branch
  branch="$(git branch --show-current)"

  test "$branch" = "$EXPECTED_BRANCH" || \
    fail "branch is $branch, expected $EXPECTED_BRANCH"

  test -z "$(git status --porcelain)" || {
    git status --short
    fail "repository is not clean"
  }
}

check_linker_variable() {
  local variable="$1"
  local file
  local found=""
  local const_file=""

  while IFS= read -r file; do
    # A const declaration can never be set through -ldflags -X.
    if grep -Eq \
      "^[[:space:]]*const[[:space:]]+${variable}([[:space:]]+string)?[[:space:]]*=" \
      "$file"
    then
      const_file="$file"
      break
    fi

    # Single declaration: var Name = "default"
    if grep -Eq \
      "^[[:space:]]*var[[:space:]]+${variable}([[:space:]]+string)?[[:space:]]*=" \
      "$file"
    then
      found="$file"
      break
    fi

    # Grouped declaration inside a var ( ... ) or const ( ... ) block.
    if awk -v name="$variable" '
        /^[[:space:]]*var[[:space:]]*\(/ { inblock = "var"; next }
        /^[[:space:]]*const[[:space:]]*\(/ { inblock = "const"; next }
        inblock != "" && /^[[:space:]]*\)/ { inblock = ""; next }
        inblock == "const" && $1 == name { found = "const" }
        inblock == "var" && $1 == name { found = "var" }
        END { exit found == "var" ? 0 : 1 }
      ' "$file"
    then
      found="$file"
      break
    fi

    if awk -v name="$variable" '
        /^[[:space:]]*const[[:space:]]*\(/ { inblock = 1; next }
        inblock && /^[[:space:]]*\)/ { inblock = 0; next }
        inblock && $1 == name { found = 1 }
        END { exit found ? 0 : 1 }
      ' "$file"
    then
      const_file="$file"
      break
    fi
  done < <(find internal/buildinfo -type f -name '*.go' | sort)

  test -z "$const_file" || \
    fail "internal/buildinfo.${variable} is declared as const in ${const_file}"

  test -n "$found" || \
    fail "linker variable internal/buildinfo.${variable} is not an assignable package variable"

  printf '%s -> %s\n' "$variable" "$found"
}

run_security_checks() {
  test -z "$(
    grep -RInE \
      '&(gt|lt|amp|quot|apos|nbsp);|&#(39|34);' \
      --include='*.go' \
      . || true
  )" || fail "HTML entities found in Go files"

  test -z "$(
    grep -RIn \
      'telecli/internal/telegram' \
      internal/outbox || true
  )" || fail "internal/outbox imports Telegram"

  test -z "$(
    grep -RIn --include='*.go' \
      'NewMemoryStore\|outbox\.MemoryStore{' \
      cmd internal/application internal/tui \
      | grep -v '_test.go' || true
  )" || fail "production MemoryStore use found"

  test -z "$(
    grep -RInE \
      'SendTextMessage|QueueMessage' \
      internal/tui \
      --include='*.go' \
      | grep -v '_test.go' || true
  )" || fail "direct transport call found in TUI"
}

run_macos_c3b_gate() {
  test "$(uname -s)" = "Darwin" || \
    fail "C3b release script requires macOS"

  local files=(
    internal/outbox/keyprovider_secitem.go
    internal/outbox/keyprovider_secitem_test.go
    internal/outbox/keyprovider_darwin.go
    internal/outbox/keyprovider_darwin_nocgo.go
    internal/outbox/keyprovider_darwin_integration_test.go
  )

  local file
  for file in "${files[@]}"; do
    test -f "$file" || fail "missing C3b file: $file"
  done

  CGO_ENABLED=0 go test ./internal/outbox/... -run '^$' -count=1
  go test ./internal/outbox/... -run '^TestDarwinKeyProvider' -count=10
  go test ./internal/outbox/... -run '^TestDarwinKeyProvider' -race -count=10
  CGO_ENABLED=1 go build ./internal/outbox/...
  CGO_ENABLED=1 go test ./internal/outbox/... -count=1

  grep -qF 'var _ KeyProvider = (*darwinKeyProvider)(nil)' \
    internal/outbox/keyprovider_secitem.go || \
    fail "darwin KeyProvider compile-time assertion is missing"

  grep -RInE \
    'SecItemCopyMatching|SecItemAdd|SecItemDelete' \
    "${files[@]}" >/dev/null || \
    fail "SecItem API usage not found"
}

run_full_gate() {
  local unformatted
  unformatted="$(gofmt -l .)"
  test -z "$unformatted" || {
    printf '%s\n' "$unformatted"
    fail "gofmt check failed"
  }

  CGO_ENABLED=0 go test ./... -count=1
  CGO_ENABLED=0 go build ./...
  go test ./... -count=1
  go test -race ./... -count=1
  go vet ./...
}

build_artifact() {
  local buildinfo_path
  local commit
  local built
  local ldflags

  buildinfo_path="$(go list -f '{{.ImportPath}}' ./internal/buildinfo)"
  commit="$(git rev-parse --short=12 HEAD)"
  built="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"

  check_linker_variable "$BUILDINFO_VERSION_VAR"
  check_linker_variable "$BUILDINFO_COMMIT_VAR"
  check_linker_variable "$BUILDINFO_BUILT_VAR"

  ldflags="-X ${buildinfo_path}.${BUILDINFO_VERSION_VAR}=${RELEASE_VERSION}"
  ldflags+=" -X ${buildinfo_path}.${BUILDINFO_COMMIT_VAR}=${commit}"
  ldflags+=" -X ${buildinfo_path}.${BUILDINFO_BUILT_VAR}=${built}"

  rm -rf "$DIST_DIR"
  mkdir -p "$DIST_DIR"

  CGO_ENABLED=0 go build \
    -trimpath \
    -ldflags "$ldflags" \
    -o "${DIST_DIR}/${BINARY_NAME}" \
    ./cmd/telecli

  test -x "${DIST_DIR}/${BINARY_NAME}" || \
    fail "release binary was not created"

  printf 'version=%s\ncommit=%s\nbuilt=%s\n' \
    "$RELEASE_VERSION" \
    "$commit" \
    "$built" \
    > "${DIST_DIR}/${BINARY_NAME}.metadata"

  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "${DIST_DIR}/${BINARY_NAME}" \
      > "${DIST_DIR}/${BINARY_NAME}.sha256"
  else
    shasum -a 256 "${DIST_DIR}/${BINARY_NAME}" \
      > "${DIST_DIR}/${BINARY_NAME}.sha256"
  fi

  file "${DIST_DIR}/${BINARY_NAME}"
  cat "${DIST_DIR}/${BINARY_NAME}.metadata"
  cat "${DIST_DIR}/${BINARY_NAME}.sha256"
}

verify_metadata() {
  local output
  local expected_commit

  expected_commit="$(git rev-parse --short=12 HEAD)"

  if output="$("${DIST_DIR}/${BINARY_NAME}" version 2>&1)"; then
    :
  elif output="$("${DIST_DIR}/${BINARY_NAME}" --version 2>&1)"; then
    :
  else
    printf '%s\n' "$output"
    fail "version command failed"
  fi

  printf '%s\n' "$output"

  printf '%s\n' "$output" | grep -F "$RELEASE_VERSION" >/dev/null || \
    fail "release version is missing from version output"

  printf '%s\n' "$output" | grep -F "$expected_commit" >/dev/null || \
    fail "commit is missing from version output"

  if printf '%s\n' "$output" | grep -Eq '(^|[[:space:]])dev([[:space:]]|$)|commit none|built unknown'; then
    fail "default build metadata remains in version output"
  fi
}

verify_checksum() {
  local expected
  local actual

  expected="$(awk '{print $1}' "${DIST_DIR}/${BINARY_NAME}.sha256")"
  actual="$(shasum -a 256 "${DIST_DIR}/${BINARY_NAME}" | awk '{print $1}')"

  test "$actual" = "$expected" || \
    fail "checksum mismatch: expected $expected, got $actual"

  printf 'PASS: checksum verified: %s\n' "$actual"
}

verify_dist_ignored() {
  git check-ignore -q "${DIST_DIR}/${BINARY_NAME}" || \
    fail "release binary is not ignored"

  git check-ignore -q "${DIST_DIR}/${BINARY_NAME}.sha256" || \
    fail "checksum file is not ignored"

  git check-ignore -q "${DIST_DIR}/${BINARY_NAME}.metadata" || \
    fail "metadata file is not ignored"

  test -z "$(git status --porcelain)" || {
    git status --short
    fail "release build changed repository state"
  }
}

render_release_notes() {
  local template="RELEASE_NOTES_TEMPLATE.md"
  local output="${DIST_DIR}/RELEASE_NOTES.md"
  local commit
  local built
  local checksum

  test -f "$template" || {
    printf '%s\n' "SKIP: $template is not present"
    return
  }

  commit="$(git rev-parse --short=12 HEAD)"
  built="$(awk -F= '$1 == "built" {print $2}' "${DIST_DIR}/${BINARY_NAME}.metadata")"
  checksum="$(awk '{print $1}' "${DIST_DIR}/${BINARY_NAME}.sha256")"

  sed \
    -e "s/<COMMIT>/${commit}/g" \
    -e "s/<UTC_TIMESTAMP>/${built}/g" \
    -e "s/<SHA256>/${checksum}/g" \
    "$template" \
    > "$output"

  printf 'release notes: %s\n' "$output"
}

main() {
  require_command git
  require_command go
  require_command grep
  require_command find
  require_command file
  require_command shasum
  require_command awk

  log "Repository"
  require_clean_repository
  printf 'branch:  %s\n' "$(git branch --show-current)"
  printf 'HEAD:    %s\n' "$(git rev-parse --short=12 HEAD)"
  printf 'version: %s\n' "$RELEASE_VERSION"

  log "Security checks"
  run_security_checks

  log "Full automated gate"
  run_full_gate

  log "macOS Keychain C3b gate"
  run_macos_c3b_gate

  log "Build release artifact"
  build_artifact

  log "Verify build metadata"
  verify_metadata

  log "Verify checksum"
  verify_checksum

  log "Verify ignored artifacts"
  verify_dist_ignored

  log "Render release notes"
  render_release_notes

  log "Release result"
  printf '%s\n' 'PASS: automated release gate is green'
  printf 'artifact: %s/%s\n' "$DIST_DIR" "$BINARY_NAME"
  printf 'checksum: %s/%s.sha256\n' "$DIST_DIR" "$BINARY_NAME"
  printf '%s\n' 'MANUAL: direct interactive smoke remains required'
  printf '%s\n' 'MANUAL: durable and fail-closed macOS smoke remain required'
}

main "$@"

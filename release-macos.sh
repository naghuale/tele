#!/usr/bin/env bash
set -euo pipefail

readonly EXPECTED_BRANCH="${EXPECTED_BRANCH:-release/telecli-v0.1-bootstrap}"
readonly RELEASE_VERSION="${RELEASE_VERSION:-v0.1.0-rc1}"
readonly BUILDINFO_VERSION_VAR="${BUILDINFO_VERSION_VAR:-Version}"
readonly BUILDINFO_COMMIT_VAR="${BUILDINFO_COMMIT_VAR:-Commit}"
readonly BUILDINFO_BUILT_VAR="${BUILDINFO_BUILT_VAR:-Date}"
readonly DIST_DIR="${DIST_DIR:-dist}"
readonly BINARY_NAME="${BINARY_NAME:-telecli}"
readonly PACKAGE_SCRIPT="scripts/package_macos.sh"
readonly PACKAGE_VERIFY_SCRIPT="scripts/verify_package.sh"

# DIST_DIR reaches the packaging scripts as an environment variable, and
# the default above is only a shell variable until it is exported.
export DIST_DIR

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

# validate_dist_dir refuses a distribution directory that a targeted
# cleanup could damage.
#
# The script deletes named files inside DIST_DIR rather than the whole
# tree, so a mistyped or empty value would still be able to remove
# something outside the project. The guard makes the safe set explicit.
validate_dist_dir() {
  test -n "$DIST_DIR" || fail "DIST_DIR is empty"

  case "$DIST_DIR" in
    /|"$HOME"|"."|..|/*/../*)
      fail "unsafe DIST_DIR: $DIST_DIR"
      ;;
  esac

  local absolute
  absolute="$(cd "$DIST_DIR" 2>/dev/null && pwd -P || printf '%s' "$DIST_DIR")"

  case "$absolute" in
    /|"$HOME")
      fail "unsafe DIST_DIR: $absolute"
      ;;
  esac

  local repo_root
  repo_root="$(git rev-parse --show-toplevel 2>/dev/null || printf '')"
  if test -n "$repo_root" && test "$absolute" = "$repo_root"; then
    fail "DIST_DIR must not be the repository root"
  fi

  printf 'dist dir: %s\n' "$absolute"
}

# package_basename is the single source of truth for every package asset
# name, so cleanup and reporting cannot drift apart.
package_basename() {
  printf 'telecli_%s_darwin_arm64\n' "${RELEASE_VERSION#v}"
}

# generated_dist_files lists the files this script owns and may replace.
#
# Anything else in DIST_DIR belongs to the operator: release evidence,
# captured smoke output, local notes. Those files are never removed.
generated_dist_files() {
  local package
  package="$(package_basename)"

  # Only regular files are listed, and every package asset is named from
  # the current version. A wildcard such as *.manifest.json would let a
  # release delete the manifest of a different one. The package directory
  # is removed by the packager itself with rm -rf, and a plain rm -f on a
  # directory would fail under set -euo pipefail.
  printf '%s\n' \
    "${DIST_DIR}/${BINARY_NAME}" \
    "${DIST_DIR}/${BINARY_NAME}.sha256" \
    "${DIST_DIR}/${BINARY_NAME}.metadata" \
    "${DIST_DIR}/RELEASE_NOTES.md" \
    "${DIST_DIR}/${package}.tar.gz" \
    "${DIST_DIR}/${package}.tar.gz.sha256" \
    "${DIST_DIR}/${package}.manifest.json"
}

# preserve_release_evidence records the checksum of a manual smoke report
# so the build can prove it did not touch it.
preserve_release_evidence() {
  manual_report="$(
    find "$DIST_DIR" \
      -maxdepth 1 \
      -type f \
      -name 'MANUAL_SMOKE_REPORT_*.md' \
      -print \
      -quit 2>/dev/null || true
  )"

  if test -z "$manual_report"; then
    printf 'manual smoke report: none found\n'
    return 0
  fi

  evidence_checksum="$(
    shasum -a 256 "$manual_report" | awk '{print $1}'
  )"

  printf 'manual smoke report: %s\n' "$manual_report"
  printf 'evidence checksum:    %s\n' "$evidence_checksum"
}

# verify_release_evidence_unchanged fails when a manual smoke report was
# modified during the build.
verify_release_evidence_unchanged() {
  if test -z "${manual_report:-}"; then
    return 0
  fi

  test -f "$manual_report" || \
    fail "manual smoke report disappeared: $manual_report"

  local after
  after="$(shasum -a 256 "$manual_report" | awk '{print $1}')"

  test "$after" = "$evidence_checksum" || \
    fail "manual smoke report changed during release build"

  printf 'PASS: manual smoke report is unchanged\n'
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
  doctor_output=""

  check_linker_variable "$BUILDINFO_VERSION_VAR"
  check_linker_variable "$BUILDINFO_COMMIT_VAR"
  check_linker_variable "$BUILDINFO_BUILT_VAR"

  ldflags="-X ${buildinfo_path}.${BUILDINFO_VERSION_VAR}=${RELEASE_VERSION}"
  ldflags+=" -X ${buildinfo_path}.${BUILDINFO_COMMIT_VAR}=${commit}"
  ldflags+=" -X ${buildinfo_path}.${BUILDINFO_BUILT_VAR}=${built}"

  # Only the files this script generates are removed. Release evidence
  # and operator files in DIST_DIR survive every build.
  mkdir -p "$DIST_DIR"
  generated_dist_files | while IFS= read -r generated; do
    rm -f -- "$generated"
  done

  CGO_ENABLED=1 go build \
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

  test -n "${TELECLI_TDLIB_LIBRARY:-}" || \
    fail "TELECLI_TDLIB_LIBRARY is required for macOS release artifact"

  test -f "$TELECLI_TDLIB_LIBRARY" || \
    fail "TDLib library does not exist: $TELECLI_TDLIB_LIBRARY"

  doctor_output="$(
    TELECLI_TDLIB_LIBRARY="$TELECLI_TDLIB_LIBRARY" \
      "${DIST_DIR}/${BINARY_NAME}" doctor
  )" || fail "release artifact doctor command failed"

  printf '%s\n' "$doctor_output"

  printf '%s\n' "$doctor_output" |
    grep -F 'TDLib runtime: available' >/dev/null || \
    fail "release artifact cannot load TDLib"

  printf '%s\n' "$doctor_output" |
    grep -F 'TDLib compatibility: verified' >/dev/null || \
    fail "release artifact rejected TDLib compatibility"

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

# verify_configure_commands checks that the shipped artifact exposes the
# setup workflow.
#
# Only the help output is inspected. An interactive configure, a real
# credential prompt and configure reset against a live profile are never
# run from a release script.
verify_configure_commands() {
  local binary="${DIST_DIR}/${BINARY_NAME}"

  "$binary" configure --help >/dev/null || \
    fail "configure --help failed"

  "$binary" configure status --help >/dev/null || \
    fail "configure status --help failed"

  "$binary" configure reset --help >/dev/null || \
    fail "configure reset --help failed"

  printf 'PASS: configure, configure status and configure reset respond\n'
}

verify_dist_ignored() {
  local repo_root
  repo_root="$(git rev-parse --show-toplevel 2>/dev/null || printf '')"

  # The ignore rules only describe paths inside the repository. When
  # DIST_DIR points elsewhere the check has nothing to say, so it is
  # skipped instead of failing a legitimate out-of-tree build.
  if test -n "$repo_root"; then
    case "$(cd "$DIST_DIR" 2>/dev/null && pwd -P || printf '%s' "$DIST_DIR")" in
      "$repo_root"|"$repo_root"/*)
        git check-ignore -q "${DIST_DIR}/${BINARY_NAME}" || \
          fail "release binary is not ignored"

        git check-ignore -q "${DIST_DIR}/${BINARY_NAME}.sha256" || \
          fail "checksum file is not ignored"

        git check-ignore -q "${DIST_DIR}/${BINARY_NAME}.metadata" || \
          fail "metadata file is not ignored"
        ;;
      *)
        printf 'dist dir is outside the repository; ' \
          'ignore rules do not apply\n'
        ;;
    esac
  fi

  test -z "$(git status --porcelain)" || {
    git status --short
    fail "release build changed repository state"
  }
}

# resolve_package_inputs fills in the packager inputs.
#
# The TDLib version and commit are read from the Go source of truth rather
# than repeated here, so the manifest can never disagree with the runtime
# the binary verifies against. Every value stays overridable for a local
# probe or a different build machine.
resolve_package_inputs() {
  local manifest_source="internal/telegram/manifest.go"

  test -f "$manifest_source" || \
    fail "TDLib manifest source is missing: $manifest_source"

  # gofmt aligns the assignment, so the spacing before = is not fixed.
  TDLIB_VERSION="$(
    awk '/expectedTDLibVersion *= "/ {gsub(/"/, "", $3); print $3}' \
      "$manifest_source"
  )"
  TDLIB_COMMIT="$(
    awk '/expectedTDLibCommit *= "/ {gsub(/"/, "", $3); print $3}' \
      "$manifest_source"
  )"

  test -n "$TDLIB_VERSION" || fail "could not read the pinned TDLib version"
  test -n "$TDLIB_COMMIT" || fail "could not read the pinned TDLib commit"

  TDLIB_LIBRARY="${TDLIB_LIBRARY:-${TELECLI_TDLIB_LIBRARY:-}}"
  test -n "$TDLIB_LIBRARY" || \
    fail "TDLIB_LIBRARY (or TELECLI_TDLIB_LIBRARY) is required"
  test -f "$TDLIB_LIBRARY" || \
    fail "TDLib library does not exist: $TDLIB_LIBRARY"

  # The TDLib checkout is inferred from the library location unless it is
  # given, because the licence notice has to come from the real source.
  # The library normally sits in <checkout>/build, so the checkout is the
  # parent of its own directory.
  if test -z "${TDLIB_SOURCE_DIR:-}"; then
    TDLIB_SOURCE_DIR="$(cd "$(dirname "$TDLIB_LIBRARY")/.." \
      2>/dev/null && pwd -P || printf '')"
  fi
  test -n "${TDLIB_SOURCE_DIR:-}" || \
    fail "TDLIB_SOURCE_DIR could not be inferred; set it explicitly"
  test -f "${TDLIB_SOURCE_DIR}/LICENSE_1_0.txt" || \
    fail "TDLib licence is missing from ${TDLIB_SOURCE_DIR}; set TDLIB_SOURCE_DIR to the checkout root"

  require_command brew
  OPENSSL_PREFIX="${OPENSSL_PREFIX:-$(brew --prefix openssl@3)}"
  ZLIB_PREFIX="${ZLIB_PREFIX:-$(brew --prefix zlib)}"

  # The version comes from the installed formula, not from the ABI name
  # in libssl.3.dylib, which says nothing about the release.
  OPENSSL_VERSION="${OPENSSL_VERSION:-$(brew list --versions openssl@3 | awk '{print $2}')}"
  ZLIB_VERSION="${ZLIB_VERSION:-$(brew list --versions zlib | awk '{print $2}')}"

  test -n "$OPENSSL_VERSION" || fail "could not resolve the OpenSSL version"
  test -n "$ZLIB_VERSION" || fail "could not resolve the zlib version"

  export TDLIB_VERSION TDLIB_COMMIT TDLIB_LIBRARY TDLIB_SOURCE_DIR
  export OPENSSL_PREFIX OPENSSL_VERSION ZLIB_PREFIX ZLIB_VERSION

  printf 'tdlib:    %s (%s)\n' "$TDLIB_VERSION" "$TDLIB_COMMIT"
  printf 'tdlib src: %s\n' "$TDLIB_SOURCE_DIR"
  printf 'openssl:  %s (%s)\n' "$OPENSSL_VERSION" "$OPENSSL_PREFIX"
  printf 'zlib:     %s (%s)\n' "$ZLIB_VERSION" "$ZLIB_PREFIX"
}

# build_relocatable_package produces the self-contained package and
# verifies it independently.
#
# The single-binary artifact stays in DIST_DIR for the existing checks; the
# package is the redistributable unit and carries its own TDLib runtime.
build_relocatable_package() {
  local package
  local root

  package="$(package_basename)"
  root="${DIST_DIR}/${package}"

  test -x "$PACKAGE_SCRIPT" || fail "packager is not executable: $PACKAGE_SCRIPT"
  test -x "$PACKAGE_VERIFY_SCRIPT" || \
    fail "package verifier is not executable: $PACKAGE_VERIFY_SCRIPT"

  PACKAGE_VERSION="$RELEASE_VERSION" \
  PACKAGE_CHANNEL="${PACKAGE_CHANNEL:-prerelease}" \
    ./"$PACKAGE_SCRIPT" || fail "package build failed"

  test -d "$root" || fail "package root was not created: $root"
  test -f "${DIST_DIR}/${package}.tar.gz" || \
    fail "package archive was not created"

  # The external manifest, the archive and the archive checksum are part
  # of the release contract, so they are verified alongside the unpacked
  # package rather than only after publication.
  ./"$PACKAGE_VERIFY_SCRIPT" "$root" --dist "$DIST_DIR" || \
    fail "package verification failed"

  printf 'package: %s\n' "$root"
  printf 'archive: %s/%s.tar.gz\n' "$DIST_DIR" "$package"
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
  validate_dist_dir
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

  log "Preserve release evidence"
  preserve_release_evidence

  log "Build release artifact"
  build_artifact

  log "Verify build metadata"
  verify_metadata

  log "Verify checksum"
  verify_checksum

  log "Verify ignored artifacts"
  verify_dist_ignored

  log "Verify preserved release evidence"
  verify_release_evidence_unchanged

  log "Verify configure subcommands"
  verify_configure_commands

  log "Render release notes"
  render_release_notes

  log "Resolve package inputs"
  resolve_package_inputs

  log "Build relocatable package"
  build_relocatable_package

  log "Verify preserved release evidence"
  verify_release_evidence_unchanged

  log "Release result"
  printf '%s\n' 'PASS: automated release gate is green'
  printf 'artifact: %s/%s\n' "$DIST_DIR" "$BINARY_NAME"
  printf 'checksum: %s/%s.sha256\n' "$DIST_DIR" "$BINARY_NAME"
  printf 'package:  %s/%s\n' "$DIST_DIR" "$(package_basename)"
  printf 'archive:  %s/%s.tar.gz\n' "$DIST_DIR" "$(package_basename)"
  printf '%s\n' 'NOTE: the package is unsigned; Mach-O files are ad-hoc sealed'
  printf '%s\n' 'MANUAL: direct interactive smoke remains required'
  printf '%s\n' 'MANUAL: durable and fail-closed macOS smoke remain required'
}

main "$@"

#!/usr/bin/env bash
#
# Tests for release-macos.sh cleanup and evidence preservation.
#
# Every case runs against a temporary git fixture and a temporary dist
# directory. The real dist/ and the real repository are never touched.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd -P)"
RELEASE_SCRIPT="$REPO_ROOT/release-macos.sh"
NOTES_TEMPLATE="$REPO_ROOT/RELEASE_NOTES_TEMPLATE.md"

passed=0
failed=0
current=""

start() {
  current="$1"
  printf '\n--- %s\n' "$current"
}

pass() {
  passed=$((passed + 1))
  printf '  ok   %s\n' "$1"
}

fail() {
  failed=$((failed + 1))
  printf '  FAIL %s\n' "$1"
}

check() {
  if test "$2" = "yes"; then
    pass "$1"
  else
    fail "$1"
  fi
}

# rejects runs a guard in a subshell and requires it to refuse.
#
# The guard calls fail(), which exits, so it must not run in the test
# process itself. "yes" means the guard correctly rejected the input.
rejects() {
  local label="$1"
  shift

  if ( "$@" ) >/dev/null 2>&1; then
    check "$label" "no"
  else
    check "$label" "yes"
  fi
}

# make_fixture creates a throwaway repository and dist directory.
#
# The fixture copies the release script so the real one cannot be
# affected by a test.
make_fixture() {
  FIXTURE="$(mktemp -d "${TMPDIR:-/tmp}/telecli-release-test.XXXXXX")"
  DIST="$FIXTURE/dist"

  mkdir -p "$DIST"
  cp "$RELEASE_SCRIPT" "$FIXTURE/release-macos.sh"
  chmod +x "$FIXTURE/release-macos.sh"

  printf 'placeholder\n' > "$DIST/${BINARY_NAME:-telecli}"
}

drop_fixture() {
  test -n "${FIXTURE:-}" && rm -rf "$FIXTURE"
  FIXTURE=""
}

# extract_function prints a shell function from the release script so
# its behaviour can be asserted without running a full release.
extract_function() {
  local name="$1"

  awk -v want="$name" '
    $0 ~ "^" want "\\(\\)" {inside = 1}
    inside {print}
    inside && /^}/ {exit}
  ' "$RELEASE_SCRIPT"
}

# load_functions evaluates the helpers under test in the current shell.
load_functions() {
  fail_fn() { printf 'FAIL: %s\n' "$1" >&2; exit 1; }
  log() { :; }
  DIST_DIR="$DIST"
  BINARY_NAME="telecli"
  RELEASE_VERSION="v0.1.0-rc1"

  # shellcheck disable=SC1090
  eval "$(extract_function fail)"
  # shellcheck disable=SC1090
  eval "$(extract_function validate_dist_dir)"
  # shellcheck disable=SC1090
  eval "$(extract_function package_basename)"
  # shellcheck disable=SC1090
  eval "$(extract_function generated_dist_files)"
  # shellcheck disable=SC1090
  eval "$(extract_function preserve_release_evidence)"
  # shellcheck disable=SC1090
  eval "$(extract_function verify_release_evidence_unchanged)"
}

# ---- syntax -------------------------------------------------------------

start "release script parses"
if bash -n "$RELEASE_SCRIPT" 2>/dev/null; then
  pass "bash -n release-macos.sh"
else
  fail "bash -n release-macos.sh"
fi

start "release script has no HTML entities"
if grep -qE '&(gt|lt|amp|quot|apos|nbsp);|&#(39|34);' \
  "$RELEASE_SCRIPT" "$NOTES_TEMPLATE" 2>/dev/null; then
  fail "HTML entities found"
else
  pass "no HTML entities"
fi

# ---- targeted cleanup ---------------------------------------------------

start "generated files are the only ones removed"
make_fixture
load_functions

printf 'operator evidence\n' > "$DIST/MANUAL_SMOKE_REPORT_TEST.md"
printf 'captured output\n' > "$DIST/direct-smoke.stderr"
printf 'operator note\n' > "$DIST/NOTES.md"

generated_dist_files | while IFS= read -r generated; do
  rm -f -- "$generated"
done

test -f "$DIST/MANUAL_SMOKE_REPORT_TEST.md" && r=yes || r=no
check "manual smoke report survives" "$r"
test -f "$DIST/direct-smoke.stderr" && r=yes || r=no
check "captured stderr survives" "$r"
test -f "$DIST/NOTES.md" && r=yes || r=no
check "operator note survives" "$r"
test -f "$DIST/telecli" && r=no || r=yes
check "generated binary is removed" "$r"
drop_fixture

start "generated list covers binary, checksum, metadata and notes"
make_fixture
load_functions
expected='telecli
telecli.sha256
telecli.metadata
RELEASE_NOTES.md
telecli_0.1.0-rc1_darwin_arm64.tar.gz
telecli_0.1.0-rc1_darwin_arm64.tar.gz.sha256
telecli_0.1.0-rc1_darwin_arm64.manifest.json'
actual="$(generated_dist_files | while IFS= read -r f; do
  printf '%s\n' "$(basename "$f")"
done)"
test "$actual" = "$expected" && r=yes || r=no
check "generated list covers the binary, notes and package archives" "$r"
drop_fixture

start "package assets are named from the release version"
for version in v0.1.0-rc1 v0.2.0; do
  RELEASE_VERSION="$version"
  got="$(package_basename)"
  want="telecli_${version#v}_darwin_arm64"
  test "$got" = "$want" && r=yes || r=no
  check "package_basename for $version is $want" "$r"
done
RELEASE_VERSION="v0.1.0-rc1"

start "generated list excludes the package directory"
# rm -f on a directory would fail under set -euo pipefail, and the
# packager removes the directory itself.
if generated_dist_files | grep -q "_darwin_arm64$"; then
  fail "generated list must not contain the package directory"
else
  pass "generated list contains no directory entry"
fi

start "release script no longer removes the whole dist tree"
if grep -qE 'rm -rf +"?\$?\{?DIST_DIR' "$RELEASE_SCRIPT"; then
  fail "rm -rf DIST_DIR is still present"
else
  pass "no rm -rf DIST_DIR"
fi

# ---- DIST_DIR guard -----------------------------------------------------

start "empty DIST_DIR is rejected"
make_fixture
load_functions
DIST_DIR=""
rejects "empty DIST_DIR is refused" validate_dist_dir
drop_fixture

start "root and home DIST_DIR are rejected"
for candidate in "/" "$HOME" "." ".."; do
  make_fixture
  load_functions
  DIST_DIR="$candidate"
  rejects "DIST_DIR=$candidate is refused" validate_dist_dir
  drop_fixture
done

start "repository root as DIST_DIR is rejected"
make_fixture
load_functions
DIST_DIR="$REPO_ROOT"
rejects "repository root is refused" validate_dist_dir
drop_fixture

start "parent traversal in DIST_DIR is rejected"
make_fixture
load_functions
DIST_DIR="$DIST/../.."
rejects "parent traversal is refused" validate_dist_dir
drop_fixture

start "a normal DIST_DIR is accepted"
make_fixture
load_functions
( validate_dist_dir ) >/dev/null 2>&1 && r=yes || r=no
check "temporary dist dir passes" "$r"
drop_fixture

# ---- evidence preservation ---------------------------------------------

start "manual smoke report is checksummed before and after the build"
make_fixture
load_functions

printf 'release evidence probe\n' > "$DIST/MANUAL_SMOKE_REPORT_TEST.md"

# These run in the test process so the recorded path and checksum are
# visible to the checks that follow. Neither calls fail() on this path.
preserve_release_evidence >/dev/null
test -n "${manual_report:-}" && r=yes || r=no
check "evidence path recorded" "$r"
test -n "${evidence_checksum:-}" && r=yes || r=no
check "evidence checksum recorded" "$r"

# Simulate the build touching only generated files.
printf 'new binary\n' > "$DIST/telecli"
verify_release_evidence_unchanged >/dev/null 2>&1 && r=yes || r=no
check "unchanged evidence passes" "$r"

printf 'tampered\n' > "$manual_report"
rejects "modified evidence is refused" verify_release_evidence_unchanged
drop_fixture

start "missing report is not an error"
make_fixture
load_functions
preserve_release_evidence >/dev/null
verify_release_evidence_unchanged >/dev/null 2>&1 && r=yes || r=no
check "absent evidence passes" "$r"
drop_fixture

start "disappearing report fails the verification"
make_fixture
load_functions
printf 'evidence\n' > "$DIST/MANUAL_SMOKE_REPORT_TEST.md"
preserve_release_evidence >/dev/null
rm -f "$manual_report"
rejects "vanished evidence is refused" verify_release_evidence_unchanged
drop_fixture

# ---- main wiring --------------------------------------------------------

start "main runs the guard, the evidence steps and the configure check"
main_body="$(extract_function main)"

for step in \
  'validate_dist_dir' \
  'preserve_release_evidence' \
  'verify_release_evidence_unchanged' \
  'verify_configure_commands'
do
  if printf '%s\n' "$main_body" | grep -qE "^[[:space:]]+$step"; then
    pass "main calls $step"
  else
    fail "main does not call $step"
  fi
done

start "main preserves evidence before the build and verifies it after"
build_line="$(printf '%s\n' "$main_body" |
  grep -nE 'build_artifact' | head -1 | cut -d: -f1)"
before_line="$(printf '%s\n' "$main_body" |
  grep -nE 'preserve_release_evidence' | head -1 | cut -d: -f1)"
after_line="$(printf '%s\n' "$main_body" |
  grep -nE 'verify_release_evidence_unchanged' | head -1 | cut -d: -f1)"

if test -n "$build_line" && test -n "$before_line" && test -n "$after_line" &&
  test "$before_line" -lt "$build_line" &&
  test "$after_line" -gt "$build_line"; then
  pass "evidence is recorded before and checked after the build"
else
  fail "evidence steps are not ordered around the build"
fi

# ---- configure validation ----------------------------------------------

start "release script validates configure subcommands"
if grep -q 'verify_configure_commands' "$RELEASE_SCRIPT" &&
  grep -q 'configure status --help' "$RELEASE_SCRIPT" &&
  grep -q 'configure reset --help' "$RELEASE_SCRIPT"; then
  pass "configure subcommands are checked"
else
  fail "configure subcommands are not checked"
fi

start "release script never runs an interactive configure or a live reset"
if grep -qE 'configure[[:space:]]+(-config[=[:space:]]|status[[:space:]]*$|reset[[:space:]]+--config)' \
  "$RELEASE_SCRIPT"; then
  fail "an interactive or destructive configure call is present"
else
  pass "only help output is inspected"
fi

# ---- notes template -----------------------------------------------------

start "release notes template covers the implemented work"
for needle in \
  'setLogVerbosityLevel' \
  'new_verbosity_level = 1' \
  'Unexpected setTdlibParameters' \
  'CGO_ENABLED=1' \
  'ea97bcdd3a15523c58ddfe772b4547187cf5bbeb' \
  'telecli configure status' \
  'telecli configure reset' \
  'telecli-auth' \
  'TELECLI_CONFIG' \
  'Release hygiene'
do
  if grep -qF "$needle" "$NOTES_TEMPLATE"; then
    pass "template mentions $needle"
  else
    fail "template is missing $needle"
  fi
done

start "release notes template keeps unverified smokes honest"
if grep -qE 'Direct interactive smoke: NOT VERIFIED' "$NOTES_TEMPLATE" &&
  grep -qE 'Durable interactive smoke .*: NOT VERIFIED' "$NOTES_TEMPLATE" &&
  grep -qE 'Fail-closed Keychain smoke: NOT VERIFIED' "$NOTES_TEMPLATE"; then
  pass "smokes stay NOT VERIFIED until performed"
else
  fail "smoke status was overstated"
fi

start "release notes template keeps the placeholders the renderer needs"
for placeholder in '<COMMIT>' '<UTC_TIMESTAMP>' '<SHA256>'; do
  if grep -qF "$placeholder" "$NOTES_TEMPLATE"; then
    pass "placeholder $placeholder present"
  else
    fail "placeholder $placeholder missing"
  fi
done

# ---- result -------------------------------------------------------------

printf '\n===== result =====\n'
printf 'passed: %d\n' "$passed"
printf 'failed: %d\n' "$failed"

test "$failed" -eq 0

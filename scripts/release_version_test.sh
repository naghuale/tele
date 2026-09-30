#!/usr/bin/env bash
#
# Tests for scripts/release_version.sh.
#
# Every case runs against a throwaway repository: the real CHANGELOG.md,
# the real git history and the real release-macos.sh are never touched.
# The release itself is never pushed anywhere; a tag in a fixture is not a
# release of anything.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd -P)"
RELEASE_SCRIPT="$SCRIPT_DIR/release_version.sh"
CHANGELOG_TEMPLATE="$REPO_ROOT/CHANGELOG.md"

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

# rejects runs the release in a subshell and requires it to refuse.
rejects() {
  local label="$1"
  shift

  if run_release "$@" >/dev/null 2>&1; then
    check "$label" "no"
  else
    check "$label" "yes"
  fi
}

# accepts runs the release in a subshell and requires it to succeed.
accepts() {
  local label="$1"
  shift

  if run_release "$@" >/dev/null 2>&1; then
    check "$label" "yes"
  else
    check "$label" "no"
    tail -5 "$FIXTURE/last.log"
  fi
}

# run_release runs the script inside the fixture repository.
run_release() {
  ( cd "$FIXTURE" && ./scripts/release_version.sh "$@" ) >"$FIXTURE/last.log" 2>&1
}

git_fixture() {
  git -C "$FIXTURE" \
    -c user.name=telecli-test \
    -c user.email=test@example.com \
    "$@"
}

# write_changelog writes the changelog shape the script has to handle: a
# heading, a section with entries, an older version and its link.
write_changelog() {
  cat >"$FIXTURE/CHANGELOG.md" <<'CHANGELOG'
# Журнал изменений

Разделы идут от новых к старым.

## Не выпущено

### Добавлено

- строка, которую человек прочитает
  [#1](https://github.com/naghuale/tele/pull/1)

## [v0.1.0-rc1] - 2026-09-26

### Добавлено

- старая строка, которую нельзя потерять

[v0.1.0-rc1]: https://github.com/naghuale/tele/releases/tag/v0.1.0-rc1
CHANGELOG
}

# make_fixture creates a throwaway repository with the script under test,
# a changelog and a stub of the release script.
#
# The stub records the environment it was given instead of building
# anything, so a test can see that the version reached the build.
make_fixture() {
  FIXTURE="$(mktemp -d "${TMPDIR:-/tmp}/telecli-version-test.XXXXXX")"
  BUILD_RECORD="$FIXTURE/build.env"

  mkdir -p "$FIXTURE/scripts"
  cp "$RELEASE_SCRIPT" "$FIXTURE/scripts/release_version.sh"
  chmod +x "$FIXTURE/scripts/release_version.sh"

  cat >"$FIXTURE/release-macos.sh" <<'STUB'
#!/usr/bin/env bash
printf 'version=%s\nbranch=%s\n' \
  "${RELEASE_VERSION:-}" "${EXPECTED_BRANCH:-}" >"$BUILD_RECORD"
STUB
  chmod +x "$FIXTURE/release-macos.sh"
  export BUILD_RECORD

  # The test writes its own notes into the fixture, and an untracked file
  # is a dirty tree for the script under test.
  cat >"$FIXTURE/.gitignore" <<'IGNORE'
build.env
entries.*
last.log
IGNORE

  git -C "$FIXTURE" init -q -b main

  write_changelog
  git_fixture add -A
  git_fixture commit -q -m "fixture"
}

drop_fixture() {
  test -n "${FIXTURE:-}" && rm -rf "$FIXTURE"
  FIXTURE=""
}

# entries prints every list item of a changelog, so a test can compare the
# wording before and after a release.
entries() {
  grep -E '^- ' "$1"
}

# heading prints the version headings of a changelog, newest first.
heading() {
  grep -E '^## \[' "$1"
}

# section_is_empty reports whether the unreleased section holds no list
# item, which is what a release has to leave behind.
section_is_empty() {
  local file="$1"

  awk '/^## Не выпущено$/ {inside = 1; next}
       /^## / {inside = 0}
       inside && /^- / {found = 1}
       END {exit found ? 1 : 0}' "$file" && printf 'yes' || printf 'no'
}

# ---- syntax -------------------------------------------------------------

start "the release script parses"
if bash -n "$RELEASE_SCRIPT" 2>/dev/null; then
  pass "bash -n release_version.sh"
else
  fail "bash -n release_version.sh"
fi

start "the release script is executable"
if test -x "$RELEASE_SCRIPT"; then
  pass "release_version.sh is executable"
else
  fail "release_version.sh is executable"
fi

# ---- refusals -----------------------------------------------------------

start "a version that is not SemVer is refused"
make_fixture
for bad in 0.1.0 v1.2 v1.2.3.4 v01.2.3 latest ""; do
  rejects "refused: ${bad:-no version at all}" "$bad"
done
drop_fixture

start "a date that is not YYYY-MM-DD is refused"
make_fixture
rejects "refused: 02.10.2026" v0.2.0 --date 02.10.2026
rejects "refused: yesterday" v0.2.0 --date yesterday
drop_fixture

start "a release outside the default branch is refused"
make_fixture
git_fixture checkout -q -b side
rejects "refused on branch side" v0.2.0
git_fixture checkout -q main
accepts "accepted back on main" v0.2.0
drop_fixture

start "a release from a dirty tree is refused"
make_fixture
printf '\nчерновик\n' >>"$FIXTURE/CHANGELOG.md"
rejects "refused with an uncommitted change" v0.2.0
git_fixture checkout -q -- CHANGELOG.md
accepts "accepted once the tree is clean" v0.2.0
drop_fixture

start "a tag that already exists is refused"
make_fixture
git_fixture tag v0.2.0
rejects "refused: the tag v0.2.0 is taken" v0.2.0
rejects "refused: --tag would overwrite it" v0.2.0 --tag
accepts "accepted: another number is free" v0.3.0
drop_fixture

start "an empty unreleased section is refused"
make_fixture
cat >"$FIXTURE/CHANGELOG.md" <<'CHANGELOG'
# Журнал изменений

## Не выпущено

## [v0.1.0-rc1] - 2026-09-26

- старая строка
CHANGELOG
git_fixture commit -q -am "empty section"
rejects "refused: nothing to release" v0.2.0
drop_fixture

start "a changelog without an unreleased section is refused"
make_fixture
cat >"$FIXTURE/CHANGELOG.md" <<'CHANGELOG'
# Журнал изменений

## [v0.1.0-rc1] - 2026-09-26

- старяя строка
CHANGELOG
git_fixture commit -q -am "no unreleased section"
rejects "refused: no section to release" v0.2.0
drop_fixture

start "a version that already has a section is refused"
make_fixture
cat >>"$FIXTURE/CHANGELOG.md" <<'CHANGELOG'

## [v0.2.0] - 2026-10-01

- строка будущего выпуска
CHANGELOG
git_fixture commit -q -am "a section for v0.2.0"
rejects "refused: v0.2.0 is already written down" v0.2.0
drop_fixture

# ---- the rewrite --------------------------------------------------------

start "the unreleased section takes the number and the date"
make_fixture
entries "$FIXTURE/CHANGELOG.md" >"$FIXTURE/entries.before"
accepts "the release runs" v0.2.0 --date 2026-10-02
check "the section became the version" \
  "$(grep -qxF '## [v0.2.0] - 2026-10-02' "$FIXTURE/CHANGELOG.md" && echo yes || echo no)"
check "one unreleased section is left above it" \
  "$(test "$(grep -cxF '## Не выпущено' "$FIXTURE/CHANGELOG.md")" = 1 && echo yes || echo no)"
check "the unreleased section is above the release" \
  "$(test "$(grep -nxF '## Не выпущено' "$FIXTURE/CHANGELOG.md" | cut -d: -f1)" \
    -lt "$(grep -nxF '## [v0.2.0] - 2026-10-02' "$FIXTURE/CHANGELOG.md" | cut -d: -f1)" \
    && echo yes || echo no)"
check "the new section is empty" \
  "$(test "$(section_is_empty "$FIXTURE/CHANGELOG.md")" = yes && echo yes || echo no)"
entries "$FIXTURE/CHANGELOG.md" >"$FIXTURE/entries.after"
check "no entry was lost" \
  "$(diff -q "$FIXTURE/entries.before" "$FIXTURE/entries.after" >/dev/null && echo yes || echo no)"
check "the older version is untouched" \
  "$(grep -qxF '## [v0.1.0-rc1] - 2026-09-26' "$FIXTURE/CHANGELOG.md" && echo yes || echo no)"
check "the link of the release is in the file" \
  "$(grep -qxF '[v0.2.0]: https://github.com/naghuale/tele/releases/tag/v0.2.0' \
    "$FIXTURE/CHANGELOG.md" && echo yes || echo no)"
check "the link of the older version survived" \
  "$(grep -qxF '[v0.1.0-rc1]: https://github.com/naghuale/tele/releases/tag/v0.1.0-rc1' \
    "$FIXTURE/CHANGELOG.md" && echo yes || echo no)"
check "the file still parses as a sequence of headings" \
  "$(test "$(grep -cE '^## ' "$FIXTURE/CHANGELOG.md")" = 3 && echo yes || echo no)"
git_fixture commit -q -am "release v0.2.0"
rejects "a second release of the same number is refused" v0.2.0
drop_fixture

start "a release without links yet gets one block"
make_fixture
cat >"$FIXTURE/CHANGELOG.md" <<'CHANGELOG'
# Журнал изменений

## Не выпущено

### Исправлено

- строка без ссылок внизу файла

## [v0.1.0-rc1] - 2026-09-26

- старая строка
CHANGELOG
git_fixture commit -q -am "no link block"
accepts "the release runs" v0.2.0
check "the file ends with the link of the release" \
  "$(test "$(tail -1 "$FIXTURE/CHANGELOG.md")" = \
    '[v0.2.0]: https://github.com/naghuale/tele/releases/tag/v0.2.0' && echo yes || echo no)"
check "one blank line separates the entries from the links" \
  "$(test "$(tail -2 "$FIXTURE/CHANGELOG.md" | head -1)" = "" && \
    test "$(tail -3 "$FIXTURE/CHANGELOG.md" | head -1)" != "" && echo yes || echo no)"
drop_fixture

start "the real changelog releases as it stands"
make_fixture
cp "$CHANGELOG_TEMPLATE" "$FIXTURE/CHANGELOG.md"
git_fixture commit -q -am "the real changelog"
accepts "the release runs against the real file" v0.1.0
check "the released section is on top" \
  "$(grep -qxF '## [v0.1.0] - 2026-09-30' "$FIXTURE/CHANGELOG.md" && echo yes || echo no)"
check "every merged pull request kept its line" \
  "$(for number in 47 46 45 40 36 34 32 30 28 24 23 20 18 12 8 7 5 4 3 2 61 58 56 52 51; do
      grep -q "pull/$number)" "$FIXTURE/CHANGELOG.md" || exit 1
    done && echo yes || echo no)"
check "the rc1 section survived" \
  "$(grep -qxF '## [v0.1.0-rc1] - 2026-09-26' "$FIXTURE/CHANGELOG.md" && echo yes || echo no)"
# A line that promises what the interface does not do is worse than a
# missing line: the journal is read instead of the code.
check "no line promises a chat list that updates itself" \
  "$(grep -q 'список чатов обновляется сам' "$FIXTURE/CHANGELOG.md" && echo no || echo yes)"
check "no line promises messages arriving on their own" \
  "$(grep -q 'приходят в ленту сразу' "$FIXTURE/CHANGELOG.md" && echo no || echo yes)"
check "the loading of the chat list is named instead" \
  "$(grep -q 'Loading chats' "$FIXTURE/CHANGELOG.md" && echo yes || echo no)"
drop_fixture

# ---- the tag and the build ---------------------------------------------

start "--tag commits the change and cuts the tag"
make_fixture
accepts "the release runs" v0.2.0 --date 2026-10-02 --tag
check "the tag exists" \
  "$(test "$(git_fixture tag -l v0.2.0)" = "v0.2.0" && echo yes || echo no)"
check "the tag is annotated" \
  "$(test "$(git_fixture cat-file -t v0.2.0)" = "tag" && echo yes || echo no)"
check "the tag points at the commit with the release" \
  "$(test "$(git_fixture rev-list -n1 v0.2.0)" = \
    "$(git_fixture rev-parse HEAD)" && echo yes || echo no)"
check "the commit carries the changelog" \
  "$(git_fixture show --name-only --format= HEAD | grep -qxF 'CHANGELOG.md' \
    && echo yes || echo no)"
check "the working tree is clean afterwards" \
  "$(test -z "$(git_fixture status --porcelain)" && echo yes || echo no)"
drop_fixture

start "--build stamps the build with the version"
make_fixture
rejects "refused: the build without a tag" v0.2.0 --build
accepts "the release runs" v0.2.0 --tag --build
check "the build was given the version" \
  "$(grep -qxF 'version=v0.2.0' "$BUILD_RECORD" && echo yes || echo no)"
check "the build was given the branch" \
  "$(grep -qxF 'branch=main' "$BUILD_RECORD" && echo yes || echo no)"
drop_fixture

start "the release says what to do next"
make_fixture
run_release v0.2.0
check "the commit command is printed" \
  "$(grep -q 'git commit -m' "$FIXTURE/last.log" && echo yes || echo no)"
check "the tag command is printed" \
  "$(grep -q 'git tag -a v0.2.0' "$FIXTURE/last.log" && echo yes || echo no)"
check "the build command carries the version" \
  "$(grep -q 'RELEASE_VERSION=v0.2.0' "$FIXTURE/last.log" && echo yes || echo no)"
drop_fixture

# ---- result -------------------------------------------------------------

printf '\n===== result =====\n'
printf 'passed: %d\n' "$passed"
printf 'failed: %d\n' "$failed"

test "$failed" -eq 0

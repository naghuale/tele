#!/usr/bin/env bash
#
# release_version.sh turns the "Не выпущено" section of CHANGELOG.md into
# a numbered, dated version and tags it.
#
# The question "what changed in this version" has to be answered by one
# file, and the build has to know its own version instead of printing
# "dev". Both facts are made here rather than left to memory:
#
#   1. the section gets the number and the date of the release;
#   2. a fresh empty "Не выпущено" section opens above it;
#   3. the version is linked at the bottom of the file;
#   4. with --tag the change is committed and an annotated tag is made;
#   5. with --build release-macos.sh runs with RELEASE_VERSION set, so the
#      binary is stamped with this version through -ldflags.
#
# The guards are the point of the script. A release must not be cut from a
# dirty tree, from a branch other than the default one, from a tag that
# already exists, or from an empty section: each of those would put a
# version number in the changelog that no build can be trusted to match.
#
# Usage:
#   scripts/release_version.sh v0.1.0
#   scripts/release_version.sh v0.1.0 --date 2026-10-02 --tag --build

set -euo pipefail

readonly SCRIPT_NAME="${BASH_SOURCE[0]##*/}"
readonly SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
readonly REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd -P)"

readonly CHANGELOG="$REPO_ROOT/CHANGELOG.md"
readonly UNRELEASED_HEADING="## Не выпущено"
readonly EMPTY_SECTION="<!-- nothing is written for the next release yet -->"
readonly RELEASES_URL="https://github.com/naghuale/tele/releases/tag"

# release-macos.sh requires the branch to match, and its own default names
# the bootstrap branch. Releasing from main means saying so.
readonly EXPECTED_BRANCH="${EXPECTED_BRANCH:-main}"
readonly RELEASE_SCRIPT="${RELEASE_SCRIPT:-$REPO_ROOT/release-macos.sh}"

VERSION=""
RELEASE_DATE=""
MAKE_TAG="no"
MAKE_BUILD="no"

log() {
  printf '\n===== %s =====\n' "$1"
}

fail() {
  printf 'FAIL: %s\n' "$1" >&2
  exit 1
}

usage() {
  cat <<'USAGE'
usage: scripts/release_version.sh vMAJOR.MINOR.PATCH [--date YYYY-MM-DD] [--tag] [--build]

  --date   release date, YYYY-MM-DD; today by default
  --tag    commit CHANGELOG.md and create the annotated tag
  --build  run release-macos.sh with RELEASE_VERSION=<version>
USAGE
}

# require_command refuses a run whose prerequisites are missing, rather
# than failing later with an error about a file nobody wrote.
require_command() {
  command -v "$1" >/dev/null 2>&1 || fail "required command is missing: $1"
}

# validate_version refuses anything SemVer does not allow as a release
# number. A version in the changelog that the tag cannot carry would make
# the two disagree for good, because tags are never rewritten.
validate_version() {
  local version="$1"

  printf '%s' "$version" | grep -Eq \
    '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?$' \
    || fail "version must be vMAJOR.MINOR.PATCH, got: $version"
}

validate_date() {
  local date="$1"

  printf '%s' "$date" | grep -Eq '^[0-9]{4}-[0-9]{2}-[0-9]{2}$' \
    || fail "date must be YYYY-MM-DD, got: $date"
}

parse_args() {
  while test "$#" -gt 0; do
    case "$1" in
      --date)
        test "$#" -ge 2 || fail "--date needs a value"
        RELEASE_DATE="$2"
        shift 2
        ;;
      --tag)
        MAKE_TAG="yes"
        shift
        ;;
      --build)
        MAKE_BUILD="yes"
        shift
        ;;
      -h|--help)
        usage
        exit 0
        ;;
      -*)
        usage >&2
        fail "unknown option: $1"
        ;;
      *)
        test -z "$VERSION" || fail "one version per run, got also: $1"
        VERSION="$1"
        shift
        ;;
    esac
  done

  test -n "$VERSION" || { usage >&2; fail "no version given"; }
  test -n "$RELEASE_DATE" || RELEASE_DATE="$(date '+%Y-%m-%d')"
}

# refuse_wrong_place keeps the release on the default branch and out of a
# tree that already carries other changes: both would put a version number
# next to work that is not part of it.
refuse_wrong_place() {
  local branch

  branch="$(git -C "$REPO_ROOT" branch --show-current)"
  test "$branch" = "$EXPECTED_BRANCH" \
    || fail "a release is cut from $EXPECTED_BRANCH, the current branch is: ${branch:-none}"

  test -z "$(git -C "$REPO_ROOT" status --porcelain)" \
    || fail "the working tree has changes; commit or stash them first"
}

refuse_existing_tag() {
  test -z "$(git -C "$REPO_ROOT" tag -l "$VERSION")" \
    || fail "the tag already exists: $VERSION"
}

# read_changelog loads the file into LINES, one element per line.
#
# A file without a trailing newline still yields its last line, so a
# hand-edited changelog is read the same way as a generated one.
read_changelog() {
  LINES=()
  local line

  while IFS= read -r line || test -n "$line"; do
    LINES+=("$line")
  done < "$CHANGELOG"
}

# find_unreleased returns the index of the "Не выпущено" heading, or fails:
# without it there is nowhere to put the new section.
find_unreleased() {
  local index=0

  while test "$index" -lt "${#LINES[@]}"; do
    if test "${LINES[$index]}" = "$UNRELEASED_HEADING"; then
      printf '%s' "$index"
      return 0
    fi
    index=$((index + 1))
  done

  fail "$CHANGELOG has no \"$UNRELEASED_HEADING\" section"
}

# section_has_entries counts the list items of the section that starts at
# the given index, up to the next heading.
#
# A release of an empty section would claim a version that contains
# nothing, which is exactly the kind of claim this file exists to prevent.
section_has_entries() {
  local index="$1"
  local total=0

  index=$((index + 1))
  while test "$index" -lt "${#LINES[@]}"; do
    case "${LINES[$index]}" in
      '## '*)
        break
        ;;
      '- '*)
        total=$((total + 1))
        ;;
    esac
    index=$((index + 1))
  done

  test "$total" -gt 0 && printf 'yes' || printf 'no'
}

refuse_missing_section() {
  local index="$1"

  test "$(section_has_entries "$index")" = "yes" \
    || fail "\"$UNRELEASED_HEADING\" is empty; there is nothing to release"
}

refuse_duplicate_section() {
  local line

  for line in "${LINES[@]}"; do
    case "$line" in
      "## [$VERSION]"*)
        fail "the changelog already has a section for $VERSION"
        ;;
    esac
  done
}

# write_changelog renames the section in place and leaves a new empty one
# above it.
#
# The rewrite is line based and touches only the heading line and the tail
# of the file: every entry is copied verbatim, so a release cannot drop
# what the previous releases said.
write_changelog() {
  local index="$1"
  local current=0
  local last
  local rewritten

  rewritten="$(mktemp "${TMPDIR:-/tmp}/telecli-changelog.XXXXXX")"

  {
    while test "$current" -lt "$index"; do
      printf '%s\n' "${LINES[$current]}"
      current=$((current + 1))
    done

    printf '%s\n' "$UNRELEASED_HEADING"
    printf '\n'
    printf '%s\n' "$EMPTY_SECTION"
    printf '\n'
    printf '## [%s] - %s\n' "$VERSION" "$RELEASE_DATE"

    current=$((index + 1))
    while test "$current" -lt "${#LINES[@]}"; do
      printf '%s\n' "${LINES[$current]}"
      current=$((current + 1))
    done

    # The link of a version is the last thing in the file, so a release
    # adds its own line next to the previous ones instead of a second
    # block.
    last="$(last_reference_line)"
    if test -z "$last"; then
      printf '\n'
    fi
    printf '[%s]: %s/%s\n' "$VERSION" "$RELEASES_URL" "$VERSION"
  } >"$rewritten"

  mv "$rewritten" "$CHANGELOG"
}

# last_reference_line prints the last link reference of the file, or
# nothing when the file has none yet.
last_reference_line() {
  grep -E '^\[[^]]+\]: ' "$CHANGELOG" | tail -1 || true
}

commit_and_tag() {
  git -C "$REPO_ROOT" add "$CHANGELOG"
  git -C "$REPO_ROOT" commit \
    -m "docs: the unreleased section takes $VERSION and its date" \
    -m "The section above it opens empty for the next release."
  git -C "$REPO_ROOT" tag -a "$VERSION" -m "telecli $VERSION"
}

build_artifact() {
  # env, not a command prefix: a readonly variable of this shell cannot be
  # assigned to, and the build needs its own copy.
  env RELEASE_VERSION="$VERSION" EXPECTED_BRANCH="$EXPECTED_BRANCH" \
    "$RELEASE_SCRIPT"
}

# refuse_build_without_tag runs before the changelog is touched: a build
# without a tag would stamp a version that nothing can be checked against,
# and the file must stay untouched when the run is refused.
#
# --tag cuts the tag moments later, in the same run, so that combination
# needs no tag of its own.
refuse_build_without_tag() {
  if test "$MAKE_BUILD" = "no"; then
    return 0
  fi
  if test "$MAKE_TAG" = "yes"; then
    return 0
  fi

  test -n "$(git -C "$REPO_ROOT" tag -l "$VERSION")" \
    || fail "the tag $VERSION does not exist; cut it before building"
}

print_next_steps() {
  log "next steps"

  if test "$MAKE_TAG" = "yes"; then
    printf 'committed the changelog and tagged %s\n' "$VERSION"
  else
    printf 'git add %s\n' "${CHANGELOG#"$REPO_ROOT/"}"
    printf 'git commit -m "docs: the unreleased section takes %s and its date"\n' "$VERSION"
    printf 'git tag -a %s -m "telecli %s"\n' "$VERSION" "$VERSION"
  fi

  if test "$MAKE_BUILD" = "yes"; then
    printf 'built with RELEASE_VERSION=%s; telecli --version prints it\n' "$VERSION"
  else
    printf 'RELEASE_VERSION=%s EXPECTED_BRANCH=%s ./release-macos.sh\n' \
      "$VERSION" "$EXPECTED_BRANCH"
  fi
}

main() {
  parse_args "$@"

  require_command git
  validate_version "$VERSION"
  validate_date "$RELEASE_DATE"
  test -f "$CHANGELOG" || fail "$CHANGELOG is missing"
  test -f "$RELEASE_SCRIPT" || fail "the release script is missing: $RELEASE_SCRIPT"

  refuse_wrong_place
  refuse_existing_tag

  read_changelog
  local unreleased
  unreleased="$(find_unreleased)"
  refuse_duplicate_section
  refuse_missing_section "$unreleased"
  refuse_build_without_tag

  log "releasing $VERSION"
  printf 'section: %s (line %s)\n' "$UNRELEASED_HEADING" "$((unreleased + 1))"
  printf 'date: %s\n' "$RELEASE_DATE"

  write_changelog "$unreleased"

  if test "$MAKE_TAG" = "yes"; then
    commit_and_tag
  fi
  if test "$MAKE_BUILD" = "yes"; then
    build_artifact
  fi

  print_next_steps
}

main "$@"

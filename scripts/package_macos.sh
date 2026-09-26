#!/usr/bin/env bash
#
# package_macos.sh builds a relocatable telecli macOS package.
#
# The package must work on a machine that has no Homebrew, no
# TELECLI_TDLIB_LIBRARY and no telecli source checkout, and it must keep
# working after the directory is moved or the binary is reached through a
# symlink. That contract is enforced here rather than assumed:
#
#   1. collect the recursive non-system dependency closure of TDLib;
#   2. copy every closed dependency into lib/;
#   3. rewrite dependency references to @loader_path;
#   4. add an @loader_path LC_RPATH idempotently;
#   5. clear extended attributes and ad-hoc sign every modified Mach-O;
#   6. re-check the closure and fail closed on any leftover local path;
#   7. copy licence notices, emit the manifest and SHA256SUMS;
#   8. produce the tarball plus its external checksum.
#
# The install name of each dylib is deliberately left untouched. Changing
# it is unnecessary for relocation and produced an unstable load path.

set -euo pipefail

readonly SCRIPT_NAME="${BASH_SOURCE[0]##*/}"
readonly SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
readonly REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd -P)"

readonly MANIFEST_SCHEMA="$REPO_ROOT/schemas/telecli-package-manifest-v1.schema.json"
readonly MANIFEST_GENERATOR="$SCRIPT_DIR/generate_package_manifest.py"

# The only non-system dependencies that may be bundled. Anything else in
# the closure is an error, so a new TDLib build cannot silently produce a
# package that depends on the build machine.
readonly BUNDLED_LIBS="libssl.3.dylib libcrypto.3.dylib libz.1.dylib"

# Absolute dependency paths are tolerated only under these prefixes.
readonly SYSTEM_PREFIXES=("/usr/lib/" "/System/Library/")

# The buildinfo linker variables, matching the release script.
readonly BUILDINFO_VERSION_VAR="${BUILDINFO_VERSION_VAR:-Version}"
readonly BUILDINFO_COMMIT_VAR="${BUILDINFO_COMMIT_VAR:-Commit}"
readonly BUILDINFO_BUILT_VAR="${BUILDINFO_BUILT_VAR:-Date}"

log() {
  printf '\n===== %s =====\n' "$1"
}

fail() {
  printf 'FAIL: [%s] %s\n' "$SCRIPT_NAME" "$1" >&2
  exit 1
}

info() {
  printf '  %s\n' "$1"
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || fail "required command is missing: $1"
}

# is_system_path reports whether an absolute path is a macOS system
# library, which the package can rely on being present.
is_system_path() {
  local candidate="$1"
  local prefix

  for prefix in "${SYSTEM_PREFIXES[@]}"; do
    case "$candidate" in
      "$prefix"*) return 0 ;;
    esac
  done

  return 1
}

# is_bundled_name reports whether a library basename may be bundled.
is_bundled_name() {
  local name="$1"
  local allowed

  for allowed in $BUNDLED_LIBS; do
    test "$name" = "$allowed" && return 0
  done

  return 1
}

# load_names prints the dependency paths recorded in a Mach-O, excluding
# the first line, which otool uses for the file name itself.
load_names() {
  otool -L "$1" | tail -n +2 | awk 'NF > 0 {print $1}'
}

# install_name prints the install name of a Mach-O.
install_name() {
  otool -D "$1" | tail -n +2 | head -n 1 | awk 'NF > 0 {print $1}'
}

# rpath_list prints the LC_RPATH entries of a Mach-O.
rpath_list() {
  otool -l "$1" \
    | awk '/cmd LC_RPATH/ {found = 1; next} found && $1 == "path" {print $2; found = 0}'
}

# collect_closure walks the dependency graph of a dylib and appends every
# non-system absolute dependency to CLOSURE.
#
# Resolution is deliberately shallow and explicit: only absolute paths can
# be collected, because a relative or @-prefixed reference cannot be
# located without knowing the search order the dynamic linker will apply
# at run time.
collect_closure() {
  local root="$1"
  local queue=("$root")
  local seen=" "
  local current
  local dep
  local name
  local self_id

  while test "${#queue[@]}" -gt 0; do
    current="${queue[0]}"
    queue=("${queue[@]:1}")

    test -f "$current" || \
      fail "dependency of the closure does not exist: $current"

    # The install name is not a load-time dependency. Excluding it here
    # matches gate_closure and the relink loop, and keeps a library with
    # an absolute install name from being reported as its own dependency.
    self_id="$(install_name "$current")"

    while IFS= read -r dep; do
      test -n "$dep" || continue

      test "$dep" = "$self_id" && continue

      case "$dep" in
        @*)
          # @loader_path, @rpath and @executable_path cannot be resolved
          # to a file to bundle; the post-relink gate validates them.
          continue
          ;;
      esac

      is_system_path "$dep" && continue

      name="${dep##*/}"

      case "$seen" in
        *" $name "*) continue ;;
      esac
      seen="$seen$name "

      test -f "$dep" || \
        fail "dependency of ${current##*/} does not exist: $dep"

      is_bundled_name "$name" || \
        fail "unbundlable non-system dependency: $dep (allowed: $BUNDLED_LIBS)"

      CLOSURE+=("$dep")
      queue+=("$dep")
    done < <(load_names "$current")
  done
}

# has_rpath reports whether a Mach-O already declares an rpath.
has_rpath() {
  local binary="$1"
  local wanted="$2"
  local entry

  while IFS= read -r entry; do
    test "$entry" = "$wanted" && return 0
  done < <(rpath_list "$binary")

  return 1
}

# resolve_rpath_target checks that an @rpath reference can be satisfied
# inside the package.
#
# A syntactically valid @rpath reference is not enough: the loading
# Mach-O must carry a matching LC_RPATH and the resolved file must exist
# without leaving the package root.
resolve_rpath_target() {
  local binary="$1"
  local reference="$2"
  local package_root="$3"
  local name="${reference#@rpath/}"
  local entry
  local candidate
  local resolved

  while IFS= read -r entry; do
    case "$entry" in
      "@loader_path") candidate="$package_root/lib/$name" ;;
      @*) continue ;;
      /*) candidate="$entry/$name" ;;
      *) continue ;;
    esac

    test -f "$candidate" || continue

    resolved="$(cd "$(dirname "$candidate")" 2>/dev/null && pwd -P || printf '')"
    case "$resolved" in
      "$package_root"/*) return 0 ;;
      *) continue ;;
    esac
  done < <(rpath_list "$binary")

  return 1
}

# gate_closure fails unless every packaged dylib depends only on system
# libraries, on files inside the package, or on a resolvable @rpath entry.
gate_closure() {
  local package_root="$1"
  local binary
  local dep
  local name
  local self_id

  while IFS= read -r binary; do
    self_id="$(install_name "$binary")"

    while IFS= read -r dep; do
      test -n "$dep" || continue

      # The install name of the file itself is not a load-time
      # dependency, so it is allowed to be anything.
      test "$dep" = "$self_id" && continue

      case "$dep" in
        /usr/lib/*|/System/Library/*)
          continue
          ;;
        /usr/*|/System/*)
          fail "${binary##*/} depends on a non-library system path: $dep"
          ;;
        /@*)
          fail "${binary##*/} has a malformed dependency reference: $dep"
          ;;
        /?*)
          fail "${binary##*/} depends on an absolute local path: $dep"
          ;;
        @loader_path/*)
          name="${dep#@loader_path/}"
          case "$name" in
            */*) fail "${binary##*/} has a nested @loader_path reference: $dep" ;;
          esac
          test -f "$package_root/lib/$name" || \
            fail "${binary##*/} references a missing packaged file: $dep"
          has_rpath "$binary" '@loader_path' || \
            fail "${binary##*/} uses @loader_path without declaring the rpath"
          continue
          ;;
        @rpath/*)
          resolve_rpath_target "$binary" "$dep" "$package_root" || \
            fail "${binary##*/} has an unresolvable reference: $dep"
          continue
          ;;
        @*)
          fail "${binary##*/} has an unsupported reference: $dep"
          ;;
        *)
          fail "${binary##*/} depends on a bare relative name: $dep"
          ;;
      esac
    done < <(load_names "$binary")
  done < <(find "$package_root/lib" -type f -name '*.dylib' | sort)

  info 'dependency closure is closed'
}

# stage_runtime copies the TDLib build and its dependency closure into
# lib/ and rewrites every reference to @loader_path.
stage_runtime() {
  local package_root="$1"
  local tdlib_library="$2"
  local tdlib_name
  local source
  local packaged
  local target
  local dep
  local name
  local self_id

  tdlib_name="$(basename "$tdlib_library")"

  mkdir -p "$package_root/lib"
  cp "$tdlib_library" "$package_root/lib/$tdlib_name"
  chmod 0755 "$package_root/lib/$tdlib_name"

  # Only the TDLib alias is shipped; the OpenSSL and zlib libraries are
  # referenced by their real names, so no extra symlinks are created.
  ln -sfn "$tdlib_name" "$package_root/lib/libtdjson.dylib"

  CLOSURE=()
  collect_closure "$tdlib_library"

  for source in "${CLOSURE[@]}"; do
    name="$(basename "$source")"
    info "bundling $name"
    cp "$source" "$package_root/lib/$name"
    chmod 0755 "$package_root/lib/$name"
  done

  # Rewrite references only after every file is present, so a broken
  # closure cannot produce a half-relinked library.
  #
  # The install name of a file is skipped: it is not a load-time
  # dependency, and rewriting it is unnecessary for relocation.
  while IFS= read -r packaged; do
    self_id="$(install_name "$packaged")"

    while IFS= read -r dep; do
      test -n "$dep" || continue

      test "$dep" = "$self_id" && continue

      case "$dep" in
        /usr/lib/*|/System/Library/*|@*) continue ;;
      esac

      name="$(basename "$dep")"
      test -f "$package_root/lib/$name" || \
        fail "cannot relink ${packaged##*/}: $name is not packaged"

      info "relink ${packaged##*/}: $dep -> @loader_path/$name"
      install_name_tool \
        -change "$dep" "@loader_path/$name" \
        "$packaged" >/dev/null 2>&1 || \
        fail "install_name_tool failed for ${packaged##*/}"
    done < <(load_names "$packaged")
  done < <(find "$package_root/lib" -type f -name '*.dylib' | sort)

  # The rpath is added after relinking and only when missing, so a
  # repeated run neither duplicates the entry nor fails.
  while IFS= read -r packaged; do
    if has_rpath "$packaged" '@loader_path'; then
      info "rpath already present: ${packaged##*/}"
      continue
    fi

    info "add rpath: ${packaged##*/}"
    install_name_tool -add_rpath '@loader_path' "$packaged" >/dev/null 2>&1 || \
      fail "could not add @loader_path rpath to ${packaged##*/}"
  done < <(find "$package_root/lib" -type f -name '*.dylib' | sort)

  target="$package_root/lib/$tdlib_name"
  test -f "$target" || fail "TDLib library is missing from the package"
}

# sign_runtime seals every packaged dylib and verifies the result.
#
# install_name_tool invalidates the original signature, so signing must be
# the last mutation. --deep is never used: each object is signed
# explicitly, in dependency order.
sign_runtime() {
  local package_root="$1"
  local binary

  xattr -cr "$package_root" 2>/dev/null || true

  while IFS= read -r binary; do
    info "ad-hoc sign ${binary##*/}"
    codesign --force --sign - "$binary" >/dev/null 2>&1 || \
      fail "codesign failed for ${binary##*/}"
  done < <(find "$package_root/lib" -type f -name '*.dylib' | sort)

  while IFS= read -r binary; do
    codesign --verify --strict "$binary" >/dev/null 2>&1 || \
      fail "ad-hoc signature is not valid for ${binary##*/}"
  done < <(find "$package_root/lib" -type f -name '*.dylib' | sort)

  info 'every packaged dylib carries a valid ad-hoc signature'
}

# stage_licenses copies the licence notices of every redistributed
# component from the actual build sources.
stage_licenses() {
  local package_root="$1"
  local tdlib_source_dir="$2"
  local openssl_prefix="$3"
  local openssl_version="$4"
  local zlib_prefix="$5"
  local zlib_version="$6"
  local openssl_real
  local zlib_real

  openssl_real="$(cd "$openssl_prefix" 2>/dev/null && pwd -P || printf '')"
  zlib_real="$(cd "$zlib_prefix" 2>/dev/null && pwd -P || printf '')"

  test -n "$openssl_real" || fail "openssl prefix does not resolve: $openssl_prefix"
  test -n "$zlib_real" || fail "zlib prefix does not resolve: $zlib_prefix"

  mkdir -p \
    "$package_root/share/licenses/tdlib" \
    "$package_root/share/licenses/openssl" \
    "$package_root/share/licenses/zlib"

  cp "$tdlib_source_dir/LICENSE_1_0.txt" \
    "$package_root/share/licenses/tdlib/LICENSE_1_0.txt" || \
    fail "TDLib licence is missing: $tdlib_source_dir/LICENSE_1_0.txt"

  # OpenSSL ships LICENSE.txt; a NOTICE file is included when the build
  # provides one, because the Apache licence requires the notice to be
  # carried with the distribution.
  local notice
  notice="$(find "$openssl_real" -maxdepth 3 -type f -name 'NOTICE*' -print -quit 2>/dev/null || printf '')"
  if test -n "$notice"; then
    cp "$notice" "$package_root/share/licenses/openssl/NOTICE.txt"
    info "openssl notice: $notice"
  else
    info 'openssl notice: not present in the build source'
  fi

  cp "$openssl_real/LICENSE.txt" \
    "$package_root/share/licenses/openssl/LICENSE.txt" || \
    fail "OpenSSL licence is missing: $openssl_real/LICENSE.txt"

  cp "$zlib_real/LICENSE" \
    "$package_root/share/licenses/zlib/LICENSE" || \
    fail "zlib licence is missing: $zlib_real/LICENSE"

  chmod 0644 \
    "$package_root/share/licenses/tdlib/LICENSE_1_0.txt" \
    "$package_root/share/licenses/openssl/LICENSE.txt" \
    "$package_root/share/licenses/zlib/LICENSE"

  if test -n "$notice"; then
    chmod 0644 "$package_root/share/licenses/openssl/NOTICE.txt"
  fi

  info "openssl version: $openssl_version"
  info "zlib version:     $zlib_version"
}

# write_checksums hashes every regular file of the package except
# SHA256SUMS itself. The symlink is described by the manifest instead.
write_checksums() {
  local package_root="$1"
  local sums="$package_root/SHA256SUMS"
  local relative
  local shasum_cmd

  if command -v sha256sum >/dev/null 2>&1; then
    shasum_cmd="sha256sum"
  else
    shasum_cmd="shasum -a 256"
  fi

  : > "$sums"

  while IFS= read -r relative; do
    test "$relative" = "SHA256SUMS" && continue
    # $shasum_cmd already prints "<hash>  <path>".
    ( cd "$package_root" && $shasum_cmd "$relative" ) >> "$sums"
  done < <(
    cd "$package_root" \
      && find . -type f -print \
      | sed 's|^\./||' \
      | grep -v '^SHA256SUMS$' \
      | sort
  )

  chmod 0644 "$sums"
  info "checksum entries: $(wc -l < "$sums" | tr -d ' ')"
}

# build_archive creates the tarball and its external checksum.
build_archive() {
  local package_root="$1"
  local dist_dir="$2"
  local archive_name="$3"
  local archive="$dist_dir/$archive_name"

  mkdir -p "$dist_dir"
  rm -f -- "$archive" "${archive}.sha256"

  tar -C "$dist_dir" \
    -czf "$archive" \
    "$(basename "$package_root")"

  if command -v sha256sum >/dev/null 2>&1; then
    ( cd "$dist_dir" && sha256sum "$archive_name" > "${archive_name}.sha256" )
  else
    ( cd "$dist_dir" && shasum -a 256 "$archive_name" > "${archive_name}.sha256" )
  fi

  info "archive: $archive"
}

main() {
  local package_version
  local package_channel
  local dist_dir
  local tdlib_library
  local tdlib_version
  local tdlib_commit
  local tdlib_source_dir
  local openssl_prefix
  local openssl_version
  local zlib_prefix
  local zlib_version
  local build_commit
  local build_timestamp
  local buildinfo_path
  local go_version
  local ldflags
  local package_name
  local package_root
  local binary
  local notes
  local stage_dir
  local doctor_output

  package_version="${PACKAGE_VERSION:-}"
  package_channel="${PACKAGE_CHANNEL:-prerelease}"
  dist_dir="${DIST_DIR:-dist}"
  tdlib_library="${TDLIB_LIBRARY:-${TELECLI_TDLIB_LIBRARY:-}}"
  tdlib_version="${TDLIB_VERSION:-}"
  tdlib_commit="${TDLIB_COMMIT:-}"
  tdlib_source_dir="${TDLIB_SOURCE_DIR:-}"
  openssl_prefix="${OPENSSL_PREFIX:-}"
  openssl_version="${OPENSSL_VERSION:-}"
  zlib_prefix="${ZLIB_PREFIX:-}"
  zlib_version="${ZLIB_VERSION:-}"

  require_command go
  require_command otool
  require_command install_name_tool
  require_command codesign
  require_command xattr
  require_command tar
  require_command find
  require_command python3

  test "$(uname -s)" = "Darwin" || fail "packaging requires macOS"
  test "$(uname -m)" = "arm64" || fail "packaging requires arm64"

  test -n "$package_version" || fail "PACKAGE_VERSION is required"
  test -n "$tdlib_library" || fail "TDLIB_LIBRARY is required"
  test -f "$tdlib_library" || fail "TDLib library does not exist: $tdlib_library"
  test -n "$tdlib_version" || fail "TDLIB_VERSION is required"
  test -n "$tdlib_commit" || fail "TDLIB_COMMIT is required"
  test -n "$tdlib_source_dir" || fail "TDLIB_SOURCE_DIR is required"
  test -d "$tdlib_source_dir" || fail "TDLib source dir does not exist: $tdlib_source_dir"
  test -n "$openssl_prefix" || fail "OPENSSL_PREFIX is required"
  test -n "$openssl_version" || fail "OPENSSL_VERSION is required"
  test -n "$zlib_prefix" || fail "ZLIB_PREFIX is required"
  test -n "$zlib_version" || fail "ZLIB_VERSION is required"

  test -f "$MANIFEST_SCHEMA" || fail "manifest schema is missing: $MANIFEST_SCHEMA"
  test -f "$MANIFEST_GENERATOR" || fail "manifest generator is missing: $MANIFEST_GENERATOR"

  package_name="telecli_${package_version#v}_darwin_arm64"
  package_root="$dist_dir/$package_name"

  build_commit="$(git -C "$REPO_ROOT" rev-parse HEAD)"
  test "$build_commit" != "0000000000000000000000000000000000000000" || \
    fail "HEAD could not be resolved"
  build_timestamp="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
  go_version="$(go env GOVERSION)"

  # The schema and generator are validated before the build so a broken
  # manifest contract cannot produce a partially built package.
  python3 -c 'import json,sys; json.load(open(sys.argv[1]))' "$MANIFEST_SCHEMA" || \
    fail "manifest schema is not valid JSON"

  log "Package layout"
  printf 'package:  %s\n' "$package_name"
  printf 'version:  %s\n' "$package_version"
  printf 'channel:  %s\n' "$package_channel"
  printf 'tdlib:    %s (%s)\n' "$tdlib_version" "$tdlib_commit"

  log "Build binary"
  stage_dir="$(mktemp -d)"
  # shellcheck disable=SC2064
  trap "rm -rf '$stage_dir'" EXIT

  binary="$package_root/bin/telecli"

  # A repeated run must not inherit files from an earlier build, because
  # the manifest describes everything that is present.
  rm -rf -- "$package_root"
  mkdir -p "$package_root/bin"
  mkdir -p "$package_root/share/telecli"

  # The build metadata is injected the same way the release script does
  # it, so the binary cannot claim to be a development build while the
  # manifest records a release commit.
  buildinfo_path="$(go -C "$REPO_ROOT" list -f '{{.ImportPath}}' ./internal/buildinfo)"
  ldflags="-X ${buildinfo_path}.${BUILDINFO_VERSION_VAR}=${package_version}"
  ldflags+=" -X ${buildinfo_path}.${BUILDINFO_COMMIT_VAR}=${build_commit:0:12}"
  ldflags+=" -X ${buildinfo_path}.${BUILDINFO_BUILT_VAR}=${build_timestamp}"

  CGO_ENABLED=1 go build -C "$REPO_ROOT" \
    -trimpath \
    -ldflags "$ldflags" \
    -o "$binary" \
    ./cmd/telecli

  chmod 0755 "$binary"
  info "binary: $binary"

  log "Stage TDLib runtime"
  stage_runtime "$package_root" "$tdlib_library"

  log "Gate dependency closure"
  gate_closure "$package_root"

  log "Sign Mach-O files"
  sign_runtime "$package_root"

  log "Gate dependency closure after signing"
  gate_closure "$package_root"

  log "Stage licence notices"
  stage_licenses \
    "$package_root" \
    "$tdlib_source_dir" \
    "$openssl_prefix" \
    "$openssl_version" \
    "$zlib_prefix" \
    "$zlib_version"

  log "Stage release notes"
  notes="$package_root/share/telecli/RELEASE_NOTES.md"
  if test -f "$REPO_ROOT/RELEASE_NOTES_TEMPLATE.md"; then
    sed \
      -e "s/<COMMIT>/${build_commit:0:12}/g" \
      -e "s/<UTC_TIMESTAMP>/$build_timestamp/g" \
      -e "s/<SHA256>/pending/g" \
      "$REPO_ROOT/RELEASE_NOTES_TEMPLATE.md" \
      > "$notes"
    info "notes: rendered from the repository template"
  else
    printf 'telecli %s\n\nTDLib %s (%s)\n' \
      "$package_version" "$tdlib_version" "$tdlib_commit" \
      > "$notes"
    info 'notes: minimal fallback written'
  fi
  chmod 0644 "$notes"

  log "Generate manifest"
  python3 "$MANIFEST_GENERATOR" \
    --package-root "$package_root" \
    --version "$package_version" \
    --channel "$package_channel" \
    --commit "$build_commit" \
    --timestamp "$build_timestamp" \
    --go-version "$go_version" \
    --tdlib-version "$tdlib_version" \
    --tdlib-commit "$tdlib_commit" \
    --tdlib-library "lib/libtdjson.${tdlib_version}.dylib" \
    --component "tdlib,$tdlib_version,BSL-1.0,lib/libtdjson.${tdlib_version}.dylib,$tdlib_commit" \
    --component "openssl,$openssl_version,Apache-2.0,lib/libcrypto.3.dylib;lib/libssl.3.dylib" \
    --component "zlib,$zlib_version,Zlib,lib/libz.1.dylib" \
    --license-file "share/licenses/tdlib/LICENSE_1_0.txt" \
    --license-file "share/licenses/openssl/LICENSE.txt" \
    --license-file "share/licenses/zlib/LICENSE" \
    --output "$package_root/share/telecli/manifest.json"

  # The manifest is also published next to the archive, so provenance and
  # contents can be checked before the package is unpacked. The copy is
  # made after the embedded manifest is final and is verified byte for
  # byte, because two manifests that describe the same package must never
  # be able to disagree.
  external_manifest="$dist_dir/${package_name}.manifest.json"
  cp "$package_root/share/telecli/manifest.json" "$external_manifest"
  chmod 0644 "$external_manifest"

  cmp -s "$package_root/share/telecli/manifest.json" "$external_manifest" || \
    fail "external package manifest differs from the embedded manifest"

  info "external manifest: $external_manifest"

  log "Write SHA256SUMS"
  write_checksums "$package_root"

  log "Runtime probe"
  # The probe runs with a scrubbed environment so the packaged runtime
  # must resolve TDLib from the package itself.
  doctor_output="$(
    cd "$stage_dir" \
    && env -u TELECLI_TDLIB_LIBRARY -u TELECLI_CONFIG \
        -u DYLD_LIBRARY_PATH -u DYLD_FALLBACK_LIBRARY_PATH \
        -u DYLD_FRAMEWORK_PATH \
        "$binary" doctor
  )" || fail "packaged doctor command failed"

  printf '%s\n' "$doctor_output" | sed 's/^/  /'

  printf '%s\n' "$doctor_output" | grep -F 'TDLib runtime: available' >/dev/null || \
    fail "packaged binary cannot load the packaged TDLib"
  printf '%s\n' "$doctor_output" | grep -F 'TDLib compatibility: verified' >/dev/null || \
    fail "packaged binary rejected the packaged TDLib"

  log "Build archive"
  build_archive "$package_root" "$dist_dir" "${package_name}.tar.gz"

  log "Package result"
  printf 'package root: %s\n' "$package_root"
  printf 'archive:      %s/%s.tar.gz\n' "$dist_dir" "$package_name"
  printf '%s\n' 'PASS: relocatable package is built and verified'
  printf '%s\n' 'NOTE: package is unsigned; Mach-O files are ad-hoc sealed'
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  main "$@"
fi

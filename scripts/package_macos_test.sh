#!/usr/bin/env bash
#
# Tests for the relocatable macOS package.
#
# The package contract is that it works on a machine with no Homebrew, no
# TELECLI_TDLIB_LIBRARY and no telecli checkout, and that it keeps working
# after the directory is moved or the binary is reached through a symlink.
# These tests build one real package and then attack that contract from
# every angle, including deliberate tampering.
#
# Required environment:
#
#   TELECLI_TDLIB_LIBRARY  absolute path to a loadable libtdjson
#
# Everything is built under a temporary directory. The real dist/ and the
# real repository are never touched.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd -P)"
PACKAGER="$SCRIPT_DIR/package_macos.sh"
VERIFIER="$SCRIPT_DIR/verify_package.sh"

passed=0
failed=0
skipped=0
current=""

WORK=""
PACKAGE_ROOT=""
PACKAGE_NAME=""

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

skip_all() {
  skipped=$((skipped + 1))
  printf '\nSKIP: %s\n' "$1"
  printf 'passed: %d\nfailed: %d\nskipped: %d\n' "$passed" "$failed" "$skipped"
  exit 0
}

check() {
  if test "$2" = "yes"; then
    pass "$1"
  else
    fail "$1"
  fi
}

# rejects runs a command in a subshell and requires it to fail.
rejects() {
  local label="$1"
  shift

  if ( "$@" ) >/dev/null 2>&1; then
    check "$label" "no"
  else
    check "$label" "yes"
  fi
}

# accepts runs a command and requires it to succeed.
accepts() {
  local label="$1"
  shift

  if ( "$@" ) >/dev/null 2>&1; then
    check "$label" "yes"
  else
    check "$label" "no"
  fi
}

# scrubbed runs a command with every telecli and dyld override removed, so
# the package has to resolve TDLib on its own.
scrubbed() {
  env -u TELECLI_TDLIB_LIBRARY \
      -u TELECLI_CONFIG \
      -u DYLD_LIBRARY_PATH \
      -u DYLD_FALLBACK_LIBRARY_PATH \
      -u DYLD_FRAMEWORK_PATH \
      "$@"
}

# doctor_output prints the TDLib lines of a doctor run.
doctor_lines() {
  local binary="$1"
  local workdir="$2"

  ( cd "$workdir" && scrubbed "$binary" doctor 2>&1 ) \
    | grep -E '^TDLib ' || true
}

# has_line reports whether stdin contains an exact line.
has_line() {
  grep -qxF "$1"
}

# build_package produces one real package in a temporary dist directory.
build_package() {
  local tdlib_source
  local openssl_prefix
  local zlib_prefix

  WORK="$(mktemp -d "${TMPDIR:-/tmp}/telecli-package-test.XXXXXX")"
  PACKAGE_NAME="telecli_0.1.0-test_darwin_arm64"
  PACKAGE_ROOT="$WORK/dist/$PACKAGE_NAME"

  # The TDLib checkout is the parent of the build directory that holds
  # the library.
  tdlib_source="$(cd "$(dirname "$TELECLI_TDLIB_LIBRARY")/.." && pwd -P)"

  openssl_prefix="${OPENSSL_PREFIX:-$(brew --prefix openssl@3)}"
  zlib_prefix="${ZLIB_PREFIX:-$(brew --prefix zlib)}"

  DIST_DIR="$WORK/dist" \
  PACKAGE_VERSION="v0.1.0-test" \
  TDLIB_LIBRARY="$TELECLI_TDLIB_LIBRARY" \
  TDLIB_VERSION="1.8.67" \
  TDLIB_COMMIT="ea97bcdd3a15523c58ddfe772b4547187cf5bbeb" \
  TDLIB_SOURCE_DIR="$tdlib_source" \
  OPENSSL_PREFIX="$openssl_prefix" \
  OPENSSL_VERSION="${OPENSSL_VERSION:-$(brew list --versions openssl@3 | awk '{print $2}')}" \
  ZLIB_PREFIX="$zlib_prefix" \
  ZLIB_VERSION="${ZLIB_VERSION:-$(brew list --versions zlib | awk '{print $2}')}" \
    "$PACKAGER" > "$WORK/build.log" 2>&1
}

drop_package() {
  test -n "${WORK:-}" && rm -rf "$WORK"
  WORK=""
}

# ---- preconditions ------------------------------------------------------

if test "$(uname -s)" != "Darwin"; then
  skip_all "packaging tests require macOS"
fi

if test -z "${TELECLI_TDLIB_LIBRARY:-}"; then
  skip_all "TELECLI_TDLIB_LIBRARY is not set"
fi

if test ! -f "${TELECLI_TDLIB_LIBRARY}"; then
  skip_all "TELECLI_TDLIB_LIBRARY does not exist: $TELECLI_TDLIB_LIBRARY"
fi

for tool in brew otool codesign python3 tar; do
  command -v "$tool" >/dev/null 2>&1 || skip_all "required tool is missing: $tool"
done

start "the packager and the verifier parse"
if bash -n "$PACKAGER" 2>/dev/null && bash -n "$VERIFIER" 2>/dev/null; then
  pass "bash -n on both scripts"
else
  fail "bash -n on both scripts"
fi

start "a real package is built"
if build_package; then
  pass "package built at $PACKAGE_ROOT"
else
  tail -20 "$WORK/build.log"
  fail "package build failed"
  drop_package
  printf '\npassed: %d\nfailed: %d\nskipped: %d\n' "$passed" "$failed" "$skipped"
  exit 1
fi

trap 'drop_package' EXIT

# ---- runtime behaviour --------------------------------------------------

start "TestPackageLoadsBundledTDLib"
lines="$(doctor_lines "$PACKAGE_ROOT/bin/telecli" "$WORK")"
printf '%s\n' "$lines" | has_line "TDLib runtime: available" \
  && pass "TDLib runtime: available" || fail "TDLib runtime: available"
printf '%s\n' "$lines" | has_line "TDLib source: packaged" \
  && pass "TDLib source: packaged" || fail "TDLib source: packaged"
printf '%s\n' "$lines" | has_line "TDLib version: 1.8.67" \
  && pass "TDLib version: 1.8.67" || fail "TDLib version: 1.8.67"
printf '%s\n' "$lines" | has_line "TDLib compatibility: verified" \
  && pass "TDLib compatibility: verified" || fail "TDLib compatibility: verified"

start "TestPackageRunsWithoutTDLibEnvironment"
# The DoD: with the build-time variable removed, the package resolves its
# own bundled library.
lines="$(doctor_lines "$PACKAGE_ROOT/bin/telecli" "$WORK")"
printf '%s\n' "$lines" | has_line "TDLib source: packaged" \
  && pass "the packaged library is used when TELECLI_TDLIB_LIBRARY is unset" \
  || fail "the packaged library is used when TELECLI_TDLIB_LIBRARY is unset"

# An exported value is an explicit operator choice and deliberately wins,
# so the precedence is pinned by a test rather than left implicit.
out="$WORK/with-env.log"
env TELECLI_TDLIB_LIBRARY="$TELECLI_TDLIB_LIBRARY" \
    "$PACKAGE_ROOT/bin/telecli" doctor > "$out" 2>&1
if grep -qxF "TDLib source: environment" "$out"; then
  pass "an exported TELECLI_TDLIB_LIBRARY still takes precedence"
else
  fail "an exported TELECLI_TDLIB_LIBRARY still takes precedence"
  sed 's/^/    /' "$out"
fi

start "TestPackageRunsWithoutConfigOverride"
# A configuration that names no library must leave the packaged one in
# place. An absent file is a different case: it is a configuration error.
# The build-time variable is scrubbed so the configuration is what is
# actually under test.
config_file="$WORK/telecli.toml"
printf 'log_level = "info"\n' > "$config_file"
out="$WORK/with-config.log"
env -u TELECLI_TDLIB_LIBRARY TELECLI_CONFIG="$config_file" \
    "$PACKAGE_ROOT/bin/telecli" doctor > "$out" 2>&1
if grep -qxF "TDLib source: packaged" "$out"; then
  pass "a configuration without tdlib.library_path keeps the packaged library"
else
  fail "a configuration without tdlib.library_path keeps the packaged library"
  sed 's/^/    /' "$out"
fi

# A configuration that does name a library is an explicit choice too.
config_file="$WORK/explicit.toml"
printf 'log_level = "info"\n\n[tdlib]\nlibrary_path = "%s"\n' \
  "$TELECLI_TDLIB_LIBRARY" > "$config_file"
out="$WORK/with-explicit-config.log"
env -u TELECLI_TDLIB_LIBRARY TELECLI_CONFIG="$config_file" \
    "$PACKAGE_ROOT/bin/telecli" doctor > "$out" 2>&1
if grep -qxF "TDLib source: configured" "$out"; then
  pass "a configured tdlib.library_path still takes precedence"
else
  fail "a configured tdlib.library_path still takes precedence"
  sed 's/^/    /' "$out"
fi

start "TestPackageRunsAfterMove"
moved="$WORK/moved/deeper"
mkdir -p "$(dirname "$moved")"
cp -R "$PACKAGE_ROOT" "$moved"
lines="$(doctor_lines "$moved/bin/telecli" "$WORK")"
printf '%s\n' "$lines" | has_line "TDLib source: packaged" \
  && pass "TDLib still resolves after the package is moved" \
  || fail "TDLib still resolves after the package is moved"
printf '%s\n' "$lines" | has_line "TDLib compatibility: verified" \
  && pass "compatibility is still verified after the move" \
  || fail "compatibility is still verified after the move"

start "TestPackageRunsThroughExecutableSymlink"
ln -sfn "$moved/bin/telecli" "$WORK/telecli-link"
lines="$(doctor_lines "$WORK/telecli-link" "$WORK")"
printf '%s\n' "$lines" | has_line "TDLib source: packaged" \
  && pass "TDLib resolves through a symlinked executable" \
  || fail "TDLib resolves through a symlinked executable"

start "TestPackageRunsFromUnrelatedWorkingDirectory"
for dir in / /tmp "$HOME"; do
  test -d "$dir" || continue
  lines="$(doctor_lines "$moved/bin/telecli" "$dir")"
  if printf '%s\n' "$lines" | has_line "TDLib source: packaged"; then
    pass "TDLib resolves from $dir"
  else
    fail "TDLib resolves from $dir"
  fi
done

# ---- dependency graph ---------------------------------------------------

start "TestPackageHasClosedDynamicDependencyGraph"
closed=yes
for dylib in "$PACKAGE_ROOT"/lib/*.dylib; do
  test -L "$dylib" && continue
  while IFS= read -r dep; do
    test -n "$dep" || continue
    case "$dep" in
      /usr/lib/*|/System/Library/*) continue ;;
      @loader_path/*)
        name="${dep#@loader_path/}"
        test -f "$PACKAGE_ROOT/lib/$name" || {
          closed=no
          printf '    %s -> missing %s\n' "${dylib##*/}" "$name"
        }
        continue
        ;;
      @rpath/*)
        name="${dep#@rpath/}"
        test -f "$PACKAGE_ROOT/lib/$name" || {
          closed=no
          printf '    %s -> unresolved %s\n' "${dylib##*/}" "$name"
        }
        continue
        ;;
      /*)
        # The install name of the file itself is not a load-time
        # dependency and is allowed to stay as built.
        continue
        ;;
      *)
        closed=no
        printf '    %s -> unexpected %s\n' "${dylib##*/}" "$dep"
        ;;
    esac
  done < <(otool -L "$dylib" | tail -n +2 | awk 'NF > 0 {print $1}')
done
check "every load-time dependency stays inside the package" "$closed"

start "TestPackageContainsNoHomebrewPaths"
found=no
for dylib in "$PACKAGE_ROOT"/lib/*.dylib; do
  test -L "$dylib" && continue
  self="$(otool -D "$dylib" | tail -n +2 | head -1 | awk '{print $1}')"
  while IFS= read -r dep; do
    test "$dep" = "$self" && continue
    case "$dep" in
      /opt/homebrew/*|/usr/local/*) found=yes; printf '    %s -> %s\n' "${dylib##*/}" "$dep" ;;
    esac
  done < <(otool -L "$dylib" | tail -n +2 | awk 'NF > 0 {print $1}')
done
check "no load-time dependency points at Homebrew" \
  "$(test "$found" = "no" && echo yes || echo no)"

start "TestPackageContainsNoUserPaths"
found=no
for dylib in "$PACKAGE_ROOT"/lib/*.dylib; do
  test -L "$dylib" && continue
  while IFS= read -r dep; do
    case "$dep" in
      /Users/*|/home/*) found=yes; printf '    %s -> %s\n' "${dylib##*/}" "$dep" ;;
    esac
  done < <(otool -L "$dylib" | tail -n +2 | awk 'NF > 0 {print $1}')
done
check "no dependency points at a user directory" \
  "$(test "$found" = "no" && echo yes || echo no)"

# ---- signatures ---------------------------------------------------------

start "TestEveryModifiedDylibHasValidAdHocSignature"
signed=yes
count=0
for dylib in "$PACKAGE_ROOT"/lib/*.dylib; do
  test -L "$dylib" && continue
  count=$((count + 1))
  if ! codesign --verify --strict "$dylib" >/dev/null 2>&1; then
    signed=no
    printf '    %s has no valid signature\n' "${dylib##*/}"
    continue
  fi
  details="$(codesign --display --verbose=1 "$dylib" 2>&1)"
  printf '%s\n' "$details" | grep -q 'Signature=adhoc' || {
    signed=no
    printf '    %s is not ad-hoc signed\n' "${dylib##*/}"
  }
done
check "all $count packaged dylibs are validly ad-hoc signed" "$signed"

# ---- manifest and checksums --------------------------------------------

manifest_value() {
  python3 -c '
import json, sys
data = json.load(open(sys.argv[1]))
value = data
for key in sys.argv[2].split("."):
    value = value[int(key)] if isinstance(value, list) else value[key]
print(value)
' "$PACKAGE_ROOT/share/telecli/manifest.json" "$1"
}

start "TestManifestDeclaresEveryBundledLibrary"
undeclared=""
for dylib in "$PACKAGE_ROOT"/lib/*.dylib; do
  test -L "$dylib" && continue
  relative="lib/${dylib##*/}"
  if ! python3 -c '
import json, sys
data = json.load(open(sys.argv[1]))
declared = {
    library
    for component in data["components"]
    for library in component["libraries"]
}
sys.exit(0 if sys.argv[2] in declared else 1)
' "$PACKAGE_ROOT/share/telecli/manifest.json" "$relative"; then
    undeclared="$undeclared $relative"
  fi
done
if test -z "$undeclared"; then
  pass "every packaged dylib is declared by a component"
else
  fail "undeclared libraries:$undeclared"
fi

start "TestManifestDeclaresAllLicenseFiles"
licence_state=yes
for required in \
  "share/licenses/tdlib/LICENSE_1_0.txt" \
  "share/licenses/openssl/LICENSE.txt" \
  "share/licenses/zlib/LICENSE"
do
  test -f "$PACKAGE_ROOT/$required" || {
    licence_state=no
    printf '    missing file %s\n' "$required"
    continue
  }
  python3 -c '
import json, sys
data = json.load(open(sys.argv[1]))
declared = {
    path
    for component in data["components"]
    for path in component["license_files"]
}
sys.exit(0 if sys.argv[2] in declared else 1)
' "$PACKAGE_ROOT/share/telecli/manifest.json" "$required" || {
    licence_state=no
    printf '    not declared in the manifest: %s\n' "$required"
  }
done
check "every licence file is shipped and declared" "$licence_state"

start "TestManifestRecordsHonestSigningState"
signing=yes
test "$(manifest_value signing.package_status)" = "unsigned" || signing=no
test "$(manifest_value signing.mach_o_status)" = "adhoc" || signing=no
test "$(manifest_value signing.notarized)" = "False" || signing=no
test "$(manifest_value signing.hardened_runtime)" = "False" || signing=no
test "$(manifest_value tdlib.source)" = "packaged" || signing=no
check "the manifest records unsigned, ad-hoc, not notarized" "$signing"

start "TestSHA256SUMSCoversEveryRegularPayloadFile"
coverage=yes
listed="$(awk '{print $2}' "$PACKAGE_ROOT/SHA256SUMS" | sort)"
regular="$(
  cd "$PACKAGE_ROOT" \
    && find . -type f \
    | sed 's|^\./||' \
    | grep -v '^SHA256SUMS$' \
    | sort
)"
test "$listed" = "$regular" || {
  coverage=no
  printf '    listed and present differ:\n'
  printf '%s\n' "$listed" "$regular" | sort -u | sed 's/^/      /'
}
# The symlink is described by the manifest, not by SHA256SUMS.
printf '%s\n' "$listed" | grep -qxF 'lib/libtdjson.dylib' && {
  coverage=no
  printf '    the symlink must not be in SHA256SUMS\n'
}
# Every recorded hash must still match.
while read -r _hash path; do
  actual="$(cd "$PACKAGE_ROOT" && shasum -a 256 "$path" | awk '{print $1}')"
  test "$actual" = "$_hash" || {
    coverage=no
    printf '    checksum mismatch for %s\n' "$path"
  }
done < "$PACKAGE_ROOT/SHA256SUMS"
check "SHA256SUMS covers every regular file and every hash matches" "$coverage"

start "TestPackageHasNoUndeclaredFiles"
extra=no
while IFS= read -r relative; do
  case "$relative" in
    SHA256SUMS|share/telecli/manifest.json) continue ;;
  esac
  if ! python3 -c '
import json, sys
data = json.load(open(sys.argv[1]))
listed = {entry["path"] for entry in data["files"]}
sys.exit(0 if sys.argv[2] in listed else 1)
' "$PACKAGE_ROOT/share/telecli/manifest.json" "$relative"; then
    extra=yes
    printf '    undeclared: %s\n' "$relative"
  fi
done < <(
  cd "$PACKAGE_ROOT" \
    && find . \( -type f -o -type l \) \
    | sed 's|^\./||' \
    | sort
)
check "every packaged entry is declared in the manifest" \
  "$(test "$extra" = "no" && echo yes || echo no)"

# ---- negative tests -----------------------------------------------------

start "the verifier accepts the untampered package"
accepts "verify_package.sh accepts the real package" \
  "$VERIFIER" "$PACKAGE_ROOT"

start "TestTamperedPayloadIsRejected"
tampered="$WORK/tampered"
cp -R "$PACKAGE_ROOT" "$tampered"
printf 'x' >> "$tampered/lib/libz.1.dylib"
rejects "a modified library fails verification" \
  "$VERIFIER" "$tampered"

start "TestUndeclaredExtraFileIsRejected"
extra_dir="$WORK/extra"
cp -R "$PACKAGE_ROOT" "$extra_dir"
printf 'stray\n' > "$extra_dir/lib/stray.txt"
rejects "an undeclared file fails verification" \
  "$VERIFIER" "$extra_dir"

start "TestBrokenSignatureIsRejected"
unsigned_dir="$WORK/unsigned"
cp -R "$PACKAGE_ROOT" "$unsigned_dir"
codesign --remove-signature "$unsigned_dir/lib/libz.1.dylib" >/dev/null 2>&1
rejects "a dylib without a valid signature fails verification" \
  "$VERIFIER" "$unsigned_dir"

start "TestMissingLicenceIsRejected"
no_licence="$WORK/no-licence"
cp -R "$PACKAGE_ROOT" "$no_licence"
rm -f "$no_licence/share/licenses/zlib/LICENSE"
rejects "a missing licence fails verification" \
  "$VERIFIER" "$no_licence"

start "TestDanglingSymlinkIsRejected"
dangling="$WORK/dangling"
cp -R "$PACKAGE_ROOT" "$dangling"
rm -f "$dangling/lib/libtdjson.1.8.67.dylib"
rejects "a dangling TDLib alias fails verification" \
  "$VERIFIER" "$dangling"

start "TestPackagerRefusesAnUnbundlableDependency"
probe="$WORK/probe"
mkdir -p "$probe"
cat > "$probe/fake.c" <<'C'
int telecli_gate_probe(void) { return 1; }
C
cat > "$probe/user.c" <<'C'
int telecli_gate_probe(void);
int telecli_user_entry(void) { return telecli_gate_probe(); }
C
if command -v clang >/dev/null 2>&1; then
  clang -dynamiclib -install_name "$probe/libgateprobe.dylib" \
    "$probe/fake.c" -o "$probe/libgateprobe.dylib" 2>/dev/null
  clang -dynamiclib -install_name "$probe/libuser.dylib" \
    "$probe/user.c" "$probe/libgateprobe.dylib" -o "$probe/libuser.dylib" 2>/dev/null

  if test -f "$probe/libuser.dylib"; then
    rejected=no
    message="$(
      # The closure helper reports through stderr and exits non-zero, so
      # the redirection has to sit on the command itself.
      # shellcheck source=./package_macos.sh
      source "$PACKAGER" >/dev/null 2>&1
      CLOSURE=()
      collect_closure "$probe/libuser.dylib" 2>&1
    )"
    if printf '%s' "$message" | grep -q 'unbundlable non-system dependency'; then
      rejected=yes
    else
      printf '    %s\n' "$message"
    fi
    check "a non-system dependency outside the allow-list is refused" "$rejected"
  else
    skip_all "clang could not build the dependency probe"
  fi
else
  printf '  --   clang is unavailable, the closure probe was skipped\n'
fi

# ---- external manifest --------------------------------------------------

EXTERNAL_MANIFEST="$WORK/dist/${PACKAGE_NAME}.manifest.json"
EMBEDDED_MANIFEST="$PACKAGE_ROOT/share/telecli/manifest.json"

start "TestPackageProducesExternalManifest"
if test -f "$EXTERNAL_MANIFEST"; then
  pass "the external manifest is published next to the archive"
else
  fail "the external manifest is published next to the archive: $EXTERNAL_MANIFEST"
fi
for asset in "${PACKAGE_NAME}.tar.gz" "${PACKAGE_NAME}.tar.gz.sha256" \
             "${PACKAGE_NAME}.manifest.json"; do
  test -f "$WORK/dist/$asset" \
    && pass "asset present: $asset" \
    || fail "asset present: $asset"
done

start "TestExternalManifestMatchesEmbeddedManifest"
if cmp -s "$EXTERNAL_MANIFEST" "$EMBEDDED_MANIFEST"; then
  pass "the external and embedded manifests are byte identical"
else
  fail "the external and embedded manifests are byte identical"
  diff "$EXTERNAL_MANIFEST" "$EMBEDDED_MANIFEST" | sed 's/^/    /' | head -10
fi
accepts "the verifier accepts the package with its external assets" \
  "$VERIFIER" "$PACKAGE_ROOT" --dist "$WORK/dist"

start "TestVerifierRejectsMissingExternalManifest"
missing="$WORK/missing-manifest"
mkdir -p "$missing"
cp "$WORK/dist/${PACKAGE_NAME}.tar.gz" "$missing/"
cp "$WORK/dist/${PACKAGE_NAME}.tar.gz.sha256" "$missing/"
rejects "an absent external manifest fails verification" \
  "$VERIFIER" "$PACKAGE_ROOT" --dist "$missing"

start "TestVerifierRejectsChangedExternalManifest"
changed="$WORK/dist-changed"
rm -rf "$changed"; mkdir -p "$changed"
cp "$WORK/dist/${PACKAGE_NAME}.tar.gz" "$changed/"
cp "$WORK/dist/${PACKAGE_NAME}.tar.gz.sha256" "$changed/"
printf '\n' >> "$changed/${PACKAGE_NAME}.manifest.json"
cp "$EMBEDDED_MANIFEST" "$changed/${PACKAGE_NAME}.manifest.json"
printf 'x' >> "$changed/${PACKAGE_NAME}.manifest.json"
rejects "an altered external manifest fails verification" \
  "$VERIFIER" "$PACKAGE_ROOT" --dist "$changed"

start "TestVerifierRejectsExternalEmbeddedManifestMismatch"
# The embedded manifest is changed and its recorded hash is repaired, so
# the internal checksum check passes and only the comparison against the
# external copy can catch the substitution.
mismatch="$WORK/mismatch"
rm -rf "$mismatch"; mkdir -p "$mismatch"
cp "$WORK/dist/${PACKAGE_NAME}.tar.gz" "$mismatch/"
cp "$WORK/dist/${PACKAGE_NAME}.tar.gz.sha256" "$mismatch/"
cp "$EXTERNAL_MANIFEST" "$mismatch/${PACKAGE_NAME}.manifest.json"
cp -R "$PACKAGE_ROOT" "$mismatch/$PACKAGE_NAME"
python3 - "$mismatch/$PACKAGE_NAME" <<'PYTHON'
import hashlib
import json
import os
import sys

root = sys.argv[1]
manifest = os.path.join(root, "share", "telecli", "manifest.json")
sums = os.path.join(root, "SHA256SUMS")

with open(manifest, encoding="utf-8") as stream:
    document = json.load(stream)
document["build"]["go_version"] = "go1.0.0"
with open(manifest, "w", encoding="utf-8") as stream:
    json.dump(document, stream, indent=2)
    stream.write("\n")

with open(manifest, "rb") as stream:
    digest = hashlib.sha256(stream.read()).hexdigest()

lines = []
with open(sums, encoding="utf-8") as stream:
    for line in stream.read().splitlines():
        if line.endswith("  share/telecli/manifest.json"):
            lines.append(f"{digest}  share/telecli/manifest.json")
        else:
            lines.append(line)
with open(sums, "w", encoding="utf-8") as stream:
    stream.write("\n".join(lines) + "\n")
PYTHON
rejects "a substituted embedded manifest fails verification" \
  "$VERIFIER" "$mismatch/$PACKAGE_NAME" --dist "$mismatch"

start "TestRepeatedReleaseReplacesOnlyItsOwnExternalManifest"
# An external manifest of the current version must be replaced, and an
# unknown one belonging to a different version must survive untouched.
stale="$WORK/stale-dist"
rm -rf "$stale"; mkdir -p "$stale"
foreign="$stale/telecli_9.9.9_darwin_arm64.manifest.json"
printf '{"foreign":true}\n' > "$foreign"
before="$(shasum -a 256 "$foreign" | awk '{print $1}')"
printf '{"stale":true}\n' > "$stale/${PACKAGE_NAME}.manifest.json"

DIST_DIR="$stale" PACKAGE_VERSION="v0.1.0-test" \
TDLIB_LIBRARY="$TELECLI_TDLIB_LIBRARY" \
TDLIB_VERSION="1.8.67" \
TDLIB_COMMIT="ea97bcdd3a15523c58ddfe772b4547187cf5bbeb" \
TDLIB_SOURCE_DIR="$(cd "$(dirname "$TELECLI_TDLIB_LIBRARY")/.." && pwd -P)" \
OPENSSL_PREFIX="${OPENSSL_PREFIX:-$(brew --prefix openssl@3)}" \
OPENSSL_VERSION="${OPENSSL_VERSION:-$(brew list --versions openssl@3 | awk '{print $2}')}" \
ZLIB_PREFIX="${ZLIB_PREFIX:-$(brew --prefix zlib)}" \
ZLIB_VERSION="${ZLIB_VERSION:-$(brew list --versions zlib | awk '{print $2}')}" \
  "$PACKAGER" > "$WORK/rebuild.log" 2>&1

if grep -q '"stale"' "$stale/${PACKAGE_NAME}.manifest.json" 2>/dev/null; then
  fail "a stale external manifest of the current version is replaced"
else
  pass "a stale external manifest of the current version is replaced"
fi

after="$(shasum -a 256 "$foreign" | awk '{print $1}')"
test "$before" = "$after" \
  && pass "an external manifest of another version is preserved" \
  || fail "an external manifest of another version is preserved"

start "TestPackagerIsIdempotent"
first="$(cd "$PACKAGE_ROOT" && find . -type f | sort | shasum -a 256 | awk '{print $1}')"
if build_package; then
  second="$(cd "$PACKAGE_ROOT" && find . -type f | sort | shasum -a 256 | awk '{print $1}')"
  # Each dylib must carry exactly one @loader_path rpath: the TDLib
  # install name is an @rpath reference, so a missing or repeated entry
  # would change how the library resolves.
  rpath_state=yes
  for dylib in "$PACKAGE_ROOT"/lib/*.dylib; do
    test -L "$dylib" && continue
    mapfile_count="$(
      otool -l "$dylib" \
        | awk '/cmd LC_RPATH/ {f=1; next} f && $1 == "path" && $2 == "@loader_path" {n++} f && $1 == "path" {f=0} END {print n + 0}'
    )"
    if test "$mapfile_count" != "1"; then
      rpath_state=no
      printf '    %s has %s @loader_path rpath entries\n' \
        "${dylib##*/}" "$mapfile_count"
    fi
  done
  check "a repeated run produces the same payload" \
    "$(test "$first" = "$second" && echo yes || echo no)"
  check "every dylib has exactly one @loader_path rpath" "$rpath_state"
else
  fail "a repeated run must succeed"
  tail -20 "$WORK/build.log"
fi

# ---- result -------------------------------------------------------------

printf '\n===== result =====\n'
printf 'passed: %d\n' "$passed"
printf 'failed: %d\n' "$failed"
printf 'skipped: %d\n' "$skipped"

test "$failed" -eq 0

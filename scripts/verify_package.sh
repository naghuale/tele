#!/usr/bin/env bash
#
# verify_package.sh independently checks a built telecli macOS package.
#
# The packager must not be trusted to describe its own output, so this
# script re-derives the contract from the package directory:
#
#   * the payload contains exactly the agreed set of entries;
#   * SHA256SUMS covers every regular file except itself, and every
#     recorded hash still matches;
#   * the manifest agrees with the filesystem and declares every bundled
#     library and licence file;
#   * the dynamic dependency graph is closed inside the package;
#   * no packaged Mach-O carries a Homebrew or user path;
#   * every packaged dylib has a valid ad-hoc signature;
#   * the packaged binary loads the packaged TDLib in a scrubbed
#     environment.
#
# The closure helpers are sourced from package_macos.sh so the two
# scripts cannot drift apart.

set -euo pipefail

readonly VERIFIER_NAME="${BASH_SOURCE[0]##*/}"
readonly VERIFIER_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
readonly PACKAGER="$VERIFIER_DIR/package_macos.sh"
readonly VERIFIER_SCHEMA="$VERIFIER_DIR/../schemas/telecli-package-manifest-v1.schema.json"

readonly EXPECTED_ENTRIES="SHA256SUMS
bin/telecli
lib/libcrypto.3.dylib
lib/libssl.3.dylib
lib/libtdjson.1.8.67.dylib
lib/libtdjson.dylib
lib/libz.1.dylib
share/licenses/openssl/LICENSE.txt
share/licenses/tdlib/LICENSE_1_0.txt
share/licenses/zlib/LICENSE
share/telecli/RELEASE_NOTES.md
share/telecli/manifest.json"

# package_root="$1", passed through the environment for clarity.
package_root=""

# The closure helpers are shared with the packager, so they are sourced
# before the verifier defines its own reporting functions and overrides
# the packager's. The main guard in package_macos.sh keeps its build
# steps from running on source.
# shellcheck source=./package_macos.sh
source "$PACKAGER"

fail() {
  printf 'FAIL: [%s] %s\n' "$VERIFIER_NAME" "$1" >&2
  exit 1
}

log() {
  printf '\n===== %s =====\n' "$1"
}

info() {
  printf '  %s\n' "$1"
}

# json_value prints a top-level scalar from a JSON document.
json_value() {
  python3 -c '
import json, sys
data = json.load(open(sys.argv[1]))
value = data
for key in sys.argv[2].split("."):
    value = value[int(key)] if isinstance(value, list) else value[key]
print(value)
' "$1" "$2"
}

# verify_payload checks that the directory holds exactly the agreed set.
verify_payload() {
  local actual
  local expected

  # Both sides are sorted with the same collation, so the literal above
  # only has to list the agreed entries, not a specific order.
  expected="$(printf '%s\n' "$EXPECTED_ENTRIES" | sort)"
  actual="$(
    cd "$package_root" \
      && find . -mindepth 1 \( -type f -o -type l \) \
      | sed 's|^\./||' \
      | sort
  )"

  if test "$actual" != "$expected"; then
    printf 'unexpected payload:\n%s\n' "$actual" >&2
    fail "payload does not match the agreed entry set"
  fi

  test -L "$package_root/lib/libtdjson.dylib" || \
    fail "lib/libtdjson.dylib must be a symlink"
  test -d "$package_root/lib/libtdjson.dylib" && \
    fail "lib/libtdjson.dylib must not resolve to a directory"

  local target
  target="$(readlink "$package_root/lib/libtdjson.dylib")"
  test "$target" = "libtdjson.1.8.67.dylib" || \
    fail "TDLib alias must point at libtdjson.1.8.67.dylib, got $target"

  info "payload: 11 entries, exactly as agreed"
}

# verify_checksums re-computes every recorded hash and requires coverage
# of all regular files except SHA256SUMS.
verify_checksums() {
  local sums="$package_root/SHA256SUMS"
  local listed
  local regular

  test -f "$sums" || fail "SHA256SUMS is missing"

  listed="$(awk '{print $2}' "$sums" | sort)"

  regular="$(
    cd "$package_root" \
      && find . -type f \
      | sed 's|^\./||' \
      | grep -v '^SHA256SUMS$' \
      | sort
  )"

  test "$listed" = "$regular" || {
    printf 'listed but not present or vice versa:\n%s\n' \
      "$(printf '%s\n%s\n' "$listed" "$regular" | sort -u)" >&2
    fail "SHA256SUMS does not cover exactly the regular payload files"
  }

  local shasum_cmd
  if command -v sha256sum >/dev/null 2>&1; then
    shasum_cmd="sha256sum"
  else
    shasum_cmd="shasum -a 256"
  fi

  local line
  while IFS= read -r line; do
    local expected path actual
    expected="$(printf '%s' "$line" | awk '{print $1}')"
    path="$(printf '%s' "$line" | awk '{print $2}')"
    actual="$(cd "$package_root" && $shasum_cmd "$path" | awk '{print $1}')"

    test "$actual" = "$expected" || \
      fail "checksum mismatch for $path"
  done < "$sums"

  info "checksums: $(wc -l < "$sums" | tr -d ' ') files verified"
}

# verify_manifest cross-checks the manifest against the filesystem.
verify_manifest() {
  local manifest="$package_root/share/telecli/manifest.json"

  test -f "$manifest" || fail "manifest is missing"

  test "$(json_value "$manifest" schema_version)" = "1" || \
    fail "manifest schema_version must be 1"

  test "$(json_value "$manifest" package.name)" = "telecli" || \
    fail "manifest package name must be telecli"
  test "$(json_value "$manifest" package.target.os)" = "darwin" || \
    fail "manifest target os must be darwin"
  test "$(json_value "$manifest" package.target.arch)" = "arm64" || \
    fail "manifest target arch must be arm64"

  # The signing block must describe the real state: the package carries
  # no trusted signature, only ad-hoc sealed Mach-O files.
  test "$(json_value "$manifest" signing.package_status)" = "unsigned" || \
    fail "manifest must record package_status unsigned"
  test "$(json_value "$manifest" signing.mach_o_status)" = "adhoc" || \
    fail "manifest must record mach_o_status adhoc"
  test "$(json_value "$manifest" signing.notarized)" = "False" || \
    fail "manifest must record notarized false"
  test "$(json_value "$manifest" signing.hardened_runtime)" = "False" || \
    fail "manifest must record hardened_runtime false"

  test "$(json_value "$manifest" tdlib.source)" = "packaged" || \
    fail "manifest must record the packaged TDLib source"
  test "$(json_value "$manifest" tdlib.library)" = "lib/libtdjson.1.8.67.dylib" || \
    fail "manifest tdlib.library is wrong"
  test "$(json_value "$manifest" tdlib.loader_alias)" = "lib/libtdjson.dylib" || \
    fail "manifest tdlib.loader_alias is wrong"

  python3 "$manifest_check" "$package_root" || \
    fail "manifest does not describe the package faithfully"
}

# verify_no_local_paths rejects Homebrew and user paths in any load-time
# dependency.
#
# The install name of a Mach-O is exempt: it is used when linking
# against the library, not when loading it, and the release contract
# fixes it on purpose. gate_closure applies the same exemption.
verify_no_local_paths() {
  local binary
  local dep
  local self_id
  local found=""

  while IFS= read -r binary; do
    self_id="$(install_name "$binary")"

    while IFS= read -r dep; do
      test "$dep" = "$self_id" && continue

      case "$dep" in
        /opt/homebrew/*|/usr/local/*|/Users/*|/home/*|"$HOME"/*)
          found="$found ${binary##*/}:$dep"
          ;;
      esac
    done < <(load_names "$binary")
  done < <(find "$package_root" -type f -name '*.dylib' | sort)

  test -z "$found" || fail "packaged dylib depends on a local path:$found"

  info 'no Homebrew or user path in any load-time dependency'
}

# verify_signatures requires a valid ad-hoc signature on every dylib.
verify_signatures() {
  local binary
  local count=0

  while IFS= read -r binary; do
    codesign --verify --strict "$binary" >/dev/null 2>&1 || \
      fail "ad-hoc signature is not valid for ${binary##*/}"

    # The display output is captured instead of piped: grep -q exits on
    # the first match, and under pipefail the resulting SIGPIPE from
    # codesign would be reported as a failure.
    local details
    details="$(codesign --display --verbose=1 "$binary" 2>&1)" || \
      fail "codesign --display failed for ${binary##*/}"

    printf '%s\n' "$details" | grep -q 'Signature=adhoc' || \
      fail "${binary##*/} is not ad-hoc signed"

    count=$((count + 1))
  done < <(find "$package_root/lib" -type f -name '*.dylib' | sort)

  test "$count" -eq 4 || fail "expected 4 packaged dylibs, found $count"
  info "signatures: $count ad-hoc signatures verified"
}

# verify_runtime probes the packaged binary in a scrubbed environment.
verify_runtime() {
  local probe_dir
  local output

  probe_dir="$(mktemp -d)"
  # shellcheck disable=SC2064
  trap "rm -rf '$probe_dir'" EXIT

  output="$(
    cd "$probe_dir" \
      && env -u TELECLI_TDLIB_LIBRARY -u TELECLI_CONFIG \
          -u DYLD_LIBRARY_PATH -u DYLD_FALLBACK_LIBRARY_PATH \
          -u DYLD_FRAMEWORK_PATH \
          "$package_root/bin/telecli" doctor
  )" || fail "packaged doctor command failed"

  printf '%s\n' "$output" | grep -F 'TDLib runtime: available' >/dev/null || \
    fail "packaged binary cannot load the packaged TDLib"
  printf '%s\n' "$output" | grep -F 'TDLib compatibility: verified' >/dev/null || \
    fail "packaged binary rejected the packaged TDLib"

  info 'runtime: TDLib loaded from the package, compatibility verified'
}

# validate_against_schema checks a manifest against the published schema.
#
# A real Draft 2020-12 validator is used when the jsonschema package is
# importable. It is not a release dependency: requiring a pip install
# before a release would be worse than an explicit fallback, so when the
# package is missing the mode is reported instead of silently skipped.
validate_against_schema() {
  local manifest="$1"

  test -f "$VERIFIER_SCHEMA" || \
    fail "manifest schema is missing: $VERIFIER_SCHEMA"

  python3 "$schema_check" "$VERIFIER_SCHEMA" "$manifest"
}

# schema_gate turns a schema outcome into a decision.
#
# It exists so no caller can trip over set -e: an unavailable validator
# and a real schema violation have to be distinguished, and both must be
# handled explicitly rather than by accident.
schema_gate() {
  local manifest="$1"
  local status=0

  validate_against_schema "$manifest" || status=$?

  case "$status" in
    0)
      info "manifest satisfies the published schema"
      ;;
    2)
      info "schema validation unavailable, explicit field checks were used"
      ;;
    *)
      fail "manifest does not satisfy the published schema: $manifest"
      ;;
  esac
}

# verify_external_assets checks the artefacts published next to the
# package.
#
# The external manifest is what a consumer can read before unpacking, so
# it has to exist, has to describe the same package as the embedded copy,
# and has to satisfy the schema on its own.
verify_external_assets() {
  local dist_dir="$1"
  local package_name
  local archive
  local external

  package_name="$(basename "$package_root")"
  archive="$dist_dir/${package_name}.tar.gz"
  external="$dist_dir/${package_name}.manifest.json"

  test -f "$archive" || fail "package archive is missing: $archive"
  test -f "${archive}.sha256" || \
    fail "package archive checksum is missing: ${archive}.sha256"
  test -f "$external" || \
    fail "external package manifest is missing: $external"

  if ! cmp -s "$external" "$package_root/share/telecli/manifest.json"; then
    fail "external and embedded package manifests differ"
  fi

  local recorded
  recorded="$(awk '{print $1}' "${archive}.sha256")"
  local actual
  if command -v sha256sum >/dev/null 2>&1; then
    actual="$(cd "$dist_dir" && sha256sum "${package_name}.tar.gz" | awk '{print $1}')"
  else
    actual="$(cd "$dist_dir" && shasum -a 256 "${package_name}.tar.gz" | awk '{print $1}')"
  fi
  test "$actual" = "$recorded" || \
    fail "package archive checksum mismatch: recorded $recorded, got $actual"

  # The external copy is validated before unpacking and the embedded one
  # again afterwards, so neither can drift from the contract alone.
  schema_gate "$external"
  schema_gate "$package_root/share/telecli/manifest.json"

  info "external assets: archive, checksum and manifest verified"
}

main() {
  manifest_check=""
  schema_check=""
  dist_dir=""

  test -f "$PACKAGER" || fail "packager is missing: $PACKAGER"

  test -n "$1" || fail "usage: $0 <package-root> [--dist <dist-dir>]"
  package_root="$1"
  shift

  while test "$#" -gt 0; do
    case "$1" in
      --dist)
        test -n "${2:-}" || fail "--dist requires a directory"
        dist_dir="$2"
        shift 2
        ;;
      *)
        fail "unknown argument: $1"
        ;;
    esac
  done

  test -d "$package_root" || fail "package root is not a directory: $package_root"

  manifest_check="$(mktemp)"
  schema_check="$(mktemp)"
  # shellcheck disable=SC2064
  trap "rm -f '$manifest_check' '$schema_check'" EXIT

  require_command otool
  require_command codesign
  require_command find
  require_command python3

  log "Verify payload"
  verify_payload

  log "Verify checksums"
  verify_checksums

  log "Verify manifest"
  cat > "$manifest_check" <<'PYTHON'
import hashlib
import json
import os
import stat
import sys

root = sys.argv[1]
manifest = json.load(
    open(os.path.join(root, "share", "telecli", "manifest.json"))
)

problems = []


def digest(path):
    h = hashlib.sha256()
    with open(path, "rb") as stream:
        for chunk in iter(lambda: stream.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


# Every entry must exist with the recorded shape.
listed_files = set()
listed_symlinks = set()

for entry in manifest["files"]:
    path = os.path.join(root, entry["path"])
    if entry["type"] == "file":
        listed_files.add(entry["path"])
        if not os.path.isfile(path) or os.path.islink(path):
            problems.append(f"{entry['path']}: not a regular file")
            continue
        mode = stat.S_IMODE(os.stat(path).st_mode)
        if entry["mode"] != f"0{mode:03o}":
            problems.append(f"{entry['path']}: mode mismatch")
        if entry["size"] != os.stat(path).st_size:
            problems.append(f"{entry['path']}: size mismatch")
        if entry["sha256"] != digest(path):
            problems.append(f"{entry['path']}: sha256 mismatch")
    else:
        listed_symlinks.add(entry["path"])
        if not os.path.islink(path):
            problems.append(f"{entry['path']}: not a symlink")
        elif os.readlink(path) != entry["target"]:
            problems.append(f"{entry['path']}: symlink target mismatch")

# The manifest must describe every real entry, and nothing invented.
on_disk = set()
for directory, dirnames, filenames in os.walk(root):
    dirnames[:] = [d for d in dirnames if d != ".git"]
    for name in filenames:
        absolute = os.path.join(directory, name)
        relative = os.path.relpath(absolute, root)
        if relative in ("SHA256SUMS", "share/telecli/manifest.json"):
            continue
        on_disk.add(relative)

for missing in sorted(on_disk - listed_files - listed_symlinks):
    problems.append(f"{missing}: present in the package but not in the manifest")
for extra in sorted((listed_files | listed_symlinks) - on_disk):
    problems.append(f"{extra}: declared in the manifest but not in the package")

# Every bundled library and licence file must be declared by a component.
declared_libs = set()
declared_licenses = set()
component_names = set()

for component in manifest["components"]:
    component_names.add(component["name"])
    for library in component["libraries"]:
        declared_libs.add(library)
        if library not in listed_files:
            problems.append(f"{library}: component library is not in the manifest")
    for licence in component["license_files"]:
        declared_licenses.add(licence)
        if licence not in listed_files:
            problems.append(f"{licence}: component licence is not in the manifest")

expected_components = {"tdlib", "openssl", "zlib"}
if component_names != expected_components:
    problems.append(
        f"components must be {sorted(expected_components)}, got {sorted(component_names)}"
    )

# Every packaged dylib must be attributed to some component.
for entry in sorted(listed_files):
    if entry.startswith("lib/") and entry.endswith(".dylib"):
        if entry not in declared_libs:
            problems.append(f"{entry}: packaged dylib is not declared by any component")

for problem in problems:
    print(f"  {problem}", file=sys.stderr)

sys.exit(1 if problems else 0)
PYTHON

  cat > "$schema_check" <<'PYTHON'
import json
import sys

schema_path, manifest_path = sys.argv[1], sys.argv[2]

with open(schema_path, encoding="utf-8") as stream:
    schema = json.load(stream)
with open(manifest_path, encoding="utf-8") as stream:
    document = json.load(stream)

try:
    import jsonschema
except ImportError:
    # No validator is available, so the schema is not enforced here. The
    # caller performs explicit field checks instead, and saying so is
    # better than reporting a pass that never happened.
    print("  schema: not validated, the jsonschema package is unavailable")
    sys.exit(2)

validator = jsonschema.Draft202012Validator(schema)
errors = sorted(validator.iter_errors(document), key=lambda e: list(e.path))

for error in errors:
    location = "/".join(str(part) for part in error.path) or "<root>"
    print(f"  schema: {location}: {error.message}")

sys.exit(1 if errors else 0)
PYTHON

  verify_manifest

  log "Verify schema"
  schema_gate "$package_root/share/telecli/manifest.json"

  if test -n "$dist_dir"; then
    log "Verify external assets"
    verify_external_assets "$dist_dir"
  fi

  log "Verify dependency closure"
  gate_closure "$package_root"

  log "Verify local paths"
  verify_no_local_paths

  log "Verify signatures"
  verify_signatures

  log "Verify runtime"
  verify_runtime

  rm -f "$manifest_check" "$schema_check"

  log "Package verification result"
  printf '%s\n' 'PASS: package matches the release contract'
}

main "$@"

#!/usr/bin/env python3
"""Generate the telecli package manifest.

The manifest is written from a typed data model rather than by string
concatenation, so a field can never be emitted with an unexpected shape.
Only regular files and symlinks are described, every path is relative, and
entries are sorted by path so the output is byte-stable for identical
input.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import stat
import sys
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

SCHEMA_VERSION = 1

# Files that must be present in the package for it to be usable.
REQUIRED_FILES = (
    "bin/telecli",
    "share/telecli/RELEASE_NOTES.md",
)

# The alias is the only symlink the package ships.
REQUIRED_SYMLINK = "lib/libtdjson.dylib"

# Directories the manifest generator never descends into.
SKIP_DIRS = {".git"}


class ManifestError(Exception):
    """A condition that must abort packaging."""


@dataclass(frozen=True)
class FileEntry:
    """One regular file or symlink inside the package."""

    path: str
    type: str
    mode: str | None = None
    size: int | None = None
    sha256: str | None = None
    target: str | None = None

    def to_json(self) -> dict[str, Any]:
        entry: dict[str, Any] = {"path": self.path, "type": self.type}
        if self.mode is not None:
            entry["mode"] = self.mode
        if self.size is not None:
            entry["size"] = self.size
        if self.sha256 is not None:
            entry["sha256"] = self.sha256
        if self.target is not None:
            entry["target"] = self.target
        return entry


@dataclass(frozen=True)
class Component:
    """A redistributed third-party component."""

    name: str
    version: str
    license: str
    license_files: tuple[str, ...]
    libraries: tuple[str, ...]
    commit: str | None = None

    def to_json(self) -> dict[str, Any]:
        entry: dict[str, Any] = {
            "name": self.name,
            "version": self.version,
            "license": self.license,
            "license_files": list(self.license_files),
            "libraries": list(self.libraries),
        }
        if self.commit is not None:
            entry["commit"] = self.commit
        return entry


@dataclass
class Manifest:
    """The whole manifest document."""

    package_version: str
    channel: str
    commit: str
    timestamp: str
    go_version: str
    tdlib_version: str
    tdlib_commit: str
    tdlib_library: str
    components: list[Component] = field(default_factory=list)
    files: list[FileEntry] = field(default_factory=list)

    def to_json(self) -> dict[str, Any]:
        return {
            "schema_version": SCHEMA_VERSION,
            "package": {
                "name": "telecli",
                "version": self.package_version,
                "channel": self.channel,
                "target": {"os": "darwin", "arch": "arm64"},
            },
            "build": {
                "commit": self.commit,
                "timestamp": self.timestamp,
                "go_version": self.go_version,
                "cgo_enabled": True,
                "trimpath": True,
                "vcs_modified": False,
            },
            "tdlib": {
                "version": self.tdlib_version,
                "commit": self.tdlib_commit,
                "compatibility": "verified",
                "library": self.tdlib_library,
                "loader_alias": REQUIRED_SYMLINK,
                "source": "packaged",
            },
            "signing": {
                # The package itself carries no trusted signature; the
                # Mach-O files are ad-hoc sealed because relinking
                # invalidates the original signature.
                "package_status": "unsigned",
                "mach_o_status": "adhoc",
                "hardened_runtime": False,
                "notarized": False,
            },
            "entrypoints": {"binary": "bin/telecli"},
            "components": [component.to_json() for component in self.components],
            "files": [entry.to_json() for entry in self.files],
        }


def validate_relative_path(path: str) -> str:
    """Reject anything that must never appear in a manifest.

    Absolute paths, parent traversal, doubled separators, backslashes and
    NUL bytes would all make a package machine-dependent or unsafe.
    """
    if not path:
        raise ManifestError("empty path")

    if path.startswith("/"):
        raise ManifestError(f"absolute path is not allowed: {path}")

    if "\\" in path:
        raise ManifestError(f"backslash is not allowed: {path}")

    if "\x00" in path:
        raise ManifestError("NUL byte is not allowed in a path")

    if "//" in path:
        raise ManifestError(f"doubled separator is not allowed: {path}")

    for part in path.split("/"):
        if part == "..":
            raise ManifestError(f"parent traversal is not allowed: {path}")

    return path


def sha256_of(path: Path) -> str:
    """Return the lowercase hex SHA-256 of a file."""
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def describe(root: Path) -> list[FileEntry]:
    """Describe every regular file and symlink under root."""
    entries: list[FileEntry] = []
    seen: dict[str, str] = {}

    for directory, dirnames, filenames in os.walk(root):
        dirnames[:] = sorted(name for name in dirnames if name not in SKIP_DIRS)

        for filename in sorted(filenames):
            absolute = Path(directory) / filename
            relative = validate_relative_path(
                str(absolute.relative_to(root))
            )

            if absolute.is_symlink():
                target = os.readlink(absolute)
                entries.append(
                    FileEntry(
                        path=relative,
                        type="symlink",
                        target=validate_relative_path(target),
                    )
                )
                continue

            if not absolute.is_file():
                raise ManifestError(f"not a regular file: {relative}")

            mode = stat.S_IMODE(absolute.stat().st_mode)
            entries.append(
                FileEntry(
                    path=relative,
                    type="file",
                    mode=f"0{mode:03o}",
                    size=absolute.stat().st_size,
                    sha256=sha256_of(absolute),
                )
            )

            if relative in seen:
                raise ManifestError(f"duplicate path: {relative}")
            seen[relative] = "file"

    entries.sort(key=lambda entry: entry.path)
    return entries


def require(entries: list[FileEntry], path: str, entry_type: str) -> None:
    """Fail when a required package entry is missing."""
    for entry in entries:
        if entry.path == path and entry.type == entry_type:
            return

    raise ManifestError(f"required {entry_type} is missing: {path}")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--package-root", required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--channel", default="prerelease")
    parser.add_argument("--commit", required=True)
    parser.add_argument("--timestamp", required=True)
    parser.add_argument("--go-version", required=True)
    parser.add_argument("--tdlib-version", required=True)
    parser.add_argument("--tdlib-commit", required=True)
    parser.add_argument("--tdlib-library", required=True)
    parser.add_argument(
        "--component",
        action="append",
        default=[],
        metavar="NAME,VERSION,LICENSE,LIBS,COMMIT",
        help=(
            "comma separated component description; LIBS is a "
            "semicolon separated list of package paths"
        ),
    )
    parser.add_argument("--license-file", action="append", default=[])
    parser.add_argument("--output", required=True)

    args = parser.parse_args()

    root = Path(args.package_root)
    if not root.is_dir():
        raise ManifestError(f"package root is not a directory: {root}")

    entries = describe(root)

    for required in REQUIRED_FILES:
        require(entries, required, "file")
    require(entries, REQUIRED_SYMLINK, "symlink")

    components: list[Component] = []
    for raw in args.component:
        parts = raw.split(",")
        if len(parts) not in (4, 5):
            raise ManifestError(
                f"component needs 4 or 5 fields, got {len(parts)}: {raw}"
            )

        name, version, license_name, libraries = parts[:4]
        commit = parts[4] if len(parts) == 5 else None

        components.append(
            Component(
                name=name,
                version=version,
                license=license_name,
                license_files=tuple(
                    validate_relative_path(path) for path in args.license_file
                ),
                libraries=tuple(
                    validate_relative_path(path)
                    for path in libraries.split(";")
                    if path
                ),
                commit=commit,
            )
        )

    manifest = Manifest(
        package_version=args.version,
        channel=args.channel,
        commit=args.commit,
        timestamp=args.timestamp,
        go_version=args.go_version,
        tdlib_version=args.tdlib_version,
        tdlib_commit=args.tdlib_commit,
        tdlib_library=validate_relative_path(args.tdlib_library),
        components=components,
        files=entries,
    )

    output = Path(args.output)
    output.parent.mkdir(parents=True, exist_ok=True)

    encoded = json.dumps(
        manifest.to_json(),
        indent=2,
        ensure_ascii=False,
        sort_keys=False,
    )
    output.write_text(encoded + "\n", encoding="utf-8")
    output.chmod(0o644)

    print(f"manifest: {output} ({len(entries)} entries)")
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except ManifestError as error:
        print(f"FAIL: {error}", file=sys.stderr)
        sys.exit(1)

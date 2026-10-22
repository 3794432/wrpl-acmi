#!/usr/bin/env python3
"""Build the countermeasure ammo catalogue using only pinned outer bulletType.

Run without arguments to download four bounded, hash-verified Datamine sources.
For offline use, --source-root must contain the original Datamine directory tree
at COMMIT, with the exact source bytes listed in SOURCES. Offline mode never
falls back to the network. No third-party packages are required.

Launcher keys are exact .blkx filename stems. Bullet keys are direct launcher
block names, not bulletName display identifiers. The key "bullet" denotes the
default block selected by an empty storage bullet string. Named blocks retain
their exact spelling. Mixed, missing, or unrecognized outer bulletType values
are omitted and recorded as unclassified; nested rocket.bulletType is ignored.
This catalogue makes no claim about numeric shell_type indexes.
"""

import argparse
import hashlib
import json
from pathlib import Path
import sys
import urllib.error
import urllib.request


COMMIT = "f7952e4291e9fd7b6fe38e84020dddef925c3457"
REPOSITORY = "https://github.com/gszabi99/War-Thunder-Datamine"
RAW_ROOT = "https://raw.githubusercontent.com/gszabi99/War-Thunder-Datamine"
SOURCE_DIRECTORY = "aces.vromfs.bin_u/gamedata/weapons/rocketguns"
MAX_SOURCE_BYTES = 1 << 20
DOWNLOAD_ATTEMPTS = 2
SOURCES = {
    "countermeasure_large_split_launcher_jet":
        "7138cdd0f6f7feac4679dff73aa22d15cde9456d57d84b2aa47ad374eb5583a6",
    "countermeasure_split_large_launcher_jet_maw":
        "22358795502318b1f67659aa7fadc0e943731f26e1f0d7186f54f35f5db2eddb",
    "countermeasure_split_launcher_jet":
        "61697c8f33ba79e60756281f2c1cbd2b82878e2d3a836755387d3329fa27d4e1",
    "countermeasure_split_launcher_jet_bol":
        "1832bad6ffdd94f80aade4a5b36b915e1e0a59609a477cfb887481d9093d5215",
}
AMMO_TYPES = {"flr": "Flare", "chff": "Chaff"}
DEFAULT_OUTPUT = Path(__file__).resolve().parents[2] / "cmd/acmi/data/countermeasure_ammo.json"


def bounded_read(stream):
    raw = stream.read(MAX_SOURCE_BYTES + 1)
    if len(raw) > MAX_SOURCE_BYTES:
        raise ValueError(f"source exceeds {MAX_SOURCE_BYTES} bytes")
    return raw


def read_source(relative_path, source_root, timeout):
    if source_root is not None:
        root = source_root.resolve(strict=True)
        path = (root / relative_path).resolve(strict=True)
        if not path.is_relative_to(root):
            raise ValueError(f"offline source escapes source root: {relative_path}")
        with path.open("rb") as stream:
            return bounded_read(stream)

    url = f"{RAW_ROOT}/{COMMIT}/{relative_path}"
    request = urllib.request.Request(url, headers={"User-Agent": "wrpl-acmi-countermeasure-catalogue/1"})
    for attempt in range(DOWNLOAD_ATTEMPTS):
        try:
            with urllib.request.urlopen(request, timeout=timeout) as response:
                return bounded_read(response)
        except (OSError, urllib.error.URLError) as error:
            if attempt + 1 == DOWNLOAD_ATTEMPTS:
                raise ValueError(f"download failed for {relative_path}: {error}") from error
    raise AssertionError("unreachable")


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError(f"duplicate JSON object key: {key}")
        result[key] = value
    return result


def classify_bullets(value):
    """Accept a bullet object or repeated bullet blocks only when all agree."""
    bullets = value if isinstance(value, list) else [value]
    if not bullets:
        return None
    kinds = set()
    for bullet in bullets:
        if not isinstance(bullet, dict):
            return None
        outer_type = bullet.get("bulletType")
        if not isinstance(outer_type, str) or outer_type not in AMMO_TYPES:
            return None
        kinds.add(AMMO_TYPES[outer_type])
    return next(iter(kinds)) if len(kinds) == 1 else None


def launcher_ammo(source):
    if not isinstance(source, dict):
        raise ValueError("launcher JSON must be an object")
    entries = {}
    unclassified = []
    for name, value in sorted(source.items()):
        if name == "bullet":
            bullets = value
        elif isinstance(value, dict) and "bullet" in value:
            bullets = value["bullet"]
        elif isinstance(value, list) and any(isinstance(v, dict) and "bullet" in v for v in value):
            # Repeated named blocks are not a unique storage bullet selection.
            unclassified.append(name)
            continue
        else:
            continue
        kind = classify_bullets(bullets)
        if kind is None:
            unclassified.append(name)
        else:
            entries[name] = kind
    return entries, unclassified


def build_catalog(source_root=None, timeout=20):
    launchers = {}
    provenance = {}
    unclassified = {}
    for launcher, expected_sha256 in sorted(SOURCES.items()):
        relative_path = f"{SOURCE_DIRECTORY}/{launcher}.blkx"
        raw = read_source(relative_path, source_root, timeout)
        digest = hashlib.sha256(raw).hexdigest()
        if digest != expected_sha256:
            raise ValueError(f"SHA256 mismatch for {relative_path}: expected {expected_sha256}, got {digest}")
        source = json.loads(raw, object_pairs_hook=unique_object)
        entries, skipped = launcher_ammo(source)
        launchers[launcher] = entries
        provenance[launcher] = {
            "path": relative_path,
            "url": f"{RAW_ROOT}/{COMMIT}/{relative_path}",
            "sha256": digest,
            "bytes": len(raw),
        }
        if skipped:
            unclassified[launcher] = skipped
    return {
        "schema": 1,
        "commit": COMMIT,
        "launchers": launchers,
        "provenance": {
            "repository": REPOSITORY,
            "builder": "tools/countermeasures/build_catalog.py",
            "classification": "Outer bulletType only: flr=Flare; chff=Chaff. Mixed or unknown entries omitted.",
            "launcher_key": "Exact .blkx filename stem, without extension.",
            "bullet_key": "Exact direct block name; bullet is the default for an empty storage bullet string.",
            "sources": provenance,
        },
        "unclassified": unclassified,
    }


def render_catalog(catalog):
    return (json.dumps(catalog, ensure_ascii=False, indent=2, sort_keys=True) + "\n").encode("utf-8")


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--source-root", type=Path, help="offline Datamine root containing the pinned directory tree")
    parser.add_argument("--output", type=Path, default=DEFAULT_OUTPUT, help="output JSON (default: repository cmd/acmi/data)")
    parser.add_argument("--timeout", type=int, default=20, choices=range(1, 61), metavar="SECONDS", help="network timeout, 1-60 seconds (default: 20)")
    args = parser.parse_args(argv)
    try:
        catalog = build_catalog(args.source_root, args.timeout)
        rendered = render_catalog(catalog)
        # Resolve and validate every source before touching the output.
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_bytes(rendered)
    except (OSError, ValueError, RecursionError) as error:
        parser.exit(1, f"error: {error}\n")
    count = sum(len(ammo) for ammo in catalog["launchers"].values())
    skipped = sum(len(names) for names in catalog["unclassified"].values())
    print(f"wrote {args.output}: {len(catalog['launchers'])} launchers, {count} ammo mappings, {skipped} unclassified")
    print(f"commit {COMMIT}; SHA256 {hashlib.sha256(rendered).hexdigest()}")
    return 0


if __name__ == "__main__":
    sys.exit(main())

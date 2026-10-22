"""Build a deterministic catalogue of explicit airfield prefab geometry.

The catalogue is source-only. It scans every bounded file in the pinned
Datamine objectgroups directory and emits a prefab only when that prefab has
an explicit airfield block. Runway data comes from start, end, and width.
Radar anchors come only from nodes whose type is exactly air_main_radar or
air_flight_radar.

to_ground is copied only when the explicit airfield block contains that
boolean. In particular, placeAboveTerrain is not re-interpreted as to_ground.
Endpoint Y values remain the source's nominal local values.
"""

from __future__ import annotations

import argparse
import base64
import hashlib
import json
import math
import shutil
import time
import urllib.request
from collections import Counter
from concurrent.futures import ThreadPoolExecutor, as_completed
from pathlib import Path
from typing import Any, Iterable

COMMIT = "f7952e4291e9fd7b6fe38e84020dddef925c3457"
REPOSITORY = "gszabi99/War-Thunder-Datamine"
TREE_DIR = "aces.vromfs.bin_u/gamedata/objectgroups"
MAX_FILE_BYTES = 4 * 1024 * 1024
WORKERS = 4
LISTING_NAME = "objectgroups-listing.json"
USER_AGENT = "wrpl-inspector-prefabs"
RADAR_TYPES = ("air_main_radar", "air_flight_radar")


def _workspace_root() -> Path:
    # tools/airfields/build_prefabs.py -> repo -> work -> workspace
    return Path(__file__).resolve().parents[3]


def _default_cache_dir() -> Path:
    return _workspace_root() / "airfields-all" / "sources" / "cache"


def _default_output() -> Path:
    return _workspace_root() / "airfields-all" / "prefabs.json"


def _default_offline_root() -> Path:
    return _workspace_root() / "airfields" / "offline-datamine"


def _request(url: str, *, timeout: float = 35) -> bytes:
    request = urllib.request.Request(url, headers={"User-Agent": USER_AGENT})
    with urllib.request.urlopen(request, timeout=timeout) as response:
        payload = response.read(MAX_FILE_BYTES + 1)
    if len(payload) > MAX_FILE_BYTES:
        raise ValueError("response exceeds 4 MiB")
    return payload


def _github_listing() -> list[dict[str, Any]]:
    url = (
        f"https://api.github.com/repos/{REPOSITORY}/contents/"
        f"{TREE_DIR}?ref={COMMIT}"
    )
    payload = json.loads(_request(url))
    if not isinstance(payload, list):
        raise ValueError("GitHub objectgroups listing is not an array")
    entries = []
    for item in payload:
        if (
            item.get("type") != "file"
            or not str(item.get("name", "")).lower().endswith(".blkx")
        ):
            continue
        entries.append({
            "name": item["name"],
            "path": str(item["path"]).replace("\\", "/"),
            "size": int(item["size"]),
        })
    if len(payload) >= 1000:
        raise ValueError("GitHub directory listing is truncated")
    return sorted(entries, key=lambda entry: entry["name"])


def _local_listing(source_root: Path) -> list[dict[str, Any]]:
    directory = source_root / TREE_DIR
    if not directory.is_dir():
        raise FileNotFoundError(f"objectgroups directory not found: {directory}")
    entries = []
    for path in sorted(directory.iterdir()):
        if path.is_file() and path.suffix.lower() == ".blkx":
            entries.append({
                "name": path.name,
                "path": f"{TREE_DIR}/{path.name}",
                "size": path.stat().st_size,
            })
    return entries


def _load_or_fetch_listing(
    source_root: Path | None, cache_dir: Path
) -> tuple[list[dict[str, Any]], str]:
    if source_root is not None:
        return _local_listing(source_root), "source-root"

    listing_path = cache_dir / LISTING_NAME
    if listing_path.is_file():
        try:
            listing = json.loads(listing_path.read_text(encoding="utf-8"))
            if (
                listing.get("schema") == 1
                and listing.get("commit") == COMMIT
                and listing.get("directory") == TREE_DIR
                and isinstance(listing.get("files"), list)
            ):
                return listing["files"], "cache"
        except (OSError, ValueError, TypeError):
            pass

    entries = _github_listing()
    cache_dir.mkdir(parents=True, exist_ok=True)
    document = {
        "schema": 1,
        "repository": REPOSITORY,
        "commit": COMMIT,
        "directory": TREE_DIR,
        "files": entries,
    }
    listing_path.write_text(
        json.dumps(document, indent=2, ensure_ascii=False) + "\n",
        encoding="utf-8",
    )
    return entries, "github"


def _download(entry: dict[str, Any]) -> bytes:
    name = entry["name"]
    raw_url = (
        f"https://raw.githubusercontent.com/{REPOSITORY}/{COMMIT}/"
        f"{TREE_DIR}/{name}"
    )
    contents_url = (
        f"https://api.github.com/repos/{REPOSITORY}/contents/"
        f"{TREE_DIR}/{name}?ref={COMMIT}"
    )
    errors: list[str] = []
    for attempt in range(3):
        try:
            raw = _request(raw_url)
            if len(raw) > MAX_FILE_BYTES:
                raise ValueError("file exceeds 4 MiB")
            return raw
        except Exception as exc:
            errors.append(f"raw: {exc}")
            time.sleep(0.35 * (attempt + 1))
    try:
        payload = json.loads(_request(contents_url))
        raw = base64.b64decode(payload["content"])
        if len(raw) > MAX_FILE_BYTES:
            raise ValueError("file exceeds 4 MiB")
        return raw
    except Exception as exc:
        errors.append(f"contents: {exc}")
    raise RuntimeError("; ".join(errors))


def _materialize_file(
    entry: dict[str, Any],
    source_root: Path | None,
    cache_dir: Path,
) -> tuple[bytes, str]:
    name = entry["name"]
    expected_size = entry["size"]
    if expected_size > MAX_FILE_BYTES:
        raise ValueError("candidate exceeds 4 MiB")

    cache_path = cache_dir / TREE_DIR / name
    if source_root is not None:
        source_path = source_root / TREE_DIR / name
        if source_path.is_file() and source_path.stat().st_size == expected_size:
            return source_path.read_bytes(), "source-root"

    offline_path = _default_offline_root() / TREE_DIR / name
    if offline_path.is_file() and offline_path.stat().st_size == expected_size:
        return offline_path.read_bytes(), "offline-datamine"

    if cache_path.is_file() and cache_path.stat().st_size == expected_size:
        return cache_path.read_bytes(), "cache"

    # A previous version of the scratch probe cached files by basename.
    # Move that data into the provenance-preserving tree layout.
    legacy_path = cache_dir / name
    if legacy_path.is_file() and legacy_path.stat().st_size == expected_size:
        raw = legacy_path.read_bytes()
        cache_path.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(legacy_path, cache_path)
        return raw, "legacy-cache"

    raw = _download(entry)
    if len(raw) != expected_size:
        raise ValueError(f"size mismatch: {len(raw)} != {expected_size}")
    cache_path.parent.mkdir(parents=True, exist_ok=True)
    temporary = cache_path.with_name(cache_path.name + ".part")
    temporary.write_bytes(raw)
    temporary.replace(cache_path)
    return raw, "download"


def _vector3(value: Any, label: str) -> list[int | float]:
    if not isinstance(value, list) or len(value) != 3:
        raise ValueError(f"{label} is not a three-element array")
    result = []
    for coordinate in value:
        if isinstance(coordinate, bool) or not isinstance(coordinate, (int, float)):
            raise ValueError(f"{label} contains a non-number")
        if not math.isfinite(coordinate):
            raise ValueError(f"{label} contains a non-finite number")
        result.append(coordinate)
    return result


def _airfield_blocks(document: Any) -> Iterable[tuple[str, Any]]:
    def walk(path: str, value: Any) -> Iterable[tuple[str, Any]]:
        if isinstance(value, dict):
            for key, child in value.items():
                child_path = f"{path}.{key}" if path else key
                if key == "airfield":
                    yield child_path, child
                else:
                    yield from walk(child_path, child)
        elif isinstance(value, list):
            for index, child in enumerate(value):
                child_path = f"{path}[{index}]"
                yield from walk(child_path, child)

    yield from walk("", document)


def _radar_anchors(document: Any) -> dict[str, list[list[int | float]]]:
    anchors: dict[str, list[list[int | float]]] = {"main": [], "flight": []}
    stack: list[Any] = [document]
    while stack:
        value = stack.pop()
        if isinstance(value, dict):
            node_type = value.get("type")
            if node_type in RADAR_TYPES:
                tm = value.get("tm")
                if not isinstance(tm, list) or len(tm) != 4:
                    raise ValueError(f"{node_type}.tm is not a four-row transform")
                position = _vector3(tm[3], f"{node_type}.tm[3]")
                role = "flight" if node_type == "air_flight_radar" else "main"
                anchors[role].append(position)
            child_values = list(value.values())
            stack.extend(reversed(child_values))
        elif isinstance(value, list):
            stack.extend(reversed(value))
    return anchors


def _prefab_id(stem: str, block_count: int, block_index: int) -> str:
    if block_count == 1:
        return stem
    return f"{stem}#airfield-{block_index + 1}"


def _build_with_stats(
    source_root: Path | None = None,
    cache_dir: Path | None = None,
) -> tuple[dict[str, Any], dict[str, Any]]:
    if source_root is not None:
        source_root = Path(source_root)
    cache_dir = Path(cache_dir) if cache_dir is not None else _default_cache_dir()
    cache_dir.mkdir(parents=True, exist_ok=True)

    listing, listing_origin = _load_or_fetch_listing(source_root, cache_dir)
    prefabs: list[dict[str, Any]] = []
    exclusions: list[dict[str, str]] = []
    origin_counts: Counter[str] = Counter()
    airfield_block_count = 0
    files_with_multiple_blocks: list[str] = []

    def read_entry(
        entry: dict[str, Any],
    ) -> tuple[dict[str, Any], bytes | None, str]:
        try:
            raw, origin = _materialize_file(entry, source_root, cache_dir)
            return entry, raw, origin
        except Exception as exc:
            return entry, None, str(exc)

    with ThreadPoolExecutor(max_workers=WORKERS) as pool:
        futures = {pool.submit(read_entry, entry): entry for entry in listing}
        results = [future.result() for future in as_completed(futures)]

    for entry, raw, origin in sorted(results, key=lambda item: item[0]["name"]):
        stem = Path(entry["name"]).stem
        if raw is None:
            exclusions.append({
                "id": stem,
                "reason": f"source unavailable: {origin}",
            })
            continue
        origin_counts[origin] += 1
        try:
            document = json.loads(raw)
        except (UnicodeDecodeError, json.JSONDecodeError) as exc:
            exclusions.append({
                "id": stem,
                "reason": f"invalid JSON source: {exc}",
            })
            continue

        blocks = list(_airfield_blocks(document))
        if not blocks:
            continue
        airfield_block_count += len(blocks)
        if len(blocks) > 1:
            files_with_multiple_blocks.append(stem)
        try:
            radars = _radar_anchors(document)
        except ValueError as exc:
            exclusions.append({
                "id": stem,
                "reason": f"invalid radar anchor: {exc}",
            })
            continue

        digest = hashlib.sha256(raw.replace(b"\r\n", b"\n")).hexdigest()
        source = entry["path"]
        for block_index, (block_path, block) in enumerate(blocks):
            prefab_id = _prefab_id(stem, len(blocks), block_index)
            try:
                if not isinstance(block, dict):
                    raise ValueError("block is not an object")
                start = _vector3(block.get("start"), f"{block_path}.start")
                end = _vector3(block.get("end"), f"{block_path}.end")
                width = block.get("width")
                if isinstance(width, bool) or not isinstance(width, (int, float)):
                    raise ValueError("width is not a number")
                if not math.isfinite(width) or width <= 0:
                    raise ValueError("width is not a positive finite number")
                record: dict[str, Any] = {
                    "id": prefab_id,
                    "source": source,
                    "sha256_lf": digest,
                    "start": start,
                    "end": end,
                    "width": width,
                    "radars": {
                        "main": [list(position) for position in radars["main"]],
                        "flight": [list(position) for position in radars["flight"]],
                    },
                }
                if isinstance(block.get("toGround"), bool):
                    record["to_ground"] = block["toGround"]
                prefabs.append(record)
            except ValueError as exc:
                exclusions.append({
                    "id": prefab_id,
                    "reason": f"invalid explicit {block_path}: {exc}",
                })

    prefabs.sort(key=lambda prefab: prefab["id"])
    exclusions.sort(key=lambda exclusion: exclusion["id"])
    result = {
        "schema": 1,
        "commit": COMMIT,
        "prefabs": prefabs,
        "exclusions": exclusions,
    }

    flight_counts = Counter(len(prefab["radars"]["flight"]) for prefab in prefabs)
    missing_roles = [
        prefab["id"]
        for prefab in prefabs
        if bool(prefab["radars"]["main"]) != bool(prefab["radars"]["flight"])
    ]
    degenerate = [
        prefab["id"]
        for prefab in prefabs
        if prefab["start"] == prefab["end"]
    ]
    stats = {
        "objectgroup_files": len(listing),
        "listing_origin": listing_origin,
        "file_origins": dict(sorted(origin_counts.items())),
        "prefab_count": len(prefabs),
        "airfield_block_count": airfield_block_count,
        "files_with_multiple_airfield_blocks": files_with_multiple_blocks,
        "exclusion_count": len(exclusions),
        "with_radars": sum(bool(prefab["radars"]["main"]) for prefab in prefabs),
        "without_radars": sum(not prefab["radars"]["main"] for prefab in prefabs),
        "flight_anchor_counts": dict(sorted(flight_counts.items())),
        "missing_radar_roles": missing_roles,
        "degenerate_endpoints": degenerate,
    }
    return result, stats


def build(
    source_root: Path | str | None = None,
    cache_dir: Path | str | None = None,
) -> dict[str, Any]:
    """Return the source prefab catalogue.

    source_root selects an offline Datamine checkout, and cache_dir selects a
    persistent bounded download cache. Relative repository paths are retained
    inside the cache.
    """

    return _build_with_stats(source_root, cache_dir)[0]


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source-root", type=Path)
    parser.add_argument("--cache-dir", type=Path, default=_default_cache_dir())
    parser.add_argument("--output", type=Path, default=_default_output())
    args = parser.parse_args()

    result, stats = _build_with_stats(args.source_root, args.cache_dir)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(
        json.dumps(result, indent=2, ensure_ascii=False, allow_nan=False) + "\n",
        encoding="utf-8",
    )
    print(json.dumps(stats, sort_keys=True))
    print(f"Wrote {len(result['prefabs'])} prefabs to {args.output}")


if __name__ == "__main__":
    main()

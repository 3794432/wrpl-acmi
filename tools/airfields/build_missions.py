#!/usr/bin/env python3
"""Build the all-map, pinned Datamine mission airfield inventory.

The output is an offline sidecar. It records where mission airfield object
groups came from and keeps rank-conditional variants separate. Only the root
mission's mission_settings.mission.level assigns a map. Imported files often
carry a different or placeholder level and are never used for map selection.

Python standard library only. Network access uses bounded, cached GitHub API
tree listings and raw source files. --source-root can use a pinned checkout.
"""

from __future__ import annotations

import argparse
import concurrent.futures
import gzip
import hashlib
import json
import math
import os
import re
import sys
import tempfile
import threading
import time
from dataclasses import dataclass, field
from pathlib import Path, PurePosixPath
from typing import Any, Iterable
from urllib.error import HTTPError, URLError
from urllib.parse import quote
from urllib.request import Request, urlopen


COMMIT = "f7952e4291e9fd7b6fe38e84020dddef925c3457"
TREE_SHA = "709cb24a1d0aa4e2f66b651de901fe5f0e69b96b"
REPOSITORY = "gszabi99/War-Thunder-Datamine"
MISSION_PREFIX = "mis.vromfs.bin_u/gamedata/missions/"
OBJECTGROUP_PREFIX = "aces.vromfs.bin_u/gamedata/objectgroups/"
RAW_BASE = f"https://raw.githubusercontent.com/{REPOSITORY}/{COMMIT}/"
API_BASE = f"https://api.github.com/repos/{REPOSITORY}/"
MAX_FILE_BYTES = 4 * 1024 * 1024
MAX_TREE_BYTES = 4 * 1024 * 1024
MAX_WORKERS = 4
MAX_SOURCE_FILES = 4096
MAX_PREFAB_FILES = 96
MAX_DEPTH = 32

# Suffixes identify likely entry missions for downloading. They never assign a
# map; every downloaded candidate is accepted only after its root level is read.
ENTRY_SUFFIXES = (
    "_bfd_norespawn", "_ad", "_afd", "_afdn", "_afdto", "_gs", "_gsn",
    "_gsa", "_gsb", "_i2m", "_tdm", "_duel", "_race", "_biatlon",
    "_tournament", "_pve", "_pvet",
)
SKIP_STEM_PARTS = (
    "template", "_part", "spawns", "convoys", "_ports", "terrain",
    "sound", "effect", "_logic", "unit_set", "_decor",
)
IMPORT_STANDARD_KEYS = {
    "file", "importAreas", "importUnits", "importTriggers",
    "importMissionObjectives", "importWayPoints", "importDialogs",
    "showAreas", "showUnits", "showTriggers", "showMissionObjectives",
    "showWayPoints", "showDialogs", "excludes",
}
CONDITION_KEYS = (
    "rankRange", "condition", "conditions", "expression", "expressions",
    "enableIf", "disableIf", "requiredVariables", "variables", "selected_tag",
)


def _finite_number(value: Any) -> bool:
    return isinstance(value, (int, float)) and not isinstance(value, bool) and math.isfinite(value)


def _stable_json(value: Any) -> str:
    return json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False, allow_nan=False)


def _sha256_lf(raw: bytes) -> str:
    return hashlib.sha256(raw.replace(b"\r\n", b"\n")).hexdigest()


def _atomic_write(path: Path, data: bytes) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, temporary = tempfile.mkstemp(prefix=path.name + ".", suffix=".tmp", dir=path.parent)
    try:
        with os.fdopen(fd, "wb") as stream:
            stream.write(data)
        os.replace(temporary, path)
    except BaseException:
        try:
            os.unlink(temporary)
        except FileNotFoundError:
            pass
        raise


class Cache:
    def __init__(self, root: Path, workers: int = MAX_WORKERS):
        self.root = root
        self.workers = max(1, min(workers, MAX_WORKERS))
        self._lock = threading.Lock()
        self.bytes_written = 0
        self.downloaded: list[str] = []
        self.cached: list[str] = []

    def read(self, name: str, url: str, limit: int = MAX_FILE_BYTES, api: bool = False) -> bytes:
        safe = PurePosixPath(name)
        if safe.is_absolute() or ".." in safe.parts:
            raise ValueError("unsafe cache path: " + name)
        path = self.root.joinpath(*safe.parts)
        if path.is_file() and 0 < path.stat().st_size <= limit:
            with self._lock:
                self.cached.append(name)
            return path.read_bytes()
        headers = {"User-Agent": "wrpl-airfield-inventory/1"}
        if api:
            headers["Accept"] = "application/vnd.github+json"
            headers["X-GitHub-Api-Version"] = "2022-11-28"
        request = Request(url, headers=headers)
        last_error: Exception | None = None
        for attempt in range(3):
            try:
                with urlopen(request, timeout=20) as response:
                    declared = response.headers.get("Content-Length")
                    if declared and int(declared) > limit:
                        raise ValueError(f"oversize download ({declared} bytes): {name}")
                    raw = response.read(limit + 1)
                break
            except HTTPError as exc:
                if exc.code in (404, 410):
                    raise RuntimeError(f"HTTP {exc.code} for {url}") from exc
                last_error = exc
            except (URLError, TimeoutError, OSError) as exc:
                last_error = exc
            if attempt < 2:
                time.sleep(1 << attempt)
        else:
            raise RuntimeError(f"download failed for {url}: {last_error}") from last_error
        if len(raw) > limit:
            raise ValueError(f"oversize download (> {limit} bytes): {name}")
        # GitHub occasionally serves JSON compressed even without an explicit
        # Accept-Encoding request. Keep transparent support for both forms.
        if raw[:2] == b"\x1f\x8b":
            raw = gzip.decompress(raw)
            if len(raw) > limit:
                raise ValueError(f"oversize decompressed download: {name}")
        _atomic_write(path, raw)
        with self._lock:
            self.bytes_written += len(raw)
            self.downloaded.append(name)
        return raw


def _read_json_bytes(raw: bytes, label: str) -> Any:
    try:
        return json.loads(raw)
    except (UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise ValueError(f"invalid JSON in {label}: {exc}") from exc


def parse_catalog_ids(repo_root: Path) -> list[str]:
    text = (repo_root / "internal/terrainmap/terrainmap.go").read_text(encoding="utf-8")
    match = re.search(r"var knownIDs = strings\.Fields\(`([^`]*)`\)", text, re.S)
    if not match:
        raise ValueError("terrainmap knownIDs catalogue not found")
    ids = match.group(1).split()
    if len(ids) != 69 or len(set(ids)) != 69:
        raise ValueError(f"expected 69 unique terrain catalogue ids, got {len(ids)}")
    return ids


def map_from_level(value: Any, known: set[str]) -> str | None:
    if not isinstance(value, str):
        return None
    base = PurePosixPath(value.replace("\\", "/")).name.lower()
    if base.endswith(".blkx"):
        base = base[:-5]
    elif base.endswith(".bin"):
        base = base[:-4]
    return base if base in known else None


@dataclass
class SourceTree:
    files: dict[str, dict[str, Any]]
    by_folded: dict[str, str]

    @classmethod
    def from_cache(cls, cache: Cache) -> "SourceTree":
        # Each mode subtree is fetched separately so no cache file approaches
        # the repository's much larger 16 MiB recursive root tree response.
        modes = [
            "mis.vromfs.bin_u/gamedata/missions/cta/planes",
            "mis.vromfs.bin_u/gamedata/missions/cta/ffa_race",
            "mis.vromfs.bin_u/gamedata/missions/singlemissions",
            "mis.vromfs.bin_u/gamedata/missions/dynamic_campaign",
            "mis.vromfs.bin_u/gamedata/missions/pve_missions",
            "mis.vromfs.bin_u/gamedata/missions/templates",
            "mis.vromfs.bin_u/gamedata/missions/bridges",
            "mis.vromfs.bin_u/gamedata/missions/training",
        ]
        files: dict[str, dict[str, Any]] = {}
        for mode in modes:
            cache_name = "trees/" + mode.removeprefix("mis.vromfs.bin_u/gamedata/missions/").replace("/", "__") + ".json"
            # The commit root contains one direct child for each of these paths.
            # Resolve the mode's tree SHA through its parent contents listing.
            parts = mode.split("/")
            parent = "/".join(parts[:-1])
            listing_name = "trees/contents__" + parent.replace("/", "__") + ".json"
            listing_url = API_BASE + "contents/" + quote(parent, safe="/") + "?ref=" + COMMIT
            listing = _read_json_bytes(cache.read(listing_name, listing_url, MAX_TREE_BYTES, api=True), listing_name)
            if not isinstance(listing, list):
                raise ValueError("GitHub contents listing is not an array: " + parent)
            entry = next((x for x in listing if x.get("name") == parts[-1]), None)
            if entry is None or not entry.get("sha"):
                raise ValueError("GitHub tree child missing: " + mode)
            tree_url = API_BASE + "git/trees/" + entry["sha"] + "?recursive=1"
            tree = _read_json_bytes(cache.read(cache_name, tree_url, MAX_TREE_BYTES, api=True), cache_name)
            if tree.get("truncated"):
                raise ValueError("GitHub tree response is truncated: " + mode)
            for item in tree.get("tree", []):
                if item.get("type") != "blob":
                    continue
                path = mode + "/" + item["path"]
                files[path] = item

        # Object-group prefabs are a separate small subtree. Discover them on
        # demand later; reserve the paths here for case-insensitive resolution.
        return cls(files=files, by_folded={p.casefold(): p for p in files})

    @classmethod
    def from_source_root(cls, root: Path) -> "SourceTree":
        files: dict[str, dict[str, Any]] = {}
        for mode in (
            "mis.vromfs.bin_u/gamedata/missions/cta/planes",
            "mis.vromfs.bin_u/gamedata/missions/cta/ffa_race",
            "mis.vromfs.bin_u/gamedata/missions/singlemissions",
            "mis.vromfs.bin_u/gamedata/missions/dynamic_campaign",
            "mis.vromfs.bin_u/gamedata/missions/pve_missions",
            "mis.vromfs.bin_u/gamedata/missions/templates",
            "mis.vromfs.bin_u/gamedata/missions/bridges",
            "mis.vromfs.bin_u/gamedata/missions/training",
            "aces.vromfs.bin_u/gamedata/objectgroups",
        ):
            base = root.joinpath(*mode.split("/"))
            if not base.is_dir():
                raise ValueError("source-root subtree missing: " + mode)
            for path in base.rglob("*.blkx"):
                relative = path.relative_to(root).as_posix()
                stat = path.stat()
                files[relative] = {"path": relative, "size": stat.st_size}
        if not files:
            raise ValueError("source-root contains no mission or object-group files")
        return cls(files=files, by_folded={p.casefold(): p for p in files})

    def resolve_import(self, value: str) -> str | None:
        value = value.replace("\\", "/").lstrip("/")
        if value.startswith("mis.vromfs.bin_u/"):
            path = value
        elif value.casefold().startswith("gamedata/missions/"):
            path = "mis.vromfs.bin_u/" + value
        elif value.casefold().startswith("missions/"):
            path = "mis.vromfs.bin_u/gamedata/" + value
        else:
            path = MISSION_PREFIX + value
        if path.lower().endswith(".blk"):
            path += "x"
        elif not path.lower().endswith(".blkx"):
            path += ".blkx"
        return self.by_folded.get(path.casefold())

    def prefab_path(self, prefab: str) -> str:
        name = PurePosixPath(prefab.replace("\\", "/")).name
        if name.lower().endswith(".blkx"):
            name = name[:-5]
        # Object-group filenames are canonical lowercase even when mission
        # unit_class spelling preserves mixed case such as dynAF_sicily_ally.
        return OBJECTGROUP_PREFIX + name.lower() + ".blkx"


def _basename_tokens(map_id: str) -> list[str]:
    # Used only to schedule likely candidates. Acceptance remains exclusively
    # the parsed root mission level.
    aliases = {
        "air_archipelago": ("archipelago",),
        "air_equatorial_island": ("equatorial", "equatorial_island"),
        "air_race_phiphi_islands": ("phiphi", "phiphi_islands"),
        "air_south_eastern_city": ("south_eastern_city", "southeastern_city"),
        "air_southeastern_cliffs": ("southeastern_cliffs", "south_eastern_cliffs"),
        "arcade_africa_canyon": ("africa_canyon",),
        "arcade_africa_seashore": ("africa_seashore", "seashore"),
        "arcade_asia_4roads": ("asia_4roads", "4roads"),
        "arcade_canyon_snow": ("canyon_snow", "snow_canyon"),
        "arcade_mediterranean": ("mediterranean",),
        "arcade_norway_fjords": ("fjords",),
        "arcade_norway_green": ("norway_green",),
        "arcade_norway_plain": ("norway_plain",),
        "arcade_phiphi_crater": ("phiphi_crater",),
        "arcade_phiphi_crater_rocks": ("phiphi_crater_rocks",),
        "arcade_rice_terraces": ("rice_terraces",),
        "arcade_snow_rocks": ("snow_rocks",),
        "arcade_tabletop_mountain": ("tabletop_mountain",),
        "arcade_zhang_park": ("zhang_park",),
        "caribbean_islands": ("caribbean",),
        "dover_strait": ("dover", "dover_strait"),
        "iwo_jima": ("iwo_jima",),
        "khalkhin_gol": ("khalkhin_gol",),
        "sector_montmedy": ("sector_montmedy", "montmedy"),
        "stalingrad_w": ("stalingrad_w",),
        "wake_island": ("wake_island",),
        "avg_egypt_sinai": ("egypt_sinai", "sinai"),
    }
    base = map_id[4:] if map_id.startswith("air_") else map_id
    return list(dict.fromkeys((map_id, base, *aliases.get(map_id, ()))))


def _mission_key(value: str) -> str:
    return re.sub(r"[^a-z0-9]+", "", PurePosixPath(value).stem.casefold())


def candidate_missions(source_tree: SourceTree, ids: list[str], cap: int,
                       replay_missions: Iterable[str] = ()) -> list[str]:
    replay_keys = {_mission_key(value) for value in replay_missions}
    ranked: list[tuple[tuple[int, int, int, int, int, str], str]] = []
    all_rows: list[tuple[tuple[int, int, int, int, int, str], str]] = []
    replay_rows: list[tuple[tuple[int, int, int, int, int, str], str]] = []
    map_rows: dict[str, list[tuple[tuple[int, int, int, int, int, str], str]]] = {map_id: [] for map_id in ids}
    for path, item in source_tree.files.items():
        if not path.endswith(".blkx") or not path.startswith(MISSION_PREFIX):
            continue
        stem = PurePosixPath(path).stem.lower()
        if any(part in stem for part in SKIP_STEM_PARTS):
            continue
        suffix = next((index for index, end in enumerate(ENTRY_SUFFIXES) if stem.endswith(end)), None)
        folded_path = path.casefold()
        matched = [map_id for map_id in ids if any(token in folded_path for token in _basename_tokens(map_id))]
        single_mission = "/singlemissions/" in path
        dynamic_mission = "/dynamic_campaign/" in path
        race_mission = "/cta/ffa_race/" in path
        cta_prefix = "mis.vromfs.bin_u/gamedata/missions/cta/planes/"
        cta_top_level = path.startswith(cta_prefix) and len(path[len(cta_prefix):].split("/")) == 2
        if suffix is None and not single_mission and not dynamic_mission and not race_mission and not cta_top_level:
            continue
        # Historical BFD entries are preferred, then explicitly suffixed
        # entries, then smaller single-mission files. Sorting is deterministic.
        historical = 0 if "/historical/" in path else 1
        suffix_rank = suffix if suffix is not None else len(ENTRY_SUFFIXES)
        alias_rank = 0 if matched else 1
        size = int(item.get("size") or (1 << 62))
        replay = 0 if _mission_key(path) in replay_keys else 1
        plain_bfd = 0 if "/bfd/" in path and stem.endswith("_bfd_norespawn") else 1
        row = ((replay, historical, plain_bfd, suffix_rank, alias_rank, f"{size:09d}", path), path)
        if replay == 0:
            replay_rows.append(row)
        all_rows.append(row)
        for map_id in matched:
            map_rows[map_id].append(row)
    # Keep the discovery request bounded while giving every catalogue id two
    # candidates. Actual replay mission names are always retained and ranked
    # first. Root-level validation still decides every map assignment.
    for rows in map_rows.values():
        rows.sort()
        ranked.extend(rows[:2])
    ranked.extend(replay_rows)
    # Preserve every global candidate as fallback rather than silently
    # discarding non-two-per-map rows when the cap permits.
    ranked.extend(all_rows)
    ranked.sort()
    unique: list[str] = []
    seen: set[str] = set()
    for _, path in ranked:
        if path not in seen:
            seen.add(path)
            unique.append(path)
    return unique[:cap]


def _import_records(container: Any) -> list[dict[str, Any]]:
    if not isinstance(container, dict):
        return []
    value = container.get("import_record")
    if isinstance(value, dict):
        return [value]
    if isinstance(value, list):
        return [record for record in value if isinstance(record, dict)]
    return []


def _condition_from_record(record: dict[str, Any], source: str) -> dict[str, Any] | None:
    selected = {key: record[key] for key in CONDITION_KEYS if key in record}
    extra = {key: value for key, value in record.items() if key not in IMPORT_STANDARD_KEYS and key not in selected}
    if not selected and not extra:
        return None
    result: dict[str, Any] = {"source": source}
    if isinstance(record.get("rankRange"), list) and len(record["rankRange"]) == 2:
        result["rank_range"] = record["rankRange"][:]
    if selected:
        result["record"] = selected
    if extra:
        result["extra"] = extra
    return result


@dataclass
class LoadedNode:
    path: str
    data: dict[str, Any]
    chain: tuple[str, ...]
    conditions: tuple[dict[str, Any], ...]


class MissionLoader:
    def __init__(self, source_tree: SourceTree, cache: Cache, source_root: Path | None):
        self.source_tree = source_tree
        self.cache = cache
        self.source_root = source_root
        self.files_seen: set[str] = set()
        self.json_cache: dict[str, dict[str, Any]] = {}

    def _raw(self, path: str) -> bytes:
        safe = PurePosixPath(path)
        if safe.is_absolute() or ".." in safe.parts:
            raise ValueError("unsafe Datamine path: " + path)
        if self.source_root is not None:
            candidate = self.source_root.joinpath(*safe.parts)
            if candidate.is_file():
                size = candidate.stat().st_size
                if size > MAX_FILE_BYTES:
                    raise ValueError(f"oversize source file ({size} bytes): {path}")
                return candidate.read_bytes()
        return self.cache.read("files/" + path, RAW_BASE + quote(path, safe="/"), MAX_FILE_BYTES)

    def load(self, path: str) -> dict[str, Any]:
        if path in self.json_cache:
            return self.json_cache[path]
        if len(self.files_seen) >= MAX_SOURCE_FILES:
            raise ValueError(f"source file cap {MAX_SOURCE_FILES} reached")
        raw = self._raw(path)
        data = _read_json_bytes(raw, path)
        if not isinstance(data, dict):
            raise ValueError("mission source is not an object: " + path)
        self.files_seen.add(path)
        self.json_cache[path] = data
        return data

    def closure(self, root: str) -> list[LoadedNode]:
        root_data = self.load(root)
        result: list[LoadedNode] = []
        visiting: set[str] = set()

        def visit(path: str, chain: tuple[str, ...], conditions: tuple[dict[str, Any], ...], depth: int) -> None:
            if depth > MAX_DEPTH or path in visiting:
                return
            visiting.add(path)
            data = self.load(path)
            result.append(LoadedNode(path, data, chain, conditions))
            for record in _import_records(data.get("imports")):
                if record.get("importUnits") is False:
                    continue
                file_value = record.get("file")
                if not isinstance(file_value, str) or not file_value.strip():
                    continue
                imported = self.source_tree.resolve_import(file_value)
                if imported is None:
                    continue
                condition = _condition_from_record(record, path)
                next_conditions = conditions + ((condition,) if condition is not None else ())
                visit(imported, chain + (imported,), next_conditions, depth + 1)
            visiting.remove(path)

        visit(root, (root,), (), 0)
        return result


def _rank_range(conditions: Iterable[dict[str, Any]]) -> list[int] | None:
    ranges = [x["rank_range"] for x in conditions if isinstance(x.get("rank_range"), list) and len(x["rank_range"]) == 2]
    if not ranges:
        return None
    low = max(int(x[0]) for x in ranges)
    high = min(int(x[1]) for x in ranges)
    return [low, high] if low <= high else None


def _condition_fields(node: LoadedNode) -> dict[str, Any]:
    return {
        "conditional": bool(node.conditions),
        "rank_range": _rank_range(node.conditions),
        "conditions": [dict(x) for x in node.conditions] if node.conditions else [],
    }


def _trigger_condition(node: LoadedNode, trigger_name: str, trigger: dict[str, Any]) -> dict[str, Any] | None:
    conditions = trigger.get("conditions")
    events = trigger.get("events")
    props = trigger.get("props") if isinstance(trigger.get("props"), dict) else {}
    enabled = trigger.get("is_enabled", True)
    begin_event = isinstance(events, dict) and "initMission" in events
    conditional = bool(conditions) or not enabled or not begin_event
    if not conditional:
        return None
    return {
        "source": node.path + "#triggers/" + trigger_name,
        "trigger": trigger_name,
        "trigger_conditions": conditions if isinstance(conditions, (dict, list)) else {},
        "events": events if isinstance(events, (dict, list)) else {},
        "is_enabled": bool(enabled),
        "conditions_type": props.get("conditionsType", "ALL"),
    }


def _valid_tm(value: Any) -> bool:
    return (isinstance(value, list) and len(value) == 4
            and all(isinstance(row, list) and len(row) == 3 and all(_finite_number(v) for v in row) for row in value))


def _unit_items(container: Any) -> Iterable[dict[str, Any]]:
    if isinstance(container, dict):
        for value in container.values():
            yield from _unit_items(value)
    elif isinstance(container, list):
        for value in container:
            if isinstance(value, dict):
                if isinstance(value.get("unit_class"), str) and _valid_tm(value.get("tm")):
                    yield value
                else:
                    yield from _unit_items(value)


def _looks_like_airfield_prefab(prefab: Any) -> bool:
    if not isinstance(prefab, str):
        return False
    name = PurePosixPath(prefab.replace("\\", "/")).name.casefold()
    return name.startswith(("dynaf_", "airfield", "runway"))


def _area_point(node: LoadedNode, name: str, areas: dict[str, Any]) -> list[float] | None:
    area = areas.get(name)
    if not isinstance(area, dict) or not _valid_tm(area.get("tm")):
        return None
    point = area["tm"][3]
    return [float(point[0]), float(point[1]), float(point[2])]


NAMED_ENDPOINT = re.compile(r"^(airfield|runway)_(start|end)_([A-Za-z0-9_.-]+)$", re.I)


def _named_runway_pairs(node: LoadedNode) -> Iterable[tuple[str, str, str, dict[str, Any]]]:
    """Yield exact named start/end pairs from this owner's own areas only."""
    areas = node.data.get("areas")
    if not isinstance(areas, dict):
        return
    groups: dict[tuple[str, str], dict[str, Any]] = {}
    for name, area in areas.items():
        match = NAMED_ENDPOINT.match(name)
        if match is None or not isinstance(area, dict) or not _valid_tm(area.get("tm")):
            continue
        family, side, suffix = match.group(1).casefold(), match.group(2).casefold(), match.group(3)
        groups.setdefault((family, suffix), {})[side] = (name, area)
    for (family, suffix), sides in sorted(groups.items()):
        if "start" not in sides or "end" not in sides:
            continue
        start_name, start_area = sides["start"]
        end_name, end_area = sides["end"]
        start, end = start_area["tm"][3], end_area["tm"][3]
        length = math.dist(start, end)
        if not math.isfinite(length) or length <= 0 or length > 50_000:
            continue
        yield start_name, end_name, family + "_" + suffix, end_area


def _addairfield_actions(value: Any, path: str = "") -> Iterable[tuple[str, dict[str, Any]]]:
    if isinstance(value, dict):
        for key, child in value.items():
            child_path = path + "/" + str(key)
            if key == "addAirfield":
                if isinstance(child, dict):
                    yield child_path, child
                elif isinstance(child, list):
                    for index, action in enumerate(child):
                        if isinstance(action, dict):
                            yield child_path + "/" + str(index), action
            else:
                yield from _addairfield_actions(child, child_path)
    elif isinstance(value, list):
        for index, child in enumerate(value):
            yield from _addairfield_actions(child, path + "/" + str(index))


class PrefabCatalog:
    def __init__(self, loader: MissionLoader):
        self.loader = loader
        self.cache: dict[str, dict[str, Any] | None] = {}
        self.files = 0

    def geometry(self, prefab: str) -> dict[str, Any] | None:
        if prefab in self.cache:
            return self.cache[prefab]
        if self.files >= MAX_PREFAB_FILES:
            return None
        path = self.loader.source_tree.prefab_path(prefab)
        self.files += 1
        try:
            data = self.loader.load(path)
        except (RuntimeError, ValueError, OSError):
            self.cache[prefab] = None
            return None
        value = data.get("airfield")
        if not isinstance(value, dict):
            result = None
        else:
            start, end, width = value.get("start"), value.get("end"), value.get("width")
            if (isinstance(start, list) and len(start) == 3 and all(_finite_number(x) for x in start)
                    and isinstance(end, list) and len(end) == 3 and all(_finite_number(x) for x in end)
                    and _finite_number(width) and width >= 0):
                result = {"start": [float(x) for x in start], "end": [float(x) for x in end], "width": float(width)}
            else:
                result = None
        self.cache[prefab] = result
        return result


def _prefab_to_world(tm: list[list[float]], local: list[float]) -> list[float]:
    return [
        tm[3][axis] + sum(tm[row][axis] * local[row] for row in range(3))
        for axis in range(3)
    ]


def process_mission(path: str, loader: MissionLoader, prefabs: PrefabCatalog, known: set[str]) -> tuple[str, dict[str, Any]] | None:
    root = loader.load(path)
    # Deliberately use the root mission only. Imported helper level values are
    # often placeholders and are not map evidence.
    map_id = map_from_level(((root.get("mission_settings") or {}).get("mission") or {}).get("level"), known)
    if map_id is None:
        return None
    nodes = loader.closure(path)
    placements: list[dict[str, Any]] = []
    direct: list[dict[str, Any]] = []
    unresolved: list[dict[str, Any]] = []
    seen_placement: set[tuple[Any, ...]] = set()
    seen_direct: set[tuple[Any, ...]] = set()
    areas: dict[str, Any] = {}
    for node in nodes:
        if isinstance(node.data.get("areas"), dict):
            areas.update(node.data["areas"])

    for node in nodes:
        units = node.data.get("units")
        for unit in _unit_items(units):
            prefab = unit.get("unit_class")
            if not _looks_like_airfield_prefab(prefab):
                continue
            geometry = prefabs.geometry(prefab)
            if geometry is None:
                unresolved.append({"name": unit.get("name", ""), "prefab": prefab,
                                   "reason": "airfield prefab geometry unavailable",
                                   "source": node.path, "source_chain": list(node.chain)})
                continue
            tm = unit["tm"]
            name = unit.get("name", "")
            props = unit.get("props") if isinstance(unit.get("props"), dict) else {}
            team = props.get("army", unit.get("army"))
            if not isinstance(team, int) or isinstance(team, bool):
                unresolved.append({"name": name, "prefab": prefab, "reason": "missing integer team",
                                   "source": node.path, "source_chain": list(node.chain)})
                continue
            fields = _condition_fields(node)
            record = {
                "name": name,
                "team": team,
                "prefab": prefab,
                "tm": [[float(v) for v in row] for row in tm],
                "source": node.path,
                "source_chain": list(node.chain),
                **fields,
            }
            key = (_stable_json(record["tm"]), prefab, name, team, node.path,
                   record["conditional"], _stable_json(record["rank_range"]))
            if key not in seen_placement:
                seen_placement.add(key)
                placements.append(record)

    for node in nodes:
        if not isinstance(node.data.get("triggers"), (dict, list)):
            continue
        for trigger_name, trigger in node.data["triggers"].items() if isinstance(node.data["triggers"], dict) else ():
            if not isinstance(trigger, dict):
                continue
            actions_value = trigger.get("actions")
            if actions_value is None:
                continue
            for action_path, action in _addairfield_actions({"actions": actions_value}):
                start_name, end_name = action.get("runwayStart"), action.get("runwayEnd")
                if isinstance(start_name, list) and isinstance(end_name, list):
                    start, end = start_name, end_name
                elif isinstance(start_name, str) and isinstance(end_name, str):
                    start, end = _area_point(node, start_name, areas), _area_point(node, end_name, areas)
                else:
                    start = end = None
                width = action.get("runwayWidth")
                team = action.get("army", action.get("team"))
                name = action.get("name")
                if not isinstance(name, str):
                    name = start_name if isinstance(start_name, str) else "addAirfield"
                if (not (isinstance(start, list) and len(start) == 3 and all(_finite_number(x) for x in start))
                        or not (isinstance(end, list) and len(end) == 3 and all(_finite_number(x) for x in end))
                        or not _finite_number(width) or not isinstance(team, int) or isinstance(team, bool)):
                    unresolved.append({"name": name, "kind": "addAirfield", "reason": "runway endpoints/area/team/width incomplete",
                                       "source": node.path, "source_chain": list(node.chain)})
                    continue
                fields = _condition_fields(node)
                trigger_condition = _trigger_condition(node, trigger_name, trigger)
                if trigger_condition is not None:
                    fields["conditional"] = True
                    fields["conditions"] = fields["conditions"] + [trigger_condition]
                record = {
                    "name": name,
                    "team": team,
                    "start": [float(x) for x in start],
                    "end": [float(x) for x in end],
                    "width": float(width),
                    "source": node.path + "#triggers/" + trigger_name + action_path,
                    "source_chain": list(node.chain),
                    **fields,
                }
                key = (_stable_json(record["start"]), _stable_json(record["end"]), record["width"], name, team,
                       record["conditional"], _stable_json(record["rank_range"]))
                if key not in seen_direct:
                    seen_direct.add(key)
                    direct.append(record)

    for node in nodes:
        for start_name, end_name, runway_name, end_area in _named_runway_pairs(node):
            start = _area_point(node, start_name, node.data["areas"])
            end = _area_point(node, end_name, node.data["areas"])
            if start is None or end is None:
                continue
            duplicate = any(math.dist(x["start"], start) <= .5 and math.dist(x["end"], end) <= .5
                            for x in direct if x.get("width_known", True))
            if not duplicate:
                for placement in placements:
                    geometry = prefabs.geometry(placement["prefab"])
                    if geometry is None:
                        continue
                    ps = _prefab_to_world(placement["tm"], geometry["start"])
                    pe = _prefab_to_world(placement["tm"], geometry["end"])
                    if math.dist(ps, start) <= .5 and math.dist(pe, end) <= .5:
                        duplicate = True
                        break
            if duplicate:
                continue
            props = end_area.get("props") if isinstance(end_area.get("props"), dict) else {}
            team = props.get("army", 0)
            if not isinstance(team, int) or isinstance(team, bool):
                team = 0
            fields = _condition_fields(node)
            record = {
                "name": runway_name,
                "team": team,
                "start": start,
                "end": end,
                "width": 0.0,
                "width_known": False,
                "geometry_kind": "NamedRunwayEndpointAreas",
                "source": node.path + "#/areas/" + start_name + "," + end_name,
                "source_chain": list(node.chain),
                **fields,
            }
            key = (_stable_json(record["start"]), _stable_json(record["end"]), runway_name, team,
                   record["conditional"], _stable_json(record["rank_range"]))
            if key not in seen_direct:
                seen_direct.add(key)
                direct.append(record)

    placements.sort(key=lambda x: (x["name"], x["team"], x["prefab"], _stable_json(x["tm"]), x["source"]))
    direct.sort(key=lambda x: (x["name"], x["team"], _stable_json(x["start"]), _stable_json(x["end"]), x["source"]))
    unresolved.sort(key=_stable_json)
    if not placements and not direct and not unresolved:
        reason = ("RaceAirStartNoLandRunway: no prefab airfield group, addAirfield action, or exact named runway endpoint areas"
                  if "/cta/ffa_race/" in path else
                  "matching root level but no source-backed airfield prefab, addAirfield action, or exact named runway endpoint areas")
        unresolved.append({"kind": "mission", "reason": reason,
                           "source": path, "source_chain": [path]})
    mission = {
        "mission": PurePosixPath(path).stem,
        "level": "levels/" + map_id + ".bin",
        "source": path,
        "placements": placements,
        "direct_runways": direct,
        "unresolved": unresolved,
    }
    return map_id, mission


def validate_result(result: dict[str, Any], ids: list[str]) -> None:
    if result.get("schema") != 1 or result.get("commit") != COMMIT:
        raise ValueError("invalid schema/commit")
    maps = result.get("maps")
    if not isinstance(maps, list) or [x.get("map_id") for x in maps] != ids:
        raise ValueError("maps must contain the 69 catalogue ids in catalogue order")
    for item in maps:
        map_id = item["map_id"]
        for mission in item["missions"]:
            if mission.get("level") != "levels/" + map_id + ".bin":
                raise ValueError("mission level/map mismatch: " + mission.get("source", "?"))
            for placement in mission["placements"]:
                if not isinstance(placement.get("conditional"), bool) or "rank_range" not in placement:
                    raise ValueError("placement conditional metadata missing")
                if not _valid_tm(placement.get("tm")) or not placement.get("source_chain"):
                    raise ValueError("placement transform/source chain incomplete")
            for runway in mission["direct_runways"]:
                if len(runway.get("start", [])) != 3 or len(runway.get("end", [])) != 3:
                    raise ValueError("direct runway endpoints incomplete")
                if not isinstance(runway.get("conditional"), bool):
                    raise ValueError("direct runway conditional flag missing")


def prefetch_closures(paths: list[str], source_tree: SourceTree, cache: Cache,
                      loader: MissionLoader) -> tuple[int, list[str]]:
    """Fetch the reachable import graph in bounded parallel waves.

    Downloading recursively while processing one mission makes unrelated large
    unit templates serialize the whole inventory. This prefetch keeps at most
    four requests in flight and still follows the same importUnits semantics.
    """
    seen = set(paths)
    pending = list(paths)
    missing: list[str] = []
    depth = 0
    while pending and len(seen) <= MAX_SOURCE_FILES:
        depth += 1
        batch = pending[:MAX_SOURCE_FILES - (len(seen) - len(pending))]
        pending = pending[len(batch):]
        with concurrent.futures.ThreadPoolExecutor(max_workers=cache.workers) as pool:
            results = list(pool.map(lambda path: (path, _try_raw(loader, path)), batch))
        before = len(seen)
        for path, raw in results:
            if raw is None:
                continue
            try:
                data = _read_json_bytes(raw, path)
            except ValueError:
                continue
            if not isinstance(data, dict):
                continue
            for record in _import_records(data.get("imports")):
                if record.get("importUnits") is False:
                    continue
                value = record.get("file")
                if not isinstance(value, str) or not value.strip():
                    continue
                resolved = source_tree.resolve_import(value)
                if resolved is None:
                    missing.append(path + " -> " + value)
                elif resolved not in seen:
                    seen.add(resolved)
                    pending.append(resolved)
        print(f"closure wave={depth} files={len(seen)} missing={len(missing)}",
              file=sys.stderr, flush=True)
        if depth >= MAX_DEPTH + 1:
            break
    return len(seen), missing


def _try_raw(loader: MissionLoader, path: str) -> bytes | None:
    try:
        return loader._raw(path)
    except (RuntimeError, ValueError, OSError) as exc:
        print(f"skip unavailable source {path}: {exc}", file=sys.stderr, flush=True)
        return None


def build(repo_root: Path, output: Path, cache_root: Path, source_root: Path | None,
          workers: int, mission_cap: int, replay_samples: Path | None = None) -> dict[str, Any]:
    ids = parse_catalog_ids(repo_root)
    known = set(ids)
    cache = Cache(cache_root, workers)
    source_tree = SourceTree.from_source_root(source_root) if source_root is not None else SourceTree.from_cache(cache)
    replay_missions: list[str] = []
    if replay_samples is not None and replay_samples.is_file():
        replay_data = json.loads(replay_samples.read_text(encoding="utf-8"))
        if not isinstance(replay_data, list):
            raise ValueError("replay samples must be a JSON array")
        for sample in replay_data:
            value = sample.get("mission") if isinstance(sample, dict) else None
            if isinstance(value, str) and value.strip():
                replay_missions.append(value.strip())
    candidates = candidate_missions(source_tree, ids, mission_cap, replay_missions)
    print(f"catalogue maps={len(ids)} candidates={len(candidates)} replay_missions={len(replay_missions)}",
          file=sys.stderr, flush=True)
    loader = MissionLoader(source_tree, cache, source_root)
    prefabs = PrefabCatalog(loader)

    if source_root is None:
        closure_files, missing_imports = prefetch_closures(candidates, source_tree, cache, loader)
        print(f"prefetched import closure files={closure_files} missing={len(missing_imports)}",
              file=sys.stderr, flush=True)
    else:
        missing_imports = []

    accepted: dict[str, dict[str, Any]] = {map_id: {"map_id": map_id, "missions": []} for map_id in ids}
    rejected: list[dict[str, str]] = []
    for index, path in enumerate(candidates, 1):
        print(f"scan {index}/{len(candidates)} {path}", file=sys.stderr, flush=True)
        try:
            value = process_mission(path, loader, prefabs, known)
        except (RuntimeError, ValueError, OSError) as exc:
            rejected.append({"source": path, "reason": str(exc)})
            continue
        if value is None:
            continue
        map_id, mission = value
        accepted[map_id]["missions"].append(mission)

    for item in accepted.values():
        item["missions"].sort(key=lambda x: x["source"].casefold())
        has_geometry = any(m["placements"] or m["direct_runways"] for m in item["missions"])
        if has_geometry:
            item["status"] = "supported"
            item["reason"] = "matching root mission with source-backed airfield geometry"
        elif item["missions"] and all(
                any(str(u.get("reason", "")).startswith("RaceAirStartNoLandRunway") for u in m["unresolved"])
                for m in item["missions"]):
            item["status"] = "no_runway"
            item["reason"] = f"{len(item['missions'])} matching race root missions; source defines air starts with no land-runway geometry"
        elif item["missions"]:
            item["status"] = "partial"
            item["reason"] = "matching root mission found; no land-runway geometry was established"
        else:
            if item["map_id"] == "caribbean_islands":
                item["status"] = "unknown"
                item["reason"] = ("Unknown: a bounded CTA ship/event-name check found no mission with root level "
                                  "caribbean_islands; the terrain level exists, but no mission source establishes a land "
                                  "runway or carrier-only operation")
            else:
                item["status"] = "no_matching_root"
                item["reason"] = ("NoMatchingRootMission: no candidate entry mission in the bounded scan has this root level; "
                                  "runway presence is not asserted")
    maps = list(accepted.values())
    with_geometry = [x for x in maps if x["missions"]]
    with_prefabs = [x for x in maps if any(m["placements"] for m in x["missions"])]
    with_direct = [x for x in maps if any(m["direct_runways"] for m in x["missions"])]
    unsupported = []
    for item in maps:
        if item["status"] != "supported":
            unsupported.append({"map_id": item["map_id"], "status": item["status"], "reason": item["reason"]})
    placements = sum(len(m["placements"]) for item in maps for m in item["missions"])
    direct = sum(len(m["direct_runways"]) for item in maps for m in item["missions"])
    conditional = sum(p["conditional"] for item in maps for m in item["missions"] for p in m["placements"])
    unresolved = sum(len(m["unresolved"]) for item in maps for m in item["missions"])
    result = {
        "schema": 1,
        "commit": COMMIT,
        "maps": maps,
        "coverage": {
            "catalog_maps": len(ids),
            "maps_with_missions": len(with_geometry),
            "maps_with_prefab_placements": len(with_prefabs),
            "maps_with_direct_runways": len(with_direct),
            "missions": sum(len(x["missions"]) for x in maps),
            "placements": placements,
            "conditional_placements": conditional,
            "direct_runways": direct,
            "unresolved": unresolved,
            "candidate_missions": len(candidates),
            "replay_missions_prioritized": len(replay_missions),
            "source_files_scanned": len(loader.files_seen),
            "missing_imports": missing_imports[:100],
            "prefab_files_read": prefabs.files,
            "downloads": len(cache.downloaded),
            "download_bytes": cache.bytes_written,
            "rejected_candidates": rejected,
            "unsupported": unsupported,
        },
    }
    validate_result(result, ids)
    return result


def write_result(result: dict[str, Any], output: Path) -> None:
    payload = json.dumps(result, indent=2, ensure_ascii=False, allow_nan=False) + "\n"
    _atomic_write(output, payload.encode("utf-8"))


def main(argv: list[str] | None = None) -> int:
    default_repo = Path(__file__).resolve().parents[2]
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repo-root", type=Path, default=default_repo)
    parser.add_argument("--output", type=Path, default=default_repo.parent / "airfields-all/missions.json")
    parser.add_argument("--cache-dir", "--cache", dest="cache_dir", type=Path,
                        default=default_repo.parent / "airfields-all/mission-sources")
    parser.add_argument("--source-root", type=Path, help="pinned War-Thunder-Datamine checkout")
    parser.add_argument("--workers", type=int, default=MAX_WORKERS)
    parser.add_argument("--max-missions", type=int, default=1000)
    parser.add_argument("--replay-samples", type=Path,
                        default=default_repo.parent / "airfields-all/replay-samples.json",
                        help="optional replay header JSON used to rank likely missions")
    parser.add_argument("--check", action="store_true", help="validate an existing output without downloading")
    args = parser.parse_args(argv)
    if args.check:
        result = json.loads(args.output.read_text(encoding="utf-8"))
        validate_result(result, parse_catalog_ids(args.repo_root))
        print(f"PASS: {len(result['maps'])} maps, {result['coverage']['placements']} placements")
        return 0
    if args.workers < 1 or args.workers > MAX_WORKERS:
        parser.error(f"--workers must be 1..{MAX_WORKERS}")
    result = build(args.repo_root, args.output, args.cache_dir, args.source_root, args.workers,
                   args.max_missions, args.replay_samples)
    write_result(result, args.output)
    coverage = result["coverage"]
    print(
        f"Wrote {coverage['maps_with_missions']}/{coverage['catalog_maps']} maps, "
        f"{coverage['missions']} missions, {coverage['placements']} prefab placements, "
        f"{coverage['direct_runways']} direct runways to {args.output}"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

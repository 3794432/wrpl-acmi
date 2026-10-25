#!/usr/bin/env python3
"""Build the compact aircraft-sensor table embedded by cmd/acmi/radar_modes.go.

The upstream Datamine stores aircraft sensor lists in flight-model JSON files and
the referenced sensors in a sibling directory. This script preserves those key
orders because replay kind-1 records index the ordered transceiver, scan-pattern
and signal blocks.
"""

from __future__ import annotations

import argparse
import concurrent.futures
import json
import os
from pathlib import Path
import sys
import time
import urllib.error
import urllib.request


COMMIT = "f7952e4291e9fd7b6fe38e84020dddef925c3457"
REPOSITORY = "gszabi99/War-Thunder-Datamine"
FLIGHTMODELS_TREE = "298a56c0cd83f9b11f6ee3d6e7ba53fc3c98dd25"
SENSORS_TREE = "68bede0daa581d1d211f2b5dc3418a29a70dd7b4"
FLIGHTMODELS_PREFIX = "aces.vromfs.bin_u/gamedata/flightmodels/"
SENSORS_PREFIX = "aces.vromfs.bin_u/gamedata/sensors/"
SCHEMA = 1


def request_bytes(url: str, attempts: int = 4) -> bytes:
    req = urllib.request.Request(url, headers={"User-Agent": "wrpl-radar-modes-builder"})
    for attempt in range(attempts):
        try:
            with urllib.request.urlopen(req, timeout=60) as response:
                return response.read()
        except urllib.error.HTTPError as exc:
            if exc.code not in (429, 500, 502, 503, 504) or attempt + 1 == attempts:
                raise
        except (TimeoutError, urllib.error.URLError):
            if attempt + 1 == attempts:
                raise
        time.sleep(0.5 * (2**attempt))
    raise AssertionError("unreachable")


def request_json(url: str):
    return json.loads(request_bytes(url))


def tree_files(tree_sha: str, prefix: str) -> list[str]:
    url = f"https://api.github.com/repos/{REPOSITORY}/git/trees/{tree_sha}?recursive=1"
    data = request_json(url)
    if data.get("truncated"):
        raise RuntimeError(f"upstream tree {tree_sha} was truncated")
    # Aircraft and sensor definitions are top-level .blkx files. The recursive
    # trees also contain thousands of unrelated FM, damage and preset blobs.
    return [
        entry["path"]
        for entry in data["tree"]
        if entry.get("type") == "blob"
        and "/" not in entry["path"]
        and entry["path"].endswith(".blkx")
    ]


def raw_url(path: str) -> str:
    return f"https://raw.githubusercontent.com/{REPOSITORY}/{COMMIT}/{path}"


def raw_fallback_url(path: str) -> str:
    return f"https://cdn.jsdelivr.net/gh/{REPOSITORY}@{COMMIT}/{path}"


def request_raw(path: str) -> bytes:
    try:
        return request_bytes(raw_url(path), attempts=2)
    except (urllib.error.HTTPError, urllib.error.URLError, TimeoutError):
        return request_bytes(raw_fallback_url(path), attempts=3)


def cached_raw(cache: Path, path: str) -> dict:
    target = cache / path
    if not target.exists():
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(request_raw(path))
    with target.open("r", encoding="utf-8") as handle:
        value = json.load(handle)
    if not isinstance(value, dict):
        raise ValueError(f"{path}: expected a JSON object")
    return value


def download_missing(cache: Path, paths: list[str], workers: int) -> None:
    missing = [path for path in paths if not (cache / path).exists()]
    if not missing:
        return
    cache.mkdir(parents=True, exist_ok=True)

    def fetch(path: str) -> tuple[str, bytes]:
        return path, request_raw(path)

    failures: list[str] = []
    with concurrent.futures.ThreadPoolExecutor(max_workers=workers) as pool:
        futures = {pool.submit(fetch, path): path for path in missing}
        for completed, future in enumerate(concurrent.futures.as_completed(futures), 1):
            path = futures[future]
            try:
                _, body = future.result()
                target = cache / path
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_bytes(body)
            except Exception as exc:  # print all failures, then fail atomically
                failures.append(f"{path}: {exc}")
            if completed % 200 == 0 or completed == len(futures):
                print(f"downloaded {completed}/{len(futures)}", file=sys.stderr)
    if failures:
        raise RuntimeError("download failures:\n" + "\n".join(failures[:20]))


def sensor_reference(spec: dict) -> str | None:
    value = spec.get("blk") if isinstance(spec, dict) else None
    if not isinstance(value, str) or not value.strip():
        return None
    normalized = value.replace("\\", "/").strip()
    marker = "gamedata/sensors/"
    at = normalized.lower().find(marker)
    if at < 0:
        return None
    return normalized[at + len(marker) :]


def sensor_source_name(reference: str) -> str:
    name = reference.rsplit("/", 1)[-1]
    if name.lower().endswith(".blk"):
        return name[:-4] + ".blkx"
    if not name.lower().endswith(".blkx"):
        return name + ".blkx"
    return name


def transceiver_record(key: str, value: dict) -> dict:
    record = {
        "id": key,
        "visibility_type": value.get("visibilityType") or "",
        "active": value.get("active", True) is not False,
    }
    return record


def scan_pattern_record(key: str, value: dict) -> dict:
    raw_type = value.get("type")
    if isinstance(raw_type, str) and raw_type.strip():
        types = [raw_type]
    elif isinstance(raw_type, list):
        types = [item for item in raw_type if isinstance(item, str) and item.strip()]
    else:
        types = []
    record = {"id": key, "types": types}
    width = value.get("width")
    height = value.get("barHeight")
    if isinstance(width, (int, float)) and not isinstance(width, bool):
        record["width_deg"] = float(width)
    if isinstance(height, (int, float)) and not isinstance(height, bool):
        record["bar_height_deg"] = float(height)
    return record


def compact_sensor(path: str, data: dict) -> dict:
    transceivers = data.get("transivers") or {}
    patterns = data.get("scanPatterns") or {}
    signals = data.get("signals") or {}
    if not isinstance(transceivers, dict):
        transceivers = {}
    if not isinstance(patterns, dict):
        patterns = {}
    if not isinstance(signals, dict):
        signals = {}
    return {
        "path": path,
        "name": data.get("name") if isinstance(data.get("name"), str) else "",
        "type": data.get("type") if isinstance(data.get("type"), str) else "",
        "transceivers": [
            transceiver_record(key, value if isinstance(value, dict) else {})
            for key, value in transceivers.items()
        ],
        "scan_patterns": [
            scan_pattern_record(key, value if isinstance(value, dict) else {})
            for key, value in patterns.items()
        ],
        "signals": list(signals.keys()),
    }


def build(cache: Path, workers: int) -> dict:
    flight_paths = [FLIGHTMODELS_PREFIX + p for p in tree_files(FLIGHTMODELS_TREE, FLIGHTMODELS_PREFIX)]
    sensor_paths = [SENSORS_PREFIX + p for p in tree_files(SENSORS_TREE, SENSORS_PREFIX)]
    download_missing(cache, flight_paths, workers)

    configs: list[dict] = []
    referenced: set[str] = set()
    for path in flight_paths:
        data = cached_raw(cache, path)
        sensors = data.get("sensors")
        if not isinstance(sensors, dict):
            continue
        entries = sensors.get("sensor")
        if isinstance(entries, dict):
            entries = [entries]
        if not isinstance(entries, list):
            continue
        refs: list[str] = []
        for entry in entries:
            ref = sensor_reference(entry)
            if ref is None:
                continue
            refs.append(ref)
            referenced.add(sensor_source_name(ref))
        if refs:
            model = data.get("model")
            if not isinstance(model, str) or not model.strip():
                model = path.rsplit("/", 1)[-1].removesuffix(".blkx")
            configs.append(
                {
                    "model": model,
                    "stem": path.rsplit("/", 1)[-1].removesuffix(".blkx"),
                    "refs": refs,
                    "source": path,
                }
            )

    # Source references use .blk names and are not consistently case-matched
    # (for example LaserDesignatorSensor vs laserdesignatorsensor).
    sensor_by_name = {path.rsplit("/", 1)[-1].lower(): path for path in sensor_paths}
    missing_sources = sorted(name for name in referenced if name.lower() not in sensor_by_name)
    if missing_sources:
        raise RuntimeError("referenced sensor sources not found: " + ", ".join(missing_sources[:20]))

    canonical_configs: list[dict] = []
    canonical_names: set[str] = set()
    for config in configs:
        canonical = [
            sensor_by_name[sensor_source_name(ref).lower()].rsplit("/", 1)[-1]
            for ref in config["refs"]
        ]
        canonical_configs.append({**config, "refs": canonical})
        canonical_names.update(canonical)
    selected_sensor_paths = [sensor_by_name[name.lower()] for name in sorted(canonical_names)]
    download_missing(cache, selected_sensor_paths, workers)

    sensors = {}
    for source_path in selected_sensor_paths:
        name = source_path.rsplit("/", 1)[-1]
        sensors[name] = compact_sensor(source_path, cached_raw(cache, source_path))

    # The static `model` field and source filename can disagree for a few
    # variants. Include both spellings only when they resolve unambiguously;
    # omit conflicting aliases instead of guessing which variant a replay meant.
    alias_candidates: dict[str, dict[tuple[str, ...], set[str]]] = {}
    for config in canonical_configs:
        refs = tuple(config["refs"])
        for root in (config["model"], config["stem"]):
            for alias in (root, root.replace("_", "-"), root.replace("-", "_")):
                alias = alias.strip().lower()
                if alias:
                    alias_candidates.setdefault(alias, {}).setdefault(refs, set()).add(config["source"])

    aliases: dict[str, list[str]] = {}
    ambiguous_aliases: dict[str, list[dict]] = {}
    for alias, choices in alias_candidates.items():
        if len(choices) == 1:
            refs = next(iter(choices))
            aliases[alias] = list(refs)
        else:
            ambiguous_aliases[alias] = [
                {"sensors": list(refs), "sources": sorted(sources)}
                for refs, sources in sorted(choices.items(), key=lambda item: item[0])
            ]

    return {
        "schema": SCHEMA,
        "source": {
            "repository": REPOSITORY,
            "commit": COMMIT,
            "flightmodels_tree": FLIGHTMODELS_TREE,
            "sensors_tree": SENSORS_TREE,
            "flightmodels_path": FLIGHTMODELS_PREFIX.rstrip("/"),
            "sensors_path": SENSORS_PREFIX.rstrip("/"),
            "generator": "tools/radar-modes/build_radar_modes.py",
            "note": (
                "Sensor lists preserve the datamine list order, including RWR placeholders; "
                "ambiguous model aliases are omitted and listed separately."
            ),
        },
        "ambiguous_aliases": dict(sorted(ambiguous_aliases.items())),
        "models": dict(sorted(aliases.items())),
        "sensors": dict(sorted(sensors.items())),
    }


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", type=Path, default=Path("cmd/acmi/data/radar_modes.json"))
    parser.add_argument("--cache", type=Path, default=Path("tools/radar-modes/.cache"))
    parser.add_argument("--workers", type=int, default=12)
    args = parser.parse_args()
    if args.workers < 1 or args.workers > 32:
        parser.error("--workers must be between 1 and 32")

    result = build(args.cache, args.workers)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    encoded = json.dumps(result, ensure_ascii=False, separators=(",", ":"), sort_keys=False) + "\n"
    temp = args.output.with_suffix(args.output.suffix + ".tmp")
    temp.write_text(encoded, encoding="utf-8")
    os.replace(temp, args.output)
    print(
        f"wrote {args.output}: {len(result['models'])} model aliases, "
        f"{len(result['sensors'])} sensors, {len(encoded)} bytes",
        file=sys.stderr,
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

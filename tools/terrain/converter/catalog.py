#!/usr/bin/env python3
"""Build a bounded, read-only catalog of War Thunder level data.

The level .bin files are never read in full.  Only the top-level block records
and the small HM2/lmap headers needed by the terrain exporter are inspected.
"""

from __future__ import annotations

import argparse
import json
import math
import os
import re
import struct
from pathlib import Path
from typing import Any, Iterator

from targets import TargetReferenceError, load_reference_ids


LEVEL_REF_RE = re.compile(r'"level"\s*:\s*"(levels/[^"\\]+)"')
MAX_CONFIG_BYTES = 2 * 1024**2
MODERN_NON_AIR_PREFIXES = ("avg_", "avn_", "hvg_")
TRAINING_OR_BLANK_IDS = {"firing_range", "water"}
OBSOLETE_ALIASES = {
    "air_afghan_ec": {
        "alias_of": "air_afghan",
        "parent": "cta/planes/historical/bfd_ec/air_afghan_ec_bfd_norespawn.blkx:81",
    },
    "spain_ec": {
        "alias_of": "spain",
        "parent": "cta/planes/historical/bfd_ec/spain_ec_bfd_norespawn.blkx:81",
    },
}


def _iter_blocks(handle: Any, size: int, limit: int = 512) -> Iterator[tuple[int, int, bytes]]:
    """Yield (record offset, masked length, tag) for top-level Dagor blocks."""
    pos = 12
    for _ in range(limit):
        if pos + 8 > size:
            break
        handle.seek(pos)
        header = handle.read(8)
        if len(header) != 8:
            break
        raw_length = struct.unpack_from("<I", header)[0]
        length = raw_length & 0x3FFFFFFF
        tag = header[4:8]
        if length < 4 or pos + 4 + length > size:
            break
        yield pos, length, tag
        pos += 4 + length


def _probe_level(path: Path) -> dict[str, Any]:
    result: dict[str, Any] = {
        "file_size": None,
        "has_hm2": False,
        "has_lmap": False,
        "has_frt": False,
        "legacyformat": None,
        "heightmap_format": None,
        "hm2": None,
        "lmap": None,
        "header_error": None,
    }
    if not path.is_file():
        return result

    try:
        with path.open("rb") as handle:
            size = handle.seek(0, os.SEEK_END)
            result["file_size"] = size
            for pos, length, tag in _iter_blocks(handle, size):
                if tag == b"\x00HM2" or tag == b"HM2":
                    if length < 36:
                        continue
                    handle.seek(pos + 8)
                    payload = handle.read(40)
                    if len(payload) < 36:
                        continue
                    cell, h_min, height_scale, ofs_x, ofs_z = struct.unpack_from(
                        "<5f", payload, 0
                    )
                    width_word, height_word = struct.unpack_from("<II", payload, 20)
                    width = width_word & 0xFFFF
                    height = height_word & 0xFFFF
                    layout = struct.unpack_from("<I", payload, 28)[0]
                    result["has_hm2"] = True
                    result["hm2"] = {
                        "version": (width_word >> 24) & 0xFF,
                        "cell": cell,
                        "h_min": h_min,
                        "height_scale": height_scale,
                        "ofs_x": ofs_x,
                        "ofs_z": ofs_z,
                        "width": width,
                        "height": height,
                        "mirror_height": (height_word >> 31) & 1,
                        "layout": f"0x{layout:08x}",
                    }
                elif tag == b"lmap":
                    if length < 36:
                        continue
                    handle.seek(pos + 8)
                    payload = handle.read(40)
                    if len(payload) < 36 or payload[:4] != b"lndm":
                        continue
                    version = struct.unpack_from("<I", payload, 4)[0]
                    grid_cell, land_cell = struct.unpack_from("<2f", payload, 8)
                    map_x, map_y = struct.unpack_from("<II", payload, 16)
                    origin_x, origin_y = struct.unpack_from("<ii", payload, 24)
                    use_tile = struct.unpack_from("<I", payload, 32)[0]
                    result["has_lmap"] = True
                    result["lmap"] = {
                        "version": version,
                        "grid_cell_size": grid_cell,
                        "land_cell_size": land_cell,
                        "map_size": [map_x, map_y],
                        "origin": [origin_x, origin_y],
                        "use_tile": use_tile,
                    }
                elif tag == b"\x00FRT" or tag == b"FRT":
                    result["has_frt"] = True
    except (OSError, struct.error, ValueError) as exc:
        result["header_error"] = f"{type(exc).__name__}: {exc}"

    result["legacyformat"] = result["has_hm2"]
    if result["has_hm2"]:
        result["heightmap_format"] = "HM2"
    elif result["has_frt"]:
        result["heightmap_format"] = "FRT"
    elif result["has_lmap"]:
        result["heightmap_format"] = "LMp2/lmap"
    return result


def _config_value(document: dict[str, Any], key: str, vector: bool = False) -> Any:
    """Datamine represents repeated BLK properties as an array of values."""
    value = document.get(key)
    if value is None:
        return None

    def scalar(item: Any) -> bool:
        return isinstance(item, (int, float)) and not isinstance(item, bool) and math.isfinite(item)

    def valid(item: Any) -> bool:
        if vector:
            return isinstance(item, list) and len(item) == 2 and all(scalar(v) for v in item)
        return scalar(item)

    if not valid(value):
        if not isinstance(value, list) or not value or not all(valid(item) for item in value):
            raise ValueError(f"invalid level config {key}")
        if any(item != value[0] for item in value[1:]):
            raise ValueError(f"conflicting repeated level config {key}")
        value = value[0]
    return [float(v) for v in value] if vector else float(value)


def _read_config(path: Path) -> dict[str, Any]:
    if not path.is_file():
        return {
            "map_coord0": None,
            "map_coord1": None,
            "water_level": None,
            "config_error": None,
        }
    try:
        if path.stat().st_size > MAX_CONFIG_BYTES:
            raise ValueError(f"level config exceeds {MAX_CONFIG_BYTES} bytes")
        document = json.loads(path.read_text(encoding="utf-8"))
        if not isinstance(document, dict):
            raise ValueError("level config must be a JSON object")
        return {
            "map_coord0": _config_value(document, "mapCoord0", vector=True),
            "map_coord1": _config_value(document, "mapCoord1", vector=True),
            "water_level": _config_value(document, "water_level"),
            "config_error": None,
        }
    except (OSError, ValueError) as exc:
        return {
            "map_coord0": None,
            "map_coord1": None,
            "water_level": None,
            "config_error": f"{type(exc).__name__}: {exc}",
        }


def level_config_path(datamine: Path, map_id: str) -> Path:
    """Accept a normal Datamine clone or a flattened levels directory."""
    name = f"{map_id}.blkx"
    for candidate in (
        datamine / "aces.vromfs.bin_u" / "levels" / name,
        datamine / "levels" / name,
    ):
        if candidate.is_file():
            return candidate
    return datamine / "aces.vromfs.bin_u" / "levels" / name


def _scan_mission_refs(datamine: Path) -> dict[str, list[str]]:
    root = datamine / "mis.vromfs.bin_u" / "gamedata" / "missions"
    refs: dict[str, set[str]] = {}
    if not root.is_dir():
        return {}
    for path in sorted(root.rglob("*.blkx")):
        try:
            text = path.read_text(encoding="utf-8", errors="ignore")
        except OSError:
            continue
        relative = path.relative_to(datamine).as_posix()
        for level_path in LEVEL_REF_RE.findall(text):
            refs.setdefault(level_path, set()).add(relative)
    return {key: sorted(value) for key, value in refs.items()}


def _is_air_template_ref(ref: str) -> bool:
    lower = "/" + ref.replace("\\", "/").lower()
    return any(
        marker in lower
        for marker in (
            "/cta/planes/",
            "/templates/air_templates/",
            "/templates/enduring_confrontation/",
            "/units_sets/plane_sets_",
            "/templates/air_race_basic_template.blkx",
            "/templates/air_domination_template.blkx",
            "/templates/air_helicopters_spawn_template.blkx",
        )
    )


def _coords_from_hm2(hm2: dict[str, Any] | None) -> tuple[list[float], list[float]] | None:
    if not hm2:
        return None
    cell = hm2.get("cell")
    width = hm2.get("width")
    height = hm2.get("height")
    ofs_x = hm2.get("ofs_x")
    ofs_z = hm2.get("ofs_z")
    if not all(isinstance(v, (int, float)) and math.isfinite(v) for v in (cell, width, height, ofs_x, ofs_z)):
        return None
    if cell <= 0 or width <= 0 or height <= 0:
        return None
    return [ofs_x, ofs_z], [ofs_x + width * cell, ofs_z + height * cell]


def _coords_from_lmap(lmap: dict[str, Any] | None) -> tuple[list[float], list[float]] | None:
    if not lmap or lmap.get("use_tile") != 1:
        return None
    land_cell = lmap.get("land_cell_size")
    size = lmap.get("map_size")
    origin = lmap.get("origin")
    if land_cell is None or not size or not origin or land_cell <= 0:
        return None
    coord0 = [origin[0] * land_cell, origin[1] * land_cell]
    coord1 = [
        (origin[0] + size[0]) * land_cell,
        (origin[1] + size[1]) * land_cell,
    ]
    return coord0, coord1


def _classify(
    map_id: str,
    refs: list[str],
    exists: bool,
    config_path: Path | None,
    probe: dict[str, Any],
    explicit_target: bool = False,
) -> tuple[bool, list[str]]:
    reasons: list[str] = []
    air_refs = [ref for ref in refs if _is_air_template_ref(ref)]

    if refs:
        reasons.append(f"scanned datamine level references: {len(refs)} mission/config file(s)")
    else:
        reasons.append("no 'level' reference found in the local datamine mission subset")
    if air_refs:
        shown = "; ".join(air_refs[:3])
        suffix = f" (+{len(air_refs) - 3} more)" if len(air_refs) > 3 else ""
        reasons.append(f"air/plane-mode reference: {shown}{suffix}")
    alias = OBSOLETE_ALIASES.get(map_id)
    if explicit_target:
        is_air = True
        reasons.append("explicit ID from the authoritative AtlasReferences.json target set")
    elif alias:
        is_air = False
        reasons.extend([
            f"obsolete subpart reference; parent {alias['parent']} declares the effective level",
            f"not a separate terrain target; effective map is {alias['alias_of']} (no HM2/lmap copied)",
            "upstream config tree has no matching .blkx and the client has no matching .bin",
        ])
    elif map_id.startswith("hangar_"):
        is_air = False
        reasons.append("excluded hangar/scenery level")
    elif map_id in TRAINING_OR_BLANK_IDS:
        is_air = False
        reasons.append("excluded training/blank placeholder level")
    elif air_refs:
        is_air = True
        reasons.append("air role established by local plane/air-mode reference")
    elif map_id.startswith("air_"):
        is_air = True
        reasons.append("air_ filename family")
    elif map_id.startswith("arcade_"):
        is_air = True
        reasons.append("arcade_ air-arcade map family")
    elif map_id.startswith(MODERN_NON_AIR_PREFIXES):
        is_air = False
        reasons.append(f"excluded filename family: {map_id.split('_', 1)[0]}_ (ground/naval level)")
    else:
        is_air = True
        reasons.append("legacy historical untagged air level (no avg_/avn_/hvg_ prefix)")

    if probe["has_hm2"]:
        hm2 = probe["hm2"]
        reasons.append(
            "HM2 header: {}x{}, cell={} m, hMin={} m, heightScale={} m".format(
                hm2["width"], hm2["height"], hm2["cell"], hm2["h_min"], hm2["height_scale"]
            )
        )
    elif exists:
        reasons.append("HM2 block absent; modern FRT/LMp2 terrain path requires separate mapping")
    if probe["has_lmap"]:
        lmap = probe["lmap"]
        reasons.append(
            "lmap/lndm header v{}: grid={} m, landCell={} m, size={}x{}".format(
                lmap["version"], lmap["grid_cell_size"], lmap["land_cell_size"],
                lmap["map_size"][0], lmap["map_size"][1]
            )
        )
    elif exists:
        reasons.append("lmap block absent or unrecognized")
    if not exists:
        reasons.append("client source .bin missing" + (" (active target)" if is_air else ""))
    if config_path is not None and config_path.is_file():
        reasons.append("selected level config present")
    else:
        reasons.append("selected level .blkx config is absent")
    if probe["header_error"]:
        reasons.append(f"header probe error: {probe['header_error']}")
    return is_air, reasons


def build_catalog(
    datamine: Path, client: Path, target_ids: list[str] | None = None
) -> list[dict[str, Any]]:
    """Return all locally discoverable level candidates with explicit air flags."""
    datamine = Path(datamine)
    client = Path(client)
    refs_by_level = _scan_mission_refs(datamine)
    refs_by_level_casefold = {key.casefold(): value for key, value in refs_by_level.items()}
    client_levels = client / "levels"
    if not client_levels.is_dir():
        raise FileNotFoundError(f"client levels directory not found: {client_levels}")

    paths_by_rel: dict[str, str] = {}
    for path in sorted(client_levels.glob("*.bin")):
        paths_by_rel[f"levels/{path.name}".lower()] = f"levels/{path.name}"
    for level_ref in refs_by_level:
        if level_ref.lower().endswith(".bin"):
            paths_by_rel.setdefault(level_ref.lower(), level_ref)
    for map_id in target_ids or ():
        level_ref = f"levels/{map_id}.bin"
        paths_by_rel.setdefault(level_ref.lower(), level_ref)

    catalog: list[dict[str, Any]] = []
    for level_rel in sorted(paths_by_rel.values(), key=lambda value: value.lower()):
        map_id = Path(level_rel).stem
        source_path = client / level_rel
        datamine_config = level_config_path(datamine, map_id)
        if map_id in OBSOLETE_ALIASES:
            config_path = None
        elif datamine_config.is_file():
            config_path = datamine_config
        else:
            config_path = None
        refs = refs_by_level_casefold.get(level_rel.casefold(), [])
        exists = source_path.is_file()
        probe = _probe_level(source_path)
        config = _read_config(config_path) if config_path is not None else {
            "map_coord0": None,
            "map_coord1": None,
            "water_level": None,
            "config_error": None,
        }
        explicit_target = target_ids is not None and map_id in target_ids
        if explicit_target and config["config_error"]:
            raise ValueError(f"invalid config for {map_id}: {config['config_error']}")
        is_air, reasons = _classify(
            map_id, refs, exists, config_path, probe, explicit_target
        )

        coord0 = config["map_coord0"]
        coord1 = config["map_coord1"]
        if explicit_target and (coord0 is None) != (coord1 is None):
            raise ValueError(f"{map_id}: both mapCoord0 and mapCoord1 must be supplied together")
        if coord0 is None or coord1 is None:
            coords = _coords_from_lmap(probe["lmap"]) or _coords_from_hm2(probe["hm2"])
            if coords:
                coord0 = coord0 if coord0 is not None else coords[0]
                coord1 = coord1 if coord1 is not None else coords[1]

        catalog.append({
            "id": map_id,
            "source_path": source_path.as_posix(),
            "config_path": config_path.as_posix() if config_path is not None else None,
            "map_coord0": coord0,
            "map_coord1": coord1,
            "water_level": config["water_level"],
            "selection_reasons": reasons,
            "mission_refs": refs,
            "exists": exists,
            "is_air": is_air,
            "explicit_target": explicit_target,
            "config_exists": config_path is not None and config_path.is_file(),
            "config_source": "local_datamine" if datamine_config.is_file() else "none",
            "config_status": (
                "local_datamine" if datamine_config.is_file() else "not_available"
            ),
            "alias_of": OBSOLETE_ALIASES.get(map_id, {}).get("alias_of"),
            "alias_source": OBSOLETE_ALIASES.get(map_id, {}).get("parent"),
            "obsolete_reference": map_id in OBSOLETE_ALIASES,
            "active_missing": is_air and not exists,
            "file_size": probe["file_size"],
            "has_hm2": probe["has_hm2"],
            "has_lmap": probe["has_lmap"],
            "has_frt": probe["has_frt"],
            "legacyformat": probe["legacyformat"],
            "heightmap_format": probe["heightmap_format"],
            "hm2_header": probe["hm2"],
            "lmap_header": probe["lmap"],
            "config_error": config["config_error"],
            "header_error": probe["header_error"],
        })
    return catalog


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--datamine", type=Path, required=True)
    parser.add_argument("--client", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--air-only", action="store_true", help="write only is_air=true entries")
    parser.add_argument(
        "--targets",
        type=Path,
        help="authoritative JSON object such as tools/terrain/AtlasReferences.json",
    )
    parser.add_argument("--expected-targets", type=int)
    parser.add_argument("--allow-missing-sources", action="store_true")
    parser.add_argument("--allow-missing-configs", action="store_true")
    args = parser.parse_args()

    target_ids: list[str] | None = None
    if args.targets is not None:
        try:
            target_ids = load_reference_ids(args.targets, args.expected_targets)
        except TargetReferenceError as exc:
            parser.error(str(exc))
    elif args.expected_targets is not None:
        parser.error("--expected-targets requires --targets")

    try:
        catalog = build_catalog(args.datamine, args.client, target_ids)
    except (OSError, ValueError) as exc:
        parser.error(str(exc))
    if target_ids is not None:
        by_id = {entry["id"]: entry for entry in catalog}
        catalog = [by_id[map_id] for map_id in target_ids]
        missing_sources = [e["id"] for e in catalog if not e["exists"]]
        missing_configs = [e["id"] for e in catalog if not e["config_exists"]]
        if missing_sources and not args.allow_missing_sources:
            parser.error("authoritative targets have missing client .bin files: " + ", ".join(missing_sources))
        if missing_configs and not args.allow_missing_configs:
            parser.error("authoritative targets have missing Datamine .blkx files: " + ", ".join(missing_configs))
    if args.air_only:
        catalog = [entry for entry in catalog if entry["is_air"]]
    args.output.parent.mkdir(parents=True, exist_ok=True)
    temporary = args.output.with_name(args.output.name + ".tmp")
    temporary.write_text(
        json.dumps(catalog, ensure_ascii=False, indent=2) + "\n", encoding="utf-8"
    )
    os.replace(temporary, args.output)
    target_count = sum(1 for entry in catalog if entry.get("explicit_target"))
    print(
        f"wrote {len(catalog)} entries ({target_count} authoritative targets) "
        f"to {args.output}"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

#!/usr/bin/env python3
"""Validate an exact installer-ready terrain package without decoding sources."""

from __future__ import annotations

import argparse
import hashlib
import json
import math
import os
from pathlib import Path
import re
import sys
import xml.etree.ElementTree as ET

from targets import TargetReferenceError, load_reference_ids


TOOL_VERSION = 1
MAX_JSON_BYTES = 8 * 1024**2
MAX_XML_BYTES = 2 * 1024**2
TOLERANCE = 1e-6
SHA256_RE = re.compile(r"[0-9a-f]{64}\Z")


class PackageError(ValueError):
    """The converted package is incomplete or installer-incompatible."""


def _read_json(path: Path) -> object:
    try:
        if path.stat().st_size > MAX_JSON_BYTES:
            raise PackageError(f"JSON file is too large: {path}")
        return json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise PackageError(f"cannot read {path}: {exc}") from exc


def _sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        while block := handle.read(8 * 1024**2):
            digest.update(block)
    return digest.hexdigest()


def _text(node: ET.Element, name: str) -> str:
    value = node.findtext(name)
    if value is None:
        raise PackageError(f"XML node is missing {name}")
    return value.strip()


def _zero_origin(x: float, z: float) -> tuple[float, float]:
    latitude = z / 111320.0
    longitude = x / (111320.0 * math.cos(math.radians(latitude)))
    return longitude, latitude


def _verify_xml(path: Path, map_id: str, output: dict[str, object]) -> None:
    try:
        if path.stat().st_size > MAX_XML_BYTES:
            raise PackageError(f"XML file is too large: {path}")
        root = ET.parse(path).getroot()
    except (OSError, ET.ParseError) as exc:
        raise PackageError(f"cannot parse {path}: {exc}") from exc
    if root.tag != "Resources":
        raise PackageError(f"{path}: root must be Resources")
    node = root.find("CustomHeightmapList/CustomHeightmap")
    if node is None:
        raise PackageError(f"{path}: CustomHeightmap node is missing")
    if node.get("ID") != f"WarThunder.{map_id}":
        raise PackageError(f"{path}: unexpected CustomHeightmap ID")
    expected = {
        "File": str(output["data_file"]),
        "Visible": "true",
        "BigEndian": "0",
        "Signed": "true",
        "Width": str(output["width"]),
        "Height": str(output["height"]),
        "AltitudeFactor": "1",
        "AltitudeOffset": "0",
        "Projection": "Quad",
    }
    for name, value in expected.items():
        if _text(node, name) != value:
            raise PackageError(f"{path}: {name} must be {value!r}")
    bounds = output.get("sample_bounds_m")
    if not isinstance(bounds, list) or len(bounds) != 4:
        raise PackageError(f"{path}: metadata sample bounds are invalid")
    x0, z0, x1, z1 = (float(value) for value in bounds)
    corners = {
        "BottomLeft": (x0, z0),
        "BottomRight": (x1, z0),
        "TopRight": (x1, z1),
        "TopLeft": (x0, z1),
    }
    for name, (x, z) in corners.items():
        corner = node.find(name)
        if corner is None:
            raise PackageError(f"{path}: {name} is missing")
        expected_lon, expected_lat = _zero_origin(x, z)
        try:
            actual_lon = float(_text(corner, "Longitude"))
            actual_lat = float(_text(corner, "Latitude"))
        except ValueError as exc:
            raise PackageError(f"{path}: {name} has invalid coordinates") from exc
        if abs(actual_lon - expected_lon) > TOLERANCE or abs(actual_lat - expected_lat) > TOLERANCE:
            raise PackageError(f"{path}: {name} is not zero-origin")


def _verify_map(root: Path, map_id: str) -> dict[str, object]:
    folder = root / "maps" / map_id
    data_path = folder / f"{map_id}.data"
    metadata_path = folder / "metadata.json"
    xml_path = folder / "CustomHeightmapList.xml"
    for path in (data_path, metadata_path, xml_path):
        if not path.is_file():
            raise PackageError(f"missing package file: {path}")
    metadata = _read_json(metadata_path)
    if not isinstance(metadata, dict) or metadata.get("status") != "ok" or metadata.get("id") != map_id:
        raise PackageError(f"{metadata_path}: status or ID is invalid")
    output = metadata.get("output")
    if not isinstance(output, dict):
        raise PackageError(f"{metadata_path}: output object is missing")
    width = output.get("width")
    height = output.get("height")
    if not isinstance(width, int) or not isinstance(height, int) or width < 2 or height < 2:
        raise PackageError(f"{metadata_path}: dimensions are invalid")
    expected_bytes = width * height * 2
    if output.get("data_bytes") != expected_bytes or data_path.stat().st_size != expected_bytes:
        raise PackageError(f"{data_path}: size disagrees with 2*width*height")
    recorded_hash = output.get("data_sha256")
    if not isinstance(recorded_hash, str) or not SHA256_RE.fullmatch(recorded_hash):
        raise PackageError(f"{metadata_path}: data SHA256 is invalid")
    if _sha256(data_path) != recorded_hash:
        raise PackageError(f"{data_path}: data SHA256 differs from metadata")
    bounds = output.get("sample_bounds_m")
    if (
        not isinstance(bounds, list)
        or len(bounds) != 4
        or not all(isinstance(v, (int, float)) and math.isfinite(v) for v in bounds)
        or bounds[2] <= bounds[0]
        or bounds[3] <= bounds[1]
    ):
        raise PackageError(f"{metadata_path}: sample bounds are invalid")
    _verify_xml(xml_path, map_id, output)
    mode = metadata.get("format_decoder_info", {}).get("heightmap_mode")
    return {"width": width, "height": height, "mode": mode, "data_bytes": expected_bytes}


def _verify_special(root: Path, map_id: str, mode: str) -> dict[str, object]:
    metadata = _read_json(root / "maps" / map_id / "metadata.json")
    if not isinstance(metadata, dict):
        raise PackageError(f"{map_id}: metadata is not an object")
    info = metadata.get("format_decoder_info", {})
    if not isinstance(info, dict) or info.get("heightmap_mode") != mode:
        raise PackageError(f"{map_id}: expected heightmap mode {mode!r}")
    if map_id == "avg_egypt_sinai":
        if info.get("base_decoder") != "render_mesh":
            raise PackageError("avg_egypt_sinai: expected render_mesh base decoder")
        coverage = metadata.get("coverage", {})
        if not isinstance(coverage, dict) or int(coverage.get("hm2_overlay_pixel_count", 0)) <= 0:
            raise PackageError("avg_egypt_sinai: HM2 overlay did not contribute pixels")
        blocks = info.get("actual_blocks", {})
        if not isinstance(blocks, dict) or not all(blocks.get(k) for k in ("has_hm2", "has_lmap", "has_frt")):
            raise PackageError("avg_egypt_sinai: expected HM2, lmap, and FRT source blocks")
    return {"id": map_id, "mode": mode}


def finalize(
    root: Path,
    references: Path,
    catalog_path: Path,
    expected_count: int | None,
    specials: list[str],
) -> dict[str, object]:
    root = root.resolve()
    references = references.resolve()
    target_ids = load_reference_ids(references, expected_count)
    target_set = set(target_ids)
    catalog = _read_json(catalog_path.resolve())
    if not isinstance(catalog, list):
        raise PackageError("catalog must be a JSON array")
    catalog_ids = [entry.get("id") for entry in catalog if isinstance(entry, dict)]
    if catalog_ids != target_ids:
        raise PackageError("catalog IDs/order differ from authoritative references")
    for entry in catalog:
        if not entry.get("is_air") or not entry.get("explicit_target") or not entry.get("exists"):
            raise PackageError(f"catalog target {entry.get('id')!r} is not an available explicit air target")

    manifest = _read_json(root / "manifest.json")
    if not isinstance(manifest, dict) or manifest.get("status") != "complete":
        raise PackageError("manifest status is not complete")
    if manifest.get("requested_ids") != target_ids:
        raise PackageError("manifest request differs from authoritative references")
    entries = manifest.get("entries")
    if not isinstance(entries, dict) or set(entries) != target_set:
        raise PackageError("manifest entries do not match the authoritative target set")
    bad = sorted(
        key for key, value in entries.items()
        if not isinstance(value, dict) or value.get("status") not in {"ok", "skipped"}
    )
    if bad:
        raise PackageError("manifest has incomplete maps: " + ", ".join(bad))

    maps_root = root / "maps"
    actual_dirs = {path.name for path in maps_root.iterdir() if path.is_dir()} if maps_root.is_dir() else set()
    if actual_dirs != target_set:
        extra = sorted(actual_dirs - target_set)
        missing = sorted(target_set - actual_dirs)
        raise PackageError(f"package map directory set differs; extra={extra}, missing={missing}")
    maps = {map_id: _verify_map(root, map_id) for map_id in target_ids}

    wrapped_refs = root / "AtlasReferences.json"
    if not wrapped_refs.is_file() or _sha256(wrapped_refs) != _sha256(references):
        raise PackageError("output AtlasReferences.json is missing or differs from the authoritative copy")
    special_results: dict[str, object] = {}
    for specification in specials:
        map_id, separator, mode = specification.partition("=")
        if not separator or map_id not in target_set or not mode:
            raise PackageError(f"invalid --special value: {specification!r}")
        special_results[map_id] = _verify_special(root, map_id, mode)

    report: dict[str, object] = {
        "schema_version": TOOL_VERSION,
        "status": "complete",
        "installer_ready": True,
        "map_count": len(target_ids),
        "maps": maps,
        "special": special_results,
        "references": {
            "path": str(references),
            "sha256": _sha256(references),
        },
        "catalog": {"path": str(catalog_path.resolve()), "sha256": _sha256(catalog_path.resolve())},
        "xml_layout": "zero-origin; installer applies GeoReferences or AtlasReferences",
    }
    output = root / "terrain-build.json"
    temporary = output.with_name(output.name + ".tmp")
    temporary.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    os.replace(temporary, output)
    return report


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, required=True)
    parser.add_argument("--references", type=Path, required=True)
    parser.add_argument("--catalog", type=Path, required=True)
    parser.add_argument("--expected-count", type=int, help="optional count assertion; membership comes from --references")
    parser.add_argument("--special", action="append", default=[])
    args = parser.parse_args(argv)
    try:
        report = finalize(
            args.root,
            args.references,
            args.catalog,
            args.expected_count,
            args.special,
        )
    except (OSError, PackageError, TargetReferenceError) as exc:
        print(f"finalize: {exc}", file=sys.stderr)
        return 1
    print(f"finalize: complete {report['map_count']} installer-ready maps")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

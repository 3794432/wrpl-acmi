#!/usr/bin/env python3
"""Independently verify War Thunder Tacview terrain export packages.

The validator never opens a source level and never repeats terrain decoding.
It streams the exported int16 data for hashing, size and extrema checks, and
reads only a bounded number of rows for the visual overview.

NumPy and Pillow are imported only after MemoryGuard has assigned the process
its budget.  Run with:

    python verify_outputs.py --root <package> --catalog catalog.json
"""

from __future__ import annotations

import argparse
from datetime import datetime, timezone
import hashlib
import json
import math
import os
from pathlib import Path
import re
import struct
from typing import Any, Iterable, Iterator
import xml.etree.ElementTree as ET

from memory_guard import MemoryGuard


TOOL_VERSION = 1
MEMORY_LIMIT_BYTES = 2 * 1024**3
HASH_CHUNK_BYTES = 8 * 1024**2
MAX_METADATA_BYTES = 8 * 1024**2
MAX_XML_BYTES = 2 * 1024**2
MAX_PREVIEW_ROW_BYTES = 64 * 1024**2
PREVIEW_SOURCE_ROWS = 128
THUMBNAIL_PX = 256
LABEL_HEIGHT = 36
SEA_LEVEL_TOLERANCE_M = 2.0
HALF_METER_TOLERANCE = 0.500001
LAT_LON_PAIR_TOLERANCE = 1e-6
SAFE_ID = re.compile(r"^[A-Za-z0-9][A-Za-z0-9_.-]*$")
SHA256_RE = re.compile(r"^[0-9a-fA-F]{64}$")


class CheckLog:
    """Collect checks and their review-facing consequences."""

    def __init__(self) -> None:
        self.items: list[dict[str, Any]] = []
        self.errors: list[str] = []
        self.warnings: list[str] = []

    def add(
        self,
        name: str,
        status: str,
        message: str | None = None,
        **evidence: Any,
    ) -> None:
        item: dict[str, Any] = {"name": name, "status": status}
        if message:
            item["message"] = message
        if evidence:
            item["evidence"] = _json_safe(evidence)
        self.items.append(item)
        if status == "fail" and message:
            self.errors.append(message)
        elif status in {"warn", "review"} and message:
            self.warnings.append(message)

    @property
    def status(self) -> str:
        if self.errors:
            return "fail"
        if self.warnings or any(item["status"] == "review" for item in self.items):
            return "review"
        return "pass"


def _json_safe(value: Any) -> Any:
    """Convert path objects and finite numeric values to JSON-safe values."""

    if isinstance(value, Path):
        return str(value)
    if isinstance(value, dict):
        return {str(key): _json_safe(item) for key, item in value.items()}
    if isinstance(value, (list, tuple)):
        return [_json_safe(item) for item in value]
    if isinstance(value, float) and not math.isfinite(value):
        return str(value)
    if value is None or isinstance(value, (str, int, float, bool)):
        return value
    return str(value)


def _utc_now() -> str:
    return datetime.now(timezone.utc).isoformat(timespec="seconds")


def _read_limited(path: Path, limit: int, label: str) -> bytes:
    try:
        size = path.stat().st_size
        if size > limit:
            raise ValueError(f"{label} is {size} bytes; limit is {limit}")
        with path.open("rb") as stream:
            return stream.read(limit + 1)
    except OSError as exc:
        raise ValueError(f"cannot read {label} {path}: {exc}") from exc


def _load_json_object(path: Path) -> Any:
    blob = _read_limited(path, MAX_METADATA_BYTES, "JSON")
    try:
        return json.loads(blob.decode("utf-8-sig"))
    except (UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise ValueError(f"invalid JSON in {path}: {exc}") from exc


def _as_dict(value: Any) -> dict[str, Any] | None:
    return value if isinstance(value, dict) else None


def _as_number(value: Any) -> float | None:
    if isinstance(value, bool) or not isinstance(value, (int, float)):
        return None
    number = float(value)
    return number if math.isfinite(number) else None


def _as_positive_int(value: Any) -> int | None:
    if isinstance(value, bool):
        return None
    if isinstance(value, int):
        return value if value > 0 else None
    if isinstance(value, float) and math.isfinite(value) and value.is_integer():
        integer = int(value)
        return integer if integer > 0 else None
    if isinstance(value, str) and re.fullmatch(r"\s*\d+\s*", value):
        integer = int(value)
        return integer if integer > 0 else None
    return None


def _as_bool(value: Any) -> bool | None:
    if isinstance(value, bool):
        return value
    if isinstance(value, (int, float)) and not isinstance(value, bool):
        if value == 0:
            return False
        if value == 1:
            return True
    if isinstance(value, str):
        normalized = value.strip().lower()
        if normalized in {"true", "yes", "1"}:
            return True
        if normalized in {"false", "no", "0"}:
            return False
    return None


def _normal_key(value: str) -> str:
    return re.sub(r"[^a-z0-9]", "", value.lower())


def _find_key(
    obj: Any,
    candidates: set[str],
    *,
    max_depth: int = 6,
) -> tuple[Any, str] | None:
    """Find a known optional key without assuming a fixed metadata nesting."""

    wanted = {_normal_key(candidate) for candidate in candidates}

    def visit(value: Any, path: str, depth: int) -> tuple[Any, str] | None:
        if depth > max_depth:
            return None
        if isinstance(value, dict):
            for key, item in value.items():
                key_text = str(key)
                child_path = f"{path}.{key_text}" if path else key_text
                if _normal_key(key_text) in wanted:
                    return item, child_path
            for key, item in value.items():
                child_path = f"{path}.{key}" if path else str(key)
                found = visit(item, child_path, depth + 1)
                if found is not None:
                    return found
        elif isinstance(value, list):
            for index, item in enumerate(value):
                found = visit(item, f"{path}[{index}]", depth + 1)
                if found is not None:
                    return found
        return None

    return visit(obj, "", 0)


def _find_number(obj: Any, candidates: Iterable[str]) -> tuple[float, str] | None:
    found = _find_key(obj, set(candidates))
    if found is None:
        return None
    number = _as_number(found[0])
    return (number, found[1]) if number is not None else None


def _find_bool(obj: Any, candidates: Iterable[str]) -> tuple[bool, str] | None:
    found = _find_key(obj, set(candidates))
    if found is None:
        return None
    boolean = _as_bool(found[0])
    return (boolean, found[1]) if boolean is not None else None


def _output_value(meta: dict[str, Any], key: str) -> tuple[Any, str]:
    output = _as_dict(meta.get("output"))
    if output is not None and key in output:
        return output[key], f"output.{key}"
    return meta.get(key), key


def _bounds(value: Any) -> tuple[float, float, float, float] | None:
    if isinstance(value, (list, tuple)) and len(value) == 4:
        values = tuple(_as_number(item) for item in value)
        if all(item is not None for item in values):
            return tuple(values)  # type: ignore[return-value]
    if isinstance(value, dict):
        names = ("x0", "z0", "x1", "z1")
        aliases = {
            "x0": ("x0", "x_min", "min_x", "west"),
            "z0": ("z0", "z_min", "min_z", "south"),
            "x1": ("x1", "x_max", "max_x", "east"),
            "z1": ("z1", "z_max", "max_z", "north"),
        }
        output: list[float] = []
        normalized = {_normal_key(str(key)): value for key, value in value.items()}
        for name in names:
            number = None
            for alias in aliases[name]:
                number = _as_number(normalized.get(_normal_key(alias)))
                if number is not None:
                    break
            if number is None:
                return None
            output.append(number)
        return tuple(output)  # type: ignore[return-value]
    return None


def _normal_path(value: Any) -> str | None:
    if not isinstance(value, (str, os.PathLike)):
        return None
    text = os.fspath(value).strip()
    if not text:
        return None
    text = text.replace("\\", os.sep).replace("/", os.sep)
    return os.path.normcase(os.path.normpath(text))


def _catalog_entries(document: Any) -> list[dict[str, Any]]:
    if isinstance(document, list):
        items = document
    elif isinstance(document, dict):
        items = None
        for key in ("entries", "maps", "catalog", "items"):
            candidate = document.get(key)
            if isinstance(candidate, list):
                items = candidate
                break
            if isinstance(candidate, dict):
                values = list(candidate.values())
                if values and all(isinstance(item, dict) for item in values):
                    items = values
                    break
        if items is None and "id" in document:
            items = [document]
    else:
        items = None
    if items is None:
        raise ValueError("catalog must be an entry list or contain entries/maps")
    if not all(isinstance(item, dict) for item in items):
        raise ValueError("catalog contains a non-object entry")
    return items


def _entry_exists(entry: dict[str, Any]) -> bool:
    if "exists" not in entry:
        return True
    value = _as_bool(entry.get("exists"))
    return bool(value) if value is not None else False


def _entry_is_air(entry: dict[str, Any]) -> bool:
    value = _as_bool(entry.get("is_air"))
    return bool(value)


def _local_tag(tag: str) -> str:
    return tag.rsplit("}", 1)[-1]


def _child(node: ET.Element, name: str) -> ET.Element | None:
    for child in list(node):
        if _local_tag(child.tag) == name:
            return child
    return None


def _child_text(node: ET.Element, name: str) -> str | None:
    child = _child(node, name)
    if child is None or child.text is None:
        return None
    value = child.text.strip()
    return value or None


def _parse_xml(path: Path, map_id: str) -> dict[str, Any]:
    blob = _read_limited(path, MAX_XML_BYTES, "XML")
    try:
        root = ET.fromstring(blob)
    except ET.ParseError as exc:
        raise ValueError(f"invalid XML in {path}: {exc}") from exc
    if _local_tag(root.tag) != "Resources":
        raise ValueError(f"XML root is {_local_tag(root.tag)!r}, expected Resources")
    listing = _child(root, "CustomHeightmapList")
    if listing is None:
        raise ValueError("XML lacks Resources/CustomHeightmapList")
    entries = [child for child in list(listing) if _local_tag(child.tag) == "CustomHeightmap"]
    if not entries:
        raise ValueError("CustomHeightmapList has no CustomHeightmap entry")

    expected_file = f"{map_id}.data"
    selected = next(
        (entry for entry in entries if (_child_text(entry, "File") or "").strip() == expected_file),
        entries[0],
    )
    fields: dict[str, str | None] = {}
    for name in (
        "File",
        "Visible",
        "BigEndian",
        "Signed",
        "Width",
        "Height",
        "AltitudeFactor",
        "AltitudeOffset",
        "Projection",
    ):
        fields[name] = _child_text(selected, name)

    corners: dict[str, dict[str, float | None] | None] = {}
    for name in ("BottomLeft", "BottomRight", "TopRight", "TopLeft"):
        node = _child(selected, name)
        if node is None:
            corners[name] = None
            continue
        corners[name] = {
            "Longitude": _parse_float_text(_child_text(node, "Longitude")),
            "Latitude": _parse_float_text(_child_text(node, "Latitude")),
        }
    return {
        "entry_count": len(entries),
        "customheightmaplist_attributes": dict(listing.attrib),
        "customheightmap_attributes": dict(selected.attrib),
        "fields": fields,
        "corners": corners,
    }


def _parse_float_text(value: str | None) -> float | None:
    if value is None:
        return None
    try:
        number = float(value)
    except ValueError:
        return None
    return number if math.isfinite(number) else None


def _scan_data(path: Path, np: Any) -> dict[str, Any]:
    digest = hashlib.sha256()
    size = 0
    data_min: int | None = None
    data_max: int | None = None
    odd = False
    carry = b""
    try:
        with path.open("rb") as stream:
            while True:
                blob = stream.read(HASH_CHUNK_BYTES)
                if not blob:
                    break
                size += len(blob)
                digest.update(blob)
                combined = carry + blob
                even = len(combined) & ~1
                if even:
                    values = np.frombuffer(memoryview(combined)[:even], dtype="<i2")
                    if values.size:
                        low = int(values.min())
                        high = int(values.max())
                        data_min = low if data_min is None else min(data_min, low)
                        data_max = high if data_max is None else max(data_max, high)
                carry = combined[even:]
    except OSError as exc:
        raise ValueError(f"cannot stream {path}: {exc}") from exc
    odd = bool(carry)
    return {
        "path": str(path),
        "bytes": size,
        "sha256": digest.hexdigest(),
        "minimum_m": data_min,
        "maximum_m": data_max,
        "odd_trailing_byte": odd,
        "streamed": True,
        "chunk_bytes": HASH_CHUNK_BYTES,
    }


def _palette_rgb(np: Any, raster: Any, low: int, high: int) -> Any:
    positions = np.asarray([0.0, 0.22, 0.48, 0.70, 0.86, 1.0], dtype=np.float32)
    colors = np.asarray(
        [
            [24, 48, 92],
            [34, 105, 115],
            [77, 145, 88],
            [184, 177, 107],
            [139, 101, 72],
            [232, 232, 220],
        ],
        dtype=np.float32,
    )
    lut = np.empty((256, 3), dtype=np.uint8)
    sample = np.linspace(0.0, 1.0, 256, dtype=np.float32)
    for channel in range(3):
        lut[:, channel] = np.rint(
            np.interp(sample, positions, colors[:, channel])
        ).astype(np.uint8)
    if low == high:
        indexes = np.full(raster.shape, 112, dtype=np.uint8)
    else:
        normalized = (raster.astype(np.float32) - float(low)) / float(high - low)
        indexes = np.clip(np.rint(normalized * 255.0), 0, 255).astype(np.uint8)
    return lut[indexes]


def _make_preview(
    path: Path,
    width: int,
    height: int,
    low: int,
    high: int,
    rows: int,
    np: Any,
) -> tuple[Any, dict[str, Any]]:
    row_bytes = width * 2
    if row_bytes > MAX_PREVIEW_ROW_BYTES:
        raise ValueError(
            f"preview row would allocate {row_bytes} bytes; limit is {MAX_PREVIEW_ROW_BYTES}"
        )
    source_rows = max(1, min(rows, height))
    source_columns = max(1, min(THUMBNAIL_PX, width))
    row_indexes = np.unique(
        np.rint(np.linspace(0, height - 1, source_rows, dtype=np.float64)).astype(np.int64)
    )
    column_indexes = np.unique(
        np.rint(np.linspace(0, width - 1, source_columns, dtype=np.float64)).astype(np.int64)
    )
    raster = np.empty((len(row_indexes), len(column_indexes)), dtype=np.int16)
    try:
        with path.open("rb") as stream:
            for output_row, source_row in enumerate(row_indexes):
                stream.seek(int(source_row) * row_bytes)
                blob = stream.read(row_bytes)
                if len(blob) != row_bytes:
                    raise ValueError(f"truncated preview row {source_row} in {path}")
                values = np.frombuffer(blob, dtype="<i2")
                raster[output_row] = values[column_indexes]
    except OSError as exc:
        raise ValueError(f"cannot sample preview from {path}: {exc}") from exc
    return _palette_rgb(np, raster, low, high), {
        "source_rows": [int(row_indexes[0]), int(row_indexes[-1])],
        "source_row_count": int(len(row_indexes)),
        "source_column_count": int(len(column_indexes)),
        "thumbnail_px": THUMBNAIL_PX,
    }


def _font(ImageFont: Any) -> Any:
    for name in (
        "C:/Windows/Fonts/consola.ttf",
        "C:/Windows/Fonts/arial.ttf",
        "/usr/share/fonts/truetype/dejavu/DejaVuSansMono.ttf",
    ):
        try:
            return ImageFont.truetype(name, 11)
        except OSError:
            continue
    return ImageFont.load_default()


def _render_tile(
    report: dict[str, Any],
    preview: Any,
    Image: Any,
    ImageDraw: Any,
    font: Any,
) -> Any:
    image_height = THUMBNAIL_PX - LABEL_HEIGHT
    tile = Image.new("RGB", (THUMBNAIL_PX, THUMBNAIL_PX), (236, 236, 231))
    draw = ImageDraw.Draw(tile)
    map_id = report["id"]
    status = report["status"]
    if preview is not None:
        image = Image.fromarray(preview, "RGB")
        image = image.resize(
            (THUMBNAIL_PX, image_height), resample=Image.Resampling.BILINEAR
        )
        tile.paste(image, (0, 0))
    else:
        color = (108, 42, 42) if status in {"fail", "missing"} else (88, 88, 80)
        draw.rectangle((0, 0, THUMBNAIL_PX, image_height), fill=color)
        draw.text((8, image_height // 2 - 8), "no preview", fill=(245, 245, 240), font=font)

    border = {"pass": (53, 126, 79), "review": (206, 137, 42), "fail": (173, 46, 46),
              "missing": (120, 120, 120)}.get(status, (80, 80, 80))
    draw.rectangle((0, 0, THUMBNAIL_PX - 1, THUMBNAIL_PX - 1), outline=border, width=2)
    draw.rectangle((2, image_height, THUMBNAIL_PX - 3, THUMBNAIL_PX - 3), fill=(245, 245, 239))
    data = report.get("data") or {}
    low = data.get("minimum_m")
    high = data.get("maximum_m")
    if isinstance(low, int) and isinstance(high, int):
        range_text = f"{low} .. {high} m"
    else:
        range_text = "range unavailable"
    draw.text((5, image_height + 2), map_id, fill=(25, 25, 22), font=font)
    suffix = " [review]" if status == "review" else " [fail]" if status in {"fail", "missing"} else ""
    draw.text((5, image_height + 17), range_text + suffix, fill=(55, 55, 50), font=font)
    return tile


def _render_overview(
    path: Path,
    reports: list[dict[str, Any]],
    previews: dict[str, Any],
    Image: Any,
    ImageDraw: Any,
    ImageFont: Any,
) -> dict[str, Any]:
    reports = sorted(reports, key=lambda item: item["id"].casefold())
    count = max(1, len(reports))
    columns = min(8, count)
    rows = (count + columns - 1) // columns
    sheet = Image.new("RGB", (columns * THUMBNAIL_PX, rows * THUMBNAIL_PX), (55, 55, 52))
    font = _font(ImageFont)
    if not reports:
        tile = Image.new("RGB", (THUMBNAIL_PX, THUMBNAIL_PX), (70, 70, 66))
        draw = ImageDraw.Draw(tile)
        draw.text((12, 110), "no catalog maps", fill=(245, 245, 240), font=font)
        sheet.paste(tile, (0, 0))
    else:
        for index, report in enumerate(reports):
            tile = _render_tile(report, previews.get(report["id"]), Image, ImageDraw, font)
            x = (index % columns) * THUMBNAIL_PX
            y = (index // columns) * THUMBNAIL_PX
            sheet.paste(tile, (x, y))
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_name(f".{path.name}.tmp-{os.getpid()}")
    try:
        sheet.save(temporary, format="PNG", compress_level=6)
        os.replace(temporary, path)
    finally:
        try:
            temporary.unlink()
        except FileNotFoundError:
            pass
    return {
        "path": str(path),
        "status": "ok",
        "thumbnail_count": len(reports),
        "thumbnail_px": THUMBNAIL_PX,
        "grid_columns": columns,
        "grid_rows": rows,
        "bytes": path.stat().st_size,
    }


def _verify_memory(meta: dict[str, Any], log: CheckLog) -> dict[str, Any]:
    guard = _as_dict(meta.get("memoryguard")) or _as_dict(meta.get("memory_guard"))
    if guard is None:
        guard = _as_dict(meta.get("memory"))
    if guard is None:
        log.add("memoryguard", "fail", "metadata lacks memoryguard statistics")
        return {}

    limit, limit_key = _find_number(
        guard, ("limit_bytes", "memory_limit_bytes", "limit_mb", "limit_mib")
    ) or (None, "")
    evidence: dict[str, Any] = {"recorded": guard, "limit_key": limit_key}
    if limit is None:
        log.add("memoryguard", "fail", "memoryguard has no recognizable limit", **evidence)
        return guard
    if "mb" in limit_key.casefold() and "bytes" not in limit_key.casefold():
        limit *= 1024**2
    if limit <= 0 or limit > MEMORY_LIMIT_BYTES:
        log.add(
            "memoryguard",
            "fail",
            f"memory limit {limit} exceeds the 2 GiB contract",
            limit_bytes=limit,
        )
    peaks: list[tuple[str, float]] = []
    for key, value in guard.items():
        normalized = _normal_key(str(key))
        number = _as_number(value)
        if number is None:
            continue
        if "peak" in normalized and (
            "byte" in normalized or "commit" in normalized or normalized.endswith("kib")
        ):
            if normalized.endswith("kib"):
                number *= 1024
            peaks.append((str(key), number))
    if not peaks:
        log.add("memoryguard", "fail", "memoryguard has no recognizable peak usage", **evidence)
    else:
        evidence["peaks"] = peaks
        too_high = [(key, value) for key, value in peaks if value >= MEMORY_LIMIT_BYTES]
        if too_high:
            log.add(
                "memoryguard",
                "fail",
                f"recorded peak memory is not below 2 GiB: {too_high}",
                **evidence,
            )
        else:
            log.add("memoryguard", "pass", "recorded memory peak is below 2 GiB", **evidence)
    return guard


def _check_source(
    meta: dict[str, Any], entry: dict[str, Any], log: CheckLog
) -> dict[str, Any]:
    source = _as_dict(meta.get("source"))
    if source is None:
        log.add("source_provenance", "fail", "metadata lacks a source object")
        return {}
    source_path = source.get("path")
    expected_path = entry.get("source_path")
    source_norm = _normal_path(source_path)
    expected_norm = _normal_path(expected_path)
    if source_norm is None:
        log.add("source_provenance", "fail", "source.path is missing")
    elif expected_norm is not None and source_norm != expected_norm:
        log.add(
            "source_provenance",
            "fail",
            "metadata source.path does not match the catalog",
            metadata_path=source_path,
            catalog_path=expected_path,
        )
    else:
        log.add("source_provenance", "pass", "source path matches the catalog")

    source_size = _as_number(source.get("size"))
    catalog_size = _as_number(entry.get("file_size"))
    if source_size is not None and catalog_size is not None:
        if abs(source_size - catalog_size) > 0.5:
            log.add(
                "source_size",
                "fail",
                "metadata source size differs from the catalog",
                metadata=source_size,
                catalog=catalog_size,
            )
        else:
            log.add("source_size", "pass", "source size agrees with the catalog")
    source_sha = source.get("sha256")
    if isinstance(source_sha, str) and SHA256_RE.fullmatch(source_sha):
        log.add("source_sha256_shape", "pass", "source SHA-256 is recorded")
    else:
        log.add("source_sha256_shape", "fail", "source SHA-256 is missing or malformed")
    if source.get("hash_streamed") is False:
        log.add("source_hash_streaming", "warn", "source was not recorded as stream-hashed")
    elif "hash_streamed" not in source:
        log.add("source_hash_streaming", "warn", "source hash streaming flag is absent")
    else:
        log.add("source_hash_streaming", "pass", "source hash was streamed")
    return source


def _verify_xml(
    xml_report: dict[str, Any] | None,
    map_id: str,
    width: int | None,
    height: int | None,
    meta_altitude: tuple[float | None, float | None],
    log: CheckLog,
) -> tuple[int | None, int | None]:
    if xml_report is None:
        return None, None
    if xml_report["entry_count"] != 1:
        log.add(
            "xml_entries",
            "warn",
            f"XML has {xml_report['entry_count']} CustomHeightmap entries; verifier selected one",
        )
    else:
        log.add("xml_entries", "pass", "XML has one CustomHeightmap entry")
    fields = xml_report["fields"]
    expected_file = f"{map_id}.data"
    if fields.get("File") != expected_file:
        log.add(
            "xml_file",
            "fail",
            f"XML File is {fields.get('File')!r}, expected {expected_file!r}",
        )
    else:
        log.add("xml_file", "pass", "XML references the expected data file")
    xml_width = _as_positive_int(fields.get("Width"))
    xml_height = _as_positive_int(fields.get("Height"))
    if xml_width is None or xml_height is None:
        log.add("xml_dimensions", "fail", "XML Width/Height are missing or invalid")
    elif width is not None and height is not None and (xml_width, xml_height) != (width, height):
        log.add(
            "xml_dimensions",
            "fail",
            "XML dimensions do not match metadata",
            xml=[xml_width, xml_height],
            metadata=[width, height],
        )
    else:
        log.add("xml_dimensions", "pass", "XML dimensions are positive and consistent")
    if fields.get("BigEndian") != "0":
        log.add("xml_endian", "fail", f"XML BigEndian is {fields.get('BigEndian')!r}, expected 0")
    else:
        log.add("xml_endian", "pass", "XML declares little-endian data")
    if (fields.get("Signed") or "").strip().lower() != "true":
        log.add("xml_signed", "fail", "XML Signed must be true")
    else:
        log.add("xml_signed", "pass", "XML declares signed samples")
    factor = _parse_float_text(fields.get("AltitudeFactor"))
    offset = _parse_float_text(fields.get("AltitudeOffset"))
    if factor is None or abs(factor - 1.0) > 1e-12:
        log.add("xml_altitude", "fail", "XML AltitudeFactor must be 1")
    elif offset is None or abs(offset) > 1e-12:
        log.add("xml_altitude", "fail", "XML AltitudeOffset must be 0")
    else:
        log.add("xml_altitude", "pass", "XML altitude factor/offset are 1/0")
        for name, value in zip(("factor", "offset"), meta_altitude):
            if value is not None:
                expected = 1.0 if name == "factor" else 0.0
                if abs(value - expected) > 1e-12:
                    log.add(
                        "metadata_altitude",
                        "fail",
                        f"metadata altitude {name} is {value}, expected {expected}",
                    )
    if (fields.get("Projection") or "").strip().lower() != "quad":
        log.add("xml_projection", "fail", "XML Projection must be Quad")
    else:
        log.add("xml_projection", "pass", "XML uses quad projection")

    attributes = xml_report["customheightmap_attributes"]
    expected_id = f"WarThunder.{map_id}"
    reported_id = attributes.get("ID")
    if reported_id != expected_id:
        log.add(
            "xml_map_id",
            "warn",
            f"XML CustomHeightmap ID is {reported_id!r}, expected {expected_id!r}",
        )
    else:
        log.add("xml_map_id", "pass", "XML map ID matches the package")
    if "Layer" not in attributes:
        log.add("xml_layer", "warn", "XML CustomHeightmap has no Layer attribute")
    else:
        log.add("xml_layer", "pass", "XML CustomHeightmap Layer attribute is present")

    corners = xml_report["corners"]
    valid_corners = True
    corner_values: dict[str, tuple[float, float]] = {}
    for name in ("BottomLeft", "BottomRight", "TopRight", "TopLeft"):
        corner = corners.get(name)
        lon = corner.get("Longitude") if corner else None
        lat = corner.get("Latitude") if corner else None
        if lon is None or lat is None or not (-180 <= lon <= 180) or not (-90 < lat < 90):
            valid_corners = False
            log.add("xml_corners", "fail", f"XML corner {name} is missing or out of range")
        else:
            corner_values[name] = (lon, lat)
    if valid_corners and len(corner_values) == 4:
        bl = corner_values["BottomLeft"]
        br = corner_values["BottomRight"]
        tr = corner_values["TopRight"]
        tl = corner_values["TopLeft"]
        pairing_ok = (
            abs(bl[1] - br[1]) <= LAT_LON_PAIR_TOLERANCE
            and abs(tl[1] - tr[1]) <= LAT_LON_PAIR_TOLERANCE
        )
        # Quad sides need not have equal longitude: the world-to-geographic
        # transform divides X by cos(latitude) separately at each corner.
        nondegenerate = (
            tl[1] - bl[1] > 1e-12
            and tr[1] - br[1] > 1e-12
            and br[0] - bl[0] > 1e-12
            and tr[0] - tl[0] > 1e-12
        )
        if not pairing_ok or not nondegenerate:
            log.add(
                "xml_corners",
                "fail",
                "XML four corners do not form a nondegenerate west/east-south/north quad",
                corners=corner_values,
            )
        else:
            log.add("xml_corners", "pass", "XML four corners are finite, ordered and paired")
    return xml_width, xml_height


def _verify_decoder_dimensions(
    decoder: dict[str, Any],
    meta: dict[str, Any],
    width: int | None,
    height: int | None,
    log: CheckLog,
) -> None:
    mode = str(decoder.get("heightmap_mode") or decoder.get("mode") or "").lower()
    hm2 = _as_dict(decoder.get("hm2")) or {}
    if mode == "hm2_native":
        log.add("native_hm2_decoder", "pass", f"native HM2 decoder mode is {mode!r}")
        dimensions = (_as_positive_int(hm2.get("width")), _as_positive_int(hm2.get("height")))
        if None in dimensions or width is None or height is None:
            log.add("hm2_dimensions", "fail", "native HM2/output dimensions are missing or invalid")
        elif dimensions != (width, height):
            log.add(
                "hm2_dimensions", "fail",
                f"HM2 decoder dimensions {dimensions[0]}x{dimensions[1]} differ from output {width}x{height}",
            )
        else:
            log.add("hm2_dimensions", "pass", "HM2 decoder dimensions match the output")
    elif mode.endswith("_hm2_overlay"):
        # HM2 is a partial patch. Only the full-map base must match output size.
        base = _as_dict(decoder.get("base_decoder_meta")) or {}
        shape = base.get("shape")
        dimensions_ok = (
            width is not None and height is not None
            and isinstance(shape, (list, tuple)) and len(shape) == 2
            and tuple(_as_positive_int(v) for v in shape) == (height, width)
        )
        resolution = base.get("resolution")
        if resolution is not None:
            dimensions_ok = dimensions_ok and (
                isinstance(resolution, (list, tuple)) and len(resolution) == 2
                and tuple(_as_positive_int(v) for v in resolution) == (width, height)
            )
        if dimensions_ok:
            log.add("hm2_overlay_base_dimensions", "pass", "HM2 overlay base dimensions match the output")
        else:
            log.add("hm2_overlay_base_dimensions", "fail", "HM2 overlay base dimensions are missing or differ from output")
        coverage = _as_dict(meta.get("coverage")) or {}
        overlay_pixels = _as_positive_int(coverage.get("hm2_overlay_pixel_count"))
        if (
            overlay_pixels is None or width is None or height is None
            or overlay_pixels > width * height
        ):
            log.add("hm2_overlay_coverage", "fail", "HM2 overlay pixel count must be positive and bounded by output size")
        else:
            log.add("hm2_overlay_coverage", "pass", "HM2 overlay contributes a positive bounded pixel count")


def _extract_coverage(meta: dict[str, Any], log: CheckLog, width: int | None, height: int | None) -> dict[str, Any]:
    coverage = _as_dict(meta.get("coverage")) or {}
    valid = _find_number(
        coverage,
        ("terrain_pixel_count", "valid_pixel_count", "validmask_count", "valid_pixels", "nonzero_height_count"),
    )
    fraction = _find_number(coverage, ("coverage_fraction", "validmask_fraction", "valid_fraction"))
    sample = _find_number(coverage, ("sample_count", "sample_pixels", "pixel_count"))
    allwater_found = _find_bool(meta, ("allwater", "all_water"))
    if valid is not None:
        count = valid[0]
        if count <= 0:
            log.add("validmask", "fail", "coverage reports zero valid pixels", path=valid[1])
        else:
            log.add("validmask", "pass", "coverage has a nonzero valid-pixel count", count=count, path=valid[1])
        if width is not None and height is not None and count > width * height:
            log.add(
                "validmask_bounds",
                "fail",
                f"valid-pixel count {count} exceeds raster cells {width * height}",
            )
    elif fraction is not None:
        if fraction[0] <= 0:
            log.add("validmask", "fail", "coverage fraction is zero", path=fraction[1])
        else:
            log.add("validmask", "pass", "coverage fraction is nonzero", fraction=fraction[0], path=fraction[1])
    else:
        log.add("validmask", "warn", "metadata does not report a valid-mask count or fraction")
    if fraction is not None and not (0 <= fraction[0] <= 1 + 1e-12):
        log.add("coverage_fraction_range", "fail", f"coverage fraction is outside [0,1]: {fraction[0]}")
    if fraction is not None and 0 < fraction[0] < 0.1:
        log.add("coverage_low", "review", f"coverage is very low ({fraction[0]:.4f})")
    if sample is not None and width is not None and height is not None:
        if abs(sample[0] - width * height) > 0.5:
            log.add(
                "coverage_sample_count",
                "fail",
                f"coverage sample count {sample[0]} does not equal {width * height}",
            )
        else:
            log.add("coverage_sample_count", "pass", "coverage sample count equals width*height")
    if allwater_found is not None and allwater_found[0]:
        log.add("allwater", "review", "metadata explicitly marks the raster as all water")
    return {
        "valid_pixel_count": None if valid is None else valid[0],
        "coverage_fraction": None if fraction is None else fraction[0],
        "sample_count": None if sample is None else sample[0],
        "raw": coverage,
    }


def _check_flatness(
    meta: dict[str, Any],
    entry: dict[str, Any],
    data_report: dict[str, Any],
    coverage: dict[str, Any],
    log: CheckLog,
) -> dict[str, Any]:
    low = data_report.get("minimum_m")
    high = data_report.get("maximum_m")
    flat_flag = _find_bool(meta, ("isflat", "is_flat", "allflat", "constant_height"))
    allwater = _find_bool(meta, ("allwater", "all_water"))
    result: dict[str, Any] = {
        "isflat_metadata": None if flat_flag is None else flat_flag[0],
        "isflat_path": None if flat_flag is None else flat_flag[1],
        "constant_data": low is not None and low == high,
        "classification": "normal",
        "water_level_m": entry.get("water_level"),
    }
    if flat_flag is not None and flat_flag[0] and not result["constant_data"]:
        log.add("flatness", "review", "metadata says isflat, but int16 data is not constant")
        result["classification"] = "metadata_inconsistent"
    if result["constant_data"]:
        water_level = _as_number(entry.get("water_level"))
        if water_level is None:
            water_level = _as_number(_find_number(meta, ("water_level", "water_level_m")) and _find_number(meta, ("water_level", "water_level_m"))[0])
        level_delta = None if water_level is None else abs(float(high) - water_level)
        result["water_level_delta_m"] = level_delta
        plausible_sea = level_delta is not None and level_delta <= SEA_LEVEL_TOLERANCE_M
        if plausible_sea:
            result["classification"] = "plausible_sea_surface"
            log.add(
                "flatness",
                "review",
                f"flat int16 raster ({low} m) is near catalog water level {water_level} m; inspect as a possible sea surface",
                water_level_delta_m=level_delta,
            )
        else:
            result["classification"] = "suspicious_flat"
            log.add(
                "flatness",
                "review",
                f"int16 raster is constant at {low} m; inspect for failed or empty decode",
            )
    elif flat_flag is not None and flat_flag[0]:
        log.add("flatness", "review", "metadata reports isflat=true; raster retained for visual review")
        if result["classification"] == "normal":
            result["classification"] = "flat_flag"
    if allwater is not None and allwater[0]:
        result["allwater"] = True
        log.add("flatness", "review", "all-water output is retained for visual review")
    if flat_flag is None and not result["constant_data"]:
        log.add("flatness", "pass", "metadata does not flag flatness and data is nonconstant")
    return result


def _verify_map(
    root: Path,
    entry: dict[str, Any],
    np: Any,
    preview_rows: int,
) -> tuple[dict[str, Any], Any]:
    map_id = str(entry.get("id"))
    folder = root / "maps" / map_id
    data_path = folder / f"{map_id}.data"
    metadata_path = folder / "metadata.json"
    xml_path = folder / "CustomHeightmapList.xml"
    log = CheckLog()
    report: dict[str, Any] = {
        "id": map_id,
        "folder": str(folder),
        "catalog_is_air": _entry_is_air(entry),
        "catalog_source_path": entry.get("source_path"),
        "files": {
            "data": str(data_path),
            "metadata": str(metadata_path),
            "xml": str(xml_path),
        },
        "data": None,
        "metadata": {},
        "xml": None,
        "checks": log.items,
        "errors": log.errors,
        "warnings": log.warnings,
    }
    missing_files = [
        label
        for label, path in (("data", data_path), ("metadata", metadata_path), ("xml", xml_path))
        if not path.is_file()
    ]
    if missing_files:
        report["missing_files"] = missing_files
        log.add("required_files", "fail", f"missing required files: {', '.join(missing_files)}")
    else:
        log.add("required_files", "pass", "all required map files are present")

    meta: dict[str, Any] | None = None
    if metadata_path.is_file():
        try:
            loaded = _load_json_object(metadata_path)
            if not isinstance(loaded, dict):
                raise ValueError("metadata root is not an object")
            meta = loaded
            log.add("metadata_json", "pass", "metadata JSON parses as an object")
        except ValueError as exc:
            log.add("metadata_json", "fail", str(exc))
    if meta is not None:
        metadata_id = meta.get("id")
        output = _as_dict(meta.get("output"))
        output_id = output.get("id") if output else meta.get("id")
        if metadata_id not in (None, map_id) or output_id not in (None, map_id):
            log.add(
                "metadata_id",
                "fail",
                f"metadata id {metadata_id!r}/output id {output_id!r} does not match {map_id!r}",
            )
        else:
            log.add("metadata_id", "pass", "metadata identifiers match the directory")
        status = meta.get("status")
        if status is not None and str(status).lower() not in {"ok", "success", "complete", "completed"}:
            log.add("metadata_status", "fail", f"metadata status is {status!r}")
        elif status is None:
            log.add("metadata_status", "warn", "metadata has no status field")
        else:
            log.add("metadata_status", "pass", f"metadata status is {status!r}")

    xml_report: dict[str, Any] | None = None
    if xml_path.is_file():
        try:
            xml_report = _parse_xml(xml_path, map_id)
            report["xml"] = xml_report
            log.add("xml_parse", "pass", "CustomHeightmapList XML parses")
        except ValueError as exc:
            log.add("xml_parse", "fail", str(exc))

    width = height = None
    data_file = None
    metadata_data_bytes = None
    metadata_sha = None
    metadata_min = metadata_max = None
    bounds = None
    altitude = (None, None)
    if meta is not None:
        width_value, width_path = _output_value(meta, "width")
        height_value, height_path = _output_value(meta, "height")
        data_file_value, data_file_path = _output_value(meta, "data_file")
        bytes_value, bytes_path = _output_value(meta, "data_bytes")
        sha_value, sha_path = _output_value(meta, "data_sha256")
        min_value, min_path = _output_value(meta, "minimum_m")
        max_value, max_path = _output_value(meta, "maximum_m")
        bounds_value, bounds_path = _output_value(meta, "sample_bounds_m")
        width = _as_positive_int(width_value)
        height = _as_positive_int(height_value)
        data_file = data_file_value if isinstance(data_file_value, str) else None
        metadata_data_bytes = _as_number(bytes_value)
        metadata_sha = sha_value if isinstance(sha_value, str) else None
        metadata_min = _as_number(min_value)
        metadata_max = _as_number(max_value)
        bounds = _bounds(bounds_value)
        factor_value, _ = _output_value(meta, "altitude_factor")
        offset_value, _ = _output_value(meta, "altitude_offset")
        altitude = (_as_number(factor_value), _as_number(offset_value))
        missing_core: list[str] = []
        if width is None:
            missing_core.append(width_path)
        if height is None:
            missing_core.append(height_path)
        if metadata_data_bytes is None:
            missing_core.append(bytes_path)
        if metadata_sha is None:
            missing_core.append(sha_path)
        if metadata_min is None:
            missing_core.append(min_path)
        if metadata_max is None:
            missing_core.append(max_path)
        if bounds is None:
            missing_core.append(bounds_path)
        if missing_core:
            log.add("metadata_core", "fail", f"missing or invalid output fields: {missing_core}")
        else:
            log.add("metadata_core", "pass", "required output fields are present")
        output_type, _ = _output_value(meta, "output_type")
        row_order, _ = _output_value(meta, "output_row_order")
        unit, _ = _output_value(meta, "output_unit")
        for label, actual, expected in (
            ("output_type", output_type, "int16_le"),
            ("output_row_order", row_order, "north_to_south"),
            ("output_unit", unit, "meter"),
        ):
            if actual is None:
                log.add(f"metadata_{label}", "warn", f"metadata lacks {label}; intrinsic encoding checks continue")
            elif str(actual).strip().lower() != expected:
                log.add(f"metadata_{label}", "fail", f"metadata {label} is {actual!r}, expected {expected!r}")
            else:
                log.add(f"metadata_{label}", "pass", f"metadata {label} is {actual!r}")
        quantization, _ = _output_value(meta, "quantization_max_error_m")
        quantization_value = _as_number(quantization)
        if quantization_value is not None and quantization_value > HALF_METER_TOLERANCE:
            log.add("metadata_quantization", "fail", f"quantization error {quantization_value} exceeds 0.5 m")
        report["metadata"] = {
            "schema_version": meta.get("schema_version"),
            "outer_status": meta.get("status"),
            "output": output or {},
            "coverage": _as_dict(meta.get("coverage")) or {},
            "library_keys": sorted(str(key) for key in meta.keys()),
        }

    xml_width = xml_height = None
    if width is None and xml_report is not None:
        width = _as_positive_int(xml_report["fields"].get("Width"))
        height = _as_positive_int(xml_report["fields"].get("Height"))
    if xml_report is not None:
        xml_width, xml_height = _verify_xml(
            xml_report, map_id, width, height, altitude, log
        )
    if width is not None and height is not None and width * height > 64_000_000:
        log.add("raster_dimensions", "fail", f"raster has {width * height} cells; limit is 64,000,000")
    elif width is not None and height is not None:
        log.add("raster_dimensions", "pass", f"raster dimensions are {width}x{height}")

    if bounds is not None:
        x0, z0, x1, z1 = bounds
        if x1 <= x0 or z1 <= z0:
            log.add("sample_bounds", "fail", "sample_bounds_m is empty or reversed")
        else:
            log.add("sample_bounds", "pass", "sample_bounds_m is finite and ordered")
        catalog0 = entry.get("map_coord0")
        catalog1 = entry.get("map_coord1")
        if (
            isinstance(catalog0, (list, tuple)) and len(catalog0) >= 2
            and isinstance(catalog1, (list, tuple)) and len(catalog1) >= 2
            and all(_as_number(value) is not None for value in list(catalog0[:2]) + list(catalog1[:2]))
        ):
            cx0, cz0 = float(catalog0[0]), float(catalog0[1])
            cx1, cz1 = float(catalog1[0]), float(catalog1[1])
            epsilon = 1e-6
            contained = (
                x0 >= min(cx0, cx1) - epsilon
                and x1 <= max(cx0, cx1) + epsilon
                and z0 >= min(cz0, cz1) - epsilon
                and z1 <= max(cz0, cz1) + epsilon
            )
            if not contained:
                log.add(
                    "catalog_bounds",
                    "fail",
                    "sample_bounds_m lies outside catalog map coordinates",
                    output=bounds,
                    catalog=[catalog0, catalog1],
                )
            else:
                log.add("catalog_bounds", "pass", "sample_bounds_m lies within catalog coordinates")
    if metadata_sha is not None and not SHA256_RE.fullmatch(metadata_sha):
        log.add("metadata_sha_shape", "fail", "metadata data_sha256 is malformed")

    decoder = None
    memory = {}
    coverage = {}
    source = {}
    if meta is not None:
        source = _check_source(meta, entry, log)
        memory = _verify_memory(meta, log)
        coverage = _extract_coverage(meta, log, width, height)
        decoder = _as_dict(meta.get("format_decoder_info")) or _as_dict(meta.get("decoder"))
        if decoder is None:
            log.add("decoder_provenance", "warn", "metadata lacks decoder provenance")
        else:
            log.add("decoder_provenance", "pass", "decoder provenance is recorded")
            _verify_decoder_dimensions(decoder, meta, width, height, log)

    data_report = None
    if data_path.is_file():
        try:
            data_report = _scan_data(data_path, np)
            report["data"] = data_report
            log.add("data_stream", "pass", "data SHA-256 and int16 extrema were streamed")
        except ValueError as exc:
            log.add("data_stream", "fail", str(exc))

    if data_report is not None:
        actual_bytes = int(data_report["bytes"])
        if data_report["odd_trailing_byte"]:
            log.add("data_even_bytes", "fail", "data ends with an unpaired byte")
        if width is not None and height is not None:
            expected_bytes = width * height * 2
            if actual_bytes != expected_bytes:
                log.add(
                    "data_size_formula",
                    "fail",
                    f"data is {actual_bytes} bytes; 2*width*height is {expected_bytes}",
                )
            else:
                log.add("data_size_formula", "pass", "data size equals 2*width*height")
            if metadata_data_bytes is not None and int(metadata_data_bytes) != actual_bytes:
                log.add(
                    "metadata_data_bytes",
                    "fail",
                    f"metadata data_bytes is {metadata_data_bytes}, file is {actual_bytes}",
                )
            elif metadata_data_bytes is not None:
                log.add("metadata_data_bytes", "pass", "metadata data_bytes matches the file")
        if data_file is not None and data_file != data_path.name:
            log.add("metadata_data_file", "fail", f"metadata data_file is {data_file!r}, expected {data_path.name!r}")
        data_sha = data_report["sha256"]
        if metadata_sha is not None:
            if data_sha.lower() != metadata_sha.lower():
                log.add(
                    "data_sha256",
                    "fail",
                    "streamed data SHA-256 differs from metadata",
                    computed=data_sha,
                    metadata=metadata_sha,
                )
            else:
                log.add("data_sha256", "pass", "streamed data SHA-256 matches metadata")
        low = data_report.get("minimum_m")
        high = data_report.get("maximum_m")
        if low is None or high is None:
            log.add("data_extrema", "fail", "data contains no complete int16 samples")
        else:
            if low < -32768 or high > 32767:
                log.add("data_int16", "fail", "data extrema exceed signed int16")
            else:
                log.add("data_int16", "pass", "data samples are signed int16")
            if metadata_min is not None and metadata_max is not None:
                comparisons: dict[str, Any] = {}
                extrema_ok = True
                half_meter_ok = True
                for label, metadata_value, data_value in (
                    ("minimum_m", metadata_min, low),
                    ("maximum_m", metadata_max, high),
                ):
                    rounded = int(round(metadata_value))
                    delta = abs(float(data_value) - metadata_value)
                    round_ok = rounded == data_value
                    within_half = delta <= HALF_METER_TOLERANCE
                    extrema_ok = extrema_ok and round_ok
                    half_meter_ok = half_meter_ok and within_half
                    comparisons[label] = {
                        "metadata": metadata_value,
                        "round_metadata": rounded,
                        "data": data_value,
                        "difference_m": delta,
                        "round_matches": round_ok,
                        "within_0_5_m": within_half,
                    }
                data_report["meta_comparison"] = comparisons
                if not extrema_ok:
                    log.add("minmax_rounding", "fail", "int16 extrema do not match round(metadata extrema)", **comparisons)
                else:
                    log.add("minmax_rounding", "pass", "int16 extrema match round(metadata extrema)", **comparisons)
                if not half_meter_ok:
                    log.add("minmax_quantization", "fail", "source float extrema differ from int16 by more than 0.5 m")
                else:
                    log.add("minmax_quantization", "pass", "source extrema differ from int16 by at most 0.5 m")
        if width is not None and height is not None and actual_bytes == width * height * 2:
            try:
                preview, preview_meta = _make_preview(
                    data_path, width, height, int(low), int(high), preview_rows, np
                )
                report["preview"] = preview_meta
            except (OSError, ValueError) as exc:
                preview = None
                log.add("preview", "warn", f"could not sample preview: {exc}")
            else:
                log.add("preview", "pass", "preview was sampled from a bounded number of rows")
        else:
            preview = None

    if data_report is None:
        preview = None
    if meta is not None and data_report is not None:
        flatness = _check_flatness(meta, entry, data_report, coverage, log)
        report["flatness"] = flatness
        if flatness.get("classification") in {"suspicious_flat", "metadata_inconsistent"}:
            report["suspicious"] = True
        elif flatness.get("classification") in {"plausible_sea_surface", "flat_flag"}:
            report["suspicious"] = True
    report["source"] = source
    report["decoder"] = decoder or {}
    report["memoryguard"] = memory
    report["coverage"] = coverage
    report["checks"] = log.items
    report["errors"] = log.errors
    report["warnings"] = log.warnings
    report["status"] = log.status
    return report, preview


def verify_package(
    root: Path,
    catalog_path: Path,
    np: Any,
    preview_rows: int,
) -> tuple[dict[str, Any], dict[str, Any]]:
    top_errors: list[str] = []
    top_warnings: list[str] = []
    try:
        document = _load_json_object(catalog_path)
        entries = _catalog_entries(document)
    except ValueError as exc:
        return {
            "schema_version": TOOL_VERSION,
            "tool": "verify_outputs.py",
            "generated_utc": _utc_now(),
            "root": str(root),
            "catalog": {"path": str(catalog_path)},
            "status": "fail",
            "errors": [str(exc)],
            "warnings": [],
            "summary": {},
            "maps": [],
        }, {}

    by_id: dict[str, dict[str, Any]] = {}
    duplicate_ids: list[str] = []
    for entry in entries:
        map_id = entry.get("id")
        if not isinstance(map_id, str) or not map_id:
            top_warnings.append("catalog entry without a usable id was ignored")
            continue
        if not SAFE_ID.fullmatch(map_id):
            top_errors.append(f"catalog id is not path-safe: {map_id!r}")
            continue
        if map_id in by_id:
            duplicate_ids.append(map_id)
        else:
            by_id[map_id] = entry
    required_entries = [
        entry for entry in by_id.values() if _entry_is_air(entry) and _entry_exists(entry)
    ]
    required_entries.sort(key=lambda item: str(item["id"]).casefold())
    required_ids = [str(entry["id"]) for entry in required_entries]
    air_without_existing_source = sorted(
        str(entry["id"])
        for entry in by_id.values()
        if _entry_is_air(entry) and not _entry_exists(entry)
    )
    if not required_ids:
        top_errors.append("catalog contains no existing is_air targets")
    if duplicate_ids:
        top_errors.append(f"duplicate catalog ids: {sorted(set(duplicate_ids))}")

    maps_dir = root / "maps"
    package_dirs = (
        sorted(
            (item.name for item in maps_dir.iterdir() if item.is_dir()),
            key=str.casefold,
        )
        if maps_dir.is_dir()
        else []
    )
    if not maps_dir.is_dir():
        top_errors.append(f"package maps directory does not exist: {maps_dir}")
    extra_dirs = [name for name in package_dirs if name not in set(required_ids)]
    if extra_dirs:
        top_warnings.append(f"package has directories outside the existing is_air manifest: {extra_dirs}")

    reports: list[dict[str, Any]] = []
    previews: dict[str, Any] = {}
    for entry in required_entries:
        report, preview = _verify_map(root, entry, np, preview_rows)
        reports.append(report)
        if preview is not None:
            previews[report["id"]] = preview

    missing_required = [
        report["id"]
        for report in reports
        if report.get("status") == "fail" and report.get("missing_files")
    ]
    complete = sum(1 for report in reports if not report.get("missing_files"))
    passed = sum(1 for report in reports if report["status"] == "pass")
    review = sum(1 for report in reports if report["status"] == "review")
    failed = sum(1 for report in reports if report["status"] == "fail")
    suspicious_ids = [report["id"] for report in reports if report.get("suspicious")]
    map_errors = [error for report in reports for error in report.get("errors", [])]
    map_warnings = [warning for report in reports for warning in report.get("warnings", [])]
    if failed:
        status = "fail"
    elif top_errors:
        status = "fail"
    elif review or top_warnings or map_warnings:
        status = "needs_review"
    else:
        status = "pass"
    return {
        "schema_version": TOOL_VERSION,
        "tool": "verify_outputs.py",
        "generated_utc": _utc_now(),
        "root": str(root),
        "catalog": {
            "path": str(catalog_path),
            "entries": len(entries),
            "unique_ids": len(by_id),
            "air_total": sum(1 for entry in by_id.values() if _entry_is_air(entry)),
            "required_existing_air": len(required_ids),
            "air_without_existing_source": air_without_existing_source,
        },
        "manifest": {
            "required_ids": required_ids,
            "package_map_dirs": package_dirs,
            "missing_required": missing_required,
            "extra_package_dirs": extra_dirs,
        },
        "summary": {
            "required": len(required_ids),
            "present_complete": complete,
            "passed": passed,
            "review": review,
            "failed": failed,
            "missing": len(missing_required),
            "suspicious": len(suspicious_ids),
            "map_errors": len(map_errors),
            "map_warnings": len(map_warnings),
        },
        "suspicious_ids": suspicious_ids,
        "status": status,
        "errors": top_errors + map_errors,
        "warnings": top_warnings + map_warnings,
        "maps": reports,
    }, previews


def _write_json(path: Path, report: dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_name(f".{path.name}.tmp-{os.getpid()}")
    try:
        temporary.write_text(
            json.dumps(_json_safe(report), ensure_ascii=False, indent=2) + "\n",
            encoding="utf-8",
        )
        os.replace(temporary, path)
    finally:
        try:
            temporary.unlink()
        except FileNotFoundError:
            pass


def _runtime_memory(guard: MemoryGuard) -> dict[str, Any]:
    stats = guard.stats()
    values: dict[str, Any] = {"limit_bytes": guard.limit, "stats": stats}
    peaks: list[tuple[str, float]] = []
    for key, value in stats.items():
        normalized = _normal_key(str(key))
        number = _as_number(value)
        if number is not None and "peak" in normalized:
            peaks.append((str(key), number))
    values["peaks"] = peaks
    values["below_2gib"] = all(value < MEMORY_LIMIT_BYTES for _, value in peaks) if peaks else False
    return values


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, required=True, help="terrain package root containing maps/")
    parser.add_argument("--catalog", type=Path, required=True, help="catalog.json path")
    parser.add_argument("--verification-json", type=Path, help="default: <root>/verification.json")
    parser.add_argument("--overview", type=Path, help="default: <root>/overview.png")
    parser.add_argument(
        "--preview-rows",
        type=int,
        default=PREVIEW_SOURCE_ROWS,
        help=f"number of source rows sampled for thumbnails (default {PREVIEW_SOURCE_ROWS})",
    )
    parser.add_argument(
        "--fail-on-review",
        action="store_true",
        help="return nonzero when suspicious/low-coverage maps require visual review",
    )
    args = parser.parse_args(argv)
    args.root = args.root.resolve(strict=False)
    args.catalog = args.catalog.resolve(strict=False)
    args.verification_json = (
        args.verification_json.resolve(strict=False)
        if args.verification_json is not None
        else args.root / "verification.json"
    )
    args.overview = (
        args.overview.resolve(strict=False)
        if args.overview is not None
        else args.root / "overview.png"
    )
    if args.preview_rows < 2:
        parser.error("--preview-rows must be at least 2")

    # This must precede NumPy and Pillow.  Their import allocates native memory
    # and would otherwise fall outside the intended per-process job budget.
    guard = MemoryGuard(MEMORY_LIMIT_BYTES)
    try:
        import numpy as np
    except Exception as exc:
        print(f"cannot import numpy after MemoryGuard: {exc}")
        return 2

    report, previews = verify_package(
        args.root, args.catalog, np, min(args.preview_rows, 1024)
    )
    try:
        from PIL import Image, ImageDraw, ImageFont
    except Exception as exc:
        report.setdefault("errors", []).append(f"cannot import Pillow for overview: {exc}")
        report.setdefault("warnings", [])
        report["overview"] = {"path": str(args.overview), "status": "failed", "error": str(exc)}
        report["status"] = "fail"
    else:
        try:
            overview = _render_overview(
                args.overview, report.get("maps", []), previews, Image, ImageDraw, ImageFont
            )
            report["overview"] = overview
        except Exception as exc:
            report.setdefault("errors", []).append(f"overview rendering failed: {exc}")
            report["overview"] = {"path": str(args.overview), "status": "failed", "error": str(exc)}
            report["status"] = "fail"

    runtime = _runtime_memory(guard)
    report["runtime_memory"] = runtime
    if not runtime.get("below_2gib"):
        report.setdefault("errors", []).append("verification process peak memory was not proven below 2 GiB")
        report["status"] = "fail"
    _write_json(args.verification_json, report)
    summary = report.get("summary", {})
    print(
        f"verification status={report.get('status')} "
        f"required={summary.get('required', 0)} complete={summary.get('present_complete', 0)} "
        f"pass={summary.get('passed', 0)} review={summary.get('review', 0)} "
        f"fail={summary.get('failed', 0)} suspicious={summary.get('suspicious', 0)}"
    )
    print(f"wrote {args.verification_json}")
    print(f"wrote {args.overview}")
    if report.get("status") == "fail":
        return 1
    if report.get("status") == "needs_review" and args.fail_on_review:
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

#!/usr/bin/env python3
"""Decode one air-map terrain entry into Tacview custom heightmap files.

The process installs its MemoryGuard before importing numpy or the codec
backend.  All JSON writes are atomic; a failure still leaves result.json.
"""

from __future__ import annotations

import argparse
from datetime import datetime, timezone
import hashlib
import importlib
import inspect
import json
import math
import os
from pathlib import Path
import platform
import re
import sys
import time
from typing import Any


SCHEMA_VERSION = 1
HASH_CHUNK = 8 << 20
MAP_ID_RE = re.compile(r"[A-Za-z0-9][A-Za-z0-9._-]{0,127}\Z")


class MissingSource(ValueError):
    pass


def utc_now() -> str:
    return datetime.now(timezone.utc).isoformat(timespec="seconds")


def atomic_write_json(path: Path, value: Any) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    text = json.dumps(
        value, ensure_ascii=False, indent=2, allow_nan=False, sort_keys=False
    ) + "\n"
    temporary = path.with_name(path.name + f".tmp.{os.getpid()}")
    try:
        with temporary.open("w", encoding="utf-8", newline="\n") as handle:
            handle.write(text)
            handle.flush()
            os.fsync(handle.fileno())
        os.replace(temporary, path)
    finally:
        if temporary.exists():
            try:
                temporary.unlink()
            except OSError:
                pass


def stream_sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        while True:
            data = handle.read(HASH_CHUNK)
            if not data:
                break
            digest.update(data)
    return digest.hexdigest()


def source_snapshot(path: Path) -> dict[str, Any]:
    try:
        stat = path.stat()
        if not path.is_file():
            raise MissingSource(f"source is not a regular file: {path}")
        digest = stream_sha256(path)
    except MissingSource:
        raise
    except OSError as exc:
        raise ValueError(f"cannot read source {path}: {exc}") from exc
    return {
        "path": str(path),
        "size": int(stat.st_size),
        "mtime_ns": int(stat.st_mtime_ns),
        "mtime_utc": datetime.fromtimestamp(
            stat.st_mtime, timezone.utc
        ).isoformat(timespec="seconds"),
        "sha256": digest,
        "hash_algorithm": "sha256",
        "hash_streamed": True,
        "hash_chunk_bytes": HASH_CHUNK,
    }


def short_error(exc: BaseException, limit: int = 600) -> str:
    text = " ".join(str(exc).split())
    return f"{type(exc).__name__}: {text[:limit]}"


def jsonable(value: Any, np: Any) -> Any:
    if isinstance(value, np.ndarray):
        raise ValueError("numpy arrays must be summarized before JSON output")
    if isinstance(value, np.generic):
        return value.item()
    if isinstance(value, Path):
        return str(value)
    if isinstance(value, dict):
        return {str(key): jsonable(item, np) for key, item in value.items()}
    if isinstance(value, (list, tuple)):
        return [jsonable(item, np) for item in value]
    return value


def load_entry(path: Path) -> dict[str, Any]:
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise ValueError(f"cannot read entry JSON {path}: {exc}") from exc
    if not isinstance(value, dict):
        raise ValueError("entry JSON must contain one object")
    return value


def checked_id(entry: dict[str, Any]) -> str:
    value = entry.get("id")
    if not isinstance(value, str) or not MAP_ID_RE.fullmatch(value):
        raise ValueError(f"invalid map id in entry: {value!r}")
    return value


def checked_bounds(entry: dict[str, Any]) -> tuple[float, float, float, float]:
    first = entry.get("map_coord0")
    second = entry.get("map_coord1")
    if not isinstance(first, (list, tuple)) or len(first) != 2:
        raise ValueError("entry map_coord0 must be [x, z]")
    if not isinstance(second, (list, tuple)) or len(second) != 2:
        raise ValueError("entry map_coord1 must be [x, z]")
    try:
        x0, z0, x1, z1 = float(first[0]), float(first[1]), float(second[0]), float(second[1])
    except (TypeError, ValueError) as exc:
        raise ValueError("entry map coordinates must be finite numbers") from exc
    if not all(math.isfinite(v) for v in (x0, z0, x1, z1)):
        raise ValueError("entry map coordinates must be finite")
    if x0 >= x1 or z0 >= z1:
        raise ValueError(f"invalid map bounds: x={x0}..{x1}, z={z0}..{z1}")
    return x0, x1, z0, z1


def checked_water_level(entry: dict[str, Any], warnings: list[str]) -> float:
    value = entry.get("water_level")
    if value is None:
        warnings.append("water_level is absent in catalog; using 0 m for uncovered lmap pixels")
        return 0.0
    try:
        result = float(value)
    except (TypeError, ValueError) as exc:
        raise ValueError(f"invalid water_level: {value!r}") from exc
    if not math.isfinite(result):
        raise ValueError(f"invalid water_level: {value!r}")
    return result


def config_snapshot(value: Any) -> dict[str, Any] | None:
    if value is None:
        return None
    if not isinstance(value, str) or not value:
        raise ValueError("catalog config_path is invalid")
    path = Path(value)
    if not path.is_file():
        raise ValueError(f"catalog level config is missing: {path}")
    size = path.stat().st_size
    if size > 2 * 1024**2:
        raise ValueError(f"catalog level config exceeds 2 MiB: {path}")
    return {
        "path": str(path),
        "size": size,
        "sha256": stream_sha256(path),
        "hash_algorithm": "sha256",
    }


def is_all_water(entry: dict[str, Any]) -> bool:
    return entry.get("allwater") is True or entry.get("is_all_water") is True


def scan_actual_blocks(path: Path, container: Any) -> dict[str, bool]:
    tags = {block.tag for block in container.iter_blocks(path)}
    return {
        "has_hm2": b"\x00HM2" in tags,
        "has_lmap": b"lmap" in tags,
        "has_frt": any(tag in (b"\x00FRT", b"FRT") for tag in tags),
    }


def hm2_meters(grid: Any, meta: dict[str, Any], np: Any) -> Any:
    height = grid.astype(np.float32, copy=True)
    height *= np.float32(float(meta["hscale"]) / 65535.0)
    height += np.float32(float(meta["hmin"]))
    if not np.isfinite(height).all():
        raise ValueError("HM2 decode produced non-finite metric heights")
    return np.ascontiguousarray(height, dtype=np.float32)


def hm2_sample_bounds(meta: dict[str, Any]) -> list[float]:
    cell = float(meta["cell"])
    x0 = float(meta["origin_x"])
    z0 = float(meta["origin_z"])
    width = int(meta["width"])
    height = int(meta["height"])
    return [x0, z0, x0 + (width - 1) * cell, z0 + (height - 1) * cell]


def hm2_cell_bounds(meta: dict[str, Any]) -> tuple[float, float, float, float]:
    cell = float(meta["cell"])
    x0 = float(meta["origin_x"])
    z0 = float(meta["origin_z"])
    width = int(meta["width"])
    height = int(meta["height"])
    return x0, x0 + width * cell, z0, z0 + height * cell


def hm2_covers_map(
    cell_bounds: tuple[float, float, float, float],
    map_bounds: tuple[float, float, float, float],
    cell: float,
) -> bool:
    hx0, hx1, hz0, hz1 = cell_bounds
    mx0, mx1, mz0, mz1 = map_bounds
    tolerance = max(0.001, abs(cell) * 1e-4)
    return (
        hx0 <= mx0 + tolerance
        and hx1 >= mx1 - tolerance
        and hz0 <= mz0 + tolerance
        and hz1 >= mz1 - tolerance
    )


def lmap_sample_bounds(
    map_bounds: tuple[float, float, float, float], resolution: int
) -> list[float]:
    x0, x1, z0, z1 = map_bounds
    dx = (x1 - x0) / resolution
    dz = (z1 - z0) / resolution
    if not (math.isfinite(dx) and math.isfinite(dz)) or dx <= 0 or dz <= 0:
        raise ValueError("invalid lmap output pixel size")
    return [x0 + dx / 2.0, z0 + dz / 2.0, x1 - dx / 2.0, z1 - dz / 2.0]


def decoder_meta_summary(meta: dict[str, Any], np: Any) -> dict[str, Any]:
    mask = meta.get("mask")
    water_mask = meta.get("water_mask")
    if not isinstance(mask, np.ndarray):
        raise ValueError("decoder metadata has no numpy terrain mask")
    if not isinstance(water_mask, np.ndarray):
        raise ValueError("decoder metadata has no numpy water_mask")
    valid = int(np.count_nonzero(mask))
    water = int(np.count_nonzero(water_mask))
    total = int(mask.size)
    result: dict[str, Any] = {}
    for key, value in meta.items():
        if key in ("mask", "terrain_mask", "water_mask"):
            continue
        if isinstance(value, np.ndarray):
            result[str(key)] = {
                "summarized_array": True,
                "shape": list(value.shape),
                "dtype": str(value.dtype),
                "size": int(value.size),
            }
        else:
            result[str(key)] = jsonable(value, np)
    result["mask_summary"] = {
        "shape": list(mask.shape),
        "dtype": str(mask.dtype),
        "valid_pixels": valid,
        "coverage_fraction": valid / total if total else 0.0,
    }
    result["water_mask_summary"] = {
        "shape": list(water_mask.shape),
        "dtype": str(water_mask.dtype),
        "water_pixels": water,
        "water_fraction": water / total if total else 0.0,
    }
    return result


def overlay_hm2(
    height: Any,
    mask: Any,
    hm2_height: Any,
    hm2_meta: dict[str, Any],
    map_bounds: tuple[float, float, float, float],
    resolution: int,
    np: Any,
) -> tuple[int, Any]:
    grid_h, grid_w = height.shape
    source_h, source_w = hm2_height.shape
    if source_h < 2 or source_w < 2:
        raise ValueError("partial HM2 overlay requires at least 2x2 samples")
    x0, x1, z0, z1 = map_bounds
    dx = (x1 - x0) / resolution
    dz = (z1 - z0) / resolution
    origin_x = float(hm2_meta["origin_x"])
    origin_z = float(hm2_meta["origin_z"])
    cell = float(hm2_meta["cell"])
    sample_x_max = origin_x + (source_w - 1) * cell
    sample_z_max = origin_z + (source_h - 1) * cell

    overlay_mask = np.zeros((grid_h, grid_w), dtype=bool)
    x_centers = x0 + (np.arange(grid_w, dtype=np.float64) + 0.5) * dx
    columns = (x_centers - origin_x) / cell
    inside_x = (columns >= 0.0) & (columns <= source_w - 1)
    count = 0

    for row in range(grid_h):
        z = z0 + (row + 0.5) * dz
        source_row = (z - origin_z) / cell
        if source_row < 0.0 or source_row > source_h - 1:
            continue
        valid = inside_x.copy()
        if not valid.any():
            continue
        column = columns[valid]
        c0 = np.floor(column).astype(np.int64)
        c1 = np.minimum(c0 + 1, source_w - 1)
        fx = column - c0
        r0 = int(math.floor(source_row))
        r1 = min(r0 + 1, source_h - 1)
        fz = source_row - r0

        top = (
            hm2_height[r0, c0].astype(np.float64) * (1.0 - fx)
            + hm2_height[r0, c1].astype(np.float64) * fx
        )
        bottom = (
            hm2_height[r1, c0].astype(np.float64) * (1.0 - fx)
            + hm2_height[r1, c1].astype(np.float64) * fx
        )
        height[row, valid] = (top * (1.0 - fz) + bottom * fz).astype(np.float32)
        overlay_mask[row, valid] = True
        count += int(np.count_nonzero(valid))

    if sample_x_max < x0 or origin_x > x1 or sample_z_max < z0 or origin_z > z1:
        return 0, overlay_mask
    return count, overlay_mask


def coverage_stats(
    height: Any,
    mask: Any,
    water_mask: Any,
    sample_bounds: list[float],
    np: Any,
) -> dict[str, Any]:
    h, w = height.shape
    count = int(np.count_nonzero(mask))
    water_count = int(np.count_nonzero(water_mask))
    result: dict[str, Any] = {
        "sample_count": int(h * w),
        "terrain_pixel_count": count,
        "coverage_fraction": count / (h * w) if h * w else 0.0,
        "water_pixel_count": water_count,
        "water_fraction": water_count / (h * w) if h * w else 0.0,
        "min_height_m": None,
        "max_height_m": None,
        "bbox_pixel": None,
        "bbox_m": None,
        "mask_semantics": "terrain samples covered by HM2 or a decoded lmap triangle",
    }
    if count:
        values = height[mask]
        result["min_height_m"] = float(values.min())
        result["max_height_m"] = float(values.max())
        rows = np.flatnonzero(mask.any(axis=1))
        columns = np.flatnonzero(mask.any(axis=0))
        row0, row1 = int(rows[0]), int(rows[-1])
        col0, col1 = int(columns[0]), int(columns[-1])
        result["bbox_pixel"] = [col0, row0, col1, row1]
        x0, z0, x1, z1 = sample_bounds
        x_at = lambda col: x0 + (x1 - x0) * col / max(1, w - 1)
        z_at = lambda row: z0 + (z1 - z0) * row / max(1, h - 1)
        result["bbox_m"] = [
            float(x_at(col0)),
            float(z_at(row0)),
            float(x_at(col1)),
            float(z_at(row1)),
        ]
    return result


def optional_import(name: str) -> tuple[Any | None, str | None]:
    try:
        return importlib.import_module(name), None
    except ImportError as exc:
        return None, short_error(exc)


def call_decoder(
    function: Any,
    source: Path,
    decompress: Any,
    map_bounds: tuple[float, float, float, float],
    resolution: int,
    water_level: float,
) -> Any:
    parameters = inspect.signature(function).parameters
    kwargs = {
        "bounds": map_bounds,
        "resolution": resolution,
        "water_level": water_level,
    }
    if "decompress" in parameters:
        return function(source, decompress, **kwargs)
    return function(source, **kwargs)


def validate_decoder_result(
    result: Any, width: int, height: int, np: Any, label: str
) -> tuple[Any, dict[str, Any], Any, Any]:
    if not isinstance(result, tuple) or len(result) != 2:
        raise ValueError(f"{label} must return (height, metadata)")
    heights, metadata = result
    if not isinstance(metadata, dict):
        raise ValueError(f"{label} metadata must be a dict")
    heights = np.ascontiguousarray(np.asarray(heights, dtype=np.float32))
    if heights.shape != (height, width):
        raise ValueError(
            f"{label} returned shape {heights.shape}, expected {(height, width)}"
        )
    if not np.isfinite(heights).all():
        raise ValueError(f"{label} returned non-finite heights")
    mask = metadata.get("mask")
    water_mask = metadata.get("water_mask")
    if not isinstance(mask, np.ndarray) or mask.shape != heights.shape:
        raise ValueError(f"{label} terrain mask is missing or has the wrong shape")
    if not isinstance(water_mask, np.ndarray) or water_mask.shape != heights.shape:
        raise ValueError(f"{label} water_mask is missing or has the wrong shape")
    return (
        heights,
        metadata,
        mask.astype(bool, copy=False),
        water_mask.astype(bool, copy=False),
    )


def is_no_tracer_error(exc: BaseException) -> bool:
    return "no LandRayTracer" in str(exc)


def decode_base_map(
    source: Path,
    actual: dict[str, bool],
    map_bounds: tuple[float, float, float, float],
    resolution: int,
    water_level: float,
    allwater: bool,
    np: Any,
    codec: Any,
    lmap: Any,
    frt_module: Any | None,
    render_mesh_module: Any | None,
    module_errors: dict[str, str | None],
) -> tuple[str, Any, dict[str, Any], Any, Any, list[str]]:
    warnings: list[str] = []
    allow_empty = bool(allwater or actual["has_hm2"])

    # FRT is an additional scene collision dump, not an exclusive terrain
    # format. Prefer the lmap terrain surface even when FRT is also present.
    if actual["has_frt"] and not actual["has_lmap"]:
        if frt_module is None:
            raise ValueError(
                "FRT block is present but frt.decode_frt is unavailable: "
                + (module_errors.get("frt") or "module not installed")
            )
        if not callable(getattr(frt_module, "decode_frt", None)):
            raise ValueError("FRT block is present but frt.decode_frt is not callable")
        try:
            height, metadata, mask, water_mask = validate_decoder_result(
                call_decoder(
                    frt_module.decode_frt,
                    source,
                    codec.decompress,
                    map_bounds,
                    resolution,
                    water_level,
                ),
                resolution,
                resolution,
                np,
                "frt",
            )
        except Exception as exc:
            raise ValueError(f"frt decode failed: {short_error(exc)}") from exc
        if int(np.count_nonzero(mask)) == 0 and not allow_empty:
            raise ValueError("frt returned an empty terrain mask")
        return "frt", height, metadata, mask, water_mask, warnings

    if not actual["has_lmap"]:
        raise ValueError("no supported terrain decoder: no FRT and no lmap block")
    try:
        height, metadata, mask, water_mask = validate_decoder_result(
            call_decoder(
                lmap.decode_lmap,
                source,
                codec.decompress,
                map_bounds,
                resolution,
                water_level,
            ),
            resolution,
            resolution,
            np,
            "lmap",
        )
    except Exception as exc:
        if not is_no_tracer_error(exc):
            raise ValueError(f"lmap decode failed: {short_error(exc)}") from exc
        warnings.append("lmap has no LandRayTracer; using render_mesh fallback")
        if render_mesh_module is None:
            raise ValueError(
                "lmap has no LandRayTracer and render_mesh.decode_render_mesh is unavailable: "
                + (module_errors.get("render_mesh") or "module not installed")
            ) from exc
        function = getattr(render_mesh_module, "decode_render_mesh", None)
        if not callable(function):
            raise ValueError(
                "lmap has no LandRayTracer and render_mesh.decode_render_mesh is not callable"
            ) from exc
        try:
            height, metadata, mask, water_mask = validate_decoder_result(
                call_decoder(
                    function,
                    source,
                    codec.decompress,
                    map_bounds,
                    resolution,
                    water_level,
                ),
                resolution,
                resolution,
                np,
                "render_mesh",
            )
        except Exception as render_exc:
            raise ValueError(
                f"render_mesh fallback failed: {short_error(render_exc)}"
            ) from render_exc
        if int(np.count_nonzero(mask)) == 0 and not allow_empty:
            raise ValueError("render_mesh returned an empty terrain mask")
        return "render_mesh", height, metadata, mask, water_mask, warnings

    if int(np.count_nonzero(mask)) == 0 and not allow_empty:
        raise ValueError(
            "lmap returned an empty terrain mask; set allwater=true only for a water-only map"
        )
    return "lmap", height, metadata, mask, water_mask, warnings


def write_error_result(
    output: Path,
    map_id: str,
    status: str,
    error: str,
    started_utc: str,
    elapsed: float,
    memory: dict[str, Any] | None,
) -> Path:
    folder = output / "maps" / map_id
    path = folder / "result.json"
    value = {
        "schema_version": SCHEMA_VERSION,
        "id": map_id,
        "status": status,
        "error": error,
        "started_utc": started_utc,
        "finished_utc": utc_now(),
        "elapsed_seconds": round(elapsed, 6),
        "memoryguard": memory,
    }
    atomic_write_json(path, value)
    return path


def process_entry(
    entry: dict[str, Any],
    entry_path: Path,
    output: Path,
    resolution: int,
    np: Any,
    codec: Any,
    container: Any,
    hm2: Any,
    lmap: Any,
    frt_module: Any | None,
    render_mesh_module: Any | None,
    module_errors: dict[str, str | None],
    tacview: Any,
    guard: Any,
    started_utc: str,
    started_clock: float,
) -> dict[str, Any]:
    map_id = checked_id(entry)
    source_value = entry.get("source_path")
    if not isinstance(source_value, str) or not source_value:
        raise ValueError("entry has no source_path")
    source = Path(source_value)
    if entry.get("exists") is False or not source.is_file():
        raise MissingSource(f"source file is missing: {source}")

    map_bounds = checked_bounds(entry)
    warnings: list[str] = []
    water_level = checked_water_level(entry, warnings)
    allwater = is_all_water(entry)
    source_before = source_snapshot(source)
    config_before = config_snapshot(entry.get("config_path"))
    actual = scan_actual_blocks(source, container)

    catalog_hm2 = bool(entry.get("has_hm2"))
    catalog_lmap = bool(entry.get("has_lmap"))
    catalog_frt = bool(entry.get("has_frt"))
    if catalog_hm2 != actual["has_hm2"]:
        warnings.append(
            f"catalog has_hm2={catalog_hm2} differs from actual={actual['has_hm2']}"
        )
    if catalog_lmap != actual["has_lmap"]:
        warnings.append(
            f"catalog has_lmap={catalog_lmap} differs from actual={actual['has_lmap']}"
        )
    if catalog_frt != actual["has_frt"]:
        warnings.append(
            f"catalog has_frt={catalog_frt} differs from actual={actual['has_frt']}"
        )

    hm2_meta: dict[str, Any] | None = None
    hm2_height: Any = None
    base_meta: dict[str, Any] | None = None
    base_meta_summary: dict[str, Any] | None = None
    base_kind: str | None = None
    native = False
    overlay_pixels = 0

    if actual["has_hm2"]:
        grid, hm2_meta = hm2.decode_hm2(source, codec.decompress)
        hm2_height = hm2_meters(grid, hm2_meta, np)
        sample_bounds = hm2_sample_bounds(hm2_meta)
        cell_bounds = hm2_cell_bounds(hm2_meta)
        native = hm2_covers_map(cell_bounds, map_bounds, float(hm2_meta["cell"]))
        if native:
            if tuple(hm2_height.shape[::-1]) != (resolution, resolution):
                warnings.append(
                    "native HM2 grid is "
                    f"{hm2_height.shape[1]}x{hm2_height.shape[0]}, "
                    f"requested resolution {resolution} was not resampled"
                )
            if hm2_height.shape[0] < 2 or hm2_height.shape[1] < 2:
                raise ValueError("native HM2 grid must be at least 2x2")
            height = hm2_height
            coverage = {
                "sample_count": int(height.size),
                "terrain_pixel_count": int(height.size),
                "coverage_fraction": 1.0,
                "water_pixel_count": 0,
                "water_fraction": 0.0,
                "min_height_m": float(height.min()),
                "max_height_m": float(height.max()),
                "bbox_pixel": [0, 0, height.shape[1] - 1, height.shape[0] - 1],
                "bbox_m": sample_bounds,
                "hm2_overlay_pixel_count": 0,
                "mask_semantics": "complete native HM2 sample grid",
            }
            mode = "hm2_native"
        else:
            warnings.append(
                "HM2 covers only part of the map; using a full-map decoder plus HM2 overlay"
            )
    else:
        sample_bounds = lmap_sample_bounds(map_bounds, resolution)

    if not native:
        sample_bounds = lmap_sample_bounds(map_bounds, resolution)
        (
            base_kind,
            height,
            base_meta,
            mask,
            water_mask,
            base_warnings,
        ) = decode_base_map(
            source,
            actual,
            map_bounds,
            resolution,
            water_level,
            allwater,
            np,
            codec,
            lmap,
            frt_module,
            render_mesh_module,
            module_errors,
        )
        warnings.extend(base_warnings)
        base_meta_summary = decoder_meta_summary(base_meta, np)
        decoder_warnings = base_meta.get("warnings")
        if isinstance(decoder_warnings, list):
            warnings.extend(str(item) for item in decoder_warnings)

        combined_mask = mask
        combined_water = water_mask
        if actual["has_hm2"]:
            overlay_pixels, overlay_mask = overlay_hm2(
                height,
                mask,
                hm2_height,
                hm2_meta,
                map_bounds,
                resolution,
                np,
            )
            combined_mask = mask | overlay_mask
            combined_water = water_mask.copy()
            if overlay_pixels:
                combined_water[overlay_mask] = height[overlay_mask] <= water_level
            if overlay_pixels == 0:
                warnings.append(f"HM2 does not intersect any {base_kind} pixel centre")
            if not np.count_nonzero(mask):
                warnings.append(
                    f"{base_kind} terrain mask is empty; coverage comes from HM2 overlay only"
                )
            mode = f"{base_kind}_hm2_overlay"
        else:
            mode = str(base_kind)

        coverage = coverage_stats(
            height, combined_mask, combined_water, sample_bounds, np
        )
        coverage["hm2_overlay_pixel_count"] = int(overlay_pixels)

        if coverage["terrain_pixel_count"] == 0:
            if not allwater:
                raise ValueError(
                    f"{base_kind} returned an empty terrain mask; "
                    "set allwater=true only for a water-only map"
                )
            warnings.append("allwater=true: accepting an empty terrain mask")
        elif allwater:
            warnings.append("allwater=true but decoded terrain mask is non-empty")

    if not all(math.isfinite(v) for v in sample_bounds):
        raise ValueError("output sample bounds are non-finite")
    if sample_bounds[2] <= sample_bounds[0] or sample_bounds[3] <= sample_bounds[1]:
        raise ValueError(f"invalid output sample bounds: {sample_bounds!r}")

    source_after = source_snapshot(source)
    if (
        source_after["size"] != source_before["size"]
        or source_after["sha256"] != source_before["sha256"]
    ):
        raise ValueError("source changed while it was being decoded")
    config_after = config_snapshot(entry.get("config_path"))
    if config_after != config_before:
        raise ValueError("catalog level config changed while it was being decoded")

    folder = output / "maps" / map_id
    tacview_meta = tacview.write_data(
        folder, map_id, height, {"sample_bounds_m": sample_bounds}
    )
    xml_meta = dict(tacview_meta)
    xml_meta["id"] = map_id
    tacview.write_xml(folder, xml_meta)

    try:
        memory = guard.stats()
    except Exception as exc:
        memory = {"error": short_error(exc)}
        warnings.append("MemoryGuard.stats() failed")

    finished_utc = utc_now()
    elapsed = time.perf_counter() - started_clock
    decoder_info = {
        "heightmap_mode": mode,
        "actual_blocks": actual,
        "catalog_blocks": {
            "has_hm2": catalog_hm2,
            "has_lmap": catalog_lmap,
            "has_frt": catalog_frt,
        },
        "hm2": jsonable(hm2_meta, np) if hm2_meta is not None else None,
        "base_decoder": base_kind,
        "base_decoder_meta": base_meta_summary,
        "optional_modules": {
            "frt": module_errors.get("frt"),
            "render_mesh": module_errors.get("render_mesh"),
        },
        "codec_module": str(getattr(codec, "__name__", "codec")),
        "numpy_version": np.__version__,
        "python_version": platform.python_version(),
    }
    metadata = {
        "schema_version": SCHEMA_VERSION,
        "id": map_id,
        "status": "ok",
        "source": source_before,
        "request": {
            "entry": str(entry_path),
            "output": str(output),
            "resolution": resolution,
        },
        "catalog": {
            "is_air": entry.get("is_air"),
            "exists": entry.get("exists"),
            "map_coord0": entry.get("map_coord0"),
            "map_coord1": entry.get("map_coord1"),
            "water_level": entry.get("water_level"),
            "heightmap_format": entry.get("heightmap_format"),
            "has_hm2": entry.get("has_hm2"),
            "has_lmap": entry.get("has_lmap"),
            "has_frt": entry.get("has_frt"),
            "config_path": config_before["path"] if config_before else None,
            "config_size": config_before["size"] if config_before else None,
            "config_sha256": config_before["sha256"] if config_before else None,
            "explicit_target": entry.get("explicit_target"),
        },
        "format_decoder_info": decoder_info,
        "allwater": allwater,
        "warnings": warnings,
        "coverage": coverage,
        "output": jsonable(tacview_meta, np),
        "memoryguard": memory,
        "timing": {
            "started_utc": started_utc,
            "finished_utc": finished_utc,
            "elapsed_seconds": round(elapsed, 6),
        },
    }
    metadata_path = folder / "metadata.json"
    atomic_write_json(metadata_path, metadata)

    result = {
        "schema_version": SCHEMA_VERSION,
        "id": map_id,
        "status": "ok",
        "mode": mode,
        "started_utc": started_utc,
        "finished_utc": finished_utc,
        "elapsed_seconds": round(elapsed, 6),
        "source_sha256": source_before["sha256"],
        "width": int(height.shape[1]),
        "height": int(height.shape[0]),
        "sample_bounds_m": sample_bounds,
        "coverage_fraction": coverage["coverage_fraction"],
        "warnings": warnings,
        "data_file": str(Path("maps") / map_id / f"{map_id}.data"),
        "xml_file": str(Path("maps") / map_id / "CustomHeightmapList.xml"),
        "metadata_file": str(Path("maps") / map_id / "metadata.json"),
        "data_sha256": tacview_meta.get("data_sha256"),
        "memoryguard": memory,
    }
    atomic_write_json(folder / "result.json", result)
    return result


def parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--entry", type=Path, required=True, help="one catalog entry JSON")
    parser.add_argument("--output", type=Path, required=True, help="output root")
    parser.add_argument("--resolution", type=int, default=4096)
    args = parser.parse_args(argv)
    if args.resolution < 2:
        parser.error("--resolution must be at least 2")
    return args


def main(argv: list[str] | None = None) -> int:
    args = parse_args(argv)
    output = args.output.resolve()
    entry_path = args.entry.resolve()
    started_utc = utc_now()
    started_clock = time.perf_counter()
    map_id: str | None = None
    guard: Any = None
    try:
        from memory_guard import MemoryGuard

        guard = MemoryGuard()
        entry = load_entry(entry_path)
        map_id = checked_id(entry)

        import numpy as np
        import codec
        import container
        import hm2
        import lmap
        import tacview
        frt_module, frt_error = optional_import("frt")
        render_mesh_module, render_mesh_error = optional_import("render_mesh")
        module_errors = {"frt": frt_error, "render_mesh": render_mesh_error}

        result = process_entry(
            entry,
            entry_path,
            output,
            args.resolution,
            np,
            codec,
            container,
            hm2,
            lmap,
            frt_module,
            render_mesh_module,
            module_errors,
            tacview,
            guard,
            started_utc,
            started_clock,
        )
    except Exception as exc:
        elapsed = time.perf_counter() - started_clock
        status = "missing" if isinstance(exc, MissingSource) else "error"
        error = short_error(exc)
        memory: dict[str, Any] | None = None
        if guard is not None:
            try:
                memory = guard.stats()
            except Exception:
                memory = None
        if map_id is not None:
            try:
                write_error_result(
                    output,
                    map_id,
                    status,
                    error,
                    started_utc,
                    elapsed,
                    memory,
                )
            except Exception as write_exc:
                print(f"worker: failed to write result.json: {short_error(write_exc)}", file=sys.stderr)
        print(f"{map_id or 'entry'}: {status}: {error}", file=sys.stderr)
        return 2 if status == "missing" else 1

    print(
        f"{result['id']}: ok {result['mode']} "
        f"{result['width']}x{result['height']} {result['elapsed_seconds']:.2f}s",
        flush=True,
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

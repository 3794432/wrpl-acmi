"""Decode the current Dagor FastRtDump (RTdump) static-scene collision stream.

The FRT block stores a StaticSceneRayTracer dump: a shared vertex array,
32-bit triangles, and a spatial grid.  This module reads the vertex and face
arrays and rasterises their actual XZ triangle surfaces.  It deliberately
ignores the gridHt-style acceleration data.

The dump may contain static scene collision geometry as well as the land mesh
(buildings, bridges, and other objects).  The returned metadata says so
explicitly; these heights are not a bare-earth surface.

Public entry point::

    height, metadata = decode_frt(
        path,
        decompress,                  # decompress(data, expected, codec)
        bounds=(x_min, x_max, z_min, z_max),
        resolution=4096,
        water_level=0.0,
    )

Rows increase toward +Z and columns increase toward +X.  A pixel stores its
centre sample.  Pixels not covered by a triangle contain water_level and have
mask=False.  Where triangles overlap, the highest surface is kept.

Install the caller's MemoryGuard before calling this function: numpy and the
compression backend are imported lazily here so the guard can be established
first.  The decompressor callback follows codec.py: codec 1 is ZSTD and the
returned byte length must equal the declared dump size.
"""

from __future__ import annotations

import math
from pathlib import Path
import struct
from typing import Any, Mapping

try:
    from .container import find_block
except ImportError:  # direct import when terrain-export is on sys.path
    from container import find_block


FRT_TAG = b"\x00FRT"
FRT_VERSION_ID = b"\xff\x76\x31\xff"  # MAKE4C(0xff, 'v', '1', 0xff)
RTDUMP_SIGN = b"RTdump"
CURRENT_VERSION = 43
ZSTD_COMPRESSION = 0
DUMP_HEADER_SIZE = 144
MAX_DUMP_BYTES = 512 * 1024**2
MAX_RASTER_CELLS = 64_000_000
MAX_DERIVED_BYTES = 512 * 1024**2
FRT_HEADER_BYTES = 20


class FrtFormatError(ValueError):
    """An FRT/RTdump stream is malformed or outside the decoder budget."""


def _budget_product(count: int, size: int, label: str) -> int:
    if isinstance(count, bool) or not isinstance(count, int) or count < 0:
        raise FrtFormatError(f"{label} count is invalid: {count!r}")
    total = count * size
    if count and total // count != size:
        raise FrtFormatError(f"{label} byte count overflows")
    if total > MAX_DERIVED_BYTES:
        raise FrtFormatError(
            f"{label} needs {total} bytes, above the {MAX_DERIVED_BYTES}-byte cap"
        )
    return total


def _read_frt_header(path: Path) -> tuple[dict[str, Any], bytes]:
    try:
        block = find_block(path, FRT_TAG)
    except ValueError as exc:
        raise FrtFormatError(f"{path}: {exc}") from exc
    if block.payload_size < FRT_HEADER_BYTES:
        raise FrtFormatError(
            f"{path}: FRT payload is {block.payload_size} bytes; "
            f"at least {FRT_HEADER_BYTES} are required"
        )
    try:
        with path.open("rb") as stream:
            stream.seek(block.payload_offset)
            header = stream.read(FRT_HEADER_BYTES)
    except OSError as exc:
        raise FrtFormatError(f"{path}: cannot read FRT header: {exc}") from exc
    if len(header) != FRT_HEADER_BYTES:
        raise FrtFormatError(f"{path}: truncated FRT header")

    if header[:4] != FRT_VERSION_ID:
        raise FrtFormatError(
            f"{path}: FRT version id is {header[:4].hex()}, expected "
            f"{FRT_VERSION_ID.hex()}"
        )
    block_rest = struct.unpack_from("<I", header, 4)[0]
    if block_rest < 12:
        raise FrtFormatError(f"{path}: RTdump block_rest is {block_rest}, below 12")
    if 8 + block_rest > block.payload_size:
        raise FrtFormatError(
            f"{path}: RTdump block ends at {8 + block_rest}, beyond FRT payload "
            f"size {block.payload_size}"
        )
    if header[8:14] != RTDUMP_SIGN:
        raise FrtFormatError(f"{path}: missing RTdump signature")
    full_version = struct.unpack_from("<H", header, 14)[0]
    version = full_version >> 3
    compression = full_version & 7
    if version != CURRENT_VERSION:
        raise FrtFormatError(
            f"{path}: RTdump version {version} is unsupported; "
            f"this decoder handles current version {CURRENT_VERSION}"
        )
    if compression != ZSTD_COMPRESSION:
        raise FrtFormatError(
            f"{path}: RTdump compression {compression} is unsupported; "
            "current FRT streams use ZSTD (0)"
        )
    dump_size = struct.unpack_from("<I", header, 16)[0]
    if dump_size < DUMP_HEADER_SIZE or dump_size > MAX_DUMP_BYTES:
        raise FrtFormatError(
            f"{path}: RTdump size {dump_size} is outside "
            f"{DUMP_HEADER_SIZE}..{MAX_DUMP_BYTES}"
        )
    compressed_size = block_rest - 12
    if compressed_size <= 0 or compressed_size > MAX_DUMP_BYTES:
        raise FrtFormatError(
            f"{path}: compressed RTdump size {compressed_size} is outside budget"
        )
    info = {
        "block_payload_offset": block.payload_offset,
        "block_payload_size": block.payload_size,
        "block_rest": block_rest,
        "compressed_size": compressed_size,
        "full_version": full_version,
        "version": version,
        "compression": compression,
        "uncompressed_size": dump_size,
        "trailer_size": block.payload_size - (8 + block_rest),
    }
    return info, header


def inspect_frt(path: str | Path) -> dict[str, Any]:
    """Read the FRT/RTdump header without decompressing the level dump."""

    level = Path(path)
    info, _ = _read_frt_header(level)
    result = dict(info)
    result["source"] = str(level)
    result["format"] = "FRT"
    return result


def parse_rtdump(dump: bytes | bytearray | memoryview) -> dict[str, Any]:
    """Parse the uncompressed RTdump vertex/face tables.

    The 144-byte Dump header stores dump-relative offsets.  For version 43 the
    serialized order is vertices (Point3_vec4), faces (three u32 indices), and
    faceIndices.  The trailing hierarchical grid is not needed for rasterising
    the true surface and is intentionally left unread.
    """

    if not isinstance(dump, (bytes, bytearray, memoryview)):
        raise TypeError("RTdump must be bytes-like")
    data = bytes(dump)
    if len(data) < DUMP_HEADER_SIZE:
        raise FrtFormatError(
            f"RTdump is {len(data)} bytes, below header size {DUMP_HEADER_SIZE}"
        )
    if len(data) > MAX_DUMP_BYTES:
        raise FrtFormatError(f"RTdump exceeds {MAX_DUMP_BYTES} bytes")

    rt_box = struct.unpack_from("<6f", data, 0)
    sphere = struct.unpack_from("<4f", data, 24)
    leaf_size = struct.unpack_from("<3f", data, 40)
    leaf_limits = struct.unpack_from("<6i", data, 52)
    verts_ofs, faces_ofs, obsolete_ofs, face_indices_ofs, grid_ofs = (
        struct.unpack_from("<5Q", data, 76)
    )
    verts_count, faces_count, face_indices_count = struct.unpack_from(
        "<3i", data, 116
    )
    dump_version = struct.unpack_from("<i", data, 128)[0]

    scalars = rt_box + sphere + leaf_size
    if not all(math.isfinite(float(value)) for value in scalars):
        raise FrtFormatError("RTdump header contains a non-finite float")
    if rt_box[1] < rt_box[0] or rt_box[4] < rt_box[3]:
        raise FrtFormatError(f"RTdump XZ bounds are invalid: {rt_box!r}")
    if dump_version != CURRENT_VERSION:
        raise FrtFormatError(
            f"RTdump payload version {dump_version} is unsupported; "
            f"expected {CURRENT_VERSION}"
        )
    total = len(data)
    if verts_ofs != DUMP_HEADER_SIZE:
        raise FrtFormatError(
            f"unexpected vertex table offset {verts_ofs}; "
            f"expected {DUMP_HEADER_SIZE}"
        )
    _budget_product(verts_count, 16, "vertex")
    _budget_product(faces_count, 12, "face")
    _budget_product(face_indices_count, 4, "face index")
    verts_end = verts_ofs + verts_count * 16
    faces_end = faces_ofs + faces_count * 12
    face_indices_end = face_indices_ofs + face_indices_count * 4
    if faces_ofs < verts_end:
        raise FrtFormatError("RTdump face table overlaps the vertex table")
    if face_indices_ofs < faces_end:
        raise FrtFormatError("RTdump faceIndices table overlaps the face table")
    if verts_end > total or faces_end > total or face_indices_end > total:
        raise FrtFormatError(
            "RTdump table extends beyond the declared dump size: "
            f"verts_end={verts_end}, faces_end={faces_end}, "
            f"face_indices_end={face_indices_end}, dump={total}"
        )
    if grid_ofs and not 0 <= grid_ofs < total:
        raise FrtFormatError(f"RTdump grid offset {grid_ofs} is outside the dump")
    if obsolete_ofs and not 0 <= obsolete_ofs <= total:
        raise FrtFormatError(
            f"RTdump obsolete face-bounds offset {obsolete_ofs} is outside the dump"
        )

    derived_bytes = verts_count * 24 + faces_count * 24
    if derived_bytes > MAX_DERIVED_BYTES:
        raise FrtFormatError(
            f"decoded vertex/index arrays need {derived_bytes} bytes, above "
            f"the {MAX_DERIVED_BYTES}-byte cap"
        )

    import numpy as np

    raw_verts = np.frombuffer(
        data, dtype="<f4", count=verts_count * 4, offset=verts_ofs
    ).reshape(verts_count, 4)
    world = raw_verts[:, :3].astype(np.float64, copy=True)
    if world.size and not bool(np.isfinite(world).all()):
        raise FrtFormatError("RTdump contains a non-finite vertex")

    raw_faces = np.frombuffer(
        data, dtype="<u4", count=faces_count * 3, offset=faces_ofs
    ).reshape(faces_count, 3)
    # SceneRayI24F8 puts a 24-bit vertex index in the low bits and flags above.
    faces = raw_faces.astype(np.int64)
    faces &= 0x00FFFFFF
    if faces.size:
        high = int(faces.max())
        if high >= verts_count:
            raise FrtFormatError(
                f"RTdump face references vertex {high}, but only "
                f"{verts_count} vertices exist"
            )

    return {
        "world_vertices": world,
        "triangle_indices": faces,
        "vertex_count": int(verts_count),
        "triangle_count": int(faces_count),
        "face_index_count": int(face_indices_count),
        "rt_box": tuple(float(value) for value in rt_box),
        "sphere": tuple(float(value) for value in sphere),
        "leaf_size": tuple(float(value) for value in leaf_size),
        "leaf_limits": tuple(int(value) for value in leaf_limits),
        "version": int(dump_version),
        "obsolete_facebounds_offset": int(obsolete_ofs),
        "grid_offset": int(grid_ofs),
        "dump_size": total,
    }


def _validate_raster_args(
    bounds: Any, resolution: Any, water_level: Any
) -> tuple[tuple[float, float, float, float] | None, int, float]:
    if isinstance(resolution, bool) or not isinstance(resolution, int):
        raise FrtFormatError(f"resolution must be an integer, got {resolution!r}")
    if resolution <= 0 or resolution * resolution > MAX_RASTER_CELLS:
        raise FrtFormatError(
            f"resolution {resolution} creates {resolution * resolution} cells, "
            f"above the {MAX_RASTER_CELLS}-cell cap"
        )
    try:
        water = float(water_level)
    except (TypeError, ValueError) as exc:
        raise FrtFormatError(f"water_level is invalid: {water_level!r}") from exc
    if not math.isfinite(water):
        raise FrtFormatError(f"water_level must be finite, got {water_level!r}")
    if bounds is None:
        return None, resolution, water
    if isinstance(bounds, (str, bytes)) or len(bounds) != 4:
        raise FrtFormatError(
            "bounds must be a 4-tuple (x_min, x_max, z_min, z_max)"
        )
    try:
        values = tuple(float(value) for value in bounds)
    except (TypeError, ValueError) as exc:
        raise FrtFormatError(f"bounds is invalid: {bounds!r}") from exc
    if not all(math.isfinite(value) for value in values):
        raise FrtFormatError(f"bounds contains a non-finite value: {bounds!r}")
    x_min, x_max, z_min, z_max = values
    if x_max <= x_min or z_max <= z_min:
        raise FrtFormatError(
            "bounds must satisfy x_min < x_max and z_min < z_max; "
            f"got {values!r}"
        )
    return values, resolution, water


def _rasterize_mesh(
    parsed: Mapping[str, Any],
    *,
    bounds: tuple[float, float, float, float],
    resolution: int,
    water_level: float,
) -> tuple[Any, Any, dict[str, int]]:
    try:
        import lmap
        import numpy as np
    except ImportError as exc:
        raise FrtFormatError(
            "frt requires numpy and the sibling lmap rasterizer"
        ) from exc

    height = np.full((resolution, resolution), water_level, dtype=np.float32)
    mask = np.zeros((resolution, resolution), dtype=bool)
    x_min, x_max, z_min, z_max = bounds
    dx = (x_max - x_min) / resolution
    dz = (z_max - z_min) / resolution
    if not (math.isfinite(dx) and math.isfinite(dz) and dx > 0 and dz > 0):
        raise FrtFormatError(
            f"bounds produce invalid pixel sizes dx={dx!r}, dz={dz!r}"
        )
    stats = {
        "triangles_total": int(parsed["triangle_count"]),
        "triangles_rasterized": 0,
        "degenerate_triangles": 0,
        "outside_bounds_triangles": 0,
        "candidate_pixels": 0,
        "written_candidates": 0,
    }
    if parsed["triangle_count"]:
        lmap._rasterize_cell(
            parsed["world_vertices"],
            parsed["triangle_indices"],
            ox=x_min,
            oz=z_min,
            dx=dx,
            dz=dz,
            res=resolution,
            height_flat=height.reshape(-1),
            mask_flat=mask.reshape(-1),
            stats=stats,
        )
    return height, mask, stats


def decode_frt(
    path: str | Path,
    decompress,
    bounds: tuple[float, float, float, float] | None = None,
    resolution: int = 4096,
    water_level: float = 0.0,
) -> tuple[Any, dict[str, Any]]:
    """Decode current FRT/RTdump geometry into a float32 metric raster."""

    if not callable(decompress):
        raise TypeError("decompress must be callable")
    level = Path(path)
    requested, resolution, water = _validate_raster_args(
        bounds, resolution, water_level
    )
    info, _ = _read_frt_header(level)
    payload_offset = int(info["block_payload_offset"])
    compressed_size = int(info["compressed_size"])
    dump_size = int(info["uncompressed_size"])
    try:
        with level.open("rb") as stream:
            stream.seek(payload_offset + FRT_HEADER_BYTES)
            compressed = stream.read(compressed_size)
    except OSError as exc:
        raise FrtFormatError(f"{level}: cannot read compressed RTdump: {exc}") from exc
    if len(compressed) != compressed_size:
        raise FrtFormatError(
            f"{level}: truncated compressed RTdump: {len(compressed)} of "
            f"{compressed_size} bytes"
        )

    try:
        dump = decompress(compressed, dump_size, 1)
    except Exception as exc:
        raise FrtFormatError(f"{level}: RTdump decompression failed: {exc}") from exc
    if isinstance(dump, memoryview):
        dump = dump.tobytes()
    elif isinstance(dump, bytearray):
        dump = bytes(dump)
    if not isinstance(dump, bytes):
        raise FrtFormatError(
            f"{level}: decompressor returned {type(dump).__name__}; bytes required"
        )
    if len(dump) != dump_size:
        raise FrtFormatError(
            f"{level}: decompressed RTdump is {len(dump)} bytes, "
            f"expected {dump_size}"
        )

    parsed = parse_rtdump(dump)
    rt_box = parsed["rt_box"]
    header_bounds = (
        float(rt_box[0]),
        float(rt_box[3]),
        float(rt_box[2]),
        float(rt_box[5]),
    )
    if requested is None:
        if header_bounds[1] <= header_bounds[0] or header_bounds[3] <= header_bounds[2]:
            raise FrtFormatError(
                f"{level}: RTdump header has an empty XZ bounding box: "
                f"{header_bounds!r}"
            )
        chosen = header_bounds
        bounds_source = "rtdump_header"
    else:
        chosen = requested
        bounds_source = "requested"

    height, mask, stats = _rasterize_mesh(
        parsed, bounds=chosen, resolution=resolution, water_level=water
    )
    warnings = [
        "FRT is the FastRtDump static-scene collision mesh; its surface may "
        "include buildings, bridges, and other static objects."
    ]
    water_mask = (~mask) | (height <= water)
    x_min, x_max, z_min, z_max = chosen
    dx = (x_max - x_min) / resolution
    dz = (z_max - z_min) / resolution
    valid_pixels = int(mask.sum())
    metadata: dict[str, Any] = {
        "format": "FRT",
        "source": str(level),
        "surface": {
            "kind": "FastRtDump static scene ray tracer",
            "includes": "land mesh plus static collision geometry (buildings/objects)",
            "is_bare_earth": False,
            "terrain_priority": (
                "prefer the same map's lmap LandRayTracer, then its lmap "
                "render mesh, as the terrain surface; use FRT as the "
                "collision-scene fallback"
            ),
        },
        "bounds": chosen,
        "bounds_source": bounds_source,
        "rtdump_header_bounds": header_bounds,
        "origin": (chosen[0], 0.0, chosen[2]),
        "pixel_size_m": (dx, dz),
        "pixel_center_offset": (0.5, 0.5),
        "row_direction": "+Z",
        "column_direction": "+X",
        "resolution": resolution,
        "height_dtype": "float32",
        "height_units": "metres",
        "water_level_m": water,
        "mask": mask,
        "water_mask": water_mask,
        "valid_pixel_count": valid_pixels,
        "water_pixel_count": int(water_mask.sum()),
        "statistics": stats,
        "rtdump": {
            key: parsed[key]
            for key in (
                "version",
                "dump_size",
                "vertex_count",
                "triangle_count",
                "face_index_count",
                "rt_box",
                "sphere",
                "leaf_size",
                "leaf_limits",
                "grid_offset",
            )
        },
        "container": {
            "payload_offset": payload_offset,
            "payload_size": int(info["block_payload_size"]),
            "block_rest": int(info["block_rest"]),
            "compressed_size": compressed_size,
            "trailer_size": int(info["trailer_size"]),
            "full_version": int(info["full_version"]),
            "version": int(info["version"]),
            "compression": int(info["compression"]),
        },
        "warnings": warnings,
    }
    return height, metadata

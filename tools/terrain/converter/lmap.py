"""Decode Dagor lmap land-mesh geometry into a metric terrain raster.

The module deliberately reads the triangle surface stored in the LandRayTracer
LTdump stream.  It does not use gridHt (a conservative ray-culling upper
bound) as terrain height, and it does not apply the community script's
flat-sea heuristic.

Public entry point::

    height, metadata = decode_lmap(
        path,
        decompress,                  # decompress(data, expectedbytes, codec)
        bounds=(x_min, x_max, z_min, z_max),
        resolution=2048,
        water_level=0.0,
    )

The returned array is float32 metres with shape (resolution, resolution).
Row 0 is the low-Z edge and rows increase toward +Z; column 0 is the low-X edge
and columns increase toward +X.  A pixel stores the height at its centre:
x = x_min + (col + 0.5) * dx and z = z_min + (row + 0.5) * dz.

Where no triangle covers a pixel the array contains water_level and the mask
array stored in metadata is false.  A covered pixel keeps its actual (possibly
negative) seabed height and has mask true.  The water_mask metadata array marks
both uncovered pixels and covered pixels at or below water_level.  This lets a
caller choose either a water surface or a terrain surface without losing the
real seabed values.

The decompress callback receives exactly the compressed frame body.  For Oodle
frames that means the four-byte src_sz prefix has already been read, validated,
and removed; expectedbytes is that src_sz and codec is 2.  For ZSTD/LZMA frames
expectedbytes is the size declared in the frame header when present, otherwise 0
for a caller-provided bounded-unknown convention.  The callback must return bytes
whose uncompressed stream is int32 dump_sz followed by dump_sz bytes beginning
with LTdump.

An lmap with no LandRayTracer frame is rejected with LmapFormatError.  It is not
reported as a successful all-water map with zero valid pixels.

This is a bounded decoder.  It enforces file, frame, section, and cell limits
before allocating, and it processes geometry one cell at a time in vectorised
batches.  It has no dependency on container.py or on any game executable.
"""

from __future__ import annotations

import math
import os
import struct
from pathlib import Path
from typing import Any, Callable, Mapping

import numpy as np

__all__ = [
    "LmapFormatError",
    "decode_lmap",
    "decode_ltdump",
    "inspect_lmap",
    "parse_ltdump",
]


# The engine's shared stream cap.  A claimed dump larger than this is corrupt.
MAX_SECTION_BYTES = 512 << 20
MAX_DUMP_BYTES = 512 << 20
MAX_CELLS = 64_000_000
MAX_RASTER_CELLS = 64_000_000

# A DBLD container may be larger than one section; keep a generous but finite
# cap so a malformed file cannot make us read an unbounded stream.
MAX_CONTAINER_BYTES = 1 << 30
MAX_BLOCK_COUNT = 1_000_000

LTDUMP_HEADER_BYTES = 54
LEGACY_BUDGET_HEADER_BYTES = 78  # the engine's conservative budget constant
CELL_BYTES = 64

# The cell record is serialized as raw little-endian bytes; no padding.
CELL_DTYPE = np.dtype(
    [
        ("ofs", "<f4", (4,)),
        ("scale", "<f4", (4,)),
        ("maxHt", "<f4"),
        ("gridHtStart", "<u4"),
        ("gridStart", "<u4"),
        ("fistartGridsize", "<u4"),
        ("facesStart", "<u4"),
        ("vertsStart", "<u4"),
        ("resv", "<u4", (2,)),
    ],
    align=False,
)
if CELL_DTYPE.itemsize != CELL_BYTES:  # pragma: no cover - import-time invariant
    raise RuntimeError("LTdump cell record layout is not 64 bytes")


class LmapFormatError(ValueError):
    """Raised when an lmap/LTdump stream violates its declared format."""


# ---------------------------------------------------------------------------
# Small binary helpers


def _path_size(path: Path) -> int:
    try:
        return os.stat(path).st_size
    except OSError as exc:
        raise LmapFormatError(f"cannot stat {path}: {exc}") from exc


def _read_lmap_payload(path: Path) -> tuple[bytes, dict[str, Any]]:
    """Return the lmap payload and container metadata.

    A DBLD top-level record is u32 raw; 4-byte tag; payload where
    raw & 0x3fffffff is len(tag) + len(payload).  A bare lmap stream beginning
    with lndm is also accepted, which keeps the decoder usable with an
    already-extracted payload in tests.
    """

    path = Path(path)
    size = _path_size(path)
    if size < 12:
        raise LmapFormatError(f"{path}: file is too short ({size} bytes)")
    if size > MAX_CONTAINER_BYTES:
        raise LmapFormatError(
            f"{path}: container is {size} bytes, above the {MAX_CONTAINER_BYTES}-byte cap"
        )

    with path.open("rb") as stream:
        header = stream.read(12)
        if len(header) != 12:
            raise LmapFormatError(f"{path}: truncated file header")

        if header[:8] == b"DBLD3x64":
            position = 12
            tags: list[str] = []
            for _ in range(MAX_BLOCK_COUNT):
                if position == size:
                    break
                if position + 8 > size:
                    raise LmapFormatError(
                        f"{path}: truncated top-level block header at 0x{position:X}"
                    )
                stream.seek(position)
                block_header = stream.read(8)
                if len(block_header) != 8:
                    raise LmapFormatError(
                        f"{path}: truncated top-level block header at 0x{position:X}"
                    )
                raw = struct.unpack_from("<I", block_header, 0)[0]
                length = raw & 0x3FFFFFFF
                tag = block_header[4:8]
                end = position + 4 + length
                if length < 4:
                    raise LmapFormatError(
                        f"{path}: top-level block at 0x{position:X} has length {length}"
                    )
                if end > size:
                    raise LmapFormatError(
                        f"{path}: top-level block at 0x{position:X} ends at "
                        f"0x{end:X}, past file size 0x{size:X}"
                    )
                tags.append(tag.decode("latin-1", "replace"))
                if tag == b"lmap" or tag.rstrip(b"\0") == b"lmap":
                    payload_length = length - 4
                    if payload_length > MAX_SECTION_BYTES:
                        raise LmapFormatError(
                            f"{path}: lmap payload is {payload_length} bytes, "
                            f"above the {MAX_SECTION_BYTES}-byte cap"
                        )
                    stream.seek(position + 8)
                    payload = stream.read(payload_length)
                    if len(payload) != payload_length:
                        raise LmapFormatError(f"{path}: truncated lmap payload")
                    return payload, {
                        "kind": "DBLD3x64",
                        "block_offset": position,
                        "block_length": length,
                        "block_tags": tags,
                        "file_size": size,
                    }
                position = end
            raise LmapFormatError(
                f"{path}: no lmap top-level block (tags seen: {tags!r})"
            )

        # A bare lmap payload is useful for tests and for a future container
        # front-end.  It still goes through every lmap/LTdump validation below.
        stream.seek(0)
        payload = stream.read()
        if payload[:4] == b"lndm":
            if len(payload) > MAX_SECTION_BYTES:
                raise LmapFormatError(
                    f"{path}: bare lmap payload is {len(payload)} bytes, "
                    f"above the {MAX_SECTION_BYTES}-byte cap"
                )
            return payload, {
                "kind": "bare-lmap",
                "block_offset": 0,
                "block_length": len(payload),
                "block_tags": ["lndm"],
                "file_size": size,
            }
        raise LmapFormatError(
            f"{path}: expected DBLD3x64 or a bare lndm stream, got {header[:8]!r}"
        )


def _parse_lmap_header(payload: bytes) -> dict[str, Any]:
    if len(payload) < 52:
        raise LmapFormatError(f"lmap payload is {len(payload)} bytes; at least 52 required")
    magic, version, grid_cell, land_cell = struct.unpack_from("<4sI2f", payload, 0)
    if magic != b"lndm":
        raise LmapFormatError(f"lmap payload magic is {magic!r}, expected b'lndm'")
    if version != 4:
        raise LmapFormatError(f"unsupported lmap version {version}; expected 4")
    map_x, map_y, origin_x, origin_z, use_tile = struct.unpack_from(
        "<5i", payload, 16
    )
    if not (1 <= map_x <= 4096 and 1 <= map_y <= 4096):
        raise LmapFormatError(f"lmap mapSize is ({map_x}, {map_y}); expected 1..4096")
    cells = map_x * map_y
    if cells > MAX_CELLS:
        raise LmapFormatError(
            f"lmap declares {cells} cells, above the {MAX_CELLS}-cell cap"
        )
    if not math.isfinite(grid_cell) or grid_cell <= 0.0:
        raise LmapFormatError(f"lmap gridCellSize is {grid_cell!r}; must be finite and > 0")
    if not math.isfinite(land_cell) or land_cell <= 0.0:
        raise LmapFormatError(f"lmap landCellSize is {land_cell!r}; must be finite and > 0")
    if use_tile not in (0, 1):
        raise LmapFormatError(f"lmap useTile is {use_tile}; expected 0 or 1")

    base_data_offset = 36
    mesh_rel, detail_rel, tile_rel, ray_rel = struct.unpack_from("<4i", payload, 36)
    mesh_ofs = base_data_offset + mesh_rel
    detail_ofs = base_data_offset + detail_rel
    tile_ofs = base_data_offset + tile_rel
    ray_ofs = base_data_offset + ray_rel
    for name, value in (
        ("meshMapOfs", mesh_ofs),
        ("detailDataOfs", detail_ofs),
        ("tileDataOfs", tile_ofs),
    ):
        if value < base_data_offset or value > len(payload):
            raise LmapFormatError(
                f"lmap {name}={value} is outside payload 0..{len(payload)}"
            )
    if not (mesh_ofs <= detail_ofs <= tile_ofs):
        raise LmapFormatError(
            "lmap offsets are not ordered: "
            f"mesh={mesh_ofs}, detail={detail_ofs}, tile={tile_ofs}"
        )

    info: dict[str, Any] = {
        "version": version,
        "grid_cell_size_m": float(grid_cell),
        "land_cell_size_m": float(land_cell),
        "map_size": [map_x, map_y],
        "origin_cells": [origin_x, origin_z],
        "use_tile": use_tile,
        "base_data_offset": base_data_offset,
        "mesh_map_ofs": mesh_ofs,
        "detail_data_ofs": detail_ofs,
        "tile_data_ofs": tile_ofs,
        "ray_tracer_ofs": ray_ofs,
        "payload_bytes": len(payload),
        "has_tracer": False,
        "codec": None,
        "frame_header_offset": None,
        "frame_length": 0,
        "frame_body_length": 0,
        "oodle_src_size": None,
        "expected_size": None,
    }

    # rayTracerOfs == baseDataOffset means the level has no tracer stream.
    if ray_ofs <= base_data_offset:
        return info
    if ray_ofs < base_data_offset + 16 or ray_ofs > len(payload):
        raise LmapFormatError(
            f"lmap rayTracerOfs={ray_ofs} is outside the usable payload"
        )
    if ray_ofs - 4 < base_data_offset + 16:
        raise LmapFormatError(
            f"lmap rayTracerOfs={ray_ofs} leaves no room for a frame header"
        )

    raw = struct.unpack_from("<I", payload, ray_ofs - 4)[0]
    frame_length = raw & 0x3FFFFFFF
    codec = raw >> 30
    if codec not in (0, 1, 2):
        raise LmapFormatError(
            f"lmap ray frame at 0x{ray_ofs - 4:X} uses unknown codec {codec}"
        )
    frame_end = ray_ofs + frame_length
    if frame_length < 4 or frame_end > len(payload):
        raise LmapFormatError(
            f"lmap ray frame at 0x{ray_ofs - 4:X} has length {frame_length}, "
            f"outside payload end 0x{len(payload):X}"
        )
    if codec == 2:
        if frame_length < 8:
            raise LmapFormatError(
                f"Oodle ray frame at 0x{ray_ofs - 4:X} is only {frame_length} bytes; "
                "an uncompressed-size prefix plus payload is required"
            )
        src_size = struct.unpack_from("<I", payload, ray_ofs)[0]
        if src_size < 4 or src_size > MAX_DUMP_BYTES + 4:
            raise LmapFormatError(
                f"Oodle szprefix at 0x{ray_ofs:X} is {src_size}, outside 4.."
                f"{MAX_DUMP_BYTES + 4}"
            )
        info["oodle_src_size"] = src_size
        info["expected_size"] = src_size
        info["frame_body_length"] = frame_length - 4
    else:
        info["frame_body_length"] = frame_length
        info["expected_size"] = _frame_content_size(
            codec, payload[ray_ofs:frame_end]
        )

    info.update(
        {
            "has_tracer": True,
            "codec": codec,
            "frame_header_offset": ray_ofs - 4,
            "frame_length": frame_length,
            "frame_end": frame_end,
        }
    )
    return info


def _frame_content_size(codec: int, body: bytes) -> int | None:
    """Read a ZSTD or LZMA-alone uncompressed size when the frame carries it.

    Oodle uses the explicit u32 szprefix handled by _parse_lmap_header.  ZSTD
    frame headers normally carry FCS; LZMA-alone carries an 8-byte size in its
    13-byte header.  None means the frame does not declare a size and the
    caller's bounded-unknown decompressor convention is required.
    """

    if codec == 1:
        if len(body) < 5 or body[:4] != b"\x28\xb5\x2f\xfd":
            return None
        fhd = body[4]
        fcs_flag = fhd >> 6
        single_segment = (fhd >> 5) & 1
        dict_id_flag = fhd & 0x3
        offset = 5
        if not single_segment:
            offset += 1
        offset += {0: 0, 1: 1, 2: 2, 3: 4}[dict_id_flag]
        fcs_size = {0: 1 if single_segment else 0, 1: 2, 2: 4, 3: 8}[fcs_flag]
        if fcs_size == 0 or offset + fcs_size > len(body):
            return None
        value = int.from_bytes(body[offset : offset + fcs_size], "little")
        if fcs_size in (1, 2):
            value += 256
        return value if 0 < value <= MAX_DUMP_BYTES + 4 else None

    if codec == 0:
        if len(body) < 13:
            return None
        value = int.from_bytes(body[5:13], "little")
        if value == 0 or value == 0xFFFFFFFFFFFFFFFF:
            return None
        return value if 0 < value <= MAX_DUMP_BYTES + 4 else None

    return None


def _decompress_frame(
    payload: bytes,
    lmap: Mapping[str, Any],
    decompress: Callable[[bytes, int, int], bytes],
) -> tuple[bytes | None, dict[str, Any]]:
    if not lmap["has_tracer"]:
        return None, {"has_tracer": False}

    ray_ofs = int(lmap["ray_tracer_ofs"])
    frame_length = int(lmap["frame_length"])
    codec = int(lmap["codec"])
    if codec == 2:
        expected = int(lmap["oodle_src_size"])
        body = payload[ray_ofs + 4 : ray_ofs + frame_length]
    else:
        expected = int(lmap.get("expected_size") or 0)
        body = payload[ray_ofs : ray_ofs + frame_length]
    if not body:
        raise LmapFormatError("lmap ray frame has an empty compressed body")

    try:
        result = decompress(bytes(body), expected, codec)
    except Exception as exc:  # keep the frame context visible to the caller
        raise LmapFormatError(
            f"lmap decompressor failed for codec {codec}, "
            f"frame at 0x{lmap['frame_header_offset']:X}: {exc}"
        ) from exc

    if isinstance(result, np.ndarray):
        result = result.tobytes()
    elif isinstance(result, memoryview):
        result = result.tobytes()
    elif isinstance(result, bytearray):
        result = bytes(result)
    if not isinstance(result, bytes):
        raise LmapFormatError(
            f"lmap decompressor returned {type(result).__name__}; bytes required"
        )

    if codec == 2 and len(result) != expected:
        raise LmapFormatError(
            f"Oodle decompressor returned {len(result)} bytes, expected {expected}"
        )
    if codec != 2 and expected > 0 and len(result) != expected:
        raise LmapFormatError(
            f"codec {codec} decompressor returned {len(result)} bytes, "
            f"frame declares {expected}"
        )
    if len(result) < 4:
        raise LmapFormatError("decompressed lmap frame is too short to contain dump_sz")
    dump_size = struct.unpack_from("<i", result, 0)[0]
    if dump_size < LTDUMP_HEADER_BYTES:
        raise LmapFormatError(
            f"LTdump dump_sz={dump_size} is below the {LTDUMP_HEADER_BYTES}-byte header"
        )
    if dump_size > MAX_DUMP_BYTES:
        raise LmapFormatError(
            f"LTdump dump_sz={dump_size} exceeds the {MAX_DUMP_BYTES}-byte cap"
        )
    if codec == 2:
        if len(result) != 4 + dump_size:
            raise LmapFormatError(
                f"Oodle stream contains {len(result)} bytes but dump_sz={dump_size} "
                "requires exactly dump_sz + 4"
            )
    else:
        if expected > 0 and len(result) != 4 + dump_size:
            raise LmapFormatError(
                f"codec {codec} stream contains {len(result)} bytes but dump_sz="
                f"{dump_size} requires exactly dump_sz + 4"
            )
        if len(result) < 4 + dump_size:
            raise LmapFormatError(
                f"decompressed stream is {len(result)} bytes, "
                f"short of 4 + dump_sz ({4 + dump_size})"
            )

    dump = result[4 : 4 + dump_size]
    trailer = len(result) - (4 + dump_size)
    return dump, {
        "has_tracer": True,
        "codec": codec,
        "compressed_bytes": len(body),
        "decompressed_bytes": len(result),
        "dump_size": dump_size,
        "trailer_bytes": trailer,
    }


# ---------------------------------------------------------------------------
# LTdump parser


def parse_ltdump(dump: bytes | bytearray | memoryview) -> dict[str, Any]:
    """Parse one uncompressed LTdump payload.

    The layout follows Dagor's LandRayTracerSoA4::loadStreamToDump: a 54-byte
    fixed header followed by six interleaved [int32 count][data] sections
    (cells, grid u32, gridHt f32, faces u16, vertices 4x u16, and the
    legacy-only faceIndices u16).  The engine deliberately ignores gridHt and
    faceIndices for surface construction; we keep their counts for diagnostics
    and never use gridHt as a height.
    """

    if isinstance(dump, memoryview):
        view = dump
    elif isinstance(dump, bytearray):
        view = memoryview(dump)
    elif isinstance(dump, bytes):
        view = memoryview(dump)
    else:
        raise TypeError("dump must be bytes-like")
    total = len(view)
    if total < LTDUMP_HEADER_BYTES:
        raise LmapFormatError(
            f"LTdump is {total} bytes; at least {LTDUMP_HEADER_BYTES} required"
        )
    if bytes(view[:6]) != b"LTdump":
        raise LmapFormatError(f"LTdump magic is {bytes(view[:6])!r}")

    cells_x, cells_y = struct.unpack_from("<2i", view, 6)
    cell_size = struct.unpack_from("<f", view, 14)[0]
    origin = struct.unpack_from("<3f", view, 18)
    bbox = struct.unpack_from("<6f", view, 30)
    if not (1 <= cells_x <= 4096 and 1 <= cells_y <= 4096):
        raise LmapFormatError(
            f"LTdump grid is ({cells_x}, {cells_y}); expected 1..4096"
        )
    cell_count_declared = cells_x * cells_y
    if cell_count_declared > MAX_CELLS:
        raise LmapFormatError(
            f"LTdump declares {cell_count_declared} cells, above the {MAX_CELLS}-cell cap"
        )
    if not math.isfinite(cell_size) or cell_size <= 0.0:
        raise LmapFormatError(f"LTdump cell size is {cell_size!r}; must be finite and > 0")
    if not all(math.isfinite(v) for v in origin):
        raise LmapFormatError(f"LTdump origin is not finite: {origin!r}")
    if not all(math.isfinite(v) for v in bbox):
        raise LmapFormatError(f"LTdump bbox is not finite: {bbox!r}")

    position = LTDUMP_HEADER_BYTES
    remaining = total - position

    def take_section(name: str, element_size: int) -> tuple[int, memoryview]:
        nonlocal position, remaining
        if remaining < 4:
            raise LmapFormatError(
                f"LTdump {name}: no room for count at 0x{position:X}"
            )
        count = struct.unpack_from("<i", view, position)[0]
        position += 4
        remaining -= 4
        if count < 0:
            raise LmapFormatError(f"LTdump {name}: negative count {count}")
        byte_count = count * element_size
        if byte_count > remaining:
            raise LmapFormatError(
                f"LTdump {name}: count {count} needs {byte_count} bytes but only "
                f"{remaining} remain"
            )
        if byte_count > MAX_SECTION_BYTES:
            raise LmapFormatError(
                f"LTdump {name}: section is {byte_count} bytes, above the "
                f"{MAX_SECTION_BYTES}-byte cap"
            )
        data = view[position : position + byte_count]
        position += byte_count
        remaining -= byte_count
        return count, data

    cell_count, cell_data = take_section("cells", CELL_BYTES)
    if cell_count != cell_count_declared:
        raise LmapFormatError(
            f"LTdump cells count {cell_count} does not match grid "
            f"{cells_x}x{cells_y}={cell_count_declared}"
        )
    grid_count, _ = take_section("grid", 4)
    grid_ht_count, _ = take_section("gridHt", 4)
    face_count, face_data = take_section("faces", 2)
    vert_count, vert_data = take_section("verts", 8)

    face_index_count = 0
    face_index_data: memoryview | None = None
    if remaining >= 4:
        face_index_count, face_index_data = take_section("faceIndices", 2)
    elif remaining:
        if any(view[position : position + remaining]):
            raise LmapFormatError(
                f"LTdump has {remaining} unexplained non-zero trailing bytes"
            )

    trailer_bytes = remaining
    if trailer_bytes > 64:
        raise LmapFormatError(
            f"LTdump has {trailer_bytes} trailing bytes, above the 64-byte padding cap"
        )
    if trailer_bytes and any(view[position : position + trailer_bytes]):
        raise LmapFormatError("LTdump has non-zero padding after its last section")

    cells = np.frombuffer(cell_data, dtype=CELL_DTYPE, count=cell_count)
    faces = np.frombuffer(face_data, dtype="<u2", count=face_count)
    verts = np.frombuffer(vert_data, dtype="<u2", count=vert_count * 4).reshape(
        vert_count, 4
    )

    # Section spans are cell-local arrays: faces are u16 indices into the
    # cell's own [vertsStart, next vertsStart) range.  Validate the entire
    # span table before any geometry is decoded.
    vert_starts = cells["vertsStart"].astype(np.int64)
    face_starts = cells["facesStart"].astype(np.int64)
    if cell_count and (vert_starts[0] != 0 or face_starts[0] != 0):
        raise LmapFormatError(
            "LTdump first cell does not start its vertex/face tables at zero"
        )
    vert_ends = np.empty(cell_count, dtype=np.int64)
    face_ends = np.empty(cell_count, dtype=np.int64)
    if cell_count:
        vert_ends[:-1] = vert_starts[1:]
        face_ends[:-1] = face_starts[1:]
        vert_ends[-1] = vert_count
        face_ends[-1] = face_count

    if (
        np.any(vert_ends < vert_starts)
        or np.any(face_ends < face_starts)
        or np.any(vert_ends > vert_count)
        or np.any(face_ends > face_count)
    ):
        raise LmapFormatError("LTdump cell section spans are not monotonic/in range")
    vert_counts = vert_ends - vert_starts
    face_counts = face_ends - face_starts
    if np.any(vert_counts < 0) or np.any(face_counts < 0):
        raise LmapFormatError("LTdump cell section spans are negative")
    if np.any((face_counts > 0) & (face_counts % 3 != 0)):
        bad = int(np.nonzero((face_counts > 0) & (face_counts % 3 != 0))[0][0])
        raise LmapFormatError(
            f"LTdump cell {bad} has {int(face_counts[bad])} face indices; "
            "a triangle list must be a multiple of 3"
        )
    if np.any((face_counts > 0) & (vert_counts <= 0)):
        bad = int(np.nonzero((face_counts > 0) & (vert_counts <= 0))[0][0])
        raise LmapFormatError(f"LTdump cell {bad} has faces but no vertices")

    # Cell-local indices are u16; reject one that points outside its cell.
    nonempty = np.nonzero(face_counts > 0)[0]
    for cell_index in nonempty:
        start = int(face_starts[cell_index])
        end = int(face_ends[cell_index])
        local_vert_count = int(vert_counts[cell_index])
        if int(faces[start:end].max(initial=0)) >= local_vert_count:
            raise LmapFormatError(
                f"LTdump cell {int(cell_index)} contains a face index outside "
                f"its {local_vert_count}-vertex range"
            )

    triangle_count = int(face_counts.sum() // 3)
    return {
        "cells_x": cells_x,
        "cells_y": cells_y,
        "cell_size_m": float(cell_size),
        "origin_m": tuple(float(v) for v in origin),
        "bbox_min_m": tuple(float(v) for v in bbox[:3]),
        "bbox_max_m": tuple(float(v) for v in bbox[3:]),
        "cell_count": int(cell_count),
        "grid_count": int(grid_count),
        "grid_ht_count": int(grid_ht_count),
        "face_count": int(face_count),
        "vert_count": int(vert_count),
        "face_index_count": int(face_index_count),
        "triangle_count": triangle_count,
        "cells": cells,
        "faces": faces,
        "verts": verts,
        "vert_starts": vert_starts,
        "vert_ends": vert_ends,
        "face_starts": face_starts,
        "face_ends": face_ends,
        "vert_counts": vert_counts,
        "face_counts": face_counts,
        "face_indices": (
            np.frombuffer(face_index_data, dtype="<u2", count=face_index_count)
            if face_index_data is not None
            else None
        ),
        "trailer_bytes": int(trailer_bytes),
    }


# ---------------------------------------------------------------------------
# Rasterisation


_TRI_CHUNK = 200_000
_MAX_CANDIDATES = 500_000
_MAX_GROUP_AREA = 500_000
_BARY_EPS = 1e-7


def _rasterize_group(
    ids: np.ndarray,
    gw: int,
    gh: int,
    ax: np.ndarray,
    ay: np.ndarray,
    az: np.ndarray,
    bx: np.ndarray,
    by: np.ndarray,
    bz: np.ndarray,
    cx: np.ndarray,
    cy: np.ndarray,
    cz: np.ndarray,
    denom: np.ndarray,
    px0: np.ndarray,
    pz0: np.ndarray,
    *,
    ox: float,
    oz: float,
    dx: float,
    dz: float,
    res: int,
    height_flat: np.ndarray,
    mask_flat: np.ndarray,
    stats: dict[str, int],
) -> None:
    """Rasterise one size-homogeneous triangle batch."""

    n = int(ids.size)
    if n == 0:
        return
    area = gw * gh
    if gw == 1 and gh == 1:
        tri_ids = ids
        px = px0[ids]
        pz = pz0[ids]
    else:
        tri_ids = np.repeat(ids, area)
        row_offset = np.tile(np.repeat(np.arange(gh, dtype=np.int64), gw), n)
        col_offset = np.tile(np.arange(gw, dtype=np.int64), n * gh)
        px = np.repeat(px0[ids], area) + col_offset
        pz = np.repeat(pz0[ids], area) + row_offset

    xx = ox + (px.astype(np.float64) + 0.5) * dx
    zz = oz + (pz.astype(np.float64) + 0.5) * dz
    axv = ax[tri_ids]
    ayv = ay[tri_ids]
    azv = az[tri_ids]
    bxv = bx[tri_ids]
    byv = by[tri_ids]
    bzv = bz[tri_ids]
    cxv = cx[tri_ids]
    cyv = cy[tri_ids]
    czv = cz[tri_ids]
    den = denom[tri_ids]

    # Barycentric coordinates in the XZ projection.
    l0 = ((bzv - czv) * (xx - cxv) + (cxv - bxv) * (zz - czv)) / den
    l1 = ((czv - azv) * (xx - cxv) + (axv - cxv) * (zz - czv)) / den
    l2 = 1.0 - l0 - l1
    inside = (l0 >= -_BARY_EPS) & (l1 >= -_BARY_EPS) & (l2 >= -_BARY_EPS)
    yy = l0 * ayv + l1 * byv + l2 * cyv
    inside &= np.isfinite(yy)
    stats["candidate_pixels"] += int(px.size)
    if not inside.any():
        return
    pixel_index = pz[inside] * res + px[inside]
    np.maximum.at(height_flat, pixel_index, yy[inside])
    mask_flat[pixel_index] = True
    stats["triangles_rasterized"] += int(np.unique(tri_ids[inside]).size)
    stats["written_candidates"] += int(np.count_nonzero(inside))


def _rasterize_large_coords(
    ax: float,
    ay: float,
    az: float,
    bx: float,
    by: float,
    bz: float,
    cx: float,
    cy: float,
    cz: float,
    *,
    ox: float,
    oz: float,
    dx: float,
    dz: float,
    res: int,
    height_flat: np.ndarray,
    mask_flat: np.ndarray,
    stats: dict[str, int],
) -> None:
    """Row-scan a triangle whose bbox is too large for a dense candidate batch."""

    den = (bz - cz) * (ax - cx) + (cx - bx) * (az - cz)
    if abs(den) <= 1e-14:
        return

    min_z = min(az, bz, cz)
    max_z = max(az, bz, cz)
    row0 = max(0, int(math.ceil((min_z - oz) / dz - 0.5)))
    row1 = min(res - 1, int(math.floor((max_z - oz) / dz - 0.5)))
    if row1 < row0:
        return

    hit = False
    for row in range(row0, row1 + 1):
        zc = oz + (row + 0.5) * dz
        intersections: list[float] = []
        for xa, za, xb, zb in ((ax, az, bx, bz), (bx, bz, cx, cz), (cx, cz, ax, az)):
            if za == zb:
                continue
            if min(za, zb) <= zc <= max(za, zb):
                intersections.append(xa + (xb - xa) * (zc - za) / (zb - za))
        if len(intersections) < 2:
            continue
        x_lo = min(intersections)
        x_hi = max(intersections)
        col0 = max(0, int(math.ceil((x_lo - ox) / dx - 0.5)))
        col1 = min(res - 1, int(math.floor((x_hi - ox) / dx - 0.5)))
        if col1 < col0:
            continue
        px = np.arange(col0, col1 + 1, dtype=np.int64)
        xx = ox + (px.astype(np.float64) + 0.5) * dx
        l0 = ((bz - cz) * (xx - cx) + (cx - bx) * (zc - cz)) / den
        l1 = ((cz - az) * (xx - cx) + (ax - cx) * (zc - cz)) / den
        l2 = 1.0 - l0 - l1
        inside = (l0 >= -_BARY_EPS) & (l1 >= -_BARY_EPS) & (l2 >= -_BARY_EPS)
        yy = l0 * ay + l1 * by + l2 * cy
        inside &= np.isfinite(yy)
        stats["candidate_pixels"] += int(px.size)
        if not inside.any():
            continue
        pixel_index = row * res + px[inside]
        np.maximum.at(height_flat, pixel_index, yy[inside])
        mask_flat[pixel_index] = True
        stats["written_candidates"] += int(np.count_nonzero(inside))
        hit = True
    if hit:
        stats["triangles_rasterized"] += 1


def _rasterize_cell(
    world: np.ndarray,
    tri: np.ndarray,
    *,
    ox: float,
    oz: float,
    dx: float,
    dz: float,
    res: int,
    height_flat: np.ndarray,
    mask_flat: np.ndarray,
    stats: dict[str, int],
) -> None:
    """Vectorised bbox-binned rasteriser for one cell's triangle soup."""

    triangle_count = int(tri.shape[0])
    if triangle_count == 0:
        return
    for start in range(0, triangle_count, _TRI_CHUNK):
        local_tri = tri[start : start + _TRI_CHUNK]
        p0 = world[local_tri[:, 0]]
        p1 = world[local_tri[:, 1]]
        p2 = world[local_tri[:, 2]]
        ax = p0[:, 0]
        ay = p0[:, 1]
        az = p0[:, 2]
        bx = p1[:, 0]
        by = p1[:, 1]
        bz = p1[:, 2]
        cx = p2[:, 0]
        cy = p2[:, 1]
        cz = p2[:, 2]
        denom = (bz - cz) * (ax - cx) + (cx - bx) * (az - cz)
        finite = (
            np.isfinite(denom)
            & np.isfinite(ax)
            & np.isfinite(ay)
            & np.isfinite(az)
            & np.isfinite(bx)
            & np.isfinite(by)
            & np.isfinite(bz)
            & np.isfinite(cx)
            & np.isfinite(cy)
            & np.isfinite(cz)
        )
        valid = finite & (np.abs(denom) > 1e-14)
        stats["degenerate_triangles"] += int(np.count_nonzero(~valid))
        if not valid.any():
            continue

        min_x = np.minimum(np.minimum(ax, bx), cx)
        max_x = np.maximum(np.maximum(ax, bx), cx)
        min_z = np.minimum(np.minimum(az, bz), cz)
        max_z = np.maximum(np.maximum(az, bz), cz)
        raw_px0 = np.ceil((min_x - ox) / dx - 0.5)
        raw_px1 = np.floor((max_x - ox) / dx - 0.5)
        raw_pz0 = np.ceil((min_z - oz) / dz - 0.5)
        raw_pz1 = np.floor((max_z - oz) / dz - 0.5)
        overlap = (
            valid
            & (raw_px1 >= 0)
            & (raw_px0 <= res - 1)
            & (raw_pz1 >= 0)
            & (raw_pz0 <= res - 1)
        )
        stats["outside_bounds_triangles"] += int(
            np.count_nonzero(valid & ~overlap)
        )
        if not overlap.any():
            continue

        px0 = np.clip(raw_px0[overlap].astype(np.int64), 0, res - 1)
        px1 = np.clip(raw_px1[overlap].astype(np.int64), 0, res - 1)
        pz0 = np.clip(raw_pz0[overlap].astype(np.int64), 0, res - 1)
        pz1 = np.clip(raw_pz1[overlap].astype(np.int64), 0, res - 1)
        width = px1 - px0 + 1
        height = pz1 - pz0 + 1
        keep = (width > 0) & (height > 0)
        if not keep.any():
            continue

        # Compact the per-triangle arrays to the triangles that touch output.
        selected = np.nonzero(overlap)[0][keep]
        if selected.size == 0:
            continue
        width = width[keep]
        height = height[keep]
        px0 = px0[keep]
        pz0 = pz0[keep]
        ax = ax[selected]
        ay = ay[selected]
        az = az[selected]
        bx = bx[selected]
        by = by[selected]
        bz = bz[selected]
        cx = cx[selected]
        cy = cy[selected]
        cz = cz[selected]
        denom = denom[selected]

        code = width.astype(np.int64) * (res + 1) + height.astype(np.int64)
        codes, inverse = np.unique(code, return_inverse=True)
        for group_index in range(codes.size):
            ids = np.nonzero(inverse == group_index)[0]
            if ids.size == 0:
                continue
            gw = int(width[ids[0]])
            gh = int(height[ids[0]])
            group_area = gw * gh
            if group_area > _MAX_GROUP_AREA:
                # A huge projected triangle is cheaper to walk row by row.
                for local_index in ids:
                    _rasterize_large_coords(
                        float(ax[local_index]),
                        float(ay[local_index]),
                        float(az[local_index]),
                        float(bx[local_index]),
                        float(by[local_index]),
                        float(bz[local_index]),
                        float(cx[local_index]),
                        float(cy[local_index]),
                        float(cz[local_index]),
                        ox=ox,
                        oz=oz,
                        dx=dx,
                        dz=dz,
                        res=res,
                        height_flat=height_flat,
                        mask_flat=mask_flat,
                        stats=stats,
                    )
                continue
            step = max(1, _MAX_CANDIDATES // group_area)
            for offset in range(0, ids.size, step):
                _rasterize_group(
                    ids[offset : offset + step],
                    gw,
                    gh,
                    ax,
                    ay,
                    az,
                    bx,
                    by,
                    bz,
                    cx,
                    cy,
                    cz,
                    denom,
                    px0,
                    pz0,
                    ox=ox,
                    oz=oz,
                    dx=dx,
                    dz=dz,
                    res=res,
                    height_flat=height_flat,
                    mask_flat=mask_flat,
                    stats=stats,
                )


def _validate_raster_args(
    bounds: tuple[float, float, float, float] | None,
    resolution: int,
    water_level: float,
) -> tuple[float, float, float, float] | None:
    if isinstance(resolution, bool) or not isinstance(resolution, (int, np.integer)):
        raise LmapFormatError(f"resolution must be an integer, got {resolution!r}")
    resolution = int(resolution)
    if resolution <= 0:
        raise LmapFormatError(f"resolution must be positive, got {resolution}")
    if resolution * resolution > MAX_RASTER_CELLS:
        raise LmapFormatError(
            f"resolution {resolution} creates {resolution * resolution} cells, "
            f"above the {MAX_RASTER_CELLS}-cell cap"
        )
    if not math.isfinite(float(water_level)):
        raise LmapFormatError(f"water_level must be finite, got {water_level!r}")
    if bounds is None:
        return None
    if isinstance(bounds, (str, bytes)) or len(bounds) != 4:
        raise LmapFormatError(
            "bounds must be a 4-tuple (x_min, x_max, z_min, z_max)"
        )
    values = tuple(float(v) for v in bounds)
    if not all(math.isfinite(v) for v in values):
        raise LmapFormatError(f"bounds contains a non-finite value: {bounds!r}")
    x_min, x_max, z_min, z_max = values
    if x_max <= x_min or z_max <= z_min:
        raise LmapFormatError(
            "bounds must satisfy x_min < x_max and z_min < z_max; "
            f"got {values!r}"
        )
    return values


def _empty_result(
    bounds: tuple[float, float, float, float],
    resolution: int,
    water_level: float,
) -> tuple[np.ndarray, np.ndarray, np.ndarray]:
    height = np.full((resolution, resolution), water_level, dtype=np.float32)
    mask = np.zeros((resolution, resolution), dtype=bool)
    water_mask = np.ones((resolution, resolution), dtype=bool)
    return height, mask, water_mask


def _rasterise_parsed(
    parsed: Mapping[str, Any] | None,
    lmap: Mapping[str, Any] | None,
    *,
    bounds: tuple[float, float, float, float] | None,
    resolution: int,
    water_level: float,
) -> tuple[np.ndarray, dict[str, Any]]:
    requested = _validate_raster_args(bounds, resolution, water_level)
    warnings: list[str] = []

    header_bounds: tuple[float, float, float, float] | None = None
    dump_bounds: tuple[float, float, float, float] | None = None
    if lmap is not None:
        mx, mz = lmap["map_size"]
        ox, oz = lmap["origin_cells"]
        cs = float(lmap["land_cell_size_m"])
        header_bounds = (
            float(ox * cs),
            float((ox + mx) * cs),
            float(oz * cs),
            float((oz + mz) * cs),
        )
    if parsed is not None:
        ox, oy, oz = parsed["origin_m"]
        cs = float(parsed["cell_size_m"])
        dump_bounds = (
            float(ox),
            float(ox + parsed["cells_x"] * cs),
            float(oz),
            float(oz + parsed["cells_y"] * cs),
        )

    if requested is not None:
        chosen = requested
        bounds_source = "requested"
    elif header_bounds is not None:
        chosen = header_bounds
        bounds_source = "lmap_header"
    elif dump_bounds is not None:
        chosen = dump_bounds
        bounds_source = "ltdump"
    else:  # pragma: no cover - a parsed/lmap-less call is rejected by callers
        raise LmapFormatError("cannot determine world bounds")

    if (
        not all(math.isfinite(v) for v in chosen)
        or chosen[1] <= chosen[0]
        or chosen[3] <= chosen[2]
    ):
        raise LmapFormatError(f"invalid world bounds {chosen!r}")
    if header_bounds is not None and dump_bounds is not None:
        tolerance = max(
            1.0,
            1e-6
            * max(
                header_bounds[1] - header_bounds[0],
                header_bounds[3] - header_bounds[2],
            ),
        )
        if any(abs(a - b) > tolerance for a, b in zip(header_bounds, dump_bounds)):
            warnings.append(
                "lmap header bounds differ from LTdump grid bounds: "
                f"header={header_bounds!r}, dump={dump_bounds!r}"
            )

    x_min, x_max, z_min, z_max = chosen
    dx = (x_max - x_min) / resolution
    dz = (z_max - z_min) / resolution
    if not (math.isfinite(dx) and math.isfinite(dz) and dx > 0 and dz > 0):
        raise LmapFormatError(
            f"requested bounds produce invalid pixel sizes dx={dx!r}, dz={dz!r}"
        )

    if parsed is None or parsed["triangle_count"] == 0:
        height, mask, water_mask = _empty_result(chosen, resolution, water_level)
        metadata = _make_metadata(
            parsed,
            lmap,
            chosen,
            bounds_source,
            header_bounds,
            dump_bounds,
            resolution,
            dx,
            dz,
            water_level,
            height,
            mask,
            water_mask,
            {
                "triangles_total": 0,
                "triangles_rasterized": 0,
                "degenerate_triangles": 0,
                "outside_bounds_triangles": 0,
                "candidate_pixels": 0,
                "written_candidates": 0,
                "cells_with_geometry": 0,
                "cells_processed": 0,
            },
            warnings,
        )
        return height, metadata

    cells = parsed["cells"]
    faces = parsed["faces"]
    verts = parsed["verts"]
    vert_starts = parsed["vert_starts"]
    vert_ends = parsed["vert_ends"]
    face_starts = parsed["face_starts"]
    face_ends = parsed["face_ends"]
    face_counts = parsed["face_counts"]
    cells_x = int(parsed["cells_x"])
    cell_size = float(parsed["cell_size_m"])
    dump_origin_x, _, dump_origin_z = parsed["origin_m"]

    height = np.full((resolution, resolution), water_level, dtype=np.float32)
    mask = np.zeros((resolution, resolution), dtype=bool)
    height_flat = height.reshape(-1)
    mask_flat = mask.reshape(-1)
    stats: dict[str, int] = {
        "triangles_total": int(parsed["triangle_count"]),
        "triangles_rasterized": 0,
        "degenerate_triangles": 0,
        "outside_bounds_triangles": 0,
        "candidate_pixels": 0,
        "written_candidates": 0,
        "cells_with_geometry": int(np.count_nonzero(face_counts > 0)),
        "cells_processed": 0,
    }

    nonempty_cells = np.nonzero(face_counts > 0)[0]
    for cell_index_raw in nonempty_cells:
        cell_index = int(cell_index_raw)
        cx_cell = cell_index % cells_x
        cz_cell = cell_index // cells_x
        cell_x0 = dump_origin_x + cx_cell * cell_size
        cell_z0 = dump_origin_z + cz_cell * cell_size
        cell_x1 = cell_x0 + cell_size
        cell_z1 = cell_z0 + cell_size
        if cell_x1 < x_min or cell_x0 > x_max or cell_z1 < z_min or cell_z0 > z_max:
            continue

        record = cells[cell_index]
        scale = np.asarray(record["scale"][:3], dtype=np.float64)
        offset = np.asarray(record["ofs"][:3], dtype=np.float64)
        if not np.all(np.isfinite(scale)) or not np.all(np.isfinite(offset)):
            raise LmapFormatError(
                f"LTdump cell {cell_index} has a non-finite scale/offset"
            )
        # Y scale 0 is a legitimate flat plane; X/Z scale 0 collapses the
        # cell's XZ projection, whose triangles are then skipped as
        # degenerate by the rasteriser.  Only negative or non-finite mappings
        # are structural errors.
        if np.any(scale < 0.0):
            raise LmapFormatError(
                f"LTdump cell {cell_index} has negative scale {scale.tolist()}"
            )
        vs = int(vert_starts[cell_index])
        ve = int(vert_ends[cell_index])
        fs = int(face_starts[cell_index])
        fe = int(face_ends[cell_index])
        local_q = verts[vs:ve, :3].astype(np.float64)
        world = local_q * scale + offset
        if not np.all(np.isfinite(world)):
            raise LmapFormatError(
                f"LTdump cell {cell_index} decodes to non-finite vertices"
            )
        # The legacy wire scale/ofs maps q directly to world metres.  The
        # engine's check_cell_mapping applies to the later box-space Cell
        # record, so validate the decoded world footprint against the grid
        # slot instead.  A whole-cell shift produces a 1.0*cell offset and is
        # rejected; partial cells (a corner triangle) stay within 0.75*cell.
        slot_center_x = dump_origin_x + (cx_cell + 0.5) * cell_size
        slot_center_z = dump_origin_z + (cz_cell + 0.5) * cell_size
        wire_center_x = 0.5 * (float(world[:, 0].min()) + float(world[:, 0].max()))
        wire_center_z = 0.5 * (float(world[:, 2].min()) + float(world[:, 2].max()))
        if (
            abs(wire_center_x - slot_center_x) > 0.75 * cell_size
            or abs(wire_center_z - slot_center_z) > 0.75 * cell_size
        ):
            raise LmapFormatError(
                f"LTdump cell {cell_index} decoded footprint centre "
                f"({wire_center_x:.6f}, {wire_center_z:.6f}) does not match grid slot "
                f"({slot_center_x:.6f}, {slot_center_z:.6f})"
            )
        local_tri = faces[fs:fe].reshape(-1, 3).astype(np.int64)
        _rasterize_cell(
            world,
            local_tri,
            ox=x_min,
            oz=z_min,
            dx=dx,
            dz=dz,
            res=resolution,
            height_flat=height_flat,
            mask_flat=mask_flat,
            stats=stats,
        )
        stats["cells_processed"] += 1

    water_mask = (~mask) | (height <= water_level)
    metadata = _make_metadata(
        parsed,
        lmap,
        chosen,
        bounds_source,
        header_bounds,
        dump_bounds,
        resolution,
        dx,
        dz,
        water_level,
        height,
        mask,
        water_mask,
        stats,
        warnings,
    )
    return height, metadata


def _make_metadata(
    parsed: Mapping[str, Any] | None,
    lmap: Mapping[str, Any] | None,
    bounds: tuple[float, float, float, float],
    bounds_source: str,
    header_bounds: tuple[float, float, float, float] | None,
    dump_bounds: tuple[float, float, float, float] | None,
    resolution: int,
    dx: float,
    dz: float,
    water_level: float,
    height: np.ndarray,
    mask: np.ndarray,
    water_mask: np.ndarray,
    stats: Mapping[str, int],
    warnings: list[str],
) -> dict[str, Any]:
    x_min, x_max, z_min, z_max = bounds
    valid_pixels = int(np.count_nonzero(mask))
    if valid_pixels:
        actual_min = float(np.min(height[mask]))
        actual_max = float(np.max(height[mask]))
    else:
        actual_min = None
        actual_max = None

    ltdump_summary: dict[str, Any] | None = None
    if parsed is not None:
        ltdump_summary = {
            "cells_x": int(parsed["cells_x"]),
            "cells_y": int(parsed["cells_y"]),
            "cell_size_m": float(parsed["cell_size_m"]),
            "origin_m": list(parsed["origin_m"]),
            "bbox_min_m": list(parsed["bbox_min_m"]),
            "bbox_max_m": list(parsed["bbox_max_m"]),
            "cell_count": int(parsed["cell_count"]),
            "vertex_count": int(parsed["vert_count"]),
            "face_count": int(parsed["face_count"]),
            "face_index_count": int(parsed["face_index_count"]),
            "triangle_count": int(parsed["triangle_count"]),
            "grid_count": int(parsed["grid_count"]),
            "grid_ht_count": int(parsed["grid_ht_count"]),
            "grid_ht_used_as_height": False,
            "trailer_bytes": int(parsed["trailer_bytes"]),
        }

    return {
        "bounds": tuple(float(v) for v in bounds),
        "bounds_axis_order": ("x_min", "x_max", "z_min", "z_max"),
        "bounds_dict": {
            "x": [float(x_min), float(x_max)],
            "z": [float(z_min), float(z_max)],
        },
        "bounds_source": bounds_source,
        "lmap_header_bounds": header_bounds,
        "ltdump_bounds": dump_bounds,
        "origin_m": (float(x_min), float(z_min)),
        "cell_size_m": (float(dx), float(dz)),
        "pixel_size_m": (float(dx), float(dz)),
        "resolution": (resolution, resolution),
        "shape": (resolution, resolution),
        "row_axis": "+Z",
        "column_axis": "+X",
        "pixel_center_offset": (0.5, 0.5),
        "height_dtype": "float32",
        "height_units": "m",
        "water_level_m": float(water_level),
        "waterlevel": float(water_level),
        "mask": mask,
        "terrain_mask": mask,
        "water_mask": water_mask,
        "mask_semantics": (
            "mask is true only where a decoded triangle covers the pixel; "
            "uncovered pixels contain water_level_m"
        ),
        "valid_pixel_count": valid_pixels,
        "min_height_m": actual_min,
        "max_height_m": actual_max,
        "empty": parsed is None
        or int(parsed["triangle_count"]) == 0
        or valid_pixels == 0,
        "stats": dict(stats),
        "lmap": dict(lmap) if lmap is not None else None,
        "ltdump": ltdump_summary,
        "warnings": warnings,
    }


# ---------------------------------------------------------------------------
# Public API


def decode_ltdump(
    dump: bytes | bytearray | memoryview,
    bounds: tuple[float, float, float, float] | None = None,
    resolution: int = 2048,
    water_level: float = 0.0,
) -> tuple[np.ndarray, dict[str, Any]]:
    """Rasterise an already-uncompressed LTdump payload.

    This is a convenience entry point for tests and for a caller that already
    owns a decompressed stream.  bounds uses the same order as decode_lmap:
    (x_min, x_max, z_min, z_max).
    """

    parsed = parse_ltdump(dump)
    return _rasterise_parsed(
        parsed,
        None,
        bounds=bounds,
        resolution=resolution,
        water_level=water_level,
    )


def inspect_lmap(path: Path) -> dict[str, Any]:
    """Read the DBLD/lmap header without decompressing the ray frame.

    This is the cheap routing check for a catalog worker: inspect_lmap(path)
    ["lmap"]["has_tracer"] tells whether decode_lmap has an LTdump surface to
    read.  A false value routes to render_mesh.decode_render_mesh for the
    LandMesh triangle fallback; decode_lmap intentionally raises for it.
    """

    payload, container = _read_lmap_payload(Path(path))
    return {
        "container": container,
        "lmap": _parse_lmap_header(payload),
    }


def decode_lmap(
    path: Path,
    decompress: Callable[[bytes, int, int], bytes],
    bounds: tuple[float, float, float, float] | None = None,
    resolution: int = 2048,
    water_level: float = 0.0,
) -> tuple[np.ndarray, dict[str, Any]]:
    """Decode the real triangle terrain in a level's lmap block.

    Parameters
    ----------
    path:
        DBLD3x64 level (or a bare lndm payload).
    decompress:
        decompress(data, expectedbytes, codec) -> bytes.  data is the
        compressed frame body only.  Oodle frames have their four-byte src_sz
        prefix validated and stripped before this call.
    bounds:
        Optional (x_min, x_max, z_min, z_max) output window in world metres.
        If omitted, the lmap header's map extent is used (falling back to the
        LTdump grid extent for a bare payload).
    resolution:
        Output width and height.  resolution * resolution is capped at
        64,000,000 cells.
    water_level:
        Value written where no triangle covers a pixel, in metres.

    Returns
    -------
    height, metadata:
        height is float32 metres, rows +Z and columns +X.  metadata contains
        bounds, origin, pixel sizes, the terrain/water masks, the decoded
        LTdump summary, and processing counters.
    """

    if not callable(decompress):
        raise TypeError("decompress must be callable")
    payload, container = _read_lmap_payload(Path(path))
    lmap = _parse_lmap_header(payload)

    # A tracer-less lmap is not a successful empty terrain.  The caller must
    # fall back to a different map variant (for example FRT) or a render-mesh
    # decoder; returning an all-water image here would silently accept zero
    # valid pixels.
    if not lmap["has_tracer"]:
        raise LmapFormatError(
            f"{path}: lmap has no LandRayTracer frame (rayTracerOfs="
            f"{lmap['ray_tracer_ofs']}); no LTdump terrain is available"
        )

    dump: bytes | None = None
    frame_meta: dict[str, Any] = {"has_tracer": False}
    if lmap["has_tracer"]:
        dump, frame_meta = _decompress_frame(payload, lmap, decompress)
    parsed = parse_ltdump(dump) if dump is not None else None

    height, metadata = _rasterise_parsed(
        parsed,
        lmap,
        bounds=bounds,
        resolution=resolution,
        water_level=water_level,
    )
    metadata["source"] = str(path)
    metadata["container"] = dict(container)
    metadata["frame"] = dict(frame_meta)
    return height, metadata

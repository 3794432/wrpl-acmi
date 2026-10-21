"""Decode the lmap LandMesh render mesh into a metric triangle terrain raster.

This is the fallback for levels whose lmap has no LandRayTracer frame.  It
parses the LMESH/matVData vertex and index tables directly and rasterises real
triangles.  It does not rasterise a vertex point cloud and does not fill holes
with neighbour averages.

The layout follows Dagor Asset Explorer's LandMesh, ShaderMesh and MatVData
parsers, checked against the official Dagor LMESH/matVData structures:

  LandMesh header: texCnt, matCnt, vdataCnt, mvdHdrSz
  name-map header: texMapSz, texIndicesOfs, texCnt
  CompressedData:  3-byte compressed size, 1-byte method, payload
  MatVData:        matOfs, matCnt, 8 reserved bytes, gvdOfs, gvdCnt
  GlobalVertexData: 0x20-byte records; land LOD0 uses stride 8 SHORT4N
  CellData:        land[0], land[1], decal, combined, optional patches
  ShaderMesh Elem:  vData, startV, numV, startI, numFace, baseVertex

The unpacked index tables in the tested land meshes are u16 values in the
global GVD vertex range.  They are converted to cell-local indices and passed
to lmap's bounded triangle rasteriser together with the real vertex positions.

decode_render_mesh(path, decompress, bounds=None, resolution=2048,
                   water_level=0.0) -> (float32 grid, metadata)

bounds uses (x_min, x_max, z_min, z_max).  The grid has rows increasing toward
+Z and columns increasing toward +X.  mask is true only for pixels covered by
decoded triangles; uncovered pixels contain water_level.

Callers that decode Oodle MVD blocks should create the process MemoryGuard
before importing numpy or ooz.  This module deliberately imports numpy at
module load, so the worker must set the guard first.
"""

from __future__ import annotations

import math
import struct
import zlib
from pathlib import Path
from typing import Any, Callable, Mapping

import numpy as np

import lmap

__all__ = [
    "RenderMeshFormatError",
    "decode_render_mesh",
]


MAX_SECTION_BYTES = 512 << 20
MAX_TRIANGLES = 64_000_000
MAX_ELEMS_PER_MESH = 1_000_000
MAX_GVDS = 4096


class RenderMeshFormatError(lmap.LmapFormatError):
    """Raised when the lmap render-mesh tables violate their format."""


def _u32(data: bytes | memoryview, offset: int, what: str) -> int:
    if offset < 0 or offset + 4 > len(data):
        raise RenderMeshFormatError(f"{what}: truncated u32 at 0x{offset:X}")
    return struct.unpack_from("<I", data, offset)[0]


def _i32(data: bytes | memoryview, offset: int, what: str) -> int:
    if offset < 0 or offset + 4 > len(data):
        raise RenderMeshFormatError(f"{what}: truncated i32 at 0x{offset:X}")
    return struct.unpack_from("<i", data, offset)[0]


def _require_range(
    offset: int, size: int, total: int, what: str
) -> None:
    if offset < 0 or size < 0 or offset + size > total:
        raise RenderMeshFormatError(
            f"{what}: range 0x{offset:X}+{size} exceeds 0x{total:X}"
        )


def _parse_compressed(
    payload: bytes,
    position: int,
    decompress: Callable[[bytes, int, int], bytes],
) -> tuple[bytes, int, dict[str, Any]]:
    """Parse one CompressedData block used by the LMESH/MVD tables."""

    _require_range(position, 4, len(payload), "LMESH CompressedData header")
    compressed_size = (
        payload[position]
        | (payload[position + 1] << 8)
        | (payload[position + 2] << 16)
    )
    method = payload[position + 3]
    if compressed_size <= 0 or compressed_size > MAX_SECTION_BYTES:
        raise RenderMeshFormatError(
            f"LMESH CompressedData size {compressed_size} is outside 1.."
            f"{MAX_SECTION_BYTES}"
        )
    data_start = position + 4
    _require_range(data_start, compressed_size, len(payload), "LMESH CompressedData")
    compressed = payload[data_start : data_start + compressed_size]
    expected = None

    if method == 0x80:
        # DAE Oodle blocks carry the uncompressed size as a u32 prefix.
        if len(compressed) < 4:
            raise RenderMeshFormatError("LMESH Oodle block lacks its size prefix")
        expected = struct.unpack_from("<I", compressed, 0)[0]
        if expected <= 0 or expected > MAX_SECTION_BYTES:
            raise RenderMeshFormatError(
                f"LMESH Oodle expected size {expected} is outside the cap"
            )
        try:
            result = decompress(bytes(compressed[4:]), expected, 2)
        except Exception as exc:
            raise RenderMeshFormatError(
                f"LMESH Oodle decompression failed: {exc}"
            ) from exc
    elif method == 0x40:
        try:
            result = decompress(bytes(compressed), 0, 1)
        except Exception as exc:
            raise RenderMeshFormatError(
                f"LMESH ZSTD decompression failed: {exc}"
            ) from exc
    elif method == 0x60:
        try:
            # zlib.decompress has no output cap; use a decompressor object so
            # a hostile inner block cannot expand past the shared limit.
            decoder = zlib.decompressobj()
            result = decoder.decompress(
                bytes(compressed), MAX_SECTION_BYTES + 1
            )
            if len(result) > MAX_SECTION_BYTES or decoder.unconsumed_tail:
                raise ValueError("zlib output exceeds the section cap")
            tail = decoder.flush(MAX_SECTION_BYTES + 1 - len(result))
            result += tail
        except Exception as exc:
            raise RenderMeshFormatError(
                f"LMESH zlib decompression failed: {exc}"
            ) from exc
    elif method == 0x20:
        try:
            result = decompress(bytes(compressed), 0, 0)
        except Exception as exc:
            raise RenderMeshFormatError(
                f"LMESH LZMA decompression failed: {exc}"
            ) from exc
    else:
        raise RenderMeshFormatError(
            f"LMESH CompressedData uses unknown method 0x{method:02X}"
        )

    if isinstance(result, np.ndarray):
        result = result.tobytes()
    elif isinstance(result, memoryview):
        result = result.tobytes()
    elif isinstance(result, bytearray):
        result = bytes(result)
    if not isinstance(result, bytes):
        raise RenderMeshFormatError(
            f"LMESH decompressor returned {type(result).__name__}; bytes required"
        )
    if not result or len(result) > MAX_SECTION_BYTES:
        raise RenderMeshFormatError(
            f"LMESH decompressed size {len(result)} is outside the cap"
        )
    if expected is not None and len(result) != expected:
        raise RenderMeshFormatError(
            f"LMESH Oodle decoded {len(result)} bytes, expected {expected}"
        )
    return result, data_start + compressed_size, {
        "method": method,
        "compressed_bytes": compressed_size,
        "decompressed_bytes": len(result),
        "expected_bytes": expected,
    }


def _parse_shader_mesh(
    cell: bytes,
    position: int,
    cell_index: int,
    kind: str,
) -> tuple[dict[str, Any], int]:
    """Parse one ShaderMesh and return its elements and end position."""

    _require_range(position, 40, len(cell), f"cell {cell_index} {kind} ShaderMesh")
    shader_ofs = _i32(cell, position, f"cell {cell_index} {kind} ofs")
    count = _i32(cell, position + 4, f"cell {cell_index} {kind} count")
    position += 8 + 8
    stages = struct.unpack_from("<8H", cell, position)
    position += 16
    deprecated = _i32(cell, position, f"cell {cell_index} {kind} maxPass")
    position += 4
    _reserved = _i32(cell, position, f"cell {cell_index} {kind} reserved")
    position += 4

    if count < 0 or count > MAX_ELEMS_PER_MESH:
        raise RenderMeshFormatError(
            f"cell {cell_index} {kind} has invalid element count {count}"
        )
    if deprecated < 0:
        deprecated &= 0x7FFFFFFF
    elif stages[2] != 0:
        count += stages[2]
    if count > MAX_ELEMS_PER_MESH:
        raise RenderMeshFormatError(
            f"cell {cell_index} {kind} element count {count} exceeds the cap"
        )
    _require_range(
        position, count * 48, len(cell), f"cell {cell_index} {kind} elements"
    )

    elems: list[tuple[int, int, int, int, int, int]] = []
    for _ in range(count):
        _ptr, _mat, vdata = struct.unpack_from("<3Q", cell, position)
        position += 24
        (
            _vd_order,
            start_v,
            num_v,
            start_i,
            num_face,
            base_vertex,
        ) = struct.unpack_from("<6i", cell, position)
        position += 24
        if (
            vdata > 0x7FFFFFFF
            or start_v < 0
            or num_v < 0
            or start_i < 0
            or num_face < 0
        ):
            raise RenderMeshFormatError(
                f"cell {cell_index} {kind} has a negative/oversized element field"
            )
        elems.append(
            (
                int(vdata),
                int(start_v),
                int(num_v),
                int(start_i),
                int(num_face),
                int(base_vertex),
            )
        )
    return {
        "ofs": int(shader_ofs),
        "count": int(count),
        "stages": tuple(int(v) for v in stages),
        "elems": elems,
    }, position


def _parse_cell(
    cell: bytes,
    cell_index: int,
) -> list[tuple[int, int, int, int, int, int]]:
    if len(cell) < 4:
        raise RenderMeshFormatError(f"cell {cell_index} is too short")
    block_size = _u32(cell, 0, f"cell {cell_index} block size")
    del block_size
    position = 4
    land0 = None
    for pair in range(4):
        kind_name = ("land0", "land1", "decal", "combined")[pair]
        kind = _i32(cell, position, f"cell {cell_index} {kind_name} kind")
        del kind
        position += 4
        shader, position = _parse_shader_mesh(
            cell, position, cell_index, kind_name
        )
        if pair == 0:
            land0 = shader["elems"]
    # Newer levels may append a patches ShaderMesh.  It is not a terrain LOD
    # and is ignored, but its presence must not make a valid cell fail.
    if position < len(cell):
        try:
            _parse_shader_mesh(cell, position, cell_index, "patches")
        except RenderMeshFormatError:
            # A few cell blocks leave trailing non-ShaderMesh bytes.  The
            # terrain LOD0 has already been parsed; only reject if there is
            # not enough room for even the ShaderMesh header.
            if len(cell) - position >= 40:
                raise
    return land0 or []


def _parse_landmesh(
    payload: bytes,
    info: Mapping[str, Any],
    decompress: Callable[[bytes, int, int], bytes],
) -> dict[str, Any]:
    mesh_ofs = int(info["mesh_map_ofs"])
    _require_range(mesh_ofs, 36, len(payload), "LandMesh header")
    tex_cnt, mat_cnt, vdata_cnt, mvd_hdr = struct.unpack_from(
        "<4i", payload, mesh_ofs
    )
    tex_map_sz, tex_indices_ofs, tex_cnt2 = struct.unpack_from(
        "<3i", payload, mesh_ofs + 16
    )
    del tex_cnt, mat_cnt, vdata_cnt, mvd_hdr, tex_map_sz
    if tex_cnt2 < 0 or tex_cnt2 > 1_000_000:
        raise RenderMeshFormatError(f"LandMesh texCnt is {tex_cnt2}")
    if tex_indices_ofs < 0x10:
        raise RenderMeshFormatError(
            f"LandMesh texIndicesOfs is {tex_indices_ofs}, below 0x10"
        )
    name_length = tex_indices_ofs - 0x10
    position = mesh_ofs + 16 + 12 + 8
    _require_range(position, name_length, len(payload), "LandMesh name map")
    position += name_length
    _require_range(position, tex_cnt2 * 4, len(payload), "LandMesh name offsets")
    position += tex_cnt2 * 4

    mvd, after_mvd, compression = _parse_compressed(payload, position, decompress)
    if len(mvd) < 24:
        raise RenderMeshFormatError(f"MatVData is only {len(mvd)} bytes")
    mat_ofs = _i32(mvd, 0, "MatVData matOfs")
    mat_count = _i32(mvd, 4, "MatVData matCount")
    gvd_ofs = _i32(mvd, 16, "MatVData gvdOfs")
    gvd_count = _i32(mvd, 20, "MatVData gvdCount")
    if gvd_count < 0 or gvd_count > MAX_GVDS:
        raise RenderMeshFormatError(f"MatVData gvdCount is {gvd_count}")
    if gvd_ofs < 24:
        raise RenderMeshFormatError(f"MatVData gvdOfs is {gvd_ofs}")
    _require_range(gvd_ofs, gvd_count * 0x20, len(mvd), "MatVData GVD table")

    vd_start = gvd_ofs + gvd_count * 0x20
    gvds: list[dict[str, Any]] = []
    cursor = vd_start
    for index in range(gvd_count):
        offset = gvd_ofs + index * 0x20
        vertex_count = _u32(mvd, offset, f"GVD {index} vertex count")
        stride = mvd[offset + 4]
        packed_indices = (
            mvd[offset + 5]
            | (mvd[offset + 6] << 8)
            | (mvd[offset + 7] << 16)
        )
        index_size = _u32(mvd, offset + 8, f"GVD {index} index size")
        flags = struct.unpack_from("<H", mvd, offset + 12)[0]
        index_count = _u32(mvd, offset + 16, f"GVD {index} index count")
        storage_format = _i32(mvd, offset + 20, f"GVD {index} storage format")
        vertex_bytes = vertex_count * stride
        index_bytes = packed_indices or index_size
        _require_range(
            cursor,
            vertex_bytes + index_bytes,
            len(mvd),
            f"GVD {index} vertex/index data",
        )
        gvds.append(
            {
                "index": index,
                "vertex_count": int(vertex_count),
                "stride": int(stride),
                "packed_indices": int(packed_indices),
                "index_size": int(index_size),
                "index_count": int(index_count),
                "flags": int(flags),
                "storage_format": int(storage_format),
                "vertex_offset": int(cursor),
                "index_offset": int(cursor + vertex_bytes),
                "index_bytes": int(index_bytes),
            }
        )
        cursor += vertex_bytes + index_bytes

    sm_length = _u32(payload, after_mvd, "LandMesh ShaderMesh block length")
    if sm_length <= 0 or sm_length > MAX_SECTION_BYTES:
        raise RenderMeshFormatError(
            f"LandMesh ShaderMesh block size {sm_length} is outside the cap"
        )
    sm_start = after_mvd + 4
    _require_range(sm_start, sm_length, len(payload), "LandMesh ShaderMesh block")
    sm = payload[sm_start : sm_start + sm_length]
    cell_count = int(info["map_size"][0]) * int(info["map_size"][1])
    if cell_count <= 0 or cell_count > lmap.MAX_CELLS:
        raise RenderMeshFormatError(f"LandMesh cell count {cell_count} is invalid")

    position = 0
    cells: list[list[tuple[int, int, int, int, int, int]]] = []
    total_triangles = 0
    for cell_index in range(cell_count):
        cell_length = _u32(sm, position, f"cell {cell_index} block length")
        position += 4
        if cell_length <= 0 or cell_length > MAX_SECTION_BYTES:
            raise RenderMeshFormatError(
                f"cell {cell_index} block length {cell_length} is outside the cap"
            )
        _require_range(position, cell_length, len(sm), f"cell {cell_index} block")
        cell = sm[position : position + cell_length]
        position += cell_length
        elems = _parse_cell(cell, cell_index)
        cells.append(elems)
        total_triangles += sum(elem[4] for elem in elems)
        if total_triangles > MAX_TRIANGLES:
            raise RenderMeshFormatError(
                f"LandMesh triangle count exceeds the {MAX_TRIANGLES} cap"
            )

    bounds_length = _u32(sm, position, "LandMesh cell-bounds length")
    position += 4
    if bounds_length < cell_count * 24:
        raise RenderMeshFormatError(
            f"LandMesh cell-bounds block is {bounds_length} bytes, "
            f"needs at least {cell_count * 24}"
        )
    _require_range(position, bounds_length, len(sm), "LandMesh cell bounds")
    cell_bounds = np.frombuffer(
        sm, dtype="<f4", count=cell_count * 6, offset=position
    ).reshape(cell_count, 2, 3)
    global_min_y = float(np.min(cell_bounds[:, 0, 1]))
    global_max_y = float(np.max(cell_bounds[:, 1, 1]))
    if not math.isfinite(global_min_y) or not math.isfinite(global_max_y):
        raise RenderMeshFormatError("LandMesh cell bounds contain a non-finite Y")

    return {
        "mvd": mvd,
        "gvds": gvds,
        "cells": cells,
        "cell_bounds": cell_bounds,
        "global_min_y": global_min_y,
        "global_max_y": global_max_y,
        "triangle_count": int(total_triangles),
        "compression": compression,
        "mat_ofs": int(mat_ofs),
        "mat_count": int(mat_count),
        "gvd_count": int(gvd_count),
        "tex_count": int(tex_cnt2),
        "sm_bytes": int(sm_length),
        "cell_count": int(cell_count),
    }


def _cell_vertices(
    mvd: bytes,
    gvd: Mapping[str, Any],
    elem: tuple[int, int, int, int, int, int],
    center_x: float,
    center_z: float,
    cell_size: float,
    y_scale: float,
    y_center: float,
) -> tuple[np.ndarray, np.ndarray]:
    _vdata, start_v, num_v, start_i, num_face, base_vertex = elem
    if gvd["stride"] != 8:
        raise RenderMeshFormatError(
            f"land LOD0 uses stride {gvd['stride']}; only SHORT4N stride 8 is supported"
        )
    if gvd["packed_indices"] != 0:
        raise RenderMeshFormatError(
            "land LOD0 uses packed indices; the unpacked u16 path is required"
        )
    if start_v + num_v > gvd["vertex_count"]:
        raise RenderMeshFormatError(
            f"LOD0 vertex range {start_v}+{num_v} exceeds "
            f"GVD {gvd['index']} count {gvd['vertex_count']}"
        )
    index_count = gvd["index_size"] // 2
    index_end = start_i + num_face * 3
    if index_end > index_count:
        raise RenderMeshFormatError(
            f"LOD0 index range {start_i}+{num_face * 3} exceeds "
            f"GVD {gvd['index']} count {index_count}"
        )

    q = np.frombuffer(
        mvd,
        dtype="<i2",
        count=num_v * 4,
        offset=gvd["vertex_offset"] + start_v * 8,
    ).reshape(-1, 4)
    q_xyz = q[:, :3].astype(np.float64) / 32767.0
    scale = np.array(
        [cell_size * 0.5, y_scale, cell_size * 0.5], dtype=np.float64
    )
    offset = np.array(
        [center_x, y_center, center_z], dtype=np.float64
    )
    world = q_xyz * scale + offset

    raw_indices = np.frombuffer(
        mvd,
        dtype="<u2",
        count=num_face * 3,
        offset=gvd["index_offset"] + start_i * 2,
    ).astype(np.int64)
    local = raw_indices - start_v
    if np.any(local < 0) or np.any(local >= num_v):
        # Some builders store indices relative to baseVertex instead of the
        # element start.  Accept that representation only when it is consistent.
        local = raw_indices - base_vertex
        if np.any(local < 0) or np.any(local >= num_v):
            raise RenderMeshFormatError(
                f"LOD0 face index outside its {num_v}-vertex range"
            )
    return world, local.reshape(-1, 3)


def decode_render_mesh(
    path: Path,
    decompress: Callable[[bytes, int, int], bytes],
    bounds: tuple[float, float, float, float] | None = None,
    resolution: int = 2048,
    water_level: float = 0.0,
) -> tuple[np.ndarray, dict[str, Any]]:
    """Decode lmap LandMesh triangles into a metric raster.

    This is intended for levels whose lmap has no LandRayTracer frame.  It may
    also be used as a cross-check on levels that do have an LTdump, but the
    LTdump path is the authoritative surface for those levels.
    """

    if not callable(decompress):
        raise TypeError("decompress must be callable")
    requested = lmap._validate_raster_args(bounds, resolution, water_level)
    payload, container = lmap._read_lmap_payload(Path(path))
    info = lmap._parse_lmap_header(payload)
    land = _parse_landmesh(payload, info, decompress)

    map_x, map_z = info["map_size"]
    cell_size = float(info["land_cell_size_m"])
    origin_x, origin_z = info["origin_cells"]
    header_bounds = (
        float(origin_x * cell_size),
        float((origin_x + map_x) * cell_size),
        float(origin_z * cell_size),
        float((origin_z + map_z) * cell_size),
    )
    if requested is not None:
        x_min, x_max, z_min, z_max = requested
        bounds_source = "requested"
    else:
        x_min, x_max, z_min, z_max = header_bounds
        bounds_source = "lmap_header"
    dx = (x_max - x_min) / resolution
    dz = (z_max - z_min) / resolution
    if not (math.isfinite(dx) and math.isfinite(dz) and dx > 0 and dz > 0):
        raise RenderMeshFormatError("bounds produce invalid pixel sizes")

    y_min = float(land["global_min_y"])
    y_max = float(land["global_max_y"])
    y_scale = 0.5 * (y_max - y_min)
    y_center = 0.5 * (y_max + y_min)

    height = np.full((resolution, resolution), water_level, dtype=np.float32)
    mask = np.zeros((resolution, resolution), dtype=bool)
    height_flat = height.reshape(-1)
    mask_flat = mask.reshape(-1)
    stats: dict[str, int] = {
        "triangles_total": int(land["triangle_count"]),
        "triangles_rasterized": 0,
        "degenerate_triangles": 0,
        "outside_bounds_triangles": 0,
        "candidate_pixels": 0,
        "written_candidates": 0,
        "cells_with_geometry": sum(1 for elems in land["cells"] if elems),
        "cells_processed": 0,
    }
    warnings: list[str] = []

    gvds = land["gvds"]
    mvd = land["mvd"]
    cells = land["cells"]
    for cell_index, elems in enumerate(cells):
        if not elems:
            continue
        cell_x = cell_index % map_x
        cell_z = cell_index // map_x
        center_x = (origin_x + cell_x + 0.5) * cell_size
        center_z = (origin_z + cell_z + 0.5) * cell_size
        cell_x0 = center_x - cell_size * 0.5
        cell_z0 = center_z - cell_size * 0.5
        if (
            cell_x0 + cell_size < x_min
            or cell_x0 > x_max
            or cell_z0 + cell_size < z_min
            or cell_z0 > z_max
        ):
            continue
        for elem in elems:
            vdata = elem[0]
            if vdata >= len(gvds):
                raise RenderMeshFormatError(
                    f"cell {cell_index} references GVD {vdata} of {len(gvds)}"
                )
            world, tri = _cell_vertices(
                mvd,
                gvds[vdata],
                elem,
                center_x,
                center_z,
                cell_size,
                y_scale,
                y_center,
            )
            lmap._rasterize_cell(
                world,
                tri,
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
    valid_pixels = int(np.count_nonzero(mask))
    if valid_pixels:
        actual_min = float(np.min(height[mask]))
        actual_max = float(np.max(height[mask]))
    else:
        actual_min = None
        actual_max = None

    metadata: dict[str, Any] = {
        "source": str(path),
        "stream": "lmap/render-mesh",
        "container": dict(container),
        "lmap": dict(info),
        "bounds": (float(x_min), float(x_max), float(z_min), float(z_max)),
        "bounds_axis_order": ("x_min", "x_max", "z_min", "z_max"),
        "bounds_source": bounds_source,
        "lmap_header_bounds": header_bounds,
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
        "mask": mask,
        "terrain_mask": mask,
        "water_mask": water_mask,
        "valid_pixel_count": valid_pixels,
        "min_height_m": actual_min,
        "max_height_m": actual_max,
        "empty": valid_pixels == 0,
        "stats": stats,
        "landmesh": {
            "tex_count": land["tex_count"],
            "mat_count": land["mat_count"],
            "gvd_count": land["gvd_count"],
            "cell_count": land["cell_count"],
            "sm_bytes": land["sm_bytes"],
            "triangle_count": land["triangle_count"],
            "cell_y_min_m": y_min,
            "cell_y_max_m": y_max,
            "y_scale_m": y_scale,
            "y_center_m": y_center,
            "compression": land["compression"],
            "gvds": [
                {
                    "index": g["index"],
                    "vertex_count": g["vertex_count"],
                    "stride": g["stride"],
                    "packed_indices": g["packed_indices"],
                    "index_size": g["index_size"],
                    "index_count": g["index_count"],
                    "storage_format": g["storage_format"],
                }
                for g in gvds
            ],
        },
        "warnings": warnings,
    }
    return height, metadata

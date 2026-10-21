"""Synthetic tests for the lmap LandMesh render-mesh decoder.

The fixtures build the same LMESH/matVData/ShaderMesh tables the DAE parser
reads, with zlib CompressedData so no Oodle backend is needed.  The real-map
Oodle smoke is run separately under MemoryGuard.
"""

from __future__ import annotations

import struct
import tempfile
import unittest
import zlib
from pathlib import Path

import numpy as np

import render_mesh
from lmap import LmapFormatError
from render_mesh import RenderMeshFormatError, decode_render_mesh


def _pack_i32(value: int) -> bytes:
    return struct.pack("<i", value)


def _pack_u32(value: int) -> bytes:
    return struct.pack("<I", value)


def _shader(elems):
    header = bytearray()
    header += _pack_i32(40)          # ofs
    header += _pack_i32(len(elems))  # count
    header += b"\0" * 8
    header += struct.pack("<8H", *([0] * 8))
    header += _pack_i32(0)           # deprecated max pass
    header += _pack_i32(0)           # reserved
    body = bytearray()
    for vdata, start_v, num_v, start_i, num_face, base_v in elems:
        body += struct.pack("<3Q", 0, 0, vdata)
        body += struct.pack("<6i", 0, start_v, num_v, start_i, num_face, base_v)
    return bytes(header + body)


def _cell(land0_elems):
    land0 = _pack_i32(88) + _shader(land0_elems)
    land1 = _pack_i32(88) + _shader([])
    decal = _pack_i32(40) + _shader([])
    combined = _pack_i32(40) + _shader([])
    payload = land0 + land1 + decal + combined
    return _pack_u32(4 + len(payload))[0:4] + payload


def _build_mvd(vertices, faces, *, packed=0, stride=8):
    vertex_bytes = len(vertices) * stride
    index_bytes = len(faces) * 2
    mvd = bytearray(_pack_i32(0) + _pack_i32(0) + b"\0" * 8)
    mvd += _pack_i32(24) + _pack_i32(1)
    mvd += _pack_u32(len(vertices))
    mvd += bytes([stride])
    mvd += bytes([packed & 0xFF, (packed >> 8) & 0xFF, (packed >> 16) & 0xFF])
    mvd += _pack_u32(index_bytes)
    mvd += struct.pack("<H", 0x20)
    mvd += struct.pack("<H", 0)
    mvd += _pack_u32(1)
    mvd += _pack_i32(1)
    mvd += b"\0" * 8
    assert len(mvd) == 56
    for q in vertices:
        mvd += struct.pack("<4h", *q)
    mvd += struct.pack("<" + "H" * len(faces), *faces)
    return bytes(mvd)


def _compressed_zlib(data: bytes) -> bytes:
    compressed = zlib.compress(data, 6)
    if len(compressed) >= 1 << 24:
        raise ValueError("test MVD is too large for a 3-byte size")
    return (
        bytes(
            [
                len(compressed) & 0xFF,
                (len(compressed) >> 8) & 0xFF,
                (len(compressed) >> 16) & 0xFF,
                0x60,
            ]
        )
        + compressed
    )


def _q4(x, y, z, center_x, center_z, cell_size, y_scale, y_center):
    sx = cell_size * 0.5
    qx = int(round((x - center_x) / sx * 32767.0))
    qy = int(round((y - y_center) / y_scale * 32767.0)) if y_scale else 0
    qz = int(round((z - center_z) / sx * 32767.0))
    q = (qx, qy, qz, 32767)
    if any(v < -32768 or v > 32767 for v in q):
        raise ValueError(f"fixture q out of int16 range: {q}")
    return q


def _build_render_lmap(
    cell_specs,
    *,
    cells_x: int,
    cells_y: int,
    cell_size: float = 10.0,
    packed: int = 0,
    stride: int = 8,
    bad_index: bool = False,
):
    if len(cell_specs) != cells_x * cells_y:
        raise ValueError("cell_specs must match the grid")
    points = []
    for spec in cell_specs:
        if spec is not None:
            points.extend(spec["verts"])
    # The decoder derives the global Y normalization from every cell's
    # bounds block, including an empty cell's zero bounds.  Mirror that here.
    y_values = [p[1] for p in points]
    y_values.extend(0.0 for spec in cell_specs if spec is None)
    y_min = min(y_values) if y_values else 0.0
    y_max = max(y_values) if y_values else 0.0
    y_scale = 0.5 * (y_max - y_min)
    y_center = 0.5 * (y_max + y_min)

    all_q = []
    all_faces = []
    cell_elems = []
    bounds = []
    for index, spec in enumerate(cell_specs):
        cell_x = index % cells_x
        cell_z = index // cells_x
        center_x = (cell_x + 0.5) * cell_size
        center_z = (cell_z + 0.5) * cell_size
        x0 = cell_x * cell_size
        z0 = cell_z * cell_size
        if spec is None:
            cell_elems.append([])
            bounds.append((x0, 0.0, z0, x0 + cell_size, 0.0, z0 + cell_size))
            continue
        verts = spec["verts"]
        faces = spec["faces"]
        start_v = len(all_q)
        start_i = len(all_faces)
        for x, y, z in verts:
            all_q.append(
                _q4(x, y, z, center_x, center_z, cell_size, y_scale, y_center)
            )
        all_faces.extend(start_v + int(v) for v in faces)
        if bad_index and all_faces:
            all_faces[-1] = start_v + len(verts) + 3
        cell_elems.append(
            [(0, start_v, len(verts), start_i, len(faces) // 3, 0)]
        )
        bounds.append(
            (
                x0,
                min(v[1] for v in verts),
                z0,
                x0 + cell_size,
                max(v[1] for v in verts),
                z0 + cell_size,
            )
        )

    mvd = _build_mvd(all_q, all_faces, packed=packed, stride=stride)
    sm = bytearray()
    for elems in cell_elems:
        cell_block = _cell(elems)
        sm += _pack_u32(len(cell_block)) + cell_block
    bounds_block = bytearray()
    for b in bounds:
        bounds_block += struct.pack("<6f", *b)
    for _ in range(len(bounds)):
        bounds_block += struct.pack("<f", 0.0)
    sm += _pack_u32(len(bounds_block)) + bounds_block

    landmesh = bytearray()
    landmesh += _pack_i32(0) + _pack_i32(0) + _pack_i32(1) + _pack_i32(0)
    landmesh += _pack_i32(0) + _pack_i32(0x10) + _pack_i32(0)
    landmesh += b"\0" * 8
    landmesh += _compressed_zlib(mvd)
    landmesh += _pack_u32(len(sm)) + sm

    header = struct.pack(
        "<4sI2f5i",
        b"lndm",
        4,
        cell_size,
        cell_size,
        cells_x,
        cells_y,
        0,
        0,
        0,
    )
    # meshOfs=16 points at baseDataOffset+16 (absolute 52).  detail/tile
    # point into the mesh header; rayTracerOfs=0 deliberately means no LTdump.
    payload = header + struct.pack("<4i", 16, 17, 18, 0) + bytes(landmesh)
    return b"DBLD3x64" + b"\0" * 4 + struct.pack("<I", 4 + len(payload)) + b"lmap" + payload


def _plane_cell(cell_size=10.0):
    return {
        "verts": [
            (0.0, 2.0, 0.0),
            (cell_size, 3.0, 0.0),
            (0.0, 4.0, cell_size),
            (cell_size, 5.0, cell_size),
        ],
        "faces": [0, 1, 2, 1, 3, 2],
    }


class RenderMeshTests(unittest.TestCase):
    def setUp(self):
        base = Path(__file__).resolve().parent
        self.temp = tempfile.TemporaryDirectory(
            prefix=".render_mesh_test_",
            dir=base,
            ignore_cleanup_errors=True,
        )
        self.root = Path(self.temp.name)

    def tearDown(self):
        self.temp.cleanup()

    def _write(self, data: bytes) -> Path:
        path = self.root / "level.bin"
        path.write_bytes(data)
        return path

    def _decompress(self, data: bytes, expected: int, codec: int) -> bytes:
        out = zlib.decompress(data)
        if expected and len(out) != expected:
            raise ValueError(f"expected {expected}, got {len(out)}")
        return out

    def test_known_plane_true_triangles(self):
        payload = _build_render_lmap([_plane_cell()], cells_x=1, cells_y=1)
        height, meta = decode_render_mesh(
            self._write(payload),
            self._decompress,
            bounds=(0.0, 10.0, 0.0, 10.0),
            resolution=20,
        )
        self.assertEqual(height.dtype, np.float32)
        self.assertEqual(height.shape, (20, 20))
        self.assertTrue(meta["mask"].all())
        self.assertEqual(meta["landmesh"]["triangle_count"], 2)
        self.assertEqual(meta["stats"]["triangles_total"], 2)
        x = (np.arange(20, dtype=np.float64) + 0.5) * 0.5
        expected = 2.0 + 0.1 * x[None, :] + 0.2 * x[:, None]
        np.testing.assert_allclose(height, expected, atol=2e-3, rtol=0.0)
        self.assertGreater(float(height[-1, -1]), float(height[0, 0]))
        self.assertEqual(meta["bounds_axis_order"], ("x_min", "x_max", "z_min", "z_max"))
        self.assertEqual(meta["stream"], "lmap/render-mesh")
        self.assertGreater(meta["valid_pixel_count"], 0)

    def test_subregion_pixel_centres(self):
        payload = _build_render_lmap([_plane_cell()], cells_x=1, cells_y=1)
        height, meta = decode_render_mesh(
            self._write(payload),
            self._decompress,
            bounds=(2.5, 7.5, 3.0, 8.0),
            resolution=11,
        )
        self.assertEqual(meta["bounds_source"], "requested")
        x = 2.5 + (np.arange(11, dtype=np.float64) + 0.5) * (5.0 / 11.0)
        z = 3.0 + (np.arange(11, dtype=np.float64) + 0.5) * (5.0 / 11.0)
        expected = 2.0 + 0.1 * x[None, :] + 0.2 * z[:, None]
        np.testing.assert_allclose(height, expected, atol=2e-3, rtol=0.0)
        self.assertTrue(meta["mask"].all())

    def test_empty_cell_keeps_mask_and_water(self):
        payload = _build_render_lmap(
            [_plane_cell(), None], cells_x=2, cells_y=1
        )
        height, meta = decode_render_mesh(
            self._write(payload),
            self._decompress,
            bounds=(0.0, 20.0, 0.0, 10.0),
            resolution=20,
            water_level=-3.0,
        )
        self.assertTrue(meta["mask"][:, :10].all())
        self.assertFalse(meta["mask"][:, 10:].any())
        self.assertTrue(np.all(height[:, 10:] == np.float32(-3.0)))
        self.assertTrue(meta["water_mask"][:, 10:].all())
        self.assertEqual(meta["stats"]["cells_with_geometry"], 1)
        self.assertFalse(meta["empty"])

    def test_bad_face_index_is_rejected(self):
        payload = _build_render_lmap(
            [_plane_cell()], cells_x=1, cells_y=1, bad_index=True
        )
        with self.assertRaises(RenderMeshFormatError):
            decode_render_mesh(
                self._write(payload),
                self._decompress,
                bounds=(0.0, 10.0, 0.0, 10.0),
                resolution=8,
            )

    def test_packed_indices_are_rejected_explicitly(self):
        payload = _build_render_lmap(
            [_plane_cell()], cells_x=1, cells_y=1, packed=4
        )
        with self.assertRaises(RenderMeshFormatError):
            decode_render_mesh(
                self._write(payload),
                self._decompress,
                bounds=(0.0, 10.0, 0.0, 10.0),
                resolution=8,
            )

    def test_non_short4n_stride_is_rejected_explicitly(self):
        payload = _build_render_lmap(
            [_plane_cell()], cells_x=1, cells_y=1, stride=16
        )
        with self.assertRaises(RenderMeshFormatError):
            decode_render_mesh(
                self._write(payload),
                self._decompress,
                bounds=(0.0, 10.0, 0.0, 10.0),
                resolution=8,
            )

    def test_truncated_landmesh_is_rejected(self):
        payload = _build_render_lmap([_plane_cell()], cells_x=1, cells_y=1)
        with self.assertRaises((RenderMeshFormatError, LmapFormatError)):
            decode_render_mesh(
                self._write(payload[:-20]),
                self._decompress,
                bounds=(0.0, 10.0, 0.0, 10.0),
                resolution=8,
            )

    def test_invalid_resolution_and_bounds(self):
        payload = _build_render_lmap([_plane_cell()], cells_x=1, cells_y=1)
        path = self._write(payload)
        with self.assertRaises((RenderMeshFormatError, LmapFormatError)):
            decode_render_mesh(path, self._decompress, resolution=0)
        with self.assertRaises((RenderMeshFormatError, LmapFormatError)):
            decode_render_mesh(
                path, self._decompress, bounds=(10.0, 0.0, 0.0, 10.0)
            )


if __name__ == "__main__":
    unittest.main(verbosity=2)

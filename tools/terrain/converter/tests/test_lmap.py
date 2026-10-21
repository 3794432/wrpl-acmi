"""Synthetic and local-header tests for lmap.py.

The geometry fixtures use exact triangle planes so a failure points at the
decoder or rasteriser rather than at a game-specific terrain sample.  The one
optional real-file test only reads the container and lmap header; it never
launches the game, a CDK, or an external decompressor.
"""

from __future__ import annotations

import struct
import os
import tempfile
import unittest
import zlib
from pathlib import Path
from unittest import mock

import numpy as np

import lmap
from lmap import (
    LmapFormatError,
    decode_lmap,
    decode_ltdump,
    inspect_lmap,
    parse_ltdump,
)


_TEST_LEVEL = os.environ.get("WT_TERRAIN_TEST_LEVEL")
REAL_LEVEL = Path(_TEST_LEVEL) if _TEST_LEVEL else Path("__WT_TERRAIN_TEST_LEVEL_NOT_SET__")


def _zlib_decompress(data: bytes, expected: int, codec: int) -> bytes:
    out = zlib.decompress(data)
    if codec == 2 and expected != len(out):
        raise ValueError(f"expected {expected}, got {len(out)}")
    return out


def _cell(x0: float, z0: float, verts, faces):
    return {"x0": x0, "z0": z0, "verts": list(verts), "faces": list(faces)}


def _build_ltdump(
    cell_specs,
    *,
    cells_x: int,
    cells_y: int,
    cell_size: float,
    origin=(0.0, 0.0, 0.0),
    bbox=None,
    grid_count: int = 0,
    grid_ht_count: int = 0,
    face_indices=None,
    include_face_indices: bool = True,
):
    if len(cell_specs) != cells_x * cells_y:
        raise ValueError("cell_specs must be row-major and match the grid")
    all_verts = []
    all_faces = []
    records = np.zeros(cells_x * cells_y, dtype=lmap.CELL_DTYPE)
    world_points = []

    for index, spec in enumerate(cell_specs):
        cell_x = origin[0] + (index % cells_x) * cell_size
        cell_z = origin[2] + (index // cells_x) * cell_size
        record = records[index]
        if spec is None:
            record["scale"] = (1.0, 1.0, 1.0, 0.0)
            record["ofs"] = (cell_x, 0.0, cell_z, 0.0)
            record["maxHt"] = 0.0
            record["vertsStart"] = sum(v.shape[0] for v in all_verts)
            record["facesStart"] = len(all_faces)
            continue

        verts = np.asarray(spec["verts"], dtype=np.float64)
        if verts.ndim != 2 or verts.shape[1] != 3:
            raise ValueError("verts must be an Nx3 array")
        y_min = float(np.min(verts[:, 1]))
        y_max = float(np.max(verts[:, 1]))
        scale = np.array(
            [
                cell_size / 65535.0,
                max((y_max - y_min) / 65535.0, 1e-6),
                cell_size / 65535.0,
            ],
            dtype=np.float64,
        )
        offset3 = np.array([cell_x, y_min, cell_z], dtype=np.float64)
        q = np.rint((verts - offset3) / scale).astype(np.int64)
        if np.any(q < 0) or np.any(q > 65535):
            raise ValueError(f"fixture vertex is outside the cell quantisation range: {q}")
        wire = np.zeros((verts.shape[0], 4), dtype=np.uint16)
        wire[:, :3] = q
        all_verts.append(wire)
        all_faces.extend(int(v) for v in spec["faces"])
        world_points.extend(verts.tolist())

        record["ofs"] = (cell_x, y_min, cell_z, 0.0)
        record["scale"] = (scale[0], scale[1], scale[2], 0.0)
        record["maxHt"] = y_max
        record["gridHtStart"] = 0
        record["gridStart"] = 0
        record["fistartGridsize"] = 0
        record["vertsStart"] = sum(v.shape[0] for v in all_verts[:-1])
        record["facesStart"] = len(all_faces) - len(spec["faces"])
        record["resv"] = (0, 0)

    if bbox is None:
        if world_points:
            points = np.asarray(world_points, dtype=np.float64)
            bbox = (
                float(points[:, 0].min()),
                float(points[:, 1].min()),
                float(points[:, 2].min()),
                float(points[:, 0].max()),
                float(points[:, 1].max()),
                float(points[:, 2].max()),
            )
        else:
            bbox = (0.0, 0.0, 0.0, 0.0, 0.0, 0.0)

    out = bytearray()
    offsets = {}
    out += b"LTdump"
    out += struct.pack("<2if", cells_x, cells_y, cell_size)
    out += struct.pack("<3f", *origin)
    out += struct.pack("<6f", *bbox)
    offsets["cell_count"] = len(out)
    out += struct.pack("<i", records.size)
    offsets["cell_data"] = len(out)
    out += records.tobytes()

    offsets["grid_count"] = len(out)
    out += struct.pack("<i", grid_count)
    out += b"\0" * (grid_count * 4)

    offsets["grid_ht_count"] = len(out)
    out += struct.pack("<i", grid_ht_count)
    out += b"\0" * (grid_ht_count * 4)

    flat_faces = np.asarray(all_faces, dtype=np.uint16)
    offsets["face_count"] = len(out)
    out += struct.pack("<i", flat_faces.size)
    offsets["face_data"] = len(out)
    out += flat_faces.tobytes()

    flat_verts = (
        np.concatenate(all_verts, axis=0)
        if all_verts
        else np.zeros((0, 4), dtype=np.uint16)
    )
    offsets["vert_count"] = len(out)
    out += struct.pack("<i", flat_verts.shape[0])
    offsets["vert_data"] = len(out)
    out += flat_verts.tobytes()

    if include_face_indices:
        values = [] if face_indices is None else list(face_indices)
        flat_indices = np.asarray(values, dtype=np.uint16)
        offsets["face_index_count"] = len(out)
        out += struct.pack("<i", flat_indices.size)
        offsets["face_index_data"] = len(out)
        out += flat_indices.tobytes()

    return bytes(out), offsets


def _build_lmap_payload(
    dump: bytes,
    *,
    codec: int = 2,
    map_size=(1, 1),
    origin_cells=(0, 0),
    land_cell_size: float = 10.0,
    grid_cell_size: float = 10.0,
    use_tile: int = 0,
    no_tracer: bool = False,
    dump_size_override: int | None = None,
):
    map_x, map_y = map_size
    header = struct.pack(
        "<4sI2f5i",
        b"lndm",
        4,
        grid_cell_size,
        land_cell_size,
        map_x,
        map_y,
        origin_cells[0],
        origin_cells[1],
        use_tile,
    )
    if no_tracer:
        return header + struct.pack("<4i", 16, 17, 18, 0) + b"\0\0\0\0"

    dump_size = len(dump) if dump_size_override is None else dump_size_override
    stream = struct.pack("<i", dump_size) + dump
    body = zlib.compress(stream, 6)
    if codec == 2:
        body = struct.pack("<I", len(stream)) + body
    raw = (codec << 30) | len(body)
    # Offsets are relative to baseDataOffset=36.  mesh/detail/tile point into
    # a four-byte pad; the parser only needs their ordering and range.
    offsets = struct.pack("<4i", 16, 17, 18, 24)
    return header + offsets + b"\0\0\0\0" + struct.pack("<I", raw) + body


def _wrap_dbld(payload: bytes, *, junk: bool = False) -> bytes:
    blocks = bytearray()
    if junk:
        blocks += struct.pack("<I", 4 + 3) + b"junk" + b"abc"
    blocks += struct.pack("<I", 4 + len(payload)) + b"lmap" + payload
    return b"DBLD3x64" + b"\0\0\0\0" + bytes(blocks)


def _plane_cell(cell_size: float = 10.0):
    verts = [
        (0.0, 2.0, 0.0),
        (cell_size, 3.0, 0.0),
        (0.0, 4.0, cell_size),
        (cell_size, 5.0, cell_size),
    ]
    return _cell(0.0, 0.0, verts, [0, 1, 2, 1, 3, 2])


class LmapTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(
            prefix=".lmap_test_",
            dir=Path(__file__).resolve().parent,
            ignore_cleanup_errors=True,
        )
        self.root = Path(self.temp.name)

    def tearDown(self):
        self.temp.cleanup()

    def _write_level(self, payload: bytes, *, junk: bool = False) -> Path:
        path = self.root / "level.bin"
        path.write_bytes(_wrap_dbld(payload, junk=junk))
        return path

    def test_plane_surface_and_axis_orientation(self):
        dump, _ = _build_ltdump(
            [_plane_cell()], cells_x=1, cells_y=1, cell_size=10.0
        )
        payload = _build_lmap_payload(dump)
        height, meta = decode_lmap(
            self._write_level(payload),
            _zlib_decompress,
            bounds=(0.0, 10.0, 0.0, 10.0),
            resolution=32,
        )
        self.assertEqual(height.dtype, np.float32)
        self.assertEqual(height.shape, (32, 32))
        self.assertEqual(meta["bounds"], (0.0, 10.0, 0.0, 10.0))
        self.assertEqual(meta["row_axis"], "+Z")
        self.assertEqual(meta["column_axis"], "+X")
        self.assertTrue(meta["mask"].all())
        self.assertEqual(meta["stats"]["triangles_total"], 2)
        self.assertFalse(meta["ltdump"]["grid_ht_used_as_height"])

        x = (np.arange(32, dtype=np.float64) + 0.5) * (10.0 / 32.0)
        z = (np.arange(32, dtype=np.float64) + 0.5) * (10.0 / 32.0)
        expected = 2.0 + 0.1 * x[None, :] + 0.2 * z[:, None]
        np.testing.assert_allclose(height, expected, atol=2e-3, rtol=0.0)
        self.assertGreater(height[-1, 0], height[0, 0])
        self.assertGreater(height[0, -1], height[0, 0])

    def test_subregion_bounds_use_pixel_centres(self):
        dump, _ = _build_ltdump(
            [_plane_cell()], cells_x=1, cells_y=1, cell_size=10.0
        )
        payload = _build_lmap_payload(dump)
        height, meta = decode_lmap(
            self._write_level(payload),
            _zlib_decompress,
            bounds=(2.5, 7.5, 3.0, 8.0),
            resolution=11,
        )
        self.assertEqual(meta["bounds_source"], "requested")
        self.assertTrue(meta["mask"].all())
        x = 2.5 + (np.arange(11, dtype=np.float64) + 0.5) * (5.0 / 11.0)
        z = 3.0 + (np.arange(11, dtype=np.float64) + 0.5) * (5.0 / 11.0)
        expected = 2.0 + 0.1 * x[None, :] + 0.2 * z[:, None]
        np.testing.assert_allclose(height, expected, atol=2e-3, rtol=0.0)
        self.assertGreater(height[-1, -1], height[0, 0])
        self.assertAlmostEqual(meta["cell_size_m"][0], 5.0 / 11.0, places=12)

    def test_empty_cell_keeps_water_mask(self):
        plane = _plane_cell()
        dump, _ = _build_ltdump(
            [plane, None], cells_x=2, cells_y=1, cell_size=10.0
        )
        payload = _build_lmap_payload(
            dump, map_size=(2, 1), land_cell_size=10.0
        )
        height, meta = decode_lmap(
            self._write_level(payload),
            _zlib_decompress,
            bounds=(0.0, 20.0, 0.0, 10.0),
            resolution=20,
            water_level=-3.0,
        )
        self.assertTrue(meta["mask"][:, :10].all())
        self.assertFalse(meta["mask"][:, 10:].any())
        self.assertTrue(np.all(height[:, 10:] == np.float32(-3.0)))
        self.assertTrue(meta["water_mask"][:, 10:].all())
        self.assertFalse(meta["empty"])
        self.assertEqual(meta["stats"]["cells_with_geometry"], 1)

    def test_all_empty_returns_water_and_false_mask(self):
        dump, _ = _build_ltdump(
            [None], cells_x=1, cells_y=1, cell_size=10.0
        )
        payload = _build_lmap_payload(dump)
        height, meta = decode_lmap(
            self._write_level(payload),
            _zlib_decompress,
            water_level=-7.0,
        )
        self.assertTrue(np.all(height == np.float32(-7.0)))
        self.assertFalse(meta["mask"].any())
        self.assertTrue(meta["empty"])
        self.assertEqual(meta["ltdump"]["triangle_count"], 0)

    def test_single_triangle_leaves_hole_instead_of_heuristic_fill(self):
        verts = [(0.0, 2.0, 0.0), (10.0, 2.0, 0.0), (0.0, 2.0, 10.0)]
        dump, _ = _build_ltdump(
            [_cell(0.0, 0.0, verts, [0, 1, 2])],
            cells_x=1,
            cells_y=1,
            cell_size=10.0,
        )
        payload = _build_lmap_payload(dump)
        height, meta = decode_lmap(
            self._write_level(payload),
            _zlib_decompress,
            bounds=(0.0, 10.0, 0.0, 10.0),
            resolution=10,
            water_level=-1.0,
        )
        self.assertGreater(int(meta["mask"].sum()), 0)
        self.assertLess(int(meta["mask"].sum()), 100)
        self.assertTrue(meta["mask"][0, 0])
        self.assertFalse(meta["mask"][1, 9])
        self.assertFalse(meta["mask"][9, 1])
        self.assertEqual(float(height[1, 9]), -1.0)
        self.assertAlmostEqual(float(height[0, 0]), 2.0, places=5)

    def test_reversed_winding_has_same_surface(self):
        verts = [(0.0, 2.0, 0.0), (10.0, 2.0, 0.0), (0.0, 2.0, 10.0)]
        dump_a, _ = _build_ltdump(
            [_cell(0.0, 0.0, verts, [0, 1, 2])],
            cells_x=1,
            cells_y=1,
            cell_size=10.0,
        )
        dump_b, _ = _build_ltdump(
            [_cell(0.0, 0.0, verts, [0, 2, 1])],
            cells_x=1,
            cells_y=1,
            cell_size=10.0,
        )
        a, _ = decode_ltdump(dump_a, bounds=(0.0, 10.0, 0.0, 10.0), resolution=10)
        b, _ = decode_ltdump(dump_b, bounds=(0.0, 10.0, 0.0, 10.0), resolution=10)
        np.testing.assert_array_equal(a, b)

    def test_codec_zero_and_bare_payload(self):
        dump, _ = _build_ltdump(
            [_plane_cell()], cells_x=1, cells_y=1, cell_size=10.0
        )
        payload = _build_lmap_payload(dump, codec=0)
        path = self.root / "bare.bin"
        path.write_bytes(payload)
        height, meta = decode_lmap(
            path,
            _zlib_decompress,
            bounds=(0.0, 10.0, 0.0, 10.0),
            resolution=8,
        )
        self.assertEqual(meta["container"]["kind"], "bare-lmap")
        self.assertEqual(meta["frame"]["codec"], 0)
        self.assertTrue(meta["mask"].all())
        self.assertGreater(float(height.mean()), 2.0)

    def test_junk_block_is_skipped_by_dbfd_traversal(self):
        dump, _ = _build_ltdump(
            [_plane_cell()], cells_x=1, cells_y=1, cell_size=10.0
        )
        payload = _build_lmap_payload(dump)
        path = self._write_level(payload, junk=True)
        _, meta = decode_lmap(
            path,
            _zlib_decompress,
            bounds=(0.0, 10.0, 0.0, 10.0),
            resolution=4,
        )
        self.assertIn("junk", meta["container"]["block_tags"])
        self.assertIn("lmap", meta["container"]["block_tags"])

    def test_no_tracer_is_rejected_without_calling_decompress(self):
        def should_not_run(data, expected, codec):
            raise AssertionError("decompress must not be called for a tracer-less lmap")

        payload = _build_lmap_payload(b"", no_tracer=True)
        with self.assertRaises(LmapFormatError):
            decode_lmap(
                self._write_level(payload),
                should_not_run,
                water_level=-2.5,
            )

    def test_inspect_lmap_routes_without_decompressing(self):
        dump, _ = _build_ltdump(
            [_plane_cell()], cells_x=1, cells_y=1, cell_size=10.0
        )
        with_tracer = self._write_level(_build_lmap_payload(dump))
        info = inspect_lmap(with_tracer)
        self.assertTrue(info["lmap"]["has_tracer"])
        self.assertEqual(info["lmap"]["codec"], 2)
        without_tracer = self._write_level(_build_lmap_payload(b"", no_tracer=True))
        self.assertFalse(inspect_lmap(without_tracer)["lmap"]["has_tracer"])

    def test_face_indices_section_is_parsed_but_not_used(self):
        dump, _ = _build_ltdump(
            [_plane_cell()],
            cells_x=1,
            cells_y=1,
            cell_size=10.0,
            face_indices=[0, 0, 0, 0, 0, 0],
        )
        parsed = parse_ltdump(dump)
        self.assertEqual(parsed["face_index_count"], 6)
        height, meta = decode_ltdump(
            dump, bounds=(0.0, 10.0, 0.0, 10.0), resolution=4
        )
        self.assertTrue(meta["mask"].all())
        self.assertEqual(meta["ltdump"]["face_index_count"], 6)
        self.assertGreater(float(height.mean()), 2.0)

    def test_missing_face_indices_section_is_allowed(self):
        dump, _ = _build_ltdump(
            [_plane_cell()],
            cells_x=1,
            cells_y=1,
            cell_size=10.0,
            include_face_indices=False,
        )
        parsed = parse_ltdump(dump)
        self.assertEqual(parsed["face_index_count"], 0)
        height, _ = decode_ltdump(
            dump, bounds=(0.0, 10.0, 0.0, 10.0), resolution=4
        )
        self.assertTrue(np.isfinite(height).all())

    def test_invalid_cell_count(self):
        dump, offsets = _build_ltdump(
            [_plane_cell()], cells_x=1, cells_y=1, cell_size=10.0
        )
        bad = bytearray(dump)
        struct.pack_into("<i", bad, offsets["cell_count"], 2)
        with self.assertRaises(LmapFormatError):
            parse_ltdump(bytes(bad))

    def test_face_count_exceeding_remaining(self):
        dump, offsets = _build_ltdump(
            [_plane_cell()], cells_x=1, cells_y=1, cell_size=10.0
        )
        bad = bytearray(dump)
        struct.pack_into("<i", bad, offsets["face_count"], 1_000_000)
        with self.assertRaises(LmapFormatError):
            parse_ltdump(bytes(bad))

    def test_negative_vertex_count(self):
        dump, offsets = _build_ltdump(
            [_plane_cell()], cells_x=1, cells_y=1, cell_size=10.0
        )
        bad = bytearray(dump)
        struct.pack_into("<i", bad, offsets["vert_count"], -1)
        with self.assertRaises(LmapFormatError):
            parse_ltdump(bytes(bad))

    def test_face_index_outside_cell(self):
        verts = [(0.0, 2.0, 0.0), (10.0, 2.0, 0.0), (0.0, 2.0, 10.0)]
        dump, _ = _build_ltdump(
            [_cell(0.0, 0.0, verts, [0, 1, 4])],
            cells_x=1,
            cells_y=1,
            cell_size=10.0,
        )
        with self.assertRaises(LmapFormatError):
            parse_ltdump(dump)

    def test_section_size_cap(self):
        dump, _ = _build_ltdump(
            [_plane_cell()], cells_x=1, cells_y=1, cell_size=10.0
        )
        with mock.patch.object(lmap, "MAX_SECTION_BYTES", 8):
            with self.assertRaises(LmapFormatError):
                parse_ltdump(dump)

    def test_oodle_size_prefix_mismatch(self):
        dump, _ = _build_ltdump(
            [_plane_cell()], cells_x=1, cells_y=1, cell_size=10.0
        )
        payload = bytearray(_build_lmap_payload(dump))
        # The ray frame body starts at absolute offset 60; its first u32 is
        # the Oodle uncompressed-size prefix.
        struct.pack_into("<I", payload, 60, 1234)

        def ignores_expected(data, expected, codec):
            return zlib.decompress(data)

        with self.assertRaises(LmapFormatError):
            decode_lmap(
                self._write_level(bytes(payload)),
                ignores_expected,
                bounds=(0.0, 10.0, 0.0, 10.0),
                resolution=4,
            )

    def test_dump_size_mismatch(self):
        dump, _ = _build_ltdump(
            [_plane_cell()], cells_x=1, cells_y=1, cell_size=10.0
        )
        payload = _build_lmap_payload(dump, dump_size_override=len(dump) + 10)
        with self.assertRaises(LmapFormatError):
            decode_lmap(
                self._write_level(payload),
                _zlib_decompress,
                bounds=(0.0, 10.0, 0.0, 10.0),
                resolution=4,
            )

    def test_invalid_bounds_and_resolution(self):
        dump, _ = _build_ltdump(
            [_plane_cell()], cells_x=1, cells_y=1, cell_size=10.0
        )
        with self.assertRaises(LmapFormatError):
            decode_ltdump(dump, bounds=(10.0, 0.0, 0.0, 10.0), resolution=4)
        with self.assertRaises(LmapFormatError):
            decode_ltdump(dump, resolution=0)
        with self.assertRaises(LmapFormatError):
            decode_ltdump(dump, resolution=8001)

    def test_metadata_is_bounded_and_explicit(self):
        dump, _ = _build_ltdump(
            [_plane_cell()], cells_x=1, cells_y=1, cell_size=10.0
        )
        payload = _build_lmap_payload(dump)
        _, meta = decode_lmap(
            self._write_level(payload),
            _zlib_decompress,
            bounds=(0.0, 10.0, 0.0, 10.0),
            resolution=16,
        )
        self.assertEqual(meta["bounds_axis_order"], ("x_min", "x_max", "z_min", "z_max"))
        self.assertEqual(meta["height_dtype"], "float32")
        self.assertEqual(meta["height_units"], "m")
        self.assertEqual(meta["mask"].shape, (16, 16))
        self.assertEqual(meta["water_mask"].shape, (16, 16))
        self.assertFalse(meta["ltdump"]["grid_ht_used_as_height"])
        self.assertEqual(meta["frame"]["codec"], 2)
        self.assertGreater(meta["frame"]["dump_size"], 0)
        self.assertEqual(meta["container"]["kind"], "DBLD3x64")

    def test_flat_y_scale_and_degenerate_xz_scale(self):
        dump, offsets = _build_ltdump(
            [_plane_cell()], cells_x=1, cells_y=1, cell_size=10.0
        )
        flat_y = bytearray(dump)
        # Cell record starts at offsets["cell_data"]; ofs[4] is 16 bytes,
        # scale[4] starts at +16, and scale.y is the second float.
        struct.pack_into("<f", flat_y, offsets["cell_data"] + 16 + 4, 0.0)
        height, meta = decode_ltdump(
            bytes(flat_y), bounds=(0.0, 10.0, 0.0, 10.0), resolution=8
        )
        self.assertTrue(meta["mask"].all())
        np.testing.assert_allclose(height, np.float32(2.0), atol=1e-5)

        degenerate = bytearray(dump)
        struct.pack_into("<f", degenerate, offsets["cell_data"] + 16, 0.0)
        struct.pack_into("<f", degenerate, offsets["cell_data"] + 16 + 8, 0.0)
        height, meta = decode_ltdump(
            bytes(degenerate), bounds=(0.0, 10.0, 0.0, 10.0), resolution=8
        )
        self.assertFalse(meta["mask"].any())
        self.assertTrue(meta["empty"])

        negative = bytearray(dump)
        struct.pack_into("<f", negative, offsets["cell_data"] + 16, -1.0)
        with self.assertRaises(LmapFormatError):
            decode_ltdump(
                bytes(negative), bounds=(0.0, 10.0, 0.0, 10.0), resolution=8
            )

    def test_zstd_frame_content_size_probe(self):
        # fhd=0x40: no single segment, FCS field 2 bytes; window descriptor
        # follows fhd; FCS is stored with a +256 bias.
        frame = b"\x28\xb5\x2f\xfd" + bytes([0x40, 0x00]) + struct.pack("<H", 744)
        self.assertEqual(lmap._frame_content_size(1, frame), 1000)
        unknown = b"\x28\xb5\x2f\xfd" + bytes([0x00, 0x00])
        self.assertIsNone(lmap._frame_content_size(1, unknown))

    @unittest.skipUnless(REAL_LEVEL.exists(), "local War Thunder level is not installed")
    def test_real_container_and_lmap_header(self):
        payload, container = lmap._read_lmap_payload(REAL_LEVEL)
        info = lmap._parse_lmap_header(payload)
        self.assertEqual(container["kind"], "DBLD3x64")
        self.assertEqual(info["version"], 4)
        self.assertEqual(info["map_size"], [32, 32])
        self.assertEqual(info["land_cell_size_m"], 4096.0)
        self.assertEqual(info["origin_cells"], [-16, -16])
        self.assertTrue(info["has_tracer"])
        self.assertEqual(info["codec"], 2)
        self.assertGreater(info["oodle_src_size"], 0)
        self.assertEqual(info["ray_tracer_ofs"] - 4, info["frame_header_offset"])


if __name__ == "__main__":
    unittest.main(verbosity=2)

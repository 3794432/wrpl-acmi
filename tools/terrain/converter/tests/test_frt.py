"""Synthetic tests for the current FastRtDump/FRT geometry decoder."""

from __future__ import annotations

import struct
import tempfile
import unittest
from pathlib import Path

import numpy as np

import frt
from frt import FrtFormatError, decode_frt, inspect_frt, parse_rtdump


TEST_TMP = Path(__file__).resolve().parent / "tmp"


def _temporary_directory():
    TEST_TMP.mkdir(exist_ok=True)
    return tempfile.TemporaryDirectory(dir=TEST_TMP, ignore_cleanup_errors=True)


def _block(tag: bytes, payload: bytes) -> bytes:
    return struct.pack("<I", len(payload) + 4) + tag + payload


def _make_level(payload: bytes, *, include_frt: bool = True) -> bytes:
    body = b"DBLD3x64\x00\x00\x00\x00"
    if include_frt:
        body += _block(b"\x00FRT", payload)
    body += _block(b"\x00END", b"")
    return body


def _make_frt_payload(
    dump: bytes,
    compressed: bytes,
    *,
    full_version: int = 0x0158,
    version_id: bytes = frt.FRT_VERSION_ID,
    trailer: bytes = b"\x00" * 12,
    block_rest: int | None = None,
) -> bytes:
    rest = 12 + len(compressed) if block_rest is None else block_rest
    return (
        version_id
        + struct.pack("<I", rest)
        + b"RTdump"
        + struct.pack("<H", full_version)
        + struct.pack("<I", len(dump))
        + compressed
        + trailer
    )


def _make_dump(
    verts,
    faces,
    *,
    rt_box=(0.0, 0.0, 0.0, 4.0, 4.0, 1.0),
    face_indices=(),
    version: int = 43,
) -> bytes:
    vertices = np.asarray(verts, dtype=np.float64)
    triangles = np.asarray(faces, dtype=np.uint32)
    if vertices.size == 0:
        vertices = vertices.reshape(0, 3)
    if triangles.size == 0:
        triangles = triangles.reshape(0, 3)
    if vertices.ndim != 2 or vertices.shape[1] != 3:
        raise ValueError("verts must be Nx3")
    if triangles.ndim != 2 or triangles.shape[1] != 3:
        raise ValueError("faces must be Fx3")
    vertex_count = vertices.shape[0]
    face_count = triangles.shape[0]
    verts_ofs = frt.DUMP_HEADER_SIZE
    faces_ofs = verts_ofs + vertex_count * 16
    face_indices_ofs = faces_ofs + face_count * 12
    total = face_indices_ofs + len(face_indices) * 4
    data = bytearray(total)
    struct.pack_into("<6f", data, 0, *rt_box)
    struct.pack_into("<4f", data, 24, 0.0, 0.0, 0.0, 100.0)
    struct.pack_into("<3f", data, 40, 1.0, 1.0, 1.0)
    struct.pack_into("<6i", data, 52, 0, 0, 0, 1, 1, 1)
    struct.pack_into("<5Q", data, 76, verts_ofs, faces_ofs, 0, face_indices_ofs, 0)
    struct.pack_into("<3i", data, 116, vertex_count, face_count, len(face_indices))
    struct.pack_into("<i", data, 128, version)
    for index, (x, y, z) in enumerate(vertices):
        struct.pack_into("<4f", data, verts_ofs + index * 16, x, y, z, 1.0)
    for index, face in enumerate(triangles):
        struct.pack_into("<3I", data, faces_ofs + index * 12, *map(int, face))
    for index, value in enumerate(face_indices):
        struct.pack_into("<I", data, face_indices_ofs + index * 4, value)
    return bytes(data)


def _fake_decompress(dump: bytes, seen: dict | None = None):
    def call(data: bytes, expected: int, codec: int) -> bytes:
        if seen is not None:
            seen.update(data=data, expected=expected, codec=codec)
        return dump

    return call


class FrtTests(unittest.TestCase):
    def _write(self, directory: str, payload: bytes, **kwargs) -> Path:
        path = Path(directory) / "level.bin"
        path.write_bytes(_make_level(payload, **kwargs))
        return path

    def test_inspect_reads_current_header_without_decompressing(self):
        dump = _make_dump(((0, 0, 0), (1, 0, 0), (0, 0, 1)), ((0, 1, 2),))
        payload = _make_frt_payload(dump, b"compressed")
        with _temporary_directory() as directory:
            path = self._write(directory, payload)
            info = inspect_frt(path)
        self.assertEqual(info["version"], 43)
        self.assertEqual(info["compression"], 0)
        self.assertEqual(info["full_version"], 0x0158)
        self.assertEqual(info["uncompressed_size"], len(dump))
        self.assertEqual(info["compressed_size"], len(b"compressed"))
        self.assertEqual(info["trailer_size"], 12)

    def test_decode_samples_triangle_plane_at_pixel_centres(self):
        dump = _make_dump(
            ((0, 0, 0), (4, 8, 0), (0, 0, 4), (4, 8, 4)),
            ((0, 1, 2), (1, 3, 2)),
            rt_box=(0.0, 0.0, 0.0, 4.0, 8.0, 4.0),
            face_indices=(0x08000000, 0x01000001, 0x02000002),
        )
        compressed = b"\x28\xb5\x2f\xfdfake"
        payload = _make_frt_payload(dump, compressed)
        seen: dict = {}
        with _temporary_directory() as directory:
            path = self._write(directory, payload)
            height, meta = decode_frt(
                path,
                _fake_decompress(dump, seen),
                bounds=(0, 4, 0, 4),
                resolution=4,
            )
        self.assertEqual(seen["expected"], len(dump))
        self.assertEqual(seen["codec"], 1)
        self.assertEqual(seen["data"], compressed)
        expected = np.tile(np.array([1.0, 3.0, 5.0, 7.0]), (4, 1))
        np.testing.assert_allclose(height, expected, atol=1e-5)
        self.assertEqual(height.dtype, np.float32)
        self.assertEqual(meta["valid_pixel_count"], 16)
        self.assertEqual(meta["bounds"], (0.0, 4.0, 0.0, 4.0))
        self.assertEqual(meta["pixel_size_m"], (1.0, 1.0))
        self.assertEqual(meta["row_direction"], "+Z")
        self.assertEqual(meta["column_direction"], "+X")
        self.assertFalse(meta["surface"]["is_bare_earth"])
        self.assertIn("collision", meta["surface"]["includes"])
        self.assertTrue(meta["mask"].all())

    def test_uncovered_pixels_stay_at_water_level(self):
        dump = _make_dump(
            ((0, 5, 0), (1, 5, 0), (0, 5, 1)),
            ((0, 1, 2),),
            rt_box=(0.0, 5.0, 0.0, 4.0, 5.0, 4.0),
        )
        payload = _make_frt_payload(dump, b"x")
        with _temporary_directory() as directory:
            path = self._write(directory, payload)
            height, meta = decode_frt(
                path,
                _fake_decompress(dump),
                bounds=(0, 4, 0, 4),
                resolution=4,
                water_level=-2.0,
            )
        self.assertEqual(float(height[0, 0]), 5.0)
        self.assertTrue(meta["mask"][0, 0])
        self.assertEqual(float(height[3, 3]), -2.0)
        self.assertFalse(meta["mask"][3, 3])
        self.assertEqual(meta["valid_pixel_count"], 1)

    def test_overlapping_triangles_keep_highest_surface(self):
        dump = _make_dump(
            (
                (0, 3, 0),
                (4, 3, 0),
                (0, 3, 4),
                (4, 3, 4),
                (0, 9, 0),
                (4, 9, 0),
                (0, 9, 4),
                (4, 9, 4),
            ),
            ((0, 1, 2), (1, 3, 2), (4, 5, 6), (5, 7, 6)),
            rt_box=(0.0, 3.0, 0.0, 4.0, 9.0, 4.0),
        )
        payload = _make_frt_payload(dump, b"x")
        with _temporary_directory() as directory:
            path = self._write(directory, payload)
            height, _ = decode_frt(
                path,
                _fake_decompress(dump),
                bounds=(0, 4, 0, 4),
                resolution=4,
            )
        self.assertTrue(np.all(height == 9.0))

    def test_default_bounds_come_from_rtdump_box(self):
        dump = _make_dump(
            ((-2, 0, -1), (2, 0, -1), (-2, 0, 3)),
            ((0, 1, 2),),
            rt_box=(-2.0, 0.0, -1.0, 2.0, 5.0, 3.0),
        )
        payload = _make_frt_payload(dump, b"x")
        with _temporary_directory() as directory:
            path = self._write(directory, payload)
            _, meta = decode_frt(
                path, _fake_decompress(dump), resolution=2
            )
        self.assertEqual(meta["bounds"], (-2.0, 2.0, -1.0, 3.0))
        self.assertEqual(meta["bounds_source"], "rtdump_header")

    def test_face_flags_do_not_change_vertex_indices(self):
        dump = _make_dump(
            ((0, 0, 0), (4, 0, 0), (0, 0, 4)),
            ((0x08000000, 0x10000001, 0x20000002),),
        )
        parsed = parse_rtdump(dump)
        self.assertEqual(parsed["triangle_indices"].tolist(), [[0, 1, 2]])

    def test_bad_version_id_is_rejected(self):
        dump = _make_dump(((0, 0, 0), (1, 0, 0), (0, 0, 1)), ((0, 1, 2),))
        payload = _make_frt_payload(dump, b"x", version_id=b"\xffv2\xff")
        with _temporary_directory() as directory:
            path = self._write(directory, payload)
            with self.assertRaisesRegex(FrtFormatError, "version id"):
                inspect_frt(path)

    def test_unsupported_version_or_compression_is_rejected(self):
        dump = _make_dump(((0, 0, 0), (1, 0, 0), (0, 0, 1)), ((0, 1, 2),))
        for full_version, message in ((44 << 3, "version 44"), ((43 << 3) | 6, "compression 6")):
            payload = _make_frt_payload(dump, b"x", full_version=full_version)
            with _temporary_directory() as directory:
                path = self._write(directory, payload)
                with self.assertRaisesRegex(FrtFormatError, message):
                    inspect_frt(path)

    def test_block_rest_beyond_payload_is_rejected(self):
        dump = _make_dump(((0, 0, 0), (1, 0, 0), (0, 0, 1)), ((0, 1, 2),))
        payload = _make_frt_payload(dump, b"x", block_rest=1000)
        with _temporary_directory() as directory:
            path = self._write(directory, payload)
            with self.assertRaisesRegex(FrtFormatError, "beyond FRT payload"):
                inspect_frt(path)

    def test_count_and_offset_budgets_are_checked_before_allocation(self):
        dump = bytearray(
            _make_dump((), (), rt_box=(0.0, 0.0, 0.0, 1.0, 1.0, 1.0))
        )
        struct.pack_into("<i", dump, 116, 100_000_000)
        with self.assertRaisesRegex(FrtFormatError, "vertex needs"):
            parse_rtdump(dump)

        dump = bytearray(
            _make_dump(((0, 0, 0), (1, 0, 0), (0, 0, 1)), ((0, 1, 2),))
        )
        struct.pack_into("<Q", dump, 84, 8)
        with self.assertRaisesRegex(FrtFormatError, "overlaps"):
            parse_rtdump(dump)

    def test_nonfinite_vertex_is_rejected(self):
        dump = _make_dump(
            ((0, float("nan"), 0), (1, 0, 0), (0, 0, 1)), ((0, 1, 2),)
        )
        with self.assertRaisesRegex(FrtFormatError, "non-finite vertex"):
            parse_rtdump(dump)

    def test_missing_frt_block_is_rejected(self):
        with _temporary_directory() as directory:
            path = Path(directory) / "level.bin"
            path.write_bytes(_make_level(b"", include_frt=False))
            with self.assertRaisesRegex(FrtFormatError, "not found"):
                inspect_frt(path)

    def test_decompressor_length_mismatch_is_rejected(self):
        dump = _make_dump(((0, 0, 0), (1, 0, 0), (0, 0, 1)), ((0, 1, 2),))
        payload = _make_frt_payload(dump, b"x")
        with _temporary_directory() as directory:
            path = self._write(directory, payload)
            with self.assertRaisesRegex(FrtFormatError, "expected"):
                decode_frt(
                    path,
                    lambda data, expected, codec: dump[:-1],
                    resolution=2,
                )

    def test_raster_argument_limits_are_enforced(self):
        with self.assertRaisesRegex(FrtFormatError, "bounds"):
            frt._validate_raster_args((0, 0, 0, 1), 2, 0.0)
        with self.assertRaisesRegex(FrtFormatError, "cell cap"):
            frt._validate_raster_args(None, 8192, 0.0)


if __name__ == "__main__":
    unittest.main()

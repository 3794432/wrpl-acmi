from __future__ import annotations

import struct
import tempfile
import unittest
from pathlib import Path

import numpy as np

import container
import hm2


def dagor_block(tag: bytes, payload: bytes, flags: int = 0) -> bytes:
    assert len(tag) == 4
    return struct.pack("<I", (flags << 30) | (len(payload) + 4)) + tag + payload


def level_file(blocks: list[bytes]) -> bytes:
    return b"DBLD3x64" + b"\x00\x00\x00\x00" + b"".join(blocks)


def temp_dir() -> tempfile.TemporaryDirectory:
    return tempfile.TemporaryDirectory(dir=Path(__file__).parent)


def hm2_base(
    width: int,
    height: int,
    *,
    version: int = 2,
    mirror: int = 0,
    cell: float = 32.0,
    hmin: float = -106.0,
    hscale: float = 4608.0,
    origin_x: float = -256.0,
    origin_z: float = -512.0,
) -> bytes:
    return struct.pack(
        "<5fII4i",
        cell,
        hmin,
        hscale,
        origin_x,
        origin_z,
        width | (version << 24),
        height | (mirror << 31),
        -1,
        -2,
        3,
        4,
    )


def frame(payload: bytes, codec: int) -> bytes:
    # HM2 nested beginBlock has a length word only; there is no tag.
    return struct.pack("<I", (codec << 30) | len(payload)) + payload


def codecs_identity(seen: list[tuple[int, int]]):
    def call(data: bytes, expected: int, codec: int) -> bytes:
        seen.append((expected, codec))
        return data

    return call


def block_specs(count: int, block_width: int) -> list[tuple[int, int, list[int]]]:
    block_size = block_width * block_width
    specs: list[tuple[int, int, list[int]]] = []
    for block in range(count):
        if block == 0:
            specs.append((1000, 0, [0] * block_size))
        elif block == 1:
            codes = [(block * 13 + i * 29) % 256 for i in range(block_size)]
            for row, first_code in enumerate([10, 250, 5, 240]):
                codes[row * block_width] = first_code
            specs.append((1000, 100, codes))
        elif block == 2:
            specs.append((65000, 535, [(i * 41) % 256 for i in range(block_size)]))
        else:
            mn = (block * 173) % 60000
            delta = min(65535 - mn, (block * 97) % 4000)
            codes = [(block * 17 + i * 31) % 256 for i in range(block_size)]
            specs.append((mn, delta, codes))
    return specs


def expected_grid(
    specs: list[tuple[int, int, list[int]]],
    blocks_x: int,
    blocks_y: int,
    block_width: int,
) -> tuple[np.ndarray, bytes, bytes]:
    width = blocks_x * block_width
    height = blocks_y * block_width
    grid = np.empty((height, width), dtype=np.uint16)
    info = bytearray()
    variance = bytearray()
    for block, (mn, delta, codes) in enumerate(specs):
        by, bx = divmod(block, blocks_x)
        decoded = np.array(
            [mn + (code * delta + 127) // 255 for code in codes], dtype=np.uint32
        ).reshape(block_width, block_width)
        grid[
            by * block_width : (by + 1) * block_width,
            bx * block_width : (bx + 1) * block_width,
        ] = decoded.astype(np.uint16)
        info += struct.pack("<HH", mn, delta)

        previous = 0
        for code in codes:
            variance.append((code - previous) & 0xFF)
            previous = code
    return grid, bytes(info), bytes(variance)


class ContainerTests(unittest.TestCase):
    def test_streams_offsets_and_requires_exact_tag(self) -> None:
        payloads = [(b"eVER", b"one"), (b"HM2\x00", b"decoy"), (b"\x00HM2", b"real")]
        contents = level_file(
            [dagor_block(tag, payload) for tag, payload in payloads]
        )
        with temp_dir() as temp:
            path = Path(temp) / "level.bin"
            path.write_bytes(contents)
            blocks = list(container.iter_blocks(path))
            self.assertEqual([block.tag for block in blocks], [p[0] for p in payloads])
            with path.open("rb") as stream:
                for block, (_, expected) in zip(blocks, payloads):
                    stream.seek(block.payload_offset)
                    self.assertEqual(stream.read(block.payload_size), expected)
            found = container.find_block(path, b"\x00HM2")
            self.assertEqual(found, blocks[2])

            decoy_only = level_file([dagor_block(b"HM2\x00", b"decoy")])
            path.write_bytes(decoy_only)
            with self.assertRaisesRegex(ValueError, "no exact"):
                hm2.decode_hm2(path, codecs_identity([]))

    def test_rejects_bad_top_level_lengths_and_trailing_bytes(self) -> None:
        with temp_dir() as temp:
            path = Path(temp) / "level.bin"
            path.write_bytes(
                b"DBLD3x64" + b"\x00" * 4 + struct.pack("<I", 100) + b"eVER"
            )
            with self.assertRaises(ValueError):
                list(container.iter_blocks(path))

            path.write_bytes(
                b"DBLD3x64" + b"\x00" * 4 + struct.pack("<I", 3) + b"eVER"
            )
            with self.assertRaises(ValueError):
                list(container.iter_blocks(path))

            path.write_bytes(level_file([dagor_block(b"eVER", b"one")]) + b"x")
            with self.assertRaises(ValueError):
                list(container.iter_blocks(path))


class V2Tests(unittest.TestCase):
    def test_real_layout_hierarchy_and_first_frame_size(self) -> None:
        # air_grand_canyon: chunkSz=0x00400403, 4096x4096, 512x512 blocks.
        hrb_subsz, levels, hierarchy_bytes = hm2._hierarchy_size(
            0x00400403, 4096, 4096
        )
        self.assertEqual((hrb_subsz, levels, hierarchy_bytes), (16, 8, 349_520))
        self.assertEqual(
            hm2._first_frame_sizes(1_048_576, 16_777_216, 349_520, False),
            [1_398_096],
        )

    def test_single_frame_non_square_delta_range_and_uint8_wrap(self) -> None:
        width, height, shift = 24, 16, 2
        block_width = 1 << shift
        blocks_x, blocks_y = width // block_width, height // block_width
        specs = block_specs(blocks_x * blocks_y, block_width)
        expected, info, variance = expected_grid(specs, blocks_x, blocks_y, block_width)

        payload = hm2_base(width, height, mirror=1) + struct.pack("<I", shift)
        payload += frame(info + variance, codec=2)
        payload += b"\x00\x00"  # observed HM2 block-end padding style
        level = level_file([dagor_block(b"eVER", b"x"), dagor_block(b"\x00HM2", payload)])

        seen: list[tuple[int, int]] = []
        with temp_dir() as temp:
            path = Path(temp) / "level.bin"
            path.write_bytes(level)
            grid, meta = hm2.decode_hm2(path, codecs_identity(seen))

        np.testing.assert_array_equal(grid, expected)
        self.assertEqual(grid.dtype, np.uint16)
        self.assertEqual(grid.shape, (height, width))
        self.assertEqual(meta["width"], width)
        self.assertEqual(meta["height"], height)
        self.assertEqual(meta["version"], 2)
        self.assertEqual(meta["mirror"], 1)
        self.assertEqual(meta["block_shift"], shift)
        self.assertEqual(meta["frame_count"], 1)
        self.assertTrue(meta["single_frame"])
        self.assertEqual(meta["trailing_padding_bytes"], 2)
        self.assertEqual(seen, [(len(info) + len(variance), 2)])

        # The explicit codes [10, 250, 5, 240] require uint8 wrap and carry
        # into the next stored row; a per-row reset changes these values.
        self.assertEqual(int(grid[0, 4]), 1000 + (10 * 100 + 127) // 255)
        self.assertEqual(int(grid[1, 4]), 1000 + (250 * 100 + 127) // 255)
        self.assertEqual(int(grid[2, 4]), 1000 + (5 * 100 + 127) // 255)
        self.assertEqual(int(grid[3, 4]), 1000 + (240 * 100 + 127) // 255)

    def test_single_frame_align16_padding_before_hierarchy(self) -> None:
        width, height, shift = 8, 16, 3
        block_width = 1 << shift
        blocks_x, blocks_y = 1, 2
        specs = block_specs(2, block_width)
        expected, info, variance = expected_grid(specs, blocks_x, blocks_y, block_width)
        base = len(info) + len(variance)
        padded_base = (base + 15) & ~15
        self.assertGreater(padded_base, base)

        payload = hm2_base(width, height) + struct.pack("<I", shift)
        payload += frame(info + variance + b"\x00" * (padded_base - base), codec=1)
        level = level_file([dagor_block(b"\x00HM2", payload)])

        seen: list[tuple[int, int]] = []
        with temp_dir() as temp:
            path = Path(temp) / "level.bin"
            path.write_bytes(level)
            grid, meta = hm2.decode_hm2(path, codecs_identity(seen))
        np.testing.assert_array_equal(grid, expected)
        self.assertEqual(seen, [(padded_base, 1)])
        self.assertEqual(meta["frame_output_bytes"], [padded_base])

    def test_single_frame_without_alignment_gap(self) -> None:
        width, height, shift = 8, 16, 3
        block_width = 1 << shift
        specs = block_specs(2, block_width)
        expected, info, variance = expected_grid(specs, 1, 2, block_width)
        payload = hm2_base(width, height) + struct.pack("<I", shift)
        payload += frame(info + variance, codec=1)
        level = level_file([dagor_block(b"\x00HM2", payload)])

        seen: list[tuple[int, int]] = []
        with temp_dir() as temp:
            path = Path(temp) / "level.bin"
            path.write_bytes(level)
            grid, meta = hm2.decode_hm2(path, codecs_identity(seen))
        np.testing.assert_array_equal(grid, expected)
        self.assertEqual(seen, [(len(info) + len(variance), 1)])
        self.assertEqual(meta["frame_output_bytes"], [len(info) + len(variance)])

    def test_chunked_variance_and_hierarchy_size(self) -> None:
        width, height, shift = 128, 64, 3
        block_width = 1 << shift
        blocks_x, blocks_y = width // block_width, height // block_width
        specs = block_specs(blocks_x * blocks_y, block_width)
        expected, info, variance = expected_grid(specs, blocks_x, blocks_y, block_width)

        # 0x1000 bytes per variance frame = 64 blocks of 8x8.  The hierarchy is
        # width/16 = 8 -> level 3 -> (4^3-1)/3 * 16 = 336 bytes.
        chunk_bytes = 0x1000
        chunk_sz = chunk_bytes | (4 << 8) | shift
        blocks_per_chunk = chunk_bytes // (block_width * block_width)
        chunk_count = (len(specs) + blocks_per_chunk - 1) // blocks_per_chunk
        frames = [frame(info + bytes(336), codec=2)]
        for index in range(chunk_count):
            start = index * blocks_per_chunk * (block_width * block_width)
            end = min(
                len(variance),
                (index + 1) * blocks_per_chunk * (block_width * block_width),
            )
            frames.append(frame(variance[start:end], codec=(index % 3)))

        payload = hm2_base(width, height) + struct.pack("<I", chunk_sz)
        payload += b"".join(frames)
        level = level_file([dagor_block(b"\x00HM2", payload)])

        seen: list[tuple[int, int]] = []
        with temp_dir() as temp:
            path = Path(temp) / "level.bin"
            path.write_bytes(level)
            grid, meta = hm2.decode_hm2(path, codecs_identity(seen))

        np.testing.assert_array_equal(grid, expected)
        self.assertFalse(meta["single_frame"])
        self.assertEqual(meta["chunk_count"], chunk_count)
        self.assertEqual(meta["frame_count"], chunk_count + 1)
        self.assertEqual(meta["hierarchy_sub_size"], 16)
        self.assertEqual(meta["hierarchy_levels"], 3)
        self.assertEqual(meta["hierarchy_bytes"], 336)
        self.assertEqual(
            seen,
            [(len(info) + 336, 2)]
            + [(64 * 64, index % 3) for index in range(chunk_count)],
        )
        self.assertEqual(meta["codecs"], [2] + [index % 3 for index in range(chunk_count)])

    def test_split_first_frame_alignment(self) -> None:
        width, height, shift = 8, 16, 3
        block_width = 1 << shift
        specs = block_specs(2, block_width)
        expected, info, variance = expected_grid(specs, 1, 2, block_width)
        chunk_bytes = 0x1000
        chunk_sz = chunk_bytes | shift
        padded_info = (len(info) + 15) & ~15
        payload = hm2_base(width, height) + struct.pack("<I", chunk_sz)
        payload += frame(info + b"\x00" * (padded_info - len(info)), codec=0)
        payload += frame(variance, codec=0)
        level = level_file([dagor_block(b"\x00HM2", payload)])

        seen: list[tuple[int, int]] = []
        with temp_dir() as temp:
            path = Path(temp) / "level.bin"
            path.write_bytes(level)
            grid, meta = hm2.decode_hm2(path, codecs_identity(seen))
        np.testing.assert_array_equal(grid, expected)
        self.assertEqual(meta["frame_count"], 2)
        self.assertEqual(seen[0], (padded_info, 0))

    def test_rejects_bad_sizes_counts_and_block_info(self) -> None:
        with temp_dir() as temp:
            path = Path(temp) / "level.bin"

            # Output shorter than declared.
            payload = hm2_base(8, 8) + struct.pack("<I", 2)
            payload += frame(struct.pack("<HH", 1, 0) + bytes(16), codec=0)
            path.write_bytes(level_file([dagor_block(b"\x00HM2", payload)]))
            with self.assertRaises(ValueError):
                hm2.decode_hm2(path, lambda data, expected, codec: data[:-1])

            # Nested frame length runs beyond the HM2 payload.
            overflow = hm2_base(8, 8) + struct.pack("<I", 2)
            overflow += struct.pack("<I", 1000) + b"FRM0" + b"short"
            path.write_bytes(level_file([dagor_block(b"\x00HM2", overflow)]))
            with self.assertRaises(ValueError):
                hm2.decode_hm2(path, codecs_identity([]))

            # Cell-count overflow is rejected before any frame work.
            huge = hm2_base(100_000, 100_000) + struct.pack("<I", 2)
            path.write_bytes(level_file([dagor_block(b"\x00HM2", huge)]))
            with self.assertRaisesRegex(ValueError, "cell count"):
                hm2.decode_hm2(path, codecs_identity([]))

            # mn + delta must remain a valid uint16 range.
            bad_range = hm2_base(4, 4) + struct.pack("<I", 2)
            bad_range += frame(struct.pack("<HH", 65000, 1000) + bytes(16), codec=0)
            path.write_bytes(level_file([dagor_block(b"\x00HM2", bad_range)]))
            with self.assertRaisesRegex(ValueError, "mn\\+delta"):
                hm2.decode_hm2(path, codecs_identity([]))

            # Official block shifts stop at 5.
            bad_shift = hm2_base(16, 16) + struct.pack("<I", 6)
            path.write_bytes(level_file([dagor_block(b"\x00HM2", bad_shift)]))
            with self.assertRaisesRegex(ValueError, "block shift"):
                hm2.decode_hm2(path, codecs_identity([]))

            # HM2 block-compressed dimensions must contain whole blocks.
            odd = hm2_base(17, 16) + struct.pack("<I", 3)
            path.write_bytes(level_file([dagor_block(b"\x00HM2", odd)]))
            with self.assertRaisesRegex(ValueError, "not divisible"):
                hm2.decode_hm2(path, codecs_identity([]))


class LegacyTests(unittest.TestCase):
    def test_v1_row_delta_and_v0_raw_non_square(self) -> None:
        width, height = 5, 3
        grid = np.array(
            [[100, 65535, 3, 4, 500], [7, 8, 9, 10, 11], [65530, 1, 2, 3, 4]],
            dtype=np.uint16,
        )
        encoded_rows: list[list[int]] = []
        for y in range(height):
            previous = 0
            row: list[int] = []
            for value in grid[y]:
                row.append((int(value) - previous) & 0xFFFF)
                previous = int(value)
            encoded_rows.append(row)
        encoded = np.array(encoded_rows, dtype=np.uint16)
        v1_payload = hm2_base(width, height, version=1) + frame(
            encoded.astype("<u2").tobytes(), codec=0
        )
        v0_payload = hm2_base(width, height, version=0, mirror=1) + frame(
            grid.astype("<u2").tobytes(), codec=1
        )

        with temp_dir() as temp:
            path = Path(temp) / "level.bin"
            path.write_bytes(level_file([dagor_block(b"\x00HM2", v1_payload)]))
            decoded_v1, meta_v1 = hm2.decode_hm2(path, codecs_identity([]))
            path.write_bytes(level_file([dagor_block(b"\x00HM2", v0_payload)]))
            decoded_v0, meta_v0 = hm2.decode_hm2(path, codecs_identity([]))

        np.testing.assert_array_equal(decoded_v1, grid)
        np.testing.assert_array_equal(decoded_v0, grid)
        self.assertEqual(meta_v1["version"], 1)
        self.assertEqual(meta_v1["mirror"], 0)
        self.assertEqual(meta_v1["frame_count"], 1)
        self.assertEqual(meta_v0["version"], 0)
        self.assertEqual(meta_v0["mirror"], 1)


if __name__ == "__main__":
    unittest.main()

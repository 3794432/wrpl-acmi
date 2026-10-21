"""Bounded decoder for Dagor HM2 heightmaps.

The implementation follows Gaijin's compressedHeightmap.cpp and
heightmapPhysHandler.cpp.  The decompressor is supplied by the caller; this
module owns framing, size checks, block reconstruction, and metadata.
"""

from __future__ import annotations

from dataclasses import dataclass
import math
from pathlib import Path
import struct
from typing import Callable

import numpy as np

try:
    from .container import Block, MAX_INPUT_BYTES, iter_blocks
except ImportError:  # direct import when this directory is on sys.path
    from container import Block, MAX_INPUT_BYTES, iter_blocks


HM2_TAG = b"\x00HM2"
MAX_OUTPUT_BYTES = 512 * 1024**2
MAX_CELLS = 64_000_000
MAX_BLOCK_SHIFT = 5
BLOCK_LENGTH_MASK = 0x3FFFFFFF
CODEC_NAMES = {0: "lzma", 1: "zstd", 2: "oodle"}


@dataclass(frozen=True, slots=True)
class _Header:
    cell: float
    hmin: float
    hscale: float
    origin_x: float
    origin_z: float
    width: int
    height: int
    version: int
    mirror: int
    exclude_bbox: tuple[int, int, int, int]
    chunk_sz: int
    header_size: int


@dataclass(frozen=True, slots=True)
class _Frame:
    offset: int
    payload_offset: int
    payload_size: int
    codec: int


def _checked_mul(left: int, right: int, label: str, limit: int) -> int:
    if not isinstance(left, int) or not isinstance(right, int) or left < 0 or right < 0:
        raise ValueError(f"invalid {label} factors: {left}, {right}")
    if left and right > limit // left:
        raise ValueError(f"{label} exceeds limit: {left} * {right} > {limit}")
    return left * right


def _align16(value: int) -> int:
    return (value + 0xF) & ~0xF


def _read_exact(stream, offset: int, size: int, what: str) -> bytes:
    if offset < 0 or size < 0:
        raise ValueError(f"invalid {what} range")
    stream.seek(offset)
    data = stream.read(size)
    if len(data) != size:
        raise ValueError(f"truncated {what} at offset 0x{offset:x}")
    return data


def _locate_hm2(path: Path) -> Block:
    for block in iter_blocks(path):
        if block.tag == HM2_TAG:
            if block.payload_size < 44:
                raise ValueError("HM2 payload is shorter than its base header")
            return block
    raise ValueError("level contains no exact \x00HM2 block")


def _parse_header(raw: bytes, version: int, chunk_sz: int, header_size: int) -> _Header:
    try:
        cell, hmin, hscale, origin_x, origin_z = struct.unpack_from("<5f", raw, 0)
        width_version, height_mirror = struct.unpack_from("<II", raw, 20)
        exclude_bbox = struct.unpack_from("<4i", raw, 28)
    except struct.error as exc:
        raise ValueError("truncated HM2 header") from exc

    if not all(math.isfinite(v) for v in (cell, hmin, hscale, origin_x, origin_z)):
        raise ValueError("HM2 header contains a non-finite real")
    if cell <= 0.0 or hscale < 0.0:
        raise ValueError(f"invalid HM2 cell/height scale: {cell}, {hscale}")

    width = width_version & 0x00FFFFFF
    stored_version = width_version >> 24
    height = height_mirror & 0x7FFFFFFF
    mirror = height_mirror >> 31
    if stored_version != version:
        raise ValueError(f"inconsistent HM2 version: {stored_version} != {version}")
    if width <= 0 or height <= 0:
        raise ValueError(f"invalid HM2 dimensions: {width}x{height}")
    _checked_mul(width, height, "HM2 cell count", MAX_CELLS)

    return _Header(
        cell=float(cell),
        hmin=float(hmin),
        hscale=float(hscale),
        origin_x=float(origin_x),
        origin_z=float(origin_z),
        width=int(width),
        height=int(height),
        version=int(version),
        mirror=int(mirror),
        exclude_bbox=tuple(int(v) for v in exclude_bbox),
        chunk_sz=int(chunk_sz),
        header_size=header_size,
    )


def _hierarchy_size(chunk_sz: int, width: int, height: int) -> tuple[int, int, int]:
    if not chunk_sz & 0xF00:
        return 0, 0, 0
    hrb_subsz = 1 << ((chunk_sz >> 8) & 0xF)
    if width < 2 * hrb_subsz or height < 2 * hrb_subsz:
        raise ValueError(
            f"hierarchy sub-size {hrb_subsz} is too large for {width}x{height}"
        )
    if width % hrb_subsz:
        raise ValueError("HM2 width is not divisible by hierarchy sub-size")
    quotient = width // hrb_subsz
    if quotient < 1 or quotient & (quotient - 1):
        raise ValueError("HM2 width / hierarchy sub-size is not a power of two")
    levels = quotient.bit_length() - 1
    if levels > 14:
        raise ValueError(f"HM2 hierarchy level {levels} is unsupported")
    block_count = (4**levels - 1) // 3
    hierarchy_bytes = _checked_mul(
        block_count, 16, "HM2 hierarchy bytes", MAX_OUTPUT_BYTES
    )
    return hrb_subsz, levels, hierarchy_bytes


def _read_frame(stream, position: int, end: int) -> tuple[_Frame, int]:
    if end - position < 4:
        raise ValueError(f"truncated HM2 frame at offset 0x{position:x}")
    raw = struct.unpack("<I", _read_exact(stream, position, 4, "HM2 frame record"))[0]
    record_length = raw & BLOCK_LENGTH_MASK
    if record_length <= 0:
        raise ValueError(f"HM2 frame at 0x{position:x} has invalid length {record_length}")
    frame_end = position + 4 + record_length
    if frame_end > end:
        raise ValueError(
            f"HM2 frame at 0x{position:x} ends beyond payload: "
            f"0x{frame_end:x} > 0x{end:x}"
        )
    codec = raw >> 30
    if codec not in CODEC_NAMES:
        raise ValueError(f"unsupported HM2 frame codec {codec}")
    # IBaseLoad::beginBlock reads only the length word.  Unlike a top-level
    # DBLD record, an HM2 nested frame has no four-byte tag: the next byte is
    # the first byte of the codec stream (for example the Kraken header).
    payload_size = record_length
    if payload_size <= 0 or payload_size > MAX_INPUT_BYTES:
        raise ValueError(f"invalid compressed HM2 frame size {payload_size}")
    return (
        _Frame(
            offset=position,
            payload_offset=position + 4,
            payload_size=payload_size,
            codec=codec,
        ),
        frame_end,
    )


def _consume_trailing_padding(stream, position: int, end: int) -> int:
    remaining = end - position
    if remaining == 0:
        return 0
    if remaining < 0 or remaining > 15:
        raise ValueError(
            f"unexpected {remaining} bytes after HM2 frames at 0x{position:x}"
        )
    padding = _read_exact(stream, position, remaining, "HM2 trailing padding")
    if any(padding):
        raise ValueError("non-zero bytes after the final HM2 frame")
    return remaining


def _decompress_exact(
    decompress: Callable[[bytes, int, int], bytes],
    data: bytes,
    expected: int,
    codec: int,
    label: str,
) -> bytes:
    if expected <= 0 or expected > MAX_OUTPUT_BYTES:
        raise ValueError(f"{label}: invalid output size {expected}")
    try:
        result = decompress(data, expected, codec)
        result = bytes(result)
    except Exception as exc:
        raise ValueError(
            f"{label}: codec {codec} ({CODEC_NAMES.get(codec, 'unknown')}) failed"
        ) from exc
    if len(result) != expected:
        raise ValueError(
            f"{label}: decoded {len(result)} bytes, expected {expected}"
        )
    return result


def _decompress_first(
    decompress: Callable[[bytes, int, int], bytes],
    data: bytes,
    candidates: list[int],
    codec: int,
) -> tuple[bytes, int]:
    errors: list[Exception] = []
    ordered = list(candidates)
    # Identity or stored frames expose their size directly.  Prefer that
    # candidate so synthetic and uncompressed callers are probed only once.
    if len(data) in ordered:
        ordered.remove(len(data))
        ordered.insert(0, len(data))
    for expected in ordered:
        if expected <= 0 or expected > MAX_OUTPUT_BYTES:
            continue
        try:
            return _decompress_exact(decompress, data, expected, codec, "HM2 first frame"), expected
        except ValueError as exc:
            errors.append(exc)
    detail = str(errors[-1]) if errors else "no valid first-frame size"
    raise ValueError(f"HM2 first frame could not be decoded: {detail}") from (
        errors[-1] if errors else None
    )


def _first_frame_sizes(
    info_bytes: int,
    variance_bytes: int,
    hierarchy_bytes: int,
    single_frame: bool,
) -> list[int]:
    base = info_bytes + variance_bytes if single_frame else info_bytes
    unpadded = base + hierarchy_bytes
    padded = _align16(base) + hierarchy_bytes
    # compress()/init() align the range grid to 16 bytes.  Some writers omit
    # that gap from the decompressed stream, so accept the unpadded form too.
    sizes = [
        size
        for size in dict.fromkeys((padded, unpadded))
        if 0 < size <= MAX_OUTPUT_BYTES
    ]
    if not sizes:
        raise ValueError("HM2 first frame exceeds the output limit")
    return sizes


def _decode_legacy(
    stream,
    header: _Header,
    payload_end: int,
    frame_start: int,
    decompress: Callable[[bytes, int, int], bytes],
) -> tuple[np.ndarray, dict]:
    frame, position = _read_frame(stream, frame_start, payload_end)
    trailing = _consume_trailing_padding(stream, position, payload_end)
    cells = _checked_mul(header.width, header.height, "HM2 cell count", MAX_CELLS)
    expected = _checked_mul(cells, 2, "legacy HM2 output bytes", MAX_OUTPUT_BYTES)
    encoded = _read_exact(stream, frame.payload_offset, frame.payload_size, "legacy HM2 frame")
    raw = _decompress_exact(decompress, encoded, expected, frame.codec, "legacy HM2 frame")

    grid = np.frombuffer(raw, dtype="<u2").reshape(header.height, header.width)
    grid = grid.astype(np.uint16, copy=True)
    if header.version == 1:
        # HMAP_DELTA_COMPRESSION_VER: each row is an independent u16 cumsum.
        np.cumsum(grid, axis=1, dtype=np.uint16, out=grid)

    meta = {
        "width": header.width,
        "height": header.height,
        "cell": header.cell,
        "origin_x": header.origin_x,
        "origin_z": header.origin_z,
        "hmin": header.hmin,
        "hscale": header.hscale,
        "version": header.version,
        "mirror": header.mirror,
        "exclude_bbox": list(header.exclude_bbox),
        "chunk_sz": header.chunk_sz,
        "block_shift": header.chunk_sz & 0xFF,
        "block_width": 1 << (header.chunk_sz & 0xFF),
        "blocks_x": 0,
        "blocks_y": 0,
        "block_count": 0,
        "chunk_bytes": 0,
        "blocks_per_chunk": 0,
        "chunk_count": 0,
        "frame_count": 1,
        "hierarchy_sub_size": 0,
        "hierarchy_levels": 0,
        "hierarchy_bytes": 0,
        "codecs": [frame.codec],
        "trailing_padding_bytes": trailing,
        "single_frame": True,
        "dtype": "uint16",
        "row_order": "stored rows, +Z; no vertical flip",
    }
    return grid, meta


def _decode_v2(
    stream,
    header: _Header,
    payload_end: int,
    frame_start: int,
    decompress: Callable[[bytes, int, int], bytes],
) -> tuple[np.ndarray, dict]:
    shift = header.chunk_sz & 0xFF
    if shift > MAX_BLOCK_SHIFT:
        raise ValueError(f"HM2 block shift {shift} exceeds {MAX_BLOCK_SHIFT}")
    block_width = 1 << shift
    if header.width % block_width or header.height % block_width:
        raise ValueError(
            f"HM2 dimensions {header.width}x{header.height} are not divisible by "
            f"block width {block_width}"
        )

    blocks_x = header.width // block_width
    blocks_y = header.height // block_width
    block_count = _checked_mul(blocks_x, blocks_y, "HM2 block count", MAX_CELLS)
    block_size = 1 << (2 * shift)
    info_bytes = _checked_mul(block_count, 4, "HM2 BlockInfo bytes", MAX_OUTPUT_BYTES)
    variance_bytes = _checked_mul(
        header.width, header.height, "HM2 variance bytes", MAX_OUTPUT_BYTES
    )
    hrb_subsz, hrb_levels, hierarchy_bytes = _hierarchy_size(
        header.chunk_sz, header.width, header.height
    )
    working_bytes = info_bytes + variance_bytes + hierarchy_bytes
    if working_bytes > MAX_OUTPUT_BYTES:
        raise ValueError(f"HM2 decoded metadata exceeds {MAX_OUTPUT_BYTES} bytes")

    chunk_bytes = header.chunk_sz & ~0xFFF
    blocks_per_chunk = chunk_bytes >> (2 * shift) if chunk_bytes else 0
    if chunk_bytes and blocks_per_chunk <= 0:
        raise ValueError("HM2 chunk size is smaller than one block")
    chunk_count = (
        (block_count + blocks_per_chunk - 1) // blocks_per_chunk
        if chunk_bytes
        else 0
    )
    single_frame = chunk_count == 0
    frame_count = 1 if single_frame else chunk_count + 1
    nested_bytes = payload_end - frame_start
    if frame_count > nested_bytes // 8:
        raise ValueError(
            f"HM2 declares {frame_count} frames but has only {nested_bytes} bytes"
        )

    first_sizes = _first_frame_sizes(
        info_bytes, variance_bytes, hierarchy_bytes, single_frame
    )
    position = frame_start
    codecs: list[int] = []
    expected_sizes: list[int] = []

    first_frame, position = _read_frame(stream, position, payload_end)
    first_compressed = _read_exact(
        stream, first_frame.payload_offset, first_frame.payload_size, "HM2 first frame"
    )
    first_raw, first_size = _decompress_first(
        decompress, first_compressed, first_sizes, first_frame.codec
    )
    del first_compressed
    codecs.append(first_frame.codec)
    expected_sizes.append(first_size)

    info_buffer = bytearray(info_bytes)
    info_buffer[:] = first_raw[:info_bytes]
    variance_buffer = bytearray(variance_bytes)

    if single_frame:
        variance_end = info_bytes + variance_bytes
        if len(first_raw) < variance_end:
            raise ValueError("HM2 single first frame is shorter than its variance area")
        variance_buffer[:] = first_raw[info_bytes:variance_end]
    del first_raw

    if not single_frame:
        block_offset = 0
        for _ in range(1, frame_count):
            frame, position = _read_frame(stream, position, payload_end)
            count = min(blocks_per_chunk, block_count - block_offset)
            if count <= 0:
                raise ValueError("HM2 frame count exceeds its block count")
            expected = _checked_mul(
                count, block_size, "HM2 variance frame bytes", MAX_OUTPUT_BYTES
            )
            encoded = _read_exact(
                stream, frame.payload_offset, frame.payload_size, "HM2 variance frame"
            )
            raw = _decompress_exact(
                decompress, encoded, expected, frame.codec, "HM2 variance frame"
            )
            start = block_offset * block_size
            variance_buffer[start : start + expected] = raw
            block_offset += count
            codecs.append(frame.codec)
            expected_sizes.append(expected)
        if block_offset != block_count:
            raise ValueError(
                f"HM2 variance frames cover {block_offset} of {block_count} blocks"
            )

    trailing = _consume_trailing_padding(stream, position, payload_end)

    info = np.frombuffer(info_buffer, dtype="<u2").reshape(block_count, 2)
    minimum = info[:, 0].astype(np.uint32)
    delta = info[:, 1].astype(np.uint32)
    if np.any(minimum + delta > 0xFFFF):
        bad = int(np.flatnonzero(minimum + delta > 0xFFFF)[0])
        raise ValueError(
            f"HM2 block {bad} has mn+delta={int(minimum[bad] + delta[bad])} > 65535"
        )

    variance = np.frombuffer(variance_buffer, dtype=np.uint8).reshape(
        blocks_y, blocks_x, block_size
    )
    # unpack_chunk() cumsums all block_size bytes in storage order.  The final
    # byte of one row carries into the next row of the same block.
    np.cumsum(variance, axis=2, dtype=np.uint8, out=variance)

    grid = np.empty((header.height, header.width), dtype=np.uint16)
    variance_rows = variance.reshape(blocks_y, blocks_x, block_width, block_width)
    for by in range(blocks_y):
        mn_row = minimum[by * blocks_x : (by + 1) * blocks_x, None, None]
        delta_row = delta[by * blocks_x : (by + 1) * blocks_x, None, None]
        codes = variance_rows[by].astype(np.uint32)
        values = mn_row + (codes * delta_row + 127) // 255
        row_view = grid[by * block_width : (by + 1) * block_width].reshape(
            block_width, blocks_x, block_width
        )
        row_view[:] = values.transpose(1, 0, 2).astype(np.uint16)

    meta = {
        "width": header.width,
        "height": header.height,
        "cell": header.cell,
        "origin_x": header.origin_x,
        "origin_z": header.origin_z,
        "hmin": header.hmin,
        "hscale": header.hscale,
        "version": header.version,
        "mirror": header.mirror,
        "exclude_bbox": list(header.exclude_bbox),
        "chunk_sz": header.chunk_sz,
        "block_shift": shift,
        "block_width": block_width,
        "block_size": block_size,
        "blocks_x": blocks_x,
        "blocks_y": blocks_y,
        "block_count": block_count,
        "chunk_bytes": chunk_bytes,
        "blocks_per_chunk": blocks_per_chunk,
        "chunk_count": chunk_count,
        "frame_count": frame_count,
        "hierarchy_sub_size": hrb_subsz,
        "hierarchy_levels": hrb_levels,
        "hierarchy_bytes": hierarchy_bytes,
        "codecs": codecs,
        "frame_output_bytes": expected_sizes,
        "trailing_padding_bytes": trailing,
        "single_frame": bool(single_frame),
        "dtype": "uint16",
        "row_order": "stored rows, +Z; no vertical flip",
    }
    return grid, meta


def _decode_hm2(
    path: Path, decompress: Callable[[bytes, int, int], bytes]
) -> tuple[np.ndarray, dict]:
    block = _locate_hm2(path)
    payload_end = block.payload_offset + block.payload_size
    try:
        stream = path.open("rb")
    except OSError as exc:
        raise ValueError(f"cannot open level file: {path}") from exc

    with stream:
        base = _read_exact(stream, block.payload_offset, 44, "HM2 base header")
        width_version = struct.unpack_from("<I", base, 20)[0]
        version = width_version >> 24
        if version > 2:
            raise ValueError(f"unsupported HM2 version {version}")
        if version == 2:
            if block.payload_size < 48:
                raise ValueError("version 2 HM2 payload is missing chunkSz")
            chunk_sz = struct.unpack(
                "<I", _read_exact(stream, block.payload_offset + 44, 4, "HM2 chunkSz")
            )[0]
            header_size = 48
        else:
            # heightmapPhysHandler.cpp uses this default for legacy versions.
            chunk_sz = 0x403
            header_size = 44

        header = _parse_header(base, version, chunk_sz, header_size)
        frame_start = block.payload_offset + header_size
        if frame_start > payload_end:
            raise ValueError("HM2 header exceeds block payload")
        if version == 2:
            grid, meta = _decode_v2(
                stream, header, payload_end, frame_start, decompress
            )
        else:
            grid, meta = _decode_legacy(
                stream, header, payload_end, frame_start, decompress
            )

    meta.update(
        {
            "tag": "\x00HM2",
            "payload_offset": block.payload_offset,
            "payload_size": block.payload_size,
            "header_size": header_size,
            "decode_limit_bytes": MAX_OUTPUT_BYTES,
            "max_cells": MAX_CELLS,
        }
    )
    return grid, meta


def decode_hm2(
    path: Path, decompress: Callable[[bytes, int, int], bytes]
) -> tuple[np.ndarray, dict]:
    """Decode the first exact \x00HM2 block into a uint16 grid.

    The returned array has shape (height, width), rows in stored +Z order and
    columns in +X order.  decompress(data, expected_output_bytes, codec) must
    return exactly the declared number of bytes.  Codec values are 2 (Oodle),
    1 (ZSTD), and 0 (legacy LZMA).
    """

    if not callable(decompress):
        raise ValueError("decompress must be callable")
    if not isinstance(path, (str, Path)):
        raise ValueError(f"invalid HM2 path: {path!r}")
    try:
        return _decode_hm2(Path(path), decompress)
    except MemoryError as exc:
        raise ValueError("HM2 decode exceeded the memory budget") from exc

"""Read the top-level block chain of a Dagor DBLD level without loading it.

The block record is a 32-bit little-endian value: the top two bits are block
flags (used by compression codecs), the low 30 bits are the record length
including the four-byte tag.  Payload starts after that tag.
"""

from __future__ import annotations

from pathlib import Path
import struct
from typing import NamedTuple


DBLD_MAGIC = b"DBLD3x64"
DBLD_HEADER_SIZE = 12
BLOCK_LENGTH_MASK = 0x3FFFFFFF
MAX_INPUT_BYTES = 512 * 1024**2


class Block(NamedTuple):
    """One top-level block; payload fields are absolute file offsets/sizes."""

    tag: bytes
    payload_offset: int
    payload_size: int


def _checked_file_size(path: Path) -> int:
    try:
        size = path.stat().st_size
    except OSError as exc:
        raise ValueError(f"cannot stat level file: {path}") from exc
    if size < DBLD_HEADER_SIZE:
        raise ValueError(f"level file is too short for DBLD header: {size} bytes")
    if size > MAX_INPUT_BYTES:
        raise ValueError(f"level file exceeds {MAX_INPUT_BYTES} bytes: {size}")
    return size


def _read_exact(stream, offset: int, size: int, what: str) -> bytes:
    if offset < 0 or size < 0:
        raise ValueError(f"invalid {what} range")
    stream.seek(offset)
    data = stream.read(size)
    if len(data) != size:
        raise ValueError(f"truncated {what} at offset 0x{offset:x}")
    return data


def iter_blocks(path: str | Path):
    """Yield top-level Block records, validating every record against EOF.

    A file must consist of the 12-byte DBLD header followed by complete block
    records with no trailing bytes.  This function seeks over payloads, so its
    memory use is constant even for a 512 MiB level.
    """

    try:
        level_path = Path(path)
    except (TypeError, ValueError) as exc:
        raise ValueError(f"invalid level path: {path!r}") from exc
    file_size = _checked_file_size(level_path)
    try:
        stream = level_path.open("rb")
    except OSError as exc:
        raise ValueError(f"cannot open level file: {level_path}") from exc
    with stream:
        if _read_exact(stream, 0, 8, "DBLD signature") != DBLD_MAGIC:
            raise ValueError("input is not a DBLD3x64 level")

        position = DBLD_HEADER_SIZE
        while position < file_size:
            if file_size - position < 4:
                raise ValueError(f"truncated block record at offset 0x{position:x}")
            raw_length = struct.unpack("<I", _read_exact(stream, position, 4, "block record"))[0]
            record_length = raw_length & BLOCK_LENGTH_MASK
            if record_length < 4:
                raise ValueError(
                    f"block at offset 0x{position:x} has invalid length {record_length}"
                )
            block_end = position + 4 + record_length
            if block_end > file_size:
                raise ValueError(
                    f"block at offset 0x{position:x} ends beyond file: "
                    f"0x{block_end:x} > 0x{file_size:x}"
                )
            tag = _read_exact(stream, position + 4, 4, "block tag")
            yield Block(tag=tag, payload_offset=position + 8, payload_size=record_length - 4)
            position = block_end

        if position != file_size:
            raise ValueError(
                f"block chain ends at 0x{position:x}, file ends at 0x{file_size:x}"
            )


def find_block(path: str | Path, tag: bytes) -> Block:
    """Return the first top-level block with an exact four-byte tag."""

    if not isinstance(tag, bytes) or len(tag) != 4:
        raise ValueError("a Dagor block tag must be exactly four bytes")
    for block in iter_blocks(path):
        if block.tag == tag:
            return block
    raise ValueError(f"block tag {tag!r} was not found")

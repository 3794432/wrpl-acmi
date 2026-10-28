"""Bounded, offline reader for terrain settings in local aces.vromfs.bin.

Only binary Dagor BLK formats 1-6 and PC VROMFS dumps are supported. Text,
includes, older BBF formats and external dictionaries fail closed. Nothing is
loaded through wrpl or a game DLL. Format references (Gaijin's primary source):
https://github.com/GaijinEntertainment/DagorEngine/blob/main/prog/engine/ioSys/vromfsLoad.cpp
https://github.com/GaijinEntertainment/DagorEngine/blob/main/prog/engine/ioSys/dataBlock/blk_serialize.cpp
https://github.com/GaijinEntertainment/DagorEngine/blob/main/prog/engine/ioSys/dataBlock/blk_zstd.cpp
https://github.com/GaijinEntertainment/DagorEngine/blob/main/prog/dagorInclude/supp/dag_zstdObfuscate.h
"""

from __future__ import annotations

import hashlib
import json
import math
import os
from pathlib import Path
import re
import struct
import tempfile
import zlib

import zstandard


# Hard limits, independent of file-declared lengths and caller input.
MAX_ARCHIVE_BYTES = 64 * 1024**2
MAX_BODY_BYTES = 128 * 1024**2
MAX_FILES = 65536
MAX_PATH_BYTES = 1024
MAX_NAMES = 524288
MAX_NAME_BYTES = 16 * 1024**2
MAX_STRING_BYTES = 8192
MAX_DICT_BYTES = 8 * 1024**2
MAX_BLK_BYTES = 8 * 1024**2
MAX_BLOCKS = 16384
MAX_PARAMS = 65536
MAX_DEPTH = 64
MAX_IDS = 128
MAX_JSON_BYTES = 16384
MAX_PROVENANCE_BYTES = 65536
MAX_WINDOW_BYTES = 32 * 1024**2
_KEYS = (b"mapCoord0", b"mapCoord1", b"water_level")
_TYPE_SIZES = (0, 4, 4, 4, 8, 12, 16, 8, 12, 1, 4, 48, 8, 16)
_ID = re.compile(r"[a-z0-9][a-z0-9_]{0,127}\Z", re.ASCII)
# AcesScene::loadLevel loads the level BLK, reads getReal("water_level", 0.f)
# at line 508, and passes that value to waterService->set_level below.
# This is an explicit, pinned engine-loader rule, not a guessed missing value.
WATER_DEFAULT_SOURCE = (
    "https://github.com/GaijinEntertainment/DagorEngine/blob/"
    "2baf19472d60b96b437b2f9e549a3a6c8958cfd8/"
    "prog/tools/sceneTools/daEditorX/binSceneView/strmLevel.cpp#L508"
)


class GameConfigError(ValueError):
    """Missing, ambiguous, unsupported or out-of-budget local game config."""


def _require(condition: bool, message: str) -> None:
    if not condition:
        raise GameConfigError(message)


def _span(data: bytes, offset: int, size: int, what: str) -> bytes:
    _require(0 <= offset <= len(data) and 0 <= size <= len(data) - offset,
             f"{what}: range outside input")
    return data[offset:offset + size]


def _check_directive(name: bytes) -> None:
    _require(not name.startswith(b"@") and name != b"include",
             "unsupported BLK include/override directive")


class _Reader:
    def __init__(self, data: bytes):
        self.data = data
        self.pos = 0

    def take(self, size: int) -> bytes:
        result = _span(self.data, self.pos, size, "BLK")
        self.pos += size
        return result

    def uint(self, limit: int = 0xFFFFFFFF) -> int:
        value = 0
        for shift in range(0, 35, 7):
            byte = self.take(1)[0]
            value |= (byte & 127) << shift
            _require(value <= limit, "BLK count/varint exceeds hard limit")
            if not byte & 128:
                _require(shift == 0 or byte != 0, "BLK noncanonical varint")
                return value
        raise GameConfigError("BLK unterminated/oversized varint")

    def end(self) -> None:
        _require(self.pos == len(self.data), "BLK unexpected trailing bytes")


def _names(reader: _Reader) -> tuple[bytes, ...]:
    count = reader.uint(MAX_NAMES)
    if not count:
        return ()
    size = reader.uint(MAX_NAME_BYTES)
    _require(count <= size, "BLK name count exceeds name storage")
    raw = reader.take(size)
    # Count before splitting, so a NUL flood cannot allocate unbounded objects.
    _require(raw.endswith(b"\0") and raw.count(b"\0") == count,
             "BLK name table count/terminators mismatch")
    names = tuple(raw[:-1].split(b"\0"))
    _require(all(len(n) <= MAX_STRING_BYTES for n in names), "BLK name exceeds hard limit")
    _require(len(set(names)) == count, "BLK duplicate names in name table")
    return names


def _zstd(data: bytes, limit: int, dictionary=None) -> bytes:
    try:
        frame = zstandard.get_frame_parameters(data)
        _require(frame.window_size <= MAX_WINDOW_BYTES, "zstd window exceeds hard limit")
        # max_output_size alone does NOT bound frames with a declared size.
        _require(frame.content_size == zstandard.CONTENTSIZE_UNKNOWN or
                 frame.content_size <= limit, "zstd output exceeds hard limit")
        result = zstandard.ZstdDecompressor(
            dict_data=dictionary, max_window_size=MAX_WINDOW_BYTES // 1024
        ).decompress(data, max_output_size=limit, allow_extra_data=False)
        _require(len(result) <= limit, "zstd output exceeds hard limit")
        return result
    except zstandard.ZstdError as exc:
        raise GameConfigError(f"invalid/truncated zstd frame: {exc}") from exc


def _deobfuscate(data: bytes) -> bytes:
    result = bytearray(data)
    key = (0xAA55AA55, 0xF00FF00F, 0xAA55AA55, 0x12481248)
    runs = [(0, key)] if len(data) >= 16 else []
    if len(data) >= 32:
        runs.append(((len(data) // 4 - 4) * 4, key[::-1]))
    for offset, words in runs:
        for i, word in enumerate(words):
            at = offset + i * 4
            struct.pack_into("<I", result, at, struct.unpack_from("<I", result, at)[0] ^ word)
    return bytes(result)


class _Archive:
    def __init__(self, path: Path):
        try:
            with path.open("rb") as handle:
                _require(os.fstat(handle.fileno()).st_size <= MAX_ARCHIVE_BYTES,
                         "aces.vromfs.bin input exceeds hard limit")
                raw = handle.read(MAX_ARCHIVE_BYTES + 1)
        except OSError as exc:
            raise GameConfigError(f"cannot read local archive {path}: {exc}") from exc
        _require(16 <= len(raw) <= MAX_ARCHIVE_BYTES, "VROMFS input length outside hard limit")
        self.sha256 = hashlib.sha256(raw).hexdigest()
        label, target, full_size, flags = struct.unpack_from("<4s4sII", raw)
        _require(label in (b"VRFs", b"VRFx") and target == b"\0\0PC",
                 "unsupported VROMFS label/target (PC dump required)")
        _require(32 <= full_size <= MAX_BODY_BYTES, "VROMFS body size exceeds hard limit")
        start = 16
        if label == b"VRFx":
            ext = _span(raw, 16, 8, "VROMFS extended header")
            ext_size, ext_flags, self.version = struct.unpack("<HHI", ext)
            _require(8 <= ext_size <= 4096 and not ext_flags & ~3,
                     "unsupported VROMFS extended header")
            _span(raw, 16, ext_size, "VROMFS extended header")
            start += ext_size
        else:
            self.version = None
        packed_size = flags & 0x3FFFFFFF
        payload = _span(raw, start, packed_size or full_size, "VROMFS payload")
        # MD5/signature trailers are not authenticity checks. Record SHA-256 of
        # the whole local archive for provenance; never claim signature verification.
        _require(len(raw) - start - len(payload) <= 4096, "VROMFS trailer exceeds hard limit")
        if packed_size and flags & 0x40000000:
            body = _zstd(_deobfuscate(payload), full_size)
        elif packed_size:
            try:
                decoder = zlib.decompressobj()
                body = decoder.decompress(payload, full_size + 1)
                _require(len(body) <= full_size and decoder.eof and not decoder.unused_data
                         and not decoder.unconsumed_tail, "invalid/oversized VROMFS zlib stream")
            except zlib.error as exc:
                raise GameConfigError(f"invalid VROMFS zlib stream: {exc}") from exc
        else:
            _require(not flags & 0x40000000, "VROMFS zstd flag without packed payload")
            body = payload
        _require(len(body) == full_size, "VROMFS decompressed size mismatch")
        self.body = body
        nt, count, r0, r1, dt, dcount, r2, r3 = struct.unpack_from("<8I", body)
        _require(0 < count <= MAX_FILES and count == dcount, "VROMFS file count outside hard limit/mismatch")
        _require(not (r0 or r1 or r2 or r3), "unsupported VROMFS table layout")
        _require(32 <= nt and nt + count * 8 <= dt and dt + count * 16 <= len(body),
                 "VROMFS tables outside body/overlapping")
        self.entries: dict[bytes, tuple[int, int]] = {}
        for i in range(count):
            name_at = struct.unpack_from("<Q", body, nt + i * 8)[0]
            _require(nt + count * 8 <= name_at < dt, "VROMFS filename outside name storage")
            end = body.find(b"\0", name_at, min(dt, name_at + MAX_PATH_BYTES + 1))
            _require(end > name_at, "VROMFS unterminated/oversized filename")
            name = body[name_at:end].lower()
            _require(name not in self.entries, f"ambiguous duplicate VROMFS entry: {name!r}")
            # PatchableTab is offset32,count32,reserved64; filename pointers are offset64.
            offset, size, a, b = struct.unpack_from("<4I", body, dt + i * 16)
            _require(not (a or b), "unsupported VROMFS entry layout")
            _require(offset <= len(body) and size <= len(body) - offset and
                     (size == 0 or offset >= dt + count * 16), "VROMFS entry outside data storage")
            self.entries[name] = (offset, size)
        self.shared_names: tuple[bytes, ...] | None = None
        self.shared_header: bytes | None = None
        self.dictionary = None

    def entry(self, name: bytes, limit: int) -> bytes:
        _require(name in self.entries, f"missing local VROMFS entry: {name!r}")
        offset, size = self.entries[name]
        _require(0 < size <= limit, f"VROMFS entry empty/exceeds hard limit: {name!r}")
        return _span(self.body, offset, size, "VROMFS entry")

    def shared(self) -> tuple[bytes, ...]:
        if self.shared_names is None:
            data = self.entry(b"\xff?nm", MAX_NAME_BYTES)
            self.shared_header = _span(data, 0, 40, "shared name header")
            reader = _Reader(_zstd(data[40:], MAX_NAME_BYTES))
            self.shared_names = _names(reader)
            reader.end()
        return self.shared_names

    def ddict(self):
        if self.dictionary is None:
            self.shared()
            name = self.shared_header[8:40].hex().encode("ascii") + b".dict"
            data = self.entry(name, MAX_DICT_BYTES)
            try:
                self.dictionary = zstandard.ZstdCompressionDict(data)
            except zstandard.ZstdError as exc:
                raise GameConfigError(f"invalid local BLK dictionary: {exc}") from exc
        return self.dictionary


def _binary_settings(data: bytes, shared: tuple[bytes, ...] = (), *, dedup=False,
                     allow_missing_water=False) -> dict:
    _require(len(data) <= MAX_BLK_BYTES, "BLK input exceeds hard limit")
    reader = _Reader(data)
    local = _names(reader)
    _require(len(shared) + len(local) <= MAX_NAMES, "combined BLK name count exceeds hard limit")
    names = shared + local
    blocks = reader.uint(MAX_BLOCKS)
    params = reader.uint(MAX_PARAMS)
    complex_size = reader.uint(MAX_BLK_BYTES)
    _require(blocks > 0, "BLK has no root block")
    complex_data = reader.take(complex_size)
    param_data = reader.take(params * 8)
    # Validate every parameter, including skipped child-block values. No object
    # tree, arbitrary include expansion, or recursive traversal is constructed.
    for at in range(0, len(param_data), 8):
        tag, value = struct.unpack_from("<II", param_data, at)
        name, typ = tag & 0xFFFFFF, tag >> 24
        _require(name < len(names) and 0 < typ < len(_TYPE_SIZES), "BLK invalid parameter name/type")
        _check_directive(names[name])
        if typ == 1:
            if value & 0x80000000:
                _require(value & 0x7FFFFFFF < len(names), "BLK string name index outside name table")
            else:
                _require(value < complex_size, "BLK string offset outside complex storage")
                end = complex_data.find(b"\0", value, value + MAX_STRING_BYTES + 1)
                _require(end >= value, "BLK unterminated/oversized string")
        elif _TYPE_SIZES[typ] > 4:
            _require(value <= complex_size and _TYPE_SIZES[typ] <= complex_size - value,
                     "BLK complex parameter outside storage")
        if typ == 9:
            _require(value in (0, 1), "BLK invalid boolean")

    seen_params, next_child, level_end, depth = 0, 1, 1, 0
    root_range = (0, 0)
    for i in range(blocks):
        if i == level_end:
            _require(next_child > i, "BLK unreachable blocks")
            depth += 1
            _require(depth <= MAX_DEPTH, "BLK nesting exceeds hard limit")
            level_end = next_child
        name = reader.uint(len(names))  # Block names are stored as nameId + 1.
        if name:
            _check_directive(names[name - 1])
        count = reader.uint(min(MAX_PARAMS, 65535))
        children = reader.uint(min(MAX_BLOCKS, 65535))
        if children:
            first = reader.uint(MAX_BLOCKS)
            _require(first == next_child and children <= blocks - first,
                     "BLK invalid child range/cycle")
            next_child += children
        start = reader.uint(MAX_PARAMS) if dedup and count else (0 if dedup else seen_params)
        _require(start <= params and count <= params - start, "BLK invalid parameter run")
        if i == 0:
            root_range = (start, count)
        if not dedup:
            seen_params += count
    _require(next_child == blocks and (dedup or seen_params == params), "BLK inconsistent block/parameter totals")
    reader.end()

    settings = {}
    start, count = root_range
    for i in range(start, start + count):
        tag, value = struct.unpack_from("<II", param_data, i * 8)
        name, typ = names[tag & 0xFFFFFF], tag >> 24
        if name not in _KEYS:
            continue
        if name == b"water_level":
            _require(typ in (2, 3), "water_level must be a real or integer")
            result = struct.unpack_from("<f" if typ == 3 else "<i", param_data, i * 8 + 4)[0]
        else:
            _require(typ == 4, f"{name.decode()}: expected Point2")
            result = list(struct.unpack_from("<2f", complex_data, value))
        values = result if isinstance(result, list) else [result]
        _require(all(math.isfinite(x) for x in values), f"{name.decode()}: non-finite value")
        key = name.decode("ascii")
        _require(key not in settings or settings[key] == result, f"{key}: conflicting repeated values")
        settings[key] = result
    missing = [name.decode() for name in _KEYS if name.decode() not in settings
               and not (allow_missing_water and name == b"water_level")]
    _require(not missing, "missing essential root settings: " + ", ".join(missing))
    _require(all(a < b for a, b in zip(settings["mapCoord0"], settings["mapCoord1"])),
             "mapCoord0/mapCoord1: bounds must have positive area")
    return settings


def _settings(data: bytes, archive: _Archive) -> dict:
    _require(0 < len(data) <= MAX_BLK_BYTES, "BLK input length outside hard limit")
    label = data[0]
    shared = archive.shared() if label in (3, 4, 5) else ()
    if label in (1, 3, 6):
        return _binary_settings(data[1:], shared, dedup=label == 6, allow_missing_water=True)
    if label == 2:
        _require(len(data) >= 4 and int.from_bytes(data[1:4], "little") == len(data) - 4,
                 "BLK compressed length mismatch")
        plain = _zstd(data[4:], MAX_BLK_BYTES)
        _require(bool(plain) and plain[0] in (1, 6), "compressed full BLK must contain format 1 or 6")
        return _binary_settings(plain[1:], dedup=plain[0] == 6, allow_missing_water=True)
    if label in (4, 5):
        dictionary = archive.ddict() if label == 5 else None
        return _binary_settings(_zstd(data[1:], MAX_BLK_BYTES, dictionary), shared, allow_missing_water=True)
    raise GameConfigError(f"unsupported BLK format {label}; binary format 1-6 required")


def extract_level_configs(game_root: Path, ids: list[str], output_root: Path) -> Path:
    """Write <output_root>/levels/<id>.blkx; return output_root for catalog.py.

    Reads only the local aces.vromfs.bin. All requested configs are validated
    before creating output. Absent water uses the pinned Dagor AcesScene loader
    default (0 metres), with explicit provenance; invalid/conflicting values fail.
    Identical repeated essential values are accepted; conflicting ones fail.
    Output must be outside the game tree. Repeated calls reuse unchanged files
    and atomically replace changed files. Existing unrelated files in levels/
    are rejected. Only the three settings go in each JSON; archive/entry hashes
    live in provenance.json, so unrelated archive changes do not defeat resume.
    Raises GameConfigError for invalid inputs, formats, limits and I/O failures.
    """
    _require(isinstance(ids, list) and 0 < len(ids) <= MAX_IDS, "ids must contain 1-128 level IDs")
    _require(all(isinstance(i, str) and _ID.fullmatch(i) for i in ids), "invalid level ID")
    _require(len(set(ids)) == len(ids), "duplicate requested level IDs")
    game_root, output_root = Path(game_root).resolve(), Path(output_root).resolve()
    _require(not output_root.is_relative_to(game_root), "config output must be outside the game tree")
    destination = output_root / "levels"
    _require(not destination.is_symlink() and destination.resolve() == destination,
             "config output levels directory must not be a link/junction")
    archive = _Archive(game_root / "aces.vromfs.bin")
    documents, errors = {}, []
    provenance = {
        "source": "local_game_vromfs", "archive": "aces.vromfs.bin",
        "archive_sha256": archive.sha256, "archive_version": archive.version,
        "defaults": {"water_level": {
            "value": 0.0, "source_url": WATER_DEFAULT_SOURCE,
            "source_symbol": "AcesScene::loadLevel",
            "rule": 'levelSettingsBlk.getReal("water_level", 0.f)',
        }},
        "entries": {},
    }
    for level_id in ids:
        name = f"levels/{level_id}.blk".encode("ascii")
        try:
            data = archive.entry(name, MAX_BLK_BYTES)
            document = _settings(data, archive)
            water_source = "level_config"
            if "water_level" not in document:
                document["water_level"] = 0.0
                water_source = "dagor_aces_scene_default"
            provenance["entries"][level_id] = {
                "entry": name.decode(), "entry_sha256": hashlib.sha256(data).hexdigest(),
                "blk_format": data[0], "water_level_source": water_source,
            }
            encoded = (json.dumps(document, indent=2, sort_keys=True, allow_nan=False) + "\n").encode("utf-8")
            _require(len(encoded) <= MAX_JSON_BYTES, "config JSON exceeds hard limit")
            documents[level_id] = encoded
        except GameConfigError as exc:
            errors.append(f"{level_id}: {exc}")
    _require(not errors, "local level config extraction failed:\n" + "\n".join(errors))
    provenance_bytes = (json.dumps(provenance, indent=2, sort_keys=True) + "\n").encode("utf-8")
    _require(len(provenance_bytes) <= MAX_PROVENANCE_BYTES, "config provenance exceeds hard limit")
    try:
        unchanged = set()
        if destination.exists():
            _require(destination.is_dir(), "config output levels path is not a directory")
            expected = {f"{mid}.blkx": mid for mid in ids}
            with os.scandir(destination) as entries:
                for entry in entries:
                    _require(entry.name in expected and entry.is_file(follow_symlinks=False),
                             "config output levels directory contains unrelated files or links")
                    level_id = expected[entry.name]
                    with open(entry.path, "rb") as handle:
                        old = handle.read(MAX_JSON_BYTES + 1)
                    _require(len(old) <= MAX_JSON_BYTES, "existing config JSON exceeds hard limit")
                    if old == documents[level_id]:
                        unchanged.add(level_id)
        output_root.mkdir(parents=True, exist_ok=True)
        with tempfile.TemporaryDirectory(prefix=".level-configs-", dir=output_root) as stage:
            staged = Path(stage) / "levels"
            staged.mkdir()
            for level_id, encoded in documents.items():
                if level_id not in unchanged:
                    (staged / f"{level_id}.blkx").write_bytes(encoded)
            _require(not destination.is_symlink() and destination.resolve() == destination,
                     "config output levels directory became a link/junction")
            if destination.exists():
                for level_id in documents:
                    if level_id not in unchanged:
                        (staged / f"{level_id}.blkx").replace(destination / f"{level_id}.blkx")
            else:
                staged.rename(destination)
            metadata = Path(stage) / "provenance.json"
            metadata.write_bytes(provenance_bytes)
            metadata.replace(output_root / "provenance.json")
    except OSError as exc:
        raise GameConfigError(f"cannot write local level configs to {output_root}: {exc}") from exc
    return output_root

"""Synthetic VROMFS/BLK fixtures; no game assets, DLLs or network required."""
from __future__ import annotations

import json
from pathlib import Path
import struct
import tempfile
import unittest
from unittest.mock import patch
import zlib

import zstandard as zstd

import game_configs as gc


def vu(value):
    out = bytearray()
    while value >= 128:
        out.append((value & 127) | 128)
        value >>= 7
    out.append(value)
    return bytes(out)


def nametable(names):
    raw = b"".join(name + b"\0" for name in names)
    return vu(len(names)) + (vu(len(raw)) + raw if names else b"")


DEFAULT = [(b"mapCoord0", 4, [-100, -200]),
           (b"mapCoord1", 4, [300, 400]), (b"water_level", 3, -11.5)]


def dump(params=None, *, shared=(), blocks=None, root_count=None, dedup=False):
    params = DEFAULT if params is None else params
    local = tuple(dict.fromkeys(name for name, _, _ in params if name not in shared))
    names = tuple(shared) + local
    complex_data, records = bytearray(), bytearray()
    for name, typ, value in params:
        if typ in (4, 5, 6):
            word = len(complex_data)
            complex_data.extend(struct.pack("<" + "f" * len(value), *value))
        elif typ == 3:
            word = struct.unpack("<I", struct.pack("<f", value))[0]
        else:
            word = value & 0xFFFFFFFF
        records.extend(struct.pack("<II", names.index(name) | typ << 24, word))
    if blocks is None:
        blocks = [(0, len(params) if root_count is None else root_count, 0, 0)]
    tail = bytearray()
    for name, count, children, first in blocks:
        tail.extend(vu(name) + vu(count) + vu(children))
        if children:
            tail.extend(vu(first))
        if dedup and count:
            tail.extend(vu(0))
    return (nametable(local) + vu(len(blocks)) + vu(len(params)) + vu(len(complex_data))
            + complex_data + records + tail)


def wrap_blk(raw=None, label=1, *, shared=(), dictionary=None):
    raw = dump(shared=shared) if raw is None else raw
    if label == 2:
        compressed = zstd.ZstdCompressor().compress(b"\1" + raw)
        return b"\2" + len(compressed).to_bytes(3, "little") + compressed
    if label in (4, 5):
        raw = zstd.ZstdCompressor(dict_data=dictionary if label == 5 else None,
                                  write_content_size=False).compress(raw)
    return bytes([label]) + raw


def archive_body(entries):
    # Independently build the documented PatchableTab/filename-pointer layout.
    nt = 32
    names_at = nt + len(entries) * 8
    strings = b"".join(name + b"\0" for name, _ in entries)
    dt = (names_at + len(strings) + 15) & ~15
    data_at = dt + len(entries) * 16
    body = bytearray(data_at)
    struct.pack_into("<8I", body, 0, nt, len(entries), 0, 0, dt, len(entries), 0, 0)
    body[names_at:names_at + len(strings)] = strings
    for i, (name, data) in enumerate(entries):
        struct.pack_into("<Q", body, nt + i * 8, names_at)
        names_at += len(name) + 1
        struct.pack_into("<4I", body, dt + i * 16, len(body), len(data), 0, 0)
        body.extend(data)
    return bytes(body)


def archive_bytes(entries=None, *, codec="zstd", extended=True, body=None):
    body = archive_body(entries) if body is None else body
    flag = 0
    if codec == "zstd":
        packed = gc._deobfuscate(zstd.ZstdCompressor().compress(body))
        flag = 0x40000000 | len(packed)
    elif codec == "zlib":
        packed = zlib.compress(body)
        flag = len(packed)
    else:
        packed = body
    return (struct.pack("<4s4sII", b"VRFx" if extended else b"VRFs", b"\0\0PC", len(body), flag)
            + (struct.pack("<HHI", 8, 0, 123) if extended else b"") + packed)


class LocalConfigTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="game-config-test-")
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.game = self.root / "game"
        self.game.mkdir()
        self.output = self.root / "output"

    def install(self, entries=None, **options):
        if entries is None:
            entries = [(b"levels/example.blk", wrap_blk())]
        raw = archive_bytes(entries, **options)
        (self.game / "aces.vromfs.bin").write_bytes(raw)
        return raw

    def extract(self, ids=None):
        return gc.extract_level_configs(self.game, ["example"] if ids is None else ids, self.output)

    def failure(self, pattern, ids=None):
        with self.assertRaisesRegex(gc.GameConfigError, pattern):
            self.extract(ids)
        self.assertFalse(self.output.exists(), "invalid inputs must not produce partial output")

    def test_archive_codecs_and_minimal_output_provenance(self):
        import hashlib
        for codec, extended in (("raw", False), ("zlib", True), ("zstd", True)):
            with self.subTest(codec=codec):
                self.output = self.root / codec
                raw = self.install(codec=codec, extended=extended)
                self.assertEqual(self.extract(), self.output.resolve())
                document = json.loads((self.output / "levels/example.blkx").read_text())
                self.assertEqual(document["mapCoord0"], [-100, -200])
                self.assertEqual(document["mapCoord1"], [300, 400])
                self.assertEqual(document["water_level"], -11.5)
                self.assertEqual(set(document), {"mapCoord0", "mapCoord1", "water_level"})
                provenance = json.loads((self.output / "provenance.json").read_text())
                self.assertEqual(provenance["archive_sha256"], hashlib.sha256(raw).hexdigest())
                self.assertEqual(provenance["source"], "local_game_vromfs")
                self.assertEqual(provenance["entries"]["example"]["blk_format"], 1)
                self.assertEqual((self.game / "aces.vromfs.bin").read_bytes(), raw)

    def test_obfuscation_known_words_head_tail_and_unaligned_length(self):
        key = bytes.fromhex("55aa55aa0ff00ff055aa55aa48124812")
        reverse_words = bytes.fromhex("4812481255aa55aa0ff00ff055aa55aa")
        self.assertEqual(gc._deobfuscate(b"\0" * 15), b"\0" * 15)
        self.assertEqual(gc._deobfuscate(b"\0" * 16), key)
        self.assertEqual(gc._deobfuscate(b"\0" * 35), key + reverse_words + b"\0" * 3)

    def test_resume_preserves_bytes_and_mtimes_across_unrelated_archive_changes(self):
        self.install()
        self.extract()
        path = self.output / "levels/example.blkx"
        before, mtime = path.read_bytes(), path.stat().st_mtime_ns
        metadata = (self.output / "provenance.json").read_bytes()
        self.extract()
        self.assertEqual((self.output / "provenance.json").read_bytes(), metadata)
        self.install([(b"levels/example.blk", wrap_blk()), (b"unrelated.bin", b"new asset")])
        self.extract()
        self.assertEqual(path.read_bytes(), before)
        self.assertEqual(path.stat().st_mtime_ns, mtime)
        self.assertNotEqual((self.output / "provenance.json").read_bytes(), metadata)
        # Reordering BLK parameters also leaves the minimal JSON stable.
        self.install([(b"levels/example.blk", wrap_blk(dump(list(reversed(DEFAULT)))))])
        self.extract()
        self.assertEqual(path.read_bytes(), before)
        self.assertEqual(path.stat().st_mtime_ns, mtime)

    def test_resume_updates_changed_settings_and_repairs_missing_file(self):
        entries = [(b"levels/example.blk", wrap_blk()), (b"levels/other.blk", wrap_blk())]
        self.install(entries)
        self.extract(["example", "other"])
        other = self.output / "levels/other.blkx"
        mtime = other.stat().st_mtime_ns
        changed = wrap_blk(dump(DEFAULT[:2] + [(b"water_level", 3, 42)]))
        self.install([(b"levels/example.blk", changed), entries[1]])
        self.extract(["example", "other"])
        self.assertEqual(json.loads((self.output / "levels/example.blkx").read_text())["water_level"], 42)
        self.assertEqual(other.stat().st_mtime_ns, mtime)
        other.unlink()
        self.extract(["example", "other"])
        self.assertTrue(other.is_file())

    def test_invalid_rerun_leaves_existing_configs_and_provenance_unchanged(self):
        self.install()
        self.extract()
        path = self.output / "levels/example.blkx"
        before, metadata = path.read_bytes(), (self.output / "provenance.json").read_bytes()
        self.install([(b"levels/example.blk", wrap_blk(dump(DEFAULT[1:])))])
        with self.assertRaisesRegex(gc.GameConfigError, "missing essential"):
            self.extract()
        self.assertEqual(path.read_bytes(), before)
        self.assertEqual((self.output / "provenance.json").read_bytes(), metadata)

    def test_private_compressed_and_deduplicated_blk(self):
        for label in (1, 2, 6):
            with self.subTest(label=label):
                self.output = self.root / str(label)
                raw = dump(dedup=label == 6)
                self.install([(b"levels/example.blk", wrap_blk(raw, label))])
                self.extract()

    def test_shared_names_and_dictionary_formats_and_local_name_suffix(self):
        shared = (b"mapCoord0", b"mapCoord1")
        dictionary_data = b"terrain synthetic dictionary mapCoord0 water_level " * 16
        dictionary = zstd.ZstdCompressionDict(dictionary_data)
        dict_hash = bytes(range(32))
        shared_entry = (b"\xff?nm", b"12345678" + dict_hash + zstd.ZstdCompressor().compress(nametable(shared)))
        for label in (3, 4, 5):
            with self.subTest(label=label):
                self.output = self.root / str(label)
                self.install([(b"levels/example.blk", wrap_blk(label=label, shared=shared, dictionary=dictionary)),
                              (dict_hash.hex().encode() + b".dict", dictionary_data), shared_entry])
                self.extract()
                self.assertEqual(json.loads((self.output / "levels/example.blkx").read_text())["water_level"], -11.5)

    def test_missing_shared_names_or_dictionary_fails(self):
        self.install([(b"levels/example.blk", wrap_blk(label=3))])
        self.failure("missing local VROMFS entry.*nm")
        shared = (b"mapCoord0", b"mapCoord1", b"water_level")
        self.install([(b"levels/example.blk", wrap_blk(label=5, shared=shared)),
                      (b"\xff?nm", b"0" * 40 + zstd.ZstdCompressor().compress(nametable(shared)))])
        self.failure("missing local VROMFS entry.*dict")

    def test_missing_essential_values_aggregate_without_writing(self):
        self.install([(b"levels/example.blk", wrap_blk(dump(DEFAULT[1:]))),
                      (b"levels/other.blk", wrap_blk(dump(DEFAULT[::2])))])
        with self.assertRaises(gc.GameConfigError) as raised:
            self.extract(["example", "other", "absent"])
        self.assertIn("example: missing essential root settings: mapCoord0", str(raised.exception))
        self.assertIn("other: missing essential root settings: mapCoord1", str(raised.exception))
        self.assertIn("absent: missing local VROMFS entry", str(raised.exception))
        self.assertFalse(self.output.exists())

    def test_identical_repeats_accepted_conflicts_fail(self):
        self.install([(b"levels/example.blk", wrap_blk(dump(DEFAULT + [DEFAULT[0], DEFAULT[2]])))])
        self.extract()
        self.output = self.root / "conflicting"
        self.install([(b"levels/example.blk", wrap_blk(dump(DEFAULT + [(b"water_level", 3, 1)])))])
        self.failure("water_level: conflicting repeated")
        self.install([(b"levels/example.blk", wrap_blk(dump(DEFAULT + [(b"mapCoord0", 4, [-50, -50])])))])
        self.failure("mapCoord0: conflicting repeated")

    def test_child_settings_do_not_override_proven_root_water_default(self):
        raw = dump(blocks=[(0, 2, 1, 1), (1, 1, 0, 0)])
        self.install([(b"levels/example.blk", wrap_blk(raw))])
        self.extract()
        self.assertEqual(json.loads((self.output / "levels/example.blkx").read_text())["water_level"], 0)
        provenance = json.loads((self.output / "provenance.json").read_text())
        self.assertEqual(provenance["entries"]["example"]["water_level_source"], "dagor_aces_scene_default")

    def test_missing_water_uses_pinned_primary_default_with_explicit_provenance(self):
        self.install([(b"levels/example.blk", wrap_blk(dump(DEFAULT[:2])))])
        self.extract()
        document = json.loads((self.output / "levels/example.blkx").read_text())
        self.assertEqual(document, {"mapCoord0": [-100, -200], "mapCoord1": [300, 400], "water_level": 0.0})
        provenance = json.loads((self.output / "provenance.json").read_text())
        self.assertEqual(provenance["entries"]["example"]["water_level_source"], "dagor_aces_scene_default")
        default = provenance["defaults"]["water_level"]
        self.assertEqual(default["value"], 0.0)
        self.assertEqual(default["source_symbol"], "AcesScene::loadLevel")
        self.assertIn("2baf19472d60b96b437b2f9e549a3a6c8958cfd8", default["source_url"])
        self.assertIn('getReal("water_level", 0.f)', default["rule"])

    def test_directives_in_child_blocks_are_rejected(self):
        shared = (b"@override:water_level",)
        raw = dump(shared=shared, blocks=[(0, 3, 1, 1), (1, 0, 0, 0)])
        with self.assertRaisesRegex(gc.GameConfigError, "include/override"):
            gc._binary_settings(raw, shared)

    def test_types_nonfinite_reversed_bounds_and_includes(self):
        cases = [([DEFAULT[0], DEFAULT[1], (b"water_level", 9, 0)], "must be a real or integer"),
                 ([DEFAULT[0], DEFAULT[1], (b"water_level", 3, float("nan"))], "non-finite"),
                 ([(b"mapCoord0", 4, [float("inf"), 0])] + DEFAULT[1:], "non-finite"),
                 ([(b"mapCoord0", 4, [300, -200])] + DEFAULT[1:], "positive area"),
                 ([(b"mapCoord0", 2, 0)] + DEFAULT[1:], "expected Point2"),
                 (DEFAULT + [(b"@include", 2, 0)], "include/override")]
        for params, message in cases:
            with self.subTest(message=message):
                self.install([(b"levels/example.blk", wrap_blk(dump(params)))])
                self.failure(message)

    def test_integer_water_zero_is_explicitly_preserved(self):
        self.install([(b"levels/example.blk", wrap_blk(dump(DEFAULT[:2] + [(b"water_level", 2, 0)])))])
        self.extract()
        self.assertEqual(json.loads((self.output / "levels/example.blkx").read_text())["water_level"], 0)
        provenance = json.loads((self.output / "provenance.json").read_text())
        self.assertEqual(provenance["entries"]["example"]["water_level_source"], "level_config")

    def test_duplicate_case_insensitive_archive_paths_fail(self):
        self.install([(b"levels/example.blk", wrap_blk()), (b"Levels/Example.blk", wrap_blk())])
        self.failure("ambiguous duplicate VROMFS entry")

    def test_id_validation_before_input_or_output_access(self):
        for ids in ([], ["../escape"], ["A"], ["x/x"], ["x\\x"], ["x.blk"], ["x", "x"], ["x"] * 129, [1], "x"):
            with self.subTest(ids=str(ids)[:50]):
                self.failure("ids must|invalid level ID|duplicate requested", ids)

    def test_refuses_output_inside_game_and_unrelated_existing_files(self):
        self.install()
        self.output = self.game / "generated"
        self.failure("outside the game tree")
        self.output = self.root / "existing"
        (self.output / "levels").mkdir(parents=True)
        sentinel = self.output / "levels/unrelated.txt"
        sentinel.write_text("untouched")
        with self.assertRaisesRegex(gc.GameConfigError, "contains unrelated"):
            self.extract()
        self.assertEqual(sentinel.read_text(), "untouched")

    def test_missing_archive_is_clear(self):
        self.failure("cannot read local archive")

    def test_archive_length_counts_and_offsets_are_checked(self):
        good = self.install(codec="raw")
        cases = []
        for at, value, message in ((8, gc.MAX_BODY_BYTES + 1, "body size"),
                                   (24 + 4, gc.MAX_FILES + 1, "file count"),
                                   (24 + 16, 0xFFFFFFFF, "tables"),
                                   (24 + 32, 0xFFFFFFFF, "filename")):
            bad = bytearray(good)
            struct.pack_into("<I", bad, at, value)
            cases.append((bad, message))
        cases.extend([(good[:15], "input length"), (good[:-1], "payload"), (b"oops" + good[4:], "label/target")])
        for raw, message in cases:
            with self.subTest(message=message):
                (self.game / "aces.vromfs.bin").write_bytes(raw)
                self.failure(message)
        self.install(codec="raw")
        with patch.object(gc, "MAX_ARCHIVE_BYTES", 32):
            self.failure("input exceeds hard limit")

    def test_entry_range_and_selected_size_limits(self):
        body = bytearray(archive_body([(b"levels/example.blk", wrap_blk())]))
        dt = struct.unpack_from("<I", body, 16)[0]
        struct.pack_into("<I", body, dt, len(body) + 1)
        self.install(body=bytes(body))
        self.failure("entry outside data storage")
        self.install()
        with patch.object(gc, "MAX_BLK_BYTES", 16):
            self.failure("entry empty/exceeds hard limit")

    def test_zstd_known_unknown_size_bombs_truncation_and_extra_frames(self):
        for content_size in (True, False):
            frame = zstd.ZstdCompressor(write_content_size=content_size).compress(b"x" * 2048)
            with self.assertRaises(gc.GameConfigError):
                gc._zstd(frame, 1024)
        frame = zstd.ZstdCompressor().compress(b"x" * 2048)
        for bad in (frame[:-1], frame + b"junk", frame + frame):
            with self.assertRaises(gc.GameConfigError):
                gc._zstd(bad, 4096)
        with patch.object(gc, "MAX_WINDOW_BYTES", 1024):
            with self.assertRaisesRegex(gc.GameConfigError, "window"):
                gc._zstd(frame, 4096)

    def test_zlib_bomb_and_trailing_or_truncated_stream(self):
        self.install(codec="zlib")
        good = (self.game / "aces.vromfs.bin").read_bytes()
        bomb = bytearray(good)
        struct.pack_into("<I", bomb, 8, 32)
        (self.game / "aces.vromfs.bin").write_bytes(bomb)
        self.failure("invalid/oversized VROMFS zlib")
        for payload in (good[24:-1], good[24:] + b"junk"):
            bad = bytearray(good[:24] + payload)
            struct.pack_into("<I", bad, 12, len(payload))
            (self.game / "aces.vromfs.bin").write_bytes(bad)
            self.failure("invalid/oversized VROMFS zlib")

    def test_blk_varints_names_counts_and_trailing_bytes(self):
        malformed = [(b"\x80" * 6, "varint"), (vu(gc.MAX_NAMES + 1), "hard limit"),
                     (b"\1\1x", "terminators"), (b"\2\4a\0a\0", "duplicate names"),
                     (b"\0" + vu(gc.MAX_BLOCKS + 1), "hard limit"),
                     (b"\0\1" + vu(gc.MAX_PARAMS + 1), "hard limit"),
                     (b"\0\1\0" + vu(gc.MAX_BLK_BYTES + 1), "hard limit"),
                     (dump() + b"junk", "trailing bytes"), (dump()[:-1], "outside input")]
        for data, message in malformed:
            with self.subTest(message=message):
                with self.assertRaisesRegex(gc.GameConfigError, message):
                    gc._binary_settings(data)

    def test_name_byte_count_and_individual_string_limits(self):
        for data, message in ((b"\1" + vu(gc.MAX_NAME_BYTES + 1), "hard limit"),
                              (nametable([b"x" * (gc.MAX_STRING_BYTES + 1)]), "name exceeds"),
                              (b"\1\5a\0b\0\0", "terminators")):
            with self.assertRaisesRegex(gc.GameConfigError, message):
                gc._binary_settings(data)

    def test_bad_child_ranges_unreachable_blocks_and_depth(self):
        for blocks, message in (([(0, 3, 1, 0), (1, 0, 0, 0)], "child range/cycle"),
                                 ([(0, 3, 0, 0), (1, 0, 0, 0)], "unreachable"),
                                 ([(0, 2, 0, 0)], "totals")):
            with self.assertRaisesRegex(gc.GameConfigError, message):
                gc._binary_settings(dump(blocks=blocks))
        blocks = [(0, 3, 1, 1)] + [(1, 0, 1, i + 1) for i in range(1, gc.MAX_DEPTH + 1)] + [(1, 0, 0, 0)]
        with self.assertRaisesRegex(gc.GameConfigError, "nesting"):
            gc._binary_settings(dump(blocks=blocks))
        valid = [(0, 3, 1, 1)] + [(1, 0, 1, i + 1) for i in range(1, gc.MAX_DEPTH)] + [(1, 0, 0, 0)]
        self.assertEqual(gc._binary_settings(dump(blocks=valid))["water_level"], -11.5)

    def test_invalid_parameter_types_names_and_complex_offsets(self):
        for params, message in ((DEFAULT + [(b"bad", 255, 0)], "name/type"),
                                (DEFAULT + [(b"bad", 1, 0x8000FFFF)], "string name index"),
                                (DEFAULT + [(b"bad", 1, 0xFFFF)], "string offset"),
                                (DEFAULT + [(b"bad", 11, 0xFFFF)], "complex parameter")):
            with self.assertRaisesRegex(gc.GameConfigError, message):
                gc._binary_settings(dump(params))

    def test_unknown_text_blk_and_nested_compression_are_rejected(self):
        for data, message in ((b"mapCoord0:p2=0,0", "unsupported BLK format"),
                              (b"\2\xff\xff\xffx", "compressed length mismatch")):
            self.install([(b"levels/example.blk", data)])
            self.failure(message)
        nested = wrap_blk(label=2)
        packed = zstd.ZstdCompressor().compress(nested)
        self.install([(b"levels/example.blk", b"\2" + len(packed).to_bytes(3, "little") + packed)])
        self.failure("must contain format 1 or 6")


if __name__ == "__main__":
    unittest.main()

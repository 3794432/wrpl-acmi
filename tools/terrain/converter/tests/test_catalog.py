from __future__ import annotations

import tempfile
import json
import unittest
from pathlib import Path

from catalog import build_catalog, _read_config


class CatalogTargetTests(unittest.TestCase):
    def test_explicit_sinai_target_beats_avg_prefix_and_uses_flat_datamine(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            game = root / "game"
            datamine = root / "datamine"
            levels = game / "levels"
            configs = datamine / "levels"
            levels.mkdir(parents=True)
            configs.mkdir(parents=True)
            (levels / "avg_egypt_sinai.bin").write_bytes(b"")
            (configs / "avg_egypt_sinai.blkx").write_text(
                '{"mapCoord0":[-65536,-65536],"mapCoord1":[65536,65536],"water_level":0}',
                encoding="utf-8",
            )
            catalog = build_catalog(datamine, game, ["avg_egypt_sinai"])
            self.assertEqual(len(catalog), 1)
            entry = catalog[0]
            self.assertTrue(entry["is_air"])
            self.assertTrue(entry["explicit_target"])
            self.assertEqual(entry["config_source"], "local_datamine")
            self.assertEqual(entry["map_coord1"], [65536.0, 65536.0])

    def test_repeated_datamine_vector_preserves_full_sinai_bounds(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'sinai.blkx'
            path.write_text(json.dumps({
                'mapCoord0': [[-65536, -65536], [-65536, -65536]],
                'mapCoord1': [65536, 65536], 'water_level': 0,
            }))
            config = _read_config(path)
            self.assertIsNone(config['config_error'])
            self.assertEqual(config['map_coord0'], [-65536.0, -65536.0])
            self.assertEqual(config['map_coord1'], [65536.0, 65536.0])

    def test_conflicting_repeated_vector_is_an_error(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'ambiguous.blkx'
            path.write_text(json.dumps({'mapCoord0': [[-1, -1], [-2, -2]]}))
            config = _read_config(path)
            self.assertIn('conflicting repeated', config['config_error'])


if __name__ == "__main__":
    unittest.main()

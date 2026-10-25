from __future__ import annotations

import importlib.util
import json
from pathlib import Path
import unittest


MODULE_PATH = Path(__file__).with_name("build_radar_modes.py")
SPEC = importlib.util.spec_from_file_location("build_radar_modes", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
BUILDER = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(BUILDER)


class RadarModeBuilderTests(unittest.TestCase):
    def test_sensor_reference_is_relative_to_documented_game_data(self) -> None:
        self.assertEqual(
            BUILDER.sensor_reference({"blk": "gameData\\sensors\\us_an_apg_68_v_7.blk"}),
            "us_an_apg_68_v_7.blk",
        )
        self.assertIsNone(BUILDER.sensor_reference({"blk": "gameData/weapons/not_a_sensor.blk"}))
        self.assertIsNone(BUILDER.sensor_reference({}))

    def test_sensor_source_name_matches_datamine_extension_and_case(self) -> None:
        self.assertEqual(BUILDER.sensor_source_name("LaserDesignatorSensor.blk"), "LaserDesignatorSensor.blkx")
        self.assertEqual(BUILDER.sensor_source_name("already_lower.blkx"), "already_lower.blkx")

    def test_scan_pattern_preserves_array_type_without_guessing(self) -> None:
        self.assertEqual(
            BUILDER.scan_pattern_record("hmdLock", {"type": ["pyramide", "pyramide"], "width": 2, "barHeight": 0.3}),
            {"id": "hmdLock", "types": ["pyramide", "pyramide"], "width_deg": 2.0, "bar_height_deg": 0.3},
        )
        self.assertEqual(BUILDER.scan_pattern_record("track", {"type": "no"}), {"id": "track", "types": ["no"]})
        self.assertEqual(BUILDER.scan_pattern_record("missing", {}), {"id": "missing", "types": []})

    def test_compact_sensor_keeps_replay_index_order(self) -> None:
        compact = BUILDER.compact_sensor(
            "sensors/example.blkx",
            {
                "name": "Example",
                "type": "radar",
                "transivers": {
                    "second": {"active": False, "visibilityType": "radarIntercept"},
                    "first": {},
                },
                "scanPatterns": {
                    "twsNarrow": {"type": "pyramide"},
                    "track": {"type": "no"},
                },
                "signals": {"search": {}, "track": {}},
            },
        )
        self.assertEqual([item["id"] for item in compact["transceivers"]], ["second", "first"])
        self.assertEqual([item["id"] for item in compact["scan_patterns"]], ["twsNarrow", "track"])
        self.assertEqual(compact["signals"], ["search", "track"])
        self.assertFalse(compact["transceivers"][0]["active"])
        self.assertTrue(compact["transceivers"][1]["active"])

    def test_generated_table_retains_pinned_provenance_and_rwr_slot(self) -> None:
        table = json.loads((MODULE_PATH.parent.parent.parent / "cmd/acmi/data/radar_modes.json").read_text(encoding="utf-8"))
        self.assertEqual(table["schema"], BUILDER.SCHEMA)
        self.assertEqual(table["source"]["commit"], BUILDER.COMMIT)
        self.assertEqual(table["source"]["repository"], BUILDER.REPOSITORY)
        refs = table["models"]["f_16c_block_50"]
        self.assertEqual(len(refs), 4)
        self.assertIn("alr_56", refs[2].lower())
        self.assertEqual(table["sensors"][refs[0]]["name"], "AN/APG-68")
        self.assertIn("f_4f", table["ambiguous_aliases"])
        self.assertNotIn("f_4f", table["models"])
        self.assertIn("f_4f_late", table["models"])


if __name__ == "__main__":
    unittest.main()

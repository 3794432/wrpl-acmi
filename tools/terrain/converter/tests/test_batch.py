from __future__ import annotations

import json
import tempfile
import unittest
from pathlib import Path

from batch import resume_status, stream_sha256


class ResumeTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.root = Path(self.temporary.name)
        self.map_id = "avg_egypt_sinai"
        self.source = self.root / "game" / "levels" / f"{self.map_id}.bin"
        self.config = (
            self.root / "datamine" / "aces.vromfs.bin_u" / "levels" / f"{self.map_id}.blkx"
        )
        self.source.parent.mkdir(parents=True)
        self.config.parent.mkdir(parents=True)
        self.source.write_bytes(b"terrain-source")
        self.config.write_text('mapCoord0:[-1,-1]\nmapCoord1:[1,1]\nwater_level:0\n', encoding="utf-8")
        self.entry = {
            "id": self.map_id,
            "source_path": str(self.source),
            "config_path": str(self.config),
            "map_coord0": [-1.0, -1.0],
            "map_coord1": [1.0, 1.0],
            "water_level": 0.0,
            "heightmap_format": "HM2",
            "has_hm2": True,
            "has_lmap": True,
            "has_frt": True,
            "explicit_target": True,
        }
        folder = self.root / "package" / "maps" / self.map_id
        folder.mkdir(parents=True)
        self.data = folder / f"{self.map_id}.data"
        self.data.write_bytes(bytes(range(8)))
        (folder / "CustomHeightmapList.xml").write_text("<Resources/>", encoding="utf-8")
        self.metadata_path = folder / "metadata.json"
        self.write_metadata()

    def tearDown(self):
        self.temporary.cleanup()

    def write_metadata(self):
        metadata = {
            "status": "ok",
            "id": self.map_id,
            "request": {"resolution": 4096},
            "source": {"path": str(self.source), "sha256": stream_sha256(self.source)},
            "catalog": {
                "map_coord0": self.entry["map_coord0"],
                "map_coord1": self.entry["map_coord1"],
                "water_level": self.entry["water_level"],
                "heightmap_format": self.entry["heightmap_format"],
                "has_hm2": True,
                "has_lmap": True,
                "has_frt": True,
                "explicit_target": True,
                "config_path": str(self.config),
                "config_size": self.config.stat().st_size,
                "config_sha256": stream_sha256(self.config),
            },
            "output": {
                "data_file": self.data.name,
                "data_sha256": stream_sha256(self.data),
            },
        }
        self.metadata_path.write_text(json.dumps(metadata), encoding="utf-8")

    def test_matching_source_config_and_parameters_resume(self):
        valid, reason = resume_status(self.root / "package", self.entry, 4096)
        self.assertTrue(valid, reason)

    def test_changed_config_rejects_stale_raster(self):
        self.config.write_text("water_level: 99\n", encoding="utf-8")
        valid, reason = resume_status(self.root / "package", self.entry, 4096)
        self.assertFalse(valid)
        self.assertIn("config SHA256 differs", reason)

    def test_changed_bounds_rejects_stale_raster(self):
        self.entry["map_coord1"] = [2.0, 2.0]
        valid, reason = resume_status(self.root / "package", self.entry, 4096)
        self.assertFalse(valid)
        self.assertIn("map_coord1 differs", reason)

    def test_changed_water_level_rejects_stale_raster(self):
        self.entry["water_level"] = -4.0
        valid, reason = resume_status(self.root / "package", self.entry, 4096)
        self.assertFalse(valid)
        self.assertIn("water_level differs", reason)


if __name__ == "__main__":
    unittest.main()

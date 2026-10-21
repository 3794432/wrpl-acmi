from __future__ import annotations

import hashlib
import json
import math
import tempfile
import unittest
import xml.etree.ElementTree as ET
from pathlib import Path

from finalize import PackageError, finalize


MAP_ID = "avg_egypt_sinai"


def zero_origin(x, z):
    latitude = z / 111320.0
    longitude = x / (111320.0 * math.cos(math.radians(latitude)))
    return longitude, latitude


class FinalizeTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.root = Path(self.temporary.name) / "package"
        self.root.mkdir(parents=True)
        self.references = self.root / "AtlasReferences.json"
        self.references.write_text(
            json.dumps({MAP_ID: {"latitude": 60, "longitude": 20}}), encoding="utf-8"
        )
        self.catalog_path = self.root / "catalog.json"
        self.catalog_path.write_text(json.dumps([{
            "id": MAP_ID, "is_air": True, "explicit_target": True, "exists": True
        }]), encoding="utf-8")
        folder = self.root / "maps" / MAP_ID
        folder.mkdir(parents=True)
        data = b"\x01\x00\x02\x00\x03\x00\x04\x00"
        self.data_path = folder / f"{MAP_ID}.data"
        self.data_path.write_bytes(data)
        metadata = {
            "status": "ok",
            "id": MAP_ID,
            "format_decoder_info": {
                "heightmap_mode": "render_mesh_hm2_overlay",
                "base_decoder": "render_mesh",
                "actual_blocks": {"has_hm2": True, "has_lmap": True, "has_frt": True},
            },
            "coverage": {"hm2_overlay_pixel_count": 4},
            "output": {
                "width": 2,
                "height": 2,
                "data_file": self.data_path.name,
                "data_bytes": len(data),
                "data_sha256": hashlib.sha256(data).hexdigest(),
                "sample_bounds_m": [-32.0, -32.0, 32.0, 32.0],
            },
        }
        self.metadata_path = folder / "metadata.json"
        self.metadata_path.write_text(json.dumps(metadata), encoding="utf-8")
        self.xml_path = folder / "CustomHeightmapList.xml"
        self.write_xml()
        manifest = {
            "status": "complete",
            "requested_ids": [MAP_ID],
            "entries": {MAP_ID: {"id": MAP_ID, "status": "ok"}},
        }
        (self.root / "manifest.json").write_text(json.dumps(manifest), encoding="utf-8")

    def tearDown(self):
        self.temporary.cleanup()

    def write_xml(self, offset=0.0):
        root = ET.Element("Resources")
        listing = ET.SubElement(root, "CustomHeightmapList")
        node = ET.SubElement(listing, "CustomHeightmap", Layer="Real World", ID="WarThunder." + MAP_ID)
        values = {
            "File": self.data_path.name, "Visible": "true", "BigEndian": "0",
            "Signed": "true", "Width": "2", "Height": "2", "AltitudeFactor": "1",
            "AltitudeOffset": "0", "Projection": "Quad",
        }
        for name, value in values.items():
            ET.SubElement(node, name).text = value
        for name, x, z in (
            ("BottomLeft", -32, -32), ("BottomRight", 32, -32),
            ("TopRight", 32, 32), ("TopLeft", -32, 32),
        ):
            corner = ET.SubElement(node, name)
            longitude, latitude = zero_origin(x, z)
            ET.SubElement(corner, "Longitude").text = format(longitude + offset, ".12f")
            ET.SubElement(corner, "Latitude").text = format(latitude, ".12f")
        ET.ElementTree(root).write(self.xml_path, encoding="utf-8", xml_declaration=True)

    def test_complete_zero_origin_package_and_sinai_mode(self):
        report = finalize(
            self.root, self.references, self.catalog_path, 1,
            [MAP_ID + "=render_mesh_hm2_overlay"],
        )
        self.assertTrue(report["installer_ready"])
        self.assertTrue((self.root / "terrain-build.json").is_file())

    def test_corrupted_raster_is_rejected_even_without_verify_outputs(self):
        self.data_path.write_bytes(b"\xff" * 8)
        with self.assertRaisesRegex(PackageError, "data SHA256 differs"):
            finalize(self.root, self.references, self.catalog_path, 1, [])

    def test_nonzero_origin_xml_is_rejected(self):
        self.write_xml(offset=0.01)
        with self.assertRaisesRegex(PackageError, "zero-origin"):
            finalize(self.root, self.references, self.catalog_path, 1, [])


if __name__ == "__main__":
    unittest.main()

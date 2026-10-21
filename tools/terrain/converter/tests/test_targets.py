from __future__ import annotations

import json
import tempfile
import unittest
from pathlib import Path

from targets import TargetReferenceError, load_reference_ids


class TargetReferenceTests(unittest.TestCase):
    def write(self, value):
        temporary = tempfile.NamedTemporaryFile("w", suffix=".json", delete=False, encoding="utf-8")
        self.addCleanup(lambda: Path(temporary.name).unlink(missing_ok=True))
        json.dump(value, temporary)
        temporary.close()
        return Path(temporary.name)

    def test_preserves_authoritative_order(self):
        path = self.write({
            "avg_egypt_sinai": {"latitude": 60, "longitude": 20},
            "air_afghan": {"latitude": -60, "longitude": -140},
        })
        self.assertEqual(load_reference_ids(path, 2), ["avg_egypt_sinai", "air_afghan"])

    def test_exact_count_is_requirement(self):
        path = self.write({"air_afghan": {"latitude": -60, "longitude": -140}})
        with self.assertRaisesRegex(TargetReferenceError, "expected exactly 69"):
            load_reference_ids(path, 69)

    def test_rejects_invalid_coordinates(self):
        path = self.write({"air_afghan": {"latitude": 100, "longitude": -140}})
        with self.assertRaisesRegex(TargetReferenceError, "latitude"):
            load_reference_ids(path)

    def test_rejects_non_object_document(self):
        path = self.write(["air_afghan"])
        with self.assertRaisesRegex(TargetReferenceError, "JSON object"):
            load_reference_ids(path)


if __name__ == "__main__":
    unittest.main()

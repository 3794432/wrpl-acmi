"""Independent verifier regressions; no game data or large raster allocation."""

import copy
import math
import unittest

from verify_outputs import CheckLog, _verify_decoder_dimensions, _verify_xml


class QuadTests(unittest.TestCase):
    def setUp(self):
        self.report = {
            "entry_count": 1,
            "customheightmap_attributes": {"ID": "WarThunder.test", "Layer": "Real World"},
            "fields": {
                "File": "test.data", "Width": "4096", "Height": "4096",
                "BigEndian": "0", "Signed": "true", "AltitudeFactor": "1",
                "AltitudeOffset": "0", "Projection": "Quad",
            },
            "corners": {},
        }
        for name, x, z in (
            ("BottomLeft", 8, 8), ("BottomRight", 65528, 8),
            ("TopRight", 65528, 65528), ("TopLeft", 8, 65528),
        ):
            lat = z / 111320.0
            lon = x / (111320.0 * math.cos(math.radians(lat)))
            self.report["corners"][name] = {"Longitude": lon, "Latitude": lat}

    def check(self):
        log = CheckLog()
        _verify_xml(self.report, "test", 4096, 4096, (1, 0), log)
        return log

    def test_asymmetric_zero_origin_quad_is_valid(self):
        corners = self.report["corners"]
        self.assertGreater(
            corners["TopRight"]["Longitude"] - corners["BottomRight"]["Longitude"], 1e-6
        )
        self.assertEqual(self.check().errors, [])

    def test_reversed_quad_is_rejected(self):
        for names in (
            ("BottomLeft", "BottomRight", "TopLeft", "TopRight"),
            ("BottomLeft", "TopLeft", "BottomRight", "TopRight"),
        ):
            with self.subTest(names=names):
                original = copy.deepcopy(self.report)
                corners = self.report["corners"]
                for first, second in (names[:2], names[2:]):
                    corners[first], corners[second] = corners[second], corners[first]
                self.assertTrue(self.check().errors)
                self.report = original

    def test_collapsed_quad_is_rejected(self):
        corners = self.report["corners"]
        corners["TopRight"] = dict(corners["TopLeft"])
        self.assertTrue(self.check().errors)


class DecoderDimensionsTests(unittest.TestCase):
    def setUp(self):
        self.decoder = {
            "heightmap_mode": "render_mesh_hm2_overlay",
            "hm2": {"width": 2048, "height": 2048},
            "base_decoder": "render_mesh",
            "base_decoder_meta": {"shape": [4096, 4096], "resolution": [4096, 4096]},
        }
        self.meta = {"coverage": {"hm2_overlay_pixel_count": 65536}}

    def check(self):
        log = CheckLog()
        _verify_decoder_dimensions(self.decoder, self.meta, 4096, 4096, log)
        return log

    def test_partial_hm2_patch_dimensions_are_accepted(self):
        log = self.check()
        self.assertEqual(log.errors, [])
        self.assertNotIn("native_hm2_decoder", [item["name"] for item in log.items])

    def test_native_hm2_dimensions_must_match_output(self):
        self.decoder["heightmap_mode"] = "hm2_native"
        self.assertIn("2048x2048 differ from output 4096x4096", self.check().errors[0])

    def test_overlay_base_dimensions_must_match_output(self):
        for key in ("shape", "resolution"):
            with self.subTest(key=key):
                original = copy.deepcopy(self.decoder)
                self.decoder["base_decoder_meta"][key] = [2048, 2048]
                self.assertTrue(self.check().errors)
                self.decoder = original

    def test_overlay_base_dimensions_must_be_present(self):
        self.decoder["base_decoder_meta"] = {}
        self.assertTrue(self.check().errors)

    def test_overlay_must_contribute_bounded_pixels(self):
        for pixels in (None, 0, -1, 4096 * 4096 + 1):
            with self.subTest(pixels=pixels):
                self.meta["coverage"]["hm2_overlay_pixel_count"] = pixels
                self.assertTrue(self.check().errors)


if __name__ == "__main__":
    unittest.main()

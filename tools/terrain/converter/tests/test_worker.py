"""A scene collision tag must not displace the actual terrain surface."""
import unittest
from pathlib import Path
from types import SimpleNamespace
import numpy as np
from worker import decode_base_map


def surface(path, decompress, bounds, resolution, water_level):
    a = np.ones((resolution, resolution), dtype=np.float32)
    return a, {'mask': np.ones_like(a, dtype=bool), 'water_mask': np.zeros_like(a, dtype=bool)}


class RoutingTests(unittest.TestCase):
    def run_route(self, lmap_decoder, render_decoder=None, frt_decoder=None):
        return decode_base_map(Path('unused.bin'),
            {'has_hm2': False, 'has_lmap': True, 'has_frt': True},
            (0, 2, 0, 2), 2, 0, False, np, SimpleNamespace(decompress=None),
            SimpleNamespace(decode_lmap=lmap_decoder),
            SimpleNamespace(decode_frt=frt_decoder),
            SimpleNamespace(decode_render_mesh=render_decoder), {})

    def test_scene_collision_tag_keeps_terrain_priority(self):
        def wrong_surface(*args, **kwargs):
            self.fail('FRT scene surface was selected despite available terrain')
        self.assertEqual(self.run_route(surface, frt_decoder=wrong_surface)[0], 'lmap')

    def test_no_tracer_uses_terrain_render_mesh(self):
        def absent(*args, **kwargs):
            raise ValueError('no LandRayTracer')
        self.assertEqual(self.run_route(absent, surface)[0], 'render_mesh')

    def test_corrupt_terrain_is_not_silently_accepted(self):
        def broken(*args, **kwargs):
            raise ValueError('corrupt count')
        with self.assertRaisesRegex(ValueError, 'corrupt count'):
            self.run_route(broken, surface)


if __name__ == '__main__':
    unittest.main()

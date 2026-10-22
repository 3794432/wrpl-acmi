import hashlib
import json
from pathlib import Path
import struct
import tempfile
import unittest

from assemble_catalog import TerrainSampler, transform


class TerrainSamplingTests(unittest.TestCase):
    def test_north_to_south_signed_grid_and_bilinear_interpolation(self):
        with tempfile.TemporaryDirectory() as temporary:
            root=Path(temporary)
            folder=root/'maps/test_map'
            folder.mkdir(parents=True)
            # North row at z=10, south row at z=0. Include negative elevation.
            raw=struct.pack('<hhhh', -20, 0, 40, 60)
            (folder/'map.data').write_bytes(raw)
            output={'id':'test_map','width':2,'height':2,'data_file':'map.data',
                    'output_type':'int16_le','output_row_order':'north_to_south',
                    'sample_bounds_m':[0,0,10,10], 'data_sha256':hashlib.sha256(raw).hexdigest()}
            (folder/'metadata.json').write_text(json.dumps({'id':'test_map','output':output}),encoding='utf-8')
            sampler=TerrainSampler(root,'test_map')
            self.assertEqual(sampler.sample(0,10),-20)
            self.assertEqual(sampler.sample(10,0),60)
            self.assertEqual(sampler.sample(5,5),20)
            with self.assertRaises(ValueError): sampler.sample(-1,0)
            (folder/'map.data').write_bytes(struct.pack('<hhhh',0,0,0,0))
            with self.assertRaisesRegex(ValueError,'hash mismatch'): TerrainSampler(root,'test_map')

    def test_rotated_placement_uses_all_affine_basis_vectors(self):
        matrix=[[0,0,-2],[0,1,0],[2,0,0],[100,30,200]]
        self.assertEqual(transform(matrix,[10,5,20]),[140,35,180])


if __name__=='__main__':
    unittest.main()

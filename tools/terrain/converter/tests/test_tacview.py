import tempfile
import unittest
import xml.etree.ElementTree as ET
from pathlib import Path
import numpy as np
from tacview import write_data, write_xml, location


class TestTacview(unittest.TestCase):
    def test_endianness_signed_and_north_first(self):
        with tempfile.TemporaryDirectory() as d:
            p = Path(d)
            # Input rows are Z increasing: SW=1, SE=258, NW=-3, NE=4000.
            meta = write_data(p, 'test', np.array([[1, 258], [-3, 4000]], dtype=np.float32),
                              {'sample_bounds_m': [0, 0, 32, 32]})
            self.assertEqual((p / 'test.data').read_bytes(), b'\xfd\xff\xa0\x0f\x01\x00\x02\x01')
            xml = ET.parse(write_xml(p, meta)).getroot().find('CustomHeightmapList/CustomHeightmap')
            self.assertEqual(xml.findtext('Signed'), 'true')
            self.assertEqual(xml.findtext('BigEndian'), '0')
            self.assertEqual(float(xml.findtext('BottomLeft/Latitude')), 0)
            self.assertGreater(float(xml.findtext('TopLeft/Latitude')), 0)

    def test_reference_matches_exporter(self):
        lon, lat = location(500, -1000, 34, 69, 500, -1000)
        self.assertEqual((lon, lat), (69, 34))

    def test_invalid_height_is_error(self):
        with tempfile.TemporaryDirectory() as d:
            for value in (float('nan'), float('inf'), 40000):
                with self.assertRaises(ValueError):
                    write_data(Path(d), 'bad', np.full((2, 2), value), {})


if __name__ == '__main__':
    unittest.main()

import lzma
import unittest
import zstandard
from codec import decompress, MAX_BYTES


class CodecTests(unittest.TestCase):
    def test_huge_count_is_rejected_before_native_allocation(self):
        for count in (29_601_127_424, 3_474_186_254_336, -1):
            with self.assertRaises(ValueError):
                decompress(b'x', count, 2)

    def test_zstd_declared_size_and_unknown_size(self):
        raw = b'height data' * 1000
        compressed = zstandard.ZstdCompressor(write_content_size=False).compress(raw)
        self.assertEqual(decompress(compressed, len(raw), 1), raw)
        self.assertEqual(decompress(compressed, 0, 1), raw)
        with self.assertRaises(ValueError):
            decompress(compressed, len(raw) - 1, 1)
        with self.assertRaises(ValueError):
            decompress(compressed, len(raw) + 1, 1)

    def test_lzma_declared_size(self):
        raw = b'terrain' * 100
        compressed = lzma.compress(raw, format=lzma.FORMAT_ALONE)
        self.assertEqual(decompress(compressed, len(raw), 0), raw)
        with self.assertRaises(ValueError):
            decompress(compressed, len(raw) - 1, 0)


if __name__ == '__main__':
    unittest.main()

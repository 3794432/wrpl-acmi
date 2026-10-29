import unittest
from build_catalog import bomb_name

class BombEvidenceTests(unittest.TestCase):
    def test_filename_and_translation_do_not_establish_bomb_identity(self):
        for data in [{}, {'bomb': {}}, {'bombGun': True}, {'bombGun': True, 'bomb': []}]:
            self.assertIsNone(bomb_name('gamedata/weapons/bombguns/test.blkx', data, {'weapons/test/short': 'Test'}))

    def test_explicit_bombgun_uses_exact_localization(self):
        self.assertEqual(bomb_name('test.blkx', {'bombGun': True, 'bomb': {}}, {'weapons/test/short': 'Test', 'weapons/test': 'Full Test'}), ('test', 'Test', 'weapons/test/short'))
        self.assertIsNone(bomb_name('test.blkx', {'bombGun': True, 'bomb': {}}, {'weapons/another/short': 'Another'}))

if __name__ == '__main__':
    unittest.main()

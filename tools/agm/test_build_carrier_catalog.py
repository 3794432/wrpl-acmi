import unittest
from build_carrier_catalog import carrier_row, family_name, container_weapons

class CarrierTests(unittest.TestCase):
    def test_nested_containers_retain_all_leaves_and_reject_cycles(self):
        prefix = 'aces.vromfs.bin_u/gamedata/weapons/'
        rack, child = prefix+'containers/rack.blkx', prefix+'containers/child.blkx'
        first, second = prefix+'rocketguns/first.blkx', prefix+'rocketguns/second.blkx'
        docs = {rack: {'container': True, 'weapons': [{'blk': first}, {'blk': child}]},
                child: {'container': True, 'blk': second}}
        entries = {rack: 'a'*40, child: 'b'*40}
        result = container_weapons(rack, docs.__getitem__, entries, {first: {}, second: {}})
        self.assertEqual(result, ([first, second], [], entries))
        row = carrier_row('air', {'WeaponSlots': {'WeaponSlot': {'Weapon': {'trigger': 'aam', 'blk': rack}}}},
                          {first: {}, second: {}}, lambda ref: result)
        self.assertTrue(row['complete']['AAM'])
        self.assertEqual(row['weapons'], [first, second])
        docs[child]['blk'] = rack
        self.assertIn(rack, container_weapons(rack, docs.__getitem__, entries, {first: {}})[1])
        del entries[child]
        self.assertIn(child, container_weapons(rack, docs.__getitem__, entries, {first: {}})[1])

    def test_family_boundaries(self):
        for model, family in [('AIM-120D', 'AIM-120'), ('AIM-9M', 'AIM-9'), ('R-73E', 'R-73'), ('R-77-1', 'R-77'), ('AAM-4', 'AAM-4'), ('AGM-114K', 'AGM-114'), ('PL-12', 'PL-12')]:
            self.assertEqual(family_name(model), family)
        self.assertNotEqual(family_name('AIM-90'), family_name('AIM-9'))

    def test_unknown_agm_does_not_certify_agm_or_block_aam(self):
        known = 'aces.vromfs.bin_u/gamedata/weapons/rocketguns/test.blkx'
        data = {'WeaponSlots': {'WeaponSlot': [{'Weapon': {'trigger': 'aam', 'blk': 'gameData/Weapons/rocketGuns/test.blk'}}, {'Weapon': {'trigger': 'agm', 'blk': 'unknown.blk'}}]}}
        result = carrier_row('air.blkx', data, {known: {}})
        self.assertEqual(result['weapons'], [known])
        self.assertEqual(result['complete'], {'AAM': True, 'AGM': False})
        self.assertIsNone(carrier_row('old.blkx', {'weapon_presets': data['WeaponSlots']}, {known: {}}))

    def test_atgm_stores_are_included_in_air_to_ground_candidates(self):
        known = 'aces.vromfs.bin_u/gamedata/weapons/rocketguns/test.blkx'
        data = {'WeaponSlots': {'WeaponSlot': {'Weapon': {'trigger': 'atgm', 'blk': known}}}}
        result = carrier_row('heli.blkx', data, {known: {'role': 'AGM'}})
        self.assertEqual(result['weapons'], [known])
        self.assertTrue(result['complete']['AGM'])

if __name__ == '__main__':
    unittest.main()

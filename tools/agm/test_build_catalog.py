import unittest
from build_catalog import weapon_rows, aam_radar_source


class WeaponEvidenceTests(unittest.TestCase):
    def test_radar_active_default_and_mixed_layouts(self):
        self.assertIn('SARH', aam_radar_source({'radarSeeker': {}}))
        self.assertIn('SARH', aam_radar_source({'radarSeeker': {'active': False}}))
        self.assertEqual(aam_radar_source({'radarSeeker': {'active': True}}), ', radarSeeker.active=true')
        self.assertEqual(aam_radar_source({}), '')
        self.assertEqual(aam_radar_source({'radarSeeker': {'active': None}}), '')
        self.assertEqual(aam_radar_source([{'radarSeeker': {}}, {'radarSeeker': {'active': True}}]), '')

    def test_single_bullet_can_use_exact_launcher_localization(self):
        data = {'rocket': {'bulletName': 'legacy_bullet', 'bulletType': 'aam', 'guidanceType': 'radar'}}
        names = {'weapons/current_launcher/short': 'Current model'}
        self.assertEqual(weapon_rows('current_launcher.blkx', data, names, 'AAM'),
                         [('legacy_bullet', 'Current model', 'radar', 'weapons/current_launcher/short')])
        data = {'bullet': [data, {'rocket': {'bulletName': 'other', 'bulletType': 'aam', 'guidanceType': 'radar'}}]}
        self.assertTrue(all(not row[1] for row in weapon_rows('current_launcher.blkx', data, names, 'AAM')))

    def test_aam_role_uses_explicit_type_and_localized_bullet_identity(self):
        data = {'rocket': {'bulletName': 'future_aam', 'bulletType': 'aam', 'guidanceType': 'radar'}}
        names = {'weapons/future_aam/short': 'Future AAM'}
        self.assertEqual(weapon_rows('any_country.blkx', data, names, 'AAM'),
                         [('future_aam', 'Future AAM', 'radar', 'weapons/future_aam/short')])
        self.assertEqual(weapon_rows('any_country.blkx', data, names, 'AGM'), [])
        data['rocket']['guidance'] = {'radarSeeker': {'groundVehiclesAsTarget': True, 'aircraftAsTarget': False}}
        self.assertEqual(weapon_rows('any_country.blkx', data, names, 'AAM'), [])

    def test_aam_typed_anti_radiation_requires_explicit_ground_only_seeker(self):
        rocket = {'bulletName': 'arm', 'bulletType': 'aam', 'guidanceType': 'radar'}
        names = {'weapons/arm/short': 'ARM'}
        self.assertEqual(weapon_rows('arm.blkx', {'rocket': rocket}, names), [])
        rocket['guidance'] = {'radarSeeker': {'groundVehiclesAsTarget': True, 'aircraftAsTarget': False}}
        self.assertEqual(weapon_rows('arm.blkx', {'rocket': rocket}, names), [('arm', 'ARM', 'radar', 'weapons/arm/short')])

    def test_unguided_is_not_named_and_mixed_bullets_keep_missing_names(self):
        data = {'bullet': [
            {'rocket': {'bulletName': 'dumb', 'bulletType': 'rocket_tank'}},
            {'rocket': {'bulletName': 'known', 'guidanceType': 'optical'}},
            {'rocket': {'bulletName': 'untranslated', 'guidanceType': 'optical'}},
        ]}
        self.assertEqual(weapon_rows('test.blkx', data, {'known': 'Known'}),
                         [('known', 'Known', 'optical', 'known'), ('untranslated', '', 'optical', '')])

    def test_sam_and_generic_aam_excluded_from_agm_catalogue(self):
        for kind in ['sam_tank', 'aam']:
            self.assertEqual(weapon_rows('test.blkx', {'rocket': {
                'bulletName': 'wrong', 'bulletType': kind, 'guidanceType': 'radar'}}, {'wrong': 'Wrong'}), [])

    def test_manual_guidance_needs_positive_control_field(self):
        rocket = {'bulletName': 'manual', 'controlSensitivity': 0}
        self.assertEqual(weapon_rows('test.blkx', {'rocket': rocket}, {'manual': 'Manual'}), [])
        rocket['controlSensitivity'] = 0.2
        self.assertEqual(weapon_rows('test.blkx', {'rocket': rocket}, {'manual': 'Manual'}), [('manual', 'Manual', 'manual', 'manual')])

if __name__ == '__main__':
    unittest.main()

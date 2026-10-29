"""Offline boundary tests: python -B -m unittest discover -s tools/sam."""

from copy import deepcopy
import unittest

from build_catalog import SOURCE_COMMIT, apply_ammunition_evidence, build_catalog, unique_object
from build_missile_names import inspect_weapon


def weapon(name="asset_ammo", **changes):
    result = {
        "kind": "missile",
        "weapon_path": "aces.vromfs.bin_u/gamedata/weapons/groundmodels_weapons/misleading_filename.blkx",
        "dummy_mount": False,
        "non_threat_projectile": False,
        "unguided_rocket": False,
        "missile": {"name": name, "bullet_type": "aam", "guidance_type": "radar"},
    }
    result.update(changes)
    return result


def catalog(weapons, classification="spaam"):
    return {"schema": 1, "source": {"commit": SOURCE_COMMIT}, "units": {
        "Exact_Case_Key": {"classification": classification, "weapons": weapons}
    }}


class BuildCatalogTests(unittest.TestCase):
    def test_weapon_audit_keeps_non_default_missile_sets(self):
        doc = {"bullet": {"bulletName": "base", "bulletType": "sam_tank", "rocket": {"guidanceType": "saclos"}},
               "upgrade_aam": {"bullet": {"bulletName": "improved", "bulletType": "sam_tank", "rocket": {"guidanceType": "saclos"}}}}
        rounds, unknown = inspect_weapon("asset.blkx", doc, True)
        self.assertFalse(unknown)
        self.assertEqual({r["bullet_name"] for r in rounds}, {"base", "improved"})
        self.assertEqual({r["bullet_set"] for r in rounds}, {None, "upgrade_aam"})

    def test_variant_audit_prevents_default_round_uniqueness(self):
        base, _ = build_catalog(catalog([weapon("base")]))
        path = base["units"]["Exact_Case_Key"]["missiles"][0]["weapon_path"]
        evidence = {"schema": 1, "source_commit": SOURCE_COMMIT, "units": {
            "Exact_Case_Key": {"candidate_complete": True, "incomplete_reasons": [], "candidates": [
                {"bullet_name": name, "weapon_path": path, "guidance": "radar", "name_source": path + "#/" + name}
                for name in ("base", "improved")
            ]}}}
        result = apply_ammunition_evidence(base, evidence)["units"]["Exact_Case_Key"]
        self.assertTrue(result["candidates_complete"])
        self.assertEqual({m["bullet_name"] for m in result["missiles"]}, {"base", "improved"})

    def test_incomplete_and_missing_audits_never_establish_uniqueness(self):
        for evidence_unit in (None, {"candidate_complete": True, "candidates": []},
                              {"candidate_complete": False, "candidates": []}):
            base, _ = build_catalog(catalog([weapon("base")]))
            evidence = {"schema": 1, "source_commit": SOURCE_COMMIT,
                        "units": {} if evidence_unit is None else {"Exact_Case_Key": evidence_unit}}
            result = apply_ammunition_evidence(base, evidence)["units"]["Exact_Case_Key"]
            self.assertFalse(result["candidates_complete"])
            self.assertEqual(result["missiles"][0]["bullet_name"], "base")

    def test_all_candidates_deduplicated_sorted_without_mutating_input(self):
        small = weapon("a", selected_range_m=1, envelope_eligible=False, trigger="unknown")
        large = weapon("z", selected_range_m=999999, trigger="gunner0")
        other_path = weapon("a", weapon_path="aces.vromfs.bin_u/gamedata/weapons/other.blkx")
        source = catalog([large, small, deepcopy(small), other_path])
        before = deepcopy(source)
        result, rejected = build_catalog(source)
        candidates = result["units"]["Exact_Case_Key"]["missiles"]
        self.assertEqual([m["bullet_name"] for m in candidates], ["a", "a", "z"])
        self.assertLess(candidates[0]["weapon_path"], candidates[1]["weapon_path"])
        self.assertEqual(result["source_commit"], SOURCE_COMMIT)
        self.assertEqual(set(result), {"schema", "source_commit", "units"})
        self.assertEqual(set(candidates[0]), {"bullet_name", "weapon_path", "guidance"})
        self.assertFalse(rejected)
        self.assertEqual(source, before)

    def test_exclude_guns_dummy_unguided_decoys_and_missing_guidance(self):
        invalid = [weapon(kind=kind) for kind in ("gun", "unknown", "excluded_dummy", "excluded_non_threat")]
        invalid += [weapon(**{flag: True}) for flag in (
            "dummy_mount", "unguided_rocket", "non_threat_projectile"
        )]
        invalid += [weapon(missile={"name": "x", "bullet_type": "sam_tank", "guidance_type": g})
                    for g in (None, "", "unguided", "unknown")]
        result, rejected = build_catalog(catalog(invalid))
        self.assertEqual(result["units"], {})
        self.assertEqual(sum(rejected.values()), 7)

    def test_anti_air_requires_projectile_type_or_explicit_boolean_mount(self):
        for bullet_type in ("aam", "sam_tank", "atgm_tank"):
            for mount in (None, False, "true", True):
                with self.subTest(bullet_type=bullet_type, mount=mount):
                    w = weapon(missile={"name": "x", "bullet_type": bullet_type, "guidance_type": "saclos"},
                               bullet_type="sam_tank", mount_target_msl=mount)
                    result, _ = build_catalog(catalog([w]))
                    self.assertEqual(bool(result["units"]), bullet_type != "atgm_tank" or mount is True)

    def test_unit_classification_bounds_and_mixed_gun_sam(self):
        for classification in ("spaa", "spaam", "air_defence", "radar_support", "mlrs", "tank", None):
            with self.subTest(classification=classification):
                result, _ = build_catalog(catalog([weapon(), weapon(kind="gun")], classification))
                self.assertEqual(bool(result["units"]), classification in {"spaa", "spaam", "air_defence"})
        result, _ = build_catalog(catalog([]))
        self.assertFalse(result["units"])

    def test_exact_asset_name_and_guidance_not_filename_inferences(self):
        for guidance in ("optical", "radar", "saclos"):
            w = weapon("Exact_Asset_Name", missile={"name": "Exact_Asset_Name", "bullet_type": "aam",
                                                   "guidance_type": guidance}, bulletName="other")
            result, _ = build_catalog(catalog([w]))
            m = result["units"]["Exact_Case_Key"]["missiles"][0]
            self.assertEqual(m["bullet_name"], "Exact_Asset_Name")
            self.assertEqual(m["guidance"], guidance)

    def test_explicit_bullet_name_fallback_and_no_filename_fallback(self):
        w = weapon(None, projectile_markers=[{"field_path": "/bullet/bulletName", "value": "From_Asset"}])
        result, _ = build_catalog(catalog([w]))
        self.assertEqual(result["units"]["Exact_Case_Key"]["missiles"][0]["bullet_name"], "From_Asset")
        result, rejected = build_catalog(catalog([weapon(None)]))
        self.assertFalse(result["units"])
        self.assertEqual(sum(rejected.values()), 1)

    def test_invalid_asset_paths_rejected(self):
        for path in (None, "", "guess.blkx", "aces.vromfs.bin_u/gamedata/weapons/../guess.blkx"):
            result, rejected = build_catalog(catalog([weapon(weapon_path=path)]))
            self.assertFalse(result["units"])
            self.assertEqual(sum(rejected.values()), 1)

    def test_wrong_schema_commit_or_structure_fails(self):
        valid = catalog([weapon()])
        for update in ({"schema": 2}, {"schema": True}, {"source": {"commit": "other"}},
                       {"units": []}, {"units": {"x": {"weapons": None}}}):
            with self.subTest(update=update), self.assertRaises(ValueError):
                build_catalog(dict(valid, **update))
        with self.assertRaises(ValueError):
            unique_object([("units", {}), ("units", {})])


if __name__ == "__main__":
    unittest.main()

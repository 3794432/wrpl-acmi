#!/usr/bin/env python3
"""Project an offline schema-1 air-defence catalogue into SAM ammo candidates.

Only the pinned catalogue's unit classification, direct weapons and projectile
evidence are used. No assets are fetched. Names and guidance retain the asset's
spelling (in particular, optical is not relabelled as infrared). All distinct
(bullet_name, weapon_path, guidance) candidates survive, irrespective of ranges,
envelopes, triggers, storage selection or replay IDs. This is not a loadout map.

Usage: python -B tools/sam/build_catalog.py --catalog INPUT --out OUTPUT
"""

import argparse
from collections import Counter
import hashlib
import json
from pathlib import Path, PurePosixPath
import sys


SOURCE_COMMIT = "f7952e4291e9fd7b6fe38e84020dddef925c3457"
MAX_CATALOG_BYTES = 16 * 1024 * 1024
UNIT_CLASSES = frozenset({"spaa", "spaam", "air_defence"})
ANTI_AIR_TYPES = frozenset({"aam", "sam_tank"})
# These are the verified guidanceType values in this fixed source snapshot.
# Future/unrecognized values are reported, never inferred from a weapon name.
GUIDANCE_TYPES = frozenset({"radar", "optical", "saclos"})
DEFAULT_OUTPUT = Path(__file__).resolve().parents[2] / "cmd/acmi/data/sam_launchers.json"


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError(f"duplicate JSON object key: {key}")
        result[key] = value
    return result


def read_catalog(path):
    with Path(path).open("rb") as stream:
        raw = stream.read(MAX_CATALOG_BYTES + 1)
    if len(raw) > MAX_CATALOG_BYTES:
        raise ValueError(f"catalogue exceeds {MAX_CATALOG_BYTES} bytes")
    return json.loads(raw, object_pairs_hook=unique_object)


def bullet_names(weapon, missile):
    """Prefer the extractor's exact asset name; fall back to explicit bulletName."""
    name = missile.get("name")
    if isinstance(name, str) and name.strip():
        return {name}
    values = [missile.get("bulletName"), weapon.get("bulletName")]
    for marker in weapon.get("projectile_markers", []):
        if marker.get("field_path") in {"/bullet/bulletName", "/bullet/rocket/bulletName"}:
            values.append(marker.get("value"))
    return {value for value in values if isinstance(value, str) and value.strip()}


def missile_candidates(weapon):
    """Return candidate tuples and, for rejected missile weapons, an audit reason."""
    if weapon.get("kind") != "missile":
        return set(), None
    if any(weapon.get(flag) for flag in (
        "dummy_mount", "unguided_rocket", "non_threat_projectile"
    )):
        return set(), "dummy/unguided/non_threat"
    missile = weapon.get("missile")
    if not isinstance(missile, dict):
        return set(), "missing missile evidence"
    if (missile.get("bullet_type") not in ANTI_AIR_TYPES
            and weapon.get("mount_target_msl") is not True):
        return set(), "no anti-air projectile or explicit mount evidence"
    guidance = missile.get("guidance_type")
    if guidance not in GUIDANCE_TYPES:
        return set(), "missing/unrecognized guided projectile evidence"
    names = bullet_names(weapon, missile)
    if not names:
        return set(), "missing asset missile.name/bulletName"
    path = weapon.get("weapon_path")
    if (not isinstance(path, str)
            or not path.startswith("aces.vromfs.bin_u/gamedata/weapons/")
            or "\\" in path or ".." in PurePosixPath(path).parts
            or not path.endswith(".blkx")):
        return set(), "missing/invalid weapon asset path"
    return {(name, path, guidance) for name in names}, None


def build_catalog(catalog):
    if not isinstance(catalog, dict) or type(catalog.get("schema")) is not int or catalog["schema"] != 1:
        raise ValueError("expected schema-1 catalogue")
    source = catalog.get("source")
    if not isinstance(source, dict) or source.get("commit") != SOURCE_COMMIT:
        raise ValueError(f"catalogue must be pinned to {SOURCE_COMMIT}")
    units = catalog.get("units")
    if not isinstance(units, dict):
        raise ValueError("catalogue units must be an object")
    result = {}
    rejected = Counter()
    for key, unit in sorted(units.items()):
        if not isinstance(unit, dict) or not isinstance(unit.get("weapons"), list):
            raise ValueError(f"{key}: expected unit.weapons list")
        candidates = set()
        for weapon in unit["weapons"]:
            if not isinstance(weapon, dict):
                raise ValueError(f"{key}: weapon must be an object")
            if unit.get("classification") not in UNIT_CLASSES:
                if weapon.get("kind") == "missile":
                    rejected[f"{key}: unit outside air-defence classifications"] += 1
                continue
            entries, reason = missile_candidates(weapon)
            candidates.update(entries)
            if reason:
                rejected[f"{key}: {reason}"] += 1
        if candidates:
            result[key] = {"missiles": [
                {"bullet_name": name, "weapon_path": path, "guidance": guidance}
                for name, path, guidance in sorted(candidates)
            ]}
    return {"schema": 1, "source_commit": SOURCE_COMMIT, "units": result}, rejected


def apply_ammunition_evidence(result, evidence):
    """Merge every audited variant; only an explicitly complete audit is unique."""
    if evidence.get("schema") != 1 or evidence.get("source_commit") != SOURCE_COMMIT:
        raise ValueError("ammunition evidence must use the same schema and pinned revision")
    audited_units = evidence.get("units")
    if not isinstance(audited_units, dict):
        raise ValueError("ammunition evidence requires a unit audit")
    for key, profile in result["units"].items():
        audit = audited_units.get(key)
        profile["candidates_complete"] = False
        if not isinstance(audit, dict):
            continue
        candidates = {(m["bullet_name"], m["weapon_path"], m["guidance"]) for m in profile["missiles"]}
        original_names = {m["bullet_name"] for m in profile["missiles"]}
        audited_names, sources = set(), set()
        valid = True
        for row in audit.get("candidates", []):
            name, path, guidance = row.get("bullet_name"), row.get("weapon_path"), row.get("guidance")
            source = row.get("name_source")
            if (not isinstance(name, str) or not name.strip() or not isinstance(path, str)
                    or not path.startswith("aces.vromfs.bin_u/gamedata/weapons/")
                    or ".." in PurePosixPath(path).parts or "\\" in path
                    or not isinstance(source, str) or not source.startswith(path + "#")):
                raise ValueError(f"{key}: invalid audited ammunition identity")
            if guidance not in GUIDANCE_TYPES:
                valid = False
                guidance = ""
            audited_names.add(name)
            candidates.add((name, path, guidance))
            sources.add(source)
        profile["missiles"] = [{"bullet_name": name, "weapon_path": path, "guidance": guidance}
                               for name, path, guidance in sorted(candidates)]
        profile["candidates_complete"] = (audit.get("candidate_complete") is True
            and not audit.get("incomplete_reasons") and valid and bool(audited_names)
            and original_names <= audited_names)
        profile["candidate_sources"] = sorted(sources)
    return result


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--catalog", required=True, type=Path, help="offline source catalogue")
    parser.add_argument("--out", type=Path, default=DEFAULT_OUTPUT)
    parser.add_argument("--evidence", type=Path, help="full fixed-commit ammunition/variant audit from build_missile_names.py")
    args = parser.parse_args(argv)
    try:
        if args.catalog.resolve() == args.out.resolve():
            raise ValueError("output must not overwrite the input catalogue")
        result, rejected = build_catalog(read_catalog(args.catalog))
        if args.evidence:
            with args.evidence.open("rb") as stream:
                raw = stream.read(64 * 1024 * 1024 + 1)
            if len(raw) > 64 * 1024 * 1024:
                raise ValueError("ammunition evidence exceeds 64 MiB")
            evidence = json.loads(raw, object_pairs_hook=unique_object)
            expected = evidence.get("rebuild", {}).get("catalog_sha256")
            if expected != hashlib.sha256(args.catalog.read_bytes()).hexdigest():
                raise ValueError("ammunition audit does not match the input catalogue hash")
            result = apply_ammunition_evidence(result, evidence)
        args.out.parent.mkdir(parents=True, exist_ok=True)
        args.out.write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    except (OSError, ValueError) as error:
        parser.exit(1, f"error: {error}\n")
    missiles = [m for unit in result["units"].values() for m in unit["missiles"]]
    print(f"units={len(result['units'])} candidates={len(missiles)} "
          f"unique_ammo={len({m['bullet_name'] for m in missiles})}")
    for reason, count in sorted(rejected.items()):
        print(f"excluded missile weapons ({count}): {reason}", file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())

#!/usr/bin/env python3
"""Read-only SAM name/completeness audit; JSON goes to stdout, never to inputs.

Rebuild (run from the repository root, redirect stdout to model-evidence.json):
  python -B tools/sam/build_missile_names.py --cache ../all-air-defence/extractor/cache \
    --catalog ../all-air-defence/extractor/catalog.json --fetch
Offline rebuild uses the same cache plus --evidence ../sam-20261028/model-evidence.json.
Add --names to emit only the schema-1 name map. --fetch reads only the pinned
weapon language CSV, unit-declared presets (including aliases), and otherwise
uncached weapons referenced by those units. All downloaded JSON is embedded in
the evidence, so no other downloaded files are required. Existing cached blobs
and downloaded blobs are checked against the pinned tree's Git blob hashes.

candidate_complete describes a conservative configuration candidate superset,
not an observed loadout: every defined non-default missile bulletSet survives,
even if a particular unit does not expose its modification. Compare distinct
bullet_name values, not the number of source records, for single-candidate use.
"""

import argparse
import base64
from concurrent.futures import ThreadPoolExecutor
import csv
import hashlib
import io
import json
from pathlib import Path, PurePosixPath
import sys
import urllib.request


COMMIT = "f7952e4291e9fd7b6fe38e84020dddef925c3457"
BASE = f"https://raw.githubusercontent.com/gszabi99/War-Thunder-Datamine/{COMMIT}/"
LANG = "lang.vromfs.bin_u/lang/units_weaponry.csv"
ROOT = Path(__file__).resolve().parents[2]
MISSILE_TYPES = {"aam", "sam_tank"}
GUIDANCE = {"optical", "saclos", "radar"}


def pointer(part):
    return str(part).replace("~", "~0").replace("/", "~1")


def walk(value, path=""):
    if isinstance(value, dict):
        yield path, value
        for key, child in value.items():
            yield from walk(child, path + "/" + pointer(key))
    elif isinstance(value, list):
        for index, child in enumerate(value):
            yield from walk(child, path + "/" + str(index))


def blob_sha(raw):
    return hashlib.sha1(b"blob " + str(len(raw)).encode() + b"\0" + raw).hexdigest()


def pinned_bytes(path, expected, maximum=16 * 1024 * 1024):
    """Raw fixed-commit URL, then public Git blob API; verify either transport."""
    try:
        request = urllib.request.Request(BASE + path, headers={"Cache-Control": "no-cache"})
        with urllib.request.urlopen(request, timeout=15) as response:
            raw = response.read(maximum + 1)
        if len(raw) > maximum or blob_sha(raw) != expected:
            raise ValueError("raw asset size/hash mismatch")
        return raw
    except Exception as raw_error:
        url = "https://api.github.com/repos/gszabi99/War-Thunder-Datamine/git/blobs/" + expected
        try:
            request = urllib.request.Request(url, headers={"Accept": "application/vnd.github+json", "User-Agent": "sam-fixed-commit-audit"})
            with urllib.request.urlopen(request, timeout=15) as response:
                payload = json.loads(response.read(2 * maximum))
            if payload.get("sha") != expected or payload.get("encoding") != "base64":
                raise ValueError("unexpected Git blob API response")
            raw = base64.b64decode(payload["content"])
            if len(raw) > maximum or blob_sha(raw) != expected:
                raise ValueError("blob API size/hash mismatch")
            return raw
        except Exception as blob_error:
            raise ValueError(f"raw: {raw_error}; blob API: {blob_error}") from blob_error


def asset_path(value):
    """Normalize an explicit Datamine BLK reference, never a model name."""
    if not isinstance(value, str):
        return None
    value = value.replace("\\", "/").lower()
    if ".." in PurePosixPath(value).parts:
        raise ValueError("unsafe asset reference")
    if value.endswith(".blk"):
        value += "x"
    if value.startswith("gamedata/"):
        value = "aces.vromfs.bin_u/" + value
    if value.startswith("aces.vromfs.bin_u/gamedata/") and value.endswith(".blkx"):
        return value
    return None


class Assets:
    def __init__(self, cache, fetch, previous):
        self.cache, self.fetch = cache, fetch
        tree = json.loads((cache / "tree.json").read_text(encoding="utf-8"))
        if tree["sha"] != COMMIT:
            raise ValueError("cache tree is not the fixed source commit")
        self.tree = {entry["path"]: entry for entry in tree["tree"]}
        self.previous = previous.get("downloaded_json_inputs", {})
        self.docs, self.inputs, self.downloads, self.errors = {}, {}, {}, {}

    def load(self, path):
        if path in self.docs or path in self.errors:
            return
        try:
            if path not in self.tree:
                raise ValueError("path absent from truncated pinned tree")
            local = self.cache / path.replace("/", "__")
            if local.is_file():
                raw, origin = local.read_bytes(), "cache"
            elif path in self.previous:
                raw = self.previous[path]["raw_utf8"].encode("utf-8")
                origin = "pinned_upstream"
            elif self.fetch:
                raw = pinned_bytes(path, self.tree[path]["sha"])
                origin = "pinned_upstream"
            else:
                raise ValueError("missing cache/embedded input; use --fetch")
            actual = blob_sha(raw)
            expected = self.tree.get(path, {}).get("sha")
            # The upstream tree is truncated; never certify an unverified blob.
            if not expected:
                raise ValueError("path absent from truncated pinned tree")
            if actual != expected:
                raise ValueError("Git blob SHA mismatch against pinned tree")
            data = json.loads(raw)
            if not isinstance(data, dict):
                raise ValueError("expected JSON object")
            self.docs[path] = data
            self.inputs[path] = {"git_blob_sha1": actual, "origin": origin}
            if origin == "pinned_upstream":
                self.downloads[path] = {"git_blob_sha1": actual, "raw_utf8": raw.decode("utf-8")}
        except Exception as error:
            self.errors[path] = f"{type(error).__name__}: {error}"

    def batch(self, paths):
        paths = sorted(set(paths) - self.docs.keys() - self.errors.keys())
        if paths:
            print(f"Inspecting {len(paths)} declared assets", file=sys.stderr, flush=True)
        with ThreadPoolExecutor(max_workers=6) as pool:
            list(pool.map(self.load, paths))
        if paths:
            print(f"Verified={len(self.docs)} unresolved={len(self.errors)}", file=sys.stderr, flush=True)


def references(doc):
    for location, obj in walk(doc):
        ref = asset_path(obj.get("blk"))
        if ref and ("/weapons/" in ref or "/weaponpresets/" in ref):
            yield location + "/blk", ref, obj


def inspect_weapon(path, doc, known_sam=False):
    rounds, unknown = [], []
    for location, obj in walk(doc):
        # Inspect every bullet array/member, including named non-default sets.
        is_bullet = location.endswith("/bullet") or "/bullet/" in location and location.rsplit("/", 1)[-1].isdigit()
        if not is_bullet or not any(key in obj for key in ("rocket", "bulletType", "bulletName")):
            continue
        rocket = obj.get("rocket", {})
        rocket = rocket if isinstance(rocket, dict) else {}
        types = set()
        for value in (obj.get("bulletType"), rocket.get("bulletType")):
            types.update(x for x in (value if isinstance(value, list) else [value]) if isinstance(x, str))
        guidance = rocket.get("guidanceType", obj.get("guidanceType"))
        is_missile = bool(types & MISSILE_TYPES) or isinstance(guidance, str) and guidance in GUIDANCE
        if not is_missile:
            continue
        names = [(location + "/bulletName", obj.get("bulletName")),
                 (location + "/rocket/bulletName", rocket.get("bulletName"))]
        names = [(key, value) for key, value in names if isinstance(value, str) and value]
        if not names:
            unknown.append({"source": path + "#" + location, "reason": "guided bullet lacks explicit bulletName"})
        if guidance not in GUIDANCE:
            unknown.append({"source": path + "#" + location, "reason": "unknown/missing guidanceType"})
        selector = location.split("/")[1] if not location.startswith("/bullet") else None
        for name_field, name in names:
            rounds.append({"bullet_name": name, "weapon_path": path,
                           "bullet_set": selector, "bullet_field": location,
                           "name_source": path + "#" + name_field,
                           "bullet_type": obj.get("bulletType"),
                           "rocket_bullet_type": rocket.get("bulletType"),
                           "guidance": guidance,
                           "guidance_source": path + "#" + location + ("/rocket/guidanceType" if "guidanceType" in rocket else "/guidanceType")})
    if known_sam and not rounds:
        unknown.append({"source": path, "reason": "catalogued SAM has no recognized guided bullet"})
    for location, obj in walk(doc):
        for key, value in obj.items():
            if isinstance(value, str) and value.lower().endswith((".blk", ".blkx")):
                unknown.append({"source": path + "#" + location + "/" + pointer(key),
                                "reason": "external weapon branch requires inspection", "value": value})
    return rounds, unknown


def language_rows(assets, names, fetch, previous, local=None):
    expected = assets.tree[LANG]["sha"]
    if local or fetch:
        raw = local.read_bytes() if local else pinned_bytes(LANG, expected)
        if blob_sha(raw) != expected:
            raise ValueError("language CSV differs from pinned blob")
        reader = csv.reader(io.StringIO(raw.decode("utf-8-sig")), delimiter=";")
        header = next(reader)
        english = header.index("<English>")
        result = []
        while True:
            line = reader.line_num + 1
            try:
                row = next(reader)
            except StopIteration:
                break
            if row and row[0] in names and len(row) > english and row[english].strip():
                result.append({"key": row[0], "english": row[english], "csv_line": line})
        return {"path": LANG, "git_blob_sha1": expected, "column": "<English>", "rows": result}
    source = previous.get("language_input", {})
    if source.get("path") != LANG or source.get("git_blob_sha1") != expected:
        raise ValueError("provide --fetch, --lang, or a pinned --evidence language extract")
    return source


def audit(args, previous):
    launchers = json.loads(args.launchers.read_text(encoding="utf-8"))
    catalog = json.loads(args.catalog.read_text(encoding="utf-8"))
    if launchers["source_commit"] != COMMIT or catalog["source"]["commit"] != COMMIT:
        raise ValueError("catalogue/launcher commit mismatch")
    assets = Assets(args.cache, args.fetch, previous)
    unit_paths = {key: catalog["units"][key]["virtual_path"] for key in launchers["units"]}
    assets.batch(unit_paths.values())
    # Follow every declared preset, including presets which alias other presets.
    frontier = set()
    for path in unit_paths.values():
        frontier.update(ref for _, ref, _ in references(assets.docs.get(path, {})) if "/weaponpresets/" in ref)
    preset_paths = set()
    for _ in range(16):
        frontier -= preset_paths
        if not frontier:
            break
        assets.batch(frontier)
        preset_paths.update(frontier)
        frontier = {ref for path in frontier for _, ref, _ in references(assets.docs.get(path, {})) if "/weaponpresets/" in ref}
    else:
        raise ValueError("preset traversal exceeded depth bound")
    # Cached conventional guns are checked too, without reading unrelated assets.
    # New downloads are limited to unit-declared weapons and presets, including
    # changed conventional mounts whose missile content is unknown. Smoke mounts are
    # explicitly excluded by triggerGroup, not inferred from asset filenames.
    needed = set()
    skip = {}
    for key, path in unit_paths.items():
        sam_paths = {m["weapon_path"] for m in launchers["units"][key]["missiles"]}
        for location, ref, mount in references(assets.docs.get(path, {})):
            if "/weapons/" not in ref:
                continue
            if mount.get("triggerGroup") == "smoke":
                skip[(path, location)] = "explicit triggerGroup=smoke"
            else:
                needed.add(ref)
        needed.update(sam_paths)
    for path in preset_paths:
        needed.update(ref for _, ref, mount in references(assets.docs.get(path, {})) if "/weapons/" in ref and mount.get("triggerGroup") != "smoke")
    assets.batch(needed)
    known_sam = {m["weapon_path"] for u in launchers["units"].values() for m in u["missiles"]}
    weapon_audits = {}
    for path in sorted(needed):
        if path in assets.docs:
            rounds, unknown = inspect_weapon(path, assets.docs[path], path in known_sam)
            weapon_audits[path] = {"projectiles": rounds, "unknown_branches": unknown}
    units = {}
    for key, path in sorted(unit_paths.items()):
        doc = assets.docs.get(path, {})
        reasons, refs, projectiles, visited = [], [], [], set()
        modifications = doc.get("modifications", {})
        all_docs = [(path, doc, [])]
        if path in assets.errors:
            reasons.append({"asset": path, "reason": assets.errors[path]})
        while all_docs:
            source, content, chain = all_docs.pop(0)
            if source in visited:
                continue
            visited.add(source)
            for location, ref, mount in references(content):
                item = {"source": source + "#" + location, "asset": ref, "via_presets": chain,
                        "trigger": mount.get("trigger"), "trigger_group": mount.get("triggerGroup")}
                if (source, location) in skip:
                    item["excluded_reason"] = skip[(source, location)]
                    if "unknown" in item["excluded_reason"]:
                        reasons.append({"source": item["source"], "asset": ref, "reason": item["excluded_reason"]})
                    refs.append(item)
                    continue
                refs.append(item)
                if ref in assets.errors:
                    reasons.append({"asset": ref, "source": item["source"], "reason": assets.errors[ref]})
                elif "/weaponpresets/" in ref:
                    if ref in chain:
                        reasons.append({"asset": ref, "reason": "cyclic preset reference"})
                    else:
                        all_docs.append((ref, assets.docs.get(ref, {}), chain + [ref]))
                elif ref in weapon_audits:
                    wa = weapon_audits[ref]
                    reasons.extend(wa["unknown_branches"])
                    for round_ in wa["projectiles"]:
                        entry = dict(round_)
                        entry["mount_source"] = item["source"]
                        entry["via_presets"] = chain
                        selector = entry["bullet_set"]
                        entry["unit_modification_source"] = path + "#/modifications/" + pointer(selector) if selector in modifications else None
                        entry["availability"] = "default" if selector is None else "unit_modification" if selector in modifications else "weapon_defined_conservative_superset"
                        projectiles.append(entry)
                else:
                    reasons.append({"asset": ref, "source": item["source"], "reason": "uninspected weapon reference"})
        # Explicit ammo selectors not represented by any mounted weapon branch
        # are unresolved; do not turn their name into a guessed projectile.
        selectors = {r["bullet_set"] for r in projectiles if r["bullet_set"]}
        unmatched = [name for name in modifications if name.lower().endswith("_aam") and name not in selectors]
        for name in unmatched:
            reasons.append({"source": path + "#/modifications/" + pointer(name), "reason": "ammo modification has no resolved mounted bulletSet"})
        original = launchers["units"][key]["missiles"]
        found_names = {r["bullet_name"] for r in projectiles}
        for entry in original:
            if entry["bullet_name"] not in found_names:
                reasons.append({"reason": "original SAM candidate not recovered", "candidate": entry})
        distinct = sorted(found_names)
        old = {m["bullet_name"] for m in original}
        units[key] = {"unit_path": path, "candidate_complete": not reasons,
                      "single_ammo_candidate": len(distinct) == 1,
                      "safe_single_candidate": not reasons and len(distinct) == 1,
                      "candidate_bullet_names": distinct,
                      "catalogue_bullet_names": sorted(old),
                      "missing_from_catalogue": sorted(found_names - old),
                      "candidates": projectiles, "references": refs,
                      "ammo_modifications": sorted(name for name in modifications if name in selectors or name in unmatched),
                      "incomplete_reasons": reasons}
    all_names = {r["bullet_name"] for u in units.values() for r in u["candidates"]}
    print("Candidate traversal complete", file=sys.stderr, flush=True)
    lang = ({"path": LANG, "git_blob_sha1": assets.tree[LANG]["sha"],
             "column": "<English>", "deferred": True, "rows": []}
            if args.defer_language else language_rows(assets, all_names, args.fetch, previous, args.lang))
    rows = {}
    for row in lang["rows"]:
        if row["key"] in rows and rows[row["key"]]["english"] != row["english"]:
            raise ValueError("conflicting language rows")
        rows[row["key"]] = row
    names = {}
    for name in sorted(all_names):
        if name in rows:
            row = rows[name]
            names[name] = {"model": row["english"], "source": f"{LANG}:{row['csv_line']} key={name!r} column=<English>; git_blob={lang['git_blob_sha1']}; exact bulletName key"}
    evidence = {"schema": 1, "source_commit": COMMIT,
                "scope": "65 input catalogue units; embedded bullet/rocket and all named bulletSets; unit commonWeapons, all modifications/effects, transitive weapon_presets. Conservative union, not selected loadout.",
                "completeness_policy": "candidate_complete=true only if every relevant reference resolves to hash-verified fixed-commit assets and no unknown ammo/guidance branch remains; union every weapon-defined missile set even when not exposed by unit modifications. Missing/unknown branches never imply uniqueness.",
                "rebuild": {"script": "tools/sam/build_missile_names.py", "catalog": str(args.catalog), "cache": str(args.cache), "launchers_sha256": hashlib.sha256(args.launchers.read_bytes()).hexdigest(), "catalog_sha256": hashlib.sha256(args.catalog.read_bytes()).hexdigest()},
                "summary": {"units": len(units), "original_unique_bullet_names": len({m['bullet_name'] for u in launchers['units'].values() for m in u['missiles']}),
                            "audited_unique_bullet_names": len(all_names), "confirmed_names": len(names),
                            "candidate_complete_units": sum(u["candidate_complete"] for u in units.values()),
                            "safe_single_candidate_units": sum(u["safe_single_candidate"] for u in units.values()),
                            "units_with_catalogue_omissions": sum(bool(u["missing_from_catalogue"]) for u in units.values())},
                "units": units, "weapon_audits": weapon_audits,
                "inputs": dict(sorted(assets.inputs.items())), "input_errors": dict(sorted(assets.errors.items())),
                "downloaded_json_inputs": dict(sorted(assets.downloads.items())),
                "language_input": lang,
                "confirmed_names": names, "unknown_names": sorted(all_names - names.keys())}
    return evidence, {"schema": 1, "source_commit": COMMIT, "names": names}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--cache", type=Path, required=True)
    parser.add_argument("--catalog", type=Path, required=True)
    parser.add_argument("--launchers", type=Path, default=ROOT / "cmd/acmi/data/sam_launchers.json")
    parser.add_argument("--evidence", type=Path)
    parser.add_argument("--lang", type=Path, help="optional exact fixed-commit units_weaponry.csv")
    parser.add_argument("--fetch", action="store_true", help="read necessary fixed-commit upstream inputs into memory")
    parser.add_argument("--names", action="store_true")
    parser.add_argument("--defer-language", action="store_true", help="emit candidate evidence immediately without requesting language data")
    args = parser.parse_args()
    if args.names and args.defer_language:
        parser.error("--names requires language evidence")
    previous = json.loads(args.evidence.read_text(encoding="utf-8")) if args.evidence else {}
    evidence, names = audit(args, previous)
    sys.stdout.reconfigure(encoding="utf-8")
    print(json.dumps(names if args.names else evidence, ensure_ascii=False, indent=2))
    print(json.dumps(evidence["summary"]), file=sys.stderr)


if __name__ == "__main__":
    main()

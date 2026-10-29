#!/usr/bin/env python3
"""Build missile families and declared aircraft custom-slot options at the pinned revision.

Run after build_catalog.py --role AAM and --role AGM, using the same verified
tree/cache. No replay-specific IDs, launch-order or player loadout guesses.
Only explicit WeaponSlots/commonWeapons missile references are included;
aircraft without custom slot declarations cannot supply a complete fallback.
"""
import argparse
from concurrent.futures import ThreadPoolExecutor, as_completed
import json
from pathlib import Path, PurePosixPath
import re
import sys
import threading

from build_catalog import COMMIT, blob_sha, download, walk

PREFIX = 'aces.vromfs.bin_u/gamedata/flightmodels/'
ROOT = Path(__file__).resolve().parents[2]

def family_name(model):
    # Numeric model boundaries prevent AIM-9/AIM-90 or R-7/R-73 collisions.
    match = re.match(r'^(AIM-\d+|AGM-\d+|R-\d+|Kh-\d+|PL-\d+|SD-\d+|AAM-\d+|RB \d+|AS\.\d+)(?=$|[^0-9])', model)
    if match:
        return match.group(1)
    return model

def asset_path(value):
    if not isinstance(value, str):
        return ''
    value = value.replace('\\', '/').lower()
    if '..' in PurePosixPath(value).parts:
        return ''
    if value.startswith('gamedata/'):
        value = 'aces.vromfs.bin_u/' + value
    if value.endswith('.blk'):
        value += 'x'
    return value

def carrier_row(path, data, aliases, resolve_container=None):
    slots = data.get('WeaponSlots')
    if not isinstance(slots, dict) or not slots.get('WeaponSlot'):
        return None
    weapons = set()
    container_inputs = {}
    unresolved = {'AAM': set(), 'AGM': set()}
    for container in [slots, data.get('commonWeapons', {})]:
        for obj in walk(container):
            role = obj.get('trigger')
            if role == 'atgm':
                role = 'agm'
            if role not in ('aam', 'agm'):
                continue
            if obj.get('bullets') == 0:
                continue
            ref = asset_path(obj.get('blk'))
            leaves = [ref]
            if '/weapons/containers/' in ref and resolve_container is not None:
                leaves, missing, inputs = resolve_container(ref)
                unresolved[role.upper()].update(missing)
                container_inputs.update(inputs)
            for leaf in leaves:
                if leaf in aliases and aliases[leaf].get('role', '') == ('AGM' if role == 'agm' else ''):
                    weapons.add(leaf)
                else:
                    unresolved[role.upper()].add(leaf or str(obj.get('blk', 'missing missile blk')))
    if not weapons and not any(unresolved.values()):
        return None
    return {'weapons': sorted(weapons), 'complete': {role: not refs for role, refs in unresolved.items()},
            'unresolved': {role: sorted(refs) for role, refs in unresolved.items()}, 'source': path,
            'container_inputs': dict(sorted(container_inputs.items())),
            'scope': 'DeclaredWeaponSlotsAndCommonWeapons'}

def container_weapons(path, read_json, entries, aliases, visiting=()):
    if path in visiting or len(visiting) >= 8:
        return [], [path], {}
    if path not in entries or '/weapons/containers/' not in path:
        return [], [path], {}
    data = read_json(path)
    inputs = {path: entries[path]}
    if data.get('container') is not True:
        return [], [path], inputs
    refs = sorted({asset_path(o.get('blk')) for o in walk(data) if isinstance(o.get('blk'), str)})
    if not refs or len(refs) > 64:
        return [], [path], inputs
    leaves, missing = set(), set()
    for ref in refs:
        if ref in aliases:
            leaves.add(ref)
        elif '/weapons/containers/' in ref:
            nested, unknown, dependencies = container_weapons(ref, read_json, entries, aliases, visiting + (path,))
            leaves.update(nested)
            missing.update(unknown)
            inputs.update(dependencies)
        else:
            missing.add(ref)
    return sorted(leaves), sorted(missing), inputs

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--cache', type=Path, required=True)
    parser.add_argument('--fetch', action='store_true')
    parser.add_argument('--out', type=Path, default=ROOT / 'cmd/acmi/data/missile_carriers.json')
    args = parser.parse_args()
    tree = json.loads((args.cache / 'tree.json').read_bytes())
    if tree['sha'] != COMMIT:
        raise ValueError('wrong pinned tree')
    entries = {e['path']: e['sha'] for e in tree['tree'] if e['type'] == 'blob'}
    aliases = {}
    for role in ['aam', 'agm']:
        aliases.update(json.loads((ROOT / f'cmd/acmi/data/{role}_models.json').read_bytes())['aliases'])
    paths = sorted(p for p in entries if str(PurePosixPath(p).parent) == PREFIX.rstrip('/') and p.endswith('.blkx'))
    locks, docs = {}, {}
    guard = threading.Lock()
    def read_json(path):
        with guard:
            lock = locks.setdefault(path, threading.Lock())
        with lock:
            if path in docs:
                return docs[path]
            data = read_verified_json(path)
            docs[path] = data
            return data
    def read_verified_json(path):
        local = args.cache / path.replace('/', '__')
        if not local.exists():
            if not args.fetch:
                raise ValueError('missing asset: ' + path)
            local.write_bytes(download(path, entries[path]))
        raw = local.read_bytes()
        if blob_sha(raw) != entries[path]:
            raise ValueError('unverified blob: ' + path)
        return json.loads(raw)
    def inspect(path):
        # Retain only shared, small container documents. Aircraft JSON can
        # be large and must be released when its compact row is extracted.
        row = carrier_row(path, read_verified_json(path), aliases,
                          lambda ref: container_weapons(ref, read_json, entries, aliases))
        if row:
            row['blob_sha1'] = entries[path]
        return Path(path).stem, row
    carriers = {}
    with ThreadPoolExecutor(max_workers=12) as pool:
        futures = [pool.submit(inspect, path) for path in paths]
        for i, future in enumerate(as_completed(futures), 1):
            name, row = future.result()
            if row:
                carriers[name] = row
            if i % 100 == 0:
                print(f'Aircraft {i}/{len(paths)}, missile slot catalogues {len(carriers)}', file=sys.stderr, flush=True)
    families = {entry['model']: family_name(entry['model']) for entry in aliases.values()
                if 'untranslated internal identifier' not in entry['source']}
    # Preserve reviewed labels used by the runtime merge.
    for entry in json.loads((ROOT / 'cmd/acmi/data/missile_models.json').read_bytes())['aliases'].values():
        families[entry['model']] = family_name(entry['model'])
    result = {'schema': 1, 'commit': COMMIT, 'families': dict(sorted(families.items())),
              'carriers': dict(sorted(carriers.items()))}
    args.out.parent.mkdir(parents=True, exist_ok=True)
    args.out.write_text(json.dumps(result, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
    print(f'{len(carriers)} carrier profiles, {len(families)} model-to-family rows', file=sys.stderr)

if __name__ == '__main__':
    main()

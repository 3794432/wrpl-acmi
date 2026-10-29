#!/usr/bin/env python3
"""Build bomb aliases from pinned BombGun definitions and English weapon names.

python tools/bombs/build_catalog.py --cache <asset-cache> --fetch \
  --out cmd/acmi/data/bomb_models.json
The cache needs tree.json at COMMIT. Omit --fetch for offline reproduction.
The neighbouring tools/agm generator supplies the shared bounded downloader.
Generated aliases contain names/provenance only, never raw game assets.
"""
import argparse
from concurrent.futures import ThreadPoolExecutor
import csv
import importlib.util
import io
import json
from pathlib import Path
import sys

_spec = importlib.util.spec_from_file_location('weapon_catalog_inputs', Path(__file__).resolve().parents[1] / 'agm/build_catalog.py')
_inputs = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(_inputs)
COMMIT, LANG = _inputs.COMMIT, _inputs.LANG

def bomb_name(path, data, names):
    # BombGun is explicit evidence; a filename or a translated name alone
    # must not turn an arbitrary weapon into a bomb.
    if data.get('bombGun') is not True or not isinstance(data.get('bomb'), dict):
        return None
    internal = Path(path).stem
    for key in ('weapons/' + internal + '/short', 'weapons/' + internal, internal):
        if names.get(key):
            return internal, names[key], key
    return None

def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument('--cache', type=Path, required=True)
    ap.add_argument('--fetch', action='store_true')
    ap.add_argument('--out', type=Path, required=True)
    args = ap.parse_args()
    tree = json.loads((args.cache / 'tree.json').read_text(encoding='utf-8'))
    if tree['sha'] != COMMIT:
        raise ValueError('tree differs from pinned commit')
    entries = {e['path']: e['sha'] for e in tree['tree'] if e['type'] == 'blob'}
    def read(path):
        local = args.cache / path.replace('/', '__')
        if local.exists():
            raw = local.read_bytes()
        elif args.fetch:
            raw = _inputs.download(path, entries[path])
            local.write_bytes(raw)
        else:
            raise ValueError('missing cached asset: ' + path)
        if len(raw) > 16 * 1024 * 1024 or _inputs.blob_sha(raw) != entries[path]:
            raise ValueError('asset size/hash mismatch: ' + path)
        return raw
    reader = csv.reader(io.StringIO(read(LANG).decode('utf-8-sig')), delimiter=';')
    header = next(reader)
    english = header.index('<English>')
    names = {r[0]: r[english].strip() for r in reader if len(r) > english and r[english].strip()}
    paths = sorted(p for p in entries if '/weapons/bombguns/' in p and p.endswith('.blkx'))
    print(f'Inspecting {len(paths)} pinned bomb definitions', file=sys.stderr, flush=True)
    def inspect(path):
        return path, bomb_name(path, json.loads(read(path)), names)
    with ThreadPoolExecutor(max_workers=8) as pool:
        inspected = list(pool.map(inspect, paths))
    aliases = {}
    for path, row in inspected:
        if row is None:
            continue
        internal, model, key = row
        entry = {'model': model, 'internal_name': internal, 'confidence': 'confirmed',
                 'source': f'Datamine {COMMIT} {path} blob={entries[path]} bombGun=true; {LANG} blob={entries[LANG]} key={key}'}
        for alias in (internal, path, path.removeprefix('aces.vromfs.bin_u/').removesuffix('x')):
            alias = alias.lower()
            if alias in aliases and aliases[alias] != entry:
                raise ValueError('conflicting bomb alias: ' + alias)
            aliases[alias] = entry
    out = {'schema': 1, 'generated_from': [f'War-Thunder-Datamine@{COMMIT}',
            'tools/bombs/build_catalog.py; explicit BombGun and English CSV'],
           'aliases': dict(sorted(aliases.items())), 'replays': {}}
    args.out.parent.mkdir(parents=True, exist_ok=True)
    args.out.write_text(json.dumps(out, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
    print(f'{len(aliases)} aliases, {len({v["model"] for v in aliases.values()})} model names', file=sys.stderr)

if __name__ == '__main__':
    main()
